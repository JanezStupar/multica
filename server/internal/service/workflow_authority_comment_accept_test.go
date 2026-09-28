package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func workflowCommentTask(t *testing.T, f principalFixture, issueID pgtype.UUID, authorID, content string) (WorkflowActor, string) {
	t.Helper()
	commentID := f.Comment(t, util.UUIDToString(issueID), content, testutil.Cols{"author_id": authorID})
	agentID := f.privateAgentOwnedBy(t, f.UserID, "comment-acceptor")
	var runtimeID string
	if err := f.Pool.QueryRow(context.Background(), `SELECT runtime_id::text FROM agent WHERE id=$1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	taskID := f.Task(t, agentID, testutil.Cols{
		"issue_id": util.UUIDToString(issueID), "runtime_id": runtimeID,
		"status": "running", "trigger_comment_id": commentID,
		"originator_user_id": authorID, "accountable_user_id": authorID,
	})
	f.Exec(t, `UPDATE agent_task_queue SET delivered_comment_ids=ARRAY[$2::uuid],
		dispatched_at=now(),started_at=now() WHERE id=$1`, taskID, commentID)
	return WorkflowActor{Type: "agent", ID: agentID, SourceTaskID: taskID}, commentID
}

func workflowCommentRequest(issue db.Issue, sourceID string) WorkflowCommentAcceptanceInput {
	return WorkflowCommentAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
		ExpectedRevision: issue.Revision, Source: "multica", SourceID: sourceID,
		Reason: "The human approved this exact candidate."}
}

func TestWorkflowCommentAcceptsHumanAssignedCandidateWithMemberAttribution(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil || before.AssigneeType.String != "member" {
		t.Fatalf("human-assigned candidate unavailable: %+v %v", before, err)
	}
	actor, commentID := workflowCommentTask(t, f, issueID, f.UserID, "Approved. Looks good to me.")
	request := workflowCommentRequest(before, commentID)
	for attempt := 0; attempt < 2; attempt++ {
		state, err := svc.AcceptWorkflowComment(ctx, before.WorkspaceID, issueID, actor, request)
		if err != nil || state != "accepted" {
			t.Fatalf("comment acceptance attempt %d: %s %v", attempt, state, err)
		}
	}
	otherAgent := f.privateAgentOwnedBy(t, f.UserID, "wrong-replay-agent")
	if _, err := svc.AcceptWorkflowComment(ctx, before.WorkspaceID, issueID,
		WorkflowActor{Type: "agent", ID: otherAgent, SourceTaskID: actor.SourceTaskID}, request); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("another agent replayed the acceptance source task: %v", err)
	}
	var acceptedBy, sourceTask, mode, state, action string
	var snapshot []byte
	if err := f.Pool.QueryRow(ctx, `SELECT actor_id::text,source_task_id::text,mode,state,
		authority_snapshot->>'delivery_action',authority_snapshot FROM issue_workflow_acceptance
		WHERE issue_id=$1`, issueID).Scan(&acceptedBy, &sourceTask, &mode, &state, &action, &snapshot); err != nil {
		t.Fatal(err)
	}
	if acceptedBy != f.UserID || sourceTask != actor.SourceTaskID || mode != "human" || state != "accepted" || action != "ready" {
		t.Fatalf("lost human/executor attribution: actor=%s task=%s mode=%s state=%s action=%s", acceptedBy, sourceTask, mode, state, action)
	}
	var evidence struct {
		Comment map[string]any `json:"comment_authority"`
	}
	if err := json.Unmarshal(snapshot, &evidence); err != nil ||
		evidence.Comment["source_content"] != "Approved. Looks good to me." ||
		evidence.Comment["source_id"] != commentID {
		t.Fatalf("immutable comment evidence missing: %s %v", snapshot, err)
	}
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil || current.Status != "done" || current.AssigneeID != before.AssigneeID {
		t.Fatalf("accepted issue changed owner or failed completion: %+v %v", current, err)
	}
	var taskStatus string
	if err := f.Pool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id=$1`, actor.SourceTaskID).Scan(&taskStatus); err != nil || taskStatus != "running" {
		t.Fatalf("acceptance killed the source conversation: %s %v", taskStatus, err)
	}
}

