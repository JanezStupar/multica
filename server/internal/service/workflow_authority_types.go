package service

import (
	"errors"
	"time"
)

var (
	ErrWorkflowAuthorityInput       = errors.New("invalid workflow authority input")
	ErrWorkflowAuthorityConflict    = errors.New("workflow authority state changed")
	ErrWorkflowAuthorityForbidden   = errors.New("workflow authority denied")
	ErrWorkflowAuthorityUnavailable = errors.New("workflow authority unavailable")
)

// WorkflowActor is resolved by the authenticated transport. Service methods
// recheck membership or the bound task in their transaction before granting
// any authority; these fields are not an authorization token by themselves.
type WorkflowActor struct {
	Type         string
	ID           string
	SourceTaskID string
}

type WorkflowCandidateView struct {
	ID           string             `json:"id"`
	Digest       string             `json:"digest"`
	ScopeDigest  string             `json:"scope_digest"`
	WriterTaskID string             `json:"writer_task_id"`
	PRs          []HandoffCandidate `json:"prs"`
	CreatedAt    time.Time          `json:"created_at"`
}

type WorkflowReviewView struct {
	ID             string    `json:"id"`
	Verdict        string    `json:"verdict"`
	ReviewerTaskID string    `json:"reviewer_task_id"`
	PRReviewURLs   []string  `json:"pr_review_urls"`
	SubmittedAt    time.Time `json:"submitted_at"`
}

// WorkflowFeedbackView identifies the immutable feedback snapshot that caused
// candidate rework. The original comment may later be edited or deleted; the
// snapshot stays in the rejection ledger and writer task, while this general
// issue view exposes only its digest.
type WorkflowFeedbackView struct {
	CommentID       string    `json:"comment_id"`
	CommentRevision int64     `json:"comment_revision"`
	ContentSHA256   string    `json:"content_sha256"`
	Kind            string    `json:"kind"`
	CandidateID     string    `json:"candidate_id"`
	SourceTaskID    string    `json:"source_task_id"`
	CreatedAt       time.Time `json:"created_at"`
}

type WorkflowAcceptanceView struct {
	ID                   string     `json:"id"`
	CandidateID          string     `json:"candidate_id"`
	State                string     `json:"state"`
	Mode                 string     `json:"mode"`
	ClassificationReason string     `json:"classification_reason,omitempty"`
	RequestedAt          time.Time  `json:"requested_at"`
	AcceptedAt           *time.Time `json:"accepted_at"`
	Blocker              string     `json:"blocker,omitempty"`
	HoldDelivery         bool       `json:"hold_delivery"`
	OutcomeComplete      bool       `json:"outcome_complete"`
	OutcomeCompletedAt   *time.Time `json:"outcome_completed_at"`
	OutcomeTaskID        string     `json:"outcome_task_id,omitempty"`
	OutcomeTaskActive    bool       `json:"outcome_task_active"`
	OutcomePending       bool       `json:"outcome_pending"`
}

type WorkflowDeliveryView struct {
	ID              string     `json:"id"`
	Ordinal         int        `json:"ordinal"`
	PRURL           string     `json:"pr_url"`
	ExpectedHeadSHA string     `json:"expected_head_sha"`
	Action          string     `json:"action"`
	MergeMethod     string     `json:"merge_method,omitempty"`
	Status          string     `json:"status"`
	Retryable       bool       `json:"retryable"`
	AttemptCount    int        `json:"attempt_count"`
	NextAttemptAt   time.Time  `json:"next_attempt_at"`
	LastErrorClass  string     `json:"last_error_class,omitempty"`
	ReadinessDoneAt *time.Time `json:"readiness_done_at"`
	MergedAt        *time.Time `json:"merged_at"`
}

