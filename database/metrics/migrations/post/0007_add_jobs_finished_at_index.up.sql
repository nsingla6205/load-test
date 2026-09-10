-- Partial index for daily jobs table cleanup (DeleteJobsOlderThan in database/metrics/interface.go).
-- Targets rows with finished_at set; NULL finished_at orphans are excluded by the partial predicate.
CREATE INDEX IF NOT EXISTS idx_jobs_finished_at
    ON jobs (finished_at)
    WHERE finished_at IS NOT NULL;
