package handler

import (
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestIssueChildProjectionsExcludeWorkflowArchive(t *testing.T) {
	parentID := dbfx.Issue(t, "Workflow projection parent")
	childID := dbfx.Issue(t, "Workflow projection child", testutil.Cols{
		"parent_issue_id": parentID,
		"workflow_policy": testutil.Raw(`'{"large_snapshot":"stored separately from list results"}'::jsonb`),
	})
	ctx := t.Context()
	children, err := testHandler.Queries.ListChildIssues(ctx, parseUUID(parentID))
	if err != nil || len(children) != 1 {
		t.Fatalf("child list: %+v, %v", children, err)
	}
	if uuidToString(children[0].ID) != childID || children[0].Title != "Workflow projection child" || len(children[0].WorkflowPolicy) != 0 {
		t.Fatalf("child projection includes archive or loses identity: %+v", children[0])
	}
	batched, err := testHandler.Queries.ListChildrenByParents(ctx, db.ListChildrenByParentsParams{
		WorkspaceID: parseUUID(testWorkspaceID), ParentIds: []pgtype.UUID{parseUUID(parentID)},
	})
	if err != nil || len(batched) != 1 || len(batched[0].WorkflowPolicy) != 0 || uuidToString(batched[0].ID) != childID {
		t.Fatalf("batched projection: %+v, %v", batched, err)
	}
	var wire struct {
		Issues []IssueResponse `json:"issues"`
	}
	testutil.Call(t, testHandler.ListChildIssues,
		withURLParam(newRequest(http.MethodGet, "/api/issues/"+parentID+"/children", nil), "id", parentID)).
		Want(http.StatusOK).JSON(&wire)
	if len(wire.Issues) != 1 || wire.Issues[0].WorkflowPolicyPresent == nil || !*wire.Issues[0].WorkflowPolicyPresent {
		t.Fatalf("child response lost policy presence: %+v", wire.Issues)
	}
	// Projection is not a mutation: the dedicated policy reader retains the
	// original bytes for claims and explicit policy inspection.
	raw, err := testHandler.Queries.GetIssueWorkflowPolicy(ctx, db.GetIssueWorkflowPolicyParams{
		ID: parseUUID(childID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil || len(raw) == 0 {
		t.Fatalf("stored archive disappeared: %s, %v", raw, err)
	}
}
