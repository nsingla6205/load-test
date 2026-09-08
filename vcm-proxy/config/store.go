package config

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

const DefaultDBValidateTimeout = 2 * time.Minute

// vlmConfigFetchStaleTimeout bounds how long a rebuild may be treated as in
// flight before Apply is allowed to retrigger it. It exceeds the Get child
// workflow execution timeout so a healthy rebuild always finalizes first.
const vlmConfigFetchStaleTimeout = 20 * time.Minute

var ErrDatabaseNotConfigured = errors.New("VCM database is not configured")

// DBOpener opens a persistence store for the given Spanner dbConfig.
type DBOpener interface {
	Open(ctx context.Context, db vcmserver.StateDbConfigV1) (vcmdb.Storage, error)
}

type spannerDBOpener struct {
	logger log.Logger
}

func (o spannerDBOpener) Open(ctx context.Context, db vcmserver.StateDbConfigV1) (vcmdb.Storage, error) {
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

	vlmConfigJSON           string
	vlmConfigFetching       bool
	vlmConfigFetchingSince  time.Time
	vlmConfigRebuildStarter VLMConfigRebuildStarter

	configured      atomic.Bool
	reconnectGen    atomic.Uint64
	dbMu            sync.Mutex
	dbStore         vcmdb.Storage
	logger          log.Logger
	opener          DBOpener
	jobResumer      OngoingJobResumer
	validateTimeout time.Duration
	validateDBFunc  func(ctx context.Context, gen uint64, db vcmserver.StateDbConfigV1)
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

// OngoingJobResumer resumes durable jobs after the configured database is ready.
type OngoingJobResumer interface {
	ResumeOngoingJobs(ctx context.Context) error
}

// SetOngoingJobResumer configures the process-restart recovery callback.
func (s *Store) SetOngoingJobResumer(resumer OngoingJobResumer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobResumer = resumer
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
	oldDB := s.config.StateDbConfig
	oldRebuildSig := s.rebuildSignatureLocked()
	s.mergeLocked(req)
	s.config.StateDbConfig = req.StateDbConfig
	dbChanged := !s.hasConfig || !dbConfigEqual(oldDB, req.StateDbConfig)
	s.hasConfig = true
	newDB := s.config.StateDbConfig

	// A change to any rebuild input invalidates the cached VLM config so no
	// reader sees stale data and a fresh rebuild is dispatched below.
	if s.rebuildSignatureLocked() != oldRebuildSig {
		s.invalidateVLMConfigLocked()
	}

	// Decide whether to kick off a VLM config rebuild; dispatched after unlock.
	rebuildParams := s.startVlmConfigRebuild()

	if !dbChanged && (s.configured.Load() || s.validating) {
		s.mu.Unlock()
		s.dispatchVLMConfigRebuild(ctx, rebuildParams)
		return
	}

	if dbChanged {
		s.configured.Store(false)
	}
	gen := s.reconnectGen.Add(1)
	s.validating = true
	s.mu.Unlock()

	s.dispatchVLMConfigRebuild(ctx, rebuildParams)
	go s.validateDBFunc(context.WithoutCancel(ctx), gen, newDB)
}

// mergeLocked applies the request over the stored config with last-write-wins
// semantics: an unset field is filled from the payload, and a changed value in a
// later re-seed from CLH is honored rather than rejected.
func (s *Store) mergeLocked(req *vcmserver.SetConfigRequestV1) {
	if !req.ClusterConfig.IsSet() {
		return
	}

	incoming := req.ClusterConfig.Value
	var stored vcmserver.ClusterConfigV1
	if s.config.ClusterConfig.IsSet() {
		stored = s.config.ClusterConfig.Value
	}
	overwriteSeedConfigIfSet(&stored.SeedConfig, incoming.SeedConfig)
	if incoming.RequiredShape.IsSet() {
		stored.RequiredShape.SetTo(incoming.RequiredShape.Value)
	}
	overwriteActualLayoutIfSet(&stored.ActualLayout, incoming.ActualLayout)
	s.config.ClusterConfig.SetTo(stored)
}

func overwriteSeedConfigIfSet(dst *vcmserver.OptSeedConfigV1, src vcmserver.OptSeedConfigV1) {
	if !src.IsSet() {
		return
	}
	incoming := src.Value
	var stored vcmserver.SeedConfigV1
	if dst.IsSet() {
		stored = dst.Value
	}
	overwriteOptStringIfSet(&stored.DeploymentId, incoming.DeploymentId)
	overwriteOptStringIfSet(&stored.Zone1, incoming.Zone1)
	overwriteOptStringIfSet(&stored.Zone2, incoming.Zone2)
	overwriteOptStringIfSet(&stored.MediatorZone, incoming.MediatorZone)
	overwriteOptStringIfSet(&stored.CaUri, incoming.CaUri)
	overwriteOptStringIfSet(&stored.OntapLicenseSecretUri, incoming.OntapLicenseSecretUri)
	overwriteOptStringIfSet(&stored.ServiceAccountEmail, incoming.ServiceAccountEmail)
	overwriteOptInt64IfSet(&stored.ConsumerProjectNum, incoming.ConsumerProjectNum)
	overwriteOptInt64IfSet(&stored.ServiceTenantProjectNum, incoming.ServiceTenantProjectNum)
	overwriteOptInt64IfSet(&stored.SnHostProjectNum, incoming.SnHostProjectNum)
	overwriteOptNetConfigIfSet(&stored.NetConfig, incoming.NetConfig)
	if len(incoming.OntapCredentials) > 0 {
		stored.OntapCredentials = cloneOntapCredentials(incoming.OntapCredentials)
	}
	dst.SetTo(stored)
}

func overwriteActualLayoutIfSet(dst *vcmserver.OptClusterLayoutV1, src vcmserver.OptClusterLayoutV1) {
	if !src.IsSet() {
		return
	}
	incoming := src.Value
	var stored vcmserver.ClusterLayoutV1
	if dst.IsSet() {
		stored = dst.Value
	}
	if incoming.Nodes != nil {
		stored.Nodes = cloneVSANodes(incoming.Nodes)
	}
	replaceOptIpConfig(&stored.IpConfig, incoming.IpConfig)
	overwriteOptStringIfSet(&stored.ActiveZone, incoming.ActiveZone)
	overwriteOptStringIfSet(&stored.StandbyZone, incoming.StandbyZone)
	overwriteOptStringIfSet(&stored.ForwardingRuleIp, incoming.ForwardingRuleIp)
	dst.SetTo(stored)
}

// overwriteOptStringIfSet replaces dst with src whenever src is set (last-write-wins).
func overwriteOptStringIfSet(dst *vcmserver.OptString, src vcmserver.OptString) {
	if src.IsSet() {
		dst.SetTo(src.Value)
	}
}

func overwriteOptInt64IfSet(dst *vcmserver.OptInt64, src vcmserver.OptInt64) {
	if src.IsSet() {
		dst.SetTo(src.Value)
	}
}

// overwriteOptNetConfigIfSet replaces dst with src whenever src is set (last-write-wins).
func overwriteOptNetConfigIfSet(dst *vcmserver.OptNetConfigV1, src vcmserver.OptNetConfigV1) {
	if src.IsSet() {
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
	if !req.ClusterConfig.IsSet() {
		return
	}
	clusterConfig := req.ClusterConfig.Value
	if clusterConfig.SeedConfig.IsSet() {
		seed := clusterConfig.SeedConfig.Value
		trimOptString(&seed.DeploymentId)
		trimOptString(&seed.Zone1)
		trimOptString(&seed.Zone2)
		trimOptString(&seed.MediatorZone)
		trimOptString(&seed.CaUri)
		trimOptString(&seed.OntapLicenseSecretUri)
		trimOptString(&seed.ServiceAccountEmail)
		for i := range seed.OntapCredentials {
			trimOntapCredential(&seed.OntapCredentials[i])
		}
		clusterConfig.SeedConfig.SetTo(seed)
	}
	if clusterConfig.ActualLayout.IsSet() {
		layout := clusterConfig.ActualLayout.Value
		if layout.IpConfig.IsSet() {
			trimIpConfig(&layout.IpConfig.Value)
		}
		trimOptString(&layout.ActiveZone)
		trimOptString(&layout.StandbyZone)
		trimOptString(&layout.ForwardingRuleIp)
		clusterConfig.ActualLayout.SetTo(layout)
	}
	req.ClusterConfig.SetTo(clusterConfig)
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

func trimOntapCredential(cred *vcmserver.OntapCredentialV1) {
	if cert, ok := cred.CertificateAuthCreds.Get(); ok {
		trimOptString(&cert.CertSecretId)
		trimOptString(&cert.CertPrivateKeySecretId)
		cred.CertificateAuthCreds.SetTo(cert)
	}
	if pwd, ok := cred.PasswordAuthCreds.Get(); ok {
		trimOptString(&pwd.Username)
		trimOptString(&pwd.PasswordSecretId)
		cred.PasswordAuthCreds.SetTo(pwd)
	}
}

func trimIpConfig(cfg *vcmserver.IpConfigV1) {
	for i := range cfg.VmLifs {
		trimOptString(&cfg.VmLifs[i].Name)
		trimOptString(&cfg.VmLifs[i].Nodemgmt)
	}
}

func dbConfigEqual(a, b vcmserver.StateDbConfigV1) bool {
	return a.GetProject() == b.GetProject() &&
		a.GetInstance() == b.GetInstance() &&
		a.GetDatabase() == b.GetDatabase()
}

func (s *Store) validateDB(parent context.Context, gen uint64, db vcmserver.StateDbConfigV1) {
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
		s.dispatchOngoingJobResume(parent)
	} else {
		_ = store.Close()
	}
}

func (s *Store) dispatchOngoingJobResume(ctx context.Context) {
	s.mu.RLock()
	resumer := s.jobResumer
	s.mu.RUnlock()
	if resumer == nil {
		return
	}

	go func() {
		if err := resumer.ResumeOngoingJobs(context.WithoutCancel(ctx)); err != nil {
			s.logger.ErrorContext(ctx, "failed to resume ongoing VCM jobs", "error", err)
		}
	}()
}

// VLMConfigRebuildStarter dispatches the VLM config rebuild workflow.
type VLMConfigRebuildStarter interface {
	StartRebuildVLMConfig(ctx context.Context, params *commonparams.GetVLMConfigParams) error
}

func (s *Store) SetVLMConfigRebuildStarter(starter VLMConfigRebuildStarter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vlmConfigRebuildStarter = starter
}

// IsVLMConfigReady reports whether a rebuilt VLM config is available.
func (s *Store) IsVLMConfigReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vlmConfigJSON != ""
}

// GetVLMConfig returns the cached VLM config JSON.
func (s *Store) GetVLMConfig() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vlmConfigJSON
}

