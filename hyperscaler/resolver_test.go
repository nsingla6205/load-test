package hyperscaler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

func TestNewCloudServiceResolver_OCI_returnsUnsupportedServices(t *testing.T) {
	resolve := NewCloudServiceResolver(string(HyperscalerOCI))
	require.NotNil(t, resolve)

	svc, err := resolve(context.Background())
	require.NoError(t, err)
	require.NotNil(t, svc)

	_, ok := svc.(UnsupportedServices)
	assert.True(t, ok, "OCI resolver should return UnsupportedServices placeholder")

	_, err = svc.CreateVPC(nil)
	assert.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.Equal(t, vsaerrors.ErrInternalServerError, customErr.TrackingID)
}

func TestNewCloudServiceResolver_GCP_usesGetGCPService(t *testing.T) {
	original := GetGCPService
	defer func() { GetGCPService = original }()

	want := &google.GcpServices{Ctx: context.Background()}
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return want, nil
	}

	resolve := NewCloudServiceResolver(string(HyperscalerGCP))
	svc, err := resolve(context.Background())
	require.NoError(t, err)
	assert.Same(t, want, svc)
}

func TestNewCloudServiceResolver_GCP_propagatesGetGCPServiceError(t *testing.T) {
	original := GetGCPService
	defer func() { GetGCPService = original }()

	wantErr := errors.New("gcp init failed")
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return nil, wantErr
	}

	resolve := NewCloudServiceResolver(string(HyperscalerGCP))
	svc, err := resolve(context.Background())
	require.Error(t, err)
	assert.Nil(t, svc)
	assert.ErrorIs(t, err, wantErr)
}

func TestNewCloudServiceResolver_unknownKind_returnsUnsupportedServices(t *testing.T) {
	original := GetGCPService
	defer func() { GetGCPService = original }()

	called := false
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		called = true
		return &google.GcpServices{Ctx: ctx}, nil
	}

	resolve := NewCloudServiceResolver("azure")
	svc, err := resolve(context.Background())
	require.NoError(t, err)
	require.NotNil(t, svc)

	_, ok := svc.(UnsupportedServices)
	assert.True(t, ok, "unknown kind should return UnsupportedServices placeholder")
	assert.False(t, called, "unknown kind must not call GetGCPService")
}

func TestKind_constants(t *testing.T) {
	assert.Equal(t, Hyperscaler("gcp"), HyperscalerGCP)
	assert.Equal(t, Hyperscaler("oci"), HyperscalerOCI)
}
