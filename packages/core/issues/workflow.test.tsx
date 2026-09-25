/** @vitest-environment jsdom */
import { act, cleanup, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import type { IssueWorkflow } from "../types";
import { issueKeys } from "./queries";
import {
  issueWorkflowKeys,
  issueWorkflowOptions,
  issueWorkflowProgressRefetchInterval,
  useAcceptIssueWorkflow,
  useIssueWorkflow,
  useRejectIssueWorkflow,
  useRetryIssueWorkflowDelivery,
} from "./workflow";

afterEach(() => cleanup());

const WS_ID = "workspace-1";
const ISSUE_ID = "issue-1";
const workflow: IssueWorkflow = {
  issue_id: ISSUE_ID,
  issue_revision: 4,
  policy_version: "sha256:policy",
  frozen: false,
  candidate: null,
  reviews: [],
  acceptance: null,
  acceptance_blockers: [],
  delivery_preview: null,
  delivery: [],
  exceptions: [],
  retained_context_options: [],
  available_actions: { accept_human: true, reject: true, request_trivial_acceptance: false, waive_review: false },
};

function wrapper(client: QueryClient) {
  return function WorkflowQueryWrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

it("isolates workflow queries by workspace and issue", async () => {
  expect(issueWorkflowOptions("ws-1", "issue-1").queryKey).not.toEqual(
    issueWorkflowOptions("ws-2", "issue-1").queryKey,
  );
  expect(issueWorkflowOptions("ws-1", "issue-1").queryKey).not.toEqual(
    issueWorkflowOptions("ws-1", "issue-2").queryKey,
  );

  const getIssueWorkflow = vi.fn().mockResolvedValue(workflow);
  setApiInstance({ getIssueWorkflow } as unknown as ApiClient);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { result } = renderHook(() => useIssueWorkflow(WS_ID, ISSUE_ID), { wrapper: wrapper(client) });
  await vi.waitFor(() => expect(result.current.data).toEqual(workflow));
  expect(getIssueWorkflow).toHaveBeenCalledWith(ISSUE_ID);
});

it("polls only while acceptance or delivery can still advance, with bounded intervals", () => {
  const now = Date.parse("2026-09-24T00:00:00Z");
  expect(issueWorkflowProgressRefetchInterval(workflow, now)).toBe(false);
  expect(issueWorkflowProgressRefetchInterval({
    ...workflow,
    acceptance: {
      id: "acceptance", candidate_id: "candidate", state: "requested", mode: "autonomous",
      requested_at: "2026-09-24T00:00:00Z", accepted_at: null,
    },
  }, now)).toBe(5_000);
  expect(issueWorkflowProgressRefetchInterval({
    ...workflow,
    accepted_status_key: "pr_ready",
    acceptance: {
      id: "acceptance", candidate_id: "candidate", state: "accepted", mode: "human",
      requested_at: "2026-09-24T00:00:00Z", accepted_at: "2026-09-24T00:00:01Z",
      outcome_task_active: true, outcome_task_id: "outcome-run",
    },
  }, now)).toBe(5_000);
  expect(issueWorkflowProgressRefetchInterval({
    ...workflow,
    accepted_status_key: "pr_ready",
    acceptance: {
      id: "acceptance", candidate_id: "candidate", state: "accepted", mode: "human",
      requested_at: "2026-09-24T00:00:00Z", accepted_at: "2026-09-24T00:00:01Z",
      outcome_task_active: false, outcome_pending: false, outcome_task_id: "failed-outcome-run",
    },
  }, now)).toBe(false);
  expect(issueWorkflowProgressRefetchInterval({
    ...workflow,
    accepted_status_key: "pr_ready",
    acceptance: {
      id: "acceptance", candidate_id: "candidate", state: "accepted", mode: "human",
      requested_at: "2026-09-24T00:00:00Z", accepted_at: "2026-09-24T00:00:01Z",
      outcome_pending: true, outcome_task_id: "pending-acknowledgment-run",
    },
  }, now)).toBe(5_000);
  expect(issueWorkflowProgressRefetchInterval({
    ...workflow,
    delivery: [{
      id: "delivery", ordinal: 1, pr_url: "https://example.test/pr/1", expected_head_sha: "abc",
      action: "merge", status: "retry", attempt_count: 1,
      next_attempt_at: "2026-09-24T00:00:03Z", readiness_done_at: null, merged_at: null,
    }],
  }, now)).toBe(5_000);
  expect(issueWorkflowProgressRefetchInterval({
    ...workflow,
    delivery: [{
      id: "delivery", ordinal: 1, pr_url: "https://example.test/pr/1", expected_head_sha: "abc",
      action: "merge", status: "pending", attempt_count: 0,
      next_attempt_at: "2026-09-24T00:10:00Z", readiness_done_at: null, merged_at: null,
    }],
  }, now)).toBe(30_000);
});

it("awaits accept/reject and invalidates issue, workflow, and task projections", async () => {
  const acceptIssueWorkflow = vi.fn().mockResolvedValue(workflow);
  const rejectIssueWorkflow = vi.fn().mockResolvedValue(workflow);
  setApiInstance({ acceptIssueWorkflow, rejectIssueWorkflow } as unknown as ApiClient);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

  const workflowKey = issueWorkflowKeys.detail(WS_ID, ISSUE_ID);
  const issueKey = issueKeys.list(WS_ID);
  const taskKey = issueKeys.tasks(ISSUE_ID);
  client.setQueryData(workflowKey, workflow);
  client.setQueryData(issueKey, []);
  client.setQueryData(taskKey, []);

  const acceptHook = renderHook(() => useAcceptIssueWorkflow(WS_ID, ISSUE_ID), { wrapper: wrapper(client) });
  await act(async () => {
    await acceptHook.result.current.mutateAsync({ candidate_id: "candidate-1", expected_revision: 4 });
  });
  expect(acceptIssueWorkflow).toHaveBeenCalledWith(ISSUE_ID, { candidate_id: "candidate-1", expected_revision: 4 });
  expect(client.getQueryState(workflowKey)?.isInvalidated).toBe(true);
  expect(client.getQueryState(issueKey)?.isInvalidated).toBe(true);
  expect(client.getQueryState(taskKey)?.isInvalidated).toBe(true);
  acceptHook.unmount();

  const rejectHook = renderHook(() => useRejectIssueWorkflow(WS_ID, ISSUE_ID), { wrapper: wrapper(client) });
  await act(async () => {
    await rejectHook.result.current.mutateAsync({
      candidate_id: "candidate-1", expected_revision: 4, kind: "in_scope_defect", reason: "Fix the failing test",
      resume_task_id: "task-1",
    });
  });
  expect(rejectIssueWorkflow).toHaveBeenCalledWith(ISSUE_ID, {
    candidate_id: "candidate-1", expected_revision: 4, kind: "in_scope_defect", reason: "Fix the failing test",
    resume_task_id: "task-1",
  });
  expect(client.getQueryState(workflowKey)?.isInvalidated).toBe(true);
});

it("invalidates workflow, issue, and task projections after delivery retry", async () => {
  const retryIssueWorkflowDelivery = vi.fn().mockResolvedValue(workflow);
  setApiInstance({ retryIssueWorkflowDelivery } as unknown as ApiClient);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const workflowKey = issueWorkflowKeys.detail(WS_ID, ISSUE_ID);
  const issueKey = issueKeys.list(WS_ID);
  const taskKey = issueKeys.tasks(ISSUE_ID);
  client.setQueryData(workflowKey, workflow);
  client.setQueryData(issueKey, []);
  client.setQueryData(taskKey, []);

  const { result } = renderHook(() => useRetryIssueWorkflowDelivery(WS_ID, ISSUE_ID), { wrapper: wrapper(client) });
  await act(async () => {
    await result.current.mutateAsync({ deliveryId: "delivery-1", input: { candidate_id: "candidate-1", expected_revision: 4 } });
  });
  expect(retryIssueWorkflowDelivery).toHaveBeenCalledWith(ISSUE_ID, "delivery-1", {
    candidate_id: "candidate-1", expected_revision: 4,
  });
  expect(client.getQueryState(workflowKey)?.isInvalidated).toBe(true);
  expect(client.getQueryState(issueKey)?.isInvalidated).toBe(true);
  expect(client.getQueryState(taskKey)?.isInvalidated).toBe(true);
});
