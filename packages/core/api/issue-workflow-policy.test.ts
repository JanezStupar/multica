// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

const hash = `sha256:${"a".repeat(64)}`;
const skillID = "11111111-1111-4111-8111-111111111111";
const policy = {
  format_version: 1,
  scope: "issue_workflow_bundle",
  coverage: {
    workflow_bundle_pinned: true,
    agent_instructions_pinned: false,
    model_settings_pinned: false,
  },
  version: hash,
  source_skill_id: skillID,
  bundle: {
    id: skillID,
    source: "workspace",
    replaces_builtin: "builtin:multica-platform",
    name: "ticket-policy",
    hash,
    size_bytes: 15,
    content: "Policy body",
    files: [{
      path: "runtime/issue-workflow.md", content: "Run policy",
      sha256: hash, size_bytes: 10,
    }],
  },
};

it("reads and enrolls the exact versioned issue policy", async () => {
  const mocked = vi.fn().mockResolvedValue(new Response(JSON.stringify(policy), { status: 201 }));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");
  await expect(client.enrollIssueWorkflowPolicy("MUL-1", skillID)).resolves.toEqual(policy);
  expect(mocked).toHaveBeenCalledWith(
    "https://api.example.test/api/issues/MUL-1/workflow-policy",
    expect.objectContaining({ method: "POST", body: JSON.stringify({ skill_id: skillID }) }),
  );
  mocked.mockResolvedValue(new Response(JSON.stringify(policy)));
  await expect(client.getIssueWorkflowPolicy("MUL-1")).resolves.toEqual(policy);
});

it("rejects a malformed policy readback or enrollment response", async () => {
  const mocked = vi.fn();
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");
  for (const malformed of [
    { ...policy, version: "not-a-digest" },
    { ...policy, source_skill_id: "22222222-2222-4222-8222-222222222222" },
    { ...policy, bundle: { ...policy.bundle, files: "missing" } },
    { ...policy, format_version: 2 },
  ]) {
    mocked.mockResolvedValueOnce(new Response(JSON.stringify(malformed)));
    await expect(client.getIssueWorkflowPolicy("MUL-1")).rejects.toThrow("Could not load issue workflow policy");
    mocked.mockResolvedValueOnce(new Response(JSON.stringify(malformed), { status: 201 }));
    await expect(client.enrollIssueWorkflowPolicy("MUL-1", skillID)).rejects.toThrow("Could not enroll issue workflow policy");
  }
});

it("includes an explicit reopen target in legacy issue migration", async () => {
  const mocked = vi.fn().mockResolvedValue(new Response(JSON.stringify(policy)));
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");
  await expect(client.migrateIssueWorkflow("MUL-2", {
    skill_id: skillID,
    reason: "historical issue is being resumed",
    reconciliation: "remaining work and prior evidence reviewed",
    reopen_to: "in_progress",
  })).resolves.toEqual(policy);
  expect(mocked).toHaveBeenCalledWith(
    "https://api.example.test/api/issues/MUL-2/workflow-migrate",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({
        skill_id: skillID,
        reason: "historical issue is being resumed",
        reconciliation: "remaining work and prior evidence reviewed",
        reopen_to: "in_progress",
      }),
    }),
  );
});

it("rejects an empty explicit reopen target before sending a migration", async () => {
  const mocked = vi.fn();
  vi.stubGlobal("fetch", mocked);
  const client = new ApiClient("https://api.example.test");
  await expect(client.migrateIssueWorkflow("MUL-2", {
    skill_id: skillID,
    reason: "reason",
    reconciliation: "reconciled",
    reopen_to: "   ",
  })).rejects.toThrow();
  expect(mocked).not.toHaveBeenCalled();
});
