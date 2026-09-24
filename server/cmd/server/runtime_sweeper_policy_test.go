package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// A failed task ends an execution attempt, not an enrolled issue's work phase.
// The legacy reset remains in place for issues without an enrolled policy.
func TestFailedTaskPreservesEnrolledIssueWorkPhase(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	for _, path := range []struct {
		name     string
		fallback bool
	}{
		{name: "task service"},
		{name: "sweeper fallback", fallback: true},
	} {
		for _, enrolled := range []bool{false, true} {
			name := "legacy"
			if enrolled {
				name = "enrolled"
			}
			t.Run(path.name+"/"+name, func(t *testing.T) {
				ctx := context.Background()
				issueID, agentID, taskID := setupSweeperTestFixture(t, "running")
				t.Cleanup(func() { cleanupSweeperFixture(t, issueID, agentID) })

				queries := db.New(testPool)
				bus := events.New()
				taskSvc := service.NewTaskService(queries, testPool, nil, bus)
				var rawPolicy []byte
				if enrolled {
					var platform service.AgentSkillData
					for _, builtin := range taskSvc.AllBuiltinSkills() {
						if builtin.Name == service.PlatformSkillName {
							platform = builtin
							break
						}
					}
					platform.ID = "11111111-1111-1111-1111-111111111111"
					platform.Files = append(platform.Files, service.AgentSkillFileData{
						Path: "runtime/issue-workflow.md", Content: "Keep the issue work phase through retries and recovery.",
					})
					policy, err := taskSvc.NewIssueWorkflowPolicy(platform)
					if err != nil {
						t.Fatalf("build policy snapshot: %v", err)
					}
					rawPolicy, err = json.Marshal(policy)
					if err != nil {
						t.Fatalf("marshal policy snapshot: %v", err)
					}
				}
				if _, err := testPool.Exec(ctx, `
					UPDATE issue SET status = 'in_progress', workflow_policy = $2 WHERE id = $1
				`, issueID, rawPolicy); err != nil {
					t.Fatalf("set issue phase and policy: %v", err)
				}
				if _, err := testPool.Exec(ctx, `
					UPDATE agent_task_queue SET status = 'failed', completed_at = now(),
						failure_reason = 'agent_error', error = 'execution failed'
					WHERE id = $1
				`, taskID); err != nil {
					t.Fatalf("fail task: %v", err)
				}
				failed, err := queries.GetAgentTask(ctx, util.MustParseUUID(taskID))
				if err != nil {
					t.Fatalf("load failed task: %v", err)
				}

				var failedEvents []events.Event
				bus.Subscribe("task:failed", func(e events.Event) { failedEvents = append(failedEvents, e) })
				if path.fallback {
					broadcastFailedTasks(ctx, queries, nil, bus, []db.AgentTaskQueue{failed})
				} else if retried := taskSvc.HandleFailedTasks(ctx, []db.AgentTaskQueue{failed}); retried != 0 {
					t.Fatalf("unexpected retry count: %d", retried)
				}

				var phase string
				if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&phase); err != nil {
					t.Fatalf("load issue phase: %v", err)
				}
				want := "todo"
				if enrolled {
					want = "in_progress"
				}
				if phase != want {
					t.Fatalf("issue phase after terminal failure = %q, want %q", phase, want)
				}
				if len(failedEvents) != 1 || failedEvents[0].TaskID != taskID || failedEvents[0].WorkspaceID != testWorkspaceID {
					t.Fatalf("task failure events = %+v, want one event for task %s in workspace %s", failedEvents, taskID, testWorkspaceID)
				}
			})
		}
	}
}