func (s *Store) SetVLMConfig(_ context.Context, jsonStr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vlmConfigJSON = jsonStr
	s.vlmConfigFetching = false
	return nil
}

func (s *Store) ClearVLMConfigFetching(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vlmConfigFetching = false
	return nil
}

// rebuildSignatureLocked returns a deterministic fingerprint of the mutable
// rebuild inputs (zones and ONTAP credentials). A change here invalidates the
// cached VLM config and retriggers a rebuild; stable identity fields (deployment
// id, tenant project, CA URI) are intentionally excluded. The caller must hold
// s.mu.
func (s *Store) rebuildSignatureLocked() string {
	seed := currentSeedConfig(s.config)
	parts := []string{
		seed.Zone1.Value,
		seed.Zone2.Value,
		seed.MediatorZone.Value,
	}
	for _, credential := range seed.OntapCredentials {
		cert := credential.CertificateAuthCreds.Value
		pwd := credential.PasswordAuthCreds.Value
		parts = append(parts,
			string(credential.Role),
			string(credential.ActiveAuthType.Value),
			cert.CertSecretId.Value,
			cert.CertPrivateKeySecretId.Value,
			pwd.Username.Value,
			pwd.PasswordSecretId.Value,
		)
	}
	return strings.Join(parts, "\x00")
}

