package oci

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	ocicommon "github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/secrets"
	"github.com/oracle/oci-go-sdk/v65/vault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ---------------------------------------------------------------------------
// Mock HTTP dispatcher — replaces the real OCI SDK HTTP client.
//
// The OCI SDK's BaseClient.HTTPClient is an HTTPRequestDispatcher interface
// (with a single Do(*http.Request) method) explicitly designed for testing.
// ---------------------------------------------------------------------------

type mockHTTPDispatcher struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPDispatcher) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}

func mockJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Opc-Request-Id": []string{"test-req-id"},
		},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: &http.Request{URL: &url.URL{Path: "/mock"}},
	}
}

// newTestOciServices creates an OciServices backed by mock HTTP dispatchers.
// Pass nil for a dispatcher you don't need in a given test.
func newTestOciServices(t *testing.T, vaultDispatcher, secretsDispatcher *mockHTTPDispatcher) *OciServices {
	t.Helper()
	ctx := proxyTestCtx()
	pemKey := testPrivateKeyPEM(t)

	configProvider := ocicommon.NewRawConfigurationProvider(
		"ocid1.tenancy.oc1..test",
		"ocid1.user.oc1..test",
		"us-ashburn-1",
		"aa:bb:cc:dd:ee:ff:00:11",
		pemKey,
		nil,
	)

	vaultCl, err := vault.NewVaultsClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)
	if vaultDispatcher != nil {
		vaultCl.HTTPClient = vaultDispatcher
	}

	secretsCl, err := secrets.NewSecretsClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)
	if secretsDispatcher != nil {
		secretsCl.HTTPClient = secretsDispatcher
	}

	return &OciServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminOCIService: &AdminOCIService{
			vaultClient:   vaultCl,
			secretsClient: secretsCl,
		},
	}
}

// ---------------------------------------------------------------------------
// TestExtractSecretBundleContent
// ---------------------------------------------------------------------------

func TestExtractSecretBundleContent(t *testing.T) {
	t.Run("positive — valid base64 content decoded", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte("my-secret-value"))
		content := secrets.Base64SecretBundleContentDetails{
			Content: ocicommon.String(encoded),
		}
		val, err := extractSecretBundleContent(content)
		assert.NoError(t, err)
		assert.Equal(t, "my-secret-value", val)
	})

	t.Run("positive — empty string payload", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte(""))
		content := secrets.Base64SecretBundleContentDetails{
			Content: ocicommon.String(encoded),
		}
		val, err := extractSecretBundleContent(content)
		assert.NoError(t, err)
		assert.Equal(t, "", val)
	})

	t.Run("negative — nil content interface", func(t *testing.T) {
		val, err := extractSecretBundleContent(nil)
		assert.Error(t, err)
		assert.Equal(t, "", val)
	})

	t.Run("negative — nil Content pointer inside valid struct", func(t *testing.T) {
		content := secrets.Base64SecretBundleContentDetails{
			Content: nil,
		}
		val, err := extractSecretBundleContent(content)
		assert.Error(t, err)
		assert.Equal(t, "", val)
	})

	t.Run("negative — invalid base64 string", func(t *testing.T) {
		content := secrets.Base64SecretBundleContentDetails{
			Content: ocicommon.String("not-valid-base64!!!"),
		}
		val, err := extractSecretBundleContent(content)
		assert.Error(t, err)
		assert.Equal(t, "", val)
	})
}

// ---------------------------------------------------------------------------
// TestIsSecretInDeletionState
// ---------------------------------------------------------------------------

func TestIsSecretInDeletionState(t *testing.T) {
	deletionStates := []vault.SecretLifecycleStateEnum{
		vault.SecretLifecycleStatePendingDeletion,
		vault.SecretLifecycleStateSchedulingDeletion,
		vault.SecretLifecycleStateDeleting,
		vault.SecretLifecycleStateDeleted,
	}
	for _, state := range deletionStates {
		t.Run("positive — "+string(state), func(t *testing.T) {
			assert.True(t, isSecretInDeletionState(state))
		})
	}

	nonDeletionStates := []vault.SecretLifecycleStateEnum{
		vault.SecretLifecycleStateCreating,
		vault.SecretLifecycleStateActive,
		vault.SecretLifecycleStateCancellingDeletion,
		vault.SecretLifecycleStateUpdating,
		vault.SecretLifecycleStateFailed,
	}
	for _, state := range nonDeletionStates {
		t.Run("negative — "+string(state), func(t *testing.T) {
			assert.False(t, isSecretInDeletionState(state))
		})
	}
}

