-- Rollback for hydrated_metrics day-wise RANGE partitioning.
--
-- Staging rollback procedure:
--   1. Stop / pause telemetry writers briefly (optional but preferred).
--   2. migrate down 1 (metrics post) OR run this file via migrator Rollback.
--   3. Verify:
--        SELECT relkind FROM pg_class WHERE relname = 'hydrated_metrics';  -- expect 'r' (ordinary table)
--        SELECT count(*) FROM hydrated_metrics;
--        SELECT count(*) FROM hydrated_metrics_partitioned_rollback; -- retained for compare, then drop
--   4. Resume writers.
--
-- Preference order:
--   A) Restore hydrated_metrics_legacy_pre_partition if still present (fast, exact pre-cutover bytes)
--   B) Otherwise rebuild a heap table from the partitioned table (still no data loss)

DO $$
DECLARE
    is_partitioned boolean;
    legacy_exists boolean;
    partitioned_count bigint;
    legacy_count bigint;
    heap_count bigint;
BEGIN
    SELECT EXISTS (
        SELECT 1
        FROM pg_partitioned_table pt
        JOIN pg_class c ON c.oid = pt.partrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relname = 'hydrated_metrics'
          AND n.nspname = current_schema()
    ) INTO is_partitioned;

    IF NOT is_partitioned THEN
        RAISE NOTICE 'hydrated_metrics is not partitioned; rollback is a no-op';
        RETURN;
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM information_schema.tables
        WHERE table_schema = current_schema()
          AND table_name = 'hydrated_metrics_legacy_pre_partition'
    ) INTO legacy_exists;

    IF legacy_exists THEN
        SELECT COUNT(*) INTO partitioned_count FROM hydrated_metrics;
        SELECT COUNT(*) INTO legacy_count FROM hydrated_metrics_legacy_pre_partition;

        IF partitioned_count IS DISTINCT FROM legacy_count THEN
            RAISE WARNING
                'legacy row count (%) differs from partitioned (%); restoring legacy anyway as approved rollback path',
                legacy_count, partitioned_count;
        END IF;

        ALTER TABLE hydrated_metrics RENAME TO hydrated_metrics_partitioned_rollback;

        -- Free standard names before restoring legacy.
        IF EXISTS (
            SELECT 1
            FROM pg_constraint
            WHERE conname = 'hydrated_metrics_pkey'
              AND conrelid = 'hydrated_metrics_partitioned_rollback'::regclass
        ) THEN
            ALTER TABLE hydrated_metrics_partitioned_rollback
                RENAME CONSTRAINT hydrated_metrics_pkey TO hydrated_metrics_partitioned_rollback_pkey;
        END IF;

        ALTER TABLE hydrated_metrics_legacy_pre_partition RENAME TO hydrated_metrics;

        IF EXISTS (
            SELECT 1
            FROM pg_constraint
            WHERE conname = 'hydrated_metrics_legacy_pre_partition_pkey'
              AND conrelid = 'hydrated_metrics'::regclass
        ) THEN
            ALTER TABLE hydrated_metrics
                RENAME CONSTRAINT hydrated_metrics_legacy_pre_partition_pkey TO hydrated_metrics_pkey;
        END IF;

        IF EXISTS (
            SELECT 1 FROM pg_class WHERE relname = 'hydrated_metrics_id_seq' AND relkind = 'S'
        ) THEN
            EXECUTE $seq$
                ALTER SEQUENCE hydrated_metrics_id_seq OWNED BY hydrated_metrics.id
            $seq$;
        END IF;

        RAISE NOTICE
            'hydrated_metrics rollback complete via legacy restore (partitioned retained as hydrated_metrics_partitioned_rollback)';
        RETURN;
    END IF;

    -- Fallback: rebuild heap from partitioned data.
    CREATE TABLE hydrated_metrics_heap_rollback (
        LIKE hydrated_metrics
            INCLUDING DEFAULTS
            INCLUDING IDENTITY
            INCLUDING STORAGE
            INCLUDING COMMENTS
    );

    -- Ensure singular primary key on id for pre-partition shape.
    ALTER TABLE hydrated_metrics_heap_rollback
        ADD CONSTRAINT hydrated_metrics_heap_rollback_pkey PRIMARY KEY (id);

    IF EXISTS (
        SELECT 1 FROM pg_class WHERE relname = 'hydrated_metrics_id_seq' AND relkind = 'S'
    ) THEN
        EXECUTE $seq$
            ALTER TABLE hydrated_metrics_heap_rollback
                ALTER COLUMN id SET DEFAULT nextval('hydrated_metrics_id_seq')
        $seq$;
    END IF;

    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_heap_rollback_metric_timestamp
        ON hydrated_metrics_heap_rollback (metric_timestamp);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_heap_rollback_measured_type
        ON hydrated_metrics_heap_rollback (measured_type);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_heap_rollback_resource_type
        ON hydrated_metrics_heap_rollback (resource_type);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_heap_rollback_resource_name
        ON hydrated_metrics_heap_rollback (resource_name);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_heap_rollback_consumer_id
        ON hydrated_metrics_heap_rollback (consumer_id);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_heap_rollback_location
        ON hydrated_metrics_heap_rollback (location);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_heap_rollback_deployment_name
        ON hydrated_metrics_heap_rollback (deployment_name);

    LOCK TABLE hydrated_metrics IN ACCESS EXCLUSIVE MODE;

    INSERT INTO hydrated_metrics_heap_rollback (
        id,
        metric_timestamp,
        measured_type,
        resource_type,
        quantity,
        resource_name,
        consumer_id,
        location,
        metadata,
        deployment_name
    )
    SELECT
        id,
        metric_timestamp,
        measured_type,
        resource_type,
        quantity,
        resource_name,
        consumer_id,
        location,
        metadata,
        deployment_name
    FROM hydrated_metrics;

    GET DIAGNOSTICS heap_count = ROW_COUNT;
    SELECT COUNT(*) INTO partitioned_count FROM hydrated_metrics;

    IF heap_count IS DISTINCT FROM partitioned_count THEN
        RAISE EXCEPTION
            'rollback row-count mismatch: partitioned=% heap=%',
            partitioned_count, heap_count;
    END IF;

    ALTER TABLE hydrated_metrics RENAME TO hydrated_metrics_partitioned_rollback;
    ALTER TABLE hydrated_metrics_heap_rollback RENAME TO hydrated_metrics;

    ALTER TABLE hydrated_metrics
        RENAME CONSTRAINT hydrated_metrics_heap_rollback_pkey TO hydrated_metrics_pkey;

    ALTER INDEX IF EXISTS idx_hydrated_metrics_heap_rollback_metric_timestamp
        RENAME TO idx_hydrated_metrics_metric_timestamp;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_heap_rollback_measured_type
        RENAME TO idx_hydrated_metrics_measured_type;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_heap_rollback_resource_type
        RENAME TO idx_hydrated_metrics_resource_type;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_heap_rollback_resource_name
        RENAME TO idx_hydrated_metrics_resource_name;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_heap_rollback_consumer_id
        RENAME TO idx_hydrated_metrics_consumer_id;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_heap_rollback_location
        RENAME TO idx_hydrated_metrics_location;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_heap_rollback_deployment_name
        RENAME TO idx_hydrated_metrics_deployment_name;

    IF EXISTS (
        SELECT 1 FROM pg_class WHERE relname = 'hydrated_metrics_id_seq' AND relkind = 'S'
    ) THEN
        EXECUTE $seq$
            ALTER SEQUENCE hydrated_metrics_id_seq OWNED BY hydrated_metrics.id
        $seq$;
    END IF;

    RAISE NOTICE
        'hydrated_metrics rollback complete via heap rebuild (rows=%); partitioned retained as hydrated_metrics_partitioned_rollback',
        heap_count;
END;
$$;

-- Drop helper only after successful rollback paths have finished using the parent name.
-- Keep function available for re-applying up migration in staging loops.