type WorkflowRetainedContextOption struct {
	TaskID    string `json:"task_id"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Kind      string `json:"kind"`
}

type WorkflowAvailableActions struct {
	AcceptHuman              bool `json:"accept_human"`
	Reject                   bool `json:"reject"`
	RequestTrivialAcceptance bool `json:"request_trivial_acceptance"`
	WaiveReview              bool `json:"waive_review"`
	HoldDelivery             bool `json:"hold_delivery"`
	ReleaseDelivery          bool `json:"release_delivery"`
	CompleteOutcome          bool `json:"complete_outcome"`
	RetryOutcome             bool `json:"retry_outcome"`
}

type WorkflowDeliveryPreview struct {
	Action        string `json:"action"`
	MergeMethod   string `json:"merge_method,omitempty"`
	RequiresOrder bool   `json:"requires_order"`
}

type WorkflowExceptionView struct {
	ID                     string         `json:"id"`
	CandidateID            string         `json:"candidate_id"`
	Scope                  string         `json:"scope"`
	GrantDetails           map[string]any `json:"grant_details"`
	ActorType              string         `json:"actor_type"`
	ActorID                string         `json:"actor_id"`
	Reason                 string         `json:"reason"`
	Consequences           string         `json:"consequences"`
	BasePolicyVersion      string         `json:"base_policy_version"`
	CreatedAt              time.Time      `json:"created_at"`
	RevokedAt              *time.Time     `json:"revoked_at"`
	RevocationReason       string         `json:"revocation_reason,omitempty"`
	RevocationConsequences string         `json:"revocation_consequences,omitempty"`
	RevokedByType          string         `json:"revoked_by_type,omitempty"`
	RevokedByID            string         `json:"revoked_by_id,omitempty"`
}

type WorkflowState struct {
	IssueID                string                          `json:"issue_id"`
	IssueRevision          int64                           `json:"issue_revision"`
	Frozen                 bool                            `json:"frozen"`
	PolicyVersion          string                          `json:"policy_version"`
	AcceptedStatusKey      string                          `json:"accepted_status_key,omitempty"`
	Candidate              *WorkflowCandidateView          `json:"candidate"`
	Reviews                []WorkflowReviewView            `json:"reviews"`
	Acceptance             *WorkflowAcceptanceView         `json:"acceptance"`
	Feedback               *WorkflowFeedbackView           `json:"feedback,omitempty"`
	Delivery               []WorkflowDeliveryView          `json:"delivery"`
	RetainedContextOptions []WorkflowRetainedContextOption `json:"retained_context_options"`
	AcceptanceBlockers     []string                        `json:"acceptance_blockers"`
	DeliveryPreview        *WorkflowDeliveryPreview        `json:"delivery_preview"`
	Exceptions             []WorkflowExceptionView         `json:"exceptions"`
	AvailableActions       WorkflowAvailableActions        `json:"available_actions"`
}

type WorkflowReviewInput struct {
	CandidateID  string   `json:"candidate_id"`
	Verdict      string   `json:"verdict"`
	PRReviewURLs []string `json:"pr_review_urls"`
}

type WorkflowAcceptanceInput struct {
	CandidateID          string   `json:"candidate_id"`
	ExpectedRevision     int64    `json:"expected_revision"`
	ClassificationReason string   `json:"classification_reason,omitempty"`
	MergeOrderPRURLs     []string `json:"merge_order_pr_urls,omitempty"`
	OutcomeComplete      *bool    `json:"outcome_complete,omitempty"`
	HoldDelivery         bool     `json:"hold_delivery,omitempty"`
}

type WorkflowRejectionInput struct {
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Kind             string `json:"kind"`
	Reason           string `json:"reason"`
	ResumeTaskID     string `json:"resume_task_id,omitempty"`
}

// WorkflowFeedbackContinuationInput makes an existing human comment actionable
// for the current candidate. The comment is resolved from the caller's running
// task, so its content and author cannot be supplied by the agent.
type WorkflowFeedbackContinuationInput struct {
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	CommentID        string `json:"comment_id"`
	Kind             string `json:"kind"`
}

type WorkflowExceptionInput struct {
	CandidateID      string         `json:"candidate_id"`
	ExpectedRevision int64          `json:"expected_revision"`
	Scope            string         `json:"scope"`
	GrantDetails     map[string]any `json:"grant_details"`
	Reason           string         `json:"reason"`
	Consequences     string         `json:"consequences"`
}

type WorkflowExceptionRevokeInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
	Consequences     string `json:"consequences"`
}
