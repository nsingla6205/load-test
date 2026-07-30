package endpoints

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/api/ontap-proxy-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/handlers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/middleware"
)

var (
	qosPolicyTestPoolUUID   = uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	qosPolicyTestPolicyUUID = uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")
)

func getQoSPolicyParams() oasgenserver.V1GetQoSPolicyParams {
	return oasgenserver.V1GetQoSPolicyParams{
		ProjectNumber: "123456",
		LocationId:    "us-central1",
		PoolId:        qosPolicyTestPoolUUID,
		UUID:          qosPolicyTestPolicyUUID,
	}
}

func updateQoSPolicyParams() oasgenserver.V1UpdateQoSPolicyParams {
	return oasgenserver.V1UpdateQoSPolicyParams{
		ProjectNumber: "123456",
		LocationId:    "us-central1",
		PoolId:        qosPolicyTestPoolUUID,
		UUID:          qosPolicyTestPolicyUUID,
	}
}

func deleteQoSPolicyParams() oasgenserver.V1DeleteQoSPolicyParams {
	return oasgenserver.V1DeleteQoSPolicyParams{
		ProjectNumber: "123456",
		LocationId:    "us-central1",
		PoolId:        qosPolicyTestPoolUUID,
		UUID:          qosPolicyTestPolicyUUID,
	}
}

func qosPolicyInfo(name, svmName string) *handlers.QoSPolicyInfo {
	policy := &handlers.QoSPolicyInfo{Name: name}
	policy.SVM.Name = svmName
	return policy
}

func useQoSPolicyMutationClient(t *testing.T, client handlers.OntapClient) {
	t.Helper()
	oldSetup := setupCredentialsForHandler
	oldEnsure := ensureCertificateOrPassword
	oldClient := newOntapClientFromContext
	t.Cleanup(func() {
		setupCredentialsForHandler = oldSetup
		ensureCertificateOrPassword = oldEnsure
		newOntapClientFromContext = oldClient
	})
	setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
		return ctx, nil
	}
	ensureCertificateOrPassword = func(context.Context) error { return nil }
	newOntapClientFromContext = func(context.Context) (handlers.OntapClient, error) {
		return client, nil
	}
}

