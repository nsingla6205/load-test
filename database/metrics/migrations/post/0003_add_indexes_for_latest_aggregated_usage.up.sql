-- Add composite index for GetLatestAggregatedUsageForAllResources query optimization
-- 
-- Query pattern being optimized:
--   SELECT DISTINCT ON (resource_uuid, measured_type)
--     resource_uuid, measured_type, last_counter_value, last_transfer_type
--   FROM aggregated_usages
--   WHERE aggregation_type = ? AND last_counter_value IS NOT NULL
--     AND (resource_uuid, measured_type) > (?, ?)  -- keyset cursor; omitted on first page
--   ORDER BY resource_uuid, measured_type, created_at DESC
--   LIMIT ?
--
-- Single optimal index strategy:
-- This composite index covers the entire query pattern:
-- 1. WHERE clause: aggregation_type (first column enables index scan filtering)
-- 2. DISTINCT ON grouping: resource_uuid, measured_type (columns 2-3)
-- 3. ORDER BY created_at DESC for selecting the latest row per group (column 4)
--
-- Column order is critical: aggregation_type first (for WHERE filtering), then resource_uuid and measured_type
-- (for DISTINCT ON grouping), then created_at DESC (for picking the latest row per group).
--
-- Note: The query filters by last_counter_value IS NOT NULL, but we don't use a partial index condition
-- to keep the index simpler and allow it to be used by other queries if needed.
CREATE INDEX IF NOT EXISTS idx_aggregated_usages_latest_for_resources 
ON aggregated_usages (aggregation_type, resource_uuid, measured_type, created_at DESC);

