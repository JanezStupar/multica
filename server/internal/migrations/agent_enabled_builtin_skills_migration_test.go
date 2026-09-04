package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const agentEnabledBuiltinSkillsMigrationTestSchema = "agent_enabled_builtin_skills_migration_test"

func TestAgentEnabledBuiltinSkillsMigrationReplaysForkSchema(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect to Postgres: %v", err)
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire Postgres connection: %v", err)
	}
	defer conn.Release()

	schemaIdent := pgx.Identifier{agentEnabledBuiltinSkillsMigrationTestSchema}.Sanitize()
	cleanup := func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schemaIdent+" CASCADE")
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schemaIdent); err != nil {
		t.Fatalf("create isolated migration schema: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT set_config('search_path', $1, false)`, agentEnabledBuiltinSkillsMigrationTestSchema); err != nil {
		t.Fatalf("set isolated migration search path: %v", err)
	}

	if _, err := conn.Exec(ctx, `
		CREATE TABLE agent (
			id TEXT PRIMARY KEY,
			enabled_builtin_skill_ids TEXT[]
		);
		INSERT INTO agent (id, enabled_builtin_skill_ids)
		VALUES ('existing-agent', ARRAY['builtin:multica-managing-issues']);
	`); err != nil {
		t.Fatalf("create pre-451 fork fixture: %v", err)
	}

	applyMigrationFile(t, ctx, conn.Conn(), "451_agent_enabled_builtin_skills.up.sql")

	var enabledIDs []string
	if err := conn.QueryRow(ctx, `
		SELECT enabled_builtin_skill_ids
		FROM agent
		WHERE id = 'existing-agent'
	`).Scan(&enabledIDs); err != nil {
		t.Fatalf("read existing built-in policy after replay: %v", err)
	}
	if len(enabledIDs) != 1 || enabledIDs[0] != "builtin:multica-managing-issues" {
		t.Fatalf("existing built-in policy after replay = %v, want preserved exact allow-list", enabledIDs)
	}

	applyMigrationFile(t, ctx, conn.Conn(), "451_agent_enabled_builtin_skills.down.sql")
	applyMigrationFile(t, ctx, conn.Conn(), "451_agent_enabled_builtin_skills.down.sql")
	applyMigrationFile(t, ctx, conn.Conn(), "451_agent_enabled_builtin_skills.up.sql")

	var columnCount int
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = $1
		  AND table_name = 'agent'
		  AND column_name = 'enabled_builtin_skill_ids'
	`, agentEnabledBuiltinSkillsMigrationTestSchema).Scan(&columnCount); err != nil {
		t.Fatalf("inspect rebuilt built-in policy column: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("built-in policy column count after down/down/up = %d, want 1", columnCount)
	}
}
