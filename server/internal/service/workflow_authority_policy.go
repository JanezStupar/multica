package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
)

// WorkflowAuthorityPolicy contains only machine-enforced authority. The
// workflow instructions remain the source of judgment about work complexity,
// review content, and capability selection. The whole file is included in the
// issue's pinned platform bundle hash.
type WorkflowAuthorityPolicy struct {
	FormatVersion         int
	AcceptedStatusKey     string // format 2: active started status for accepted, unfinished work
	OutcomeAgentID        string // format 2: coordinator for post-delivery outcome work
	HumanAcceptRoles      []string
	ReviewRequired        bool
	HumanDelivery         string // ready or merge
	AutonomousEnabled     bool
	AutonomousAgentIDs    []string
	AutonomousDelivery    string // ready or merge
	MergeMethod           string // merge, squash, rebase; required for merge
	MultiPRMergeOrder     string // explicit; absent denies multi-PR merge
	ExternalMergedHead    string // format 2: exact or accepted; never authorizes a merge POST
	SupervisorAgentScopes map[string]map[string]bool
}

func defaultWorkflowAuthorityPolicy() WorkflowAuthorityPolicy {
	return WorkflowAuthorityPolicy{
		FormatVersion: 1, HumanAcceptRoles: []string{"owner", "admin", "member"},
		ReviewRequired: true, HumanDelivery: "ready", AutonomousDelivery: "ready",
		ExternalMergedHead:    "exact",
		SupervisorAgentScopes: map[string]map[string]bool{},
	}
}

type workflowAuthorityFile struct {
	FormatVersion     *int   `json:"format_version"`
	AcceptedStatusKey string `json:"accepted_status_key"`
	OutcomeAgentID    string `json:"outcome_agent_id"`
	Human             *struct {
		AcceptRoles []string `json:"accept_roles"`
		Delivery    string   `json:"delivery"`
	} `json:"human"`
	Review *struct {
		Required *bool `json:"required"`
	} `json:"review"`
	Autonomous *struct {
		Enabled          bool     `json:"enabled"`
		AcceptorAgentIDs []string `json:"acceptor_agent_ids"`
		Delivery         string   `json:"delivery"`
	} `json:"autonomous_trivial"`
	Delivery *struct {
		MergeMethod        string `json:"merge_method"`
		MultiPRMergeOrder  string `json:"multi_pr_merge_order"`
		ExternalMergedHead string `json:"external_merged_head"`
	} `json:"delivery"`
	Supervisors []struct {
		AgentID string   `json:"agent_id"`
		Scopes  []string `json:"scopes"`
	} `json:"supervisors"`
}

