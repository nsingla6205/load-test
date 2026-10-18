-- Backfill billing_ledgers for resources that already exist.
--
-- The billing ledger is written on create/update going forward. This migration
-- seeds the current active entry for resources created before that path
-- existed, using the same eligibility rules as the billing activities, which
-- mirror the data-driven conditions of the telemetry billing collectors:
--
--   Pass 1 — pools: POOL_ALLOCATED_SIZE
--            VOLUME_POOL, or VOLUME_POOL_REGIONAL_HA when pool_attributes
--            is_regional_ha is true. Quantity is pools.size_in_bytes.
--            Requires pool_attributes.account_name.
--   Pass 2 — auto-tiered pools: POOL_HOT_TIER_PROVISIONED_SIZE
--            Same pool conditions as pass 1, plus allow_auto_tiering and a
--            positive auto_tiering_config.hot_tier_size_in_bytes.
--   Pass 3 — backup-enabled volumes: BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE
--            VOLUME, or VOLUME_REGIONAL_HA when volume_attributes
--            is_regional_ha is true. Requires a volume name,
--            volume_attributes.account_name, and backup_chain_bytes > 0. A
--            volume whose backup_vault_id does not resolve is skipped.
--            Cross-project vaults are billed to the vault's owning account.
--   Pass 4 — cross-region backups: CBS_CROSS_REGION_VOLUME_BACKUP_TRANSFER_BYTES
--            One active row per volume, from the volume's latest AVAILABLE
--            backup. Written only when that backup's vault is CROSS_REGION
--            with a backup region set and distinct from its source region,
--            and total_transfer_bytes > 0. Resource type is BACKUP and
--            resource_uuid is the volume UUID.
--
-- Consumer id is the resource's recorded account name (transfer rows use
-- attributes.account_identifier). Deployment name comes from the pool, the
-- volume attributes, or the backup vault name respectively.
--
-- HYPERSCALERDISABLED accounts are intentionally still billed here: this is a
-- deliberate divergence from the telemetry billing collectors, which suppress
-- metric emission for disabled accounts. The ledger bills regardless of
-- account state.
--
-- Rollout feature flags applied by the collectors at emission time (files
-- backup billing, CMEK, cross-region, GCBDR and expert-mode billing) are not
-- part of ledger eligibility and are not evaluated here.
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
        p.pool_attributes->>'account_name',
        COALESCE(p.deployment_name, ''),
        'POOL_ALLOCATED_SIZE',
        CASE
            WHEN p.pool_attributes->>'is_regional_ha' = 'true'
            THEN 'VOLUME_POOL_REGIONAL_HA'
            ELSE 'VOLUME_POOL'
        END
    FROM pools p
    WHERE p.deleted_at IS NULL
      AND p.uuid IS NOT NULL
      AND p.uuid <> ''
      AND p.pool_attributes IS NOT NULL
      AND p.pool_attributes <> 'null'::jsonb
      AND COALESCE(p.pool_attributes->>'account_name', '') <> ''
      AND NOT EXISTS (
          SELECT 1
          FROM billing_ledgers l
          WHERE l.deleted_at IS NULL
            AND l.resource_uuid = p.uuid
            AND l.measured_type = 'POOL_ALLOCATED_SIZE'
            AND l.resource_type = CASE
                WHEN p.pool_attributes->>'is_regional_ha' = 'true'
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
        (p.auto_tiering_config->>'hot_tier_size_in_bytes')::double precision,
        p.uuid,
        p.pool_attributes->>'account_name',
        COALESCE(p.deployment_name, ''),
        'POOL_HOT_TIER_PROVISIONED_SIZE',
        CASE
            WHEN p.pool_attributes->>'is_regional_ha' = 'true'
            THEN 'VOLUME_POOL_REGIONAL_HA'
            ELSE 'VOLUME_POOL'
        END
    FROM pools p
    WHERE p.deleted_at IS NULL
      AND p.uuid IS NOT NULL
      AND p.uuid <> ''
      AND p.pool_attributes IS NOT NULL
      AND p.pool_attributes <> 'null'::jsonb
      AND COALESCE(p.pool_attributes->>'account_name', '') <> ''
      AND p.allow_auto_tiering
      AND p.auto_tiering_config IS NOT NULL
      AND p.auto_tiering_config <> 'null'::jsonb
      AND COALESCE(p.auto_tiering_config->>'hot_tier_size_in_bytes', '') ~ '^[0-9]{1,18}$'
      AND (p.auto_tiering_config->>'hot_tier_size_in_bytes')::bigint > 0
      AND NOT EXISTS (
          SELECT 1
          FROM billing_ledgers l
          WHERE l.deleted_at IS NULL
            AND l.resource_uuid = p.uuid
            AND l.measured_type = 'POOL_HOT_TIER_PROVISIONED_SIZE'
            AND l.resource_type = CASE
                WHEN p.pool_attributes->>'is_regional_ha' = 'true'
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
-- An unresolved backup_vault_id is excluded so a missing vault is not billed.
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
        v.name,
        v.size_in_bytes::double precision,
        v.uuid,
        CASE
            WHEN bv.service_type = 'CrossProject' AND COALESCE(vault_owner.name, '') <> ''
            THEN vault_owner.name
            ELSE v.volume_attributes->>'account_name'
        END,
        COALESCE(v.volume_attributes->>'deployment_name', ''),
        'BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE',
        CASE
            WHEN v.volume_attributes->>'is_regional_ha' = 'true'
            THEN 'VOLUME_REGIONAL_HA'
            ELSE 'VOLUME'
        END
    FROM volumes v
    LEFT JOIN backup_vaults bv
        ON bv.uuid = v.data_protection->>'backup_vault_id'
       AND bv.deleted_at IS NULL
    LEFT JOIN accounts vault_owner
        ON vault_owner.id = bv.account_id
       AND vault_owner.deleted_at IS NULL
    WHERE v.deleted_at IS NULL
      AND v.uuid IS NOT NULL
      AND v.uuid <> ''
      AND COALESCE(v.name, '') <> ''
      AND v.volume_attributes IS NOT NULL
      AND v.volume_attributes <> 'null'::jsonb
      AND COALESCE(v.volume_attributes->>'account_name', '') <> ''
      AND v.data_protection IS NOT NULL
      AND v.data_protection <> 'null'::jsonb
      AND COALESCE(v.data_protection->>'backup_chain_bytes', '') ~ '^[0-9]{1,18}$'
      AND (v.data_protection->>'backup_chain_bytes')::bigint > 0
      AND (
            COALESCE(v.data_protection->>'backup_vault_id', '') = ''
            OR bv.id IS NOT NULL
          )
      AND NOT EXISTS (
          SELECT 1
          FROM billing_ledgers l
          WHERE l.deleted_at IS NULL
            AND l.resource_uuid = v.uuid
            AND l.measured_type = 'BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE'
            AND l.resource_type = CASE
                WHEN v.volume_attributes->>'is_regional_ha' = 'true'
                THEN 'VOLUME_REGIONAL_HA'
                ELSE 'VOLUME'
            END
      );
EXCEPTION
    WHEN OTHERS THEN
        RAISE WARNING 'migration 0052 pass 3 (backup-enabled volume allocated size) skipped due to error [%]: %', SQLSTATE, SQLERRM;
END
$billing_ledger_volumes$;

-- Pass 4: cross-region backup transfer bytes, one row per volume.
-- The latest AVAILABLE backup per volume is selected first (any vault), and the
-- transfer conditions are applied to that backup only. A newer CREATING or
-- ERROR backup, or an older cross-region backup superseded by a newer backup in
-- another vault, therefore cannot produce a row.
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
        (latest.attributes->>'total_transfer_bytes')::double precision,
        latest.volume_uuid,
        COALESCE(latest.attributes->>'account_identifier', ''),
        COALESCE(bv.name, ''),
        'CBS_CROSS_REGION_VOLUME_BACKUP_TRANSFER_BYTES',
        'BACKUP'
    FROM (
        SELECT DISTINCT ON (b.volume_uuid)
            b.volume_uuid,
            b.attributes,
            b.backup_vault_id
        FROM backups b
        WHERE b.deleted_at IS NULL
          AND b.volume_uuid IS NOT NULL
          AND b.volume_uuid <> ''
          AND b.state = 'AVAILABLE'
        ORDER BY b.volume_uuid, b.id DESC
    ) latest
    JOIN backup_vaults bv
        ON bv.id = latest.backup_vault_id
       AND bv.deleted_at IS NULL
    WHERE latest.attributes IS NOT NULL
      AND latest.attributes <> 'null'::jsonb
      AND bv.backup_vault_type = 'CROSS_REGION'
      AND COALESCE(bv.backup_region_name, '') <> ''
      AND bv.backup_region_name IS DISTINCT FROM bv.source_region_name
      AND COALESCE(latest.attributes->>'total_transfer_bytes', '') ~ '^[0-9]{1,18}$'
      AND (latest.attributes->>'total_transfer_bytes')::bigint > 0
      AND NOT EXISTS (
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
