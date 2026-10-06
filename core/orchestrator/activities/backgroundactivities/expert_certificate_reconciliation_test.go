package backgroundactivities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func expertPoolView(poolUUID string, credentials ...*datamodel.ExpertModeCredential) *datamodel.PoolView {
	return &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:       datamodel.BaseModel{UUID: poolUUID},
			PoolCredentials: &datamodel.PoolCredentials{},
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: credentials,
			},
		},
	}
}

func TestReconcileExpertModeCredentialsBeforePromotionEarlyReturns(t *testing.T) {
	ctx := context.Background()
	const poolUUID = "pool-1"

	t.Run("pool lookup failure", func(t *testing.T) {
		storage := database.NewMockStorage(t)
		storage.On("ListPools", ctx, mock.Anything).
			Return(nil, errors.New("database failed")).
			Once()
		activity := &RotateVcpToVsaCertificateActivity{SE: storage}

		err := activity.reconcileExpertModeCredentialsBeforePromotion(ctx, poolUUID, "project/pool/ca", false)

		require.Error(t, err)
	})

	t.Run("pool without expert credentials", func(t *testing.T) {
		storage := database.NewMockStorage(t)
		storage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{{
				Pool: datamodel.Pool{
					BaseModel:       datamodel.BaseModel{UUID: poolUUID},
					PoolCredentials: &datamodel.PoolCredentials{},
				},
			}}, nil).
			Once()
		activity := &RotateVcpToVsaCertificateActivity{SE: storage}

		require.NoError(t, activity.reconcileExpertModeCredentialsBeforePromotion(
			ctx,
			poolUUID,
			"project/pool/ca",
			false,
		))
	})

	t.Run("cloud service failure", func(t *testing.T) {
		storage := database.NewMockStorage(t)
		storage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{expertPoolView(poolUUID, &datamodel.ExpertModeCredential{
				AuthType: env.USER_CERTIFICATE,
			})}, nil).
			Once()
		activity := &RotateVcpToVsaCertificateActivity{
			SE: storage,
			deps: &certificateRotationDependencies{
				resolveCloudService: func(context.Context) (hyperscaler.Services, error) {
					return nil, errors.New("cloud failed")
				},
			},
		}

		err := activity.reconcileExpertModeCredentialsBeforePromotion(ctx, poolUUID, "project/pool/ca", false)

		require.ErrorContains(t, err, "get cloud service")
	})

	t.Run("password credentials do not enter certificate reconciliation", func(t *testing.T) {
		storage := database.NewMockStorage(t)
		storage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{expertPoolView(poolUUID, &datamodel.ExpertModeCredential{
				AuthType:           env.USERNAME_PWD_SEC_MGR,
				Username:           "gcnv_gadmin",
				UsernamePreCertFix: "pool_gadmin",
				CertificateID:      "legacy-cert",
			})}, nil).
			Once()
		activity := &RotateVcpToVsaCertificateActivity{
			SE: storage,
			deps: &certificateRotationDependencies{
				resolveCloudService: func(context.Context) (hyperscaler.Services, error) {
					return nil, errors.New("must not resolve cloud service")
				},
			},
		}

		require.NoError(t, activity.reconcileExpertModeCredentialsBeforePromotion(
			ctx,
			poolUUID,
			"project/pool/ca",
			false,
		))
	})
}

