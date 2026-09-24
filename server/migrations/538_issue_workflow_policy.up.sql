-- An issue enters the versioned workflow only through explicit enrollment.
-- The complete skill bundle is stored on the issue so source edits, removal,
-- and agent replacement changes cannot reinterpret later runs.
ALTER TABLE issue ADD COLUMN workflow_policy JSONB;
