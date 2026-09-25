package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/attribution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// RejectWorkflow revokes candidate authority and starts one explicit new work
// run in the same issue-locked transaction. Already delivered PR actions remain
// visible as history; pending actions cannot run after authority revocation.
func (s WorkflowAuthorityService) RejectWorkflow(ctx context.Context, workspaceID, issueID pgtype.UUID, actor WorkflowActor, in WorkflowRejectionInput) error {
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return ErrWorkflowAuthorityUnavailable
	}
	candidateID, err := workflowAuthorityUUID(in.CandidateID)
	if err != nil || in.ExpectedRevision < 1 {
		return fmt.Errorf("%w: candidate_id and positive expected_revision are required", ErrWorkflowAuthorityInput)
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if (in.Kind != "in_scope_defect" && in.Kind != "scope_change") || len(in.Reason) == 0 || len(in.Reason) > 4000 {
		return fmt.Errorf("%w: rejection kind, reason and retained context are inconsistent", ErrWorkflowAuthorityInput)
	}
	var resumeID pgtype.UUID
	if in.ResumeTaskID != "" {
		resumeID, err = workflowAuthorityUUID(in.ResumeTaskID)
		if err != nil {
			return err
		}
	}
	if actor.Type != "member" {
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
	role, actorID, err := workflowMemberRole(ctx, tx, workspaceID, actor)
	if err != nil {
		return err
	}
	if role != "owner" && role != "admin" &&
		(issue.AssigneeType.String != "member" || issue.AssigneeID != actorID) {
		return ErrWorkflowAuthorityForbidden
	}
	// Replay is checked before current-candidate and revision fences, because
	// the original successful response may have been lost after commit.
	var previousActor, previousKind, previousReason string
	var previousResume pgtype.UUID
	var previousRevision int64
	err = tx.QueryRow(ctx, `SELECT actor_id::text,kind,reason,resume_task_id,issue_revision
		FROM issue_workflow_rejection WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3
		ORDER BY created_at DESC,id DESC LIMIT 1`, workspaceID, issueID, candidateID).Scan(
		&previousActor, &previousKind, &previousReason, &previousResume, &previousRevision)
	if err == nil {
		if previousActor == actor.ID && previousKind == in.Kind && previousReason == in.Reason &&
			previousResume == resumeID && previousRevision == in.ExpectedRevision+1 {
			return tx.Commit(ctx)
		}
		return ErrWorkflowAuthorityConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID || issue.Revision != in.ExpectedRevision {
		return ErrWorkflowAuthorityConflict
	}
	pinned, authority, err := workflowAuthorityPolicy(ctx, s, issue)
	if err != nil {
		return err
	}
	if issue.Status != "in_review" && issue.Status != "done" &&
		(authority.FormatVersion != 2 || issue.Status != authority.AcceptedStatusKey) {
		return ErrWorkflowAuthorityConflict
	}
	candidate, err := loadCurrentWorkflowCandidate(ctx, tx, issue, pinned.Version)
	if err != nil {
		return err
	}
	active, err := workflowHasMutableRuns(ctx, tx, issue.ID, pgtype.UUID{})
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
	if err != nil || !agent.RuntimeID.Valid || (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, workspaceID, actorID, agent) != nil {
		return ErrWorkflowAuthorityForbidden
	}
	canResume := sourceSession != "" && sourceRuntimeID == agent.RuntimeID
	if canResume && !resumeID.Valid {
		return fmt.Errorf("%w: exact retained writer context is required", ErrWorkflowAuthorityInput)
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
	if (issue.Status == "done" || authority.FormatVersion == 2 && issue.Status == authority.AcceptedStatusKey) && !acceptanceID.Valid {
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
		revoked_by_type='member',revoked_by_id=$3
		WHERE issue_id=$1 AND candidate_id=$2 AND revoked_at IS NULL`, issue.ID, candidateID, actorID); err != nil {
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
		AND started_at IS NULL`, issue.ID); err != nil {
		return err
	}
	var acceptedID any
	if acceptanceID.Valid {
		acceptedID = acceptanceID
	}
	rejectionID := dbid.NewV7()
	_, err = tx.Exec(ctx, `INSERT INTO issue_workflow_rejection
		(id,workspace_id,issue_id,candidate_id,acceptance_id,actor_type,actor_id,kind,reason,
		resume_task_id,resume_agent_id,issue_revision,context_mode,continuity_note)
		VALUES($1,$2,$3,$4,$5,'member',$6,$7,$8,$9,$10,$11,$12,$13)`, rejectionID,
		workspaceID, issueID, candidateID, acceptedID, actorID, in.Kind, in.Reason, resumeID, agentID, issue.Revision+1,
		contextMode, continuityNote)
	if err != nil {
		return err
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
	continuation += "\n" + continuityNote
	queued, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		ID: dbid.NewV7(), AgentID: agentID, RuntimeID: agent.RuntimeID, IssueID: issue.ID,
		Priority: priorityToInt(issue.Priority), ForceFreshSession: pgtype.Bool{Bool: true, Valid: true},
		HandoffNote:   pgtype.Text{String: continuation, Valid: true},
		RerunOfTaskID: actualResumeID, OriginatorUserID: actorID, AccountableUserID: actorID,
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
	return nil
}
