-- Backfill billing_ledgers for resources that already exist.
--
-- The billing ledger is written on create/update going forward. This migration
-- seeds the current active entry for resources created before that path
-- existed, using the same rules as the billing activities:
--
--   Pass 1 — pools: POOL_ALLOCATED_SIZE
--            VOLUME_POOL, or VOLUME_POOL_REGIONAL_HA when pool_attributes
--            is_regional_ha is true. Quantity is pools.size_in_bytes.
--   Pass 2 — auto-tiered pools: POOL_HOT_TIER_PROVISIONED_SIZE
--            Only when allow_auto_tiering is true and auto_tiering_config is
--            present. Quantity is hot_tier_size_in_bytes (0 when absent).
--   Pass 3 — backup-enabled volumes: BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE
--            VOLUME, or VOLUME_REGIONAL_HA when volume_attributes
--            is_regional_ha is true. Requires backup_chain_bytes > 0.
--            Cross-region vaults are skipped; they are billed via transfer
--            bytes in pass 4. A volume whose backup_vault_id does not resolve
--            is skipped, matching the activity (vault lookup failure does not
--            write a ledger row).
--   Pass 4 — cross-region backups: CBS_CROSS_REGION_VOLUME_BACKUP_TRANSFER_BYTES
--            One active row per volume, taken from the latest non-deleted
--            backup whose vault type is CROSS_REGION. Resource type is BACKUP
--            and resource_uuid is the volume UUID.
--
-- Consumer id is the account name when present, otherwise the pool vendor id
-- (backup transfer rows use attributes.account_identifier). Deployment name
-- comes from the pool. Transfer-byte rows leave deployment_name empty, matching
-- CreateCrossRegionBackupTransferBillingLedger.
--
-- Idempotent: each pass inserts only when no active (deleted_at IS NULL) row
-- already exists for the same resource_uuid, resource_type, and measured_type.
--
-- Failure isolation: each pass is a DO block. A runtime error is downgraded to
-- a warning so schema_migrations_post is not left dirty. Rows that are not
-- backfilled are filled later by the create/update billing activities.

-- Pass 1: pool allocated size.
DO $billing_ledger_pools$
BEGIN
    IF to_regclass('billing_ledgers') IS NULL THEN
        RAISE WARNING 'migration 0052 pass 1 skipped: billing_ledgers does not exist';
        RETURN;
    END IF;

    INSERT INTO billing_ledgers (
        uuid,
        created_at,
        updated_at,
        resource_name,
        quantity,
        resource_uuid,
        consumer_id,
        deployment_name,
        measured_type,
        resource_type
    )
    SELECT
        gen_random_uuid()::text,
        NOW(),
        NOW(),
        COALESCE(p.name, ''),
        p.size_in_bytes::double precision,
        p.uuid,
        COALESCE(NULLIF(a.name, ''), NULLIF(p.vendor_id, ''), ''),
        COALESCE(p.deployment_name, ''),
        'POOL_ALLOCATED_SIZE',
        CASE
            WHEN p.pool_attributes IS NOT NULL
             AND p.pool_attributes->>'is_regional_ha' = 'true'
            THEN 'VOLUME_POOL_REGIONAL_HA'
            ELSE 'VOLUME_POOL'
        END
    FROM pools p
    LEFT JOIN accounts a
        ON a.id = p.account_id
       AND a.deleted_at IS NULL
    WHERE p.deleted_at IS NULL
      AND p.uuid IS NOT NULL
      AND p.uuid <> ''
      AND NOT EXISTS (
          SELECT 1
          FROM billing_ledgers l
          WHERE l.deleted_at IS NULL
            AND l.resource_uuid = p.uuid
            AND l.measured_type = 'POOL_ALLOCATED_SIZE'
            AND l.resource_type = CASE
                WHEN p.pool_attributes IS NOT NULL
                 AND p.pool_attributes->>'is_regional_ha' = 'true'
                THEN 'VOLUME_POOL_REGIONAL_HA'
                ELSE 'VOLUME_POOL'
            END
      );
EXCEPTION
    WHEN OTHERS THEN
        RAISE WARNING 'migration 0052 pass 1 (pool allocated size) skipped due to error [%]: %', SQLSTATE, SQLERRM;
