package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func workflowCutoverTestSkill(t *testing.T, workspace, content string) string {
	t.Helper()
	id := dbfx.Insert(t, "skill", testutil.Cols{
		"workspace_id": workspace, "name": t.Name() + content,
		"description": "Workflow cutover test", "content": "---\nname: cutover-test\n---\n\n" + content,
		"config": testutil.Raw("'{}'::jsonb"), "created_by": testUserID,
	})
	for _, builtin := range testHandler.TaskService.BuiltinSkills("", false) {
		if builtin.Name != service.PlatformSkillName {
			continue
		}
		for _, file := range builtin.Files {
			dbfx.Insert(t, "skill_file", testutil.Cols{"skill_id": id, "path": file.Path, "content": file.Content})
		}
	}
	dbfx.Insert(t, "skill_file", testutil.Cols{
		"skill_id": id, "path": "runtime/issue-workflow.md", "content": content,
	})
	return id
}

func TestWorkflowCutoverRejectsInvalidFormat2StatusBeforeFreeze(t *testing.T) {
	workspace := dbfx.Workspace(t, "Format two cutover", fmt.Sprintf("format-two-cutover-%d", time.Now().UnixNano()))
	dbfx.Member(t, workspace, testUserID, "owner")
	oldIssue := dbfx.Issue(t, "Old issue awaiting freeze", testutil.Cols{"workspace_id": workspace})
	runtime := dbfx.Runtime(t, "format two cutover runtime", testutil.Cols{"workspace_id": workspace})
	agent := dbfx.Agent(t, "format two outcome agent", runtime, testutil.Cols{"workspace_id": workspace})
	status := dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": workspace, "key": "pr_ready", "name": "PR Ready", "category": "done",
		"color": "#22c55e", "position": 1,
	})
	skill := workflowCutoverTestSkill(t, workspace, "Format two cutover")
	dbfx.Insert(t, "skill_file", testutil.Cols{"skill_id": skill, "path": "runtime/policy.json",
		"content": `{"format_version":2,"accepted_status_key":"pr_ready","outcome_agent_id":"` + agent + `"}`})
	request := func() *testutil.Response {
		return testutil.Call(t, testHandler.CutoverWorkspaceWorkflowDefault,
			workflowCutoverRequest(http.MethodPost, "/", workspace, map[string]any{"skill_id": skill}))
	}
	request().Want(http.StatusConflict)
	var frozen bool
	var cutover bool
	dbfx.QueryRow(t, `SELECT workflow_frozen FROM issue WHERE id=$1`, oldIssue).Scan(&frozen)
	dbfx.QueryRow(t, `SELECT workflow_cutover_at IS NOT NULL FROM workspace WHERE id=$1`, workspace).Scan(&cutover)
	if frozen || cutover {
		t.Fatalf("invalid format two policy partially cut over: frozen=%t cutover=%t", frozen, cutover)
	}
	dbfx.Exec(t, `UPDATE issue_status SET category='started' WHERE id=$1`, status)
	request().Want(http.StatusCreated)
}

