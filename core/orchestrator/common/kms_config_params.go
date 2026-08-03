package common

// CreateKmsConfigParams describes parameters supplied to CreateKmsConfigActivity
type CreateKmsConfigParams struct {
	AccountName         string
	Name                string
	Description         string
	Instructions        string
	KeyFullPath         string
	KmsState            string
	KmsStateDetails     string
	ResourceID          string
	ServiceAccountEmail string
	UUID                string
	LocationID          string
	ProjectNumber       string
	XCorrelationID      string
	OperationUri        string
	OperationDone       bool
	SdeJobUUID          string
	TrialMode           *TrialModeParams
}

// GetKmsConfigParams describes parameters supplied to CreateKmsConfigActivity
type GetKmsConfigParams struct {
	AccountName         string
	Name                string
	Description         string
	Instructions        string
	KeyFullPath         string
	KmsState            string
	KmsStateDetails     string
	ResourceID          string
	ServiceAccountEmail string
	UUID                string
	LocationID          string
	ProjectNumber       string
}

// CheckKmsConfigParams check kms config reachability
type CheckKmsConfigParams struct {
	KmsConfigUUID string
	LocationID    string
	ProjectNumber string
}

type DeleteKmsConfigParams struct {
	KmsConfigID    string
	AccountName    string
	Region         string
	XCorrelationID string
}

type MigrateKmsConfigParams struct {
	LocationID     string
	ProjectNumber  string
	UUID           string
	SdeUUID        string
	State          string
	Name           string
	AccountName    string
	XCorrelationID string
	ResourceID     string
}

// EncryptPoolParams describes parameters for encrypting or migrating CMEK on a single pool.
type EncryptPoolParams struct {
	PoolUUID             string
	KmsConfigID          string
	KmsConfigState       string
	AccountName          string
	ProjectNumber        string
	XCorrelationID       string
	PreviousState        string
	PreviousStateDetails string
}

type PollKmsConfigParams struct {
	ResourceID     string
	LocationID     string
	ProjectNumber  string
	XCorrelationID string
	OperationUri   string
	OperationDone  bool
}

// UpdateKmsConfigParams describes parameters supplied to UpdateKmsConfig
type UpdateKmsConfigParams struct {
	AccountName     string
	KmsConfigID     string
	ResourceID      string
	KeyRing         string
	KeyRingLocation string
	KeyName         string
	KeyProjectID    string
	KmsGrant        string
	Description     *string
	Region          string
	XCorrelationID  string
	KeyUri          string
}

type RotateKmsConfigParams struct {
	KmsConfigID    string
	AccountName    string
	XCorrelationID string
}
