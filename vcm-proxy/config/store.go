package config

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconstants "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/constants"
)

const dbValidateTimeout = 10 * time.Second

var ErrDatabaseNotConfigured = errors.New("VCM database is not configured")

// DBOpener opens a persistence store for the given Spanner dbConfig.
type DBOpener interface {
	Open(ctx context.Context, db vcmserver.DbConfigV1) (vcmdb.Storage, error)
}

type spannerDBOpener struct {
	logger log.Logger
}

func (o spannerDBOpener) Open(ctx context.Context, db vcmserver.DbConfigV1) (vcmdb.Storage, error) {
	cfg := vcmdb.Config{
		Driver: vcmdb.DriverSpanner,
		Spanner: vcmdb.SpannerConfig{
			Project:  db.GetProject(),
			Instance: db.GetInstance(),
			Database: db.GetDatabase(),
		},
	}
	return vcmdb.Open(ctx, cfg, o.logger, true)
}

// Store holds the in-memory VCM configuration and database readiness state.
type Store struct {
	mu sync.RWMutex

	config     vcmserver.SetConfigRequestV1
	hasConfig  bool
	validating bool

	configured     atomic.Bool
	reconnectGen   atomic.Uint64
	dbMu           sync.Mutex
	dbStore        vcmdb.Storage
	logger         log.Logger
	opener         DBOpener
	validateDBFunc func(ctx context.Context, gen uint64, db vcmserver.DbConfigV1)
}

// NewStore creates an in-memory configuration store backed by Cloud Spanner.
func NewStore(logger log.Logger) *Store {
	s := &Store{
		logger: logger,
		opener: spannerDBOpener{logger: logger},
	}
	s.validateDBFunc = s.validateDB
	return s
}

// NewStoreWithOpener creates a store using a custom DB opener (tests).
func NewStoreWithOpener(logger log.Logger, opener DBOpener) *Store {
	s := &Store{
		logger: logger,
		opener: opener,
	}
	s.validateDBFunc = s.validateDB
	return s
}

// Configured reports whether the current dbConfig has passed reachability validation.
func (s *Store) Configured() bool {
	return s.configured.Load()
}

// storage returns a snapshot of the configured DB handle without holding dbMu
// across the subsequent I/O call.
func (s *Store) storage() (vcmdb.Storage, error) {
	s.dbMu.Lock()
	store := s.dbStore
	s.dbMu.Unlock()
	if store == nil {
		return nil, ErrDatabaseNotConfigured
	}
	return store, nil
}

// CreateJob inserts a durable VCM job using the configured database.
func (s *Store) CreateJob(ctx context.Context, job *vcmdb.Job) error {
	store, err := s.storage()
	if err != nil {
		return err
	}
	return store.CreateJob(ctx, job)
}

// GetJob reads a durable VCM job by operation ID.
func (s *Store) GetJob(ctx context.Context, jobID string) (*vcmdb.Job, error) {
	store, err := s.storage()
	if err != nil {
		return nil, err
	}
	return store.GetJob(ctx, jobID)
}

// UpdateJobProgress persists progress after a workflow step.
func (s *Store) UpdateJobProgress(ctx context.Context, jobID, step string, state vcmdb.JobState, errorMessage string) error {
	store, err := s.storage()
	if err != nil {
		return err
	}
	return store.UpdateJobProgress(ctx, jobID, step, state, errorMessage)
}

// Snapshot returns a defensive copy of the stored configuration.
func (s *Store) Snapshot() vcmserver.SetConfigRequestV1 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSetConfigRequestV1(s.config)
}

// Apply merges the request into memory, replaces dbConfig, and asynchronously
// validates database reachability when dbConfig changes or the previous
// validation attempt failed.
func (s *Store) Apply(ctx context.Context, req *vcmserver.SetConfigRequestV1) {
	if req == nil {
		return
	}

	s.mu.Lock()
	oldDB := s.config.DbConfig
	s.mergeLocked(req)
	s.config.DbConfig = req.DbConfig
	dbChanged := !s.hasConfig || !dbConfigEqual(oldDB, req.DbConfig)
	s.hasConfig = true
	newDB := s.config.DbConfig

	if !dbChanged && (s.configured.Load() || s.validating) {
		s.mu.Unlock()
		return
	}

	if dbChanged {
		s.configured.Store(false)
	}
	gen := s.reconnectGen.Add(1)
	s.validating = true
	s.mu.Unlock()

	go s.validateDBFunc(context.WithoutCancel(ctx), gen, newDB)
}