func TestFormat2PolicyReferencesPreventStatusArchive(t *testing.T) {
	t.Run("enrolled issue", func(t *testing.T) {
		issueID := dbfx.Issue(t, "Pinned issue using PR Ready")
		runtime := createClaimReclaimRuntime(t, nil, "pinned archive runtime")
		agent := dbfx.Agent(t, "pinned archive agent", runtime)
		statusID := dbfx.Insert(t, "issue_status", testutil.Cols{
			"workspace_id": testWorkspaceID, "key": "pr_ready", "name": "PR Ready",
			"category": "started", "color": "#22c55e", "position": 1,
		})
		skill := insertCompleteWorkflowSkill(t, "---\nname: archive-pin\n---\n\nPinned archive policy")
		dbfx.Insert(t, "skill_file", testutil.Cols{"skill_id": skill, "path": "runtime/policy.json",
			"content": `{"format_version":2,"accepted_status_key":"pr_ready","outcome_agent_id":"` + agent + `"}`})
		enrollWorkflowPolicy(t, issueID, skill).Want(http.StatusCreated)
		dbfx.Exec(t, `UPDATE issue SET status='in_progress' WHERE id=$1`, issueID)
		format1Issue := dbfx.Issue(t, "Unrelated pinned format one issue")
		enrollWorkflowPolicy(t, format1Issue,
			workflowCutoverTestSkill(t, testWorkspaceID, "Unrelated format one policy")).Want(http.StatusCreated)
		unrelatedStatus := dbfx.Insert(t, "issue_status", testutil.Cols{
			"workspace_id": testWorkspaceID, "key": "unrelated_archive", "name": "Unrelated archive",
			"category": "started", "color": "#22c55e", "position": 2,
		})
		testutil.Call(t, testHandler.ArchiveIssueStatus,
			withURLParam(newRequest(http.MethodDelete, "/api/issue-statuses/"+unrelatedStatus, nil),
				"id", unrelatedStatus)).Want(http.StatusOK)
		archive := func() *testutil.Response {
			return testutil.Call(t, testHandler.ArchiveIssueStatus,
				withURLParam(newRequest(http.MethodDelete, "/api/issue-statuses/"+statusID, nil), "id", statusID))
		}
		archive().Want(http.StatusConflict)
		dbfx.Exec(t, `UPDATE issue SET workflow_frozen=true WHERE id=$1`, issueID)
		archive().Want(http.StatusOK)
	})
	t.Run("active workspace default", func(t *testing.T) {
		workspace := dbfx.Workspace(t, "Default PR Ready archive", fmt.Sprintf("default-pr-ready-%d", time.Now().UnixNano()))
		dbfx.Member(t, workspace, testUserID, "owner")
		runtime := dbfx.Runtime(t, "default archive runtime", testutil.Cols{"workspace_id": workspace})
		agent := dbfx.Agent(t, "default archive outcome agent", runtime, testutil.Cols{"workspace_id": workspace})
		statusID := dbfx.Insert(t, "issue_status", testutil.Cols{
			"workspace_id": workspace, "key": "pr_ready", "name": "PR Ready",
			"category": "started", "color": "#22c55e", "position": 1,
		})
		skill := workflowCutoverTestSkill(t, workspace, "Default archive policy")
		dbfx.Insert(t, "skill_file", testutil.Cols{"skill_id": skill, "path": "runtime/policy.json",
			"content": `{"format_version":2,"accepted_status_key":"pr_ready","outcome_agent_id":"` + agent + `"}`})
		testutil.Call(t, testHandler.CutoverWorkspaceWorkflowDefault,
			workflowCutoverRequest(http.MethodPost, "/", workspace, map[string]any{"skill_id": skill})).Want(http.StatusCreated)
		archive := withURLParam(newRequest(http.MethodDelete, "/api/issue-statuses/"+statusID, nil), "id", statusID)
		archive.Header.Set("X-Workspace-ID", workspace)
		testutil.Call(t, testHandler.ArchiveIssueStatus, archive).Want(http.StatusConflict)
	})
}

