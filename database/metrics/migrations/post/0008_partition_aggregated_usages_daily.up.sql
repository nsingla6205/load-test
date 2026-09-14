-- Convert aggregated_usages to daily RANGE partitions on aggregation_end.
-- Retention cleanup can then remove complete days with DROP TABLE.

CREATE OR REPLACE FUNCTION ensure_aggregated_usages_day_partitions(
    p_parent regclass,
    p_start_day date,
    p_end_day date
) RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    d date;
    part_name text;
BEGIN
    IF p_start_day IS NULL OR p_end_day IS NULL OR p_end_day < p_start_day THEN
        RAISE EXCEPTION 'invalid partition range: start=%, end=%', p_start_day, p_end_day;
    END IF;

    d := p_start_day;
    WHILE d <= p_end_day LOOP
        part_name := format('aggregated_usages_%s', to_char(d, 'YYYY_MM_DD'));
        PERFORM pg_advisory_xact_lock(hashtext(part_name));
        IF to_regclass(format('%I.%I', current_schema(), part_name)) IS NULL THEN
            EXECUTE format(
                'CREATE TABLE %I PARTITION OF %s FOR VALUES FROM (%L) TO (%L)',
                part_name,
                p_parent,
                d::timestamp AT TIME ZONE 'UTC',
                (d + 1)::timestamp AT TIME ZONE 'UTC'
            );
        END IF;
        d := d + 1;
    END LOOP;
END;
$$;

DO $$
DECLARE
    table_exists boolean;
    is_partitioned boolean;
    min_ts timestamptz;
    max_ts timestamptz;
    start_day date;
    end_day date;
    utc_today date := (CURRENT_TIMESTAMP AT TIME ZONE 'UTC')::date;
    source_count bigint;
    copied_count bigint;
    legacy_idx record;
