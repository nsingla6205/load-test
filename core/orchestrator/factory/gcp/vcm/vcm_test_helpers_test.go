package vcm

import (
	"context"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

type stubStore struct {
	input        vsa.NodeProviderInput
	inputErr     error
	createdJob   *vcmdb.Job
	createErr    error
	existingJob  *vcmdb.Job
	getJobErr    error
	getJobState  vcmdb.JobState
	updatedJobID string
	updatedState vcmdb.JobState
	updatedStep  string
	updatedError string
	updatedRetriable bool
	updateErr        error
}

func (s *stubStore) NodeProviderInput() (vsa.NodeProviderInput, error) {
	if s.inputErr != nil {
		return vsa.NodeProviderInput{}, s.inputErr
	}
	return s.input, nil
}

func (s *stubStore) CreateJob(_ context.Context, job *vcmdb.Job) error {
	s.createdJob = job
	return s.createErr
}

func (s *stubStore) GetJob(_ context.Context, jobID string) (*vcmdb.Job, error) {
	if s.getJobErr != nil {
		return nil, s.getJobErr
	}
	if s.existingJob != nil {
		return s.existingJob, nil
	}
	if s.getJobState != "" {
		return &vcmdb.Job{ID: jobID, State: s.getJobState}, nil
	}
	return &vcmdb.Job{ID: jobID, State: vcmdb.JobStateNew}, nil
}

func (s *stubStore) UpdateJobProgress(_ context.Context, params vcmdb.UpdateJobProgressParams) error {
	s.updatedJobID = params.JobID
	s.updatedState = params.State
	s.updatedStep = params.Step
	s.updatedError = params.ErrorMessage
	s.updatedRetriable = params.Retriable
	return s.updateErr
}

type stubWorkflowStarter struct {
	instanceID string
	workflowFn interface{}
	args       []any
	err        error
}

func (s *stubWorkflowStarter) Start(_ context.Context, instanceID string, workflowFn interface{}, args ...any) (*gowf.Instance, error) {
	s.instanceID = instanceID
	s.workflowFn = workflowFn
	s.args = args
	return nil, s.err
}

func validNodeProviderInput() vsa.NodeProviderInput {
	return vsa.NodeProviderInput{
		DeploymentName: "gcnv-test",
		OntapCredentials: &datamodel.PoolCredentials{
			SecretID: "secret-1",
			AuthType: env.USERNAME_PWD_SEC_MGR,
		},
		Nodes: []*datamodel.Node{
			{
				Name:            "vm1",
				EndpointAddress: "198.18.0.5",
				HostDNSName:     "vm1.gcnv-test.internal",
			},
		},
	}
}
