package service

import (
	"strings"
	"testing"
)

func TestWorkflowAuthorityPolicyDefaultsAndExplicitAuthority(t *testing.T) {
	defaults, err := ParseWorkflowAuthorityPolicy(AgentSkillData{})
	if err != nil || !defaults.ReviewRequired || defaults.AutonomousEnabled || defaults.HumanDelivery != "ready" || defaults.MergeMethod != "" {
		t.Fatalf("safe absent-policy defaults: %+v, %v", defaults, err)
	}
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	config := `{"format_version":1,"human":{"accept_roles":["owner","admin"],"delivery":"merge"},` +
		`"review":{"required":true},"autonomous_trivial":{"enabled":true,"acceptor_agent_ids":["` + id + `"],"delivery":"merge"},` +
		`"delivery":{"merge_method":"squash","multi_pr_merge_order":"explicit"},` +
		`"supervisors":[{"agent_id":"` + id + `","scopes":["review"]}]}`
	policy, err := ParseWorkflowAuthorityPolicy(AgentSkillData{Files: []AgentSkillFileData{{Path: "runtime/policy.json", Content: config}}})
	if err != nil || !policy.AutonomousEnabled || policy.MergeMethod != "squash" || policy.MultiPRMergeOrder != "explicit" ||
		len(policy.HumanAcceptRoles) != 2 || !policy.SupervisorAgentScopes[id]["review"] {
		t.Fatalf("explicit pinned authority: %+v, %v", policy, err)
	}
}

func TestWorkflowAuthorityPolicyRejectsMalformedOrImplicitGrants(t *testing.T) {
	for _, content := range []string{
		" ", `{"format_version":2}`, `{"format_version":1,"mystery":true}`,
		`{"format_version":1,"human":{"delivery":"merge"}}`,
		`{"format_version":1,"autonomous_trivial":{"enabled":true}}`,
		`{"format_version":1,"delivery":{"merge_method":"fast-forward"}}`,
		`{"format_version":1,"supervisors":[{"agent_id":"bad","scopes":["delivery"]}]}`,
		`{"format_version":1}{"format_version":1}`,
	} {
		t.Run(strings.ReplaceAll(content, "/", "_"), func(t *testing.T) {
			_, err := ParseWorkflowAuthorityPolicy(AgentSkillData{Files: []AgentSkillFileData{{Path: "runtime/policy.json", Content: content}}})
			if err == nil {
				t.Fatalf("accepted malformed policy %q", content)
			}
		})
	}
}
