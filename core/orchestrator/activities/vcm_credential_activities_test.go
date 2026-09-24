package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// assertNonRetryable asserts err is an engine-agnostic orchestrator application
// error with the expected retryability. VCM runs on go-workflows, so the
// non-retryable signal must be carried by orchestrator.ApplicationError (not a
// temporal.ApplicationError) for gowfbackend.ToGowfError to honour it.
func assertNonRetryable(t *testing.T, err error, wantNonRetryable bool) {
	t.Helper()
	appErr, ok := orchestrator.AsApplicationError(err)
	require.True(t, ok, "expected an orchestrator.ApplicationError, got %T", err)
	assert.Equal(t, wantNonRetryable, appErr.NonRetryable())
}

// fakeCloudService satisfies hyperscaler.Services by embedding the interface
// (all unused methods are nil and panic only if called) and overriding just the
// secret lookup the activity exercises. This keeps the test independent of the
// large generated google-services mock.
type fakeCloudService struct {
	hyperscaler.Services
	secret     *hmodels.CustomSecret
	secretErr  error
	gotProject string
	gotSecret  string
}

func (f *fakeCloudService) GetSecretWithLatestVersion(projectID, secretID string) (*hmodels.CustomSecret, error) {
	f.gotProject = projectID
	f.gotSecret = secretID
	return f.secret, f.secretErr
}

func TestCloudServiceResolver_DefaultsToPackageResolver(t *testing.T) {
	// With no injected resolver the activity must fall back to the package
	// default (hyperscaler.ResolveCloudService). We only assert the resolver is
	// wired, not invoke it, so no real cloud credentials are needed.
	act := &VCMCredentialActivity{}
	assert.NotNil(t, act.cloudServiceResolver(), "nil ResolveCloudService must fall back to the default resolver")
}

func TestResolveOntapCertificate_Success_WithPassword(t *testing.T) {
	origCert := vsa.GetProvidedCertificate
	t.Cleanup(func() { vsa.GetProvidedCertificate = origCert })

	var gotCertRef, gotKeyRef string
	vsa.GetProvidedCertificate = func(_ context.Context, certRef, keyRef string) (*coremodels.Certificate, error) {
		gotCertRef, gotKeyRef = certRef, keyRef
		return &coremodels.Certificate{
			SignedCertificate:        "signed-cert",
			PrivateKey:               "private-key",
			InterMediateCertificates: []string{"intermediate"},
			CommonName:               "cn.example.com",
		}, nil
	}

	// The admin password is read from Secret Manager via the cloud service.
	fake := &fakeCloudService{
		secret: &hmodels.CustomSecret{SecretVersion: &hmodels.CustomSecretVersion{Value: "AdminPass1"}},
	}
	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) { return fake, nil },
	}
	creds, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{
			CertificateID:          "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1",
			CertPrivateKeySecretID: "projects/p/secrets/key-1",
			SecretID:               "projects/p/secrets/admin-secret",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "signed-cert", creds.Certificate.Certificate)
	assert.Equal(t, "private-key", creds.Certificate.PrivateKey)
	assert.Equal(t, "cn.example.com", creds.Certificate.CommonName)
	assert.Equal(t, []string{"intermediate"}, creds.Certificate.InterMediateCertificate)
	assert.Equal(t, "AdminPass1", creds.AdminPassword)
	assert.Equal(t, "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1", gotCertRef)
	assert.Equal(t, "projects/p/secrets/key-1", gotKeyRef)
	// The admin password secret's project/secret must be parsed and forwarded.
	assert.Equal(t, "p", fake.gotProject)
	assert.Equal(t, "admin-secret", fake.gotSecret)
}

