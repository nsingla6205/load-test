package coreapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	coreapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	ontaputils "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// noopTestLogger implements log.Logger with no side effects. These tests assert
// behavior via the Core API invoker mock; logging is not under test here.
type noopTestLogger struct{}

func (noopTestLogger) Errorf(string, ...any) {}
func (noopTestLogger) Error(string, ...any)  {}
func (noopTestLogger) Warnf(string, ...any)  {}
func (noopTestLogger) Warn(string, ...any)   {}
func (noopTestLogger) Infof(string, ...any)  {}
func (noopTestLogger) Info(string, ...any)   {}
func (noopTestLogger) Debugf(string, ...any) {}
func (noopTestLogger) Debug(string, ...any)  {}

func (noopTestLogger) InfoContext(context.Context, string, ...any)  {}
func (noopTestLogger) WarnContext(context.Context, string, ...any)  {}
func (noopTestLogger) ErrorContext(context.Context, string, ...any) {}
func (noopTestLogger) DebugContext(context.Context, string, ...any) {}

func (n noopTestLogger) WithFields(string, log.Fields) log.Logger { return n }
func (n noopTestLogger) With(log.Fields) log.Logger               { return n }

func TestFetchCredentials(t *testing.T) {
	originalCreateClient := createCoreAPIClient
	originalHost := coreAPIHost
	defer func() {
		createCoreAPIClient = originalCreateClient
		coreAPIHost = originalHost
	}()

	var logger log.Logger = noopTestLogger{}
	mockInvoker := coreapi.NewMockInvoker(t)

	coreAPIHost = "test-host"
	createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
		assert.Equal(t, "test-host", host)
		assert.Equal(t, "test-jwt", jwtToken)
		return &coreapi.CoreAPIClient{Invoker: mockInvoker}
	}

	poolDetails := &models.PoolDetails{
		PoolID:      "pool-1",
		AccountName: "acct-1",
		UserName:    "user-1",
	}

	mockInvoker.On("V1GetOntapCredentials", mock.Anything, mock.MatchedBy(func(p coreapi.V1GetOntapCredentialsParams) bool {
		return p.PoolId == poolDetails.PoolID &&
			p.AccountName.IsSet() && p.AccountName.Value == poolDetails.AccountName &&
			p.UserName.IsSet() && p.UserName.Value == poolDetails.UserName
	})).Return(&coreapi.OntapCredentialsV1{
		AuthType: coreapi.NewOptInt(2),
		CaURI:    coreapi.NewOptString("proj/pool/ca"),
	}, nil)

	got, err := FetchCredentials(context.Background(), poolDetails, "test-jwt", logger)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.True(t, got.AuthType.IsSet())
	assert.Equal(t, 2, got.AuthType.Value)
	mockInvoker.AssertExpectations(t)
}

func TestFetchCredentials_ForwardsClusterName(t *testing.T) {
	originalCreateClient := createCoreAPIClient
	originalHost := coreAPIHost
	defer func() {
		createCoreAPIClient = originalCreateClient
		coreAPIHost = originalHost
	}()

	var logger log.Logger = noopTestLogger{}
	mockInvoker := coreapi.NewMockInvoker(t)

	coreAPIHost = "test-host"
	createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
		return &coreapi.CoreAPIClient{Invoker: mockInvoker}
	}

	poolDetails := &models.PoolDetails{
		PoolID:      "pool-1",
		AccountName: "acct-1",
		UserName:    "user-1",
		ClusterName: "cluster-a",
	}

	// The x-netapp-cluster-name header must be forwarded for dedicated-capacity pools.
	mockInvoker.On("V1GetOntapCredentials", mock.Anything, mock.MatchedBy(func(p coreapi.V1GetOntapCredentialsParams) bool {
		return p.XNetappClusterName.IsSet() && p.XNetappClusterName.Value == "cluster-a"
	})).Return(&coreapi.OntapCredentialsV1{
		AuthType: coreapi.NewOptInt(2),
	}, nil)

	got, err := FetchCredentials(context.Background(), poolDetails, "test-jwt", logger)
	require.NoError(t, err)
	require.NotNil(t, got)
	mockInvoker.AssertExpectations(t)
}

