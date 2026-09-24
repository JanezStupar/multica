package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// IssueWorkflowProfile is the behavioral portion of one selected execution
// profile. It deliberately contains no credentials, permission mode, MCP
// configuration, environment, custom arguments or physical runtime binding.
// The custom-argument digest detects later edits without archiving their bytes.
type IssueWorkflowProfile struct {
	FormatVersion     int    `json:"format_version"`
	IssueID           string `json:"issue_id"`
	AgentID           string `json:"agent_id"`
	PolicyVersion     string `json:"policy_version"`
	AgentName         string `json:"agent_name"`
	AgentInstructions string `json:"agent_instructions"`
	// A ticket-and-agent scoped behavioral instruction selected by an
	// authorized operator. It conveys no review, acceptance or delivery grant.
	SupplementalInstructions string           `json:"supplemental_instructions,omitempty"`
	WorkspaceContext         string           `json:"workspace_context"`
	ExpectedProvider         string           `json:"expected_provider"`
	Model                    string           `json:"model"`
	ThinkingLevel            string           `json:"thinking_level"`
	ServiceTier              string           `json:"service_tier"`
	CustomArgsDigest         string           `json:"custom_args_digest"`
	Skills                   []AgentSkillData `json:"skills"`
}

func IssueWorkflowCustomArgsDigest(raw []byte) string {
	// JSONB canonicalization is enough for an equality guard; the raw values
	// are never returned from or persisted in the issue profile.
	var args []string
	if len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return ""
		}
	}
	encoded, _ := json.Marshal(args)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (p IssueWorkflowProfile) encodedAndDigest() ([]byte, string, error) {
	encoded, err := json.Marshal(p)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(sum[:]), nil
}

func DecodeIssueWorkflowProfile(raw []byte, digest, issueID, agentID, policyVersion string) (*IssueWorkflowProfile, error) {
	var profile IssueWorkflowProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil, fmt.Errorf("decode issue workflow profile: %w", err)
	}
	if profile.FormatVersion != 1 || profile.IssueID != issueID || profile.AgentID != agentID ||
		profile.PolicyVersion != policyVersion || profile.PolicyVersion == "" ||
		strings.TrimSpace(profile.ExpectedProvider) == "" || profile.CustomArgsDigest == "" {
		return nil, errors.New("invalid issue workflow profile identity")
	}
	if _, actual, err := profile.encodedAndDigest(); err != nil || actual != digest {
		return nil, errors.New("issue workflow profile digest mismatch")
	}
	var platforms int
	for _, skill := range profile.Skills {
		bundles, _ := BuildAgentSkillBundles([]AgentSkillData{skill})
		if len(bundles) != 1 || bundles[0].Hash != skill.Hash {
			return nil, errors.New("issue workflow profile skill digest mismatch")
		}
		if skill.ReplacesBuiltin == BuiltinSkillID(PlatformSkillName) {
			platforms++
			if skill.Hash != policyVersion {
				return nil, errors.New("issue workflow profile platform version mismatch")
			}
		}
	}
	if platforms != 1 {
		return nil, errors.New("issue workflow profile must contain one pinned platform bundle")
	}
	return &profile, nil
}

// BindIssueWorkflowProfile serializes first use for one enrolled issue and
// keeps the task's effective profile/version in its own history row. A prior
// task for this issue+agent with no binding is a cutover ambiguity, not a
// license to capture today's edited agent configuration retroactively.
// capture runs under a repeatable-read snapshot after the issue is locked.
func (s *TaskService) BindIssueWorkflowProfile(
	ctx context.Context, workspaceID pgtype.UUID, task db.AgentTaskQueue,
	capture func(*db.Queries, *IssueWorkflowPolicy) (IssueWorkflowProfile, error),
) (*IssueWorkflowProfile, pgtype.UUID, error) {
	if !task.IssueID.Valid {
		return nil, pgtype.UUID{}, errors.New("issue workflow profile requires an issue task")
	}
	for attempt := 0; attempt < 3; attempt++ {
		profile, id, retry, err := s.bindIssueWorkflowProfileOnce(ctx, workspaceID, task, capture)
		if !retry {
			return profile, id, err
		}
	}
	return nil, pgtype.UUID{}, errors.New("issue workflow profile capture conflicted; retry task claim")
}

