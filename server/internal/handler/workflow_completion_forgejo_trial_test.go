//go:build workflowintegration

package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const (
	completionTrialOrigin = "https://git.thn.janezstupar.com"
	completionTrialRepo   = "Janez/test"
	completionTrialBase   = "main"
)

// TestMain creates a DB fixture before individual tests run. Refuse a trial
// invocation pointed at a normal checkout DB before TestMain can touch it.
func init() {
	if os.Getenv("MULTICA_RUN_FORGEJO_WORKFLOW_TRIAL") != "1" {
		return
	}
	if os.Getenv("MULTICA_FORGEJO_TRIAL_REPO") != completionTrialRepo ||
		os.Getenv("MULTICA_FORGEJO_TRIAL_ALLOW_MERGE") != "1" {
		panic("Forgejo workflow trial requires the explicit Janez/test repository and merge allowance")
	}
	want := os.Getenv("MULTICA_FORGEJO_TRIAL_DATABASE")
	parsed, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	allowedHost := func(host string, port uint16) bool {
		return (host == "127.0.0.1" || host == "localhost") && port == 15440
	}
	if err != nil || !(strings.HasPrefix(want, "multica_workflow_trial_") ||
		strings.HasPrefix(want, "multica_mica_completion_clean_")) ||
		parsed.ConnConfig.Database != want ||
		!allowedHost(parsed.ConnConfig.Host, parsed.ConnConfig.Port) {
		panic("Forgejo workflow trial requires its explicitly named loopback managed database on port 15440")
	}
	for _, fallback := range parsed.ConnConfig.Fallbacks {
		if fallback == nil || !allowedHost(fallback.Host, fallback.Port) {
			panic("Forgejo workflow trial database fallback must use the same loopback managed port")
		}
	}
}

type completionTrialForgejo struct {
	token  string
	client *http.Client
}

type completionTrialPull struct {
	Number         int64  `json:"number"`
	Index          int64  `json:"index"`
	HTMLURL        string `json:"html_url"`
	Title          string `json:"title"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	ContentVersion int64  `json:"content_version"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

func (p completionTrialPull) number() int64 {
	if p.Number != 0 {
		return p.Number
	}
	return p.Index
}

// A credential file is a single raw token or a MULTICA_API_TOKEN=value line.
// Never report its contents or include the token in command arguments.
func completionTrialToken(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("MULTICA_FORGEJO_TRIAL_TOKEN_FILE is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read trial credential file")
	}
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, "export ") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "export "))
	}
	if key, rest, found := strings.Cut(value, "="); found {
		if key != "MULTICA_API_TOKEN" {
			return "", fmt.Errorf("trial credential file has an unexpected key")
		}
		value = rest
	}
	if len(value) >= 2 && (value[0] == '\'' && value[len(value)-1] == '\'' ||
		value[0] == '"' && value[len(value)-1] == '"') {
		value = value[1 : len(value)-1]
	}
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("trial credential file is malformed")
	}
	return value, nil
}

// The trial API client intentionally drops provider error bodies: they can
// echo credentials or arbitrary repository content.
func (f completionTrialForgejo) call(ctx context.Context, method, path string, body, out any) (int, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode trial request")
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, completionTrialOrigin+path, payload)
	if err != nil {
		return 0, fmt.Errorf("construct trial request")
	}
	req.Header.Set("Authorization", "token "+f.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("trial provider request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode trial provider response")
		}
	}
	return resp.StatusCode, nil
}

func completionTrialStatus(t *testing.T, operation string, got int, err error, want ...int) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
	for _, status := range want {
		if got == status {
			return
		}
	}
	t.Fatalf("%s: HTTP %d, expected %v", operation, got, want)
}

type completionTrialArtifact struct {
	branch, marker, markerText, title string
	branchAttempted, pullAttempted    bool
	pullNumber                        int64
}