func TestV1GetQoSPolicy(t *testing.T) {
	handler := Handler{}
	params := getQoSPolicyParams()

	t.Run("WhenSetupCredentialsFails_ReturnsUnauthorized", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		defer func() { setupCredentialsForHandler = oldSetup }()
		setupCredentialsForHandler = func(context.Context, string, string, string) (context.Context, error) {
			return nil, errors.New("no credentials")
		}

		res, err := handler.V1GetQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		unauth, ok := res.(*oasgenserver.V1GetQoSPolicyUnauthorized)
		require.True(t, ok, "expected Unauthorized, got %T", res)
		assert.Equal(t, 401, unauth.Code)
		assert.Contains(t, unauth.Message, "authentication error")
	})

	t.Run("WhenEnsureCertificateOrPasswordFails_ReturnsUnauthorized", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		defer func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
		}()
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return errors.New("no cert") }

		res, err := handler.V1GetQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		unauth, ok := res.(*oasgenserver.V1GetQoSPolicyUnauthorized)
		require.True(t, ok, "expected Unauthorized, got %T", res)
		assert.Equal(t, 401, unauth.Code)
	})

	t.Run("WhenNewOntapClientFails_ReturnsInternalServerError", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		oldClient := newOntapClientFromContext
		defer func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
			newOntapClientFromContext = oldClient
		}()
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return nil }
		newOntapClientFromContext = func(context.Context) (handlers.OntapClient, error) {
			return nil, errors.New("no client")
		}

		res, err := handler.V1GetQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		internal, ok := res.(*oasgenserver.V1GetQoSPolicyInternalServerError)
		require.True(t, ok, "expected InternalServerError, got %T", res)
		assert.Equal(t, 500, internal.Code)
		assert.Contains(t, internal.Message, "failed to connect to ONTAP")
	})

	t.Run("WhenNewOntapClientFailsWithProxyHTTPError_ReturnsMappedResponse", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		oldClient := newOntapClientFromContext
		defer func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
			newOntapClientFromContext = oldClient
		}()
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return nil }
		newOntapClientFromContext = func(context.Context) (handlers.OntapClient, error) {
			return nil, &middleware.ProxyHTTPError{Status: http.StatusBadRequest, Message: "Pool is in deleting state"}
		}

		res, err := handler.V1GetQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		badReq, ok := res.(*oasgenserver.V1GetQoSPolicyBadRequest)
		require.True(t, ok, "expected BadRequest, got %T", res)
		assert.Equal(t, http.StatusBadRequest, badReq.Code)
		assert.Equal(t, "Pool is in deleting state", badReq.Message)
	})

	t.Run("WhenGetQoSPolicyNotFound_ReturnsNotFound", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		oldClient := newOntapClientFromContext
		defer func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
			newOntapClientFromContext = oldClient
		}()
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return nil }
		mockClient := handlers.NewMockOntapClient(t)
		mockClient.On("GetQoSPolicy", mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(nil, errors.New("QoS policy not found: "+qosPolicyTestPolicyUUID.String())).Once()
		newOntapClientFromContext = func(context.Context) (handlers.OntapClient, error) {
			return mockClient, nil
		}

		res, err := handler.V1GetQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		notFound, ok := res.(*oasgenserver.V1GetQoSPolicyNotFound)
		require.True(t, ok, "expected NotFound, got %T", res)
		assert.Equal(t, 404, notFound.Code)
		assert.Contains(t, notFound.Message, "QoS policy not found")
	})

	t.Run("WhenGetQoSPolicyFailsWithProxyHTTPError_ReturnsMappedResponse", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		oldClient := newOntapClientFromContext
		defer func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
			newOntapClientFromContext = oldClient
		}()
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return nil }
		mockClient := handlers.NewMockOntapClient(t)
		mockClient.On("GetQoSPolicy", mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(nil, &middleware.ProxyHTTPError{Status: http.StatusForbidden, Message: "forbidden by pool state"}).Once()
		newOntapClientFromContext = func(context.Context) (handlers.OntapClient, error) {
			return mockClient, nil
		}

		res, err := handler.V1GetQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		forbidden, ok := res.(*oasgenserver.V1GetQoSPolicyForbidden)
		require.True(t, ok, "expected Forbidden, got %T", res)
		assert.Equal(t, http.StatusForbidden, forbidden.Code)
		assert.Equal(t, "forbidden by pool state", forbidden.Message)
	})

	t.Run("WhenGetQoSPolicyFails_ReturnsInternalServerError", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		oldClient := newOntapClientFromContext
		defer func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
			newOntapClientFromContext = oldClient
		}()
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return nil }
		mockClient := handlers.NewMockOntapClient(t)
		mockClient.On("GetQoSPolicy", mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(nil, errors.New("connection reset")).Once()
		newOntapClientFromContext = func(context.Context) (handlers.OntapClient, error) {
			return mockClient, nil
		}

		res, err := handler.V1GetQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		internal, ok := res.(*oasgenserver.V1GetQoSPolicyInternalServerError)
		require.True(t, ok, "expected InternalServerError, got %T", res)
		assert.Equal(t, 500, internal.Code)
		assert.Contains(t, internal.Message, "ONTAP operation failed")
	})

	t.Run("WhenGetQoSPolicySucceeds_ReturnsQoSPolicy", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		oldClient := newOntapClientFromContext
		defer func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
			newOntapClientFromContext = oldClient
		}()
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, credentialType string) (context.Context, error) {
			assert.Equal(t, middleware.CredentialTypeExpertModeUser, credentialType)
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return nil }

		maxIops := 5000
		shared := true
		mockClient := handlers.NewMockOntapClient(t)
		mockClient.On("GetQoSPolicy", mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(&handlers.QoSPolicyInfo{
				UUID:        qosPolicyTestPolicyUUID.String(),
				Name:        "cust-qos",
				Scope:       "svm",
				PolicyClass: "user_defined",
				ObjectCount: 2,
				Pgid:        12573,
				SVM: struct {
					Name string `json:"name"`
					UUID string `json:"uuid"`
				}{Name: "svm1", UUID: "02c9e252-41be-11e9-81d5-00a0986138f7"},
				Fixed: &struct {
					MaxThroughputIops *int  `json:"max_throughput_iops"`
					MaxThroughputMbps *int  `json:"max_throughput_mbps"`
					MinThroughputIops *int  `json:"min_throughput_iops"`
					MinThroughputMbps *int  `json:"min_throughput_mbps"`
					CapacityShared    *bool `json:"capacity_shared"`
				}{
					MaxThroughputIops: &maxIops,
					CapacityShared:    &shared,
				},
			}, nil).Once()
		newOntapClientFromContext = func(context.Context) (handlers.OntapClient, error) {
			return mockClient, nil
		}

		res, err := handler.V1GetQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		policy, ok := res.(*oasgenserver.QoSPolicy)
		require.True(t, ok, "expected QoSPolicy, got %T", res)
		assert.Equal(t, qosPolicyTestPolicyUUID, policy.UUID.Value)
		assert.Equal(t, "cust-qos", policy.Name.Value)
		assert.Equal(t, 12573, policy.Pgid.Value)
		assert.Equal(t, "svm1", policy.Svm.Value.Name.Value)
		assert.Equal(t, 5000, policy.Fixed.Value.MaxThroughputIops.Value)
		assert.True(t, policy.Fixed.Value.CapacityShared.Value)
	})
}

