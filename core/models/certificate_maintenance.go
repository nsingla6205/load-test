package models

// CertificateMaintenanceOperation identifies an asynchronous certificate operation.
type CertificateMaintenanceOperation string

const (
	// CertificateMaintenanceOperationCAReconciliation reconciles pools to the configured CA.
	CertificateMaintenanceOperationCAReconciliation CertificateMaintenanceOperation = "CA_RECONCILIATION"
	// CertificateMaintenanceOperationForcedRotation forces certificate rotation.
	CertificateMaintenanceOperationForcedRotation CertificateMaintenanceOperation = "FORCED_ROTATION"
)

// CertificateMaintenancePoolStatus reports one pool's progress within a maintenance job.
type CertificateMaintenancePoolStatus struct {
	PoolID     string
	JobID      string
	WorkflowID string
	Status     string
	Error      string
}

// CertificateMaintenanceProgress reports aggregate and per-pool job progress.
type CertificateMaintenanceProgress struct {
	JobID           string
	WorkflowID      string
	Operation       CertificateMaintenanceOperation
	Status          string
	SelectionType   string
	DesiredCAURI    string
	TotalPools      int
	CompletedPools  int
	FailedPools     int
	InProgressPools int
	Pools           []CertificateMaintenancePoolStatus
}