// ParseWorkflowAuthorityPolicy returns safe manual defaults when the optional
// file is absent. An invalid file fails enrollment and every later read closed;
// it is never silently treated as granting authority.
func ParseWorkflowAuthorityPolicy(bundle AgentSkillData) (WorkflowAuthorityPolicy, error) {
	policy := defaultWorkflowAuthorityPolicy()
	content := ""
	found := false
	for _, file := range bundle.Files {
		if file.Path == "runtime/policy.json" {
			content = file.Content
			found = true
			break
		}
	}
	if !found {
		return policy, nil
	}
	if strings.TrimSpace(content) == "" {
		return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json is empty")
	}
	if len(content) > 32*1024 {
		return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json exceeds 32 KiB")
	}
	var input workflowAuthorityFile
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return WorkflowAuthorityPolicy{}, fmt.Errorf("invalid runtime/policy.json: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json must contain one object")
	}
	if input.FormatVersion == nil || (*input.FormatVersion != 1 && *input.FormatVersion != 2) {
		return WorkflowAuthorityPolicy{}, errors.New("unsupported runtime/policy.json format_version")
	}
	policy.FormatVersion = *input.FormatVersion
	if policy.FormatVersion == 2 {
		// The pinned JSON key is also compared by database lifecycle fences.
		// Reject padded values rather than letting Go and SQL resolve different
		// accepted statuses (including tabs and Unicode whitespace).
		if input.AcceptedStatusKey != strings.TrimSpace(input.AcceptedStatusKey) {
			return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json accepted_status_key must be canonical")
		}
		policy.AcceptedStatusKey = input.AcceptedStatusKey
		policy.OutcomeAgentID = strings.TrimSpace(input.OutcomeAgentID)
		if policy.AcceptedStatusKey == "" || policy.AcceptedStatusKey == "done" || policy.AcceptedStatusKey == "cancelled" {
			return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json format 2 requires accepted_status_key")
		}
		if _, err := util.ParseUUID(policy.OutcomeAgentID); err != nil {
			return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json format 2 requires outcome_agent_id UUID")
		}
	} else if input.AcceptedStatusKey != "" || input.OutcomeAgentID != "" {
		return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json format 1 cannot set format 2 fields")
	}
	if input.Human != nil {
		if input.Human.AcceptRoles != nil {
			roles, err := validatedAuthoritySet(input.Human.AcceptRoles, map[string]bool{"owner": true, "admin": true, "member": true})
			if err != nil {
				return WorkflowAuthorityPolicy{}, err
			}
			policy.HumanAcceptRoles = roles
		}
		if input.Human.Delivery != "" {
			policy.HumanDelivery = input.Human.Delivery
		}
	}
	if input.Review != nil && input.Review.Required != nil {
		policy.ReviewRequired = *input.Review.Required
	}
	if input.Autonomous != nil {
		policy.AutonomousEnabled = input.Autonomous.Enabled
		policy.AutonomousAgentIDs = input.Autonomous.AcceptorAgentIDs
		if input.Autonomous.Delivery != "" {
			policy.AutonomousDelivery = input.Autonomous.Delivery
		}
	}
	if input.Delivery != nil {
		policy.MergeMethod = input.Delivery.MergeMethod
		policy.MultiPRMergeOrder = input.Delivery.MultiPRMergeOrder
		if input.Delivery.ExternalMergedHead != "" {
			if policy.FormatVersion != 2 {
				return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json external_merged_head requires format 2")
			}
			policy.ExternalMergedHead = input.Delivery.ExternalMergedHead
		}
	}
	if policy.ExternalMergedHead != "exact" && policy.ExternalMergedHead != "accepted" {
		return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json external_merged_head must be exact or accepted")
	}
	if policy.HumanDelivery != "ready" && policy.HumanDelivery != "merge" ||
		policy.AutonomousDelivery != "ready" && policy.AutonomousDelivery != "merge" {
		return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json delivery must be ready or merge")
	}
	if policy.MergeMethod != "" && policy.MergeMethod != "merge" && policy.MergeMethod != "squash" && policy.MergeMethod != "rebase" ||
		(policy.HumanDelivery == "merge" || policy.AutonomousEnabled && policy.AutonomousDelivery == "merge") && policy.MergeMethod == "" {
		return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json merge requires a supported explicit method")
	}
	if policy.MultiPRMergeOrder != "" && policy.MultiPRMergeOrder != "explicit" {
		return WorkflowAuthorityPolicy{}, errors.New("runtime/policy.json multi-PR merge order must be explicit")
	}
	if !policy.AutonomousEnabled && len(policy.AutonomousAgentIDs) > 0 {
		return WorkflowAuthorityPolicy{}, errors.New("disabled autonomous acceptance cannot list acceptors")
	}
	seenAgents := map[string]bool{}
	for _, id := range policy.AutonomousAgentIDs {
		if _, err := util.ParseUUID(id); err != nil || seenAgents[id] {
			return WorkflowAuthorityPolicy{}, errors.New("invalid or duplicate autonomous acceptor agent ID")
		}
		seenAgents[id] = true
	}
	if policy.AutonomousEnabled && len(policy.AutonomousAgentIDs) == 0 {
		return WorkflowAuthorityPolicy{}, errors.New("autonomous acceptance requires explicit agent IDs")
	}
	for _, supervisor := range input.Supervisors {
		if _, err := util.ParseUUID(supervisor.AgentID); err != nil || policy.SupervisorAgentScopes[supervisor.AgentID] != nil {
			return WorkflowAuthorityPolicy{}, errors.New("invalid or duplicate supervisor agent ID")
		}
		scopes, err := validatedAuthoritySet(supervisor.Scopes, map[string]bool{"review": true, "acceptance": true, "delivery": true})
		if err != nil || len(scopes) == 0 {
			return WorkflowAuthorityPolicy{}, errors.New("supervisor requires valid delegated scopes")
		}
		policy.SupervisorAgentScopes[supervisor.AgentID] = map[string]bool{}
		for _, scope := range scopes {
			policy.SupervisorAgentScopes[supervisor.AgentID][scope] = true
		}
	}
	return policy, nil
}

func validatedAuthoritySet(values []string, allowed map[string]bool) ([]string, error) {
	seen := map[string]bool{}
	for _, value := range values {
		if !allowed[value] || seen[value] {
			return nil, errors.New("runtime/policy.json has invalid or duplicate authority value")
		}
		seen[value] = true
	}
	return values, nil
}
