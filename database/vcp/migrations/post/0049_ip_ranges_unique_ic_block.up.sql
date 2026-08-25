-- Backstop for concurrent intercluster carves.
--
-- CreateIPRange rejects an overlapping block with a SELECT-then-INSERT inside one transaction,
-- which two concurrent creates can both pass under READ COMMITTED before either inserts. Two pools
-- would then each hold a row for the same carved block and hand out the same intercluster LIF IPs.
-- The carve is scoped to the data subnet, so uniqueness is keyed on (network, start_ip, end_ip);
-- blocks claimed on different subnets of one VPC are rejected earlier by GCP, which refuses
-- overlapping secondary ranges within a network.
CREATE UNIQUE INDEX IF NOT EXISTS idx_ip_ranges_unique_ic_block
    ON ip_ranges (network, start_ip, end_ip)
    WHERE deleted_at IS NULL AND lif_type = 'interclusterLIF';
