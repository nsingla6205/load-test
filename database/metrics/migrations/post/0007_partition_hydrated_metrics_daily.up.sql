-- Phase 1 (VCP Billing Backfilling): convert hydrated_metrics to day-wise RANGE
-- partitioning on metric_timestamp.
--
-- Approved plan: https://netapp.atlassian.net/wiki/spaces/~712020c38fa12b6eae4dd6a76167965dc5d27c/pages/645418159
--
-- Cutover (no data loss):
--   1. Create partitioned parent + daily partitions covering existing data + ahead buffer
--   2. ACCESS EXCLUSIVE lock on source, copy all rows, atomic rename swap
--   3. Keep hydrated_metrics_legacy_pre_partition for staging rollback verification
--
-- Idempotent: no-op if hydrated_metrics is already partitioned.
--
-- Staging verification (partition pruning):
--   EXPLAIN (ANALYZE, BUFFERS)
--   SELECT count(*) FROM hydrated_metrics
--   WHERE metric_timestamp >= DATE '2026-08-11'
--     AND metric_timestamp <  DATE '2026-08-12';
--   Expect: only hydrated_metrics_YYYY_MM_DD scanned (Partitions removed: ...).

CREATE OR REPLACE FUNCTION ensure_hydrated_metrics_day_partitions(
    p_parent regclass,
    p_start_day date,
    p_end_day date
) RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    d date;
    part_name text;
    from_ts timestamptz;
    to_ts timestamptz;
BEGIN
    IF p_start_day IS NULL OR p_end_day IS NULL OR p_end_day < p_start_day THEN
        RAISE EXCEPTION 'invalid partition range: start=%, end=%', p_start_day, p_end_day;
    END IF;

    d := p_start_day;
    WHILE d <= p_end_day LOOP
        part_name := format('hydrated_metrics_%s', to_char(d, 'YYYY_MM_DD'));
        from_ts := d::timestamptz;
        to_ts := (d + 1)::timestamptz;

        IF NOT EXISTS (
            SELECT 1
            FROM pg_class c
            JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE c.relname = part_name
              AND n.nspname = ANY (current_schemas(true))
        ) THEN
            EXECUTE format(
                'CREATE TABLE %I PARTITION OF %s FOR VALUES FROM (%L) TO (%L)',
                part_name,
                p_parent,
                from_ts,
                to_ts
            );
        END IF;

        d := d + 1;
    END LOOP;
END;
$$;

DO $$
DECLARE
    is_partitioned boolean;
    table_exists boolean;
    min_ts timestamptz;
    max_ts timestamptz;
    start_day date;
    end_day date;
    row_count bigint;
    copied_count bigint;
    legacy_idx record;
