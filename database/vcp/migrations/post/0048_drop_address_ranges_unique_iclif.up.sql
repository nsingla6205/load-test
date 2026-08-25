-- Allow multiple interclusterLIF address ranges per VPC + host project so
-- operators can extend IC LIF capacity. Distinct CIDR/name uniqueness remains
-- via idx_address_ranges_unique_cidr and idx_address_ranges_unique_name.
DROP INDEX IF EXISTS idx_address_ranges_unique_iclif;
