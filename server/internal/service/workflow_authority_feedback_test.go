package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func workflowFeedbackCandidate(t *testing.T, status string) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID, pgtype.UUID, WorkflowActor) {
	t.Helper()
	f, wakeups, issueID, writerAgent, supervisorAgent := handoffFixture(t)
	ctx := context.Background()
	writerTask := handoffSourceTask(t, f, issueID, writerAgent)
	f.Exec(t, `UPDATE agent_task_queue SET session_id='writer-session' WHERE id=$1`, writerTask)
	in := handoffInput(writerTask, parseTestUUID(t, supervisorAgent))
	in.Candidates = []HandoffCandidate{}
	handoff, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), writerTask, in)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writerTask)
	wakeDispatch(t, wakeups, handoff)
	stored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: handoff.ID, WorkspaceID: handoff.WorkspaceID})
	if err != nil || !stored.LastTaskID.Valid {
		t.Fatalf("handoff recipient: %+v, %v", stored, err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now(),session_id='supervisor-handoff' WHERE id=$1`, stored.LastTaskID)
	commentID := parseTestUUID(t, f.Insert(t, "comment", testutil.Cols{
		"issue_id": issueID, "workspace_id": f.WorkspaceID, "author_type": "member", "author_id": f.UserID,
		"type": "comment", "content": "Please correct the missing regression and update this candidate.",
	}))
	feedbackTask := parseTestUUID(t, f.Task(t, supervisorAgent, testutil.Cols{
		"issue_id": issueID, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + supervisorAgent + "')"),
		"trigger_comment_id": commentID, "status": "running", "started_at": testutil.Raw("now()"),
		"session_id": "supervisor-comment",
	}))
	f.Exec(t, `UPDATE agent_task_queue SET delivered_comment_ids=ARRAY[$2]::uuid[] WHERE id=$1`, feedbackTask, commentID)
	f.Exec(t, `UPDATE agent_task_queue SET dispatched_at=now() WHERE id=$1`, feedbackTask)
	f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, status)
	return f, WorkflowAuthorityService{Tasks: wakeups.Tasks}, issueID, writerTask, commentID,
		WorkflowActor{Type: "agent", ID: supervisorAgent, SourceTaskID: util.UUIDToString(feedbackTask)}
}

func TestWorkflowFeedbackContinuationAllowsNonterminalStatusesAndRevokesCandidate(t *testing.T) {
	for _, status := range []string{"blocked", "in_progress", "in_review"} {
		t.Run(status, func(t *testing.T) {
			f, svc, issueID, writerTask, commentID, actor := workflowFeedbackCandidate(t, status)
			ctx := context.Background()
			before, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			kind := "in_scope_defect"
			if status == "in_review" {
				kind = "scope_change"
			}
			in := WorkflowFeedbackContinuationInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID),
				ExpectedRevision: before.Revision, CommentID: util.UUIDToString(commentID), Kind: kind}
			exceptionID := dbid.NewV7()
			f.Exec(t, `INSERT INTO issue_workflow_exception
				(id,workspace_id,issue_id,candidate_id,base_policy_version,scope,grant_details,
				actor_type,actor_id,reason,consequences)
				VALUES($1,$2,$3,$4,'test','review','{}'::jsonb,'member',$5,'test','test')`,
				exceptionID, before.WorkspaceID, issueID, before.WorkflowCandidateID, parseTestUUID(t, f.UserID))
			if err := svc.ContinueWorkflowFeedback(ctx, before.WorkspaceID, issueID, actor, in); err != nil {
				t.Fatalf("continue feedback from %s: %v", status, err)
			}
			if err := svc.ContinueWorkflowFeedback(ctx, before.WorkspaceID, issueID, actor, in); err != nil {
				t.Fatalf("exact replay: %v", err)
			}
			after, err := f.q.GetIssue(ctx, issueID)
			if err != nil || after.WorkflowCandidateID.Valid || after.AssigneeType.String != "agent" ||
				util.UUIDToString(after.AssigneeID) == actor.ID || after.Revision != before.Revision+1 {
				t.Fatalf("feedback did not reopen writer: %+v, %v", after, err)
			}
			var source, recordedComment, resume pgtype.UUID
			var commentRevision int64
			var reason, contextMode string
			if err := f.Pool.QueryRow(ctx, `SELECT source_task_id,comment_id,comment_revision,resume_task_id,reason,context_mode
				FROM issue_workflow_rejection WHERE issue_id=$1`, issueID).Scan(&source, &recordedComment, &commentRevision, &resume, &reason, &contextMode); err != nil ||
				source != parseTestUUID(t, actor.SourceTaskID) || recordedComment != commentID || resume != writerTask ||
				commentRevision != 1 || contextMode != "resume" || !strings.Contains(reason, "missing regression") {
				t.Fatalf("feedback provenance or retained writer missing: %v %v %d %v %q %v", source, recordedComment, commentRevision, resume, reason, err)
			}
			if got := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND rerun_of_task_id=$2 AND status='queued'`, issueID, writerTask); got != 1 {
				t.Fatalf("queued writer continuations: %d", got)
			}
			var note, revokedType string
			var revokedID pgtype.UUID
			var forceFresh bool
			if err := f.Pool.QueryRow(ctx, `SELECT handoff_note,force_fresh_session FROM agent_task_queue
				WHERE issue_id=$1 AND rerun_of_task_id=$2 AND status='queued'`, issueID, writerTask).Scan(&note, &forceFresh); err != nil ||
				!strings.Contains(note, "missing regression") || !strings.Contains(note, util.UUIDToString(commentID)) {
				t.Fatalf("writer lacks exact immutable feedback: %q %v", note, err)
			}
			if !forceFresh {
				t.Fatal("rerun task lost rolling-deploy fresh fallback")
			}
			if kind == "scope_change" && !strings.Contains(note, "Update the ticket objective") {
				t.Fatalf("scope change lacks ticket reconciliation instruction: %q", note)
			}
			if err := f.Pool.QueryRow(ctx, `SELECT revoked_by_type,revoked_by_id FROM issue_workflow_exception WHERE id=$1`,
				exceptionID).Scan(&revokedType, &revokedID); err != nil || revokedType != "agent" ||
				revokedID != parseTestUUID(t, actor.ID) {
				t.Fatalf("exception revoker attribution: %s %v %v", revokedType, revokedID, err)
			}
			f.Exec(t, `UPDATE comment SET content='changed after continuation',revision=revision+1,updated_at=now() WHERE id=$1`, commentID)
			f.Exec(t, `UPDATE comment SET deleted_at=now(),content='' WHERE id=$1`, commentID)
			state, err := svc.ReadState(ctx, before.WorkspaceID, issueID, actor)
			snapshotDigest := sha256.Sum256([]byte(reason))
			if err != nil || state.Feedback == nil || state.Feedback.CommentID != util.UUIDToString(commentID) ||
				state.Feedback.CommentRevision != 1 || state.Feedback.ContentSHA256 != hex.EncodeToString(snapshotDigest[:]) {
				t.Fatalf("feedback snapshot changed with comment: %+v, %v", state.Feedback, err)
			}
			// The queued writer cannot run concurrently with the feedback task.
			if claimed, err := svc.Tasks.ClaimTask(ctx, after.AssigneeID); err != nil || claimed != nil {
				t.Fatalf("writer claimed while supervisor active: %+v, %v", claimed, err)
			}
		})
	}
}

