package backgroundactivities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestUpdateExpertModeStaging_UsesFullPayloadCAS(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	expected := &datamodel.ExpertModeCredentials{
		ExpertModeCredential: []*datamodel.ExpertModeCredential{
			{CertificateID: "active", Username: "expert", AuthType: env.USER_CERTIFICATE},
		},
	}
	pool := &datamodel.Pool{
		BaseModel:             datamodel.BaseModel{UUID: "pool-1"},
		State:                 "READY",
		ExpertModeCredentials: expected,
	}
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: *pool}}, nil).
		Once()
	storage.On(
		"CompareAndSwapExpertModeCredentials",
		ctx,
		pool.UUID,
		mock.MatchedBy(func(value *datamodel.ExpertModeCredentials) bool {
			return value.ExpertModeCredential[0].CertificateIDNew == ""
		}),
		mock.MatchedBy(func(value *datamodel.ExpertModeCredentials) bool {
			credential := value.ExpertModeCredential[0]
			return credential.CertificateIDNew == "staged" &&
				credential.StagedCaURI == "project/pool/ca"
		}),
		mock.Anything,
	).Return(nil).Once()

	err := (&RotateVcpToVsaCertificateActivity{SE: storage}).updateExpertModeStaging(
		ctx,
		pool.UUID,
		0,
		"",
		"",
		"staged",
		"project/pool/ca",
	)

	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything)
}

func TestPromoteExpertModeCandidate_RejectsConcurrentPayloadChange(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	expected := &datamodel.ExpertModeCredentials{
		ExpertModeCredential: []*datamodel.ExpertModeCredential{
			{
				CertificateID:    "active",
				CaURI:            "project/pool/old",
				CertificateIDNew: "staged",
				StagedCaURI:      "project/pool/new",
				Username:         "expert",
				AuthType:         env.USER_CERTIFICATE,
			},
		},
	}
	pool := &datamodel.Pool{
		BaseModel:             datamodel.BaseModel{UUID: "pool-1"},
		State:                 "READY",
		ExpertModeCredentials: expected,
	}
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: *pool}}, nil).
		Once()
	storage.On(
		"CompareAndSwapExpertModeCredentials",
		ctx,
		pool.UUID,
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Return(errors.New("expert credentials changed")).Once()

	err := (&RotateVcpToVsaCertificateActivity{SE: storage}).promoteExpertModeCandidate(
		ctx,
		pool.UUID,
		0,
		"staged",
		"project/pool/new",
		"active",
		"project/pool/old",
		"expert",
		false,
	)

	require.ErrorContains(t, err, "expert credentials changed")
}

func TestReconcileOneExpertModeCredential_BackfillsValidLegacyIssuerWithoutMint(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	storage := database.NewMockStorage(t)
	expertCredentials := &datamodel.ExpertModeCredentials{
		ExpertModeCredential: []*datamodel.ExpertModeCredential{
			{
				CertificateID: "legacy-active",
				Username:      "expert",
				AuthType:      env.USER_CERTIFICATE,
			},
		},
	}
	pool := &datamodel.Pool{
		BaseModel:             datamodel.BaseModel{UUID: "pool-1"},
		State:                 "READY",
		DeploymentName:        "deployment",
		PoolCredentials:       &datamodel.PoolCredentials{CaURI: "project/pool/ca"},
		ExpertModeCredentials: expertCredentials,
	}
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: *pool}}, nil).
		Once()
	storage.On(
		"CompareAndSwapExpertModeCredentials",
		ctx,
		pool.UUID,
		mock.MatchedBy(func(value *datamodel.ExpertModeCredentials) bool {
			return value.ExpertModeCredential[0].CaURI == ""
		}),
		mock.MatchedBy(func(value *datamodel.ExpertModeCredentials) bool {
			return value.ExpertModeCredential[0].CaURI == "project/pool/ca"
		}),
		mock.Anything,
	).Return(nil).Once()
	activity := &RotateVcpToVsaCertificateActivity{
		SE: storage,
		deps: &certificateRotationDependencies{
			now: func() time.Time { return now },
			getCertificateAndPrivateKeyByID: func(
				hyperscaler2.Services,
				string,
				string,
				string,
				string,
				string,
			) (*hyperscalermodels.CustomCertificateResponse, error) {
				return &hyperscalermodels.CustomCertificateResponse{
					Certificate: &hyperscalermodels.CustomCertificate{
						CertOwningEntity: "project",
						CaGroupName:      "pool",
						CaName:           "ca",
						Region:           env.Region,
						PemCertificate: generateTestCertificate(
							now.Add(-10*time.Minute),
							now.Add(24*time.Hour),
						),
					},
				}, nil
			},
		},
	}

	err := activity.reconcileOneExpertModeCredential(
		ctx,
		pool,
		0,
		"project/pool/ca",
		hyperscaler2.NewMockServices(t),
		false,
		true,
	)

	require.NoError(t, err)
	storage.AssertExpectations(t)
}

func TestReconcileOneExpertModeCredential_WrongCANameRemintsWithoutBackfill(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	storage := database.NewMockStorage(t)
	cloudService := hyperscaler2.NewMockServices(t)
	expertCredentials := &datamodel.ExpertModeCredentials{
		ExpertModeCredential: []*datamodel.ExpertModeCredential{
			{
				CertificateID: "legacy-active",
				Username:      "expert",
				AuthType:      env.USER_CERTIFICATE,
			},
		},
	}
	pool := &datamodel.Pool{
		BaseModel:             datamodel.BaseModel{ID: 1, UUID: "pool-1"},
		State:                 "READY",
		DeploymentName:        "deployment",
		PoolCredentials:       &datamodel.PoolCredentials{CaURI: "project/pool/expected-ca"},
		ExpertModeCredentials: expertCredentials,
	}
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: *pool}}, nil).
		Once()
	storage.On(
		"CompareAndSwapExpertModeCredentials",
		ctx,
		pool.UUID,
		mock.MatchedBy(func(value *datamodel.ExpertModeCredentials) bool {
			return value.ExpertModeCredential[0].CaURI == ""
		}),
		mock.MatchedBy(func(value *datamodel.ExpertModeCredentials) bool {
			credential := value.ExpertModeCredential[0]
			return credential.CaURI == "" &&
				credential.CertificateIDNew != "" &&
				credential.CertificateIDNew != "legacy-active" &&
				credential.StagedCaURI == "project/pool/expected-ca"
		}),
		mock.Anything,
	).Return(nil).Once()
	storage.On("GetNodesByPoolID", ctx, int64(1)).
		Return(nil, errors.New("stop after remint")).
		Once()
	generateCalled := false
	activity := &RotateVcpToVsaCertificateActivity{
		SE: storage,
		deps: &certificateRotationDependencies{
			resolveCloudService: func(context.Context) (hyperscaler2.Services, error) {
				return cloudService, nil
			},
			getHyperscaler: func() string { return "oci" },
			now:            func() time.Time { return now },
			getCertificateAndPrivateKeyByID: func(
				hyperscaler2.Services,
				string,
				string,
				string,
				string,
				string,
			) (*hyperscalermodels.CustomCertificateResponse, error) {
				return &hyperscalermodels.CustomCertificateResponse{
					Certificate: &hyperscalermodels.CustomCertificate{
						CertOwningEntity: "project",
						CaGroupName:      "pool",
						CaName:           "wrong-ca",
						Region:           env.Region,
						PemCertificate: generateTestCertificate(
							now.Add(-10*time.Minute),
							now.Add(24*time.Hour),
						),
					},
				}, nil
			},
			generateExpertCertificate: func(
				hyperscaler2.Services,
				string,
				string,
				*datamodel.PoolCredentials,
				bool,
			) (*hyperscalermodels.CustomCertificateResponse, error) {
				generateCalled = true
				return &hyperscalermodels.CustomCertificateResponse{
					Certificate: &hyperscalermodels.CustomCertificate{
						CertificateID: "replacement",
					},
				}, nil
			},
		},
	}

	err := activity.reconcileOneExpertModeCredential(
		ctx,
		pool,
		0,
		"project/pool/expected-ca",
		cloudService,
		false,
		true,
	)

	require.ErrorContains(t, err, "stop after remint")
	require.True(t, generateCalled)
	storage.AssertExpectations(t)
}

