-- Migration: Replace volume_performance_groups.is_shared (bool) with allocation_type (enum string)

ALTER TABLE volume_performance_groups
    ADD COLUMN IF NOT EXISTS allocation_type VARCHAR(32);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'volume_performance_groups'
          AND column_name = 'is_shared'
    ) THEN
        UPDATE volume_performance_groups
        SET allocation_type = CASE WHEN is_shared THEN 'SHARED' ELSE 'PER_VOLUME' END
        WHERE allocation_type IS NULL;
    ELSE
        -- Fresh schemas may already be allocation_type-only.
        UPDATE volume_performance_groups
        SET allocation_type = 'SHARED'
        WHERE allocation_type IS NULL;
    END IF;
END $$;

ALTER TABLE volume_performance_groups
    ALTER COLUMN allocation_type SET NOT NULL;

-- Update pool_views to allocation_type semantics before dropping is_shared.
-- DROP + CREATE required because AutoMigrate may add columns to pools before this runs,
-- changing p.* ordinal positions and breaking CREATE OR REPLACE VIEW (SQLSTATE 42P16).
DROP VIEW IF EXISTS pool_views;
CREATE VIEW pool_views AS
SELECT
    p.*,
    coalesce(
        CASE
            WHEN p.qos_type = 'manual' THEN
                -- Sum throughput for non-shared VPGs (each volume gets full VPG throughput)
                COALESCE(SUM(vpg.throughput_mibps) FILTER (WHERE vpg.allocation_type = 'PER_VOLUME'), 0) +
                -- Sum throughput for shared VPGs (divided by volume count per VPG)
                COALESCE(SUM(
                    CASE
                        WHEN vpg.allocation_type = 'SHARED' AND vpg.id IS NOT NULL THEN
                            vpg.throughput_mibps / NULLIF(vpg_counts.active_volume_count, 0)
                        ELSE 0
                    END
                ), 0)
            ELSE sum(v.throughput)
        END,
        0.0
    ) as throughput,
    coalesce(
        CASE
            WHEN p.qos_type = 'manual' THEN
                -- Sum IOPS for non-shared VPGs (each volume gets full VPG IOPS)
                COALESCE(SUM(vpg.iops) FILTER (WHERE vpg.allocation_type = 'PER_VOLUME'), 0) +
                -- Sum IOPS for shared VPGs (divided by volume count per VPG)
                COALESCE(SUM(
                    CASE
                        WHEN vpg.allocation_type = 'SHARED' AND vpg.id IS NOT NULL THEN
                            vpg.iops / NULLIF(vpg_counts.active_volume_count, 0)
                        ELSE 0
                    END
                ), 0)
            ELSE 0
        END,
        0
    ) as iops,
    coalesce(greatest(0, sum(v.size_in_bytes - v.clones_shared_bytes)), 0) as quota_in_bytes,
    coalesce(count(v.id) filter (where v.clones_shared_bytes > 0), 0) as thin_clone_volume_count,
    count(v.id) as volume_count
FROM pools p
    LEFT JOIN volumes v on v.pool_id = p.id
    and v.account_id = p.account_id
    and v.deleted_at is null
    LEFT JOIN volume_performance_groups vpg on vpg.id = v.volume_performance_group_id
    LEFT JOIN (
        SELECT volume_performance_group_id, COUNT(*) AS active_volume_count
        FROM volumes
        WHERE deleted_at IS NULL
        GROUP BY volume_performance_group_id
    ) vpg_counts ON vpg_counts.volume_performance_group_id = vpg.id
GROUP BY
    p.id,
    p.name;

ALTER TABLE volume_performance_groups
    DROP COLUMN IF EXISTS is_shared;
