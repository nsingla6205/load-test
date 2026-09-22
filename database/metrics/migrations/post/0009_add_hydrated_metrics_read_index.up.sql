-- Supports hydrated metrics aggregation reads filtered by resource type,
-- measured type, and timestamp, with id as the stable pagination tie-breaker.
CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_read
    ON hydrated_metrics (resource_type, measured_type, metric_timestamp, id);
