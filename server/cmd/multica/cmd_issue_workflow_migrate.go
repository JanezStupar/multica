package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/spf13/cobra"
)

func newIssueWorkflowMigrateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "migrate <issue-id>",
		Short: "Reconcile and migrate an issue workflow",
		Long:  "Explicitly migrate a frozen issue or an enrolled unfinished issue to a workspace workflow skill. Current PR candidates and live acceptance on active issues must be resolved first. Record why it is moving and how remaining work/evidence was reconciled. Requires a human workspace owner or administrator.",
		Args:  cobra.ExactArgs(1),
		RunE:  runIssueWorkflowMigrate,
	}
	command.Flags().String("skill-id", "", "Workspace workflow skill UUID to snapshot")
	command.Flags().String("reason", "", "Why this issue is being migrated")
	command.Flags().String("reconciliation", "", "How remaining work and evidence were reconciled")
	command.Flags().String("reopen-to", "", "Active nonterminal status to set when reopening a terminal issue")
	_ = command.MarkFlagRequired("skill-id")
	_ = command.MarkFlagRequired("reason")
	_ = command.MarkFlagRequired("reconciliation")
	return command
}

func runIssueWorkflowMigrate(cmd *cobra.Command, args []string) error {
	skillID, _ := cmd.Flags().GetString("skill-id")
	reason, _ := cmd.Flags().GetString("reason")
	reconciliation, _ := cmd.Flags().GetString("reconciliation")
	reopenTo, _ := cmd.Flags().GetString("reopen-to")
	if _, err := util.ParseUUID(skillID); err != nil || skillID == "" {
		return fmt.Errorf("--skill-id requires a workspace skill UUID")
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("--reason must not be empty")
	}
	if strings.TrimSpace(reconciliation) == "" {
		return fmt.Errorf("--reconciliation must not be empty")
	}
	reopenTo = strings.TrimSpace(reopenTo)
	if cmd.Flags().Changed("reopen-to") && reopenTo == "" {
		return fmt.Errorf("--reopen-to must not be empty when provided")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	issue, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve issue: %w", err)
	}
	body := map[string]string{
		"skill_id":       skillID,
		"reason":         strings.TrimSpace(reason),
		"reconciliation": strings.TrimSpace(reconciliation),
	}
	if reopenTo != "" {
		body["reopen_to"] = reopenTo
	}
	var result map[string]any
	path := "/api/issues/" + url.PathEscape(issue.ID) + "/workflow-migrate"
	if err := client.PostJSON(ctx, path, body, &result); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, result)
}
