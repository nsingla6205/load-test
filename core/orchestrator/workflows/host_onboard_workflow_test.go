package workflows

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/testsuite"
)

const (
	testHostOnboardHostUUID = "host-uuid-123"
	testHostOnboardJobUUID  = "job-uuid-123"
)

func newHostOnboardWorkflowTestEnvironment(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	testEnvironment := suite.NewTestWorkflowEnvironment()
	testEnvironment.RegisterActivity(&activities.CommonActivities{})
	testEnvironment.RegisterActivity(&activities.HostOnboardActivity{})
	return testEnvironment
}

func hostOnboardWorkflowParams() *orchcommon.OnboardHostWorkflowParams {
	return &orchcommon.OnboardHostWorkflowParams{
		HostUUID: testHostOnboardHostUUID,
		JobUUID:  testHostOnboardJobUUID,
		DbHost: &datamodel.Host{
			BaseModel: datamodel.BaseModel{UUID: testHostOnboardHostUUID},
			Name:      "host-01",
			State:     datamodel.LifeCycleStateCreating,
		},
	}
}

func TestOnboardHostWorkflow_Success_RunsActivitiesInOrderAndCompletesJob(t *testing.T) {
	testEnvironment := newHostOnboardWorkflowTestEnvironment(t)
	commonActivity := &activities.CommonActivities{}
	hostActivity := &activities.HostOnboardActivity{}
	var activityOrder []string

	testEnvironment.OnActivity(
		commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job != nil && job.State == string(datamodel.JobsStatePROCESSING)
		}),
	).Return(func(_ context.Context, _ *datamodel.Job) error {
		activityOrder = append(activityOrder, "job-processing")
		return nil
	}).Once()

	hostActivityExpectations := []struct {
		name     string
		activity interface{}
	}{
		{name: "discover-host", activity: hostActivity.DiscoverHost},
		{name: "generate-certificate", activity: hostActivity.GenerateHostCertificate},
		{name: "create-dns-record", activity: hostActivity.CreateHostCloudDNSRecord},
		{name: "install-server-certificate", activity: hostActivity.InstallHostServerCertificate},
		{name: "install-trust-certificates", activity: hostActivity.InstallHostCACertificate},
		{name: "onboard-host", activity: hostActivity.OnboardHost},
	}
	for _, expectation := range hostActivityExpectations {
		expectation := expectation
		testEnvironment.OnActivity(
			expectation.activity,
			mock.Anything,
			mock.MatchedBy(func(result *orchcommon.HostOnboardResult) bool {
				return result != nil &&
					result.HostUUID == testHostOnboardHostUUID &&
					result.DbHost != nil
			}),
		).Return(func(_ context.Context, result *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error) {
			activityOrder = append(activityOrder, expectation.name)
			return result, nil
		}).Once()
	}

	testEnvironment.OnActivity(
		commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job != nil && job.State == string(datamodel.JobsStateDONE)
		}),
	).Return(func(_ context.Context, _ *datamodel.Job) error {
		activityOrder = append(activityOrder, "job-done")
		return nil
	}).Once()

	testEnvironment.ExecuteWorkflow(OnboardHostWorkflow, hostOnboardWorkflowParams())

	require.True(t, testEnvironment.IsWorkflowCompleted())
	require.NoError(t, testEnvironment.GetWorkflowError())
	assert.Equal(t, []string{
		"job-processing",
		"discover-host",
		"generate-certificate",
		"create-dns-record",
		"install-server-certificate",
		"install-trust-certificates",
		"onboard-host",
		"job-done",
	}, activityOrder)
	testEnvironment.AssertExpectations(t)
}

func TestOnboardHostWorkflow_ServerCertificateFailure_RetriesAndMarksHostAndJobFailed(t *testing.T) {
	testEnvironment := newHostOnboardWorkflowTestEnvironment(t)
	commonActivity := &activities.CommonActivities{}
	hostActivity := &activities.HostOnboardActivity{}
	serverCertificateAttempts := 0

	testEnvironment.OnActivity(
		commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job != nil && job.State == string(datamodel.JobsStatePROCESSING)
		}),
	).Return(nil).Once()

	for _, activity := range []interface{}{
		hostActivity.DiscoverHost,
		hostActivity.GenerateHostCertificate,
		hostActivity.CreateHostCloudDNSRecord,
	} {
		testEnvironment.OnActivity(activity, mock.Anything, mock.Anything).
			Return(func(_ context.Context, result *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error) {
				return result, nil
			}).Once()
	}

	testEnvironment.OnActivity(
		hostActivity.InstallHostServerCertificate,
		mock.Anything,
		mock.Anything,
	).Return(func(_ context.Context, _ *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error) {
		serverCertificateAttempts++
		return nil, errors.New("server certificate install failed")
	})

	testEnvironment.OnActivity(
		hostActivity.MarkHostOnboardFailed,
		mock.Anything,
		testHostOnboardHostUUID,
		mock.MatchedBy(func(details string) bool {
			return strings.Contains(details, "server certificate install failed")
		}),
	).Return(nil).Once()

	testEnvironment.OnActivity(
		commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job != nil &&
				job.State == string(datamodel.JobsStateERROR) &&
				strings.Contains(job.ErrorDetails, "server certificate install failed")
		}),
	).Return(nil).Once()

	testEnvironment.ExecuteWorkflow(OnboardHostWorkflow, hostOnboardWorkflowParams())

	require.True(t, testEnvironment.IsWorkflowCompleted())
	require.NoError(t, testEnvironment.GetWorkflowError())
	assert.Equal(t, installHostServerCertificateRetryMaxAttempts, serverCertificateAttempts)
	testEnvironment.AssertNotCalled(t, "InstallHostCACertificate", mock.Anything, mock.Anything)
	testEnvironment.AssertNotCalled(t, "OnboardHost", mock.Anything, mock.Anything)
	testEnvironment.AssertExpectations(t)
}
