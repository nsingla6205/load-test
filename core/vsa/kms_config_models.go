package vsa

import "github.com/go-openapi/strfmt"

type CreateKmsConfigParams struct {
	KeyName           string
	KeyRingLocation   string
	KeyRingName       string
	ProjectID         string
	Credentials       *strfmt.Password
	SvmName           string
	PrivilegedAccount string // Cloud KMS account to impersonate.
}

type DeleteKmsConfigParams struct {
	ExternalKmsConfigID string
}

// RekeyGcpKmsParams carries the inputs for an ONTAP GCP KMS external rekey.
type RekeyGcpKmsParams struct {
	ExternalKmsConfigID string
	KeyName             string
	KeyRingLocation     string
	KeyRingName         string
	ProjectID           string
}

type CreateKmsConfigResponse struct {
	ProviderResponse
}

type DeleteKmsConfigResponse struct {
	ProviderResponse
}

type GetKmsConfigParams struct {
	ExternalKmsConfigID string
}

// GcpKmsHierarchy is the GCP KMS key hierarchy currently configured on an SVM's external key manager
type GcpKmsHierarchy struct {
	ProjectID       string
	KeyRingName     string
	KeyRingLocation string
	KeyName         string
}
