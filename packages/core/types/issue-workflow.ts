/** Immutable work candidate submitted for independent review and acceptance. */
export interface IssueWorkflowPullRequest {
  repository_url: string;
  pr_url: string;
  branch: string;
  commit_sha: string;
  draft: boolean;
}

export interface IssueWorkflowCandidate {
  id: string;
  digest: string;
  scope_digest: string;
  writer_task_id: string;
  prs: IssueWorkflowPullRequest[];
  created_at: string;
}

/** Review verdicts and delivery states remain open strings for forward compatibility. */
export interface IssueWorkflowReview {
  id: string;
  verdict: string;
  reviewer_task_id: string;
  pr_review_urls: string[];
  submitted_at: string;
}

export interface IssueWorkflowAcceptance {
  id: string;
  candidate_id: string;
  state: string;
  mode: string;
  classification_reason?: string;
  requested_at: string;
  accepted_at: string | null;
  blocker?: string;
  hold_delivery?: boolean;
  outcome_complete?: boolean;
  outcome_completed_at?: string | null;
  outcome_pending?: boolean;
  outcome_task_id?: string;
  /** True only while the bound outcome run can still advance. */
  outcome_task_active?: boolean;
}

export interface IssueWorkflowDelivery {
  id: string;
  ordinal: number;
  pr_url: string;
  expected_head_sha: string;
  action: string;
  merge_method?: string;
  status: string;
  attempt_count: number;
  next_attempt_at: string;
  last_error_class?: string;
  readiness_done_at: string | null;
  merged_at: string | null;
  /** The current viewer may retry this blocked intent without changing its approved PR head or action. */
  retryable?: boolean;
}

/** Server-provided destinations for retaining writer context after rejection. */
export interface IssueWorkflowRetainedContextOption {
  task_id: string;
  agent_id: string;
  agent_name: string;
  kind: string;
}

export interface IssueWorkflowAvailableActions {
  accept_human: boolean;
  accept_comment?: boolean;
  reject: boolean;
  request_trivial_acceptance: boolean;
  request_reviewed_acceptance?: boolean;
  waive_review: boolean;
  hold_delivery?: boolean;
  release_delivery?: boolean;
  complete_outcome?: boolean;
  retry_outcome?: boolean;
}

export interface IssueWorkflowDeliveryPreview {
  action: string;
  merge_method?: string;
  requires_order: boolean;
}

export interface IssueWorkflowException {
  id: string;
  candidate_id: string;
  scope: string;
  grant_details: Record<string, unknown>;
  actor_type: string;
  actor_id: string;
  reason: string;
  consequences: string;
  base_policy_version: string;
  created_at: string;
  revoked_at: string | null;
  revocation_reason?: string;
  revocation_consequences?: string;
  revoked_by_type?: string;
  revoked_by_id?: string;
}

export interface IssueWorkflowFeedback {
  comment_id: string;
  comment_revision: number;
  content_sha256: string;
  kind: string;
  candidate_id: string;
  source_task_id: string;
  created_at: string;
}

export interface IssueWorkflow {
  issue_id: string;
  issue_revision: number;
  policy_version: string;
  /** Present for workflow policies that separate accepted work from issue completion. */
  accepted_status_key?: string;
  frozen: boolean;
  candidate: IssueWorkflowCandidate | null;
  reviews: IssueWorkflowReview[];
  acceptance: IssueWorkflowAcceptance | null;
  feedback?: IssueWorkflowFeedback;
  acceptance_blockers: string[];
  delivery_preview: IssueWorkflowDeliveryPreview | null;
  reviewed_delivery_preview?: IssueWorkflowDeliveryPreview | null;
  delivery: IssueWorkflowDelivery[];
  exceptions: IssueWorkflowException[];
  retained_context_options: IssueWorkflowRetainedContextOption[];
  available_actions: IssueWorkflowAvailableActions;
}

export interface AcceptIssueWorkflowRequest {
  candidate_id: string;
  expected_revision: number;
  acceptance_mode?: "reviewed";
  classification_reason?: string;
  merge_order_pr_urls?: string[];
  outcome_complete?: boolean;
  hold_delivery?: boolean;
}

export interface UpdateIssueWorkflowAcceptanceRequest {
  candidate_id: string;
  expected_revision: number;
  reason: string;
}

export type IssueWorkflowRejectionKind = "in_scope_defect" | "scope_change";

export interface RejectIssueWorkflowRequest {
  candidate_id: string;
  expected_revision: number;
  kind: IssueWorkflowRejectionKind;
  reason: string;
  resume_task_id?: string;
}

export interface RevokeIssueWorkflowExceptionRequest {
  expected_revision: number;
  reason: string;
  consequences: string;
}

/** Retries one accepted delivery intent against the displayed candidate. */
export interface RetryIssueWorkflowDeliveryRequest {
  candidate_id: string;
  expected_revision: number;
  reason?: string;
}
