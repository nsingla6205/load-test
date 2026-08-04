package flexcache_activities

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/flexcache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
)

var fetchTemporalClientForFlexCacheDelete = _fetchTemporalClientForFlexCacheDelete

const (
	flexCacheCreateWorkflowTerminalPollInterval = 2 * time.Second
	flexCacheCreateWorkflowTerminalWaitTimeout  = 3 * time.Minute
)

func _fetchTemporalClientForFlexCacheDelete(ctx context.Context) client.Client {
	return activity.GetClient(ctx)
}

func isFlexCacheCreateWorkflowExecutionTerminal(status enumspb.WorkflowExecutionStatus) bool {
	switch status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
		enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
		enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED,
		enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED,
		enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return true
	default:
		return false
	}
}

type FlexCacheVolumeDeleteActivity struct {
	SE database.Storage
}

// RefreshDBVolumeForDeleteActivity reloads the volume from the database so later delete steps
// see fields written by a concurrent establish-peering workflow (that is cancelled now)
func (a FlexCacheVolumeDeleteActivity) RefreshDBVolumeForDeleteActivity(ctx context.Context, result *flexcache.DeleteFlexCacheResult) (*flexcache.DeleteFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("refreshing FlexCache volume %s from database for delete", result.DBVolume.UUID))
	vol, err := a.SE.DescribeVolume(ctx, result.DBVolume.UUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	result.DBVolume = vol
	logger.Debugf("refreshed volume %s from database for delete workflow", vol.UUID)
	return result, nil
}

// UnmountVolumeInOntapActivity deletes a FlexCache volume in ONTAP
func (a FlexCacheVolumeDeleteActivity) UnmountVolumeInOntapActivity(ctx context.Context, result *flexcache.DeleteFlexCacheResult) (*flexcache.DeleteFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume
	node := result.Node

	recordHeartbeat(ctx, fmt.Sprintf("getting provider to unmount FlexCache volume %s", volume.UUID))
	provider, err := hyperscalerGetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if volume.VolumeAttributes.ExternalUUID == "" {
		logger.Debug("no external UUID found for the volume, skipping unmount")
		return result, nil // No volume in ONTAP to unmount
	}

	recordHeartbeat(ctx, fmt.Sprintf("unmounting FlexCache volume %s in ONTAP", volume.UUID))
	response, err := provider.UnmountVolume(volume.VolumeAttributes.ExternalUUID)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrUnmountingFlexCacheVolume, err)
	}

	result.UnmountJobResponse = response
	logger.Debugf("FlexCache volume unmount job for volume with UUID %s initiated successfully", volume.UUID)

	return result, nil
}

// DeleteFlexCacheVolumeInOntapActivity deletes a FlexCache volume in ONTAP
func (a FlexCacheVolumeDeleteActivity) DeleteFlexCacheVolumeInOntapActivity(ctx context.Context, result *flexcache.DeleteFlexCacheResult) (*flexcache.DeleteFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume
	node := result.Node

	recordHeartbeat(ctx, fmt.Sprintf("getting provider to delete FlexCache volume %s", volume.UUID))
	provider, err := hyperscalerGetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if volume.VolumeAttributes.ExternalUUID == "" {
		logger.Debug("no external UUID found for the volume, skipping delete")
		return result, nil // No volume in ONTAP to delete
	}

	recordHeartbeat(ctx, fmt.Sprintf("deleting FlexCache volume %s in ONTAP", volume.UUID))
	response, err := provider.DeleteFlexCacheVolume(volume.VolumeAttributes.ExternalUUID, volume.Name)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDeletingFlexCacheVolume, err)
	}

	result.DeleteJobResponse = response
	logger.Debugf("FlexCache volume delete job for volume with UUID %s initiated successfully", volume.UUID)

	return result, nil
}

