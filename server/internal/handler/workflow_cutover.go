package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workflowDefaultRequest struct {
	SkillID string `json:"skill_id"`
}

type workflowMigrationRequest struct {
	SkillID        string `json:"skill_id"`
	Reason         string `json:"reason"`
	Reconciliation string `json:"reconciliation"`
	ReopenTo       string `json:"reopen_to,omitempty"`
}

func decodeWorkflowRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// selectedWorkflowPolicy captures a complete source in one MVCC read. The
// resulting value, not the mutable source ID, becomes the durable policy.
func (h *Handler) selectedWorkflowPolicy(w http.ResponseWriter, r *http.Request, workspaceID, skillID string) ([]byte, *service.IssueWorkflowPolicy, bool) {
	skillUUID, ok := parseUUIDOrBadRequest(w, skillID, "skill_id")
	if !ok {
		return nil, nil, false
	}
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return nil, nil, false
	}
	sourceRaw, err := h.Queries.GetWorkflowPolicySourceSkill(r.Context(), db.GetWorkflowPolicySourceSkillParams{ID: skillUUID, WorkspaceID: workspaceUUID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "skill not found")
		return nil, nil, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow skill")
		return nil, nil, false
	}
	var source service.AgentSkillData
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow skill")
		return nil, nil, false
	}
	for _, file := range source.Files {
		if !validateFilePath(file.Path) {
			writeError(w, http.StatusBadRequest, "workflow skill has unsafe file path")
			return nil, nil, false
		}
	}
	policy, err := h.TaskService.NewIssueWorkflowPolicy(source)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, nil, false
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode workflow policy")
		return nil, nil, false
	}
	return raw, &policy, true
}

func validateSelectedCompletionPolicy(ctx context.Context, tx pgx.Tx, workspaceID pgtype.UUID,
	policy *service.IssueWorkflowPolicy,
) error {
	authority, err := service.ParseWorkflowAuthorityPolicy(policy.Bundle)
	if err != nil {
		return err
	}
	return service.ValidateWorkflowCompletionConfig(ctx, tx, db.Issue{WorkspaceID: workspaceID}, authority)
}

func workflowLockConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "55P03" || pgErr.Code == "40P01")
}

func writeFrozenWorkflowMutationError(w http.ResponseWriter, err error) bool {
	if workflowLockConflict(err) {
		writeError(w, http.StatusConflict, "issue activity is busy; retry")
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "issue_workflow_frozen" {
		writeError(w, http.StatusConflict, "issue is frozen until explicit workflow migration")
		return true
	}
	return false
}

func writeIssueWorkflowAuthorityError(w http.ResponseWriter, err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "issue_workflow_authority_fence" {
		writeError(w, http.StatusConflict, "workflow completion requires exact candidate acceptance or recorded rejection")
		return true
	}
	return false
}

func (h *Handler) rejectFrozenCommentMutation(w http.ResponseWriter, r *http.Request, issueID, workspaceID pgtype.UUID) bool {
	var frozen bool
	if err := h.DB.QueryRow(r.Context(), `SELECT workflow_frozen FROM issue WHERE id=$1 AND workspace_id=$2`, issueID, workspaceID).Scan(&frozen); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check issue workflow state")
		return true
	}
	if frozen {
		writeError(w, http.StatusConflict, "issue is frozen until explicit workflow migration")
		return true
	}
	return false
}

func (h *Handler) authorizeWorkflowWorkspace(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "this endpoint is only available to human actors")
		return "", "", false
	}
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return "", "", false
	}
	return workspaceID, uuidToString(member.UserID), true
}

func (h *Handler) GetWorkspaceWorkflowDefault(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}
	var raw []byte
	var marker *time.Time
	err := h.DB.QueryRow(r.Context(), `SELECT workflow_default_policy,workflow_cutover_at FROM workspace WHERE id=$1`, workspaceID).Scan(&raw, &marker)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow default")
		return
	}
	policy, err := h.TaskService.DecodeIssueWorkflowPolicy(raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow default")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"policy": policy, "cutover_at": marker})
}

