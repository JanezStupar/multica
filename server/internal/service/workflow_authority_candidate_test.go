package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func bindWorkflowCandidateToGitHub(t *testing.T, f principalFixture, svc *WorkflowAuthorityService, issueID pgtype.UUID, candidate HandoffCandidate) {
	t.Helper()
	installationID := int64(92000000 + principalSeq.Add(1))
	f.Insert(t, "github_installation", testutil.Cols{
		"workspace_id": f.WorkspaceID, "installation_id": installationID,
		"account_login": "team", "account_type": "Organization",
	})
	prID := f.Insert(t, "github_pull_request", testutil.Cols{
		"workspace_id": f.WorkspaceID, "installation_id": installationID, "repo_owner": "team", "repo_name": "repo",
		"pr_number": 1, "title": "Original work", "state": "open", "html_url": candidate.PRURL,
		"branch": candidate.Branch, "pr_created_at": testutil.Raw("now()"), "pr_updated_at": testutil.Raw("now()"),
		"head_sha": candidate.CommitSHA,
	})
	f.InsertNoID(t, "issue_pull_request", testutil.Cols{"issue_id": util.UUIDToString(issueID), "pull_request_id": prID}, "issue_id=$1 AND pull_request_id=$2", issueID, prID)
	svc.ReviewVerifier = func(_ context.Context, evidence WorkflowReviewEvidenceInput) error {
		if util.UUIDToString(evidence.WorkspaceID) != f.WorkspaceID || len(evidence.PRs) != 1 || len(evidence.ReviewURLs) != 1 {
			return fmt.Errorf("unexpected provider review evidence shape: %+v", evidence)
		}
		pr := evidence.PRs[0]
		if pr.Provider != "github" || pr.PRURL != candidate.PRURL || pr.RepositoryURL != candidate.RepositoryURL ||
			pr.Owner != "team" || pr.Repo != "repo" || pr.Number != 1 || pr.ExpectedHeadSHA != candidate.CommitSHA ||
			evidence.ReviewURLs[0] != candidate.PRURL+"#review" {
			return fmt.Errorf("provider review proof did not bind to the exact candidate head: %+v", evidence)
		}
		return nil
	}
}

func TestCanonicalWorkflowCandidatesAcceptDraftAndReadyPRs(t *testing.T) {
	candidates := []HandoffCandidate{
		{RepositoryURL: "https://example.test/repo", PRURL: "https://example.test/repo/pull/2", Branch: "feature/ready", CommitSHA: strings.Repeat("A", 40), Draft: false},
		{RepositoryURL: "https://example.test/repo", PRURL: "https://example.test/repo/pull/1", Branch: "feature/draft", CommitSHA: strings.Repeat("b", 40), Draft: true},
	}
	ordered, _, err := canonicalWorkflowCandidates(candidates)
	if err != nil {
		t.Fatalf("candidate PR state rejected: %v", err)
	}
	if len(ordered) != 2 || !ordered[0].Draft || ordered[1].Draft {
		t.Fatalf("candidate readiness was not preserved after canonical ordering: %+v", ordered)
	}
	if ordered[0].CommitSHA != strings.Repeat("b", 40) {
		t.Fatalf("candidate SHA was not canonicalized: %q", ordered[0].CommitSHA)
	}
}

func TestWorkflowScopeDigestIncludesAcceptanceCriteria(t *testing.T) {
	issue := db.Issue{Title: "Keep the same objective", AcceptanceCriteria: []byte(`[{"text":"old criterion"}]`)}
	before := WorkflowScopeDigest(issue, "policy-v1")
	issue.AcceptanceCriteria = []byte(`[{"text":"new criterion"}]`)
	if after := WorkflowScopeDigest(issue, "policy-v1"); after == before {
		t.Fatal("acceptance criteria change did not change scope evidence")
	}
}

func TestWorkflowCandidateCodeSurfaceIgnoresReadiness(t *testing.T) {
	base := []HandoffCandidate{{RepositoryURL: "https://example.test/repo", PRURL: "https://example.test/repo/pull/1",
		Branch: "feature/work", CommitSHA: strings.Repeat("a", 40), Draft: true}}
	ready := append([]HandoffCandidate(nil), base...)
	ready[0].Draft = false
	if !sameWorkflowCandidateCodeSurface(base, ready) {
		t.Fatal("PR readiness change incorrectly changed the code candidate identity")
	}
}

