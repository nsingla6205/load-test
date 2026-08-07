-- Partial index for PO delete network cleanup gate query: CountActiveSVMsByIPSpace.

CREATE INDEX IF NOT EXISTS idx_svms_ip_space_active
    ON svms ((svm_details->>'ip_space'))
    WHERE deleted_at IS NULL
      AND svm_details->>'ip_space' IS NOT NULL
      AND svm_details->>'ip_space' <> '';