// Expert mode certificates have their own lifetime, independent of the pool
// certificate. A forced rotation has to reach them too, otherwise an ONTAP mode pool
// comes out of a "forced" run with a new pool certificate and an untouched expert
// certificate.
func TestReconcileOneExpertModeCredential_ForceRotatesCurrentCertificate(t *testing.T) {
	const targetCAURI = "project/pool/expected-ca"

	newActivity := func(t *testing.T, storage *database.MockStorage,
		cloudService hyperscaler2.Services, generateCalled *bool) *RotateVcpToVsaCertificateActivity {
		t.Helper()
		now := time.Now()
		return &RotateVcpToVsaCertificateActivity{
			SE: storage,
			deps: &certificateRotationDependencies{
				resolveCloudService: func(context.Context) (hyperscaler2.Services, error) {
					return cloudService, nil
				},
				getHyperscaler: func() string { return "oci" },
				now:            func() time.Time { return now },
				generateExpertCertificate: func(
					hyperscaler2.Services,
					string,
					string,
					*datamodel.PoolCredentials,
					bool,
				) (*hyperscalermodels.CustomCertificateResponse, error) {
					*generateCalled = true
					return nil, errors.New("stop after mint")
				},
			},
		}
	}

	// An expert credential already on the target CA with a certificate that is nowhere
	// near expiry: the case the eligibility check would normally wave through.
	newPool := func() *datamodel.Pool {
		return &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: "pool-1"},
			State:          "READY",
			DeploymentName: "deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "pool-cert",
				CaURI:         targetCAURI,
				AuthType:      env.USER_CERTIFICATE,
			},
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						CertificateID: "expert-active",
						CaURI:         targetCAURI,
						Username:      "expert",
						AuthType:      env.USER_CERTIFICATE,
					},
				},
			},
		}
	}

	stubFreshCertificate := func(t *testing.T) {
		t.Helper()
		original := getCertificateFromCacheOrSecretManager
		t.Cleanup(func() { getCertificateFromCacheOrSecretManager = original })
		certificate := &models.Certificate{
			SignedCertificate: generateTestCertificate(
				time.Now().Add(-time.Hour), time.Now().Add(365*24*time.Hour),
			),
		}
		getCertificateFromCacheOrSecretManager = func(context.Context, *datamodel.PoolCredentials) (*models.Certificate, error) {
			return certificate, nil
		}
		// certificateNeedsRotation treats a cache miss as "rotate", so the certificate
		// has to be cached for it to be judged current.
		common.AddToCertAuthCache("expert-active", certificate)
		t.Cleanup(func() { common.RemoveFromCertAuthCache("expert-active") })
	}

	t.Run("WithoutForce_SkipsCurrentCertificate", func(tt *testing.T) {
		stubFreshCertificate(tt)
		storage := database.NewMockStorage(tt)
		pool := newPool()

		generateCalled := false
		activity := newActivity(tt, storage, hyperscaler2.NewMockServices(tt), &generateCalled)

		err := activity.reconcileOneExpertModeCredential(
			context.Background(), pool, 0, targetCAURI, hyperscaler2.NewMockServices(tt), false, true,
		)

		require.NoError(tt, err)
		require.False(tt, generateCalled, "a current expert certificate must not be reminted without force")
	})

	t.Run("WithForce_RemintsCurrentCertificate", func(tt *testing.T) {
		stubFreshCertificate(tt)
		storage := database.NewMockStorage(tt)
		pool := newPool()

		generateCalled := false
		activity := newActivity(tt, storage, hyperscaler2.NewMockServices(tt), &generateCalled)

		err := activity.reconcileOneExpertModeCredential(
			context.Background(), pool, 0, targetCAURI, hyperscaler2.NewMockServices(tt), true, true,
		)

		require.ErrorContains(tt, err, "stop after mint")
		require.True(tt, generateCalled, "force must remint a current expert certificate")
	})
}

func TestReconcileOneExpertModeCredential_LeftoverStagedCertIsNotReused(t *testing.T) {
	const targetCAURI = "project/pool/expected-ca"
	const leftoverID = "expert-previous"

	newPool := func() *datamodel.Pool {
		return &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: "pool-1"},
			State:          "READY",
			DeploymentName: "deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "pool-cert",
				CaURI:         targetCAURI,
				AuthType:      env.USER_CERTIFICATE,
			},
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{
					{
						CertificateID:    "expert-active",
						CaURI:            targetCAURI,
						CertificateIDNew: leftoverID,
						StagedCaURI:      targetCAURI,
						Username:         "expert",
						AuthType:         env.USER_CERTIFICATE,
					},
				},
			},
		}
	}

	stubFreshCertificate := func(t *testing.T) {
		t.Helper()
		original := getCertificateFromCacheOrSecretManager
		t.Cleanup(func() { getCertificateFromCacheOrSecretManager = original })
		certificate := &models.Certificate{
			SignedCertificate: generateTestCertificate(
				time.Now().Add(-time.Hour), time.Now().Add(365*24*time.Hour),
			),
		}
		getCertificateFromCacheOrSecretManager = func(context.Context, *datamodel.PoolCredentials) (*models.Certificate, error) {
			return certificate, nil
		}
		common.AddToCertAuthCache("expert-active", certificate)
		t.Cleanup(func() { common.RemoveFromCertAuthCache("expert-active") })
	}

	expectDetachLeftover := func(t *testing.T, storage *database.MockStorage, pool *datamodel.Pool, revoked *string) {
		t.Helper()
		storage.On("ListPools", mock.Anything, mock.Anything).
			Return([]*datamodel.PoolView{{Pool: *pool}}, nil)
		storage.On(
			"CompareAndSwapExpertModeCredentials",
			mock.Anything,
			pool.UUID,
			mock.MatchedBy(func(value *datamodel.ExpertModeCredentials) bool {
				return value.ExpertModeCredential[0].CertificateIDNew == leftoverID
			}),
			mock.MatchedBy(func(value *datamodel.ExpertModeCredentials) bool {
				credential := value.ExpertModeCredential[0]
				return credential.CertificateID == "expert-active" &&
					credential.CertificateIDNew == "" &&
					credential.StagedCaURI == ""
			}),
			mock.Anything,
		).Return(nil).Once()
		originalRevoke := revokeCertificateAndDeleteFromCacheAndSecretManager
		t.Cleanup(func() { revokeCertificateAndDeleteFromCacheAndSecretManager = originalRevoke })
		revokeCertificateAndDeleteFromCacheAndSecretManager = func(
			_ hyperscaler2.Services,
			creds *datamodel.PoolCredentials,
		) error {
			*revoked = creds.CertificateID
			return nil
		}
	}

	t.Run("WithoutForce_DetachesLeftoverAndSkips", func(tt *testing.T) {
		stubFreshCertificate(tt)
		storage := database.NewMockStorage(tt)
		pool := newPool()
		var revoked string
		expectDetachLeftover(tt, storage, pool, &revoked)
		generateCalled := false
		activity := &RotateVcpToVsaCertificateActivity{
			SE: storage,
			deps: &certificateRotationDependencies{
				resolveCloudService: func(context.Context) (hyperscaler2.Services, error) {
					return hyperscaler2.NewMockServices(tt), nil
				},
				getHyperscaler: func() string { return "oci" },
				now:            time.Now,
				generateExpertCertificate: func(
					hyperscaler2.Services,
					string,
					string,
					*datamodel.PoolCredentials,
					bool,
				) (*hyperscalermodels.CustomCertificateResponse, error) {
					generateCalled = true
					return nil, errors.New("must not mint")
				},
			},
		}

		err := activity.reconcileOneExpertModeCredential(
			context.Background(), pool, 0, targetCAURI, hyperscaler2.NewMockServices(tt), false, true,
		)

		require.NoError(tt, err)
		require.False(tt, generateCalled)
		require.Equal(tt, leftoverID, revoked)
		storage.AssertExpectations(tt)
	})

	t.Run("WithForce_DetachesLeftoverAndRemints", func(tt *testing.T) {
		stubFreshCertificate(tt)
		storage := database.NewMockStorage(tt)
		pool := newPool()
		var revoked string
		expectDetachLeftover(tt, storage, pool, &revoked)
		generateCalled := false
		activity := &RotateVcpToVsaCertificateActivity{
			SE: storage,
			deps: &certificateRotationDependencies{
				resolveCloudService: func(context.Context) (hyperscaler2.Services, error) {
					return hyperscaler2.NewMockServices(tt), nil
				},
				getHyperscaler: func() string { return "oci" },
				now:            time.Now,
				generateExpertCertificate: func(
					hyperscaler2.Services,
					string,
					string,
					*datamodel.PoolCredentials,
					bool,
				) (*hyperscalermodels.CustomCertificateResponse, error) {
					generateCalled = true
					return nil, errors.New("stop after mint")
				},
			},
		}

		err := activity.reconcileOneExpertModeCredential(
			context.Background(), pool, 0, targetCAURI, hyperscaler2.NewMockServices(tt), true, true,
		)

		require.ErrorContains(tt, err, "stop after mint")
		require.True(tt, generateCalled)
		require.Equal(tt, leftoverID, revoked)
		storage.AssertExpectations(tt)
	})
}

func TestFetchValidatedAuthReconciliationCandidate_AcceptsNonPrefixedStagedID(t *testing.T) {
	now := time.Now()
	const stagedID = "legacy-arbitrary-certificate-id"
	fetchedID := ""
	activity := &RotateVcpToVsaCertificateActivity{
		deps: &certificateRotationDependencies{
			now: func() time.Time { return now },
			getCertificateAndPrivateKeyByID: func(
				_ hyperscaler2.Services,
				_, _, _, _, certificateID string,
			) (*hyperscalermodels.CustomCertificateResponse, error) {
				fetchedID = certificateID
				return &hyperscalermodels.CustomCertificateResponse{
					Certificate: &hyperscalermodels.CustomCertificate{
						CertOwningEntity: "project",
						CaGroupName:      "pool",
						CaName:           "ca",
						Region:           env.Region,
						PemCertificate: generateTestCertificate(
							now.Add(-10*time.Minute),
							now.Add(24*time.Hour),
						),
					},
				}, nil
			},
		},
	}

	response, err := activity.fetchValidatedAuthReconciliationCandidate(
		hyperscaler2.NewMockServices(t),
		"project/pool/ca",
		stagedID,
	)

	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, stagedID, fetchedID)
}

