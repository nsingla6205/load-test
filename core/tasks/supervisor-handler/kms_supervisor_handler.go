package supervisorhandler

import (
	"context"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/sde"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	getSignedJwtTokenFn         = auth.GetSignedJwtToken
	deleteSDEKmsConfigurationFn = sde.DeleteSDEKmsConfiguration
	listSDEKmsConfigurationsFn  = sde.ListSDEKmsConfigurations
)

// Event represents a supervisor-detected condition for a workflow.
type Event string

const (
	// EventTimeout indicates that a workflow execution timed out.
	EventTimeout Event = "TIMEOUT"
	// EventTerminated indicates that a workflow was terminated (e.g., due to history size limit).
	EventTerminated Event = "TERMINATED"
	// WorkflowTimeoutDetail records the error detail that accompanies timeout cleanup.
	WorkflowTimeoutDetail = "We could not complete the request. Please try again."
)

// Handler encapsulates compensating actions for supported job types when a supervisor event is detected.
type Handler interface {
	JobTypes() []datamodel.JobType
	Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error
}

// IsActionableEvent returns true if the event requires handler cleanup (timeout or terminated).
func IsActionableEvent(event Event) bool {
	return event == EventTimeout || event == EventTerminated
}

// CmekHandler implements Handler for CMEK resources.
type CmekHandler struct{}

// NewCmekHandler returns the default handler that cleans up CMEK-related resources.
func NewCmekHandler() *CmekHandler {
	return &CmekHandler{}
}

// JobTypes enumerates the job types supported by the CMEK handler.
func (h *CmekHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateKmsConfig,
		datamodel.JobTypeSdeKmsCreate,
	}
}

// Handle removes KMS configuration artifacts both in SDE and VSA for the job.
func (h *CmekHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	correlationID := utils.GetCoRelationIDFromContext(ctx)
	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): correlationID,
	})

	jobType := datamodel.JobType(job.Type)
	switch jobType {
	case datamodel.JobTypeSdeKmsCreate:
		jobCorrelation := job.CorrelationID
		if jobCorrelation == "" {
			jobCorrelation = correlationID
		}
		return h.handleSdeKmsCreateTimeout(ctx, job, logger, jobCorrelation)
	default:
		return h.handleCreateKmsConfigTimeout(ctx, job, storage, logger)
	}
}

func (h *CmekHandler) handleCreateKmsConfigTimeout(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: job lacks resource UUID; skipping CMEK cleanup")
		return nil
	}

	kmsConfig, err := storage.GetKmsConfig(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: kms config already deleted")
			return nil
		}
		return fmt.Errorf("load kms config: %w", err)
	}

	region := strings.TrimSpace(env.Region)
	if region == "" {
		logger.Warnf("workflow-supervisor-task: environment region missing; skipping CMEK cleanup")
		return nil
	}

	deleteParams := &common.DeleteKmsConfigParams{
		KmsConfigID:    kmsConfig.UUID,
		AccountName:    kmsConfig.CustomerProjectID,
		Region:         region,
		XCorrelationID: job.CorrelationID,
	}

	if kmsConfig.KmsAttributes != nil && kmsConfig.KmsAttributes.SdeKmsConfigUUID != "" && deleteParams.AccountName != "" {
		token, tokenErr := getSignedJwtTokenFn(deleteParams.AccountName)
		if tokenErr != nil {
			logger.Warnf("workflow-supervisor-task: unable to fetch auth token for SDE cleanup: %v", tokenErr)
		} else {
			ctxWithToken := context.WithValue(ctx, middleware.AuthorizationToken, token)
			if _, sdeErr := deleteSDEKmsConfigurationFn(ctxWithToken, kmsConfig, deleteParams); sdeErr != nil {
				logger.Warnf("workflow-supervisor-task: SDE cleanup failed: %v", sdeErr)
			}
		}
	} else {
		logger.Debug("workflow-supervisor-task: skipping SDE cleanup (missing attributes)")
	}

	if _, err := storage.DeleteKmsConfig(ctx, kmsConfig.UUID, datamodel.LifeCycleStateError, WorkflowTimeoutDetail); err != nil {
		return fmt.Errorf("delete kms config from VSA: %w", err)
	}

	logger.Infof("workflow-supervisor-task: kms config %s removed from VSA", kmsConfig.UUID)
	return nil
}

