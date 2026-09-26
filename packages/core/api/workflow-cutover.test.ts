// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

const workspaceID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const issueID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
const skillID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";
const hash = `sha256:${"a".repeat(64)}`;
const policy = {
  format_version: 1,
  scope: "issue_workflow_bundle",
  coverage: { workflow_bundle_pinned: true, agent_instructions_pinned: false, model_settings_pinned: false },
  version: hash,
  source_skill_id: skillID,
  bundle: {
    id: skillID, source: "workspace", replaces_builtin: "builtin:multica-platform",
    name: "workflow", hash, size_bytes: 1, content: "x", files: [],
  },
};
const issueWorkflow = {
  issue_id: issueID,
  issue_revision: 8,
  frozen: false,
  policy_version: hash,
  candidate: {
    id: "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
    digest: hash,
    scope_digest: hash,
    writer_task_id: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
    prs: [{ repository_url: "https://github.com/acme/app", pr_url: "https://github.com/acme/app/pull/8", branch: "feature", commit_sha: "b".repeat(40), draft: true }],
    created_at: "2026-09-24T10:00:00Z",
  },
  reviews: [{ id: "review-1", verdict: "future_verdict", reviewer_task_id: "review-task", pr_review_urls: [], submitted_at: "2026-09-24T10:05:00Z" }],
  acceptance: null,
  acceptance_blockers: ["independent_review_pending"],
  delivery_preview: { action: "merge", merge_method: "squash", requires_order: false },
  delivery: [{ id: "delivery-1", ordinal: 0, pr_url: "https://github.com/acme/app/pull/8", expected_head_sha: "b".repeat(40), action: "ready", status: "future_state", attempt_count: 0, next_attempt_at: "2026-09-24T10:00:00Z", readiness_done_at: null, merged_at: null, retryable: false }],
  exceptions: [{ id: "exception-1", candidate_id: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", scope: "review", grant_details: { depth: "targeted" }, actor_type: "member", actor_id: "user-1", reason: "narrow change", consequences: "one review waived", base_policy_version: hash, created_at: "2026-09-24T10:00:00Z", revoked_at: null }],
  retained_context_options: [{ task_id: "ffffffff-ffff-4fff-8fff-ffffffffffff", agent_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", agent_name: "Mica", kind: "writer" }],
  available_actions: { accept_human: true, reject: true, request_trivial_acceptance: false, waive_review: false },
};

it("calls workspace cutover/default and issue migration endpoints with their exact contracts", async () => {
  const mocked = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ policy: null, cutover_at: null })))
    .mockResolvedValueOnce(new Response(JSON.stringify({ policy, frozen_issues: 7 }), { status: 201 }))
    .mockResolvedValueOnce(new Response(JSON.stringify(policy)))
    .mockResolvedValueOnce(new Response(JSON.stringify(policy)));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");

  await expect(client.getWorkspaceWorkflowDefault(workspaceID)).resolves.toEqual({ policy: null, cutover_at: null });
  await expect(client.cutoverWorkspaceWorkflow(workspaceID, skillID)).resolves.toEqual({ policy, frozen_issues: 7 });
  await expect(client.setWorkspaceWorkflowDefault(workspaceID, skillID)).resolves.toEqual(policy);
  await expect(client.migrateIssueWorkflow(issueID, {
    skill_id: skillID, reason: "  old ticket  ", reconciliation: "  reviewed remaining work  ",
  })).resolves.toEqual(policy);

  expect(mocked.mock.calls.map(([url]) => url)).toEqual([
    `https://api.example.test/api/workspaces/${workspaceID}/workflow-default`,
    `https://api.example.test/api/workspaces/${workspaceID}/workflow-cutover`,
    `https://api.example.test/api/workspaces/${workspaceID}/workflow-default`,
    `https://api.example.test/api/issues/${issueID}/workflow-migrate`,
  ]);
  expect(mocked.mock.calls[1]?.[1]).toEqual(expect.objectContaining({ method: "POST", body: JSON.stringify({ skill_id: skillID }) }));
  expect(mocked.mock.calls[2]?.[1]).toEqual(expect.objectContaining({ method: "PUT", body: JSON.stringify({ skill_id: skillID }) }));
  expect(mocked.mock.calls[3]?.[1]).toEqual(expect.objectContaining({
    method: "POST",
    body: JSON.stringify({ skill_id: skillID, reason: "old ticket", reconciliation: "reviewed remaining work" }),
  }));
});

