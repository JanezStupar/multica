package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// A direct assignment is fresh human intent after a consumed member handoff,
// while the handoff must continue fencing unrelated and agent-derived work.
func TestWorkflowClaimDirectHumanAssignmentAfterMemberHandoff(t *testing.T) {
	f, owner := newPrincipalFixture(t)
	actor := f.member(t, "assignment-actor")
	coordinator := f.privateAgentOwnedBy(t, owner, "assignment-coordinator")
	assigned := f.privateAgentOwnedBy(t, actor, "assignment-recipient")
	issue := workflowClaimEnrolledIssue(t, f, "Member assigns work after handoff")
	source := workflowClaimTask(t, f, coordinator, issue, testutil.Cols{"status": "completed"})
	handoff := workflowClaimHandoff(t, f, issue, coordinator, coordinator, source)
	f.Exec(t, `UPDATE issue_wakeup SET handoff=jsonb_build_object('assignee_type','member',
		'assignee_id',$2::text,'outgoing_task_id',$3::text),
		handoff_completed_at=now()-interval '1 minute',enabled=false WHERE id=$1`, handoff, owner, source)
	// A completed agent transfer after the human handoff must not obscure
	// the completed human boundary for this new explicit assignment.
	laterTransfer := workflowClaimHandoff(t, f, issue, coordinator, assigned, source)
	f.Exec(t, `UPDATE issue_wakeup SET handoff=jsonb_build_object('assignee_type','agent',
		'assignee_id',$2::text,'outgoing_task_id',$3::text),
		handoff_completed_at=now()-interval '30 seconds',enabled=false WHERE id=$1`, laterTransfer, assigned, source)
	f.Exec(t, `UPDATE issue SET status='in_progress',assignee_type='agent',assignee_id=$2 WHERE id=$1`, issue, assigned)
	f.Exec(t, `UPDATE issue SET creator_type='member',creator_id=$2 WHERE id=$1`, issue, actor)
	assignment := f.Insert(t, "activity_log", testutil.Cols{
		"workspace_id": f.WorkspaceID, "issue_id": issue, "actor_type": "member", "actor_id": actor,
		"action": "assignee_changed", "created_at": testutil.Raw("now()-interval '10 seconds'"),
		"details": testutil.Raw("jsonb_build_object('from_type','member','from_id','" + owner + "','to_type','agent','to_id','" + assigned + "')"),
	})
	assignedIssue, err := f.q.GetIssue(context.Background(), util.MustParseUUID(issue))
	if err != nil {
		t.Fatal(err)
	}
	directTask, err := f.svc.TaskSvc.EnqueueTaskForIssueWithHandoff(context.Background(), assignedIssue, "", util.MustParseUUID(actor))
	if err != nil {
		t.Fatalf("enqueue direct human assignment: %v", err)
	}
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, directTask.ID)
	queued := util.UUIDToString(directTask.ID)
	var runtime string
	f.QueryRow(t, `SELECT runtime_id::text FROM agent WHERE id=$1`, assigned).Scan(&runtime)
	otherRuntime := f.Runtime(t, "assignment-other-runtime")
	otherIssue := f.Issue(t, "Unrelated evidence")
	comment := f.Comment(t, issue, "Unrelated comment")
	outsider := f.User(t, "assignment outsider", "assignment-outsider-"+issue+"@multica.test")

	claimable := func() bool {
		t.Helper()
		var got bool
		f.QueryRow(t, `SELECT workflow_task_claimable($1::uuid,$2::uuid)`, queued, issue).Scan(&got)
		return got
	}
	var regular bool
	f.QueryRow(t, `SELECT workflow_regular_task_claimable($1::uuid,$2::uuid)`, queued, issue).Scan(&regular)
	if regular || !claimable() {
		t.Fatalf("assignment claim paths: regular=%v full=%v", regular, claimable())
	}

	// Restore each failed proof before exercising the next guard on the same
	// task, then exercise the actual claim transaction with that exact task.
	for _, tc := range []struct {
		name, breakSQL, restoreSQL string
		args                       []any
	}{
		{"assignee_changed", `UPDATE issue SET assignee_id=$3 WHERE id=$1`, `UPDATE issue SET assignee_id=$2 WHERE id=$1`, []any{issue, assigned, coordinator}},
		{"member_assignee", `UPDATE issue SET assignee_type='member',assignee_id=$3 WHERE id=$1`, `UPDATE issue SET assignee_type='agent',assignee_id=$2 WHERE id=$1`, []any{issue, assigned, actor}},
		{"runtime_changed", `UPDATE agent_task_queue SET runtime_id=$3 WHERE id=$1`, `UPDATE agent_task_queue SET runtime_id=$2 WHERE id=$1`, []any{queued, runtime, otherRuntime}},
		{"archived_recipient", `UPDATE agent SET archived_at=now() WHERE id=$1`, `UPDATE agent SET archived_at=NULL WHERE id=$1`, []any{assigned}},
		{"missing_membership", `UPDATE member SET user_id=$3 WHERE workspace_id=$1 AND user_id=$2`, `UPDATE member SET user_id=$2 WHERE workspace_id=$1 AND user_id=$3`, []any{f.WorkspaceID, actor, outsider}},
		{"terminal", `UPDATE issue SET status='cancelled' WHERE id=$1`, `UPDATE issue SET status='in_progress' WHERE id=$1`, []any{issue}},
		{"agent_origin", `UPDATE agent_task_queue SET originator_source='delegation' WHERE id=$1`, `UPDATE agent_task_queue SET originator_source='direct_human' WHERE id=$1`, []any{queued}},
		{"wrong_evidence", `UPDATE agent_task_queue SET trigger_evidence_ref_id=$3 WHERE id=$1`, `UPDATE agent_task_queue SET trigger_evidence_ref_id=$2 WHERE id=$1`, []any{queued, issue, otherIssue}},
		{"comment_evidence", `UPDATE agent_task_queue SET trigger_evidence_kind='comment' WHERE id=$1`, `UPDATE agent_task_queue SET trigger_evidence_kind='issue_assignment' WHERE id=$1`, []any{queued}},
		{"comment_trigger", `UPDATE agent_task_queue SET trigger_comment_id=$2 WHERE id=$1`, `UPDATE agent_task_queue SET trigger_comment_id=NULL WHERE id=$1`, []any{queued, comment}},
		{"delegated", `UPDATE agent_task_queue SET delegated_from_task_id=$2 WHERE id=$1`, `UPDATE agent_task_queue SET delegated_from_task_id=NULL WHERE id=$1`, []any{queued, source}},
		{"parent", `UPDATE agent_task_queue SET parent_task_id=$2 WHERE id=$1`, `UPDATE agent_task_queue SET parent_task_id=NULL WHERE id=$1`, []any{queued, source}},
		{"retry", `UPDATE agent_task_queue SET retry_of_task_id=$2 WHERE id=$1`, `UPDATE agent_task_queue SET retry_of_task_id=NULL WHERE id=$1`, []any{queued, source}},
		{"rerun", `UPDATE agent_task_queue SET rerun_of_task_id=$2 WHERE id=$1`, `UPDATE agent_task_queue SET rerun_of_task_id=NULL WHERE id=$1`, []any{queued, source}},
		{"stale_assignment", `UPDATE agent_task_queue SET created_at=now()-interval '2 minutes' WHERE id=$1`, `UPDATE agent_task_queue SET created_at=now() WHERE id=$1`, []any{queued}},
		{"missing_explicit_actor", `UPDATE agent_task_queue SET context=context-'explicit_assignment_actor_user_id' WHERE id=$1`, `UPDATE agent_task_queue SET context=jsonb_set(COALESCE(context,'{}'::jsonb),'{explicit_assignment_actor_user_id}',to_jsonb($2::text)) WHERE id=$1`, []any{queued, actor}},
		{"wrong_explicit_actor", `UPDATE agent_task_queue SET context=jsonb_set(context,'{explicit_assignment_actor_user_id}',to_jsonb($3::text)) WHERE id=$1`, `UPDATE agent_task_queue SET context=jsonb_set(context,'{explicit_assignment_actor_user_id}',to_jsonb($2::text)) WHERE id=$1`, []any{queued, actor, owner}},
		{"agent_assignment_receipt", `UPDATE activity_log SET actor_type='agent' WHERE id=$1`, `UPDATE activity_log SET actor_type='member' WHERE id=$1`, []any{assignment}},
		{"wrong_assignment_actor", `UPDATE activity_log SET actor_id=$3 WHERE id=$1`, `UPDATE activity_log SET actor_id=$2 WHERE id=$1`, []any{assignment, actor, owner}},
		{"wrong_assignment_recipient", `UPDATE activity_log SET details=jsonb_set(details,'{to_id}',to_jsonb($3::text)) WHERE id=$1`, `UPDATE activity_log SET details=jsonb_set(details,'{to_id}',to_jsonb($2::text)) WHERE id=$1`, []any{assignment, assigned, coordinator}},
		{"assignment_after_task", `UPDATE activity_log SET created_at=now()+interval '1 minute' WHERE id=$1`, `UPDATE activity_log SET created_at=(SELECT created_at-interval '1 second' FROM agent_task_queue WHERE id=$2) WHERE id=$1`, []any{assignment, queued}},
		{"old_assignment_receipt", `UPDATE activity_log SET created_at=now()-interval '2 minutes' WHERE id=$1`, `UPDATE activity_log SET created_at=(SELECT created_at-interval '1 second' FROM agent_task_queue WHERE id=$2) WHERE id=$1`, []any{assignment, queued}},
		{"handoff_source_mismatch", `UPDATE issue_wakeup SET source_task_id=$3 WHERE id=$1`, `UPDATE issue_wakeup SET source_task_id=$2 WHERE id=$1`, []any{handoff, source, queued}},
		{"handoff_outgoing_mismatch", `UPDATE issue_wakeup SET handoff=jsonb_set(handoff,'{outgoing_task_id}',to_jsonb($3::text)) WHERE id=$1`, `UPDATE issue_wakeup SET handoff=jsonb_set(handoff,'{outgoing_task_id}',to_jsonb($2::text)) WHERE id=$1`, []any{handoff, source, queued}},
		{"unfinished_source", `UPDATE agent_task_queue SET status='failed' WHERE id=$1`, `UPDATE agent_task_queue SET status='completed' WHERE id=$1`, []any{source}},
	} {
		// Each mutation shares its row and restored/broken value, but a
		// statement binds only the parameters it uses.
		exec := func(sql string) {
			t.Helper()
			args := tc.args[:1]
			if strings.Contains(sql, "$3") && !strings.Contains(sql, "$2") {
				sql = strings.ReplaceAll(sql, "$3", "$2")
				args = []any{tc.args[0], tc.args[2]}
			} else if strings.Contains(sql, "$3") {
				args = tc.args
			} else if strings.Contains(sql, "$2") {
				args = tc.args[:2]
			}
			f.Exec(t, sql, args...)
		}
		exec(tc.breakSQL)
		if claimable() {
			t.Fatalf("%s: invalid assignment became claimable", tc.name)
		}
		exec(tc.restoreSQL)
		if !claimable() {
			t.Fatalf("%s: restored assignment stayed blocked", tc.name)
		}
	}

	f.Exec(t, `UPDATE issue SET workflow_frozen=true WHERE id=$1`, issue)
	if claimable() {
		t.Fatal("assignment bypassed frozen issue")
	}
	// Reset through the explicit migration guard, as production does, so the
	// fixture never disables the frozen-issue write fence.
	tx, err := f.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), "SET LOCAL multica.workflow_migration='on'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `UPDATE issue SET workflow_frozen=false,workflow_migrated_at=now() WHERE id=$1`, issue); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !claimable() {
		t.Fatal("assignment stayed blocked after explicit workflow migration")
	}

	// A later human status edit must not invalidate an already queued direct
	// assignment; trigger actor proof is evaluated at task creation.
	laterStatus := f.Insert(t, "activity_log", testutil.Cols{
		"workspace_id": f.WorkspaceID, "issue_id": issue, "actor_type": "member", "actor_id": actor,
		"action": "status_changed", "created_at": testutil.Raw("now()+interval '1 minute'"),
	})
	if !claimable() {
		t.Fatal("later human status event blocked the earlier assignment")
	}
	// Even a missing best-effort audit event cannot lend member-creator
	// attribution to an actual agent status promotion.
	currentIssue, err := f.q.GetIssue(context.Background(), util.MustParseUUID(issue))
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='cancelled' WHERE id=$1`, queued)
	fallback, err := f.svc.TaskSvc.EnqueueTaskForIssueWithHandoff(context.Background(), currentIssue, "", pgtype.UUID{})
	if err != nil {
		t.Fatalf("enqueue fallback assignment: %v", err)
	}
	f.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, fallback.ID)
	if fallback.OriginatorSource.String != "direct_human" || util.UUIDToString(fallback.OriginatorUserID) != actor {
		t.Fatalf("fallback origin = %q; want direct_human to exercise the attribution ambiguity", fallback.OriginatorSource.String)
	}
	var fallbackClaimable bool
	f.QueryRow(t, `SELECT workflow_task_claimable($1::uuid,$2::uuid)`, fallback.ID, issue).Scan(&fallbackClaimable)
	if fallbackClaimable {
		t.Fatal("unaudited agent promotion borrowed the old human assignment receipt")
	}
	f.Exec(t, `UPDATE activity_log SET actor_type='agent',actor_id=$2,
		created_at=(SELECT created_at-interval '1 microsecond' FROM agent_task_queue WHERE id=$3)
		WHERE id=$1`, laterStatus, assigned, fallback.ID)
	f.QueryRow(t, `SELECT workflow_task_claimable($1::uuid,$2::uuid)`, fallback.ID, issue).Scan(&fallbackClaimable)
	if fallbackClaimable {
		t.Fatal("audited agent promotion borrowed member-creator attribution")
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='cancelled' WHERE id=$1`, fallback.ID)
	f.Exec(t, `UPDATE agent_task_queue SET status='queued' WHERE id=$1`, queued)
	f.Exec(t, `UPDATE activity_log SET created_at=now()+interval '1 minute' WHERE id=$1`, laterStatus)

	candidate := dbid.NewV7()
	f.Exec(t, `UPDATE issue SET workflow_candidate_id=$2 WHERE id=$1`, issue, candidate)
	acceptance := f.Insert(t, "issue_workflow_acceptance", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": f.WorkspaceID, "issue_id": issue, "candidate_id": candidate,
		"mode": "human", "actor_type": "member", "actor_id": actor, "state": "requested",
		"policy_version": "test", "authority_snapshot": "{}", "completion_version": 2,
		"accepted_status_key": "in_review", "outcome_agent_id": assigned,
	})
	for _, state := range []string{"requested", "accepted"} {
		f.Exec(t, `UPDATE issue_workflow_acceptance SET state=$2 WHERE id=$1`, acceptance, state)
		if claimable() {
			t.Fatalf("assignment bypassed %s acceptance", state)
		}
	}
	f.Exec(t, `UPDATE issue_workflow_acceptance SET revoked_at=now() WHERE id=$1`, acceptance)
	if !claimable() {
		t.Fatal("revoked acceptance still blocked assignment")
	}

	pending := workflowClaimHandoff(t, f, issue, coordinator, coordinator, source)
	f.Exec(t, `UPDATE issue_wakeup SET created_at=(SELECT created_at+interval '1 second' FROM issue_wakeup WHERE id=$2),
		handoff=jsonb_build_object('assignee_type','member','assignee_id',$3::text,'outgoing_task_id',$4::text)
		WHERE id=$1`, pending, handoff, owner, source)
	if claimable() {
		t.Fatal("assignment bypassed newer pending human handoff")
	}
	f.Exec(t, `UPDATE issue_wakeup SET enabled=false WHERE id=$1`, pending)
	if !claimable() {
		t.Fatal("disabled pending handoff still blocked assignment")
	}
	for _, status := range []string{"dispatched", "running", "waiting_local_directory"} {
		active := workflowClaimTask(t, f, coordinator, issue, testutil.Cols{"status": status})
		if claimable() {
			t.Fatalf("assignment bypassed %s issue work", status)
		}
		f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, active)
	}
	if !claimable() {
		t.Fatal("assignment stayed blocked after active work completed")
	}

	svc := NewTaskService(f.q, f.Pool, nil, events.New())
	task, err := svc.ClaimTask(context.Background(), util.MustParseUUID(assigned))
	if err != nil || task == nil || util.UUIDToString(task.ID) != queued {
		t.Fatalf("direct assignment claim = %+v, %v; want %s", task, err, queued)
	}
	if duplicate, err := svc.ClaimTask(context.Background(), util.MustParseUUID(assigned)); err != nil || duplicate != nil {
		t.Fatalf("second assignment claim = %+v, %v; want no duplicate", duplicate, err)
	}
}
