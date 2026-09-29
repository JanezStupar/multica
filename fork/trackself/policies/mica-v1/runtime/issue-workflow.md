This issue run uses the ticket's pinned Trackself policy. Its detailed rules
are in `references/workflow.md`; use the sections relevant to the current role
and action. This brief is injected at claim. Do not treat the bundle name,
workspace default, or a live agent record as a replacement for the pinned
ticket policy and selected task profile.

1. Read the issue, its trigger and relevant comments, then the repository-owned
   requirements and current work. On a resumed turn, reconcile retained
   assumptions with intervening changes before writing. Use the platform issue
   reference when you need command effects.
2. For implementation or coordination, deliver the assigned outcome within
   current authority. Select a recipient only when a handoff is needed; use the
   current recorded owner, selected profile and configured route as described
   under **Responsibility and execution** and **Status and handoffs** in the
   workflow reference. Record a durable handoff before outgoing evaluation.
3. For review, use a context independent from implementation for the first
   review/fix pass and a separate fresh, read-only final reviewer. Record the
   exact candidate, PR review evidence and verdict. Read **Review and
   exceptions** before judging or changing an accepted candidate.
4. Treat actual human issue or bound PR comments as workflow evidence. Classify
   corrections, scope changes and clear acceptance; a question is not
   acceptance. Read **Member feedback continuation** before using
   `feedback-continue` and **Review and exceptions** before `comment-accept`.
5. Before any review, acceptance, rejection, exception or delivery action,
   inspect `multica issue workflow get <issue-id>` and bind the action to its
   current candidate and revision. Human approval ordinarily authorizes PR
   readiness only; merge needs an explicit human instruction or separately
   enabled scoped authority. Preserve holds, review independence and provider
   head checks. Use **PRs, ticket records and delivery** for completion.

If a required capability or pin is absent, stop the affected workflow action
and report the technical boundary. Do not silently enroll, migrate, waive
review, merge or claim deployment proof.
