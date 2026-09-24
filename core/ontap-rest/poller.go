package ontap_rest

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest/transport"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	t "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	timeout = time.Duration(env.GetUint("ONTAP_REST_ASYNC_POLL_TIMEOUT_MINUTES", 30)) * time.Minute
	wait    = time.Duration(env.GetUint("ONTAP_REST_ASYNC_POLL_WAIT_SECONDS", 3)) * time.Second
	// pollerHeartbeatInterval is the interval at which heartbeats are recorded during ONTAP job polling.
	// Note: Temporal SDK throttles heartbeats to send at most once per (HeartbeatTimeout * 0.8).
	// With the default HeartbeatTimeout of 5 minutes (VOLUME_ACTIVITIES_HEARTBEAT_TIMEOUT_SEC=300),
	// actual heartbeats will be sent every ~4 minutes regardless of this interval.
	pollerHeartbeatInterval     = 1 * time.Minute
	pollWorkflowStartMaxRetries = int(env.GetUint("ONTAP_REST_POLL_WORKFLOW_MAX_RETRIES", 3))
	pollWorkflowStartRetryWait  = time.Duration(env.GetUint("ONTAP_REST_POLL_WORKFLOW_RETRY_WAIT_SECONDS", 10)) * time.Second
)

// Poller describes a poller that polls a job
type Poller interface { // generate:mock
	Poll(UUID string) error
}

type poller struct {
	api          cluster.ClientService
	logger       log.Logger
	clientParams RESTClientParams
}

var fetchTemporalClient = _fetchTemporalClient
var recordHeartbeat = activity.RecordHeartbeat

func _fetchTemporalClient(ctx context.Context) client.Client {
	return activity.GetClient(ctx)
}

// Poll polls an ontap job given UUID.
// When called from a Temporal activity (Google proxy / VCP worker), it starts the
// PollOntapJob child workflow so the parent activity can heartbeat while waiting.
// Otherwise (e.g. VCM go-workflows), it polls ONTAP inline via PollOntapJobDirectly.
func (p *poller) Poll(UUID string) error {
	ctx := p.clientParams.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if !activity.IsActivity(ctx) {
		return pollOntapJobDirectly(ctx, p.clientParams, UUID, p.logger)
	}

	tempClient := fetchTemporalClient(ctx)

	startOpts := client.StartWorkflowOptions{
		ID:                       "ontap-rest-job-poll-" + UUID,
		TaskQueue:                temporal.CustomerTaskQueue,
		WorkflowExecutionTimeout: timeout,
	}

	var fut client.WorkflowRun
	var err error
	for attempt := 1; attempt <= pollWorkflowStartMaxRetries; attempt++ {
		fut, err = tempClient.ExecuteWorkflow(ctx, startOpts, PollOntapJob, p.clientParams, UUID)
		if err == nil {
			break
		}
		p.logger.Errorf("Failed to start job poll workflow for UUID %s (attempt %d/%d), error: %v", UUID, attempt, pollWorkflowStartMaxRetries, err)
		if attempt < pollWorkflowStartMaxRetries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pollWorkflowStartRetryWait):
			}
		}
	}
	if err != nil {
		return errors.New("failed to start ontap-rest job poll workflow")
	}

	// Spawn goroutine to record heartbeats while waiting for the polling workflow.
	// This ensures that activities using Poll() don't get killed by HeartbeatTimeout
	// during long-running ONTAP operations (e.g., large volume creation).
	// Note: Temporal SDK throttles heartbeats to send at most once per (HeartbeatTimeout * 0.8).
	heartbeatMsg := "Polling ONTAP job: " + UUID
	done := make(chan struct{})
	go func() {
		// Record initial heartbeat immediately
		recordHeartbeat(ctx, heartbeatMsg)

		ticker := time.NewTicker(pollerHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				recordHeartbeat(ctx, heartbeatMsg)
			}
		}
	}()

	// Non-blocking wait for the workflow to complete.
	// Temporal automatically retries the workflow per the RetryPolicy above.
	err = fut.Get(ctx, nil)
	close(done) // Stop heartbeat goroutine
	if err != nil {
		p.logger.Errorf("Failed to poll job with UUID %s, error: %v", UUID, err)
		// Extract the original ONTAP error from the Temporal error chain so
		// callers (e.g. error classifiers) can inspect the real failure reason.
		var appErr *t.ApplicationError
		if stderrors.As(err, &appErr) {
			return errors.New(appErr.Message())
		}
		return errors.New("failed to poll ontap-rest job")
	}

	return nil
}

func pollOntapJobDirectly(ctx context.Context, clientParams RESTClientParams, jobUUID string, logger log.Logger) error {
	clientParams.Trace = logger
	clientParams.Ctx = ctx
	client, err := NewOntapRestClient(clientParams)
	if err != nil {
		logger.Errorf("Failed to create Ontap REST client for direct job poll, error: %v", err)
		return errors.New("failed to create ontap-rest client for direct job poll")
	}
	if err := PollOntapJobDirectly(ctx, client, jobUUID, timeout, wait, logger); err != nil {
		logger.Errorf("Failed to poll job with UUID %s directly, error: %v", jobUUID, err)
		return err
	}
	return nil
}

var workflowSleep = workflow.Sleep

// PollOntapJobActivity is an activity that polls a single ONTAP job
func PollOntapJobActivity(ctx context.Context, clientParams RESTClientParams, UUID string) (*models.Job, error) {
	logger := util.GetLogger(ctx)
	clientParams.Trace = logger
	clientParams.Ctx = ctx
	api, err := NewOntapRestClient(clientParams)
	if err != nil {
		logger.Errorf("Failed to create Ontap REST client, error: %v", err)
		return nil, errors.NewNonRetryableErr("failed to create ontap-rest client")
	}

	rsp, err := api.Cluster().GetJob(UUID)
	if err != nil {
		logger.Errorf("Failed to poll job for UUID %s, error: %v", UUID, err)
		return nil, errors.NewNonRetryableErr("failed to poll job")
	}

	if *rsp.Payload.State == models.JobStateFailure {
		return nil, errors.NewNonRetryableErr(transport.ConvertFromRESTError(logger, rsp).Error())
	}

	if *rsp.Payload.State == models.JobStateSuccess {
		return nil, nil
	}

	return nil, errors.New("Job is still processing")
}

// PollOntapJob is a workflow that polls an ONTAP REST job until it is either successful or failed.
func PollOntapJob(ctx workflow.Context, clientParams RESTClientParams, UUID string) error {
	logger := util.GetLogger(ctx)
	// Since logger is not serializable, it becomes nil when sent as workflow param.
	// Hence, we need to fetch and set it again in the clientParams.
	clientParams.Trace = logger

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy: &t.RetryPolicy{
			InitialInterval:        wait,
			BackoffCoefficient:     0,
			MaximumInterval:        wait,
			NonRetryableErrorTypes: []string{"NonRetryableErr", "PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	err := workflow.ExecuteActivity(ctx, PollOntapJobActivity, clientParams, UUID).Get(ctx, nil)
	if err != nil {
		logger.Errorf("Failed to poll job for UUID %s, error: %v", UUID, err)
		return err
	}

	return nil
}
