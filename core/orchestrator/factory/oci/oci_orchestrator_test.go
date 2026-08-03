package oci

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	ociworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

func TestNewOCIOrchestrator(t *testing.T) {
	t.Run("Success_CreatesOrchestrator", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)

		orch := NewOCIOrchestrator(mockStorage, mockTemporal)

		assert.NotNil(tt, orch)
		assert.Equal(tt, mockStorage, orch.storage)
		assert.Equal(tt, mockTemporal, orch.temporal)
	})
}

// TestOCIOrchestrator_UpdatePool documents the orchestrator's contract for
// degenerate UpdatePoolParams. The endpoint layer (oci-proxy/api/endpoints/
// pool_endpoints.go) already rejects an empty PoolOCID with 400 before the
// request reaches this factory, so the orchestrator does NOT re-validate it.
// If a future caller smuggles an empty-everything params struct through, the
// orchestrator must still degrade gracefully — GetPoolByName returns
// ErrRecordNotFound and the factory surfaces a typed NotFoundErr rather than
// panicking on a nil storage or returning a misleading 500.
func TestOCIOrchestrator_UpdatePool(t *testing.T) {
	t.Run("EmptyParamsFlowThroughAsNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetAccount(mock.Anything, mock.Anything).Return(
			&datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)
		orch := &OCIOrchestrator{storage: mockStorage}
		ctx := context.Background()

		result, jobID, err := orch.UpdatePool(ctx, &commonparams.UpdatePoolParams{})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotFoundErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_DescribePool(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.DescribePool(ctx, "pool-id", "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetExpertModePoolCreds(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetExpertModePoolCreds(ctx, "pool-id", "account-name", "user-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetDedicatedClusterPoolCreds(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetDedicatedClusterPoolCreds(ctx, "pool-id", "account-name", "cluster-external-name", "user-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_DeletePool(t *testing.T) {
	t.Run("ReturnsBadRequestWhenPoolOCIDMissing", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeletePoolParams{}

		result, jobID, err := orch.DeletePool(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsBadRequestErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetMultiplePools(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetMultiplePools(ctx, "account-name", []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetPoolByVendorID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetPoolByVendorID(ctx, "vendor-id", "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetPoolByName(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetPoolByName(ctx, "pool-name", "account-name", 1)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_ListPools(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.ListPools(ctx, "account-name", false)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_ListAllPools(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.ListAllPools(ctx)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_CreateHostGroup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateHostGroupParams{}

		result, err := orch.CreateHostGroup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetHostGroup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetHostGroup(ctx, "hostgroup-uuid", "account-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_DeleteHostGroup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.DeleteHostGroup(ctx, "hostgroup-uuid", "account-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateHostGroup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateHostGroupParams{}

		result, jobID, err := orch.UpdateHostGroup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetMultipleHostGroups(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetMultipleHostGroups(ctx, "account-name", []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetHostGroupsByUUIDs(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetHostGroupsByUUIDs(ctx, []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_CreateVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateVolumeParams{}

		result, jobID, err := orch.CreateVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_CreateFlexCacheVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateVolumeParams{}

		result, jobID, err := orch.CreateFlexCacheVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_RevertVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.RevertVolumeParams{}

		result, jobID, err := orch.RevertVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetVolume(ctx, "volume-id", false)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateVolumeParams{}

		result, jobID, err := orch.UpdateVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_UpdateVolumeV2(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateVolumeParams{}

		result, jobID, err := orch.UpdateVolumeV2(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetVolumeCount(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetVolumeCount(ctx, "project-number")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Equal(tt, int64(0), result)
	})
}

func TestOCIOrchestrator_DeleteVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, jobID, err := orch.DeleteVolume(ctx, "volume-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetMultipleVolumes(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetMultipleVolumes(ctx, []string{"id1", "id2"}, "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetVolumesByUUIDs(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetVolumesByUUIDs(ctx, []string{"id1", "id2"}, commonparams.VolumeFetchOptions{})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_ListVolumes(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.ListVolumes(ctx, "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetKmsConfigsByUUIDs(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetKmsConfigsByUUIDs(ctx, []string{"kms-uuid"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_EstablishFlexCacheVolumePeering(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.EstablishVolumePeeringParams{}

		result, jobID, err := orch.EstablishFlexCacheVolumePeering(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_EstablishReplicationPeering(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.EstablishReplicationPeeringParams{}

		result, jobID, err := orch.EstablishReplicationPeering(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_RestoreFilesFromBackup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.RestoreFilesFromBackupParams{}

		result, err := orch.RestoreFilesFromBackup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_SplitCloneVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.SplitStartVolumeParams{}

		result, jobID, err := orch.SplitStartVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetJob(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetJob(ctx, "operation-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetReplicationJobs(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetReplicationJobs(ctx, "project-name", "pool-uuid")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetJobByResourceUUID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetJobByResourceUUID(ctx, "resource-uuid", "job-type")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_CreateJob(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateJobParams{}

		result, err := orch.CreateJob(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateJobStatus(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		err := orch.UpdateJobStatus(ctx, "job-id", "status", 0, "error-details")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_UpdateJobAttributes(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		err := orch.UpdateJobAttributes(ctx, "job-id", nil)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_CreateSnapshot(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateSnapshotParams{}

		result, jobID, err := orch.CreateSnapshot(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetSnapshot(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.GetSnapshotParams{}

		result, err := orch.GetSnapshot(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_DeleteSnapshot(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteSnapshotParams{}

		result, jobID, err := orch.DeleteSnapshot(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_ListSnapshots(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ListSnapshotsParams{}

		result, err := orch.ListSnapshots(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateSnapshot(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateSnapshotParams{}

		result, jobID, err := orch.UpdateSnapshot(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetMultipleSnapshots(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetMultipleSnapshots(ctx, "volume-uuid", "account-name", []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_DeleteSnapmirrorSnapshots(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.SnapshotsInternalDeleteParams{}

		result, err := orch.DeleteSnapmirrorSnapshots(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_CreateQuotaRule(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateQuotaRulesParam{}

		result, jobID, err := orch.CreateQuotaRule(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_CreateQuotaRuleInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateQuotaRulesParam{}

		result, job, err := orch.CreateQuotaRuleInternal(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_UpdateQuotaRule(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateQuotaRulesParam{}

		result, jobID, err := orch.UpdateQuotaRule(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_UpdateQuotaRuleInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateQuotaRulesParam{}

		result, job, err := orch.UpdateQuotaRuleInternal(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_DeleteQuotaRule(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteQuotaRulesParam{}

		result, jobID, err := orch.DeleteQuotaRule(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_DeleteQuotaRuleInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteQuotaRulesParam{}

		result, job, err := orch.DeleteQuotaRuleInternal(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_ListQuotaRules(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ListQuotaRulesParams{}

		result, err := orch.ListQuotaRules(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetMultipleQuotaRules(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetMultipleQuotaRules(ctx, "volume-uuid", "account-name", []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetQuotaRulesByUUIDs(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetQuotaRulesByUUIDs(ctx, []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_DescribeQuotaRule(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.DescribeQuotaRule(ctx, "volume-uuid", "account-name", "quota-rule-uuid")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_CreateVolumeReplicationInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateVolumeReplicationInternalParams{}

		result, job, err := orch.CreateVolumeReplicationInternal(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_GetReplicationCount(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetReplicationCount(ctx, "project-number")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Equal(tt, int64(0), result)
	})
}

func TestOCIOrchestrator_CreateVolumeReplication(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateVolumeReplicationParams{}

		result, jobID, err := orch.CreateVolumeReplication(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_UpdateVolumeReplicationInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateVolumeReplicationInternalParams{}

		result, job, err := orch.UpdateVolumeReplicationInternal(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_UpdateVolumeReplicationAttributes(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := models.UpdateVolumeReplicationAttributesParams{}

		result, err := orch.UpdateVolumeReplicationAttributes(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateVolumeReplicationState(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := models.UpdateVolumeReplicationStateParams{}

		result, err := orch.UpdateVolumeReplicationState(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetMultipleReplicationsInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetMultipleReplicationsInternal(ctx, "account-name", []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetMultipleReplications(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := commonparams.GetMultipleReplicationsParams{}

		result, err := orch.GetMultipleReplications(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetMultipleReplicationsByExternalUUID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := commonparams.GetMultipleReplicationsByExternalUUIDParams{}

		result, err := orch.GetMultipleReplicationsByExternalUUID(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_AcceptClusterPeer(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ClusterPeerParams{}

		result, job, err := orch.AcceptClusterPeer(ctx, params, "pool-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_PerformMountCheck(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.PerformMountCheck(ctx, "replication-uuid", "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_ResumeReplication(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ResumeReplicationParams{}

		result, jobID, err := orch.ResumeReplication(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_UpdateReplication(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateReplicationParams{}

		result, jobID, err := orch.UpdateReplication(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_ResumeReplicationInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, job, err := orch.ResumeReplicationInternal(ctx, "volume-replication-id", "account-name", false)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_ReverseReplicationInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, job, err := orch.ReverseReplicationInternal(ctx, "volume-replication-id", "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_GetReplication(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetReplication(ctx, "volume-replication-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_ReleaseVolumeReplication(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, job, err := orch.ReleaseVolumeReplication(ctx, "replication-uuid")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_DeleteReplicationInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, job, err := orch.DeleteReplicationInternal(ctx, "volume-replication-id", false, false)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_StopReplicationInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, job, err := orch.StopReplicationInternal(ctx, "replication-uuid", "account-name", false)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_StopReplication(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.StopReplicationParams{}

		result, jobID, err := orch.StopReplication(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_DeleteReplication(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteReplicationParams{}

		result, jobID, err := orch.DeleteReplication(ctx, params, "cleanup-job-id", false)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_SyncReplication(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ResumeReplicationParams{}

		result, jobID, err := orch.SyncReplication(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_ReverseAndResumeReplication(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ReverseAndResumeReplicationParams{}

		result, jobID, err := orch.ReverseAndResumeReplication(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, jobID)
	})
}

func TestOCIOrchestrator_CreateKmsConfig(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateKmsConfigParams{}

		result, jobID, err := orch.CreateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetKmsConfig(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.GetKmsConfigParams{}

		result, err := orch.GetKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetKmsConfigByKeyFullPath(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.GetKmsConfigParams{}

		result, err := orch.GetKmsConfigByKeyFullPath(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetMultipleKMSConfigs(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetMultipleKMSConfigs(ctx, []string{"id1", "id2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateKmsConfig(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateKmsConfigParams{}

		result, err := orch.UpdateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_CheckAndUpdateKmsConfigHealth(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &models.KmsConfigCheck{}

		result, err := orch.CheckAndUpdateKmsConfigHealth(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_AccessCryptoKeyAndEncryptDataWithImpersonation(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		kmsConfig := &models.KmsConfig{}

		err := orch.AccessCryptoKeyAndEncryptDataWithImpersonation(ctx, kmsConfig)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_DeleteKmsConfig(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteKmsConfigParams{}

		result, jobID, err := orch.DeleteKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_MigrateKmsConfig(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.MigrateKmsConfigParams{}

		result, err := orch.MigrateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_RotateKmsConfig(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.RotateKmsConfigParams{}

		result, job, err := orch.RotateKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Nil(tt, job)
	})
}

func TestOCIOrchestrator_CreateAndSyncKmsConfig(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateKmsConfigParams{}

		result, err := orch.CreateAndSyncKmsConfig(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetSDEKmsConfiguration(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.GetKmsConfigParams{}

		result, err := orch.GetSDEKmsConfiguration(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetBackupVaultByNameAndOwnerID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupVaultByNameAndOwnerID(ctx, "bv-name", "owner-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetBackupPolicyByNameAndOwnerID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupPolicyByNameAndOwnerID(ctx, "policy-name", "owner-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetBackupPolicyByUUIDAndOwnerID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupPolicyByUUIDAndOwnerID(ctx, "uuid", "owner-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateBackupPolicy(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateBackupPolicyParams{}

		result, jobID, err := orch.UpdateBackupPolicy(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_ListBackupPoliciesAndVolumeCount(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		volumeCounts, policies, err := orch.ListBackupPoliciesAndVolumeCount(ctx, "owner-id", []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, volumeCounts)
		assert.Nil(tt, policies)
	})
}

func TestOCIOrchestrator_GetBackupPoliciesByUUIDs(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		volumeCounts, policies, err := orch.GetBackupPoliciesByUUIDs(ctx, []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, volumeCounts)
		assert.Nil(tt, policies)
	})
}

func TestOCIOrchestrator_DeleteBackupPolicy(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteBackupPolicyParams{}

		result, jobID, err := orch.DeleteBackupPolicy(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetBackupPolicyUUIDsFromBackupVaultUUID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupPolicyUUIDsFromBackupVaultUUID(ctx, "backup-vault-uuid", "owner-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_ListBackupVaults(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.ListBackupVaults(ctx, "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetBackupVaultByUUID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupVaultByUUID(ctx, "bv-uuid", "owner-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateBackupVault(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.BackupVaultParams{}

		result, jobID, err := orch.UpdateBackupVault(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetMultipleBackupVaults(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetMultipleBackupVaults(ctx, []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_DeleteBackupVault(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.BackupVaultParams{}

		result, jobID, err := orch.DeleteBackupVault(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_DeleteBackupVaultInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.BackupVaultParams{}

		result, err := orch.DeleteBackupVaultInternal(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_UpdateBackupVaultInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.BackupVaultParams{}

		result, jobID, err := orch.UpdateBackupVaultInternal(ctx, params, false)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_IsBackupVaultAttachedToVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.IsBackupVaultAttachedToVolume(ctx, "backup-vault-uuid")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.False(tt, result)
	})
}

func TestOCIOrchestrator_GetBackupVaultUUIDsFromBackupPolicyUUID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupVaultUUIDsFromBackupPolicyUUID(ctx, "backup-policy-uuid", "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_CreateBackupVaultEntryInVCP(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		bv := &datamodel.BackupVault{}

		result, err := orch.CreateBackupVaultEntryInVCP(ctx, bv, &commonparams.BackupVaultParams{})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_BackfillBackupVaultInVCP(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		bv := &datamodel.BackupVault{}

		result, err := orch.BackfillBackupVaultInVCP(ctx, bv, "project-number")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_CreateBackupVault(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateBackupVaultParams{}

		jobID, err := orch.CreateBackupVault(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, jobID)
	})

	t.Run("ReturnsNotImplementedError_nilParams", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		jobID, err := orch.CreateBackupVault(ctx, nil)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetBackupVaultByExternalUUIDAndOwnerID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupVaultByExternalUUIDAndOwnerID(ctx, "external-uuid", "owner-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetAccount(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetAccount(ctx, "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_PersistAccountTrialMetadataIfSet(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		err := orch.PersistAccountTrialMetadataIfSet(ctx, "account-name", nil)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_UpdateResourceState(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateResourceStateParams{}

		result, err := orch.UpdateResourceState(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_CreateBackup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateBackupParams{}

		result, jobID, err := orch.CreateBackup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_CreateBackupInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateBackupParams{}

		result, jobID, err := orch.CreateBackupInternal(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetBackup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.GetBackupParams{}

		result, err := orch.GetBackup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetBackupByExternalUUID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupByExternalUUID(ctx, "backup-vault-uuid", "external-uuid", "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_DeleteBackup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteBackupParams{}

		result, jobID, err := orch.DeleteBackup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_DeleteBackupInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteBackupParams{}

		result, err := orch.DeleteBackupInternal(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_ListBackups(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.ListBackups(ctx, "backup-vault-id", "owner-id", nil)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateBackup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateBackupParams{}

		result, jobID, err := orch.UpdateBackup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_UpdateBackupInternal(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateBackupParams{}

		result, jobID, err := orch.UpdateBackupInternal(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetBackupsUnderBackupVault(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupsUnderBackupVault(ctx, "backup-vault-id", "owner-id", []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateBackupLatestLogicalBackupSizeByVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		err := orch.UpdateBackupLatestLogicalBackupSizeByVolume(ctx, "volume-uuid", "backup-uuid")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_RotateCmekBackupsForBackupVault(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.BackupVaultParams{}

		result, err := orch.RotateCmekBackupsForBackupVault(ctx, params, "primary-key-version")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_CreateOrGetStartProjectEventJob(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.StartProjectEventParams{}

		result, err := orch.CreateOrGetStartProjectEventJob(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_CreateOrGetFinishProjectEventJob(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.FinishProjectEventParams{}

		result, err := orch.CreateOrGetFinishProjectEventJob(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_CreateActiveDirectory(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateActiveDirectoryParams{}

		result, jobID, err := orch.CreateActiveDirectory(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_UpdateActiveDirectory(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateActiveDirectoryParams{}

		result, jobID, err := orch.UpdateActiveDirectory(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})
}

func TestOCIOrchestrator_GetActiveDirectory(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.GetADParams{}

		result, err := orch.GetActiveDirectory(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_ListActiveDirectories(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.ListActiveDirectories(ctx, "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetMultipleActiveDirectories(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetMultipleActiveDirectories(ctx, []string{"uuid1", "uuid2"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetADConfig(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.GetADParams{}

		result, err := orch.GetADConfig(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetSDEActiveDirectory(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.GetADParams{}

		result, err := orch.GetSDEActiveDirectory(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_DeleteActiveDirectory(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteActiveDirectoryParams{}

		result, err := orch.DeleteActiveDirectory(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_GetExpertModeVolumeByExternalUUID(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetExpertModeVolumeByExternalUUID(ctx, "volume-uuid")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_CreateExpertModeVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ExpertModeVolumeParams{}

		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_UpdateExpertModeVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ExpertModeVolumeParams{}

		err := orch.UpdateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_DeleteExpertModeVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ExpertModeVolumeParams{}

		err := orch.DeleteExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_RenameExpertModeVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ExpertModeVolumeRenameParams{}

		err := orch.RenameExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_StartExpertModeFlexCloneSplit(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ExpertModeFlexCloneSplitParams{}

		err := orch.StartExpertModeFlexCloneSplit(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_UpdateRbacForPools(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.UpdateRbacForPools(ctx)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_UpdateRbacForPoolById(t *testing.T) {
	t.Run("ReturnsBadRequestWhenPoolOCIDMissing", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		orch := NewOCIOrchestrator(mockStorage, mockTemporal)
		ctx := context.Background()

		result, err := orch.UpdateRbacForPoolById(ctx, &commonparams.RefreshRbacForPoolParams{PoolID: "6bed33e1-cc9c-e0b5-ac63-24e9410e64c1"})

		assert.Error(tt, err)
		assert.True(tt, errors.IsBadRequestErr(err))
		assert.Empty(tt, result)
	})
}

func TestOCIOrchestrator_GetBackupConfigsForPool(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.GetBackupConfigsForPool(ctx, "pool-id", "account-name", "us-west1-a")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_CreateVolumePerformanceGroup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.CreateVolumePerformanceGroupParams{}

		result, err := orch.CreateVolumePerformanceGroup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_ListVolumePerformanceGroups(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ListVolumePerformanceGroupsParams{}

		result, err := orch.ListVolumePerformanceGroups(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_GetVolumePerformanceGroup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.GetVolumePerformanceGroupParams{}

		result, err := orch.GetVolumePerformanceGroup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_UpdateVolumePerformanceGroup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.UpdateVolumePerformanceGroupParams{}

		result, jobUUID, err := orch.UpdateVolumePerformanceGroup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, jobUUID)
	})
}

func TestOCIOrchestrator_DeleteVolumePerformanceGroup(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.DeleteVolumePerformanceGroupParams{}

		_, jobUUID, err := orch.DeleteVolumePerformanceGroup(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Empty(tt, jobUUID)
	})
}

func TestOCIOrchestrator_ReplaceDstQuotaRulesWithSrc(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		req := &commonparams.UpdateDstWithSrcQuotaRulesV1beta{}
		params := commonparams.V1betaUpdateDestinationQuotaRulesVCPParams{}

		result, err := orch.ReplaceDstQuotaRulesWithSrc(ctx, req, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}

func TestOCIOrchestrator_ManageBackupConfigForExpertModeVolume(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{}

		backupConfig, jobUUID, err := orch.ManageBackupConfigForExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, backupConfig)
		assert.Empty(tt, jobUUID)
	})
}

func TestOCIOrchestrator_GetPoolNodesAndCredentials(t *testing.T) {
	t.Run("WrapsStorageErrorOnPoolLookup", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(nil, fmt.Errorf("db unavailable"))
		orch := &OCIOrchestrator{storage: mockStorage}

		nodes, creds, err := orch.GetPoolNodesAndCredentials(context.Background(), "pool-uuid")

		assert.Error(tt, err)
		// Wrapped with operation name + pool uuid for log-side triage; underlying
		// error preserved via %w.
		assert.Contains(tt, err.Error(), "GetPoolNodesAndCredentials")
		assert.Contains(tt, err.Error(), "pool-uuid")
		assert.Contains(tt, err.Error(), "db unavailable")
		assert.Nil(tt, nodes)
		assert.Nil(tt, creds)
	})

	t.Run("WrapsStorageErrorOnNodeListing", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 42, UUID: "pool-uuid"}}
		mockStorage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil)
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, int64(42)).Return(nil, fmt.Errorf("nodes unavailable"))
		orch := &OCIOrchestrator{storage: mockStorage}

		nodes, creds, err := orch.GetPoolNodesAndCredentials(context.Background(), "pool-uuid")

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "GetPoolNodesAndCredentials")
		assert.Contains(tt, err.Error(), "id=42")
		assert.Contains(tt, err.Error(), "nodes unavailable")
		assert.Nil(tt, nodes)
		assert.Nil(tt, creds)
	})

	t.Run("ReturnsNilWhenPoolNotFound", func(tt *testing.T) {
		// DataStoreRepository.GetPoolByUUID returns customerrors.NewNotFoundErr
		// (never (nil, nil)) when the pool is missing — see database/vcp/pools.go.
		// Translate that to (nil, nil, nil) so best-effort callers skip enrichment
		// without logging a noisy error.
		mockStorage := database.NewMockStorage(tt)
		missingUUID := "missing-uuid"
		mockStorage.EXPECT().GetPoolByUUID(mock.Anything, missingUUID).
			Return(nil, errors.NewNotFoundErr("Pool", &missingUUID))
		orch := &OCIOrchestrator{storage: mockStorage}

		nodes, creds, err := orch.GetPoolNodesAndCredentials(context.Background(), missingUUID)

		assert.NoError(tt, err)
		assert.Nil(tt, nodes)
		assert.Nil(tt, creds)
	})

	t.Run("ReturnsNodesAndCredentialsWhenPoolExists", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 42, UUID: "pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				ExternalCertificate: &datamodel.ExternalCredRef{
					ExternalIdentifier: "ocid1.certificate.oc1.iad.testcert",
					Version:            7,
				},
			},
		}
		nodes := []*datamodel.Node{
			{BaseModel: datamodel.BaseModel{UUID: "node-uuid-1"}, Name: "vm-01"},
			{BaseModel: datamodel.BaseModel{UUID: "node-uuid-2"}, Name: "vm-02"},
		}
		mockStorage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil)
		mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, int64(42)).Return(nodes, nil)
		orch := &OCIOrchestrator{storage: mockStorage}

		result, creds, err := orch.GetPoolNodesAndCredentials(context.Background(), "pool-uuid")

		assert.NoError(tt, err)
		if assert.Len(tt, result, 2) {
			assert.Equal(tt, "node-uuid-1", result[0].UUID)
			assert.Equal(tt, "vm-01", result[0].Name)
		}
		// Live credentials are returned from the same pool read (no extra lookup).
		if assert.NotNil(tt, creds) && assert.NotNil(tt, creds.ExternalCertificate) {
			assert.Equal(tt, int64(7), creds.ExternalCertificate.Version)
		}
	})
}

func TestOCIOrchestrator_HasActiveClusterUpgrade(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		result, err := orch.HasActiveClusterUpgrade(ctx, "cluster-id")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.False(tt, result)
	})
}

func TestOCIOrchestrator_DeleteImageVersion(t *testing.T) {
	t.Run("ReturnsNotImplementedError", func(tt *testing.T) {
		orch := &OCIOrchestrator{}
		ctx := context.Background()

		err := orch.DeleteImageVersion(ctx, "9.17.1P2")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
	})
}

func TestOCIOrchestrator_UpdateRbacForPoolById_Success(t *testing.T) {
	t.Run("DispatchesWorkflowSuccessfully", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		orch := NewOCIOrchestrator(mockStorage, mockTemporal)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 10}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
				State:     datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		// The RBAC refresh workflow must be dispatched on the customer task queue
		// so it runs alongside other customer-facing OCI workflows rather than on
		// the background queue.
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
			return opts.TaskQueue == workflowengine.CustomerTaskQueue
		}), mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		params := &commonparams.RefreshRbacForPoolParams{
			PoolOCID:    "ocid1.pool.oc1..abc",
			AccountName: "test-account",
			RbacFileURL: "https://example.com/rbac.yaml",
		}
		result, err := orch.UpdateRbacForPoolById(ctx, params)

		assert.NoError(tt, err)
		assert.NotEmpty(tt, result)
	})

	t.Run("ReturnsErrorWhenAccountNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		orch := NewOCIOrchestrator(mockStorage, mockTemporal)
		ctx := context.Background()

		mockStorage.EXPECT().GetAccount(mock.Anything, "missing-account").Return(nil, gorm.ErrRecordNotFound)

		params := &commonparams.RefreshRbacForPoolParams{
			PoolOCID:    "ocid1.pool.oc1..abc",
			AccountName: "missing-account",
		}
		result, err := orch.UpdateRbacForPoolById(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotFoundErr(err))
		assert.Empty(tt, result)
	})

	t.Run("ReturnsErrorWhenPoolNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		orch := NewOCIOrchestrator(mockStorage, mockTemporal)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 10}, Name: "test-account"}
		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)

		params := &commonparams.RefreshRbacForPoolParams{
			PoolOCID:    "ocid1.pool.oc1..missing",
			AccountName: "test-account",
		}
		result, err := orch.UpdateRbacForPoolById(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotFoundErr(err))
		assert.Empty(tt, result)
	})

	t.Run("ReturnsErrorWhenWorkflowFails", func(tt *testing.T) {
		withFastWorkflowRetry(tt)
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		orch := NewOCIOrchestrator(mockStorage, mockTemporal)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 10}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
				State:     datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("temporal unavailable"))

		params := &commonparams.RefreshRbacForPoolParams{
			PoolOCID:    "ocid1.pool.oc1..abc",
			AccountName: "test-account",
		}
		result, err := orch.UpdateRbacForPoolById(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to dispatch RBAC refresh workflow")
		assert.Empty(tt, result)
	})
}

func TestGetPoolByOCID(t *testing.T) {
	t.Run("ReturnsBadRequestWhenPoolOCIDEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		result, err := getPoolByOCID(ctx, mockStorage, "", "account-name")

		assert.Error(tt, err)
		assert.True(tt, errors.IsBadRequestErr(err))
		assert.Nil(tt, result)
	})

	t.Run("ReturnsBadRequestWhenAccountNameEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		result, err := getPoolByOCID(ctx, mockStorage, "ocid1.pool.oc1..abc", "")

		assert.Error(tt, err)
		assert.True(tt, errors.IsBadRequestErr(err))
		assert.Nil(tt, result)
	})

	t.Run("ReturnsBadRequestWhenBothEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		result, err := getPoolByOCID(ctx, mockStorage, "", "")

		assert.Error(tt, err)
		assert.True(tt, errors.IsBadRequestErr(err))
		assert.Nil(tt, result)
	})

	t.Run("ReturnsNotFoundWhenAccountNotFound_GormErr", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		mockStorage.EXPECT().GetAccount(mock.Anything, "missing-account").Return(nil, gorm.ErrRecordNotFound)

		result, err := getPoolByOCID(ctx, mockStorage, "ocid1.pool.oc1..abc", "missing-account")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotFoundErr(err))
		assert.Nil(tt, result)
	})

	t.Run("ReturnsNotFoundWhenAccountNotFound_CustomErr", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		accountName := "missing-account"
		mockStorage.EXPECT().GetAccount(mock.Anything, accountName).Return(nil, errors.NewNotFoundErr("account", &accountName))

		result, err := getPoolByOCID(ctx, mockStorage, "ocid1.pool.oc1..abc", accountName)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotFoundErr(err))
		assert.Nil(tt, result)
	})

	t.Run("ReturnsRawErrorOnAccountLookupFailure", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		mockStorage.EXPECT().GetAccount(mock.Anything, "account-name").Return(nil, fmt.Errorf("db connection failed"))

		result, err := getPoolByOCID(ctx, mockStorage, "ocid1.pool.oc1..abc", "account-name")

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "db connection failed")
		assert.Nil(tt, result)
	})

	t.Run("ReturnsNotFoundWhenPoolNotFound_GormErr", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 5}, Name: "test-account"}
		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)

		result, err := getPoolByOCID(ctx, mockStorage, "ocid1.pool.oc1..missing", "test-account")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotFoundErr(err))
		assert.Nil(tt, result)
	})

	t.Run("ReturnsNotFoundWhenPoolNotFound_CustomErr", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		poolOCID := "ocid1.pool.oc1..missing"
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 5}, Name: "test-account"}
		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("pool", &poolOCID))

		result, err := getPoolByOCID(ctx, mockStorage, poolOCID, "test-account")

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotFoundErr(err))
		assert.Nil(tt, result)
	})

	t.Run("ReturnsRawErrorOnPoolLookupFailure", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 5}, Name: "test-account"}
		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("timeout"))

		result, err := getPoolByOCID(ctx, mockStorage, "ocid1.pool.oc1..abc", "test-account")

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "timeout")
		assert.Nil(tt, result)
	})

	t.Run("ReturnsPoolOnSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 5}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-1", ID: 100},
				PoolExternalIdentifier: "ocid1.pool.oc1..abc",
				State:                  datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)

		result, err := getPoolByOCID(ctx, mockStorage, "ocid1.pool.oc1..abc", "test-account")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "pool-uuid-1", result.UUID)
	})
}

func TestCheckActiveUpgradeJob(t *testing.T) {
	t.Run("ReturnsNilWhenNoJobs", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "cluster-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)

		result, err := common.CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")

		assert.NoError(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("ReturnsNilWhenAllJobsCompleted", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		jobs := []*datamodel.ClusterUpgradeJob{
			{BaseModel: datamodel.BaseModel{UUID: "job-1"}, Status: string(models.UpgradeStatusCompleted)},
			{BaseModel: datamodel.BaseModel{UUID: "job-2"}, Status: string(models.UpgradeStatusFailed)},
		}
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "cluster-1").Return(jobs, nil)

		result, err := common.CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")

		assert.NoError(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("ReturnsPendingJob", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		jobs := []*datamodel.ClusterUpgradeJob{
			{BaseModel: datamodel.BaseModel{UUID: "job-1"}, Status: string(models.UpgradeStatusCompleted)},
			{BaseModel: datamodel.BaseModel{UUID: "job-2"}, Status: string(models.UpgradeStatusPending)},
		}
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "cluster-1").Return(jobs, nil)

		result, err := common.CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "job-2", result.UUID)
		assert.Equal(tt, string(models.UpgradeStatusPending), result.Status)
	})

	t.Run("ReturnsInProgressJob", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		jobs := []*datamodel.ClusterUpgradeJob{
			{BaseModel: datamodel.BaseModel{UUID: "job-1"}, Status: string(models.UpgradeStatusInProgress)},
		}
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "cluster-1").Return(jobs, nil)

		result, err := common.CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "job-1", result.UUID)
		assert.Equal(tt, string(models.UpgradeStatusInProgress), result.Status)
	})

	t.Run("ReturnsErrorOnStorageFailure", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "cluster-1").Return(nil, fmt.Errorf("db error"))

		result, err := common.CheckActiveUpgradeJob(ctx, mockStorage, "cluster-1")

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "db error")
		assert.Nil(tt, result)
	})
}

func TestCreateUpgradeJobInDB(t *testing.T) {
	t.Run("CreatesJobWithBuildInfoVersion", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.16.1"},
		}
		params := &commonparams.UpgradeClusterParams{
			ClusterID: "cluster-1",
			Metadata:  map[string]string{"key": "value"},
		}

		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.MatchedBy(func(job *datamodel.ClusterUpgradeJob) bool {
			return job.UUID == "test-job-uuid" &&
				job.ClusterID == "pool-uuid-1" &&
				job.PoolID == "pool-uuid-1" &&
				job.TargetVersion == "9.17.1P2" &&
				job.CurrentVersion == "9.16.1" &&
				job.VSABuildImage == "/path/to/image" &&
				job.Status == string(models.UpgradeStatusPending) &&
				job.Metadata != nil &&
				(*job.Metadata)[clusterUpgradeWorkflowIDMetadataKey] == "test-workflow-uuid"
		})).Return(&datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Status:    string(models.UpgradeStatusPending),
		}, nil)

		result, err := createUpgradeJobInDB(ctx, mockStorage, params, pool, "9.17.1P2", "test-job-uuid", "/path/to/image", "test-workflow-uuid", "ocid1.image.oc1.iad.mediator")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-job-uuid", result.UUID)
	})

	t.Run("CreatesJobWithUnknownVersionWhenBuildInfoNil", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"},
			BuildInfo: nil,
		}
		params := &commonparams.UpgradeClusterParams{
			ClusterID:    "cluster-2",
			ForceUpgrade: true,
		}

		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.MatchedBy(func(job *datamodel.ClusterUpgradeJob) bool {
			return job.CurrentVersion == "unknown" &&
				job.ForceUpgrade == true &&
				job.Metadata != nil &&
				(*job.Metadata)[clusterUpgradeWorkflowIDMetadataKey] == "test-workflow-uuid-2"
		})).Return(&datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid-2"},
		}, nil)

		result, err := createUpgradeJobInDB(ctx, mockStorage, params, pool, "9.17.1P2", "test-job-uuid-2", "/path/to/image", "test-workflow-uuid-2", "")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
	})

	t.Run("CreatesJobWithUnknownVersionWhenOntapVersionEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-3"},
			BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: ""},
		}
		params := &commonparams.UpgradeClusterParams{ClusterID: "cluster-3"}

		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.MatchedBy(func(job *datamodel.ClusterUpgradeJob) bool {
			return job.CurrentVersion == "unknown"
		})).Return(&datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid-3"},
		}, nil)

		result, err := createUpgradeJobInDB(ctx, mockStorage, params, pool, "9.17.1P2", "test-job-uuid-3", "", "test-workflow-uuid-3", "")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
	})

	t.Run("ReturnsErrorOnStorageFailure", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		ctx := context.Background()

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-4"},
		}
		params := &commonparams.UpgradeClusterParams{ClusterID: "cluster-4"}

		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("constraint violation"))

		result, err := createUpgradeJobInDB(ctx, mockStorage, params, pool, "9.17.1P2", "test-job-uuid-4", "", "test-workflow-uuid-4", "")

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "constraint violation")
		assert.Nil(tt, result)
	})
}

func TestConvertMetadataToJSONB(t *testing.T) {
	t.Run("ReturnsNilForNilInput", func(tt *testing.T) {
		result := common.ConvertMetadataToJSONB(nil)
		assert.Nil(tt, result)
	})

	t.Run("ConvertsEmptyMap", func(tt *testing.T) {
		result := common.ConvertMetadataToJSONB(map[string]string{})

		assert.NotNil(tt, result)
		assert.Empty(tt, *result)
	})

	t.Run("ConvertsSingleEntry", func(tt *testing.T) {
		input := map[string]string{"key1": "value1"}

		result := common.ConvertMetadataToJSONB(input)

		assert.NotNil(tt, result)
		assert.Equal(tt, "value1", (*result)["key1"])
	})

	t.Run("ConvertsMultipleEntries", func(tt *testing.T) {
		input := map[string]string{
			"env":     "staging",
			"team":    "storage",
			"version": "9.17.1",
		}

		result := common.ConvertMetadataToJSONB(input)

		assert.NotNil(tt, result)
		assert.Len(tt, *result, 3)
		assert.Equal(tt, "staging", (*result)["env"])
		assert.Equal(tt, "storage", (*result)["team"])
		assert.Equal(tt, "9.17.1", (*result)["version"])
	})
}

func TestUpgradeCluster(t *testing.T) {
	t.Run("ReturnsErrorWhenGetPoolByOCIDFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(nil, gorm.ErrRecordNotFound)

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:    "ocid1.pool.oc1..abc",
			AccountName: "test-account",
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsNotFoundErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, wfID)
	})

	t.Run("ReturnsErrorWhenClusterNotInValidState", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     "CREATING",
			},
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:    "ocid1.pool.oc1..abc",
			AccountName: "test-account",
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "READY or DISABLED")
		assert.Nil(tt, result)
		assert.Empty(tt, wfID)
	})

	t.Run("ReturnsErrorWhenCheckActiveUpgradeJobFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(nil, fmt.Errorf("db error"))

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:    "ocid1.pool.oc1..abc",
			AccountName: "test-account",
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsUnavailableErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, wfID)
	})

	t.Run("ReturnsConflictWhenActiveUpgradeJobExists", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
			},
		}
		activeJobs := []*datamodel.ClusterUpgradeJob{
			{
				BaseModel: datamodel.BaseModel{UUID: "active-job"},
				Status:    string(models.UpgradeStatusInProgress),
				Metadata:  withClusterUpgradeWorkflowIDMetadata(nil, "upgrade-wf-123"),
			},
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return(activeJobs, nil)

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:    "ocid1.pool.oc1..abc",
			AccountName: "test-account",
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "upgrade-wf-123")
		assert.Nil(tt, result)
		assert.Empty(tt, wfID)
	})

	t.Run("ReturnsErrorWhenCreateUpgradeJobFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("insert failed"))

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:           "ocid1.pool.oc1..abc",
			AccountName:        "test-account",
			TargetOntapVersion: "9.17.1P2",
			VSAImagePath:       "/path/to/image",
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsUnavailableErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, wfID)
	})

	t.Run("ReturnsErrorWhenWorkflowExecutionFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
			},
		}
		createdJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "new-job-uuid"},
			Status:    string(models.UpgradeStatusPending),
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("workflow error"))
		mockStorage.EXPECT().GetClusterUpgradeJobByUUID(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateClusterUpgradeJob(mock.Anything, mock.MatchedBy(func(j *datamodel.ClusterUpgradeJob) bool {
			return j.Status == string(models.UpgradeStatusFailed) && j.ErrorDetails != nil
		})).Return(nil)

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:           "ocid1.pool.oc1..abc",
			AccountName:        "test-account",
			TargetOntapVersion: "9.17.1P2",
			VSAImagePath:       "/path/to/image",
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsUnavailableErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, wfID)
	})

	t.Run("MarkJobFailedErrorIsLoggedNonFatal", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
			},
		}
		createdJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "new-job-uuid"},
			Status:    string(models.UpgradeStatusPending),
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("workflow error"))
		mockStorage.EXPECT().GetClusterUpgradeJobByUUID(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("db unavailable"))

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:           "ocid1.pool.oc1..abc",
			AccountName:        "test-account",
			TargetOntapVersion: "9.17.1P2",
			VSAImagePath:       "/path/to/image",
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.True(tt, errors.IsUnavailableErr(err))
		assert.Nil(tt, result)
		assert.Empty(tt, wfID)
	})

	t.Run("SucceedsWithREADYStatePool", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.16.1"},
			},
		}
		createdJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "new-job-uuid"},
			Status:    string(models.UpgradeStatusPending),
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.MatchedBy(func(j *datamodel.ClusterUpgradeJob) bool {
			// Verify that empty mediator image is stored
			return j.MediatorBuildImage == ""
		})).Return(createdJob, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(wp interface{}) bool {
			// Verify workflow params have empty mediator image
			if params, ok := wp.(*ociworkflows.OCIClusterUpgradeWorkflowParams); ok {
				return params.MediatorImageOcid == ""
			}
			return false
		})).Return(nil, nil)

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:           "ocid1.pool.oc1..abc",
			AccountName:        "test-account",
			TargetOntapVersion: "9.17.1P2",
			VSAImagePath:       "/path/to/image",
			MediatorBuildImage: "", // Empty - mediator upgrade will be skipped
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotEmpty(tt, wfID)
		assert.Equal(tt, "pool-uuid", result.ClusterID)
		assert.Equal(tt, models.UpgradeStatusInProgress, result.Status)
	})

	t.Run("SucceedsWithDISABLEDStatePool", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateDisabled,
			},
		}
		createdJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "new-job-uuid"},
			Status:    string(models.UpgradeStatusPending),
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:           "ocid1.pool.oc1..abc",
			AccountName:        "test-account",
			TargetOntapVersion: "9.17.1P2",
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotEmpty(tt, wfID)
		assert.Equal(tt, models.UpgradeStatusInProgress, result.Status)
	})

	// MediatorBuildImage OCID format validation is handled by the API handler
	// (validateUpgradePoolRequest) and env var validation at container startup
	// (ValidateOCIWorkerStartupEnv). The factory layer trusts these boundaries
	// and does not re-validate. See TestUpgradePool_InvalidMediatorImageOcid
	// in oci-proxy/api/endpoints/cluster_upgrade_endpoints_test.go.

	t.Run("SucceedsWithEmptyMediatorImageOcid", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.16.1"},
			},
		}
		createdJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "new-job-uuid"},
			Status:    string(models.UpgradeStatusPending),
		}

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.MatchedBy(func(j *datamodel.ClusterUpgradeJob) bool {
			// Verify that empty mediator image is stored
			return j.MediatorBuildImage == ""
		})).Return(createdJob, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(wp interface{}) bool {
			// Verify workflow params have empty mediator image
			if params, ok := wp.(*ociworkflows.OCIClusterUpgradeWorkflowParams); ok {
				return params.MediatorImageOcid == ""
			}
			return false
		})).Return(nil, nil)

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:           "ocid1.pool.oc1..abc",
			AccountName:        "test-account",
			TargetOntapVersion: "9.17.1P2",
			VSAImagePath:       "/path/to/image",
			MediatorBuildImage: "", // Empty - mediator upgrade will be skipped
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotEmpty(tt, wfID)
		assert.Equal(tt, "pool-uuid", result.ClusterID)
		assert.Equal(tt, models.UpgradeStatusInProgress, result.Status)
	})

	t.Run("SucceedsWithValidMediatorImageOcid", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		ctx := context.Background()

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.16.1"},
			},
		}
		createdJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "new-job-uuid"},
			Status:    string(models.UpgradeStatusPending),
		}

		validMediatorOcid := "ocid1.image.oc1.iad.aaaaaaatest123"

		mockStorage.EXPECT().GetAccount(mock.Anything, "test-account").Return(account, nil)
		mockStorage.EXPECT().GetPoolByName(mock.Anything, mock.Anything).Return(poolView, nil)
		mockStorage.EXPECT().GetClusterUpgradeJobsByClusterID(mock.Anything, "pool-uuid").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.EXPECT().CreateClusterUpgradeJob(mock.Anything, mock.MatchedBy(func(j *datamodel.ClusterUpgradeJob) bool {
			// Verify that valid mediator image is stored
			return j.MediatorBuildImage == validMediatorOcid
		})).Return(createdJob, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(wp interface{}) bool {
			// Verify workflow params have the mediator image
			if params, ok := wp.(*ociworkflows.OCIClusterUpgradeWorkflowParams); ok {
				return params.MediatorImageOcid == validMediatorOcid
			}
			return false
		})).Return(nil, nil)

		params := &commonparams.UpgradeClusterParams{
			PoolOCID:           "ocid1.pool.oc1..abc",
			AccountName:        "test-account",
			TargetOntapVersion: "9.17.1P2",
			VSAImagePath:       "/path/to/image",
			MediatorBuildImage: validMediatorOcid,
		}
		result, wfID, err := upgradeCluster(ctx, mockStorage, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotEmpty(tt, wfID)
		assert.Equal(tt, "pool-uuid", result.ClusterID)
		assert.Equal(tt, models.UpgradeStatusInProgress, result.Status)
	})
}

func TestOCIOrchestrator_ExternalClusterMethods(t *testing.T) {
	orch := &OCIOrchestrator{}
	ctx := context.Background()

	t.Run("OnboardExternalClusters", func(tt *testing.T) {
		result, err := orch.OnboardExternalClusters(ctx, &commonparams.OnboardExternalClustersParams{})
		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})

	t.Run("GetExternalCluster", func(tt *testing.T) {
		result, err := orch.GetExternalCluster(ctx, "host-id")
		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})

	t.Run("UpdateExternalCluster", func(tt *testing.T) {
		result, err := orch.UpdateExternalCluster(ctx, &commonparams.UpdateExternalClusterParams{ExternalClusterID: "host-id"})
		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})

	t.Run("DeleteExternalCluster", func(tt *testing.T) {
		result, err := orch.DeleteExternalCluster(ctx, "host-id")
		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})

	t.Run("PinAccountHosts", func(tt *testing.T) {
		result, err := orch.PinAccountHosts(ctx, &commonparams.PinAccountHostsParams{})
		assert.Error(tt, err)
		assert.True(tt, errors.IsNotImplementedYetErr(err))
		assert.Nil(tt, result)
	})
}
