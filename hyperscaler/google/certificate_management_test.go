package google

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/privateca/v1"
)

func issuedLeafCertificate(name string, notBefore, notAfter time.Time) *privateca.Certificate {
	return &privateca.Certificate{
		Name:       name,
		CreateTime: notBefore.UTC().Format(time.RFC3339Nano),
		Lifetime:   fmt.Sprintf("%ds", int(notAfter.Sub(notBefore)/time.Second)),
		CertificateDescription: &privateca.CertificateDescription{
			SubjectDescription: &privateca.SubjectDescription{
				NotBeforeTime: notBefore.UTC().Format(time.RFC3339Nano),
				NotAfterTime:  notAfter.UTC().Format(time.RFC3339Nano),
			},
		},
	}
}

func enabledCAWithNotAfter(notAfter string, pems []string) *privateca.CertificateAuthority {
	return &privateca.CertificateAuthority{
		State:             "ENABLED",
		PemCaCertificates: pems,
		CaCertificateDescriptions: []*privateca.CertificateDescription{
			{
				SubjectDescription: &privateca.SubjectDescription{
					NotAfterTime: notAfter,
				},
			},
		},
	}
}

func TestGetCertificateAuthority(t *testing.T) {
	const (
		projectID = "test-project"
		region    = "us-east4"
		poolName  = "test-pool"
		caName    = "test-ca"
	)
	resourceName := fmt.Sprintf(
		"projects/%s/locations/%s/caPools/%s/certificateAuthorities/%s",
		projectID,
		region,
		poolName,
		caName,
	)
	requestPath := "/v1/" + resourceName

	newService := func(t *testing.T, handler http.HandlerFunc) *GcpServices {
		t.Helper()
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		ctx := context.Background()
		service, err := privateca.NewService(
			ctx,
			option.WithHTTPClient(&http.Client{Timeout: time.Second}),
			option.WithEndpoint(server.URL),
		)
		require.NoError(t, err)
		return &GcpServices{
			AdminGCPService: &AdminGCPService{privateCaService: service},
			Ctx:             ctx,
			Logger:          util.GetLogger(ctx),
		}
	}

	t.Run("converts metadata and chain", func(t *testing.T) {
		expiry := "2029-08-25T12:34:56.123456789Z"
		service := newService(t, func(writer http.ResponseWriter, request *http.Request) {
			require.Equal(t, requestPath, request.URL.Path)
			require.Equal(t, http.MethodGet, request.Method)
			ca := enabledCAWithNotAfter(expiry, []string{"root-pem", "intermediate-pem"})
			ca.Name = resourceName
			_ = json.NewEncoder(writer).Encode(ca)
		})

		result, err := service.GetCertificateAuthority(
			context.Background(),
			projectID,
			region,
			poolName,
			caName,
		)

		require.NoError(t, err)
		require.Equal(t, resourceName, result.Name)
		require.Equal(t, "ENABLED", result.State)
		require.Equal(t, []string{"root-pem", "intermediate-pem"}, result.PemCertificateChain)
		require.Equal(t, expiry, result.ExpireTime.Format(time.RFC3339Nano))
	})

	t.Run("wraps API error", func(t *testing.T) {
		service := newService(t, func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, `{"error":{"code":503,"message":"unavailable"}}`, http.StatusServiceUnavailable)
		})

		result, err := service.GetCertificateAuthority(
			context.Background(),
			projectID,
			region,
			poolName,
			caName,
		)

		require.Nil(t, result)
		require.Error(t, err)
	})

	t.Run("uses CA certificate notAfterTime when expireTime is empty", func(t *testing.T) {
		expiry := "2029-08-25T12:34:56Z"
		service := newService(t, func(writer http.ResponseWriter, _ *http.Request) {
			ca := enabledCAWithNotAfter(expiry, []string{"root-pem"})
			ca.Name = resourceName
			ca.ExpireTime = ""
			_ = json.NewEncoder(writer).Encode(ca)
		})

		result, err := service.GetCertificateAuthority(
			context.Background(),
			projectID,
			region,
			poolName,
			caName,
		)

		require.NoError(t, err)
		require.Equal(t, expiry, result.ExpireTime.UTC().Format(time.RFC3339))
	})

	t.Run("rejects invalid expiry", func(t *testing.T) {
		service := newService(t, func(writer http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(writer).Encode(enabledCAWithNotAfter("not-a-time", []string{"root-pem"}))
		})

		result, err := service.GetCertificateAuthority(
			context.Background(),
			projectID,
			region,
			poolName,
			caName,
		)

		require.Nil(t, result)
		customError, ok := err.(*vsaerrors.CustomError)
		require.True(t, ok)
		require.ErrorContains(t, customError.OriginalErr, "notAfterTime")
	})

	t.Run("rejects missing CA certificate notAfterTime", func(t *testing.T) {
		service := newService(t, func(writer http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(writer).Encode(&privateca.CertificateAuthority{
				Name:              resourceName,
				State:             "ENABLED",
				PemCaCertificates: []string{"root-pem"},
			})
		})

		result, err := service.GetCertificateAuthority(
			context.Background(),
			projectID,
			region,
			poolName,
			caName,
		)

		require.Nil(t, result)
		customError, ok := err.(*vsaerrors.CustomError)
		require.True(t, ok)
		require.ErrorContains(t, customError.OriginalErr, "has no CA certificate notAfterTime")
	})

	t.Run("rejects missing PEM chain", func(t *testing.T) {
		service := newService(t, func(writer http.ResponseWriter, _ *http.Request) {
			ca := enabledCAWithNotAfter("2029-08-25T12:34:56Z", nil)
			ca.Name = resourceName
			_ = json.NewEncoder(writer).Encode(ca)
		})

		result, err := service.GetCertificateAuthority(
			context.Background(),
			projectID,
			region,
			poolName,
			caName,
		)

		require.Nil(t, result)
		customError, ok := err.(*vsaerrors.CustomError)
		require.True(t, ok)
		require.ErrorContains(t, customError.OriginalErr, "has no PEM certificates")
	})
}

