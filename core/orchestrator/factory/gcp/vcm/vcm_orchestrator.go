package vcm

import (
	"context"
	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// Store exposes VCM config and durable job persistence.
type Store interface {
	NodeProviderInput() (vsa.NodeProviderInput, error)
	CreateJob(ctx context.Context, job *vcmdb.Job) error
	GetJob(ctx context.Context, jobID string) (*vcmdb.Job, error)
	UpdateJobProgress(ctx context.Context, params vcmdb.UpdateJobProgressParams) error
}

// GoWorkflowStarter starts a go-workflows instance.
type GoWorkflowStarter interface {
	Start(ctx context.Context, instanceID string, workflowFn interface{}, args ...any) (*gowf.Instance, error)
}

// VCMOrchestrator implements factory.OrchestratorFactory for VCM.
type VCMOrchestrator struct {
	notImplementedOrchestrator
	store     Store
	wfStarter GoWorkflowStarter
	logger    log.Logger
}

// NewVCMOrchestrator creates a VCM orchestrator.
func NewVCMOrchestrator(store Store, wfStarter GoWorkflowStarter, logger log.Logger) *VCMOrchestrator {
	return &VCMOrchestrator{
		store:     store,
		wfStarter: wfStarter,
		logger:    logger,
	}
}
