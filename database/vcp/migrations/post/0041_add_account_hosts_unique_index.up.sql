-- Partial unique index and lookup indexes for account_hosts.
-- Table schema (including service_credentials) is created by GORM AutoMigrate.

CREATE UNIQUE INDEX IF NOT EXISTS idx_account_hosts_account_host_active
    ON account_hosts (account_id, host_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_account_hosts_account_id
    ON account_hosts (account_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_account_hosts_host_id
    ON account_hosts (host_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_account_hosts_account_pinned
    ON account_hosts (account_id, pinned)
    WHERE deleted_at IS NULL;
