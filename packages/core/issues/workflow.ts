import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { AcceptIssueWorkflowRequest, IssueWorkflow, RejectIssueWorkflowRequest, RevokeIssueWorkflowExceptionRequest, RetryIssueWorkflowDeliveryRequest, UpdateIssueWorkflowAcceptanceRequest } from "../types";
import { issueKeys } from "./queries";

export const ISSUE_WORKFLOW_PROGRESS_MIN_REFETCH_MS = 5_000;
export const ISSUE_WORKFLOW_PROGRESS_MAX_REFETCH_MS = 30_000;

/** Poll only while acceptance, an outcome run, or provider delivery can advance. */
export function issueWorkflowProgressRefetchInterval(workflow: IssueWorkflow | undefined, now = Date.now()): number | false {
  if (!workflow) return false;
  if (workflow.acceptance?.state === "requested") return ISSUE_WORKFLOW_PROGRESS_MIN_REFETCH_MS;
  if (workflow.acceptance?.outcome_pending || workflow.acceptance?.outcome_task_active) return ISSUE_WORKFLOW_PROGRESS_MIN_REFETCH_MS;
  const nextRetry = workflow.delivery
    .filter((item) => item.status === "pending" || item.status === "retry")
    .map((item) => Date.parse(item.next_attempt_at))
    .filter(Number.isFinite)
    .sort((left, right) => left - right)[0];
  if (nextRetry === undefined) return false;
  const untilRetry = nextRetry - now;
  return Math.min(
    ISSUE_WORKFLOW_PROGRESS_MAX_REFETCH_MS,
    Math.max(ISSUE_WORKFLOW_PROGRESS_MIN_REFETCH_MS, untilRetry),
  );
}

export const issueWorkflowKeys = {
  all: (workspaceId: string) => ["issue-workflow", workspaceId] as const,
  detail: (workspaceId: string, issueId: string) => [...issueWorkflowKeys.all(workspaceId), issueId] as const,
};

export function issueWorkflowOptions(workspaceId: string, issueId: string) {
  return queryOptions({
    queryKey: issueWorkflowKeys.detail(workspaceId, issueId),
    queryFn: () => api.getIssueWorkflow(issueId),
    enabled: !!workspaceId && !!issueId,
    refetchOnWindowFocus: true,
    refetchInterval: (query) => issueWorkflowProgressRefetchInterval(query.state.data),
  });
}

export function useIssueWorkflow(workspaceId: string, issueId: string, enabled = true) {
  return useQuery({
    ...issueWorkflowOptions(workspaceId, issueId),
    enabled: enabled && !!workspaceId && !!issueId,
  });
}

async function invalidateIssueWorkflow(client: ReturnType<typeof useQueryClient>, workspaceId: string, issueId: string) {
  await Promise.all([
    client.invalidateQueries({ queryKey: issueWorkflowKeys.detail(workspaceId, issueId) }),
    client.invalidateQueries({ queryKey: issueKeys.all(workspaceId) }),
    client.invalidateQueries({ queryKey: issueKeys.tasks(issueId) }),
  ]);
}

export function useAcceptIssueWorkflow(workspaceId: string, issueId: string) {
  const client = useQueryClient();
  return useMutation<IssueWorkflow, Error, AcceptIssueWorkflowRequest>({
    mutationFn: (input) => api.acceptIssueWorkflow(issueId, input),
    onSettled: () => invalidateIssueWorkflow(client, workspaceId, issueId),
  });
}

export function useUpdateIssueWorkflowAcceptance(workspaceId: string, issueId: string) {
  const client = useQueryClient();
  return useMutation<IssueWorkflow, Error, { acceptanceId: string; action: "hold" | "release" | "complete" | "retry-outcome"; input: UpdateIssueWorkflowAcceptanceRequest }>({
    mutationFn: ({ acceptanceId, action, input }) => api.updateIssueWorkflowAcceptance(issueId, acceptanceId, action, input),
    onSettled: () => invalidateIssueWorkflow(client, workspaceId, issueId),
  });
}

export function useRejectIssueWorkflow(workspaceId: string, issueId: string) {
  const client = useQueryClient();
  return useMutation<IssueWorkflow, Error, RejectIssueWorkflowRequest>({
    mutationFn: (input) => api.rejectIssueWorkflow(issueId, input),
    onSettled: () => invalidateIssueWorkflow(client, workspaceId, issueId),
  });
}

export function useRevokeIssueWorkflowException(workspaceId: string, issueId: string) {
  const client = useQueryClient();
  return useMutation<IssueWorkflow, Error, { exceptionId: string; input: RevokeIssueWorkflowExceptionRequest }>({
    mutationFn: ({ exceptionId, input }) => api.revokeIssueWorkflowException(issueId, exceptionId, input),
    onSettled: () => invalidateIssueWorkflow(client, workspaceId, issueId),
  });
}

export function useRetryIssueWorkflowDelivery(workspaceId: string, issueId: string) {
  const client = useQueryClient();
  return useMutation<IssueWorkflow, Error, { deliveryId: string; input: RetryIssueWorkflowDeliveryRequest }>({
    mutationFn: ({ deliveryId, input }) => api.retryIssueWorkflowDelivery(issueId, deliveryId, input),
    onSettled: () => invalidateIssueWorkflow(client, workspaceId, issueId),
  });
}
