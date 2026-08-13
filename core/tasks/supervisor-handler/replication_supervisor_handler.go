package supervisorhandler

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ReplicationHandler implements Handler for volume replication resources.
type ReplicationHandler struct{}

// NewReplicationHandler returns the handler that cleans up replication resources in VCP.
func NewReplicationHandler() *ReplicationHandler {
	return &ReplicationHandler{}
}

// JobTypes enumerates the job types supported by the replication handler.
func (h *ReplicationHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateVolumeReplication,
		datamodel.JobTypeCreateVolumeReplicationInternal,
	}
}

// Handle removes replication artifacts from VCP for the job.
func (h *ReplicationHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: job lacks replication resource UUID; skipping cleanup")
		return nil
	}

	replication, err := storage.GetVolumeReplication(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: replication already deleted in VCP")
			return nil
		}
		return fmt.Errorf("load volume replication: %w", err)
	}
	if replication == nil {
		logger.Warnf("workflow-supervisor-task: replication %s not found; skipping cleanup", job.JobAttributes.ResourceUUID)
		return nil
	}

	if _, err := storage.DeleteVolumeReplication(ctx, replication); err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: replication already deleted in VCP")
			return nil
		}
		return fmt.Errorf("delete volume replication from VCP: %w", err)
	}

	logger.Infof("workflow-supervisor-task: volume replication %s removed from VCP", job.JobAttributes.ResourceUUID)
	return nil
}