func (s *TaskService) bindIssueWorkflowProfileOnce(
	ctx context.Context, workspaceID pgtype.UUID, task db.AgentTaskQueue,
	capture func(*db.Queries, *IssueWorkflowPolicy) (IssueWorkflowProfile, error),
) (*IssueWorkflowProfile, pgtype.UUID, bool, error) {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return nil, pgtype.UUID{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ"); err != nil {
		return nil, pgtype.UUID{}, false, err
	}
	// Serialize first use and future explicit profile changes at the issue.
	var issueID pgtype.UUID
	var workflowFrozen bool
	err = tx.QueryRow(ctx, "SELECT id, workflow_frozen FROM issue WHERE id = $1 AND workspace_id = $2 FOR UPDATE", task.IssueID, workspaceID).Scan(&issueID, &workflowFrozen)
	if err != nil {
		return nil, pgtype.UUID{}, false, err
	}
	if workflowFrozen {
		return nil, pgtype.UUID{}, false, errors.New("issue workflow is frozen")
	}
	qtx := s.Queries.WithTx(tx)
	rawPolicy, err := qtx.GetIssueWorkflowPolicy(ctx, db.GetIssueWorkflowPolicyParams{ID: task.IssueID, WorkspaceID: workspaceID})
	if err != nil {
		return nil, pgtype.UUID{}, false, err
	}
	policy, err := s.DecodeIssueWorkflowPolicy(rawPolicy)
	if err != nil || policy == nil {
		return nil, pgtype.UUID{}, false, errors.New("issue workflow policy unavailable or invalid")
	}
	var profileID pgtype.UUID
	var raw []byte
	var digest, rowPolicyVersion string
	lineageID := task.RetryOfTaskID
	if !lineageID.Valid {
		lineageID = task.RerunOfTaskID
	}
	if task.WorkflowProfileID.Valid {
		row, lookupErr := qtx.GetIssueWorkflowProfileByID(ctx, db.GetIssueWorkflowProfileByIDParams{
			ID: task.WorkflowProfileID, WorkspaceID: workspaceID, IssueID: task.IssueID, AgentID: task.AgentID,
		})
		err = lookupErr
		if err == nil {
			profileID, raw, digest, rowPolicyVersion = row.ID, row.Snapshot, row.Digest, row.PolicyVersion
		}
	} else if lineageID.Valid {
		// An automatic retry or named manual rerun continues its source turn's
		// behavioral contract, even after an explicit same-policy reselection.
		// A legacy source with no binding is ambiguous and must fail closed.
		var sourceProfileID pgtype.UUID
		var sourcePolicyVersion pgtype.Text
		err = tx.QueryRow(ctx, `SELECT source.workflow_profile_id, source.workflow_policy_version
			FROM agent_task_queue source JOIN issue i ON i.id=source.issue_id AND i.workspace_id=$4
			WHERE source.id=$1 AND source.issue_id=$2 AND source.agent_id=$3`,
			lineageID, task.IssueID, task.AgentID, workspaceID).Scan(&sourceProfileID, &sourcePolicyVersion)
		if err != nil {
			return nil, pgtype.UUID{}, false, fmt.Errorf("load source task workflow profile: %w", err)
		}
		if !sourceProfileID.Valid || !sourcePolicyVersion.Valid {
			return nil, pgtype.UUID{}, false, errors.New("source task has no workflow profile; explicit reconciliation required")
		}
		row, lookupErr := qtx.GetIssueWorkflowProfileByID(ctx, db.GetIssueWorkflowProfileByIDParams{
			ID: sourceProfileID, WorkspaceID: workspaceID, IssueID: task.IssueID, AgentID: task.AgentID,
		})
		err = lookupErr
		if err == nil {
			profileID, raw, digest, rowPolicyVersion = row.ID, row.Snapshot, row.Digest, row.PolicyVersion
			if rowPolicyVersion != sourcePolicyVersion.String {
				return nil, pgtype.UUID{}, false, errors.New("source task workflow policy version mismatch")
			}
		}
	} else {
		row, lookupErr := qtx.GetIssueWorkflowProfile(ctx, db.GetIssueWorkflowProfileParams{WorkspaceID: workspaceID, IssueID: task.IssueID, AgentID: task.AgentID, PolicyVersion: policy.Version})
		err = lookupErr
		if err == nil {
			profileID, raw, digest, rowPolicyVersion = row.ID, row.Snapshot, row.Digest, row.PolicyVersion
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, pgtype.UUID{}, false, err
	}
	if (task.WorkflowProfileID.Valid || lineageID.Valid) && errors.Is(err, pgx.ErrNoRows) {
		return nil, pgtype.UUID{}, false, errors.New("task lineage workflow profile is missing")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		earlier, checkErr := qtx.HasEarlierIssueAgentTask(ctx, db.HasEarlierIssueAgentTaskParams{
			IssueID: task.IssueID, AgentID: task.AgentID, ID: task.ID, WorkspaceID: workspaceID,
		})
		if checkErr != nil {
			return nil, pgtype.UUID{}, false, checkErr
		}
		if earlier {
			return nil, pgtype.UUID{}, false, errors.New("earlier issue-agent task has no workflow profile; explicit migration required")
		}
		candidate, captureErr := capture(qtx, policy)
		if captureErr != nil {
			return nil, pgtype.UUID{}, false, captureErr
		}
		candidate.FormatVersion = 1
		candidate.IssueID = util.UUIDToString(task.IssueID)
		candidate.AgentID = util.UUIDToString(task.AgentID)
		candidate.PolicyVersion = policy.Version
		encoded, candidateDigest, encodeErr := candidate.encodedAndDigest()
		if encodeErr != nil {
			return nil, pgtype.UUID{}, false, encodeErr
		}
		if _, checkErr := DecodeIssueWorkflowProfile(encoded, candidateDigest, candidate.IssueID, candidate.AgentID, policy.Version); checkErr != nil {
			return nil, pgtype.UUID{}, false, checkErr
		}
		inserted, insertErr := qtx.InsertIssueWorkflowProfile(ctx, db.InsertIssueWorkflowProfileParams{
			ID: dbid.NewV7(), WorkspaceID: workspaceID, IssueID: task.IssueID,
			AgentID: task.AgentID, PolicyVersion: policy.Version,
			Snapshot: encoded, Digest: candidateDigest,
		})
		err = insertErr
		if err != nil {
			return nil, pgtype.UUID{}, isProfileSerializationConflict(err), err
		}
		profileID, raw, digest, rowPolicyVersion = inserted.ID, inserted.Snapshot, inserted.Digest, inserted.PolicyVersion
	}
	if !task.WorkflowProfileID.Valid && !lineageID.Valid && rowPolicyVersion != policy.Version {
		return nil, pgtype.UUID{}, false, errors.New("issue workflow profile uses another policy version; explicit migration required")
	}
	if task.WorkflowPolicyVersion.Valid && task.WorkflowPolicyVersion.String != rowPolicyVersion {
		return nil, pgtype.UUID{}, false, errors.New("task workflow policy version changed")
	}
	profile, err := DecodeIssueWorkflowProfile(raw, digest, util.UUIDToString(task.IssueID), util.UUIDToString(task.AgentID), rowPolicyVersion)
	if err != nil {
		return nil, pgtype.UUID{}, false, err
	}
	bound, err := qtx.BindTaskWorkflowProfile(ctx, db.BindTaskWorkflowProfileParams{
		ID: task.ID, WorkflowProfileID: profileID, WorkflowPolicyVersion: pgtype.Text{String: rowPolicyVersion, Valid: true},
		IssueID: task.IssueID, AgentID: task.AgentID, RuntimeID: task.RuntimeID,
		DispatchedAt: task.DispatchedAt, WorkspaceID: workspaceID,
	})
	if err != nil {
		return nil, pgtype.UUID{}, isProfileSerializationConflict(err), err
	}
	if bound != task.ID {
		return nil, pgtype.UUID{}, false, errors.New("task workflow profile binding mismatch")
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, pgtype.UUID{}, isProfileSerializationConflict(err), err
	}
	return profile, profileID, false, nil
}

// LoadIssueWorkflowProfileForTask serves slim skill resolution from the same
// immutable content used by the full claim, independent of later skill edits.
func (s *TaskService) LoadIssueWorkflowProfileForTask(ctx context.Context, workspaceID pgtype.UUID, task db.AgentTaskQueue) (*IssueWorkflowProfile, error) {
	if !task.IssueID.Valid || !task.WorkflowProfileID.Valid || !task.WorkflowPolicyVersion.Valid {
		return nil, errors.New("task has no complete issue workflow profile binding")
	}
	row, err := s.Queries.GetIssueWorkflowProfileByID(ctx, db.GetIssueWorkflowProfileByIDParams{
		ID: task.WorkflowProfileID, WorkspaceID: workspaceID, IssueID: task.IssueID, AgentID: task.AgentID,
	})
	if err != nil {
		return nil, err
	}
	if row.PolicyVersion != task.WorkflowPolicyVersion.String {
		return nil, errors.New("task workflow policy version mismatch")
	}
	return DecodeIssueWorkflowProfile(row.Snapshot, row.Digest, util.UUIDToString(task.IssueID), util.UUIDToString(task.AgentID), row.PolicyVersion)
}

func isProfileSerializationConflict(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && (pgErr.SQLState() == "40001" || pgErr.SQLState() == "23505")
}

var (
	ErrIssueWorkflowProfileConflict  = errors.New("issue workflow profile changed; refresh and retry")
	ErrIssueWorkflowProfileBusy      = errors.New("issue task activity is busy; retry profile reselection")
	ErrIssueWorkflowProfileForbidden = errors.New("not authorized to reselect this agent workflow profile")
)

// IssueWorkflowProfileReselection is an explicit operator decision for future
// deliberate work on one issue-agent pair. A nil instruction override retains
// the previous ticket-scoped supplement; an empty value revokes it.
type IssueWorkflowProfileReselection struct {
	WorkspaceID              pgtype.UUID
	IssueID                  pgtype.UUID
	AgentID                  pgtype.UUID
	ExpectedProfileID        pgtype.UUID
	RequestID                pgtype.UUID
	ActorUserID              pgtype.UUID
	Reason                   string
	Consequences             string
	Reconciliation           string
	SupplementalInstructions *string
}

func (in IssueWorkflowProfileReselection) intentDigest() (string, error) {
	encoded, err := json.Marshal(struct {
		AgentID                  string  `json:"agent_id"`
		ExpectedProfileID        string  `json:"expected_profile_id"`
		RequestKey               string  `json:"request_key"`
		ActorUserID              string  `json:"actor_user_id"`
		Reason                   string  `json:"reason"`
		Consequences             string  `json:"consequences"`
		Reconciliation           string  `json:"reconciliation"`
		SupplementalInstructions *string `json:"supplemental_instructions"`
	}{
		AgentID: util.UUIDToString(in.AgentID), ExpectedProfileID: util.UUIDToString(in.ExpectedProfileID),
		RequestKey: util.UUIDToString(in.RequestID), ActorUserID: util.UUIDToString(in.ActorUserID),
		Reason: in.Reason, Consequences: in.Consequences, Reconciliation: in.Reconciliation,
		SupplementalInstructions: in.SupplementalInstructions,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type IssueWorkflowProfileSelection struct {
	ProfileID                      string `json:"workflow_profile_id"`
	PreviousProfileID              string `json:"previous_profile_id"`
	PolicyVersion                  string `json:"workflow_policy_version"`
	Revision                       int32  `json:"revision"`
	IssueID                        string `json:"issue_id"`
	AgentID                        string `json:"agent_id"`
	Scope                          string `json:"scope"`
	SupplementalInstructionsActive bool   `json:"supplemental_instructions_active"`
}

// ReselectIssueWorkflowProfile appends a behavioral revision under the same
// immutable base policy. The task table fence excludes enqueue/claim while we
// prove quiescence; an issue row NOWAIT lock avoids lock-order deadlocks.
func (s *TaskService) ReselectIssueWorkflowProfile(ctx context.Context, input IssueWorkflowProfileReselection,
	capture func(*db.Queries, *IssueWorkflowPolicy) (IssueWorkflowProfile, error),
) (*IssueWorkflowProfileSelection, error) {
	requestDigest, err := input.intentDigest()
	if err != nil {
		return nil, err
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout = '2s'"); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE agent_task_queue IN SHARE ROW EXCLUSIVE MODE NOWAIT"); err != nil {
		return nil, profileLockError(err)
	}
	var rawPolicy []byte
	var frozen bool
	err = tx.QueryRow(ctx, `SELECT workflow_policy, workflow_frozen FROM issue
		WHERE id=$1 AND workspace_id=$2 FOR UPDATE NOWAIT`, input.IssueID, input.WorkspaceID).Scan(&rawPolicy, &frozen)
	if err != nil {
		return nil, profileLockError(err)
	}
	if frozen {
		return nil, ErrIssueWorkflowProfileConflict
	}
	policy, err := s.DecodeIssueWorkflowPolicy(rawPolicy)
	if err != nil || policy == nil {
		return nil, errors.New("issue workflow policy unavailable or invalid")
	}
	qtx := s.Queries.WithTx(tx)
	requested, err := qtx.GetIssueWorkflowProfileByRequest(ctx, db.GetIssueWorkflowProfileByRequestParams{
		WorkspaceID: input.WorkspaceID, IssueID: input.IssueID, AgentID: input.AgentID, RequestID: input.RequestID,
	})
	if err == nil {
		if !requested.RequestDigest.Valid || requested.RequestDigest.String != requestDigest {
			return nil, ErrIssueWorkflowProfileConflict
		}
		profile, decodeErr := DecodeIssueWorkflowProfile(requested.Snapshot, requested.Digest,
			util.UUIDToString(input.IssueID), util.UUIDToString(input.AgentID), requested.PolicyVersion)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if input.SupplementalInstructions != nil && profile.SupplementalInstructions != *input.SupplementalInstructions {
			return nil, ErrIssueWorkflowProfileConflict
		}
		return issueWorkflowSelection(requested.ID, requested.PreviousProfileID, requested.PolicyVersion,
			requested.Revision, input.IssueID, input.AgentID, profile), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	current, err := qtx.GetIssueWorkflowProfile(ctx, db.GetIssueWorkflowProfileParams{
		WorkspaceID: input.WorkspaceID, IssueID: input.IssueID, AgentID: input.AgentID, PolicyVersion: policy.Version,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrIssueWorkflowProfileConflict
	}
	if err != nil {
		return nil, err
	}
	if current.ID != input.ExpectedProfileID {
		return nil, ErrIssueWorkflowProfileConflict
	}
	previous, err := DecodeIssueWorkflowProfile(current.Snapshot, current.Digest,
		util.UUIDToString(input.IssueID), util.UUIDToString(input.AgentID), current.PolicyVersion)
	if err != nil {
		return nil, err
	}
	var pending bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue
		WHERE issue_id=$1 AND agent_id=$2 AND status IN
		('queued','deferred','dispatched','running','waiting_local_directory'))`,
		input.IssueID, input.AgentID).Scan(&pending)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, ErrIssueWorkflowProfileBusy
	}
	profile, err := capture(qtx, policy)
	if err != nil {
		return nil, err
	}
	profile.FormatVersion = 1
	profile.IssueID = util.UUIDToString(input.IssueID)
	profile.AgentID = util.UUIDToString(input.AgentID)
	profile.PolicyVersion = policy.Version
	profile.SupplementalInstructions = previous.SupplementalInstructions
	if input.SupplementalInstructions != nil {
		profile.SupplementalInstructions = *input.SupplementalInstructions
	}
	encoded, digest, err := profile.encodedAndDigest()
	if err != nil {
		return nil, err
	}
	if _, err = DecodeIssueWorkflowProfile(encoded, digest, profile.IssueID, profile.AgentID, policy.Version); err != nil {
		return nil, err
	}
	inserted, err := qtx.InsertReselectedIssueWorkflowProfile(ctx, db.InsertReselectedIssueWorkflowProfileParams{
		ID: dbid.NewV7(), WorkspaceID: input.WorkspaceID, IssueID: input.IssueID, AgentID: input.AgentID,
		PolicyVersion: policy.Version, Revision: current.Revision + 1, PreviousProfileID: current.ID,
		RequestID: input.RequestID, ActorUserID: input.ActorUserID,
		RequestDigest:  pgtype.Text{String: requestDigest, Valid: true},
		Reason:         pgtype.Text{String: input.Reason, Valid: true},
		Consequences:   pgtype.Text{String: input.Consequences, Valid: true},
		Reconciliation: pgtype.Text{String: input.Reconciliation, Valid: true},
		Snapshot:       encoded, Digest: digest,
	})
	if err != nil {
		return nil, err
	}
	details, _ := json.Marshal(map[string]any{
		"workflow_profile_id": util.UUIDToString(inserted.ID),
		"previous_profile_id": util.UUIDToString(current.ID),
		"policy_version":      policy.Version, "revision": inserted.Revision,
		"reason": input.Reason, "consequences": input.Consequences,
		"reconciliation":                   input.Reconciliation,
		"supplemental_instructions_active": profile.SupplementalInstructions != "",
	})
	if _, err = tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
		VALUES($1,$2,'member',$3,'workflow_profile_reselected',$4)`,
		input.WorkspaceID, input.IssueID, input.ActorUserID, details); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return issueWorkflowSelection(inserted.ID, current.ID, policy.Version, inserted.Revision,
		input.IssueID, input.AgentID, &profile), nil
}

func issueWorkflowSelection(id, previousID pgtype.UUID, policyVersion string, revision int32,
	issueID, agentID pgtype.UUID, profile *IssueWorkflowProfile) *IssueWorkflowProfileSelection {
	return &IssueWorkflowProfileSelection{
		ProfileID: util.UUIDToString(id), PreviousProfileID: util.UUIDToString(previousID),
		PolicyVersion: policyVersion, Revision: revision,
		IssueID: util.UUIDToString(issueID), AgentID: util.UUIDToString(agentID),
		Scope: "issue_agent_until_reselected", SupplementalInstructionsActive: profile.SupplementalInstructions != "",
	}
}

func profileLockError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "55P03" || pgErr.Code == "40P01") {
		return ErrIssueWorkflowProfileBusy
	}
	return err
}