func TestWorkflowFeedbackContinuationRequiresDeliveredLiveHumanComment(t *testing.T) {
	f, svc, issueID, _, commentID, actor := workflowFeedbackCandidate(t, "blocked")
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	in := WorkflowFeedbackContinuationInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
		ExpectedRevision: issue.Revision, CommentID: util.UUIDToString(commentID), Kind: "in_scope_defect"}
	other := parseTestUUID(t, f.Insert(t, "comment", testutil.Cols{
		"issue_id": issueID, "workspace_id": f.WorkspaceID, "author_type": "member", "author_id": f.UserID,
		"type": "comment", "content": "Another comment, not delivered to this task.",
	}))
	issue, err = f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision = issue.Revision
	in.CommentID = util.UUIDToString(other)
	if err := svc.ContinueWorkflowFeedback(ctx, issue.WorkspaceID, issueID, actor, in); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("undelivered human comment accepted: %v", err)
	}
	in.CommentID = util.UUIDToString(commentID)
	f.Exec(t, `UPDATE agent_task_queue SET delivered_comment_ids='{}'::uuid[] WHERE id=$1`,
		parseTestUUID(t, actor.SourceTaskID))
	if err := svc.ContinueWorkflowFeedback(ctx, issue.WorkspaceID, issueID, actor, in); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("planned but undelivered human comment accepted: %v", err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET delivered_comment_ids=ARRAY[$2]::uuid[] WHERE id=$1`,
		parseTestUUID(t, actor.SourceTaskID), commentID)
	f.Exec(t, `UPDATE comment SET deleted_at=now() WHERE id=$1`, commentID)
	issue, err = f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision = issue.Revision
	if err := svc.ContinueWorkflowFeedback(ctx, issue.WorkspaceID, issueID, actor, in); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("deleted human comment accepted: %v", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_rejection WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("denied feedback mutated ledger: %d", got)
	}
}

func TestWorkflowFeedbackContinuationRejectsEditsAfterDeliveryAndOversizeSnapshots(t *testing.T) {
	for _, reason := range []string{"edited", "oversize"} {
		t.Run(reason, func(t *testing.T) {
			f, svc, issueID, _, commentID, actor := workflowFeedbackCandidate(t, "blocked")
			ctx := context.Background()
			if reason == "edited" {
				f.Exec(t, `UPDATE comment SET content='changed after delivery',revision=revision+1,
					updated_at=now()+interval '1 second' WHERE id=$1`, commentID)
			} else {
				f.Exec(t, `UPDATE comment SET content=repeat('x',4001),revision=revision+1,
					updated_at=now() WHERE id=$1`, commentID)
				f.Exec(t, `UPDATE agent_task_queue SET dispatched_at=now()+interval '1 second' WHERE id=$1`,
					parseTestUUID(t, actor.SourceTaskID))
			}
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			in := WorkflowFeedbackContinuationInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
				ExpectedRevision: issue.Revision, CommentID: util.UUIDToString(commentID), Kind: "in_scope_defect"}
			err = svc.ContinueWorkflowFeedback(ctx, issue.WorkspaceID, issueID, actor, in)
			want := ErrWorkflowAuthorityForbidden
			if reason == "oversize" {
				want = ErrWorkflowAuthorityInput
			}
			if !errors.Is(err, want) {
				t.Fatalf("%s feedback accepted: %v", reason, err)
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_rejection WHERE issue_id=$1`, issueID); got != 0 {
				t.Fatalf("%s feedback mutated ledger: %d", reason, got)
			}
		})
	}
}

