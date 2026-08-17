package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

type agentBuiltinSkillSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type agentBuiltinPolicy struct {
	Mode                   string                     `json:"mode"`
	EnabledBuiltinSkillIDs []string                   `json:"enabled_builtin_skill_ids"`
	BuiltinSkills          []agentBuiltinSkillSummary `json:"builtin_skills"`
}

var agentBuiltinsCmd = &cobra.Command{
	Use:   "builtins",
	Short: "Manage the Multica built-in skills available to an agent",
	Long: "Built-in skills are inherited by default. The first disable creates an " +
		"exact per-agent allow-list, so future built-ins stay off until enabled. " +
		"Use reset to restore inheritance of every current and future built-in.",
}

var agentBuiltinsListCmd = &cobra.Command{
	Use:   "list <agent-id>",
	Short: "List built-in skills and the agent's inheritance mode",
	Args:  exactArgs(1),
	RunE:  runAgentBuiltinsList,
}

var agentBuiltinsEnableCmd = &cobra.Command{
	Use:   "enable <agent-id> <skill-id>",
	Short: "Enable one Multica built-in skill for an agent",
	Args:  exactArgs(2),
	RunE:  runAgentBuiltinsEnable,
}

var agentBuiltinsDisableCmd = &cobra.Command{
	Use:   "disable <agent-id> <skill-id>",
	Short: "Disable one Multica built-in skill for an agent",
	Args:  exactArgs(2),
	RunE:  runAgentBuiltinsDisable,
}

var agentBuiltinsResetCmd = &cobra.Command{
	Use:   "reset <agent-id>",
	Short: "Restore inheritance of every current and future built-in skill",
	Args:  exactArgs(1),
	RunE:  runAgentBuiltinsReset,
}

func init() {
	agentCmd.AddCommand(agentBuiltinsCmd)
	agentBuiltinsCmd.AddCommand(
		agentBuiltinsListCmd,
		agentBuiltinsEnableCmd,
		agentBuiltinsDisableCmd,
		agentBuiltinsResetCmd,
	)
	for _, cmd := range []*cobra.Command{
		agentBuiltinsListCmd,
		agentBuiltinsEnableCmd,
		agentBuiltinsDisableCmd,
		agentBuiltinsResetCmd,
	} {
		cmd.Flags().String("output", "table", "Output format: table or json")
	}
}

func agentBuiltinsPath(agentID string) string {
	return "/api/agents/" + url.PathEscape(strings.TrimSpace(agentID)) + "/builtin-skills"
}

func fetchAgentBuiltinPolicy(ctx context.Context, client *cli.APIClient, agentID string) (agentBuiltinPolicy, error) {
	var response struct {
		EnabledBuiltinSkillIDs []string                   `json:"enabled_builtin_skill_ids"`
		BuiltinSkills          []agentBuiltinSkillSummary `json:"builtin_skills"`
	}
	path := "/api/agents/" + url.PathEscape(strings.TrimSpace(agentID))
	if err := client.GetJSON(ctx, path, &response); err != nil {
		return agentBuiltinPolicy{}, err
	}
	if response.BuiltinSkills == nil {
		return agentBuiltinPolicy{}, fmt.Errorf("built-in skill controls are not available from this server")
	}
	mode := "exact"
	if response.EnabledBuiltinSkillIDs == nil {
		mode = "inherit_all"
	}
	return agentBuiltinPolicy{
		Mode:                   mode,
		EnabledBuiltinSkillIDs: response.EnabledBuiltinSkillIDs,
		BuiltinSkills:          response.BuiltinSkills,
	}, nil
}

func runAgentBuiltinsList(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	policy, err := fetchAgentBuiltinPolicy(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("list agent built-ins: %w", err)
	}
	return printAgentBuiltinPolicy(cmd, policy)
}

func runAgentBuiltinsEnable(cmd *cobra.Command, args []string) error {
	return setAgentBuiltinEnabled(cmd, args, true)
}

func runAgentBuiltinsDisable(cmd *cobra.Command, args []string) error {
	return setAgentBuiltinEnabled(cmd, args, false)
}

func setAgentBuiltinEnabled(cmd *cobra.Command, args []string, enabled bool) error {
	agentID, skillID := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	body := map[string]any{"skill_id": skillID, "enabled": enabled}
	if err := client.PutJSON(ctx, agentBuiltinsPath(agentID)+"/enabled", body, nil); err != nil {
		return fmt.Errorf("update agent built-in skill: %w", err)
	}
	policy, err := fetchAgentBuiltinPolicy(ctx, client, agentID)
	if err != nil {
		return fmt.Errorf("built-in skill updated, but read-back failed: %w", err)
	}
	return printAgentBuiltinPolicy(cmd, policy)
}

func runAgentBuiltinsReset(cmd *cobra.Command, args []string) error {
	agentID := strings.TrimSpace(args[0])
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	if err := client.DeleteJSON(ctx, agentBuiltinsPath(agentID)); err != nil {
		return fmt.Errorf("reset agent built-ins: %w", err)
	}
	policy, err := fetchAgentBuiltinPolicy(ctx, client, agentID)
	if err != nil {
		return fmt.Errorf("built-in skills reset, but read-back failed: %w", err)
	}
	return printAgentBuiltinPolicy(cmd, policy)
}

func printAgentBuiltinPolicy(cmd *cobra.Command, policy agentBuiltinPolicy) error {
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, policy)
	}

	fmt.Fprintf(os.Stdout, "Policy: %s\n", policy.Mode)
	headers := []string{"ID", "NAME", "STATUS", "DESCRIPTION"}
	rows := make([][]string, 0, len(policy.BuiltinSkills))
	for _, skill := range policy.BuiltinSkills {
		status := "disabled"
		if skill.Enabled {
			status = "enabled"
		}
		rows = append(rows, []string{skill.ID, skill.Name, status, skill.Description})
	}
	cli.PrintTable(os.Stdout, headers, rows)
	return nil
}
