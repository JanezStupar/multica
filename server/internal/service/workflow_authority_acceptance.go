package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workflowCandidateRecord struct {
	ID            pgtype.UUID
	PolicyVersion string
	ScopeDigest   string
	WriterTaskID  pgtype.UUID
	PRs           []HandoffCandidate
}

type workflowDeliveryBinding struct {
	PR                    HandoffCandidate
	ProviderRepositoryURL string
	Provider              string
	BindingID             pgtype.UUID
	Owner                 string
	Repo                  string
	Number                int64
}

type WorkflowReviewPR struct {
	Provider        string
	BindingID       pgtype.UUID
	RepositoryURL   string
	PRURL           string
	Owner           string
	Repo            string
	Number          int64
	ExpectedHeadSHA string
}

type WorkflowReviewEvidenceInput struct {
	WorkspaceID pgtype.UUID
	PRs         []WorkflowReviewPR
	ReviewURLs  []string
}

func lockWorkflowAuthorityIssue(ctx context.Context, tx pgx.Tx, q *db.Queries, workspaceID, issueID pgtype.UUID) (db.Issue, error) {
	var locked pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM issue WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, issueID, workspaceID).Scan(&locked); err != nil {
		return db.Issue{}, err
	}
	return q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issueID, WorkspaceID: workspaceID})
}

func loadCurrentWorkflowCandidate(ctx context.Context, tx pgx.Tx, issue db.Issue, policyVersion string) (workflowCandidateRecord, error) {
	var candidate workflowCandidateRecord
	if !issue.WorkflowCandidateID.Valid {
		return candidate, fmt.Errorf("%w: no current candidate", ErrWorkflowAuthorityConflict)
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT id,policy_version,scope_digest,writer_task_id,pr_set
		FROM issue_workflow_candidate WHERE id=$1 AND issue_id=$2 AND workspace_id=$3`,
		issue.WorkflowCandidateID, issue.ID, issue.WorkspaceID).Scan(
		&candidate.ID, &candidate.PolicyVersion, &candidate.ScopeDigest, &candidate.WriterTaskID, &raw)
	if err != nil {
		return candidate, fmt.Errorf("%w: current candidate unavailable: %v", ErrWorkflowAuthorityConflict, err)
	}
	if candidate.PolicyVersion != policyVersion ||
		json.Unmarshal(raw, &candidate.PRs) != nil {
		return workflowCandidateRecord{}, fmt.Errorf("%w: candidate scope or policy changed", ErrWorkflowAuthorityConflict)
	}
	return candidate, nil
}

func workflowReviewSatisfied(ctx context.Context, tx pgx.Tx, issue db.Issue, candidate workflowCandidateRecord, required bool) (string, error) {
	return workflowReviewSatisfiedForRequest(ctx, tx, issue, candidate, required, pgtype.UUID{})
}

// A running final reviewer may request autonomous acceptance for its own task.
// The request has no completion or delivery authority until the finalizer
// rechecks this review after that exact task succeeds.
func workflowReviewSatisfiedForRequest(ctx context.Context, tx pgx.Tx, issue db.Issue, candidate workflowCandidateRecord,
	required bool, pendingReviewerTaskID pgtype.UUID,
) (string, error) {
	if !required {
		return "", nil
	}
	var reviewID, reviewerTaskID pgtype.UUID
	var verdict string
	err := tx.QueryRow(ctx, `SELECT id,reviewer_task_id,verdict FROM issue_workflow_review
		WHERE issue_id=$1 AND workspace_id=$2 AND candidate_id=$3
		ORDER BY submitted_at DESC,id DESC LIMIT 1`, issue.ID, issue.WorkspaceID, candidate.ID).Scan(&reviewID, &reviewerTaskID, &verdict)
	if err != nil || verdict != "pass" || reviewerTaskID == candidate.WriterTaskID {
		return "", fmt.Errorf("%w: independent passing review is required", ErrWorkflowAuthorityConflict)
	}
	var reviewerStatus, reviewerSession, writerStatus, writerSession string
	var reviewerFresh bool
	err = tx.QueryRow(ctx, `SELECT status,COALESCE(session_id,''),force_fresh_session
		FROM agent_task_queue WHERE id=$1 AND issue_id=$2`, reviewerTaskID, issue.ID).Scan(
		&reviewerStatus, &reviewerSession, &reviewerFresh)
	if err != nil {
		return "", fmt.Errorf("%w: reviewer task unavailable", ErrWorkflowAuthorityConflict)
	}
	err = tx.QueryRow(ctx, `SELECT status,COALESCE(session_id,'') FROM agent_task_queue
		WHERE id=$1 AND issue_id=$2`, candidate.WriterTaskID, issue.ID).Scan(&writerStatus, &writerSession)
	reviewerFinished := reviewerStatus == "completed"
	reviewerOwnsPendingRequest := pendingReviewerTaskID.Valid && reviewerTaskID == pendingReviewerTaskID && reviewerStatus == "running"
	if err != nil || !(reviewerFinished || reviewerOwnsPendingRequest) || writerStatus != "completed" || !reviewerFresh ||
		reviewerSession == "" || writerSession == "" || reviewerSession == writerSession {
		return "", fmt.Errorf("%w: reviewer must complete in a distinct fresh provider session", ErrWorkflowAuthorityConflict)
	}
	if genuine, err := workflowFreshReviewerOrigin(ctx, tx, issue.ID, candidate.ID, reviewerTaskID); err != nil {
		return "", err
	} else if !genuine {
		return "", fmt.Errorf("%w: reviewer did not originate in a fresh handoff", ErrWorkflowAuthorityConflict)
	}
	return util.UUIDToString(reviewID), nil
}

func workflowHasMutableRuns(ctx context.Context, tx pgx.Tx, issueID pgtype.UUID, except pgtype.UUID) (bool, error) {
	var active bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue
		WHERE issue_id=$1 AND id IS DISTINCT FROM $2
		AND status IN ('dispatched','running','waiting_local_directory'))`, issueID, except).Scan(&active)
	return active, err
}

