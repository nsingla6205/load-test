-- Partial composite indexes for telemetry job dequeue (claimJob in pgjobs.go).
-- The subquery filters queue/type_name, orders by scheduled_at then created_at,
-- and targets rows in 'new' or retriable 'failed' status (the hot empty-poll path).
CREATE INDEX IF NOT EXISTS idx_jobs_dequeue_ready
    ON jobs (queue, type_name, scheduled_at, created_at, id)
    WHERE status IN ('new', 'failed');

-- Supports reclaim of in_progress jobs whose lease expired (third OR branch in claimJob).
CREATE INDEX IF NOT EXISTS idx_jobs_dequeue_expired_in_progress
    ON jobs (queue, type_name, scheduled_at, created_at, id)
    WHERE status = 'in_progress' AND lease_expires_at IS NOT NULL;