func TestWorkflowFeedbackContinuationCannotRevokeAcceptedDecisionFromOrdinaryMember(t *testing.T) {
	f, svc, issueID, _, commentID, actor := workflowFeedbackCandidate(t, "blocked")
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	memberID := f.member(t, "feedback-member")
	f.Exec(t, `UPDATE comment SET author_id=$2 WHERE id=$1`, commentID, parseTestUUID(t, memberID))
	acceptanceID := dbid.NewV7()
	f.Exec(t, `INSERT INTO issue_workflow_acceptance
		(id,workspace_id,issue_id,candidate_id,mode,actor_type,actor_id,state,policy_version,authority_snapshot)
		VALUES($1,$2,$3,$4,'human','member',$5,'requested',$6,'{}'::jsonb)`,
		acceptanceID, issue.WorkspaceID, issueID, issue.WorkflowCandidateID, parseTestUUID(t, f.UserID),
		"test")
	in := WorkflowFeedbackContinuationInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
		ExpectedRevision: issue.Revision, CommentID: util.UUIDToString(commentID), Kind: "in_scope_defect"}
	if err := svc.ContinueWorkflowFeedback(ctx, issue.WorkspaceID, issueID, actor, in); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("ordinary member revoked requested acceptance: %v", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_rejection WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("denied feedback mutated ledger: %d", got)
	}
}

