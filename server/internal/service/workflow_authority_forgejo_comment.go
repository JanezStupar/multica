package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workflowForgejoApprover struct {
	ProviderUserID string `json:"provider_user_id"`
	MemberID       string `json:"member_id"`
}

// workflowForgejoCommentAuthority resolves a signed, delivered provider input.
// The agent supplies only the input ID and interpretation; neither a provider
// login nor an agent-supplied member ID can confer member authority.
func workflowForgejoCommentAuthority(ctx context.Context, tx pgx.Tx, issue db.Issue,
	actor WorkflowActor, in WorkflowCommentAcceptanceInput) (WorkflowCommentAuthority, error) {
	return workflowForgejoCommentAuthorityWithLock(ctx, tx, issue, actor, in, true)
}

func workflowForgejoCommentAuthorityWithLock(ctx context.Context, tx pgx.Tx, issue db.Issue,
	actor WorkflowActor, in WorkflowCommentAcceptanceInput, lock bool) (WorkflowCommentAuthority, error) {
	proof := WorkflowCommentAuthority{}
	agentID, sourceTaskID, err := workflowAgentTask(ctx, tx, issue, actor)
	if err != nil {
		return proof, err
	}
	inputID, err := workflowAuthorityUUID(in.SourceID)
	if err != nil {
		return proof, ErrWorkflowAuthorityInput
	}
	var connectionID, prID, candidateID pgtype.UUID
	var provider, kind, objectID, objectRevision, objectAction, authorID, authorLogin, body, htmlURL, headSHA, prURL string
	var revisionAt pgtype.Timestamptz
	var sourceCreatedAt time.Time
	var mappings []byte
	query := `WITH RECURSIVE lineage AS (
	  SELECT t.id,t.retry_of_task_id,t.rerun_of_task_id,t.issue_id,t.agent_id,
	         t.originator_user_id,t.accountable_user_id,ARRAY[t.id] visited
	  FROM agent_task_queue t WHERE t.id=$4 AND t.issue_id=$3 AND t.agent_id=$5
	  UNION ALL
	  SELECT parent.id,parent.retry_of_task_id,parent.rerun_of_task_id,parent.issue_id,parent.agent_id,
	         parent.originator_user_id,parent.accountable_user_id,child.visited||parent.id
	  FROM lineage child JOIN agent_task_queue parent ON parent.id=COALESCE(child.retry_of_task_id,child.rerun_of_task_id)
	  WHERE NOT parent.id=ANY(child.visited) AND parent.issue_id=child.issue_id AND parent.agent_id=child.agent_id
	    AND parent.originator_user_id IS NOT DISTINCT FROM child.originator_user_id
	    AND parent.accountable_user_id IS NOT DISTINCT FROM child.accountable_user_id
	)
	SELECT input.connection_id,input.pull_request_id,candidate.id,conn.provider,
	       input.kind,input.object_id,input.object_revision,input.object_revision_at,input.object_action,
	       input.provider_author_id,input.provider_author_login,input.body,input.html_url,input.head_sha,input.created_at,
	       pr.html_url,conn.workflow_approvers
	FROM vcs_workflow_input input
	JOIN vcs_connection conn ON conn.id=input.connection_id AND conn.workspace_id=input.workspace_id
	JOIN vcs_pull_request pr ON pr.id=input.pull_request_id AND pr.connection_id=conn.id AND pr.workspace_id=input.workspace_id
	JOIN issue_workflow_candidate candidate ON candidate.id=$6 AND candidate.issue_id=input.issue_id
	  AND candidate.workspace_id=input.workspace_id
	WHERE input.id=$1 AND input.workspace_id=$2 AND input.issue_id=$3
	  AND conn.provider='forgejo'
	  AND input.kind IN ('comment','review') AND input.object_id<>''
	  AND input.provider_author_id<>'' AND input.object_revision_at IS NOT NULL
	  AND input.object_action NOT IN ('deleted','agent_output')
	  AND input.processed_at IS NOT NULL AND input.task_id IN (SELECT id FROM lineage)
	  -- Null binding is usable only for the first single-PR candidate registered
	  -- after receipt. The last provider-timed head before the approval must
	  -- match, so a delayed old comment cannot inherit a newer mirrored SHA.
	  AND (input.candidate_id=candidate.id AND input.object_revision_at>=candidate.created_at
	    OR input.candidate_id IS NULL AND input.created_at<=candidate.created_at
	      AND jsonb_array_length(candidate.pr_set)=1
	      AND NOT EXISTS(SELECT 1 FROM issue_workflow_candidate intervening
	        WHERE intervening.workspace_id=input.workspace_id AND intervening.issue_id=input.issue_id
	          AND intervening.id<>candidate.id AND intervening.created_at>=input.created_at
	          AND intervening.created_at<=candidate.created_at)
	      AND EXISTS(SELECT 1 FROM LATERAL (SELECT prior.head_sha,prior.object_revision_at FROM vcs_workflow_input prior
	        WHERE prior.workspace_id=input.workspace_id AND prior.issue_id=input.issue_id
	          AND prior.connection_id=input.connection_id AND prior.pull_request_id=input.pull_request_id
	          AND prior.kind='head' AND prior.object_revision_at IS NOT NULL
	          AND prior.object_revision_at<=input.object_revision_at AND prior.created_at<=input.created_at
	        ORDER BY prior.object_revision_at DESC,prior.created_at DESC,prior.id DESC LIMIT 1) latest
	        WHERE latest.head_sha=input.head_sha AND latest.object_revision_at<input.object_revision_at))
	  AND EXISTS(SELECT 1 FROM agent_task_queue invoking WHERE invoking.id=$4 AND invoking.issue_id=$3
	    AND invoking.agent_id=$5 AND invoking.workflow_policy_version=candidate.policy_version
	    AND invoking.workflow_profile_id IS NOT NULL)
	  AND input.head_sha<>'' AND lower(input.head_sha)=lower(pr.head_sha)
	  AND EXISTS(SELECT 1 FROM jsonb_array_elements(candidate.pr_set) candidate_pr
	    WHERE candidate_pr->>'pr_url'=pr.html_url
	      AND lower(candidate_pr->>'commit_sha')=lower(input.head_sha))
	  AND workflow_provider_feedback_task_current($4,$3)
	  AND NOT EXISTS(SELECT 1 FROM vcs_workflow_input newer
	    WHERE newer.connection_id=input.connection_id AND newer.pull_request_id=input.pull_request_id
	      AND newer.kind=input.kind AND newer.object_id=input.object_id AND newer.id<>input.id
	      AND (newer.object_revision_at>=input.object_revision_at
	        OR newer.object_action='deleted' AND newer.created_at>=input.created_at))`
	if lock {
		query += ` FOR SHARE OF input,conn,pr`
	}
	err = tx.QueryRow(ctx, query, inputID, issue.WorkspaceID, issue.ID, sourceTaskID, agentID,
		issue.WorkflowCandidateID).Scan(&connectionID, &prID, &candidateID, &provider,
		&kind, &objectID, &objectRevision, &revisionAt, &objectAction,
		&authorID, &authorLogin, &body, &htmlURL, &headSHA, &sourceCreatedAt, &prURL, &mappings)
	if err == pgx.ErrNoRows {
		return proof, fmt.Errorf("%w: Forgejo comment is not current delivered evidence for this candidate", ErrWorkflowAuthorityForbidden)
	}
	if err != nil {
		return proof, err
	}
	if strings.TrimSpace(body) == "" || strings.HasSuffix(strings.TrimSpace(body), "<!-- multica-agent-output -->") {
		return proof, ErrWorkflowAuthorityForbidden
	}
	var approved []workflowForgejoApprover
	if err := json.Unmarshal(mappings, &approved); err != nil {
		return proof, ErrWorkflowAuthorityForbidden
	}
	var memberID pgtype.UUID
	matches := 0
	for _, entry := range approved {
		if entry.ProviderUserID != authorID {
			continue
		}
		id, parseErr := workflowAuthorityUUID(entry.MemberID)
		if parseErr != nil {
			return proof, ErrWorkflowAuthorityForbidden
		}
		memberID = id
		matches++
	}
	if matches != 1 {
		return proof, ErrWorkflowAuthorityForbidden
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2`,
		issue.WorkspaceID, memberID).Scan(&role); err != nil {
		return proof, ErrWorkflowAuthorityForbidden
	}
	proof.MemberID = memberID
	proof.SourceTaskID = sourceTaskID
	proof.Snapshot = map[string]any{
		"source": "forgejo", "source_id": util.UUIDToString(inputID),
		"connection_id": util.UUIDToString(connectionID), "pull_request_id": util.UUIDToString(prID),
		"candidate_id": util.UUIDToString(candidateID), "pr_url": prURL,
		"source_url": htmlURL, "source_kind": kind, "source_object_id": objectID,
		"source_revision": objectRevision, "source_revision_at": revisionAt.Time.Format(time.RFC3339Nano),
		"source_created_at": revisionAt.Time, "source_recorded_at": sourceCreatedAt,
		"source_action": objectAction, "source_content": body, "head_sha": headSHA,
		"provider": provider, "provider_author_id": authorID, "provider_author_login": authorLogin,
		"member_id": util.UUIDToString(memberID), "member_role": role,
		"executor_agent_id": actor.ID, "executor_task_id": actor.SourceTaskID,
	}
	return proof, nil
}

// workflowForgejoCommentAvailable is the state-view counterpart of the
// mutation proof. It never treats an unmapped provider account as a member.
func workflowForgejoCommentAvailable(ctx context.Context, tx pgx.Tx, issue db.Issue,
	actor WorkflowActor, authority WorkflowAuthorityPolicy, policyVersion string) (bool, error) {
	if !issue.WorkflowCandidateID.Valid || actor.Type != "agent" || policyVersion == "" {
		return false, nil
	}
	_, taskID, err := workflowAgentTask(ctx, tx, issue, actor)
	if err != nil {
		if errors.Is(err, ErrWorkflowAuthorityForbidden) {
			return false, nil
		}
		return false, err
	}
	var bound bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue t
		WHERE t.id=$1 AND t.issue_id=$2 AND t.workflow_policy_version=$3 AND t.workflow_profile_id IS NOT NULL)`,
		taskID, issue.ID, policyVersion).Scan(&bound); err != nil || !bound {
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT input.id FROM vcs_workflow_input input
		JOIN issue_workflow_candidate candidate ON candidate.id=$3 AND candidate.issue_id=input.issue_id
		  AND candidate.workspace_id=input.workspace_id
		WHERE input.workspace_id=$1 AND input.issue_id=$2
		AND (input.candidate_id=candidate.id AND input.object_revision_at>=candidate.created_at
		  OR input.candidate_id IS NULL AND input.created_at<=candidate.created_at
		    AND jsonb_array_length(candidate.pr_set)=1
		    AND NOT EXISTS(SELECT 1 FROM issue_workflow_candidate intervening
		      WHERE intervening.workspace_id=input.workspace_id AND intervening.issue_id=input.issue_id
		        AND intervening.id<>candidate.id AND intervening.created_at>=input.created_at
		        AND intervening.created_at<=candidate.created_at)
		    AND EXISTS(SELECT 1 FROM LATERAL (SELECT prior.head_sha,prior.object_revision_at FROM vcs_workflow_input prior
		      WHERE prior.workspace_id=input.workspace_id AND prior.issue_id=input.issue_id
		        AND prior.connection_id=input.connection_id AND prior.pull_request_id=input.pull_request_id
		        AND prior.kind='head' AND prior.object_revision_at IS NOT NULL
		        AND prior.object_revision_at<=input.object_revision_at AND prior.created_at<=input.created_at
		      ORDER BY prior.object_revision_at DESC,prior.created_at DESC,prior.id DESC LIMIT 1) latest
		      WHERE latest.head_sha=input.head_sha AND latest.object_revision_at<input.object_revision_at))
		AND input.kind IN ('comment','review') AND input.processed_at IS NOT NULL
		AND provider_author_id<>'' AND object_revision_at IS NOT NULL
		ORDER BY input.created_at DESC,input.id DESC`, issue.WorkspaceID, issue.ID, issue.WorkflowCandidateID)
	if err != nil {
		return false, err
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	_, acceptanceGrant, err := workflowExceptionGrant(ctx, tx, issue, issue.WorkflowCandidateID, policyVersion, "acceptance")
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		proof, err := workflowForgejoCommentAuthorityWithLock(ctx, tx, issue, actor,
			WorkflowCommentAcceptanceInput{SourceID: util.UUIDToString(id)}, false)
		if errors.Is(err, ErrWorkflowAuthorityForbidden) || errors.Is(err, ErrWorkflowAuthorityInput) {
			continue
		}
		if err != nil {
			return false, err
		}
		role, _ := proof.Snapshot["member_role"].(string)
		if workflowHumanAcceptanceAllowed(authority, role, util.UUIDToString(proof.MemberID), acceptanceGrant) {
			return true, nil
		}
	}
	return false, nil
}
