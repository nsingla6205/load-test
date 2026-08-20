-- Composite lookup for GetLifsForNodesWithProtocol:
--   account_id + node_id + lif_details.protocol_type
-- AutoMigrate cannot create expression indexes on JSONB keys, so this lives here.

CREATE INDEX IF NOT EXISTS idx_lifs_account_node_protocol_active
    ON lifs (account_id, node_id, (lif_details->>'protocol_type'))
    WHERE deleted_at IS NULL;
