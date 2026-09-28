package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func activeWorkflowMigrationFixture(t *testing.T) (workspace, issue, nextSkill, runtime, agent string, migrate func() *testutil.Response) {
	t.Helper()
	workspace = dbfx.Workspace(t, "Active migration", fmt.Sprintf("active-migration-%d", time.Now().UnixNano()))
	dbfx.Member(t, workspace, testUserID, "owner")
	firstSkill := workflowCutoverTestSkill(t, workspace, "First active policy")
	testutil.Call(t, testHandler.CutoverWorkspaceWorkflowDefault,
		workflowCutoverRequest(http.MethodPost, "/", workspace, map[string]any{"skill_id": firstSkill})).Want(http.StatusCreated)
	issue = dbfx.Issue(t, "Enrolled unfinished issue", testutil.Cols{"workspace_id": workspace, "status": "in_progress"})
	nextSkill = workflowCutoverTestSkill(t, workspace, "Next active policy")
	runtime = dbfx.Runtime(t, "active migration runtime", testutil.Cols{"workspace_id": workspace})
	agent = dbfx.Agent(t, "active migration agent", runtime, testutil.Cols{"workspace_id": workspace})
	migrate = func() *testutil.Response {
		request := workflowCutoverRequest(http.MethodPost, "/api/issues/"+issue+"/workflow-migrate", workspace,
			map[string]any{"skill_id": nextSkill, "reason": "Adopt the approved workflow revision",
				"reconciliation": "All prior work is retained; no claimed task is in flight."})
		return testutil.Call(t, testHandler.MigrateIssueWorkflow, withURLParam(request, "id", issue))
	}
	return
}

func TestActiveWorkflowMigrationPreservesEvidenceAndRetiresOldDispatch(t *testing.T) {
	workspace, issue, _, runtime, agent, migrate := activeWorkflowMigrationFixture(t)
	queued := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime, "status": "queued"})
	completed := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime,
		"status": "completed", "started_at": testutil.Raw("now()-interval '2 hours'"),
		"completed_at": testutil.Raw("now()-interval '1 hour'")})
	comment := dbfx.Insert(t, "comment", testutil.Cols{"workspace_id": workspace, "issue_id": issue,
		"author_type": "system", "author_id": testUserID, "content": "Completed task evidence",
		"type": "progress_update", "source_task_id": completed})
	var prior []byte
	dbfx.QueryRow(t, `SELECT workflow_policy FROM issue WHERE id=$1`, issue).Scan(&prior)
	var previous map[string]any
	if err := json.Unmarshal(prior, &previous); err != nil {
		t.Fatal(err)
	}
	// A superseded candidate and its review remain immutable history even when
	// the issue no longer points to that candidate.
	historicalCandidate := dbfx.Insert(t, "issue_workflow_candidate", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
		"policy_version": previous["version"], "digest": "historical-digest", "scope_digest": "historical-scope",
		"source_handoff_id": dbid.NewV7(), "source_task_id": dbid.NewV7(), "writer_task_id": completed,
		"pr_set": testutil.Raw("'[]'::jsonb"),
	})
	historicalReview := dbfx.Insert(t, "issue_workflow_review", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
		"candidate_id": historicalCandidate, "reviewer_task_id": dbid.NewV7(),
		"verdict": "pass", "pr_review_urls": testutil.Raw("'[]'::jsonb"),
	})
	migrate().Want(http.StatusOK)
	var after []byte
	var frozen, migrated bool
	var status string
	dbfx.QueryRow(t, `SELECT workflow_policy,workflow_frozen,workflow_migrated_at IS NOT NULL,status
		FROM issue WHERE id=$1`, issue).Scan(&after, &frozen, &migrated, &status)
	var current map[string]any
	if err := json.Unmarshal(after, &current); err != nil {
		t.Fatal(err)
	}
	if frozen || !migrated || status != "in_progress" || previous["version"] == current["version"] {
		t.Fatalf("migration state frozen=%t migrated=%t status=%s previous=%v current=%v",
			frozen, migrated, status, previous["version"], current["version"])
	}
	var queuedStatus, completedStatus string
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, queued).Scan(&queuedStatus)
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, completed).Scan(&completedStatus)
	if queuedStatus != "cancelled" || completedStatus != "completed" ||
		dbfx.Count(t, `SELECT count(*) FROM comment WHERE id=$1 AND content='Completed task evidence'`, comment) != 1 ||
		dbfx.Count(t, `SELECT count(*) FROM issue_workflow_candidate WHERE id=$1 AND digest='historical-digest'`, historicalCandidate) != 1 ||
		dbfx.Count(t, `SELECT count(*) FROM issue_workflow_review WHERE id=$1 AND candidate_id=$2 AND verdict='pass'`, historicalReview, historicalCandidate) != 1 {
		t.Fatalf("old evidence changed: queued=%s completed=%s", queuedStatus, completedStatus)
	}
	var details []byte
	dbfx.QueryRow(t, `SELECT details FROM activity_log WHERE issue_id=$1 AND action='workflow_migrated'`, issue).Scan(&details)
	var audit map[string]any
	if err := json.Unmarshal(details, &audit); err != nil {
		t.Fatal(err)
	}
	if audit["previous_policy"] == nil || audit["new_policy"] == nil || audit["reconciliation"] == nil {
		t.Fatalf("migration lacks policy/reconciliation audit: %s", details)
	}
}