func TestWorkflowExceptionsCanChangeAfterAcceptanceWithoutRewritingSnapshot(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t, true)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	complete := false
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
		ExpectedRevision: issue.Revision, OutcomeComplete: &complete}
	if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request); err != nil || state != "accepted" {
		t.Fatalf("accept candidate: state=%q error=%v", state, err)
	}
	var acceptanceID = dbid.NewV7()
	var beforeSnapshot []byte
	if err := f.Pool.QueryRow(ctx, `SELECT id,authority_snapshot FROM issue_workflow_acceptance
		WHERE issue_id=$1 AND candidate_id=$2 AND state='accepted' AND revoked_at IS NULL`,
		issueID, issue.WorkflowCandidateID).Scan(&acceptanceID, &beforeSnapshot); err != nil {
		t.Fatal(err)
	}
	acceptedIssue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	otherMember := f.member(t, "postacceptance-named-acceptor")
	exceptionID, err := svc.GrantException(ctx, issue.WorkspaceID, issueID, actor, WorkflowExceptionInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: acceptedIssue.Revision,
		Scope: "acceptance", GrantDetails: map[string]any{"human_actor_id": otherMember},
		Reason: "Delegate a bounded acceptance action.", Consequences: "The named member may accept only this candidate.",
	})
	if err != nil {
		t.Fatalf("grant scoped exception after acceptance: %v", err)
	}
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeException(ctx, current.WorkspaceID, issueID, parseTestUUID(t, exceptionID), actor,
		WorkflowExceptionRevokeInput{ExpectedRevision: current.Revision,
			Reason: "Delegation is no longer needed.", Consequences: "Only the original accepted authority remains."}); err != nil {
		t.Fatalf("revoke scoped exception after acceptance: %v", err)
	}
	var afterSnapshot []byte
	var acceptanceState string
	if err := f.Pool.QueryRow(ctx, `SELECT authority_snapshot,state FROM issue_workflow_acceptance WHERE id=$1`,
		acceptanceID).Scan(&afterSnapshot, &acceptanceState); err != nil {
		t.Fatal(err)
	}
	if acceptanceState != "accepted" || string(afterSnapshot) != string(beforeSnapshot) {
		t.Fatalf("exception change rewrote accepted authority snapshot: state=%s before=%s after=%s",
			acceptanceState, beforeSnapshot, afterSnapshot)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_exception WHERE id=$1 AND revoked_at IS NOT NULL`, parseTestUUID(t, exceptionID)); got != 1 {
		t.Fatalf("revoked exception history not retained: %d rows", got)
	}
}

func TestAcceptedCandidateReadinessChangeRetainsCandidateAndApproval(t *testing.T) {
	candidate := HandoffCandidate{
		RepositoryURL: "https://github.com/team/repo", PRURL: "https://github.com/team/repo/pull/1",
		Branch: "feature/original", CommitSHA: strings.Repeat("c", 40), Draft: false,
	}
	f, svc, issueID, writerTask := workflowReviewedHumanCandidateForCandidates(t, true, nil, []HandoffCandidate{candidate})
	bindWorkflowCandidateToGitHub(t, f, &svc, issueID, candidate)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	candidateID := issue.WorkflowCandidateID
	complete := false
	if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: f.UserID},
		WorkflowAcceptanceInput{CandidateID: util.UUIDToString(candidateID), ExpectedRevision: issue.Revision, OutcomeComplete: &complete}); err != nil || state != "accepted" {
		t.Fatalf("accept bound PR candidate: state=%q error=%v", state, err)
	}
	accepted, err := f.q.GetIssue(ctx, issueID)
	if err != nil || accepted.Status != "pr_ready" {
		t.Fatalf("accepted candidate did not remain in the PR-ready status: %+v, %v", accepted, err)
	}
	target := f.privateAgentOwnedBy(t, f.UserID, "accepted-readiness-reviewer")
	in := handoffInput(writerTask, parseTestUUID(t, target))
	in.Candidates = []HandoffCandidate{{RepositoryURL: "https://github.com/team/repo", PRURL: "https://github.com/team/repo/pull/1",
		Branch: "feature/original", CommitSHA: strings.Repeat("c", 40), Draft: true}}
	f.Exec(t, `UPDATE issue_wakeup SET disabled_at=now() WHERE issue_id=$1 AND disabled_at IS NULL`, issueID)
	wakeups := &IssueWakeupService{Tasks: svc.Tasks}
	w, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), writerTask, in)
	if err != nil {
		t.Fatalf("readiness-only accepted-candidate handoff: %v", err)
	}
	wakeDispatch(t, wakeups, w)
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil || current.WorkflowCandidateID != candidateID {
		t.Fatalf("readiness change replaced accepted candidate: current=%s err=%v", util.UUIDToString(current.WorkflowCandidateID), err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance
		WHERE issue_id=$1 AND candidate_id=$2 AND state='accepted' AND revoked_at IS NULL`, issueID, candidateID); got != 1 {
		t.Fatalf("readiness change discarded accepted approval: %d active rows", got)
	}
}

