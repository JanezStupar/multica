package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/attribution"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type HandoffCandidate struct {
	RepositoryURL string `json:"repository_url"`
	PRURL         string `json:"pr_url"`
	Branch        string `json:"branch"`
	CommitSHA     string `json:"commit_sha"`
	Draft         bool   `json:"draft"`
}

type HandoffInput struct {
	RequestKey     string             `json:"request_key"`
	OutgoingTaskID string             `json:"outgoing_task_id"`
	AgentID        string             `json:"agent_id"`
	AssigneeType   string             `json:"assignee_type,omitempty"`
	AssigneeID     string             `json:"assignee_id,omitempty"`
	Status         string             `json:"status"`
	ContextMode    string             `json:"context_mode"`
	ResumeTaskID   string             `json:"resume_task_id,omitempty"`
	Candidates     []HandoffCandidate `json:"candidates"`
	EvidenceURLs   []string           `json:"evidence_urls"`
	Instruction    string             `json:"instruction"`
}

type issueHandoffIntent struct {
	HandoffInput
	ExpectedStatus       string `json:"expected_status"`
	ExpectedAssigneeType string `json:"expected_assignee_type"`
	ExpectedAssigneeID   string `json:"expected_assignee_id"`
}

var fullHandoffCommit = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func handoffHTTPURL(raw string) bool {
	if len(raw) == 0 || len(raw) > 2048 || strings.IndexFunc(raw, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Opaque == ""
}

func normalizeHandoffInput(in HandoffInput) (HandoffInput, error) {
	bad := func(message string) (HandoffInput, error) {
		return HandoffInput{}, fmt.Errorf("%w: %s", ErrWakeupInput, message)
	}
	in.Instruction = strings.TrimSpace(in.Instruction)
	if len(in.Instruction) == 0 || len(in.Instruction) > 12000 {
		return bad("instruction must contain 1–12000 bytes")
	}
	for _, value := range []string{in.RequestKey, in.OutgoingTaskID} {
		id, err := wakeupUUID(value)
		if err != nil || !id.Valid {
			return bad("request_key and outgoing_task_id must be UUIDs")
		}
	}
	requestKey, _ := wakeupUUID(in.RequestKey)
	outgoingID, _ := wakeupUUID(in.OutgoingTaskID)
	in.RequestKey = util.UUIDToString(requestKey)
	in.OutgoingTaskID = util.UUIDToString(outgoingID)
	if in.AssigneeType == "" && in.AgentID != "" && in.AssigneeID == "" {
		in.AssigneeType, in.AssigneeID = "agent", in.AgentID
	}
	if in.AssigneeType != "agent" && in.AssigneeType != "member" {
		return bad("assignee_type must be agent or member")
	}
	if in.AssigneeType == "agent" {
		if in.AssigneeID == "" {
			in.AssigneeID = in.AgentID
		}
		if in.AgentID != "" && in.AgentID != in.AssigneeID {
			return bad("agent_id and assignee_id must identify the same agent")
		}
		in.AgentID = in.AssigneeID
	} else if in.AgentID != "" {
		return bad("agent_id is only valid for an agent recipient")
	}
	assigneeID, err := wakeupUUID(in.AssigneeID)
	if err != nil || !assigneeID.Valid {
		return bad("assignee_id must be a UUID")
	}
	in.AssigneeID = util.UUIDToString(assigneeID)
	if in.AssigneeType == "agent" {
		in.AgentID = in.AssigneeID
	}
	if in.Status != "in_progress" && in.Status != "in_review" {
		return bad("status must be in_progress or in_review")
	}
	if in.ContextMode != "fresh" && in.ContextMode != "resume" {
		return bad("context_mode must be fresh or resume")
	}
	if (in.ContextMode == "resume") != (in.ResumeTaskID != "") {
		return bad("resume_task_id is required only for resume")
	}
	if in.AssigneeType == "member" && (in.Status != "in_review" || in.ContextMode != "fresh") {
		return bad("member handoff requires in_review and fresh context mode")
	}
	if in.ResumeTaskID != "" {
		id, err := wakeupUUID(in.ResumeTaskID)
		if err != nil || !id.Valid {
			return bad("resume_task_id must be a UUID")
		}
		in.ResumeTaskID = util.UUIDToString(id)
	}
	if len(in.Candidates) > 20 || len(in.EvidenceURLs) > 32 {
		return bad("too many candidate or evidence references")
	}
	if in.Candidates == nil {
		in.Candidates = []HandoffCandidate{}
	}
	if in.EvidenceURLs == nil {
		in.EvidenceURLs = []string{}
	}
	seenPRs := make(map[string]struct{}, len(in.Candidates))
	for i := range in.Candidates {
		c := &in.Candidates[i]
		c.CommitSHA = strings.ToLower(c.CommitSHA)
		if !handoffHTTPURL(c.RepositoryURL) || !handoffHTTPURL(c.PRURL) || strings.TrimSpace(c.Branch) != c.Branch || c.Branch == "" || len(c.Branch) > 500 || strings.IndexFunc(c.Branch, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || !fullHandoffCommit.MatchString(c.CommitSHA) {
			return bad("candidate requires repository and PR HTTP URLs, branch and full commit SHA")
		}
		if _, exists := seenPRs[c.PRURL]; exists {
			return bad("each PR may appear only once")
		}
		seenPRs[c.PRURL] = struct{}{}
	}
	for _, raw := range in.EvidenceURLs {
		if !handoffHTTPURL(raw) || len(raw) > 2048 {
			return bad("evidence URLs must be HTTP(S) URLs without credentials")
		}
	}
	return in, nil
}

func handoffIntent(w db.IssueWakeup) (issueHandoffIntent, error) {
	var intent issueHandoffIntent
	if len(w.Handoff) == 0 {
		return intent, fmt.Errorf("%w: wakeup is not a handoff", ErrWakeupInput)
	}
	if err := json.Unmarshal(w.Handoff, &intent); err != nil {
		return intent, err
	}
	normalized, err := normalizeHandoffInput(intent.HandoffInput)
	legacyAgentPayload := intent.AssigneeType == "" && intent.AssigneeID == "" && intent.AgentID != ""
	if err != nil || (!legacyAgentPayload && !reflect.DeepEqual(normalized, intent.HandoffInput)) {
		return issueHandoffIntent{}, fmt.Errorf("%w: stored handoff intent is invalid", ErrWakeupInput)
	}
	if legacyAgentPayload {
		intent.HandoffInput = normalized
	}
	if w.RequestKey != mustHandoffUUID(intent.RequestKey) || w.SourceTaskID != mustHandoffUUID(intent.OutgoingTaskID) ||
		w.FilterTaskID != w.SourceTaskID ||
		w.Kind != "event" || w.Mode != "once" || !w.ForceFreshSession || w.Instruction != intent.Instruction {
		return issueHandoffIntent{}, fmt.Errorf("%w: stored handoff intent is invalid", ErrWakeupInput)
	}
	if intent.AssigneeType == "agent" && w.AgentID != mustHandoffUUID(intent.AssigneeID) {
		return issueHandoffIntent{}, fmt.Errorf("%w: stored agent handoff target is invalid", ErrWakeupInput)
	}
	if intent.AssigneeType == "member" && w.AgentID != w.FilterAgentID {
		return issueHandoffIntent{}, fmt.Errorf("%w: stored member handoff owner is invalid", ErrWakeupInput)
	}
	return intent, nil
}

func mustHandoffUUID(raw string) pgtype.UUID {
	id, _ := wakeupUUID(raw)
	return id
}

func nullableUUIDString(id pgtype.UUID) string {
	if id.Valid {
		return util.UUIDToString(id)
	}
	return ""
}

// CreateHandoff records an immutable transfer intent under the issue lock.
// The owner/status change and recipient enqueue occur together only after the
// named outgoing task has completed successfully.
func (s *IssueWakeupService) CreateHandoff(ctx context.Context, issueID, member, trustedSource pgtype.UUID, raw HandoffInput) (db.IssueWakeup, error) {
	var empty db.IssueWakeup
	in, err := normalizeHandoffInput(raw)
	if err != nil {
		return empty, err
	}
	requestKey, _ := wakeupUUID(in.RequestKey)
	outgoingID, _ := wakeupUUID(in.OutgoingTaskID)
	assigneeID, _ := wakeupUUID(in.AssigneeID)
	if trustedSource.Valid && trustedSource != outgoingID {
		return empty, fmt.Errorf("%w: outgoing task must be the authenticated source task", ErrWakeupForbidden)
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	var workspace pgtype.UUID
	if err = tx.QueryRow(ctx, "SELECT w.id FROM workspace w JOIN issue i ON i.workspace_id=w.id WHERE i.id=$1 FOR KEY SHARE OF w", issueID).Scan(&workspace); err != nil {
		return empty, err
	}
	issue, err := q.LockWakeupIssue(ctx, issueID)
	if err != nil {
		return empty, err
	}
	if existing, e := q.GetIssueHandoffByRequestKey(ctx, db.GetIssueHandoffByRequestKeyParams{IssueID: issueID, RequestKey: requestKey}); e == nil {
		intent, e := handoffIntent(existing)
		if e != nil || !reflect.DeepEqual(intent.HandoffInput, in) {
			return empty, ErrWakeupConflict
		}
		membership, e := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: member, WorkspaceID: workspace})
		if e != nil || (existing.CreatedBy != member && membership.Role != "owner" && membership.Role != "admin") {
			return empty, ErrWakeupForbidden
		}
		for _, id := range []pgtype.UUID{existing.AgentID, existing.FilterAgentID} {
			a, e := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: workspace})
			if e != nil || s.authorize(ctx, q, workspace, member, a) != nil {
				return empty, ErrWakeupForbidden
			}
		}
		if intent.AssigneeType == "member" {
			recipient, _ := wakeupUUID(intent.AssigneeID)
			if _, e := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: recipient, WorkspaceID: workspace}); e != nil {
				return empty, ErrWakeupForbidden
			}
		}
		return existing, tx.Commit(ctx)
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return empty, e
	}
	active, err := wakeupIssueActive(ctx, q, issue)
	if err != nil {
		return empty, err
	}
	if !active {
		return empty, fmt.Errorf("%w: issue is closed", ErrWakeupInput)
	}
	policy, err := s.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || policy == nil {
		return empty, fmt.Errorf("%w: issue has no valid pinned workflow", ErrWakeupInput)
	}
	var target db.Agent
	if in.AssigneeType == "agent" {
		target, err = q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: assigneeID, WorkspaceID: workspace})
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, ErrWakeupForbidden
		}
		if err != nil {
			return empty, err
		}
		if err = s.authorize(ctx, q, workspace, member, target); err != nil {
			return empty, err
		}
	} else {
		if _, err = q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: assigneeID, WorkspaceID: workspace}); err != nil {
			return empty, ErrWakeupForbidden
		}
	}
	outgoing, err := q.LockWakeupSourceTask(ctx, db.LockWakeupSourceTaskParams{ID: outgoingID, IssueID: issueID})
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, fmt.Errorf("%w: outgoing task does not belong to issue", ErrWakeupInput)
	}
	if err != nil {
		return empty, err
	}
	if outgoing.Status != "running" && outgoing.Status != "waiting_local_directory" && outgoing.Status != "completed" {
		return empty, fmt.Errorf("%w: outgoing task must be running or completed", ErrWakeupInput)
	}
	sourceAgent, err := q.LockHandoffSourceAgent(ctx, db.LockHandoffSourceAgentParams{AgentID: outgoing.AgentID, WorkspaceID: workspace})
	var sourceLockError *pgconn.PgError
	if errors.As(err, &sourceLockError) && sourceLockError.Code == "55P03" {
		return empty, fmt.Errorf("%w: source agent is being updated", ErrWakeupConflict)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	if err != nil || !outgoing.RuntimeID.Valid || outgoing.RuntimeID != sourceAgent.RuntimeID {
		return empty, ErrWakeupForbidden
	}
	if err = s.authorize(ctx, q, workspace, member, sourceAgent); err != nil {
		return empty, err
	}
	if in.ContextMode == "resume" {
		resumeID, _ := wakeupUUID(in.ResumeTaskID)
		resume, e := q.GetAgentTask(ctx, resumeID)
		if e != nil || resume.IssueID != issueID || resume.AgentID != assigneeID || resume.RuntimeID != target.RuntimeID || resume.Status != "completed" {
			return empty, fmt.Errorf("%w: resume source must be a completed task for this agent, runtime and issue", ErrWakeupInput)
		}
	}
	previous, err := q.LatestIssueHandoff(ctx, issueID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	directAssignment := err == nil && s.handoffHumanAssignmentMatches(ctx, q, issue, sourceAgent, previous, outgoing)
	childContinuation, e := childCompletionContinuation(ctx, tx, issue, outgoing)
	if e != nil {
		return empty, e
	}
	providerContinuation, e := workflowProviderFeedbackTaskMatches(ctx, tx, issue, outgoing)
	if e != nil {
		return empty, e
	}
	authorizedContinuation := directAssignment || childContinuation || providerContinuation
	if err == nil && !previous.DisabledAt.Valid && !authorizedContinuation {
		if !previous.LastTaskID.Valid {
			return empty, fmt.Errorf("%w: previous handoff is awaiting its recipient", ErrWakeupConflict)
		}
		if !handoffRecipientMatches(ctx, q, previous, outgoing) && !handoffCommentContinuationMatches(ctx, q, previous, outgoing) {
			// A validated delegated-failure recovery is the coordinator's
			// retained follow-up to that recipient. It may issue the next
			// handoff without a human relaying the failed candidate.
			recoveredSource, recoveryErr := s.Tasks.validateWorkflowRecoverySourceWithQueries(ctx, q, outgoing)
			if recoveryErr != nil || recoveredSource.ID != previous.FilterTaskID {
				return empty, fmt.Errorf("%w: outgoing task is not the previous handoff recipient, retained comment continuation or recovery", ErrWakeupConflict)
			}
		}
	}
	var otherActive bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM agent_task_queue WHERE issue_id=$1 AND id<>$2 AND status IN ('dispatched','running','waiting_local_directory'))", issueID, outgoingID).Scan(&otherActive); err != nil {
		return empty, err
	}
	if otherActive {
		return empty, fmt.Errorf("%w: another issue run is active", ErrWakeupConflict)
	}
	retirePrevious := authorizedContinuation && previous.ID.Valid && !previous.DisabledAt.Valid && !previous.LastTaskID.Valid && !previous.HandoffCompletedAt.Valid
	if authorizedContinuation && previous.ID.Valid && !previous.DisabledAt.Valid && previous.LastTaskID.Valid && previous.LastTaskID != outgoing.ID {
		priorRecipient, e := q.GetAgentTask(ctx, previous.LastTaskID)
		if e != nil {
			return empty, e
		}
		retirePrevious = !priorRecipient.StartedAt.Valid && (priorRecipient.Status == "queued" || priorRecipient.Status == "deferred")
	}
	var cancelled []db.AgentTaskQueue
	if retirePrevious {
		// Withdraw an obsolete transfer before it can race the replacement.
		// Keep its intent and history as evidence of the earlier instruction.
		if _, err = tx.Exec(ctx, "UPDATE issue_wakeup SET enabled=false,disabled_at=COALESCE(disabled_at,now()),updated_at=now() WHERE id=$1", previous.ID); err != nil {
			return empty, err
		}
		if err = q.DiscardWakeupReceipts(ctx, previous.ID); err != nil {
			return empty, err
		}
		cancelled, err = q.CancelUnstartedWakeupTasks(ctx, util.UUIDToString(previous.ID))
		if err != nil {
			return empty, err
		}
		if err = SettleDeliveredDelegatedFailureRecoveries(ctx, q, cancelled...); err != nil {
			return empty, err
		}
	}
	intent := issueHandoffIntent{HandoffInput: in, ExpectedStatus: issue.Status, ExpectedAssigneeType: issue.AssigneeType.String, ExpectedAssigneeID: nullableUUIDString(issue.AssigneeID)}
	payload, err := json.Marshal(intent)
	if err != nil {
		return empty, err
	}
	storageAgentID := assigneeID
	if in.AssigneeType == "member" {
		storageAgentID = outgoing.AgentID
	}
	w, err := q.CreateIssueHandoff(ctx, db.CreateIssueHandoffParams{ID: dbid.NewV7(), WorkspaceID: workspace, IssueID: issueID, AgentID: storageAgentID, CreatedBy: member, OutgoingTaskID: outgoingID, OutgoingAgentID: outgoing.AgentID, Instruction: in.Instruction, Handoff: payload, RequestKey: requestKey})
	if err != nil {
		return empty, err
	}
	if outgoing.Status == "completed" {
		payload, _ := json.Marshal(map[string]any{"event_id": in.OutgoingTaskID + ":completed", "event_type": "task.completed", "source_task_id": in.OutgoingTaskID, "status": "completed", "registration_snapshot": true})
		if _, err = q.RecordWakeupReceipt(ctx, db.RecordWakeupReceiptParams{ID: dbid.NewV7(), WakeupID: w.ID, Revision: w.Revision, EventKey: in.OutgoingTaskID + ":completed", EventType: "task.completed", Payload: payload}); err != nil {
			return empty, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	s.Tasks.BroadcastCancelledTasks(ctx, util.UUIDToString(workspace), cancelled)
	return w, nil
}

// A human-attributed run for the current assigned agent is an authorized turn,
// independent of earlier workflow recipients. Enqueue can resolve its human
// from the assigning actor or the issue creator; revalidate that person's access
// rather than requiring an artificial recipient ancestry or a new assignment.
func (s *IssueWakeupService) handoffHumanAssignmentMatches(ctx context.Context, q *db.Queries, issue db.Issue, agent db.Agent, previous db.IssueWakeup, task db.AgentTaskQueue) bool {
	if issue.AssigneeType.String != "agent" || issue.AssigneeID != task.AgentID ||
		task.IssueID != issue.ID || task.AgentID != agent.ID || task.RuntimeID != agent.RuntimeID ||
		!task.OriginatorSource.Valid || task.OriginatorSource.String != string(attribution.SourceDirectHuman) ||
		!task.TriggerEvidenceKind.Valid || task.TriggerEvidenceKind.String != string(attribution.EvidenceIssueAssignment) ||
		task.TriggerEvidenceRefID != issue.ID || !task.OriginatorUserID.Valid ||
		!task.CreatedAt.Valid || !previous.CreatedAt.Valid || task.CreatedAt.Time.Before(previous.CreatedAt.Time) {
		return false
	}
	return s.authorize(ctx, q, issue.WorkspaceID, task.OriginatorUserID, agent) == nil
}

func handoffIssueSnapshotMatches(issue db.Issue, intent issueHandoffIntent) bool {
	return issue.Status == intent.ExpectedStatus && issue.AssigneeType.String == intent.ExpectedAssigneeType && nullableUUIDString(issue.AssigneeID) == intent.ExpectedAssigneeID
}

// A recipient's system retry (or explicit rerun) keeps the same handoff
// provenance. Follow same-scope lineage to the one task the wakeup actually
// enqueued; sharing a context key alone is not sufficient. A visited set
// terminates corrupt cycles without rejecting a legitimate long repair chain.
func handoffRecipientMatches(ctx context.Context, q *db.Queries, w db.IssueWakeup, task db.AgentTaskQueue) bool {
	recipient, err := q.GetAgentTask(ctx, w.LastTaskID)
	if err != nil || !recipient.RuntimeID.Valid {
		return false
	}
	visited := map[pgtype.UUID]struct{}{}
	for {
		if task.IssueID != w.IssueID || task.AgentID != w.AgentID || task.RuntimeID != recipient.RuntimeID {
			return false
		}
		if _, repeated := visited[task.ID]; repeated {
			return false
		}
		visited[task.ID] = struct{}{}
		if task.ID == w.LastTaskID {
			return true
		}
		parent := task.RetryOfTaskID
		if !parent.Valid {
			parent = task.RerunOfTaskID
		}
		if !parent.Valid {
			return false
		}
		var err error
		task, err = q.GetAgentTask(ctx, parent)
		if err != nil {
			return false
		}
	}
}

// Ordinary comments may continue a handoff recipient's retained provider
// session. Only the source selected and recorded by server claim delivery is
// authority; identical context keys, wakeup markers or client session IDs are
// insufficient. Each completed turn must preserve the exact scope and session.
func handoffCommentContinuationMatches(ctx context.Context, q *db.Queries, w db.IssueWakeup, task db.AgentTaskQueue) bool {
	recipient, err := q.GetAgentTask(ctx, w.LastTaskID)
	if err != nil || !recipient.RuntimeID.Valid {
		return false
	}
	visited := map[pgtype.UUID]struct{}{}
	for {
		if task.IssueID != w.IssueID || task.AgentID != w.AgentID || task.RuntimeID != recipient.RuntimeID ||
			!task.SessionID.Valid || task.SessionID.String == "" {
			return false
		}
		if _, repeated := visited[task.ID]; repeated {
			return false
		}
		visited[task.ID] = struct{}{}
		// A completed retry/rerun is an exact recipient too, even when the
		// original failed before pinning a session or its retry started fresh.
		if handoffRecipientMatches(ctx, q, w, task) {
			return true
		}
		// Explicit recipient identity is authority independent of provider
		// context. An ordinary continuation may not borrow ancestry after its
		// same-task fallback, even if a late pin restores the abandoned ID.
		if task.RetainedContextInvalidated {
			return false
		}
		parent := task.CommentResumeFromTaskID
		if parent.Valid {
			if task.ForceFreshSession {
				return false
			}
			// A live delivered comment is required even after a coalesced plan
			// changes its primary trigger. Deletion/redaction revokes authority.
			liveComment := false
			for _, id := range task.DeliveredCommentIds {
				if id != task.TriggerCommentID && !slices.Contains(task.CoalescedCommentIds, id) {
					continue
				}
				comment, e := q.GetComment(ctx, id)
				if e == nil && comment.IssueID == w.IssueID && comment.WorkspaceID == w.WorkspaceID && comment.AuthorType == "member" && !comment.DeletedAt.Valid {
					liveComment = true
					break
				}
			}
			if !liveComment {
				return false
			}
		} else {
			parent = task.RetryOfTaskID
			if !parent.Valid {
				parent = task.RerunOfTaskID
			}
			if !parent.Valid {
				return false
			}
		}
		prior, e := q.GetAgentTask(ctx, parent)
		if e != nil || prior.SessionID != task.SessionID || (task.CommentResumeFromTaskID.Valid && prior.Status != "completed") {
			return false
		}
		task = prior
	}
}

// Start is the final point at which an issue freeze or a superseded handoff
// can stop a delivered task. The caller already holds the task row. NOWAIT on
// the issue avoids task→issue waits against a writer taking issue→task locks;
// the caller rolls back and may retry after contention or human supersession.
func (s *TaskService) checkWorkflowStart(ctx context.Context, tx pgx.Tx, q *db.Queries, task db.AgentTaskQueue) error {
	if task.IssueID.Valid {
		var locked pgtype.UUID
		var frozen bool
		if err := tx.QueryRow(ctx, "SELECT id,workflow_frozen FROM issue WHERE id=$1 FOR NO KEY UPDATE NOWAIT", task.IssueID).Scan(&locked, &frozen); err != nil {
			var pgErr *pgconn.PgError
			if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "55P03") {
				return fmt.Errorf("%w: issue unavailable for task start", pgx.ErrNoRows)
			}
			return err
		}
		if frozen {
			return fmt.Errorf("%w: issue workflow is frozen", pgx.ErrNoRows)
		}
		claimable, err := q.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{
			TaskID: task.ID, IssueID: task.IssueID,
		})
		if err != nil {
			return err
		}
		if !claimable {
			return fmt.Errorf("%w: issue workflow no longer permits task start", pgx.ErrNoRows)
		}
	}
	if len(task.Context) == 0 {
		return nil
	}
	var contextValue struct {
		Handoff  json.RawMessage `json:"workflow_handoff"`
		Recovery json.RawMessage `json:"workflow_recovery"`
		Outcome  json.RawMessage `json:"workflow_outcome"`
		WakeupID string          `json:"wakeup_id"`
	}
	if err := json.Unmarshal(task.Context, &contextValue); err != nil {
		return fmt.Errorf("%w: invalid task context", pgx.ErrNoRows)
	}
	if len(contextValue.Handoff) == 0 && len(contextValue.Recovery) == 0 && len(contextValue.Outcome) == 0 {
		return nil
	}
	if len(contextValue.Outcome) != 0 {
		if len(contextValue.Handoff) != 0 || len(contextValue.Recovery) != 0 ||
			validateWorkflowOutcomeTask(ctx, tx, task) != nil {
			return fmt.Errorf("%w: workflow outcome no longer claimable", pgx.ErrNoRows)
		}
		return nil
	}
	if len(contextValue.Handoff) != 0 && len(contextValue.Recovery) != 0 {
		return fmt.Errorf("%w: ambiguous workflow task context", pgx.ErrNoRows)
	}
	if len(contextValue.Recovery) != 0 {
		if _, err := s.validateWorkflowRecoverySourceWithQueries(ctx, q, task); err != nil {
			return fmt.Errorf("%w: workflow recovery no longer claimable", pgx.ErrNoRows)
		}
		return nil
	}
	id, err := wakeupUUID(contextValue.WakeupID)
	if err != nil || !id.Valid || !task.IssueID.Valid {
		return fmt.Errorf("%w: invalid handoff context", pgx.ErrNoRows)
	}
	w, err := q.LocklessWakeup(ctx, id)
	if err != nil || len(w.Handoff) == 0 {
		return fmt.Errorf("%w: handoff unavailable", pgx.ErrNoRows)
	}
	if err = (&IssueWakeupService{Tasks: s}).checkClaimWithQueries(ctx, q, task); err != nil {
		if errors.Is(err, ErrWakeupForbidden) || errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: handoff no longer claimable", pgx.ErrNoRows)
		}
		return err
	}
	return nil
}