func TestWorkflowCutoverRejectsConcurrentAndLaterIssueWrites(t *testing.T) {
	workspace := dbfx.Workspace(t, "Workflow freeze race", fmt.Sprintf("workflow-freeze-%d", time.Now().UnixNano()))
	dbfx.Member(t, workspace, testUserID, "owner")
	issue := dbfx.Issue(t, "Frozen legacy issue", testutil.Cols{"workspace_id": workspace})
	skill := workflowCutoverTestSkill(t, workspace, "Freeze race policy")
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var comment string
	err = tx.QueryRow(ctx, `INSERT INTO comment(workspace_id,issue_id,author_type,author_id,content,type)
		VALUES($1,$2,'member',$3,'In-flight reconciliation','comment') RETURNING id`, workspace, issue, testUserID).Scan(&comment)
	if err != nil {
		t.Fatal(err)
	}
	cutover := func() *testutil.Response {
		return testutil.Call(t, testHandler.CutoverWorkspaceWorkflowDefault,
			workflowCutoverRequest(http.MethodPost, "/", workspace, map[string]any{"skill_id": skill}))
	}
	cutover().Want(http.StatusConflict)
	var frozen bool
	if err := tx.QueryRow(ctx, `SELECT workflow_frozen FROM issue WHERE id=$1`, issue).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	if frozen {
		t.Fatal("cutover froze issue before in-flight comment committed")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	runtime := dbfx.Runtime(t, "frozen comment runtime", testutil.Cols{"workspace_id": workspace})
	agent := dbfx.Agent(t, "frozen comment agent", runtime, testutil.Cols{"workspace_id": workspace})
	queued := dbfx.Task(t, agent, testutil.Cols{
		"issue_id": issue, "runtime_id": runtime, "status": "queued", "trigger_comment_id": comment,
	})
	cutover().Want(http.StatusCreated)
	frozenRequest := func(method, path, routeKey, routeID string, body any) *http.Request {
		req := withURLParam(newRequest(method, path, body), routeKey, routeID)
		req.Header.Set("X-Workspace-ID", workspace)
		return req
	}
	testutil.Call(t, testHandler.CreateComment,
		frozenRequest(http.MethodPost, "/api/issues/"+issue+"/comments", "id", issue,
			map[string]any{"content": "must wait for migration"})).Want(http.StatusConflict)
	testutil.Call(t, testHandler.DeleteComment,
		frozenRequest(http.MethodDelete, "/api/comments/"+comment, "commentId", comment, nil)).Want(http.StatusConflict)
	testutil.Call(t, testHandler.UpdateIssue,
		frozenRequest(http.MethodPut, "/api/issues/"+issue, "id", issue,
			map[string]any{"title": "must wait for migration"})).Want(http.StatusConflict)
	batch := newRequest(http.MethodPatch, "/api/issues/batch", map[string]any{
		"issue_ids": []string{issue}, "updates": map[string]any{"title": "must wait for migration"},
	})
	batch.Header.Set("X-Workspace-ID", workspace)
	testutil.Call(t, testHandler.BatchUpdateIssues, batch).Want(http.StatusConflict)
	testutil.Call(t, testHandler.ListComments,
		frozenRequest(http.MethodGet, "/api/issues/"+issue+"/comments", "id", issue, nil)).Want(http.StatusOK)
	_, err = testPool.Exec(ctx, `UPDATE issue SET title='Mutation after freeze' WHERE id=$1`, issue)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "issue_workflow_frozen" {
		t.Fatalf("frozen issue UPDATE error=%v, want frozen constraint", err)
	}
	_, err = testPool.Exec(ctx, `INSERT INTO comment(workspace_id,issue_id,author_type,author_id,content,type)
		VALUES($1,$2,'member',$3,'Late comment','comment')`, workspace, issue, testUserID)
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != "issue_workflow_frozen" {
		t.Fatalf("frozen comment INSERT error=%v, want frozen constraint", err)
	}
	var title string
	dbfx.QueryRow(t, `SELECT title FROM issue WHERE id=$1`, issue).Scan(&title)
	if title != "Frozen legacy issue" {
		t.Fatalf("frozen issue title changed to %q", title)
	}
	if _, err := testHandler.deleteCommentWithTaskCancellation(ctx, parseUUID(comment), parseUUID(workspace)); err == nil {
		t.Fatal("direct deletion unexpectedly bypassed frozen issue guard")
	}
	var taskStatus string
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, queued).Scan(&taskStatus)
	if taskStatus != "queued" {
		t.Fatalf("failed comment deletion cancelled frozen task: %s", taskStatus)
	}
	if _, err := testHandler.Queries.AddReaction(ctx, db.AddReactionParams{
		CommentID: parseUUID(comment), WorkspaceID: parseUUID(workspace),
		ActorType: "member", ActorID: parseUUID(testUserID), Emoji: "👍",
	}); err != nil {
		t.Fatalf("reaction on frozen comment: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE issue SET revision=revision+1 WHERE id=$1`, issue); err != nil {
		t.Fatalf("revision bookkeeping on frozen issue: %v", err)
	}
}

func workflowCutoverRequest(method, path, workspace string, body any) *http.Request {
	req := withURLParam(newRequest(method, path, body), "id", workspace)
	req.Header.Set("X-Workspace-ID", workspace)
	return req
}

func TestWorkflowCutoverFreezesOldIssuesAndDefaultsOnlyNewIssues(t *testing.T) {
	workspace := dbfx.Workspace(t, "Workflow cutover", fmt.Sprintf("workflow-cutover-%d", time.Now().UnixNano()))
	dbfx.Member(t, workspace, testUserID, "owner")
	old := dbfx.Issue(t, "Old issue", testutil.Cols{"workspace_id": workspace})
	runtime := dbfx.Runtime(t, "cutover runtime", testutil.Cols{"workspace_id": workspace})
	agent := dbfx.Agent(t, "cutover agent", runtime, testutil.Cols{"workspace_id": workspace})
	queued := dbfx.Task(t, agent, testutil.Cols{"issue_id": old, "runtime_id": runtime,
		"status": "dispatched", "dispatched_at": testutil.Raw("now()")})
	failed := dbfx.Task(t, agent, testutil.Cols{"issue_id": old, "runtime_id": runtime,
		"status": "failed", "started_at": testutil.Raw("now()-interval '2 hours'"),
		"completed_at": testutil.Raw("now()-interval '1 hour'")})
	recovery := dbfx.Insert(t, "comment", testutil.Cols{
		"workspace_id": workspace, "issue_id": old, "author_type": "system",
		"author_id": testUserID, "content": "Retained delegated failure", "type": "progress_update", "source_task_id": failed,
	})
	firstSkill := workflowCutoverTestSkill(t, workspace, "Policy one")
	cutover := workflowCutoverRequest(http.MethodPost, "/api/workspaces/"+workspace+"/workflow-cutover", workspace,
		map[string]any{"skill_id": firstSkill})
	testutil.Call(t, testHandler.CutoverWorkspaceWorkflowDefault, cutover).Want(http.StatusConflict)
	var markerCount int
	dbfx.QueryRow(t, `SELECT count(*) FROM workspace WHERE id=$1 AND workflow_cutover_at IS NOT NULL`, workspace).Scan(&markerCount)
	if markerCount != 0 {
		t.Fatal("rejected cutover wrote an activation marker")
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='queued',dispatched_at=NULL WHERE id=$1`, queued)

	testutil.Call(t, testHandler.CutoverWorkspaceWorkflowDefault,
		workflowCutoverRequest(http.MethodPost, "/", workspace, map[string]any{"skill_id": firstSkill})).Want(http.StatusCreated)
	var frozen, claimable bool
	dbfx.QueryRow(t, `SELECT workflow_frozen FROM issue WHERE id=$1`, old).Scan(&frozen)
	dbfx.QueryRow(t, `SELECT workflow_task_claimable($1::uuid,$2::uuid)`, queued, old).Scan(&claimable)
	if !frozen || claimable {
		t.Fatalf("old issue frozen=%t claimable=%t", frozen, claimable)
	}
	var status string
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, queued).Scan(&status)
	if status != "queued" {
		t.Fatalf("cutover changed pending task status to %q", status)
	}
	for _, forbidden := range []string{"dispatched", "failed"} {
		_, err := testPool.Exec(context.Background(), `UPDATE agent_task_queue SET status=$2 WHERE id=$1`, queued, forbidden)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != "issue_workflow_frozen" {
			t.Fatalf("frozen task transition to %s: %v, want frozen constraint", forbidden, err)
		}
	}
	// Time and runtime health may advance while an issue is frozen. Neither
	// the queued-expiry sweeper nor the daemon may reinterpret that wait.
	dbfx.Exec(t, `UPDATE agent_task_queue SET created_at=now()-interval '2 hours' WHERE id=$1`, queued)
	dbfx.Exec(t, `UPDATE agent_runtime SET status='offline',last_seen_at=now()-interval '2 hours' WHERE id=$1`, runtime)
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := testHandler.Queries.WithTx(tx).ExpireStaleQueuedTasks(context.Background(), db.ExpireStaleQueuedTasksParams{
		ReconnectGraceSecs: 60, MaxPerTick: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(context.Background(), `SELECT status FROM agent_task_queue WHERE id=$1`, queued).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("frozen queued task expired as %q", status)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}

	newIssue := dbfx.Issue(t, "New policy one issue", testutil.Cols{"workspace_id": workspace})
	var firstPolicy []byte
	dbfx.QueryRow(t, `SELECT workflow_policy,workflow_frozen FROM issue WHERE id=$1`, newIssue).Scan(&firstPolicy, &frozen)
	if frozen || len(firstPolicy) == 0 {
		t.Fatalf("new issue frozen=%t policy bytes=%d", frozen, len(firstPolicy))
	}
	first, err := testHandler.TaskService.DecodeIssueWorkflowPolicy(firstPolicy)
	if err != nil || first == nil {
		t.Fatalf("decode new issue policy: %v", err)
	}
	// A task status writer must fail quickly behind an issue-first transaction,
	// rather than wait with the reverse lock order and deadlock deletion.
	newTask := dbfx.Task(t, agent, testutil.Cols{"issue_id": newIssue, "runtime_id": runtime, "status": "queued"})
	lockedIssue, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lockedIssue.Rollback(context.Background())
	if _, err := lockedIssue.Exec(context.Background(), `SELECT id FROM issue WHERE id=$1 FOR UPDATE`, newIssue); err != nil {
		t.Fatal(err)
	}
	_, err = testPool.Exec(context.Background(), `UPDATE agent_task_queue SET status='failed' WHERE id=$1`, newTask)
	var busy *pgconn.PgError
	if !errors.As(err, &busy) || busy.Code != "55P03" {
		t.Fatalf("task status behind issue lock = %v, want lock-not-available", err)
	}
	if err := lockedIssue.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}

	secondSkill := workflowCutoverTestSkill(t, workspace, "Policy two")
	testutil.Call(t, testHandler.UpdateWorkspaceWorkflowDefault,
		workflowCutoverRequest(http.MethodPut, "/", workspace, map[string]any{"skill_id": secondSkill})).Want(http.StatusOK)
	var unchanged []byte
	dbfx.QueryRow(t, `SELECT workflow_policy FROM issue WHERE id=$1`, newIssue).Scan(&unchanged)
	if string(unchanged) != string(firstPolicy) {
		t.Fatal("new default rewrote an existing issue")
	}
	laterIssue := dbfx.Issue(t, "New policy two issue", testutil.Cols{"workspace_id": workspace})
	var laterRaw []byte
	dbfx.QueryRow(t, `SELECT workflow_policy FROM issue WHERE id=$1`, laterIssue).Scan(&laterRaw)
	later, err := testHandler.TaskService.DecodeIssueWorkflowPolicy(laterRaw)
	if err != nil || later == nil || later.Version == first.Version {
		t.Fatalf("later issue policy=%v err=%v", later, err)
	}

	migration := map[string]any{"skill_id": secondSkill, "reason": "Move remaining work to approved workflow",
		"reconciliation": "Prior queued task has not run; continue from the retained issue description."}
	badMigration := map[string]any{"skill_id": secondSkill, "reason": migration["reason"],
		"reconciliation": migration["reconciliation"], "reopen_to": "todo"}
	badRequest := withURLParam(workflowCutoverRequest(http.MethodPost, "/", workspace, badMigration), "id", old)
	testutil.Call(t, testHandler.MigrateIssueWorkflow, badRequest).Want(http.StatusBadRequest)
	request := workflowCutoverRequest(http.MethodPost, "/api/issues/"+old+"/workflow-migrate", workspace, migration)
	request = withURLParam(request, "id", old)
	testutil.Call(t, testHandler.MigrateIssueWorkflow, request).Want(http.StatusOK)
	var migrated bool
	dbfx.QueryRow(t, `SELECT NOT workflow_frozen AND workflow_migrated_at IS NOT NULL FROM issue WHERE id=$1`, old).Scan(&migrated)
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, queued).Scan(&status)
	if !migrated || status != "cancelled" {
		t.Fatalf("migration attestation=%t old task=%s", migrated, status)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM comment WHERE id=$1 AND recovery_settled_at IS NOT NULL`, recovery); got != 1 {
		t.Fatal("migration left old delegated recovery executable")
	}
	var details []byte
	dbfx.QueryRow(t, `SELECT details FROM activity_log WHERE issue_id=$1 AND action='workflow_migrated'`, old).Scan(&details)
	var history map[string]any
	if err := json.Unmarshal(details, &history); err != nil {
		t.Fatal(err)
	}
	if history["reason"] != migration["reason"] || history["reconciliation"] != migration["reconciliation"] {
		t.Fatalf("migration lost reconciliation: %s", details)
	}
	if dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND status IN ('queued','dispatched','running')`, old) != 0 {
		t.Fatal("migration automatically dispatched old work")
	}
}

func TestWorkflowCutoverAllowsExplicitIssueAndWorkspaceDeletion(t *testing.T) {
	newFrozenWorkspace := func(label string) (string, string, string) {
		workspace := dbfx.Workspace(t, label, fmt.Sprintf("freeze-delete-%d", time.Now().UnixNano()))
		dbfx.Member(t, workspace, testUserID, "owner")
		parent := dbfx.Issue(t, label+" parent", testutil.Cols{"workspace_id": workspace})
		child := dbfx.Issue(t, label+" child", testutil.Cols{"workspace_id": workspace, "parent_issue_id": parent})
		comment := dbfx.Insert(t, "comment", testutil.Cols{
			"workspace_id": workspace, "issue_id": parent, "author_type": "member", "author_id": testUserID,
			"content": label + " comment", "type": "comment",
		})
		dbfx.Insert(t, "comment", testutil.Cols{
			"workspace_id": workspace, "issue_id": parent, "author_type": "member", "author_id": testUserID,
			"content": label + " reply", "type": "comment", "parent_id": comment,
		})
		skill := workflowCutoverTestSkill(t, workspace, label+" policy")
		testutil.Call(t, testHandler.CutoverWorkspaceWorkflowDefault,
			workflowCutoverRequest(http.MethodPost, "/", workspace, map[string]any{"skill_id": skill})).Want(http.StatusCreated)
		return workspace, parent, child
	}
	workspace, parent, child := newFrozenWorkspace("Issue delete")
	issueDelete := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+parent, nil), "id", parent)
	issueDelete.Header.Set("X-Workspace-ID", workspace)
	testutil.Call(t, testHandler.DeleteIssue, issueDelete).Want(http.StatusNoContent)
	var remainingParent *string
	dbfx.QueryRow(t, `SELECT parent_issue_id FROM issue WHERE id=$1`, child).Scan(&remainingParent)
	if remainingParent != nil {
		t.Fatalf("frozen child still attached to deleted parent: %v", *remainingParent)
	}

	workspace, _, _ = newFrozenWorkspace("Workspace delete")
	workspaceDelete := withURLParam(newRequest(http.MethodDelete, "/api/workspaces/"+workspace, nil), "id", workspace)
	workspaceDelete.Header.Set("X-Workspace-ID", workspace)
	testutil.Call(t, testHandler.DeleteWorkspace, workspaceDelete).Want(http.StatusNoContent)
	if dbfx.Count(t, `SELECT count(*) FROM workspace WHERE id=$1`, workspace) != 0 {
		t.Fatal("frozen workspace survived authorized deletion")
	}
}

func TestWorkflowCutoverMigratesTerminalLegacyOnlyWithExplicitReopen(t *testing.T) {
	workspace := dbfx.Workspace(t, "Terminal legacy migration", fmt.Sprintf("terminal-migrate-%d", time.Now().UnixNano()))
	dbfx.Member(t, workspace, testUserID, "owner")
	skill := workflowCutoverTestSkill(t, workspace, "Reopen terminal policy")
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": workspace, "key": "historical_done", "name": "Historical accepted",
		"category": "done", "color": "#123456",
	})
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": workspace, "key": "historical_closed", "name": "Historical closed",
		"category": "closed", "color": "#654321",
	})
	issues := map[string]string{}
	for _, status := range []string{"done", "cancelled", "historical_done", "historical_closed"} {
		issues[status] = dbfx.Issue(t, "Historical "+status, testutil.Cols{"workspace_id": workspace, "status": status})
	}
	humanFrozen := dbfx.Issue(t, "Human completion before explicit migration", testutil.Cols{
		"workspace_id": workspace, "status": "in_review",
	})
	accepted := dbfx.Issue(t, "Previously accepted policy", testutil.Cols{
		"workspace_id": workspace, "status": "todo", "workflow_policy": testutil.Raw(`'{"version":"prior-v1"}'::jsonb`),
	})
	var nextRevision int64
	dbfx.QueryRow(t, `SELECT revision+1 FROM issue WHERE id=$1`, accepted).Scan(&nextRevision)
	candidate := dbfx.Insert(t, "issue_workflow_candidate", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": accepted,
		"policy_version": "prior-v1", "digest": "accepted", "scope_digest": "accepted",
		"source_handoff_id": dbid.NewV7(), "source_task_id": dbid.NewV7(), "writer_task_id": dbid.NewV7(),
		"pr_set": testutil.Raw("'[]'::jsonb"),
	})
	dbfx.Insert(t, "issue_workflow_acceptance", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": accepted, "candidate_id": candidate,
		"mode": "human", "actor_type": "member", "actor_id": testUserID, "state": "accepted",
		"issue_revision": nextRevision, "policy_version": "prior-v1",
		"authority_snapshot": testutil.Raw("'{}'::jsonb"), "accepted_at": testutil.Raw("now()"),
	})
	dbfx.Exec(t, `UPDATE issue SET status='done',workflow_candidate_id=$2,revision=revision+1 WHERE id=$1`, accepted, candidate)
	testutil.Call(t, testHandler.CutoverWorkspaceWorkflowDefault,
		workflowCutoverRequest(http.MethodPost, "/", workspace, map[string]any{"skill_id": skill})).Want(http.StatusCreated)
	var acceptedFrozen bool
	dbfx.QueryRow(t, `SELECT workflow_frozen FROM issue WHERE id=$1`, accepted).Scan(&acceptedFrozen)
	if acceptedFrozen {
		t.Fatal("previously accepted enrolled issue was frozen")
	}
	humanDone := withURLParam(newRequest(http.MethodPut, "/api/issues/"+humanFrozen,
		map[string]any{"status": "done"}), "id", humanFrozen)
	humanDone.Header.Set("X-Workspace-ID", workspace)
	testutil.Call(t, testHandler.UpdateIssue, humanDone).Want(http.StatusOK)
	migrateHuman := withURLParam(workflowCutoverRequest(http.MethodPost, "/", workspace,
		map[string]any{"skill_id": skill, "reason": "Resume the work after QA",
			"reconciliation": "The human Done decision remains in history.", "reopen_to": "todo"}), "id", humanFrozen)
	testutil.Call(t, testHandler.MigrateIssueWorkflow, migrateHuman).Want(http.StatusOK)
	var humanStillDone bool
	dbfx.QueryRow(t, `SELECT workflow_human_last_done($1)`, humanFrozen).Scan(&humanStillDone)
	if humanStillDone || dbfx.Count(t, `SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action='workflow_human_status_decision'`, humanFrozen) != 2 {
		t.Fatal("explicit migration did not supersede the frozen human Done decision")
	}
	for _, status := range []string{"done", "cancelled", "historical_done", "historical_closed"} {
		issue := issues[status]
		request := func(reopen string) *http.Request {
			body := map[string]any{"skill_id": skill, "reason": "Explicitly resume historical work",
				"reconciliation": "Historical completion remains in activity and task history."}
			if reopen != "" {
				body["reopen_to"] = reopen
			}
			return withURLParam(workflowCutoverRequest(http.MethodPost, "/", workspace, body), "id", issue)
		}
		testutil.Call(t, testHandler.MigrateIssueWorkflow, request("")).Want(http.StatusBadRequest)
		testutil.Call(t, testHandler.MigrateIssueWorkflow, request("done")).Want(http.StatusBadRequest)
		testutil.Call(t, testHandler.MigrateIssueWorkflow, request("todo")).Want(http.StatusOK)
		var got string
		var frozen bool
		dbfx.QueryRow(t, `SELECT status,workflow_frozen FROM issue WHERE id=$1`, issue).Scan(&got, &frozen)
		if got != "todo" || frozen {
			t.Fatalf("terminal %s migration status=%s frozen=%t", status, got, frozen)
		}
	}
}

func TestEnrolledIssueMachineCompletionReportsAcceptanceConflict(t *testing.T) {
	workspace := dbfx.Workspace(t, "Workflow completion conflict", fmt.Sprintf("workflow-acceptance-%d", time.Now().UnixNano()))
	dbfx.Member(t, workspace, testUserID, "owner")
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": workspace, "key": "accepted_custom", "name": "Accepted custom",
		"category": "done", "color": "#123456",
	})
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": workspace, "key": "closed_custom", "name": "Closed custom",
		"category": "closed", "color": "#654321",
	})
	issue := dbfx.Issue(t, "Enrolled without acceptance", testutil.Cols{
		"workspace_id": workspace, "status": "in_review",
		"workflow_policy": testutil.Raw(`'{"version":"pinned-v1"}'::jsonb`),
	})
	for _, target := range []string{"done", "accepted_custom"} {
		req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issue,
			map[string]any{"status": target}), "id", issue)
		req.Header.Set("X-Workspace-ID", workspace)
		req.Header.Set("X-Actor-Source", "cloud_pat")
		testutil.Call(t, testHandler.UpdateIssue, req).Want(http.StatusConflict)
		batch := newRequest(http.MethodPatch, "/api/issues/batch", map[string]any{
			"issue_ids": []string{issue}, "updates": map[string]any{"status": target},
		})
		batch.Header.Set("X-Workspace-ID", workspace)
		batch.Header.Set("X-Actor-Source", "cloud_pat")
		testutil.Call(t, testHandler.BatchUpdateIssues, batch).Want(http.StatusConflict)
	}
	var status string
	dbfx.QueryRow(t, `SELECT status FROM issue WHERE id=$1`, issue).Scan(&status)
	if status != "in_review" {
		t.Fatalf("rejected completion changed status to %s", status)
	}
	for _, target := range []string{"cancelled", "closed_custom"} {
		cancelIssue := dbfx.Issue(t, "Unaccepted cancellation "+target, testutil.Cols{
			"workspace_id": workspace, "status": "in_review",
			"workflow_policy": testutil.Raw(`'{"version":"pinned-v1"}'::jsonb`),
		})
		req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+cancelIssue,
			map[string]any{"status": target}), "id", cancelIssue)
		req.Header.Set("X-Workspace-ID", workspace)
		testutil.Call(t, testHandler.UpdateIssue, req).Want(http.StatusOK)
		var got string
		dbfx.QueryRow(t, `SELECT status FROM issue WHERE id=$1`, cancelIssue).Scan(&got)
		if got != target {
			t.Fatalf("unaccepted cancellation got %q, want %q", got, target)
		}
	}
}
