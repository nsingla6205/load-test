package backgroundactivities

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
)

type WorkflowMapping struct {
	workflowFunc interface{}
	getArgsFunc  OrphanJobWorkflowManager
	taskQueue    string
	timeout      time.Duration
}

type OrphanJobWorkflowManager interface {
	PrepareWorkflowArgs(ctx context.Context, se database.Storage, job *datamodel.Job) ([]interface{}, error)
	FailedWorkflowJob(ctx context.Context, se database.Storage, job *datamodel.Job, reason string) error
}

var (
	jobTypeToWorkflowMapping = map[datamodel.JobType]WorkflowMapping{
		datamodel.JobTypeCreateKmsConfig: {
			workflowFunc: "CreateKmsConfigWorkflow",
			getArgsFunc:  &CreateKmsConfigArgs{},
			taskQueue:    workflowengine.CustomerTaskQueue,
			timeout:      workflowengine.GetWorkflowGlobalTimeout(),
		},
		datamodel.JobTypeDeleteKmsConfig: {
			workflowFunc: "DeleteKmsConfigWorkflow",
			getArgsFunc:  &DeleteKmsConfigArgs{},
			taskQueue:    workflowengine.CustomerTaskQueue,
			timeout:      workflowengine.GetWorkflowGlobalTimeout(),
		},
		datamodel.JobTypeSplitVolume: {
			workflowFunc: "VolumePollSplitWorkflow",
			getArgsFunc:  &SplitVolumeArgs{},
			taskQueue:    workflowengine.BackgroundTaskQueue,
			timeout:      *workflowengine.GetSplitVolumeWorkflowTimeout(),
		},
	}
	processSingleJob           = _processSingleJob
	orphanJobProcessingEnabled = env.GetBool("ORPHAN_JOB_PROCESSING_ENABLED", true)
)

type OrphanJobActivity struct {
	SE             database.Storage
	TemporalClient client.Client
}

// OrphanJobsActivity finds all pending jobs and triggers appropriate workflows
func (p *OrphanJobActivity) OrphanJobsActivity(ctx context.Context) error {
	logger := util.GetLogger(ctx)
	if !orphanJobProcessingEnabled {
		logger.Debug("Orphan job processing is disabled. Exiting activity.")
		return nil
	}
	if p.TemporalClient == nil {
		p.TemporalClient = activity.GetClient(ctx)
	}
	logger.Infof("Starting OrphanJobsActivity")

	// Create filter for pending jobs
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("state", "=", datamodel.JobsStateWaitForTemporal),
	)

	// Fetch all pending jobs
	pendingJobs, err := p.SE.GetJobsWithCondition(ctx, *filter)
	if err != nil {
		logger.Errorf("Failed to fetch pending jobs: %v", err)
		return fmt.Errorf("failed to fetch pending jobs: %w", err)
	}

	logger.Infof("Found %d pending jobs to process", len(pendingJobs))
	// Process each pending job
	for _, job := range pendingJobs {
		err := processSingleJob(ctx, p.SE, job, p.TemporalClient)
		if err != nil {
			// let job remain in pending state for retry, and keep processing other jobs
			logger.Errorf("Failed to process job %s (type: %s): %v", job.UUID, job.Type, err)
			continue
		}
		logger.Infof("Successfully triggered workflow for job %s (type: %s)", job.UUID, job.Type)
	}

	logger.Infof("OrphanJobsActivity completed")
	return nil
}

// _processSingleJob processes a single pending job by triggering the appropriate workflow
func _processSingleJob(ctx context.Context, se database.Storage, job *datamodel.Job, temporalClient client.Client) error {
	logger := util.GetLogger(ctx)
	incrementRetryCount(ctx, se, job)

	// Find the appropriate workflow for this job type
	workflowMapping, exists := jobTypeToWorkflowMapping[datamodel.JobType(job.Type)]
	if !exists {
		logger.Warnf("No workflow mapping found for job type: %s", job.Type)
		return fmt.Errorf("no workflow mapping found")
	}
	getArgsFunc := workflowMapping.getArgsFunc.(OrphanJobWorkflowManager)

	// Check if we've exceeded retry count
	if job.JobAttributes.CurrentRetryCount >= models.WaitForTemporalJobMaxRetryCount {
		errMsg := fmt.Sprintf("Job %s has exceeded max retry count (%d)", job.UUID, models.WaitForTemporalJobMaxRetryCount)
		logger.Warn(errMsg)
		// mark the job resource as error
		updateErr := se.UpdateJob(ctx, job.UUID, string(datamodel.JobsStateERROR), job.TrackingID, errMsg)
		if updateErr != nil {
			logger.Error("Failed to update job to temporal pending state", "error", updateErr)
		}
		// Call FailedWorkflowJob to handle job failure
		err := getArgsFunc.FailedWorkflowJob(ctx, se, job, errMsg)
		if err != nil {
			return err
		}
		return err
	}

	// Prepare workflow arguments using the PrepareWorkflowArgs function
	workflowArgs, err := getArgsFunc.PrepareWorkflowArgs(ctx, se, job)
	if err != nil {
		logger.Errorf("Failed to prepare workflow arguments for job %s: %v", job.UUID, err)
		return fmt.Errorf("failed to prepare workflow arguments: %w", err)
	}

	// Create workflow options
	workflowOptions := client.StartWorkflowOptions{
		TaskQueue:             workflowMapping.taskQueue,
		ID:                    job.WorkflowID,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		WorkflowRunTimeout:    workflowMapping.timeout,
	}
	// Start the workflow
	workflowRun, err := temporalClient.ExecuteWorkflow(ctx, workflowOptions, workflowMapping.workflowFunc, workflowArgs...)
	if err != nil {
		logger.Errorf("Failed to start workflow for orphaned job %s: %v", job.UUID, err)
		updateErr := se.UpdateJob(ctx, job.UUID, string(datamodel.JobsStateWaitForTemporal), job.TrackingID, err.Error())
		if updateErr != nil {
			logger.Errorf("Failed to update job %s tracking ID: %v", job.UUID, updateErr)
		}
	}

	if err == nil {
		logger.Infof("Successfully started workflow %s for job %s (type: %s)", workflowRun.GetID(), job.UUID, job.Type)
	}
	return err
}

func incrementRetryCount(ctx context.Context, se database.Storage, job *datamodel.Job) {
	// Increment retry count
	logger := util.GetLogger(ctx)
	newCount := job.JobAttributes.CurrentRetryCount + 1
	jobAttributes := job.JobAttributes
	jobAttributes.CurrentRetryCount = newCount
	updateErr := se.UpdateJobAttributes(ctx, job.UUID, jobAttributes)
	if updateErr != nil {
		logger.Errorf("Failed to update job %s retry count: %v", job.UUID, updateErr)
	} else {
		logger.Infof("Incremented retry count for job %s to %d", job.UUID, newCount)
	}
}
