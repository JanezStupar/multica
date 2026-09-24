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

func init() { workspaceCmd.AddCommand(newWorkspaceWorkflowCommand()) }

func newWorkspaceWorkflowCommand() *cobra.Command {
	workflow := &cobra.Command{Use: "workflow", Short: "Inspect and change workspace workflow policy"}
	for _, action := range []string{"get", "cutover", "set-default"} {
		action := action
		command := &cobra.Command{
			Use:   action + " [workspace-id|slug|prefix]",
			Short: action + " the workspace workflow policy",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runWorkspaceWorkflow(cmd, args, action)
			},
		}
		if action == "cutover" {
			command.Long = "Perform the one-time workspace cutover. Existing issues are frozen for explicit migration; no issue task is started or cancelled. Requires a human workspace owner or administrator."
		}
		if action == "set-default" {
			command.Long = "Change only the workflow snapshot used for future issues. Existing issue policies are unchanged. Requires a human workspace owner or administrator."
		}
		if action != "get" {
			command.Flags().String("skill-id", "", "Workspace workflow skill UUID to snapshot")
			_ = command.MarkFlagRequired("skill-id")
		}
		workflow.AddCommand(command)
	}
	return workflow
}

func runWorkspaceWorkflow(cmd *cobra.Command, args []string, action string) error {
	workspaceID, err := resolveWorkspaceArg(cmd, args)
	if err != nil {
		return err
	}
	if workspaceID == "" {
		return fmt.Errorf("workspace ID is required: pass an id/slug/prefix or set MULTICA_WORKSPACE_ID")
	}
	var skillID string
	if action != "get" {
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
	path := "/api/workspaces/" + url.PathEscape(workspaceID)
	var result map[string]any
	switch action {
	case "get":
		err = client.GetJSON(ctx, path+"/workflow-default", &result)
	case "cutover":
		err = client.PostJSON(ctx, path+"/workflow-cutover", map[string]string{"skill_id": skillID}, &result)
	case "set-default":
		err = client.PutJSON(ctx, path+"/workflow-default", map[string]string{"skill_id": skillID}, &result)
	default:
		return fmt.Errorf("unknown workspace workflow action %q", action)
	}
	if err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, result)
}