func TestClassifyAuthType1Pool(t *testing.T) {
	tests := []struct {
		name string
		pool *datamodel.Pool
		want AuthType1PoolClassification
	}{
		{
			name: "native password pool",
			pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{SecretID: "secret", Username: "admin"}},
			want: AuthType1PoolNative,
		},
		{
			name: "demoted with active certificate",
			pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{CertificateID: "stale-cert"}},
			want: AuthType1PoolDemoted,
		},
		{
			name: "demoted with staged CA",
			pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{StagedCaURI: "project/pool/ca"}},
			want: AuthType1PoolDemoted,
		},
		{
			name: "demoted by fallback username marker",
			pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{
				Username:           "admin",
				UsernamePreCertFix: "deployment_admin",
			}},
			want: AuthType1PoolDemoted,
		},
		{
			name: "native pool with DNS but no certificate evidence",
			pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{}},
			want: AuthType1PoolNative,
		},
		{
			name: "native pool with certificate-like username but no certificate evidence",
			pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{Username: "deployment_admin"}},
			want: AuthType1PoolNative,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, classifyAuthType1Pool(test.pool))
		})
	}
}

func TestValidateAuthType1ReconciliationPool(t *testing.T) {
	targetCAURI := "project/pool/ca"
	tests := []struct {
		name            string
		pool            *datamodel.Pool
		alreadyPromoted bool
		skip            bool
		wantErr         string
	}{
		{name: "missing credentials", pool: &datamodel.Pool{}, wantErr: "Pool credentials are missing"},
		{
			name: "already promoted",
			pool: &datamodel.Pool{
				State:        "READY",
				ServiceLevel: "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType:      env.USER_CERTIFICATE,
					CertificateID: "certificate",
					CaURI:         targetCAURI,
				},
			},
			alreadyPromoted: true,
		},
		{
			name: "native password pool",
			pool: &datamodel.Pool{
				State:           "READY",
				ServiceLevel:    "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{AuthType: env.USERNAME_PWD_SEC_MGR},
			},
			skip: true,
		},
		{
			name: "ineligible state",
			pool: &datamodel.Pool{
				State:        "DELETING",
				ServiceLevel: "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType:      env.USERNAME_PWD_SEC_MGR,
					CertificateID: "certificate",
				},
			},
			wantErr: "invalid state",
		},
		{
			name: "eligible demoted pool",
			pool: &datamodel.Pool{
				State:        "READY",
				ServiceLevel: "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType:      env.USERNAME_PWD_SEC_MGR,
					CertificateID: "certificate",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			alreadyPromoted, skip, err := validateAuthType1ReconciliationPool(
				test.pool,
				"pool-1",
				targetCAURI,
			)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.alreadyPromoted, alreadyPromoted)
			assert.Equal(t, test.skip, skip)
		})
	}
}

func TestAuthReconciliationUsernamesRestoresFallbackIdentity(t *testing.T) {
	pool := &datamodel.Pool{
		DeploymentName: "deployment",
		PoolCredentials: &datamodel.PoolCredentials{
			Username:           "admin",
			UsernamePreCertFix: "deployment_admin",
		},
	}

	bootstrapUsername, certificateUsername, err := authReconciliationUsernames(pool)

	require.NoError(t, err)
	assert.Equal(t, "admin", bootstrapUsername)
	assert.Equal(t, "deployment_admin", certificateUsername)
}

func TestAuthReconciliationUsernamesRejectsFallbackWithoutSavedIdentity(t *testing.T) {
	pool := &datamodel.Pool{
		PoolCredentials: &datamodel.PoolCredentials{Username: "admin"},
	}

	_, _, err := authReconciliationUsernames(pool)

	require.ErrorContains(t, err, "missing username_pre_cert_fix")
}

func TestAuthReconciliationUsernamesInfersFallbackIdentityFromExpertCertificate(t *testing.T) {
	tests := []struct {
		name       string
		credential *datamodel.ExpertModeCredential
	}{
		{
			name: "active certificate ID",
			credential: &datamodel.ExpertModeCredential{
				AuthType:      env.USERNAME_PWD_SEC_MGR,
				Username:      expertDemotionFallbackUsername,
				CertificateID: "gcnv-6863ca54640e365-cert-d73249c1_gadmin-20260819-000148",
			},
		},
		{
			name: "staged certificate ID",
			credential: &datamodel.ExpertModeCredential{
				AuthType:         env.USERNAME_PWD_SEC_MGR,
				Username:         expertDemotionFallbackUsername,
				CertificateIDNew: "gcnv-6863ca54640e365-cert-d73249c1_gadmin-20260819-000228",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := &datamodel.Pool{
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType: env.USERNAME_PWD_SEC_MGR,
					Username: demotionFallbackAdminUsername,
				},
				ExpertModeCredentials: &datamodel.ExpertModeCredentials{
					ExpertModeCredential: []*datamodel.ExpertModeCredential{test.credential},
				},
			}

			bootstrapUsername, certificateUsername, err := authReconciliationUsernames(pool)

			require.NoError(t, err)
			assert.Equal(t, demotionFallbackAdminUsername, bootstrapUsername)
			assert.Equal(t, "d73249c1_admin", certificateUsername)
		})
	}
}

func TestAuthReconciliationUsernamesRejectsConflictingInferredIdentities(t *testing.T) {
	pool := &datamodel.Pool{
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: env.USERNAME_PWD_SEC_MGR,
			Username: demotionFallbackAdminUsername,
		},
		ExpertModeCredentials: &datamodel.ExpertModeCredentials{
			ExpertModeCredential: []*datamodel.ExpertModeCredential{
				{
					AuthType:      env.USERNAME_PWD_SEC_MGR,
					Username:      expertDemotionFallbackUsername,
					CertificateID: "gcnv-deployment-cert-d73249c1_gadmin-20260819-000148",
				},
				{
					AuthType:      env.USERNAME_PWD_SEC_MGR,
					Username:      expertDemotionFallbackUsername,
					CertificateID: "gcnv-deployment-cert-ea4157ee_gadmin-20260819-000332",
				},
			},
		},
	}

	_, _, err := authReconciliationUsernames(pool)

	require.ErrorContains(t, err, "conflicting usernames")
}

func TestDemotedExpertCredentialIdentity(t *testing.T) {
	tests := []struct {
		name       string
		credential *datamodel.ExpertModeCredential
		expected   string
	}{
		{
			name: "persisted pre-demotion username",
			credential: &datamodel.ExpertModeCredential{
				AuthType:           env.USERNAME_PWD_SEC_MGR,
				Username:           expertDemotionFallbackUsername,
				UsernamePreCertFix: "deployment_gadmin",
			},
			expected: "deployment_gadmin",
		},
		{
			name: "legacy active certificate ID",
			credential: &datamodel.ExpertModeCredential{
				AuthType:      env.USERNAME_PWD_SEC_MGR,
				Username:      expertDemotionFallbackUsername,
				CertificateID: "gcnv-6863ca54640e365-cert-d73249c1_gadmin-20260819-000148",
			},
			expected: "d73249c1_gadmin",
		},
		{
			name: "legacy staged certificate ID",
			credential: &datamodel.ExpertModeCredential{
				AuthType:         env.USERNAME_PWD_SEC_MGR,
				Username:         expertDemotionFallbackUsername,
				CertificateIDNew: "gcnv-6863ca54640e365-cert-d73249c1_gadmin-20260819-000228",
			},
			expected: "d73249c1_gadmin",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.True(t, isDemotedExpertCredential(test.credential))
			assert.Equal(t, test.expected, expertCertificateUsername(test.credential))
		})
	}
}

func TestExpertUsernameFromCertificateID(t *testing.T) {
	tests := []struct {
		name          string
		certificateID string
		expected      string
		inferred      bool
	}{
		{
			name:          "production legacy certificate ID",
			certificateID: "gcnv-6863ca54640e365-cert-d73249c1_gadmin-20260819-000148",
			expected:      "d73249c1_gadmin",
			inferred:      true,
		},
		{
			name:          "deployment name containing cert marker",
			certificateID: "gcnv-cert-pool-cert-d73249c1_gadmin-20260819-000148",
			expected:      "d73249c1_gadmin",
			inferred:      true,
		},
		{
			name:          "parent certificate has no expert username",
			certificateID: "gcnv-6863ca54640e365-cert-20260820-050142",
		},
		{
			name:          "fallback username is not an original identity",
			certificateID: "gcnv-6863ca54640e365-cert-gcnv_gadmin-20260819-000148",
		},
		{
			name:          "non-hex username is not inferred",
			certificateID: "gcnv-6863ca54640e365-cert-username_gadmin-20260819-000148",
		},
		{
			name:          "malformed timestamp",
			certificateID: "gcnv-6863ca54640e365-cert-d73249c1_gadmin-not-a-time",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			username, inferred := expertUsernameFromCertificateID(test.certificateID)

			assert.Equal(t, test.inferred, inferred)
			assert.Equal(t, test.expected, username)
		})
	}
}

