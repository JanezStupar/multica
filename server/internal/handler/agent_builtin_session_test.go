package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
)

func TestPlatformFingerprintInvalidatesChangedIssueSession(t *testing.T) {
	builtin := platformSkillFingerprint([]service.AgentSkillRefData{{ID: "builtin:multica-platform", Source: "builtin", Hash: "old"}})
	replacement := platformSkillFingerprint([]service.AgentSkillRefData{{ID: "skill-1", Source: "workspace", ReplacesBuiltin: "builtin:multica-platform", Hash: "new"}})
	if builtin == replacement || !platformSessionChanged(replacement, pgtype.Text{String: builtin, Valid: true}) {
		t.Fatal("changing the effective platform bundle must retire the prior session")
	}
	if platformSessionChanged(replacement, pgtype.Text{String: replacement, Valid: true}) {
		t.Fatal("unchanged replacement must keep the session")
	}
	if !platformSessionChanged(replacement, pgtype.Text{}) {
		t.Fatal("a pre-migration session must not be resumed under a new replacement")
	}
	if platformSessionChanged(builtin, pgtype.Text{}) {
		t.Fatal("a pre-migration session using the embedded bundle may continue")
	}
}
