package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/skillbundle"
)

func workflowClaimEnrolledIssue(t *testing.T, f principalFixture, title string) string {
	t.Helper()
	issue := f.Issue(t, title)
	source := AgentSkillData{
		ID: "11111111-1111-1111-1111-111111111111", Name: "workflow-policy",
		Source: skillbundle.SourceWorkspace, ReplacesBuiltin: BuiltinSkillID(PlatformSkillName),
		Content: "Pinned workflow instructions",
		Files:   []AgentSkillFileData{{Path: "runtime/issue-workflow.md", Content: "Keep the work phase stable."}},
	}
	bundles, _ := BuildAgentSkillBundles([]AgentSkillData{source})
	policy := IssueWorkflowPolicy{
		FormatVersion: 1, Scope: IssueWorkflowBundleScope, Coverage: issueWorkflowPolicyCoverage,
		Version: bundles[0].Hash, SourceSkillID: source.ID, Bundle: bundles[0],
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.TaskSvc.DecodeIssueWorkflowPolicy(raw); err != nil {
		t.Fatalf("invalid test policy: %v", err)
	}
	f.Exec(t, "UPDATE issue SET workflow_policy = $2 WHERE id = $1", issue, raw)
	return issue
}

func workflowClaimTask(t *testing.T, f principalFixture, agentID, issueID string, extra testutil.Cols) string {
	t.Helper()
	var runtimeID string
	f.QueryRow(t, "SELECT runtime_id FROM agent WHERE id = $1", agentID).Scan(&runtimeID)
	cols := testutil.Cols{"issue_id": issueID, "runtime_id": runtimeID}
	for k, v := range extra {
		cols[k] = v
	}
	return f.Task(t, agentID, cols)
}

// The same issue may have queued work for different agents. Enrollment turns
// the issue into the serialization key while legacy issues keep parallelism.
func TestWorkflowClaimSerializesEnrolledIssueAcrossAgents(t *testing.T) {
	for _, enrolled := range []bool{false, true} {
		name := "legacy"
		if enrolled {
			name = "enrolled"
		}
		t.Run(name, func(t *testing.T) {
			f, owner := newPrincipalFixture(t)
			firstAgent := f.privateAgentOwnedBy(t, owner, "claim-first")
			secondAgent := f.privateAgentOwnedBy(t, owner, "claim-second")
			var issue string
			if enrolled {
				issue = workflowClaimEnrolledIssue(t, f, "Enrolled concurrent claim")
			} else {
				issue = f.Issue(t, "Legacy concurrent claim")
			}
			workflowClaimTask(t, f, firstAgent, issue, nil)
			workflowClaimTask(t, f, secondAgent, issue, nil)

			// Stall both provisional dispatches long enough to exercise the
			// cross-agent race, not only the committed queue predicate.
			for i, agentID := range []string{firstAgent, secondAgent} {
				name := fmt.Sprintf("workflow_claim_%d_%d", time.Now().UnixNano(), i)
				createSleepTrigger(t, context.Background(), f.Pool, name, name+"_fn", agentID)
			}
			start := make(chan struct{})
			type result struct {
				agentID string
				claimed bool
				err     error
			}
			results := make(chan result, 2)
			var wg sync.WaitGroup
			for _, agentID := range []string{firstAgent, secondAgent} {
				wg.Add(1)
				go func(agentID string) {
					defer wg.Done()
					<-start
					svc := NewTaskService(db.New(f.Pool), f.Pool, nil, events.New())
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					task, err := svc.ClaimTask(ctx, util.MustParseUUID(agentID))
					results <- result{agentID: agentID, claimed: task != nil, err: err}
				}(agentID)
			}
			close(start)
			wg.Wait()
			close(results)
			claimed := 0
			var retryAgent string
			for result := range results {
				if result.err != nil {
					t.Fatalf("claim failed: %v", result.err)
				}
				if result.claimed {
					claimed++
				} else {
					retryAgent = result.agentID
				}
			}
			if !enrolled && claimed == 1 {
				// The short NOWAIT issue lock can make one simultaneous poll
				// retry. Legacy parallelism means both may be active once the
				// first claim transaction has finished, not in one poll wave.
				svc := NewTaskService(db.New(f.Pool), f.Pool, nil, events.New())
				task, err := svc.ClaimTask(context.Background(), util.MustParseUUID(retryAgent))
				if err != nil || task == nil {
					t.Fatalf("legacy claim after transient issue-lock contention: %+v, %v", task, err)
				}
				claimed++
			}
			wantClaimed := 2
			if enrolled {
				wantClaimed = 1
			}
			if claimed != wantClaimed {
				t.Fatalf("claimed = %d, want %d", claimed, wantClaimed)
			}
			if active := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND status = 'dispatched'`, issue); active != wantClaimed {
				t.Fatalf("dispatched tasks = %d, want %d", active, wantClaimed)
			}
			if queued := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id = $1 AND status = 'queued'`, issue); queued != 2-wantClaimed {
				t.Fatalf("queued tasks = %d, want %d", queued, 2-wantClaimed)
			}
		})
	}
}

