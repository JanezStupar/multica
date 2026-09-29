package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/attribution"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RejectWorkflow revokes candidate authority and starts one explicit new work
// run in the same issue-locked transaction. Already delivered PR actions remain
// visible as history; pending actions cannot run after authority revocation.
func (s WorkflowAuthorityService) RejectWorkflow(ctx context.Context, workspaceID, issueID pgtype.UUID, actor WorkflowActor, in WorkflowRejectionInput) error {
	return s.rejectWorkflow(ctx, workspaceID, issueID, actor, in, pgtype.UUID{})
}

// ContinueWorkflowFeedback lets the assigned agent, or the exact coordinator
// task for a completed human handoff, turn delivered human feedback into a
// correction run. A question has no side effect unless the agent explicitly
// classifies it as a correction.
func (s WorkflowAuthorityService) ContinueWorkflowFeedback(ctx context.Context, workspaceID, issueID pgtype.UUID, actor WorkflowActor, in WorkflowFeedbackContinuationInput) error {
	commentID, err := workflowAuthorityUUID(in.CommentID)
	if err != nil {
		return fmt.Errorf("%w: comment_id is required", ErrWorkflowAuthorityInput)
	}
	return s.rejectWorkflow(ctx, workspaceID, issueID, actor, WorkflowRejectionInput{
		CandidateID: in.CandidateID, ExpectedRevision: in.ExpectedRevision, Kind: in.Kind,
	}, commentID)
}

