-- Adds an index on snapshots.volume_id for snapshot lookups by volume.
-- If this fails dirty, drop invalid idx_snapshots_volume_id, then follow database/vcp/migrations/README.md Dirty State Recovery and re-run.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_snapshots_volume_id
    ON snapshots (volume_id);
