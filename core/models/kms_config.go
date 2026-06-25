package models

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

const (
	ProxyTypeVcp = "vcp"
	ProxyTypeCvp = "cvp"
)

const (
	KmsConfigV1betaKmsStateKEYSTATEUNSPECIFIED = "KEY_STATE_UNSPECIFIED"
)

// KmsConfig describes a KMS configuration in the VCP cloud model
type KmsConfig struct {
	BaseModel
	Name         string
	Description  string
	State        string
	StateDetails string

	KeyRing         string
	KeyRingLocation string
	KeyName         string
	KmsGrant        string

	AccountID         int64
	CustomerProjectID string
	KeyProjectID      string
	ServiceAccountID  *int64
	ResourceID        string
	KmsAttributes     *KmsAttributes
	ServiceAccount    *ServiceAccount
}
type KmsAttributes struct {
	SdeKmsConfigUUID          string
	SdeServiceAccountEmail    string
	Instructions              string
	SdeKmsConfigIsHealthy     bool
	SdeKmsConfigHealthError   string
	SdeKmsConfigOperationURI  string
	SdeKmsConfigOperationDone bool
	CreationMode              string
	VcpServiceAccountEmail    string
}

// GetServiceAccountEmail returns the appropriate service account email
// based on the creation mode. VCP configs use the VCP SA email;
// SDE configs use the SDE SA email.
func (a *KmsAttributes) GetServiceAccountEmail() string {
	if a == nil {
		return ""
	}
	if a.VcpServiceAccountEmail != "" {
		return a.VcpServiceAccountEmail
	}
	return a.SdeServiceAccountEmail
}

// GetCreationMode returns the creation mode, defaulting to "SDE" for backward compatibility.
func (a *KmsAttributes) GetCreationMode() string {
	if a == nil || a.CreationMode == "" {
		return datamodel.KmsCreationModeSDE
	}
	return a.CreationMode
}

// IsVCPCreated returns true if the KMS config was created via VCP (no SDE involvement).
func (a *KmsAttributes) IsVCPCreated() bool {
	return a.GetCreationMode() == datamodel.KmsCreationModeVCP
}

// KmsConfigCheck describes an gcp kms configuration check object in the cloud volumes model
type KmsConfigCheck struct {
	ProxyType   string
	Email       string
	IsHealthy   bool
	HealthError string
	KmsConfig   *KmsConfig
}