// A human-assigned issue retains the human assignee while a coordinator is
// woken by a comment. Only the exact server-created feedback task for the
// latest completed member handoff may use that temporary execution authority.
func workflowHumanFeedbackTask(ctx context.Context, tx pgx.Tx, issue db.Issue, agentID, taskID, candidateID, commentID pgtype.UUID) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM agent_task_queue task
		WHERE task.id=$1 AND task.issue_id=$2 AND task.agent_id=$3
		AND task.context->'workflow_feedback'->>'candidate_id'=$4::uuid::text
		AND ($5::uuid=task.trigger_comment_id OR $5::uuid=ANY(task.coalesced_comment_ids))
		AND workflow_human_comment_task_current(task.id,task.issue_id)
		AND workflow_recorded_comment_input_current(task.issue_id,task.agent_id,$5))`,
		taskID, issue.ID, agentID, candidateID, commentID).Scan(&allowed)
	return allowed, err
}

// A second human's comment may already have its own deferred coordinator task
// when the first feedback task rejects the candidate. Keep that comment's
// author, runtime overlay and agent instead of cancelling the obligation or
// moving those credentials to the writer. It becomes an ordinary conversation
// about the now-current issue, with no authority over the superseded candidate.
type preservedWorkflowComments struct {
	ids        []pgtype.UUID
	runtimeIDs []pgtype.UUID
}

func (p *preservedWorkflowComments) add(id, runtimeID pgtype.UUID) {
	p.ids = append(p.ids, id)
	for _, prior := range p.runtimeIDs {
		if prior == runtimeID {
			return
		}
	}
	p.runtimeIDs = append(p.runtimeIDs, runtimeID)
}

func (s WorkflowAuthorityService) preserveSupersededWorkflowComments(ctx context.Context, tx pgx.Tx, q *db.Queries, issue db.Issue, candidateID pgtype.UUID) (preservedWorkflowComments, error) {
	var preserved preservedWorkflowComments
	if issue.AssigneeType.String != "member" {
		var marked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue task
			WHERE task.issue_id=$1 AND task.status IN ('queued','deferred')
			AND task.trigger_evidence_kind='workflow_human_comment'
			AND task.context->'workflow_feedback'->>'candidate_id'=$2)`, issue.ID,
			util.UUIDToString(candidateID)).Scan(&marked); err != nil {
			return preserved, err
		}
		if marked {
			return preserved, fmt.Errorf("%w: queued human feedback has no current human handoff", ErrWorkflowAuthorityConflict)
		}
		return preserved, nil
	}
	rows, err := tx.Query(ctx, `SELECT task.id,task.agent_id,task.runtime_id,task.originator_user_id,feedback_comment.id
		FROM agent_task_queue task
		JOIN LATERAL (SELECT input.* FROM comment input
			JOIN member m ON m.workspace_id=$2 AND m.user_id=input.author_id
			WHERE input.issue_id=$1 AND input.workspace_id=$2
			AND (input.id=task.trigger_comment_id OR input.id=ANY(task.coalesced_comment_ids))
			AND input.author_type='member' AND input.type IN ('comment','progress_update') AND input.deleted_at IS NULL
			AND input.author_id=task.originator_user_id AND input.author_id=task.accountable_user_id
			AND btrim(input.content)<>'' AND input.content !~* '^\s*/note(\s|$)'
			AND input.created_at>(SELECT candidate.created_at FROM issue_workflow_candidate candidate
				WHERE candidate.id=$5 AND candidate.issue_id=$1)
			AND (input.author_id=$4 OR m.role IN ('owner','admin'))
			ORDER BY input.created_at DESC,input.id DESC LIMIT 1) feedback_comment ON true
		WHERE task.issue_id=$1 AND task.status IN ('queued','deferred') AND task.started_at IS NULL
		AND task.trigger_evidence_kind='workflow_human_comment'
		AND task.context->'workflow_feedback'->>'candidate_id'=$3
		AND workflow_human_comment_task_current(task.id,$1)
		ORDER BY task.created_at,task.id FOR UPDATE OF task`, issue.ID, issue.WorkspaceID,
		util.UUIDToString(candidateID), issue.AssigneeID, candidateID)
	if err != nil {
		return preserved, err
	}
	type pending struct{ id, agentID, runtimeID, authorID, commentID pgtype.UUID }
	var candidates []pending
	for rows.Next() {
		var row pending
		if err := rows.Scan(&row.id, &row.agentID, &row.runtimeID, &row.authorID, &row.commentID); err != nil {
			rows.Close()
			return preserved, err
		}
		candidates = append(candidates, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return preserved, err
	}
	var markedCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue task
		WHERE task.issue_id=$1 AND task.status IN ('queued','deferred')
		AND task.trigger_evidence_kind='workflow_human_comment'
		AND task.context->'workflow_feedback'->>'candidate_id'=$2`, issue.ID,
		util.UUIDToString(candidateID)).Scan(&markedCount); err != nil {
		return preserved, err
	}
	if markedCount != len(candidates) {
		return preserved, fmt.Errorf("%w: queued human feedback is no longer tied to the current handoff", ErrWorkflowAuthorityConflict)
	}
	for _, row := range candidates {
		agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: row.agentID, WorkspaceID: issue.WorkspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return preserved, fmt.Errorf("%w: queued human feedback coordinator is unavailable", ErrWorkflowAuthorityConflict)
		}
		if err != nil {
			return preserved, err
		}
		if !agent.RuntimeID.Valid || agent.RuntimeID != row.runtimeID {
			return preserved, fmt.Errorf("%w: queued human feedback coordinator runtime changed", ErrWorkflowAuthorityConflict)
		}
		if err := (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, issue.WorkspaceID, row.authorID, agent); err != nil {
			if errors.Is(err, ErrWakeupForbidden) {
				return preserved, fmt.Errorf("%w: queued human feedback author can no longer invoke the coordinator", ErrWorkflowAuthorityConflict)
			}
			return preserved, err
		}
		note := fmt.Sprintf("Human comment %s was queued for candidate %s, which has since been superseded. Read the current ticket and this exact comment before responding. Do not act on the old candidate's acceptance or delivery.",
			util.UUIDToString(row.commentID), util.UUIDToString(candidateID))
		command, err := tx.Exec(ctx, `UPDATE agent_task_queue SET status='deferred',fire_at=clock_timestamp(),
			priority=$2,context=COALESCE(context,'{}'::jsonb)-'workflow_feedback'-'head_sha',
			trigger_evidence_kind=NULL,trigger_evidence_ref_id=NULL,delegated_from_task_id=NULL,
			trigger_comment_id=$4,
			coalesced_comment_ids=(SELECT COALESCE(array_agg(input.id),'{}'::uuid[]) FROM comment input
			  WHERE input.issue_id=$5 AND input.workspace_id=$6 AND input.id<>$4
			  AND (input.id=agent_task_queue.trigger_comment_id OR input.id=ANY(agent_task_queue.coalesced_comment_ids))
			  AND input.author_type='member' AND input.author_id=agent_task_queue.originator_user_id
			  AND input.type IN ('comment','progress_update') AND input.deleted_at IS NULL
			  AND btrim(input.content)<>'' AND input.content !~* '^\s*/note(\s|$)'),
			handoff_note=$3 WHERE id=$1 AND status IN ('queued','deferred')`, row.id,
			priorityToInt(issue.Priority)-1, note, row.commentID, issue.ID, issue.WorkspaceID)
		if err != nil {
			return preserved, err
		}
		if command.RowsAffected() != 1 {
			return preserved, ErrWorkflowAuthorityConflict
		}
		preserved.add(row.id, row.runtimeID)
	}
	return preserved, nil
}

// Explicit mentions and ordinary comment conversations are also promises to
// deliver human input. A candidate rejection must not retire them along with
// stale review automation. Keep their original agent, author, overlay and
// comment plan, while making them read the current issue before acting.
func (s WorkflowAuthorityService) preserveOrdinaryHumanComments(ctx context.Context, tx pgx.Tx, q *db.Queries, issue db.Issue, candidateID pgtype.UUID) (preservedWorkflowComments, error) {
	var preserved preservedWorkflowComments
	rows, err := tx.Query(ctx, `SELECT task.id,task.agent_id,task.runtime_id,task.originator_user_id,
		COALESCE(primary_comment.id,task.trigger_comment_id),
		COALESCE(primary_comment.author_type='member' AND primary_comment.deleted_at IS NULL
			AND primary_comment.type IN ('comment','progress_update')
			AND task.originator_source='direct_human'
			AND task.originator_user_id=primary_comment.author_id
			AND task.accountable_user_id=primary_comment.author_id,false),
		(NOT (COALESCE(task.context,'{}'::jsonb) ? 'wakeup_id') OR EXISTS (
			SELECT 1 FROM issue_wakeup w WHERE w.id::text=task.context->>'wakeup_id'
			AND w.issue_id=$1 AND w.workspace_id=$2 AND w.disabled_at IS NULL AND w.handoff IS NULL))
		FROM agent_task_queue task
		LEFT JOIN LATERAL (SELECT input.* FROM comment input
			JOIN member m ON m.workspace_id=$2 AND m.user_id=input.author_id
			WHERE input.issue_id=$1 AND input.workspace_id=$2
			AND (input.id=task.trigger_comment_id OR input.id=ANY(task.coalesced_comment_ids))
			AND input.author_type='member' AND input.author_id=task.originator_user_id
			AND input.author_id=task.accountable_user_id AND input.type IN ('comment','progress_update')
			AND input.deleted_at IS NULL AND btrim(input.content)<>'' AND input.content !~* '^\s*/note(\s|$)'
			ORDER BY input.created_at DESC,input.id DESC LIMIT 1) primary_comment ON true
		WHERE task.issue_id=$1 AND task.status IN ('queued','deferred') AND task.started_at IS NULL
		AND task.trigger_evidence_kind IS DISTINCT FROM 'workflow_human_comment'
		AND EXISTS (SELECT 1 FROM comment input WHERE input.issue_id=$1 AND input.workspace_id=$2
			AND input.author_type='member' AND input.type IN ('comment','progress_update') AND input.deleted_at IS NULL
			AND btrim(input.content)<>'' AND input.content !~* '^\s*/note(\s|$)'
			AND (input.id=task.trigger_comment_id OR input.id=ANY(task.coalesced_comment_ids)))
		ORDER BY task.created_at,task.id FOR UPDATE OF task`, issue.ID, issue.WorkspaceID)
	if err != nil {
		return preserved, err
	}
	type pending struct {
		id, agentID, runtimeID, authorID, commentID pgtype.UUID
		validInput, validWakeup                     bool
	}
	var candidates []pending
	for rows.Next() {
		var row pending
		if err := rows.Scan(&row.id, &row.agentID, &row.runtimeID, &row.authorID,
			&row.commentID, &row.validInput, &row.validWakeup); err != nil {
			rows.Close()
			return preserved, err
		}
		candidates = append(candidates, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return preserved, err
	}
	for _, row := range candidates {
		if !row.validInput || !row.validWakeup {
			return preserved, fmt.Errorf("%w: pending human comment lacks direct, live task provenance", ErrWorkflowAuthorityConflict)
		}
		agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: row.agentID, WorkspaceID: issue.WorkspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			return preserved, fmt.Errorf("%w: pending human comment agent is unavailable", ErrWorkflowAuthorityConflict)
		}
		if err != nil {
			return preserved, err
		}
		if !agent.RuntimeID.Valid || agent.RuntimeID != row.runtimeID {
			return preserved, fmt.Errorf("%w: pending human comment agent runtime changed", ErrWorkflowAuthorityConflict)
		}
		if err := (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, issue.WorkspaceID, row.authorID, agent); err != nil {
			if errors.Is(err, ErrWakeupForbidden) {
				return preserved, fmt.Errorf("%w: pending human comment author can no longer invoke the agent", ErrWorkflowAuthorityConflict)
			}
			return preserved, err
		}
		note := fmt.Sprintf("Candidate %s has been superseded. Read the current ticket and the task's trigger and coalesced human comments before responding. Do not act on the old candidate's acceptance or delivery.",
			util.UUIDToString(candidateID))
		command, err := tx.Exec(ctx, `UPDATE agent_task_queue SET status='deferred',fire_at=clock_timestamp(),
			priority=$2,context=COALESCE(context,'{}'::jsonb)-'workflow_feedback'-'head_sha',
			trigger_comment_id=$4,
			coalesced_comment_ids=(SELECT COALESCE(array_agg(input.id),'{}'::uuid[]) FROM comment input
			  WHERE input.issue_id=$5 AND input.workspace_id=$6 AND input.id<>$4
			  AND (input.id=agent_task_queue.trigger_comment_id OR input.id=ANY(agent_task_queue.coalesced_comment_ids))
			  AND input.author_type='member'
			  AND input.type IN ('comment','progress_update') AND input.deleted_at IS NULL
			  AND btrim(input.content)<>'' AND input.content !~* '^\s*/note(\s|$)'),
			handoff_note=concat_ws(E'\n',NULLIF(handoff_note,''),$3::text)
			WHERE id=$1 AND status IN ('queued','deferred')`, row.id, priorityToInt(issue.Priority)-1, note,
			row.commentID, issue.ID, issue.WorkspaceID)
		if err != nil {
			return preserved, err
		}
		if command.RowsAffected() != 1 {
			return preserved, ErrWorkflowAuthorityConflict
		}
		preserved.add(row.id, row.runtimeID)
	}
	return preserved, nil
}

func (s WorkflowAuthorityService) rejectWorkflow(ctx context.Context, workspaceID, issueID pgtype.UUID, actor WorkflowActor, in WorkflowRejectionInput, feedbackCommentID pgtype.UUID) error {
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return ErrWorkflowAuthorityUnavailable
	}
	candidateID, err := workflowAuthorityUUID(in.CandidateID)
	if err != nil || in.ExpectedRevision < 1 {
		return fmt.Errorf("%w: candidate_id and positive expected_revision are required", ErrWorkflowAuthorityInput)
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if (in.Kind != "in_scope_defect" && in.Kind != "scope_change") ||
		(!feedbackCommentID.Valid && (len(in.Reason) == 0 || len(in.Reason) > 4000)) ||
		(feedbackCommentID.Valid && (in.Reason != "" || in.ResumeTaskID != "")) {
		return fmt.Errorf("%w: rejection kind, reason and retained context are inconsistent", ErrWorkflowAuthorityInput)
	}
	var resumeID pgtype.UUID
	if in.ResumeTaskID != "" {
		resumeID, err = workflowAuthorityUUID(in.ResumeTaskID)
		if err != nil {
			return err
		}
	}
	if feedbackCommentID.Valid && actor.Type != "agent" || !feedbackCommentID.Valid && actor.Type != "member" {
		return ErrWorkflowAuthorityForbidden
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	issue, err := lockWorkflowAuthorityIssue(ctx, tx, q, workspaceID, issueID)
	if err != nil {
		return err
	}
	var humanTerminal bool
	if err := tx.QueryRow(ctx, `SELECT workflow_human_last_done($1) AND
		(i.status='done' OR EXISTS(SELECT 1 FROM issue_status s WHERE s.workspace_id=i.workspace_id AND s.key=i.status AND s.category='done'))
		FROM issue i WHERE i.id=$1 AND i.workspace_id=$2`, issueID, workspaceID).Scan(&humanTerminal); err != nil {
		return err
	}
	if humanTerminal {
		if actor.Type != "member" || !actor.HumanCredential {
			return ErrWorkflowAuthorityForbidden
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('multica.human_rejection_member_id',$1,true)`, actor.ID); err != nil {
			return err
		}
	}
	var actorID, sourceTaskID, feedbackAuthorID pgtype.UUID
	var feedbackRole string
	if feedbackCommentID.Valid {
		actorID, sourceTaskID, err = workflowAgentTask(ctx, tx, issue, actor)
		if err != nil {
			return ErrWorkflowAuthorityForbidden
		}
	} else {
		var role string
		role, actorID, err = workflowMemberRole(ctx, tx, workspaceID, actor)
		if err != nil {
			return err
		}
		if role != "owner" && role != "admin" &&
			(issue.AssigneeType.String != "member" || issue.AssigneeID != actorID) {
			return ErrWorkflowAuthorityForbidden
		}
	}
	// Replay is checked before current-candidate and revision fences, because
	// the original successful response may have been lost after commit.
	var previousActor, previousKind, previousReason string
	var previousResume, previousComment, previousSource pgtype.UUID
	var previousRevision int64
	err = tx.QueryRow(ctx, `SELECT actor_id::text,kind,reason,resume_task_id,issue_revision,comment_id,source_task_id
		FROM issue_workflow_rejection WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3
		ORDER BY created_at DESC,id DESC LIMIT 1`, workspaceID, issueID, candidateID).Scan(
		&previousActor, &previousKind, &previousReason, &previousResume, &previousRevision, &previousComment, &previousSource)
	if err == nil {
		if previousActor == actor.ID && previousKind == in.Kind && previousRevision == in.ExpectedRevision+1 &&
			(feedbackCommentID.Valid && previousComment == feedbackCommentID && previousSource == sourceTaskID ||
				!feedbackCommentID.Valid && !previousComment.Valid && previousReason == in.Reason && previousResume == resumeID) {
			return tx.Commit(ctx)
		}
		return ErrWorkflowAuthorityConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if feedbackCommentID.Valid {
		assignedAgent := issue.AssigneeType.String == "agent" && issue.AssigneeID == actorID
		if !assignedAgent {
			if issue.AssigneeType.String != "member" {
				return ErrWorkflowAuthorityForbidden
			}
			allowed, err := workflowHumanFeedbackTask(ctx, tx, issue, actorID, sourceTaskID, candidateID, feedbackCommentID)
			if err != nil {
				return err
			}
			if !allowed {
				return ErrWorkflowAuthorityForbidden
			}
		}
	}
	if issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID || issue.Revision != in.ExpectedRevision {
		return ErrWorkflowAuthorityConflict
	}
	pinned, authority, err := workflowAuthorityPolicy(ctx, s, issue)
	if err != nil {
		return err
	}
	resumableStatus, err := workflowCorrectionStatus(ctx, tx, issue)
	if err != nil {
		return err
	}
	if !resumableStatus {
		if feedbackCommentID.Valid {
			return fmt.Errorf("%w: closed issue requires explicit reopen before feedback continuation", ErrWorkflowAuthorityConflict)
		}
		return ErrWorkflowAuthorityConflict
	}
	candidate, err := loadCurrentWorkflowCandidate(ctx, tx, issue, pinned.Version)
	if err != nil {
		return err
	}
	var feedbackRevision int64
	if feedbackCommentID.Valid {
		var feedbackContent string
		var deliveredOrCompleted bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM issue_workflow_delivery WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3
			AND status='delivered' AND (action='merge' OR merged_at IS NOT NULL)
			UNION ALL
			SELECT 1 FROM issue_workflow_acceptance WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3
			AND state='accepted' AND outcome_complete AND revoked_at IS NULL)`, workspaceID, issue.ID, candidateID).Scan(&deliveredOrCompleted); err != nil {
			return err
		}
		if deliveredOrCompleted {
			return fmt.Errorf("%w: merged or completed work needs a new follow-up scope", ErrWorkflowAuthorityConflict)
		}
		err = tx.QueryRow(ctx, `SELECT c.content,c.revision,c.author_id,m.role FROM comment c
			JOIN agent_task_queue task ON task.id=$4 AND task.issue_id=c.issue_id AND task.agent_id=$5
			JOIN issue_workflow_candidate candidate ON candidate.id=$6 AND candidate.issue_id=c.issue_id
			JOIN member m ON m.workspace_id=$2 AND m.user_id=c.author_id
			WHERE c.id=$1 AND c.workspace_id=$2 AND c.issue_id=$3 AND c.author_type='member'
			AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
			AND c.created_at>candidate.created_at
			AND task.dispatched_at IS NOT NULL AND c.updated_at<=task.dispatched_at
			AND (task.trigger_comment_id=c.id OR c.id=ANY(task.coalesced_comment_ids))
			AND (NOT $7::boolean OR
				(task.originator_user_id=c.author_id AND task.accountable_user_id=c.author_id))
			AND c.id=ANY(task.delivered_comment_ids) FOR SHARE OF c`,
			feedbackCommentID, workspaceID, issueID, sourceTaskID, actorID, candidateID,
			issue.AssigneeType.String == "member").Scan(
			&feedbackContent, &feedbackRevision, &feedbackAuthorID, &feedbackRole)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && strings.TrimSpace(feedbackContent) == "" {
			return fmt.Errorf("%w: current task has no live human feedback on this candidate", ErrWorkflowAuthorityForbidden)
		}
		if err != nil {
			return err
		}
		if len(feedbackContent) > 4000 {
			return fmt.Errorf("%w: feedback exceeds 4000 bytes for immutable continuation", ErrWorkflowAuthorityInput)
		}
		firstToken := strings.Fields(feedbackContent)[0]
		if strings.EqualFold(firstToken, "/note") {
			return ErrWorkflowAuthorityForbidden
		}
		in.Reason = feedbackContent
		var accepting bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_acceptance
			WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3
			AND state IN ('requested','accepted') AND revoked_at IS NULL)`,
			workspaceID, issue.ID, candidateID).Scan(&accepting); err != nil {
			return err
		}
		if accepting && feedbackRole != "owner" && feedbackRole != "admin" &&
			(issue.AssigneeType.String != "member" || issue.AssigneeID != feedbackAuthorID) {
			return ErrWorkflowAuthorityForbidden
		}
		if issue.AssigneeType.String == "member" && issue.AssigneeID != feedbackAuthorID &&
			feedbackRole != "owner" && feedbackRole != "admin" {
			return ErrWorkflowAuthorityForbidden
		}
	}
	active, err := workflowHasMutableRuns(ctx, tx, issue.ID, sourceTaskID)
	if err != nil {
		return err
	}
	if active {
		return fmt.Errorf("%w: cancel or finish active work before rejection", ErrWorkflowAuthorityConflict)
	}
	// The exact candidate writer is the only retained execution context in this
	// first native path. Another completed task, including a review-only run,
	// cannot be smuggled in as a writer's provider session.
	if resumeID.Valid && resumeID != candidate.WriterTaskID {
		return fmt.Errorf("%w: retained context is not the candidate writer", ErrWorkflowAuthorityInput)
	}
	var agentID, sourceRuntimeID pgtype.UUID
	var sourceStatus, sourceSession string
	err = tx.QueryRow(ctx, `SELECT agent_id,runtime_id,status,COALESCE(session_id,'') FROM agent_task_queue
		WHERE id=$1 AND issue_id=$2`, candidate.WriterTaskID, issue.ID).Scan(
		&agentID, &sourceRuntimeID, &sourceStatus, &sourceSession)
	if err != nil || sourceStatus != "completed" {
		return fmt.Errorf("%w: writer context is unavailable", ErrWorkflowAuthorityConflict)
	}
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
	authorizingUserID := actorID
	if feedbackCommentID.Valid {
		authorizingUserID = feedbackAuthorID
	}
	if err != nil || !agent.RuntimeID.Valid || (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, workspaceID, authorizingUserID, agent) != nil {
		return ErrWorkflowAuthorityForbidden
	}
	canResume := sourceSession != "" && sourceRuntimeID == agent.RuntimeID
	if canResume && !resumeID.Valid && !feedbackCommentID.Valid {
		return fmt.Errorf("%w: exact retained writer context is required", ErrWorkflowAuthorityInput)
	}
	if feedbackCommentID.Valid && canResume {
		resumeID = candidate.WriterTaskID
	}
	ordinaryComments, err := s.preserveOrdinaryHumanComments(ctx, tx, q, issue, candidateID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_task_queue task SET status='cancelled',completed_at=now(),
		error='Human comment inputs withdrawn',cancelled_by_type='system',prepare_lease_expires_at=NULL
		WHERE task.issue_id=$1 AND task.status IN ('queued','deferred') AND task.started_at IS NULL
		AND EXISTS(SELECT 1 FROM comment primary_input WHERE primary_input.id=task.trigger_comment_id
		  AND primary_input.issue_id=$1 AND primary_input.workspace_id=$2 AND primary_input.author_type='member')
		AND NOT EXISTS(SELECT 1 FROM comment input WHERE input.issue_id=$1 AND input.workspace_id=$2
		  AND (input.id=task.trigger_comment_id OR input.id=ANY(task.coalesced_comment_ids))
		  AND input.author_type='member' AND input.type IN ('comment','progress_update') AND input.deleted_at IS NULL
		  AND btrim(input.content)<>'' AND input.content !~* '^\s*/note(\s|$)')`, issue.ID, issue.WorkspaceID); err != nil {
		return err
	}
	retainedComments, err := s.preserveSupersededWorkflowComments(ctx, tx, q, issue, candidateID)
	if err != nil {
		return err
	}
	for _, id := range ordinaryComments.ids {
		// Runtime wakeups are deduplicated below. The id list is the exact set
		// excluded from stale-task cancellation in this transaction.
		retainedComments.ids = append(retainedComments.ids, id)
	}
	for _, runtimeID := range ordinaryComments.runtimeIDs {
		seen := false
		for _, existing := range retainedComments.runtimeIDs {
			if existing == runtimeID {
				seen = true
				break
			}
		}
		if !seen {
			retainedComments.runtimeIDs = append(retainedComments.runtimeIDs, runtimeID)
		}
	}
	actualResumeID := pgtype.UUID{}
	contextMode := "fresh"
	continuityNote := "Retained writer context is unavailable; a fresh session is required."
	if canResume {
		actualResumeID = resumeID
		contextMode = "resume"
		continuityNote = "The exact candidate writer session is retained."
	} else if sourceRuntimeID != agent.RuntimeID {
		continuityNote = "The writer's original runtime changed; start a fresh session on the current runtime."
	}
	var acceptanceID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM issue_workflow_acceptance
		WHERE issue_id=$1 AND candidate_id=$2 AND revoked_at IS NULL AND state IN ('requested','accepted')
		ORDER BY requested_at DESC,id DESC LIMIT 1`, issue.ID, candidateID).Scan(&acceptanceID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !feedbackCommentID.Valid && (issue.Status == "done" || authority.FormatVersion == 2 && issue.Status == authority.AcceptedStatusKey) && !acceptanceID.Valid && !humanTerminal {
		return fmt.Errorf("%w: accepted decision unavailable", ErrWorkflowAuthorityConflict)
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET state='revoked',revoked_at=now()
		WHERE issue_id=$1 AND candidate_id=$2 AND state IN ('requested','accepted') AND revoked_at IS NULL`, issue.ID, candidateID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_delivery SET status='cancelled',last_error_class='revoked',updated_at=now()
		WHERE issue_id=$1 AND candidate_id=$2 AND status IN ('pending','retry','stale','blocked')`, issue.ID, candidateID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_exception SET revoked_at=now(),
		revocation_reason='Candidate rejected',revocation_consequences='Candidate authority invalidated',
		revoked_by_type=$4,revoked_by_id=$3
		WHERE issue_id=$1 AND candidate_id=$2 AND revoked_at IS NULL`, issue.ID, candidateID, actorID, actor.Type); err != nil {
		return err
	}
	// A completed member handoff intentionally remains an active barrier. This
	// decision supersedes it, so a later handoff may be registered normally.
	if _, err := tx.Exec(ctx, `UPDATE issue_wakeup SET enabled=false,disabled_at=now(),updated_at=now()
		WHERE issue_id=$1 AND handoff IS NOT NULL AND disabled_at IS NULL`, issue.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_task_queue SET status='cancelled',completed_at=now(),
		error='Candidate rejected; queued work retired',prepare_lease_expires_at=NULL,
		cancelled_by_type='system' WHERE issue_id=$1 AND status IN ('queued','deferred')
		AND started_at IS NULL AND NOT (id=ANY(COALESCE($2::uuid[],'{}'::uuid[])))`, issue.ID, retainedComments.ids); err != nil {
		return err
	}
	var acceptedID any
	if acceptanceID.Valid {
		acceptedID = acceptanceID
	}
	rejectionID := dbid.NewV7()
	actorType := "member"
	var feedbackRevisionValue any
	if feedbackCommentID.Valid {
		actorType = "agent"
		feedbackRevisionValue = feedbackRevision
	}
	_, err = tx.Exec(ctx, `INSERT INTO issue_workflow_rejection
		(id,workspace_id,issue_id,candidate_id,acceptance_id,actor_type,actor_id,kind,reason,
		resume_task_id,resume_agent_id,issue_revision,context_mode,continuity_note,comment_id,comment_revision,source_task_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, rejectionID,
		workspaceID, issueID, candidateID, acceptedID, actorType, actorID, in.Kind, in.Reason, resumeID, agentID, issue.Revision+1,
		contextMode, continuityNote, feedbackCommentID, feedbackRevisionValue, sourceTaskID)
	if err != nil {
		return err
	}
	if humanTerminal {
		// This explicit human rejection supersedes the earlier human Done
		// decision. Keep both decisions in the timeline; the current transaction
		// is excluded from the completion fence's prior-decision lookup.
		_, err = tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
			VALUES($1,$2,'member',$3,'workflow_human_status_decision',
			jsonb_build_object('from_status',$4::text,'to_status','in_progress',
				'from_revision',$5::bigint,'to_revision',$5::bigint+1,
				'candidate_id',$6::uuid::text,'transaction_id',pg_current_xact_id()::text,
				'source','workflow_rejection'))`, workspaceID, issueID, actorID, issue.Status, issue.Revision, candidateID)
		if err != nil {
			return err
		}
	}
	var updatedRevision int64
	err = tx.QueryRow(ctx, `UPDATE issue SET status='in_progress',assignee_type='agent',assignee_id=$2,
		workflow_candidate_id=NULL,revision=revision+1,updated_at=now(),last_activity_at=now(),
		position=(SELECT COALESCE(MIN(position),0)-1 FROM issue target
		 WHERE target.workspace_id=$3 AND target.status='in_progress')
		WHERE id=$1 AND workspace_id=$3 AND revision=$4 RETURNING revision`, issue.ID, agentID, workspaceID, issue.Revision).Scan(&updatedRevision)
	if err != nil || updatedRevision != issue.Revision+1 {
		return fmt.Errorf("%w: rejection reopen failed: %v", ErrWorkflowAuthorityConflict, err)
	}
	// Both rejection kinds continue the same writer session when it exists.
	// The explicit reason tells the writer to reconcile a changed request
	// before making further changes; a fresh run is allowed when the retained
	// writer session is missing or belongs to a replaced runtime.
	continuation := "Human rejected candidate (" + in.Kind + "): " + in.Reason
	if feedbackCommentID.Valid {
		continuation = fmt.Sprintf("Human feedback snapshot from comment %s at revision %d (%s):\n%s\n",
			util.UUIDToString(feedbackCommentID), feedbackRevision, in.Kind, in.Reason)
		if in.Kind == "scope_change" {
			continuation += "Update the ticket objective to reflect this request before editing code or registering a new candidate. Ask the human if the changed scope is unclear.\n"
		} else {
			continuation += "Reconcile this correction with the ticket before editing or registering a new candidate.\n"
		}
	}
	continuation += "\n" + continuityNote
	queued, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		ID: dbid.NewV7(), AgentID: agentID, RuntimeID: agent.RuntimeID, IssueID: issue.ID,
		// The current claim handler resumes rerun_of_task_id exactly. An older
		// handler sees this flag and starts fresh instead of selecting an
		// unrelated latest session during a rolling deployment.
		Priority: priorityToInt(issue.Priority), ForceFreshSession: pgtype.Bool{Bool: true, Valid: true},
		HandoffNote:   pgtype.Text{String: continuation, Valid: true},
		RerunOfTaskID: actualResumeID, OriginatorUserID: authorizingUserID, AccountableUserID: authorizingUserID,
		OriginatorSource: pgtype.Text{String: string(attribution.SourceDirectHuman), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("%w: rejection continuation could not queue: %v", ErrWorkflowAuthorityConflict, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.PublishWorkflowIssueChange(ctx, issue, actor)
	s.Tasks.broadcastTaskEvent(ctx, protocol.EventTaskQueued, queued)
	s.Tasks.NotifyTaskEnqueued(ctx, queued)
	for _, runtimeID := range retainedComments.runtimeIDs {
		s.Tasks.notifyRuntimeMayHaveWork(runtimeID, "")
	}
	return nil
}