it("accepts an idempotent cutover readback and rejects malformed control responses", async () => {
  const mocked = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ policy, cutover_at: "2026-09-24T10:00:00Z" })))
    .mockResolvedValueOnce(new Response(JSON.stringify({ policy: { ...policy, version: "bad" }, frozen_issues: -1 })));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");
  await expect(client.cutoverWorkspaceWorkflow(workspaceID, skillID)).resolves.toEqual({
    policy, cutover_at: "2026-09-24T10:00:00Z",
  });
  await expect(client.cutoverWorkspaceWorkflow(workspaceID, skillID)).rejects.toThrow("Could not cut over workspace workflow");
});

it("validates migration reconciliation before sending a request", async () => {
  const mocked = vi.fn();
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");
  await expect(client.migrateIssueWorkflow(issueID, {
    skill_id: skillID, reason: " ", reconciliation: "present",
  })).rejects.toThrow();
  expect(mocked).not.toHaveBeenCalled();
});

it("reads progression state and sends revision-bound accept/reject decisions", async () => {
  const mocked = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)))
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)))
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)))
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");

  await expect(client.getIssueWorkflow(issueID)).resolves.toEqual(issueWorkflow);
  await expect(client.acceptIssueWorkflow(issueID, {
    candidate_id: issueWorkflow.candidate.id, expected_revision: issueWorkflow.issue_revision,
  })).resolves.toEqual(issueWorkflow);
  await expect(client.rejectIssueWorkflow(issueID, {
    candidate_id: issueWorkflow.candidate.id, expected_revision: issueWorkflow.issue_revision,
    kind: "in_scope_defect", reason: "Fix the failing test", resume_task_id: "ffffffff-ffff-4fff-8fff-ffffffffffff",
  })).resolves.toEqual(issueWorkflow);
  await expect(client.revokeIssueWorkflowException(issueID, "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", {
    expected_revision: 8, reason: "Authority changed", consequences: "Normal review applies again",
  })).resolves.toEqual(issueWorkflow);

  expect(mocked.mock.calls.map(([url]) => url)).toEqual([
    `https://api.example.test/api/issues/${issueID}/workflow`,
    `https://api.example.test/api/issues/${issueID}/workflow/acceptances`,
    `https://api.example.test/api/issues/${issueID}/workflow/rejections`,
    `https://api.example.test/api/issues/${issueID}/workflow/exceptions/eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee/revoke`,
  ]);
  expect(mocked.mock.calls[1]?.[1]).toEqual(expect.objectContaining({
    method: "POST",
    body: JSON.stringify({ candidate_id: issueWorkflow.candidate.id, expected_revision: 8 }),
  }));
  expect(mocked.mock.calls[2]?.[1]).toEqual(expect.objectContaining({
    method: "POST",
    body: JSON.stringify({ candidate_id: issueWorkflow.candidate.id, expected_revision: 8,
      kind: "in_scope_defect", reason: "Fix the failing test", resume_task_id: "ffffffff-ffff-4fff-8fff-ffffffffffff" }),
  }));
  expect(mocked.mock.calls[3]?.[1]).toEqual(expect.objectContaining({
    method: "POST",
    body: JSON.stringify({ expected_revision: 8, reason: "Authority changed", consequences: "Normal review applies again" }),
  }));
});

it("preserves optional workflow feedback while remaining compatible with older and malformed projections", async () => {
  const feedback = {
    comment_id: "comment-7",
    comment_revision: 3,
    content_sha256: "a".repeat(64),
    kind: "in_scope_defect",
    candidate_id: issueWorkflow.candidate.id,
    source_task_id: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
    created_at: "2026-09-24T10:10:00Z",
  };
  const mocked = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ ...issueWorkflow, feedback })))
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)))
    .mockResolvedValueOnce(new Response(JSON.stringify({ ...issueWorkflow, feedback: { ...feedback, comment_revision: "future" } })));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");

  await expect(client.getIssueWorkflow(issueID)).resolves.toEqual(expect.objectContaining({ feedback }));
  await expect(client.getIssueWorkflow(issueID)).resolves.toEqual(expect.not.objectContaining({ feedback: expect.anything() }));
  await expect(client.getIssueWorkflow(issueID)).resolves.toEqual(expect.not.objectContaining({ feedback: expect.anything() }));
});

it("does not send an unknown rejection kind", async () => {
  const mocked = vi.fn();
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");
  await expect(client.rejectIssueWorkflow(issueID, {
    candidate_id: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", expected_revision: 8,
    kind: "future_kind" as "in_scope_defect", reason: "reason",
  })).rejects.toThrow();
  expect(mocked).not.toHaveBeenCalled();
});