func (a FlexCacheVolumeDeleteActivity) DeleteSVMPeeringInOntapActivity(ctx context.Context, result *flexcache.DeleteFlexCacheResult) (*flexcache.DeleteFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume
	node := result.Node

	recordHeartbeat(ctx, fmt.Sprintf("getting provider to delete SVM peer for FlexCache volume %s", volume.UUID))
	provider, err := hyperscalerGetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("getting SVM peer for FlexCache volume %s from ONTAP", volume.UUID))
	svmPeer, err := provider.GetSVMPeer(&volume.Svm.Name, &volume.CacheParameters.PeerSvmName)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Debugf("SVM peer not found for svm=%s peer_svm=%s, skipping delete", volume.Svm.Name, volume.CacheParameters.PeerSvmName)
			return result, nil
		}
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	svmPeerUUID := svmPeer.UUID
	recordHeartbeat(ctx, fmt.Sprintf("deleting SVM peer for FlexCache volume %s in ONTAP", volume.UUID))
	err = provider.DeleteSVMPeer(svmPeerUUID, false)
	if err != nil {
		// Ignore FlexCache in-use condition (idempotent handling)
		if msg := err.Error(); strings.Contains(msg, "The peer relationship is in use by FlexCache") ||
			strings.Contains(msg, "Relationship is in use by SnapMirror in local cluster") {
			logger.Infof("Skipping SVM peer delete for %s: still in use (%s); leaving svm_peer_uuid unchanged", svmPeerUUID, msg)
			return result, nil
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDeletingSVMPeer, err)
	}

	logger.Debugf("SVM peering with UUID %s deleted successfully", svmPeerUUID)

	return result, nil
}

func (a FlexCacheVolumeDeleteActivity) DeleteClusterPeerInOntapActivity(ctx context.Context, result *flexcache.DeleteFlexCacheResult) (*flexcache.DeleteFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	clusterPeeringRow := result.ClusterPeeringRow
	node := result.Node

	recordHeartbeat(ctx, fmt.Sprintf("getting provider to delete cluster peer for FlexCache volume %s", result.DBVolume.UUID))
	provider, err := hyperscalerGetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if clusterPeeringRow.OntapPeerUUID != "" {
		recordHeartbeat(ctx, fmt.Sprintf("deleting cluster peer for FlexCache volume %s in ONTAP", result.DBVolume.UUID))
		err = provider.DeleteClusterPeer(clusterPeeringRow.OntapPeerUUID)
		if err != nil {
			if customerrors.IsNotFoundErr(err) {
				logger.Debugf("Cluster peer not found for UUID %s, skipping delete", clusterPeeringRow.OntapPeerUUID)
				return result, nil
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDeletingClusterPeer, err)
		}
		logger.Debugf("Cluster peering with UUID %s deleted successfully", clusterPeeringRow.OntapPeerUUID)
	}

	return result, nil
}

func (a FlexCacheVolumeDeleteActivity) DeleteClusterPeeringRowInDBActivity(ctx context.Context, result *flexcache.DeleteFlexCacheResult) (*flexcache.DeleteFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	clusterPeeringRow := result.ClusterPeeringRow

	recordHeartbeat(ctx, fmt.Sprintf("deleting cluster peering row %s from database", clusterPeeringRow.UUID))
	if err := a.SE.DeleteClusterPeeringRow(ctx, clusterPeeringRow); err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, err)
	}

	logger.Debugf("Cluster peering row with ID %d deleted successfully", clusterPeeringRow.ID)
	return result, nil
}

func (a FlexCacheVolumeDeleteActivity) GetClusterPeeringFromDBActivity(ctx context.Context,
	result *flexcache.DeleteFlexCacheResult) (*flexcache.DeleteFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume

	if volume.ClusterPeerID.Valid && volume.ClusterPeerID.Int64 != 0 {
		recordHeartbeat(ctx, fmt.Sprintf("getting cluster peering row for FlexCache volume %s from database", volume.UUID))
		existingPeer, err := a.SE.GetClusterPeeringRowByID(ctx, volume.ClusterPeerID.Int64)
		if err != nil {
			if customerrors.IsNotFoundErr(err) {
				logger.Debugf("Cluster peering row not found for cluster_peer_id=%d", volume.ClusterPeerID.Int64)
				return result, nil
			}
			logger.Errorf("Failed to get cluster peering row from database: %v", err)
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		result.ClusterPeeringRow = existingPeer
		logger.Debugf("Found existing cluster peering row in database: %s", existingPeer.UUID)
		return result, nil
	}

	logger.Debugf("cluster_peer_id not set on volume; skipping cluster peering row lookup")
	return result, nil
}

func (a FlexCacheVolumeDeleteActivity) GetFlexCacheAndReplicationCountsOnClusterPeeringActivity(ctx context.Context, result *flexcache.DeleteFlexCacheResult) (*flexcache.DeleteFlexCacheResult, error) {
	clusterPeeringRow := result.ClusterPeeringRow

	if clusterPeeringRow == nil {
		result.VolumeReplicationCountOnClusterPeering = 0
		result.FlexCacheVolumeCountOnClusterPeering = 0
		return result, nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("getting replication count for cluster peering row %s", clusterPeeringRow.UUID))
	replicationCount, err := a.SE.GetVolumeReplicationCountByClusterPeerID(ctx, clusterPeeringRow.ID)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("getting FlexCache volume count for cluster peering row %s", clusterPeeringRow.UUID))
	flexCacheCount, err := a.SE.GetFlexCacheVolumeCountByClusterPeerID(ctx, clusterPeeringRow.ID)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	result.VolumeReplicationCountOnClusterPeering = replicationCount
	result.FlexCacheVolumeCountOnClusterPeering = flexCacheCount
	return result, nil
}