// ---------------------------------------------------------------------------
// TestDerefInt64
// ---------------------------------------------------------------------------

func TestDerefInt64(t *testing.T) {
	t.Run("positive — non-nil returns value", func(t *testing.T) {
		v := int64(42)
		assert.Equal(t, int64(42), derefInt64(&v))
	})

	t.Run("positive — zero value pointer", func(t *testing.T) {
		v := int64(0)
		assert.Equal(t, int64(0), derefInt64(&v))
	})

	t.Run("negative — nil returns 0", func(t *testing.T) {
		assert.Equal(t, int64(0), derefInt64(nil))
	})
}

// ---------------------------------------------------------------------------
// TestDerefString
// ---------------------------------------------------------------------------

func TestDerefString(t *testing.T) {
	t.Run("positive — non-nil returns value", func(t *testing.T) {
		v := "hello"
		assert.Equal(t, "hello", derefString(&v))
	})

	t.Run("positive — empty string pointer", func(t *testing.T) {
		v := ""
		assert.Equal(t, "", derefString(&v))
	})

	t.Run("negative — nil returns empty string", func(t *testing.T) {
		assert.Equal(t, "", derefString(nil))
	})
}

// ---------------------------------------------------------------------------
// TestCreateSecret
// ---------------------------------------------------------------------------