func handoffNote(intent issueHandoffIntent, candidateID pgtype.UUID) string {
	var b strings.Builder
	b.WriteString(intent.Instruction)
	if candidateID.Valid {
		fmt.Fprintf(&b, "\n\nCandidate ID: %s", util.UUIDToString(candidateID))
	}
	if len(intent.Candidates) != 0 {
		b.WriteString("\n\nCandidate references (verify these exact commits before acting):")
		for _, c := range intent.Candidates {
			fmt.Fprintf(&b, "\n- Repository: %s; draft PR: %s; branch: %s; commit: %s", c.RepositoryURL, c.PRURL, c.Branch, c.CommitSHA)
		}
	}
	if len(intent.EvidenceURLs) != 0 {
		b.WriteString("\n\nEvidence links:")
		for _, ref := range intent.EvidenceURLs {
			fmt.Fprintf(&b, "\n- %s", ref)
		}
	}
	return b.String()
}

func (s *IssueWakeupService) dispatchHandoff(ctx context.Context, tx pgx.Tx, q *db.Queries, issue db.Issue, w db.IssueWakeup, agent db.Agent, overlay runtimeMCPOverlayData) error {
	intent, err := handoffIntent(w)
	if err != nil {
		return err
	}
	if w.LastTaskID.Valid || w.HandoffCompletedAt.Valid {
		if err = q.DiscardWakeupReceipts(ctx, w.ID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	outgoing, err := q.LockWakeupSourceTask(ctx, db.LockWakeupSourceTaskParams{ID: w.FilterTaskID, IssueID: w.IssueID})
	if err != nil {
		return err
	}
	receipts, err := q.ListPendingWakeupReceipts(ctx, db.ListPendingWakeupReceiptsParams{WakeupID: w.ID, Revision: w.Revision})
	if err != nil {
		return err
	}
	if len(receipts) == 0 {
		return tx.Commit(ctx)
	}
	ids := make([]pgtype.UUID, 0, len(receipts))
	for _, receipt := range receipts {
		ids = append(ids, receipt.ID)
	}
	if outgoing.Status == "failed" || outgoing.Status == "cancelled" {
		if err = q.ConsumeWakeupReceipts(ctx, db.ConsumeWakeupReceiptsParams{Ids: ids}); err != nil {
			return err
		}
		if err = q.NoteWakeupFailure(ctx, db.NoteWakeupFailureParams{ID: w.ID, LastError: pgtype.Text{String: "Outgoing task " + intent.OutgoingTaskID + " " + outgoing.Status + "; cancel this handoff and create a new request after recovery.", Valid: true}}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if outgoing.Status != "completed" {
		return tx.Commit(ctx)
	}
	if !handoffIssueSnapshotMatches(issue, intent) {
		if err = q.ConsumeWakeupReceipts(ctx, db.ConsumeWakeupReceiptsParams{Ids: ids}); err != nil {
			return err
		}
		if err = q.NoteWakeupFailure(ctx, db.NoteWakeupFailureParams{ID: w.ID, LastError: pgtype.Text{String: "Issue owner or status changed after handoff registration; cancel this handoff and create a new request.", Valid: true}}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM agent_task_queue WHERE issue_id=$1 AND id<>$2 AND status IN ('dispatched','running','waiting_local_directory'))", w.IssueID, outgoing.ID).Scan(&active); err != nil {
		return err
	}
	if active {
		return tx.Commit(ctx)
	}
	if err = guardIssueNotInTriage(ctx, q, issue.ID, OriginNamed); err != nil {
		return err
	}
	assigneeID, _ := wakeupUUID(intent.AssigneeID)
	if intent.AssigneeType == "member" {
		if _, err = q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID: assigneeID, WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				if err = q.ConsumeWakeupReceipts(ctx, db.ConsumeWakeupReceiptsParams{Ids: ids}); err != nil {
					return err
				}
				if err = q.NoteWakeupFailure(ctx, db.NoteWakeupFailureParams{ID: w.ID, LastError: pgtype.Text{String: "Member recipient is no longer in the workspace; cancel this handoff and create a new request.", Valid: true}}); err != nil {
					return err
				}
				return tx.Commit(ctx)
			}
			return err
		}
	}
	// The native recipient must own the default-thread pending slot. An older
	// generic plan cannot execute while this handoff is active, so retire that
	// queued plan with its original evidence retained on the cancelled row.
	var superseded []db.AgentTaskQueue
	if intent.AssigneeType == "agent" {
		superseded, err = q.SupersedeUnstartedGenericTaskForWorkflow(ctx, db.SupersedeUnstartedGenericTaskForWorkflowParams{
			IssueID: w.IssueID, AgentID: w.AgentID, ReplacementRef: util.UUIDToString(w.ID),
		})
		if err != nil {
			return err
		}
	}
	candidateID, err := s.registerWorkflowCandidateFromHandoff(ctx, tx, issue, w, outgoing, intent.Candidates)
	if err != nil {
		return err
	}
	updated, err := q.UpdateIssue(ctx, db.UpdateIssueParams{
		ID: issue.ID, ExpectedRevision: pgtype.Int8{Int64: issue.Revision, Valid: true}, SourceTaskID: outgoing.ID,
		Title: pgtype.Text{String: issue.Title, Valid: true}, Description: issue.Description,
		Status: pgtype.Text{String: intent.Status, Valid: true}, Priority: pgtype.Text{String: issue.Priority, Valid: true},
		AssigneeType: pgtype.Text{String: intent.AssigneeType, Valid: true}, AssigneeID: assigneeID,
		StartDate: issue.StartDate, DueDate: issue.DueDate, ParentIssueID: issue.ParentIssueID, ProjectID: issue.ProjectID, Stage: issue.Stage,
	})
	if err != nil {
		return err
	}
	activities := make([]db.ActivityLog, 0, 2)
	if issue.Status != updated.Status {
		details, _ := json.Marshal(map[string]string{"from": issue.Status, "to": updated.Status})
		activity, e := q.CreateActivity(ctx, db.CreateActivityParams{ID: dbid.NewV7(), WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ActorType: pgtype.Text{String: "agent", Valid: true}, ActorID: outgoing.AgentID, Action: "status_changed", Details: details})
		if e != nil {
			return e
		}
		activities = append(activities, activity)
	}
	if issue.AssigneeType != updated.AssigneeType || issue.AssigneeID != updated.AssigneeID {
		details := map[string]string{"to_type": intent.AssigneeType, "to_id": util.UUIDToString(assigneeID)}
		if issue.AssigneeType.Valid {
			details["from_type"] = issue.AssigneeType.String
		}
		if issue.AssigneeID.Valid {
			details["from_id"] = util.UUIDToString(issue.AssigneeID)
		}
		encoded, _ := json.Marshal(details)
		activity, e := q.CreateActivity(ctx, db.CreateActivityParams{ID: dbid.NewV7(), WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ActorType: pgtype.Text{String: "agent", Valid: true}, ActorID: outgoing.AgentID, Action: "assignee_changed", Details: encoded})
		if e != nil {
			return e
		}
		activities = append(activities, activity)
	}
	var task db.AgentTaskQueue
	if intent.AssigneeType == "agent" {
		workflowContext := map[string]any{
			"wakeup_id": util.UUIDToString(w.ID), "request_key": intent.RequestKey,
			"outgoing_task_id": intent.OutgoingTaskID, "context_mode": intent.ContextMode,
			"candidate_id": util.UUIDToString(candidateID),
			"candidates":   intent.Candidates, "evidence_urls": intent.EvidenceURLs,
		}
		if intent.ContextMode == "resume" {
			workflowContext["resume_task_id"] = intent.ResumeTaskID
		}
		contextJSON, err := json.Marshal(map[string]any{"wakeup_id": util.UUIDToString(w.ID), "wakeup_revision": w.Revision, "workflow_handoff": workflowContext})
		if err != nil {
			return err
		}
		task, err = q.CreateWakeupTask(ctx, db.CreateWakeupTaskParams{
			ForceFreshSession: pgtype.Bool{Bool: true, Valid: true}, ID: dbid.NewV7(), AgentID: w.AgentID, RuntimeID: agent.RuntimeID,
			IssueID: w.IssueID, Priority: priorityToInt(updated.Priority),
			TriggerSummary:   pgtype.Text{String: "Workflow handoff: " + truncateForSummary(intent.Instruction, 160), Valid: true},
			HandoffNote:      pgtype.Text{String: handoffNote(intent, candidateID), Valid: true},
			OriginatorUserID: w.CreatedBy, AccountableUserID: w.CreatedBy,
			OriginatorSource:    pgtype.Text{String: "trigger_owner", Valid: true},
			TriggerEvidenceKind: pgtype.Text{String: "issue_handoff", Valid: true}, TriggerEvidenceRefID: w.ID,
			DelegatedFromTaskID: outgoing.ID, WakeupContext: contextJSON,
			RuntimeMcpOverlay: overlay.Overlay, RuntimeConnectedApps: overlay.ConnectedApps,
		})
		if err != nil {
			return err
		}
	}
	if err = q.ConsumeWakeupReceipts(ctx, db.ConsumeWakeupReceiptsParams{Ids: ids, TaskID: task.ID}); err != nil {
		return err
	}
	if _, err = q.CompleteIssueHandoff(ctx, db.CompleteIssueHandoffParams{ID: w.ID, RecipientTaskID: task.ID}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.publishHandoffIssueChange(ctx, issue, updated, outgoing.AgentID, activities)
	for _, old := range superseded {
		s.Tasks.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, old)
	}
	if task.ID.Valid {
		s.Tasks.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
		s.Tasks.NotifyTaskEnqueued(ctx, task)
	}
	return nil
}

func (s *IssueWakeupService) publishHandoffIssueChange(ctx context.Context, before, after db.Issue, outgoingAgent pgtype.UUID, activities []db.ActivityLog) {
	if s.Tasks.Bus == nil {
		return
	}
	workspaceID := util.UUIDToString(after.WorkspaceID)
	actorID := util.UUIDToString(outgoingAgent)
	s.Tasks.Bus.Publish(events.Event{
		Type: protocol.EventIssueUpdated, WorkspaceID: workspaceID, ActorType: "agent", ActorID: actorID,
		Payload: map[string]any{
			"issue":            IssueToMapResolved(ctx, s.Tasks.Queries, after, s.Tasks.getIssuePrefix(after.WorkspaceID)),
			"status_changed":   before.Status != after.Status,
			"assignee_changed": before.AssigneeType != after.AssigneeType || before.AssigneeID != after.AssigneeID,
			"prev_status":      before.Status,
		},
	})
	for _, activity := range activities {
		s.Tasks.Bus.Publish(events.Event{
			Type: protocol.EventActivityCreated, WorkspaceID: workspaceID, ActorType: "agent", ActorID: actorID,
			Payload: map[string]any{"issue_id": util.UUIDToString(after.ID), "entry": map[string]any{
				"type": "activity", "id": util.UUIDToString(activity.ID), "actor_type": "agent", "actor_id": actorID,
				"action": activity.Action, "details": json.RawMessage(activity.Details), "created_at": util.TimestampToString(activity.CreatedAt),
			}},
		})
	}
}