func TestV1UpdateQoSPolicy(t *testing.T) {
	handler := Handler{}
	params := updateQoSPolicyParams()

	t.Run("customer policy update is forwarded", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(
				mock.Anything,
				http.MethodPatch,
				"/api/storage/qos/policies/"+qosPolicyTestPolicyUUID.String(),
				mock.MatchedBy(func(body []byte) bool {
					return strings.Contains(string(body), `"max_throughput_iops":5000`)
				}),
			).
			Return(nil, http.StatusOK, nil).
			Once()

		req := &oasgenserver.QoSPolicyUpdate{
			Fixed: oasgenserver.NewOptQoSPolicyUpdateFixed(oasgenserver.QoSPolicyUpdateFixed{
				MaxThroughputIops: oasgenserver.NewOptInt(5000),
			}),
		}
		res, err := handler.V1UpdateQoSPolicy(context.Background(), req, params)
		require.NoError(t, err)
		_, ok := res.(*oasgenserver.V1UpdateQoSPolicyOK)
		require.True(t, ok, "expected OK, got %T", res)
	})

	t.Run("customer policy update accepted", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(mock.Anything, http.MethodPatch, mock.Anything, mock.Anything).
			Return(nil, http.StatusAccepted, nil).
			Once()

		res, err := handler.V1UpdateQoSPolicy(
			context.Background(),
			&oasgenserver.QoSPolicyUpdate{Name: oasgenserver.NewOptString("cust2")},
			params,
		)
		require.NoError(t, err)
		_, ok := res.(*oasgenserver.V1UpdateQoSPolicyAccepted)
		require.True(t, ok, "expected Accepted, got %T", res)
	})

	t.Run("prepare mutation auth failure is mapped", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		t.Cleanup(func() { setupCredentialsForHandler = oldSetup })
		setupCredentialsForHandler = func(context.Context, string, string, string) (context.Context, error) {
			return nil, errors.New("no credentials")
		}

		res, err := handler.V1UpdateQoSPolicy(
			context.Background(),
			&oasgenserver.QoSPolicyUpdate{Name: oasgenserver.NewOptString("cust2")},
			params,
		)
		require.NoError(t, err)
		unauth, ok := res.(*oasgenserver.V1UpdateQoSPolicyUnauthorized)
		require.True(t, ok, "expected Unauthorized, got %T", res)
		assert.Equal(t, http.StatusUnauthorized, unauth.Code)
	})

	t.Run("ExecuteAPI transport failure is mapped", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(mock.Anything, http.MethodPatch, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New("connection reset")).
			Once()

		res, err := handler.V1UpdateQoSPolicy(
			context.Background(),
			&oasgenserver.QoSPolicyUpdate{Name: oasgenserver.NewOptString("cust2")},
			params,
		)
		require.NoError(t, err)
		internal, ok := res.(*oasgenserver.V1UpdateQoSPolicyInternalServerError)
		require.True(t, ok, "expected InternalServerError, got %T", res)
		assert.Contains(t, internal.Message, "ONTAP operation failed")
	})

	t.Run("ONTAP error status is mapped from body", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(mock.Anything, http.MethodPatch, mock.Anything, mock.Anything).
			Return([]byte(`{"error":{"message":"policy in use"}}`), http.StatusConflict, nil).
			Once()

		res, err := handler.V1UpdateQoSPolicy(
			context.Background(),
			&oasgenserver.QoSPolicyUpdate{Name: oasgenserver.NewOptString("cust2")},
			params,
		)
		require.NoError(t, err)
		badReq, ok := res.(*oasgenserver.V1UpdateQoSPolicyBadRequest)
		require.True(t, ok, "expected BadRequest, got %T", res)
		assert.Equal(t, http.StatusConflict, badReq.Code)
		assert.Equal(t, "policy in use", badReq.Message)
	})

	t.Run("marshal failure is mapped", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		oldMarshal := jsonMarshal
		t.Cleanup(func() { jsonMarshal = oldMarshal })
		jsonMarshal = func(any) ([]byte, error) {
			return nil, errors.New("marshal boom")
		}

		res, err := handler.V1UpdateQoSPolicy(
			context.Background(),
			&oasgenserver.QoSPolicyUpdate{Name: oasgenserver.NewOptString("cust2")},
			params,
		)
		require.NoError(t, err)
		internal, ok := res.(*oasgenserver.V1UpdateQoSPolicyInternalServerError)
		require.True(t, ok, "expected InternalServerError, got %T", res)
		assert.Contains(t, internal.Message, "failed to encode QoS policy update")
	})

	for _, test := range []struct {
		name  string
		fixed oasgenserver.QoSPolicyUpdateFixed
	}{
		{
			name: "fixed minimum IOPS is denied",
			fixed: oasgenserver.QoSPolicyUpdateFixed{
				MinThroughputIops: oasgenserver.NewOptInt(100),
			},
		},
		{
			name: "fixed minimum Mbps is denied",
			fixed: oasgenserver.QoSPolicyUpdateFixed{
				MinThroughputMbps: oasgenserver.NewOptInt(50),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := handlers.NewMockOntapClient(t)
			useQoSPolicyMutationClient(t, client)
			client.EXPECT().
				GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
				Return(qosPolicyInfo("customer-policy", "svm1"), nil).
				Once()

			res, err := handler.V1UpdateQoSPolicy(
				context.Background(),
				&oasgenserver.QoSPolicyUpdate{
					Fixed: oasgenserver.NewOptQoSPolicyUpdateFixed(test.fixed),
				},
				params,
			)
			require.NoError(t, err)
			badRequest, ok := res.(*oasgenserver.V1UpdateQoSPolicyBadRequest)
			require.True(t, ok, "expected BadRequest, got %T", res)
			assert.Equal(t, http.StatusBadRequest, badRequest.Code)
			assert.Equal(t, "fixed QoS minimum throughput is not supported", badRequest.Message)
		})
	}

	t.Run("adaptive absolute minimum is forwarded", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(
				mock.Anything,
				http.MethodPatch,
				"/api/storage/qos/policies/"+qosPolicyTestPolicyUUID.String(),
				mock.MatchedBy(func(body []byte) bool {
					return strings.Contains(string(body), `"absolute_min_iops":100`)
				}),
			).
			Return(nil, http.StatusOK, nil).
			Once()

		res, err := handler.V1UpdateQoSPolicy(
			context.Background(),
			&oasgenserver.QoSPolicyUpdate{
				Adaptive: oasgenserver.NewOptQoSPolicyUpdateAdaptive(oasgenserver.QoSPolicyUpdateAdaptive{
					AbsoluteMinIops: oasgenserver.NewOptInt(100),
				}),
			},
			params,
		)
		require.NoError(t, err)
		_, ok := res.(*oasgenserver.V1UpdateQoSPolicyOK)
		require.True(t, ok, "expected OK, got %T", res)
	})

	t.Run("reserved policy update is denied", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("svm1-qos-policy", "svm1"), nil).
			Once()

		res, err := handler.V1UpdateQoSPolicy(
			context.Background(),
			&oasgenserver.QoSPolicyUpdate{Name: oasgenserver.NewOptString("customer-policy")},
			params,
		)
		require.NoError(t, err)
		forbidden, ok := res.(*oasgenserver.V1UpdateQoSPolicyForbidden)
		require.True(t, ok, "expected Forbidden, got %T", res)
		assert.Contains(t, forbidden.Message, "reserved")
	})

	t.Run("rename to reserved policy name is denied", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()

		res, err := handler.V1UpdateQoSPolicy(
			context.Background(),
			&oasgenserver.QoSPolicyUpdate{Name: oasgenserver.NewOptString("SVM1-QOS-POLICY")},
			params,
		)
		require.NoError(t, err)
		_, ok := res.(*oasgenserver.V1UpdateQoSPolicyForbidden)
		require.True(t, ok, "expected Forbidden, got %T", res)
	})
}

