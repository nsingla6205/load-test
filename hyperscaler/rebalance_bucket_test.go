package hyperscaler

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	"google.golang.org/api/option"
)

type errRoundTripper struct{ err error }

func (e errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, e.err
}

func testRebalanceStorageClient(t *testing.T) *storage.Client {
	t.Helper()
	client, err := storage.NewClient(
		context.Background(),
		option.WithoutAuthentication(),
		option.WithHTTPClient(&http.Client{Transport: errRoundTripper{err: errors.New("transport failed")}}),
	)
	require.NoError(t, err)
	return client
}

func TestHarvestPollerIOTimeout_DefaultAndEnv(t *testing.T) {
	assert.Equal(t, 30*time.Second, HarvestPollerIOTimeout(), "default timeout should be 30s when env unset")

	t.Setenv("HARVEST_POLLER_IO_TIMEOUT_SECONDS", "5")
	assert.Equal(t, 5*time.Second, HarvestPollerIOTimeout())

	t.Setenv("HARVEST_POLLER_IO_TIMEOUT_SECONDS", "0")
	assert.Equal(t, 30*time.Second, HarvestPollerIOTimeout(), "non-positive env value should fall back to default")
}

func TestGetRebalanceBucketClient_GCPServiceError(t *testing.T) {
	old := GetGCPService
	defer func() { GetGCPService = old }()
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return nil, errors.New("gcp init failed")
	}

	_, err := _getRebalanceBucketClient(context.Background())
	require.Error(t, err)
}

func TestGetRebalanceBucketClient_Success(t *testing.T) {
	old := GetGCPService
	defer func() { GetGCPService = old }()
	GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
		return &google.GcpServices{Ctx: ctx}, nil
	}

	client, err := _getRebalanceBucketClient(context.Background())
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestGcpRebalanceBucketClient_storageClient_nilReceiver(t *testing.T) {
	var c *gcpRebalanceBucketClient
	_, err := c.storageClient(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestGcpRebalanceBucketClient_storageClient_nilGcpService(t *testing.T) {
	c := &gcpRebalanceBucketClient{}
	_, err := c.storageClient(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestGcpRebalanceBucketClient_UploadObject_storageClientError(t *testing.T) {
	old := rebalanceBucketStorageClientFn
	defer func() { rebalanceBucketStorageClientFn = old }()
	rebalanceBucketStorageClientFn = func(_ *gcpRebalanceBucketClient, _ context.Context) (*storage.Client, error) {
		return nil, errors.New("storage unavailable")
	}

	err := (&gcpRebalanceBucketClient{gcpService: &google.GcpServices{}}).UploadObject(
		context.Background(), "bucket", "obj.yaml", []byte("yaml"), "application/x-yaml",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage unavailable")
}

func TestGcpRebalanceBucketClient_DeleteObject_storageClientError(t *testing.T) {
	old := rebalanceBucketStorageClientFn
	defer func() { rebalanceBucketStorageClientFn = old }()
	rebalanceBucketStorageClientFn = func(_ *gcpRebalanceBucketClient, _ context.Context) (*storage.Client, error) {
		return nil, errors.New("storage unavailable")
	}

	err := (&gcpRebalanceBucketClient{gcpService: &google.GcpServices{}}).DeleteObject(
		context.Background(), "bucket", "obj.yaml",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage unavailable")
}

func TestGcpRebalanceBucketClient_DeleteObjectsWithPrefix_storageClientError(t *testing.T) {
	old := rebalanceBucketStorageClientFn
	defer func() { rebalanceBucketStorageClientFn = old }()
	rebalanceBucketStorageClientFn = func(_ *gcpRebalanceBucketClient, _ context.Context) (*storage.Client, error) {
		return nil, errors.New("storage unavailable")
	}

	err := (&gcpRebalanceBucketClient{gcpService: &google.GcpServices{}}).DeleteObjectsWithPrefix(
		context.Background(), "bucket", "lease-a/",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage unavailable")
}

func TestGcpRebalanceBucketClient_UploadObject_writeFails(t *testing.T) {
	old := rebalanceBucketStorageClientFn
	defer func() { rebalanceBucketStorageClientFn = old }()
	rebalanceBucketStorageClientFn = func(_ *gcpRebalanceBucketClient, _ context.Context) (*storage.Client, error) {
		return testRebalanceStorageClient(t), nil
	}

	err := (&gcpRebalanceBucketClient{gcpService: &google.GcpServices{}}).UploadObject(
		context.Background(), "bucket", "obj.yaml", []byte("yaml"), "application/x-yaml",
	)
	require.Error(t, err)
}

func TestGcpRebalanceBucketClient_DeleteObject_unreachableEndpoint(t *testing.T) {
	old := rebalanceBucketStorageClientFn
	defer func() { rebalanceBucketStorageClientFn = old }()
	rebalanceBucketStorageClientFn = func(_ *gcpRebalanceBucketClient, _ context.Context) (*storage.Client, error) {
		return testRebalanceStorageClient(t), nil
	}

	err := (&gcpRebalanceBucketClient{gcpService: &google.GcpServices{}}).DeleteObject(
		context.Background(), "bucket", "missing.yaml",
	)
	require.Error(t, err)
}

func TestGcpRebalanceBucketClient_DeleteObjectsWithPrefix_listFails(t *testing.T) {
	old := rebalanceBucketStorageClientFn
	defer func() { rebalanceBucketStorageClientFn = old }()
	rebalanceBucketStorageClientFn = func(_ *gcpRebalanceBucketClient, _ context.Context) (*storage.Client, error) {
		return testRebalanceStorageClient(t), nil
	}

	err := (&gcpRebalanceBucketClient{gcpService: &google.GcpServices{}}).DeleteObjectsWithPrefix(
		context.Background(), "bucket", "lease-a/",
	)
	require.Error(t, err)
}