func Test_GetCertificate(t *testing.T) {
	projectId := "1079058383248"
	region := "us-east4"
	certID := "cert-id"
	pooID := "pool-id"
	t.Run("WhenGetCertificateFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		errString := fmt.Errorf("GetCertificate failed for certificate : %s", certID)
		url := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates/%s/get", projectId, region, pooID, certID)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url && req.Method == http.MethodGet {
				response, _ := json.Marshal(&privateca.Operation{Name: certID, Error: &privateca.Status{Message: errString.Error(), Code: 505}})
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := privateca.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				privateCaService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
		}
		cert, err := gService.GetCertificate(projectId, region, pooID, certID)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else if cert != nil {
			tt.Errorf("Expected nil operation but got: %+v", cert)
		}
	})
	t.Run("WhenGetCertificateFailsRevoked", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		url := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates/%s", projectId, region, pooID, certID)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url && req.Method == http.MethodGet {
				response, _ := json.Marshal(&privateca.Certificate{
					Name: certID,
					RevocationDetails: &privateca.RevocationDetails{
						RevocationState: PrivilegeWithdrawn,
					},
				})
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := privateca.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				privateCaService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
		}
		cert, err := gService.GetCertificate(projectId, region, pooID, certID)
		if err != nil && cert != nil {
			tt.Error("Expected cert to be nil for revoked certificate")
		}
	})
	t.Run("WhenGetCertificateFailsNotFound", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		errString := fmt.Errorf("GetCertificate failed for certificate : %s", certID)
		url := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates/%s/get", projectId, region, pooID, certID)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url && req.Method == http.MethodGet {
				response, _ := json.Marshal(&privateca.Operation{Name: certID, Error: &privateca.Status{Message: errString.Error(), Code: 404}})
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := privateca.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				privateCaService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
		}
		cert, err := gService.GetCertificate(projectId, region, pooID, certID)
		if err != nil && cert != nil {
			tt.Error("Expected an error but got nothing")
		}
	})
	t.Run("WhenGetCertificateSuccess", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		resp := &hyperscaler.CustomCertificate{Name: certID}
		url := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates/%s", projectId, region, pooID, certID)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url && req.Method == http.MethodGet {
				response, _ := json.Marshal(&resp)
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := privateca.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				privateCaService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
		}

		cert, err := gService.GetCertificate(projectId, region, pooID, certID)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if cert == nil || cert.Name != certID {
			tt.Errorf("Unexpected operation: %+v", cert)
		}
	})
}