func TestWorkflowCommentAcceptanceRejectsUnrelatedOrEditedInput(t *testing.T) {
	for _, scenario := range []string{"unrelated", "edited", "wrong_author", "stale_revision"} {
		t.Run(scenario, func(t *testing.T) {
			f, svc, issueID, _ := workflowReviewedHumanCandidate(t)
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			authorID := f.UserID
			if scenario == "wrong_author" {
				authorID = f.member(t, "other-comment-author")
			}
			actor, commentID := workflowCommentTask(t, f, issueID, authorID, "Approved")
			request := workflowCommentRequest(issue, commentID)
			switch scenario {
			case "unrelated":
				request.SourceID = f.Comment(t, util.UUIDToString(issueID), "Approved elsewhere")
			case "edited":
				f.Exec(t, `UPDATE comment SET content='No, wait',revision=revision+1,
					updated_at=now()+interval '1 second' WHERE id=$1`, commentID)
			case "wrong_author":
				f.Exec(t, `UPDATE agent_task_queue SET originator_user_id=$2,accountable_user_id=$2
					WHERE id=$1`, actor.SourceTaskID, f.UserID)
			case "stale_revision":
				request.ExpectedRevision--
			}
			if _, err := svc.AcceptWorkflowComment(ctx, issue.WorkspaceID, issueID, actor, request); err == nil ||
				!(errors.Is(err, ErrWorkflowAuthorityForbidden) || errors.Is(err, ErrWorkflowAuthorityConflict)) {
				t.Fatalf("invalid %s evidence accepted: %v", scenario, err)
			}
			if count := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID); count != 0 {
				t.Fatalf("invalid evidence wrote %d acceptance rows", count)
			}
		})
	}
}

