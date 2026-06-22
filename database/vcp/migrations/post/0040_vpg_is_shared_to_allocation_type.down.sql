-- Rollback: Restore volume_performance_groups.is_shared from allocation_type

ALTER TABLE volume_performance_groups
    ADD COLUMN IF NOT EXISTS is_shared BOOLEAN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'volume_performance_groups'
          AND column_name = 'allocation_type'
    ) THEN
        UPDATE volume_performance_groups
        SET is_shared = CASE WHEN allocation_type = 'PER_VOLUME' THEN false ELSE true END
        WHERE is_shared IS NULL;
    ELSE
        UPDATE volume_performance_groups
        SET is_shared = true
        WHERE is_shared IS NULL;
    END IF;
END $$;

ALTER TABLE volume_performance_groups
    ALTER COLUMN is_shared SET NOT NULL;

-- Strict rollback symmetry: restore pool_views to is_shared semantics before
-- dropping allocation_type.
-- DROP + CREATE required for the same reason as the up migration: AutoMigrate
-- may have added columns to pools, making p.* ordinal positions differ from the
-- existing view — CREATE OR REPLACE VIEW would fail with SQLSTATE 42P16.
DROP VIEW IF EXISTS pool_views;
CREATE VIEW pool_views AS
SELECT
    p.*,
    coalesce(
        CASE
            WHEN p.qos_type = 'manual' THEN
                -- Sum throughput for non-shared VPGs (each volume gets full VPG throughput)
                COALESCE(SUM(vpg.throughput_mibps) FILTER (WHERE vpg.is_shared = false), 0) +
                -- Sum throughput for shared VPGs (divided by volume count per VPG)
                COALESCE(SUM(
                    CASE
                        WHEN vpg.is_shared = true AND vpg.id IS NOT NULL THEN
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
                COALESCE(SUM(vpg.iops) FILTER (WHERE vpg.is_shared = false), 0) +
                -- Sum IOPS for shared VPGs (divided by volume count per VPG)
                COALESCE(SUM(
                    CASE
                        WHEN vpg.is_shared = true AND vpg.id IS NOT NULL THEN
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
    DROP COLUMN IF EXISTS allocation_type;
