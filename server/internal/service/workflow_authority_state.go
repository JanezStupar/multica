package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type WorkflowAuthorityService struct {
	Tasks *TaskService
	// ReviewVerifier checks linked provider reviews at the exact candidate
	// commits using short-lived credentials supplied by the handler. Nil fails
	// review-required acceptance closed.
	ReviewVerifier func(context.Context, WorkflowReviewEvidenceInput) error
}

func (s WorkflowAuthorityService) ReadState(ctx context.Context, workspaceID, issueID pgtype.UUID, actor WorkflowActor) (WorkflowState, error) {
	state := WorkflowState{Reviews: []WorkflowReviewView{}, Delivery: []WorkflowDeliveryView{},
		RetainedContextOptions: []WorkflowRetainedContextOption{}, Exceptions: []WorkflowExceptionView{},
		AcceptanceBlockers: []string{}}
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return state, errors.New("workflow authority service unavailable")
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return state, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY"); err != nil {
		return state, err
	}
	q := s.Tasks.Queries.WithTx(tx)
	issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issueID, WorkspaceID: workspaceID})
	if err != nil {
		return state, err
	}
	policy, err := s.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil {
		return state, err
	}
	if policy == nil {
		return state, ErrWorkflowAuthorityUnavailable
	}
	authority, err := ParseWorkflowAuthorityPolicy(policy.Bundle)
	if err != nil {
		return state, err
	}
	state.IssueID = util.UUIDToString(issue.ID)
	state.IssueRevision = issue.Revision
	state.Frozen = issue.WorkflowFrozen
	state.PolicyVersion = policy.Version
	if issue.WorkflowCandidateID.Valid {
		var digest, scope, writer string
		var prSet []byte
		var created time.Time
		err = tx.QueryRow(ctx, `SELECT digest,scope_digest,writer_task_id::text,pr_set,created_at
			FROM issue_workflow_candidate WHERE id=$1 AND issue_id=$2 AND workspace_id=$3`,
			issue.WorkflowCandidateID, issue.ID, workspaceID).Scan(&digest, &scope, &writer, &prSet, &created)
		if err != nil {
			return state, fmt.Errorf("read current workflow candidate: %w", err)
		}
		var prs []HandoffCandidate
		if err := json.Unmarshal(prSet, &prs); err != nil {
			return state, err
		}
		if prs == nil {
			prs = []HandoffCandidate{}
		}
		state.Candidate = &WorkflowCandidateView{ID: util.UUIDToString(issue.WorkflowCandidateID), Digest: digest,
			ScopeDigest: scope, WriterTaskID: writer, PRs: prs, CreatedAt: created}
		rows, queryErr := tx.Query(ctx, `SELECT id::text,verdict,reviewer_task_id::text,pr_review_urls,submitted_at
			FROM issue_workflow_review WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3
			ORDER BY submitted_at DESC,id DESC`, workspaceID, issue.ID, issue.WorkflowCandidateID)
		if queryErr != nil {
			return state, queryErr
		}
		for rows.Next() {
			var review WorkflowReviewView
			var links []byte
			if err := rows.Scan(&review.ID, &review.Verdict, &review.ReviewerTaskID, &links, &review.SubmittedAt); err != nil {
				rows.Close()
				return state, err
			}
			if err := json.Unmarshal(links, &review.PRReviewURLs); err != nil {
				rows.Close()
				return state, err
			}
			if review.PRReviewURLs == nil {
				review.PRReviewURLs = []string{}
			}
			state.Reviews = append(state.Reviews, review)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return state, err
		}
		rows.Close()
	}
	var acceptanceID pgtype.UUID
	var acceptedAt pgtype.Timestamptz
	var classification pgtype.Text
	var acceptanceBlocker pgtype.Text
	var a WorkflowAcceptanceView
	err = tx.QueryRow(ctx, `SELECT id,candidate_id::text,state,mode,classification_reason,requested_at,accepted_at,last_error_class
		FROM issue_workflow_acceptance WHERE workspace_id=$1 AND issue_id=$2
		ORDER BY requested_at DESC,id DESC LIMIT 1`, workspaceID, issue.ID).Scan(
		&acceptanceID, &a.CandidateID, &a.State, &a.Mode, &classification, &a.RequestedAt, &acceptedAt, &acceptanceBlocker)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return state, err
	}
	if err == nil {
		a.ID = util.UUIDToString(acceptanceID)
		a.ClassificationReason = classification.String
		a.Blocker = acceptanceBlocker.String
		if acceptedAt.Valid {
			t := acceptedAt.Time
			a.AcceptedAt = &t
		}
		state.Acceptance = &a
		rows, queryErr := tx.Query(ctx, `SELECT id::text,ordinal,pr_url,expected_head_sha,action,merge_method,status,
			attempt_count,next_attempt_at,last_error_class,readiness_done_at,merged_at
			FROM issue_workflow_delivery WHERE workspace_id=$1 AND issue_id=$2 AND acceptance_id=$3 ORDER BY ordinal`,
			workspaceID, issue.ID, acceptanceID)
		if queryErr != nil {
			return state, queryErr
		}
		for rows.Next() {
			var d WorkflowDeliveryView
			var method, lastError pgtype.Text
			var ready, merged pgtype.Timestamptz
			if err := rows.Scan(&d.ID, &d.Ordinal, &d.PRURL, &d.ExpectedHeadSHA, &d.Action, &method,
				&d.Status, &d.AttemptCount, &d.NextAttemptAt, &lastError, &ready, &merged); err != nil {
				rows.Close()
				return state, err
			}
			d.MergeMethod, d.LastErrorClass = method.String, lastError.String
			if ready.Valid {
				t := ready.Time
				d.ReadinessDoneAt = &t
			}
			if merged.Valid {
				t := merged.Time
				d.MergedAt = &t
			}
			state.Delivery = append(state.Delivery, d)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return state, err
		}
		rows.Close()
	}
	if actor.Type == "member" && !issue.WorkflowFrozen && issue.Status == "done" &&
		state.Candidate != nil && state.Acceptance != nil && state.Acceptance.State == "accepted" &&
		state.Acceptance.CandidateID == state.Candidate.ID && len(state.Delivery) > 0 {
		candidate, candidateErr := loadCurrentWorkflowCandidate(ctx, tx, issue, policy.Version)
		if candidateErr == nil {
			var acceptedByType, acceptedPolicy, acceptedScope, exceptionID string
			var acceptedByID pgtype.UUID
			var revokedAt pgtype.Timestamptz
			err := tx.QueryRow(ctx, `SELECT actor_type,actor_id,policy_version,
				COALESCE(authority_snapshot->>'scope_digest',''),
				COALESCE(authority_snapshot->>'acceptance_exception_id',''),revoked_at
				FROM issue_workflow_acceptance WHERE id=$1 AND workspace_id=$2 AND issue_id=$3`,
				acceptanceID, workspaceID, issueID).Scan(
				&acceptedByType, &acceptedByID, &acceptedPolicy, &acceptedScope, &exceptionID, &revokedAt)
			if err != nil {
				return state, err
			}
			if !revokedAt.Valid && acceptedPolicy == policy.Version && acceptedScope == candidate.ScopeDigest {
				allowed, authErr := workflowDeliveryRetryActorAllowed(ctx, tx, workspaceID, issueID, candidate.ID,
					policy.Version, authority, actor, acceptedByType, acceptedByID, exceptionID)
				if authErr == nil && allowed {
					for i := range state.Delivery {
						d := &state.Delivery[i]
						if d.Status != "blocked" {
							continue
						}
						newerHead, headErr := workflowDeliveryNewerHeadKnown(ctx, tx, workspaceID, issueID,
							mustAuthorityUUID(d.ID), d.ExpectedHeadSHA)
						if headErr != nil {
							return state, headErr
						}
						d.Retryable = !newerHead
					}
				}
			}
		}
	}
	exceptionRows, err := tx.Query(ctx, `SELECT id::text,candidate_id::text,scope,grant_details,actor_type,actor_id::text,
		reason,consequences,base_policy_version,created_at,revoked_at,
		revocation_reason,revocation_consequences,revoked_by_type,revoked_by_id::text
		FROM issue_workflow_exception WHERE workspace_id=$1 AND issue_id=$2
		ORDER BY created_at DESC,id DESC`, workspaceID, issue.ID)
	if err != nil {
		return state, err
	}
	for exceptionRows.Next() {
		var view WorkflowExceptionView
		var rawGrant []byte
		var revoked pgtype.Timestamptz
		var revocationReason, revocationConsequences, revokedByType, revokedByID pgtype.Text
		if err := exceptionRows.Scan(&view.ID, &view.CandidateID, &view.Scope, &rawGrant, &view.ActorType, &view.ActorID,
			&view.Reason, &view.Consequences, &view.BasePolicyVersion, &view.CreatedAt, &revoked,
			&revocationReason, &revocationConsequences, &revokedByType, &revokedByID); err != nil {
			exceptionRows.Close()
			return state, err
		}
		if err := json.Unmarshal(rawGrant, &view.GrantDetails); err != nil {
			exceptionRows.Close()
			return state, err
		}
		if revoked.Valid {
			at := revoked.Time
			view.RevokedAt = &at
		}
		view.RevocationReason, view.RevocationConsequences = revocationReason.String, revocationConsequences.String
		view.RevokedByType, view.RevokedByID = revokedByType.String, revokedByID.String
		state.Exceptions = append(state.Exceptions, view)
	}
	if err := exceptionRows.Err(); err != nil {
		exceptionRows.Close()
		return state, err
	}
	exceptionRows.Close()
	// Only the writer's exact completed provider session is eligible for
	// rejection continuation. Do not expose session identifiers or unrelated
	// private-agent task history in a general issue read.
	if actor.Type == "member" && state.Candidate != nil {
		var taskID, agentID pgtype.UUID
		var name string
		err := tx.QueryRow(ctx, `SELECT t.id,t.agent_id,a.name FROM agent_task_queue t
			JOIN agent a ON a.id=t.agent_id AND a.workspace_id=$3
			WHERE t.id=$1 AND t.issue_id=$2 AND t.status='completed' AND t.session_id IS NOT NULL
			AND t.runtime_id=a.runtime_id`,
			mustAuthorityUUID(state.Candidate.WriterTaskID), issue.ID, workspaceID).Scan(&taskID, &agentID, &name)
		if err == nil {
			agent, agentErr := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
			memberID, parseErr := workflowAuthorityUUID(actor.ID)
			if agentErr == nil && parseErr == nil && (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, workspaceID, memberID, agent) == nil {
				state.RetainedContextOptions = append(state.RetainedContextOptions, WorkflowRetainedContextOption{
					TaskID: util.UUIDToString(taskID), AgentID: util.UUIDToString(agentID), AgentName: name, Kind: "writer",
				})
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return state, err
		}
	}
	block := func(code string) { state.AcceptanceBlockers = append(state.AcceptanceBlockers, code) }
	if issue.WorkflowFrozen {
		block("frozen")
	}
	if state.Candidate == nil {
		block("candidate_missing")
	} else {
		if state.Candidate.ScopeDigest != WorkflowScopeDigest(issue, policy.Version) {
			block("scope_changed")
		} else {
			candidate, candidateErr := loadCurrentWorkflowCandidate(ctx, tx, issue, policy.Version)
			if candidateErr != nil {
				block("scope_changed")
			} else {
				var writerStatus string
				if err := tx.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id=$1 AND issue_id=$2`,
					candidate.WriterTaskID, issue.ID).Scan(&writerStatus); err != nil || writerStatus != "completed" {
					block("source_incomplete")
				}
				_, reviewGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, policy.Version, "review")
				if err != nil {
					return state, err
				}
				var pendingReviewerTaskID pgtype.UUID
				if actor.Type == "agent" && issue.Status == "in_review" && issue.AssigneeType.String == "agent" {
					agentID, taskID, taskErr := workflowAgentTask(ctx, tx, issue, actor)
					if taskErr == nil && agentID == issue.AssigneeID {
						_, acceptanceGrant, grantErr := workflowExceptionGrant(ctx, tx, issue, candidate.ID, policy.Version, "acceptance")
						if grantErr != nil {
							return state, grantErr
						}
						allowed := acceptanceGrant["agent_actor_id"] == actor.ID
						if authority.AutonomousEnabled {
							for _, id := range authority.AutonomousAgentIDs {
								allowed = allowed || id == actor.ID
							}
						}
						if allowed {
							pendingReviewerTaskID = taskID
						}
					}
				}
				if authority.ReviewRequired && reviewGrant["waive"] != true {
					if len(state.Reviews) == 0 || state.Reviews[0].Verdict != "pass" {
						block("review_missing")
					} else if _, err := workflowReviewSatisfiedForRequest(ctx, tx, issue, candidate, true, pendingReviewerTaskID); err != nil {
						block("review_not_independent")
					}
				}
				if _, err := workflowDeliveryBindings(ctx, tx, issue, candidate.PRs); err != nil {
					block("provider_binding_missing")
				}
				_, deliveryGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, policy.Version, "delivery")
				if err != nil {
					return state, err
				}
				action := authority.HumanDelivery
				if actor.Type == "agent" {
					action = authority.AutonomousDelivery
				}
				method := authority.MergeMethod
				if override, ok := deliveryGrant["action"].(string); ok {
					action = override
				}
				if override, ok := deliveryGrant["merge_method"].(string); ok {
					method = override
				}
				state.DeliveryPreview = &WorkflowDeliveryPreview{Action: action,
					RequiresOrder: action == "merge" && len(candidate.PRs) > 1}
				if action == "merge" {
					state.DeliveryPreview.MergeMethod = method
					if len(candidate.PRs) > 1 {
						if authority.MultiPRMergeOrder != "explicit" && deliveryGrant["action"] != "merge" {
							block("merge_not_granted")
						} else {
							block("merge_order_required")
						}
					}
				}
			}
		}
	}
	var except pgtype.UUID
	if actor.Type == "agent" {
		except, _ = workflowAuthorityUUID(actor.SourceTaskID)
	}
	active, err := workflowHasMutableRuns(ctx, tx, issue.ID, except)
	if err != nil {
		return state, err
	}
	if active {
		block("active_work")
	}
	pending, err := workflowHasPendingHandoff(ctx, tx, issue.ID)
	if err != nil {
		return state, err
	}
	if pending {
		block("pending_handoff")
	}
	if state.Acceptance != nil && state.Candidate != nil && state.Acceptance.CandidateID == state.Candidate.ID {
		switch state.Acceptance.State {
		case "requested":
			block("acceptance_pending")
		case "accepted":
			block("already_accepted")
		}
	}
	canAttempt := true
	for _, blocker := range state.AcceptanceBlockers {
		if blocker != "merge_order_required" {
			canAttempt = false
		}
	}
	if actor.Type == "member" && state.Candidate != nil {
		role, _, roleErr := workflowMemberRole(ctx, tx, workspaceID, actor)
		if roleErr == nil {
			_, acceptanceGrant, grantErr := workflowExceptionGrant(ctx, tx, issue, issue.WorkflowCandidateID, policy.Version, "acceptance")
			if grantErr != nil {
				return state, grantErr
			}
			allowed := false
			for _, acceptedRole := range authority.HumanAcceptRoles {
				allowed = allowed || role == acceptedRole
			}
			allowed = allowed || acceptanceGrant["human_actor_id"] == actor.ID
			ownsHumanHandoff := issue.Status == "in_review" && issue.AssigneeType.String == "member" &&
				(util.UUIDToString(issue.AssigneeID) == actor.ID || acceptanceGrant["human_actor_id"] == actor.ID)
			if !ownsHumanHandoff {
				block("human_recipient_required")
			}
			state.AvailableActions.AcceptHuman = allowed && ownsHumanHandoff && canAttempt
			state.AvailableActions.Reject = !issue.WorkflowFrozen && !active &&
				(issue.Status == "in_review" || issue.Status == "done") &&
				(ownsHumanHandoff || role == "owner" || role == "admin")
			state.AvailableActions.WaiveReview = !issue.WorkflowFrozen && (role == "owner" || role == "admin")
		}
	} else if actor.Type == "agent" && state.Candidate != nil {
		_, acceptanceGrant, grantErr := workflowExceptionGrant(ctx, tx, issue, issue.WorkflowCandidateID, policy.Version, "acceptance")
		if grantErr != nil {
			return state, grantErr
		}
		allowed := false
		for _, id := range authority.AutonomousAgentIDs {
			allowed = allowed || id == actor.ID
		}
		agentID, taskID, taskErr := workflowAgentTask(ctx, tx, issue, actor)
		allowed = authority.AutonomousEnabled && allowed || acceptanceGrant["agent_actor_id"] == actor.ID
		state.AvailableActions.RequestTrivialAcceptance = allowed && canAttempt &&
			taskErr == nil && agentID == issue.AssigneeID && taskID.Valid && issue.Status == "in_review"
		state.AvailableActions.WaiveReview = !issue.WorkflowFrozen && authority.SupervisorAgentScopes[actor.ID]["review"]
	}
	if err := tx.Commit(ctx); err != nil {
		return state, err
	}
	return state, nil
}