func Test_CreateCertificate(t *testing.T) {
	projectId := "1079058383248"
	region := "us-east4"
	certID := "cert-id"
	caName := "ca-name"
	pooID := "pool-id"
	t.Run("WhenCreateCertificateFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		errString := fmt.Errorf("CreateCertificate failed for certificate : %s", certID)
		url := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s", projectId, region, pooID)
		caURL := fmt.Sprintf(
			"/v1/projects/%s/locations/%s/caPools/%s/certificateAuthorities/%s",
			projectId,
			region,
			pooID,
			caName,
		)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == caURL && req.Method == http.MethodGet {
				_ = json.NewEncoder(rw).Encode(enabledCAWithNotAfter(
					time.Now().Add(4*365*24*time.Hour).Format(time.RFC3339),
					[]string{"ca-pem"},
				))
				return
			}
			if req.URL.Path == url && req.Method == http.MethodPost {
				response, _ := json.Marshal(&privateca.Operation{Name: certID, Error: &privateca.Status{Message: errString.Error(), Code: 505}})
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := privateca.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				privateCaService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
		}
		certificate := &hyperscaler.CustomCertificate{
			CaName:           caName,
			CaGroupName:      pooID,
			Region:           region,
			CertOwningEntity: projectId,
			PemCsr:           "pem-csr",
		}
		cert, err := gService.CreateCertificate(certificate)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else if cert != nil {
			tt.Errorf("Expected nil operation but got: %+v", cert)
		}
	})
	t.Run("WhenCreateCertificateSuccess", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		url := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates", projectId, region, pooID)
		caURL := fmt.Sprintf(
			"/v1/projects/%s/locations/%s/caPools/%s/certificateAuthorities/%s",
			projectId,
			region,
			pooID,
			caName,
		)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == caURL && req.Method == http.MethodGet {
				_ = json.NewEncoder(rw).Encode(enabledCAWithNotAfter(
					time.Now().Add(4*365*24*time.Hour).Format(time.RFC3339),
					[]string{"ca-pem"},
				))
				return
			}
			if req.URL.Path == url && req.Method == http.MethodPost {
				notBefore := time.Now().UTC()
				notAfter := notBefore.Add(94608000 * time.Second)
				response, _ := json.Marshal(issuedLeafCertificate(certID, notBefore, notAfter))
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := privateca.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				privateCaService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
		}
		certificate := &hyperscaler.CustomCertificate{
			CaName:           caName,
			CaGroupName:      pooID,
			Region:           region,
			CertOwningEntity: projectId,
			CertificateID:    certID,
			PemCsr:           "pem-csr",
		}

		cert, err := gService.CreateCertificate(certificate)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if cert == nil || cert.Name != certID {
			tt.Errorf("Unexpected operation: %+v", cert)
		}
	})
}

