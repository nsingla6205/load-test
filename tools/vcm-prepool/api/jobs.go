package api

import (
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/prepoolinfra"
)

// JobState represents the lifecycle of an async prepool job.
type JobState string

const (
	JobStatePending JobState = "PENDING"
	JobStateRunning JobState = "RUNNING"
	JobStateDone    JobState = "DONE"
	JobStateError   JobState = "ERROR"
)

// JobOperation distinguishes setup, reclaim, and VSA cluster delete.
type JobOperation string

const (
	JobOperationSetup             JobOperation = "setup"
	JobOperationReclaimDeployment JobOperation = "reclaimDeployment"
	JobOperationReclaimAll        JobOperation = "reclaimAll"
	JobOperationDeleteVSACluster  JobOperation = "deleteVsaCluster"
)

// Job tracks the state of a single async provision, deployment reclaim, or VSA cluster delete operation.
type Job struct {
	ID             string                     `json:"id"`
	Operation      JobOperation               `json:"operation"`
	DeploymentName string                     `json:"deploymentName"`
	Request        PrepoolSetupRequest        `json:"request"`
	State          JobState                   `json:"state"`
	Result         *prepoolinfra.PrePoolInfra `json:"result,omitempty"`
	Error          string                     `json:"error,omitempty"`
	CreatedAt      time.Time                  `json:"createdAt"`
	UpdatedAt      time.Time                  `json:"updatedAt"`
}

// JobStore is a concurrency-safe, in-memory store for prepool jobs.
// All state is lost on process restart.
type JobStore struct {
	mu     sync.RWMutex
	jobs   map[string]*Job
	active map[string]string // deploymentName → jobID for PENDING/RUNNING jobs
}

// NewJobStore returns an empty store ready for use.
func NewJobStore() *JobStore {
	return &JobStore{
		jobs:   make(map[string]*Job),
		active: make(map[string]string),
	}
}

// CreateIfNoActive atomically inserts a PENDING job unless the deployment
// already has a PENDING or RUNNING job.
func (s *JobStore) CreateIfNoActive(op JobOperation, req PrepoolSetupRequest) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.active[req.DeploymentName]; ok {
		return nil, false
	}

	now := time.Now().UTC()
	job := &Job{
		ID:             uuid.New().String(),
		Operation:      op,
		DeploymentName: req.DeploymentName,
		Request:        req,
		State:          JobStatePending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	s.jobs[job.ID] = job
	s.active[req.DeploymentName] = job.ID
	return cloneJob(job), true
}

// Get returns a job by ID, or nil if not found.
func (s *JobStore) Get(id string) *Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneJob(s.jobs[id])
}

// List returns all jobs ordered by creation time (newest first).
func (s *JobStore) List() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, cloneJob(j))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// SetRunning transitions a job to RUNNING.
func (s *JobStore) SetRunning(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.State = JobStateRunning
		j.UpdatedAt = time.Now().UTC()
	}
}

// SetDone transitions a job to DONE with an optional result.
func (s *JobStore) SetDone(id string, result *prepoolinfra.PrePoolInfra) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.State = JobStateDone
		j.Result = result
		j.UpdatedAt = time.Now().UTC()
		delete(s.active, j.DeploymentName)
	}
}

// SetError transitions a job to ERROR with a message.
func (s *JobStore) SetError(id string, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.State = JobStateError
		j.Error = errMsg
		j.UpdatedAt = time.Now().UTC()
		delete(s.active, j.DeploymentName)
	}
}

func cloneJob(job *Job) *Job {
	if job == nil {
		return nil
	}

	cloned := *job
	if job.Result != nil {
		result := *job.Result
		cloned.Result = &result
	}
	return &cloned
}
