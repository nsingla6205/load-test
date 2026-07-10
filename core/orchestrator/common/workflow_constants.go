package common

// Control workflow ID format constants

const (
	// PoolDataSubnetCreateDelete is a placeholder used for sequence workflow instance that runs all
	// subnet create and delete operations for a specific account and VPC sequentially.
	PoolDataSubnetCreateDelete = "Account_%d_VPC_%s_Ops_PoolDataSubnet-CD"

	// PoolOperationsSeq is a placeholder used for sequence workflow instance that runs all
	// pool-level operations (certificate rotation, password rotation, updates, deletes, etc.) sequentially.
	// This ensures only one workflow operates on a pool at a time, preventing race conditions.
	PoolOperationsSeq = "Pool_%s_Ops_All"

	// Signal is the name of the signal used to call sequential workflows.
	Signal = "req"

	// Workflow name constants for use with control workflows
	// These are used to reference workflows by name when executing them sequentially
	RotatePoolCertificateWorkflowName = "RotatePoolCertificateWorkflow"
	RotatePoolPasswordWorkflowName    = "RotatePoolPasswordWorkflow"

	// RotateOciPoolCertificateWorkflowName is used to start the OCI per-pool certificate
	// rotation as an independent top-level workflow from the dispatch activity (the OCI
	// dispatcher fans out via the Temporal client rather than awaited child workflows).
	RotateOciPoolCertificateWorkflowName = "RotateOciPoolCertificateWorkflow"

	// RotateOciCertificatesWorkflowName is the OCI rotation dispatcher (parent) workflow,
	// referenced by name when creating its Temporal Schedule on the customer task queue.
	RotateOciCertificatesWorkflowName = "RotateOciCertificatesWorkflow"
)
