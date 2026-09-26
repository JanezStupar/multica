import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { ApiError } from "@multica/core/api";
import type { IssueWorkflow } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { IssueWorkflowSection } from "./issue-workflow-section";

const mocks = vi.hoisted(() => ({
  workflow: null as IssueWorkflow | null,
  workflowRead: vi.fn(),
  accept: vi.fn(),
  acceptError: null as unknown,
  reject: vi.fn(),
  retryDelivery: vi.fn(),
  retryDeliveryPending: false,
  retryDeliveryError: null as unknown,
  updateAcceptance: vi.fn(),
  updateAcceptanceError: null as unknown,
}));

vi.mock("@multica/core/issues/workflow", () => ({
  useIssueWorkflow: (...args: unknown[]) => {
    mocks.workflowRead(...args);
    return { data: mocks.workflow, isPending: false, isError: false, error: null, refetch: vi.fn() };
  },
  useAcceptIssueWorkflow: () => ({ mutate: mocks.accept, isPending: false, error: mocks.acceptError }),
  useRejectIssueWorkflow: () => ({ mutate: mocks.reject, isPending: false, error: null }),
  useRetryIssueWorkflowDelivery: () => ({ mutate: mocks.retryDelivery, isPending: mocks.retryDeliveryPending, error: mocks.retryDeliveryError }),
  useUpdateIssueWorkflowAcceptance: () => ({ mutate: mocks.updateAcceptance, isPending: false, error: mocks.updateAcceptanceError }),
}));

function makeWorkflow(overrides: Partial<IssueWorkflow> = {}): IssueWorkflow {
  return {
    issue_id: "issue-1",
    issue_revision: 19,
    policy_version: "v1",
    frozen: false,
    candidate: {
      id: "candidate-42",
      digest: "sha256:abc123",
      scope_digest: "sha256:scope",
      writer_task_id: "task-writer",
      created_at: "2026-09-24T12:00:00Z",
      prs: [{
        repository_url: "https://github.com/acme/service",
        pr_url: "https://github.com/acme/service/pull/42",
        branch: "fix/retry-window",
        commit_sha: "0123456789abcdef0123456789abcdef01234567",
        draft: false,
      }],
    },
    reviews: [{
      id: "review-1",
      verdict: "pass",
      reviewer_task_id: "task-review",
      pr_review_urls: ["https://github.com/acme/service/pull/42#pullrequestreview-7"],
      submitted_at: "2026-09-24T12:30:00Z",
    }],
    acceptance: null,
    acceptance_blockers: [],
    delivery_preview: { action: "ready", requires_order: false },
    delivery: [
      { id: "delivery-1", ordinal: 1, pr_url: "https://github.com/acme/service/pull/42", expected_head_sha: "0123456789abcdef0123456789abcdef01234567", action: "ready", status: "delivered", attempt_count: 1, next_attempt_at: "", readiness_done_at: "2026-09-24T12:35:00Z", merged_at: null },
      { id: "delivery-2", ordinal: 2, pr_url: "https://github.com/acme/other/pull/8", expected_head_sha: "89abcdef0123456789abcdef0123456789abcdef", action: "merge", status: "blocked", attempt_count: 2, next_attempt_at: "", last_error_class: "provider_unavailable", readiness_done_at: null, merged_at: null, retryable: true },
    ],
    exceptions: [],
    retained_context_options: [{ task_id: "task-writer", agent_id: "agent-1", agent_name: "Writer agent", kind: "writer" }],
    available_actions: { accept_human: true, reject: true, request_trivial_acceptance: false, waive_review: false },
    ...overrides,
  };
}

beforeEach(() => {
  mocks.workflow = makeWorkflow();
  mocks.workflowRead.mockClear();
  mocks.accept.mockReset();
  mocks.acceptError = null;
  mocks.reject.mockReset();
  mocks.retryDelivery.mockReset();
  mocks.retryDeliveryPending = false;
  mocks.retryDeliveryError = null;
  mocks.updateAcceptance.mockReset();
  mocks.updateAcceptanceError = null;
});

