package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type startReplayDB struct {
	mockDBTX
	startErr error
	calls    int
}

func (m *startReplayDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	m.calls++
	return &mockRow{err: m.startErr}
}

func TestStartTaskLegacyRequiresTransactionalStartGuard(t *testing.T) {
	store := &startReplayDB{startErr: pgx.ErrNoRows}
	svc := &TaskService{Queries: db.New(store)}
	got, err := svc.StartTask(context.Background(), testUUID(1))
	if got != nil || err == nil || store.calls != 0 {
		t.Fatalf("legacy start without transaction: task=%v err=%v queries=%d", got, err, store.calls)
	}
}
