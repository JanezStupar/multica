package service

import (
	"encoding/json"
	"testing"
)

func TestWorkflowExceptionGrantShapesAreClosed(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		grant map[string]any
		valid bool
	}{
		{"review waiver", "review", map[string]any{"waive": true}, true},
		{"review false", "review", map[string]any{"waive": false}, false},
		{"review extra", "review", map[string]any{"waive": true, "merge": true}, false},
		{"named human", "acceptance", map[string]any{"human_actor_id": "11111111-1111-4111-8111-111111111111"}, true},
		{"named agent", "acceptance", map[string]any{"agent_actor_id": "22222222-2222-4222-8222-222222222222"}, true},
		{"ambiguous acceptance", "acceptance", map[string]any{"human_actor_id": "11111111-1111-4111-8111-111111111111", "agent_actor_id": "22222222-2222-4222-8222-222222222222"}, false},
		{"invalid actor", "acceptance", map[string]any{"human_actor_id": "owner"}, false},
		{"actor must be string", "acceptance", map[string]any{"human_actor_id": json.Number("11111111111141118111111111111111")}, false},
		{"ready", "delivery", map[string]any{"action": "ready"}, true},
		{"ready method", "delivery", map[string]any{"action": "ready", "merge_method": "squash"}, false},
		{"merge", "delivery", map[string]any{"action": "merge", "merge_method": "squash"}, true},
		{"merge missing method", "delivery", map[string]any{"action": "merge"}, false},
		{"other scope", "runtime", map[string]any{"action": "merge", "merge_method": "squash"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizedWorkflowExceptionGrant(tc.scope, tc.grant)
			if (err == nil) != tc.valid {
				t.Fatalf("grant %v valid=%t error=%v", tc.grant, tc.valid, err)
			}
		})
	}
}
