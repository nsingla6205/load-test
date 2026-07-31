package vcm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// JobState is the durable execution state of a VCM job.
type JobState string

const (
	JobStateNew        JobState = "NEW"
	JobStateProcessing JobState = "PROCESSING"
	JobStateDone       JobState = "DONE"
	JobStateError      JobState = "ERROR"
)

// ErrJobNotFound is returned when a VCM job does not exist.
var ErrJobNotFound = errors.New("vcm job not found")

// Job stores workflow progress for status reporting and restart recovery.
type Job struct {
	ID           string    `gorm:"column:id;primaryKey;size:36"`
	ResourceID   string    `gorm:"column:resource_id;index;size:128;not null"`
	Type         string    `gorm:"column:type;size:64;not null"`
	State        JobState  `gorm:"column:state;index;size:16;not null"`
	CurrentStep  string    `gorm:"column:current_step;size:128;not null"`
	ErrorMessage string    `gorm:"column:error_message;size:65536;not null"`
	DeadlineAt   time.Time `gorm:"column:deadline_at;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

// CreateJob inserts the initial durable state before workflow execution starts.
func (s *gormStore) CreateJob(ctx context.Context, job *Job) error {
	if job == nil {
		return fmt.Errorf("vcm: create job: job is nil")
	}
	if job.ID == "" || job.ResourceID == "" || job.Type == "" {
		return fmt.Errorf("vcm: create job: id, resource id, and type are required")
	}
	if job.State == "" {
		job.State = JobStateNew
	}
	if !validJobState(job.State) {
		return fmt.Errorf("vcm: create job: invalid state %q", job.State)
	}
	db, err := s.database()
	if err != nil {
		return err
	}
	if err := db.WithContext(ctx).Create(job).Error; err != nil {
		return fmt.Errorf("vcm: create job %q: %w", job.ID, err)
	}
	return nil
}

// GetJob reads a job by operation ID.
func (s *gormStore) GetJob(ctx context.Context, jobID string) (*Job, error) {
	db, err := s.database()
	if err != nil {
		return nil, err
	}

	var job Job
	if err := db.WithContext(ctx).First(&job, "id = ?", jobID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrJobNotFound, jobID)
		}
		return nil, fmt.Errorf("vcm: get job %q: %w", jobID, err)
	}
	return &job, nil
}

// ListOngoingJobs returns non-terminal jobs (NEW or PROCESSING).
func (s *gormStore) ListOngoingJobs(ctx context.Context) ([]Job, error) {
	db, err := s.database()
	if err != nil {
		return nil, err
	}

	var jobs []Job
	result := db.WithContext(ctx).
		Where("state IN ?", []JobState{JobStateNew, JobStateProcessing}).
		Order("created_at ASC").
		Find(&jobs)
	if result.Error != nil {
		return nil, fmt.Errorf("vcm: list ongoing jobs: %w", result.Error)
	}
	return jobs, nil
}

// UpdateJobProgress persists step, state, and error details after workflow progress.
func (s *gormStore) UpdateJobProgress(ctx context.Context, jobID, step string, state JobState, errorMessage string) error {
	if !validJobState(state) {
		return fmt.Errorf("vcm: update job %q: invalid state %q", jobID, state)
	}

	db, err := s.database()
	if err != nil {
		return err
	}
	result := db.WithContext(ctx).
		Model(&Job{}).
		Where("id = ?", jobID).
		Updates(map[string]any{
			"current_step":  step,
			"state":         state,
			"error_message": errorMessage,
			"updated_at":    time.Now().UTC(),
		})
	if result.Error != nil {
		return fmt.Errorf("vcm: update job %q: %w", jobID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: %s", ErrJobNotFound, jobID)
	}
	return nil
}

func (s *gormStore) database() (*gorm.DB, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, fmt.Errorf("vcm: datastore is not connected")
	}
	return s.db, nil
}

func validJobState(state JobState) bool {
	switch state {
	case JobStateNew, JobStateProcessing, JobStateDone, JobStateError:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether no more workflow steps should be executed.
func (j Job) IsTerminal() bool {
	return j.State == JobStateDone || j.State == JobStateError
}

// IsExpired reports whether the job reached its configured deadline.
func (j Job) IsExpired(now time.Time) bool {
	if j.DeadlineAt.IsZero() {
		return false
	}
	return !j.DeadlineAt.UTC().After(now.UTC())
}