func TestReconcileOneExpertModeCredentialRejectsFallbackWithoutSavedIdentity(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	pool := datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		ExpertModeCredentials: &datamodel.ExpertModeCredentials{
			ExpertModeCredential: []*datamodel.ExpertModeCredential{{
				AuthType: env.USERNAME_PWD_SEC_MGR,
				Username: expertDemotionFallbackUsername,
			}},
		},
	}

	err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
		reconcileOneExpertModeCredential(
			ctx,
			&pool,
			0,
			"project/pool/ca",
			hyperscaler2.NewMockServices(t),
			false,
			true,
		)

	require.ErrorContains(t, err, "missing username_pre_cert_fix")
	storage.AssertExpectations(t)
}

func TestReconcileOneExpertModeCredentialUsesFallbackBootstrapAndCertificateIdentity(t *testing.T) {
	tests := []struct {
		name               string
		usernamePreCertFix string
		certificateID      string
		expectedUsername   string
	}{
		{
			name:               "saved identity",
			usernamePreCertFix: "deployment_gadmin",
			certificateID:      "expired-expert-cert",
			expectedUsername:   "deployment_gadmin",
		},
		{
			name:             "identity inferred from legacy certificate ID",
			certificateID:    "gcnv-6863ca54640e365-cert-d73249c1_gadmin-20260819-000148",
			expectedUsername: "d73249c1_gadmin",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			storage := database.NewMockStorage(t)
			cloudService := hyperscaler2.NewMockServices(t)
			pool := datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1, UUID: "pool-1"},
				State:          "READY",
				DeploymentName: "deployment",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType: env.USERNAME_PWD_SEC_MGR,
					Username: "admin",
					SecretID: "admin-secret",
					CaURI:    "project/pool/expired-ca",
				},
				ExpertModeCredentials: &datamodel.ExpertModeCredentials{
					ExpertModeCredential: []*datamodel.ExpertModeCredential{{
						AuthType:           env.USERNAME_PWD_SEC_MGR,
						Username:           expertDemotionFallbackUsername,
						UsernamePreCertFix: test.usernamePreCertFix,
						SecretID:           "admin-secret",
						CertificateID:      test.certificateID,
					}},
				},
			}
			storage.On("ListPools", ctx, mock.Anything).
				Return([]*datamodel.PoolView{{Pool: pool}}, nil).
				Once()
			storage.On(
				"CompareAndSwapExpertModeCredentials",
				ctx,
				pool.UUID,
				mock.Anything,
				mock.MatchedBy(func(credentials *datamodel.ExpertModeCredentials) bool {
					staged := credentials.ExpertModeCredential[0]
					return staged.Username == expertDemotionFallbackUsername &&
						staged.UsernamePreCertFix == test.usernamePreCertFix &&
						staged.CertificateIDNew != "" &&
						staged.StagedCaURI == "project/pool/new-ca"
				}),
				mock.Anything,
			).Return(nil).Once()
			generatedUsername := ""
			activity := &RotateVcpToVsaCertificateActivity{
				SE: storage,
				deps: &certificateRotationDependencies{
					resolveCloudService: func(context.Context) (hyperscaler2.Services, error) {
						return cloudService, nil
					},
					getHyperscaler: func() string { return "oci" },
					generateExpertCertificate: func(
						_ hyperscaler2.Services,
						_ string,
						username string,
						_ *datamodel.PoolCredentials,
						_ bool,
					) (*hyperscalermodels.CustomCertificateResponse, error) {
						generatedUsername = username
						return &hyperscalermodels.CustomCertificateResponse{
							Certificate: &hyperscalermodels.CustomCertificate{},
							Secret:      &hyperscalermodels.CustomSecret{},
						}, nil
					},
					installExpertClientTrust: func(
						_ context.Context,
						installPool *datamodel.Pool,
						_, _ interface{},
					) error {
						assert.Equal(t, "admin", installPool.PoolCredentials.Username)
						return errors.New("stop after password install")
					},
				},
			}

			err := activity.reconcileOneExpertModeCredential(
				ctx,
				&pool,
				0,
				"project/pool/new-ca",
				cloudService,
				false,
				true,
			)

			require.ErrorContains(t, err, "stop after password install")
			assert.Equal(t, test.expectedUsername, generatedUsername)
			storage.AssertExpectations(t)
		})
	}
}

func TestShouldReuseAuthReconciliationCertificate(t *testing.T) {
	tests := []struct {
		name          string
		certificateID string
		stagedCaURI   string
		targetCaURI   string
		want          bool
	}{
		{
			name:          "matching reconciliation certificate and CA",
			certificateID: "pool-cert-reconcile-123",
			stagedCaURI:   "project/pool/ca",
			targetCaURI:   "project/pool/ca",
			want:          true,
		},
		{
			name:          "valid non-reconcile-prefixed staged ID is eligible",
			certificateID: "pool-cert-previous",
			stagedCaURI:   "project/pool/ca",
			targetCaURI:   "project/pool/ca",
			want:          true,
		},
		{
			name:          "certificate under previous CA is not reused",
			certificateID: "pool-cert-reconcile-123",
			stagedCaURI:   "project/pool/old-ca",
			targetCaURI:   "project/pool/new-ca",
		},
		{
			name:          "legacy unpaired certificate is not reused directly",
			certificateID: "pool-cert-reconcile-123",
			targetCaURI:   "project/pool/ca",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, shouldReuseAuthReconciliationCertificate(
				test.certificateID,
				test.stagedCaURI,
				test.targetCaURI,
			))
		})
	}
}

func TestHasExpertModeCredentialsFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		pool *datamodel.Pool
		want bool
	}{
		{name: "none", pool: &datamodel.Pool{}, want: false},
		{name: "empty set", pool: &datamodel.Pool{ExpertModeCredentials: &datamodel.ExpertModeCredentials{}}, want: false},
		{
			name: "password credential",
			pool: &datamodel.Pool{ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{{AuthType: env.USERNAME_PWD_SEC_MGR}},
			}},
			want: true,
		},
		{
			name: "certificate credential also blocked",
			pool: &datamodel.Pool{ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: []*datamodel.ExpertModeCredential{{AuthType: env.USER_CERTIFICATE, CertificateID: "old-expert-cert"}},
			}},
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, hasExpertModeCredentials(test.pool))
		})
	}
}

func TestReconcileAuthType1PoolSkipsNativePoolWithExpertCredentials(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	pool := datamodel.Pool{
		BaseModel:    datamodel.BaseModel{UUID: "pool-1", ID: 1},
		State:        "READY",
		ServiceLevel: "FLEX",
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: env.USERNAME_PWD_SEC_MGR,
		},
		ExpertModeCredentials: &datamodel.ExpertModeCredentials{
			ExpertModeCredential: []*datamodel.ExpertModeCredential{{
				AuthType:      env.USER_CERTIFICATE,
				CertificateID: "old-expert-cert",
			}},
		},
	}
	storage.On("ListPools", ctx, mock.Anything).Return([]*datamodel.PoolView{{Pool: pool}}, nil).Once()

	err := (&RotateVcpToVsaCertificateActivity{SE: storage}).ReconcileAuthType1Pool(ctx, pool.UUID)

	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetNodesByPoolID", mock.Anything, mock.Anything)
	storage.AssertExpectations(t)
}

