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
	"unicode/utf8"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/spf13/cobra"
)

const maxIssueWorkflowInputBytes = 64 << 10

type issueWorkflowReviewInput struct {
	CandidateID  string   `json:"candidate_id"`
	Verdict      string   `json:"verdict"`
	PRReviewURLs []string `json:"pr_review_urls"`
}

type issueWorkflowAcceptanceInput struct {
	CandidateID          string   `json:"candidate_id"`
	ExpectedRevision     int64    `json:"expected_revision"`
	AcceptanceMode       string   `json:"acceptance_mode,omitempty"`
	ClassificationReason string   `json:"classification_reason,omitempty"`
	MergeOrderPRURLs     []string `json:"merge_order_pr_urls,omitempty"`
	OutcomeComplete      *bool    `json:"outcome_complete,omitempty"`
	HoldDelivery         *bool    `json:"hold_delivery,omitempty"`
}

type issueWorkflowAcceptanceDispositionInput struct {
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type issueWorkflowRejectionInput struct {
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Kind             string `json:"kind"`
	Reason           string `json:"reason"`
	ResumeTaskID     string `json:"resume_task_id,omitempty"`
}

type issueWorkflowExceptionInput struct {
	CandidateID      string         `json:"candidate_id"`
	ExpectedRevision int64          `json:"expected_revision"`
	Scope            string         `json:"scope"`
	GrantDetails     map[string]any `json:"grant_details"`
	Reason           string         `json:"reason"`
	Consequences     string         `json:"consequences"`
}

type issueWorkflowExceptionRevokeInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
	Consequences     string `json:"consequences"`
}

type issueWorkflowDeliveryRetryInput struct {
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason,omitempty"`
}

type issueWorkflowFeedbackContinuationInput struct {
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	CommentID        string `json:"comment_id"`
	Kind             string `json:"kind"`
}

func init() { issueCmd.AddCommand(newIssueWorkflowCommand()) }