func TestWorkflowCommentReviewWaiverNeedsOwnerAndExactSource(t *testing.T) {
	for _, owner := range []bool{false, true} {
		t.Run(map[bool]string{false: "member", true: "owner"}[owner], func(t *testing.T) {
			f, svc, issueID, _ := workflowReviewedHumanCandidate(t)
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			f.Exec(t, `DELETE FROM issue_workflow_review WHERE issue_id=$1`, issueID)
			authorID := f.UserID
			if !owner {
				authorID = f.member(t, "waiver-member")
			}
			actor, commentID := workflowCommentTask(t, f, issueID, authorID, "Skip review and approve this candidate.")
			request := workflowCommentRequest(issue, commentID)
			request.WaiveReview = true
			state, err := svc.AcceptWorkflowComment(ctx, issue.WorkspaceID, issueID, actor, request)
			if !owner {
				if !errors.Is(err, ErrWorkflowAuthorityForbidden) {
					t.Fatalf("member waived review: %s %v", state, err)
				}
				return
			}
			if err != nil || state != "accepted" {
				t.Fatalf("owner comment waiver: %s %v", state, err)
			}
			if count := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance
				WHERE issue_id=$1 AND authority_snapshot->'comment_decision'->>'waive_review'='true'`, issueID); count != 1 {
				t.Fatalf("waiver not candidate-scoped in acceptance snapshot: %d", count)
			}
		})
	}
}

func workflowCommentPRCandidate(t *testing.T) HandoffCandidate {
	return HandoffCandidate{RepositoryURL: "https://github.com/team/repo",
		PRURL: "https://github.com/team/repo/pull/1", Branch: "feature/comment-accept",
		CommitSHA: strings.Repeat("a", 40), Draft: true}
}

func TestWorkflowCommentReadyOverridesPinnedHumanMergePolicy(t *testing.T) {
	candidate := workflowCommentPRCandidate(t)
	f, wakeups, issueID, writerAgent, reviewerAgent := handoffFixture(t)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	old, err := wakeups.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || old == nil {
		t.Fatalf("decode fixture policy: %v", err)
	}
	source := old.Bundle
	source.ID = util.UUIDToString(dbid.NewV7())
	source.Files = append(source.Files, AgentSkillFileData{Path: "runtime/policy.json", Content: `{
		"format_version":2,"accepted_status_key":"pr_ready","outcome_agent_id":"` + writerAgent + `",
		"human":{"delivery":"merge"},"delivery":{"merge_method":"squash"}}`})
	pinned, err := wakeups.Tasks.NewIssueWorkflowPolicy(source)
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := json.Marshal(pinned)
	f.Exec(t, `INSERT INTO issue_status(workspace_id,key,name,category,color,position)
		VALUES($1,'pr_ready','PR Ready','started','#22c55e',1)`, f.WorkspaceID)
	f.Exec(t, `UPDATE issue SET workflow_policy=$2 WHERE id=$1`, issueID, archive)
	writerTask := handoffSourceTask(t, f, issueID, writerAgent)
	f.Exec(t, `UPDATE agent_task_queue SET session_id='writer-session' WHERE id=$1`, writerTask)
	firstInput := handoffInput(writerTask, parseTestUUID(t, reviewerAgent))
	firstInput.Candidates = []HandoffCandidate{candidate}
	first, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), writerTask, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writerTask)
	wakeDispatch(t, wakeups, first)
	firstStored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: first.ID, WorkspaceID: first.WorkspaceID})
	if err != nil || !firstStored.LastTaskID.Valid {
		t.Fatalf("reviewer handoff unavailable: %+v %v", firstStored, err)
	}
	reviewerTask := firstStored.LastTaskID
	f.Exec(t, `UPDATE agent_task_queue SET status='running',started_at=now(),session_id='reviewer-session' WHERE id=$1`, reviewerTask)
	bindWorkflowTestTask(t, f, issueID, reviewerTask)
	withCandidate, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	svc := WorkflowAuthorityService{Tasks: wakeups.Tasks}
	if err := svc.RegisterReview(ctx, withCandidate.WorkspaceID, issueID, WorkflowActor{
		Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(reviewerTask),
	}, WorkflowReviewInput{CandidateID: util.UUIDToString(withCandidate.WorkflowCandidateID), Verdict: "pass",
		PRReviewURLs: workflowCandidateReviewURLs([]HandoffCandidate{candidate})}); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, reviewerTask)
	secondInput := handoffInput(reviewerTask, pgtype.UUID{})
	secondInput.AgentID, secondInput.AssigneeType, secondInput.AssigneeID = "", "member", f.UserID
	secondInput.Candidates = []HandoffCandidate{candidate}
	second, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), reviewerTask, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, wakeups, second)
	bindWorkflowCandidateToGitHub(t, f, &svc, issueID, candidate)
	ready, err := f.q.GetIssue(ctx, issueID)
	if err != nil || ready.AssigneeType.String != "member" {
		t.Fatalf("human handoff unavailable: %+v %v", ready, err)
	}
	actor, commentID := workflowCommentTask(t, f, issueID, f.UserID, "Approved. Looks good.")
	if state, err := svc.AcceptWorkflowComment(ctx, ready.WorkspaceID, issueID, actor,
		workflowCommentRequest(ready, commentID)); err != nil || state != "accepted" {
		t.Fatalf("ready comment under merge policy: %s %v", state, err)
	}
	var action, status string
	if err := f.Pool.QueryRow(ctx, `SELECT d.action,i.status FROM issue_workflow_delivery d
		JOIN issue i ON i.id=d.issue_id WHERE i.id=$1`, issueID).Scan(&action, &status); err != nil ||
		action != "ready" || status != "pr_ready" {
		t.Fatalf("plain approval inherited merge policy: action=%s status=%s err=%v", action, status, err)
	}
}

func workflowCommentMergeGrant(t *testing.T, f principalFixture, issue db.Issue) {
	t.Helper()
	var policyVersion string
	if err := f.Pool.QueryRow(context.Background(), `SELECT workflow_policy->>'version' FROM issue WHERE id=$1`, issue.ID).Scan(&policyVersion); err != nil {
		t.Fatal(err)
	}
	f.Insert(t, "issue_workflow_exception", testutil.Cols{
		"id":           dbid.NewV7(),
		"workspace_id": f.WorkspaceID, "issue_id": util.UUIDToString(issue.ID),
		"candidate_id": util.UUIDToString(issue.WorkflowCandidateID), "base_policy_version": policyVersion,
		"scope": "delivery", "grant_details": testutil.Raw(`'{"action":"merge","merge_method":"squash"}'::jsonb`),
		"actor_type": "member", "actor_id": f.UserID,
		"reason": "Explicit merge method", "consequences": "This candidate may be merged.",
	})
}

func TestWorkflowCommentReadyThenLaterMergePreservesBothDecisions(t *testing.T) {
	candidate := workflowCommentPRCandidate(t)
	f, svc, issueID, _ := workflowReviewedHumanCandidateForCandidates(t, true, nil, []HandoffCandidate{candidate})
	bindWorkflowCandidateToGitHub(t, f, &svc, issueID, candidate)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	workflowCommentMergeGrant(t, f, before)
	firstActor, firstComment := workflowCommentTask(t, f, issueID, f.UserID, "Approved. Looks good.")
	first := workflowCommentRequest(before, firstComment)
	if state, err := svc.AcceptWorkflowComment(ctx, before.WorkspaceID, issueID, firstActor, first); err != nil || state != "accepted" {
		t.Fatalf("ready decision: %s %v", state, err)
	}
	var acceptanceID, action, deliveryStatus string
	if err := f.Pool.QueryRow(ctx, `SELECT a.id::text,d.action,d.status FROM issue_workflow_acceptance a
		JOIN issue_workflow_delivery d ON d.acceptance_id=a.id WHERE a.issue_id=$1`, issueID).
		Scan(&acceptanceID, &action, &deliveryStatus); err != nil || action != "ready" {
		t.Fatalf("plain approval unexpectedly merged: action=%s status=%s err=%v", action, deliveryStatus, err)
	}
	f.Exec(t, `UPDATE issue_workflow_acceptance SET hold_delivery=true,held_at=now() WHERE id=$1`, acceptanceID)
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, firstActor.SourceTaskID)
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	secondActor, secondComment := workflowCommentTask(t, f, issueID, f.UserID, "Please merge that approved PR now.")
	// PostgreSQL timestamps can share a microsecond on a fast test host. The
	// second instruction must be observably later than the accepted decision.
	f.Exec(t, `UPDATE comment SET created_at=(SELECT accepted_at+interval '1 second' FROM issue_workflow_acceptance WHERE id=$2),
		updated_at=(SELECT accepted_at+interval '1 second' FROM issue_workflow_acceptance WHERE id=$2)
		WHERE id=$1`, secondComment, acceptanceID)
	f.Exec(t, `UPDATE agent_task_queue SET dispatched_at=(SELECT accepted_at+interval '2 second' FROM issue_workflow_acceptance WHERE id=$2)
		WHERE id=$1`, secondActor.SourceTaskID, acceptanceID)
	merge := workflowCommentRequest(current, secondComment)
	merge.Action = "merge"
	merge.Reason = "The human explicitly requested merging the accepted PR."
	for attempt := 0; attempt < 2; attempt++ {
		if state, err := svc.AcceptWorkflowComment(ctx, current.WorkspaceID, issueID, secondActor, merge); err != nil || state != "accepted" {
			t.Fatalf("merge upgrade attempt %d: %s %v", attempt, state, err)
		}
	}
	var method string
	var held bool
	var snapshot []byte
	if err := f.Pool.QueryRow(ctx, `SELECT d.action,d.merge_method,a.hold_delivery,a.authority_snapshot FROM issue_workflow_acceptance a
		JOIN issue_workflow_delivery d ON d.acceptance_id=a.id WHERE a.id=$1`, acceptanceID).
		Scan(&action, &method, &held, &snapshot); err != nil || action != "merge" || method != "squash" || !held {
		t.Fatalf("explicit merge not scoped to existing delivery: %s %s %v", action, method, err)
	}
	var history struct {
		First map[string]any `json:"comment_decision"`
		Merge map[string]any `json:"merge_upgrade"`
	}
	if err := json.Unmarshal(snapshot, &history); err != nil || history.First["source_id"] != firstComment || history.Merge["executor_task_id"] != secondActor.SourceTaskID {
		t.Fatalf("decision history missing: %s %v", snapshot, err)
	}
	if count := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID); count != 1 {
		t.Fatalf("merge upgrade created %d acceptances", count)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, secondActor.SourceTaskID)
	thirdActor, thirdComment := workflowCommentTask(t, f, issueID, f.UserID, "Release the hold and merge the approved PR.")
	f.Exec(t, `UPDATE comment SET created_at=(SELECT accepted_at+interval '3 second' FROM issue_workflow_acceptance WHERE id=$2),
		updated_at=(SELECT accepted_at+interval '3 second' FROM issue_workflow_acceptance WHERE id=$2)
		WHERE id=$1`, thirdComment, acceptanceID)
	f.Exec(t, `UPDATE agent_task_queue SET dispatched_at=(SELECT accepted_at+interval '4 second' FROM issue_workflow_acceptance WHERE id=$2)
		WHERE id=$1`, thirdActor.SourceTaskID, acceptanceID)
	release := workflowCommentRequest(current, thirdComment)
	release.Action, release.ReleaseHold = "merge", true
	release.Reason = "The human explicitly released the earlier hold."
	for attempt := 0; attempt < 2; attempt++ {
		if state, err := svc.AcceptWorkflowComment(ctx, current.WorkspaceID, issueID, thirdActor, release); err != nil || state != "accepted" {
			t.Fatalf("hold release attempt %d: %s %v", attempt, state, err)
		}
	}
	var heldAt, releasedAt pgtype.Timestamptz
	if err := f.Pool.QueryRow(ctx, `SELECT hold_delivery,held_at,released_at,authority_snapshot
		FROM issue_workflow_acceptance WHERE id=$1`, acceptanceID).
		Scan(&held, &heldAt, &releasedAt, &snapshot); err != nil || held || !heldAt.Valid || !releasedAt.Valid {
		t.Fatalf("explicit release lost hold history: held=%t held_at=%v released_at=%v err=%v", held, heldAt, releasedAt, err)
	}
	var afterRelease struct {
		Merge   map[string]any `json:"merge_upgrade"`
		Release map[string]any `json:"hold_release"`
	}
	if err := json.Unmarshal(snapshot, &afterRelease); err != nil ||
		afterRelease.Merge["executor_task_id"] != secondActor.SourceTaskID ||
		afterRelease.Release["executor_task_id"] != thirdActor.SourceTaskID {
		t.Fatalf("merge/release source history missing: %s %v", snapshot, err)
	}
}

func TestWorkflowCommentReleasesHeldPartialMergeWithoutChangingDeliveredPR(t *testing.T) {
	firstPR := workflowCommentPRCandidate(t)
	secondPR := firstPR
	secondPR.PRURL = "https://github.com/team/repo/pull/2"
	secondPR.Branch = "feature/comment-accept-two"
	secondPR.CommitSHA = strings.Repeat("b", 40)
	f, svc, issueID, _ := workflowReviewedHumanCandidateForCandidates(t, true, nil,
		[]HandoffCandidate{firstPR, secondPR})
	bindWorkflowCandidateToGitHub(t, f, &svc, issueID, firstPR)
	ctx := context.Background()
	var installationID int64
	if err := f.Pool.QueryRow(ctx, `SELECT installation_id FROM github_installation WHERE workspace_id=$1
		ORDER BY created_at DESC LIMIT 1`, f.WorkspaceID).Scan(&installationID); err != nil {
		t.Fatal(err)
	}
	secondPRID := f.Insert(t, "github_pull_request", testutil.Cols{
		"workspace_id": f.WorkspaceID, "installation_id": installationID,
		"repo_owner": "team", "repo_name": "repo", "pr_number": 2,
		"title": "Second PR", "state": "open", "html_url": secondPR.PRURL,
		"branch": secondPR.Branch, "pr_created_at": testutil.Raw("now()"),
		"pr_updated_at": testutil.Raw("now()"), "head_sha": secondPR.CommitSHA,
	})
	f.InsertNoID(t, "issue_pull_request", testutil.Cols{
		"issue_id": util.UUIDToString(issueID), "pull_request_id": secondPRID,
	}, "issue_id=$1 AND pull_request_id=$2", issueID, secondPRID)
	svc.ReviewVerifier = func(_ context.Context, evidence WorkflowReviewEvidenceInput) error {
		if len(evidence.PRs) != 2 || len(evidence.ReviewURLs) != 2 {
			return errors.New("both PR heads and review links must be present")
		}
		return nil
	}
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	workflowCommentMergeGrant(t, f, issue)
	firstActor, firstComment := workflowCommentTask(t, f, issueID, f.UserID, "Merge both approved PRs in this order.")
	merge := workflowCommentRequest(issue, firstComment)
	merge.Action = "merge"
	merge.MergeOrderPRURLs = []string{firstPR.PRURL, secondPR.PRURL}
	if state, err := svc.AcceptWorkflowComment(ctx, issue.WorkspaceID, issueID, firstActor, merge); err != nil || state != "accepted" {
		t.Fatalf("initial two-PR merge approval: %s %v", state, err)
	}
	var acceptanceID, firstDeliveryID string
	if err := f.Pool.QueryRow(ctx, `SELECT a.id::text,d.id::text FROM issue_workflow_acceptance a
		JOIN issue_workflow_delivery d ON d.acceptance_id=a.id AND d.ordinal=0 WHERE a.issue_id=$1`, issueID).
		Scan(&acceptanceID, &firstDeliveryID); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE issue_workflow_acceptance SET hold_delivery=true,held_at=now() WHERE id=$1`, acceptanceID)
	f.Exec(t, `UPDATE issue_workflow_delivery SET status='delivered',merged_at=now(),merge_commit_sha=$2
		WHERE id=$1`, firstDeliveryID, strings.Repeat("c", 40))
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, firstActor.SourceTaskID)
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	secondActor, secondComment := workflowCommentTask(t, f, issueID, f.UserID,
		"Release the hold and merge the remaining approved PR.")
	f.Exec(t, `UPDATE comment SET created_at=(SELECT accepted_at+interval '1 second' FROM issue_workflow_acceptance WHERE id=$2),
		updated_at=(SELECT accepted_at+interval '1 second' FROM issue_workflow_acceptance WHERE id=$2)
		WHERE id=$1`, secondComment, acceptanceID)
	f.Exec(t, `UPDATE agent_task_queue SET dispatched_at=(SELECT accepted_at+interval '2 second' FROM issue_workflow_acceptance WHERE id=$2)
		WHERE id=$1`, secondActor.SourceTaskID, acceptanceID)
	release := workflowCommentRequest(current, secondComment)
	release.Action, release.ReleaseHold = "merge", true
	if state, err := svc.AcceptWorkflowComment(ctx, current.WorkspaceID, issueID, secondActor, release); err != nil || state != "accepted" {
		t.Fatalf("release partial held merge: %s %v", state, err)
	}
	var held bool
	if err := f.Pool.QueryRow(ctx, `SELECT hold_delivery FROM issue_workflow_acceptance WHERE id=$1`, acceptanceID).Scan(&held); err != nil || held {
		t.Fatalf("held acceptance was not released: %t %v", held, err)
	}
	var status, action string
	var mergedAt pgtype.Timestamptz
	if err := f.Pool.QueryRow(ctx, `SELECT status,action,merged_at FROM issue_workflow_delivery WHERE id=$1`, firstDeliveryID).
		Scan(&status, &action, &mergedAt); err != nil || status != "delivered" || action != "merge" || !mergedAt.Valid {
		t.Fatalf("already merged delivery changed: %s %s %v %v", status, action, mergedAt, err)
	}
	if count := f.Count(t, `SELECT count(*) FROM issue_workflow_delivery
		WHERE acceptance_id=$1 AND status='pending' AND action='merge'`, acceptanceID); count != 1 {
		t.Fatalf("remaining held merge delivery changed: %d pending rows", count)
	}
}
