-- Convert backup_chain_histories to daily RANGE partitions on created_at.
-- Tombstone retention remains based on deleted_at. Runtime cleanup deletes
-- expired tombstones first and drops only empty old partitions so active rows
-- with an old created_at are never lost.

CREATE OR REPLACE FUNCTION ensure_backup_chain_histories_day_partitions(
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
        part_name := format('backup_chain_histories_%s', to_char(d, 'YYYY_MM_DD'));
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
    SELECT to_regclass(format('%I.backup_chain_histories', current_schema())) IS NOT NULL
    INTO table_exists;
    IF NOT table_exists THEN
        RAISE NOTICE 'backup_chain_histories does not exist; skipping partition cutover';
        RETURN;
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM pg_partitioned_table pt
        JOIN pg_class c ON c.oid = pt.partrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relname = 'backup_chain_histories'
          AND n.nspname = current_schema()
    ) INTO is_partitioned;

    IF is_partitioned THEN
        PERFORM ensure_backup_chain_histories_day_partitions(
            'backup_chain_histories'::regclass,
            utc_today - 1,
            utc_today + 7
        );
        RETURN;
    END IF;

    CREATE TABLE backup_chain_histories_partitioned (
        LIKE backup_chain_histories
            INCLUDING DEFAULTS
            INCLUDING IDENTITY
            INCLUDING STORAGE
            INCLUDING COMMENTS
    ) PARTITION BY RANGE (created_at);

    ALTER TABLE backup_chain_histories_partitioned
        ADD CONSTRAINT backup_chain_histories_partitioned_pkey
        PRIMARY KEY (id, created_at);

    IF to_regclass('backup_chain_histories_id_seq') IS NOT NULL THEN
        ALTER TABLE backup_chain_histories_partitioned
            ALTER COLUMN id SET DEFAULT nextval('backup_chain_histories_id_seq');
    END IF;

    SELECT MIN(created_at), MAX(created_at)
    INTO min_ts, max_ts
    FROM backup_chain_histories;

    IF min_ts IS NULL THEN
        start_day := utc_today - 30;
        end_day := utc_today + 7;
    ELSE
        start_day := LEAST((min_ts AT TIME ZONE 'UTC')::date, utc_today - 30);
        end_day := GREATEST((max_ts AT TIME ZONE 'UTC')::date, utc_today) + 7;
    END IF;

    PERFORM ensure_backup_chain_histories_day_partitions(
        'backup_chain_histories_partitioned'::regclass,
        start_day,
        end_day
    );

    -- The partition key must be present in every parent-level unique key.
    -- BackupChainHistory overrides BaseModel.UUID so AutoMigrate does not try
    -- to replace this with an unsupported global UNIQUE(uuid) constraint.
    CREATE UNIQUE INDEX uni_backup_chain_histories_partitioned_uuid
        ON backup_chain_histories_partitioned (uuid, created_at);
    CREATE INDEX idx_backup_chain_histories_partitioned_deleted_at
        ON backup_chain_histories_partitioned (deleted_at);
    CREATE INDEX idx_backup_chain_histories_partitioned_resource_created
        ON backup_chain_histories_partitioned (resource_uuid, created_at);
    CREATE INDEX idx_backup_chain_histories_partitioned_created_deleted
        ON backup_chain_histories_partitioned (created_at, deleted_at);

    LOCK TABLE backup_chain_histories IN ACCESS EXCLUSIVE MODE;

    -- Re-read the range and count while writers are excluded. A write between
    -- the initial sizing read and this lock may require another edge partition.
    SELECT MIN(created_at), MAX(created_at), COUNT(*)
    INTO min_ts, max_ts, source_count
    FROM backup_chain_histories;
    IF min_ts IS NULL THEN
        start_day := utc_today - 30;
        end_day := utc_today + 7;
    ELSE
        start_day := LEAST((min_ts AT TIME ZONE 'UTC')::date, utc_today - 30);
        end_day := GREATEST((max_ts AT TIME ZONE 'UTC')::date, utc_today) + 7;
    END IF;
    PERFORM ensure_backup_chain_histories_day_partitions(
        'backup_chain_histories_partitioned'::regclass,
        start_day,
        end_day
    );

    INSERT INTO backup_chain_histories_partitioned
    SELECT * FROM backup_chain_histories;
    GET DIAGNOSTICS copied_count = ROW_COUNT;

    IF copied_count IS DISTINCT FROM source_count THEN
        RAISE EXCEPTION
            'backup_chain_histories cutover row-count mismatch: source=% copied=%',
            source_count, copied_count;
    END IF;

    ALTER TABLE backup_chain_histories
        RENAME TO backup_chain_histories_legacy_pre_partition;

    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'backup_chain_histories_pkey'
          AND conrelid = 'backup_chain_histories_legacy_pre_partition'::regclass
    ) THEN
        ALTER TABLE backup_chain_histories_legacy_pre_partition
            RENAME CONSTRAINT backup_chain_histories_pkey
            TO backup_chain_histories_legacy_pre_partition_pkey;
    END IF;

    FOR legacy_idx IN
        SELECT c.relname AS index_name
        FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_class t ON t.oid = i.indrelid
        JOIN pg_namespace n ON n.oid = t.relnamespace
        WHERE t.relname = 'backup_chain_histories_legacy_pre_partition'
          AND n.nspname = current_schema()
          AND NOT i.indisprimary
    LOOP
        EXECUTE format(
            'ALTER INDEX %I RENAME TO %I',
            legacy_idx.index_name,
            left(legacy_idx.index_name || '_legacy', 63)
        );
    END LOOP;

    ALTER TABLE backup_chain_histories_partitioned
        RENAME TO backup_chain_histories;
    ALTER TABLE backup_chain_histories
        RENAME CONSTRAINT backup_chain_histories_partitioned_pkey
        TO backup_chain_histories_pkey;
    ALTER INDEX uni_backup_chain_histories_partitioned_uuid
        RENAME TO uni_backup_chain_histories_uuid;
    ALTER INDEX idx_backup_chain_histories_partitioned_deleted_at
        RENAME TO idx_backup_chain_histories_deleted_at;
    ALTER INDEX idx_backup_chain_histories_partitioned_resource_created
        RENAME TO idx_backup_chain_histories_resource_created;
    ALTER INDEX idx_backup_chain_histories_partitioned_created_deleted
        RENAME TO idx_backup_chain_histories_created_deleted;

    IF to_regclass('backup_chain_histories_id_seq') IS NOT NULL THEN
        ALTER SEQUENCE backup_chain_histories_id_seq
            OWNED BY backup_chain_histories.id;
        PERFORM setval(
            'backup_chain_histories_id_seq',
            GREATEST(
                (SELECT COALESCE(MAX(id), 1) FROM backup_chain_histories),
                (SELECT last_value FROM backup_chain_histories_id_seq)
            ),
            true
        );
    END IF;

    RAISE NOTICE
        'backup_chain_histories partition cutover complete: rows=%, partitions=% to %',
        copied_count, start_day, end_day;
END;
$$;
