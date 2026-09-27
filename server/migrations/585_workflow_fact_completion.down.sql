-- Restoring the former completion fences needs explicit ticket reconciliation.
DO $$ BEGIN
    RAISE EXCEPTION 'Migration 585 requires reviewed reconciliation before restoring protocol-based completion fences';
END $$;
