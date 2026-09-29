package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// A null candidate binding can bridge the gap before the first handoff only
// while the original signed, delivered approval is still authoritative.
func TestForgejoPreCandidateApprovalRejectsUntrustedOrSupersededInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, workflowHumanCommentFixture, db.VcsConnection, pgtype.UUID)
	}{
		{name: "connection changed from Forgejo", change: func(t *testing.T, _ workflowHumanCommentFixture, conn db.VcsConnection, _ pgtype.UUID) {
			dbfx.Exec(t, `UPDATE vcs_connection SET provider='gitea' WHERE id=$1`, conn.ID)
		}},
		{name: "provider author unmapped", change: func(t *testing.T, _ workflowHumanCommentFixture, _ db.VcsConnection, inputID pgtype.UUID) {
			dbfx.Exec(t, `UPDATE vcs_workflow_input SET provider_author_id='999' WHERE id=$1`, inputID)
		}},
		{name: "input not delivered", change: func(t *testing.T, _ workflowHumanCommentFixture, _ db.VcsConnection, inputID pgtype.UUID) {
			dbfx.Exec(t, `UPDATE vcs_workflow_input SET processed_at=NULL WHERE id=$1`, inputID)
		}},
		{name: "newer edit", change: func(t *testing.T, _ workflowHumanCommentFixture, _ db.VcsConnection, inputID pgtype.UUID) {
			insertNewerForgejoComment(t, inputID, "edited")
		}},
		{name: "newer deletion", change: func(t *testing.T, _ workflowHumanCommentFixture, _ db.VcsConnection, inputID pgtype.UUID) {
			insertNewerForgejoComment(t, inputID, "deleted")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, conn, _, candidateID, inputID := setupForgejoPreCandidateApproval(t)
			tc.change(t, f, conn, inputID)
			issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
			if err != nil {
				t.Fatal(err)
			}
			var taskID pgtype.UUID
			if err := testPool.QueryRow(ctx, `SELECT task_id FROM vcs_workflow_input WHERE id=$1`, inputID).Scan(&taskID); err != nil || !taskID.Valid {
				t.Fatalf("source task missing: %v %v", taskID, err)
			}
			actor := service.WorkflowActor{Type: "agent", ID: uuidToString(f.writer), SourceTaskID: uuidToString(taskID)}
			svc := testHandler.workflowAuthorityService()
			svc.ReviewVerifier = func(context.Context, service.WorkflowReviewEvidenceInput) error { return nil }
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issue.ID, actor)
			if err != nil {
				t.Fatal(err)
			}
			if view.AvailableActions.AcceptComment {
				t.Fatal("untrusted pre-candidate input still offered comment acceptance")
			}
			_, err = svc.AcceptWorkflowComment(ctx, issue.WorkspaceID, issue.ID, actor,
				service.WorkflowCommentAcceptanceInput{CandidateID: uuidToString(candidateID), ExpectedRevision: issue.Revision,
					Source: "forgejo", SourceID: uuidToString(inputID), Action: "ready", Reason: "Stale input must not accept."})
			if !errors.Is(err, service.ErrWorkflowAuthorityForbidden) {
				t.Fatalf("untrusted pre-candidate input was not forbidden: %v", err)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, f.issueID); got != 0 {
				t.Fatalf("denied approval recorded %d acceptances", got)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery d
				JOIN issue_workflow_acceptance a ON a.id=d.acceptance_id WHERE a.issue_id=$1`, f.issueID); got != 0 {
				t.Fatalf("denied approval recorded %d deliveries", got)
			}
		})
	}
}

func insertNewerForgejoComment(t *testing.T, inputID pgtype.UUID, action string) {
	t.Helper()
	dbfx.Exec(t, `INSERT INTO vcs_workflow_input
		(id,workspace_id,issue_id,connection_id,pull_request_id,event_key,kind,content,html_url,head_sha,
		 created_at,object_id,object_revision,object_revision_at,object_action,provider_author_id,
		 provider_author_login,body,candidate_id)
		SELECT $2,workspace_id,issue_id,connection_id,pull_request_id,event_key||':'||$3,kind,content,
		 html_url,head_sha,created_at+interval '1 second',object_id,object_revision||':new',
		 object_revision_at+interval '1 second',$3,provider_author_id,provider_author_login,
		 CASE WHEN $3='deleted' THEN '' ELSE 'Approval withdrawn.' END,candidate_id
		FROM vcs_workflow_input WHERE id=$1`, inputID, dbid.NewV7(), action)
}
