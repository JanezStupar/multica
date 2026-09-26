package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func handoffFixture(t *testing.T) (principalFixture, *IssueWakeupService, pgtype.UUID, string, string) {
	t.Helper()
	f, s, issue, sourceAgent := wakeFixture(t)
	targetAgent := f.privateAgentOwnedBy(t, f.UserID, "handoff-target")
	var platform AgentSkillData
	for _, skill := range s.Tasks.AllBuiltinSkills() {
		if skill.Name == PlatformSkillName {
			platform = skill
			break
		}
	}
	platform.ID = util.UUIDToString(dbid.NewV7())
	platform.Files = append(platform.Files, AgentSkillFileData{Path: "runtime/issue-workflow.md", Content: "Follow the pinned workflow for this test issue."})
	policy, err := s.Tasks.NewIssueWorkflowPolicy(platform)
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := json.Marshal(policy)
	if _, err := f.Pool.Exec(context.Background(), "UPDATE issue SET workflow_policy=$2 WHERE id=$1", issue, archive); err != nil {
		t.Fatal(err)
	}
	return f, s, issue, sourceAgent, targetAgent
}

func handoffSourceTask(t *testing.T, f principalFixture, issue pgtype.UUID, agent string) pgtype.UUID {
	t.Helper()
	id := f.Task(t, agent, testutil.Cols{
		"issue_id": issue, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + agent + "')"),
		"status": "running", "started_at": testutil.Raw("now()"),
	})
	return parseTestUUID(t, id)
}

func handoffInput(source, target pgtype.UUID) HandoffInput {
	return HandoffInput{
		RequestKey: util.UUIDToString(dbid.NewV7()), OutgoingTaskID: util.UUIDToString(source), AgentID: util.UUIDToString(target),
		Status: "in_review", ContextMode: "fresh", Instruction: "Review the candidate against the ticket and report findings.",
		Candidates:   []HandoffCandidate{{RepositoryURL: "https://example.test/repo", PRURL: "https://example.test/repo/pull/3", Branch: "feature/work", CommitSHA: strings.Repeat("a", 40), Draft: true}},
		EvidenceURLs: []string{"https://example.test/repo/actions/runs/8"},
	}
}

func TestIssueHandoffSourceCompletionQueuesOnceWithAtomicOwnerChange(t *testing.T) {
	f, s, issue, sourceAgent, targetAgent := handoffFixture(t)
	ctx := context.Background()
	source := handoffSourceTask(t, f, issue, sourceAgent)
	in := handoffInput(source, parseTestUUID(t, targetAgent))
	w, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source, in)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.q.GetIssue(ctx, issue)
	if err != nil || before.Status == "in_review" || before.AssigneeID.Valid {
		t.Fatalf("handoff applied before source completion: issue=%+v err=%v", before, err)
	}
	if got := f.Count(t, "SELECT count(*) FROM issue_wakeup_receipt WHERE wakeup_id=$1", w.ID); got != 0 {
		t.Fatalf("premature terminal receipt: %d", got)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now(),result=$2 WHERE id=$1", source, []byte(`{"verdict":"private source report"}`)); err != nil {
		t.Fatal(err)
	}
	if got := f.Count(t, "SELECT count(*) FROM issue_wakeup_receipt WHERE wakeup_id=$1 AND event_type='task.completed'", w.ID); got != 1 {
		t.Fatalf("source completion captured %d receipts, want 1", got)
	}
	wakeDispatch(t, s, w)
	wakeDispatch(t, s, w)
	updated, err := f.q.GetIssue(ctx, issue)
	if err != nil || updated.Status != "in_review" || updated.AssigneeType.String != "agent" || updated.AssigneeID != parseTestUUID(t, targetAgent) {
		t.Fatalf("handoff target not applied: issue=%+v err=%v", updated, err)
	}
	got, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
	if err != nil || !got.LastTaskID.Valid || got.Enabled {
		t.Fatalf("handoff not consumed: wakeup=%+v err=%v", got, err)
	}
	task, err := f.q.GetAgentTask(ctx, got.LastTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !task.ForceFreshSession || task.AgentID != parseTestUUID(t, targetAgent) || strings.Contains(task.HandoffNote.String, "private source report") {
		t.Fatalf("recipient did not get bounded fresh context: %+v", task)
	}
	var contextValue map[string]any
	if err := json.Unmarshal(task.Context, &contextValue); err != nil {
		t.Fatal(err)
	}
	workflow, ok := contextValue["workflow_handoff"].(map[string]any)
	if !ok || workflow["outgoing_task_id"] != in.OutgoingTaskID || workflow["context_mode"] != "fresh" || workflow["resume_task_id"] != nil {
		t.Fatalf("wrong handoff context: %#v", contextValue)
	}
	if !updated.WorkflowCandidateID.Valid || workflow["candidate_id"] != util.UUIDToString(updated.WorkflowCandidateID) ||
		!strings.Contains(task.HandoffNote.String, "Candidate ID: "+util.UUIDToString(updated.WorkflowCandidateID)) {
		t.Fatalf("fresh reviewer lacks neutral candidate ID: context=%#v note=%q", workflow, task.HandoffNote.String)
	}
	if got := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE context->>'wakeup_id'=$1", util.UUIDToString(w.ID)); got != 1 {
		t.Fatalf("enqueued %d recipients, want one", got)
	}
	if got := f.Count(t, "SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action IN ('status_changed','assignee_changed')", issue); got != 2 {
		t.Fatalf("handoff recorded %d transition activities, want two", got)
	}
	replayed, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source, in)
	if err != nil || replayed.ID != w.ID {
		t.Fatalf("idempotent replay = %+v, %v", replayed, err)
	}
	in.Instruction = "Changed instruction"
	if _, err = s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source, in); !errors.Is(err, ErrWakeupConflict) {
		t.Fatalf("changed payload on same request key: %v", err)
	}
	if err = s.CheckClaim(ctx, task); err != nil {
		t.Fatalf("recipient claim was rejected: %v", err)
	}
	retry := db.AgentTaskQueue{ID: dbid.NewV7(), IssueID: task.IssueID, AgentID: task.AgentID, RuntimeID: task.RuntimeID, RetryOfTaskID: task.ID}
	if !handoffRecipientMatches(ctx, s.Tasks.Queries, got, retry) {
		t.Fatal("same-scope retry lost recipient lineage")
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE issue SET assignee_id=$2 WHERE id=$1", issue, parseTestUUID(t, sourceAgent)); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckClaim(ctx, task); !errors.Is(err, ErrWakeupForbidden) {
		t.Fatalf("stale recipient claim after reassignment: %v", err)
	}
}

