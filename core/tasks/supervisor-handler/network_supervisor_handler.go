package supervisorhandler

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// NetworkHandler implements Handler for network/subnet resources.
type NetworkHandler struct{}

// NewNetworkHandler returns the handler for network/subnet job timeout events.
func NewNetworkHandler() *NetworkHandler {
	return &NetworkHandler{}
}

// JobTypes enumerates the job types supported by the network handler.
func (h *NetworkHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateSubnet,
		datamodel.JobTypeCreateLargeSubnet,
	}
}

// Handle logs network job timeout events. No cleanup is performed.
// Note: Subnet jobs don't create a separate resource in VCP database, so there's no VCP database cleanup needed.
func (h *NetworkHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil {
		logger.Warnf("workflow-supervisor-task: network job %s timed out; job lacks job attributes", job.UUID)
		return nil
	}

	if job.JobAttributes.PoolUUID == "" {
		logger.Infof("workflow-supervisor-task: network job %s timed out; no pool UUID associated", job.UUID)
		return nil
	}

	logger.Infof("workflow-supervisor-task: network job %s timed out; associated pool UUID: %s", job.UUID, job.JobAttributes.PoolUUID)
	return nil
}
