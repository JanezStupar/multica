-- The requested retained source remains auditable even when its provider
-- runtime has since disappeared; the actual continuation mode is explicit.
ALTER TABLE issue_workflow_rejection
    ADD COLUMN context_mode text NOT NULL DEFAULT 'fresh' CHECK (context_mode IN ('fresh','resume')),
    ADD COLUMN continuity_note text;
