CREATE INDEX IF NOT EXISTS idx_lifs_pool_id ON lifs (pool_id) WHERE deleted_at IS NULL;