// Frozen issues remain readable to agents for inspection and reconciliation.
// Machine credentials cannot mutate them through issue or comment routes.
func (h *Handler) RejectFrozenIssueMachineMutation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMachineCredentialActor(r) || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
		if !ok {
			return
		}
		if issue.WorkflowFrozen {
			writeError(w, http.StatusConflict, "issue is frozen until explicit workflow migration")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) RejectFrozenCommentMachineMutation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMachineCredentialActor(r) || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		workspaceID := h.resolveWorkspaceID(r)
		var frozen bool
		err := h.DB.QueryRow(r.Context(), `SELECT i.workflow_frozen FROM comment c JOIN issue i ON i.id=c.issue_id
			WHERE c.id=$1 AND i.workspace_id=$2`, chi.URLParam(r, "commentId"), workspaceID).Scan(&frozen)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "comment not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to check issue workflow state")
			return
		}
		if frozen {
			writeError(w, http.StatusConflict, "issue is frozen until explicit workflow migration")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) rejectFrozenIssueBatchMachineMutation(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, rawIDs []string) bool {
	if !isMachineCredentialActor(r) {
		return false
	}
	return h.rejectFrozenIssueBatchMutation(w, r, workspaceID, rawIDs)
}

func (h *Handler) rejectFrozenIssueBatchMutation(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, rawIDs []string) bool {
	ids := make([]pgtype.UUID, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id, err := util.ParseUUID(raw)
		if err == nil {
			ids = append(ids, id)
		}
	}
	var frozen bool
	err := h.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM issue WHERE workspace_id=$1
		AND id=ANY($2::uuid[]) AND workflow_frozen)`, workspaceID, ids).Scan(&frozen)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check issue workflow state")
		return true
	}
	if frozen {
		writeError(w, http.StatusConflict, "issue is frozen until explicit workflow migration")
		return true
	}
	return false
}

// CutoverWorkspaceWorkflowDefault is a one-time, explicit workspace operation.
// It never starts or cancels a process. Existing tasks and evidence remain.
func (h *Handler) CutoverWorkspaceWorkflowDefault(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, ok := h.authorizeWorkflowWorkspace(w, r)
	if !ok {
		return
	}
	var input workflowDefaultRequest
	if !decodeWorkflowRequest(w, r, &input) {
		return
	}
	encoded, policy, ok := h.selectedWorkflowPolicy(w, r, workspaceID, input.SkillID)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin workflow cutover")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "SET LOCAL lock_timeout = '2s'"); err == nil {
		_, err = tx.Exec(r.Context(), "LOCK TABLE agent_task_queue IN SHARE ROW EXCLUSIVE MODE NOWAIT")
	}
	if err != nil {
		writeError(w, http.StatusConflict, "task activity is busy; retry cutover")
		return
	}
	var current []byte
	var marker *time.Time
	err = tx.QueryRow(r.Context(), `SELECT workflow_default_policy, workflow_cutover_at FROM workspace WHERE id=$1 FOR UPDATE NOWAIT`, workspaceID).Scan(&current, &marker)
	if workflowLockConflict(err) {
		writeError(w, http.StatusConflict, "workspace activity is busy; retry cutover")
		return
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if marker != nil {
		existing, decodeErr := h.TaskService.DecodeIssueWorkflowPolicy(current)
		if decodeErr != nil || existing == nil || existing.Version != policy.Version {
			writeError(w, http.StatusConflict, "workspace workflow has already been cut over")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"policy": existing, "cutover_at": marker})
		return
	}
	workspaceUUID, _ := util.ParseUUID(workspaceID)
	if err := validateSelectedCompletionPolicy(r.Context(), tx, workspaceUUID, policy); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	var active bool
	err = tx.QueryRow(r.Context(), `SELECT EXISTS (
		SELECT 1 FROM agent_task_queue t JOIN issue i ON i.id=t.issue_id
		WHERE i.workspace_id=$1 AND t.status IN ('dispatched','running','waiting_local_directory')
	)`, workspaceID).Scan(&active)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect issue tasks")
		return
	}
	if active {
		writeError(w, http.StatusConflict, "claimed or executing issue tasks must finish before cutover")
		return
	}
	// Legacy history has no pinned policy, so even a closed issue must be
	// explicitly migrated before reopening. Already enrolled terminal issues
	// retain their acceptance/rejection lifecycle and need no cutover freeze.
	// All unfinished pre-cutover issues freeze. The insert trigger leaves later
	// issues unfrozen with the selected default snapshot.
	var frozen int64
	err = tx.QueryRow(r.Context(), `WITH changed AS (
		UPDATE issue i SET workflow_frozen=true WHERE workspace_id=$1 AND NOT workflow_frozen
		AND (i.workflow_policy IS NULL OR NOT (
			i.status IN ('done','cancelled') OR EXISTS (
				SELECT 1 FROM issue_status s WHERE s.workspace_id=i.workspace_id
				AND s.key=i.status AND s.category IN ('done','closed')
			)
		)) RETURNING id
	) SELECT count(*) FROM changed`, workspaceID).Scan(&frozen)
	if err != nil {
		writeError(w, http.StatusConflict, "issue activity is busy; retry cutover")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE workspace SET workflow_default_policy=$2, workflow_cutover_at=now() WHERE id=$1`, workspaceID, encoded)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set workflow default")
		return
	}
	details, _ := json.Marshal(map[string]any{"policy": policy, "frozen_issues": frozen})
	_, err = tx.Exec(r.Context(), `INSERT INTO activity_log(workspace_id,actor_type,actor_id,action,details)
		VALUES($1,'member',$2,'workflow_cutover',$3)`, workspaceID, actorID, details)
	if err != nil || tx.Commit(r.Context()) != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow cutover")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"policy": policy, "frozen_issues": frozen})
}

