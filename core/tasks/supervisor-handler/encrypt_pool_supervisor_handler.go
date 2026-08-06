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

// encryptPoolKmsConfigIDAttribute must match the payload attribute key written by the
// encrypt pool factory when it creates the ENCRYPT_POOL job.
const encryptPoolKmsConfigIDAttribute = "kms_config_id"

// EncryptPoolHandler implements Handler for CMEK encrypt pool operations.
//
// EncryptPool claims two resources before its workflow is dispatched: the pool moves to
// UPDATING and the target KMS config moves to MIGRATING. Reverting only the pool leaves
// the KMS config MIGRATING with no workflow to finish it, which blocks every subsequent
// CMEK operation on the account, so this handler releases both.
//
// ENCRYPT_POOL is not in processingStateEligibleJobTypes, so the supervisor only reaches
// this handler for jobs still in NEW: the workflow never started, and neither ONTAP nor
// the pool-to-KMS association was touched.
type EncryptPoolHandler struct{}

// NewEncryptPoolHandler returns the handler that reverts pool and KMS config state for
// stale encrypt pool jobs.
func NewEncryptPoolHandler() *EncryptPoolHandler {
	return &EncryptPoolHandler{}
}

// JobTypes enumerates the job types supported by the encrypt pool handler.
func (h *EncryptPoolHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeEncryptPool,
	}
}

// Handle reverts the pool from UPDATING and the target KMS config from MIGRATING for
// stale encrypt pool jobs.
func (h *EncryptPoolHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if event != EventTimeout {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if err := revertPoolFromUpdating(ctx, job, storage, logger); err != nil {
		return err
	}

	return revertEncryptPoolKmsConfig(ctx, job, storage, logger)
}

// revertEncryptPoolKmsConfig releases the target KMS config that encrypt pool moved to
// MIGRATING. The encrypt pool pre-checks only admit a READY or IN_USE config, and both
// are a function of current SVM usage, so the state is re-derived the same way the KMS
// health check derives it rather than read from a snapshot the job never recorded.
func revertEncryptPoolKmsConfig(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	if job.JobAttributes == nil {
		return nil
	}

	kmsConfigUUID := getStringAttribute(job.JobAttributes.PayloadAttributes, encryptPoolKmsConfigIDAttribute)
	if kmsConfigUUID == "" {
		logger.Warnf("workflow-supervisor-task: encrypt pool job lacks KMS config ID; skipping KMS cleanup")
		return nil
	}

	kmsConfig, err := storage.GetKmsConfig(ctx, kmsConfigUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: KMS config %s not found for encrypt pool cleanup", kmsConfigUUID)
			return nil
		}
		return fmt.Errorf("load KMS config for encrypt pool cleanup: %w", err)
	}

	if kmsConfig.State != datamodel.LifeCycleStateMigrating {
		logger.Warnf("workflow-supervisor-task: KMS config %s not in MIGRATING state (%s); skipping KMS cleanup", kmsConfig.UUID, kmsConfig.State)
		return nil
	}

	inUse, err := storage.IsKmsConfigInUse(ctx, kmsConfig.UUID)
	if err != nil {
		return fmt.Errorf("check whether KMS config %s is in use: %w", kmsConfig.UUID, err)
	}

	state := datamodel.LifeCycleStateREADY
	stateDetails := datamodel.LifeCycleStateReadyDetails
	if inUse {
		state = datamodel.LifeCycleStateInUse
		stateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	if _, err := storage.UpdateKmsConfigState(ctx, kmsConfig.UUID, state, stateDetails); err != nil {
		return fmt.Errorf("revert KMS config %s state to %s: %w", kmsConfig.UUID, state, err)
	}

	logger.Infof("workflow-supervisor-task: reverted KMS config %s from MIGRATING to %s", kmsConfig.UUID, state)
	return nil
}
