-- Rebuild aggregated_usages as an ordinary heap. The partitioned source is
-- dropped only after row-count verification and the atomic name swap.
DO $$
DECLARE
    is_partitioned boolean;
    source_count bigint;
    copied_count bigint;
BEGIN
    SELECT EXISTS (
        SELECT 1
        FROM pg_partitioned_table pt
        JOIN pg_class c ON c.oid = pt.partrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relname = 'aggregated_usages'
          AND n.nspname = current_schema()
    ) INTO is_partitioned;
    IF NOT is_partitioned THEN
        RETURN;
    END IF;

    CREATE TABLE aggregated_usages_heap_rollback (
        LIKE aggregated_usages
            INCLUDING DEFAULTS
            INCLUDING IDENTITY
            INCLUDING STORAGE
            INCLUDING COMMENTS
    );
    ALTER TABLE aggregated_usages_heap_rollback
        ADD CONSTRAINT aggregated_usages_heap_rollback_pkey PRIMARY KEY (id);
    CREATE UNIQUE INDEX idx_aggregated_usage_heap_rollback_unique
        ON aggregated_usages_heap_rollback (
            resource_uuid,
            aggregation_end,
            aggregation_start,
            measured_type,
            resource_type
        );
    CREATE INDEX idx_aggregated_usages_heap_rollback_latest
        ON aggregated_usages_heap_rollback (
            aggregation_type,
            resource_uuid,
            measured_type,
            created_at DESC
        );
    CREATE INDEX idx_aggregated_usages_heap_rollback_resource_uuid
        ON aggregated_usages_heap_rollback (resource_uuid);
    CREATE INDEX idx_aggregated_usages_heap_rollback_account_id
        ON aggregated_usages_heap_rollback (account_id);
    CREATE INDEX idx_aggregated_usages_heap_rollback_vendor_customer_id
        ON aggregated_usages_heap_rollback (vendor_customer_id);
    CREATE INDEX idx_aggregated_usages_heap_rollback_aggregation_end
        ON aggregated_usages_heap_rollback (aggregation_end);
    CREATE INDEX idx_aggregated_usages_heap_rollback_aggregation_start
        ON aggregated_usages_heap_rollback (aggregation_start);
    CREATE INDEX idx_aggregated_usages_heap_rollback_measured_type
        ON aggregated_usages_heap_rollback (measured_type);
    CREATE INDEX idx_aggregated_usages_heap_rollback_resource_name
        ON aggregated_usages_heap_rollback (resource_name);
    CREATE INDEX idx_aggregated_usages_heap_rollback_resource_type
        ON aggregated_usages_heap_rollback (resource_type);
    CREATE INDEX idx_aggregated_usages_heap_rollback_region_name
        ON aggregated_usages_heap_rollback (region_name);
    CREATE INDEX idx_aggregated_usages_heap_rollback_billing_mode
        ON aggregated_usages_heap_rollback (billing_mode);

    IF to_regclass('aggregated_usages_id_seq') IS NOT NULL THEN
        ALTER TABLE aggregated_usages_heap_rollback
            ALTER COLUMN id SET DEFAULT nextval('aggregated_usages_id_seq');
    END IF;

    LOCK TABLE aggregated_usages IN ACCESS EXCLUSIVE MODE;
    SELECT COUNT(*) INTO source_count FROM aggregated_usages;
    INSERT INTO aggregated_usages_heap_rollback
    SELECT * FROM aggregated_usages;
    GET DIAGNOSTICS copied_count = ROW_COUNT;
    IF copied_count IS DISTINCT FROM source_count THEN
        RAISE EXCEPTION
            'aggregated_usages rollback row-count mismatch: source=% copied=%',
            source_count, copied_count;
    END IF;

    ALTER TABLE aggregated_usages
        RENAME TO aggregated_usages_partitioned_rollback;
    ALTER TABLE aggregated_usages_partitioned_rollback
        RENAME CONSTRAINT aggregated_usages_pkey
        TO aggregated_usages_partitioned_rollback_pkey;
    ALTER INDEX idx_aggregated_usage_unique
        RENAME TO idx_aggregated_usage_partitioned_rollback_unique;
    ALTER INDEX idx_aggregated_usages_latest_for_resources
        RENAME TO idx_aggregated_usages_partitioned_rollback_latest;
    ALTER INDEX idx_aggregated_usages_resource_uuid
        RENAME TO idx_aggregated_usages_partitioned_rollback_resource_uuid;
    ALTER INDEX idx_aggregated_usages_account_id
        RENAME TO idx_aggregated_usages_partitioned_rollback_account_id;
    ALTER INDEX idx_aggregated_usages_vendor_customer_id
        RENAME TO idx_aggregated_usages_partitioned_rollback_vendor_customer_id;
    ALTER INDEX idx_aggregated_usages_aggregation_end
        RENAME TO idx_aggregated_usages_partitioned_rollback_aggregation_end;
    ALTER INDEX idx_aggregated_usages_aggregation_start
        RENAME TO idx_aggregated_usages_partitioned_rollback_aggregation_start;
    ALTER INDEX idx_aggregated_usages_measured_type
        RENAME TO idx_aggregated_usages_partitioned_rollback_measured_type;
    ALTER INDEX idx_aggregated_usages_resource_name
        RENAME TO idx_aggregated_usages_partitioned_rollback_resource_name;
    ALTER INDEX idx_aggregated_usages_resource_type
        RENAME TO idx_aggregated_usages_partitioned_rollback_resource_type;
    ALTER INDEX idx_aggregated_usages_region_name
        RENAME TO idx_aggregated_usages_partitioned_rollback_region_name;
    ALTER INDEX idx_aggregated_usages_billing_mode
        RENAME TO idx_aggregated_usages_partitioned_rollback_billing_mode;

    ALTER TABLE aggregated_usages_heap_rollback
        RENAME TO aggregated_usages;
    ALTER TABLE aggregated_usages
        RENAME CONSTRAINT aggregated_usages_heap_rollback_pkey
        TO aggregated_usages_pkey;
    ALTER INDEX idx_aggregated_usage_heap_rollback_unique
        RENAME TO idx_aggregated_usage_unique;
    ALTER INDEX idx_aggregated_usages_heap_rollback_latest
        RENAME TO idx_aggregated_usages_latest_for_resources;
    ALTER INDEX idx_aggregated_usages_heap_rollback_resource_uuid
        RENAME TO idx_aggregated_usages_resource_uuid;
    ALTER INDEX idx_aggregated_usages_heap_rollback_account_id
        RENAME TO idx_aggregated_usages_account_id;
    ALTER INDEX idx_aggregated_usages_heap_rollback_vendor_customer_id
        RENAME TO idx_aggregated_usages_vendor_customer_id;
    ALTER INDEX idx_aggregated_usages_heap_rollback_aggregation_end
        RENAME TO idx_aggregated_usages_aggregation_end;
    ALTER INDEX idx_aggregated_usages_heap_rollback_aggregation_start
        RENAME TO idx_aggregated_usages_aggregation_start;
    ALTER INDEX idx_aggregated_usages_heap_rollback_measured_type
        RENAME TO idx_aggregated_usages_measured_type;
    ALTER INDEX idx_aggregated_usages_heap_rollback_resource_name
        RENAME TO idx_aggregated_usages_resource_name;
    ALTER INDEX idx_aggregated_usages_heap_rollback_resource_type
        RENAME TO idx_aggregated_usages_resource_type;
    ALTER INDEX idx_aggregated_usages_heap_rollback_region_name
        RENAME TO idx_aggregated_usages_region_name;
    ALTER INDEX idx_aggregated_usages_heap_rollback_billing_mode
        RENAME TO idx_aggregated_usages_billing_mode;

    IF to_regclass('aggregated_usages_id_seq') IS NOT NULL THEN
        ALTER SEQUENCE aggregated_usages_id_seq OWNED BY aggregated_usages.id;
    END IF;

    DROP TABLE aggregated_usages_partitioned_rollback CASCADE;
    DROP TABLE IF EXISTS aggregated_usages_legacy_pre_partition CASCADE;
END;
$$;
