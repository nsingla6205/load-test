-- Backfill allocation_type values before AutoMigrate enforces NOT NULL.
-- This must be a new migration version so already-upgraded regions at pre-version 7 can pick it up.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.tables
        WHERE table_schema = current_schema()
          AND table_name = 'volume_performance_groups'
    ) AND EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'volume_performance_groups'
          AND column_name = 'allocation_type'
    ) THEN
        IF EXISTS (
            SELECT 1
            FROM information_schema.columns
            WHERE table_schema = current_schema()
              AND table_name = 'volume_performance_groups'
              AND column_name = 'is_shared'
        ) THEN
            UPDATE volume_performance_groups
            SET allocation_type = CASE
                WHEN is_shared THEN 'SHARED'
                ELSE 'PER_VOLUME'
            END
            WHERE allocation_type IS NULL;
        ELSE
            -- If is_shared is already dropped, keep remaining NULLs deterministic.
            UPDATE volume_performance_groups
            SET allocation_type = 'SHARED'
            WHERE allocation_type IS NULL;
        END IF;
    END IF;
END;
$$;