it("retries only the displayed delivery candidate and rejects malformed retry readback", async () => {
  const deliveryID = "ffffffff-ffff-4fff-8fff-ffffffffffff";
  const mocked = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)))
    .mockResolvedValueOnce(new Response(JSON.stringify({ ...issueWorkflow, issue_revision: "invalid" })));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");
  const input = { candidate_id: issueWorkflow.candidate.id, expected_revision: issueWorkflow.issue_revision };

  await expect(client.retryIssueWorkflowDelivery(issueID, deliveryID, input)).resolves.toEqual(issueWorkflow);
  expect(mocked.mock.calls[0]?.[0]).toBe(
    `https://api.example.test/api/issues/${issueID}/workflow/delivery/${deliveryID}/retry`,
  );
  expect(mocked.mock.calls[0]?.[1]).toEqual(expect.objectContaining({ method: "POST", body: JSON.stringify(input) }));
  await expect(client.retryIssueWorkflowDelivery(issueID, deliveryID, input)).rejects.toThrow("Could not retry issue workflow delivery");
  await expect(client.retryIssueWorkflowDelivery(issueID, deliveryID, {
    candidate_id: "stale", expected_revision: 0,
  })).rejects.toThrow();
  expect(mocked).toHaveBeenCalledTimes(2);
});

it("reads format-2 acceptance state and sends explicit outcome and hold choices", async () => {
  const state = {
    ...issueWorkflow,
    accepted_status_key: "pr_ready",
    acceptance: {
      id: "accept-1", candidate_id: issueWorkflow.candidate.id, state: "accepted", mode: "human",
      requested_at: "2026-09-24T10:05:00Z", accepted_at: "2026-09-24T10:06:00Z",
      hold_delivery: true, outcome_complete: false, outcome_completed_at: null, outcome_pending: true, outcome_task_id: "task-outcome", outcome_task_active: true,
    },
    available_actions: { ...issueWorkflow.available_actions, hold_delivery: false, release_delivery: true, complete_outcome: true },
  };
  const mocked = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify(state)))
    .mockResolvedValueOnce(new Response(JSON.stringify(state)));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");

  await expect(client.getIssueWorkflow(issueID)).resolves.toEqual(state);
  await expect(client.acceptIssueWorkflow(issueID, {
    candidate_id: issueWorkflow.candidate.id, expected_revision: 8, outcome_complete: false, hold_delivery: true,
  })).resolves.toEqual(state);
  expect(mocked.mock.calls[1]?.[1]).toEqual(expect.objectContaining({ method: "POST", body: JSON.stringify({
    candidate_id: issueWorkflow.candidate.id, expected_revision: 8, outcome_complete: false, hold_delivery: true,
  }) }));
});

it("rejects a malformed completion capability instead of treating it as a legacy response", async () => {
  const mocked = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...issueWorkflow, accepted_status_key: 12 })));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");
  await expect(client.getIssueWorkflow(issueID)).rejects.toThrow("Could not load issue workflow");
});

it("binds hold, release, completion, and outcome retry to an acceptance and validates their reason", async () => {
  const acceptanceID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc";
  const input = { candidate_id: issueWorkflow.candidate.id, expected_revision: 8, reason: "Delivery is ready" };
  const mocked = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)))
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)))
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)))
    .mockResolvedValueOnce(new Response(JSON.stringify(issueWorkflow)))
    .mockResolvedValueOnce(new Response(JSON.stringify({ ...issueWorkflow, issue_revision: "bad" })));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");

  for (const action of ["hold", "release", "complete", "retry-outcome"] as const) {
    await expect(client.updateIssueWorkflowAcceptance(issueID, acceptanceID, action, input)).resolves.toEqual(issueWorkflow);
  }
  expect(mocked.mock.calls.slice(0, 4).map(([url]) => url)).toEqual(["hold", "release", "complete", "retry-outcome"].map(
    (action) => `https://api.example.test/api/issues/${issueID}/workflow/acceptances/${acceptanceID}/${action}`,
  ));
  expect(mocked.mock.calls.slice(0, 4).map(([, init]) => init)).toEqual(Array(4).fill(expect.objectContaining({ method: "POST", body: JSON.stringify(input) })));
  await expect(client.updateIssueWorkflowAcceptance(issueID, acceptanceID, "hold", input)).rejects.toThrow("Could not hold issue workflow acceptance");
  await expect(client.updateIssueWorkflowAcceptance(issueID, acceptanceID, "release", { ...input, reason: " " })).rejects.toThrow();
  expect(mocked).toHaveBeenCalledTimes(5);
});

it("defaults a missing delivery retry affordance to unavailable", async () => {
  const legacy = {
    ...issueWorkflow,
    delivery: issueWorkflow.delivery.map(({ retryable: _retryable, ...row }) => row),
  };
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(legacy))));
  const state = await new ApiClient("https://api.example.test").getIssueWorkflow(issueID);
  expect(state.delivery[0]?.retryable).toBe(false);
});