func TestV1DeleteQoSPolicy(t *testing.T) {
	handler := Handler{}
	params := deleteQoSPolicyParams()

	t.Run("customer policy delete is forwarded", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(
				mock.Anything,
				http.MethodDelete,
				"/api/storage/qos/policies/"+qosPolicyTestPolicyUUID.String(),
				[]byte(nil),
			).
			Return(nil, http.StatusNoContent, nil).
			Once()

		res, err := handler.V1DeleteQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		_, ok := res.(*oasgenserver.V1DeleteQoSPolicyNoContent)
		require.True(t, ok, "expected NoContent, got %T", res)
	})

	t.Run("customer policy delete returns OK", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(mock.Anything, http.MethodDelete, mock.Anything, mock.Anything).
			Return(nil, http.StatusOK, nil).
			Once()

		res, err := handler.V1DeleteQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		_, ok := res.(*oasgenserver.V1DeleteQoSPolicyOK)
		require.True(t, ok, "expected OK, got %T", res)
	})

	t.Run("customer policy delete returns Accepted", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(mock.Anything, http.MethodDelete, mock.Anything, mock.Anything).
			Return(nil, http.StatusAccepted, nil).
			Once()

		res, err := handler.V1DeleteQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		_, ok := res.(*oasgenserver.V1DeleteQoSPolicyAccepted)
		require.True(t, ok, "expected Accepted, got %T", res)
	})

	t.Run("prepare mutation cert failure is mapped", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		t.Cleanup(func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
		})
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return errors.New("no cert") }

		res, err := handler.V1DeleteQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		unauth, ok := res.(*oasgenserver.V1DeleteQoSPolicyUnauthorized)
		require.True(t, ok, "expected Unauthorized, got %T", res)
		assert.Equal(t, http.StatusUnauthorized, unauth.Code)
	})

	t.Run("ExecuteAPI transport failure is mapped", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(mock.Anything, http.MethodDelete, mock.Anything, mock.Anything).
			Return(nil, 0, errors.New("dial failed")).
			Once()

		res, err := handler.V1DeleteQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		internal, ok := res.(*oasgenserver.V1DeleteQoSPolicyInternalServerError)
		require.True(t, ok, "expected InternalServerError, got %T", res)
		assert.Contains(t, internal.Message, "ONTAP operation failed")
	})

	t.Run("ONTAP error status uses raw body message", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("customer-policy", "svm1"), nil).
			Once()
		client.EXPECT().
			ExecuteAPI(mock.Anything, http.MethodDelete, mock.Anything, mock.Anything).
			Return([]byte("volume still attached"), http.StatusBadRequest, nil).
			Once()

		res, err := handler.V1DeleteQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		badReq, ok := res.(*oasgenserver.V1DeleteQoSPolicyBadRequest)
		require.True(t, ok, "expected BadRequest, got %T", res)
		assert.Equal(t, "volume still attached", badReq.Message)
	})

	t.Run("reserved policy delete is denied", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, qosPolicyTestPolicyUUID.String()).
			Return(qosPolicyInfo("svm1-qos-policy", "svm1"), nil).
			Once()

		res, err := handler.V1DeleteQoSPolicy(context.Background(), params)
		require.NoError(t, err)
		forbidden, ok := res.(*oasgenserver.V1DeleteQoSPolicyForbidden)
		require.True(t, ok, "expected Forbidden, got %T", res)
		assert.Contains(t, forbidden.Message, "reserved")
	})
}

