package common

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
)

// HostOnboardResult carries host onboarding state between workflow activities.
// This is Temporal workflow/activity state only: nothing here is persisted to the
// hosts table. HostCredentials (CAS certificate/secret metadata) lives here for the
// duration of the workflow and is intentionally not written to the database.
type HostOnboardResult struct {
	HostUUID string
	DbHost   *datamodel.Host
	// HostCredentials holds the CAS/Secret Manager certificate metadata (CertificateID,
	// SecretID, CaURI, cert username) generated for this host onboard run. It is carried
	// between activities via Temporal's workflow state and is never persisted to Postgres.
	HostCredentials     *datamodel.PoolCredentials
	CertificateResponse *hyperscaler_models.CustomCertificateResponse
	CloudDNSRecord      *hyperscaler_models.CustomCloudDNSRecord
	// CloudService holds the resolved hyperscaler client reused across host onboard activities.
	// Temporal does not serialize live cloud clients; activities restore this from an in-process cache keyed by HostUUID.
	CloudService hyperscaler.Services `json:"-"`
}
