-- Soft-deletes active snapshots whose volume is already soft-deleted.
UPDATE snapshots AS s
SET deleted_at = NOW(),
    updated_at = NOW(),
    state = 'DELETED',
    state_details = 'Deleted'
FROM volumes AS v
WHERE s.volume_id = v.id
  AND s.deleted_at IS NULL
  AND v.deleted_at IS NOT NULL;