func retainedCommentHandoffFixture(t *testing.T) (principalFixture, *IssueWakeupService, db.IssueWakeup, db.AgentTaskQueue, string) {
	t.Helper()
	f, s, issue, sourceAgent, recipientAgent := handoffFixture(t)
	ctx := context.Background()
	source := handoffSourceTask(t, f, issue, sourceAgent)
	w, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source, handoffInput(source, parseTestUUID(t, recipientAgent)))
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", source)
	wakeDispatch(t, s, w)
	w, err = f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
	if err != nil || !w.LastTaskID.Valid {
		t.Fatalf("recipient missing: %+v, %v", w, err)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now(),session_id='retained-recipient-session' WHERE id=$1", w.LastTaskID)
	recipient, err := f.q.GetAgentTask(ctx, w.LastTaskID)
	if err != nil {
		t.Fatal(err)
	}
	return f, s, w, recipient, sourceAgent
}

func recordRetainedCommentTask(t *testing.T, f principalFixture, source db.AgentTaskQueue, coalesced bool) db.AgentTaskQueue {
	t.Helper()
	ctx := context.Background()
	commentID := parseTestUUID(t, f.Comment(t, util.UUIDToString(source.IssueID), "Please continue this implementation."))
	triggerID := commentID
	coalescedIDs := []pgtype.UUID{}
	if coalesced {
		triggerID = parseTestUUID(t, f.Comment(t, util.UUIDToString(source.IssueID), "Newest comment"))
		coalescedIDs = []pgtype.UUID{commentID}
	}
	id := parseTestUUID(t, f.Task(t, util.UUIDToString(source.AgentID), testutil.Cols{
		"issue_id": source.IssueID, "runtime_id": source.RuntimeID, "status": "dispatched", "dispatched_at": testutil.Raw("now()"),
		"trigger_comment_id": triggerID, "coalesced_comment_ids": coalescedIDs,
	}))
	task, err := f.q.GetAgentTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.q.SetTaskCommentResumeSource(ctx, db.SetTaskCommentResumeSourceParams{
		TaskID: id, RuntimeID: source.RuntimeID, DispatchedAt: task.DispatchedAt,
		SourceTaskID: source.ID, SessionID: source.SessionID.String,
		ExpectedTriggerCommentID: triggerID, DeliveredCommentIds: []pgtype.UUID{commentID},
	}); err != nil {
		t.Fatalf("record exact server session source: %v", err)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now(),session_id=$2,delivered_comment_ids=$3 WHERE id=$1", id, source.SessionID, []pgtype.UUID{commentID})
	task, err = f.q.GetAgentTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestIssueHandoffRetainedCommentContinuationCanTransfer(t *testing.T) {
	for _, status := range []string{"running", "waiting_local_directory", "completed"} {
		t.Run(status, func(t *testing.T) {
			f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
			task := recordRetainedCommentTask(t, f, recipient, true)
			f.Exec(t, "UPDATE agent_task_queue SET status=$2 WHERE id=$1", task.ID, status)
			in := handoffInput(task.ID, parseTestUUID(t, nextAgent))
			if _, err := s.CreateHandoff(context.Background(), w.IssueID, parseTestUUID(t, f.UserID), task.ID, in); err != nil {
				t.Fatalf("retained ordinary comment cannot hand off: %v", err)
			}
		})
	}
}

func TestIssueHandoffRetainedCommentContinuationSupportsMultipleTurnsAndRetry(t *testing.T) {
	f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
	first := recordRetainedCommentTask(t, f, recipient, false)
	f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", first.ID)
	first.Status = "completed"
	second := recordRetainedCommentTask(t, f, first, true)
	f.Exec(t, "UPDATE agent_task_queue SET status='failed',completed_at=now() WHERE id=$1", second.ID)
	// A system retry retains the server-recorded parent chain, even when the
	// selected completed turn was several comments after the initial recipient.
	retry := parseTestUUID(t, f.Task(t, util.UUIDToString(recipient.AgentID), testutil.Cols{
		"issue_id": w.IssueID, "runtime_id": recipient.RuntimeID, "status": "running",
		"session_id": recipient.SessionID, "retry_of_task_id": second.ID,
	}))
	if _, err := s.CreateHandoff(context.Background(), w.IssueID, parseTestUUID(t, f.UserID), retry, handoffInput(retry, parseTestUUID(t, nextAgent))); err != nil {
		t.Fatalf("retained comment retry cannot hand off: %v", err)
	}
}

func TestIssueHandoffFreshFallbackRevokesBorrowedAuthorityBeforeTerminal(t *testing.T) {
	for _, lateSession := range []string{"retained-recipient-session", "fresh-provider-session"} {
		t.Run(lateSession, func(t *testing.T) {
			f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
			task := recordRetainedCommentTask(t, f, recipient, false)
			ctx := context.Background()
			if _, err := f.q.InvalidateRetainedTaskContext(ctx, db.InvalidateRetainedTaskContextParams{
				TaskID: task.ID, RuntimeID: task.RuntimeID, DispatchedAt: task.DispatchedAt,
			}); err != nil {
				t.Fatal(err)
			}
			// Even a fresh backend echoing the abandoned session identifier may
			// preserve crash state but cannot restore borrowed ancestry.
			if err := f.q.UpdateAgentTaskSession(ctx, db.UpdateAgentTaskSessionParams{ID: task.ID, SessionID: pgtype.Text{String: lateSession, Valid: true}, AfterFreshReset: true}); err != nil {
				t.Fatal(err)
			}
			stored, err := f.q.GetAgentTask(ctx, task.ID)
			if err != nil || stored.Status != "running" || !stored.RetainedContextInvalidated || stored.CommentResumeFromTaskID.Valid || stored.SessionID.String != lateSession {
				t.Fatalf("fresh transition did not persist before terminal: task=%+v err=%v", stored, err)
			}
			if _, err = s.CreateHandoff(ctx, w.IssueID, parseTestUUID(t, f.UserID), task.ID, handoffInput(task.ID, parseTestUUID(t, nextAgent))); !errors.Is(err, ErrWakeupConflict) {
				t.Fatalf("running fresh comment borrowed retained authority: %v", err)
			}
			// A later retry cannot borrow through the invalidated comment either.
			f.Exec(t, "UPDATE agent_task_queue SET status='failed',completed_at=now() WHERE id=$1", task.ID)
			retry := parseTestUUID(t, f.Task(t, util.UUIDToString(recipient.AgentID), testutil.Cols{
				"issue_id": w.IssueID, "runtime_id": recipient.RuntimeID, "status": "running",
				"session_id": recipient.SessionID, "retry_of_task_id": task.ID,
			}))
			if _, err = s.CreateHandoff(ctx, w.IssueID, parseTestUUID(t, f.UserID), retry, handoffInput(retry, parseTestUUID(t, nextAgent))); !errors.Is(err, ErrWakeupConflict) {
				t.Fatalf("retry borrowed authority through invalidated comment: %v", err)
			}
		})
	}
}

func TestIssueHandoffFreshFallbackPreservesExplicitRecipientAuthority(t *testing.T) {
	for _, edge := range []string{"direct recipient", "retry_of_task_id", "rerun_of_task_id"} {
		t.Run(edge, func(t *testing.T) {
			f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
			ctx := context.Background()
			task := recipient
			if edge != "direct recipient" {
				id := parseTestUUID(t, f.Task(t, util.UUIDToString(recipient.AgentID), testutil.Cols{
					"issue_id": w.IssueID, "runtime_id": recipient.RuntimeID, "status": "running",
					"dispatched_at": testutil.Raw("now()"), "session_id": recipient.SessionID, edge: recipient.ID,
				}))
				var err error
				task, err = f.q.GetAgentTask(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				f.Exec(t, "UPDATE agent_task_queue SET status='running',dispatched_at=now() WHERE id=$1", recipient.ID)
				var err error
				task, err = f.q.GetAgentTask(ctx, recipient.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.q.InvalidateRetainedTaskContext(ctx, db.InvalidateRetainedTaskContextParams{
				TaskID: task.ID, RuntimeID: task.RuntimeID, DispatchedAt: task.DispatchedAt,
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.q.UpdateAgentTaskSession(ctx, db.UpdateAgentTaskSessionParams{ID: task.ID, SessionID: pgtype.Text{String: "fresh-native-recipient-session", Valid: true}, AfterFreshReset: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateHandoff(ctx, w.IssueID, parseTestUUID(t, f.UserID), task.ID, handoffInput(task.ID, parseTestUUID(t, nextAgent))); err != nil {
				t.Fatalf("explicit recipient lost authority after fresh context: %v", err)
			}
		})
	}
}

func TestIssueHandoffFreshFallbackRecipientCanSeedNewRetainedContinuation(t *testing.T) {
	f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
	ctx := context.Background()
	f.Exec(t, "UPDATE agent_task_queue SET status='running',dispatched_at=now() WHERE id=$1", recipient.ID)
	var err error
	recipient, err = f.q.GetAgentTask(ctx, recipient.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.q.InvalidateRetainedTaskContext(ctx, db.InvalidateRetainedTaskContextParams{
		TaskID: recipient.ID, RuntimeID: recipient.RuntimeID, DispatchedAt: recipient.DispatchedAt,
	}); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed',session_id='fresh-owned-recipient-session',completed_at=now() WHERE id=$1", recipient.ID)
	recipient, err = f.q.GetAgentTask(ctx, recipient.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := recordRetainedCommentTask(t, f, recipient, false)
	if _, err := s.CreateHandoff(ctx, w.IssueID, parseTestUUID(t, f.UserID), task.ID, handoffInput(task.ID, parseTestUUID(t, nextAgent))); err != nil {
		t.Fatalf("fresh explicitly owned recipient could not seed retained comment: %v", err)
	}
}

func TestIssueHandoffRetainedCommentContinuationRejectsReboundRuntime(t *testing.T) {
	f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
	task := recordRetainedCommentTask(t, f, recipient, false)
	f.Exec(t, "UPDATE agent SET runtime_id=(SELECT runtime_id FROM agent WHERE id=$2) WHERE id=$1", recipient.AgentID, parseTestUUID(t, nextAgent))
	if _, err := s.CreateHandoff(context.Background(), w.IssueID, parseTestUUID(t, f.UserID), task.ID, handoffInput(task.ID, parseTestUUID(t, nextAgent))); !errors.Is(err, ErrWakeupForbidden) {
		t.Fatalf("stale runtime continuation accepted: %v", err)
	}
}

func TestIssueHandoffSourceAgentRebindCannotRaceRecordedAuthority(t *testing.T) {
	f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
	task := recordRetainedCommentTask(t, f, recipient, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rebinding, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rebinding.Rollback(context.Background())
	if _, err = rebinding.Exec(ctx, "UPDATE agent SET runtime_id=(SELECT runtime_id FROM agent WHERE id=$2) WHERE id=$1", recipient.AgentID, parseTestUUID(t, nextAgent)); err != nil {
		t.Fatal(err)
	}
	in := handoffInput(task.ID, parseTestUUID(t, nextAgent))
	if _, err = s.CreateHandoff(ctx, w.IssueID, parseTestUUID(t, f.UserID), task.ID, in); !errors.Is(err, ErrWakeupConflict) {
		t.Fatalf("source agent contention did not return retryable conflict: %v", err)
	}
	if count := f.Count(t, "SELECT count(*) FROM issue_wakeup WHERE request_key=$1", mustHandoffUUID(in.RequestKey)); count != 0 {
		t.Fatalf("contended binding recorded %d handoffs", count)
	}
	if err = rebinding.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateHandoff(ctx, w.IssueID, parseTestUUID(t, f.UserID), task.ID, in); !errors.Is(err, ErrWakeupForbidden) {
		t.Fatalf("committed rebind granted old runtime authority: %v", err)
	}
	if count := f.Count(t, "SELECT count(*) FROM issue_wakeup WHERE request_key=$1", mustHandoffUUID(in.RequestKey)); count != 0 {
		t.Fatalf("stale binding recorded %d handoffs", count)
	}
}

func TestIssueHandoffRetainedCommentCanResumeCompletedRecipientRetryOrRerun(t *testing.T) {
	for _, state := range []string{"failed", "cancelled"} {
		for _, edge := range []string{"retry_of_task_id", "rerun_of_task_id"} {
			t.Run(state+"/"+edge, func(t *testing.T) {
				f, s, w, original, nextAgent := retainedCommentHandoffFixture(t)
				f.Exec(t, "UPDATE agent_task_queue SET status=$2,session_id=NULL WHERE id=$1", original.ID, state)
				descendantID := parseTestUUID(t, f.Task(t, util.UUIDToString(original.AgentID), testutil.Cols{
					"issue_id": w.IssueID, "runtime_id": original.RuntimeID, "status": "completed", "completed_at": testutil.Raw("now()"),
					"session_id": "successful-recipient-descendant-session", "force_fresh_session": true, edge: original.ID,
				}))
				descendant, err := f.q.GetAgentTask(context.Background(), descendantID)
				if err != nil {
					t.Fatal(err)
				}
				task := recordRetainedCommentTask(t, f, descendant, true)
				if _, err = s.CreateHandoff(context.Background(), w.IssueID, parseTestUUID(t, f.UserID), task.ID, handoffInput(task.ID, parseTestUUID(t, nextAgent))); err != nil {
					t.Fatalf("completed exact recipient descendant lost retained authority: %v", err)
				}
			})
		}
	}
}

func TestIssueHandoffRetainedCommentSupportsManualRerunBetweenHumanTurns(t *testing.T) {
	f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
	first := recordRetainedCommentTask(t, f, recipient, false)
	f.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", first.ID)
	rerunID := parseTestUUID(t, f.Task(t, util.UUIDToString(recipient.AgentID), testutil.Cols{
		"issue_id": w.IssueID, "runtime_id": recipient.RuntimeID, "status": "completed", "completed_at": testutil.Raw("now()"),
		"session_id": recipient.SessionID, "force_fresh_session": true, "rerun_of_task_id": first.ID,
	}))
	rerun, err := f.q.GetAgentTask(context.Background(), rerunID)
	if err != nil {
		t.Fatal(err)
	}
	second := recordRetainedCommentTask(t, f, rerun, true)
	if _, err = s.CreateHandoff(context.Background(), w.IssueID, parseTestUUID(t, f.UserID), second.ID, handoffInput(second.ID, parseTestUUID(t, nextAgent))); err != nil {
		t.Fatalf("exact manual rerun between human turns lost retained authority: %v", err)
	}
}

func TestIssueHandoffRetainedCommentContinuationRejectsUnverifiedLineage(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, principalFixture, db.IssueWakeup, db.AgentTaskQueue, db.AgentTaskQueue, string)
	}{
		{"forged wakeup context", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE agent_task_queue SET comment_resume_from_task_id=NULL,context=$2 WHERE id=$1", task.ID, map[string]string{"wakeup_id": util.UUIDToString(w.ID), "comment_resume_from_task_id": util.UUIDToString(recipient.ID)})
		}},
		{"wrong agent", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE agent_task_queue SET agent_id=$2 WHERE id=$1", task.ID, parseTestUUID(t, other))
		}},
		{"wrong runtime", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE agent_task_queue SET runtime_id=(SELECT runtime_id FROM agent WHERE id=$2) WHERE id=$1", task.ID, parseTestUUID(t, other))
		}},
		{"fresh provider session", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE agent_task_queue SET session_id='different-provider-session' WHERE id=$1", task.ID)
		}},
		{"forced fresh", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE agent_task_queue SET force_fresh_session=true WHERE id=$1", task.ID)
		}},
		{"missing receipt", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE agent_task_queue SET delivered_comment_ids='{}' WHERE id=$1", task.ID)
		}},
		{"deleted comment", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE comment SET deleted_at=now() WHERE id=$1", task.TriggerCommentID)
		}},
		{"agent authored self mention", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE comment SET author_type='agent',author_id=$2 WHERE id=$1", task.TriggerCommentID, recipient.AgentID)
		}},
		{"failed comment resume parent", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			parent := parseTestUUID(t, f.Task(t, util.UUIDToString(recipient.AgentID), testutil.Cols{"issue_id": w.IssueID, "runtime_id": recipient.RuntimeID, "status": "failed", "session_id": recipient.SessionID, "comment_resume_from_task_id": recipient.ID}))
			f.Exec(t, "UPDATE agent_task_queue SET comment_resume_from_task_id=$2 WHERE id=$1", task.ID, parent)
		}},
		{"failed source", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE agent_task_queue SET status='failed' WHERE id=$1", recipient.ID)
		}},
		{"cancelled source", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE agent_task_queue SET status='cancelled' WHERE id=$1", recipient.ID)
		}},
		{"unrelated exact source", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			unrelated := parseTestUUID(t, f.Task(t, util.UUIDToString(recipient.AgentID), testutil.Cols{"issue_id": w.IssueID, "runtime_id": recipient.RuntimeID, "status": "completed", "session_id": recipient.SessionID}))
			f.Exec(t, "UPDATE agent_task_queue SET comment_resume_from_task_id=$2 WHERE id=$1", task.ID, unrelated)
		}},
		{"cyclic source", func(t *testing.T, f principalFixture, w db.IssueWakeup, recipient, task db.AgentTaskQueue, other string) {
			f.Exec(t, "UPDATE agent_task_queue SET status='completed',comment_resume_from_task_id=id WHERE id=$1", task.ID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
			task := recordRetainedCommentTask(t, f, recipient, false)
			tc.change(t, f, w, recipient, task, nextAgent)
			want := ErrWakeupConflict
			if tc.name == "wrong agent" || tc.name == "wrong runtime" {
				want = ErrWakeupForbidden
			}
			if _, err := s.CreateHandoff(context.Background(), w.IssueID, parseTestUUID(t, f.UserID), task.ID, handoffInput(task.ID, parseTestUUID(t, nextAgent))); !errors.Is(err, want) {
				t.Fatalf("unverified continuation accepted: %v", err)
			}
		})
	}
}