// invalidateVLMConfigLocked drops the cached VLM config and resets the fetching
// state so startVlmConfigRebuild dispatches a fresh rebuild. The caller must
// hold s.mu.
func (s *Store) invalidateVLMConfigLocked() {
	s.vlmConfigJSON = ""
	s.vlmConfigFetching = false
	s.vlmConfigFetchingSince = time.Time{}
}

// startVlmConfigRebuild marks the store as fetching and returns the rebuild
// params when a rebuild is needed, else nil. The caller must hold s.mu.
func (s *Store) startVlmConfigRebuild() *commonparams.GetVLMConfigParams {
	if s.vlmConfigRebuildStarter == nil || s.vlmConfigJSON != "" {
		return nil
	}
	// A rebuild that never finalized (e.g. the process died mid-flight) leaves
	// the flag set; retrigger once the in-flight attempt is considered stale.
	if s.vlmConfigFetching && time.Since(s.vlmConfigFetchingSince) < vlmConfigFetchStaleTimeout {
		return nil
	}
	seed := currentSeedConfig(s.config)
	params := &commonparams.GetVLMConfigParams{
		DeploymentID:         seed.DeploymentId.Value,
		ServiceTenantProject: optionalInt64String(seed.ServiceTenantProjectNum),
		Zone1:                seed.Zone1.Value,
		Zone2:                seed.Zone2.Value,
		MediatorZone:         seed.MediatorZone.Value,
		CaURI:                seed.CaUri.Value,
		OntapCredentials:     ToVCMOntapCredentials(seed.OntapCredentials),
	}
	s.vlmConfigFetching = true
	s.vlmConfigFetchingSince = time.Now()
	return params
}

