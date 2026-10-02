package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func authenticatedWakeupTask(ctx context.Context, q *db.Queries, task db.AgentTaskQueue, requireReceipt bool) (bool, error) {
	var source struct {
		ID       string `json:"wakeup_id"`
		Revision int64  `json:"wakeup_revision"`
	}
	if json.Unmarshal(task.Context, &source) != nil || source.ID == "" {
		return false, nil
	}
	return q.HasAuthenticatedWakeupTaskProvenance(ctx, db.HasAuthenticatedWakeupTaskProvenanceParams{
		WakeupID: source.ID, WakeupRevision: source.Revision, IssueID: task.IssueID, AgentID: task.AgentID,
		OriginatorUserID: task.OriginatorUserID, TriggerEvidenceRefID: task.TriggerEvidenceRefID,
		TriggerEvidenceKind: task.TriggerEvidenceKind.String, TaskID: task.ID, RequireReceipt: requireReceipt,
	})
}

// ValidateWakeupRetainedSource preserves the actual provider-session selection.
// A scheduled run may inherit workflow authority only through that exact run,
// never through another task that happens to name the same provider session.
// Call again inside claim finalization so provenance and authority cannot drift
// between response assembly and persistence.
func (s *TaskService) ValidateWakeupRetainedSource(ctx context.Context, q *db.Queries, task db.AgentTaskQueue, sourceID pgtype.UUID, session string) (bool, error) {
	var provenance struct {
		ID string `json:"wakeup_id"`
	}
	if json.Unmarshal(task.Context, &provenance) != nil || provenance.ID == "" {
		return false, nil
	}
	wakeupID, err := wakeupUUID(provenance.ID)
	if err != nil {
		return false, err
	}
	wakeup, err := q.LocklessWakeup(ctx, wakeupID)
	if err != nil {
		return false, err
	}
	// Internal child inputs and handoff recipients have independent authority.
	if wakeup.ChildIssueID.Valid || len(wakeup.Handoff) != 0 {
		return false, nil
	}
	if err = (&IssueWakeupService{Tasks: s}).checkClaimWithQueries(ctx, q, task); err != nil {
		return false, err
	}
	// Retries and manual reruns keep their explicit source ancestry. Dispatch
	// receipts belong to the original wakeup task and are not rebound to them.
	if task.RetryOfTaskID.Valid || task.RerunOfTaskID.Valid {
		return false, nil
	}
	handoff, err := q.LatestIssueHandoff(ctx, task.IssueID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && handoff.DisabledAt.Valid) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	valid, err := authenticatedWakeupTask(ctx, q, task, true)
	if err != nil {
		return false, err
	}
	if !valid {
		return false, fmt.Errorf("%w: wakeup has no authenticated consumed input for this task", ErrWakeupConflict)
	}
	source, err := q.GetAgentTask(ctx, sourceID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err != nil || task.ForceFreshSession || task.RetainedContextInvalidated || source.Status != "completed" ||
		source.IssueID != task.IssueID || source.AgentID != task.AgentID || source.RuntimeID != task.RuntimeID ||
		!source.SessionID.Valid || source.SessionID.String == "" || source.SessionID.String != session ||
		(!handoffRecipientMatches(ctx, q, handoff, source) && !handoffCommentContinuationMatches(ctx, q, handoff, source)) {
		return false, fmt.Errorf("%w: wakeup retained source is not the current handoff recipient or verified continuation", ErrWakeupConflict)
	}
	return true, nil
}
