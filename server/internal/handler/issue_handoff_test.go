package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func registerTestHandoff(t *testing.T, issue, outgoing, agent, mode, resume string) db.IssueWakeup {
	t.Helper()
	body := service.HandoffInput{
		RequestKey: uuidToString(dbid.NewV7()), OutgoingTaskID: outgoing, AgentID: agent,
		Status: "in_review", ContextMode: mode, ResumeTaskID: resume,
		Instruction: "Evaluate the identified candidate against the owning requirements.",
		Candidates: []service.HandoffCandidate{
			{RepositoryURL: "https://forge.example/a", PRURL: "https://forge.example/a/pulls/1", Branch: "feature/a", CommitSHA: strings.Repeat("a", 40), Draft: true},
			{RepositoryURL: "https://forge.example/b", PRURL: "https://forge.example/b/pulls/2", Branch: "feature/b", CommitSHA: strings.Repeat("b", 40), Draft: true},
		}, EvidenceURLs: []string{},
	}
	req := withURLParam(newRequest(http.MethodPost, "/", body), "id", issue)
	req.Header.Set("X-Agent-ID", func() string {
		source, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(outgoing))
		if err != nil {
			t.Fatal(err)
		}
		return uuidToString(source.AgentID)
	}())
	req.Header.Set("X-Task-ID", outgoing)
	req.Header.Set("X-Actor-Source", "task_token")
	var saved issueHandoffResponse
	testutil.Call(t, testHandler.CreateIssueHandoff, req).Want(http.StatusOK).JSON(&saved)
	var replay issueHandoffResponse
	testutil.Call(t, testHandler.CreateIssueHandoff, withURLParam(newRequest(http.MethodPost, "/", body), "id", issue)).Want(http.StatusOK).JSON(&replay)
	if replay.ID != saved.ID {
		t.Fatal("retry created another handoff")
	}
	return saved.IssueWakeup
}