func TestIssueHandoffSupersedesQueuedGenericRecipientWithoutLosingEvidence(t *testing.T) {
	f, s, issue, sourceAgent, targetAgent := handoffFixture(t)
	ctx := context.Background()
	source := handoffSourceTask(t, f, issue, sourceAgent)
	w, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source,
		handoffInput(source, parseTestUUID(t, targetAgent)))
	if err != nil {
		t.Fatal(err)
	}
	old := parseTestUUID(t, f.Task(t, targetAgent, testutil.Cols{
		"issue_id": issue, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + targetAgent + "')"),
		"status": "queued", "trigger_summary": "Existing independent comment plan",
	}))
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", source); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, w)
	prior, err := f.q.GetAgentTask(ctx, old)
	if err != nil || prior.Status != "cancelled" || prior.TriggerSummary.String != "Existing independent comment plan" ||
		!strings.Contains(string(prior.Context), util.UUIDToString(w.ID)) {
		t.Fatalf("queued plan not visibly superseded with provenance: %+v, %v", prior, err)
	}
	stored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
	if err != nil || !stored.LastTaskID.Valid {
		t.Fatalf("handoff did not obtain released recipient slot: %+v, %v", stored, err)
	}
}

func TestIssueHandoffToMemberWaitsForSourceAndKeepsClaimFence(t *testing.T) {
	f, s, issue, sourceAgent, targetAgent := handoffFixture(t)
	ctx := context.Background()
	source := handoffSourceTask(t, f, issue, sourceAgent)
	in := handoffInput(source, parseTestUUID(t, targetAgent))
	in.AgentID = ""
	in.AssigneeType = "member"
	in.AssigneeID = f.UserID
	w, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source, in)
	if err != nil {
		t.Fatal(err)
	}
	if w.AgentID != parseTestUUID(t, sourceAgent) || w.FilterAgentID != w.AgentID {
		t.Fatalf("member handoff lost source-agent storage ownership: %+v", w)
	}
	if got := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND status='queued'", issue); got != 0 {
		t.Fatalf("member handoff queued %d task before source completion", got)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", source); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, w)
	wakeDispatch(t, s, w)
	got, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
	if err != nil || !got.HandoffCompletedAt.Valid || got.LastTaskID.Valid || got.Enabled {
		t.Fatalf("member handoff not consumed without agent enqueue: %+v, %v", got, err)
	}
	state, err := f.q.GetIssue(ctx, issue)
	if err != nil || state.Status != "in_review" || state.AssigneeType.String != "member" || state.AssigneeID != parseTestUUID(t, f.UserID) {
		t.Fatalf("member handoff owner/phase: %+v, %v", state, err)
	}
	if got := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND id<>$2", issue, source); got != 0 {
		t.Fatalf("member handoff enqueued %d agent tasks", got)
	}
	queued := parseTestUUID(t, f.Task(t, targetAgent, testutil.Cols{
		"issue_id": issue, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + targetAgent + "')"),
		"status": "queued",
	}))
	claimable, err := f.q.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{TaskID: queued, IssueID: issue})
	if err != nil || claimable {
		t.Fatalf("waiting member handoff let a generic writer claim: %v, %v", claimable, err)
	}
	continuation := parseTestUUID(t, f.Task(t, sourceAgent, testutil.Cols{
		"issue_id": issue, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + sourceAgent + "')"),
		"status": "queued", "retry_of_task_id": source,
	}))
	claimable, err = f.q.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{TaskID: continuation, IssueID: issue})
	if err != nil || claimable {
		t.Fatalf("waiting member handoff let an outgoing-source descendant claim: %v, %v", claimable, err)
	}
}

