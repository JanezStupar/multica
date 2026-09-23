package migrations

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAgentBuiltinPolicyMigrationPreservesOlderForkColumn(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires DATABASE_URL")
	}
	for _, hadColumn := range []bool{false, true} {
		t.Run(fmt.Sprintf("had_column_%t", hadColumn), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			pool, err := pgxpool.New(ctx, dbURL)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			conn, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Release()
			schema := fmt.Sprintf("agent_builtin_policy_%d", time.Now().UnixNano())
			ident := pgx.Identifier{schema}.Sanitize()
			if _, err := conn.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := conn.Exec(context.Background(), "DROP SCHEMA "+ident+" CASCADE"); err != nil {
					t.Errorf("drop scratch schema: %v", err)
				}
			}()
			if _, err := conn.Exec(ctx, `SELECT set_config('search_path', $1, false)`, schema); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(ctx, `CREATE TABLE agent (id TEXT PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(ctx, `CREATE TABLE agent_task_queue (id TEXT PRIMARY KEY)`); err != nil {
				t.Fatal(err)
			}
			if hadColumn {
				if _, err := conn.Exec(ctx, `ALTER TABLE agent ADD COLUMN enabled_builtin_skill_ids TEXT[];
					INSERT INTO agent VALUES ('existing', ARRAY['builtin:multica-mentioning'])`); err != nil {
					t.Fatal(err)
				}
			}
			applyMigrationFile(t, ctx, conn.Conn(), "536_agent_builtin_skill_policy.up.sql")
			var enabled []string
			var replacements []byte
			if hadColumn {
				if err := conn.QueryRow(ctx, `SELECT enabled_builtin_skill_ids, builtin_skill_replacements FROM agent WHERE id = 'existing'`).Scan(&enabled, &replacements); err != nil {
					t.Fatal(err)
				}
				if len(enabled) != 1 || enabled[0] != "builtin:multica-mentioning" || string(replacements) != "{}" {
					t.Fatalf("replayed policy = %v %s", enabled, replacements)
				}
			}
			applyMigrationFile(t, ctx, conn.Conn(), "536_agent_builtin_skill_policy.down.sql")
			applyMigrationFile(t, ctx, conn.Conn(), "536_agent_builtin_skill_policy.up.sql")
			if hadColumn {
				if err := conn.QueryRow(ctx, `SELECT enabled_builtin_skill_ids FROM agent WHERE id = 'existing'`).Scan(&enabled); err != nil {
					t.Fatal(err)
				}
				if len(enabled) != 1 || enabled[0] != "builtin:multica-mentioning" {
					t.Fatalf("rollback lost old policy: %v", enabled)
				}
			}
		})
	}
}
