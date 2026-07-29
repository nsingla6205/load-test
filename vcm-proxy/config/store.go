package config

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

const dbValidateTimeout = 10 * time.Second

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
	return vcmdb.Open(ctx, cfg, o.logger, false)
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

	s.dbMu.Lock()
	oldStore := s.dbStore
	if err != nil {
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