func newIssueWorkflowCommand() *cobra.Command {
	workflow := &cobra.Command{Use: "workflow", Short: "Inspect and act on a ticket's workflow state"}
	get := &cobra.Command{
		Use:   "get <issue-id>",
		Short: "Show the current candidate, review, authority and delivery state",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runIssueWorkflow(cmd, args, "get") },
	}
	workflow.AddCommand(get)
	var exceptionCommand *cobra.Command
	for _, action := range []string{"review", "accept", "reject", "feedback-continue", "exception", "delivery-retry", "hold", "release", "complete", "retry-outcome"} {
		action := action
		use := action + " <issue-id>"
		args := cobra.ExactArgs(1)
		if action == "delivery-retry" {
			use = "delivery-retry <issue-id> <delivery-id>"
			args = cobra.ExactArgs(2)
		}
		command := &cobra.Command{
			Use:   use,
			Short: action + " using a revision-bound workflow JSON request",
			Long:  "Reads one strict JSON request from --file or stdin. Unknown fields and trailing JSON are rejected; use the candidate ID and issue revision shown by `multica issue workflow get`.",
			Args:  args,
			RunE:  func(cmd *cobra.Command, args []string) error { return runIssueWorkflow(cmd, args, action) },
		}
		if action == "accept" {
			command.Long = "Accept the current workflow candidate using a revision-bound JSON request from --file or stdin. " +
				"Use candidate.id and issue_revision from `multica issue workflow get`. " +
				"A human acceptor sends candidate_id and expected_revision. An authorized autonomous agent also supplies a non-empty classification_reason; set acceptance_mode to reviewed for policy-authorized nontrivial work after independent review, or omit it for the legacy trivial route. Its request is pending until the source task completes successfully. " +
				"Set outcome_complete only when the ticket's actual requirements are complete; required PR merges remain server-gated. Set hold_delivery to true to keep accepted work in PR Ready without merging; PR readiness checks may still proceed. " +
				"For reviewed acceptance, inspect reviewed_delivery_preview. Include merge_order_pr_urls when the selected delivery preview requires an order, listing every candidate PR URL in the intended order. Unknown fields and trailing JSON are rejected."
			command.Example = `  Human acceptance JSON:
	{"candidate_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","expected_revision":4}

	Autonomous acceptance JSON:
	{"candidate_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","expected_revision":4,"classification_reason":"Scoped trivial change with completed independent review"}

	Reviewed autonomous acceptance JSON:
	{"candidate_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","expected_revision":4,"acceptance_mode":"reviewed","classification_reason":"Approved technical outcome with independent review"}

	Accept and hold delivery:
	{"candidate_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","expected_revision":4,"hold_delivery":true}

	Add "merge_order_pr_urls":["https://git.example.com/team/app/pulls/7","https://git.example.com/team/app/pulls/8"] when the delivery preview requires an explicit PR order.`
		}
		if action == "feedback-continue" {
			command.Short = "Continue the retained workflow from a classified member comment"
			command.Long = "Reads one strict JSON request from --file or stdin. Use this current-agent continuation only when a member comment clearly requests an in-scope correction or identifies a scope change. Ordinary questions do not revoke review or acceptance; ambiguous scope must be escalated. The request is bound to the exact candidate, issue revision and comment. Unknown fields and trailing JSON are rejected."
			command.Example = `  {"candidate_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","expected_revision":4,"comment_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","kind":"in_scope_defect"}`
		}
		if isIssueWorkflowAcceptanceAction(action) {
			command.Flags().String("acceptance-id", "", "Accepted workflow record UUID")
			_ = command.MarkFlagRequired("acceptance-id")
			command.Long = issueWorkflowAcceptanceDispositionHelp(action)
			command.Example = "  {\"candidate_id\":\"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa\",\"expected_revision\":4,\"reason\":\"" + issueWorkflowDispositionExampleReason(action) + "\"}"
		}
		command.Flags().String("file", "", "Workflow request JSON file, or - to read JSON from stdin")
		command.Flags().Bool("allow-external-file", false, "Allow --file to read a path outside the current working directory")
		// `exception` is also the parent for `exception revoke`, so enforcing a
		// required local flag here would incorrectly reject the child command.
		if action != "exception" {
			_ = command.MarkFlagRequired("file")
		}
		workflow.AddCommand(command)
		if action == "exception" {
			exceptionCommand = command
		}
	}
	revoke := &cobra.Command{
		Use:   "revoke <issue-id> <exception-id>",
		Short: "Revoke one scoped workflow exception",
		Long:  "Reads expected_revision, reason and consequences from a strict JSON file or stdin. The exception ID is an explicit argument so a stale request cannot revoke a different grant.",
		Args:  cobra.ExactArgs(2),
		RunE:  func(cmd *cobra.Command, args []string) error { return runIssueWorkflow(cmd, args, "exception-revoke") },
	}
	revoke.Flags().String("file", "", "Workflow exception revocation JSON file, or - to read JSON from stdin")
	revoke.Flags().Bool("allow-external-file", false, "Allow --file to read a path outside the current working directory")
	_ = revoke.MarkFlagRequired("file")
	exceptionCommand.AddCommand(revoke)
	return workflow
}

