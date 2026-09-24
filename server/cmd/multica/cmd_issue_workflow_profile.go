package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/spf13/cobra"
)

const maxIssueWorkflowProfileRequestBytes = 32 << 10

type issueWorkflowProfileReselectRequest struct {
	AgentID                  string  `json:"agent_id"`
	ExpectedProfileID        string  `json:"expected_profile_id"`
	RequestKey               string  `json:"request_key"`
	Reason                   string  `json:"reason"`
	Consequences             string  `json:"consequences"`
	Reconciliation           string  `json:"reconciliation"`
	SupplementalInstructions *string `json:"supplemental_instructions,omitempty"`
}

func init() { issueCmd.AddCommand(newIssueWorkflowProfileCommand()) }

func newIssueWorkflowProfileCommand() *cobra.Command {
	profile := &cobra.Command{Use: "workflow-profile", Short: "Manage an issue agent's selected execution profile"}
	reselect := &cobra.Command{
		Use: "reselect <issue-id>", Args: cobra.ExactArgs(1),
		Short: "Select a new behavioral profile for future issue work",
		Long:  "Append a ticket-and-agent scoped execution profile from current agent settings. Requires a human workspace owner or administrator who can invoke the agent and use its runtime. Existing runs and their retries retain their prior profile. Outstanding issue-agent work must be reconciled first. Supplemental instructions do not grant review, acceptance, or delivery authority. Keep request_key unchanged when retrying an ambiguous request.",
		RunE:  runIssueWorkflowProfileReselect,
	}
	reselect.Flags().String("file", "", "Reselection request JSON file, or - to read from stdin")
	reselect.Flags().Bool("allow-external-file", false, "Allow --file outside the current working directory")
	_ = reselect.MarkFlagRequired("file")
	profile.AddCommand(reselect)
	return profile
}

func runIssueWorkflowProfileReselect(cmd *cobra.Command, args []string) error {
	input, err := readIssueWorkflowProfileReselectRequest(cmd)
	if err != nil {
		return err
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
	var result map[string]any
	path := "/api/issues/" + url.PathEscape(issue.ID) + "/workflow-profile/reselect"
	if err := client.PostJSON(ctx, path, input, &result); err != nil {
		return err
	}
	return cli.PrintJSON(os.Stdout, result)
}

func readIssueWorkflowProfileReselectRequest(cmd *cobra.Command) (issueWorkflowProfileReselectRequest, error) {
	var input issueWorkflowProfileReselectRequest
	path, _ := cmd.Flags().GetString("file")
	if strings.TrimSpace(path) == "" {
		return input, fmt.Errorf("--file is required")
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, maxIssueWorkflowProfileRequestBytes+1))
	} else {
		if err = ensureFileFlagWithinWorkdir(cmd, "file", "workflow profile", path); err != nil {
			return input, err
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return input, fmt.Errorf("read profile reselection JSON: %w", err)
	}
	if len(data) == 0 || len(data) > maxIssueWorkflowProfileRequestBytes {
		return input, fmt.Errorf("profile reselection JSON must contain 1–%d bytes", maxIssueWorkflowProfileRequestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, fmt.Errorf("decode profile reselection JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return input, fmt.Errorf("profile reselection JSON must contain exactly one object")
	}
	for _, field := range []struct{ name, value string }{
		{"agent_id", input.AgentID}, {"expected_profile_id", input.ExpectedProfileID}, {"request_key", input.RequestKey},
	} {
		if _, err := util.ParseUUID(field.value); err != nil {
			return input, fmt.Errorf("%s must be a UUID", field.name)
		}
	}
	input.Reason, input.Consequences, input.Reconciliation = strings.TrimSpace(input.Reason), strings.TrimSpace(input.Consequences), strings.TrimSpace(input.Reconciliation)
	if input.Reason == "" || input.Consequences == "" || input.Reconciliation == "" {
		return input, fmt.Errorf("reason, consequences and reconciliation are required")
	}
	if input.SupplementalInstructions != nil {
		*input.SupplementalInstructions = strings.TrimSpace(*input.SupplementalInstructions)
	}
	return input, nil
}