func TestCreateSecret(t *testing.T) {
	t.Run("positive — secret created successfully", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":                   "ocid1.vaultsecret.oc1..created",
					"secretName":           "my-secret",
					"compartmentId":        "ocid1.compartment.oc1..test",
					"lifecycleState":       "ACTIVE",
					"currentVersionNumber": 1
				}`), nil
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		result, err := svc.CreateSecret(
			"ocid1.compartment.oc1..test",
			"ocid1.vault.oc1..test",
			"ocid1.key.oc1..test",
			"my-secret",
			"super-secret-value",
		)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "ocid1.vaultsecret.oc1..created", result.Ocid)
		assert.Equal(t, "my-secret", result.Name)
		assert.Equal(t, "super-secret-value", result.Value)
		assert.Equal(t, int64(1), result.Version)
	})

	t.Run("negative — empty secret value", func(t *testing.T) {
		svc := newTestOciServices(t, nil, nil)
		result, err := svc.CreateSecret(
			"ocid1.compartment.oc1..test",
			"ocid1.vault.oc1..test",
			"ocid1.key.oc1..test",
			"my-secret",
			"",
		)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, errors.Unwrap(err).Error(), "empty")
	})

	t.Run("negative — API returns conflict error", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockServiceError{statusCode: http.StatusConflict, code: "Conflict", message: "secret already exists"}
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		result, err := svc.CreateSecret(
			"ocid1.compartment.oc1..test",
			"ocid1.vault.oc1..test",
			"ocid1.key.oc1..test",
			"dup-secret",
			"value",
		)
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — transport error", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		result, err := svc.CreateSecret(
			"ocid1.compartment.oc1..test",
			"ocid1.vault.oc1..test",
			"ocid1.key.oc1..test",
			"my-secret",
			"value",
		)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

// ---------------------------------------------------------------------------
// TestGetSecretByName
// ---------------------------------------------------------------------------

func TestGetSecretByName(t *testing.T) {
	encodedValue := base64.StdEncoding.EncodeToString([]byte("fetched-value"))

	t.Run("positive — secret found and decoded", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"secretId":      "ocid1.vaultsecret.oc1..found",
					"versionNumber": 3,
					"secretBundleContent": {
						"contentType": "BASE64",
						"content":     "`+encodedValue+`"
					}
				}`), nil
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := svc.GetSecretByName("my-secret", "ocid1.vault.oc1..test")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "my-secret", result.Name)
		assert.Equal(t, "fetched-value", result.Value)
		assert.Equal(t, int64(3), result.Version)
		assert.Equal(t, "ocid1.vaultsecret.oc1..found", result.Ocid)
	})

	t.Run("negative — 404 returns nil nil", func(t *testing.T) {
		err404 := &mockServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "resource not found"}
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, err404
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := svc.GetSecretByName("missing", "ocid1.vault.oc1..test")
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — non-404 API error returned", func(t *testing.T) {
		err403 := &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "not authorized"}
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, err403
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := svc.GetSecretByName("broken", "ocid1.vault.oc1..test")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — nil bundle content returns nil nil", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"secretId":      "ocid1.vaultsecret.oc1..test",
					"versionNumber": 1
				}`), nil
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := svc.GetSecretByName("empty-content", "ocid1.vault.oc1..test")
		assert.NoError(t, err)
		assert.Nil(t, result)
	})
}

// ---------------------------------------------------------------------------
// TestGetSecretWithLatestVersion
// ---------------------------------------------------------------------------

func TestGetSecretWithLatestVersion(t *testing.T) {
	t.Run("positive — metadata + version returned", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..test",
					"secretName":     "admin-password",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		origGetSecretVersion := GetSecretVersion
		defer func() { GetSecretVersion = origGetSecretVersion }()
		GetSecretVersion = func(ociSvc *OciServices, secretID string, versionNumber ...int64) (*OCICustomSecret, error) {
			return &OCICustomSecret{Value: "latest-password", Version: 5}, nil
		}

		result, err := svc.GetSecretWithLatestVersion("ocid1.vaultsecret.oc1..test")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "admin-password", result.Name)
		assert.Equal(t, "latest-password", result.Value)
		assert.Equal(t, int64(5), result.Version)
		assert.Equal(t, "ocid1.vaultsecret.oc1..test", result.Ocid)
	})

	t.Run("negative — GetSecret API fails", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "vault access denied"}
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		result, err := svc.GetSecretWithLatestVersion("ocid1.vaultsecret.oc1..test")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — GetSecret 404 returns error", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "not found"}
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		result, err := svc.GetSecretWithLatestVersion("ocid1.vaultsecret.oc1..gone")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — secret in deletion state returns nil nil", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..test",
					"secretName":     "old-secret",
					"lifecycleState": "PENDING_DELETION"
				}`), nil
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		result, err := svc.GetSecretWithLatestVersion("ocid1.vaultsecret.oc1..test")
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — GetSecretVersion fails", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..test",
					"secretName":     "admin-password",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		origGetSecretVersion := GetSecretVersion
		defer func() { GetSecretVersion = origGetSecretVersion }()
		GetSecretVersion = func(ociSvc *OciServices, secretID string, versionNumber ...int64) (*OCICustomSecret, error) {
			return nil, errors.New("bundle fetch failed")
		}

		result, err := svc.GetSecretWithLatestVersion("ocid1.vaultsecret.oc1..test")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — GetSecretVersion returns nil version", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..test",
					"secretName":     "admin-password",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		origGetSecretVersion := GetSecretVersion
		defer func() { GetSecretVersion = origGetSecretVersion }()
		GetSecretVersion = func(ociSvc *OciServices, secretID string, versionNumber ...int64) (*OCICustomSecret, error) {
			return nil, nil
		}

		result, err := svc.GetSecretWithLatestVersion("ocid1.vaultsecret.oc1..test")
		assert.NoError(t, err)
		assert.Nil(t, result)
	})
}

// ---------------------------------------------------------------------------
// TestGetSecretWithCustomVersion
// ---------------------------------------------------------------------------

