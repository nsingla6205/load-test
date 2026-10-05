SELECT 'pool' AS section, p.* FROM pools p 
WHERE p.pool_external_identifier = '<pool_ocid>';

SELECT 'nodes' AS section, n.* FROM nodes n 
JOIN pools p ON p.id = n.pool_id 

WHERE p.pool_external_identifier = '<pool_ocid>'; 
SELECT 'svms' AS section, s.* FROM svms s 
JOIN pools p ON p.id = s.pool_id 
WHERE p.pool_external_identifier = '<pool_ocid>'; 

SELECT 'cluster_upgrade_jobs' AS section, j.* 
FROM cluster_upgrade_jobs j 
JOIN pools p ON p.uuid = j.cluster_id 
WHERE p.pool_external_identifier = '<pool_ocid>' 
ORDER BY j.created_at DESC; 