CREATE INDEX IF NOT EXISTS idx_pools_tenancy_cluster_po
ON pools ((cluster_details->>'external_name'), id)
WHERE deleted_at IS NULL
  AND service_level IN ('EXTREME', 'PREMIUM')
  AND cluster_details->>'external_name' IS NOT NULL
  AND cluster_details->>'external_name' <> '';

CREATE INDEX IF NOT EXISTS idx_expert_mode_volumes_tenancy_pool_cursor
ON expert_mode_volumes (pool_id, id)
WHERE deleted_at IS NULL
  AND state <> 'CREATING'
  AND external_uuid IS NOT NULL
  AND external_uuid <> '';