func TestFetchCredentials_ErrorMappings(t *testing.T) {
	originalCreateClient := createCoreAPIClient
	originalHost := coreAPIHost
	defer func() {
		createCoreAPIClient = originalCreateClient
		coreAPIHost = originalHost
	}()

	var logger log.Logger = noopTestLogger{}
	poolDetails := &models.PoolDetails{
		PoolID:      "pool-1",
		AccountName: "acct-1",
		UserName:    "user-1",
	}

	t.Run("WhenCoreInvokerReturnsTransportError_ShouldReturnServiceUnavailable", func(t *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(t)
		coreAPIHost = "test-host"
		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{Invoker: mockInvoker}
		}
		mockInvoker.On("V1GetOntapCredentials", mock.Anything, mock.Anything).
			Return(nil, errors.New("transport down")).Once()

		_, err := FetchCredentials(context.Background(), poolDetails, "jwt", logger)
		require.Error(t, err)
		var he interface{ GetStatus() int }
		_ = he
		assert.Contains(t, err.Error(), "Service unavailable")
	})

	t.Run("WhenCoreReturnsTypedResponses_ShouldMapToExpectedStatusAndMessage", func(t *testing.T) {
		cases := []struct {
			name        string
			response    any
			wantStatus  int
			wantMessage string
		}{
			{
				name:        "WhenResponseIsNotFound_ShouldReturnNotFound",
				response:    &coreapi.V1GetOntapCredentialsNotFound{Code: 404, Message: "missing"},
				wantStatus:  http.StatusNotFound,
				wantMessage: "Pool not found",
			},
			{
				name:        "WhenResponseIsBadRequestWithMessage_ShouldPreserveMessage",
				response:    &coreapi.V1GetOntapCredentialsBadRequest{Code: 400, Message: "Pool is in deleting state"},
				wantStatus:  http.StatusBadRequest,
				wantMessage: "Pool is in deleting state",
			},
			{
				name:        "WhenResponseIsBadRequestWithoutMessage_ShouldUseFallbackMessage",
				response:    &coreapi.V1GetOntapCredentialsBadRequest{Code: 400, Message: "   "},
				wantStatus:  http.StatusBadRequest,
				wantMessage: "Invalid pool details",
			},
			{
				name:        "WhenResponseIsUnauthorized_ShouldReturnUnauthorized",
				response:    &coreapi.V1GetOntapCredentialsUnauthorized{Code: 401, Message: "no auth"},
				wantStatus:  http.StatusUnauthorized,
				wantMessage: "Unauthorized access",
			},
			{
				name:        "WhenResponseIsForbidden_ShouldReturnForbidden",
				response:    &coreapi.V1GetOntapCredentialsForbidden{Code: 403, Message: "forbidden"},
				wantStatus:  http.StatusForbidden,
				wantMessage: "Forbidden access",
			},
			{
				name:        "WhenResponseIsInternalServerError_ShouldReturnInternalServerError",
				response:    &coreapi.V1GetOntapCredentialsInternalServerError{Code: 500, Message: "oops"},
				wantStatus:  http.StatusInternalServerError,
				wantMessage: "Internal server error",
			},
			{
				name:        "WhenResponseTypeIsUnexpected_ShouldReturnInternalServerError",
				response:    &coreapi.V1GetOntapCredentialsConflict{Code: 409, Message: "conflict"},
				wantStatus:  http.StatusInternalServerError,
				wantMessage: "Internal server error",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				mockInvoker := coreapi.NewMockInvoker(t)
				createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
					return &coreapi.CoreAPIClient{Invoker: mockInvoker}
				}
				mockInvoker.On("V1GetOntapCredentials", mock.Anything, mock.Anything).
					Return(tc.response, nil).Once()

				_, err := FetchCredentials(context.Background(), poolDetails, "jwt", logger)
				require.Error(t, err)
				httpErr, ok := err.(*ontaputils.HTTPError)
				require.True(t, ok)
				assert.Equal(t, tc.wantStatus, httpErr.Status)
				assert.Equal(t, tc.wantMessage, httpErr.Message)
			})
		}
	})
}

