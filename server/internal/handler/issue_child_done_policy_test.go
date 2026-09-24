package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestPinnedParentChildCompletionDefersToSelectedWorkflow(t *testing.T) {
	for _, staged := range []bool{false, true} {
		t.Run(map[bool]string{false: "unstaged", true: "staged"}[staged], func(t *testing.T) {
			parent := dbfx.Issue(t, "Pinned parent", testutil.Cols{"status": "in_progress"})
			source := insertCompleteWorkflowSkill(t, "Selected parent workflow")
			enrollWorkflowPolicy(t, parent, source).Want(http.StatusCreated)
			cols := testutil.Cols{"status": "in_progress", "parent_issue_id": parent}
			if staged {
				cols["stage"] = 1
			}
			child := dbfx.Issue(t, "Useful scoped child", cols)
			updateChildStatus(t, child, "done")
			content, _, _, _ := systemCommentOn(t, parent)
			for _, required := range []string{child, "reached done", "selected ticket workflow", "linked PR reviews"} {
				if !strings.Contains(content, required) {
					t.Errorf("missing coordination fact %q: %s", required, content)
				}
			}
			for _, forbidden := range []string{"in_review", "create that stage", "promote its"} {
				if strings.Contains(content, forbidden) {
					t.Errorf("child notice imposed compiled policy %q: %s", forbidden, content)
				}
			}
		})
	}
}