// workflowClaimHandoff inserts the exact persistent fields the queue fence
// reads. Handoff validation and dispatch have their own service tests.
func workflowClaimHandoff(t *testing.T, f principalFixture, issue, outgoingAgent, recipientAgent, outgoingTask string) string {
	t.Helper()
	return f.Insert(t, "issue_wakeup", testutil.Cols{
		"id": testutil.Raw("gen_random_uuid()"), "workspace_id": f.WorkspaceID,
		"issue_id": issue, "agent_id": recipientAgent, "created_by": f.UserID,
		"source_task_id": outgoingTask, "filter_task_id": outgoingTask,
		"filter_agent_id": outgoingAgent, "instruction": "Continue the pinned issue workflow",
		"kind": "event", "mode": "once",
		"event_types": testutil.Raw("ARRAY['task.completed','task.failed','task.cancelled']::text[]"),
		"handoff":     testutil.Raw("'{}'::jsonb"), "request_key": testutil.Raw("gen_random_uuid()"),
	})
}

func TestWorkflowClaimPendingHandoffDoesNotStarveOtherIssue(t *testing.T) {
	f, owner := newPrincipalFixture(t)
	outgoing := f.privateAgentOwnedBy(t, owner, "pending-outgoing")
	recipient := f.privateAgentOwnedBy(t, owner, "pending-recipient")
	issue := workflowClaimEnrolledIssue(t, f, "Pending handoff")
	source := workflowClaimTask(t, f, outgoing, issue, testutil.Cols{"status": "completed"})
	blocked := workflowClaimTask(t, f, recipient, issue, testutil.Cols{"priority": 100})
	workflowClaimHandoff(t, f, issue, outgoing, recipient, source)
	otherIssue := f.Issue(t, "Unrelated eligible work")
	allowed := workflowClaimTask(t, f, recipient, otherIssue, nil)

	svc := NewTaskService(f.q, f.Pool, nil, events.New())
	task, err := svc.ClaimTask(context.Background(), util.MustParseUUID(recipient))
	if err != nil || task == nil || util.UUIDToString(task.ID) != allowed {
		t.Fatalf("claim behind pending handoff = %+v, %v; want unrelated task %s", task, err, allowed)
	}
	var blockedStatus string
	f.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id = $1", blocked).Scan(&blockedStatus)
	if blockedStatus != "queued" {
		t.Fatalf("generic task on pending handoff issue = %q, want queued", blockedStatus)
	}
}