func TestActiveWorkflowMigrationRejectsClaimedTaskWithoutMutation(t *testing.T) {
	_, issue, _, runtime, agent, migrate := activeWorkflowMigrationFixture(t)
	active := dbfx.Task(t, agent, testutil.Cols{"issue_id": issue, "runtime_id": runtime,
		"status": "running", "started_at": testutil.Raw("now()")})
	var before []byte
	dbfx.QueryRow(t, `SELECT workflow_policy FROM issue WHERE id=$1`, issue).Scan(&before)
	migrate().Want(http.StatusConflict)
	var after []byte
	var taskStatus string
	dbfx.QueryRow(t, `SELECT workflow_policy FROM issue WHERE id=$1`, issue).Scan(&after)
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, active).Scan(&taskStatus)
	if string(after) != string(before) || taskStatus != "running" ||
		dbfx.Count(t, `SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action='workflow_migrated'`, issue) != 0 {
		t.Fatal("rejected migration changed policy, task, or audit")
	}
}

func TestActiveWorkflowMigrationRejectsCurrentCandidateAndAcceptance(t *testing.T) {
	t.Run("candidate", func(t *testing.T) {
		workspace, issue, _, _, _, migrate := activeWorkflowMigrationFixture(t)
		var prior []byte
		dbfx.QueryRow(t, `SELECT workflow_policy FROM issue WHERE id=$1`, issue).Scan(&prior)
		var pinned map[string]any
		if err := json.Unmarshal(prior, &pinned); err != nil {
			t.Fatal(err)
		}
		candidate := dbfx.Insert(t, "issue_workflow_candidate", testutil.Cols{
			"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
			"policy_version": pinned["version"], "digest": "exact-candidate", "scope_digest": "exact-scope",
			"source_handoff_id": dbid.NewV7(), "source_task_id": dbid.NewV7(), "writer_task_id": dbid.NewV7(),
			"pr_set": testutil.Raw("'[]'::jsonb"),
		})
		dbfx.Exec(t, `UPDATE issue SET workflow_candidate_id=$2 WHERE id=$1`, issue, candidate)
		migrate().Want(http.StatusConflict)
		var after []byte
		var currentCandidate string
		dbfx.QueryRow(t, `SELECT workflow_policy,workflow_candidate_id::text FROM issue WHERE id=$1`, issue).
			Scan(&after, &currentCandidate)
		if string(after) != string(prior) || currentCandidate != candidate ||
			dbfx.Count(t, `SELECT count(*) FROM issue_workflow_candidate WHERE id=$1 AND digest='exact-candidate'`, candidate) != 1 {
			t.Fatal("rejected migration changed exact candidate evidence")
		}
	})
	t.Run("acceptance without current pointer", func(t *testing.T) {
		workspace, issue, _, _, _, migrate := activeWorkflowMigrationFixture(t)
		var prior []byte
		dbfx.QueryRow(t, `SELECT workflow_policy FROM issue WHERE id=$1`, issue).Scan(&prior)
		var pinned map[string]any
		if err := json.Unmarshal(prior, &pinned); err != nil {
			t.Fatal(err)
		}
		candidate := dbfx.Insert(t, "issue_workflow_candidate", testutil.Cols{
			"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
			"policy_version": pinned["version"], "digest": "accepted-candidate", "scope_digest": "accepted-scope",
			"source_handoff_id": dbid.NewV7(), "source_task_id": dbid.NewV7(), "writer_task_id": dbid.NewV7(),
			"pr_set": testutil.Raw("'[]'::jsonb"),
		})
		acceptance := dbfx.Insert(t, "issue_workflow_acceptance", testutil.Cols{
			"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue, "candidate_id": candidate,
			"mode": "human", "actor_type": "member", "actor_id": testUserID, "state": "accepted",
			"policy_version": pinned["version"], "authority_snapshot": testutil.Raw("'{}'::jsonb"),
			"accepted_at": testutil.Raw("now()"),
		})
		migrate().Want(http.StatusConflict)
		if dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='accepted'`, acceptance) != 1 ||
			dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND workflow_policy=$2::jsonb`, issue, prior) != 1 {
			t.Fatal("rejected migration changed acceptance or pinned policy")
		}
	})
}