func TestResolveOntapCertificate_Success_NoPassword(t *testing.T) {
	origCert := vsa.GetProvidedCertificate
	t.Cleanup(func() { vsa.GetProvidedCertificate = origCert })
	vsa.GetProvidedCertificate = func(context.Context, string, string) (*coremodels.Certificate, error) {
		return &coremodels.Certificate{SignedCertificate: "signed", PrivateKey: "key"}, nil
	}

	// A missing admin-password secret means no password lookup; the cloud
	// service must not be reached in that case.
	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) {
			t.Fatal("cloud service must not be resolved when no admin-password secret id is present")
			return nil, nil
		},
	}
	creds, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{
			CertificateID:          "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1",
			CertPrivateKeySecretID: "projects/p/secrets/key-1",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "signed", creds.Certificate.Certificate)
	assert.Empty(t, creds.AdminPassword)
}

func TestResolveOntapCertificate_MissingCertOrKeyReference(t *testing.T) {
	act := &VCMCredentialActivity{}
	tests := []struct {
		name string
		req  ResolveOntapCertificateRequest
	}{
		{name: "nil pool credentials", req: ResolveOntapCertificateRequest{}},
		{name: "empty references", req: ResolveOntapCertificateRequest{PoolCredentials: &datamodel.PoolCredentials{}}},
		{name: "missing private key ref", req: ResolveOntapCertificateRequest{PoolCredentials: &datamodel.PoolCredentials{CertificateID: "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1"}}},
		{name: "missing cert ref", req: ResolveOntapCertificateRequest{PoolCredentials: &datamodel.PoolCredentials{CertPrivateKeySecretID: "projects/p/secrets/key-1"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := act.ResolveOntapCertificate(context.Background(), tc.req)
			require.Error(t, err)
			// Missing input can never succeed on retry, so it must be non-retryable.
			assertNonRetryable(t, err, true)
		})
	}
}

func TestResolveOntapCertificate_CertFetchError(t *testing.T) {
	origCert := vsa.GetProvidedCertificate
	t.Cleanup(func() { vsa.GetProvidedCertificate = origCert })
	vsa.GetProvidedCertificate = func(context.Context, string, string) (*coremodels.Certificate, error) {
		return nil, errors.New("secret manager unavailable")
	}

	act := &VCMCredentialActivity{}
	_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{
			CertificateID:          "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1",
			CertPrivateKeySecretID: "projects/p/secrets/key-1",
		},
	})

	require.Error(t, err)
	// A transient fetch failure must remain retryable.
	assertNonRetryable(t, err, false)
}

func TestResolveOntapCertificate_IncompleteCertMaterial(t *testing.T) {
	origCert := vsa.GetProvidedCertificate
	t.Cleanup(func() { vsa.GetProvidedCertificate = origCert })

	tests := []struct {
		name string
		cert *coremodels.Certificate
	}{
		{name: "nil cert", cert: nil},
		{name: "missing signed cert", cert: &coremodels.Certificate{PrivateKey: "key"}},
		{name: "missing private key", cert: &coremodels.Certificate{SignedCertificate: "signed"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vsa.GetProvidedCertificate = func(context.Context, string, string) (*coremodels.Certificate, error) {
				return tc.cert, nil
			}
			act := &VCMCredentialActivity{}
			_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
				PoolCredentials: &datamodel.PoolCredentials{
					CertificateID:          "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1",
					CertPrivateKeySecretID: "projects/p/secrets/key-1",
				},
			})
			require.Error(t, err)
			// Incomplete material won't complete on retry, so non-retryable.
			assertNonRetryable(t, err, true)
		})
	}
}

func TestResolveOntapCertificate_PasswordFetchError(t *testing.T) {
	origCert := vsa.GetProvidedCertificate
	t.Cleanup(func() { vsa.GetProvidedCertificate = origCert })
	vsa.GetProvidedCertificate = func(context.Context, string, string) (*coremodels.Certificate, error) {
		return &coremodels.Certificate{SignedCertificate: "signed", PrivateKey: "key"}, nil
	}
	fake := &fakeCloudService{secretErr: errors.New("permission denied on password secret")}

	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) { return fake, nil },
	}
	_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{
			CertificateID:          "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1",
			CertPrivateKeySecretID: "projects/p/secrets/key-1",
			SecretID:               "projects/p/secrets/admin-secret",
		},
	})

	require.Error(t, err)
	// A transient password fetch failure must remain retryable.
	assertNonRetryable(t, err, false)
}

