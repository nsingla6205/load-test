package common

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestClassifyAuthType1Pool(t *testing.T) {
	tests := []struct {
		name     string
		pool     *datamodel.Pool
		expected AuthType1PoolClassification
	}{
		{name: "missing pool is native", expected: AuthType1PoolNative},
		{
			name: "plaintext only is native",
			pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{
				AuthType: env.USERNAME_PWD_SEC_MGR,
				SecretID: "pool-secret",
			}},
			expected: AuthType1PoolNative,
		},
		{
			name: "certificate id proves demotion",
			pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{
				AuthType:      env.USERNAME_PWD_SEC_MGR,
				CertificateID: "old-certificate",
			}},
			expected: AuthType1PoolDemoted,
		},
		{
			name: "pre certificate username proves demotion",
			pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{
				AuthType:           env.USERNAME_PWD_SEC_MGR,
				UsernamePreCertFix: "vcpadmin",
			}},
			expected: AuthType1PoolDemoted,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, ClassifyAuthType1Pool(test.pool))
		})
	}
}

func TestAuthTypeReconciliationEligibility(t *testing.T) {
	eligibleStates := map[string]struct{}{"READY": {}}
	eligiblePool := &datamodel.Pool{
		State:        "READY",
		ServiceLevel: "FLEX",
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType:      env.USERNAME_PWD_SEC_MGR,
			CertificateID: "old-certificate",
		},
	}

	require.NoError(t, AuthTypeReconciliationEligibility(eligiblePool, eligibleStates))

	nativePool := *eligiblePool
	nativePool.PoolCredentials = &datamodel.PoolCredentials{AuthType: env.USERNAME_PWD_SEC_MGR}
	require.ErrorContains(t,
		AuthTypeReconciliationEligibility(&nativePool, eligibleStates),
		"native password-auth pool",
	)

	ineligibleState := *eligiblePool
	ineligibleState.State = "DELETING"
	require.ErrorContains(t,
		AuthTypeReconciliationEligibility(&ineligibleState, eligibleStates),
		"not eligible",
	)

	require.ErrorContains(t,
		AuthTypeReconciliationEligibility(nil, eligibleStates),
		"not found",
	)

	missingCredentials := *eligiblePool
	missingCredentials.PoolCredentials = nil
	require.ErrorContains(t,
		AuthTypeReconciliationEligibility(&missingCredentials, eligibleStates),
		"credentials are missing",
	)

	wrongAuthType := *eligiblePool
	wrongAuthType.PoolCredentials = &datamodel.PoolCredentials{AuthType: env.USER_CERTIFICATE}
	require.ErrorContains(t,
		AuthTypeReconciliationEligibility(&wrongAuthType, eligibleStates),
		"auth type is 2, expected 1",
	)

	dedicatedCapacity := *eligiblePool
	dedicatedCapacity.ServiceLevel = "STANDARD"
	require.ErrorContains(t,
		AuthTypeReconciliationEligibility(&dedicatedCapacity, eligibleStates),
		"dedicated-capacity",
	)
}