// Cleanup checks the unique marker before removing it from main. A merged
// trial necessarily leaves a cleanup commit that deletes that one file.
func (a *completionTrialArtifact) cleanup(t *testing.T, f completionTrialForgejo) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := "/api/v1/repos/" + completionTrialRepo
	if a.pullAttempted && a.pullNumber == 0 {
		var pulls []completionTrialPull
		status, err := f.call(ctx, http.MethodGet, base+"/pulls?state=all&limit=50", nil, &pulls)
		if err != nil || status != http.StatusOK {
			t.Errorf("trial cleanup could not discover an ambiguous PR create; branch retained")
			return
		}
		for _, pull := range pulls {
			if pull.Head.Ref == a.branch && pull.Title == a.title {
				a.pullNumber = pull.number()
				break
			}
		}
		if a.pullNumber == 0 {
			t.Errorf("trial cleanup could not identify an ambiguous PR create in the bounded listing; branch retained")
			return
		}
	}
	if a.pullNumber != 0 {
		var pull completionTrialPull
		path := fmt.Sprintf("%s/pulls/%d", base, a.pullNumber)
		status, err := f.call(ctx, http.MethodGet, path, nil, &pull)
		if err != nil || status != http.StatusOK || pull.Head.Ref != a.branch {
			t.Errorf("trial cleanup could not verify its PR; branch retained")
			return
		}
		if pull.State == "open" {
			edit := map[string]any{"state": "closed"}
			if pull.ContentVersion > 0 {
				edit["content_version"] = pull.ContentVersion
			}
			status, err = f.call(ctx, http.MethodPatch, path, edit, nil)
			if err != nil || status != http.StatusOK && status != http.StatusCreated {
				t.Errorf("trial cleanup could not close its open PR (HTTP %d); branch retained", status)
				return
			}
			var closed completionTrialPull
			status, err = f.call(ctx, http.MethodGet, path, nil, &closed)
			if err != nil || status != http.StatusOK || closed.Head.Ref != a.branch ||
				closed.State != "closed" || closed.Merged {
				t.Errorf("trial cleanup could not verify the closed PR (HTTP %d); branch retained", status)
				return
			}
		}
		if pull.Merged {
			var marker struct {
				SHA     string `json:"sha"`
				Content string `json:"content"`
			}
			markerPath := base + "/contents/" + a.marker
			status, err = f.call(ctx, http.MethodGet, markerPath+"?ref="+completionTrialBase, nil, &marker)
			if err != nil || status != http.StatusOK {
				t.Errorf("trial cleanup could not read merged marker")
			} else if decoded, decodeErr := base64.StdEncoding.DecodeString(marker.Content); decodeErr != nil ||
				string(decoded) != a.markerText || marker.SHA == "" {
				t.Errorf("trial cleanup found a marker mismatch; main left untouched")
			} else {
				status, err = f.call(ctx, http.MethodDelete, markerPath, map[string]string{
					"branch": completionTrialBase, "sha": marker.SHA,
					"message": "test: remove workflow completion trial marker",
				}, nil)
				if err != nil || status != http.StatusOK {
					t.Errorf("trial cleanup could not delete its merged marker from main (HTTP %d)", status)
				}
			}
		}
	}
	if a.branchAttempted {
		var branch struct {
			Name string `json:"name"`
		}
		status, err := f.call(ctx, http.MethodGet, base+"/branches/"+a.branch, nil, &branch)
		if err != nil || status != http.StatusOK && status != http.StatusNotFound ||
			status == http.StatusOK && branch.Name != a.branch {
			t.Errorf("trial cleanup could not verify its unique branch; branch retained")
			return
		}
		if status == http.StatusNotFound {
			return
		}
		status, err = f.call(ctx, http.MethodDelete, base+"/branches/"+a.branch, nil, nil)
		if err != nil || status != http.StatusNoContent && status != http.StatusOK && status != http.StatusNotFound {
			t.Errorf("trial cleanup could not delete its unique branch (HTTP %d)", status)
		}
	}
}

