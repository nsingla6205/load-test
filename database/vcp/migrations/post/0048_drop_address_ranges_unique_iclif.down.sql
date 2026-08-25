-- Restore the one-interclusterLIF-per-VPC constraint from 0032.
CREATE UNIQUE INDEX IF NOT EXISTS idx_address_ranges_unique_iclif
    ON address_ranges(vpc_name, host_project_number, lif_type)
    WHERE deleted_at IS NULL AND lif_type = 'interclusterLIF';
