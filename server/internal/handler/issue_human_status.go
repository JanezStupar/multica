package handler

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

// Only an authenticated workspace member acting as themselves can make a
// human terminal decision. Task tokens and cloud PATs also carry a member ID,
// so the member ID alone is not authority for this path.
func (h *Handler) humanIssueStatusActor(r *http.Request, workspaceID pgtype.UUID) pgtype.UUID {
	if isMachineCredentialActor(r) {
		return pgtype.UUID{}
	}
	userID := requestUserID(r)
	actorType, actorID := h.resolveActor(r, userID, uuidToString(workspaceID))
	if actorType != "member" || actorID != userID {
		return pgtype.UUID{}
	}
	if _, err := h.getWorkspaceMember(r.Context(), userID, uuidToString(workspaceID)); err != nil {
		return pgtype.UUID{}
	}
	actor, err := util.ParseUUID(userID)
	if err != nil {
		return pgtype.UUID{}
	}
	return actor
}

// Record the human decision before the issue write in the same transaction.
// The database fence checks its transaction ID, exact row revision and status
// transition; a failed or stale update rolls the receipt back with the write.
func recordHumanIssueStatusDecision(ctx context.Context, tx pgx.Tx, workspaceID, issueID, actorID pgtype.UUID, target string) error {
	if tx == nil || !actorID.Valid || target == "" {
		return nil
	}
	var current string
	var revision int64
	var candidateID pgtype.UUID
	var oldDone, newDone bool
	var governed bool
	err := tx.QueryRow(ctx, `SELECT i.status,i.revision,i.workflow_candidate_id,
	  (i.status='done' OR EXISTS(SELECT 1 FROM issue_status s WHERE s.workspace_id=i.workspace_id AND s.key=i.status AND s.category='done')),
	  ($3='done' OR EXISTS(SELECT 1 FROM issue_status s WHERE s.workspace_id=i.workspace_id AND s.key=$3 AND s.category='done')),
	  (i.workflow_policy IS NOT NULL OR i.workflow_frozen)
	  FROM issue i WHERE i.id=$1 AND i.workspace_id=$2 FOR UPDATE`, issueID, workspaceID, target).
		Scan(&current, &revision, &candidateID, &oldDone, &newDone, &governed)
	if err != nil {
		return err
	}
	if !governed || oldDone == newDone {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
	  VALUES($1,$2,'member',$3,'workflow_human_status_decision',
	    jsonb_build_object('from_status',$4::text,'to_status',$5::text,'from_revision',$6::bigint,'to_revision',$6::bigint+1,
	      'candidate_id',COALESCE($7::uuid::text,''),'transaction_id',pg_current_xact_id()::text))`,
		workspaceID, issueID, actorID, current, target, revision, candidateID)
	return err
}

// Batch updates are item-wise. Reject an ineligible frozen target before any
// earlier item can commit, while the frozen row trigger remains the race fence.
func (h *Handler) rejectIneligibleFrozenHumanStatusBatch(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, rawIDs []string, target string) bool {
	ids := make([]pgtype.UUID, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id, err := util.ParseUUID(raw)
		if err == nil {
			ids = append(ids, id)
		}
	}
	var invalid bool
	err := h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM issue i WHERE i.workspace_id=$1
		AND i.id=ANY($2::uuid[]) AND i.workflow_frozen AND NOT (
			i.status=$3 OR
			(NOT (i.status='done' OR EXISTS(SELECT 1 FROM issue_status s WHERE s.workspace_id=i.workspace_id AND s.key=i.status AND s.category='done'))
			 AND ($3='done' OR EXISTS(SELECT 1 FROM issue_status s WHERE s.workspace_id=i.workspace_id AND s.key=$3 AND s.category='done'))) OR
			((i.status='done' OR EXISTS(SELECT 1 FROM issue_status s WHERE s.workspace_id=i.workspace_id AND s.key=i.status AND s.category='done'))
			 AND NOT ($3='done' OR EXISTS(SELECT 1 FROM issue_status s WHERE s.workspace_id=i.workspace_id AND s.key=$3 AND s.category='done'))
			 AND i.workflow_candidate_id IS NULL AND workflow_human_last_done(i.id)
			 AND NOT EXISTS(SELECT 1 FROM issue_workflow_acceptance a WHERE a.issue_id=i.id AND a.workspace_id=i.workspace_id AND a.state='accepted' AND a.revoked_at IS NULL))
		))`, workspaceID, ids, target).Scan(&invalid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check issue workflow state")
		return true
	}
	if invalid {
		writeError(w, http.StatusConflict, "issue is frozen until explicit workflow migration")
		return true
	}
	return false
}