func TestPrepareQoSPolicyMutation(t *testing.T) {
	t.Run("client build ProxyHTTPError is returned", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		oldClient := newOntapClientFromContext
		t.Cleanup(func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
			newOntapClientFromContext = oldClient
		})
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return nil }
		newOntapClientFromContext = func(context.Context) (handlers.OntapClient, error) {
			return nil, &middleware.ProxyHTTPError{Status: http.StatusBadRequest, Message: "pool deleting"}
		}

		_, _, _, status, message := prepareQoSPolicyMutation(context.Background(), "123", "pool", "uuid")
		assert.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, "pool deleting", message)
	})

	t.Run("client build generic error is returned", func(t *testing.T) {
		oldSetup := setupCredentialsForHandler
		oldEnsure := ensureCertificateOrPassword
		oldClient := newOntapClientFromContext
		t.Cleanup(func() {
			setupCredentialsForHandler = oldSetup
			ensureCertificateOrPassword = oldEnsure
			newOntapClientFromContext = oldClient
		})
		setupCredentialsForHandler = func(ctx context.Context, _, _ string, _ string) (context.Context, error) {
			return ctx, nil
		}
		ensureCertificateOrPassword = func(context.Context) error { return nil }
		newOntapClientFromContext = func(context.Context) (handlers.OntapClient, error) {
			return nil, errors.New("no client")
		}

		_, _, _, status, message := prepareQoSPolicyMutation(context.Background(), "123", "pool", "uuid")
		assert.Equal(t, http.StatusInternalServerError, status)
		assert.Contains(t, message, "failed to connect to ONTAP")
	})

	t.Run("policy not found is returned", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, "missing").
			Return(nil, errors.New("QoS policy not found: missing")).
			Once()

		_, _, _, status, message := prepareQoSPolicyMutation(context.Background(), "123", "pool", "missing")
		assert.Equal(t, http.StatusNotFound, status)
		assert.Contains(t, message, "QoS policy not found")
	})

	t.Run("policy lookup failure is returned", func(t *testing.T) {
		client := handlers.NewMockOntapClient(t)
		useQoSPolicyMutationClient(t, client)
		client.EXPECT().
			GetQoSPolicy(mock.Anything, "uuid").
			Return(nil, errors.New("timeout")).
			Once()

		_, _, _, status, message := prepareQoSPolicyMutation(context.Background(), "123", "pool", "uuid")
		assert.Equal(t, http.StatusInternalServerError, status)
		assert.Contains(t, message, "QoS policy lookup failed")
	})
}