END
$billing_ledger_pools$;

-- Pass 2: pool hot-tier provisioned size for auto-tiered pools.
DO $billing_ledger_pool_hot_tier$
BEGIN
    IF to_regclass('billing_ledgers') IS NULL THEN
        RAISE WARNING 'migration 0052 pass 2 skipped: billing_ledgers does not exist';
        RETURN;
    END IF;

    INSERT INTO billing_ledgers (
        uuid,
        created_at,
        updated_at,
        resource_name,
        quantity,
        resource_uuid,
        consumer_id,
        deployment_name,
        measured_type,
        resource_type
    )
    SELECT
        gen_random_uuid()::text,
        NOW(),
        NOW(),
        COALESCE(p.name, ''),
        CASE
            WHEN COALESCE(p.auto_tiering_config->>'hot_tier_size_in_bytes', '') ~ '^[0-9]{1,18}$'
            THEN (p.auto_tiering_config->>'hot_tier_size_in_bytes')::double precision
            ELSE 0
        END,
        p.uuid,
        COALESCE(NULLIF(a.name, ''), NULLIF(p.vendor_id, ''), ''),
        COALESCE(p.deployment_name, ''),
        'POOL_HOT_TIER_PROVISIONED_SIZE',
        CASE
            WHEN p.pool_attributes IS NOT NULL
             AND p.pool_attributes->>'is_regional_ha' = 'true'
            THEN 'VOLUME_POOL_REGIONAL_HA'
            ELSE 'VOLUME_POOL'
        END
    FROM pools p
    LEFT JOIN accounts a
        ON a.id = p.account_id
       AND a.deleted_at IS NULL
    WHERE p.deleted_at IS NULL
      AND p.uuid IS NOT NULL
      AND p.uuid <> ''
      AND p.allow_auto_tiering
      AND p.auto_tiering_config IS NOT NULL
      AND p.auto_tiering_config <> 'null'::jsonb
      AND NOT EXISTS (
          SELECT 1
          FROM billing_ledgers l
          WHERE l.deleted_at IS NULL
            AND l.resource_uuid = p.uuid
            AND l.measured_type = 'POOL_HOT_TIER_PROVISIONED_SIZE'
            AND l.resource_type = CASE
                WHEN p.pool_attributes IS NOT NULL
                 AND p.pool_attributes->>'is_regional_ha' = 'true'
                THEN 'VOLUME_POOL_REGIONAL_HA'
                ELSE 'VOLUME_POOL'
            END
      );
EXCEPTION
    WHEN OTHERS THEN
        RAISE WARNING 'migration 0052 pass 2 (pool hot tier) skipped due to error [%]: %', SQLSTATE, SQLERRM;
END
$billing_ledger_pool_hot_tier$;

