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

// HostGroupUpdateHandler implements Handler for host group update operations.
type HostGroupUpdateHandler struct{}

// NewHostGroupUpdateHandler returns the handler that recovers host group state for stale update jobs.
func NewHostGroupUpdateHandler() *HostGroupUpdateHandler {
	return &HostGroupUpdateHandler{}
}

// JobTypes enumerates the job types supported by the host group update handler.
func (h *HostGroupUpdateHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeUpdateHostGroup,
	}
}

// Handle recovers a host group left in UPDATING by a stale update job. For NEW state
// jobs (workflow never started) it reverts to the previous state; for PROCESSING state
// jobs (workflow started then timed out) it transitions the host group to ERROR.
func (h *HostGroupUpdateHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if event != EventTimeout {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		"jobState":                              job.State,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: update job lacks host group resource UUID; skipping cleanup")
		return nil
	}

	if !job.AccountID.Valid {
		logger.Warnf("workflow-supervisor-task: update job lacks account id; skipping host group cleanup")
		return nil
	}
	accountID := job.AccountID.Int64

	hostGroup, err := storage.GetHostGroup(ctx, job.JobAttributes.ResourceUUID, accountID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: host group not found for update cleanup")
			return nil
		}
		return fmt.Errorf("load host group for update cleanup: %w", err)
	}

	// Only recover if the host group is still in the transitional UPDATING state.
	if hostGroup.State != datamodel.LifeCycleStateUpdating {
		logger.Infof("workflow-supervisor-task: host group %s not in UPDATING state (%s); skipping update cleanup", hostGroup.UUID, hostGroup.State)
		return nil
	}

	// PROCESSING timeout: the workflow started but was terminated; the igroup sync may
	// have partially applied, so surface the failure as ERROR rather than reverting.
	if job.State == string(datamodel.JobsStatePROCESSING) {
		if err := storage.UpdateHostGroupsState(ctx, []string{hostGroup.UUID}, accountID, datamodel.LifeCycleStateError, datamodel.LifeCycleStateUpdateErrorDetails); err != nil {
			return fmt.Errorf("set host group %s state to ERROR: %w", hostGroup.UUID, err)
		}
		logger.Infof("workflow-supervisor-task: host group %s transitioned from UPDATING to ERROR due to workflow timeout", hostGroup.UUID)
		return nil
	}

	// NEW timeout: the workflow never started, so no ONTAP changes occurred. Revert to
	// the state captured before the update, falling back to READY for older jobs.
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails
	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not stored for host group %s, defaulting to READY", hostGroup.UUID)
		previousState = datamodel.LifeCycleStateREADY
		previousStateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	if err := storage.UpdateHostGroupsState(ctx, []string{hostGroup.UUID}, accountID, previousState, previousStateDetails); err != nil {
		return fmt.Errorf("revert host group %s state to %s: %w", hostGroup.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted host group %s from UPDATING to %s", hostGroup.UUID, previousState)
	return nil
}
