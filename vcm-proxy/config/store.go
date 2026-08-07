package config

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

const DefaultDBValidateTimeout = 2 * time.Minute

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

	configured      atomic.Bool
	reconnectGen    atomic.Uint64
	dbMu            sync.Mutex
	dbStore         vcmdb.Storage
	logger          log.Logger
	opener          DBOpener
	validateTimeout time.Duration
	validateDBFunc  func(ctx context.Context, gen uint64, db vcmserver.DbConfigV1)
}

// NewStore creates an in-memory configuration store backed by Cloud Spanner.
// NewStoreWithConfig creates a Cloud Spanner store with explicit initialization settings.
// NewStoreWithConfig creates a Cloud Spanner store with ValidateTimeout.
func NewStore(logger log.Logger, validateTimeout time.Duration) *Store {
	return newStore(logger, spannerDBOpener{logger: logger}, validateTimeout)
}

// NewStoreWithOpener creates a store using a custom DB opener (tests).
func NewStoreWithOpener(logger log.Logger, opener DBOpener) *Store {
	return newStore(logger, opener, DefaultDBValidateTimeout)
}

func newStore(logger log.Logger, opener DBOpener, validateTimeout time.Duration) *Store {
	if validateTimeout <= 0 {
		logger.Warn("VCM DB validate timeout missing or non-positive; using default %v", DefaultDBValidateTimeout)
		validateTimeout = DefaultDBValidateTimeout
	}
	s := &Store{
		logger:          logger,
		opener:          opener,
		validateTimeout: validateTimeout,
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

// ListOngoingJobs returns durable VCM jobs that have not reached a terminal state.
func (s *Store) ListOngoingJobs(ctx context.Context) ([]vcmdb.Job, error) {
	store, err := s.storage()
	if err != nil {
		return nil, err
	}
	return store.ListOngoingJobs(ctx)
}

// UpdateJobProgress persists progress after a workflow step.
func (s *Store) UpdateJobProgress(ctx context.Context, params vcmdb.UpdateJobProgressParams) error {
	store, err := s.storage()
	if err != nil {
		return err
	}
	return store.UpdateJobProgress(ctx, params)
}

// CurrentSetConfig returns a defensive copy of the stored SetConfig request.
func (s *Store) CurrentSetConfig() vcmserver.SetConfigRequestV1 {
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
	trimSetConfigRequest(req)

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
	replaceOptIpConfig(&s.config.IpConfig, req.IpConfig)
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

// replaceOptIpConfig overwrites runtime addressing when the caller supplies ipConfig
func replaceOptIpConfig(dst *vcmserver.OptIpConfigV1, src vcmserver.OptIpConfigV1) {
	if src.IsSet() {
		dst.SetTo(src.Value)
	}
}

func trimSetConfigRequest(req *vcmserver.SetConfigRequestV1) {
	trimOptString(&req.ServiceTenantProject)
	trimOptString(&req.DeploymentId)
	trimOptString(&req.Zone1)
	trimOptString(&req.Zone2)
	trimOptString(&req.MediatorZone)
	if req.IpConfig.IsSet() {
		trimIpConfig(&req.IpConfig.Value)
	}
	for i := range req.OntapCredentials {
		trimOptString(&req.OntapCredentials[i].SecretId)
		trimOptString(&req.OntapCredentials[i].CertificateId)
	}
}

func trimOptString(opt *vcmserver.OptString) {
	if !opt.IsSet() {
		return
	}
	trimmed := strings.TrimSpace(opt.Value)
	if trimmed == "" {
		// Keep blank equivalent to unset so later merges can apply a real value.
		opt.Reset()
		return
	}
	opt.SetTo(trimmed)
}

func trimIpConfig(cfg *vcmserver.IpConfigV1) {
	for i := range cfg.Lifs {
		trimOptString(&cfg.Lifs[i].Name)
		trimOptString(&cfg.Lifs[i].Nodemgmt)
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

	ctx, cancel := context.WithTimeout(parent, s.validateTimeout)
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
