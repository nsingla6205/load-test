-- Remove the index supporting the job claim predicate and the lease sweeper.
DROP INDEX IF EXISTS idx_jobs_status_lease;