// TestWorkflowFormat2RealForgejoHeldMergeAndOutcome is an explicitly gated,
// disposable provider boundary trial. It scripts task completion after native
// claim/start; it does not execute a model or re-prove model session continuity.
func TestWorkflowFormat2RealForgejoHeldMergeAndOutcome(t *testing.T) {
	if os.Getenv("MULTICA_RUN_FORGEJO_WORKFLOW_TRIAL") != "1" {
		t.Skip("set MULTICA_RUN_FORGEJO_WORKFLOW_TRIAL=1 for the isolated Forgejo trial")
	}
	if os.Getenv("MULTICA_FORGEJO_TRIAL_REPO") != completionTrialRepo ||
		os.Getenv("MULTICA_FORGEJO_TRIAL_ALLOW_MERGE") != "1" {
		t.Fatal("trial requires the explicit Janez/test repository and merge allowance")
	}
	if testHandler == nil || dbfx == nil {
		t.Fatal("trial requires prepared handler DB fixtures")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var currentDB string
	if err := testPool.QueryRow(ctx, `SELECT current_database()`).Scan(&currentDB); err != nil ||
		currentDB != os.Getenv("MULTICA_FORGEJO_TRIAL_DATABASE") {
		t.Fatal("trial database identity changed")
	}

	// Build all local authority prerequisites before reading a credential or
	// contacting Forgejo. The policy deliberately omits independent review;
	// this trial targets held delivery and outcome completion only.
	withVCSBox(t)
	runtimeID := dbfx.Runtime(t, "workflow completion trial runtime")
	writerAgent := dbfx.Agent(t, "workflow completion trial agent", runtimeID)
	outcomeAgent := dbfx.Agent(t, "workflow completion trial outcome agent", runtimeID)
	dbfx.Insert(t, "issue_status", testutil.Cols{
		"workspace_id": testWorkspaceID, "key": "pr_ready", "name": "PR Ready",
		"category": "started", "color": "#22c55e", "position": 1,
	})
	issueID := dbfx.Issue(t, "Disposable real Forgejo workflow completion trial", testutil.Cols{
		"status": "in_review", "assignee_type": "member", "assignee_id": testUserID,
	})
	for _, table := range workflowLedgerTables {
		dbfx.Cleanup(t, fmt.Sprintf(`DELETE FROM %s WHERE issue_id=$1`, table), issueID)
	}
	dbfx.Cleanup(t, `DELETE FROM agent_task_queue WHERE issue_id=$1`, issueID)
	skillID := insertCompleteWorkflowSkill(t, "---\nname: workflow-completion-trial\n---\n\nDisposable format-2 provider trial")
	policyJSON := fmt.Sprintf(`{"format_version":2,"accepted_status_key":"pr_ready","outcome_agent_id":%q,"review":{"required":false},"human":{"accept_roles":["owner"],"delivery":"merge"},"delivery":{"merge_method":"squash"}}`, outcomeAgent)
	dbfx.Insert(t, "skill_file", testutil.Cols{"skill_id": skillID, "path": "runtime/policy.json", "content": policyJSON})
	var pinned service.IssueWorkflowPolicy
	enrollWorkflowPolicy(t, issueID, skillID).Want(http.StatusCreated).JSON(&pinned)

	token, err := completionTrialToken(os.Getenv("MULTICA_FORGEJO_TRIAL_TOKEN_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	forgejo := completionTrialForgejo{token: token, client: &http.Client{Timeout: 10 * time.Second}}
	base := "/api/v1/repos/" + completionTrialRepo
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	status, err := forgejo.call(ctx, http.MethodGet, base, nil, &repo)
	completionTrialStatus(t, "verify trial repository", status, err, http.StatusOK)
	if repo.DefaultBranch != completionTrialBase {
		t.Fatal("trial repository default branch is not main")
	}

	suffix := strings.ReplaceAll(uuidToString(dbid.NewV7()), "-", "")
	artifact := &completionTrialArtifact{
		branch:     "workflow-completion-trial-" + suffix,
		marker:     "workflow-trials/format2-" + suffix + ".txt",
		markerText: "Disposable Multica format-2 workflow trial " + suffix + "\n",
		title:      "WIP: disposable workflow completion trial " + suffix,
	}
	status, err = forgejo.call(ctx, http.MethodGet, base+"/branches/"+artifact.branch, nil, nil)
	completionTrialStatus(t, "verify unique trial branch does not exist", status, err, http.StatusNotFound)
	artifact.branchAttempted = true
	t.Cleanup(func() { artifact.cleanup(t, forgejo) })
	status, err = forgejo.call(ctx, http.MethodPost, base+"/branches", map[string]string{
		"old_branch_name": completionTrialBase, "new_branch_name": artifact.branch,
	}, nil)
	completionTrialStatus(t, "create unique trial branch", status, err, http.StatusCreated)
	status, err = forgejo.call(ctx, http.MethodPost, base+"/contents/"+artifact.marker,
		map[string]string{"branch": artifact.branch, "message": "test: add disposable workflow completion marker",
			"content": base64.StdEncoding.EncodeToString([]byte(artifact.markerText))}, nil)
	completionTrialStatus(t, "create trial marker", status, err, http.StatusCreated)
	artifact.pullAttempted = true
	var pull completionTrialPull
	status, err = forgejo.call(ctx, http.MethodPost, base+"/pulls", map[string]string{
		"base": completionTrialBase, "head": artifact.branch, "title": artifact.title,
		"body": "Disposable format-2 held delivery and outcome trial; cleanup removes this marker.",
	}, &pull)
	completionTrialStatus(t, "create trial PR", status, err, http.StatusCreated)
	artifact.pullNumber = pull.number()
	repoURL := completionTrialOrigin + "/" + completionTrialRepo
	prURL := fmt.Sprintf("%s/pulls/%d", repoURL, artifact.pullNumber)
	if artifact.pullNumber < 1 || pull.HTMLURL != prURL || pull.Head.Ref != artifact.branch ||
		(len(pull.Head.SHA) != 40 && len(pull.Head.SHA) != 64) || pull.Merged || pull.State != "open" {
		t.Fatal("trial PR identity or exact head did not match its unique branch")
	}
	t.Logf("created disposable PR %d at exact head %s", artifact.pullNumber, pull.Head.SHA)

	sealed, err := testHandler.sealVCSSecret(token)
	if err != nil {
		t.Fatal("could not seal trial credential for fixture")
	}
	bindingID := dbfx.Insert(t, "vcs_connection", testutil.Cols{
		"workspace_id": testWorkspaceID, "provider": "forgejo", "instance_url": completionTrialOrigin,
		"account_login": "workflow completion trial", "access_token_encrypted": sealed,
		"webhook_secret_encrypted": sealed,
	})
	prID := dbfx.Insert(t, "vcs_pull_request", testutil.Cols{
		"workspace_id": testWorkspaceID, "connection_id": bindingID, "provider": "forgejo",
		"repo_owner": "Janez", "repo_name": "test", "pr_number": artifact.pullNumber,
		"title": artifact.title, "state": "open", "html_url": prURL,
		"branch": artifact.branch, "head_sha": pull.Head.SHA,
		"pr_created_at": testutil.Raw("now()"), "pr_updated_at": testutil.Raw("now()"),
	})
	dbfx.InsertNoID(t, "issue_vcs_pull_request", testutil.Cols{
		"issue_id": issueID, "pull_request_id": prID,
	}, "issue_id=$1 AND pull_request_id=$2", issueID, prID)
	writerTaskID := dbfx.Task(t, writerAgent, testutil.Cols{
		"issue_id": issueID, "runtime_id": runtimeID, "status": "completed",
		"session_id": "scripted-writer-session", "started_at": testutil.Raw("now()"),
		"completed_at": testutil.Raw("now()"),
	})
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal("could not read trial issue")
	}
	prSet, _ := json.Marshal([]service.HandoffCandidate{{
		RepositoryURL: repoURL, PRURL: prURL, Branch: artifact.branch,
		CommitSHA: pull.Head.SHA, Draft: true,
	}})
	candidateID := dbfx.Insert(t, "issue_workflow_candidate", testutil.Cols{
		"id":           dbid.NewV7(),
		"workspace_id": testWorkspaceID, "issue_id": issueID,
		"policy_version": pinned.Version, "digest": "trial-" + suffix,
		"scope_digest":      service.WorkflowScopeDigest(issue, pinned.Version),
		"source_handoff_id": dbid.NewV7(), "source_task_id": writerTaskID,
		"writer_task_id": writerTaskID, "pr_set": prSet,
	})
	dbfx.Exec(t, `UPDATE issue SET workflow_candidate_id=$2 WHERE id=$1`, issueID, candidateID)
	issue, err = testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal("could not read candidate issue")
	}
	svc := testHandler.workflowAuthorityService()
	incomplete := false
	state, err := svc.AcceptWorkflow(ctx, parseUUID(testWorkspaceID), parseUUID(issueID),
		service.WorkflowActor{Type: "member", ID: testUserID}, service.WorkflowAcceptanceInput{
			CandidateID: candidateID, ExpectedRevision: issue.Revision,
			OutcomeComplete: &incomplete, HoldDelivery: true,
		})
	if err != nil || state != "accepted" {
		t.Fatalf("held exact-head acceptance failed: state=%q err=%v", state, err)
	}
	accepted, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil || accepted.Status != "pr_ready" {
		t.Fatal("acceptance did not move to PR Ready")
	}
	workflow, err := svc.ReadState(ctx, accepted.WorkspaceID, accepted.ID, service.WorkflowActor{Type: "member", ID: testUserID})
	if err != nil || workflow.Acceptance == nil || !workflow.Acceptance.HoldDelivery || workflow.Acceptance.OutcomeComplete {
		t.Fatal("held, incomplete acceptance was not persisted")
	}
	acceptanceID := workflow.Acceptance.ID

	worker := NewWorkflowDeliveryWorker(testHandler)
	worked, err := worker.ProcessNext(ctx)
	if err != nil || !worked {
		t.Fatalf("real provider readiness failed: worked=%v err=%v", worked, err)
	}
	if dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1 AND readiness_done_at IS NOT NULL AND merged_at IS NULL AND status='pending'`, issueID) != 1 {
		t.Fatal("held delivery did not persist ready-but-unmerged state")
	}
	var observed completionTrialPull
	status, err = forgejo.call(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", base, artifact.pullNumber), nil, &observed)
	completionTrialStatus(t, "verify held PR", status, err, http.StatusOK)
	if observed.Merged || observed.State != "open" || observed.Head.SHA != pull.Head.SHA || strings.HasPrefix(observed.Title, "WIP:") {
		t.Fatal("held PR is not open, ready, and at the accepted head")
	}
	worker = NewWorkflowDeliveryWorker(testHandler)
	_, err = worker.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("reconstructed worker held observation failed: %v", err)
	}
	observed = completionTrialPull{}
	status, err = forgejo.call(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", base, artifact.pullNumber), nil, &observed)
	completionTrialStatus(t, "verify reconstructed hold", status, err, http.StatusOK)
	if observed.Merged || observed.State != "open" || observed.Head.SHA != pull.Head.SHA {
		t.Fatal("reconstructed worker crossed hold")
	}
	workflow, err = svc.ReadState(ctx, accepted.WorkspaceID, accepted.ID, service.WorkflowActor{Type: "member", ID: testUserID})
	if err != nil || workflow.Acceptance == nil || !workflow.Acceptance.HoldDelivery {
		t.Fatal("held acceptance did not survive worker reconstruction")
	}
	changed, err := svc.ChangeCompletion(ctx, accepted.WorkspaceID, accepted.ID, parseUUID(acceptanceID),
		service.WorkflowActor{Type: "member", ID: testUserID}, "release",
		service.WorkflowCompletionActionInput{CandidateID: candidateID, ExpectedRevision: accepted.Revision,
			Reason: "Held provider readiness verified; release the exact accepted PR."})
	if err != nil || !changed {
		t.Fatalf("release held delivery: changed=%v err=%v", changed, err)
	}
	worker = NewWorkflowDeliveryWorker(testHandler)
	worked, err = worker.ProcessNext(ctx)
	if err != nil || !worked {
		t.Fatalf("real exact-head merge failed: worked=%v err=%v", worked, err)
	}
	observed = completionTrialPull{}
	status, err = forgejo.call(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", base, artifact.pullNumber), nil, &observed)
	completionTrialStatus(t, "verify merged PR", status, err, http.StatusOK)
	if !observed.Merged || observed.State != "closed" || observed.Head.SHA != pull.Head.SHA ||
		dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1 AND status='delivered' AND merged_at IS NOT NULL`, issueID) != 1 {
		t.Fatal("provider merge and durable exact-head delivery did not agree")
	}
	ready, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil || ready.Status != "pr_ready" {
		t.Fatal("merged PR prematurely completed the issue")
	}
	workflow, err = svc.ReadState(ctx, ready.WorkspaceID, ready.ID, service.WorkflowActor{Type: "member", ID: testUserID})
	if err != nil || workflow.Acceptance == nil || workflow.Acceptance.OutcomeTaskID == "" ||
		!workflow.Acceptance.OutcomeTaskActive || workflow.Acceptance.OutcomeComplete {
		t.Fatal("bound outcome task was not queued after merge")
	}
	outcomeTaskID := workflow.Acceptance.OutcomeTaskID
	claimed, err := svc.Tasks.ClaimTask(ctx, parseUUID(outcomeAgent))
	if err != nil || claimed == nil || claimed.ID != parseUUID(outcomeTaskID) {
		t.Fatalf("native bound outcome claim failed: %v", err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID: parseUUID(runtimeID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatal("could not load outcome runtime")
	}
	profile, profileID, err := testHandler.bindClaimIssueWorkflowProfile(ctx, *claimed, runtime)
	if err != nil || profile == nil || !profileID.Valid {
		t.Fatalf("native outcome profile capture failed: %v", err)
	}
	started, err := svc.Tasks.StartTask(ctx, claimed.ID)
	if err != nil || started == nil || started.Status != "running" {
		t.Fatalf("native bound outcome start failed: %v", err)
	}
	ready, err = testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal("could not read outcome revision")
	}
	changed, err = svc.ChangeCompletion(ctx, ready.WorkspaceID, ready.ID, parseUUID(acceptanceID),
		service.WorkflowActor{Type: "agent", ID: outcomeAgent, SourceTaskID: outcomeTaskID}, "complete",
		service.WorkflowCompletionActionInput{CandidateID: candidateID, ExpectedRevision: ready.Revision,
			Reason: "Scripted trial outcome check passed."})
	if err != nil || !changed {
		t.Fatalf("bound outcome acknowledgment failed: changed=%v err=%v", changed, err)
	}
	workflow, err = svc.ReadState(ctx, ready.WorkspaceID, ready.ID, service.WorkflowActor{Type: "member", ID: testUserID})
	if err != nil || workflow.Acceptance == nil || !workflow.Acceptance.OutcomePending || workflow.Acceptance.OutcomeComplete {
		t.Fatal("running task acknowledgment completed the outcome early")
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, outcomeTaskID)
	finalized, err := svc.FinalizeNextOutcomeAcknowledgment(ctx)
	if err != nil || !finalized {
		t.Fatalf("exact task outcome finalization failed: finalized=%v err=%v", finalized, err)
	}
	done, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil || done.Status != "done" {
		t.Fatal("issue did not become done after real merge and successful bound outcome task")
	}
	t.Logf("format-2 PR %d merged at accepted head; scripted bound outcome task %s completed", artifact.pullNumber, outcomeTaskID)
}
