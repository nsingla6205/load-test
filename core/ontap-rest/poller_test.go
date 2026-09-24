package ontap_rest

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
	temporalsdk "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestPollOntapJob_Workflow_Success(t *testing.T) {
	origWait := wait
	wait = 10 * time.Millisecond
	defer func() { wait = origWait }()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetTestTimeout(30 * time.Second) // Set test timeout to prevent deadlock detection
	env.RegisterWorkflow(PollOntapJob)
	env.RegisterActivity(PollOntapJobActivity)

	// Mock the activity to return success
	env.OnActivity(PollOntapJobActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	clientParams := RESTClientParams{}
	uuid := "job-uuid"

	env.ExecuteWorkflow(PollOntapJob, clientParams, uuid)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestPollOntapJob_Workflow_Error(t *testing.T) {
	origWait := wait
	wait = 10 * time.Millisecond
	defer func() { wait = origWait }()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollOntapJob)
	env.RegisterActivity(PollOntapJobActivity)

	// Mock the activity to return an error
	env.OnActivity(PollOntapJobActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("mock error"))

	clientParams := RESTClientParams{}
	uuid := "job-uuid"

	env.ExecuteWorkflow(PollOntapJob, clientParams, uuid)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestPollOntapJob_Workflow_Timeout(t *testing.T) {
	origWait := wait
	wait = 10 * time.Millisecond
	defer func() { wait = origWait }()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollOntapJob)
	env.RegisterActivity(PollOntapJobActivity)

	// Mock the activity to return an error for in-progress state
	env.OnActivity(PollOntapJobActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("Job is still processing"))

	clientParams := RESTClientParams{}
	uuid := "job-uuid"

	env.ExecuteWorkflow(PollOntapJob, clientParams, uuid)

	// The workflow should complete with an error due to retry policy
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestPollOntapJob_Workflow_NewOntapRestClient_Error(t *testing.T) {
	origWait := wait
	wait = 10 * time.Millisecond
	defer func() { wait = origWait }()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollOntapJob)
	env.RegisterActivity(PollOntapJobActivity)

	clientParams := RESTClientParams{}
	uuid := "job-uuid"

	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		return nil, errors.New("client creation failed")
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	env.ExecuteWorkflow(PollOntapJob, clientParams, uuid)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "failed to create ontap-rest client")
}