func runIssueWorkflow(cmd *cobra.Command, args []string, action string) error {
	if action == "exception-revoke" || action == "delivery-retry" {
		if _, err := util.ParseUUID(args[1]); err != nil {
			if action == "delivery-retry" {
				return fmt.Errorf("delivery ID must be a UUID")
			}
			return fmt.Errorf("exception ID must be a UUID")
		}
	}
	if isIssueWorkflowAcceptanceAction(action) {
		acceptanceID, _ := cmd.Flags().GetString("acceptance-id")
		if _, err := util.ParseUUID(acceptanceID); err != nil {
			return fmt.Errorf("acceptance ID must be a UUID")
		}
	}
	var input any
	var body []byte
	if action != "get" {
		data, err := readIssueWorkflowInput(cmd)
		if err != nil {
			return err
		}
		input, err = decodeIssueWorkflowInput(data, action)
		if err != nil {
			return err
		}
		body, err = json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode workflow request: %w", err)
		}
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
	basePath := "/api/issues/" + url.PathEscape(issue.ID) + "/workflow"
	var result map[string]any
	if action == "get" {
		if err := client.GetJSON(ctx, basePath, &result); err != nil {
			return err
		}
	} else {
		path := basePath
		switch action {
		case "review":
			path += "/reviews"
		case "accept":
			path += "/acceptances"
		case "hold", "release", "complete", "retry-outcome":
			acceptanceID, _ := cmd.Flags().GetString("acceptance-id")
			path += "/acceptances/" + url.PathEscape(acceptanceID) + "/" + action
		case "reject":
			path += "/rejections"
		case "feedback-continue":
			path += "/feedback-continuations"
		case "exception":
			path += "/exceptions"
		case "exception-revoke":
			path += "/exceptions/" + url.PathEscape(args[1]) + "/revoke"
		case "delivery-retry":
			path += "/delivery/" + url.PathEscape(args[1]) + "/retry"
		}
		if err := client.PostJSON(ctx, path, json.RawMessage(body), &result); err != nil {
			return err
		}
	}
	return cli.PrintJSON(os.Stdout, result)
}

func readIssueWorkflowInput(cmd *cobra.Command) ([]byte, error) {
	filePath, _ := cmd.Flags().GetString("file")
	if strings.TrimSpace(filePath) == "" {
		return nil, fmt.Errorf("--file is required")
	}
	var reader io.Reader
	var file *os.File
	if filePath == "-" {
		reader = os.Stdin
	} else {
		if err := ensureFileFlagWithinWorkdir(cmd, "file", "workflow", filePath); err != nil {
			return nil, err
		}
		opened, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("read --file: %w", err)
		}
		file = opened
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxIssueWorkflowInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read workflow JSON: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("workflow JSON is empty")
	}
	if len(data) > maxIssueWorkflowInputBytes {
		return nil, fmt.Errorf("workflow JSON exceeds %d bytes", maxIssueWorkflowInputBytes)
	}
	return data, nil
}

func decodeIssueWorkflowInput(data []byte, action string) (any, error) {
	var input any
	switch action {
	case "review":
		input = &issueWorkflowReviewInput{}
	case "accept":
		input = &issueWorkflowAcceptanceInput{}
	case "hold", "release", "complete", "retry-outcome":
		input = &issueWorkflowAcceptanceDispositionInput{}
	case "reject":
		input = &issueWorkflowRejectionInput{}
	case "exception":
		input = &issueWorkflowExceptionInput{}
	case "exception-revoke":
		input = &issueWorkflowExceptionRevokeInput{}
	case "delivery-retry":
		input = &issueWorkflowDeliveryRetryInput{}
	case "feedback-continue":
		input = &issueWorkflowFeedbackContinuationInput{}
	default:
		return nil, fmt.Errorf("unknown workflow action %q", action)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return nil, fmt.Errorf("decode workflow JSON: %w", err)
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, fmt.Errorf("workflow JSON must contain exactly one object")
	}

	var err error
	switch typed := input.(type) {
	case *issueWorkflowReviewInput:
		err = validateIssueWorkflowReview(typed)
	case *issueWorkflowAcceptanceInput:
		err = validateIssueWorkflowAcceptance(typed)
	case *issueWorkflowAcceptanceDispositionInput:
		err = validateIssueWorkflowAcceptanceDisposition(typed)
	case *issueWorkflowRejectionInput:
		err = validateIssueWorkflowRejection(typed)
	case *issueWorkflowExceptionInput:
		err = validateIssueWorkflowException(typed)
	case *issueWorkflowExceptionRevokeInput:
		err = validateIssueWorkflowExceptionRevoke(typed)
	case *issueWorkflowDeliveryRetryInput:
		err = validateIssueWorkflowDeliveryRetry(typed)
	case *issueWorkflowFeedbackContinuationInput:
		err = validateIssueWorkflowFeedbackContinuation(typed)
	}
	if err != nil {
		return nil, err
	}
	return input, nil
}

