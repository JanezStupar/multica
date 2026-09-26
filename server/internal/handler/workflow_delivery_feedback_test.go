package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type workflowDeliveryFeedbackFixture struct {
	issueID, candidateID, acceptanceID, deliveryID                       string
	runtimeID, coordinatorID, coordinatorTaskID, writerTaskID, handoffID string
}

func workflowDeliveryFeedbackCandidate(t *testing.T, server *httptest.Server, ready bool, outcomeComplete ...bool) workflowDeliveryFeedbackFixture {
	t.Helper()
	complete := true
	if len(outcomeComplete) > 0 {
		complete = outcomeComplete[0]
	}
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, complete)
	writerID, runtimeID := workflowOutcomeAgent(t, acceptanceID)
	writerTaskID := dbfx.Task(t, writerID, testutil.Cols{"issue_id": issueID, "runtime_id": runtimeID,
		"status": "dispatched", "dispatched_at": testutil.Raw("now()"),
		"session_id": "retained-delivery-writer", "work_dir": "/tmp/retained-delivery-writer",
		"originator_user_id": testUserID, "accountable_user_id": testUserID})
	bindWorkflowDeliveryFeedbackSourceProfile(t, writerTaskID, runtimeID)
	coordinatorID := dbfx.Agent(t, "Delivery feedback coordinator", runtimeID)
	coordinatorTaskID := dbfx.Task(t, coordinatorID, testutil.Cols{"issue_id": issueID, "runtime_id": runtimeID,
		"status": "dispatched", "dispatched_at": testutil.Raw("now()"), "session_id": "delivery-coordinator",
		"originator_user_id": testUserID, "accountable_user_id": testUserID})
	bindWorkflowDeliveryFeedbackSourceProfile(t, coordinatorTaskID, runtimeID)
	dbfx.Exec(t, `UPDATE issue_workflow_candidate SET writer_task_id=$2 WHERE id=$1`, candidateID, writerTaskID)
	dbfx.Exec(t, `UPDATE issue SET assignee_type='member',assignee_id=$2 WHERE id=$1`, issueID, testUserID)
	handoffID := dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(),
		"workspace_id": testWorkspaceID, "issue_id": issueID, "agent_id": coordinatorID,
		"created_by": testUserID, "source_task_id": coordinatorTaskID, "filter_task_id": coordinatorTaskID,
		"filter_agent_id": coordinatorID, "instruction": "Present the exact accepted candidate to the human",
		"kind": "event", "mode": "once", "enabled": true, "handoff_completed_at": testutil.Raw("now()"),
		"handoff": testutil.Raw(`jsonb_build_object('outgoing_task_id','` + coordinatorTaskID + `','assignee_type','member','assignee_id','` + testUserID + `')`)})
	if ready {
		dbfx.Exec(t, `UPDATE issue_workflow_delivery SET readiness_done_at=now() WHERE id=$1`, ids[0])
	}
	return workflowDeliveryFeedbackFixture{issueID, candidateID, acceptanceID, ids[0], runtimeID,
		coordinatorID, coordinatorTaskID, writerTaskID, handoffID}
}

