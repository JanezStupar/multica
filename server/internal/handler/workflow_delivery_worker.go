package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/workflowdelivery"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	workflowDeliveryPollInterval  = 2 * time.Second
	workflowDeliveryActionTimeout = 40 * time.Second
	workflowDeliveryHTTPTimeout   = 8 * time.Second
)

// WorkflowDeliveryWorker is deliberately independent of issue wakeups. Closed
// issues do not receive ordinary wakeups, but accepted delivery remains due.
// Database issue-row locks serialize its outbound mutations with rejection.
type WorkflowDeliveryWorker struct {
	h      *Handler
	notify chan struct{}
	done   chan struct{}
	client *http.Client // test transport; nil uses a bounded production client
}

func NewWorkflowDeliveryWorker(h *Handler) *WorkflowDeliveryWorker {
	return &WorkflowDeliveryWorker{h: h, notify: make(chan struct{}, 1), done: make(chan struct{})}
}

func (w *WorkflowDeliveryWorker) Notify() {
	if w == nil {
		return
	}
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *WorkflowDeliveryWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	defer close(w.done)
	if w.h == nil || w.h.DB == nil || w.h.TxStarter == nil {
		return
	}
	ticker := time.NewTicker(workflowDeliveryPollInterval)
	defer ticker.Stop()
	for {
		// Requests made by running agents become acceptance only after their
		// exact requesting task succeeds. This durable scan, like delivery,
		// runs independently of issue wakeups and survives process restarts.
		finalizeCtx, cancel := context.WithTimeout(ctx, workflowDeliveryActionTimeout)
		finalized, finalizeErr := w.h.workflowAuthorityService().FinalizeNextRequestedAcceptance(finalizeCtx)
		cancel()
		if finalizeErr != nil && !errors.Is(finalizeErr, context.Canceled) {
			slog.Error("workflow acceptance: finalize request", "error", finalizeErr)
		}
		outcomeCtx, outcomeCancel := context.WithTimeout(ctx, workflowDeliveryActionTimeout)
		outcomeFinalized, outcomeErr := w.h.workflowAuthorityService().FinalizeNextOutcomeAcknowledgment(outcomeCtx)
		outcomeCancel()
		if outcomeErr != nil && !errors.Is(outcomeErr, context.Canceled) {
			slog.Error("workflow outcome: finalize acknowledgment", "error", outcomeErr)
		}
		dispatchCtx, dispatchCancel := context.WithTimeout(ctx, workflowDeliveryActionTimeout)
		outcomeDispatched, dispatchErr := w.h.workflowAuthorityService().RetryNextWorkflowCompletionDispatch(dispatchCtx)
		dispatchCancel()
		if dispatchErr != nil && !errors.Is(dispatchErr, context.Canceled) {
			slog.Warn("workflow outcome: retry dispatch", "error", dispatchErr)
		}
		worked, err := w.ProcessNext(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("workflow delivery: process intent", "error", err)
		}
		observed, observeErr := w.ProcessNextReadyObservation(ctx)
		if observeErr != nil && !errors.Is(observeErr, context.Canceled) {
			slog.Error("workflow delivery: observe external merge", "error", observeErr)
		}
		// A failed transaction can report that it found work without having
		// advanced it. Wait for the poll interval instead of spinning on it.
		if (finalized || outcomeFinalized || outcomeDispatched || worked || observed) &&
			finalizeErr == nil && outcomeErr == nil && dispatchErr == nil && err == nil && observeErr == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.notify:
		case <-ticker.C:
		}
	}
}

func (w *WorkflowDeliveryWorker) WaitWithTimeout(timeout time.Duration) bool {
	if w == nil {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-w.done:
		return true
	case <-timer.C:
		return false
	}
}

