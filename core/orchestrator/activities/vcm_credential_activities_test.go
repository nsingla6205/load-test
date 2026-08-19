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

func TestParseSecretResourceName(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantProject string
		wantSecret  string
		wantErr     bool
	}{
		{
			name:        "bare secret form",
			in:          "projects/237071556299/secrets/ontap-admin-password",
			wantProject: "237071556299",
			wantSecret:  "ontap-admin-password",
		},
		{
			name:        "versioned form ignores version",
			in:          "projects/proj-1/secrets/sec-1/versions/5",
			wantProject: "proj-1",
			wantSecret:  "sec-1",
		},
		{
			name:        "leading and trailing slashes trimmed",
			in:          "/projects/proj-1/secrets/sec-1/",
			wantProject: "proj-1",
			wantSecret:  "sec-1",
		},
		{name: "empty", in: "", wantErr: true},
		{name: "wrong prefix", in: "foo/proj/secrets/sec", wantErr: true},
		{name: "missing secrets segment", in: "projects/proj/bar/sec", wantErr: true},
		{name: "empty project", in: "projects//secrets/sec", wantErr: true},
		{name: "empty secret", in: "projects/proj/secrets/", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project, secret, err := parseSecretResourceName(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantProject, project)
			assert.Equal(t, tc.wantSecret, secret)
		})
	}
}

func TestCloudServiceResolver_DefaultsToPackageResolver(t *testing.T) {
	// With no injected resolver the activity must fall back to the package
	// default (hyperscaler.ResolveCloudService). We only assert the resolver is
	// wired, not invoke it, so no real cloud credentials are needed.
	act := &VCMCredentialActivity{}
	assert.NotNil(t, act.cloudServiceResolver(), "nil ResolveCloudService must fall back to the default resolver")
}

func TestResolveOntapCertificate_Success_WithPassword(t *testing.T) {
	origCert := vsa.GetCertificateFromCacheOrSecretManager
	origPwd := vsa.GetPasswordFromCacheOrSecretManager
	t.Cleanup(func() {
		vsa.GetCertificateFromCacheOrSecretManager = origCert
		vsa.GetPasswordFromCacheOrSecretManager = origPwd
	})

	var gotCredsPtr *datamodel.PoolCredentials
	vsa.GetCertificateFromCacheOrSecretManager = func(_ context.Context, pc *datamodel.PoolCredentials) (*coremodels.Certificate, error) {
		gotCredsPtr = pc
		return &coremodels.Certificate{
			SignedCertificate:        "signed-cert",
			PrivateKey:               "private-key",
			InterMediateCertificates: []string{"intermediate"},
			CommonName:               "cn.example.com",
		}, nil
	}
	var gotSecretID string
	vsa.GetPasswordFromCacheOrSecretManager = func(_ context.Context, secretID string) (string, error) {
		gotSecretID = secretID
		return "admin-pass", nil
	}

	act := &VCMCredentialActivity{}
	creds, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{
			CertificateID: "cert-1",
			CaURI:         "ca-uri",
			SecretID:      "admin-secret",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "signed-cert", creds.Certificate.Certificate)
	assert.Equal(t, "private-key", creds.Certificate.PrivateKey)
	assert.Equal(t, "cn.example.com", creds.Certificate.CommonName)
	assert.Equal(t, []string{"intermediate"}, creds.Certificate.InterMediateCertificate)
	assert.Equal(t, "admin-pass", creds.AdminPassword)
	assert.Equal(t, "admin-secret", gotSecretID)
	assert.Equal(t, "cert-1", gotCredsPtr.CertificateID)
}

func TestResolveOntapCertificate_Success_NoPassword(t *testing.T) {
	origCert := vsa.GetCertificateFromCacheOrSecretManager
	t.Cleanup(func() { vsa.GetCertificateFromCacheOrSecretManager = origCert })
	vsa.GetCertificateFromCacheOrSecretManager = func(context.Context, *datamodel.PoolCredentials) (*coremodels.Certificate, error) {
		return &coremodels.Certificate{SignedCertificate: "signed", PrivateKey: "key"}, nil
	}
	// A missing admin-password secret means no password lookup; the password
	// resolver must not be reached in that case.
	origPwd := vsa.GetPasswordFromCacheOrSecretManager
	t.Cleanup(func() { vsa.GetPasswordFromCacheOrSecretManager = origPwd })
	vsa.GetPasswordFromCacheOrSecretManager = func(context.Context, string) (string, error) {
		t.Fatal("password resolver must not be called when no admin-password secret id is present")
		return "", nil
	}

	act := &VCMCredentialActivity{}
	creds, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{CertificateID: "cert-1"},
	})

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "signed", creds.Certificate.Certificate)
	assert.Empty(t, creds.AdminPassword)
}

