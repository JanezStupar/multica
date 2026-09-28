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

func TestAutonomousAcceptanceModesUseSeparatePolicyAuthority(t *testing.T) {
	actor := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	authority := WorkflowAuthorityPolicy{
		AutonomousEnabled:          true,
		AutonomousAgentIDs:         []string{actor},
		AutonomousReviewedEnabled:  true,
		AutonomousReviewedAgentIDs: []string{"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
	}
	if !workflowAutonomousAcceptanceAllowed(authority, "trivial", actor, nil) {
		t.Fatal("trivial acceptor was denied")
	}
	if workflowAutonomousAcceptanceAllowed(authority, "reviewed", actor, nil) {
		t.Fatal("trivial acceptor unexpectedly gained reviewed authority")
	}
	if workflowAutonomousAcceptanceAllowed(authority, "unknown", actor, nil) {
		t.Fatal("unknown acceptance mode was authorized")
	}
	if !workflowAutonomousAcceptanceAllowed(authority, "reviewed", actor, map[string]any{"agent_actor_id": actor}) {
		t.Fatal("candidate-scoped reviewed exception was denied")
	}
}

func TestWorkflowDeliveryPlanUsesReviewedDeliveryPolicy(t *testing.T) {
	authority := WorkflowAuthorityPolicy{HumanDelivery: "ready", AutonomousDelivery: "merge", AutonomousReviewedDelivery: "ready", MergeMethod: "squash"}
	action, method, _, err := workflowDeliveryPlan(nil, authority, "reviewed", nil, nil)
	if err != nil || action != "ready" || method != "" {
		t.Fatalf("reviewed delivery policy = %q/%q, err=%v", action, method, err)
	}
	action, method, _, err = workflowDeliveryPlan(nil, authority, "trivial", nil, nil)
	if err != nil || action != "merge" || method != "squash" {
		t.Fatalf("trivial delivery policy = %q/%q, err=%v", action, method, err)
	}
}
