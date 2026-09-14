-- Rebuild backup_chain_histories as an ordinary heap. The partitioned source
-- is dropped only after row-count verification and the atomic name swap.
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
        WHERE c.relname = 'backup_chain_histories'
          AND n.nspname = current_schema()
    ) INTO is_partitioned;
    IF NOT is_partitioned THEN
        RETURN;
    END IF;

    CREATE TABLE backup_chain_histories_heap_rollback (
        LIKE backup_chain_histories
            INCLUDING DEFAULTS
            INCLUDING IDENTITY
            INCLUDING STORAGE
            INCLUDING COMMENTS
    );
    ALTER TABLE backup_chain_histories_heap_rollback
        ADD CONSTRAINT backup_chain_histories_heap_rollback_pkey PRIMARY KEY (id);
    CREATE UNIQUE INDEX uni_backup_chain_histories_heap_rollback_uuid
        ON backup_chain_histories_heap_rollback (uuid);
    CREATE INDEX idx_backup_chain_histories_heap_rollback_deleted_at
        ON backup_chain_histories_heap_rollback (deleted_at);
    CREATE INDEX idx_backup_chain_histories_heap_rollback_resource_created
        ON backup_chain_histories_heap_rollback (resource_uuid, created_at);

    IF to_regclass('backup_chain_histories_id_seq') IS NOT NULL THEN
        ALTER TABLE backup_chain_histories_heap_rollback
            ALTER COLUMN id SET DEFAULT nextval('backup_chain_histories_id_seq');
    END IF;

    LOCK TABLE backup_chain_histories IN ACCESS EXCLUSIVE MODE;
    SELECT COUNT(*) INTO source_count FROM backup_chain_histories;
    INSERT INTO backup_chain_histories_heap_rollback
    SELECT * FROM backup_chain_histories;
    GET DIAGNOSTICS copied_count = ROW_COUNT;
    IF copied_count IS DISTINCT FROM source_count THEN
        RAISE EXCEPTION
            'backup_chain_histories rollback row-count mismatch: source=% copied=%',
            source_count, copied_count;
    END IF;

    ALTER TABLE backup_chain_histories
        RENAME TO backup_chain_histories_partitioned_rollback;
    ALTER TABLE backup_chain_histories_partitioned_rollback
        RENAME CONSTRAINT backup_chain_histories_pkey
        TO backup_chain_histories_partitioned_rollback_pkey;
    ALTER INDEX uni_backup_chain_histories_uuid
        RENAME TO uni_backup_chain_histories_partitioned_rollback_uuid;
    ALTER INDEX idx_backup_chain_histories_deleted_at
        RENAME TO idx_backup_chain_histories_partitioned_rollback_deleted_at;
    ALTER INDEX idx_backup_chain_histories_resource_created
        RENAME TO idx_backup_chain_histories_partitioned_rollback_resource_created;
    ALTER INDEX idx_backup_chain_histories_created_deleted
        RENAME TO idx_backup_chain_histories_partitioned_rollback_created_deleted;

    ALTER TABLE backup_chain_histories_heap_rollback
        RENAME TO backup_chain_histories;
    ALTER TABLE backup_chain_histories
        RENAME CONSTRAINT backup_chain_histories_heap_rollback_pkey
        TO backup_chain_histories_pkey;
    ALTER INDEX uni_backup_chain_histories_heap_rollback_uuid
        RENAME TO uni_backup_chain_histories_uuid;
    ALTER INDEX idx_backup_chain_histories_heap_rollback_deleted_at
        RENAME TO idx_backup_chain_histories_deleted_at;
    ALTER INDEX idx_backup_chain_histories_heap_rollback_resource_created
        RENAME TO idx_backup_chain_histories_resource_created;

    IF to_regclass('backup_chain_histories_id_seq') IS NOT NULL THEN
        ALTER SEQUENCE backup_chain_histories_id_seq
            OWNED BY backup_chain_histories.id;
    END IF;

    DROP TABLE backup_chain_histories_partitioned_rollback CASCADE;
    DROP TABLE IF EXISTS backup_chain_histories_legacy_pre_partition CASCADE;
END;
$$;
