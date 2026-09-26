package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestClaimCommentRecordsOnlyExactCompletedRetainedSessionSource(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, tc := range []struct {
		name         string
		sourceState  string
		fresh        bool
		foreignRun   bool
		wantLineage  bool
		agentOnly    bool
		agentTrigger bool
		oldDaemon    bool
	}{
		{name: "coalesced retained completed turn", sourceState: "completed", wantLineage: true},
		{name: "failed session remains conversational only", sourceState: "failed"},
		{name: "cancelled session remains conversational only", sourceState: "cancelled"},
		{name: "fresh claim clears prior lineage", sourceState: "completed", fresh: true},
		{name: "runtime changed", sourceState: "completed", foreignRun: true},
		{name: "agent self mention has no human continuation authority", sourceState: "completed", agentOnly: true},
		{name: "coalesced human evidence survives agent primary trigger", sourceState: "completed", agentTrigger: true, wantLineage: true},
		{name: "old daemon resumes conversation without borrowing handoff ancestry", sourceState: "completed", oldDaemon: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := createCommentDeliveryFixture(t, tc.name)
			if tc.agentOnly {
				for _, id := range fixture.commentID {
					dbfx.Exec(t, `UPDATE comment SET author_type='agent',author_id=$2 WHERE id=$1`, id, fixture.agentID)
				}
			} else if tc.agentTrigger {
				dbfx.Exec(t, `UPDATE comment SET author_type='agent',author_id=$2 WHERE id=$1`, fixture.commentID[2], fixture.agentID)
			}
			sourceRuntime := fixture.runtimeID
			if tc.foreignRun {
				sourceRuntime = createClaimReclaimRuntime(t, ctx, "Foreign retained comment runtime")
			}
			sourceID := dbfx.Task(t, fixture.agentID, testutil.Cols{
				"issue_id": fixture.issueID, "runtime_id": sourceRuntime, "status": tc.sourceState,
				"session_id": "exact-retained-comment-session", "work_dir": "/tmp/exact-retained-comment-workdir",
				"completed_at": testutil.Raw("now()-interval '1 minute'"),
			})
			// A context assertion is never authoritative. A fresh redelivery also
			// clears a previously persisted source rather than inheriting it.
			dbfx.Exec(t, `UPDATE agent_task_queue SET context=jsonb_build_object('comment_resume_from_task_id',$2::text),
				comment_resume_from_task_id=$2::uuid,force_fresh_session=$3 WHERE id=$1`, fixture.taskID, sourceID, tc.fresh)
			capabilities := protocol.DaemonCapabilityCoalescedCommentsV1
			if !tc.oldDaemon {
				capabilities += "," + protocol.DaemonCapabilityRetainedContextResetV1
			}
			resp := claimCommentDeliveryFixture(t, fixture, capabilities)
			stored, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(fixture.taskID))
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantLineage {
				if stored.CommentResumeFromTaskID != parseUUID(sourceID) || resp.PriorSessionID != "exact-retained-comment-session" || len(stored.DeliveredCommentIds) != 3 {
					t.Fatalf("coalesced claim lost exact retained source or receipt: source=%v prior=%q delivered=%v", stored.CommentResumeFromTaskID, resp.PriorSessionID, stored.DeliveredCommentIds)
				}
			} else if stored.CommentResumeFromTaskID.Valid {
				t.Fatalf("claim recorded unauthorized continuation source: %v", stored.CommentResumeFromTaskID)
			}
			if tc.oldDaemon && resp.PriorSessionID != "exact-retained-comment-session" {
				t.Fatalf("old daemon lost conversational resume: %q", resp.PriorSessionID)
			}
		})
	}
}