func TestIssueHandoffReviewerToMemberRetainsCandidateGeneration(t *testing.T) {
	f, s, issue, sourceAgent, reviewerAgent := handoffFixture(t)
	ctx := context.Background()
	source := handoffSourceTask(t, f, issue, sourceAgent)
	firstInput := handoffInput(source, parseTestUUID(t, reviewerAgent))
	first, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", source); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, first)
	firstState, err := f.q.GetIssue(ctx, issue)
	if err != nil || !firstState.WorkflowCandidateID.Valid {
		t.Fatalf("initial candidate was not recorded: %+v, %v", firstState, err)
	}
	firstStored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: first.ID, WorkspaceID: first.WorkspaceID})
	if err != nil || !firstStored.LastTaskID.Valid {
		t.Fatalf("reviewer task missing: %+v, %v", firstStored, err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", firstStored.LastTaskID); err != nil {
		t.Fatal(err)
	}
	secondInput := handoffInput(firstStored.LastTaskID, pgtype.UUID{})
	secondInput.AgentID, secondInput.AssigneeType, secondInput.AssigneeID = "", "member", f.UserID
	secondInput.Candidates = firstInput.Candidates
	second, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), firstStored.LastTaskID, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, second)
	finalState, err := f.q.GetIssue(ctx, issue)
	if err != nil || finalState.WorkflowCandidateID != firstState.WorkflowCandidateID || finalState.AssigneeType.String != "member" {
		t.Fatalf("reviewer-to-member handoff changed candidate or target: %+v, %v", finalState, err)
	}
	var writer pgtype.UUID
	if err = f.Pool.QueryRow(ctx, "SELECT writer_task_id FROM issue_workflow_candidate WHERE id=$1", finalState.WorkflowCandidateID).Scan(&writer); err != nil || writer != source {
		t.Fatalf("candidate writer changed across review: %s, %v", util.UUIDToString(writer), err)
	}
}