func workflowHasPendingHandoff(ctx context.Context, tx pgx.Tx, issueID pgtype.UUID) (bool, error) {
	var pending bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_wakeup
		WHERE issue_id=$1 AND handoff IS NOT NULL AND enabled AND handoff_completed_at IS NULL)`, issueID).Scan(&pending)
	return pending, err
}

func workflowRepositoryURLMatchesBase(base, candidate string) bool {
	return candidate == base || candidate == base+".git"
}

func workflowDeliveryBindings(ctx context.Context, tx pgx.Tx, issue db.Issue, prs []HandoffCandidate) ([]workflowDeliveryBinding, error) {
	bindings := make([]workflowDeliveryBinding, 0, len(prs))
	for _, pr := range prs {
		parsed, err := url.Parse(pr.PRURL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("%w: candidate PR URL must be HTTPS without credentials", ErrWorkflowAuthorityInput)
		}
		var b workflowDeliveryBinding
		b.PR = pr
		err = tx.QueryRow(ctx, `SELECT 'github',gi.id,gpr.repo_owner,gpr.repo_name,gpr.pr_number
			FROM github_pull_request gpr JOIN github_installation gi
			ON gi.installation_id=gpr.installation_id AND gi.workspace_id=gpr.workspace_id
			JOIN issue_pull_request ipr ON ipr.pull_request_id=gpr.id AND ipr.issue_id=$3
			WHERE gpr.workspace_id=$1 AND gpr.html_url=$2`, issue.WorkspaceID, pr.PRURL, issue.ID).Scan(
			&b.Provider, &b.BindingID, &b.Owner, &b.Repo, &b.Number)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `SELECT vc.provider,vc.id,vpr.repo_owner,vpr.repo_name,vpr.pr_number
				FROM vcs_pull_request vpr JOIN vcs_connection vc
				ON vc.id=vpr.connection_id AND vc.workspace_id=vpr.workspace_id
				JOIN issue_vcs_pull_request ivpr ON ivpr.pull_request_id=vpr.id AND ivpr.issue_id=$3
				WHERE vpr.workspace_id=$1 AND vpr.html_url=$2 AND vc.provider IN ('forgejo','gitea')`,
				issue.WorkspaceID, pr.PRURL, issue.ID).Scan(&b.Provider, &b.BindingID, &b.Owner, &b.Repo, &b.Number)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: verified provider binding is unavailable for %s", ErrWorkflowAuthorityConflict, pr.PRURL)
		}
		base := "https://" + parsed.Host + "/" + b.Owner + "/" + b.Repo
		pullPath := "/pulls/"
		if b.Provider == "github" {
			if parsed.Host != "github.com" {
				return nil, fmt.Errorf("%w: GitHub PR origin mismatch", ErrWorkflowAuthorityConflict)
			}
			pullPath = "/pull/"
		} else {
			var instance string
			if err := tx.QueryRow(ctx, `SELECT instance_url FROM vcs_connection WHERE id=$1 AND workspace_id=$2`,
				b.BindingID, issue.WorkspaceID).Scan(&instance); err != nil {
				return nil, fmt.Errorf("%w: provider binding missing", ErrWorkflowAuthorityConflict)
			}
			instanceURL, parseErr := url.Parse(instance)
			if parseErr != nil || instanceURL.Scheme != "https" || instanceURL.User != nil ||
				instanceURL.Host != parsed.Host || instanceURL.RawQuery != "" || instanceURL.Fragment != "" {
				return nil, fmt.Errorf("%w: provider origin mismatch", ErrWorkflowAuthorityConflict)
			}
			base = strings.TrimRight(instance, "/") + "/" + b.Owner + "/" + b.Repo
		}
		if !workflowRepositoryURLMatchesBase(base, pr.RepositoryURL) || pr.PRURL != fmt.Sprintf("%s%s%d", base, pullPath, b.Number) {
			return nil, fmt.Errorf("%w: candidate PR identity differs from provider mirror", ErrWorkflowAuthorityConflict)
		}
		b.ProviderRepositoryURL = base
		bindings = append(bindings, b)
	}
	return bindings, nil
}