func (h *CmekHandler) handleSdeKmsCreateTimeout(ctx context.Context, job *datamodel.Job, logger log.Logger, correlationID string) error {
	if job.ResourceName == "" {
		logger.Warnf("workflow-supervisor-task: job lacks resource name; skipping SDE cleanup")
		return nil
	}
	if job.JobAttributes == nil || job.JobAttributes.PayloadAttributes == nil {
		logger.Warnf("workflow-supervisor-task: job lacks SDE attributes; skipping SDE cleanup")
		return nil
	}

	keyFullPath := getStringAttribute(job.JobAttributes.PayloadAttributes, "keyFullPath")
	if keyFullPath == "" {
		logger.Warnf("workflow-supervisor-task: job lacks key full path; skipping SDE cleanup")
		return nil
	}

	projectNumber := getStringAttribute(job.JobAttributes.PayloadAttributes, "projectNumber")
	locationID := getStringAttribute(job.JobAttributes.PayloadAttributes, "locationId")
	if projectNumber == "" || locationID == "" {
		logger.Warnf("workflow-supervisor-task: job missing SDE project or location metadata; skipping SDE cleanup")
		return nil
	}

	token, err := getSignedJwtTokenFn(projectNumber)
	if err != nil {
		logger.Warnf("workflow-supervisor-task: unable to fetch auth token for SDE cleanup: %v", err)
		return nil
	}

	ctxWithToken := context.WithValue(ctx, middleware.AuthorizationToken, token)
	kmsConfigs, err := listSDEKmsConfigurationsFn(ctxWithToken, projectNumber, locationID, correlationID)
	if err != nil {
		logger.Warnf("workflow-supervisor-task: failed to list SDE kms configs: %v", err)
		return nil
	}

	jobResourceName := strings.TrimSpace(job.ResourceName)
	for _, cfg := range kmsConfigs {
		if cfg.UUID == "" {
			continue
		}
		if cfg.KeyFullPath != keyFullPath {
			logger.With(log.Fields{
				"configUUID": cfg.UUID,
				"kmsKey":     cfg.KeyFullPath,
				"jobKey":     keyFullPath,
			}).Warn("workflow-supervisor-task: skipping SDE cleanup due to key mismatch")
			continue
		}
		if strings.TrimSpace(cfg.ResourceID) != jobResourceName {
			logger.With(log.Fields{
				"sdeResourceID": cfg.ResourceID,
				"jobResourceID": jobResourceName,
				"configUUID":    cfg.UUID,
			}).Warn("workflow-supervisor-task: skipping SDE cleanup due to resource ID mismatch")
			continue
		}

		dmKmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{SdeKmsConfigUUID: cfg.UUID},
		}
		deleteParams := &common.DeleteKmsConfigParams{
			KmsConfigID:    cfg.UUID,
			AccountName:    projectNumber,
			Region:         locationID,
			XCorrelationID: correlationID,
		}

		if _, delErr := deleteSDEKmsConfigurationFn(ctxWithToken, dmKmsConfig, deleteParams); delErr != nil {
			logger.Warnf("workflow-supervisor-task: SDE cleanup failed for config %s: %v", cfg.UUID, delErr)
		} else {
			logger.Infof("workflow-supervisor-task: SDE kms config %s removed for resource %s", cfg.UUID, job.ResourceName)
		}
		break
	}

	return nil
}

func getStringAttribute(attrs map[string]interface{}, key string) string {
	if attrs == nil {
		return ""
	}
	if value, exists := attrs[key]; exists {
		if str, ok := value.(string); ok {
			return strings.TrimSpace(str)
		}
	}
	return ""
}