func TestIssueHandoffFailedSourceRemainsBlockedUntilExplicitCancellation(t *testing.T) {
	f, s, issue, sourceAgent, targetAgent := handoffFixture(t)
	ctx := context.Background()
	source := handoffSourceTask(t, f, issue, sourceAgent)
	w, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source, handoffInput(source, parseTestUUID(t, targetAgent)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='failed',completed_at=now(),error='source failed' WHERE id=$1", source); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, w)
	got, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
	if err != nil || got.LastTaskID.Valid || !got.LastError.Valid || !strings.Contains(got.LastError.String, "failed") {
		t.Fatalf("failed source did not hold handoff: wakeup=%+v err=%v", got, err)
	}
	issueState, err := f.q.GetIssue(ctx, issue)
	if err != nil || issueState.AssigneeID.Valid || issueState.Status == "in_review" {
		t.Fatalf("failed source changed issue: %+v, %v", issueState, err)
	}
	if _, err = s.Disable(ctx, issue, w.ID, parseTestUUID(t, f.UserID)); err != nil {
		t.Fatal(err)
	}
}

func TestIssueHandoffStartRejectsOwnerChangeAfterClaim(t *testing.T) {
	f, s, issue, sourceAgent, targetAgent := handoffFixture(t)
	ctx := context.Background()
	source := handoffSourceTask(t, f, issue, sourceAgent)
	w, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source, handoffInput(source, parseTestUUID(t, targetAgent)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", source); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, w)
	stored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='dispatched',dispatched_at=now() WHERE id=$1", stored.LastTaskID); err != nil {
		t.Fatal(err)
	}
	task, err := f.q.GetAgentTask(ctx, stored.LastTaskID)
	if err != nil {
		t.Fatal(err)
	}
	// A human transition between response preparation and Start must win.
	if _, err = f.Pool.Exec(ctx, "UPDATE issue SET assignee_id=$2 WHERE id=$1", issue, parseTestUUID(t, sourceAgent)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Tasks.StartTaskForClaim(ctx, db.LockAgentTaskStartClaimParams{ID: task.ID, RuntimeID: task.RuntimeID, DispatchedAt: task.DispatchedAt}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("generation-aware start after reassignment = %v, want no rows", err)
	}
	if _, err = s.Tasks.StartTask(ctx, task.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("legacy start after reassignment = %v, want no rows", err)
	}
	unchanged, err := f.q.GetAgentTask(ctx, task.ID)
	if err != nil || unchanged.Status != "dispatched" || unchanged.StartedAt.Valid {
		t.Fatalf("stale start changed task: %+v, %v", unchanged, err)
	}
}

func TestIssueHandoffFailedRecipientAutonomouslyRecoversAndHandsOff(t *testing.T) {
	f, s, issue, coordinatorAgent, reviewerAgent := handoffFixture(t)
	ctx := context.Background()
	coordinator := handoffSourceTask(t, f, issue, coordinatorAgent)
	first, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), coordinator,
		handoffInput(coordinator, parseTestUUID(t, reviewerAgent)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now(),session_id='coordinator-exact-session' WHERE id=$1", coordinator); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, first)
	stored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: first.ID, WorkspaceID: first.WorkspaceID})
	if err != nil || !stored.LastTaskID.Valid {
		t.Fatalf("recipient not queued: %+v, %v", stored, err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1", stored.LastTaskID); err != nil {
		t.Fatal(err)
	}
	oldCoordinator := parseTestUUID(t, f.Task(t, coordinatorAgent, testutil.Cols{
		"issue_id": issue, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + coordinatorAgent + "')"),
		"status": "queued", "trigger_summary": "Earlier coordinator comment plan",
	}))
	failed, err := s.Tasks.FailTask(ctx, stored.LastTaskID, "reviewer exhausted", "", "", "", "agent_error.process_failure", false, "", "")
	if err != nil || failed.Status != "failed" {
		t.Fatalf("terminal reviewer failure: %+v, %v", failed, err)
	}
	var recoveryID pgtype.UUID
	if err = f.Pool.QueryRow(ctx, `SELECT id FROM agent_task_queue
		WHERE issue_id=$1 AND trigger_evidence_kind='delegated_failure' AND trigger_evidence_ref_id=$2`, issue, failed.ID).Scan(&recoveryID); err != nil {
		t.Fatalf("automatic recovery was not queued: %v", err)
	}
	recovery, err := f.q.GetAgentTask(ctx, recoveryID)
	if err != nil || recovery.AgentID != parseTestUUID(t, coordinatorAgent) || recovery.IssueID != issue || !recovery.ForceFreshSession {
		t.Fatalf("recovery target: %+v, %v", recovery, err)
	}
	prior, err := f.q.GetAgentTask(ctx, oldCoordinator)
	if err != nil || prior.Status != "cancelled" || prior.TriggerSummary.String != "Earlier coordinator comment plan" ||
		!strings.Contains(string(prior.Context), util.UUIDToString(recovery.TriggerCommentID)) {
		t.Fatalf("queued coordinator plan not visibly superseded: %+v, %v", prior, err)
	}
	if strings.Contains(string(recovery.Context), "head_sha") || !strings.Contains(string(recovery.Context), util.UUIDToString(coordinator)) {
		t.Fatalf("recovery lost exact source or inherited review candidate: %s", recovery.Context)
	}
	before, err := f.q.GetIssue(ctx, issue)
	if err != nil || before.AssigneeID != parseTestUUID(t, reviewerAgent) || before.Status != "in_review" {
		t.Fatalf("recovery changed human phase/owner: %+v, %v", before, err)
	}
	resume, err := s.Tasks.ValidateWorkflowRecoverySource(ctx, recovery)
	if err != nil || resume.ID != coordinator {
		t.Fatalf("recovery source = %+v, %v; want exact coordinator %s", resume, err, util.UUIDToString(coordinator))
	}
	claimable, err := f.q.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{TaskID: recovery.ID, IssueID: issue})
	if err != nil || !claimable {
		t.Fatalf("native recovery blocked by latest handoff: %v, %v", claimable, err)
	}
	claimed, err := s.Tasks.ClaimTask(ctx, parseTestUUID(t, coordinatorAgent))
	if err != nil || claimed == nil || claimed.ID != recovery.ID {
		t.Fatalf("automatic recovery claim: %+v, %v", claimed, err)
	}
	if _, err = s.Tasks.StartTaskForClaim(ctx, db.LockAgentTaskStartClaimParams{ID: claimed.ID, RuntimeID: claimed.RuntimeID, DispatchedAt: claimed.DispatchedAt}); err != nil {
		t.Fatalf("automatic recovery start: %v", err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now(),session_id='recovery-exact-session' WHERE id=$1", recovery.ID); err != nil {
		t.Fatal(err)
	}
	next := handoffInput(recovery.ID, parseTestUUID(t, reviewerAgent))
	next.Candidates = nil
	next.EvidenceURLs = nil
	next.Instruction = "Review the corrected candidate after coordinator recovery."
	second, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), recovery.ID, next)
	if err != nil {
		t.Fatalf("coordinator recovery could not hand off without human relay: %v", err)
	}
	wakeDispatch(t, s, second)
	if got := f.Count(t, "SELECT count(*) FROM agent_task_queue WHERE context->>'wakeup_id'=$1", util.UUIDToString(second.ID)); got != 1 {
		t.Fatalf("recovered coordinator queued %d next recipients, want one", got)
	}
}

