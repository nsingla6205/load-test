package hyperscaler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestSetCloudResolver_replacesBoundResolver(t *testing.T) {
	restoreRuntimeTestState(t)

	want := UnsupportedServices{}
	SetCloudResolver(func(ctx context.Context) (Services, error) {
		return want, nil
	})

	useProviderDI = true

	svc, err := resolveCloudService(context.Background())
	require.NoError(t, err)

	got, ok := svc.(UnsupportedServices)
	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestResolveCloudService_flagOff_usesLegacyGetGCPService(t *testing.T) {
	restoreRuntimeTestState(t)

	useProviderDI = false

	want := &google.GcpServices{Ctx: context.Background()}
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return want, nil
	}

	svc, err := ResolveCloudService(context.Background())
	require.NoError(t, err)
	assert.Same(t, want, svc)
}

func TestResolveCloudService_flagOff_propagatesGetGCPServiceError(t *testing.T) {
	restoreRuntimeTestState(t)

	useProviderDI = false

	wantErr := errors.New("legacy gcp init failed")
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return nil, wantErr
	}

	svc, err := ResolveCloudService(context.Background())
	require.Error(t, err)
	assert.Nil(t, svc)
	assert.ErrorIs(t, err, wantErr)
}

func TestResolveCloudService_flagOn_usesBoundResolver(t *testing.T) {
	restoreRuntimeTestState(t)

	useProviderDI = true

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

func TestResolveCloudService_flagOn_propagatesResolverError(t *testing.T) {
	restoreRuntimeTestState(t)

	useProviderDI = true

	wantErr := errors.New("resolver failed")
	SetCloudResolver(func(ctx context.Context) (Services, error) {
		return nil, wantErr
	})

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
	originalUseProviderDI := useProviderDI

	t.Cleanup(func() {
		GetGCPService = originalGetGCPService
		useProviderDI = originalUseProviderDI
		SetCloudResolver(NewCloudServiceResolver("gcp"))
	})
}

func TestResolveCloudService_flagOff_OCI_returnsUnsupportedServices(t *testing.T) {
	restoreRuntimeTestState(t)

	useProviderDI = false

	origHyperscaler := env.Hyperscaler
	t.Cleanup(func() { env.Hyperscaler = origHyperscaler })
	env.Hyperscaler = "oci"

	called := false
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		called = true
		return &google.GcpServices{Ctx: ctx}, nil
	}

	svc, err := ResolveCloudService(context.Background())
	require.NoError(t, err)

	_, ok := svc.(UnsupportedServices)
	assert.True(t, ok, "OCI legacy path should return UnsupportedServices")
	assert.False(t, called, "OCI legacy path must not call GetGCPService")
}
func TestResolveCloudService_flagOff_unknownKind_returnsUnsupportedServices(t *testing.T) {
	restoreRuntimeTestState(t)

	useProviderDI = false

	origHyperscaler := env.Hyperscaler
	t.Cleanup(func() { env.Hyperscaler = origHyperscaler })
	env.Hyperscaler = "azure"

	called := false
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		called = true
		return &google.GcpServices{Ctx: ctx}, nil
	}

	svc, err := ResolveCloudService(context.Background())
	require.NoError(t, err)

	_, ok := svc.(UnsupportedServices)
	assert.True(t, ok)
	assert.False(t, called)
}
