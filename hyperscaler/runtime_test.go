package hyperscaler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
)

func TestSetCloudResolver_replacesBoundResolver(t *testing.T) {
	restoreRuntimeTestState(t)

	want := UnsupportedServices{}
	SetCloudResolver(func(ctx context.Context) (Services, error) {
		return want, nil
	})

	svc, err := resolveCloudService(context.Background())
	require.NoError(t, err)

	got, ok := svc.(UnsupportedServices)
	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestResolveCloudService_usesBoundResolver(t *testing.T) {
	restoreRuntimeTestState(t)

	want := UnsupportedServices{}
	SetCloudResolver(func(ctx context.Context) (Services, error) {
		return want, nil
	})

	svc, err := ResolveCloudService(context.Background())
	require.NoError(t, err)

	got, ok := svc.(UnsupportedServices)
	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestResolveCloudService_propagatesResolverError(t *testing.T) {
	restoreRuntimeTestState(t)

	wantErr := errors.New("resolver failed")
	SetCloudResolver(func(ctx context.Context) (Services, error) {
		return nil, wantErr
	})

	svc, err := ResolveCloudService(context.Background())
	require.Error(t, err)
	assert.Nil(t, svc)
	assert.ErrorIs(t, err, wantErr)
}

func TestResolveCloudService_GCP_usesGetGCPService(t *testing.T) {
	restoreRuntimeTestState(t)

	want := &google.GcpServices{Ctx: context.Background()}
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return want, nil
	}
	SetCloudResolver(NewCloudServiceResolver("gcp"))

	svc, err := ResolveCloudService(context.Background())
	require.NoError(t, err)
	assert.Same(t, want, svc)
}

func TestResolveCloudService_GCP_propagatesGetGCPServiceError(t *testing.T) {
	restoreRuntimeTestState(t)

	wantErr := errors.New("gcp init failed")
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return nil, wantErr
	}
	SetCloudResolver(NewCloudServiceResolver("gcp"))

	svc, err := ResolveCloudService(context.Background())
	require.Error(t, err)
	assert.Nil(t, svc)
	assert.ErrorIs(t, err, wantErr)
}

func TestResolveCloudService_isAssignableForTests(t *testing.T) {
	restoreRuntimeTestState(t)

	original := ResolveCloudService
	defer func() { ResolveCloudService = original }()

	want := UnsupportedServices{}
	ResolveCloudService = func(ctx context.Context) (Services, error) {
		return want, nil
	}

	svc, err := ResolveCloudService(context.Background())
	require.NoError(t, err)
	assert.Equal(t, want, svc)
}

// restoreRuntimeTestState resets package-level runtime state so tests do not
// leak stubs into other tests in this package.
func restoreRuntimeTestState(t *testing.T) {
	t.Helper()

	originalGetGCPService := GetGCPService

	t.Cleanup(func() {
		GetGCPService = originalGetGCPService
		SetCloudResolver(NewCloudServiceResolver("gcp"))
	})
}
