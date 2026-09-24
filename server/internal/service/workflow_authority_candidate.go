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
// to its presentation position, assignee, comments, or status. A changed title
// or description needs a new candidate and evaluation even if PR heads match.
func WorkflowScopeDigest(issue db.Issue, policyVersion string) string {
	encoded, _ := json.Marshal(struct {
		Title         string `json:"title"`
		Description   string `json:"description"`
		PolicyVersion string `json:"policy_version"`
	}{issue.Title, issue.Description.String, policyVersion})
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
			!fullHandoffCommit.MatchString(strings.ToLower(ordered[i].CommitSHA)) || !ordered[i].Draft ||
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
	_, prSet, err := canonicalWorkflowCandidates(candidates)
	if err != nil {
		return pgtype.UUID{}, err
	}
	scopeDigest := WorkflowScopeDigest(issue, policy.Version)
	digest := workflowCandidateDigest(policy.Version, scopeDigest, prSet)
	if issue.WorkflowCandidateID.Valid {
		var existingDigest string
		err = tx.QueryRow(ctx, `SELECT digest FROM issue_workflow_candidate
			WHERE id=$1 AND issue_id=$2 AND workspace_id=$3`, issue.WorkflowCandidateID, issue.ID, issue.WorkspaceID).Scan(&existingDigest)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("current workflow candidate missing: %w", err)
		}
		if existingDigest == digest {
			return issue.WorkflowCandidateID, nil
		}
	}
	var accepted bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_acceptance
		WHERE issue_id=$1 AND state='accepted' AND revoked_at IS NULL)`, issue.ID).Scan(&accepted)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if accepted {
		return pgtype.UUID{}, errors.New("accepted issue requires explicit rejection before a new candidate")
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