func TestSubmitExpertModeVolumeOperation(t *testing.T) {
	originalCreateClient := createCoreAPIClient
	defer func() { createCoreAPIClient = originalCreateClient }()

	var logger log.Logger = noopTestLogger{}

	t.Run("SuccessCases", func(tt *testing.T) {
		tt.Run("VolumeCreatedSuccessfully", func(ttt *testing.T) {
			mockInvoker := coreapi.NewMockInvoker(ttt)

			createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
				return &coreapi.CoreAPIClient{
					Invoker: mockInvoker,
				}
			}

			request := &coreapi.ExpertModeVolumeV1{
				ProjectNumber: "12345",
				PoolUUID:      "pool-uuid-123",
				VolumeName:    "test-volume",
				Action:        coreapi.ExpertModeVolumeV1ActionCreate,
				Style:         coreapi.ExpertModeVolumeV1StyleFlexvol,
				SizeInBytes:   coreapi.NewOptFloat64(1073741824),
			}

			mockInvoker.On("V1ExpertModeVolume", mock.Anything, request, mock.MatchedBy(func(params coreapi.V1ExpertModeVolumeParams) bool {
				return params.XCorrelationID.IsSet() && params.XCorrelationID.Value == "corr-id-123"
			})).Return(&coreapi.V1ExpertModeVolumeOK{}, nil)

			ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-id-123")
			err := SubmitExpertModeVolumeOperation(ctx, request, "test-jwt-token", logger)

			assert.NoError(ttt, err)
			mockInvoker.AssertExpectations(ttt)
		})

		tt.Run("VolumeUpdatedSuccessfully", func(ttt *testing.T) {
			mockInvoker := coreapi.NewMockInvoker(ttt)

			createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
				return &coreapi.CoreAPIClient{
					Invoker: mockInvoker,
				}
			}

			request := &coreapi.ExpertModeVolumeV1{
				ProjectNumber: "12345",
				PoolUUID:      "pool-uuid-123",
				VolumeUUID:    coreapi.NewOptString("volume-uuid-456"),
				VolumeName:    "test-volume",
				Action:        coreapi.ExpertModeVolumeV1ActionUpdate,
				Style:         coreapi.ExpertModeVolumeV1StyleFlexvol,
				SizeInBytes:   coreapi.NewOptFloat64(2147483648),
			}

			mockInvoker.On("V1ExpertModeVolume", mock.Anything, request, mock.MatchedBy(func(params coreapi.V1ExpertModeVolumeParams) bool {
				return params.XCorrelationID.IsSet() && params.XCorrelationID.Value == "corr-id-456"
			})).Return(&coreapi.V1ExpertModeVolumeOK{}, nil)

			ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-id-456")
			err := SubmitExpertModeVolumeOperation(ctx, request, "test-jwt-token", logger)

			assert.NoError(ttt, err)
			mockInvoker.AssertExpectations(ttt)
		})

		tt.Run("VolumeDeletedSuccessfully", func(ttt *testing.T) {
			mockInvoker := coreapi.NewMockInvoker(ttt)

			createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
				return &coreapi.CoreAPIClient{
					Invoker: mockInvoker,
				}
			}

			request := &coreapi.ExpertModeVolumeV1{
				ProjectNumber: "12345",
				PoolUUID:      "pool-uuid-123",
				VolumeUUID:    coreapi.NewOptString("volume-uuid-789"),
				Action:        coreapi.ExpertModeVolumeV1ActionDelete,
			}

			mockInvoker.On("V1ExpertModeVolume", mock.Anything, request, mock.Anything).Return(&coreapi.V1ExpertModeVolumeOK{}, nil)

			ctx := context.Background()
			err := SubmitExpertModeVolumeOperation(ctx, request, "test-jwt-token", logger)

			assert.NoError(ttt, err)
			mockInvoker.AssertExpectations(ttt)
		})

		tt.Run("NoCorrelationIDInContext", func(ttt *testing.T) {
			mockInvoker := coreapi.NewMockInvoker(ttt)

			createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
				return &coreapi.CoreAPIClient{
					Invoker: mockInvoker,
				}
			}

			request := &coreapi.ExpertModeVolumeV1{
				ProjectNumber: "12345",
				PoolUUID:      "pool-uuid-123",
				VolumeName:    "test-volume",
				Action:        coreapi.ExpertModeVolumeV1ActionCreate,
				Style:         coreapi.ExpertModeVolumeV1StyleFlexvol,
				SizeInBytes:   coreapi.NewOptFloat64(1073741824),
			}

			mockInvoker.On("V1ExpertModeVolume", mock.Anything, request, mock.MatchedBy(func(params coreapi.V1ExpertModeVolumeParams) bool {
				return !params.XCorrelationID.IsSet() || params.XCorrelationID.Value == ""
			})).Return(&coreapi.V1ExpertModeVolumeOK{}, nil)

			ctx := context.Background()
			err := SubmitExpertModeVolumeOperation(ctx, request, "test-jwt-token", logger)

			assert.NoError(ttt, err)
			mockInvoker.AssertExpectations(ttt)
		})
	})

	t.Run("ErrorCases", func(tt *testing.T) {
		tt.Run("BadRequest", func(ttt *testing.T) {
			mockInvoker := coreapi.NewMockInvoker(ttt)

			createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
				return &coreapi.CoreAPIClient{
					Invoker: mockInvoker,
				}
			}

			request := &coreapi.ExpertModeVolumeV1{
				ProjectNumber: "12345",
				PoolUUID:      "pool-uuid-123",
				VolumeName:    "",
				Action:        coreapi.ExpertModeVolumeV1ActionCreate,
			}

			mockInvoker.On("V1ExpertModeVolume", mock.Anything, request, mock.Anything).Return(&coreapi.V1ExpertModeVolumeBadRequest{
				Code:    400,
				Message: "Volume name is required",
			}, nil)

			ctx := context.Background()
			err := SubmitExpertModeVolumeOperation(ctx, request, "test-jwt-token", logger)

			assert.Error(ttt, err)
			assert.Contains(ttt, err.Error(), "bad request: Volume name is required")
			mockInvoker.AssertExpectations(ttt)
		})

		tt.Run("BadRequest_InsufficientCapacity_SanitizesRawBytes", func(ttt *testing.T) {
			mockInvoker := coreapi.NewMockInvoker(ttt)
			mockLogger := &log.MockLogger{}

			mockLogger.On("DebugContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
			mockLogger.On("InfoContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
			mockLogger.On("ErrorContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

			createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
				return &coreapi.CoreAPIClient{
					Invoker: mockInvoker,
				}
			}

			request := &coreapi.ExpertModeVolumeV1{
				ProjectNumber: "12345",
				PoolUUID:      "pool-uuid-123",
				VolumeName:    "test-volume",
				Action:        coreapi.ExpertModeVolumeV1ActionCreate,
				SizeInBytes:   coreapi.OptFloat64{Value: 1073741824, Set: true},
			}

			mockInvoker.On("V1ExpertModeVolume", mock.Anything, request, mock.Anything).Return(&coreapi.V1ExpertModeVolumeBadRequest{
				Code:    400,
				Message: "insufficient pool capacity for the requested volume size",
			}, nil)

			ctx := context.Background()
			err := SubmitExpertModeVolumeOperation(ctx, request, "test-jwt-token", mockLogger)

			assert.Error(ttt, err)
			assert.Equal(ttt, "bad request: insufficient pool capacity for the requested volume size", err.Error())
			assert.NotContains(ttt, err.Error(), "bytes")
			mockInvoker.AssertExpectations(ttt)
		})

		tt.Run("BadRequest_InsufficientCapacity_NegativeAvailable", func(ttt *testing.T) {
			mockInvoker := coreapi.NewMockInvoker(ttt)
			mockLogger := &log.MockLogger{}

			mockLogger.On("DebugContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
			mockLogger.On("InfoContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
			mockLogger.On("ErrorContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

			createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
				return &coreapi.CoreAPIClient{
					Invoker: mockInvoker,
				}
			}

			request := &coreapi.ExpertModeVolumeV1{
				ProjectNumber: "12345",
				PoolUUID:      "pool-uuid-123",
				VolumeName:    "test-volume",
				Action:        coreapi.ExpertModeVolumeV1ActionUpdate,
				SizeInBytes:   coreapi.OptFloat64{Value: 2147483648, Set: true},
			}

			mockInvoker.On("V1ExpertModeVolume", mock.Anything, request, mock.Anything).Return(&coreapi.V1ExpertModeVolumeBadRequest{
				Code:    400,
				Message: "insufficient pool capacity for the requested volume size",
			}, nil)

			ctx := context.Background()
			err := SubmitExpertModeVolumeOperation(ctx, request, "test-jwt-token", mockLogger)

			assert.Error(ttt, err)
			assert.Equal(ttt, "bad request: insufficient pool capacity for the requested volume size", err.Error())
			mockInvoker.AssertExpectations(ttt)
		})

		tt.Run("Conflict", func(ttt *testing.T) {
			mockInvoker := coreapi.NewMockInvoker(ttt)

			createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
				return &coreapi.CoreAPIClient{
					Invoker: mockInvoker,
				}
			}

			request := &coreapi.ExpertModeVolumeV1{
				ProjectNumber: "12345",
				PoolUUID:      "pool-uuid-123",
				VolumeName:    "existing-volume",
				Action:        coreapi.ExpertModeVolumeV1ActionCreate,
			}

			mockInvoker.On("V1ExpertModeVolume", mock.Anything, request, mock.Anything).Return(&coreapi.V1ExpertModeVolumeConflict{
				Code:    409,
				Message: "Volume already exists",
			}, nil)

			ctx := context.Background()
			err := SubmitExpertModeVolumeOperation(ctx, request, "test-jwt-token", logger)

			assert.Error(ttt, err)
			assert.Contains(ttt, err.Error(), "conflict: Volume already exists")
			mockInvoker.AssertExpectations(ttt)
		})
	})
}

func TestSubmitExpertModeVolumeRename(t *testing.T) {
	originalCreateClient := createCoreAPIClient
	defer func() { createCoreAPIClient = originalCreateClient }()

	var logger log.Logger = noopTestLogger{}

	t.Run("VolumeRenamedSuccessfully", func(tt *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(tt)

		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{
				Invoker: mockInvoker,
			}
		}

		request := &coreapi.ExpertModeVolumeRenameV1{
			Name:          "reconcile_update004",
			ProjectNumber: "12345",
			PoolUUID:      "pool-uuid-123",
			SvmName:       "vs1",
		}
		params := coreapi.V1ExpertModeVolumeRenameParams{
			Name: "reconcile004",
		}

		mockInvoker.On("V1ExpertModeVolumeRename", mock.Anything, request, mock.MatchedBy(func(p coreapi.V1ExpertModeVolumeRenameParams) bool {
			return p.Name == "reconcile004" && p.XCorrelationID.IsSet() && p.XCorrelationID.Value == "corr-id-rename"
		})).Return(&coreapi.V1ExpertModeVolumeRenameOK{}, nil)

		ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-id-rename")
		err := SubmitExpertModeVolumeRename(ctx, request, params, "test-jwt-token", logger)

		assert.NoError(tt, err)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("RenameBadRequest", func(tt *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(tt)

		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{
				Invoker: mockInvoker,
			}
		}

		request := &coreapi.ExpertModeVolumeRenameV1{
			Name:          "reconcile_update004",
			ProjectNumber: "12345",
			PoolUUID:      "pool-uuid-123",
			SvmName:       "vs1",
		}
		params := coreapi.V1ExpertModeVolumeRenameParams{Name: "reconcile004"}

		mockInvoker.On("V1ExpertModeVolumeRename", mock.Anything, request, mock.Anything).Return(&coreapi.V1ExpertModeVolumeRenameBadRequest{
			Code:    400,
			Message: "Invalid new volume name",
		}, nil)

		ctx := context.Background()
		err := SubmitExpertModeVolumeRename(ctx, request, params, "test-jwt-token", logger)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "bad request: Invalid new volume name")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("RenameBadRequest_NoCapacityLeak", func(tt *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(tt)
		mockLogger := &log.MockLogger{}

		mockLogger.On("DebugContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
		mockLogger.On("InfoContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
		mockLogger.On("ErrorContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{
				Invoker: mockInvoker,
			}
		}

		request := &coreapi.ExpertModeVolumeRenameV1{
			Name:          "new-name",
			ProjectNumber: "12345",
			PoolUUID:      "pool-uuid-123",
			SvmName:       "vs1",
		}
		params := coreapi.V1ExpertModeVolumeRenameParams{Name: "old-name"}

		mockInvoker.On("V1ExpertModeVolumeRename", mock.Anything, request, mock.Anything).Return(&coreapi.V1ExpertModeVolumeRenameBadRequest{
			Code:    400,
			Message: "Invalid new volume name",
		}, nil)

		ctx := context.Background()
		err := SubmitExpertModeVolumeRename(ctx, request, params, "test-jwt-token", mockLogger)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "bad request: Invalid new volume name")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("RenameNotFound", func(tt *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(tt)

		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{
				Invoker: mockInvoker,
			}
		}

		request := &coreapi.ExpertModeVolumeRenameV1{
			Name:          "reconcile_update004",
			ProjectNumber: "12345",
			PoolUUID:      "pool-uuid-123",
			SvmName:       "vs1",
		}
		params := coreapi.V1ExpertModeVolumeRenameParams{Name: "reconcile004"}

		mockInvoker.On("V1ExpertModeVolumeRename", mock.Anything, request, mock.Anything).Return(&coreapi.V1ExpertModeVolumeRenameNotFound{
			Code:    404,
			Message: "Volume not found",
		}, nil)

		ctx := context.Background()
		err := SubmitExpertModeVolumeRename(ctx, request, params, "test-jwt-token", logger)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume not found: Volume not found")
		mockInvoker.AssertExpectations(tt)
	})
}

func TestSubmitExpertModeFlexCloneSplit(t *testing.T) {
	originalCreateClient := createCoreAPIClient
	defer func() { createCoreAPIClient = originalCreateClient }()

	t.Run("Accepted202", func(tt *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(tt)
		mockLogger := &log.MockLogger{}

		mockLogger.On("InfoContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
		mockLogger.On("ErrorContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{
				Invoker: mockInvoker,
			}
		}

		mockInvoker.On("V1ExpertModeVolumeFlexCloneSplit", mock.Anything,
			mock.MatchedBy(func(req *coreapi.ExpertModeVolumeFlexCloneSplitV1) bool {
				return req != nil &&
					req.VolumeUUID.IsSet() && req.VolumeUUID.Value == "ext-vol-uuid" &&
					!req.VolumeName.IsSet() &&
					req.ProjectNumber == "12345" &&
					req.PoolUUID == "pool-uuid-123"
			}),
			mock.MatchedBy(func(p coreapi.V1ExpertModeVolumeFlexCloneSplitParams) bool {
				return p.XCorrelationID.IsSet() && p.XCorrelationID.Value == "corr-split"
			}),
		).Return(&coreapi.V1ExpertModeVolumeFlexCloneSplitAccepted{}, nil)

		ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-split")
		err := SubmitExpertModeFlexCloneSplit(ctx, "ext-vol-uuid", "", "12345", "pool-uuid-123", "jwt", mockLogger)

		assert.NoError(tt, err)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("BadRequest", func(tt *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(tt)
		mockLogger := &log.MockLogger{}

		mockLogger.On("InfoContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
		mockLogger.On("ErrorContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{
				Invoker: mockInvoker,
			}
		}

		mockInvoker.On("V1ExpertModeVolumeFlexCloneSplit", mock.Anything, mock.Anything, mock.Anything).Return(&coreapi.V1ExpertModeVolumeFlexCloneSplitBadRequest{
			Code:    400,
			Message: "not a flexclone",
		}, nil)

		ctx := context.Background()
		err := SubmitExpertModeFlexCloneSplit(ctx, "ext-vol-uuid", "", "12345", "pool-uuid-123", "jwt", mockLogger)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "bad request: not a flexclone")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("Accepted202_WithVolumeName", func(tt *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(tt)
		mockLogger := &log.MockLogger{}

		mockLogger.On("InfoContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
		mockLogger.On("ErrorContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{
				Invoker: mockInvoker,
			}
		}

		mockInvoker.On("V1ExpertModeVolumeFlexCloneSplit", mock.Anything,
			mock.MatchedBy(func(req *coreapi.ExpertModeVolumeFlexCloneSplitV1) bool {
				return req != nil &&
					!req.VolumeUUID.IsSet() &&
					req.VolumeName.IsSet() && req.VolumeName.Value == "vol-by-name" &&
					req.ProjectNumber == "12345" &&
					req.PoolUUID == "pool-uuid-123"
			}),
			mock.Anything,
		).Return(&coreapi.V1ExpertModeVolumeFlexCloneSplitAccepted{}, nil)

		err := SubmitExpertModeFlexCloneSplit(context.Background(), "", "vol-by-name", "12345", "pool-uuid-123", "jwt", mockLogger)

		assert.NoError(tt, err)
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("UnexpectedResponse_ReturnsError", func(tt *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(tt)
		mockLogger := &log.MockLogger{}

		mockLogger.On("InfoContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
		mockLogger.On("ErrorContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{
				Invoker: mockInvoker,
			}
		}

		mockInvoker.On("V1ExpertModeVolumeFlexCloneSplit", mock.Anything, mock.Anything, mock.Anything).
			Return(&coreapi.V1ExpertModeVolumeFlexCloneSplitForbidden{
				Code:    403,
				Message: "forbidden",
			}, nil)

		err := SubmitExpertModeFlexCloneSplit(context.Background(), "ext-vol-uuid", "", "12345", "pool-uuid-123", "jwt", mockLogger)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "unexpected response from Core API")
		mockInvoker.AssertExpectations(tt)
	})

	t.Run("InvokerError_ReturnsError", func(tt *testing.T) {
		mockInvoker := coreapi.NewMockInvoker(tt)
		mockLogger := &log.MockLogger{}

		mockLogger.On("InfoContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
		mockLogger.On("ErrorContext", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

		createCoreAPIClient = func(host, jwtToken string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{
				Invoker: mockInvoker,
			}
		}

		mockInvoker.On("V1ExpertModeVolumeFlexCloneSplit", mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("transport error"))

		err := SubmitExpertModeFlexCloneSplit(context.Background(), "ext-vol-uuid", "", "12345", "pool-uuid-123", "jwt", mockLogger)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "transport error")
		mockInvoker.AssertExpectations(tt)
	})
}