func TestResolveOntapCertificate_MissingCertificateID(t *testing.T) {
	act := &VCMCredentialActivity{}
	tests := []struct {
		name string
		req  ResolveOntapCertificateRequest
	}{
		{name: "nil pool credentials", req: ResolveOntapCertificateRequest{}},
		{name: "empty certificate id", req: ResolveOntapCertificateRequest{PoolCredentials: &datamodel.PoolCredentials{}}},
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
	origCert := vsa.GetCertificateFromCacheOrSecretManager
	t.Cleanup(func() { vsa.GetCertificateFromCacheOrSecretManager = origCert })
	vsa.GetCertificateFromCacheOrSecretManager = func(context.Context, *datamodel.PoolCredentials) (*coremodels.Certificate, error) {
		return nil, errors.New("secret manager unavailable")
	}

	act := &VCMCredentialActivity{}
	_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{CertificateID: "cert-1"},
	})

	require.Error(t, err)
	// A transient fetch failure must remain retryable.
	assertNonRetryable(t, err, false)
}

func TestResolveOntapCertificate_IncompleteCertMaterial(t *testing.T) {
	origCert := vsa.GetCertificateFromCacheOrSecretManager
	t.Cleanup(func() { vsa.GetCertificateFromCacheOrSecretManager = origCert })

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
			vsa.GetCertificateFromCacheOrSecretManager = func(context.Context, *datamodel.PoolCredentials) (*coremodels.Certificate, error) {
				return tc.cert, nil
			}
			act := &VCMCredentialActivity{}
			_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
				PoolCredentials: &datamodel.PoolCredentials{CertificateID: "cert-1"},
			})
			require.Error(t, err)
			// Incomplete material won't complete on retry, so non-retryable.
			assertNonRetryable(t, err, true)
		})
	}
}

func TestResolveOntapCertificate_PasswordFetchError(t *testing.T) {
	origCert := vsa.GetCertificateFromCacheOrSecretManager
	origPwd := vsa.GetPasswordFromCacheOrSecretManager
	t.Cleanup(func() {
		vsa.GetCertificateFromCacheOrSecretManager = origCert
		vsa.GetPasswordFromCacheOrSecretManager = origPwd
	})
	vsa.GetCertificateFromCacheOrSecretManager = func(context.Context, *datamodel.PoolCredentials) (*coremodels.Certificate, error) {
		return &coremodels.Certificate{SignedCertificate: "signed", PrivateKey: "key"}, nil
	}
	vsa.GetPasswordFromCacheOrSecretManager = func(context.Context, string) (string, error) {
		return "", errors.New("permission denied on password secret")
	}

	act := &VCMCredentialActivity{}
	_, err := act.ResolveOntapCertificate(context.Background(), ResolveOntapCertificateRequest{
		PoolCredentials: &datamodel.PoolCredentials{CertificateID: "cert-1", SecretID: "admin-secret"},
	})

	require.Error(t, err)
	// A transient password fetch failure must remain retryable.
	assertNonRetryable(t, err, false)
}

func TestResolveOntapAdminPassword_Success(t *testing.T) {
	fake := &fakeCloudService{
		secret: &hmodels.CustomSecret{SecretVersion: &hmodels.CustomSecretVersion{Value: "s3cr3t"}},
	}
	act := &VCMCredentialActivity{
		ResolveCloudService: func(context.Context) (hyperscaler.Services, error) { return fake, nil },
	}

	creds, err := act.ResolveOntapAdminPassword(context.Background(), ResolveOntapAdminPasswordRequest{
		SecretResourceName: "projects/237071556299/secrets/ontap-admin-password",
	})

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "s3cr3t", creds.AdminPassword)
	// The bare project/secret identifiers must be passed through unmodified.
	assert.Equal(t, "237071556299", fake.gotProject)
	assert.Equal(t, "ontap-admin-password", fake.gotSecret)
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