func (a FlexCacheVolumeDeleteActivity) UpdateClusterPeeringRowStateDeletedInDBActivity(ctx context.Context,
	result *flexcache.DeleteFlexCacheResult) (*flexcache.DeleteFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	clusterPeeringRow := result.ClusterPeeringRow
	se := a.SE
	clusterPeeringRow.State = datamodel.CvpClusterPeeringStatusDELETED
	recordHeartbeat(ctx, fmt.Sprintf("persisting deleted state for cluster peering row %s", clusterPeeringRow.UUID))
	if err := se.UpdateClusterPeeringRow(ctx, clusterPeeringRow); err != nil {
		logger.Errorf("Failed to update cluster peering row in DB: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Cluster peering row with UUID %s updated to state %s", clusterPeeringRow.UUID, clusterPeeringRow.State)
	return result, nil
}

// CancelPrepopulateJobsForVolume cancels all active prepopulate jobs for a volume.
// This is called during volume deletion to prevent orphaned prepopulate jobs.
// Active jobs (NEW/PROCESSING) are moved to ERROR state, which effectively cancels them
// since jobs in a terminal state (DONE/ERROR) are no longer processed by the background sync workflow.
func (a FlexCacheVolumeDeleteActivity) CancelPrepopulateJobsForVolume(ctx context.Context, volumeUUID string) error {
	logger := utilGetLogger(ctx)
	logger.Infof("Cancelling prepopulate jobs for volume %s", volumeUUID)

	recordHeartbeat(ctx, fmt.Sprintf("cancelling prepopulate jobs for FlexCache volume %s in database", volumeUUID))
	err := a.SE.CancelPrepopulateJobsForVolume(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to cancel prepopulate jobs for volume %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully cancelled prepopulate jobs for volume %s", volumeUUID)
	return nil
}

// CancelFlexCacheCreateWorkflowIfPreparingActivity uses DBVolume from result; if it is a FlexCache volume in
// PREPARING state, cancels the FlexCache create workflow and clears running jobs for the resource.
func (a FlexCacheVolumeDeleteActivity) CancelFlexCacheCreateWorkflowIfPreparingActivity(ctx context.Context, result *flexcache.DeleteFlexCacheResult) error {
	logger := utilGetLogger(ctx)
	dbVolume := result.DBVolume
	recordHeartbeat(ctx, fmt.Sprintf("starting create workflow cancellation check for FlexCache volume %s", dbVolume.UUID))

	if dbVolume.CacheParameters == nil {
		logger.Debugf("skip cancel create workflow for volume %s: not a FlexCache volume", dbVolume.Name)
		return nil
	}
	if dbVolume.State != datamodel.LifeCycleStatePreparing {
		logger.Debugf("cannot cancel create workflow for volume %s as it is not in PREPARING state", dbVolume.Name)
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("getting create job for FlexCache volume %s from database", dbVolume.UUID))
	createJob, err := a.SE.GetJobByResourceUUID(ctx, dbVolume.UUID, string(datamodel.JobTypeFlexCacheCreateVolume))
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Debugf("no create job found for volume %s", dbVolume.Name)
			return nil
		}
		logger.Errorf("error retrieving create job for volume %s: %v", dbVolume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("getting Temporal client to cancel create workflow for FlexCache volume %s", dbVolume.UUID))
	temporalClient := fetchTemporalClientForFlexCacheDelete(ctx)
	// The create job will be DONE at this point but all flexcache creation jobs use the same workflow ID (it is all the same workflow)
	recordHeartbeat(ctx, fmt.Sprintf("cancelling create workflow %s for FlexCache volume %s", createJob.WorkflowID, dbVolume.UUID))
	err = temporalClient.CancelWorkflow(ctx, createJob.WorkflowID, "")
	workflowMissing := false
	if err != nil {
		var notFoundErr *serviceerror.NotFound
		if stderrors.As(err, &notFoundErr) {
			workflowMissing = true
			logger.Debugf("create workflow not found in Temporal (workflowID=%s volume=%s); clearing running jobs for resource",
				createJob.WorkflowID, dbVolume.Name)
		} else {
			logger.Errorf("failed to cancel create workflow %s for volume %s: %v", createJob.WorkflowID, dbVolume.Name, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	recordHeartbeat(ctx, fmt.Sprintf("cancelling running jobs for FlexCache volume %s in database", dbVolume.UUID))
	err = a.SE.CancelRunningJobsForResource(ctx, dbVolume.UUID)
	if err != nil {
		logger.Errorf("failed to cancel running jobs for volume %s: %v", dbVolume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if workflowMissing {
		logger.Infof("cleared running jobs for volume %s (create workflow %s was not running in Temporal)",
			dbVolume.Name, createJob.WorkflowID)
	} else {
		logger.Infof("successfully cancelled create workflow %s for volume %s", createJob.WorkflowID, dbVolume.Name)
	}
	return nil
}

// WaitForFlexCacheCreateWorkflowTerminalActivity polls Temporal until the FlexCache create workflow for this
// volume is no longer RUNNING (or no longer exists). It is a no-op when there is no create job for the
// resource, or when the execution is already absent or terminal.
func (a FlexCacheVolumeDeleteActivity) WaitForFlexCacheCreateWorkflowTerminalActivity(ctx context.Context, result *flexcache.DeleteFlexCacheResult) error {
	logger := utilGetLogger(ctx)
	dbVolume := result.DBVolume

	recordHeartbeat(ctx, fmt.Sprintf("getting create job for FlexCache volume %s from database", dbVolume.UUID))
	createJob, err := a.SE.GetJobByResourceUUID(ctx, dbVolume.UUID, string(datamodel.JobTypeFlexCacheCreateVolume))
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Debugf("skip wait for create workflow for volume %s: no create job", dbVolume.Name)
			return nil
		}
		logger.Errorf("error retrieving create job while waiting for create workflow: volume %s: %v", dbVolume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	wfID := createJob.WorkflowID
	recordHeartbeat(ctx, fmt.Sprintf("getting Temporal client to monitor create workflow %s", wfID))
	temporalClient := fetchTemporalClientForFlexCacheDelete(ctx)
	deadline := time.Now().Add(flexCacheCreateWorkflowTerminalWaitTimeout)

	for {
		if time.Now().After(deadline) {
			return vsaerrors.WrapAsTemporalApplicationError(
				fmt.Errorf("timed out after %s waiting for create workflow %s to reach a terminal state",
					flexCacheCreateWorkflowTerminalWaitTimeout, wfID))
		}

		recordHeartbeat(ctx, fmt.Sprintf("polling create workflow %s in Temporal", wfID))
		desc, derr := temporalClient.DescribeWorkflowExecution(ctx, wfID, "")
		if derr != nil {
			var notFoundErr *serviceerror.NotFound
			if stderrors.As(derr, &notFoundErr) {
				logger.Debugf("create workflow %s no longer present in Temporal; done waiting", wfID)
				return nil
			}
			return vsaerrors.WrapAsTemporalApplicationError(derr)
		}

		info := desc.GetWorkflowExecutionInfo()
		if info == nil {
			return vsaerrors.WrapAsTemporalApplicationError(
				fmt.Errorf("DescribeWorkflowExecution returned nil workflow_execution_info for workflow %s", wfID))
		}

		st := info.Status
		if isFlexCacheCreateWorkflowExecutionTerminal(st) {
			logger.Debugf("create workflow %s reached terminal status %s", wfID, st.String())
			return nil
		}

		recordHeartbeat(ctx, fmt.Sprintf("waiting for create workflow %s status=%s", wfID, st.String()))
		select {
		case <-time.After(flexCacheCreateWorkflowTerminalPollInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
