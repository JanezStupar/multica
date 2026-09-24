package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func workflowAuthorityUUID(raw string) (pgtype.UUID, error) {
	id, err := util.ParseUUID(raw)
	if err != nil || !id.Valid {
		return pgtype.UUID{}, fmt.Errorf("%w: invalid UUID", ErrWorkflowAuthorityInput)
	}
	return id, nil
}

// A retry may resume a reviewer session, so force_fresh_session on the current
// row does not prove that the review began independently. Trace the native
// retry lineage to the handoff that created it and validate its original
// context against the completed handoff record. A resumed review/fix handoff
// can never become a final review merely by setting the fallback fresh flag.
func workflowFreshReviewerOrigin(ctx context.Context, tx pgx.Tx, issueID, candidateID, taskID pgtype.UUID) (bool, error) {
	seen := make(map[pgtype.UUID]bool)
	current := taskID
	var reviewerAgent pgtype.UUID
	for depth := 0; ; depth++ {
		if !current.Valid || seen[current] {
			return false, nil
		}
		seen[current] = true
		var agentID, rowIssueID, retryOf, rerunOf, evidenceID pgtype.UUID
		var forceFresh bool
		var taskContext []byte
		var evidenceKind string
		err := tx.QueryRow(ctx, `SELECT agent_id,issue_id,retry_of_task_id,rerun_of_task_id,
			force_fresh_session,context,COALESCE(trigger_evidence_kind,''),trigger_evidence_ref_id
			FROM agent_task_queue WHERE id=$1`, current).Scan(
			&agentID, &rowIssueID, &retryOf, &rerunOf, &forceFresh, &taskContext, &evidenceKind, &evidenceID)
		if err == pgx.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if rowIssueID != issueID || rerunOf.Valid || !forceFresh {
			return false, nil
		}
		if depth == 0 {
			reviewerAgent = agentID
		} else if reviewerAgent != agentID {
			return false, nil
		}
		var contextValue struct {
			WakeupID string `json:"wakeup_id"`
			Handoff  struct {
				WakeupID     string          `json:"wakeup_id"`
				RequestKey   string          `json:"request_key"`
				OutgoingTask string          `json:"outgoing_task_id"`
				ContextMode  string          `json:"context_mode"`
				CandidateID  string          `json:"candidate_id"`
				ResumeTaskID json.RawMessage `json:"resume_task_id"`
			} `json:"workflow_handoff"`
		}
		if json.Unmarshal(taskContext, &contextValue) != nil ||
			contextValue.Handoff.ContextMode != "fresh" || len(contextValue.Handoff.ResumeTaskID) != 0 ||
			contextValue.Handoff.CandidateID != util.UUIDToString(candidateID) ||
			contextValue.WakeupID == "" || contextValue.WakeupID != contextValue.Handoff.WakeupID ||
			evidenceKind != "issue_handoff" || util.UUIDToString(evidenceID) != contextValue.WakeupID {
			return false, nil
		}
		if retryOf.Valid {
			current = retryOf
			continue
		}
		var genuine bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_wakeup
			WHERE id=$1 AND issue_id=$2 AND last_task_id=$3 AND handoff IS NOT NULL
			AND handoff_completed_at IS NOT NULL AND handoff->>'context_mode'='fresh'
			AND request_key::text=$4 AND handoff->>'outgoing_task_id'=$5
			AND (source_task_id IS NULL OR source_task_id::text=$5))`, evidenceID, issueID,
			current, contextValue.Handoff.RequestKey, contextValue.Handoff.OutgoingTask).Scan(&genuine)
		return genuine, err
	}
}

func (s WorkflowAuthorityService) RegisterReview(ctx context.Context, workspaceID, issueID pgtype.UUID, actor WorkflowActor, in WorkflowReviewInput) error {
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return ErrWorkflowAuthorityUnavailable
	}
	candidateID, err := workflowAuthorityUUID(in.CandidateID)
	if err != nil {
		return err
	}
	if in.Verdict != "pass" && in.Verdict != "changes_requested" || len(in.PRReviewURLs) > 32 {
		return fmt.Errorf("%w: verdict and PR review URLs are required", ErrWorkflowAuthorityInput)
	}
	seen := map[string]bool{}
	for _, raw := range in.PRReviewURLs {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
			len(raw) > 2048 || strings.IndexFunc(raw, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || seen[raw] {
			return fmt.Errorf("%w: review URLs must be distinct HTTPS links", ErrWorkflowAuthorityInput)
		}
		seen[raw] = true
	}
	if actor.Type != "agent" || actor.ID == "" || actor.SourceTaskID == "" {
		return ErrWorkflowAuthorityForbidden
	}
	agentID, err := workflowAuthorityUUID(actor.ID)
	if err != nil {
		return ErrWorkflowAuthorityForbidden
	}
	taskID, err := workflowAuthorityUUID(actor.SourceTaskID)
	if err != nil {
		return ErrWorkflowAuthorityForbidden
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	issue, err := lockWorkflowAuthorityIssue(ctx, tx, q, workspaceID, issueID)
	if err != nil {
		return err
	}
	if issue.WorkflowFrozen || issue.Status != "in_review" || issue.AssigneeType.String != "agent" || issue.AssigneeID != agentID || issue.WorkflowCandidateID != candidateID {
		return ErrWorkflowAuthorityConflict
	}
	policy, err := s.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || policy == nil {
		return ErrWorkflowAuthorityUnavailable
	}
	candidate, err := loadCurrentWorkflowCandidate(ctx, tx, issue, policy.Version)
	if err != nil {
		return err
	}
	if len(candidate.PRs) > 0 && len(in.PRReviewURLs) == 0 || len(candidate.PRs) == 0 && len(in.PRReviewURLs) > 0 {
		return fmt.Errorf("%w: PR review links must match candidate PR presence", ErrWorkflowAuthorityInput)
	}
	if candidate.WriterTaskID == taskID {
		return fmt.Errorf("%w: writer cannot review own candidate", ErrWorkflowAuthorityForbidden)
	}
	var status, session string
	var fresh bool
	var taskPolicy pgtype.Text
	var taskProfile pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT status,COALESCE(session_id,''),force_fresh_session,
		workflow_policy_version,workflow_profile_id FROM agent_task_queue
		WHERE id=$1 AND issue_id=$2 AND agent_id=$3`, taskID, issue.ID, agentID).Scan(
		&status, &session, &fresh, &taskPolicy, &taskProfile); err != nil || status != "running" || session == "" || !fresh ||
		!taskPolicy.Valid || taskPolicy.String != policy.Version || !taskProfile.Valid {
		return fmt.Errorf("%w: a running fresh reviewer task is required", ErrWorkflowAuthorityForbidden)
	}
	if genuine, err := workflowFreshReviewerOrigin(ctx, tx, issue.ID, candidate.ID, taskID); err != nil {
		return err
	} else if !genuine {
		return fmt.Errorf("%w: final review requires a fresh handoff origin", ErrWorkflowAuthorityForbidden)
	}
	links, _ := json.Marshal(in.PRReviewURLs)
	var previousVerdict string
	var previousLinks []byte
	err = tx.QueryRow(ctx, `SELECT verdict,pr_review_urls FROM issue_workflow_review
		WHERE issue_id=$1 AND candidate_id=$2 AND reviewer_task_id=$3 ORDER BY submitted_at DESC,id DESC LIMIT 1`, issue.ID, candidateID, taskID).Scan(&previousVerdict, &previousLinks)
	if err == nil {
		if previousVerdict != in.Verdict || string(previousLinks) != string(links) {
			return fmt.Errorf("%w: reviewer task already attested differently", ErrWorkflowAuthorityConflict)
		}
		return tx.Commit(ctx)
	}
	if err != pgx.ErrNoRows {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO issue_workflow_review
		(id,workspace_id,issue_id,candidate_id,reviewer_task_id,verdict,pr_review_urls)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, dbid.NewV7(), workspaceID, issueID, candidateID, taskID, in.Verdict, links)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.PublishWorkflowIssueChange(ctx, issue, actor)
	return nil
}
