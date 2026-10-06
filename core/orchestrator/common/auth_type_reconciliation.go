package common

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// AuthType1PoolClassification identifies whether password authentication is
// native or is a temporary demotion from certificate authentication.
type AuthType1PoolClassification string

const (
	// AuthType1PoolNative is a pool originally configured for password authentication.
	AuthType1PoolNative AuthType1PoolClassification = "native"
	// AuthType1PoolDemoted is a pool retaining evidence of prior certificate authentication.
	AuthType1PoolDemoted AuthType1PoolClassification = "demoted"
)

// ClassifyAuthType1Pool classifies an auth_type=1 pool from persisted credential evidence.
func ClassifyAuthType1Pool(pool *datamodel.Pool) AuthType1PoolClassification {
	if pool != nil && pool.PoolCredentials != nil {
		credentials := pool.PoolCredentials
		if credentials.CertificateID != "" || credentials.CertificateIDNew != "" ||
			credentials.CaURI != "" || credentials.StagedCaURI != "" ||
			credentials.UsernamePreCertFix != "" {
			return AuthType1PoolDemoted
		}
	}
	return AuthType1PoolNative
}

// AuthTypeReconciliationEligibility validates the inexpensive DB-only checks
// performed before core-api queues a per-pool reconciliation.
func AuthTypeReconciliationEligibility(pool *datamodel.Pool, eligibleStates map[string]struct{}) error {
	if pool == nil {
		return fmt.Errorf("pool not found")
	}
	if pool.PoolCredentials == nil {
		return fmt.Errorf("pool credentials are missing")
	}
	if pool.PoolCredentials.AuthType != env.USERNAME_PWD_SEC_MGR {
		return fmt.Errorf("auth type is %d, expected %d", pool.PoolCredentials.AuthType, env.USERNAME_PWD_SEC_MGR)
	}
	if _, eligible := eligibleStates[pool.State]; !eligible {
		return fmt.Errorf("state %q is not eligible", pool.State)
	}
	if ClassifyAuthType1Pool(pool) != AuthType1PoolDemoted {
		return fmt.Errorf("native password-auth pool has no certificate demotion evidence")
	}
	if pool.IsDedicatedCapacity() {
		return fmt.Errorf("dedicated-capacity pool is not eligible")
	}
	return nil
}
