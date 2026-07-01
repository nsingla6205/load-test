-- Composite index to make the job claim predicate and the lease sweeper efficient.
-- The sweeper scans `status = 'in_progress' AND lease_expires_at < now()` and the
-- claim predicate filters on status; ordering status first keeps both fast.
CREATE INDEX IF NOT EXISTS idx_jobs_status_lease ON jobs (status, lease_expires_at);