func TestReconcileAuthType1PoolRunsCertificatePreflightBeforeMint(t *testing.T) {
	const targetCAURI = "project/pool/new-ca"
	ctx := context.Background()
	t.Setenv("CERTIFICATE_LIFETIME", "1h")
	t.Setenv("MINIMUM_ISSUING_CA_REMAINING_LIFETIME", "1h")

	rootPEM, intermediatePEM := createCAChain(t)
	cloud := &certificateAuthorityCloud{
		ca: &hyperscalermodels.CertificateAuthority{
			State:               "ENABLED",
			ExpireTime:          time.Now().Add(24 * time.Hour),
			PemCertificateChain: []string{intermediatePEM, rootPEM},
		},
	}
	provider := &accountCapableProvider{
		MockProvider: vsa.NewMockProvider(t),
		accounts: []*vsa.SecurityAccount{
			certificateLoginAccount("owner"),
			certificateLoginAccount("owner"),
		},
	}
	provider.On("InstallClientCA", mock.Anything).
		Return(&vsa.ServerCertificateResponse{}, nil).
		Twice()
	provider.On("GetServerCertificates", mock.Anything).
		Return([]*vsa.ServerCertificateResponse{{PublicCertificate: rootPEM}}, nil).
		Twice()
	provider.On("InstallServerCA", mock.Anything).
		Return(&vsa.ServerCertificateResponse{}, nil).
		Twice()
	provider.On("GetServerCertificates", mock.Anything).
		Return([]*vsa.ServerCertificateResponse{{PublicCertificate: intermediatePEM}}, nil).
		Twice()
	provider.On("InstallServerCertificate", mock.Anything).
		Return(&vsa.ServerCertificateResponse{SerialNumber: "serial"}, nil).
		Once()
	provider.On("ModifySSL", mock.Anything).
		Return(&vsa.ModifySSLResponse{Success: true}, nil).
		Once()
	ontapVersion := "9.19.1"
	provider.On("GetONTAPVersion").
		Return(&ontapVersion, nil).
		Once()

	pool := datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: "pool-1", ID: 1},
		State:          "READY",
		ServiceLevel:   "FLEX",
		DeploymentName: "deployment",
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType:           env.USERNAME_PWD_SEC_MGR,
			Username:           "admin",
			UsernamePreCertFix: "deployment_admin",
			SecretID:           "admin-secret",
			Password:           "password",
			CertificateID:      "old-cert",
			CaURI:              "project/pool/old-ca",
		},
	}
	nodes := []*datamodel.Node{{
		Name:            "node-1",
		EndpointAddress: "10.0.0.1",
		HostDNSName:     "node-1.example.com",
	}}
	storage := database.NewMockStorage(t)
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: pool}}, nil).
		Twice()
	storage.On("GetNodesByPoolID", ctx, pool.ID).
		Return(nodes, nil).
		Times(5)
	storage.On("UpdateNodeHostDNSNames", ctx, pool.ID, map[string]string{
		"10.0.0.1": "node-1.example.com",
	}).Return(nil).Once()
	storage.On(
		"CompareAndSwapPoolCredentials",
		ctx,
		pool.UUID,
		env.USERNAME_PWD_SEC_MGR,
		"",
		"",
		mock.Anything,
		mock.MatchedBy(func(credentials *datamodel.PoolCredentials) bool {
			return credentials.CertificateIDNew != "" &&
				credentials.StagedCaURI == targetCAURI
		}),
	).Return(nil).Once()
	stagedPool := pool
	stagedCredentials := *pool.PoolCredentials
	stagedCredentials.CertificateIDNew = "deployment-cert-20260908-000000"
	stagedCredentials.StagedCaURI = targetCAURI
	stagedPool.PoolCredentials = &stagedCredentials
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: stagedPool}}, nil).
		Once()
	storage.On(
		"CompareAndSwapPoolCredentials",
		ctx,
		pool.UUID,
		env.USERNAME_PWD_SEC_MGR,
		"deployment-cert-20260908-000000",
		targetCAURI,
		mock.Anything,
		mock.MatchedBy(func(credentials *datamodel.PoolCredentials) bool {
			return credentials.AuthType == env.USER_CERTIFICATE &&
				credentials.CertificateID == "deployment-cert-20260908-000000" &&
				credentials.CaURI == targetCAURI
		}),
	).Return(nil).Once()

	originalCloudService := getGcpServiceForCerts
	originalGenerate := generateAndCreateCertificateForVSACluster
	originalGetProvider := vsa.GetProviderByNode
	originalGetCertificate := vsa.GetCertificateAndPrivateKeyByID
	t.Cleanup(func() {
		getGcpServiceForCerts = originalCloudService
		generateAndCreateCertificateForVSACluster = originalGenerate
		vsa.GetProviderByNode = originalGetProvider
		vsa.GetCertificateAndPrivateKeyByID = originalGetCertificate
	})
	getGcpServiceForCerts = func(context.Context) (hyperscaler2.Services, error) {
		return cloud, nil
	}
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}
	vsa.GetCertificateAndPrivateKeyByID = func(
		hyperscaler2.Services,
		string,
		string,
		string,
		string,
		string,
	) (*hyperscalermodels.CustomCertificateResponse, error) {
		return nil, errors.New("cache refresh unavailable")
	}
	generateAndCreateCertificateForVSACluster = func(
		hyperscaler2.Services,
		string,
		string,
		*datamodel.PoolCredentials,
		bool,
	) (*hyperscalermodels.CustomCertificateResponse, error) {
		return nil, nil
	}

	certificateFetches := 0
	activity := &RotateVcpToVsaCertificateActivity{
		SE: storage,
		deps: &certificateRotationDependencies{
			resolveCloudService: func(context.Context) (hyperscaler2.Services, error) {
				return cloud, nil
			},
			getProvider: func(
				context.Context,
				*datamodel.Pool,
				[]*datamodel.Node,
				int,
				string,
			) (vsa.Provider, error) {
				return provider, nil
			},
			getCertificateAndPrivateKeyByID: func(
				hyperscaler2.Services,
				string,
				string,
				string,
				string,
				string,
			) (*hyperscalermodels.CustomCertificateResponse, error) {
				certificateFetches++
				if certificateFetches > 1 {
					return nil, errors.New("cache refresh unavailable")
				}
				return &hyperscalermodels.CustomCertificateResponse{
					Certificate: &hyperscalermodels.CustomCertificate{
						CertificateID:  "new-cert",
						PemCertificate: "certificate",
						SerialNumber:   "serial",
						CaName:         "new-ca",
					},
					Secret: &hyperscalermodels.CustomSecret{
						SecretVersion: &hyperscalermodels.CustomSecretVersion{Value: "private-key"},
					},
				}, nil
			},
			now: func() time.Time {
				return time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC)
			},
		},
	}

	err := activity.ReconcileAuthType1PoolForDesiredCA(ctx, pool.UUID, targetCAURI)

	require.ErrorContains(t, err, "refresh promoted certificate cache")
	require.Equal(t, 2, provider.getCallNumber)
	provider.AssertExpectations(t)
	storage.AssertExpectations(t)
}

func TestReconcileAuthType1PoolRejectsInvalidRuntimeState(t *testing.T) {
	tests := []struct {
		name      string
		pool      datamodel.Pool
		nodes     []*datamodel.Node
		nodesErr  error
		wantError string
	}{
		{
			name: "credentials missing",
			pool: datamodel.Pool{
				BaseModel:    datamodel.BaseModel{UUID: "pool-1", ID: 1},
				State:        "READY",
				ServiceLevel: "FLEX",
			},
			wantError: "Pool credentials are missing or invalid",
		},
		{
			name: "auth type changed",
			pool: datamodel.Pool{
				BaseModel:    datamodel.BaseModel{UUID: "pool-1", ID: 1},
				State:        "READY",
				ServiceLevel: "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType: env.USERNAME_PWD,
				},
			},
			wantError: "Resource is in an invalid state",
		},
		{
			name: "state no longer eligible",
			pool: datamodel.Pool{
				BaseModel:    datamodel.BaseModel{UUID: "pool-1", ID: 1},
				State:        "DELETING",
				ServiceLevel: "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType:      env.USERNAME_PWD_SEC_MGR,
					CertificateID: "old-cert",
				},
			},
			wantError: "Resource is in an invalid state",
		},
		{
			name: "node lookup fails",
			pool: datamodel.Pool{
				BaseModel:    datamodel.BaseModel{UUID: "pool-1", ID: 1},
				State:        "READY",
				ServiceLevel: "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType:      env.USERNAME_PWD_SEC_MGR,
					CertificateID: "old-cert",
				},
			},
			nodesErr:  errors.New("database unavailable"),
			wantError: "An internal error occurred",
		},
		{
			name: "pool has no nodes",
			pool: datamodel.Pool{
				BaseModel:    datamodel.BaseModel{UUID: "pool-1", ID: 1},
				State:        "READY",
				ServiceLevel: "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType:      env.USERNAME_PWD_SEC_MGR,
					CertificateID: "old-cert",
				},
			},
			wantError: "Pool has no associated nodes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			storage := database.NewMockStorage(t)
			storage.On("ListPools", ctx, mock.Anything).
				Return([]*datamodel.PoolView{{Pool: test.pool}}, nil).
				Once()
			if test.pool.PoolCredentials != nil &&
				test.pool.PoolCredentials.AuthType == env.USERNAME_PWD_SEC_MGR &&
				test.pool.State == "READY" {
				storage.On("GetNodesByPoolID", ctx, test.pool.ID).
					Return(test.nodes, test.nodesErr).
					Once()
			}

			err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
				ReconcileAuthType1PoolForDesiredCA(ctx, test.pool.UUID, "project/pool/ca")

			require.ErrorContains(t, err, test.wantError)
			storage.AssertExpectations(t)
		})
	}
}

