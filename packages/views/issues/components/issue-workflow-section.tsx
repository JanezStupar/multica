"use client";

import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { AlertTriangle, Check, ExternalLink, GitPullRequest, Loader2, ShieldAlert } from "lucide-react";
import type { AcceptIssueWorkflowRequest, IssueWorkflow, IssueWorkflowPullRequest, IssueWorkflowRetainedContextOption } from "@multica/core/types";
import { errorCode } from "@multica/core/api";
import {
  useAcceptIssueWorkflow,
  useIssueWorkflow,
  useRejectIssueWorkflow,
  useRetryIssueWorkflowDelivery,
  useUpdateIssueWorkflowAcceptance,
} from "@multica/core/issues/workflow";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { useT } from "../../i18n";

interface IssueWorkflowSectionProps {
  workspaceId: string;
  issueId: string;
  enabled: boolean;
  frozen?: boolean;
  focusAccept?: boolean;
  acceptAction?: {
    mutate: (input: AcceptIssueWorkflowRequest) => void;
    isPending: boolean;
    error: unknown;
  };
}

function isConflict(error: unknown): boolean {
  return errorCode(error) === "workflow_authority_conflict" || errorCode(error) === "revision_conflict";
}

function deliveryStatusKey(status: string):
  | "delivery_pending"
  | "delivery_delivered"
  | "delivery_stale"
  | "delivery_blocked"
  | "delivery_cancelled"
  | "delivery_unknown" {
  switch (status) {
    case "delivered":
      return "delivery_delivered";
    case "stale":
      return "delivery_stale";
    case "blocked":
      return "delivery_blocked";
    case "cancelled":
      return "delivery_cancelled";
    case "pending":
    case "retry":
      return "delivery_pending";
    default:
      return "delivery_unknown";
  }
}

function deliveryActionKey(action: string): "delivery_action_ready" | "delivery_action_merge" | "delivery_action_unknown" {
  if (action === "ready") return "delivery_action_ready";
  if (action === "merge") return "delivery_action_merge";
  return "delivery_action_unknown";
}

function mergeMethodKey(method: string): "merge_method_merge" | "merge_method_squash" | "merge_method_rebase" | "merge_method_default" {
  switch (method) {
    case "merge": return "merge_method_merge";
    case "squash": return "merge_method_squash";
    case "rebase": return "merge_method_rebase";
    default: return "merge_method_default";
  }
}

function blockerKey(code: string):
  | "blocker_candidate_missing"
  | "blocker_scope_changed"
  | "blocker_candidate_head_changed"
  | "blocker_source_incomplete"
  | "blocker_review_missing"
  | "blocker_review_not_independent"
  | "blocker_active_work"
  | "blocker_pending_handoff"
  | "blocker_provider_binding_missing"
  | "blocker_merge_not_granted"
  | "blocker_human_recipient_required"
  | "blocker_terminal_status"
  | "blocker_frozen"
  | "blocker_merge_order_required"
  | "blocker_acceptance_pending"
  | "blocker_human_comment_pending"
  | "blocker_already_accepted"
  | "outcome_run_not_queued"
  | "outcome_run_stopped"
  | "completion_status_retry"
  | "blocker_unknown" {
  switch (code) {
    case "candidate_missing": return "blocker_candidate_missing";
    case "scope_changed": return "blocker_scope_changed";
    case "candidate_head_changed": return "blocker_candidate_head_changed";
    case "source_incomplete": return "blocker_source_incomplete";
    case "review_missing": return "blocker_review_missing";
    case "review_not_independent": return "blocker_review_not_independent";
    case "active_work": return "blocker_active_work";
    case "pending_handoff": return "blocker_pending_handoff";
    case "provider_binding_missing": return "blocker_provider_binding_missing";
    case "merge_not_granted": return "blocker_merge_not_granted";
    case "human_recipient_required": return "blocker_human_recipient_required";
    case "terminal_status": return "blocker_terminal_status";
    case "frozen": return "blocker_frozen";
    case "merge_order_required": return "blocker_merge_order_required";
    case "acceptance_pending": return "blocker_acceptance_pending";
    case "human_comment_pending":
    case "human_feedback_pending": return "blocker_human_comment_pending";
    case "already_accepted": return "blocker_already_accepted";
    case "outcome_dispatch_failed": return "outcome_run_not_queued";
    case "outcome_task_failed": return "outcome_run_stopped";
    case "completion_reconcile_failed": return "completion_status_retry";
    default: return "blocker_unknown";
  }
}