BEGIN
    SELECT EXISTS (
        SELECT 1
        FROM information_schema.tables
        WHERE table_schema = current_schema()
          AND table_name = 'hydrated_metrics'
    ) INTO table_exists;

    IF NOT table_exists THEN
        RAISE NOTICE 'hydrated_metrics does not exist yet; skipping partition cutover';
        RETURN;
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM pg_partitioned_table pt
        JOIN pg_class c ON c.oid = pt.partrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relname = 'hydrated_metrics'
          AND n.nspname = current_schema()
    ) INTO is_partitioned;

    IF is_partitioned THEN
        RAISE NOTICE 'hydrated_metrics already partitioned; ensuring forward partitions only';
        PERFORM ensure_hydrated_metrics_day_partitions(
            'hydrated_metrics'::regclass,
            (CURRENT_DATE - 1),
            (CURRENT_DATE + 7)
        );
        RETURN;
    END IF;

    -- Create partitioned parent mirroring current heap columns (no heap PK/indexes).
    CREATE TABLE hydrated_metrics_partitioned (
        LIKE hydrated_metrics
            INCLUDING DEFAULTS
            INCLUDING IDENTITY
            INCLUDING STORAGE
            INCLUDING COMMENTS
    ) PARTITION BY RANGE (metric_timestamp);

    ALTER TABLE hydrated_metrics_partitioned
        ADD CONSTRAINT hydrated_metrics_partitioned_pkey
        PRIMARY KEY (id, metric_timestamp);

    -- Preserve serial/identity sequence for inserts after cutover.
    IF EXISTS (
        SELECT 1 FROM pg_class WHERE relname = 'hydrated_metrics_id_seq' AND relkind = 'S'
    ) THEN
        EXECUTE $seq$
            ALTER TABLE hydrated_metrics_partitioned
                ALTER COLUMN id SET DEFAULT nextval('hydrated_metrics_id_seq')
        $seq$;
    END IF;

    SELECT MIN(metric_timestamp), MAX(metric_timestamp), COUNT(*)
    INTO min_ts, max_ts, row_count
    FROM hydrated_metrics;

    IF min_ts IS NULL THEN
        start_day := CURRENT_DATE - 30;
        end_day := CURRENT_DATE + 7;
    ELSE
        start_day := LEAST(min_ts::date, CURRENT_DATE - 30);
        end_day := GREATEST(max_ts::date, CURRENT_DATE) + 7;
    END IF;

    PERFORM ensure_hydrated_metrics_day_partitions(
        'hydrated_metrics_partitioned'::regclass,
        start_day,
        end_day
    );

    -- Recreate indexes on the partitioned parent (propagates to partitions).
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_partitioned_metric_timestamp
        ON hydrated_metrics_partitioned (metric_timestamp);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_partitioned_measured_type
        ON hydrated_metrics_partitioned (measured_type);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_partitioned_resource_type
        ON hydrated_metrics_partitioned (resource_type);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_partitioned_resource_name
        ON hydrated_metrics_partitioned (resource_name);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_partitioned_consumer_id
        ON hydrated_metrics_partitioned (consumer_id);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_partitioned_location
        ON hydrated_metrics_partitioned (location);
    CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_partitioned_deployment_name
        ON hydrated_metrics_partitioned (deployment_name);

    -- Exclusive lock + full copy + rename keeps writers from racing the cutover.
    LOCK TABLE hydrated_metrics IN ACCESS EXCLUSIVE MODE;

    INSERT INTO hydrated_metrics_partitioned (
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

    GET DIAGNOSTICS copied_count = ROW_COUNT;

    IF copied_count IS DISTINCT FROM row_count THEN
        RAISE EXCEPTION
            'hydrated_metrics cutover row-count mismatch: source=% copied=%',
            row_count, copied_count;
    END IF;

    ALTER TABLE hydrated_metrics RENAME TO hydrated_metrics_legacy_pre_partition;

    -- Avoid name collisions: table rename does not rename PK/index identifiers.
    IF EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'hydrated_metrics_pkey'
          AND conrelid = 'hydrated_metrics_legacy_pre_partition'::regclass
    ) THEN
        ALTER TABLE hydrated_metrics_legacy_pre_partition
            RENAME CONSTRAINT hydrated_metrics_pkey TO hydrated_metrics_legacy_pre_partition_pkey;
    END IF;

    FOR legacy_idx IN
        SELECT c.relname AS index_name
        FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_class t ON t.oid = i.indrelid
        JOIN pg_namespace n ON n.oid = t.relnamespace
        WHERE t.relname = 'hydrated_metrics_legacy_pre_partition'
          AND n.nspname = current_schema()
          AND NOT i.indisprimary
    LOOP
        EXECUTE format(
            'ALTER INDEX %I RENAME TO %I',
            legacy_idx.index_name,
            left(legacy_idx.index_name || '_legacy', 63)
        );
    END LOOP;

    ALTER TABLE hydrated_metrics_partitioned RENAME TO hydrated_metrics;

    ALTER TABLE hydrated_metrics
        RENAME CONSTRAINT hydrated_metrics_partitioned_pkey TO hydrated_metrics_pkey;

    ALTER INDEX IF EXISTS idx_hydrated_metrics_partitioned_metric_timestamp
        RENAME TO idx_hydrated_metrics_metric_timestamp;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_partitioned_measured_type
        RENAME TO idx_hydrated_metrics_measured_type;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_partitioned_resource_type
        RENAME TO idx_hydrated_metrics_resource_type;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_partitioned_resource_name
        RENAME TO idx_hydrated_metrics_resource_name;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_partitioned_consumer_id
        RENAME TO idx_hydrated_metrics_consumer_id;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_partitioned_location
        RENAME TO idx_hydrated_metrics_location;
    ALTER INDEX IF EXISTS idx_hydrated_metrics_partitioned_deployment_name
        RENAME TO idx_hydrated_metrics_deployment_name;

    IF EXISTS (
        SELECT 1 FROM pg_class WHERE relname = 'hydrated_metrics_id_seq' AND relkind = 'S'
    ) THEN
        EXECUTE $seq$
            ALTER SEQUENCE hydrated_metrics_id_seq OWNED BY hydrated_metrics.id
        $seq$;
        EXECUTE $seq$
            SELECT setval(
                'hydrated_metrics_id_seq',
                GREATEST(
                    (SELECT COALESCE(MAX(id), 1) FROM hydrated_metrics),
                    (SELECT last_value FROM hydrated_metrics_id_seq)
                ),
                true
            )
        $seq$;
    END IF;

    RAISE NOTICE
        'hydrated_metrics partitioned cutover complete: rows=%, partitions=% to % (legacy table retained as hydrated_metrics_legacy_pre_partition)',
        copied_count, start_day, end_day;
END;
$$;
