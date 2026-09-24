// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { IssueHandoffRequestSchema, IssueHandoffSchema } from "./schemas";

afterEach(() => vi.unstubAllGlobals());

const issueId = "11111111-1111-4111-8111-111111111111";
const handoffId = "55555555-5555-4555-8555-555555555555";
const request = {
  request_key: "22222222-2222-4222-8222-222222222222",
  outgoing_task_id: "33333333-3333-4333-8333-333333333333",
  agent_id: "44444444-4444-4444-8444-444444444444",
  status: "in_review" as const,
  context_mode: "fresh" as const,
  candidates: [
    {
      repository_url: "https://forge.example/team/backend",
      pr_url: "https://forge.example/team/backend/pulls/17",
      branch: "mica/recoverable-handoff",
      commit_sha: "a".repeat(40),
      draft: true as const,
    },
    {
      repository_url: "https://forge.example/team/desktop",
      pr_url: "https://forge.example/team/desktop/pulls/9",
      branch: "mica/desktop-handoff",
      commit_sha: "b".repeat(64),
      draft: true as const,
    },
  ],
  evidence_urls: ["https://ci.example/runs/123"],
  instruction: "Review these exact commits in a fresh context.",
};

function handoffRow(handoff: unknown = request) {
  return {
    id: handoffId,
    issue_id: issueId,
    agent_id: request.agent_id,
    agent_name: "Reviewer",
    instruction: request.instruction,
    kind: "event",
    mode: "once",
    event_types: ["task.completed", "task.failed", "task.cancelled"],
    filter_agent_id: request.agent_id,
    filter_task_id: request.outgoing_task_id,
    interval_seconds: null,
    cron_expression: null,
    timezone: "UTC",
    next_fire_at: null,
    enabled: true,
    disabled_at: null,
    last_task_id: null,
    last_error: null,
    created_at: "2026-09-24T00:00:00Z",
    request_key: request.request_key,
    handoff_completed_at: null,
    handoff,
  };
}

it("creates an idempotent handoff with multiple exact PR candidates", async () => {
  const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(handoffRow())));
  vi.stubGlobal("fetch", fetcher);
  const result = await new ApiClient("https://api.example.test").createIssueHandoff(issueId, request);
  const [url, init] = fetcher.mock.calls[0]!;
  expect(url).toBe(`https://api.example.test/api/issues/${issueId}/handoffs`);
  expect(init.method).toBe("POST");
  expect(JSON.parse(init.body)).toEqual(request);
  expect(result).toMatchObject({ request_key: request.request_key, handoff: { candidates: request.candidates } });
});

it("lists handoffs and preserves a valid empty response", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]")));
  await expect(new ApiClient("https://api.example.test").listIssueHandoffs(issueId)).resolves.toEqual([]);
});

it("rejects malformed handoff rows instead of treating them as an empty list", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify([{ id: handoffId }]))));
  await expect(new ApiClient("https://api.example.test").listIssueHandoffs(issueId)).rejects.toThrow(
    "Could not load issue handoffs",
  );
});

it("uses the native wakeup disable action to cancel a durable handoff", async () => {
  const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...handoffRow(), enabled: false })));
  vi.stubGlobal("fetch", fetcher);
  const result = await new ApiClient("https://api.example.test").cancelIssueHandoff(issueId, handoffId);
  const [url, init] = fetcher.mock.calls[0]!;
  expect(url).toBe(`https://api.example.test/api/issues/${issueId}/wakeups/${handoffId}/disable`);
  expect(init.method).toBe("POST");
  expect(result.enabled).toBe(false);
});

it("requires the exact task reference for resume context", () => {
  expect(() => IssueHandoffRequestSchema.parse({
    ...request,
    context_mode: "resume",
    resume_task_id: undefined,
  })).toThrow();
});

it("accepts canonical human handoffs and requires their review/fresh-context semantics", async () => {
  const memberRequest = {
    request_key: request.request_key,
    outgoing_task_id: request.outgoing_task_id,
    assignee_type: "member" as const,
    assignee_id: "77777777-7777-4777-8777-777777777777",
    status: "in_review" as const,
    context_mode: "fresh" as const,
    candidates: [],
    evidence_urls: [],
    instruction: "Please review the completed work.",
  };
  expect(IssueHandoffRequestSchema.parse(memberRequest)).toEqual(memberRequest);
  expect(() => IssueHandoffRequestSchema.parse({ ...memberRequest, status: "in_progress" })).toThrow();
  expect(() => IssueHandoffRequestSchema.parse({ ...memberRequest, context_mode: "resume", resume_task_id: handoffId })).toThrow();

  const completed = {
    ...handoffRow({
    ...memberRequest,
    agent_id: "",
    expected_status: "in_progress",
    expected_assignee_type: "agent",
    expected_assignee_id: "88888888-8888-4888-8888-888888888888",
    }),
    handoff_completed_at: "2026-09-24T00:00:00Z",
  };
  const parsed = IssueHandoffSchema.parse(completed);
  expect(parsed.handoff.assignee_type).toBe("member");
  expect(parsed.handoff_completed_at).toBe("2026-09-24T00:00:00Z");
  expect(parsed.last_task_id).toBeNull();
});

it("does not send a resume request without its exact source task", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  await expect(
    new ApiClient("https://api.example.test").createIssueHandoff(issueId, {
      ...request,
      context_mode: "resume",
    }),
  ).rejects.toThrow();
  expect(fetcher).not.toHaveBeenCalled();
});

it("preserves future stored status and context values without authorizing them", () => {
  const futureHandoff = { ...request, status: "future_review", context_mode: "future_context" };
  const parsed = IssueHandoffSchema.parse(handoffRow(futureHandoff));
  expect(parsed.handoff.status).toBe("future_review");
  expect(parsed.handoff.context_mode).toBe("future_context");
});
