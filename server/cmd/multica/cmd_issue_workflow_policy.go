package main

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/spf13/cobra"
)

func init() { issueCmd.AddCommand(newIssueWorkflowPolicyCommand()) }

func newIssueWorkflowPolicyCommand() *cobra.Command {
	policy := &cobra.Command{
		Use:   "workflow-policy",
		Short: "Inspect or explicitly pin an issue's workflow bundle",
	}
	for _, action := range []string{"get", "pin"} {
		command := &cobra.Command{
			Use:   action + " <issue-id>",
			Args:  cobra.ExactArgs(1),
			Short: action + " the issue workflow policy",
			RunE: func(cmd *cobra.Command, args []string) error {
				return runIssueWorkflowPolicy(cmd, args[0], action)
			},
		}
		if action == "pin" {
			command.Long = "Pin a complete workspace workflow skill before the issue has any task history. Requires a human workspace owner or administrator. This does not activate workspace defaults or migrate an existing policy."
			command.Flags().String("skill-id", "", "Workspace workflow skill UUID to snapshot")
			_ = command.MarkFlagRequired("skill-id")
		}
		policy.AddCommand(command)
	}
	policy.AddCommand(newIssueWorkflowMigrateCommand())
	return policy
}

func runIssueWorkflowPolicy(cmd *cobra.Command, issueID, action string) error {
	var skillID string
	if action == "pin" {
		skillID, _ = cmd.Flags().GetString("skill-id")
		if _, err := util.ParseUUID(skillID); err != nil || skillID == "" {
			return fmt.Errorf("--skill-id requires a workspace skill UUID")
		}
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	ref, err := resolveIssueRef(ctx, client, issueID)
	if err != nil {
		return err
	}
	path := "/api/issues/" + url.PathEscape(ref.ID) + "/workflow-policy"
	var result map[string]any
	if action == "pin" {
		err = client.PostJSON(ctx, path, map[string]string{"skill_id": skillID}, &result)
	} else {
		err = client.GetJSON(ctx, path, &result)
	}
	if err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, result)
}