func (s *Store) mergeLocked(req *vcmserver.SetConfigRequestV1) {
	mergeOptString(&s.config.ServiceTenantProject, req.ServiceTenantProject)
	mergeOptNetConfig(&s.config.NetConfig, req.NetConfig)
	if len(s.config.OntapCredentials) == 0 && len(req.OntapCredentials) > 0 {
		s.config.OntapCredentials = append([]vcmserver.OntapCredentialsV1(nil), req.OntapCredentials...)
	}
	mergeOptString(&s.config.Zone1, req.Zone1)
	mergeOptString(&s.config.Zone2, req.Zone2)
	mergeOptString(&s.config.MediatorZone, req.MediatorZone)
	mergeOptString(&s.config.DeploymentId, req.DeploymentId)
}

func mergeOptString(dst *vcmserver.OptString, src vcmserver.OptString) {
	if !dst.IsSet() && src.IsSet() {
		dst.SetTo(src.Value)
	}
}

func mergeOptNetConfig(dst *vcmserver.OptNetConfigV1, src vcmserver.OptNetConfigV1) {
	if !dst.IsSet() && src.IsSet() {
		dst.SetTo(src.Value)
	}
}

func dbConfigEqual(a, b vcmserver.DbConfigV1) bool {
	return a.GetProject() == b.GetProject() &&
		a.GetInstance() == b.GetInstance() &&
		a.GetDatabase() == b.GetDatabase()
}

func (s *Store) validateDB(parent context.Context, gen uint64, db vcmserver.DbConfigV1) {
	defer func() {
		s.mu.Lock()
		if s.reconnectGen.Load() == gen {
			s.validating = false
		}
		s.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(parent, dbValidateTimeout)
	defer cancel()

	store, err := s.opener.Open(ctx, db)
	if s.reconnectGen.Load() != gen {
		if store != nil {
			_ = store.Close()
		}
		return
	}
	if err != nil {
		s.dbMu.Lock()
		oldStore := s.dbStore
		s.dbStore = nil
		s.dbMu.Unlock()
		if oldStore != nil {
			_ = oldStore.Close()
		}
		if s.reconnectGen.Load() == gen {
			s.configured.Store(false)
		}
		s.logger.Error("VCM dbConfig validation failed", "error", err)
		return
	}

	// Job recovery is best-effort: a transient List/Update failure must not
	// invalidate an already-reachable DB or block readiness.
	if loadErr := s.loadOngoingJobs(ctx, store); loadErr != nil {
		s.logger.Error("VCM ongoing job recovery failed; continuing with validated DB", "error", loadErr)
	}

	s.dbMu.Lock()
	oldStore := s.dbStore
	s.dbStore = store
	s.dbMu.Unlock()

	if oldStore != nil {
		_ = oldStore.Close()
	}

	if s.reconnectGen.Load() == gen {
		s.configured.Store(true)
	} else {
		_ = store.Close()
	}
}

func (s *Store) loadOngoingJobs(ctx context.Context, store vcmdb.Storage) error {
	jobs, err := store.ListOngoingJobs(ctx)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	s.logger.InfoContext(ctx, "Loaded ongoing VCM jobs", "job_count", len(jobs))
	for _, job := range jobs {
		if job.IsExpired(now) {
			if err := store.UpdateJobProgress(ctx, job.ID, job.CurrentStep, vcmdb.JobStateError, vcmconstants.OperationTimedOut); err != nil {
				if errors.Is(err, vcmdb.ErrJobNotFound) {
					s.logger.InfoContext(ctx, "VCM job disappeared during restart recovery; skipping timeout update",
						"job_id", job.ID)
					continue
				}
				s.logger.ErrorContext(ctx, "Failed to mark timed-out VCM job during restart recovery; continuing",
					"job_id", job.ID,
					"error", err)
				continue
			}
			s.logger.InfoContext(ctx, "Marked VCM job as timed out during restart recovery",
				"job_id", job.ID,
				"resource_id", job.ResourceID,
				"job_type", job.Type,
				"deadline_at", job.DeadlineAt.UTC(),
				"created_at", job.CreatedAt.UTC())
			continue
		}

		s.logger.InfoContext(ctx, "Resuming VCM job during restart recovery",
			"job_id", job.ID,
			"resource_id", job.ResourceID,
			"job_type", job.Type,
			"state", job.State,
			"current_step", job.CurrentStep,
			"deadline_at", job.DeadlineAt.UTC())
	}
	return nil
}

func cloneSetConfigRequestV1(src vcmserver.SetConfigRequestV1) vcmserver.SetConfigRequestV1 {
	dst := src
	dst.OntapCredentials = cloneOntapCredentials(src.OntapCredentials)
	return dst
}

func cloneOntapCredentials(src []vcmserver.OntapCredentialsV1) []vcmserver.OntapCredentialsV1 {
	if len(src) == 0 {
		return nil
	}
	return append([]vcmserver.OntapCredentialsV1(nil), src...)
}
