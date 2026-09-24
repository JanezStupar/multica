package handler

import (
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// handoffResumeSource chooses a deliberate context boundary, never the latest
// conversation for an agent. Automatic retry continues the actual failed run;
// a first review run must not adopt a previous review's session or workdir.
func handoffResumeSource(task db.AgentTaskQueue) (source pgtype.UUID, handoff bool, err error) {
	var envelope struct {
		Handoff *struct {
			ContextMode  string `json:"context_mode"`
			ResumeTaskID string `json:"resume_task_id"`
		} `json:"workflow_handoff"`
	}
	if len(task.Context) == 0 {
		return task.RerunOfTaskID, false, nil
	}
	if err := json.Unmarshal(task.Context, &envelope); err != nil {
		return source, false, fmt.Errorf("invalid task context: %w", err)
	}
	if envelope.Handoff == nil {
		return task.RerunOfTaskID, false, nil
	}
	h := envelope.Handoff
	if h.ContextMode != "fresh" && h.ContextMode != "resume" {
		return source, true, fmt.Errorf("invalid handoff context mode")
	}
	if h.ContextMode == "fresh" && h.ResumeTaskID != "" {
		return source, true, fmt.Errorf("fresh handoff names a retained context")
	}
	if h.ContextMode == "resume" {
		source, err = util.ParseUUID(h.ResumeTaskID)
		if err != nil || !source.Valid {
			return pgtype.UUID{}, true, fmt.Errorf("invalid handoff resume source")
		}
	}
	if task.RetryOfTaskID.Valid {
		return task.RetryOfTaskID, true, nil
	}
	if !task.ForceFreshSession {
		return source, true, fmt.Errorf("handoff lacks explicit session selection")
	}
	return source, true, nil
}
