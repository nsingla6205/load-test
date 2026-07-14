-- Partial unique index for data LIF IP reservations within an ip_range.
-- Column additions (ip_range_id, svm_id) are created by GORM AutoMigrate.

CREATE UNIQUE INDEX IF NOT EXISTS idx_lifs_ip_range_ip_active
    ON lifs (ip_range_id, ip_address)
    WHERE deleted_at IS NULL AND ip_range_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_lifs_ip_range_active
    ON lifs (ip_range_id)
    WHERE deleted_at IS NULL AND ip_range_id IS NOT NULL;
