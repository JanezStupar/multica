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
}));

vi.mock("@multica/core/issues/workflow", () => ({
  useIssueWorkflow: (...args: unknown[]) => {
    mocks.workflowRead(...args);
    return { data: mocks.workflow, isPending: false, isError: false, error: null, refetch: vi.fn() };
  },
  useAcceptIssueWorkflow: () => ({ mutate: mocks.accept, isPending: false, error: mocks.acceptError }),
  useRejectIssueWorkflow: () => ({ mutate: mocks.reject, isPending: false, error: null }),
  useRetryIssueWorkflowDelivery: () => ({ mutate: mocks.retryDelivery, isPending: mocks.retryDeliveryPending, error: mocks.retryDeliveryError }),
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
});

describe("IssueWorkflowSection", () => {
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
