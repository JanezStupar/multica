package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// WorkflowScopeDigest binds an evaluation to the issue objective, rather than
// to its presentation position, assignee, comments, or status. A changed title,
// description or acceptance criteria changes the scope evidence even if PR
// heads match.
func WorkflowScopeDigest(issue db.Issue, policyVersion string) string {
	encoded, _ := json.Marshal(struct {
		Title              string          `json:"title"`
		Description        string          `json:"description"`
		AcceptanceCriteria json.RawMessage `json:"acceptance_criteria"`
		PolicyVersion      string          `json:"policy_version"`
	}{issue.Title, issue.Description.String, json.RawMessage(issue.AcceptanceCriteria), policyVersion})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func canonicalWorkflowCandidates(candidates []HandoffCandidate) ([]HandoffCandidate, []byte, error) {
	if len(candidates) > 20 {
		return nil, nil, errors.New("too many workflow PR candidates")
	}
	ordered := append([]HandoffCandidate{}, candidates...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PRURL < ordered[j].PRURL })
	for i := range ordered {
		if ordered[i].RepositoryURL == "" || ordered[i].PRURL == "" || ordered[i].Branch == "" ||
			!fullHandoffCommit.MatchString(strings.ToLower(ordered[i].CommitSHA)) ||
			i > 0 && ordered[i].PRURL == ordered[i-1].PRURL {
			return nil, nil, errors.New("invalid or duplicate workflow PR candidate")
		}
		ordered[i].CommitSHA = strings.ToLower(ordered[i].CommitSHA)
	}
	encoded, err := json.Marshal(ordered)
	return ordered, encoded, err
}

func workflowCandidateDigest(policyVersion, scopeDigest string, prSet []byte) string {
	encoded, _ := json.Marshal(struct {
		PolicyVersion string          `json:"policy_version"`
		ScopeDigest   string          `json:"scope_digest"`
		PRSet         json.RawMessage `json:"pr_set"`
	}{policyVersion, scopeDigest, prSet})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// sameWorkflowCandidateCodeSurface compares the evaluated code identity. PR
// readiness is provider state, and issue text is reconciled by the agent; neither
// changes the code candidate by itself.
func sameWorkflowCandidateCodeSurface(left, right []HandoffCandidate) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].RepositoryURL != right[i].RepositoryURL || left[i].PRURL != right[i].PRURL ||
			left[i].Branch != right[i].Branch || left[i].CommitSHA != right[i].CommitSHA {
			return false
		}
	}
	return true
}

// registerWorkflowCandidateFromHandoff runs in the handoff dispatch
// transaction, with the issue row already locked and the exact source task
// successfully completed. It is called before the existing owner/status
// UpdateIssue. An unchanged full surface retains the original candidate and
// writer identity across reviewer-to-reviewer or reviewer-to-human handoffs.
func (s *IssueWakeupService) registerWorkflowCandidateFromHandoff(
	ctx context.Context, tx pgx.Tx, issue db.Issue, handoff db.IssueWakeup, source db.AgentTaskQueue,
	candidates []HandoffCandidate,
) (pgtype.UUID, error) {
	if !issue.ID.Valid || !issue.WorkspaceID.Valid || issue.WorkflowFrozen ||
		handoff.ID == (pgtype.UUID{}) || handoff.IssueID != issue.ID || handoff.WorkspaceID != issue.WorkspaceID ||
		source.ID != handoff.SourceTaskID || source.IssueID != issue.ID || source.Status != "completed" {
		return pgtype.UUID{}, errors.New("candidate requires a completed, same-issue handoff source")
	}
	policy, err := s.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || policy == nil {
		return pgtype.UUID{}, errors.New("candidate requires a valid pinned issue policy")
	}
	canonicalCandidates, prSet, err := canonicalWorkflowCandidates(candidates)
	if err != nil {
		return pgtype.UUID{}, err
	}
	scopeDigest := WorkflowScopeDigest(issue, policy.Version)
	digest := workflowCandidateDigest(policy.Version, scopeDigest, prSet)
	if issue.WorkflowCandidateID.Valid {
		var existingPolicyVersion string
		var existingPRSet []byte
		err = tx.QueryRow(ctx, `SELECT policy_version,pr_set FROM issue_workflow_candidate
			WHERE id=$1 AND issue_id=$2 AND workspace_id=$3`, issue.WorkflowCandidateID, issue.ID, issue.WorkspaceID).
			Scan(&existingPolicyVersion, &existingPRSet)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("current workflow candidate missing: %w", err)
		}
		var existingCandidates []HandoffCandidate
		if json.Unmarshal(existingPRSet, &existingCandidates) == nil && existingPolicyVersion == policy.Version {
			existingCandidates, _, err = canonicalWorkflowCandidates(existingCandidates)
			if err == nil && sameWorkflowCandidateCodeSurface(existingCandidates, canonicalCandidates) {
				return issue.WorkflowCandidateID, nil
			}
		}
	}
	var accepted bool
	if issue.WorkflowCandidateID.Valid {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_acceptance
			WHERE issue_id=$1 AND candidate_id=$2 AND state='accepted' AND revoked_at IS NULL)`,
			issue.ID, issue.WorkflowCandidateID).Scan(&accepted)
	}
	if err != nil {
		return pgtype.UUID{}, err
	}
	if accepted {
		intent, intentErr := handoffIntent(handoff)
		if intentErr != nil || intent.ContextMode != "fresh" {
			return pgtype.UUID{}, errors.New("replacing an accepted candidate requires a fresh handoff")
		}
		// Keep the accepted record as provenance, but retire any outstanding
		// merge intent for its exact commits before moving the issue to a new
		// candidate. Delivered readiness and observed merges remain immutable
		// provider facts on their original rows.
		if _, err = tx.Exec(ctx, `UPDATE issue_workflow_delivery SET status='cancelled',
			last_error_class='candidate_superseded',updated_at=now()
			WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3
			AND merged_at IS NULL AND status IN ('pending','retry','stale','blocked')`,
			issue.WorkspaceID, issue.ID, issue.WorkflowCandidateID); err != nil {
			return pgtype.UUID{}, err
		}
	}
	id := dbid.NewV7()
	_, err = tx.Exec(ctx, `INSERT INTO issue_workflow_candidate
		(id,workspace_id,issue_id,policy_version,digest,scope_digest,source_handoff_id,source_task_id,writer_task_id,pr_set)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8,$9)`,
		id, issue.WorkspaceID, issue.ID, policy.Version, digest, scopeDigest, handoff.ID, source.ID, prSet)
	if err != nil {
		return pgtype.UUID{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET state='blocked'
		WHERE issue_id=$1 AND state='requested' AND revoked_at IS NULL`, issue.ID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE issue_workflow_exception SET revoked_at=now()
		WHERE issue_id=$1 AND candidate_id IS DISTINCT FROM $2 AND revoked_at IS NULL`, issue.ID, id)
	if err != nil {
		return pgtype.UUID{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE issue SET workflow_candidate_id=$1 WHERE id=$2 AND workspace_id=$3`, id, issue.ID, issue.WorkspaceID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}
