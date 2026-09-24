package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/spf13/cobra"
)

const maxIssueHandoffBodyBytes = 64 << 10

type issueHandoffCandidate struct {
	RepositoryURL string `json:"repository_url"`
	PRURL         string `json:"pr_url"`
	Branch        string `json:"branch"`
	CommitSHA     string `json:"commit_sha"`
	Draft         bool   `json:"draft"`
}

type issueHandoffRequest struct {
	RequestKey     string                  `json:"request_key"`
	OutgoingTaskID string                  `json:"outgoing_task_id"`
	AgentID        string                  `json:"agent_id,omitempty"`
	AssigneeType   string                  `json:"assignee_type,omitempty"`
	AssigneeID     string                  `json:"assignee_id,omitempty"`
	Status         string                  `json:"status"`
	ContextMode    string                  `json:"context_mode"`
	ResumeTaskID   string                  `json:"resume_task_id,omitempty"`
	Candidates     []issueHandoffCandidate `json:"candidates"`
	EvidenceURLs   []string                `json:"evidence_urls"`
	Instruction    string                  `json:"instruction"`
}

func init() { issueCmd.AddCommand(newIssueHandoffCommand()) }

func newIssueHandoffCommand() *cobra.Command {
	handoff := &cobra.Command{Use: "handoff", Short: "Create and inspect recoverable issue handoffs"}
	create := &cobra.Command{
		Use:   "create <issue-id>",
		Short: "Create an idempotent handoff from an outgoing task",
		Long:  "Create one durable issue handoff from a JSON request file. The issue must have a pinned workflow policy. Keep the same request_key when retrying an ambiguous request; the server uses it to return the original handoff instead of launching another recipient. Empty candidates is valid for work without code changes.",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runIssueHandoff(cmd, args, "create") },
	}
	create.Flags().String("file", "", "Handoff request JSON file, or - to read JSON from stdin")
	create.Flags().Bool("allow-external-file", false, "Allow --file to read a path outside the current working directory")
	create.Flags().String("output", "json", "Output format: json or table")
	_ = create.MarkFlagRequired("file")

	list := &cobra.Command{
		Use:   "list <issue-id>",
		Short: "List an issue's durable handoffs",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runIssueHandoff(cmd, args, "list") },
	}
	list.Flags().String("output", "json", "Output format: json or table")

	cancel := &cobra.Command{
		Use:   "cancel <issue-id>",
		Short: "Cancel pending handoff execution",
		Long:  "Disable the durable handoff wakeup and cancel its unstarted recipient task when possible. A recipient run that has started keeps running; use `multica issue cancel-task <run-id>` to interrupt it.",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runIssueHandoff(cmd, args, "cancel") },
	}
	cancel.Flags().String("handoff-id", "", "Handoff UUID to disable")
	cancel.Flags().String("output", "json", "Output format: json or table")
	_ = cancel.MarkFlagRequired("handoff-id")

	handoff.AddCommand(create, list, cancel)
	return handoff
}

func runIssueHandoff(cmd *cobra.Command, args []string, action string) error {
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
	basePath := "/api/issues/" + url.PathEscape(issue.ID)
	var result any
	switch action {
	case "create":
		input, err := readIssueHandoffRequest(cmd)
		if err != nil {
			return err
		}
		var row map[string]any
		if err := client.PostJSON(ctx, basePath+"/handoffs", input, &row); err != nil {
			return fmt.Errorf("create issue handoff: %w", err)
		}
		result = row
	case "list":
		var rows []map[string]any
		if err := client.GetJSON(ctx, basePath+"/handoffs", &rows); err != nil {
			return fmt.Errorf("list issue handoffs: %w", err)
		}
		result = rows
	case "cancel":
		handoffID, _ := cmd.Flags().GetString("handoff-id")
		handoffUUID, err := util.ParseUUID(strings.TrimSpace(handoffID))
		if err != nil {
			return fmt.Errorf("--handoff-id must be a UUID")
		}
		var row map[string]any
		path := basePath + "/wakeups/" + url.PathEscape(util.UUIDToString(handoffUUID)) + "/disable"
		if err := client.PostJSON(ctx, path, map[string]any{}, &row); err != nil {
			return fmt.Errorf("cancel issue handoff: %w", err)
		}
		result = row
	default:
		return fmt.Errorf("unsupported issue handoff action %q", action)
	}
	return printIssueHandoffResult(cmd, result)
}