func TestOntapAPIErrorMessage(t *testing.T) {
	t.Run("parses ONTAP error envelope", func(t *testing.T) {
		assert.Equal(t, "busy", ontapAPIErrorMessage(http.StatusConflict, []byte(`{"error":{"message":"busy"}}`)))
	})
	t.Run("falls back to raw body", func(t *testing.T) {
		assert.Equal(t, "raw failure", ontapAPIErrorMessage(http.StatusBadRequest, []byte(" raw failure ")))
	})
	t.Run("falls back to status text", func(t *testing.T) {
		assert.Equal(t, http.StatusText(http.StatusBadGateway), ontapAPIErrorMessage(http.StatusBadGateway, nil))
	})
}

func TestUpdateQoSPolicyError(t *testing.T) {
	tests := []struct {
		status int
		want   any
	}{
		{http.StatusUnauthorized, (*oasgenserver.V1UpdateQoSPolicyUnauthorized)(nil)},
		{http.StatusForbidden, (*oasgenserver.V1UpdateQoSPolicyForbidden)(nil)},
		{http.StatusNotFound, (*oasgenserver.V1UpdateQoSPolicyNotFound)(nil)},
		{http.StatusBadRequest, (*oasgenserver.V1UpdateQoSPolicyBadRequest)(nil)},
		{http.StatusConflict, (*oasgenserver.V1UpdateQoSPolicyBadRequest)(nil)},
		{http.StatusOK, (*oasgenserver.V1UpdateQoSPolicyInternalServerError)(nil)},
		{http.StatusBadGateway, (*oasgenserver.V1UpdateQoSPolicyInternalServerError)(nil)},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			res := updateQoSPolicyError(tt.status, "msg")
			assert.IsType(t, tt.want, res)
		})
	}
}