func TestIssueHandoffSameAgentFailureOutboxReplaysAfterDispatchGap(t *testing.T) {
	f, s, issue, coordinatorAgent, _ := handoffFixture(t)
	ctx := context.Background()
	source := handoffSourceTask(t, f, issue, coordinatorAgent)
	w, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), source,
		handoffInput(source, parseTestUUID(t, coordinatorAgent)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", source); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, w)
	stored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
	if err != nil || !stored.LastTaskID.Valid {
		t.Fatalf("self-review recipient not queued: %+v, %v", stored, err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='failed',completed_at=now(),error='failed review' WHERE id=$1", stored.LastTaskID); err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.Tasks.ensureDelegatedFailureRecoveryComment(ctx, stored.LastTaskID); err != nil || !created {
		t.Fatalf("durable same-agent recovery signal: created=%v err=%v", created, err)
	}
	result, err := s.Tasks.RecoverPendingDelegatedFailures(ctx, 5)
	if err != nil || result.Replayed != 1 {
		t.Fatalf("same-agent durable outbox replay: %+v, %v", result, err)
	}
	var recoveryID pgtype.UUID
	if err = f.Pool.QueryRow(ctx, `SELECT id FROM agent_task_queue
		WHERE issue_id=$1 AND trigger_evidence_kind='delegated_failure' AND trigger_evidence_ref_id=$2`,
		issue, stored.LastTaskID).Scan(&recoveryID); err != nil {
		t.Fatalf("same-agent recovery task missing: %v", err)
	}
	recovery, err := f.q.GetAgentTask(ctx, recoveryID)
	if err != nil || !recovery.ForceFreshSession {
		t.Fatalf("same-agent recovery lost safe fresh fallback: %+v, %v", recovery, err)
	}
	resume, err := s.Tasks.ValidateWorkflowRecoverySource(ctx, recovery)
	if err != nil || resume.ID != source {
		t.Fatalf("same-agent recovery did not retain exact source: %+v, %v", resume, err)
	}
}

func TestWorkflowClaimUsesTerminalCreatedAtAcrossV4AndV7Lineage(t *testing.T) {
	f, s, issue, coordinatorAgent, reviewerAgent := handoffFixture(t)
	ctx := context.Background()
	coordinator := handoffSourceTask(t, f, issue, coordinatorAgent)
	w, err := s.CreateHandoff(ctx, issue, parseTestUUID(t, f.UserID), coordinator,
		handoffInput(coordinator, parseTestUUID(t, reviewerAgent)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", coordinator); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, w)
	stored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	legacyRoot := parseTestUUID(t, "f0000000-0000-4000-8000-000000000001")
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET id=$2,status='failed',completed_at=now(),created_at=now()-interval '1 hour' WHERE id=$1", stored.LastTaskID, legacyRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE issue_wakeup SET last_task_id=$2 WHERE id=$1", w.ID, legacyRoot); err != nil {
		t.Fatal(err)
	}
	retryID := dbid.NewV7()
	if _, err = f.Pool.Exec(ctx, `INSERT INTO agent_task_queue(id,agent_id,runtime_id,issue_id,status,priority,retry_of_task_id,created_at,completed_at)
		SELECT $1,id,runtime_id,$2,'completed',0,$3,now(),now() FROM agent WHERE id=$4`, retryID, issue, legacyRoot, parseTestUUID(t, reviewerAgent)); err != nil {
		t.Fatal(err)
	}
	candidate := f.Task(t, coordinatorAgent, testutil.Cols{"issue_id": issue, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + coordinatorAgent + "')"), "status": "queued"})
	allowed, err := f.q.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{TaskID: parseTestUUID(t, candidate), IssueID: issue})
	if err != nil || !allowed {
		t.Fatalf("newer completed v7 retry did not outrank older failed v4 root: %v, %v", allowed, err)
	}
}