type workflowDeliveryIntent struct {
	id, issueID, workspaceID, acceptanceID, candidateID, bindingID                        pgtype.UUID
	provider, repositoryURL, prURL, owner, repo, expectedSHA, action, mergeMethod, status string
	prNumber                                                                              int64
	attemptCount                                                                          int32
	readinessDone                                                                         bool
	completionVersion                                                                     int16
	acceptedStatus                                                                        string
	held                                                                                  bool
}

// ProcessNext claims one due intent. A transaction holds the issue lock through
// the provider action and its result write. A crash rolls the write back; the
// next attempt reconciles the provider before repeating a mutation.
func (w *WorkflowDeliveryWorker) ProcessNext(ctx context.Context) (bool, error) {
	if w == nil || w.h == nil || w.h.DB == nil || w.h.TxStarter == nil {
		return false, nil
	}
	var id, issueID pgtype.UUID
	err := w.h.DB.QueryRow(ctx, `
		SELECT d.id, d.issue_id FROM issue_workflow_delivery d
		JOIN issue_workflow_acceptance a ON a.id=d.acceptance_id AND a.issue_id=d.issue_id AND a.workspace_id=d.workspace_id
		JOIN issue i ON i.id=d.issue_id AND i.workspace_id=d.workspace_id
		WHERE d.status IN ('pending','retry') AND d.next_attempt_at <= now()
		AND a.state='accepted' AND a.revoked_at IS NULL
		AND i.workflow_candidate_id=d.candidate_id
		AND (a.completion_version=1 AND i.status='done' OR
		     a.completion_version=2 AND i.status=a.accepted_status_key)
		AND NOT (a.completion_version=2 AND a.hold_delivery AND d.action='merge' AND d.readiness_done_at IS NOT NULL)
		AND NOT EXISTS (
			SELECT 1 FROM issue_workflow_delivery prior
			WHERE prior.acceptance_id=d.acceptance_id AND prior.ordinal<d.ordinal
			AND prior.status <> 'delivered'
		)
		ORDER BY d.next_attempt_at,d.id LIMIT 1`).Scan(&id, &issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return w.ProcessNextReadyObservation(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("select workflow delivery intent: %w", err)
	}

	workCtx, cancel := context.WithTimeout(ctx, workflowDeliveryActionTimeout)
	defer cancel()
	tx, err := w.h.TxStarter.Begin(workCtx)
	if err != nil {
		return true, fmt.Errorf("begin workflow delivery: %w", err)
	}
	defer func() {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer rollbackCancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	var currentCandidate pgtype.UUID
	var issueStatus string
	// SKIP LOCKED avoids holding up a rejection request or another server's
	// worker. Rejection uses this same first lock before revoking authority.
	err = tx.QueryRow(workCtx, `SELECT workflow_candidate_id,status FROM issue WHERE id=$1 FOR UPDATE SKIP LOCKED`, issueID).Scan(&currentCandidate, &issueStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("lock workflow delivery issue: %w", err)
	}
	var d workflowDeliveryIntent
	var readinessAt pgtype.Timestamptz
	err = tx.QueryRow(workCtx, `
		SELECT d.id,d.issue_id,d.workspace_id,d.acceptance_id,d.candidate_id,d.provider_binding_id,
		 d.provider,d.repository_url,d.pr_url,d.repo_owner,d.repo_name,d.pr_number,d.expected_head_sha,
		 d.action,COALESCE(d.merge_method,''),d.attempt_count,d.readiness_done_at,
		 a.completion_version,COALESCE(a.accepted_status_key,''),a.hold_delivery
		FROM issue_workflow_delivery d
		JOIN issue_workflow_acceptance a ON a.id=d.acceptance_id AND a.issue_id=d.issue_id AND a.workspace_id=d.workspace_id
		WHERE d.id=$1 AND d.issue_id=$2 AND a.state='accepted' AND a.revoked_at IS NULL
		AND a.candidate_id=d.candidate_id AND d.status IN ('pending','retry')
		AND d.next_attempt_at<=now()
		AND NOT (a.completion_version=2 AND a.hold_delivery AND d.action='merge' AND d.readiness_done_at IS NOT NULL)
		AND NOT EXISTS (SELECT 1 FROM issue_workflow_delivery prior
		 WHERE prior.acceptance_id=d.acceptance_id AND prior.ordinal<d.ordinal AND prior.status<>'delivered')
		FOR UPDATE OF d`, id, issueID).Scan(
		&d.id, &d.issueID, &d.workspaceID, &d.acceptanceID, &d.candidateID, &d.bindingID,
		&d.provider, &d.repositoryURL, &d.prURL, &d.owner, &d.repo, &d.prNumber, &d.expectedSHA,
		&d.action, &d.mergeMethod, &d.attemptCount, &readinessAt,
		&d.completionVersion, &d.acceptedStatus, &d.held)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("lock workflow delivery intent: %w", err)
	}
	d.readinessDone = readinessAt.Valid
	validStatus := d.completionVersion == 1 && issueStatus == "done" ||
		d.completionVersion == 2 && issueStatus == d.acceptedStatus
	if !currentCandidate.Valid || currentCandidate != d.candidateID || !validStatus {
		if _, err := tx.Exec(workCtx, `UPDATE issue_workflow_delivery SET status='cancelled',last_error_class='revoked',updated_at=now() WHERE id=$1 AND status IN ('pending','retry')`, d.id); err != nil {
			return true, err
		}
		return true, tx.Commit(workCtx)
	}
	op := "prepare"
	if d.readinessDone && d.action == "merge" {
		op = "merge"
	}
	provider, token, err := w.providerForIntent(workCtx, tx, d)
	if err != nil {
		status, class := "retry", "provider_unavailable"
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			status, class = "retry", "binding_missing"
		case errors.Is(err, workflowdelivery.ErrInvalid):
			status, class = "blocked", "provider_configuration_invalid"
		case errors.Is(err, workflowdelivery.ErrUnsupported):
			status, class = "blocked", "provider_unsupported"
		}
		if err := w.finishAttempt(workCtx, tx, d, op, status, class, "", ""); err != nil {
			return true, err
		}
		return true, tx.Commit(workCtx)
	}
	ref := workflowdelivery.Ref{Owner: d.owner, Repo: d.repo, Number: d.prNumber, RepositoryURL: d.repositoryURL, PullURL: d.prURL}
	var observedSHA, mergeSHA string
	if op == "merge" {
		result, mergeErr := provider.Merge(workCtx, ref, token, d.expectedSHA, workflowdelivery.MergeMethod(d.mergeMethod))
		err = mergeErr
		observedSHA, mergeSHA = result.HeadSHA, result.MergeCommitSHA
	} else {
		pr, readyErr := provider.PrepareReady(workCtx, ref, token, d.expectedSHA)
		err = readyErr
		observedSHA = pr.HeadSHA
	}
	if err != nil {
		status, class := classifyWorkflowDeliveryError(err)
		if err := w.finishAttempt(workCtx, tx, d, op, status, class, observedSHA, ""); err != nil {
			return true, err
		}
		if status == "stale" && d.completionVersion == 2 {
			issue, err := w.h.Queries.WithTx(tx).GetIssueInWorkspace(workCtx,
				db.GetIssueInWorkspaceParams{ID: d.issueID, WorkspaceID: d.workspaceID})
			if err != nil {
				return true, err
			}
			if err := w.invalidateStaleCandidate(workCtx, tx, d); err != nil {
				return true, err
			}
			if err := tx.Commit(workCtx); err != nil {
				return true, err
			}
			w.h.workflowAuthorityService().PublishWorkflowIssueChange(ctx, issue, service.WorkflowActor{Type: "system"})
			return true, nil
		}
		return true, tx.Commit(workCtx)
	}
	status := "pending"
	if op == "merge" || d.action == "ready" {
		status = "delivered"
	}
	if err := w.finishAttempt(workCtx, tx, d, op, status, "", observedSHA, mergeSHA); err != nil {
		return true, err
	}
	if status == "delivered" && d.completionVersion == 2 {
		q := w.h.Queries.WithTx(tx)
		issue, err := q.GetIssueInWorkspace(workCtx, db.GetIssueInWorkspaceParams{ID: d.issueID, WorkspaceID: d.workspaceID})
		if err != nil {
			return true, err
		}
		outcomeTask, _, dispatchErr, err := service.TryReconcileWorkflowCompletion(workCtx, tx, q, issue, d.acceptanceID)
		if err != nil {
			return true, err
		}
		if err := tx.Commit(workCtx); err != nil {
			return true, err
		}
		if dispatchErr != nil {
			slog.Warn("workflow delivery merged but outcome dispatch deferred", "issue_id", d.issueID, "error", dispatchErr)
		}
		if outcomeTask != nil {
			w.h.TaskService.NotifyTaskEnqueued(ctx, *outcomeTask)
		}
		w.h.workflowAuthorityService().PublishWorkflowIssueChange(ctx, issue, service.WorkflowActor{Type: "system"})
		return true, nil
	}
	return true, tx.Commit(workCtx)
}