func TestAuthReconciliationErrorAndCompatibilityPaths(t *testing.T) {
	t.Run("username defaults", func(t *testing.T) {
		bootstrap, username, err := authReconciliationUsernames(nil)
		require.Error(t, err)
		require.Equal(t, "admin", bootstrap)
		require.Empty(t, username)

		bootstrap, username, err = authReconciliationUsernames(&datamodel.Pool{
			DeploymentName:  "deployment",
			PoolCredentials: &datamodel.PoolCredentials{},
		})
		require.NoError(t, err)
		require.Equal(t, "admin", bootstrap)
		require.Equal(t, "deployment_admin", username)
	})

	t.Run("candidate lookup failures", func(t *testing.T) {
		activity := &RotateVcpToVsaCertificateActivity{deps: &certificateRotationDependencies{
			getCertificateAndPrivateKeyByID: func(
				hyperscaler2.Services,
				string,
				string,
				string,
				string,
				string,
			) (*hyperscalermodels.CustomCertificateResponse, error) {
				return nil, errors.New("lookup failed")
			},
		}}
		_, err := activity.fetchValidatedAuthReconciliationCandidate(nil, "project/pool/ca", "cert")
		require.ErrorContains(t, err, "lookup failed")

		activity.deps.getCertificateAndPrivateKeyByID = func(
			hyperscaler2.Services,
			string,
			string,
			string,
			string,
			string,
		) (*hyperscalermodels.CustomCertificateResponse, error) {
			return nil, nil
		}
		_, err = activity.fetchValidatedAuthReconciliationCandidate(nil, "project/pool/ca", "cert")
		require.ErrorContains(t, err, "nil response")
	})

	t.Run("DNS rejects malformed nodes", func(t *testing.T) {
		original := getGcpServiceForCerts
		t.Cleanup(func() { getGcpServiceForCerts = original })
		getGcpServiceForCerts = func(context.Context) (hyperscaler2.Services, error) {
			return &certificateAuthorityCloud{}, nil
		}
		activity := &RotateVcpToVsaCertificateActivity{SE: database.NewMockStorage(t)}
		err := activity.ensureAuthReconciliationDNS(
			context.Background(),
			&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
			[]*datamodel.Node{{
				Name:            "node-1",
				EndpointAddress: "10.0.0.1",
				HostDNSName:     "node-1.example.com",
			}, nil},
		)
		require.ErrorContains(t, err, "without an endpoint address")
	})

	t.Run("DNS creates and persists missing hostname", func(t *testing.T) {
		ctx := context.Background()
		originalCloud := getGcpServiceForCerts
		originalDNS := vsa.GetOrCreateCloudDNSRecord
		t.Cleanup(func() {
			getGcpServiceForCerts = originalCloud
			vsa.GetOrCreateCloudDNSRecord = originalDNS
		})
		getGcpServiceForCerts = func(context.Context) (hyperscaler2.Services, error) {
			return &certificateAuthorityCloud{}, nil
		}
		vsa.GetOrCreateCloudDNSRecord = func(
			hyperscaler2.Services,
			string,
			string,
		) (*hyperscalermodels.CustomCloudDNSRecord, error) {
			return &hyperscalermodels.CustomCloudDNSRecord{RecordName: "node-1.example.com"}, nil
		}
		storage := database.NewMockStorage(t)
		storage.On("UpdateNodeHostDNSNames", ctx, int64(1), map[string]string{
			"10.0.0.1": "node-1.example.com",
		}).Return(nil).Once()
		node := &datamodel.Node{Name: "node-1", EndpointAddress: "10.0.0.1"}

		err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
			ensureAuthReconciliationDNS(
				ctx,
				&datamodel.Pool{
					BaseModel:      datamodel.BaseModel{UUID: "pool-1", ID: 1},
					DeploymentName: "deployment",
				},
				[]*datamodel.Node{node},
			)

		require.NoError(t, err)
		require.Equal(t, "node-1.example.com", node.HostDNSName)
	})

	t.Run("certificate account preparation reports secret failure", func(t *testing.T) {
		original := vsa.GetPasswordFromCacheOrSecretManager
		t.Cleanup(func() { vsa.GetPasswordFromCacheOrSecretManager = original })
		vsa.GetPasswordFromCacheOrSecretManager = func(context.Context, string) (string, error) {
			return "", errors.New("secret unavailable")
		}
		err := (&RotateVcpToVsaCertificateActivity{}).
			preparePoolCertificateAuthentication(
				context.Background(),
				&datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{SecretID: "secret"}},
				nil,
				"admin",
				"deployment_admin",
			)
		require.Error(t, err)
	})

	t.Run("staging rejects changed row", func(t *testing.T) {
		tests := []struct {
			name string
			pool datamodel.Pool
			want string
		}{
			{name: "missing credentials", pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			}, want: "has no credentials"},
			{name: "auth changed", pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{UUID: "pool-1"},
				State:           "READY",
				ServiceLevel:    "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{AuthType: env.USER_CERTIFICATE},
			}, want: "auth type changed"},
			{name: "state changed", pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{UUID: "pool-1"},
				State:           "DELETING",
				ServiceLevel:    "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{AuthType: env.USERNAME_PWD_SEC_MGR},
			}, want: "state changed"},
			{name: "staged pair changed", pool: datamodel.Pool{
				BaseModel:    datamodel.BaseModel{UUID: "pool-1"},
				State:        "READY",
				ServiceLevel: "FLEX",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType:         env.USERNAME_PWD_SEC_MGR,
					CertificateIDNew: "concurrent-cert",
					StagedCaURI:      "project/pool/ca",
				},
			}, want: "pair changed"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				ctx := context.Background()
				storage := database.NewMockStorage(t)
				storage.On("ListPools", ctx, mock.Anything).
					Return([]*datamodel.PoolView{{Pool: test.pool}}, nil).
					Once()
				err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
					stageAuthReconciliationCertificate(
						ctx,
						test.pool.UUID,
						"new-cert",
						"project/pool/ca",
						"",
						"",
					)
				require.ErrorContains(t, err, test.want)
			})
		}
	})

	t.Run("rollback rejects changed row", func(t *testing.T) {
		ctx := context.Background()
		storage := database.NewMockStorage(t)
		pool := datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateIDNew: "different-cert",
				StagedCaURI:      "project/pool/ca",
			},
		}
		storage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{{Pool: pool}}, nil).
			Once()
		err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
			rollbackAuthReconciliationStage(ctx, pool.UUID, "expected-cert", "project/pool/ca")
		require.ErrorContains(t, err, "pair changed")
	})

	t.Run("rollback clears matching staged pair", func(t *testing.T) {
		ctx := context.Background()
		storage := database.NewMockStorage(t)
		pool := datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			PoolCredentials: &datamodel.PoolCredentials{
				AuthType:         env.USERNAME_PWD_SEC_MGR,
				CertificateIDNew: "staged-cert",
				StagedCaURI:      "project/pool/ca",
			},
		}
		storage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{{Pool: pool}}, nil).
			Once()
		storage.On(
			"CompareAndSwapPoolCredentials",
			ctx,
			pool.UUID,
			env.USERNAME_PWD_SEC_MGR,
			"staged-cert",
			"project/pool/ca",
			mock.Anything,
			mock.MatchedBy(func(credentials *datamodel.PoolCredentials) bool {
				return credentials.CertificateIDNew == "" && credentials.StagedCaURI == ""
			}),
		).Return(nil).Once()
		err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
			rollbackAuthReconciliationStage(ctx, pool.UUID, "staged-cert", "project/pool/ca")
		require.NoError(t, err)
	})
}

