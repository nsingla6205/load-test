package workflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// CreateVolumePerformanceGroupWorkflow creates the QoS policy in ONTAP for a VPG that was already created in the database,
// then updates the VPG row with the Ontap QoS policy ID.
// The orchestrator creates the VPG in the DB first, starts this workflow, and returns the VPG to the caller as the ontap id is not returned to the caller
func CreateVolumePerformanceGroupWorkflow(ctx workflow.Context, vpgUUID string) (*datamodel.VolumePerformanceGroup, error) {
	logger := workflow.GetLogger(ctx)
	vpgActivity := &activities.VolumePerformanceGroupActivity{}
	commonActivity := activities.CommonActivities{}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}
	// Use volume-activity timeouts and heartbeat so ONTAP-related activities match volume create/update behavior.
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var vpg *datamodel.VolumePerformanceGroup
	err = workflow.ExecuteActivity(ctx, vpgActivity.GetVolumePerformanceGroupByUUID, vpgUUID).Get(ctx, &vpg)
	if err != nil {
		logger.Error("Failed to get VPG by UUID", "error", err, "vpg_uuid", vpgUUID)
		return nil, err
	}

	var pool *datamodel.Pool
	err = workflow.ExecuteActivity(ctx, commonActivity.GetPoolBySvmPoolId, vpg.PoolID).Get(ctx, &pool)
	if err != nil {
		logger.Error("Failed to get pool for VPG", "error", err, "pool_id", vpg.PoolID)
		return nil, err
	}

	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, commonActivity.GetNode, vpg.PoolID).Get(ctx, &dbNodes)
	if err != nil {
		logger.Error("Failed to get nodes for VPG", "error", err, "pool_id", vpg.PoolID)
		return nil, err
	}
	if len(dbNodes) == 0 {
		logger.Error("No nodes found for pool", "pool_id", vpg.PoolID)
		return nil, fmt.Errorf("no nodes found for pool %d", vpg.PoolID)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})
	if node == nil {
		logger.Error("CreateNodeForProvider returned nil", "pool_id", vpg.PoolID)
		return nil, fmt.Errorf("CreateNodeForProvider returned nil for pool %d", vpg.PoolID)
	}
	// Fail fast if pool has no valid node endpoints (avoids generic ONTAP timeout later).
	if len(node.EndpointAddressesToHostNameMap) == 0 {
		logger.Error("No valid node endpoints for pool", "pool_id", vpg.PoolID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrVSAClusterNodeIPAddressNotFound,
			fmt.Errorf("pool %d has no nodes with endpoint addresses; cannot contact ONTAP", vpg.PoolID)))
	}

	// Check ONTAP cluster health before calling ONTAP (same as volume create) so unreachable storage fails fast.
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	var isOntapClusterHealthy *bool
	err = workflow.ExecuteActivity(ctx, volumeCreateActivity.GetOntapClusterHealth, node).Get(ctx, &isOntapClusterHealthy)
	if err != nil {
		logger.Error("Failed to check ONTAP cluster health", "error", err, "pool_id", vpg.PoolID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOntapClusterHealthCheckFailed, err))
	}
	if !*isOntapClusterHealthy {
		logger.Error("ONTAP cluster is not available", "pool_id", vpg.PoolID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOntapClusterNotAvailable,
			fmt.Errorf("ONTAP cluster is down or unreachable for pool %d", vpg.PoolID)))
	}

	var qosPolicyID string
	err = workflow.ExecuteActivity(ctx, vpgActivity.CreateQoSPolicyInONTAP, vpg, node).Get(ctx, &qosPolicyID)
	if err != nil {
		logger.Error("Failed to create QoS policy in ONTAP", "error", err, "vpg_name", vpg.Name)
		return nil, err
	}

	err = workflow.ExecuteActivity(ctx, vpgActivity.UpdateVPGWithOntapID, vpgUUID, qosPolicyID).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to update VPG with Ontap ID", "error", err, "vpg_uuid", vpgUUID)
		return nil, err
	}

	err = workflow.ExecuteActivity(ctx, vpgActivity.UpdateVPGStateInDB, vpgUUID, datamodel.LifeCycleStateREADY, "").Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to set VPG state to READY", "error", err, "vpg_uuid", vpgUUID)
		return nil, err
	}

	// Re-fetch VPG so we return the updated row
	err = workflow.ExecuteActivity(ctx, vpgActivity.GetVolumePerformanceGroupByUUID, vpgUUID).Get(ctx, &vpg)
	if err != nil {
		logger.Error("Failed to re-fetch VPG after update", "error", err, "vpg_uuid", vpgUUID)
		return nil, err
	}
	return vpg, nil
}