func TestReconcileOneExpertModeCredentialEarlyReturns(t *testing.T) {
	ctx := context.Background()
	const poolUUID = "pool-1"

	t.Run("nil pool", func(t *testing.T) {
		activity := &RotateVcpToVsaCertificateActivity{}
		err := activity.reconcileOneExpertModeCredential(
			ctx,
			nil,
			0,
			"project/pool/ca",
			hyperscaler.NewMockServices(t),
			false,
			false,
		)

		require.Error(t, err)
	})

	tests := []struct {
		name        string
		credentials []*datamodel.ExpertModeCredential
		index       int
		wantError   string
	}{
		{
			name:        "invalid index",
			credentials: []*datamodel.ExpertModeCredential{},
			wantError:   "invalid expert credential index",
		},
		{
			name:        "nil credential",
			credentials: []*datamodel.ExpertModeCredential{nil},
		},
		{
			name: "demoted certificate credential is skipped",
			credentials: []*datamodel.ExpertModeCredential{{
				AuthType:           env.USERNAME_PWD_SEC_MGR,
				Username:           "gcnv_gadmin",
				UsernamePreCertFix: "pool_gadmin",
				CertificateID:      "legacy-cert",
			}},
		},
		{
			name: "non-certificate credential is skipped",
			credentials: []*datamodel.ExpertModeCredential{{
				AuthType: env.USERNAME_PWD_SEC_MGR,
				Username: "native-password-user",
			}},
		},
		{
			name: "certificate credential requires username",
			credentials: []*datamodel.ExpertModeCredential{{
				AuthType: env.USER_CERTIFICATE,
			}},
			wantError: "empty username",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			activity := &RotateVcpToVsaCertificateActivity{}
			pool := ConvertPoolViewToPool(expertPoolView(poolUUID, test.credentials...))

			err := activity.reconcileOneExpertModeCredential(
				ctx,
				pool,
				test.index,
				"project/pool/ca",
				hyperscaler.NewMockServices(t),
				false,
				false,
			)

			if test.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestBackfillExpertModeCredentialCAURI(t *testing.T) {
	ctx := context.Background()
	const (
		poolUUID = "pool-1"
		targetCA = "project/pool/ca"
		certID   = "legacy-expert-cert"
	)

	credential := &datamodel.ExpertModeCredential{
		AuthType:      env.USER_CERTIFICATE,
		Username:      "pool_gadmin",
		CertificateID: certID,
	}
	poolView := expertPoolView(poolUUID, credential)
	poolView.Pool.PoolCredentials.CaURI = targetCA

	storage := database.NewMockStorage(t)
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{poolView}, nil).
		Once()
	storage.On(
		"CompareAndSwapExpertModeCredentials",
		ctx,
		poolUUID,
		mock.Anything,
		mock.MatchedBy(func(replacement *datamodel.ExpertModeCredentials) bool {
			return replacement.ExpertModeCredential[0].CaURI == targetCA
		}),
		mock.Anything,
	).Return(nil).Once()

	activity := &RotateVcpToVsaCertificateActivity{SE: storage}

	err := activity.backfillExpertModeCredentialCAURI(
		ctx,
		poolUUID,
		0,
		certID,
		targetCA,
	)

	require.NoError(t, err)
	storage.AssertExpectations(t)

	t.Run("pool lookup failure", func(t *testing.T) {
		failingStorage := database.NewMockStorage(t)
		failingStorage.On("ListPools", ctx, mock.Anything).
			Return(nil, errors.New("database failed")).
			Once()

		err := (&RotateVcpToVsaCertificateActivity{SE: failingStorage}).
			backfillExpertModeCredentialCAURI(ctx, poolUUID, 0, certID, targetCA)

		require.Error(t, err)
	})

	t.Run("invalid index", func(t *testing.T) {
		invalidStorage := database.NewMockStorage(t)
		invalidStorage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{expertPoolView(poolUUID)}, nil).
			Once()

		err := (&RotateVcpToVsaCertificateActivity{SE: invalidStorage}).
			backfillExpertModeCredentialCAURI(ctx, poolUUID, 0, certID, targetCA)

		require.ErrorContains(t, err, "invalid expert credential index")
	})

	t.Run("credential changed", func(t *testing.T) {
		changedStorage := database.NewMockStorage(t)
		changedStorage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{
				expertPoolView(poolUUID, &datamodel.ExpertModeCredential{
					AuthType:      env.USER_CERTIFICATE,
					CertificateID: "different-cert",
				}),
			}, nil).
			Once()

		err := (&RotateVcpToVsaCertificateActivity{SE: changedStorage}).
			backfillExpertModeCredentialCAURI(ctx, poolUUID, 0, certID, targetCA)

		require.ErrorContains(t, err, "changed before issuer backfill")
	})
}