func TestWorkflowAcceptanceRequestFencesEnrolledClaim(t *testing.T) {
	f, s, issue, sourceAgent, _ := handoffFixture(t)
	ctx := context.Background()
	queued := parseTestUUID(t, f.Task(t, sourceAgent, testutil.Cols{
		"issue_id": issue, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + sourceAgent + "')"),
		"status": "queued",
	}))
	claimable, err := f.q.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{TaskID: queued, IssueID: issue})
	if err != nil || !claimable {
		t.Fatalf("ordinary enrolled task should initially claim: %v, %v", claimable, err)
	}
	candidate := dbid.NewV7()
	if _, err = f.Pool.Exec(ctx, "UPDATE issue SET workflow_candidate_id=$2 WHERE id=$1", issue, candidate); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, `INSERT INTO issue_workflow_acceptance
		(id,workspace_id,issue_id,candidate_id,mode,actor_type,actor_id,state,policy_version,authority_snapshot)
		VALUES($1,$2,$3,$4,'human','member',$5,'requested','test-policy','{}'::jsonb)`,
		dbid.NewV7(), parseTestUUID(t, f.WorkspaceID), issue, candidate, parseTestUUID(t, f.UserID)); err != nil {
		t.Fatal(err)
	}
	claimable, err = f.q.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{TaskID: queued, IssueID: issue})
	if err != nil || claimable {
		t.Fatalf("requested acceptance allowed an enrolled claim: %v, %v", claimable, err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='dispatched',dispatched_at=now() WHERE id=$1", queued); err != nil {
		t.Fatal(err)
	}
	prepared, err := f.q.GetAgentTask(ctx, queued)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Tasks.StartTaskForClaim(ctx, db.LockAgentTaskStartClaimParams{
		ID: prepared.ID, RuntimeID: prepared.RuntimeID, DispatchedAt: prepared.DispatchedAt,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("start after acceptance request = %v, want no rows", err)
	}
	if _, err = f.Pool.Exec(ctx, `UPDATE issue_workflow_acceptance
		SET state='accepted', issue_revision=(SELECT revision FROM issue WHERE id=$1), accepted_at=now()
		WHERE issue_id=$1`, issue); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE issue SET status='done' WHERE id=$1", issue); err != nil {
		t.Fatal(err)
	}
	claimable, err = f.q.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{TaskID: queued, IssueID: issue})
	if err != nil || claimable {
		t.Fatalf("closed enrolled issue allowed a claim: %v, %v", claimable, err)
	}
}

