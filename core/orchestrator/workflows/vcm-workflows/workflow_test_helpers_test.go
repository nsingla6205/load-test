package vcm_workflows

import (
	"context"
	"errors"
	"sync"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

const testExpectedSvmName = "gcnv-test-svm-01"

const workflowTestErrType = "VCM_WORKFLOW_TEST"

func nonRetryableTestErr(msg string) error {
	return orchestrator.NewNonRetryableApplicationError(msg, workflowTestErrType, errors.New(msg))
}

type jobProgressCall struct {
	jobID           string
	state           vcmdb.JobState
	trackingID      int
	errorMessage    string
	retriable       bool
	responsePayload string
	recoveryPayload string
}

type stubJobActivity struct {
	mu            sync.Mutex
	calls         []jobProgressCall
	failOnState   vcmdb.JobState
	failErr       error
	failOnceOnly  bool
	failedOnce    bool
	getJobErr     error
	recoveryPayload string
}

func (s *stubJobActivity) UpdateJobProgress(_ context.Context, params vcmdb.UpdateJobProgressParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, jobProgressCall{
		jobID:           params.JobID,
		state:           params.State,
		trackingID:      params.TrackingID,
		errorMessage:    params.ErrorMessage,
		retriable:       params.Retriable,
		responsePayload: string(params.ResponsePayload),
		recoveryPayload: params.RecoveryPayload,
	})
	if s.failErr != nil && s.failOnState == params.State {
		if s.failOnceOnly {
			if s.failedOnce {
				return nil
			}
			s.failedOnce = true
		}
		return nonRetryableTestErr(s.failErr.Error())
	}
	return nil
}

func (s *stubJobActivity) snapshot() []jobProgressCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]jobProgressCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *stubJobActivity) GetJob(_ context.Context, jobID string) (*vcmdb.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getJobErr != nil {
		return nil, s.getJobErr
	}
	job := &vcmdb.Job{
		ID:           jobID,
		State:        vcmdb.JobStateProcessing,
		RecoveryPayload: s.recoveryPayload,
	}
	return job, nil
}

type stubSvmActivity struct {
	mu sync.Mutex

	err   error
	calls int
}

func (s *stubSvmActivity) GetSvmByName(_ context.Context, _ *models.Node, _ string) (*ontapRest.Svm, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &ontapRest.Svm{}, nil
}

func (s *stubSvmActivity) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func testNode() *models.Node {
	return &models.Node{
		DeploymentName: "gcnv-test",
		SecretID:       "secret-1",
		EndpointAddressesToHostNameMap: map[string]string{
			"198.18.0.5": "vm1.gcnv-test.internal",
		},
	}
}