// stubProvidedCertificate makes the cert lookup succeed so the admin-password
// leg of ResolveOntapCertificate is the only thing under test.
func stubProvidedCertificate(t *testing.T) {
	t.Helper()
	orig := vsa.GetProvidedCertificate
	t.Cleanup(func() { vsa.GetProvidedCertificate = orig })
	vsa.GetProvidedCertificate = func(context.Context, string, string) (*coremodels.Certificate, error) {
		return &coremodels.Certificate{SignedCertificate: "signed", PrivateKey: "key"}, nil
	}
}

func TestResolveOntapCertificate_InvalidPasswordSecretResourceName(t *testing.T) {
	stubProvidedCertificate(t)

	// The cloud service must never be reached when the secret resource name
	// cannot be parsed.
	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) {
			t.Fatal("cloud service should not be resolved for an invalid secret resource name")
			return nil, nil
		},
	}
	_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{
			CertificateID:          "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1",
			CertPrivateKeySecretID: "projects/p/secrets/key-1",
			SecretID:               "not-a-valid-secret-name",
		},
	})

	require.Error(t, err)
	// A malformed reference can never succeed on retry.
	assertNonRetryable(t, err, true)
}

func TestResolveOntapCertificate_PasswordCloudResolveError(t *testing.T) {
	stubProvidedCertificate(t)

	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) {
			return nil, errors.New("no credentials")
		},
	}
	_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{
			CertificateID:          "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1",
			CertPrivateKeySecretID: "projects/p/secrets/key-1",
			SecretID:               "projects/p/secrets/admin-secret",
		},
	})

	// Resolver failures are environmental; they propagate unchanged and stay
	// retryable (WrapAsAppError leaves a plain error alone when retryable).
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no credentials")
}

func TestResolveOntapCertificate_EmptyPasswordSecretValue(t *testing.T) {
	stubProvidedCertificate(t)

	tests := []struct {
		name   string
		secret *hmodels.CustomSecret
	}{
		{name: "nil secret", secret: nil},
		{name: "nil version", secret: &hmodels.CustomSecret{}},
		{name: "empty value", secret: &hmodels.CustomSecret{SecretVersion: &hmodels.CustomSecretVersion{Value: ""}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeCloudService{secret: tc.secret}
			act := &VCMCredentialActivity{
				ResolveCloudService: func(context.Context) (hyperscaler.Services, error) { return fake, nil },
			}

			_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
				PoolCredentials: &datamodel.PoolCredentials{
					CertificateID:          "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1",
					CertPrivateKeySecretID: "projects/p/secrets/key-1",
					SecretID:               "projects/p/secrets/admin-secret",
				},
			})

			require.Error(t, err)
			// An absent secret version will not appear on retry.
			assertNonRetryable(t, err, true)
		})
	}
}

func TestResolveOntapAdminPassword_Success(t *testing.T) {
	fake := &fakeCloudService{
		secret: &hmodels.CustomSecret{SecretVersion: &hmodels.CustomSecretVersion{Value: "S3cr3tPw1"}},
	}
	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) { return fake, nil },
	}

	creds, err := act.ResolveOntapAdminPassword(context.Background(), ResolveOntapAdminPasswordRequest{
		SecretResourceName: "projects/237071556299/secrets/ontap-admin-password",
	})

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "S3cr3tPw1", creds.AdminPassword)
	// The bare project/secret identifiers must be passed through unmodified.
	assert.Equal(t, "237071556299", fake.gotProject)
	assert.Equal(t, "ontap-admin-password", fake.gotSecret)
}