func TestCreateCertificate_RejectsUnusableIssuingCABeforeCreate(t *testing.T) {
	const (
		projectID = "test-project"
		region    = "us-east4"
		poolName  = "test-pool"
		caName    = "test-ca"
	)
	t.Setenv("CERTIFICATE_LIFETIME", "24h")
	t.Setenv("MINIMUM_ISSUING_CA_REMAINING_LIFETIME", "48h")
	tests := []struct {
		name       string
		state      string
		expireTime time.Time
		wantError  string
	}{
		{
			name:       "disabled CA",
			state:      "DISABLED",
			expireTime: time.Now().Add(72 * time.Hour),
			wantError:  "expected ENABLED",
		},
		{
			name:       "CA below effective floor",
			state:      "ENABLED",
			expireTime: time.Now().Add(47 * time.Hour),
			wantError:  "below required floor",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			caURL := fmt.Sprintf(
				"/v1/projects/%s/locations/%s/caPools/%s/certificateAuthorities/%s",
				projectID,
				region,
				poolName,
				caName,
			)
			createCalled := false
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch {
				case request.URL.Path == caURL && request.Method == http.MethodGet:
					ca := enabledCAWithNotAfter(test.expireTime.Format(time.RFC3339), []string{"ca-pem"})
					ca.State = test.state
					_ = json.NewEncoder(writer).Encode(ca)
				case request.Method == http.MethodPost:
					createCalled = true
					writer.WriteHeader(http.StatusInternalServerError)
				default:
					writer.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)
			service, err := privateca.NewService(
				ctx,
				option.WithHTTPClient(&http.Client{Timeout: time.Second}),
				option.WithEndpoint(server.URL),
			)
			require.NoError(t, err)
			gcpService := &GcpServices{
				AdminGCPService: &AdminGCPService{privateCaService: service},
				Ctx:             ctx,
				Logger:          util.GetLogger(ctx),
			}

			result, err := gcpService.CreateCertificate(&hyperscaler.CustomCertificate{
				CaName:           caName,
				CaGroupName:      poolName,
				Region:           region,
				CertOwningEntity: projectID,
				CertificateID:    "leaf-cert",
				PemCsr:           "pem-csr",
			})

			require.Nil(t, result)
			customError, ok := err.(*vsaerrors.CustomError)
			require.True(t, ok)
			require.ErrorContains(t, customError.OriginalErr, test.wantError)
			require.False(t, createCalled)
		})
	}
}

func TestParseIssuedCertificateLifetimeSlack(t *testing.T) {
	slack, err := parseIssuedCertificateLifetimeSlack("1m")
	require.NoError(t, err)
	require.Equal(t, time.Minute, slack)

	slack, err = parseIssuedCertificateLifetimeSlack("0s")
	require.NoError(t, err)
	require.Equal(t, time.Duration(0), slack)

	_, err = parseIssuedCertificateLifetimeSlack("not-a-duration")
	require.ErrorContains(t, err, "invalid ISSUED_CERTIFICATE_LIFETIME_SLACK")

	_, err = parseIssuedCertificateLifetimeSlack("-1s")
	require.ErrorContains(t, err, "must not be negative")
}