function retainedOptionLabel(
  option: IssueWorkflowRetainedContextOption,
  index: number,
  sameKindCount: number,
  t: ReturnType<typeof useT<"issues">>["t"],
): string {
  const kind = option.kind === "review_fix"
    ? t(($) => $.detail.workflow.context_review_fix)
    : option.kind === "implementation" || option.kind === "writer"
      ? t(($) => $.detail.workflow.context_implementation)
      : t(($) => $.detail.workflow.context_previous);
  const agent = option.agent_name;
  const ordinal = sameKindCount > 1 ? ` · ${index + 1}` : "";
  return `${kind} · ${agent || t(($) => $.detail.workflow.context_agent)}${ordinal}`;
}

function WorkflowDeliveryRow({
  delivery,
  retryPending,
  canRetry,
  onRetry,
}: {
  delivery: IssueWorkflow["delivery"][number];
  retryPending: boolean;
  canRetry: boolean;
  onRetry: () => void;
}) {
  const { t } = useT("issues");
  return (
    <li className="flex min-w-0 flex-col gap-1 rounded-md border border-border/70 px-2.5 py-2 text-caption">
      <div className="flex min-w-0 items-center gap-2">
        <a
          href={delivery.pr_url}
          target="_blank"
          rel="noreferrer"
          className="flex min-w-0 items-center gap-1.5 text-foreground hover:underline"
        >
          <GitPullRequest className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{delivery.pr_url}</span>
          <ExternalLink className="size-3 shrink-0 text-muted-foreground" />
        </a>
        <span className="ml-auto shrink-0 text-muted-foreground">
          {t(($) => $.detail.workflow[deliveryActionKey(delivery.action)])}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-muted-foreground">
        <span>{t(($) => $.detail.workflow[deliveryStatusKey(delivery.status)])}</span>
        <code className="font-mono">{delivery.expected_head_sha}</code>
        {delivery.last_error_class && delivery.status !== "delivered" ? (
          <span className={delivery.last_error_class === "human_feedback_pending" ? undefined : "text-destructive"}>
            {delivery.last_error_class === "human_feedback_pending"
              ? t(($) => $.detail.workflow.blocker_human_comment_pending)
              : t(($) => $.detail.workflow.delivery_error, { reason: delivery.last_error_class })}
          </span>
        ) : null}
      </div>
      {canRetry && delivery.status === "blocked" && delivery.retryable === true ? (
        <div>
          <Button type="button" variant="outline" size="xs" disabled={retryPending} aria-busy={retryPending} onClick={onRetry}>
            {retryPending ? <Loader2 className="size-3.5 animate-spin" aria-hidden="true" /> : null}
            {t(($) => $.detail.workflow.retry_delivery, { number: delivery.ordinal })}
          </Button>
        </div>
      ) : null}
    </li>
  );
}

function CandidatePullRequest({
  pr,
  index,
}: {
  pr: IssueWorkflowPullRequest;
  index: number;
}) {
  const { t } = useT("issues");
  return (
    <li className="min-w-0 rounded-md border border-border/70 px-2.5 py-2 text-caption">
      <div className="flex min-w-0 items-center gap-2">
        <span className="shrink-0 text-muted-foreground">{index + 1}.</span>
        <a
          href={pr.pr_url}
          target="_blank"
          rel="noreferrer"
          className="flex min-w-0 items-center gap-1.5 text-foreground hover:underline"
        >
          <GitPullRequest className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="truncate">{pr.repository_url}</span>
          <ExternalLink className="size-3 shrink-0 text-muted-foreground" />
        </a>
      </div>
      <div className="mt-1 space-y-1 pl-5 text-muted-foreground">
        <div className="truncate">{pr.branch}</div>
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="shrink-0">{t(($) => $.detail.workflow.commit)}</span>
          <code className="min-w-0 break-all font-mono">{pr.commit_sha}</code>
        </div>
      </div>
    </li>
  );
}