func TestResolveOntapAdminPassword_InvalidPasswordComplexity(t *testing.T) {
	fake := &fakeCloudService{
		secret: &hmodels.CustomSecret{SecretVersion: &hmodels.CustomSecretVersion{Value: "weak"}},
	}
	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) { return fake, nil },
	}

	_, err := act.ResolveOntapAdminPassword(context.Background(), ResolveOntapAdminPasswordRequest{
		SecretResourceName: "projects/p/secrets/s",
	})

	require.Error(t, err)
	assertNonRetryable(t, err, true)
}

func TestResolveOntapCertificate_InvalidPasswordComplexity(t *testing.T) {
	origCert := vsa.GetProvidedCertificate
	t.Cleanup(func() { vsa.GetProvidedCertificate = origCert })
	vsa.GetProvidedCertificate = func(context.Context, string, string) (*coremodels.Certificate, error) {
		return &coremodels.Certificate{SignedCertificate: "signed", PrivateKey: "key"}, nil
	}
	fake := &fakeCloudService{
		secret: &hmodels.CustomSecret{SecretVersion: &hmodels.CustomSecretVersion{Value: "invalid"}},
	}

	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) { return fake, nil },
	}
	_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{
			CertificateID:          "projects/p/locations/us-central1/caPools/pool-1/certificates/cert-1",
			CertPrivateKeySecretID: "projects/p/secrets/key-1",
			SecretID:               "projects/p/secrets/admin-secret",
		},
	})

	require.Error(t, err)
	assertNonRetryable(t, err, true)
}

func TestResolveOntapAdminPassword_InvalidResourceName(t *testing.T) {
	// The cloud service must never be reached when the resource name is invalid.
	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) {
			t.Fatal("cloud service should not be resolved for an invalid resource name")
			return nil, nil
		},
	}

	_, err := act.ResolveOntapAdminPassword(context.Background(), ResolveOntapAdminPasswordRequest{
		SecretResourceName: "not-a-valid-name",
	})

	require.Error(t, err)
	// Invalid input can never succeed on retry, so it must be non-retryable.
	assertNonRetryable(t, err, true)
}

func TestResolveOntapAdminPassword_CloudResolveError(t *testing.T) {
	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) {
			return nil, errors.New("no credentials")
		},
	}

	_, err := act.ResolveOntapAdminPassword(context.Background(), ResolveOntapAdminPasswordRequest{
		SecretResourceName: "projects/p/secrets/s",
	})

	require.Error(t, err)
}

func TestResolveOntapAdminPassword_GetSecretError(t *testing.T) {
	fake := &fakeCloudService{secretErr: errors.New("permission denied")}
	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) { return fake, nil },
	}

	_, err := act.ResolveOntapAdminPassword(context.Background(), ResolveOntapAdminPasswordRequest{
		SecretResourceName: "projects/p/secrets/s",
	})

	require.Error(t, err)
	// A Secret Manager fetch error is transient, so it must remain retryable.
	assertNonRetryable(t, err, false)
}

func TestResolveOntapAdminPassword_EmptySecretValue(t *testing.T) {
	tests := []struct {
		name   string
		secret *hmodels.CustomSecret
	}{
		{name: "nil secret", secret: nil},
		{name: "nil version", secret: &hmodels.CustomSecret{}},
		{name: "empty value", secret: &hmodels.CustomSecret{SecretVersion: &hmodels.CustomSecretVersion{Value: ""}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeCloudService{secret: tc.secret}
			act := &VCMCredentialActivity{
				ResolveCloudService: func(context.Context) (hyperscaler.Services, error) { return fake, nil },
			}

			_, err := act.ResolveOntapAdminPassword(context.Background(), ResolveOntapAdminPasswordRequest{
				SecretResourceName: "projects/p/secrets/s",
			})

			// The empty/absent secret value must surface as an error; the exact
			// wrapped vsaerror text is an implementation detail.
			require.Error(t, err)
			// An empty/absent secret will not appear on retry, so non-retryable.
			assertNonRetryable(t, err, true)
		})
	}
}