// ProcessNextReadyObservation watches format-2 ready-only PRs and held,
// already-prepared merge intents for an external merge of the accepted head.
// It never invokes Merge or releases a held merge.
func (w *WorkflowDeliveryWorker) ProcessNextReadyObservation(ctx context.Context) (bool, error) {
	var id, issueID pgtype.UUID
	err := w.h.DB.QueryRow(ctx, `SELECT d.id,d.issue_id FROM issue_workflow_delivery d
		JOIN issue_workflow_acceptance a ON a.id=d.acceptance_id AND a.issue_id=d.issue_id
		JOIN issue i ON i.id=d.issue_id AND i.workspace_id=d.workspace_id
		WHERE a.completion_version=2 AND a.state='accepted' AND a.revoked_at IS NULL
		AND i.workflow_candidate_id=d.candidate_id AND i.status=a.accepted_status_key
		AND (d.action='ready' AND d.status='delivered' OR
		     d.action='merge' AND d.status IN ('pending','retry') AND
		     d.readiness_done_at IS NOT NULL AND a.hold_delivery)
		AND d.merged_at IS NULL
		AND d.next_attempt_at<=now()
		ORDER BY d.next_attempt_at,d.id LIMIT 1`).Scan(&id, &issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	workCtx, cancel := context.WithTimeout(ctx, workflowDeliveryActionTimeout)
	defer cancel()
	tx, err := w.h.TxStarter.Begin(workCtx)
	if err != nil {
		return true, err
	}
	defer tx.Rollback(workCtx)
	var currentCandidate pgtype.UUID
	var issueStatus string
	err = tx.QueryRow(workCtx, `SELECT workflow_candidate_id,status FROM issue WHERE id=$1 FOR UPDATE SKIP LOCKED`, issueID).
		Scan(&currentCandidate, &issueStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	var d workflowDeliveryIntent
	var previousError pgtype.Text
	err = tx.QueryRow(workCtx, `SELECT d.id,d.issue_id,d.workspace_id,d.acceptance_id,d.candidate_id,
		d.provider_binding_id,d.provider,d.repository_url,d.pr_url,d.repo_owner,d.repo_name,
		d.pr_number,d.expected_head_sha,a.accepted_status_key,d.last_error_class,d.action,d.status,a.hold_delivery
		FROM issue_workflow_delivery d JOIN issue_workflow_acceptance a ON a.id=d.acceptance_id
		WHERE d.id=$1 AND d.issue_id=$2 AND
		(d.action='ready' AND d.status='delivered' OR
		 d.action='merge' AND d.status IN ('pending','retry') AND
		 d.readiness_done_at IS NOT NULL AND a.hold_delivery)
		AND d.merged_at IS NULL AND d.next_attempt_at<=now() AND a.completion_version=2
		AND a.state='accepted' AND a.revoked_at IS NULL FOR UPDATE OF d`, id, issueID).Scan(
		&d.id, &d.issueID, &d.workspaceID, &d.acceptanceID, &d.candidateID,
		&d.bindingID, &d.provider, &d.repositoryURL, &d.prURL, &d.owner, &d.repo,
		&d.prNumber, &d.expectedSHA, &d.acceptedStatus, &previousError,
		&d.action, &d.status, &d.held)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if currentCandidate != d.candidateID || issueStatus != d.acceptedStatus {
		return false, nil
	}
	q := w.h.Queries.WithTx(tx)
	issue, err := q.GetIssueInWorkspace(workCtx, db.GetIssueInWorkspaceParams{ID: d.issueID, WorkspaceID: d.workspaceID})
	if err != nil {
		return true, err
	}
	commit := func(publish bool, outcomeTask *db.AgentTaskQueue) (bool, error) {
		if err := tx.Commit(workCtx); err != nil {
			return true, err
		}
		if outcomeTask != nil {
			w.h.TaskService.NotifyTaskEnqueued(ctx, *outcomeTask)
		}
		if publish {
			w.h.workflowAuthorityService().PublishWorkflowIssueChange(ctx, issue, service.WorkflowActor{Type: "system"})
		}
		return true, nil
	}
	provider, token, err := w.providerForIntent(workCtx, tx, d)
	if err != nil {
		_, updateErr := tx.Exec(workCtx, `UPDATE issue_workflow_delivery SET next_attempt_at=now()+interval '30 seconds',
			last_error_class='provider_unavailable',updated_at=now() WHERE id=$1`, d.id)
		if updateErr != nil {
			return true, updateErr
		}
		return commit(previousError.String != "provider_unavailable", nil)
	}
	ref := workflowdelivery.Ref{Owner: d.owner, Repo: d.repo, Number: d.prNumber,
		RepositoryURL: d.repositoryURL, PullURL: d.prURL}
	pr, err := provider.ReadPR(workCtx, ref, token)
	if err != nil {
		_, updateErr := tx.Exec(workCtx, `UPDATE issue_workflow_delivery SET next_attempt_at=now()+interval '30 seconds',
			last_error_class='provider_unavailable',updated_at=now() WHERE id=$1`, d.id)
		if updateErr != nil {
			return true, updateErr
		}
		return commit(previousError.String != "provider_unavailable", nil)
	}
	if pr.HeadSHA != d.expectedSHA {
		if _, err := tx.Exec(workCtx, `UPDATE issue_workflow_delivery SET status='stale',last_error_class='stale_head',
			updated_at=now() WHERE id=$1`, d.id); err != nil {
			return true, err
		}
		if _, err := tx.Exec(workCtx, `INSERT INTO issue_workflow_delivery_attempt
			(id,workspace_id,issue_id,delivery_id,attempt_number,operation,outcome,error_class,observed_head_sha)
			VALUES(gen_random_uuid(),$1,$2,$3,
			(SELECT attempt_count+1 FROM issue_workflow_delivery WHERE id=$3),'reconcile','stale','stale_head',$4)`,
			d.workspaceID, d.issueID, d.id, pr.HeadSHA); err != nil {
			return true, err
		}
		if err := w.invalidateStaleCandidate(workCtx, tx, d); err != nil {
			return true, err
		}
		return commit(true, nil)
	}
	if !pr.Merged || pr.State != "closed" {
		class := ""
		if pr.Merged {
			class = "provider_state_inconsistent"
		} else if pr.State != "open" {
			class = "closed_unmerged"
		}
		_, err = tx.Exec(workCtx, `UPDATE issue_workflow_delivery SET next_attempt_at=now()+interval '30 seconds',
			last_error_class=NULLIF($2,''),updated_at=now() WHERE id=$1`, d.id, class)
		if err != nil {
			return true, err
		}
		return commit(previousError.String != class, nil)
	}
	if _, err := tx.Exec(workCtx, `UPDATE issue_workflow_delivery SET status='delivered',
		merged_at=now(),merge_commit_sha=$2,last_error_class=NULL,updated_at=now()
		WHERE id=$1 AND merged_at IS NULL`, d.id, pr.MergeCommitSHA); err != nil {
		return true, err
	}
	outcomeTask, _, dispatchErr, err := service.TryReconcileWorkflowCompletion(workCtx, tx, q, issue, d.acceptanceID)
	if err != nil {
		return true, err
	}
	if dispatchErr != nil {
		slog.Warn("workflow ready-only merge observed but outcome dispatch deferred", "issue_id", d.issueID, "error", dispatchErr)
	}
	return commit(true, outcomeTask)
}

func (w *WorkflowDeliveryWorker) invalidateStaleCandidate(ctx context.Context, tx pgx.Tx, d workflowDeliveryIntent) error {
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET state='revoked',revoked_at=now(),
		last_error_class='stale_head' WHERE id=$1 AND state='accepted' AND revoked_at IS NULL`, d.acceptanceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_delivery SET status='cancelled',last_error_class='stale_head',updated_at=now()
		WHERE acceptance_id=$1 AND status IN ('pending','retry','blocked')`, d.acceptanceID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE issue SET status='in_review',
		revision=revision+1,updated_at=now(),last_activity_at=now()
		WHERE id=$1 AND workspace_id=$2 AND workflow_candidate_id=$3`, d.issueID, d.workspaceID, d.candidateID)
	return err
}

func classifyWorkflowDeliveryError(err error) (status, class string) {
	switch {
	case errors.Is(err, workflowdelivery.ErrStaleHead):
		return "stale", "stale_head"
	case errors.Is(err, workflowdelivery.ErrUnsupported):
		return "blocked", "unsupported"
	case errors.Is(err, workflowdelivery.ErrInvalid):
		return "blocked", "invalid"
	case errors.Is(err, workflowdelivery.ErrNotFound):
		return "blocked", "not_found"
	case errors.Is(err, workflowdelivery.ErrClosed):
		return "blocked", "closed_unmerged"
	case errors.Is(err, workflowdelivery.ErrUnauthorized):
		return "retry", "unauthorized"
	case errors.Is(err, workflowdelivery.ErrBlocked):
		return "retry", "provider_blocked"
	case errors.Is(err, workflowdelivery.ErrTransient):
		return "retry", "provider_unavailable"
	default:
		return "retry", "ambiguous"
	}
}

func workflowDeliveryRetryDelay(attempt int32) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<uint(attempt-1)) * 5 * time.Second
}

func (w *WorkflowDeliveryWorker) finishAttempt(ctx context.Context, tx pgx.Tx, d workflowDeliveryIntent, op, status, class, observedSHA, mergeSHA string) error {
	attempt := d.attemptCount + 1
	next := time.Now()
	if status == "retry" {
		next = next.Add(workflowDeliveryRetryDelay(attempt))
	}
	_, err := tx.Exec(ctx, `UPDATE issue_workflow_delivery SET
		status=$2,attempt_count=$3,next_attempt_at=$4,
		readiness_done_at=CASE WHEN $5='prepare' AND $2 IN ('pending','delivered') THEN now() ELSE readiness_done_at END,
		merged_at=CASE WHEN $5='merge' AND $2='delivered' THEN now() ELSE merged_at END,
		merge_commit_sha=CASE WHEN $5='merge' AND $2='delivered' THEN NULLIF($6,'') ELSE merge_commit_sha END,
		last_error_class=NULLIF($7,''),updated_at=now() WHERE id=$1 AND status IN ('pending','retry')`,
		d.id, status, attempt, next, op, mergeSHA, class)
	if err != nil {
		return fmt.Errorf("update workflow delivery intent: %w", err)
	}
	outcome := status
	if class == "ambiguous" {
		outcome = "ambiguous"
	} else if status == "pending" {
		outcome = "delivered"
	}
	_, err = tx.Exec(ctx, `INSERT INTO issue_workflow_delivery_attempt
		(id,workspace_id,issue_id,delivery_id,attempt_number,operation,outcome,error_class,observed_head_sha)
		VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''))`, d.workspaceID, d.issueID, d.id, attempt, op, outcome, class, observedSHA)
	if err != nil {
		return fmt.Errorf("record workflow delivery attempt: %w", err)
	}
	return nil
}

func (w *WorkflowDeliveryWorker) httpClient() *http.Client {
	if w.client != nil {
		return w.client
	}
	return &http.Client{Timeout: workflowDeliveryHTTPTimeout}
}

func (w *WorkflowDeliveryWorker) providerForIntent(ctx context.Context, query dbExecutor, d workflowDeliveryIntent) (workflowdelivery.Provider, string, error) {
	var config workflowdelivery.Config
	var token string
	switch d.provider {
	case "github":
		var installationID int64
		if err := query.QueryRow(ctx, `SELECT installation_id FROM github_installation WHERE id=$1 AND workspace_id=$2`, d.bindingID, d.workspaceID).Scan(&installationID); err != nil {
			return nil, "", err
		}
		apiBase := githubAPIBase
		repoBase := "https://github.com"
		if apiBase != "https://api.github.com" {
			repoBase = apiBase
		}
		config = workflowdelivery.Config{Kind: workflowdelivery.GitHub, APIBaseURL: apiBase, RepositoryBaseURL: repoBase}
		var err error
		token, err = w.mintGitHubDeliveryToken(ctx, installationID)
		if err != nil {
			return nil, "", err
		}
	case "forgejo", "gitea":
		if w.h.VCSSecretBox == nil {
			return nil, "", errors.New("VCS secret box unavailable")
		}
		var provider, base, encrypted string
		if err := query.QueryRow(ctx, `SELECT provider,instance_url,access_token_encrypted FROM vcs_connection WHERE id=$1 AND workspace_id=$2`, d.bindingID, d.workspaceID).Scan(&provider, &base, &encrypted); err != nil {
			return nil, "", err
		}
		if provider != d.provider {
			return nil, "", workflowdelivery.ErrInvalid
		}
		var err error
		token, err = w.h.openVCSSecret(encrypted)
		if err != nil {
			return nil, "", err
		}
		config = workflowdelivery.Config{Kind: workflowdelivery.Kind(provider), APIBaseURL: strings.TrimRight(base, "/") + "/api/v1", RepositoryBaseURL: base}
	default:
		return nil, "", workflowdelivery.ErrUnsupported
	}
	provider, err := workflowdelivery.New(config, w.httpClient())
	if err != nil {
		return nil, "", err
	}
	return provider, token, nil
}

// GitHub App tokens come from the workspace's installation binding. Neither
// the agent's PR URL nor a handoff payload can name a credential endpoint.
func (w *WorkflowDeliveryWorker) mintGitHubDeliveryToken(ctx context.Context, installationID int64) (string, error) {
	jwt, err := signGitHubAppJWT(time.Now())
	if err != nil || jwt == "" {
		return "", errors.New("GitHub App credentials unavailable")
	}
	base, err := url.Parse(githubAPIBase)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil {
		return "", errors.New("GitHub API base invalid")
	}
	endpoint := strings.TrimRight(base.String(), "/") + fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	body := strings.NewReader(`{"permissions":{"pull_requests":"write"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return "", errors.New("GitHub token request failed")
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	client := *w.httpClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("GitHub token endpoint unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", errors.New("GitHub token request denied")
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload); err != nil || payload.Token == "" {
		return "", errors.New("GitHub token response invalid")
	}
	return payload.Token, nil
}