function WorkflowRejectionDialog({
  workflow,
  workspaceId,
  issueId,
}: {
  workflow: IssueWorkflow;
  workspaceId: string;
  issueId: string;
}) {
  const { t } = useT("issues");
  const reject = useRejectIssueWorkflow(workspaceId, issueId);
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"in_scope_defect" | "scope_change">("in_scope_defect");
  const [reason, setReason] = useState("");
  const [resumeTaskId, setResumeTaskId] = useState("");
  const options = workflow.retained_context_options;
  const defaultResumeTaskId = options.find((option) => option.kind === "writer")?.task_id ?? options[0]?.task_id ?? "";
  const optionsByKind = useMemo(() => {
    const counts = new Map<string, number>();
    for (const option of options) counts.set(option.kind, (counts.get(option.kind) ?? 0) + 1);
    const seen = new Map<string, number>();
    return options.map((option) => {
      const index = seen.get(option.kind) ?? 0;
      seen.set(option.kind, index + 1);
      return {
        option,
        label: retainedOptionLabel(option, index, counts.get(option.kind) ?? 1, t),
      };
    });
  }, [options, t]);
  const error = reject.error;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!workflow.candidate || !reason.trim()) return;
    reject.mutate(
      {
        candidate_id: workflow.candidate.id,
        expected_revision: workflow.issue_revision,
        kind,
        reason: reason.trim(),
        ...(resumeTaskId ? { resume_task_id: resumeTaskId } : {}),
      },
      {
        onSuccess: () => {
          setOpen(false);
          setReason("");
          setResumeTaskId("");
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => {
      setOpen(nextOpen);
      if (nextOpen) setResumeTaskId(defaultResumeTaskId);
    }}>
      <DialogTrigger render={<Button type="button" variant="outline" size="sm" disabled={reject.isPending} />}>
        {t(($) => $.detail.workflow.reject_candidate)}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{t(($) => $.detail.workflow.reject_title)}</DialogTitle>
            <DialogDescription>{t(($) => $.detail.workflow.reject_description)}</DialogDescription>
          </DialogHeader>
          <fieldset className="mt-4 space-y-3" disabled={reject.isPending}>
            <legend className="mb-2 text-caption font-medium">{t(($) => $.detail.workflow.reject_kind_label)}</legend>
            <label className="flex items-start gap-2 text-body">
              <input
                type="radio"
                name={`workflow-rejection-kind-${issueId}`}
                value="in_scope_defect"
                checked={kind === "in_scope_defect"}
                onChange={() => setKind("in_scope_defect")}
              />
              <span>{t(($) => $.detail.workflow.in_scope_defect)}</span>
            </label>
            <label className="flex items-start gap-2 text-body">
              <input
                type="radio"
                name={`workflow-rejection-kind-${issueId}`}
                value="scope_change"
                checked={kind === "scope_change"}
                onChange={() => {
                  setKind("scope_change");
                }}
              />
              <span>{t(($) => $.detail.workflow.scope_change)}</span>
            </label>
            <label className="block space-y-1.5 text-caption font-medium" htmlFor={`workflow-rejection-reason-${issueId}`}>
              {t(($) => $.detail.workflow.reason_label)}
              <Textarea
                id={`workflow-rejection-reason-${issueId}`}
                value={reason}
                onChange={(event) => setReason(event.target.value)}
                rows={3}
                required
              />
            </label>
            {optionsByKind.length > 0 ? (
              <label className="block space-y-1.5 text-caption font-medium" htmlFor={`workflow-resume-context-${issueId}`}>
                {t(($) => $.detail.workflow.resume_context_label)}
                <select
                  id={`workflow-resume-context-${issueId}`}
                  value={resumeTaskId || defaultResumeTaskId}
                  onChange={(event) => setResumeTaskId(event.target.value)}
                  className="h-9 w-full rounded-md border border-input bg-background px-2 text-body text-foreground"
                >
                  {optionsByKind.map(({ option, label }) => (
                    <option key={option.task_id} value={option.task_id}>{label}</option>
                  ))}
                </select>
              </label>
            ) : null}
          </fieldset>
          {error ? (
            <p role="alert" className="mt-3 text-caption text-destructive">
              {isConflict(error)
                ? t(($) => $.detail.workflow.candidate_changed)
                : t(($) => $.detail.workflow.reject_failed)}
            </p>
          ) : null}
          <DialogFooter className="mt-5">
            <DialogClose render={<Button type="button" variant="outline" disabled={reject.isPending} />}>
              {t(($) => $.detail.workflow.cancel)}
            </DialogClose>
            <Button type="submit" variant="destructive" disabled={!reason.trim() || reject.isPending} aria-busy={reject.isPending}>
              {reject.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
              {t(($) => $.detail.workflow.confirm_reject)}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function WorkflowAcceptanceActionDialog({ workflow, workspaceId, issueId, action }: {
  workflow: IssueWorkflow;
  workspaceId: string;
  issueId: string;
  action: "hold" | "release" | "complete" | "retry-outcome";
}) {
  const { t } = useT("issues");
  const mutation = useUpdateIssueWorkflowAcceptance(workspaceId, issueId);
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const label = action === "hold" ? t(($) => $.detail.workflow.hold_delivery) : action === "release" ? t(($) => $.detail.workflow.release_delivery) : action === "retry-outcome" ? t(($) => $.detail.workflow.retry_outcome) : t(($) => $.detail.workflow.complete_outcome);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!workflow.candidate || !workflow.acceptance || workflow.candidate.id !== workflow.acceptance.candidate_id || !reason.trim()) return;
    mutation.mutate({
      acceptanceId: workflow.acceptance.id,
      action,
      input: { candidate_id: workflow.candidate.id, expected_revision: workflow.issue_revision, reason: reason.trim() },
    }, {
      onSuccess: () => { setOpen(false); setReason(""); },
    });
  };
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button type="button" variant="outline" size="xs" disabled={mutation.isPending} />}>
        {label}
      </DialogTrigger>
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{label}</DialogTitle>
            <DialogDescription>{t(($) => $.detail.workflow.acceptance_action_description)}</DialogDescription>
          </DialogHeader>
          <label className="mt-4 block space-y-1.5 text-caption font-medium" htmlFor={`workflow-${action}-reason-${issueId}`}>
            {t(($) => $.detail.workflow.reason_label)}
            <Textarea id={`workflow-${action}-reason-${issueId}`} value={reason} onChange={(event) => setReason(event.target.value)} rows={3} maxLength={500} required />
          </label>
          {mutation.error ? <p role="alert" className="mt-3 text-caption text-destructive">{isConflict(mutation.error) ? t(($) => $.detail.workflow.candidate_changed) : t(($) => $.detail.workflow.acceptance_action_failed)}</p> : null}
          <DialogFooter className="mt-5">
            <DialogClose render={<Button type="button" variant="outline" disabled={mutation.isPending} />}>{t(($) => $.detail.workflow.cancel)}</DialogClose>
            <Button type="submit" disabled={!reason.trim() || mutation.isPending} aria-busy={mutation.isPending}>
              {mutation.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}{label}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function IssueWorkflowSection({ workspaceId, issueId, enabled, frozen: issueFrozen = false, focusAccept = false, acceptAction }: IssueWorkflowSectionProps) {
  const { t } = useT("issues");
  const { data: workflow, isPending, isError, refetch } = useIssueWorkflow(workspaceId, issueId, enabled);
  const localAccept = useAcceptIssueWorkflow(workspaceId, issueId);
  const retryDelivery = useRetryIssueWorkflowDelivery(workspaceId, issueId);
  const accept = acceptAction ?? localAccept;
  const [mergeOrder, setMergeOrder] = useState<{ candidateId: string; ranks: Record<string, string> } | null>(null);
  const [outcomeComplete, setOutcomeComplete] = useState(false);
  const [holdDelivery, setHoldDelivery] = useState(false);
  const acceptButtonRef = useRef<HTMLButtonElement>(null);
  const sectionRef = useRef<HTMLElement>(null);
  const candidate = workflow?.candidate ?? null;
  const frozen = workflow?.frozen ?? false;
  const preview = workflow?.delivery_preview ?? null;
  const hasCompletionContract = !!workflow?.accepted_status_key;
  const requiresMergeOrder = preview?.action === "merge" && preview.requires_order && (candidate?.prs.length ?? 0) > 1;
  const currentRanks = mergeOrder && mergeOrder.candidateId === candidate?.id ? mergeOrder.ranks : {};
  const orderedPRs = candidate
    ? candidate.prs
        .map((pr) => ({ pr, rank: Number(currentRanks[pr.pr_url]) }))
        .filter(({ rank }) => Number.isInteger(rank) && rank > 0)
        .sort((a, b) => a.rank - b.rank)
        .map(({ pr }) => pr.pr_url)
    : [];
  const mergeOrderComplete = !requiresMergeOrder || (
    orderedPRs.length === candidate?.prs.length && new Set(orderedPRs).size === candidate?.prs.length
  );
  const blockingCodes = (workflow?.acceptance_blockers ?? []).filter(
    (code) => !(code === "merge_order_required" && mergeOrderComplete),
  );
  const canAccept = !!candidate && !!workflow?.available_actions.accept_human && !frozen && blockingCodes.length === 0 && mergeOrderComplete;

  useEffect(() => {
    if (!focusAccept || !workflow) return;
    if (canAccept) acceptButtonRef.current?.focus();
    else sectionRef.current?.focus();
  }, [focusAccept, workflow, canAccept]);

  useEffect(() => {
    setMergeOrder(null);
    setOutcomeComplete(false);
    setHoldDelivery(false);
  }, [candidate?.id]);

  if (!enabled && !issueFrozen) return null;
  if (!enabled && issueFrozen) {
    return (
      <section className="rounded-lg border border-border/80 bg-card p-3" aria-labelledby={`issue-workflow-title-${issueId}`}>
        <h3 id={`issue-workflow-title-${issueId}`} className="text-body font-semibold">{t(($) => $.detail.workflow.frozen_label)}</h3>
        <div className="mt-3 flex gap-2 rounded-md bg-muted/60 p-2.5 text-caption" role="status">
          <ShieldAlert className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden="true" />
          <p>{t(($) => $.detail.workflow.frozen_body)}</p>
        </div>
      </section>
    );
  }
  if (isPending) {
    return <div className="rounded-lg border p-3 text-caption text-muted-foreground" aria-live="polite">{t(($) => $.detail.workflow.loading)}</div>;
  }
  if (isError || !workflow) {
    return (
      <section className="rounded-lg border p-3" aria-labelledby={`issue-workflow-title-${issueId}`}>
        <h3 id={`issue-workflow-title-${issueId}`} className="text-body font-medium">{t(($) => $.detail.workflow.section_title)}</h3>
        <p role="alert" className="mt-1 text-caption text-muted-foreground">{t(($) => $.detail.workflow.load_failed)}</p>
        <Button type="button" variant="outline" size="xs" className="mt-2" onClick={() => void refetch()}>
          {t(($) => $.detail.workflow.retry)}
        </Button>
      </section>
    );
  }

  const deliveredCount = workflow.delivery.filter((item) => item.status === "delivered").length;
  const partialDelivery = deliveredCount > 0 && deliveredCount < workflow.delivery.length;
  const activeDeliveryHold = !!workflow.acceptance?.hold_delivery && workflow.delivery.some((item) => item.action === "merge" && !item.merged_at);
  const acceptError = accept.error;

  return (
    <section
      className="rounded-lg border border-border/80 bg-card p-3"
      aria-labelledby={`issue-workflow-title-${issueId}`}
      tabIndex={focusAccept && !canAccept ? -1 : undefined}
      ref={sectionRef}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 id={`issue-workflow-title-${issueId}`} className="text-body font-semibold">{t(($) => $.detail.workflow.section_title)}</h3>
          {candidate ? (
            <p className="mt-0.5 break-all font-mono text-caption text-muted-foreground">
              {t(($) => $.detail.workflow.candidate_digest)} {candidate.digest}
            </p>
          ) : null}
        </div>
        {frozen ? <ShieldAlert aria-label={t(($) => $.detail.workflow.frozen_label)} className="size-4 shrink-0 text-warning" /> : null}
      </div>

      {frozen ? (
        <div className="mt-3 flex gap-2 rounded-md bg-muted/60 p-2.5 text-caption" role="status">
          <ShieldAlert className="mt-0.5 size-4 shrink-0 text-warning" />
          <p>{t(($) => $.detail.workflow.frozen_body)}</p>
        </div>
      ) : null}

      {workflow.feedback ? (
        <p className="mt-3 text-caption text-muted-foreground" role="status">
          {t(($) => $.detail.workflow.feedback_historical)}{" "}
          <a href={`#comment-${workflow.feedback.comment_id}`} className="text-foreground hover:underline">
            {t(($) => $.detail.workflow.feedback_open_comment)}
          </a>
        </p>
      ) : null}

      {!candidate && !frozen ? (
        <p className="mt-3 text-caption text-muted-foreground">{t(($) => $.detail.workflow.no_candidate)}</p>
      ) : null}

      {candidate ? (
        <div className="mt-3 space-y-3">
          <div>
            <h4 className="mb-1.5 text-caption font-medium">{t(($) => $.detail.workflow.pull_requests)}</h4>
            {candidate.prs.length > 0 ? (
              <ul className="space-y-1.5">
                {candidate.prs.map((pr, index) => <CandidatePullRequest key={`${pr.pr_url}-${pr.commit_sha}`} pr={pr} index={index} />)}
              </ul>
            ) : <p className="text-caption text-muted-foreground">{t(($) => $.detail.workflow.no_pull_requests)}</p>}
          </div>

          <div>
            <h4 className="mb-1.5 text-caption font-medium">{t(($) => $.detail.workflow.reviews)}</h4>
            {workflow.reviews.length > 0 ? (
              <ul className="space-y-1.5">
                {workflow.reviews.map((review) => (
                  <li key={review.id} className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-caption">
                    <span className={review.verdict === "pass" ? "text-success" : review.verdict === "changes_requested" ? "text-warning" : "text-muted-foreground"}>
                      {review.verdict === "pass"
                        ? t(($) => $.detail.workflow.review_passed)
                        : review.verdict === "changes_requested"
                          ? t(($) => $.detail.workflow.review_changes_requested)
                          : t(($) => $.detail.workflow.review_pending)}
                    </span>
                    {review.pr_review_urls.map((url, index) => (
                      <a key={`${review.id}-${url}`} href={url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-foreground hover:underline">
                        {t(($) => $.detail.workflow.open_review, { number: index + 1 })}
                        <ExternalLink className="size-3" />
                      </a>
                    ))}
                  </li>
                ))}
              </ul>
            ) : <p className="text-caption text-muted-foreground">{t(($) => $.detail.workflow.no_reviews)}</p>}
          </div>
        </div>
      ) : null}

      {workflow.acceptance_blockers.length > 0 ? (
        <ul className="mt-3 space-y-1 text-caption text-warning" aria-label={t(($) => $.detail.workflow.acceptance_blockers)}>
          {workflow.acceptance_blockers.map((code) => (
            <li key={code} className="flex items-start gap-1.5">
              <AlertTriangle aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
              <span>{t(($) => $.detail.workflow[blockerKey(code)])}</span>
            </li>
          ))}
        </ul>
      ) : null}

      {candidate ? (
        <p className="mt-3 text-caption text-muted-foreground">
          {hasCompletionContract && holdDelivery && !workflow.acceptance
            ? t(($) => $.detail.workflow.preview_hold)
            : preview?.action === "ready"
            ? t(($) => $.detail.workflow.preview_ready)
            : preview?.action === "merge"
              ? t(($) => $.detail.workflow.preview_merge, { method: t(($) => $.detail.workflow[mergeMethodKey(preview.merge_method ?? "")]) })
              : candidate.prs.length === 0
                ? t(($) => $.detail.workflow[hasCompletionContract ? "accept_action_no_pr_format2" : "accept_action_no_pr"])
                : t(($) => $.detail.workflow.preview_unavailable)}
        </p>
      ) : null}

      {workflow.acceptance ? (
        <div className="mt-3 flex items-start gap-2 text-caption" role="status">
          {workflow.acceptance.state === "accepted" ? <Check className="mt-0.5 size-3.5 shrink-0 text-success" /> : <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />}
          <p>
            {workflow.acceptance.state === "accepted"
              ? t(($) => $.detail.workflow.accepted)
              : workflow.acceptance.state === "requested"
                ? t(($) => $.detail.workflow.acceptance_pending)
                : workflow.acceptance.state === "revoked"
                  ? t(($) => $.detail.workflow.acceptance_revoked)
                  : t(($) => $.detail.workflow.acceptance_blocked)}
            {workflow.acceptance.classification_reason ? ` · ${workflow.acceptance.classification_reason}` : ""}
            {workflow.acceptance.blocker && workflow.acceptance.blocker !== "outcome_task_failed" && workflow.acceptance.blocker !== "outcome_dispatch_failed" && workflow.acceptance.blocker !== "completion_reconcile_failed" ? ` · ${t(($) => $.detail.workflow[blockerKey(workflow.acceptance!.blocker!)])}` : ""}
          </p>
        </div>
      ) : null}

      {hasCompletionContract && workflow.acceptance?.state === "accepted" ? (
        <div className="mt-2 space-y-1 text-caption" role="status">
          <p>{workflow.acceptance.outcome_complete
            ? t(($) => $.detail.workflow.outcome_complete_status)
            : workflow.acceptance.outcome_task_active
              ? t(($) => $.detail.workflow.outcome_run_active)
              : workflow.acceptance.outcome_pending
              ? t(($) => $.detail.workflow.outcome_confirmation_pending)
              : t(($) => $.detail.workflow.outcome_incomplete_status)}</p>
          {workflow.acceptance.outcome_task_active && workflow.acceptance.outcome_pending ? <p>{t(($) => $.detail.workflow.outcome_confirmation_pending)}</p> : null}
          {workflow.acceptance.blocker === "outcome_task_failed" ? <p className="text-warning">{t(($) => $.detail.workflow.outcome_run_stopped)}</p> : null}
          {workflow.acceptance.blocker === "outcome_dispatch_failed" ? <p className="text-warning">{t(($) => $.detail.workflow.outcome_run_not_queued)}</p> : null}
          {workflow.acceptance.blocker === "completion_reconcile_failed" ? <p className="text-warning">{t(($) => $.detail.workflow.completion_status_retry)}</p> : null}
          {activeDeliveryHold ? <p className="text-warning">{t(($) => $.detail.workflow.delivery_held_status)}</p> : null}
          {workflow.acceptance.outcome_task_id ? <p>{t(($) => $.detail.workflow.outcome_task_status)} <code className="break-all font-mono">{workflow.acceptance.outcome_task_id}</code></p> : null}
        </div>
      ) : null}

      {hasCompletionContract && workflow.acceptance?.state === "accepted" && candidate?.id === workflow.acceptance.candidate_id && !frozen ? (
        <div className="mt-3 flex flex-wrap gap-2">
          {workflow.available_actions.hold_delivery && !workflow.acceptance.hold_delivery ? <WorkflowAcceptanceActionDialog workflow={workflow} workspaceId={workspaceId} issueId={issueId} action="hold" /> : null}
          {workflow.available_actions.release_delivery && activeDeliveryHold ? <WorkflowAcceptanceActionDialog workflow={workflow} workspaceId={workspaceId} issueId={issueId} action="release" /> : null}
          {workflow.available_actions.complete_outcome && !workflow.acceptance.outcome_complete && !workflow.acceptance.outcome_pending ? <WorkflowAcceptanceActionDialog workflow={workflow} workspaceId={workspaceId} issueId={issueId} action="complete" /> : null}
          {workflow.available_actions.retry_outcome && !workflow.acceptance.outcome_task_active && !workflow.acceptance.outcome_complete ? <WorkflowAcceptanceActionDialog workflow={workflow} workspaceId={workspaceId} issueId={issueId} action="retry-outcome" /> : null}
        </div>
      ) : null}

      {workflow.delivery.length > 0 ? (
        <div className="mt-3 space-y-1.5">
          <div className="flex items-center gap-2">
            <h4 className="text-caption font-medium">{t(($) => $.detail.workflow.delivery)}</h4>
            {partialDelivery ? <span className="rounded bg-warning/15 px-1.5 py-0.5 text-caption text-warning">{t(($) => $.detail.workflow.partial_delivery, { delivered: deliveredCount, total: workflow.delivery.length })}</span> : null}
          </div>
          <ul className="space-y-1.5">
            {workflow.delivery.map((item) => (
              <WorkflowDeliveryRow
                key={item.id}
                delivery={item}
                retryPending={retryDelivery.isPending}
                canRetry={!frozen && !!candidate && !(activeDeliveryHold && item.action === "merge")}
                onRetry={() => {
                  if (frozen || !candidate || (activeDeliveryHold && item.action === "merge") || item.retryable !== true || item.status !== "blocked") return;
                  retryDelivery.mutate({
                    deliveryId: item.id,
                    input: { candidate_id: candidate.id, expected_revision: workflow.issue_revision },
                  });
                }}
              />
            ))}
          </ul>
          {retryDelivery.error ? (
            <p role="alert" className="text-caption text-destructive">{t(($) => $.detail.workflow.retry_delivery_failed)}</p>
          ) : null}
        </div>
      ) : null}

      {canAccept || (candidate && workflow.available_actions.accept_human && !frozen && requiresMergeOrder) || workflow.available_actions.reject ? (
        <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-border/70 pt-3">
          {candidate && requiresMergeOrder && workflow.available_actions.accept_human && !frozen ? (
            <fieldset className="w-full space-y-2" aria-describedby={`workflow-order-help-${issueId}`}>
              <legend className="text-caption font-medium">{t(($) => $.detail.workflow.merge_order_title)}</legend>
              <p id={`workflow-order-help-${issueId}`} className="text-caption text-muted-foreground">
                {t(($) => $.detail.workflow.merge_order_help)}
              </p>
              <div className="grid gap-2 sm:grid-cols-2">
                {candidate.prs.map((pr, index) => {
                  const rank = currentRanks[pr.pr_url] ?? "";
                  return (
                    <label key={pr.pr_url} className="flex min-w-0 items-center gap-2 text-caption" htmlFor={`workflow-merge-order-${issueId}-${index}`}>
                      <span className="min-w-0 flex-1 truncate">{pr.repository_url}</span>
                      <select
                        id={`workflow-merge-order-${issueId}-${index}`}
                        aria-label={t(($) => $.detail.workflow.merge_order_for_pr, { number: index + 1 })}
                        value={rank}
                        onChange={(event) => {
                          const next = { ...currentRanks };
                          const selectedRank = event.target.value;
                          if (selectedRank) {
                            for (const url of Object.keys(next)) {
                              if (next[url] === selectedRank && url !== pr.pr_url) delete next[url];
                            }
                            next[pr.pr_url] = selectedRank;
                          } else {
                            delete next[pr.pr_url];
                          }
                          setMergeOrder({ candidateId: candidate.id, ranks: next });
                        }}
                        className="h-8 w-24 rounded-md border border-input bg-background px-2 text-caption text-foreground"
                      >
                        <option value="">{t(($) => $.detail.workflow.merge_order_unset)}</option>
                        {candidate.prs.map((_, position) => (
                          <option key={position} value={String(position + 1)}>{position + 1}</option>
                        ))}
                      </select>
                    </label>
                  );
                })}
              </div>
            </fieldset>
          ) : null}
          {canAccept ? (
            <div className="min-w-0 flex-1">
              {hasCompletionContract ? (
                <fieldset className="mb-3 space-y-2 text-caption">
                  <legend className="font-medium">{t(($) => $.detail.workflow.acceptance_options)}</legend>
                  <label className="flex items-start gap-2">
                    <input type="checkbox" checked={outcomeComplete} onChange={(event) => setOutcomeComplete(event.target.checked)} />
                    <span>{t(($) => $.detail.workflow.outcome_complete_label)}</span>
                  </label>
                  {candidate.prs.length > 0 ? (
                    <label className="flex items-start gap-2">
                      <input type="checkbox" checked={holdDelivery} onChange={(event) => setHoldDelivery(event.target.checked)} />
                      <span>{t(($) => $.detail.workflow.hold_delivery_label)}</span>
                    </label>
                  ) : null}
                </fieldset>
              ) : null}
              <Button
                ref={acceptButtonRef}
                type="button"
                size="sm"
                disabled={accept.isPending}
                aria-busy={accept.isPending}
                onClick={() => candidate && accept.mutate({
                  candidate_id: candidate.id,
                  expected_revision: workflow.issue_revision,
                  ...(hasCompletionContract ? { outcome_complete: outcomeComplete, hold_delivery: holdDelivery } : {}),
                  ...(requiresMergeOrder ? { merge_order_pr_urls: orderedPRs } : {}),
                })}
              >
                {accept.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : null}
                {hasCompletionContract ? t(($) => $.detail.workflow.accept_candidate) : t(($) => $.detail.workflow.accept_and_done)}
              </Button>
            </div>
          ) : null}
          {candidate && workflow.available_actions.reject && !frozen ? (
            <WorkflowRejectionDialog workflow={workflow} workspaceId={workspaceId} issueId={issueId} />
          ) : null}
        </div>
      ) : null}

      {acceptError ? (
        <p role="alert" className="mt-2 text-caption text-destructive">
          {isConflict(acceptError)
            ? t(($) => $.detail.workflow.candidate_changed)
            : t(($) => $.detail.workflow.accept_failed)}
        </p>
      ) : null}
    </section>
  );
}