// UpdateWorkspaceWorkflowDefault changes only the snapshot copied to future
// issue inserts. Existing issue rows and their policy identities are untouched.
func (h *Handler) UpdateWorkspaceWorkflowDefault(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, ok := h.authorizeWorkflowWorkspace(w, r)
	if !ok {
		return
	}
	var input workflowDefaultRequest
	if !decodeWorkflowRequest(w, r, &input) {
		return
	}
	encoded, policy, ok := h.selectedWorkflowPolicy(w, r, workspaceID, input.SkillID)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin workflow default update")
		return
	}
	defer tx.Rollback(r.Context())
	var marker *time.Time
	err = tx.QueryRow(r.Context(), `SELECT workflow_cutover_at FROM workspace WHERE id=$1 FOR UPDATE NOWAIT`, workspaceID).Scan(&marker)
	if workflowLockConflict(err) {
		writeError(w, http.StatusConflict, "workspace activity is busy; retry default update")
		return
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if marker == nil {
		writeError(w, http.StatusConflict, "workspace workflow is not cut over")
		return
	}
	workspaceUUID, _ := util.ParseUUID(workspaceID)
	if err := validateSelectedCompletionPolicy(r.Context(), tx, workspaceUUID, policy); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE workspace SET workflow_default_policy=$2 WHERE id=$1`, workspaceID, encoded)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update workflow default")
		return
	}
	details, _ := json.Marshal(map[string]any{"policy": policy})
	_, err = tx.Exec(r.Context(), `INSERT INTO activity_log(workspace_id,actor_type,actor_id,action,details)
		VALUES($1,'member',$2,'workflow_default_changed',$3)`, workspaceID, actorID, details)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record workflow default")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit workflow default")
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

// MigrateIssueWorkflow explicitly reconciles a frozen issue or an enrolled,
// unfinished issue before allowing future execution under a new policy. Old
// tasks, sessions and evidence remain inspectable. A current candidate or live
// acceptance on an unfrozen issue must be reconciled separately; changing the
// policy version would invalidate their exact-version authority records.
func (h *Handler) MigrateIssueWorkflow(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "this endpoint is only available to human actors")
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, uuidToString(issue.WorkspaceID), "issue not found", "owner", "admin")
	if !ok {
		return
	}
	var input workflowMigrationRequest
	if !decodeWorkflowRequest(w, r, &input) {
		return
	}
	input.Reason, input.Reconciliation = strings.TrimSpace(input.Reason), strings.TrimSpace(input.Reconciliation)
	input.ReopenTo = strings.ToLower(strings.TrimSpace(input.ReopenTo))
	if input.Reason == "" || input.Reconciliation == "" {
		writeError(w, http.StatusBadRequest, "reason and reconciliation are required")
		return
	}
	encoded, policy, ok := h.selectedWorkflowPolicy(w, r, uuidToString(issue.WorkspaceID), input.SkillID)
	if !ok {
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin issue migration")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), "SET LOCAL lock_timeout = '2s'"); err == nil {
		_, err = tx.Exec(r.Context(), "LOCK TABLE agent_task_queue IN SHARE ROW EXCLUSIVE MODE NOWAIT")
	}
	if err != nil {
		writeError(w, http.StatusConflict, "task activity is busy; retry migration")
		return
	}
	qtx := h.Queries.WithTx(tx)
	if input.ReopenTo != "" && !issuestatus.IsBuiltIn(input.ReopenTo) {
		if err := qtx.LockIssueStatusCatalogShared(r.Context(), issue.WorkspaceID); err != nil {
			writeError(w, http.StatusConflict, "status catalog is busy; retry migration")
			return
		}
	}
	var marker *time.Time
	err = tx.QueryRow(r.Context(), `SELECT workflow_cutover_at FROM workspace WHERE id=$1 FOR KEY SHARE NOWAIT`, issue.WorkspaceID).Scan(&marker)
	if workflowLockConflict(err) {
		writeError(w, http.StatusConflict, "workspace activity is busy; retry migration")
		return
	}
	if err != nil || marker == nil {
		writeError(w, http.StatusConflict, "workspace workflow is not cut over")
		return
	}
	var prior []byte
	var frozen bool
	var priorStatus string
	var priorRevision int64
	var candidateID pgtype.UUID
	err = tx.QueryRow(r.Context(), `SELECT workflow_policy, workflow_frozen, status, workflow_candidate_id,revision FROM issue WHERE id=$1 AND workspace_id=$2 FOR UPDATE NOWAIT`, issue.ID, issue.WorkspaceID).Scan(&prior, &frozen, &priorStatus, &candidateID, &priorRevision)
	if workflowLockConflict(err) {
		writeError(w, http.StatusConflict, "issue activity is busy; retry migration")
		return
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if !frozen && len(prior) == 0 {
		writeError(w, http.StatusConflict, "unfrozen issue is not enrolled in a workflow policy")
		return
	}
	if err := validateSelectedCompletionPolicy(r.Context(), tx, issue.WorkspaceID, policy); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	priorEntry, err := issuestatus.Resolve(r.Context(), qtx, issue.WorkspaceID, priorStatus)
	if err != nil {
		writeError(w, http.StatusConflict, "current issue status is unavailable")
		return
	}
	priorCategory, _ := issuestatus.ParseCategory(priorEntry.Category)
	terminal := priorCategory == issuestatus.CategoryDone || priorCategory == issuestatus.CategoryClosed
	if !frozen && terminal {
		writeError(w, http.StatusConflict, "completed enrolled issue cannot change workflow policy")
		return
	}
	if !frozen {
		previousPolicy, err := h.TaskService.DecodeIssueWorkflowPolicy(prior)
		if err != nil || previousPolicy == nil {
			writeError(w, http.StatusConflict, "current issue workflow policy is invalid")
			return
		}
		if previousPolicy.Version == policy.Version {
			writeError(w, http.StatusConflict, "issue already uses the selected workflow policy")
			return
		}
		var liveAcceptance bool
		err = tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM issue_workflow_acceptance
			WHERE issue_id=$1 AND workspace_id=$2 AND state IN ('requested','accepted') AND revoked_at IS NULL)`,
			issue.ID, issue.WorkspaceID).Scan(&liveAcceptance)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to inspect workflow acceptance")
			return
		}
		if candidateID.Valid || liveAcceptance {
			writeError(w, http.StatusConflict, "current workflow candidate or acceptance requires explicit reconciliation before policy migration")
			return
		}
	}
	if terminal && input.ReopenTo == "" {
		writeError(w, http.StatusBadRequest, "reopen_to is required for a completed or cancelled issue")
		return
	}
	if !terminal && input.ReopenTo != "" {
		writeError(w, http.StatusBadRequest, "reopen_to is only valid for a completed or cancelled issue")
		return
	}
	nextStatus := priorStatus
	if terminal {
		target, err := issuestatus.Resolve(r.Context(), qtx, issue.WorkspaceID, input.ReopenTo)
		if err != nil {
			writeError(w, http.StatusBadRequest, "reopen_to must name an active issue status")
			return
		}
		category, _ := issuestatus.ParseCategory(target.Category)
		if category != issuestatus.CategoryUnstarted && category != issuestatus.CategoryStarted {
			writeError(w, http.StatusBadRequest, "reopen_to must be a nonterminal issue status")
			return
		}
		nextStatus = target.Key
	}
	var unsettled bool
	err = tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM agent_task_queue WHERE issue_id=$1 AND
		(status IN ('dispatched','running','waiting_local_directory') OR
		 (status IN ('queued','deferred') AND started_at IS NOT NULL)))`, issue.ID).Scan(&unsettled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect issue tasks")
		return
	}
	if unsettled {
		writeError(w, http.StatusConflict, "claimed or executing issue tasks require reconciliation before migration")
		return
	}
	// Retire only unstarted old work. Its rows and evidence remain historical;
	// a deliberate later trigger must create any new-policy execution.
	_, err = tx.Exec(r.Context(), `UPDATE agent_task_queue SET status='cancelled',completed_at=now(),
		error='Superseded by explicit workflow migration'
		WHERE issue_id=$1 AND status IN ('queued','deferred') AND started_at IS NULL`, issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retire unstarted tasks")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE issue_wakeup SET enabled=false,disabled_at=COALESCE(disabled_at,now()),
		last_error='Workflow migrated; rearm explicitly',updated_at=now() WHERE issue_id=$1 AND disabled_at IS NULL`, issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retire issue wakeups")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE issue_wakeup_receipt SET processed_at=now() WHERE processed_at IS NULL
		AND wakeup_id IN (SELECT id FROM issue_wakeup WHERE issue_id=$1)`, issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to settle wakeup receipts")
		return
	}
	// A failed delegated run can leave an outbox comment for later replay.
	// Preserve that comment as evidence while retiring its old-policy dispatch
	// obligation before the issue is unfrozen.
	_, err = tx.Exec(r.Context(), `UPDATE comment SET recovery_settled_at=clock_timestamp()
		WHERE issue_id=$1 AND author_type='system' AND type='progress_update'
		AND source_task_id IS NOT NULL AND recovery_settled_at IS NULL`, issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to settle old recovery inputs")
		return
	}
	if _, err = tx.Exec(r.Context(), "SET LOCAL multica.workflow_migration = 'on'"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to authorize issue migration")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE issue SET workflow_policy=$2,workflow_frozen=false,workflow_migrated_at=clock_timestamp(),
		status=$4,workflow_candidate_id=NULL,revision=revision+1,updated_at=clock_timestamp(),
		last_activity_at=clock_timestamp() WHERE id=$1 AND workspace_id=$3`, issue.ID, encoded, issue.WorkspaceID, nextStatus)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to migrate issue policy")
		return
	}
	var priorHumanDone bool
	if err = tx.QueryRow(r.Context(), `SELECT workflow_human_last_done($1)`, issue.ID).Scan(&priorHumanDone); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check prior human decision")
		return
	}
	if priorHumanDone {
		_, err = tx.Exec(r.Context(), `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
			VALUES($1,$2,'member',$3,'workflow_human_status_decision',
			jsonb_build_object('from_status',$4::text,'to_status',$5::text,
				'from_revision',$6::bigint,'to_revision',$6::bigint+1,
				'candidate_id',COALESCE($7::uuid::text,''),
				'transaction_id',pg_current_xact_id()::text,'source','workflow_migration'))`,
			issue.WorkspaceID, issue.ID, member.UserID, priorStatus, nextStatus, priorRevision, candidateID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record human migration decision")
			return
		}
	}
	details, _ := json.Marshal(map[string]any{"reason": input.Reason, "reconciliation": input.Reconciliation,
		"previous_policy": json.RawMessage(prior), "new_policy": policy,
		"previous_status": priorStatus, "reopened_to": input.ReopenTo})
	_, err = tx.Exec(r.Context(), `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
		VALUES($1,$2,'member',$3,'workflow_migrated',$4)`, issue.WorkspaceID, issue.ID, member.UserID, details)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record issue migration")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit issue migration")
		return
	}
	writeJSON(w, http.StatusOK, policy)
}