func TestExpertReconciliationGuardPaths(t *testing.T) {
	require.Empty(t, expertCertificateUsername(nil))

	t.Run("pool lookup failure", func(t *testing.T) {
		ctx := context.Background()
		storage := database.NewMockStorage(t)
		storage.On("ListPools", ctx, mock.Anything).
			Return(nil, errors.New("lookup failed")).
			Once()
		err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
			reconcileExpertModeCredentialsBeforePromotion(ctx, "pool-1", "project/pool/ca", false)
		require.Error(t, err)
	})

	t.Run("pool without expert credentials", func(t *testing.T) {
		ctx := context.Background()
		storage := database.NewMockStorage(t)
		pool := datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}}
		storage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{{Pool: pool}}, nil).
			Once()
		err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
			reconcileExpertModeCredentialsBeforePromotion(ctx, pool.UUID, "project/pool/ca", false)
		require.NoError(t, err)
	})

	tests := []struct {
		name       string
		credential []*datamodel.ExpertModeCredential
		index      int
		wantError  string
	}{
		{name: "index out of range", credential: []*datamodel.ExpertModeCredential{}, wantError: "invalid expert credential index"},
		{name: "nil credential", credential: []*datamodel.ExpertModeCredential{nil}},
		{name: "non certificate credential", credential: []*datamodel.ExpertModeCredential{{
			AuthType: env.USERNAME_PWD,
			Username: "expert",
		}}},
		{name: "empty certificate username", credential: []*datamodel.ExpertModeCredential{{
			AuthType: env.USER_CERTIFICATE,
		}}, wantError: "empty username"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			storage := database.NewMockStorage(t)
			pool := datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-1"},
				ExpertModeCredentials: &datamodel.ExpertModeCredentials{
					ExpertModeCredential: test.credential,
				},
			}

			err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
				reconcileOneExpertModeCredential(
					ctx,
					&pool,
					test.index,
					"project/pool/ca",
					nil,
					false,
					true,
				)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestExpertCredentialMutationGuards(t *testing.T) {
	newActivity := func(t *testing.T, credentials []*datamodel.ExpertModeCredential) (*RotateVcpToVsaCertificateActivity, string) {
		t.Helper()
		ctx := context.Background()
		storage := database.NewMockStorage(t)
		pool := datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			ExpertModeCredentials: &datamodel.ExpertModeCredentials{
				ExpertModeCredential: credentials,
			},
		}
		storage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{{Pool: pool}}, nil).
			Once()
		return &RotateVcpToVsaCertificateActivity{SE: storage}, pool.UUID
	}

	t.Run("backfill invalid index", func(t *testing.T) {
		activity, poolUUID := newActivity(t, nil)
		err := activity.backfillExpertModeCredentialCAURI(
			context.Background(),
			poolUUID,
			0,
			"certificate",
			"project/pool/ca",
		)
		require.ErrorContains(t, err, "invalid expert credential index")
	})

	t.Run("backfill detects changed credential", func(t *testing.T) {
		activity, poolUUID := newActivity(t, []*datamodel.ExpertModeCredential{{
			CertificateID: "different",
		}})
		err := activity.backfillExpertModeCredentialCAURI(
			context.Background(),
			poolUUID,
			0,
			"certificate",
			"project/pool/ca",
		)
		require.ErrorContains(t, err, "changed before issuer backfill")
	})

	t.Run("staging invalid index", func(t *testing.T) {
		activity, poolUUID := newActivity(t, nil)
		err := activity.updateExpertModeStaging(
			context.Background(),
			poolUUID,
			0,
			"",
			"",
			"certificate",
			"project/pool/ca",
		)
		require.ErrorContains(t, err, "invalid expert credential index")
	})

	t.Run("staging detects changed pair", func(t *testing.T) {
		activity, poolUUID := newActivity(t, []*datamodel.ExpertModeCredential{{
			CertificateIDNew: "different",
		}})
		err := activity.updateExpertModeStaging(
			context.Background(),
			poolUUID,
			0,
			"expected",
			"",
			"certificate",
			"project/pool/ca",
		)
		require.ErrorContains(t, err, "staged pair changed")
	})

	t.Run("promotion invalid index", func(t *testing.T) {
		activity, poolUUID := newActivity(t, nil)
		err := activity.promoteExpertModeCandidate(
			context.Background(),
			poolUUID,
			0,
			"certificate",
			"project/pool/ca",
			"",
			"",
			"expert",
			false,
		)
		require.ErrorContains(t, err, "invalid expert credential index")
	})

	t.Run("promotion detects changed pair", func(t *testing.T) {
		activity, poolUUID := newActivity(t, []*datamodel.ExpertModeCredential{{
			CertificateIDNew: "different",
		}})
		err := activity.promoteExpertModeCandidate(
			context.Background(),
			poolUUID,
			0,
			"certificate",
			"project/pool/ca",
			"",
			"",
			"expert",
			false,
		)
		require.ErrorContains(t, err, "staged pair changed")
	})
}

func TestAuthTypeReconciliationEntryPointGuards(t *testing.T) {
	t.Run("legacy entry surfaces pool lookup failure", func(t *testing.T) {
		ctx := context.Background()
		storage := database.NewMockStorage(t)
		storage.On("ListPools", ctx, mock.Anything).
			Return(nil, errors.New("lookup failed")).
			Once()
		err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
			ReconcileAuthType1Pool(ctx, "pool-1")
		require.Error(t, err)
	})

	t.Run("explicit entry rejects malformed CA", func(t *testing.T) {
		err := (&RotateVcpToVsaCertificateActivity{}).
			ReconcileAuthType1PoolForDesiredCA(context.Background(), "pool-1", "invalid")
		require.Error(t, err)
	})

	t.Run("pool lookup failure", func(t *testing.T) {
		ctx := context.Background()
		storage := database.NewMockStorage(t)
		storage.On("ListPools", ctx, mock.Anything).
			Return(nil, errors.New("lookup failed")).
			Once()
		err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
			ReconcileAuthType1PoolForDesiredCA(ctx, "pool-1", "project/pool/ca")
		require.Error(t, err)
	})

	t.Run("dedicated pool is skipped", func(t *testing.T) {
		ctx := context.Background()
		storage := database.NewMockStorage(t)
		pool := datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     "READY",
			PoolCredentials: &datamodel.PoolCredentials{
				AuthType: env.USERNAME_PWD_SEC_MGR,
			},
		}
		storage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{{Pool: pool}}, nil).
			Once()
		err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
			ReconcileAuthType1PoolForDesiredCA(ctx, pool.UUID, "project/pool/ca")
		require.NoError(t, err)
	})

	t.Run("private reconciliation rejects empty target CA", func(t *testing.T) {
		ctx := context.Background()
		storage := database.NewMockStorage(t)
		pool := datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "pool-1"},
			State:        "READY",
			ServiceLevel: "FLEX",
			PoolCredentials: &datamodel.PoolCredentials{
				AuthType:      env.USERNAME_PWD_SEC_MGR,
				CertificateID: "old-cert",
			},
		}
		storage.On("ListPools", ctx, mock.Anything).
			Return([]*datamodel.PoolView{{Pool: pool}}, nil).
			Once()
		err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
			reconcileAuthType1Pool(ctx, pool.UUID, "")
		require.Error(t, err)
	})
}

func TestPromoteAuthReconciliationCertificateGuardsStateAndAuth(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		authType int
		wantErr  string
	}{
		{name: "auth changed", state: "READY", authType: env.USER_CERTIFICATE, wantErr: "auth type changed"},
		{name: "state changed", state: "DELETING", authType: env.USERNAME_PWD_SEC_MGR, wantErr: "state changed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			storage := database.NewMockStorage(t)
			pool := datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-1"},
				State:     test.state,
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType:         test.authType,
					CertificateIDNew: "new-cert",
					StagedCaURI:      "project/pool/ca",
				},
			}
			storage.On("ListPools", ctx, mock.Anything).Return([]*datamodel.PoolView{{Pool: pool}}, nil).Once()

			_, err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
				promoteAuthReconciliationCertificate(ctx, pool.UUID, "new-cert", "project/pool/ca", "user")

			require.ErrorContains(t, err, test.wantErr)
			storage.AssertNotCalled(t, "UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything)
			storage.AssertExpectations(t)
		})
	}
}

func TestPromoteAuthReconciliationCertificateRejectsMissingOrChangedStage(t *testing.T) {
	tests := []struct {
		name string
		pool datamodel.Pool
		want string
	}{
		{
			name: "missing credentials",
			pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
			want: "has no credentials before promotion",
		},
		{
			name: "staged pair changed",
			pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-1"},
				State:     "READY",
				PoolCredentials: &datamodel.PoolCredentials{
					AuthType:         env.USERNAME_PWD_SEC_MGR,
					CertificateIDNew: "different-cert",
					StagedCaURI:      "project/pool/ca",
				},
			},
			want: "staged certificate/CA pair changed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			storage := database.NewMockStorage(t)
			storage.On("ListPools", ctx, mock.Anything).
				Return([]*datamodel.PoolView{{Pool: test.pool}}, nil).
				Once()
			_, err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
				promoteAuthReconciliationCertificate(
					ctx,
					test.pool.UUID,
					"new-cert",
					"project/pool/ca",
					"deployment_admin",
				)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestRollbackAuthReconciliationStageRejectsMissingCredentials(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	pool := datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}}
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: pool}}, nil).
		Once()

	err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
		rollbackAuthReconciliationStage(ctx, pool.UUID, "cert", "project/pool/ca")

	require.ErrorContains(t, err, "has no credentials before rollback")
}

func TestPromoteAuthReconciliationCertificateRestoresFallbackUsername(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	pool := datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		State:     "READY",
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType:           env.USERNAME_PWD_SEC_MGR,
			Username:           "admin",
			UsernamePreCertFix: "deployment_admin",
			SecretID:           "admin-secret",
			CertificateID:      "expired-cert",
			CaURI:              "project/pool/expired-ca",
			CertificateIDNew:   "new-cert",
			StagedCaURI:        "project/pool/new-ca",
		},
	}
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: pool}}, nil).
		Once()
	storage.On(
		"CompareAndSwapPoolCredentials",
		ctx,
		pool.UUID,
		env.USERNAME_PWD_SEC_MGR,
		"new-cert",
		"project/pool/new-ca",
		mock.Anything,
		mock.MatchedBy(func(credentials *datamodel.PoolCredentials) bool {
			return credentials.AuthType == env.USER_CERTIFICATE &&
				credentials.Username == "deployment_admin" &&
				credentials.UsernamePreCertFix == "" &&
				credentials.SecretID == "admin-secret" &&
				credentials.CertificateID == "new-cert" &&
				credentials.CaURI == "project/pool/new-ca" &&
				credentials.CertificateIDNew == "expired-cert" &&
				credentials.StagedCaURI == "project/pool/expired-ca"
		}),
	).Return(nil).Once()

	oldCertificateID, err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
		promoteAuthReconciliationCertificate(
			ctx,
			pool.UUID,
			"new-cert",
			"project/pool/new-ca",
			"deployment_admin",
		)

	require.NoError(t, err)
	assert.Equal(t, "expired-cert", oldCertificateID)
	storage.AssertExpectations(t)
}