BEGIN
    SELECT to_regclass(format('%I.aggregated_usages', current_schema())) IS NOT NULL
    INTO table_exists;
    IF NOT table_exists THEN
        RAISE NOTICE 'aggregated_usages does not exist; skipping partition cutover';
        RETURN;
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM pg_partitioned_table pt
        JOIN pg_class c ON c.oid = pt.partrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relname = 'aggregated_usages'
          AND n.nspname = current_schema()
    ) INTO is_partitioned;
    IF is_partitioned THEN
        PERFORM ensure_aggregated_usages_day_partitions(
            'aggregated_usages'::regclass,
            utc_today - 1,
            utc_today + 7
        );
        RETURN;
    END IF;

    CREATE TABLE aggregated_usages_partitioned (
        LIKE aggregated_usages
            INCLUDING DEFAULTS
            INCLUDING IDENTITY
            INCLUDING STORAGE
            INCLUDING COMMENTS
    ) PARTITION BY RANGE (aggregation_end);

    ALTER TABLE aggregated_usages_partitioned
        ADD CONSTRAINT aggregated_usages_partitioned_pkey
        PRIMARY KEY (id, aggregation_end);

    IF to_regclass('aggregated_usages_id_seq') IS NOT NULL THEN
        ALTER TABLE aggregated_usages_partitioned
            ALTER COLUMN id SET DEFAULT nextval('aggregated_usages_id_seq');
    END IF;

    SELECT MIN(aggregation_end), MAX(aggregation_end)
    INTO min_ts, max_ts
    FROM aggregated_usages;
    IF min_ts IS NULL THEN
        start_day := utc_today - 30;
        end_day := utc_today + 7;
    ELSE
        start_day := LEAST((min_ts AT TIME ZONE 'UTC')::date, utc_today - 30);
        end_day := GREATEST((max_ts AT TIME ZONE 'UTC')::date, utc_today) + 7;
    END IF;

    PERFORM ensure_aggregated_usages_day_partitions(
        'aggregated_usages_partitioned'::regclass,
        start_day,
        end_day
    );

    CREATE UNIQUE INDEX idx_aggregated_usage_partitioned_unique
        ON aggregated_usages_partitioned (
            resource_uuid,
            aggregation_end,
            aggregation_start,
            measured_type,
            resource_type
        );
    CREATE INDEX idx_aggregated_usages_partitioned_latest_for_resources
        ON aggregated_usages_partitioned (
            aggregation_type,
            aggregation_end,
            resource_uuid,
            measured_type,
            created_at DESC
        );
    CREATE INDEX idx_aggregated_usages_partitioned_resource_uuid
        ON aggregated_usages_partitioned (resource_uuid);
    CREATE INDEX idx_aggregated_usages_partitioned_account_id
        ON aggregated_usages_partitioned (account_id);
    CREATE INDEX idx_aggregated_usages_partitioned_vendor_customer_id
        ON aggregated_usages_partitioned (vendor_customer_id);
    CREATE INDEX idx_aggregated_usages_partitioned_aggregation_end
        ON aggregated_usages_partitioned (aggregation_end);
    CREATE INDEX idx_aggregated_usages_partitioned_aggregation_start
        ON aggregated_usages_partitioned (aggregation_start);
    CREATE INDEX idx_aggregated_usages_partitioned_measured_type
        ON aggregated_usages_partitioned (measured_type);
    CREATE INDEX idx_aggregated_usages_partitioned_resource_name
        ON aggregated_usages_partitioned (resource_name);
    CREATE INDEX idx_aggregated_usages_partitioned_resource_type
        ON aggregated_usages_partitioned (resource_type);
    CREATE INDEX idx_aggregated_usages_partitioned_region_name
        ON aggregated_usages_partitioned (region_name);
    CREATE INDEX idx_aggregated_usages_partitioned_billing_mode
        ON aggregated_usages_partitioned (billing_mode);

    LOCK TABLE aggregated_usages IN ACCESS EXCLUSIVE MODE;

    -- Re-read the range and count while writers are excluded. A write between
    -- the initial sizing read and this lock may require another edge partition.
    SELECT MIN(aggregation_end), MAX(aggregation_end), COUNT(*)
    INTO min_ts, max_ts, source_count
    FROM aggregated_usages;
    IF min_ts IS NULL THEN
        start_day := utc_today - 30;
        end_day := utc_today + 7;
    ELSE
        start_day := LEAST((min_ts AT TIME ZONE 'UTC')::date, utc_today - 30);
        end_day := GREATEST((max_ts AT TIME ZONE 'UTC')::date, utc_today) + 7;
    END IF;
    PERFORM ensure_aggregated_usages_day_partitions(
        'aggregated_usages_partitioned'::regclass,
        start_day,
        end_day
    );

    INSERT INTO aggregated_usages_partitioned
    SELECT * FROM aggregated_usages;
    GET DIAGNOSTICS copied_count = ROW_COUNT;
    IF copied_count IS DISTINCT FROM source_count THEN
        RAISE EXCEPTION
            'aggregated_usages cutover row-count mismatch: source=% copied=%',
            source_count, copied_count;
    END IF;

    ALTER TABLE aggregated_usages
        RENAME TO aggregated_usages_legacy_pre_partition;
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'aggregated_usages_pkey'
          AND conrelid = 'aggregated_usages_legacy_pre_partition'::regclass
    ) THEN
        ALTER TABLE aggregated_usages_legacy_pre_partition
            RENAME CONSTRAINT aggregated_usages_pkey
            TO aggregated_usages_legacy_pre_partition_pkey;
    END IF;

    FOR legacy_idx IN
        SELECT c.relname AS index_name
        FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_class t ON t.oid = i.indrelid
        JOIN pg_namespace n ON n.oid = t.relnamespace
        WHERE t.relname = 'aggregated_usages_legacy_pre_partition'
          AND n.nspname = current_schema()
          AND NOT i.indisprimary
    LOOP
        EXECUTE format(
            'ALTER INDEX %I RENAME TO %I',
            legacy_idx.index_name,
            left(legacy_idx.index_name || '_legacy', 63)
        );
    END LOOP;

    ALTER TABLE aggregated_usages_partitioned RENAME TO aggregated_usages;
    ALTER TABLE aggregated_usages
        RENAME CONSTRAINT aggregated_usages_partitioned_pkey
        TO aggregated_usages_pkey;
    ALTER INDEX idx_aggregated_usage_partitioned_unique
        RENAME TO idx_aggregated_usage_unique;
    ALTER INDEX idx_aggregated_usages_partitioned_latest_for_resources
        RENAME TO idx_aggregated_usages_latest_for_resources;
    ALTER INDEX idx_aggregated_usages_partitioned_resource_uuid
        RENAME TO idx_aggregated_usages_resource_uuid;
    ALTER INDEX idx_aggregated_usages_partitioned_account_id
        RENAME TO idx_aggregated_usages_account_id;
    ALTER INDEX idx_aggregated_usages_partitioned_vendor_customer_id
        RENAME TO idx_aggregated_usages_vendor_customer_id;
    ALTER INDEX idx_aggregated_usages_partitioned_aggregation_end
        RENAME TO idx_aggregated_usages_aggregation_end;
    ALTER INDEX idx_aggregated_usages_partitioned_aggregation_start
        RENAME TO idx_aggregated_usages_aggregation_start;
    ALTER INDEX idx_aggregated_usages_partitioned_measured_type
        RENAME TO idx_aggregated_usages_measured_type;
    ALTER INDEX idx_aggregated_usages_partitioned_resource_name
        RENAME TO idx_aggregated_usages_resource_name;
    ALTER INDEX idx_aggregated_usages_partitioned_resource_type
        RENAME TO idx_aggregated_usages_resource_type;
    ALTER INDEX idx_aggregated_usages_partitioned_region_name
        RENAME TO idx_aggregated_usages_region_name;
    ALTER INDEX idx_aggregated_usages_partitioned_billing_mode
        RENAME TO idx_aggregated_usages_billing_mode;

    IF to_regclass('aggregated_usages_id_seq') IS NOT NULL THEN
        ALTER SEQUENCE aggregated_usages_id_seq OWNED BY aggregated_usages.id;
        PERFORM setval(
            'aggregated_usages_id_seq',
            GREATEST(
                (SELECT COALESCE(MAX(id), 1) FROM aggregated_usages),
                (SELECT last_value FROM aggregated_usages_id_seq)
            ),
            true
        );
    END IF;

    RAISE NOTICE
        'aggregated_usages partition cutover complete: rows=%, partitions=% to %',
        copied_count, start_day, end_day;
END;
$$;
