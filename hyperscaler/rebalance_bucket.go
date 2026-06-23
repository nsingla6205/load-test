package hyperscaler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	"google.golang.org/api/iterator"
)

// RebalanceBucketClient abstracts bucket operations used by poller rebalance flows.
type RebalanceBucketClient interface {
	UploadObject(ctx context.Context, bucketName, objectName string, payload []byte, contentType string) error
	DeleteObject(ctx context.Context, bucketName, objectName string) error
	DeleteObjectsWithPrefix(ctx context.Context, bucketName, objectPrefix string) error
}

var GetRebalanceBucketClient = _getRebalanceBucketClient

func _getRebalanceBucketClient(ctx context.Context) (RebalanceBucketClient, error) {
	gcpService, err := GetGCPService(ctx)
	if err != nil {
		return nil, err
	}
	return &gcpRebalanceBucketClient{gcpService: gcpService}, nil
}

type gcpRebalanceBucketClient struct {
	gcpService *google.GcpServices
}

// rebalanceBucketStorageClientFn is overridable in tests to avoid real GCS clients.
var rebalanceBucketStorageClientFn = (*gcpRebalanceBucketClient).storageClient

func (c *gcpRebalanceBucketClient) storageClient(ctx context.Context) (*storage.Client, error) {
	if c == nil || c.gcpService == nil {
		return nil, errors.New("gcp rebalance bucket client is not initialized")
	}
	if c.gcpService.Ctx == nil {
		c.gcpService.Ctx = ctx
	}
	var adminService *google.AdminGCPService
	if c.gcpService.AdminGCPService != nil {
		adminService = c.gcpService.AdminGCPService
	}
	storageClient, err := adminService.StorageService(ctx, c.gcpService)
	if err != nil {
		return nil, err
	}
	return storageClient, nil
}

func (c *gcpRebalanceBucketClient) UploadObject(ctx context.Context, bucketName, objectName string, payload []byte, contentType string) error {
	storageClient, err := rebalanceBucketStorageClientFn(c, ctx)
	if err != nil {
		return err
	}
	writer := storageClient.Bucket(strings.TrimSpace(bucketName)).Object(strings.TrimSpace(objectName)).NewWriter(ctx)
	writer.ContentType = contentType
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

func (c *gcpRebalanceBucketClient) DeleteObject(ctx context.Context, bucketName, objectName string) error {
	storageClient, err := rebalanceBucketStorageClientFn(c, ctx)
	if err != nil {
		return err
	}
	err = storageClient.Bucket(strings.TrimSpace(bucketName)).Object(strings.TrimSpace(objectName)).Delete(ctx)
	if err == nil || errors.Is(err, storage.ErrObjectNotExist) {
		return nil
	}
	return err
}

func (c *gcpRebalanceBucketClient) DeleteObjectsWithPrefix(ctx context.Context, bucketName, objectPrefix string) error {
	storageClient, err := rebalanceBucketStorageClientFn(c, ctx)
	if err != nil {
		return err
	}
	query := &storage.Query{Prefix: strings.TrimSpace(objectPrefix)}
	it := storageClient.Bucket(strings.TrimSpace(bucketName)).Objects(ctx, query)
	for {
		attrs, nextErr := it.Next()
		if errors.Is(nextErr, iterator.Done) {
			return nil
		}
		if nextErr != nil {
			return fmt.Errorf("list bucket objects with prefix %q: %w", objectPrefix, nextErr)
		}
		if attrs == nil || strings.TrimSpace(attrs.Name) == "" {
			continue
		}
		if delErr := storageClient.Bucket(strings.TrimSpace(bucketName)).Object(attrs.Name).Delete(ctx); delErr != nil && !errors.Is(delErr, storage.ErrObjectNotExist) {
			return fmt.Errorf("delete bucket object %q: %w", attrs.Name, delErr)
		}
	}
}