func TestWorkflowClaimFailedSourceAllowsOnlyRetryLineage(t *testing.T) {
	f, owner := newPrincipalFixture(t)
	outgoing := f.privateAgentOwnedBy(t, owner, "retry-outgoing")
	recipient := f.privateAgentOwnedBy(t, owner, "retry-recipient")
	issue := workflowClaimEnrolledIssue(t, f, "Failed handoff source")
	source := workflowClaimTask(t, f, outgoing, issue, testutil.Cols{"status": "failed"})
	generic := workflowClaimTask(t, f, recipient, issue, testutil.Cols{"priority": 100})
	retry := workflowClaimTask(t, f, outgoing, issue, testutil.Cols{"retry_of_task_id": source})
	workflowClaimHandoff(t, f, issue, outgoing, recipient, source)

	svc := NewTaskService(f.q, f.Pool, nil, events.New())
	task, err := svc.ClaimTask(context.Background(), util.MustParseUUID(outgoing))
	if err != nil || task == nil || util.UUIDToString(task.ID) != retry {
		t.Fatalf("claim after source failure = %+v, %v; want retry %s", task, err, retry)
	}
	var genericStatus string
	f.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id = $1", generic).Scan(&genericStatus)
	if genericStatus != "queued" {
		t.Fatalf("generic task after source failure = %q, want queued", genericStatus)
	}
	if task, err := svc.ClaimTask(context.Background(), util.MustParseUUID(recipient)); err != nil || task != nil {
		t.Fatalf("generic recipient claim after source failure = %+v, %v; want no task", task, err)
	}
}

func TestWorkflowClaimFailedRecipientBlocksUntilSuccessOrCancellation(t *testing.T) {
	for _, release := range []string{"success", "cancel"} {
		t.Run(release, func(t *testing.T) {
			f, owner := newPrincipalFixture(t)
			outgoing := f.privateAgentOwnedBy(t, owner, "recipient-outgoing")
			recipient := f.privateAgentOwnedBy(t, owner, "recipient-failed")
			issue := workflowClaimEnrolledIssue(t, f, "Failed recipient")
			source := workflowClaimTask(t, f, outgoing, issue, testutil.Cols{"status": "completed"})
			failed := workflowClaimTask(t, f, recipient, issue, testutil.Cols{"status": "failed"})
			generic := workflowClaimTask(t, f, recipient, issue, nil)
			handoff := workflowClaimHandoff(t, f, issue, outgoing, recipient, source)
			f.Exec(t, "UPDATE issue_wakeup SET last_task_id = $2 WHERE id = $1", handoff, failed)
			svc := NewTaskService(f.q, f.Pool, nil, events.New())
			if task, err := svc.ClaimTask(context.Background(), util.MustParseUUID(recipient)); err != nil || task != nil {
				t.Fatalf("claim while recipient failed = %+v, %v; want no task", task, err)
			}
			if release == "success" {
				f.Exec(t, "UPDATE agent_task_queue SET status = 'completed' WHERE id = $1", failed)
			} else {
				f.Exec(t, "UPDATE issue_wakeup SET enabled = false, disabled_at = now() WHERE id = $1", handoff)
			}
			task, err := svc.ClaimTask(context.Background(), util.MustParseUUID(recipient))
			if err != nil || task == nil || util.UUIDToString(task.ID) != generic {
				t.Fatalf("claim after handoff %s = %+v, %v; want generic %s", release, task, err, generic)
			}
		})
	}
}

func TestWorkflowClaimIssueLockRollsBackProvisionalDispatch(t *testing.T) {
	f, owner := newPrincipalFixture(t)
	agent := f.privateAgentOwnedBy(t, owner, "locked-issue")
	issue := workflowClaimEnrolledIssue(t, f, "Locked issue")
	queued := workflowClaimTask(t, f, agent, issue, nil)
	tx, err := f.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var lockedID string
	if err := tx.QueryRow(context.Background(), "SELECT id FROM issue WHERE id = $1 FOR NO KEY UPDATE", issue).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	svc := NewTaskService(f.q, f.Pool, nil, events.New())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if task, err := svc.ClaimTask(ctx, util.MustParseUUID(agent)); err != nil || task != nil {
		t.Fatalf("claim while issue locked = %+v, %v; want clean retry", task, err)
	}
	var status string
	f.QueryRow(t, "SELECT status FROM agent_task_queue WHERE id = $1", queued).Scan(&status)
	if status != "queued" {
		t.Fatalf("provisional dispatch persisted after lock conflict: %q", status)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	task, err := svc.ClaimTask(context.Background(), util.MustParseUUID(agent))
	if err != nil || task == nil || util.UUIDToString(task.ID) != queued {
		t.Fatalf("claim after issue unlock = %+v, %v; want %s", task, err, queued)
	}
}