func TestValidateIssuedCertificateLifetime(t *testing.T) {
	notBefore := time.Date(2026, 8, 19, 16, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		requested string
		issued    *privateca.Certificate
		slack     time.Duration
		wantErr   string
	}{
		{
			name:      "matches requested lifetime",
			requested: "24h",
			issued:    issuedLeafCertificate("leaf", notBefore, notBefore.Add(24*time.Hour)),
			slack:     time.Minute,
		},
		{
			name:      "allows second-level rounding within slack",
			requested: "24h",
			issued:    issuedLeafCertificate("leaf", notBefore, notBefore.Add(24*time.Hour-30*time.Second)),
			slack:     time.Minute,
		},
		{
			name:      "rejects shortfall larger than configured slack",
			requested: "24h",
			issued:    issuedLeafCertificate("leaf", notBefore, notBefore.Add(24*time.Hour-30*time.Second)),
			slack:     5 * time.Second,
			wantErr:   "shorter than requested lifetime",
		},
		{
			name:      "rejects CAS truncation",
			requested: "8760h",
			issued:    issuedLeafCertificate("leaf", notBefore, notBefore.Add(30*24*time.Hour)),
			slack:     time.Minute,
			wantErr:   "shorter than requested lifetime",
		},
		{
			name:      "rejects missing validity window",
			requested: "24h",
			issued:    &privateca.Certificate{Name: "leaf"},
			slack:     time.Minute,
			wantErr:   "no subject validity window",
		},
		{
			name:      "rejects invalid requested lifetime",
			requested: "not-a-duration",
			issued:    issuedLeafCertificate("leaf", notBefore, notBefore.Add(24*time.Hour)),
			slack:     time.Minute,
			wantErr:   "invalid requested CERTIFICATE_LIFETIME",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateIssuedCertificateLifetime(test.requested, test.issued, test.slack)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestCreateCertificate_RejectsTruncatedIssuedLifetime(t *testing.T) {
	const (
		projectID = "test-project"
		region    = "us-east4"
		poolName  = "test-pool"
		caName    = "test-ca"
	)
	t.Setenv("CERTIFICATE_LIFETIME", "8760h")
	t.Setenv("MINIMUM_ISSUING_CA_REMAINING_LIFETIME", "2160h")
	t.Setenv("ISSUED_CERTIFICATE_LIFETIME_SLACK", "1m")

	ctx := context.Background()
	caURL := fmt.Sprintf(
		"/v1/projects/%s/locations/%s/caPools/%s/certificateAuthorities/%s",
		projectID,
		region,
		poolName,
		caName,
	)
	createURL := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates", projectID, region, poolName)
	revokeURL := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates/%s:revoke", projectID, region, poolName, "leaf-cert")
	revokeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == caURL && request.Method == http.MethodGet:
			_ = json.NewEncoder(writer).Encode(enabledCAWithNotAfter(
				time.Now().Add(4*365*24*time.Hour).Format(time.RFC3339),
				[]string{"ca-pem"},
			))
		case request.URL.Path == createURL && request.Method == http.MethodPost:
			notBefore := time.Now().UTC()
			_ = json.NewEncoder(writer).Encode(issuedLeafCertificate(
				"leaf-cert",
				notBefore,
				notBefore.Add(30*24*time.Hour),
			))
		case request.URL.Path == revokeURL && request.Method == http.MethodPost:
			revokeCalls++
			writer.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(writer).Encode(map[string]string{"name": "leaf-cert"})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	service, err := privateca.NewService(
		ctx,
		option.WithHTTPClient(&http.Client{Timeout: time.Second}),
		option.WithEndpoint(server.URL),
	)
	require.NoError(t, err)
	gcpService := &GcpServices{
		AdminGCPService: &AdminGCPService{privateCaService: service},
		Ctx:             ctx,
		Logger:          util.GetLogger(ctx),
	}

	result, err := gcpService.CreateCertificate(&hyperscaler.CustomCertificate{
		CaName:           caName,
		CaGroupName:      poolName,
		Region:           region,
		CertOwningEntity: projectID,
		CertificateID:    "leaf-cert",
		PemCsr:           "pem-csr",
	})

	require.Nil(t, result)
	customError, ok := err.(*vsaerrors.CustomError)
	require.True(t, ok)
	require.ErrorContains(t, customError.OriginalErr, "shorter than requested lifetime")
	require.Equal(t, 1, revokeCalls)
}

func Test_RevokeCertificate(t *testing.T) {
	projectId := "1079058383248"
	region := "us-east4"
	certID := "cert-id"
	pooID := "pool-id"
	t.Run("WhenRevokeCertificateFails", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()
		errString := fmt.Errorf("GetCertificate failed for certificate : %s", certID)
		url := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates/%s:revok", projectId, region, pooID, certID)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url && req.Method == http.MethodPost {
				response, _ := json.Marshal(&privateca.Operation{Name: certID, Error: &privateca.Status{Message: errString.Error(), Code: 505}})
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := privateca.NewService(
			ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				privateCaService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
		}
		certificate := &hyperscaler.CustomCertificate{
			CaGroupName:      pooID,
			Region:           region,
			CertOwningEntity: projectId,
			CertificateID:    certID,
		}
		cert, err := gService.RevokeCertificate(certificate)
		if err == nil {
			tt.Error("Expected an error but got nothing")
		} else if cert != "" {
			tt.Errorf("Expected nil operation but got: %+v", cert)
		}
	})
	t.Run("WhenRevokeCertificateFailsWithCAQuotaLimit", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()

		url := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates/%s:revoke", projectId, region, pooID, certID)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url && req.Method == http.MethodPost {
				rw.Header().Set("Content-Type", "application/json")
				rw.WriteHeader(http.StatusBadRequest)
				_, _ = rw.Write([]byte(`{"error":{"code":400,"message":"Maximum number of unexpired revoked certificates per CA reached. Please try again after revoked certificates expire.","status":"FAILED_PRECONDITION"}}`))
				return
			}
		}))
		defer server.Close()
		svc, err := privateca.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				privateCaService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
		}

		certificate := &hyperscaler.CustomCertificate{
			CaGroupName:      pooID,
			Region:           region,
			CertOwningEntity: projectId,
			CertificateID:    certID,
		}
		resourceName, err := gService.RevokeCertificate(certificate)
		if err != nil {
			tt.Errorf("Expected nil error for CA revocation quota limit, got: %v", err)
		}
		expectedResourceName := fmt.Sprintf("projects/%s/locations/%s/caPools/%s/certificates/%s", projectId, region, pooID, certID)
		if resourceName != expectedResourceName {
			tt.Errorf("Expected resourceName %s but got: %s", expectedResourceName, resourceName)
		}
	})
	t.Run("WhenRevokeCertificateSuccess", func(tt *testing.T) {
		defer testReset(tt)
		ctx := context.Background()

		resp := &hyperscaler.CustomCertificate{Name: certID}
		url := fmt.Sprintf("/v1/projects/%s/locations/%s/caPools/%s/certificates/%s:revoke", projectId, region, pooID, certID)
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if req.URL.Path == url && req.Method == http.MethodPost {
				response, _ := json.Marshal(&resp)
				rw.WriteHeader(http.StatusOK)
				_, _ = rw.Write(response)
				return
			}
		}))
		defer server.Close()
		svc, err := privateca.NewService(ctx, option.WithHTTPClient(&http.Client{Timeout: time.Second}), option.WithEndpoint(server.URL))
		if err != nil {
			t.Errorf("Error getting service up: '%s'", err.Error())
		}

		gService := &GcpServices{
			AdminGCPService: &AdminGCPService{
				privateCaService: svc,
			},
			Ctx:                               ctx,
			Logger:                            util.GetLogger(ctx),
			serviceConsumerManagementEndpoint: serviceConsumerManagementEndpoint,
		}

		certificate := &hyperscaler.CustomCertificate{
			CaGroupName:      pooID,
			Region:           region,
			CertOwningEntity: projectId,
			CertificateID:    certID,
		}
		cert, err := gService.RevokeCertificate(certificate)
		if err != nil {
			tt.Errorf("Unexpected error: %v", err)
		}
		if cert != fmt.Sprintf("projects/%s/locations/%s/caPools/%s/certificates/%s", projectId, region, pooID, certID) {
			tt.Errorf("Unexpected operation: %+v", cert)
		}
	})
}
func Test_googleResourceNotFoundCheck(t *testing.T) {
	// Case: googleapi.Error with 404 code returns nil
	gErr := &googleapi.Error{Code: 404}
	err := googleResourceNotFoundCheck(gErr)
	if err != nil {
		t.Errorf("Expected nil for 404 error, got: %v", err)
	}

	// Case: googleapi.Error with non-404 code returns the error
	gErr = &googleapi.Error{Code: 500}
	err = googleResourceNotFoundCheck(gErr)
	if err == nil {
		t.Errorf("Expected non-nil for non-404 error")
	}

	// Case: non-googleapi.Error returns the error
	otherErr := fmt.Errorf("some error")
	err = googleResourceNotFoundCheck(otherErr)
	if err == nil {
		t.Errorf("Expected non-nil for generic error")
	}
}