func bindWorkflowDeliveryFeedbackSourceProfile(t *testing.T, taskID, runtimeID string) {
	t.Helper()
	ctx := context.Background()
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID: parseUUID(runtimeID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	profile, profileID, err := testHandler.bindClaimIssueWorkflowProfile(ctx, task, runtime)
	if err != nil || profile == nil || !profileID.Valid {
		t.Fatalf("bind completed delivery source's pinned execution profile: profile=%v id=%v error=%v", profile, profileID, err)
	}
	dbfx.Cleanup(t, `DELETE FROM issue_workflow_profile WHERE id=$1`, profileID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',started_at=dispatched_at,completed_at=now() WHERE id=$1`, taskID)
}

func queueWorkflowDeliveryFeedback(t *testing.T, f workflowDeliveryFeedbackFixture, content string) (string, string) {
	t.Helper()
	commentID, taskID := queuePromisedWorkflowDeliveryFeedback(t, f, false, content)
	// Model a historical task without a durable source promise. New accepted
	// conversations must first pass the real atomic recording/enqueue guard.
	dbfx.Exec(t, `UPDATE agent_task_queue SET coalesced_comment_ids=array_remove(coalesced_comment_ids,$2::uuid)
		WHERE id=$1`, f.coordinatorTaskID, commentID)
	return commentID, taskID
}

func recordPromisedWorkflowDeliveryFeedback(t *testing.T, f workflowDeliveryFeedbackFixture, assigned bool, content string) (db.Issue, db.CreateCommentRow) {
	t.Helper()
	ctx := context.Background()
	if assigned {
		dbfx.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2 WHERE id=$1`, f.issueID, f.coordinatorID)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM issue WHERE id=$1 FOR UPDATE`, f.issueID); err != nil {
		t.Fatal(err)
	}
	q := testHandler.Queries.WithTx(tx)
	issue, err := q.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	comment, err := q.CreateComment(ctx, db.CreateCommentParams{ID: dbid.NewV7(), IssueID: issue.ID,
		WorkspaceID: issue.WorkspaceID, AuthorType: "member", AuthorID: parseUUID(testUserID),
		Type: "comment", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	var matched bool
	if assigned {
		matched, err = service.RecordAcceptedAssignedComment(ctx, tx, issue, parseUUID(f.coordinatorID), comment.ID)
	} else {
		matched, err = service.RecordWorkflowHumanComment(ctx, tx, issue, parseUUID(f.coordinatorID),
			parseUUID(f.handoffID), parseUUID(f.coordinatorTaskID), parseUUID(f.candidateID), comment.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("exact human input proof was not recorded")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return issue, comment
}

func queuePromisedWorkflowDeliveryFeedback(t *testing.T, f workflowDeliveryFeedbackFixture, assigned bool, content string) (string, string) {
	t.Helper()
	ctx := context.Background()
	issue, comment := recordPromisedWorkflowDeliveryFeedback(t, f, assigned, content)
	var task db.AgentTaskQueue
	var err error
	if assigned {
		task, err = testHandler.TaskService.EnqueueTaskForIssue(ctx, issue, comment.ID)
	} else {
		task, _, err = testHandler.TaskService.EnqueueWorkflowHumanComment(ctx, issue, parseUUID(f.coordinatorID),
			parseUUID(f.handoffID), parseUUID(f.coordinatorTaskID), parseUUID(f.candidateID), comment.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	return uuidToString(comment.ID), uuidToString(task.ID)
}

func feedbackDeliveryProvider(t *testing.T, mutations *atomic.Int32, externallyMerged *atomic.Bool, ready bool) *httptest.Server {
	t.Helper()
	var providerMu sync.Mutex
	title := "WIP: Candidate awaiting delivery"
	if ready {
		title = "Candidate awaiting delivery"
	}
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerMu.Lock()
		defer providerMu.Unlock()
		switch r.Method {
		case http.MethodPatch:
			mutations.Add(1)
			title = "Candidate awaiting delivery"
		case http.MethodPost:
			mutations.Add(1)
			externallyMerged.Store(true)
		case http.MethodGet:
		default:
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		state := "open"
		if externallyMerged.Load() {
			state = "closed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": title,
			"head": map[string]string{"sha": workflowDeliveryHead}, "draft": false,
			"merged": externallyMerged.Load(), "state": state, "merge_commit_sha": workflowDeliveryHead,
			"merged_by": map[string]any{"id": 5, "login": "Multica"}})
	}))
}

func TestWorkflowDeliveryClassifiesHumanFeedbackBeforeProviderMutations(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		for _, ready := range []bool{false, true} {
			for _, correction := range []bool{false, true} {
				name := "prepare/question"
				if ready {
					name = "merge/question"
				}
				if correction {
					name += "/correction"
				}
				if assigned {
					name += "/assigned_agent"
				}
				t.Run(name, func(t *testing.T) {
					if testHandler == nil {
						t.Skip("handler test fixture unavailable")
					}
					var mutations atomic.Int32
					var merged atomic.Bool
					server := feedbackDeliveryProvider(t, &mutations, &merged, ready)
					defer server.Close()
					f := workflowDeliveryFeedbackCandidate(t, server, ready, !correction)
					content := "Can you explain the accepted candidate before delivery?"
					if correction {
						content = "Please fix the missing regression before delivery."
					}
					commentID, taskID := queuePromisedWorkflowDeliveryFeedback(t, f, assigned, content)
					worker := NewWorkflowDeliveryWorker(testHandler)
					worker.client = server.Client()
					if worked, err := worker.ProcessNext(context.Background()); err != nil || !worked {
						t.Fatalf("queued feedback pause worked=%v error=%v", worked, err)
					}
					if mutations.Load() != 0 {
						t.Fatalf("queued human input permitted %d provider mutations", mutations.Load())
					}
					if status, attempts := deliveryStatus(t, f.deliveryID); status != "pending" || attempts != 0 {
						t.Fatalf("feedback changed delivery authority: status=%s attempts=%d", status, attempts)
					}
					claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
					if claim.ID != taskID {
						t.Fatalf("feedback claim=%s, want %s", claim.ID, taskID)
					}
					if _, err := testHandler.TaskService.StartTask(context.Background(), parseUUID(taskID)); err != nil {
						t.Fatal(err)
					}
					dbfx.Exec(t, `UPDATE issue_workflow_delivery SET next_attempt_at=now() WHERE id=$1`, f.deliveryID)
					if worked, err := worker.ProcessNext(context.Background()); err != nil || !worked || mutations.Load() != 0 {
						t.Fatalf("running feedback pause worked=%v error=%v mutations=%d", worked, err, mutations.Load())
					}
					if correction {
						issue, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(f.issueID))
						if err != nil {
							t.Fatal(err)
						}
						if err := testHandler.workflowAuthorityService().ContinueWorkflowFeedback(context.Background(),
							issue.WorkspaceID, issue.ID, service.WorkflowActor{Type: "agent", ID: f.coordinatorID, SourceTaskID: taskID},
							service.WorkflowFeedbackContinuationInput{CandidateID: f.candidateID, ExpectedRevision: issue.Revision,
								CommentID: commentID, Kind: "in_scope_defect"}); err != nil {
							t.Fatal(err)
						}
						if status, _ := deliveryStatus(t, f.deliveryID); status != "cancelled" {
							t.Fatalf("correction delivery=%s", status)
						}
						if count := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1
						AND rerun_of_task_id=$2 AND status='queued'`, f.issueID, f.writerTaskID); count != 1 {
							t.Fatalf("retained correction writers=%d", count)
						}
					} else {
						dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
						dbfx.Exec(t, `UPDATE issue_workflow_delivery SET next_attempt_at=now() WHERE id=$1`, f.deliveryID)
						if worked, err := worker.ProcessNext(context.Background()); err != nil || !worked || mutations.Load() == 0 {
							t.Fatalf("answered question did not resume delivery: worked=%v error=%v mutations=%d", worked, err, mutations.Load())
						}
						var acceptance string
						dbfx.QueryRow(t, `SELECT state FROM issue_workflow_acceptance WHERE id=$1`, f.acceptanceID).Scan(&acceptance)
						if acceptance != "accepted" {
							t.Fatalf("question acceptance=%s", acceptance)
						}
					}
				})
			}
		}
	}
}

func TestWorkflowDeliveryStillRecordsExternalMergeWithPendingFeedback(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		name := "member_handoff"
		if assigned {
			name = "assigned_agent"
		}
		t.Run(name, func(t *testing.T) {
			if testHandler == nil {
				t.Skip("handler test fixture unavailable")
			}
			ctx := context.Background()
			var mutations atomic.Int32
			var merged atomic.Bool
			merged.Store(true)
			server := feedbackDeliveryProvider(t, &mutations, &merged, true)
			defer server.Close()
			f := workflowDeliveryFeedbackCandidate(t, server, true, false)
			commentID, taskID := queuePromisedWorkflowDeliveryFeedback(t, f, assigned, "Explain this candidate's delivery to me.")
			worker := NewWorkflowDeliveryWorker(testHandler)
			worker.client = server.Client()
			if worked, err := worker.ProcessNext(ctx); err != nil || !worked {
				t.Fatalf("observe external merge worked=%v error=%v", worked, err)
			}
			if count := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE id=$1 AND status='delivered' AND merged_at IS NOT NULL`, f.deliveryID); count != 1 {
				t.Fatal("pending input hid the externally merged provider fact")
			}
			if mutations.Load() != 0 {
				t.Fatalf("external merge observation made %d mutations", mutations.Load())
			}
			issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
			if err != nil {
				t.Fatal(err)
			}
			if issue.Status != "pr_ready" || (!assigned && (issue.AssigneeType.String != "member" || issue.AssigneeID != parseUUID(testUserID))) || (assigned && (issue.AssigneeType.String != "agent" || issue.AssigneeID != parseUUID(f.coordinatorID))) {
				t.Fatalf("external merge reassigned the unanswered conversation: status=%s type=%s assignee=%s", issue.Status, issue.AssigneeType.String, uuidToString(issue.AssigneeID))
			}
			claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
			if claim.ID != taskID || claim.AgentID != f.coordinatorID {
				t.Fatalf("promised postmerge question stranded: %+v", claim)
			}
			if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(taskID)); err != nil {
				t.Fatal(err)
			}
			if err := testHandler.workflowAuthorityService().ContinueWorkflowFeedback(ctx, issue.WorkspaceID, issue.ID, service.WorkflowActor{Type: "agent", ID: f.coordinatorID, SourceTaskID: taskID}, service.WorkflowFeedbackContinuationInput{CandidateID: f.candidateID, ExpectedRevision: issue.Revision, CommentID: commentID, Kind: "in_scope_defect"}); !errors.Is(err, service.ErrWorkflowAuthorityConflict) {
				t.Fatalf("postmerge answer reopened merged work: %v", err)
			}
			dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
			dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_next_attempt_at=now() WHERE id=$1`, f.acceptanceID)
			if processed, err := testHandler.workflowAuthorityService().RetryNextWorkflowCompletionDispatch(ctx); err != nil || !processed {
				t.Fatalf("answered input did not resume outcome dispatch: processed=%v error=%v", processed, err)
			}
			outcomeAgent, _ := workflowOutcomeAgent(t, f.acceptanceID)
			issue, err = testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
			if err != nil || issue.AssigneeType.String != "agent" || issue.AssigneeID != parseUUID(outcomeAgent) || outcomeAgent == f.coordinatorID {
				t.Fatalf("answered input did not reach distinct outcome owner: issue=%+v outcome=%s error=%v", issue, outcomeAgent, err)
			}
		})
	}
}

func TestWorkflowDeliveryWaitsBetweenCommentCommitAndCoordinatorEnqueue(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		name := "member_handoff"
		if assigned {
			name = "assigned_agent"
		}
		t.Run(name, func(t *testing.T) {
			if testHandler == nil {
				t.Skip("handler test fixture unavailable")
			}
			ctx := context.Background()
			var mutations atomic.Int32
			var merged atomic.Bool
			server := feedbackDeliveryProvider(t, &mutations, &merged, true)
			defer server.Close()
			f := workflowDeliveryFeedbackCandidate(t, server, true)
			issue, comment := recordPromisedWorkflowDeliveryFeedback(t, f, assigned, "Please explain the candidate before its delivery.")
			worker := NewWorkflowDeliveryWorker(testHandler)
			worker.client = server.Client()
			if worked, err := worker.ProcessNext(ctx); err != nil || !worked || mutations.Load() != 0 {
				t.Fatalf("saved input lost commit-to-enqueue fence: worked=%v error=%v mutations=%d", worked, err, mutations.Load())
			}
			var task db.AgentTaskQueue
			var err error
			if assigned {
				task, err = testHandler.TaskService.EnqueueTaskForIssue(ctx, issue, comment.ID)
			} else {
				task, _, err = testHandler.TaskService.EnqueueWorkflowHumanComment(ctx, issue, parseUUID(f.coordinatorID),
					parseUUID(f.handoffID), parseUUID(f.coordinatorTaskID), parseUUID(f.candidateID), comment.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
			if claim.ID != uuidToString(task.ID) {
				t.Fatalf("saved input claim=%s want %s", claim.ID, uuidToString(task.ID))
			}
			if _, err := testHandler.TaskService.StartTask(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, task.ID)
			dbfx.Exec(t, `UPDATE issue_workflow_delivery SET next_attempt_at=now() WHERE id=$1`, f.deliveryID)
			if worked, err := worker.ProcessNext(ctx); err != nil || !worked || mutations.Load() == 0 {
				t.Fatalf("answered saved input did not release delivery: worked=%v error=%v mutations=%d", worked, err, mutations.Load())
			}
		})
	}
}

func TestWorkflowDeliveryKeepsPromisedFeedbackAfterConversationFailure(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		for _, status := range []string{"failed", "cancelled"} {
			name := "member_handoff/" + status
			if assigned {
				name = "assigned_agent/" + status
			}
			t.Run(name, func(t *testing.T) {
				if testHandler == nil {
					t.Skip("handler test fixture unavailable")
				}
				ctx := context.Background()
				var mutations atomic.Int32
				var merged atomic.Bool
				server := feedbackDeliveryProvider(t, &mutations, &merged, true)
				defer server.Close()
				f := workflowDeliveryFeedbackCandidate(t, server, true)
				commentID, taskID := queuePromisedWorkflowDeliveryFeedback(t, f, assigned, "Please fix the missing regression before delivery.")
				claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
				if claim.ID != taskID {
					t.Fatalf("promised input claim=%s want=%s", claim.ID, taskID)
				}
				if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(taskID)); err != nil {
					t.Fatal(err)
				}
				dbfx.Exec(t, `UPDATE agent_task_queue SET status=$2,completed_at=now() WHERE id=$1`, taskID, status)
				// A completed receipt from before the latest input version cannot
				// discharge the recorded promise after its classifier failed.
				dbfx.Task(t, f.coordinatorID, testutil.Cols{"issue_id": f.issueID, "runtime_id": f.runtimeID,
					"status": "completed", "completed_at": testutil.Raw("now()"), "dispatched_at": testutil.Raw("now()-interval '1 hour'"),
					"delivered_comment_ids": testutil.Raw("ARRAY['" + commentID + "']::uuid[]"),
					"originator_user_id":    testUserID, "accountable_user_id": testUserID})
				worker := NewWorkflowDeliveryWorker(testHandler)
				worker.client = server.Client()
				if worked, err := worker.ProcessNext(ctx); err != nil || !worked || mutations.Load() != 0 {
					t.Fatalf("promised %s input lost delivery pause: worked=%v error=%v mutations=%d", status, worked, err, mutations.Load())
				}
				var blocker string
				var acceptance string
				dbfx.QueryRow(t, `SELECT d.last_error_class,a.state FROM issue_workflow_delivery d
					JOIN issue_workflow_acceptance a ON a.id=d.acceptance_id WHERE d.id=$1`, f.deliveryID).Scan(&blocker, &acceptance)
				if blocker != "human_feedback_pending" || acceptance != "accepted" {
					t.Fatalf("promised input blocker=%s acceptance=%s", blocker, acceptance)
				}
				// Withdrawal clears only this exact live obligation; task status
				// does not imply the human's feedback has been addressed.
				dbfx.Exec(t, `UPDATE comment SET deleted_at=now() WHERE id=$1`, commentID)
				dbfx.Exec(t, `UPDATE issue_workflow_delivery SET next_attempt_at=now() WHERE id=$1`, f.deliveryID)
				if worked, err := worker.ProcessNext(ctx); err != nil || !worked || mutations.Load() == 0 {
					t.Fatalf("withdrawn input did not resume delivery: worked=%v error=%v mutations=%d", worked, err, mutations.Load())
				}
			})
		}
	}
}

func TestWorkflowDeliveryKeepsIssueLockedThroughProviderMutation(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	providerStarted := make(chan struct{}, 1)
	releaseProvider := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title := "WIP: Candidate awaiting delivery"
		if r.Method == http.MethodPatch {
			providerStarted <- struct{}{}
			select {
			case <-releaseProvider:
			case <-r.Context().Done():
				return
			}
			title = "Candidate awaiting delivery"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": title,
			"head": map[string]string{"sha": workflowDeliveryHead}, "draft": false, "merged": false, "state": "open"})
	}))
	defer server.Close()
	// Register after server.Close so every failing path releases its handler.
	defer releaseOnce.Do(func() { close(releaseProvider) })
	f := workflowDeliveryFeedbackCandidate(t, server, false)
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	finished := make(chan error, 1)
	go func() {
		worked, err := worker.ProcessNext(ctx)
		if err == nil && !worked {
			err = errors.New("delivery did not select the provider mutation")
		}
		finished <- err
	}()
	select {
	case <-providerStarted:
	case err := <-finished:
		t.Fatalf("delivery ended before the provider mutation: %v", err)
	case <-ctx.Done():
		t.Fatal("provider mutation did not start")
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `SELECT id FROM issue WHERE id=$1 FOR UPDATE NOWAIT`, f.issueID)
	var lockErr *pgconn.PgError
	if !errors.As(err, &lockErr) || lockErr.Code != "55P03" {
		t.Fatalf("comment writer could acquire issue during provider mutation: %v", err)
	}
	releaseOnce.Do(func() { close(releaseProvider) })
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("provider mutation did not release worker")
	}
}

func TestWorkflowDeliveryFeedbackPauseRequiresCurrentLiveInput(t *testing.T) {
	for _, change := range []string{"generic_task", "old_candidate", "old_comment", "deleted_comment", "note", "agent_comment", "wrong_author", "completed", "failed", "cancelled", "deferred", "dispatched", "waiting_local_directory", "live_coalesced"} {
		t.Run(change, func(t *testing.T) {
			if testHandler == nil {
				t.Skip("handler test fixture unavailable")
			}
			var mutations atomic.Int32
			var merged atomic.Bool
			server := feedbackDeliveryProvider(t, &mutations, &merged, false)
			defer server.Close()
			f := workflowDeliveryFeedbackCandidate(t, server, false)
			var commentID, taskID string
			switch change {
			case "deferred", "dispatched", "waiting_local_directory", "live_coalesced":
				commentID, taskID = queuePromisedWorkflowDeliveryFeedback(t, f, false, "Please explain this exact candidate.")
			default:
				commentID, taskID = queueWorkflowDeliveryFeedback(t, f, "Please explain this exact candidate.")
			}
			pause := false
			switch change {
			case "generic_task":
				dbfx.Exec(t, `UPDATE agent_task_queue SET trigger_evidence_kind=NULL,status='running' WHERE id=$1`, taskID)
			case "old_candidate":
				dbfx.Exec(t, `UPDATE agent_task_queue SET context=jsonb_set(context,'{workflow_feedback,candidate_id}',to_jsonb($2::text)) WHERE id=$1`, taskID, uuidToString(dbid.NewV7()))
			case "old_comment":
				dbfx.Exec(t, `UPDATE comment SET created_at=now()-interval '1 hour' WHERE id=$1`, commentID)
			case "deleted_comment":
				dbfx.Exec(t, `UPDATE comment SET deleted_at=now() WHERE id=$1`, commentID)
			case "note":
				dbfx.Exec(t, `UPDATE comment SET content='/NOTE personal note' WHERE id=$1`, commentID)
			case "agent_comment":
				dbfx.Exec(t, `UPDATE comment SET author_type='agent',author_id=$2 WHERE id=$1`, commentID, f.coordinatorID)
			case "wrong_author":
				dbfx.Exec(t, `UPDATE agent_task_queue SET originator_user_id=$2,accountable_user_id=$2 WHERE id=$1`, taskID, dbid.NewV7())
			case "completed", "failed", "cancelled":
				dbfx.Exec(t, `UPDATE agent_task_queue SET status=$2,completed_at=now() WHERE id=$1`, taskID, change)
			case "deferred", "dispatched", "waiting_local_directory":
				pause = true
				dbfx.Exec(t, `UPDATE agent_task_queue SET status=$2 WHERE id=$1`, taskID, change)
			case "live_coalesced":
				pause = true
				_, live := recordPromisedWorkflowDeliveryFeedback(t, f, false, "A live coalesced question still needs an answer.")
				dbfx.Exec(t, `UPDATE comment SET deleted_at=now() WHERE id=$1`, commentID)
				dbfx.Exec(t, `UPDATE agent_task_queue SET coalesced_comment_ids=ARRAY[$2]::uuid[] WHERE id=$1`, taskID, live.ID)
			}
			worker := NewWorkflowDeliveryWorker(testHandler)
			worker.client = server.Client()
			if worked, err := worker.ProcessNext(context.Background()); err != nil || !worked {
				t.Fatalf("input check worked=%v error=%v", worked, err)
			}
			if (mutations.Load() == 0) != pause {
				t.Fatalf("change=%s pause=%v provider mutations=%d", change, pause, mutations.Load())
			}
		})
	}
}