func Test_PromoteAuthReconciliationCertificate_UnpairedOldCertificate_ClearsDeferredCleanupSlot(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	pool := datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		State:     "READY",
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType:         env.USERNAME_PWD_SEC_MGR,
			Username:         "deployment_admin",
			SecretID:         "admin-secret",
			CertificateID:    "legacy-old-cert",
			CertificateIDNew: "new-cert",
			StagedCaURI:      "project/pool/new-ca",
		},
	}
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: pool}}, nil).
		Once()
	storage.On(
		"CompareAndSwapPoolCredentials",
		ctx,
		pool.UUID,
		env.USERNAME_PWD_SEC_MGR,
		"new-cert",
		"project/pool/new-ca",
		mock.Anything,
		mock.MatchedBy(func(credentials *datamodel.PoolCredentials) bool {
			return credentials.AuthType == env.USER_CERTIFICATE &&
				credentials.CertificateID == "new-cert" &&
				credentials.CaURI == "project/pool/new-ca" &&
				credentials.CertificateIDNew == "" &&
				credentials.StagedCaURI == ""
		}),
	).Return(nil).Once()

	oldCertificateID, err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
		promoteAuthReconciliationCertificate(
			ctx,
			pool.UUID,
			"new-cert",
			"project/pool/new-ca",
			"deployment_admin",
		)

	require.NoError(t, err)
	assert.Empty(t, oldCertificateID)
	storage.AssertExpectations(t)
}

func TestPromoteExpertModeCandidateRestoresFallbackCredential(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	expert := &datamodel.ExpertModeCredentials{
		ExpertModeCredential: []*datamodel.ExpertModeCredential{{
			AuthType:           env.USERNAME_PWD_SEC_MGR,
			Username:           "gcnv_gadmin",
			UsernamePreCertFix: "deployment_gadmin",
			SecretID:           "admin-secret",
			CertificateID:      "expired-expert-cert",
			CaURI:              "project/pool/expired-ca",
			CertificateIDNew:   "new-expert-cert",
			StagedCaURI:        "project/pool/new-ca",
		}},
	}
	pool := datamodel.Pool{
		BaseModel:             datamodel.BaseModel{UUID: "pool-1"},
		State:                 "READY",
		ExpertModeCredentials: expert,
	}
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: pool}}, nil).
		Once()
	storage.On(
		"CompareAndSwapExpertModeCredentials",
		ctx,
		pool.UUID,
		mock.Anything,
		mock.MatchedBy(func(credentials *datamodel.ExpertModeCredentials) bool {
			restored := credentials.ExpertModeCredential[0]
			return restored.AuthType == env.USER_CERTIFICATE &&
				restored.Username == "deployment_gadmin" &&
				restored.UsernamePreCertFix == "" &&
				restored.SecretID == "admin-secret" &&
				restored.CertificateID == "new-expert-cert" &&
				restored.CaURI == "project/pool/new-ca" &&
				restored.CertificateIDNew == "expired-expert-cert" &&
				restored.StagedCaURI == "project/pool/expired-ca"
		}),
		mock.Anything,
	).Return(nil).Once()

	err := (&RotateVcpToVsaCertificateActivity{SE: storage}).promoteExpertModeCandidate(
		ctx,
		pool.UUID,
		0,
		"new-expert-cert",
		"project/pool/new-ca",
		"expired-expert-cert",
		"project/pool/expired-ca",
		"deployment_gadmin",
		true,
	)

	require.NoError(t, err)
	storage.AssertExpectations(t)
}

func Test_PromoteExpertModeCandidate_UnpairedOldCertificate_ClearsDeferredCleanupSlot(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	expert := &datamodel.ExpertModeCredentials{
		ExpertModeCredential: []*datamodel.ExpertModeCredential{{
			AuthType:         env.USERNAME_PWD_SEC_MGR,
			Username:         "deployment_gadmin",
			CertificateID:    "legacy-old-cert",
			CertificateIDNew: "new-expert-cert",
			StagedCaURI:      "project/pool/new-ca",
		}},
	}
	pool := datamodel.Pool{
		BaseModel:             datamodel.BaseModel{UUID: "pool-1"},
		State:                 "READY",
		ExpertModeCredentials: expert,
	}
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: pool}}, nil).
		Once()
	storage.On(
		"CompareAndSwapExpertModeCredentials",
		ctx,
		pool.UUID,
		mock.Anything,
		mock.MatchedBy(func(credentials *datamodel.ExpertModeCredentials) bool {
			promoted := credentials.ExpertModeCredential[0]
			return promoted.AuthType == env.USER_CERTIFICATE &&
				promoted.CertificateID == "new-expert-cert" &&
				promoted.CaURI == "project/pool/new-ca" &&
				promoted.CertificateIDNew == "" &&
				promoted.StagedCaURI == ""
		}),
		mock.Anything,
	).Return(nil).Once()

	err := (&RotateVcpToVsaCertificateActivity{SE: storage}).promoteExpertModeCandidate(
		ctx,
		pool.UUID,
		0,
		"new-expert-cert",
		"project/pool/new-ca",
		"legacy-old-cert",
		"",
		"deployment_gadmin",
		true,
	)

	require.NoError(t, err)
	storage.AssertExpectations(t)
}

func TestRefreshPromotedCertificateCache(t *testing.T) {
	ctx := context.Background()
	originalGetService := getGcpServiceForCerts
	originalGetCertificate := vsa.GetCertificateAndPrivateKeyByID
	t.Cleanup(func() {
		getGcpServiceForCerts = originalGetService
		vsa.GetCertificateAndPrivateKeyByID = originalGetCertificate
		common.RemoveFromCertAuthCache("old-cert")
		common.RemoveFromCertAuthCache("new-cert")
	})

	service := &hyperscaler2.MockServices{}
	getGcpServiceForCerts = func(context.Context) (hyperscaler2.Services, error) {
		return service, nil
	}
	vsa.GetCertificateAndPrivateKeyByID = func(
		hyperscaler2.Services, string, string, string, string, string,
	) (*hyperscalermodels.CustomCertificateResponse, error) {
		return &hyperscalermodels.CustomCertificateResponse{
			Certificate: &hyperscalermodels.CustomCertificate{
				PemCertificate:      "leaf",
				PemCertificateChain: []string{"issuer"},
				SubjectCommonName:   "user",
			},
			Secret: &hyperscalermodels.CustomSecret{
				SecretVersion: &hyperscalermodels.CustomSecretVersion{Value: "private-key"},
			},
		}, nil
	}
	common.AddToCertAuthCache("old-cert", &models.Certificate{SignedCertificate: "old"})

	err := (&RotateVcpToVsaCertificateActivity{}).
		refreshPromotedCertificateCache(ctx, "new-cert", "project/pool/ca", "old-cert")

	require.NoError(t, err)
	_, oldExists := common.GetCertAuthCache("old-cert")
	newEntry, newExists := common.GetCertAuthCache("new-cert")
	assert.False(t, oldExists)
	require.True(t, newExists)
	assert.Equal(t, "leaf", newEntry.Certificate.SignedCertificate)
}

func TestRefreshPromotedCertificateCacheSurfacesFailure(t *testing.T) {
	ctx := context.Background()
	originalGetService := getGcpServiceForCerts
	originalGetCertificate := vsa.GetCertificateAndPrivateKeyByID
	t.Cleanup(func() {
		getGcpServiceForCerts = originalGetService
		vsa.GetCertificateAndPrivateKeyByID = originalGetCertificate
	})
	getGcpServiceForCerts = func(context.Context) (hyperscaler2.Services, error) {
		return &hyperscaler2.MockServices{}, nil
	}
	vsa.GetCertificateAndPrivateKeyByID = func(
		hyperscaler2.Services, string, string, string, string, string,
	) (*hyperscalermodels.CustomCertificateResponse, error) {
		return nil, errors.New("cache fetch failed")
	}

	err := (&RotateVcpToVsaCertificateActivity{}).
		refreshPromotedCertificateCache(ctx, "new-cert", "project/pool/ca", "old-cert")

	require.ErrorContains(t, err, "Failed to update certificate cache")
}

func TestEnsureAuthReconciliationDNS_UsesGetGcpServiceForCertsHook(t *testing.T) {
	original := getGcpServiceForCerts
	t.Cleanup(func() { getGcpServiceForCerts = original })
	getGcpServiceForCerts = func(context.Context) (hyperscaler2.Services, error) {
		return nil, errors.New("hook used")
	}

	err := (&RotateVcpToVsaCertificateActivity{}).ensureAuthReconciliationDNS(
		context.Background(),
		&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}, DeploymentName: "deployment"},
		[]*datamodel.Node{{Name: "n1", EndpointAddress: "10.0.0.1"}},
	)

	require.ErrorContains(t, err, "hook used")
}