func TestWorkflowFeedbackContinuationSeparatesReadyFromMergedOrCompletedWork(t *testing.T) {
	for _, state := range []string{"ready", "merged", "outcome_complete"} {
		t.Run(state, func(t *testing.T) {
			f, svc, issueID, _, commentID, actor := workflowFeedbackCandidate(t, "blocked")
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			acceptanceID := dbid.NewV7()
			f.Exec(t, `INSERT INTO issue_workflow_acceptance
				(id,workspace_id,issue_id,candidate_id,mode,actor_type,actor_id,state,policy_version,
				authority_snapshot,outcome_complete)
				VALUES($1,$2,$3,$4,'human','member',$5,'accepted','test','{}'::jsonb,$6)`,
				acceptanceID, issue.WorkspaceID, issueID, issue.WorkflowCandidateID, parseTestUUID(t, f.UserID), state == "outcome_complete")
			if state == "ready" || state == "merged" {
				action, method := "ready", ""
				var mergedAt any
				if state == "merged" {
					action, method, mergedAt = "merge", "squash", time.Now()
				}
				f.Exec(t, `INSERT INTO issue_workflow_delivery
					(id,workspace_id,issue_id,acceptance_id,candidate_id,ordinal,provider,provider_binding_id,
					repository_url,pr_url,repo_owner,repo_name,pr_number,expected_head_sha,action,merge_method,status,merged_at)
					VALUES($1,$2,$3,$4,$5,0,'github',$6,'https://example.test/repo',
					'https://example.test/repo/pull/1','owner','repo',1,$7,$8,NULLIF($9,''),'delivered',$10)`,
					dbid.NewV7(), issue.WorkspaceID, issueID, acceptanceID, issue.WorkflowCandidateID,
					dbid.NewV7(), strings.Repeat("a", 40), action, method, mergedAt)
			}
			in := WorkflowFeedbackContinuationInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
				ExpectedRevision: issue.Revision, CommentID: util.UUIDToString(commentID), Kind: "in_scope_defect"}
			err = svc.ContinueWorkflowFeedback(ctx, issue.WorkspaceID, issueID, actor, in)
			if state == "ready" && err != nil || state != "ready" && !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("%s continuation: %v", state, err)
			}
			want := 0
			if state == "ready" {
				want = 1
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_rejection WHERE issue_id=$1`, issueID); got != want {
				t.Fatalf("%s rejection ledger rows: %d, want %d", state, got, want)
			}
		})
	}
}

func TestWorkflowFeedbackContinuationHumanAssigneeRequiresExactFeedbackTask(t *testing.T) {
	f, svc, issueID, writerTask, commentID, actor := workflowFeedbackCandidate(t, "blocked")
	ctx := context.Background()
	handoff, err := f.q.LatestIssueHandoff(ctx, issueID)
	if err != nil || !handoff.LastTaskID.Valid {
		t.Fatalf("completed handoff unavailable: %+v, %v", handoff, err)
	}
	// Model the server-created member handoff and its dedicated feedback task.
	f.Exec(t, `UPDATE issue_wakeup SET filter_task_id=$2::uuid,source_task_id=$2::uuid,filter_agent_id=$4::uuid,
		handoff=jsonb_set(jsonb_set(jsonb_set(handoff,'{assignee_type}','"member"'::jsonb),
		'{assignee_id}',to_jsonb($3::text)),'{outgoing_task_id}',to_jsonb($2::text)),
		handoff_completed_at=now(),disabled_at=NULL,last_task_id=NULL WHERE id=$1`,
		handoff.ID, handoff.LastTaskID, f.UserID, parseTestUUID(t, actor.ID))
	f.Exec(t, `UPDATE issue SET assignee_type='member',assignee_id=$2,revision=revision+1 WHERE id=$1`,
		issueID, parseTestUUID(t, f.UserID))
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	in := WorkflowFeedbackContinuationInput{CandidateID: util.UUIDToString(current.WorkflowCandidateID),
		ExpectedRevision: current.Revision, CommentID: util.UUIDToString(commentID), Kind: "in_scope_defect"}
	if err := svc.ContinueWorkflowFeedback(ctx, current.WorkspaceID, issueID, actor, in); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("plain agent task inherited human handoff authority: %v", err)
	}
	// A newer comment coalesces onto the same queued task. The marker binds its
	// primary trigger, while the earlier correction remains separately covered.
	primaryComment := parseTestUUID(t, f.Insert(t, "comment", testutil.Cols{
		"issue_id": issueID, "workspace_id": f.WorkspaceID, "author_type": "member", "author_id": f.UserID,
		"type": "comment", "content": "Can you also explain the review result?",
	}))
	f.Exec(t, `UPDATE agent_task_queue SET trigger_evidence_kind='workflow_human_comment',
		trigger_evidence_ref_id=$2::uuid,delegated_from_task_id=$3::uuid,
		trigger_comment_id=$6::uuid,coalesced_comment_ids=ARRAY[$7]::uuid[],
		delivered_comment_ids=ARRAY[$6,$7]::uuid[],dispatched_at=now(),
		originator_user_id=$4,accountable_user_id=$4,
		context=jsonb_build_object('workflow_feedback',jsonb_build_object(
			'candidate_id',$5::text,'handoff_id',$2::text,
			'coordinator_task_id',$3::text,'comment_id',$6::text))
		WHERE id=$1`, parseTestUUID(t, actor.SourceTaskID), handoff.ID, handoff.LastTaskID,
		parseTestUUID(t, f.UserID), in.CandidateID, primaryComment, commentID)
	f.Exec(t, `UPDATE agent_task_queue SET coalesced_comment_ids=ARRAY[$2,$3]::uuid[] WHERE id=$1`,
		handoff.LastTaskID, primaryComment, commentID)
	// A different authorized human wrote in the same thread while the first
	// conversation was queued. Its own deferred task must survive candidate
	// rejection with that human's invocation overlay and coordinator agent.
	other := f.User(t, "other feedback author", "workflow-feedback-other@multica.test")
	f.Member(t, f.WorkspaceID, other, "admin")
	f.Exec(t, `UPDATE agent SET permission_mode='public_to' WHERE id=$1`, parseTestUUID(t, actor.ID))
	f.Exec(t, `INSERT INTO agent_invocation_target(agent_id,target_type,target_id)
		VALUES($1,'member',$2)`, parseTestUUID(t, actor.ID), parseTestUUID(t, other))
	otherComment := parseTestUUID(t, f.Insert(t, "comment", testutil.Cols{
		"issue_id": issueID, "workspace_id": f.WorkspaceID, "author_type": "member", "author_id": other,
		"parent_id": primaryComment, "type": "comment", "content": "Please also check the mobile case.",
	}))
	deferred := parseTestUUID(t, f.Task(t, actor.ID, testutil.Cols{
		"issue_id": issueID, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + actor.ID + "')"),
		"status": "deferred", "trigger_comment_id": otherComment,
	}))
	f.Exec(t, `UPDATE agent_task_queue SET fire_at=now(),trigger_evidence_kind='workflow_human_comment',
		trigger_evidence_ref_id=$2::uuid,delegated_from_task_id=$3::uuid,originator_user_id=$4,
		accountable_user_id=$4,originator_source='direct_human',
		runtime_mcp_overlay='{"second_author":true}'::jsonb,
		runtime_connected_apps='{"second_author":true}'::jsonb,
		context=jsonb_build_object('head_sha','old-head','workflow_feedback',jsonb_build_object(
			'candidate_id',$5::text,'handoff_id',$2::text,
			'coordinator_task_id',$3::text,'comment_id',$6::text))
		WHERE id=$1`, deferred, handoff.ID, handoff.LastTaskID, parseTestUUID(t, other), in.CandidateID, otherComment)
	f.Exec(t, `UPDATE agent_task_queue SET coalesced_comment_ids=array_append(coalesced_comment_ids,$2) WHERE id=$1`,
		handoff.LastTaskID, otherComment)
	var writerAgent pgtype.UUID
	if err := f.Pool.QueryRow(ctx, `SELECT agent_id FROM agent_task_queue WHERE id=$1`, writerTask).Scan(&writerAgent); err != nil {
		t.Fatal(err)
	}
	mentionComment := parseTestUUID(t, f.Insert(t, "comment", testutil.Cols{
		"issue_id": issueID, "workspace_id": f.WorkspaceID, "author_type": "member", "author_id": f.UserID,
		"type": "comment", "content": "@writer Please check the separate API case.",
	}))
	mentionTask := parseTestUUID(t, f.Task(t, util.UUIDToString(writerAgent), testutil.Cols{
		"issue_id": issueID, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + util.UUIDToString(writerAgent) + "')"),
		"status": "queued", "trigger_comment_id": mentionComment,
	}))
	f.Exec(t, `UPDATE agent_task_queue SET originator_user_id=$2,accountable_user_id=$2,
		originator_source='direct_human',runtime_mcp_overlay='{"explicit_mention":true}'::jsonb,
		context=jsonb_build_object('head_sha','old-head') WHERE id=$1`, mentionTask, parseTestUUID(t, f.UserID))
	current, err = f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision = current.Revision
	f.Exec(t, `DELETE FROM agent_invocation_target WHERE agent_id=$1 AND target_type='member' AND target_id=$2`,
		parseTestUUID(t, actor.ID), parseTestUUID(t, other))
	if err := svc.ContinueWorkflowFeedback(ctx, current.WorkspaceID, issueID, actor, in); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("revoked second-author invocation silently retired feedback: %v", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='deferred'
		AND trigger_evidence_kind='workflow_human_comment'`, deferred); got != 1 {
		t.Fatalf("blocked continuation changed deferred feedback: %d", got)
	}
	f.Exec(t, `INSERT INTO agent_invocation_target(agent_id,target_type,target_id)
		VALUES($1,'member',$2)`, parseTestUUID(t, actor.ID), parseTestUUID(t, other))
	if err := svc.ContinueWorkflowFeedback(ctx, current.WorkspaceID, issueID, actor, in); err != nil {
		t.Fatalf("coalesced human-handoff feedback refused: %v", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_rejection
		WHERE issue_id=$1 AND comment_id=$2 AND actor_type='agent'`, issueID, commentID); got != 1 {
		t.Fatalf("human-assigned continuation ledger rows: %d", got)
	}
	var retainedStatus, retainedAgent, retainedAuthor, retainedTrigger, retainedNote string
	var retainedMarker, retainedHead, retainedEvidence, retainedDelegation bool
	var retainedOverlay, retainedApps []byte
	if err := f.Pool.QueryRow(ctx, `SELECT status,agent_id::text,originator_user_id::text,trigger_comment_id::text,
		COALESCE(handoff_note,''),context ? 'workflow_feedback',context ? 'head_sha',
		trigger_evidence_kind IS NOT NULL,delegated_from_task_id IS NOT NULL,
		runtime_mcp_overlay,runtime_connected_apps FROM agent_task_queue WHERE id=$1`, deferred).Scan(
		&retainedStatus, &retainedAgent, &retainedAuthor, &retainedTrigger, &retainedNote,
		&retainedMarker, &retainedHead, &retainedEvidence, &retainedDelegation,
		&retainedOverlay, &retainedApps); err != nil {
		t.Fatal(err)
	}
	var overlay, apps map[string]bool
	if err := json.Unmarshal(retainedOverlay, &overlay); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(retainedApps, &apps); err != nil {
		t.Fatal(err)
	}
	if retainedStatus != "deferred" || retainedAgent != actor.ID || retainedAuthor != other ||
		retainedTrigger != util.UUIDToString(otherComment) || retainedMarker || retainedHead ||
		retainedEvidence || retainedDelegation || !strings.Contains(retainedNote, "superseded") ||
		!overlay["second_author"] || !apps["second_author"] {
		t.Fatalf("second human's feedback was lost or reattributed: %s %s %s %s %q %v %v %v %v %s %s",
			retainedStatus, retainedAgent, retainedAuthor, retainedTrigger, retainedNote,
			retainedMarker, retainedHead, retainedEvidence, retainedDelegation, retainedOverlay, retainedApps)
	}
	var mentionStatus, mentionAgent, mentionAuthor, mentionTrigger, mentionNote string
	var mentionOldHead bool
	var mentionOverlay []byte
	if err := f.Pool.QueryRow(ctx, `SELECT status,agent_id::text,originator_user_id::text,
		trigger_comment_id::text,COALESCE(handoff_note,''),context ? 'head_sha',runtime_mcp_overlay
		FROM agent_task_queue WHERE id=$1`, mentionTask).Scan(&mentionStatus, &mentionAgent,
		&mentionAuthor, &mentionTrigger, &mentionNote, &mentionOldHead, &mentionOverlay); err != nil {
		t.Fatal(err)
	}
	var mentionCapability map[string]bool
	if err := json.Unmarshal(mentionOverlay, &mentionCapability); err != nil {
		t.Fatal(err)
	}
	if mentionStatus != "deferred" || mentionAgent != util.UUIDToString(writerAgent) ||
		mentionAuthor != f.UserID || mentionTrigger != util.UUIDToString(mentionComment) ||
		!strings.Contains(mentionNote, "superseded") || mentionOldHead || !mentionCapability["explicit_mention"] {
		t.Fatalf("explicit human mention was lost or rebound: %s %s %s %s %q %v %s",
			mentionStatus, mentionAgent, mentionAuthor, mentionTrigger, mentionNote, mentionOldHead, mentionOverlay)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`,
		parseTestUUID(t, actor.SourceTaskID))
	claimedWriter, err := svc.Tasks.ClaimTask(ctx, writerAgent)
	if err != nil || claimedWriter == nil || claimedWriter.RerunOfTaskID != writerTask {
		t.Fatalf("correction writer did not claim ahead of preserved mention: %+v %v", claimedWriter, err)
	}
	if claimedCoordinator, err := svc.Tasks.ClaimTask(ctx, parseTestUUID(t, actor.ID)); err != nil || claimedCoordinator != nil {
		t.Fatalf("preserved comment crossed active correction: %+v %v", claimedCoordinator, err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, claimedWriter.ID)
	var coordinatorRuntime pgtype.UUID
	if err := f.Pool.QueryRow(ctx, `SELECT runtime_id FROM agent_task_queue WHERE id=$1`, deferred).Scan(&coordinatorRuntime); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tasks.PromoteDueDeferredTasksForRuntime(ctx, coordinatorRuntime); err != nil {
		t.Fatalf("promote preserved coordinator comment: %v", err)
	}
	claimedCoordinator, err := svc.Tasks.ClaimTask(ctx, parseTestUUID(t, actor.ID))
	if err != nil || claimedCoordinator == nil || claimedCoordinator.ID != deferred {
		t.Fatalf("preserved coordinator comment did not promote and claim: %+v %v", claimedCoordinator, err)
	}
}