func TestIssueHandoffIndependentReviewAndRetainedFixer(t *testing.T) {
	ctx := context.Background()
	issue := dbfx.Issue(t, "Cross-repository feature")
	enrollWorkflowPolicy(t, issue, insertCompleteWorkflowSkill(t, "Mica workflow")).Want(http.StatusCreated)
	dbfx.Cleanup(t, "DELETE FROM issue_wakeup WHERE issue_id=$1", issue)
	dbfx.Cleanup(t, "DELETE FROM issue_wakeup_receipt WHERE wakeup_id IN (SELECT id FROM issue_wakeup WHERE issue_id=$1)", issue)
	dbfx.Cleanup(t, "DELETE FROM agent_task_queue WHERE issue_id=$1", issue)
	runtime := createClaimReclaimRuntime(t, nil, "Handoff runtime")
	implementor := dbfx.Agent(t, "Implementor", runtime)
	fixer := dbfx.Agent(t, "Fixer", runtime)
	finalReviewer := dbfx.Agent(t, "Independent reviewer", runtime)
	dbfx.Exec(t, "UPDATE issue SET status='in_progress',assignee_type='agent',assignee_id=$2 WHERE id=$1", issue, implementor)
	dbfx.Task(t, implementor, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "originator_user_id": testUserID, "accountable_user_id": testUserID})
	implementation := claimWorkflowTask(t, runtime, protocol.DaemonCapabilityPlatformSkillV1)
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now(),session_id='implementation-context',work_dir='/tmp/implementation' WHERE id=$1", implementation.ID)
	svc := service.IssueWakeupService{Tasks: testHandler.TaskService}
	first := registerTestHandoff(t, issue, implementation.ID, fixer, "fresh", "")
	if err := svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if task, err := testHandler.TaskService.ClaimTask(ctx, parseUUID(fixer)); err != nil || task != nil {
		t.Fatalf("recipient started before writer ended: %+v, %v", task, err)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now(),result=$2::jsonb WHERE id=$1", implementation.ID, `{"summary":"VERDICT_COACHING_SENTINEL"}`)
	if err := svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	review := claimWorkflowTask(t, runtime, protocol.DaemonCapabilityPlatformSkillV1)
	if review.AgentID != fixer || review.PriorSessionID != "" || review.PriorWorkDir != "" || strings.Contains(review.HandoffNote, "VERDICT_COACHING_SENTINEL") {
		t.Fatalf("review inherited the wrong context: %+v", review)
	}
	if !strings.Contains(review.HandoffNote, strings.Repeat("a", 40)) || !strings.Contains(review.HandoffNote, strings.Repeat("b", 40)) {
		t.Fatal("review lost cross-repository candidate")
	}
	current, err := testHandler.Queries.GetIssue(ctx, parseUUID(issue))
	if err != nil || current.Status != "in_review" || uuidToString(current.AssigneeID) != fixer {
		t.Fatalf("handoff did not change owner and phase: %+v, %v", current, err)
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now(),session_id='retained-fixer',work_dir='/tmp/fixer' WHERE id=$1", review.ID)
	registerTestHandoff(t, issue, review.ID, finalReviewer, "fresh", "")
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", review.ID)
	if err := svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	final := claimWorkflowTask(t, runtime, protocol.DaemonCapabilityPlatformSkillV1)
	if final.AgentID != finalReviewer || final.PriorSessionID != "" || final.PriorWorkDir != "" {
		t.Fatal("final reviewer was not independent")
	}
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1", final.ID)
	registerTestHandoff(t, issue, final.ID, fixer, "resume", review.ID)
	// A later, unrelated run for the same fixer must not hijack continuity.
	dbfx.Task(t, fixer, testutil.Cols{"runtime_id": runtime, "issue_id": issue, "status": "completed", "completed_at": testutil.Raw("now()"), "session_id": "unrelated-newer-context", "work_dir": "/tmp/unrelated"})
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", final.ID)
	if err := svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	resumed := claimWorkflowTask(t, runtime, protocol.DaemonCapabilityPlatformSkillV1)
	if resumed.AgentID != fixer || resumed.PriorSessionID != "retained-fixer" || resumed.PriorWorkDir != "/tmp/fixer" {
		t.Fatalf("did not resume identified fixer: session=%q workdir=%q", resumed.PriorSessionID, resumed.PriorWorkDir)
	}
	var rows []issueHandoffRowResponse
	testutil.Call(t, testHandler.ListIssueHandoffs, withURLParam(newRequest(http.MethodGet, "/", nil), "id", issue)).Want(http.StatusOK).JSON(&rows)
	if len(rows) != 3 || rows[0].ID != first.ID {
		t.Fatalf("history lost handoffs: %+v", rows)
	}

	outsider := dbfx.User(t, "Handoff outsider", "handoff-outsider@multica.test")
	dbfx.Member(t, testWorkspaceID, outsider, "member")
	for _, list := range []http.HandlerFunc{testHandler.ListIssueHandoffs, testHandler.ListIssueWakeups} {
		var hidden []json.RawMessage
		testutil.Call(t, list, withURLParam(newRequestAs(outsider, http.MethodGet, "/", nil), "id", issue)).Want(http.StatusOK).JSON(&hidden)
		if len(hidden) != 0 {
			t.Fatal("handoff disclosed private source or recipient")
		}
	}
	stored, err := testHandler.Queries.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: first.ID, WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	var original service.HandoffInput
	if err := json.Unmarshal(stored.Handoff, &original); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.CreateIssueHandoff, withURLParam(newRequestAs(outsider, http.MethodPost, "/", original), "id", issue)).Want(http.StatusForbidden)
}

func TestHandoffResumeSourceSelectsExactContext(t *testing.T) {
	first, retry := dbid.NewV7(), dbid.NewV7()
	for _, tc := range []struct {
		mode, resume string
		retry        bool
		want         string
		invalid      bool
	}{
		{"fresh", "", false, "", false}, {"resume", uuidToString(first), false, uuidToString(first), false},
		{"fresh", "", true, uuidToString(retry), false}, {"resume", uuidToString(first), true, uuidToString(retry), false},
		{"fresh", uuidToString(first), false, "", true}, {"resume", "", false, "", true}, {"unknown", "", false, "", true},
	} {
		t.Run(tc.mode+tc.resume+tc.want, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"workflow_handoff": map[string]string{"context_mode": tc.mode, "resume_task_id": tc.resume}})
			task := db.AgentTaskQueue{Context: raw, ForceFreshSession: true}
			if tc.retry {
				task.RetryOfTaskID = retry
			}
			source, handoff, err := handoffResumeSource(task)
			got := ""
			if source.Valid {
				got = uuidToString(source)
			}
			if !handoff || (err != nil) != tc.invalid || (!tc.invalid && got != tc.want) {
				t.Fatalf("source=%s handoff=%v err=%v", got, handoff, err)
			}
		})
	}
}