func TestPollOntapJob_Workflow_JobFail(t *testing.T) {
	origWait := wait
	wait = 10 * time.Millisecond
	defer func() { wait = origWait }()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollOntapJob)
	env.RegisterActivity(PollOntapJobActivity)

	// Mock the activity to return an error for job failure
	env.OnActivity(PollOntapJobActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("job failed"))

	clientParams := RESTClientParams{}
	uuid := "job-uuid"

	env.ExecuteWorkflow(PollOntapJob, clientParams, uuid)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestPollOntapJob_Workflow_OverrideTimeout(t *testing.T) {
	origWait := wait
	wait = 10 * time.Millisecond
	defer func() { wait = origWait }()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollOntapJob)
	env.RegisterActivity(PollOntapJobActivity)

	// Override the timeout variable for this test
	origTimeout := timeout
	timeout = 100 * time.Millisecond
	defer func() { timeout = origTimeout }()

	// Mock the activity to return success
	env.OnActivity(PollOntapJobActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	clientParams := RESTClientParams{}
	uuid := "job-uuid"

	env.ExecuteWorkflow(PollOntapJob, clientParams, uuid)

	// The workflow should complete successfully
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestPollOntapJob_Workflow_WorkflowSleepError(t *testing.T) {
	origWait := wait
	wait = 10 * time.Millisecond
	defer func() { wait = origWait }()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollOntapJob)
	env.RegisterActivity(PollOntapJobActivity)

	// Override workflowSleep to return an error
	origWorkflowSleep := workflowSleep
	workflowSleep = func(ctx workflow.Context, d time.Duration) error {
		return errors.New("workflow sleep error")
	}
	defer func() { workflowSleep = origWorkflowSleep }()

	// Mock the activity to return success
	env.OnActivity(PollOntapJobActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	clientParams := RESTClientParams{}
	uuid := "job-uuid"

	env.ExecuteWorkflow(PollOntapJob, clientParams, uuid)

	// The current PollOntapJob workflow doesn't use workflowSleep, so it should complete successfully
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// workflowTest calls the Poll function from within a Temporal workflow.
func workflowTest(ctx workflow.Context, clientParams RESTClientParams, uuid string) error {
	// Execute the activityTest function within the workflow context.
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	return workflow.ExecuteActivity(ctx, activityTest, clientParams, uuid).Get(ctx, nil)
}

// activityTest calls the Poll function from within a Temporal activity.
func activityTest(ctx context.Context, clientParams RESTClientParams, uuid string) error {
	clientParams.Ctx = ctx
	p := &poller{
		clientParams: clientParams,
		logger:       log.NewLogger(),
	}
	return p.Poll(uuid)
}

type mockFuture struct {
	error bool
}

func (m *mockFuture) GetID() string {
	return ""
}
func (m *mockFuture) GetRunID() string {
	return ""
}
func (m *mockFuture) GetWithOptions(ctx context.Context, valuePtr interface{}, options client.WorkflowRunGetOptions) error {
	return nil
}
func (m *mockFuture) Get(ctx context.Context, result interface{}) error {
	if m != nil && m.error {
		return errors.New("mock future error")
	}
	return nil
}

type blockingFuture struct {
	done chan struct{}
	err  error
}

func (b *blockingFuture) GetID() string {
	return ""
}
func (b *blockingFuture) GetRunID() string {
	return ""
}
func (b *blockingFuture) GetWithOptions(ctx context.Context, valuePtr interface{}, options client.WorkflowRunGetOptions) error {
	return nil
}
func (b *blockingFuture) Get(ctx context.Context, result interface{}) error {
	<-b.done
	return b.err
}

func TestPoll_PollOntapJobWorkflow_Success(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	mockTemp := workflow_engine.NewMockTemporalTestClient(t)

	origFetchTemporalClient := fetchTemporalClient
	// Patch fetchTemporalClient to return mockOntap
	fetchTemporalClient = func(ctx context.Context) client.Client {
		return mockTemp
	}
	defer func() {
		fetchTemporalClient = origFetchTemporalClient
	}()

	var mockFut *mockFuture
	mockTemp.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockFut, nil)

	// Register the workflow
	env.RegisterActivity(activityTest)
	env.ExecuteWorkflow(workflowTest, RESTClientParams{}, "job-uuid")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestPoll_PollOntapJobWorkflow_RecordsHeartbeat(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	mockTemp := workflow_engine.NewMockTemporalTestClient(t)

	origFetchTemporalClient := fetchTemporalClient
	fetchTemporalClient = func(ctx context.Context) client.Client {
		return mockTemp
	}
	defer func() {
		fetchTemporalClient = origFetchTemporalClient
	}()

	var heartbeatCount int32
	origRecordHeartbeat := recordHeartbeat
	recordHeartbeat = func(ctx context.Context, details ...interface{}) {
		atomic.AddInt32(&heartbeatCount, 1)
	}
	defer func() {
		recordHeartbeat = origRecordHeartbeat
	}()

	origHeartbeatInterval := pollerHeartbeatInterval
	pollerHeartbeatInterval = 5 * time.Millisecond
	defer func() {
		pollerHeartbeatInterval = origHeartbeatInterval
	}()

	done := make(chan struct{})
	mockFut := &blockingFuture{done: done}
	mockTemp.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockFut, nil)

	go func() {
		for {
			if atomic.LoadInt32(&heartbeatCount) > 0 {
				close(done)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	env.RegisterActivity(activityTest)
	env.ExecuteWorkflow(workflowTest, RESTClientParams{}, "job-uuid")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.GreaterOrEqual(t, atomic.LoadInt32(&heartbeatCount), int32(1))
}

func TestPoll_PollOntapJobWorkflow_Error(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	mockTemp := workflow_engine.NewMockTemporalTestClient(t)

	origFetchTemporalClient := fetchTemporalClient
	fetchTemporalClient = func(ctx context.Context) client.Client {
		return mockTemp
	}
	origStartMaxRetries := pollWorkflowStartMaxRetries
	pollWorkflowStartMaxRetries = 1
	defer func() {
		fetchTemporalClient = origFetchTemporalClient
		pollWorkflowStartMaxRetries = origStartMaxRetries
	}()

	mockTemp.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("test error"))

	env.RegisterActivity(activityTest)
	env.ExecuteWorkflow(workflowTest, RESTClientParams{}, "job-uuid")

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestPoll_PollOntapJobWorkflow_StartRetriesAndSucceeds(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	mockTemp := workflow_engine.NewMockTemporalTestClient(t)

	origFetchTemporalClient := fetchTemporalClient
	fetchTemporalClient = func(ctx context.Context) client.Client {
		return mockTemp
	}
	origStartMaxRetries := pollWorkflowStartMaxRetries
	pollWorkflowStartMaxRetries = 3
	origStartRetryWait := pollWorkflowStartRetryWait
	pollWorkflowStartRetryWait = 0
	defer func() {
		fetchTemporalClient = origFetchTemporalClient
		pollWorkflowStartMaxRetries = origStartMaxRetries
		pollWorkflowStartRetryWait = origStartRetryWait
	}()

	var successFut *mockFuture
	mockTemp.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("transient error")).Once()
	mockTemp.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(successFut, nil).Once()

	env.RegisterActivity(activityTest)
	env.ExecuteWorkflow(workflowTest, RESTClientParams{}, "job-uuid")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestPoll_PollOntapJobWorkflow_FutureError(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	mockTemp := workflow_engine.NewMockTemporalTestClient(t)

	origFetchTemporalClient := fetchTemporalClient
	fetchTemporalClient = func(ctx context.Context) client.Client {
		return mockTemp
	}
	defer func() {
		fetchTemporalClient = origFetchTemporalClient
	}()

	mockFut := &mockFuture{
		error: true,
	}
	mockTemp.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockFut, nil)

	env.RegisterActivity(activityTest)
	env.ExecuteWorkflow(workflowTest, RESTClientParams{}, "job-uuid")

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

type appErrFuture struct {
	msg string
}

func (f *appErrFuture) GetID() string    { return "" }
func (f *appErrFuture) GetRunID() string { return "" }
func (f *appErrFuture) GetWithOptions(_ context.Context, _ interface{}, _ client.WorkflowRunGetOptions) error {
	return nil
}
func (f *appErrFuture) Get(_ context.Context, _ interface{}) error {
	return temporalsdk.NewApplicationError(f.msg, "NonRetryableErr", nil)
}

func TestPoll_PollOntapJobWorkflow_FutureApplicationError_PreservesOriginalMessage(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	mockTemp := workflow_engine.NewMockTemporalTestClient(t)

	origFetchTemporalClient := fetchTemporalClient
	fetchTemporalClient = func(ctx context.Context) client.Client {
		return mockTemp
	}
	origRecordHeartbeat := recordHeartbeat
	recordHeartbeat = func(_ context.Context, _ ...interface{}) {}
	defer func() {
		fetchTemporalClient = origFetchTemporalClient
		recordHeartbeat = origRecordHeartbeat
	}()

	const ontapErrMsg = "Failed to create the Active Directory machine account. Reason: Unable to connect to any domain controllers."
	mockTemp.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&appErrFuture{msg: ontapErrMsg}, nil)

	env.RegisterActivity(activityTest)
	env.ExecuteWorkflow(workflowTest, RESTClientParams{}, "job-uuid")

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	require.Contains(t, err.Error(), ontapErrMsg)
}

func TestPoll_PollOntapJobWorkflow_FutureNonApplicationError_FallsBack(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	mockTemp := workflow_engine.NewMockTemporalTestClient(t)

	origFetchTemporalClient := fetchTemporalClient
	fetchTemporalClient = func(ctx context.Context) client.Client {
		return mockTemp
	}
	defer func() {
		fetchTemporalClient = origFetchTemporalClient
	}()

	mockFut := &mockFuture{error: true}
	mockTemp.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockFut, nil)

	env.RegisterActivity(activityTest)
	env.ExecuteWorkflow(workflowTest, RESTClientParams{}, "job-uuid")

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to poll ontap-rest job")
}

// pollActivityOutcome captures PollOntapJobActivity results from a helper
// goroutine so testify assertions run on the test goroutine.
type pollActivityOutcome struct {
	result   *models.Job
	err      error
	panicked bool
}

func runPollActivityInMockGoroutine(
	mcs *cluster.MockClientService,
	fn func() (*models.Job, error),
) <-chan pollActivityOutcome {
	out := make(chan pollActivityOutcome, 1)
	go func() {
		defer mcs.MockClientServiceDone()
		var outcome pollActivityOutcome
		defer func() {
			if r := recover(); r != nil {
				outcome.panicked = true
			}
			out <- outcome
		}()
		outcome.result, outcome.err = fn()
	}()
	return out
}

func TestPoll_NonActivityContext_UsesDirectPoll(t *testing.T) {
	mcs := cluster.NewMockClientService(t)
	mockOntap := &OntapRestClient{cluster: &clusterClient{api: mcs}}

	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		return mockOntap, nil
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	jobResponse := &cluster.JobGetOK{
		Payload: &models.Job{
			State: nillable.ToPointer(models.JobStateSuccess),
		},
	}

	out := runPollActivityInMockGoroutine(mcs, func() (*models.Job, error) {
		return nil, activityTest(context.Background(), RESTClientParams{}, "job-uuid")
	})

	mcs.AssertJobGet(cluster.NewJobGetParams().WithUUID("job-uuid").WithFields([]string{"*", "node.name"}), nil, nil, jobResponse, nil)
	mcs.AssertMockClientServiceDone()

	outcome := <-out
	require.False(t, outcome.panicked)
	require.NoError(t, outcome.err)
}

func TestPollOntapJobActivity_Success(t *testing.T) {
	ctx := context.Background()
	clientParams := RESTClientParams{}
	uuid := "test-job-uuid"

	// Mock NewOntapRestClient to return a mock client
	mcs := cluster.NewMockClientService(t)
	mockOntap := &OntapRestClient{cluster: &clusterClient{api: mcs}}

	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		return mockOntap, nil
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	// Mock successful job response
	jobResponse := &cluster.JobGetOK{
		Payload: &models.Job{
			State: nillable.ToPointer(models.JobStateSuccess),
		},
	}

	out := runPollActivityInMockGoroutine(mcs, func() (*models.Job, error) {
		return PollOntapJobActivity(ctx, clientParams, uuid)
	})

	mcs.AssertJobGet(cluster.NewJobGetParams().WithUUID(uuid).WithFields([]string{"*", "node.name"}), nil, nil, jobResponse, nil)
	mcs.AssertMockClientServiceDone()

	outcome := <-out
	require.False(t, outcome.panicked)
	require.NoError(t, outcome.err)
	require.Nil(t, outcome.result)
}

func TestPollOntapJobActivity_Failure(t *testing.T) {
	ctx := context.Background()
	clientParams := RESTClientParams{}
	uuid := "test-job-uuid"

	// Mock NewOntapRestClient to return a mock client
	mcs := cluster.NewMockClientService(t)
	mockOntap := &OntapRestClient{cluster: &clusterClient{api: mcs}}

	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		return mockOntap, nil
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	// Mock failed job response
	jobResponse := &cluster.JobGetOK{
		Payload: &models.Job{
			State: nillable.ToPointer(models.JobStateFailure),
		},
	}

	out := runPollActivityInMockGoroutine(mcs, func() (*models.Job, error) {
		return PollOntapJobActivity(ctx, clientParams, uuid)
	})

	mcs.AssertJobGet(cluster.NewJobGetParams().WithUUID(uuid).WithFields([]string{"*", "node.name"}), nil, nil, jobResponse, nil)
	mcs.AssertMockClientServiceDone()

	outcome := <-out
	require.False(t, outcome.panicked)
	require.Error(t, outcome.err)
	require.Nil(t, outcome.result)
	require.Contains(t, outcome.err.Error(), "jobGetOK")
}

func TestPollOntapJobActivity_InProgress(t *testing.T) {
	ctx := context.Background()
	clientParams := RESTClientParams{}
	uuid := "test-job-uuid"

	// Mock NewOntapRestClient to return a mock client
	mcs := cluster.NewMockClientService(t)
	mockOntap := &OntapRestClient{cluster: &clusterClient{api: mcs}}

	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		return mockOntap, nil
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	// Mock in-progress job response
	jobResponse := &cluster.JobGetOK{
		Payload: &models.Job{
			State: nillable.ToPointer("in-progress"),
		},
	}

	out := runPollActivityInMockGoroutine(mcs, func() (*models.Job, error) {
		return PollOntapJobActivity(ctx, clientParams, uuid)
	})

	mcs.AssertJobGet(cluster.NewJobGetParams().WithUUID(uuid).WithFields([]string{"*", "node.name"}), nil, nil, jobResponse, nil)
	mcs.AssertMockClientServiceDone()

	outcome := <-out
	require.False(t, outcome.panicked)
	require.Error(t, outcome.err)
	require.Nil(t, outcome.result)
	require.Contains(t, outcome.err.Error(), "Job is still processing")
}

func TestPollOntapJobActivity_ClientCreationError(t *testing.T) {
	ctx := context.Background()
	clientParams := RESTClientParams{}
	uuid := "test-job-uuid"

	// Mock NewOntapRestClient to return an error
	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		return nil, errors.New("client creation failed")
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	// Call the activity
	result, err := PollOntapJobActivity(ctx, clientParams, uuid)

	// Verify results
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "failed to create ontap-rest client")
}

func TestPollOntapJobActivity_GetJobError(t *testing.T) {
	ctx := context.Background()
	clientParams := RESTClientParams{}
	uuid := "test-job-uuid"

	// Mock NewOntapRestClient to return a mock client
	mcs := cluster.NewMockClientService(t)
	mockOntap := &OntapRestClient{cluster: &clusterClient{api: mcs}}

	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		return mockOntap, nil
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	out := runPollActivityInMockGoroutine(mcs, func() (*models.Job, error) {
		return PollOntapJobActivity(ctx, clientParams, uuid)
	})

	// Mock GetJob to return an error
	mcs.AssertJobGet(cluster.NewJobGetParams().WithUUID(uuid).WithFields([]string{"*", "node.name"}), nil, nil, nil, errors.New("job not found"))
	mcs.AssertMockClientServiceDone()

	outcome := <-out
	require.False(t, outcome.panicked)
	require.Error(t, outcome.err)
	require.Nil(t, outcome.result)
	require.Contains(t, outcome.err.Error(), "failed to poll job")
}

func TestPollOntapJobActivity_NilJobResponse(t *testing.T) {
	ctx := context.Background()
	clientParams := RESTClientParams{}
	uuid := "test-job-uuid"

	// Mock NewOntapRestClient to return a mock client
	mcs := cluster.NewMockClientService(t)
	mockOntap := &OntapRestClient{cluster: &clusterClient{api: mcs}}

	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		return mockOntap, nil
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	// Mock successful job response but with nil payload
	jobResponse := &cluster.JobGetOK{
		Payload: nil,
	}

	out := runPollActivityInMockGoroutine(mcs, func() (*models.Job, error) {
		return PollOntapJobActivity(ctx, clientParams, uuid)
	})

	mcs.AssertJobGet(cluster.NewJobGetParams().WithUUID(uuid).WithFields([]string{"*", "node.name"}), nil, nil, jobResponse, nil)
	mcs.AssertMockClientServiceDone()

	outcome := <-out
	require.True(t, outcome.panicked, "nil job payload should panic")
}

func TestPollOntapJobActivity_NilJobState(t *testing.T) {
	ctx := context.Background()
	clientParams := RESTClientParams{}
	uuid := "test-job-uuid"

	// Mock NewOntapRestClient to return a mock client
	mcs := cluster.NewMockClientService(t)
	mockOntap := &OntapRestClient{cluster: &clusterClient{api: mcs}}

	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		return mockOntap, nil
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	// Mock job response with nil state
	jobResponse := &cluster.JobGetOK{
		Payload: &models.Job{
			State: nil,
		},
	}

	out := runPollActivityInMockGoroutine(mcs, func() (*models.Job, error) {
		return PollOntapJobActivity(ctx, clientParams, uuid)
	})

	mcs.AssertJobGet(cluster.NewJobGetParams().WithUUID(uuid).WithFields([]string{"*", "node.name"}), nil, nil, jobResponse, nil)
	mcs.AssertMockClientServiceDone()

	outcome := <-out
	require.True(t, outcome.panicked, "nil job state should panic")
}

func TestPollOntapJobActivity_WithLogger(t *testing.T) {
	ctx := context.Background()
	clientParams := RESTClientParams{
		Trace: log.NewLogger(), // Add a logger to test the logger assignment
	}
	uuid := "test-job-uuid"

	// Mock NewOntapRestClient to return a mock client
	mcs := cluster.NewMockClientService(t)
	mockOntap := &OntapRestClient{cluster: &clusterClient{api: mcs}}

	var gotTrace atomic.Bool
	origMockAPI := NewOntapRestClient
	NewOntapRestClient = func(params RESTClientParams) (RESTClient, error) {
		gotTrace.Store(params.Trace != nil)
		return mockOntap, nil
	}
	defer func() {
		NewOntapRestClient = origMockAPI
	}()

	// Mock successful job response
	jobResponse := &cluster.JobGetOK{
		Payload: &models.Job{
			State: nillable.ToPointer(models.JobStateSuccess),
		},
	}

	out := runPollActivityInMockGoroutine(mcs, func() (*models.Job, error) {
		return PollOntapJobActivity(ctx, clientParams, uuid)
	})

	mcs.AssertJobGet(cluster.NewJobGetParams().WithUUID(uuid).WithFields([]string{"*", "node.name"}), nil, nil, jobResponse, nil)
	mcs.AssertMockClientServiceDone()

	outcome := <-out
	require.False(t, outcome.panicked)
	require.NoError(t, outcome.err)
	require.Nil(t, outcome.result)
	require.True(t, gotTrace.Load(), "logger should be assigned to Trace")
}