func validateIssueWorkflowIdentity(candidateID string, revision int64) error {
	if _, err := util.ParseUUID(candidateID); err != nil {
		return fmt.Errorf("candidate_id must be a UUID")
	}
	if revision <= 0 {
		return fmt.Errorf("expected_revision must be a positive integer")
	}
	return nil
}

func validateIssueWorkflowReview(input *issueWorkflowReviewInput) error {
	if _, err := util.ParseUUID(input.CandidateID); err != nil {
		return fmt.Errorf("candidate_id must be a UUID")
	}
	if input.Verdict != "pass" && input.Verdict != "changes_requested" {
		return fmt.Errorf("verdict must be pass or changes_requested")
	}
	if len(input.PRReviewURLs) > 32 {
		return fmt.Errorf("pr_review_urls must contain at most 32 HTTPS URLs")
	}
	seen := make(map[string]bool, len(input.PRReviewURLs))
	for _, raw := range input.PRReviewURLs {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment == "" ||
			len(raw) > 2048 || strings.IndexFunc(raw, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || seen[raw] {
			return fmt.Errorf("pr_review_urls must contain distinct HTTPS links with review anchors")
		}
		seen[raw] = true
	}
	return nil
}

func validateIssueWorkflowAcceptance(input *issueWorkflowAcceptanceInput) error {
	if err := validateIssueWorkflowIdentity(input.CandidateID, input.ExpectedRevision); err != nil {
		return err
	}
	input.ClassificationReason = strings.TrimSpace(input.ClassificationReason)
	if input.AcceptanceMode != "" && input.AcceptanceMode != "reviewed" {
		return fmt.Errorf("acceptance_mode must be reviewed when provided")
	}
	if len(input.ClassificationReason) > 2000 {
		return fmt.Errorf("classification_reason must be at most 2000 bytes")
	}
	if len(input.MergeOrderPRURLs) > 20 {
		return fmt.Errorf("merge_order_pr_urls must contain at most 20 PR URLs")
	}
	return nil
}

func issueWorkflowAcceptanceDispositionHelp(action string) string {
	switch action {
	case "hold":
		return "Place a durable delivery hold on this exact accepted candidate. This blocks merging while the issue remains PR Ready; required PR readiness checks may still proceed. Retries and restarts preserve the hold until an authorized release. Reads candidate_id, expected_revision and a reason from --file or stdin."
	case "release":
		return "Release the durable hold for this exact acceptance and candidate so authorized delivery can proceed. The release is bound to the acceptance ID, candidate and issue revision; it does not authorize a later candidate. Reads candidate_id, expected_revision and a reason from --file or stdin."
	case "complete":
		return "Acknowledge that the accepted ticket's actual outcome is complete after its requirements are satisfied. The server still gates completion on all required PR merges. Reads candidate_id, expected_revision and a reason from --file or stdin."
	case "retry-outcome":
		return "Retry creation or dispatch of the stored outcome task after it failed or was cancelled. This human recovery action creates exactly one new task and preserves the earlier task history; the server enforces the recovery boundary. Reads candidate_id, expected_revision and a reason from --file or stdin."
	default:
		return ""
	}
}

func issueWorkflowDispositionExampleReason(action string) string {
	switch action {
	case "hold":
		return "Hold delivery until the deployment window"
	case "release":
		return "Approved delivery can proceed"
	case "complete":
		return "The ticket requirements are complete"
	case "retry-outcome":
		return "The stored outcome task failed and needs recovery"
	default:
		return "Reason"
	}
}

func isIssueWorkflowAcceptanceAction(action string) bool {
	switch action {
	case "hold", "release", "complete", "retry-outcome":
		return true
	default:
		return false
	}
}

func validateIssueWorkflowAcceptanceDisposition(input *issueWorkflowAcceptanceDispositionInput) error {
	if err := validateIssueWorkflowIdentity(input.CandidateID, input.ExpectedRevision); err != nil {
		return err
	}
	if input.Reason = strings.TrimSpace(input.Reason); input.Reason == "" {
		return fmt.Errorf("reason is required")
	}
	if utf8.RuneCountInString(input.Reason) > 500 {
		return fmt.Errorf("reason must be at most 500 Unicode code points")
	}
	return nil
}

func validateIssueWorkflowRejection(input *issueWorkflowRejectionInput) error {
	if err := validateIssueWorkflowIdentity(input.CandidateID, input.ExpectedRevision); err != nil {
		return err
	}
	if input.Kind != "in_scope_defect" && input.Kind != "scope_change" {
		return fmt.Errorf("kind must be in_scope_defect or scope_change")
	}
	if input.Reason = strings.TrimSpace(input.Reason); input.Reason == "" {
		return fmt.Errorf("reason is required")
	}
	if input.ResumeTaskID != "" {
		if _, err := util.ParseUUID(input.ResumeTaskID); err != nil {
			return fmt.Errorf("resume_task_id must be a UUID from the workflow's retained context options")
		}
	}
	return nil
}

func validateIssueWorkflowException(input *issueWorkflowExceptionInput) error {
	if err := validateIssueWorkflowIdentity(input.CandidateID, input.ExpectedRevision); err != nil {
		return err
	}
	if input.Scope != "review" && input.Scope != "acceptance" && input.Scope != "delivery" && input.Scope != "external_merge" {
		return fmt.Errorf("scope must be review, acceptance, delivery or external_merge")
	}
	if input.GrantDetails == nil || len(input.GrantDetails) == 0 {
		return fmt.Errorf("grant_details must describe a scoped exception")
	}
	if input.Reason = strings.TrimSpace(input.Reason); input.Reason == "" {
		return fmt.Errorf("reason is required")
	}
	if input.Consequences = strings.TrimSpace(input.Consequences); input.Consequences == "" {
		return fmt.Errorf("consequences are required")
	}
	return nil
}

func validateIssueWorkflowExceptionRevoke(input *issueWorkflowExceptionRevokeInput) error {
	if input.ExpectedRevision <= 0 {
		return fmt.Errorf("expected_revision must be a positive integer")
	}
	if input.Reason = strings.TrimSpace(input.Reason); input.Reason == "" {
		return fmt.Errorf("reason is required")
	}
	if input.Consequences = strings.TrimSpace(input.Consequences); input.Consequences == "" {
		return fmt.Errorf("consequences are required")
	}
	return nil
}

func validateIssueWorkflowDeliveryRetry(input *issueWorkflowDeliveryRetryInput) error {
	if err := validateIssueWorkflowIdentity(input.CandidateID, input.ExpectedRevision); err != nil {
		return err
	}
	if input.Reason = strings.TrimSpace(input.Reason); len(input.Reason) > 500 {
		return fmt.Errorf("reason must be at most 500 bytes")
	}
	return nil
}

func validateIssueWorkflowFeedbackContinuation(input *issueWorkflowFeedbackContinuationInput) error {
	if err := validateIssueWorkflowIdentity(input.CandidateID, input.ExpectedRevision); err != nil {
		return err
	}
	if _, err := util.ParseUUID(input.CommentID); err != nil {
		return fmt.Errorf("comment_id must be a UUID")
	}
	if input.Kind != "in_scope_defect" && input.Kind != "scope_change" {
		return fmt.Errorf("kind must be in_scope_defect or scope_change")
	}
	return nil
}
