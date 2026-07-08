package expertmodeactivities

import (
	"context"
	"errors"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// RBACUpdateActivity contains activities related to expert mode RBAC workflows.
type RBACUpdateActivity struct {
	SE database.Storage
}

// PoolDetailsWithRbacHash represents pool details with RBAC hash
type PoolDetailsWithRbacHash struct {
	PoolUUID       string
	LatestRbacHash string
	CurrentHash    string
	OntapVersion   string
	NeedUpdate     bool
}

type PoolDetailWithCurrentHash struct {
	PoolUUID    string
	CurrentHash string
}

// PoolRbacUpdateRequest contains all the context needed for RBAC update activities
// Activities can populate and use fields from this struct as needed
type PoolRbacUpdateRequest struct {
	// Input fields
	PoolUUID string

	// Populated by GetPoolByUUID
	Pool *datamodel.Pool

	// Populated by PoolActivity.GetOnTapCredentials
	OntapCredentials *vlm.OntapCredentials

	// Populated by PoolActivity.GetExpertModeCredentials
	ExpertModeCredentials *vlm.OntapCredentials

	// Populated by ParseVlmConfig
	VLMConfig *vlm.VLMConfig

	// Input/Output - initialized with LatestRbacHash, updated after VSA call
	BucketFileDetails *hyperscalermodels.BucketFileDetails
}

// ListActiveExpertModePools fetches pools that are active and running in ONTAP mode.
func (a *RBACUpdateActivity) ListActiveExpertModePools(ctx context.Context) ([]*datamodel.Pool, error) {
	if a.SE == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(errors.New("storage is not configured for RBAC activity"))
	}

	pools, err := a.SE.ListExpertModePools(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return pools, nil
}

// extractPoolVersionDetail returns the ONTAP version and a PoolDetailWithCurrentHash
// for a single pool. Returns ("", nil) if the pool has no ONTAP version in BuildInfo.
func extractPoolVersionDetail(pool *datamodel.Pool) (string, *PoolDetailWithCurrentHash) {
	if pool.BuildInfo == nil || pool.BuildInfo.OntapVersion == "" {
		return "", nil
	}
	return pool.BuildInfo.OntapVersion, &PoolDetailWithCurrentHash{
		PoolUUID:    pool.UUID,
		CurrentHash: pool.BuildInfo.RbacFileHash,
	}
}

// GetPoolsDetailsByOntapVersion groups pools by their ONTAP version and returns a map
// where the key is the ONTAP version and the value is a list of PoolDetailsWithRbacHash
// containing pool UUID and RBAC hash for that version.
func (a *RBACUpdateActivity) GetPoolsDetailsByOntapVersion(ctx context.Context, pools []*datamodel.Pool) (map[string][]PoolDetailWithCurrentHash, error) {
	logger := util.GetLogger(ctx)
	poolsByVersion := make(map[string][]PoolDetailWithCurrentHash)
	if len(pools) == 0 {
		logger.Info("No active expert mode pools found")
		return poolsByVersion, nil
	}
	if a.SE == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(errors.New("storage is not configured for RBAC activity"))
	}

	for _, pool := range pools {
		version, detail := extractPoolVersionDetail(pool)
		if detail == nil {
			logger.Warnf("Skipping pool - ONTAP version not found in BuildInfo poolUUID :%s", pool.UUID)
			continue
		}
		poolsByVersion[version] = append(poolsByVersion[version], *detail)
	}

	logger.Infof("Grouped pools by ONTAP version with RBAC hash totalPools :%v", len(pools))
	return poolsByVersion, nil
}

func (j *RBACUpdateActivity) GetLatestRbacHashForAllOntapVersion(ctx context.Context, poolDetails map[string][]PoolDetailWithCurrentHash) ([]PoolDetailsWithRbacHash, error) {
	var result []PoolDetailsWithRbacHash
	for ontapVersion, poolDetail := range poolDetails {
		rbacFileurl := utils.GenerateRbacFilePath(activities.ExpertModeRbacFilePath, ontapVersion)
		cloudService, err := hyperscaler.ResolveCloudService(ctx)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err))
		}
		bucketFileDetails, err := activities.GetBucketFile(cloudService, ctx, activities.ExpertModeRbacBucketName, rbacFileurl)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if bucketFileDetails != nil {
			for i := range poolDetail {
				tempResult := PoolDetailsWithRbacHash{}
				if poolDetail[i].CurrentHash != bucketFileDetails.FileHashSHA256 {
					tempResult.NeedUpdate = true
					tempResult.CurrentHash = poolDetail[i].CurrentHash
					tempResult.PoolUUID = poolDetail[i].PoolUUID
					tempResult.LatestRbacHash = bucketFileDetails.FileHashSHA256
					tempResult.OntapVersion = ontapVersion
					result = append(result, tempResult)
				}
			}
		} else {
			return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("rbac file details not found for ontap version: %s", ontapVersion))
		}
	}
	return result, nil
}

// GetSinglePoolVersionDetails extracts the ONTAP version and current RBAC hash for a
// single pool, returning the same map structure as GetPoolsDetailsByOntapVersion so
// downstream activities (GetLatestRbacHashForAllOntapVersion) can be reused as-is.
func (a *RBACUpdateActivity) GetSinglePoolVersionDetails(ctx context.Context, pool *datamodel.Pool) (map[string][]PoolDetailWithCurrentHash, error) {
	version, detail := extractPoolVersionDetail(pool)
	if detail == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, fmt.Errorf("pool %s missing ONTAP version", pool.UUID)))
	}

	return map[string][]PoolDetailWithCurrentHash{
		version: {*detail},
	}, nil
}

// GetPoolByUUID fetches the pool by UUID
// Returns the pool so the workflow can update the context
func (j *RBACUpdateActivity) GetPoolByUUID(ctx context.Context, poolUUID string) (*datamodel.Pool, error) {
	if j.SE == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(errors.New("storage is not configured for RBAC activity"))
	}

	pool, err := j.SE.GetPoolByUUID(ctx, poolUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return pool, nil
}