func TestAcceptedCandidateReplacementPreservesApprovalAndDeliveryFacts(t *testing.T) {
	oldCandidate := HandoffCandidate{
		RepositoryURL: "https://github.com/team/repo", PRURL: "https://github.com/team/repo/pull/1",
		Branch: "feature/original", CommitSHA: strings.Repeat("c", 40), Draft: false,
	}
	f, svc, issueID, writerTask := workflowReviewedHumanCandidateForCandidates(t, true, nil, []HandoffCandidate{oldCandidate})
	bindWorkflowCandidateToGitHub(t, f, &svc, issueID, oldCandidate)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	oldCandidateID := issue.WorkflowCandidateID
	complete := false
	if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: f.UserID},
		WorkflowAcceptanceInput{CandidateID: util.UUIDToString(oldCandidateID), ExpectedRevision: issue.Revision, OutcomeComplete: &complete}); err != nil || state != "accepted" {
		t.Fatalf("accept original candidate: state=%q error=%v", state, err)
	}
	var acceptanceID = dbid.NewV7()
	if err := f.Pool.QueryRow(ctx, `SELECT id FROM issue_workflow_acceptance
		WHERE issue_id=$1 AND candidate_id=$2 AND state='accepted' AND revoked_at IS NULL`, issueID, oldCandidateID).Scan(&acceptanceID); err != nil {
		t.Fatal(err)
	}
	pendingID, readyID, mergedID := dbid.NewV7(), dbid.NewV7(), dbid.NewV7()
	providerBindingID := dbid.NewV7()
	for _, row := range []struct {
		id          pgtype.UUID
		ordinal     int
		action      string
		status      string
		mergeMethod any
		merged      bool
	}{
		{pendingID, 1, "merge", "pending", "squash", false},
		{readyID, 2, "ready", "delivered", nil, false},
		{mergedID, 3, "merge", "delivered", "squash", true},
	} {
		f.Exec(t, `INSERT INTO issue_workflow_delivery
			(id,workspace_id,issue_id,acceptance_id,candidate_id,ordinal,provider,provider_binding_id,
			repository_url,pr_url,repo_owner,repo_name,pr_number,expected_head_sha,action,merge_method,status,merged_at)
			VALUES($1,$2,$3,$4,$5,$6,'github',$7,'https://github.com/team/repo',
			'https://github.com/team/repo/pull/1','team','repo',1,$8,$9,$10,$11,
			CASE WHEN $12::boolean THEN now() ELSE NULL END)`,
			row.id, issue.WorkspaceID, issueID, acceptanceID, oldCandidateID, row.ordinal, providerBindingID,
			strings.Repeat("c", 40), row.action, row.mergeMethod, row.status, row.merged)
	}

	newReviewer := f.privateAgentOwnedBy(t, f.UserID, "candidate-replacement-reviewer")
	in := handoffInput(writerTask, parseTestUUID(t, newReviewer))
	in.Candidates = []HandoffCandidate{{
		RepositoryURL: "https://example.test/repo", PRURL: "https://example.test/repo/pull/2",
		Branch: "feature/updated", CommitSHA: strings.Repeat("d", 40), Draft: false,
	}}
	f.Exec(t, `UPDATE issue_wakeup SET disabled_at=now() WHERE issue_id=$1 AND disabled_at IS NULL`, issueID)
	wakeups := &IssueWakeupService{Tasks: svc.Tasks}
	w, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), writerTask, in)
	if err != nil {
		t.Fatalf("create replacement handoff: %v", err)
	}
	wakeDispatch(t, wakeups, w)
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if current.WorkflowCandidateID == oldCandidateID || !current.WorkflowCandidateID.Valid {
		t.Fatalf("replacement candidate not registered: old=%s current=%s", util.UUIDToString(oldCandidateID), util.UUIDToString(current.WorkflowCandidateID))
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance
		WHERE id=$1 AND state='accepted' AND revoked_at IS NULL`, acceptanceID); got != 1 {
		t.Fatalf("prior approval provenance was not preserved: %d active rows", got)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE candidate_id=$1 AND state='accepted' AND revoked_at IS NULL`, current.WorkflowCandidateID); got != 0 {
		t.Fatalf("replacement candidate inherited acceptance without review: %d rows", got)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE id=$1 AND status='cancelled' AND merged_at IS NULL`, pendingID); got != 1 {
		t.Fatalf("old unmerged intent not cancelled: %d rows", got)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE id=$1 AND status='delivered' AND merged_at IS NULL`, readyID); got != 1 {
		t.Fatalf("already delivered readiness fact was changed: %d rows", got)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE id=$1 AND status='delivered' AND merged_at IS NOT NULL`, mergedID); got != 1 {
		t.Fatalf("observed merge fact was changed: %d rows", got)
	}
}