func readIssueHandoffRequest(cmd *cobra.Command) (issueHandoffRequest, error) {
	var input issueHandoffRequest
	filePath, _ := cmd.Flags().GetString("file")
	if strings.TrimSpace(filePath) == "" {
		return input, fmt.Errorf("--file is required")
	}
	var data []byte
	if filePath == "-" {
		read, err := io.ReadAll(io.LimitReader(os.Stdin, maxIssueHandoffBodyBytes+1))
		if err != nil {
			return input, fmt.Errorf("read handoff JSON from stdin: %w", err)
		}
		data = read
	} else {
		if err := ensureFileFlagWithinWorkdir(cmd, "file", "handoff", filePath); err != nil {
			return input, err
		}
		read, err := os.ReadFile(filePath)
		if err != nil {
			return input, fmt.Errorf("read --file: %w", err)
		}
		data = read
	}
	if len(data) == 0 {
		return input, fmt.Errorf("handoff JSON is empty")
	}
	if len(data) > maxIssueHandoffBodyBytes {
		return input, fmt.Errorf("handoff JSON exceeds %d bytes", maxIssueHandoffBodyBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, fmt.Errorf("decode handoff JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return input, fmt.Errorf("handoff JSON must contain exactly one object")
	}
	if err := validateIssueHandoffRequest(&input); err != nil {
		return input, err
	}
	// The API contract represents no-code work with explicit empty arrays.
	if input.Candidates == nil {
		input.Candidates = []issueHandoffCandidate{}
	}
	if input.EvidenceURLs == nil {
		input.EvidenceURLs = []string{}
	}
	return input, nil
}

func validateIssueHandoffRequest(input *issueHandoffRequest) error {
	for _, field := range []struct{ name, value string }{
		{"request_key", input.RequestKey},
		{"outgoing_task_id", input.OutgoingTaskID},
	} {
		if _, err := util.ParseUUID(field.value); err != nil {
			return fmt.Errorf("%s must be a UUID", field.name)
		}
	}
	if input.AssigneeType == "" && input.AssigneeID == "" && input.AgentID != "" {
		if _, err := util.ParseUUID(input.AgentID); err != nil {
			return fmt.Errorf("agent_id must be a UUID")
		}
	} else {
		if input.AssigneeType != "agent" && input.AssigneeType != "member" {
			return fmt.Errorf("assignee_type must be agent or member")
		}
		if _, err := util.ParseUUID(input.AssigneeID); err != nil {
			return fmt.Errorf("assignee_id must be a UUID")
		}
		if input.AssigneeType == "agent" && input.AgentID != "" && input.AgentID != input.AssigneeID {
			return fmt.Errorf("agent_id and assignee_id must identify the same agent")
		}
		if input.AssigneeType == "member" {
			if input.AgentID != "" {
				return fmt.Errorf("agent_id is only valid for an agent recipient")
			}
			if input.Status != "in_review" || input.ContextMode != "fresh" {
				return fmt.Errorf("member handoff requires in_review and fresh context")
			}
		}
	}
	if input.Status != "in_progress" && input.Status != "in_review" {
		return fmt.Errorf("status must be in_progress or in_review")
	}
	switch input.ContextMode {
	case "fresh":
		if input.ResumeTaskID != "" {
			return fmt.Errorf("resume_task_id is only valid when context_mode is resume")
		}
	case "resume":
		if _, err := util.ParseUUID(input.ResumeTaskID); err != nil {
			return fmt.Errorf("resume context requires a UUID resume_task_id")
		}
	default:
		return fmt.Errorf("context_mode must be fresh or resume")
	}
	if len(input.Candidates) > 20 || len(input.EvidenceURLs) > 32 {
		return fmt.Errorf("handoff allows at most 20 candidates and 32 evidence URLs")
	}
	seenPRURLs := make(map[string]struct{}, len(input.Candidates))
	for i, candidate := range input.Candidates {
		if !candidate.Draft {
			return fmt.Errorf("candidates[%d].draft must be true", i)
		}
		if strings.TrimSpace(candidate.Branch) != candidate.Branch || candidate.Branch == "" || len(candidate.Branch) > 500 || strings.IndexFunc(candidate.Branch, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return fmt.Errorf("candidates[%d].branch must be a non-empty branch name of at most 500 bytes", i)
		}
		if !issueHandoffCommitSHA.MatchString(candidate.CommitSHA) {
			return fmt.Errorf("candidates[%d].commit_sha must be a 40- or 64-character hexadecimal hash", i)
		}
		for _, field := range []struct{ name, value string }{
			{"repository_url", candidate.RepositoryURL},
			{"pr_url", candidate.PRURL},
		} {
			if !isAbsoluteHTTPURL(field.value) {
				return fmt.Errorf("candidates[%d].%s must be an absolute HTTP(S) URL", i, field.name)
			}
		}
		if _, exists := seenPRURLs[candidate.PRURL]; exists {
			return fmt.Errorf("each PR may appear only once")
		}
		seenPRURLs[candidate.PRURL] = struct{}{}
	}
	for i, evidenceURL := range input.EvidenceURLs {
		if !isAbsoluteHTTPURL(evidenceURL) {
			return fmt.Errorf("evidence_urls[%d] must be an absolute HTTP(S) URL", i)
		}
	}
	return nil
}

var issueHandoffCommitSHA = regexp.MustCompile(`(?i)^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func isAbsoluteHTTPURL(raw string) bool {
	parsed, err := url.ParseRequestURI(raw)
	return err == nil && len(raw) <= 2048 && parsed.Host != "" && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func printIssueHandoffResult(cmd *cobra.Command, result any) error {
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	if output != "table" {
		return fmt.Errorf("output must be json or table")
	}
	rows := []map[string]any{}
	switch value := result.(type) {
	case []map[string]any:
		rows = value
	case map[string]any:
		rows = append(rows, value)
	}
	cells := make([][]string, 0, len(rows))
	for _, row := range rows {
		cells = append(cells, []string{
			strVal(row, "id"), strVal(row, "agent_id"), strVal(row, "filter_task_id"),
			strVal(row, "request_key"), fmt.Sprint(row["enabled"]), strVal(row, "last_task_id"),
		})
	}
	cli.PrintTable(os.Stdout, []string{"HANDOFF", "AGENT", "OUTGOING TASK", "REQUEST KEY", "ENABLED", "RECIPIENT TASK"}, cells)
	return nil
}
