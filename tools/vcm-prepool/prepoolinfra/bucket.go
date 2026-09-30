package prepoolinfra

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/names"
)

func (p *provisioner) createAutoTierBucket(ctx context.Context, tenantProject, region, deploymentName string) (string, error) {
	bucketName := names.GenerateBucketName(deploymentName)
	slog.Info("creating auto tier bucket", "bucketName", bucketName)

	err := p.GCPService.CreateBucketIfNotExists(ctx, tenantProject, bucketName, region, nil)
	if err != nil {
		return "", fmt.Errorf("create bucket %s: %w", bucketName, err)
	}

	slog.Info("auto tier bucket created", "bucketName", bucketName)
	return bucketName, nil
}

func (p *provisioner) deleteAutoTierBucket(ctx context.Context, bucketName string) error {
	slog.Info("emptying auto tier bucket", "bucketName", bucketName)
	emptyErr := p.GCPService.EmptyBucket(ctx, bucketName)
	if emptyErr != nil {
		// EmptyBucket does not wrap 404; continue so DeleteBucketWithLifecyclePolicy
		// can treat a missing bucket as already gone.
		slog.Warn("failed to empty auto tier bucket", "bucketName", bucketName, "error", emptyErr)
	} else {
		slog.Info("auto tier bucket emptied", "bucketName", bucketName)
	}

	slog.Info("deleting auto tier bucket", "bucketName", bucketName)
	isDeleted, err := p.GCPService.DeleteBucketWithLifecyclePolicy(ctx, bucketName)
	if err != nil {
		if emptyErr != nil {
			return fmt.Errorf("delete auto tier bucket %s: %w (empty failed: %v)", bucketName, err, emptyErr)
		}
		return fmt.Errorf("delete auto tier bucket %s: %w", bucketName, err)
	}
	if isDeleted {
		slog.Info("auto tier bucket deleted", "bucketName", bucketName)
		return nil
	}

	slog.Warn("auto tier bucket still exists; delete lifecycle policy applied", "bucketName", bucketName)
	if emptyErr != nil {
		return fmt.Errorf("auto tier bucket %s still exists after delete: %w", bucketName, emptyErr)
	}
	return fmt.Errorf("auto tier bucket %s still exists after empty and delete", bucketName)
}