func TestWorkflowClaimLockKeepsLegacyParallelAgentsExecutable(t *testing.T) {
	f, s, issue, firstAgent := wakeFixture(t)
	secondAgent := f.privateAgentOwnedBy(t, f.UserID, "legacy-parallel-agent")
	for _, agent := range []string{firstAgent, secondAgent} {
		f.Task(t, agent, testutil.Cols{
			"issue_id": issue, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + agent + "')"),
			"status": "queued",
		})
	}
	first, err := s.Tasks.ClaimTask(context.Background(), parseTestUUID(t, firstAgent))
	if err != nil || first == nil {
		t.Fatalf("first legacy task claim: %+v, %v", first, err)
	}
	second, err := s.Tasks.ClaimTask(context.Background(), parseTestUUID(t, secondAgent))
	if err != nil || second == nil || second.ID == first.ID {
		t.Fatalf("second legacy task claim after issue-lock release: %+v, %v", second, err)
	}
}

func TestIssueHandoffInputRejectsAmbiguousOrUnsafeCandidate(t *testing.T) {
	in := handoffInput(dbid.NewV7(), dbid.NewV7())
	cases := []struct {
		name   string
		change func(*HandoffInput)
	}{
		{"duplicate PR", func(v *HandoffInput) { v.Candidates = append(v.Candidates, v.Candidates[0]) }},
		{"credentialed repository", func(v *HandoffInput) { v.Candidates[0].RepositoryURL = "https://user:secret@example.test/repo" }},
		{"short SHA", func(v *HandoffInput) { v.Candidates[0].CommitSHA = "abc123" }},
		{"non-draft", func(v *HandoffInput) { v.Candidates[0].Draft = false }},
		{"branch control", func(v *HandoffInput) { v.Candidates[0].Branch = "branch\nignore" }},
		{"URL control", func(v *HandoffInput) { v.EvidenceURLs[0] = "https://example.test/a\nb" }},
		{"member with agent alias", func(v *HandoffInput) { v.AssigneeType, v.AssigneeID = "member", util.UUIDToString(dbid.NewV7()) }},
		{"member resume", func(v *HandoffInput) {
			v.AssigneeType, v.AssigneeID, v.AgentID = "member", util.UUIDToString(dbid.NewV7()), ""
			v.ContextMode, v.ResumeTaskID = "resume", util.UUIDToString(dbid.NewV7())
		}},
		{"member wrong phase", func(v *HandoffInput) {
			v.AssigneeType, v.AssigneeID, v.AgentID = "member", util.UUIDToString(dbid.NewV7()), ""
			v.Status = "in_progress"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copy := in
			copy.Candidates = append([]HandoffCandidate(nil), in.Candidates...)
			copy.EvidenceURLs = append([]string(nil), in.EvidenceURLs...)
			tc.change(&copy)
			if _, err := normalizeHandoffInput(copy); !errors.Is(err, ErrWakeupInput) {
				t.Fatalf("expected invalid handoff, got %v", err)
			}
		})
	}
}