func TestGetSecretWithCustomVersion(t *testing.T) {
	t.Run("positive — specific version returned", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..test",
					"secretName":     "db-password",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		origGetSecretVersion := GetSecretVersion
		defer func() { GetSecretVersion = origGetSecretVersion }()
		GetSecretVersion = func(ociSvc *OciServices, secretID string, versionNumber ...int64) (*OCICustomSecret, error) {
			require.Len(t, versionNumber, 1)
			assert.Equal(t, int64(7), versionNumber[0])
			return &OCICustomSecret{Value: "version-7-password", Version: 7}, nil
		}

		result, err := svc.GetSecretWithCustomVersion("ocid1.vaultsecret.oc1..test", 7)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "db-password", result.Name)
		assert.Equal(t, "version-7-password", result.Value)
		assert.Equal(t, int64(7), result.Version)
		assert.Equal(t, "ocid1.vaultsecret.oc1..test", result.Ocid)
	})

	t.Run("negative — GetSecret API fails", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthenticated", message: "not authenticated"}
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		result, err := svc.GetSecretWithCustomVersion("ocid1.vaultsecret.oc1..test", 3)
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — GetSecret 404 returns error", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "not found or unauthorized"}
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		result, err := svc.GetSecretWithCustomVersion("ocid1.vaultsecret.oc1..gone", 1)
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — secret in SCHEDULING_DELETION state", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..test",
					"secretName":     "old-secret",
					"lifecycleState": "SCHEDULING_DELETION"
				}`), nil
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		result, err := svc.GetSecretWithCustomVersion("ocid1.vaultsecret.oc1..test", 1)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — GetSecretVersion fails", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..test",
					"secretName":     "db-password",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		origGetSecretVersion := GetSecretVersion
		defer func() { GetSecretVersion = origGetSecretVersion }()
		GetSecretVersion = func(ociSvc *OciServices, secretID string, versionNumber ...int64) (*OCICustomSecret, error) {
			return nil, errors.New("version not accessible")
		}

		result, err := svc.GetSecretWithCustomVersion("ocid1.vaultsecret.oc1..test", 2)
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — GetSecretVersion returns nil", func(t *testing.T) {
		vaultDispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..test",
					"secretName":     "db-password",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServices(t, vaultDispatcher, nil)

		origGetSecretVersion := GetSecretVersion
		defer func() { GetSecretVersion = origGetSecretVersion }()
		GetSecretVersion = func(ociSvc *OciServices, secretID string, versionNumber ...int64) (*OCICustomSecret, error) {
			return nil, nil
		}

		result, err := svc.GetSecretWithCustomVersion("ocid1.vaultsecret.oc1..test", 99)
		assert.NoError(t, err)
		assert.Nil(t, result)
	})
}

// ---------------------------------------------------------------------------
// TestDeleteSecret
// ---------------------------------------------------------------------------

func TestDeleteSecret(t *testing.T) {
	// DeleteSecret first issues GET /secrets/{id} (lifecycle pre-flight) and then,
	// only if the secret is not already in a deletion state, POSTs to
	// /secrets/{id}/actions/scheduleDeletion. The dispatchers below discriminate
	// the two calls by request URL path.
	const scheduleDeletionPathFragment = "scheduleDeletion"

	t.Run("positive — active secret: deletion scheduled successfully", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, scheduleDeletionPathFragment) {
					return mockJSONResponse(http.StatusOK, `{}`), nil
				}
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..todelete",
					"secretName":     "to-delete",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		err := svc.DeleteSecret("ocid1.vaultsecret.oc1..todelete")
		assert.NoError(t, err)
	})

	t.Run("positive — secret already PENDING_DELETION: ScheduleSecretDeletion is skipped", func(t *testing.T) {
		var scheduleDeletionCalled bool
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, scheduleDeletionPathFragment) {
					scheduleDeletionCalled = true
					return mockJSONResponse(http.StatusConflict, `{"code":"Conflict","message":"already scheduled"}`), nil
				}
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..alreadydeleting",
					"secretName":     "old-secret",
					"lifecycleState": "PENDING_DELETION"
				}`), nil
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		err := svc.DeleteSecret("ocid1.vaultsecret.oc1..alreadydeleting")
		assert.NoError(t, err)
		assert.False(t, scheduleDeletionCalled, "ScheduleSecretDeletion must not be invoked for secrets already in deletion state")
	})

	t.Run("positive — GetSecret 404 treated as already deleted (no-op)", func(t *testing.T) {
		var scheduleDeletionCalled bool
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, scheduleDeletionPathFragment) {
					scheduleDeletionCalled = true
					return mockJSONResponse(http.StatusOK, `{}`), nil
				}
				return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "not found"}
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		err := svc.DeleteSecret("ocid1.vaultsecret.oc1..gone")
		assert.NoError(t, err)
		assert.False(t, scheduleDeletionCalled)
	})

	t.Run("negative — ScheduleSecretDeletion returns conflict for an active secret", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, scheduleDeletionPathFragment) {
					return nil, &mockServiceError{statusCode: http.StatusConflict, code: "Conflict", message: "secret is already scheduled for deletion"}
				}
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..racy",
					"secretName":     "racy",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		err := svc.DeleteSecret("ocid1.vaultsecret.oc1..racy")
		assert.Error(t, err)
	})

	t.Run("negative — GetSecret transport error", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("network timeout")
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		err := svc.DeleteSecret("ocid1.vaultsecret.oc1..test")
		assert.Error(t, err)
	})

	t.Run("negative — ScheduleSecretDeletion transport error", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, scheduleDeletionPathFragment) {
					return nil, errors.New("network timeout")
				}
				return mockJSONResponse(http.StatusOK, `{
					"id":             "ocid1.vaultsecret.oc1..test",
					"secretName":     "test",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		err := svc.DeleteSecret("ocid1.vaultsecret.oc1..test")
		assert.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// TestAddSecretVersion (_addSecretVersion via the test seam)
// ---------------------------------------------------------------------------

func TestAddSecretVersion(t *testing.T) {
	t.Run("positive — new version created", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"id":                   "ocid1.vaultsecret.oc1..test",
					"secretName":           "rotated-secret",
					"currentVersionNumber": 4
				}`), nil
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		result, err := _addSecretVersion(svc, "ocid1.vaultsecret.oc1..test", "new-password")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "rotated-secret", result.Name)
		assert.Equal(t, "new-password", result.Value)
		assert.Equal(t, int64(4), result.Version)
		assert.Equal(t, "ocid1.vaultsecret.oc1..test", result.Ocid)
	})

	t.Run("negative — empty secret value", func(t *testing.T) {
		svc := newTestOciServices(t, nil, nil)
		result, err := _addSecretVersion(svc, "ocid1.vaultsecret.oc1..test", "")
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, errors.Unwrap(err).Error(), "empty")
	})

	t.Run("negative — API returns bad request error", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockServiceError{statusCode: http.StatusBadRequest, code: "InvalidParameter", message: "content too large"}
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		result, err := _addSecretVersion(svc, "ocid1.vaultsecret.oc1..test", "big-value")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — transport error", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("connection reset")
			},
		}
		svc := newTestOciServices(t, dispatcher, nil)

		result, err := _addSecretVersion(svc, "ocid1.vaultsecret.oc1..test", "value")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

