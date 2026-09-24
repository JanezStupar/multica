package service

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/util"
	"strings"
	"testing"
	"time"
)

type wakeupTaskLockProbe struct {
	TxStarter
	beforeTaskLock func()
}

func (s wakeupTaskLockProbe) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return wakeupProbeTx{Tx: tx, beforeTaskLock: s.beforeTaskLock}, nil
}

type wakeupProbeTx struct {
	pgx.Tx
	beforeTaskLock func()
}

func (tx wakeupProbeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "-- name: FindPendingWakeupTask") {
		tx.beforeTaskLock()
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}

func TestIssueWakeupTaskLockWaitAllowsPromptCommentRetry(t *testing.T) {
	f, s, issue, agent := wakeFixture(t)
	w := wakeCreate(t, f, s, issue, WakeupInput{AgentID: agent, Kind: "event", Mode: "continuous", EventTypes: []string{"comment.created"}, Instruction: "check"})
	f.Comment(t, util.UUIDToString(issue), "first")
	wakeDispatch(t, s, w)
	f.Comment(t, util.UUIDToString(issue), "pending")
	checked := false
	retryComment := false
	s.Tasks.TxStarter = wakeupTaskLockProbe{TxStarter: f.Pool, beforeTaskLock: func() {
		checked = true
		// The cutover comment guard now takes a NOWAIT issue lock. A writer
		// crossing dispatch must either commit or fail promptly for retry;
		// it must never wait behind the pending receipt or task lock.
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		_, err := f.Pool.Exec(ctx, `INSERT INTO comment(issue_id,workspace_id,author_type,author_id,content,type) VALUES($1,$2,'member',$3,'concurrent','comment')`, issue, f.WorkspaceID, f.UserID)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
				retryComment = true
			} else {
				t.Errorf("comment capture blocked behind task-lock wait: %v", err)
			}
		}
	}}
	wakeDispatch(t, s, w)
	if !checked {
		t.Fatal("task-lock boundary not exercised")
	}
	if retryComment {
		f.Comment(t, util.UUIDToString(issue), "concurrent after issue lock released")
		if got := f.Count(t, "SELECT count(*) FROM issue_wakeup_receipt WHERE wakeup_id=$1 AND processed_at IS NULL", w.ID); got == 0 {
			t.Fatal("retried comment did not leave a durable wakeup receipt")
		}
	}
}