describe("IssueWorkflowSection", () => {
  it("shows historical feedback evidence with a deep link without exposing the snapshot", () => {
    mocks.workflow = makeWorkflow({
      feedback: {
        comment_id: "comment-7",
        comment_revision: 3,
        content_sha256: "a".repeat(64),
        kind: "in_scope_defect",
        candidate_id: "candidate-42",
        source_task_id: "task-writer",
        created_at: "2026-09-24T12:40:00Z",
      },
      acceptance: {
        id: "accept-1",
        candidate_id: "candidate-42",
        state: "accepted",
        mode: "human",
        requested_at: "2026-09-24T12:45:00Z",
        accepted_at: "2026-09-24T12:46:00Z",
      },
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText("Changes were requested from this comment.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to comment" })).toHaveAttribute("href", "#comment-comment-7");
    expect(screen.queryByText("a".repeat(64))).not.toBeInTheDocument();
  });

  it("explains a changed candidate PR head to a read-only viewer", () => {
    mocks.workflow = makeWorkflow({
      acceptance_blockers: ["candidate_head_changed"],
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText("The candidate PR head changed. Reject this candidate and evaluate a new one.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reject candidate" })).not.toBeInTheDocument();
  });

  it("shows exact candidate commits, independent reviews, and mixed delivery outcomes", () => {
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText((_, element) => element?.tagName === "P" && element.textContent?.includes("sha256:abc123") === true)).toBeInTheDocument();
    expect(screen.getAllByText("0123456789abcdef0123456789abcdef01234567")).toHaveLength(2);
    expect(screen.getByRole("link", { name: /Open review 1/ })).toHaveAttribute("href", "https://github.com/acme/service/pull/42#pullrequestreview-7");
    expect(screen.getByText("1 of 2 delivered")).toBeInTheDocument();
    expect(screen.getByText("Blocked")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry PR 2" })).toBeInTheDocument();
  });

  it("retries only a server-authorized blocked PR using the displayed candidate and revision", () => {
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    fireEvent.click(screen.getByRole("button", { name: "Retry PR 2" }));

    expect(mocks.retryDelivery).toHaveBeenCalledWith({
      deliveryId: "delivery-2",
      input: { candidate_id: "candidate-42", expected_revision: 19 },
    });
  });

  it("keeps blocked delivery visible and reports a failed retry", () => {
    mocks.retryDeliveryError = new ApiError("retry conflict", 409, "Conflict", { code: "workflow_authority_conflict" });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByRole("alert")).toHaveTextContent("Could not retry delivery. Refresh the workflow and check availability.");
    expect(screen.getByText("Blocked")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry PR 2" })).toBeEnabled();
  });

  it("does not offer retry without server authorization or on a frozen issue", () => {
    const workflow = makeWorkflow();
    mocks.workflow = makeWorkflow({
      delivery: workflow.delivery.map((delivery) => ({ ...delivery, retryable: false })),
    });
    const view = renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);
    expect(screen.queryByRole("button", { name: "Retry PR 2" })).not.toBeInTheDocument();

    mocks.workflow = makeWorkflow({
      frozen: true,
      delivery: workflow.delivery.map((delivery) => ({ ...delivery, retryable: true })),
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false },
    });
    view.rerender(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);
    expect(screen.queryByRole("button", { name: "Retry PR 2" })).not.toBeInTheDocument();
  });

  it("accepts the displayed candidate using its current issue revision", () => {
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    fireEvent.click(screen.getByRole("button", { name: "Accept and mark done" }));

    expect(mocks.accept).toHaveBeenCalledWith({ candidate_id: "candidate-42", expected_revision: 19 });
  });

  it("requires an explicit outcome choice and hold choice for the completion contract", () => {
    mocks.workflow = makeWorkflow({ accepted_status_key: "pr_ready" });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    fireEvent.click(screen.getByRole("button", { name: "Accept candidate" }));
    expect(mocks.accept).toHaveBeenLastCalledWith({ candidate_id: "candidate-42", expected_revision: 19, outcome_complete: false, hold_delivery: false });

    fireEvent.click(screen.getByRole("checkbox", { name: "The actual outcome is complete" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Hold PR delivery until released" }));
    fireEvent.click(screen.getByRole("button", { name: "Accept candidate" }));
    expect(mocks.accept).toHaveBeenLastCalledWith({ candidate_id: "candidate-42", expected_revision: 19, outcome_complete: true, hold_delivery: true });
  });

  it("keeps no-PR acceptance separate from outcome completion", () => {
    const candidate = makeWorkflow().candidate!;
    mocks.workflow = makeWorkflow({ accepted_status_key: "pr_ready", candidate: { ...candidate, prs: [] }, delivery_preview: null, delivery: [] });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText("Accept this candidate. The issue is done only when its actual outcome is complete.")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: "Hold PR delivery until released" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Accept candidate" }));
    expect(mocks.accept).toHaveBeenCalledWith({ candidate_id: "candidate-42", expected_revision: 19, outcome_complete: false, hold_delivery: false });
  });

  it("shows accepted incomplete and held work, and binds release to the displayed revision", () => {
    mocks.workflow = makeWorkflow({
      accepted_status_key: "pr_ready",
      acceptance: { id: "accept-1", candidate_id: "candidate-42", state: "accepted", mode: "human", requested_at: "2026-09-24T12:30:00Z", accepted_at: "2026-09-24T12:31:00Z", hold_delivery: true, outcome_complete: false, outcome_completed_at: null },
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false, release_delivery: true, complete_outcome: true },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText(/actual outcome is still pending/)).toBeInTheDocument();
    expect(screen.getByText("PR delivery is on hold.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry PR 2" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Release delivery" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "Ready for merge" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Release delivery" }).at(-1)!);
    expect(mocks.updateAcceptance).toHaveBeenCalledWith({
      acceptanceId: "accept-1", action: "release", input: { candidate_id: "candidate-42", expected_revision: 19, reason: "Ready for merge" },
    }, expect.any(Object));
  });

  it("does not present a historical hold as active after the PR is verified merged", () => {
    const workflow = makeWorkflow();
    mocks.workflow = makeWorkflow({
      accepted_status_key: "pr_ready",
      acceptance: { id: "accept-1", candidate_id: "candidate-42", state: "accepted", mode: "human", requested_at: "2026-09-24T12:30:00Z", accepted_at: "2026-09-24T12:31:00Z", hold_delivery: true, outcome_complete: true, outcome_completed_at: "2026-09-24T13:00:00Z" },
      delivery: [{ ...workflow.delivery[1]!, pr_url: workflow.candidate!.prs[0]!.pr_url, status: "delivered", merged_at: "2026-09-24T12:50:00Z" }],
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false, release_delivery: true },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText("The actual outcome is complete.")).toBeInTheDocument();
    expect(screen.queryByText("PR delivery is on hold.")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Release delivery" })).not.toBeInTheDocument();
  });

  it("shows a pending outcome run without claiming completion", () => {
    mocks.workflow = makeWorkflow({
      accepted_status_key: "pr_ready",
      acceptance: { id: "accept-1", candidate_id: "candidate-42", state: "accepted", mode: "human", requested_at: "2026-09-24T12:30:00Z", accepted_at: "2026-09-24T12:31:00Z", outcome_pending: true, outcome_task_id: "task-5", outcome_task_active: true },
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false, complete_outcome: true },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);
    expect(screen.getByText("Outcome confirmation is pending.")).toBeInTheDocument();
    expect(screen.getByText("Outcome run is queued or running.")).toBeInTheDocument();
    expect(screen.getByText("task-5")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Confirm outcome complete" })).not.toBeInTheDocument();
  });

  it("offers outcome completion only when authorized and records the displayed revision", () => {
    mocks.workflow = makeWorkflow({
      accepted_status_key: "pr_ready",
      acceptance: { id: "accept-1", candidate_id: "candidate-42", state: "accepted", mode: "human", requested_at: "2026-09-24T12:30:00Z", accepted_at: "2026-09-24T12:31:00Z", outcome_complete: false },
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false, hold_delivery: true, complete_outcome: true },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByRole("button", { name: "Hold delivery" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Confirm outcome complete" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "Acceptance QA passed" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Confirm outcome complete" }).at(-1)!);
    expect(mocks.updateAcceptance).toHaveBeenCalledWith({
      acceptanceId: "accept-1", action: "complete", input: { candidate_id: "candidate-42", expected_revision: 19, reason: "Acceptance QA passed" },
    }, expect.any(Object));
  });

  it("offers recovery for a failed outcome run without showing it as active", () => {
    mocks.workflow = makeWorkflow({
      accepted_status_key: "pr_ready",
      acceptance: { id: "accept-1", candidate_id: "candidate-42", state: "accepted", mode: "human", requested_at: "2026-09-24T12:30:00Z", accepted_at: "2026-09-24T12:31:00Z", outcome_complete: false, outcome_pending: false, outcome_task_id: "failed-run", outcome_task_active: false, blocker: "outcome_task_failed" },
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false, retry_outcome: true },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText(/actual outcome is still pending/)).toBeInTheDocument();
    expect(screen.queryByText("Outcome run is queued or running.")).not.toBeInTheDocument();
    expect(screen.getByText("Outcome run stopped before completion.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry outcome run" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "Recover from failed run" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Retry outcome run" }).at(-1)!);
    expect(mocks.updateAcceptance).toHaveBeenCalledWith({
      acceptanceId: "accept-1", action: "retry-outcome", input: { candidate_id: "candidate-42", expected_revision: 19, reason: "Recover from failed run" },
    }, expect.any(Object));
  });

  it("shows a failed outcome run to viewers without recovery permission", () => {
    mocks.workflow = makeWorkflow({
      accepted_status_key: "pr_ready",
      acceptance: { id: "accept-1", candidate_id: "candidate-42", state: "accepted", mode: "human", requested_at: "2026-09-24T12:30:00Z", accepted_at: "2026-09-24T12:31:00Z", outcome_complete: false, outcome_pending: false, outcome_task_id: "failed-run", outcome_task_active: false, blocker: "outcome_task_failed" },
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false, retry_outcome: false },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText("Outcome run stopped before completion.")).toBeInTheDocument();
    expect(screen.queryByText("Acceptance is not ready. Refresh the workflow details.")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry outcome run" })).not.toBeInTheDocument();
  });

  it("shows a failed outcome dispatch to viewers without retry permission", () => {
    mocks.workflow = makeWorkflow({
      accepted_status_key: "pr_ready",
      acceptance: { id: "accept-1", candidate_id: "candidate-42", state: "accepted", mode: "human", requested_at: "2026-09-24T12:30:00Z", accepted_at: "2026-09-24T12:31:00Z", outcome_complete: false, outcome_pending: false, outcome_task_active: false, blocker: "outcome_dispatch_failed" },
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false, retry_outcome: false },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText("The outcome run could not be queued. It will retry automatically.")).toBeInTheDocument();
    expect(screen.queryByText("Outcome run stopped before completion.")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry outcome run" })).not.toBeInTheDocument();
  });

  it("explains a delayed Done transition after verified delivery to a viewer", () => {
    const workflow = makeWorkflow();
    mocks.workflow = makeWorkflow({
      accepted_status_key: "pr_ready",
      acceptance: { id: "accept-1", candidate_id: "candidate-42", state: "accepted", mode: "human", requested_at: "2026-09-24T12:30:00Z", accepted_at: "2026-09-24T12:31:00Z", outcome_complete: true, blocker: "completion_reconcile_failed" },
      delivery: [{ ...workflow.delivery[1]!, pr_url: workflow.candidate!.prs[0]!.pr_url, status: "delivered", merged_at: "2026-09-24T12:50:00Z" }],
      available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false, retry_outcome: false },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText("The actual outcome is complete.")).toBeInTheDocument();
    expect(screen.getByText("The issue could not be marked done after delivery. The workflow will retry automatically.")).toBeInTheDocument();
    expect(screen.queryByText("Acceptance is not ready. Refresh the workflow details.")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retry outcome run" })).not.toBeInTheDocument();
  });

  it("uses the refreshed candidate and revision after a candidate change", () => {
    const view = renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);
    mocks.workflow = makeWorkflow({
      issue_revision: 20,
      candidate: { ...makeWorkflow().candidate!, id: "candidate-43", digest: "sha256:def456" },
    });
    view.rerender(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);
    fireEvent.click(screen.getByRole("button", { name: "Accept and mark done" }));

    expect(mocks.accept).toHaveBeenCalledWith({ candidate_id: "candidate-43", expected_revision: 20 });
  });

  it("requires a complete explicit PR order when acceptance will merge multiple PRs", () => {
    const workflow = makeWorkflow();
    const prs = [
      ...workflow.candidate!.prs,
      {
        repository_url: "https://github.com/acme/worker",
        pr_url: "https://github.com/acme/worker/pull/9",
        branch: "fix/queue",
        commit_sha: "fedcba9876543210fedcba9876543210fedcba98",
        draft: false,
      },
    ];
    mocks.workflow = makeWorkflow({
      candidate: { ...workflow.candidate!, prs },
      acceptance_blockers: ["merge_order_required"],
      delivery_preview: { action: "merge", merge_method: "squash", requires_order: true },
    });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText("Acceptance merges the listed pull requests using squash commits.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Accept and mark done" })).not.toBeInTheDocument();
    fireEvent.change(screen.getByRole("combobox", { name: "Merge order for pull request 1" }), { target: { value: "1" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Merge order for pull request 2" }), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: "Accept and mark done" }));

    expect(mocks.accept).toHaveBeenCalledWith({
      candidate_id: "candidate-42",
      expected_revision: 19,
      merge_order_pr_urls: prs.map((pr) => pr.pr_url),
    });
  });

  it("clears a prior PR order when the displayed candidate changes", () => {
    const workflow = makeWorkflow();
    const prs = [
      ...workflow.candidate!.prs,
      {
        repository_url: "https://github.com/acme/worker",
        pr_url: "https://github.com/acme/worker/pull/9",
        branch: "fix/queue",
        commit_sha: "fedcba9876543210fedcba9876543210fedcba98",
        draft: false,
      },
    ];
    const first = makeWorkflow({
      candidate: { ...workflow.candidate!, prs },
      acceptance_blockers: ["merge_order_required"],
      delivery_preview: { action: "merge", merge_method: "squash", requires_order: true },
    });
    mocks.workflow = first;
    const view = renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);
    fireEvent.change(screen.getByRole("combobox", { name: "Merge order for pull request 1" }), { target: { value: "1" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Merge order for pull request 2" }), { target: { value: "2" } });

    mocks.workflow = makeWorkflow({
      issue_revision: 20,
      candidate: { ...first.candidate!, id: "candidate-43", digest: "sha256:new", prs },
      acceptance_blockers: ["merge_order_required"],
      delivery_preview: { action: "merge", merge_method: "squash", requires_order: true },
    });
    view.rerender(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByRole("combobox", { name: "Merge order for pull request 1" })).toHaveValue("");
    expect(screen.getByRole("combobox", { name: "Merge order for pull request 2" })).toHaveValue("");
    expect(screen.queryByRole("button", { name: "Accept and mark done" })).not.toBeInTheDocument();
  });

  it("keeps the candidate action visible and reports a failed acceptance", () => {
    mocks.acceptError = new ApiError("revision conflict", 409, "Conflict", { code: "revision_conflict" });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByRole("alert")).toHaveTextContent("The candidate changed. Review the updated candidate before continuing.");
    expect(screen.getByRole("button", { name: "Accept and mark done" })).toBeInTheDocument();
  });

  it("shows frozen state and hides candidate actions", () => {
    mocks.workflow = makeWorkflow({ frozen: true, available_actions: { accept_human: false, reject: false, request_trivial_acceptance: false, waive_review: false } });
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);

    expect(screen.getByText((_, element) => element?.tagName === "P" && element.textContent?.includes("This issue is frozen") === true)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Accept and mark done" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reject candidate" })).not.toBeInTheDocument();
  });

  it("renders a frozen legacy notice without requesting workflow authority", () => {
    mocks.workflow = null;
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="legacy-1" enabled={false} frozen />);

    expect(screen.getByLabelText("Frozen issue")).toBeInTheDocument();
    expect(screen.getByText("This issue is frozen until it is explicitly migrated. Its existing work and history are preserved.")).toBeInTheDocument();
    expect(mocks.workflowRead).toHaveBeenCalledWith("ws-1", "legacy-1", false);
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
  });

  it("offers only server-provided retained contexts when rejecting an in-scope defect", () => {
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);
    fireEvent.click(screen.getAllByRole("button", { name: "Reject candidate" }).at(-1)!);

    expect(screen.getByRole("option", { name: /Implementation · Writer agent/ })).toHaveValue("task-writer");
    expect(screen.queryByRole("textbox", { name: /task id/i })).not.toBeInTheDocument();
  });

  it("submits the selected retained task with the rejection reason", () => {
    renderWithI18n(<IssueWorkflowSection workspaceId="ws-1" issueId="issue-1" enabled />);
    fireEvent.click(screen.getAllByRole("button", { name: "Reject candidate" }).at(-1)!);
    fireEvent.change(screen.getByRole("textbox", { name: "Reason" }), { target: { value: "Retry handling still drops the final attempt." } });
    fireEvent.change(screen.getByRole("combobox", { name: "Continue a retained context" }), { target: { value: "task-writer" } });
    fireEvent.click(screen.getByRole("button", { name: "Reject candidate" }));

    expect(mocks.reject).toHaveBeenCalledWith({
      candidate_id: "candidate-42",
      expected_revision: 19,
      kind: "in_scope_defect",
      reason: "Retry handling still drops the final attempt.",
      resume_task_id: "task-writer",
    }, expect.any(Object));
  });
});