// ---------------------------------------------------------------------------
// TestGetSecretVersionFunc (_getSecretVersion via the test seam)
// ---------------------------------------------------------------------------

func TestGetSecretVersionFunc(t *testing.T) {
	encodedValue := base64.StdEncoding.EncodeToString([]byte("version-content"))

	t.Run("positive — latest version (no version arg)", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"secretId":      "ocid1.vaultsecret.oc1..test",
					"versionNumber": 10,
					"secretBundleContent": {
						"contentType": "BASE64",
						"content":     "`+encodedValue+`"
					}
				}`), nil
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := _getSecretVersion(svc, "ocid1.vaultsecret.oc1..test")
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "version-content", result.Value)
		assert.Equal(t, int64(10), result.Version)
		assert.Equal(t, "ocid1.vaultsecret.oc1..test", result.Ocid)
	})

	t.Run("positive — specific version number", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"secretId":      "ocid1.vaultsecret.oc1..test",
					"versionNumber": 3,
					"secretBundleContent": {
						"contentType": "BASE64",
						"content":     "`+encodedValue+`"
					}
				}`), nil
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := _getSecretVersion(svc, "ocid1.vaultsecret.oc1..test", 3)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, int64(3), result.Version)
	})

	t.Run("positive — version 0 treated as latest", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockJSONResponse(http.StatusOK, `{
					"secretId":      "ocid1.vaultsecret.oc1..test",
					"versionNumber": 8,
					"secretBundleContent": {
						"contentType": "BASE64",
						"content":     "`+encodedValue+`"
					}
				}`), nil
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := _getSecretVersion(svc, "ocid1.vaultsecret.oc1..test", 0)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, int64(8), result.Version)
	})

	t.Run("negative — 404 returns nil error (not found)", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "secret bundle not found"}
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := _getSecretVersion(svc, "ocid1.vaultsecret.oc1..gone")
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — non-404 API error", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "access denied"}
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := _getSecretVersion(svc, "ocid1.vaultsecret.oc1..test")
		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("negative — transport error", func(t *testing.T) {
		dispatcher := &mockHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("TLS handshake timeout")
			},
		}
		svc := newTestOciServices(t, nil, dispatcher)

		result, err := _getSecretVersion(svc, "ocid1.vaultsecret.oc1..test")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}
