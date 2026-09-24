package service

import (
	"errors"
	"strings"
	"testing"
)

func TestAutonomousAcceptanceMissingReasonIsInputErrorOnlyAfterAuthorization(t *testing.T) {
	cases := []struct {
		name, reason      string
		allowed, assigned bool
		want              error
	}{
		{name: "authorized but missing classification", allowed: true, assigned: true, want: ErrWorkflowAuthorityInput},
		{name: "agent excluded by policy", assigned: true, want: ErrWorkflowAuthorityForbidden},
		{name: "not current assignee", allowed: true, want: ErrWorkflowAuthorityForbidden},
		{name: "unauthorized with a reason", reason: "trivial", assigned: true, want: ErrWorkflowAuthorityForbidden},
		{name: "authorized with a reason", reason: "trivial", allowed: true, assigned: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAutonomousAcceptanceAuthority(tc.allowed, tc.assigned, tc.reason)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("authority result = %v, want %v", err, tc.want)
			}
			if tc.want == ErrWorkflowAuthorityInput && !strings.Contains(err.Error(), "classification_reason") {
				t.Fatalf("missing reason error lacks field name: %v", err)
			}
		})
	}
}