func (s *Store) dispatchVLMConfigRebuild(ctx context.Context, params *commonparams.GetVLMConfigParams) {
	if params == nil {
		return
	}
	s.mu.RLock()
	starter := s.vlmConfigRebuildStarter
	s.mu.RUnlock()
	if starter == nil {
		return
	}

	go func() {
		if err := starter.StartRebuildVLMConfig(context.WithoutCancel(ctx), params); err != nil {
			s.logger.Error("failed to start VLM config rebuild",
				"deploymentId", params.DeploymentID, "error", err)
			s.mu.Lock()
			s.vlmConfigFetching = false
			s.mu.Unlock()
		}
	}()
}

func currentSeedConfig(config vcmserver.SetConfigRequestV1) vcmserver.SeedConfigV1 {
	if !config.ClusterConfig.IsSet() || !config.ClusterConfig.Value.SeedConfig.IsSet() {
		return vcmserver.SeedConfigV1{}
	}
	return config.ClusterConfig.Value.SeedConfig.Value
}

func optionalInt64String(value vcmserver.OptInt64) string {
	if !value.IsSet() {
		return ""
	}
	return strconv.FormatInt(value.Value, 10)
}

func cloneSetConfigRequestV1(src vcmserver.SetConfigRequestV1) vcmserver.SetConfigRequestV1 {
	dst := src
	if !src.ClusterConfig.IsSet() {
		return dst
	}
	clusterConfig := src.ClusterConfig.Value
	if clusterConfig.SeedConfig.IsSet() {
		seed := clusterConfig.SeedConfig.Value
		seed.OntapCredentials = cloneOntapCredentials(seed.OntapCredentials)
		clusterConfig.SeedConfig.SetTo(seed)
	}
	if clusterConfig.ActualLayout.IsSet() {
		layout := clusterConfig.ActualLayout.Value
		layout.Nodes = cloneVSANodes(layout.Nodes)
		clusterConfig.ActualLayout.SetTo(layout)
	}
	dst.ClusterConfig.SetTo(clusterConfig)
	return dst
}

func cloneOntapCredentials(src []vcmserver.OntapCredentialV1) []vcmserver.OntapCredentialV1 {
	if len(src) == 0 {
		return nil
	}
	return append([]vcmserver.OntapCredentialV1(nil), src...)
}

func cloneVSANodes(src []vcmserver.VsaNodeV1) []vcmserver.VsaNodeV1 {
	if src == nil {
		return nil
	}
	nodes := make([]vcmserver.VsaNodeV1, len(src))
	copy(nodes, src)
	for i := range nodes {
		if nodes[i].Disks != nil {
			disks := make([]vcmserver.AttachedDiskV1, len(nodes[i].Disks))
			copy(disks, nodes[i].Disks)
			nodes[i].Disks = disks
		}
	}
	return nodes
}
