// Package recovery coordinates recovery of durable VCM jobs after a process restart.
package recovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// Handler resumes one durable job type.
type Handler interface {
	Resume(ctx context.Context, job *vcmdb.Job) error
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, job *vcmdb.Job) error

// Resume calls f with the durable job selected for recovery.
func (f HandlerFunc) Resume(ctx context.Context, job *vcmdb.Job) error {
	return f(ctx, job)
}

// Coordinator dispatches ongoing durable jobs to handlers registered by job type.
type Coordinator struct {
	store  vcmdb.VCMDatastore
	logger log.Logger
	now    func() time.Time

	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewCoordinator creates a job recovery coordinator.
func NewCoordinator(store vcmdb.VCMDatastore, logger log.Logger) *Coordinator {
	return &Coordinator{
		store:    store,
		logger:   logger,
		now:      time.Now,
		handlers: make(map[string]Handler),
	}
}

// Register associates one durable job type with its recovery handler.
func (c *Coordinator) Register(jobType string, handler Handler) error {
	jobType = strings.TrimSpace(jobType)
	if jobType == "" {
		return errors.New("recovery: job type is required")
	}
	if handler == nil {
		return fmt.Errorf("recovery: handler for job type %q is nil", jobType)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.handlers[jobType]; exists {
		return fmt.Errorf("recovery: handler for job type %q is already registered", jobType)
	}
	c.handlers[jobType] = handler
	return nil
}

// ResumeOngoingJobs restarts NEW and PROCESSING jobs after a process crash.
// DONE and ERROR jobs are never resumed: callers poll GetOperation for the
// terminal result. A job failure does not prevent recovery of the remaining jobs.
func (c *Coordinator) ResumeOngoingJobs(ctx context.Context) error {
	if c == nil || c.store == nil {
		return errors.New("recovery: datastore is not configured")
	}

	jobs, err := c.store.ListOngoingJobs(ctx)
	if err != nil {
		return fmt.Errorf("recovery: list ongoing jobs: %w", err)
	}

	var recoveryErrors []error
	now := c.now().UTC()
	for i := range jobs {
		job := &jobs[i]
		if job.IsTerminal() {
			if c.logger != nil {
				c.logger.WarnContext(ctx, "skipping terminal job during recovery",
					"job_id", job.ID, "job_type", job.Type, "state", job.State)
			}
			continue
		}
		if job.IsExpired(now) {
			if err := c.expireJob(ctx, job); err != nil {
				recoveryErrors = append(recoveryErrors, err)
			}
			continue
		}

		handler, ok := c.handler(job.Type)
		if !ok {
			if c.logger != nil {
				c.logger.WarnContext(ctx, "no recovery handler registered for ongoing job",
					"job_id", job.ID, "job_type", job.Type)
			}
			continue
		}

		if err := handler.Resume(ctx, job); err != nil {
			if c.logger != nil {
				c.logger.ErrorContext(ctx, "failed to resume ongoing job",
					"job_id", job.ID, "job_type", job.Type, "error", err)
			}
			recoveryErrors = append(recoveryErrors,
				fmt.Errorf("resume job %q (%s): %w", job.ID, job.Type, err))
		}
	}
	return errors.Join(recoveryErrors...)
}

func (c *Coordinator) expireJob(ctx context.Context, job *vcmdb.Job) error {
	message := fmt.Sprintf("job deadline exceeded at %s", job.DeadlineAt.UTC().Format(time.RFC3339))
	err := c.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
		JobID:        job.ID,
		Step:         job.CurrentStep,
		State:        vcmdb.JobStateError,
		ErrorMessage: message,
		Retriable:    false,
	})
	if err != nil {
		return fmt.Errorf("expire job %q (%s): %w", job.ID, job.Type, err)
	}
	if c.logger != nil {
		c.logger.WarnContext(ctx, "ongoing job deadline exceeded; marked job ERROR",
			"job_id", job.ID, "job_type", job.Type, "deadline", job.DeadlineAt.UTC())
	}
	return nil
}

func (c *Coordinator) handler(jobType string) (Handler, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	handler, ok := c.handlers[jobType]
	return handler, ok
}
