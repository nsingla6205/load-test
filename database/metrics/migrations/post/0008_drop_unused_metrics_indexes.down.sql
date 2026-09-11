CREATE INDEX IF NOT EXISTS idx_aggregated_usages_measured_type ON aggregated_usages (measured_type);
CREATE INDEX IF NOT EXISTS idx_aggregated_usages_vendor_customer_id ON aggregated_usages (vendor_customer_id);
CREATE INDEX IF NOT EXISTS idx_aggregated_usages_account_id ON aggregated_usages (account_id);
CREATE INDEX IF NOT EXISTS idx_aggregated_usages_region_name ON aggregated_usages (region_name);
CREATE INDEX IF NOT EXISTS idx_aggregated_usages_resource_type ON aggregated_usages (resource_type);
CREATE INDEX IF NOT EXISTS idx_aggregated_usages_resource_name ON aggregated_usages (resource_name);

CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_location ON hydrated_metrics (location);
CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_consumer_id ON hydrated_metrics (consumer_id);
CREATE INDEX IF NOT EXISTS idx_hydrated_metrics_resource_name ON hydrated_metrics (resource_name);
