package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkflowNativeCompletionFallbackDoesNotInvalidatePendingAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name, interveningChange string
		wantAccepted            bool
		wantRevisionDelta       int64
		wantFallback            bool
	}{
		{name: "only completion fallback", wantAccepted: true, wantFallback: true},
		{name: "source task manual conversation", interveningChange: "source", wantAccepted: true, wantRevisionDelta: 1},
		{name: "external member conversation", interveningChange: "member", wantAccepted: true, wantRevisionDelta: 2, wantFallback: true},
		{name: "objective changed after request", interveningChange: "scope", wantRevisionDelta: 2, wantFallback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, svc, issueID, reviewerTask, reviewerAgent := workflowSameReviewerAcceptanceFixture(t)
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(reviewerTask)}
			state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor,
				WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
					ExpectedRevision: issue.Revision, ClassificationReason: "Independent exact-candidate review passed."})
			if err != nil || state != "requested" {
				t.Fatalf("request acceptance: state=%s err=%v", state, err)
			}
			switch tc.interveningChange {
			case "source":
				svc.Tasks.createAgentComment(ctx, issueID, parseTestUUID(t, reviewerAgent),
					"A manual note after the acceptance request", "comment", pgtype.UUID{}, reviewerTask)
			case "member":
				if _, err := f.q.CreateComment(ctx, db.CreateCommentParams{ID: dbid.NewV7(),
					IssueID: issueID, WorkspaceID: issue.WorkspaceID, AuthorType: "member",
					AuthorID: parseTestUUID(t, f.UserID), Content: "A new member comment after the request", Type: "comment"}); err != nil {
					t.Fatal(err)
				}
			case "scope":
				f.Exec(t, `UPDATE issue SET description='The human changed the objective after this request.',revision=revision+1 WHERE id=$1`, issueID)
			}
			result, _ := json.Marshal(protocol.TaskCompletedPayload{TaskID: util.UUIDToString(reviewerTask), Output: "Final review finished."})
			completed, transitioned, err := svc.Tasks.CompleteTaskWithTransition(ctx, reviewerTask, result,
				"reviewer-session", "", "", false, "", "")
			if err != nil || !transitioned || completed.Status != "completed" {
				t.Fatalf("native task completion: transitioned=%v task=%+v err=%v", transitioned, completed, err)
			}
			var fallbackCount int
			if err := f.Pool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id=$1 AND source_task_id=$2
				AND content='Final review finished.'`, issueID, reviewerTask).Scan(&fallbackCount); err != nil {
				t.Fatal(err)
			}
			if (fallbackCount == 1) != tc.wantFallback {
				t.Fatalf("completion fallback count=%d, want=%v", fallbackCount, tc.wantFallback)
			}
			afterCompletion, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			if afterCompletion.Revision != issue.Revision+tc.wantRevisionDelta {
				t.Fatalf("issue revision %d -> %d; want delta %d", issue.Revision, afterCompletion.Revision, tc.wantRevisionDelta)
			}
			processed, err := svc.FinalizeNextRequestedAcceptance(ctx)
			if err != nil || !processed {
				t.Fatalf("finalize native completion: processed=%v err=%v", processed, err)
			}
			wantState := "blocked"
			if tc.wantAccepted {
				wantState = "accepted"
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state=$2`, issueID, wantState); got != 1 {
				t.Fatalf("intervening %q left %d %s acceptance rows", tc.interveningChange, got, wantState)
			}
		})
	}
}