func TestDeleteQoSPolicyError(t *testing.T) {
	tests := []struct {
		status int
		want   any
	}{
		{http.StatusUnauthorized, (*oasgenserver.V1DeleteQoSPolicyUnauthorized)(nil)},
		{http.StatusForbidden, (*oasgenserver.V1DeleteQoSPolicyForbidden)(nil)},
		{http.StatusNotFound, (*oasgenserver.V1DeleteQoSPolicyNotFound)(nil)},
		{http.StatusBadRequest, (*oasgenserver.V1DeleteQoSPolicyBadRequest)(nil)},
		{http.StatusConflict, (*oasgenserver.V1DeleteQoSPolicyBadRequest)(nil)},
		{http.StatusOK, (*oasgenserver.V1DeleteQoSPolicyInternalServerError)(nil)},
		{http.StatusBadGateway, (*oasgenserver.V1DeleteQoSPolicyInternalServerError)(nil)},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			res := deleteQoSPolicyError(tt.status, "msg")
			assert.IsType(t, tt.want, res)
		})
	}
}

func TestQoSPolicyInfoToOAS(t *testing.T) {
	t.Run("nil returns empty", func(t *testing.T) {
		out := qosPolicyInfoToOAS(nil)
		require.NotNil(t, out)
		assert.False(t, out.Name.IsSet())
	})

	t.Run("maps fixed and adaptive fields", func(t *testing.T) {
		maxMbps := 500
		minIops := 100
		minMbps := 50
		absMin := 10
		expected := 100
		peak := 200
		shared := false
		out := qosPolicyInfoToOAS(&handlers.QoSPolicyInfo{
			Name: "full-pol",
			Fixed: &struct {
				MaxThroughputIops *int  `json:"max_throughput_iops"`
				MaxThroughputMbps *int  `json:"max_throughput_mbps"`
				MinThroughputIops *int  `json:"min_throughput_iops"`
				MinThroughputMbps *int  `json:"min_throughput_mbps"`
				CapacityShared    *bool `json:"capacity_shared"`
			}{
				MaxThroughputMbps: &maxMbps,
				MinThroughputIops: &minIops,
				MinThroughputMbps: &minMbps,
				CapacityShared:    &shared,
			},
			Adaptive: &struct {
				ExpectedIops           *int   `json:"expected_iops"`
				PeakIops               *int   `json:"peak_iops"`
				AbsoluteMinIops        *int   `json:"absolute_min_iops"`
				ExpectedIopsAllocation string `json:"expected_iops_allocation"`
				PeakIopsAllocation     string `json:"peak_iops_allocation"`
				BlockSize              string `json:"block_size"`
			}{
				ExpectedIops:           &expected,
				PeakIops:               &peak,
				AbsoluteMinIops:        &absMin,
				ExpectedIopsAllocation: "used-space",
				PeakIopsAllocation:     "allocated-space",
				BlockSize:              "ANY",
			},
		})
		assert.Equal(t, "full-pol", out.Name.Value)
		assert.Equal(t, 500, out.Fixed.Value.MaxThroughputMbps.Value)
		assert.Equal(t, 100, out.Fixed.Value.MinThroughputIops.Value)
		assert.Equal(t, 50, out.Fixed.Value.MinThroughputMbps.Value)
		assert.False(t, out.Fixed.Value.CapacityShared.Value)
		assert.Equal(t, 10, out.Adaptive.Value.AbsoluteMinIops.Value)
		assert.Equal(t, "allocated-space", out.Adaptive.Value.PeakIopsAllocation.Value)
		assert.Equal(t, "used-space", out.Adaptive.Value.ExpectedIopsAllocation.Value)
		assert.Equal(t, "ANY", out.Adaptive.Value.BlockSize.Value)
	})
}