-- Pass 3: backup-enabled volume allocated size.
-- Cross-region vaults are excluded. An unresolved backup_vault_id is also
-- excluded so a missing vault is not billed as an in-region backup.
DO $billing_ledger_volumes$
BEGIN
    IF to_regclass('billing_ledgers') IS NULL THEN
        RAISE WARNING 'migration 0052 pass 3 skipped: billing_ledgers does not exist';
        RETURN;
    END IF;

    INSERT INTO billing_ledgers (
        uuid,
        created_at,
        updated_at,
        resource_name,
        quantity,
        resource_uuid,
        consumer_id,
        deployment_name,
        measured_type,
        resource_type
    )
    SELECT
        gen_random_uuid()::text,
        NOW(),
        NOW(),
        COALESCE(v.name, ''),
        v.size_in_bytes::double precision,
        v.uuid,
        COALESCE(NULLIF(a.name, ''), NULLIF(p.vendor_id, ''), ''),
        COALESCE(p.deployment_name, ''),
        'BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE',
        CASE
            WHEN v.volume_attributes IS NOT NULL
             AND v.volume_attributes->>'is_regional_ha' = 'true'
            THEN 'VOLUME_REGIONAL_HA'
            ELSE 'VOLUME'
        END
    FROM volumes v
    LEFT JOIN pools p
        ON p.id = v.pool_id
       AND p.deleted_at IS NULL
    LEFT JOIN accounts a
        ON a.id = v.account_id
       AND a.deleted_at IS NULL
    LEFT JOIN backup_vaults bv
        ON bv.uuid = v.data_protection->>'backup_vault_id'
       AND bv.deleted_at IS NULL
    WHERE v.deleted_at IS NULL
      AND v.uuid IS NOT NULL
      AND v.uuid <> ''
      AND v.data_protection IS NOT NULL
      AND v.data_protection <> 'null'::jsonb
      AND COALESCE(v.data_protection->>'backup_chain_bytes', '') ~ '^[0-9]{1,18}$'
      AND (v.data_protection->>'backup_chain_bytes')::bigint > 0
      AND (
            COALESCE(v.data_protection->>'backup_vault_id', '') = ''
            OR (
                bv.id IS NOT NULL
                AND bv.backup_vault_type IS DISTINCT FROM 'CROSS_REGION'
            )
          )
      AND NOT EXISTS (
          SELECT 1
          FROM billing_ledgers l
          WHERE l.deleted_at IS NULL
            AND l.resource_uuid = v.uuid
            AND l.measured_type = 'BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE'
            AND l.resource_type = CASE
                WHEN v.volume_attributes IS NOT NULL
                 AND v.volume_attributes->>'is_regional_ha' = 'true'
                THEN 'VOLUME_REGIONAL_HA'
                ELSE 'VOLUME'
            END
      );
EXCEPTION
    WHEN OTHERS THEN
        RAISE WARNING 'migration 0052 pass 3 (backup-enabled volume allocated size) skipped due to error [%]: %', SQLSTATE, SQLERRM;
END
$billing_ledger_volumes$;

-- Pass 4: latest cross-region backup transfer bytes, one row per volume.
DO $billing_ledger_crr_backups$
BEGIN
    IF to_regclass('billing_ledgers') IS NULL THEN
        RAISE WARNING 'migration 0052 pass 4 skipped: billing_ledgers does not exist';
        RETURN;
    END IF;

    INSERT INTO billing_ledgers (
        uuid,
        created_at,
        updated_at,
        resource_name,
        quantity,
        resource_uuid,
        consumer_id,
        deployment_name,
        measured_type,
        resource_type
    )
    SELECT
        gen_random_uuid()::text,
        NOW(),
        NOW(),
        COALESCE(latest.attributes->>'volume_name', ''),
        CASE
            WHEN COALESCE(latest.attributes->>'total_transfer_bytes', '') ~ '^[0-9]{1,18}$'
            THEN (latest.attributes->>'total_transfer_bytes')::double precision
            ELSE 0
        END,
        latest.volume_uuid,
        COALESCE(latest.attributes->>'account_identifier', ''),
        '',
        'CBS_CROSS_REGION_VOLUME_BACKUP_TRANSFER_BYTES',
        'BACKUP'
    FROM (
        SELECT DISTINCT ON (b.volume_uuid)
            b.volume_uuid,
            b.attributes
        FROM backups b
        JOIN backup_vaults bv
            ON bv.id = b.backup_vault_id
           AND bv.deleted_at IS NULL
        WHERE b.deleted_at IS NULL
          AND b.volume_uuid IS NOT NULL
          AND b.volume_uuid <> ''
          AND b.backup_vault_id <> 0
          AND b.attributes IS NOT NULL
          AND b.attributes <> 'null'::jsonb
          AND bv.backup_vault_type = 'CROSS_REGION'
        ORDER BY b.volume_uuid, b.created_at DESC, b.id DESC
    ) latest
    WHERE NOT EXISTS (
        SELECT 1
        FROM billing_ledgers l
        WHERE l.deleted_at IS NULL
          AND l.resource_uuid = latest.volume_uuid
          AND l.resource_type = 'BACKUP'
          AND l.measured_type = 'CBS_CROSS_REGION_VOLUME_BACKUP_TRANSFER_BYTES'
    );
EXCEPTION
    WHEN OTHERS THEN
        RAISE WARNING 'migration 0052 pass 4 (cross-region backup transfer bytes) skipped due to error [%]: %', SQLSTATE, SQLERRM;
END
$billing_ledger_crr_backups$;
