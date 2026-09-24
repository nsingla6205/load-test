package config

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

type stubDBOpener struct {
	mu     sync.Mutex
	calls  int
	delay  time.Duration
	err    error
	onOpen func(context.Context, vcmserver.StateDbConfigV1) (vcmdb.Storage, error)
}

func (s *stubDBOpener) Open(ctx context.Context, db vcmserver.StateDbConfigV1) (vcmdb.Storage, error) {
	s.mu.Lock()
	s.calls++
	delay := s.delay
	err := s.err
	onOpen := s.onOpen
	s.mu.Unlock()

	if onOpen != nil {
		return onOpen(ctx, db)
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	if err != nil {
		return nil, err
	}
	return openSQLiteStore(ctx)
}

func (s *stubDBOpener) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type stubOngoingJobResumer struct {
	calls atomic.Int32
	err   error
}

func (s *stubOngoingJobResumer) ResumeOngoingJobs(context.Context) error {
	s.calls.Add(1)
	return s.err
}

func openSQLiteStore(ctx context.Context) (vcmdb.Storage, error) {
	return vcmdb.Open(ctx, vcmdb.Config{
		Driver: vcmdb.DriverSQLite,
		SQLite: vcmdb.SQLConfig{DSN: "file:vcm-config-test?mode=memory&cache=shared"},
	}, log.NewLogger(), true)
}

func testDBConfig() vcmserver.StateDbConfigV1 {
	return vcmserver.StateDbConfigV1{
		Project:  "test-project",
		Instance: "test-instance",
		Database: "test-db",
	}
}

func testSetConfigRequest(db vcmserver.StateDbConfigV1, zone1 string) *vcmserver.SetConfigRequestV1 {
	req := &vcmserver.SetConfigRequestV1{StateDbConfig: db}
	if zone1 != "" {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.Zone1.SetTo(zone1)
		})
	}
	return req
}

func mutateSeed(req *vcmserver.SetConfigRequestV1, mutate func(*vcmserver.SeedConfigV1)) {
	var clusterConfig vcmserver.ClusterConfigV1
	if req.ClusterConfig.IsSet() {
		clusterConfig = req.ClusterConfig.Value
	}
	var seed vcmserver.SeedConfigV1
	if clusterConfig.SeedConfig.IsSet() {
		seed = clusterConfig.SeedConfig.Value
	}
	mutate(&seed)
	clusterConfig.SeedConfig.SetTo(seed)
	req.ClusterConfig.SetTo(clusterConfig)
}

func mutateLayout(req *vcmserver.SetConfigRequestV1, mutate func(*vcmserver.ClusterLayoutV1)) {
	var clusterConfig vcmserver.ClusterConfigV1
	if req.ClusterConfig.IsSet() {
		clusterConfig = req.ClusterConfig.Value
	}
	var layout vcmserver.ClusterLayoutV1
	if clusterConfig.ActualLayout.IsSet() {
		layout = clusterConfig.ActualLayout.Value
	}
	mutate(&layout)
	clusterConfig.ActualLayout.SetTo(layout)
	req.ClusterConfig.SetTo(clusterConfig)
}

func seedOf(config vcmserver.SetConfigRequestV1) vcmserver.SeedConfigV1 {
	return config.ClusterConfig.Value.SeedConfig.Value
}

func layoutOf(config vcmserver.SetConfigRequestV1) vcmserver.ClusterLayoutV1 {
	return config.ClusterConfig.Value.ActualLayout.Value
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	require.Eventually(t, condition, 2*time.Second, 10*time.Millisecond)
}

func TestStore_Apply_RebuildFieldsLastWriteWins(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), "us-central1-a"))
	waitFor(t, func() bool { return store.Configured() })

	// Rebuild-relevant fields (e.g. zone1) update when a later re-seed changes
	// them so the VLM config can be rebuilt for the new inputs.
	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), "us-central1-b"))

	setConfig := store.CurrentSetConfig()
	zone1, ok := seedOf(setConfig).Zone1.Get()
	require.True(t, ok)
	assert.Equal(t, "us-central1-b", zone1)
}

func TestStore_Apply_BlankOptionalStringTreatedAsUnset(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})

	blankZoneReq := testSetConfigRequest(testDBConfig(), "")
	mutateSeed(blankZoneReq, func(seed *vcmserver.SeedConfigV1) {
		seed.Zone1.SetTo("   ")
	})
	store.Apply(context.Background(), blankZoneReq)
	waitFor(t, func() bool { return store.Configured() })

	setConfig := store.CurrentSetConfig()
	assert.False(t, seedOf(setConfig).Zone1.IsSet())

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), "us-central1-b"))
	setConfig = store.CurrentSetConfig()
	zone1, ok := seedOf(setConfig).Zone1.Get()
	require.True(t, ok)
	assert.Equal(t, "us-central1-b", zone1)
}

func TestStore_Apply_ReplacesDbConfig(t *testing.T) {
	opener := &stubDBOpener{delay: 10 * time.Millisecond}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	first := testDBConfig()
	store.Apply(context.Background(), testSetConfigRequest(first, ""))
	waitFor(t, func() bool { return store.Configured() })

	second := vcmserver.StateDbConfigV1{
		Project:  "other-project",
		Instance: "other-instance",
		Database: "other-db",
	}
	store.Apply(context.Background(), testSetConfigRequest(second, ""))
	waitFor(t, func() bool {
		setConfig := store.CurrentSetConfig()
		return setConfig.StateDbConfig.GetProject() == "other-project" && store.Configured()
	})

	setConfig := store.CurrentSetConfig()
	assert.Equal(t, "other-project", setConfig.StateDbConfig.GetProject())
	assert.Equal(t, "other-instance", setConfig.StateDbConfig.GetInstance())
	assert.Equal(t, "other-db", setConfig.StateDbConfig.GetDatabase())
}

func TestStore_Apply_SameDbConfigDoesNotRevalidate(t *testing.T) {
	opener := &stubDBOpener{delay: 10 * time.Millisecond}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })
	assert.Equal(t, 1, opener.callCount())

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), "us-central1-b"))
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, opener.callCount())
}

func TestStore_Apply_SameDbConfigDoesNotStartConcurrentValidation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	releaseValidation := func() {
		releaseOnce.Do(func() { close(release) })
	}
	t.Cleanup(releaseValidation)

	opener := &stubDBOpener{
		onOpen: func(ctx context.Context, db vcmserver.StateDbConfigV1) (vcmdb.Storage, error) {
			startedOnce.Do(func() { close(started) })
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return openSQLiteStore(ctx)
			}
		},
	}
	store := NewStoreWithOpener(log.NewLogger(), opener)
	req := testSetConfigRequest(testDBConfig(), "")

	store.Apply(context.Background(), req)
	<-started

	store.Apply(context.Background(), req)
	store.Apply(context.Background(), req)
	store.Apply(context.Background(), req)
	assert.Equal(t, 1, opener.callCount())

	releaseValidation()
	waitFor(t, func() bool { return store.Configured() })
}

func TestStore_Apply_SameDbConfigRetriesAfterValidationFailure(t *testing.T) {
	var attempts atomic.Int32
	opener := &stubDBOpener{
		onOpen: func(ctx context.Context, db vcmserver.StateDbConfigV1) (vcmdb.Storage, error) {
			if attempts.Add(1) == 1 {
				return nil, errors.New("connection refused")
			}
			return openSQLiteStore(ctx)
		},
	}
	store := NewStoreWithOpener(log.NewLogger(), opener)
	req := testSetConfigRequest(testDBConfig(), "")

	store.Apply(context.Background(), req)
	waitFor(t, func() bool {
		store.mu.RLock()
		defer store.mu.RUnlock()
		return opener.callCount() == 1 && !store.validating
	})
	assert.False(t, store.Configured())

	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })
	assert.Equal(t, 2, opener.callCount())
}

func TestStore_Apply_DbConfigChangeSetsConfiguredFalseUntilValidationCompletes(t *testing.T) {
	var startedOnce sync.Once
	started := make(chan struct{})
	opener := &stubDBOpener{
		delay: 200 * time.Millisecond,
		onOpen: func(ctx context.Context, db vcmserver.StateDbConfigV1) (vcmdb.Storage, error) {
			startedOnce.Do(func() { close(started) })
			return openSQLiteStore(ctx)
		},
	}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), ""))
	waitFor(t, func() bool { return store.Configured() })

	store.Apply(context.Background(), testSetConfigRequest(vcmserver.StateDbConfigV1{
		Project:  "changed-project",
		Instance: "changed-instance",
		Database: "changed-db",
	}, ""))

	assert.False(t, store.Configured())
	<-started
	waitFor(t, func() bool { return store.Configured() })
}

func TestStore_Apply_DbValidationFailureKeepsConfiguredFalse(t *testing.T) {
	opener := &stubDBOpener{err: errors.New("connection refused")}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), ""))
	waitFor(t, func() bool { return opener.callCount() == 1 })

	assert.False(t, store.Configured())
}

func TestNewStore_DefaultsValidateTimeout(t *testing.T) {
	store := NewStore(log.NewLogger(), 0)
	assert.Equal(t, DefaultDBValidateTimeout, store.validateTimeout)

	store = NewStore(log.NewLogger(), -time.Second)
	assert.Equal(t, DefaultDBValidateTimeout, store.validateTimeout)

	store = NewStore(log.NewLogger(), 45*time.Second)
	assert.Equal(t, 45*time.Second, store.validateTimeout)
}

func TestStore_Apply_ValidateTimeoutIsHonored(t *testing.T) {
	const validateTimeout = 25 * time.Millisecond
	openResult := make(chan error, 1)
	opener := &stubDBOpener{
		onOpen: func(ctx context.Context, _ vcmserver.StateDbConfigV1) (vcmdb.Storage, error) {
			<-ctx.Done()
			openResult <- ctx.Err()
			return nil, ctx.Err()
		},
	}
	store := newStore(log.NewLogger(), opener, validateTimeout)

	startedAt := time.Now()
	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), ""))

	require.ErrorIs(t, <-openResult, context.DeadlineExceeded)
	assert.Less(t, time.Since(startedAt), time.Second)
	waitFor(t, func() bool {
		store.mu.RLock()
		defer store.mu.RUnlock()
		return !store.validating
	})
	assert.False(t, store.Configured())
}

func TestStore_ConfiguredStartsFalse(t *testing.T) {
	store := NewStore(log.NewLogger(), 0)
	assert.False(t, store.Configured())
}

func TestStore_JobLifecycleUsesConfiguredDatabase(t *testing.T) {
	ctx := context.Background()
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	store.Apply(ctx, testSetConfigRequest(testDBConfig(), ""))
	waitFor(t, func() bool { return store.Configured() })

	job := &vcmdb.Job{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		ResourceID: "550e8400-e29b-41d4-a716-446655440000",
		Type:       string(datamodel.JobTypeCreatePool),
		State:      vcmdb.JobStateNew,
		DeadlineAt: time.Now().UTC().Add(9000 * time.Second),
	}
	require.NoError(t, store.CreateJob(ctx, job))
	require.NoError(t, store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
		JobID:     job.ID,
		State:     vcmdb.JobStateProcessing,
		Retriable: true,
	}))

	stored, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, vcmdb.JobStateProcessing, stored.State)
	assert.Empty(t, stored.CurrentStep)
	assert.True(t, stored.Retriable)

	ongoing, err := store.ListOngoingJobs(ctx)
	require.NoError(t, err)
	require.Len(t, ongoing, 1)
	assert.Equal(t, job.ID, ongoing[0].ID)
}

func TestStore_CreateJobBeforeConfigurationFails(t *testing.T) {
	store := NewStore(log.NewLogger(), 0)

	err := store.CreateJob(context.Background(), &vcmdb.Job{})

	assert.ErrorIs(t, err, ErrDatabaseNotConfigured)
}

func TestStore_CurrentSetConfig_DeepCopyIsolatedFromStore(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})

	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
		seed.OntapCredentials = ontapCredentials("admin-pass",
			passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", "secret-a"))
	})
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	setConfig := store.CurrentSetConfig()
	require.True(t, seedOf(setConfig).OntapCredentials.IsSet())

	mutated := setConfig.ClusterConfig.Value.SeedConfig.Value.OntapCredentials.Value
	def := mutated.DefaultModeUserCredentials.Value
	def.PasswordSecret.SetTo("mutated-secret")
	mutated.DefaultModeUserCredentials.SetTo(def)
	setConfig.ClusterConfig.Value.SeedConfig.Value.OntapCredentials.SetTo(mutated)

	internal := store.CurrentSetConfig()
	secretID := seedOf(internal).OntapCredentials.Value.DefaultModeUserCredentials.Value.PasswordSecret
	got, ok := secretID.Get()
	require.True(t, ok)
	assert.Equal(t, "secret-a", got)
}

func TestStore_NodeProviderInput(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})

	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
		seed.DeploymentId = vcmserver.NewOptString("gcnv-test")
		seed.OntapCredentials = ontapCredentials("admin-pass",
			passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", "secret-a"))
	})
	mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
		layout.Nodes = []vcmserver.VsaNodeV1{layoutNode("vm1", "198.18.0.5")}
	})
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	assert.Equal(t, "gcnv-test", input.DeploymentName)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, "secret-a", input.OntapCredentials.SecretID)
	assert.Equal(t, "", input.OntapCredentials.CertificateID) // ignored for password auth
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, input.OntapCredentials.AuthType)
	require.Len(t, input.Nodes, 1)
	assert.Equal(t, "vm1", input.Nodes[0].Name)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
}

func TestStore_Apply_ReplacesNodesWhenPresent(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "")
	mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
		layout.Nodes = []vcmserver.VsaNodeV1{layoutNode("vm1", "198.18.0.5")}
	})

	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	setConfig := store.CurrentSetConfig()
	require.Len(t, layoutOf(setConfig).Nodes, 1)
	ip, ok := layoutOf(setConfig).Nodes[0].NodeManagementIp.Get()
	require.True(t, ok)
	assert.Equal(t, "198.18.0.5", ip)

	// Runtime addressing can change; a later SetConfig replaces nodes when present.
	req2 := testSetConfigRequest(testDBConfig(), "")
	mutateLayout(req2, func(layout *vcmserver.ClusterLayoutV1) {
		layout.Nodes = []vcmserver.VsaNodeV1{layoutNode("vm1", "198.18.0.99")}
	})
	store.Apply(context.Background(), req2)
	ip, ok = layoutOf(store.CurrentSetConfig()).Nodes[0].NodeManagementIp.Get()
	require.True(t, ok)
	assert.Equal(t, "198.18.0.99", ip)
}

func TestStore_Apply_PreservesNodesWhenOmitted(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "")
	mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
		layout.Nodes = []vcmserver.VsaNodeV1{layoutNode("vm1", "198.18.0.5")}
	})

	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), "us-central1-a"))

	ip, ok := layoutOf(store.CurrentSetConfig()).Nodes[0].NodeManagementIp.Get()
	require.True(t, ok)
	assert.Equal(t, "198.18.0.5", ip)
}

func TestStore_Apply_ReplacesDisksWhenPresent(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "")
	mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
		layout.Disks = []vcmserver.GceDiskV1{{Name: vcmserver.NewOptString("disk-1")}}
	})

	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })
	require.Len(t, layoutOf(store.CurrentSetConfig()).Disks, 1)

	// An explicitly empty list clears the stored disks, while omitting the field
	// preserves them.
	req2 := testSetConfigRequest(testDBConfig(), "")
	mutateLayout(req2, func(layout *vcmserver.ClusterLayoutV1) {
		layout.Disks = []vcmserver.GceDiskV1{}
	})
	store.Apply(context.Background(), req2)
	assert.Empty(t, layoutOf(store.CurrentSetConfig()).Disks)
}

func TestStore_Apply_StaleValidationDoesNotUnsetConfigured(t *testing.T) {
	var startedOnce sync.Once
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	opener := &stubDBOpener{
		onOpen: func(ctx context.Context, db vcmserver.StateDbConfigV1) (vcmdb.Storage, error) {
			if db.GetProject() == "slow-project" {
				startedOnce.Do(func() { close(firstStarted) })
				<-releaseFirst
			}
			return openSQLiteStore(ctx)
		},
	}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	store.Apply(context.Background(), testSetConfigRequest(vcmserver.StateDbConfigV1{
		Project:  "slow-project",
		Instance: "slow-instance",
		Database: "slow-db",
	}, ""))
	<-firstStarted

	store.Apply(context.Background(), testSetConfigRequest(vcmserver.StateDbConfigV1{
		Project:  "fast-project",
		Instance: "fast-instance",
		Database: "fast-db",
	}, ""))
	waitFor(t, func() bool { return store.Configured() })

	close(releaseFirst)
	time.Sleep(100 * time.Millisecond)
	assert.True(t, store.Configured())
}

func TestStore_Apply_PropagatesContextValuesAfterCallerCancel(t *testing.T) {
	type ctxKey struct{}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, utilsmiddleware.CorrelationContextKey, "corr-123")
	ctx = context.WithValue(ctx, ctxKey{}, "trace-value")
	cancel()

	var gotCorrelationID string
	var gotCustomValue string
	opener := &stubDBOpener{
		onOpen: func(openCtx context.Context, db vcmserver.StateDbConfigV1) (vcmdb.Storage, error) {
			gotCorrelationID, _ = openCtx.Value(utilsmiddleware.CorrelationContextKey).(string)
			gotCustomValue, _ = openCtx.Value(ctxKey{}).(string)
			return openSQLiteStore(openCtx)
		},
	}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	store.Apply(ctx, testSetConfigRequest(testDBConfig(), ""))
	waitFor(t, func() bool { return store.Configured() })

	assert.Equal(t, "corr-123", gotCorrelationID)
	assert.Equal(t, "trace-value", gotCustomValue)
}

// fakeRebuildStarter records StartRebuildVLMConfig calls and can inject an error.
type fakeRebuildStarter struct {
	mu     sync.Mutex
	calls  int
	params []*commonparams.GetVLMConfigParams
	err    error
}

func (f *fakeRebuildStarter) StartRebuildVLMConfig(_ context.Context, p *commonparams.GetVLMConfigParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.params = append(f.params, p)
	return f.err
}

func (f *fakeRebuildStarter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeRebuildStarter) lastParams() *commonparams.GetVLMConfigParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.params) == 0 {
		return nil
	}
	return f.params[len(f.params)-1]
}

func fetchingFlag(s *Store) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vlmConfigFetching
}

// rebuildConfigRequest builds a SetConfig request with the fields the VLM config
// rebuild needs.
func rebuildConfigRequest(deploymentID string) *vcmserver.SetConfigRequestV1 {
	req := &vcmserver.SetConfigRequestV1{StateDbConfig: testDBConfig()}
	mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
		if deploymentID != "" {
			seed.DeploymentId.SetTo(deploymentID)
		}
		seed.ServiceTenantProjectNum.SetTo(123)
		seed.Zone1.SetTo("us-central1-a")
		seed.Zone2.SetTo("us-central1-b")
		seed.MediatorZone.SetTo("us-central1-c")
		seed.CaUri.SetTo("123456789012/pool/intermediate")
		seed.OntapCredentials = ontapCredentials("admin-pass",
			passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", "projects/p/secrets/s"))
	})
	return req
}

func TestStore_Apply_TriggersRebuildOnceWhenEmpty(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	starter := &fakeRebuildStarter{}
	store.SetVLMConfigRebuildStarter(starter)

	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))

	waitFor(t, func() bool { return starter.callCount() == 1 })
	require.True(t, fetchingFlag(store))

	p := starter.lastParams()
	require.NotNil(t, p)
	assert.Equal(t, "dep-1", p.DeploymentID)
	assert.Equal(t, "123", p.ServiceTenantProject)
	assert.Equal(t, "us-central1-a", p.Zone1)
	assert.Equal(t, "us-central1-b", p.Zone2)
	assert.Equal(t, "us-central1-c", p.MediatorZone)
	assert.Equal(t, "123456789012/pool/intermediate", p.CaURI)
	// admin (adminPassSecretId) + default (VCM_ADMIN) entries.
	require.Len(t, p.OntapCredentials, 2)
	assert.Equal(t, "admin-pass", commonparams.AdminPasswordSecret(p.OntapCredentials))
	var def commonparams.VCMOntapCredential
	for _, c := range p.OntapCredentials {
		if c.Type == "default" {
			def = c
		}
	}
	assert.Equal(t, "projects/p/secrets/s", def.SecretID)
}

func TestStore_Apply_TriggersEvenWithoutDeploymentID(t *testing.T) {
	// The store triggers purely on config state; input validation (including a
	// missing deployment id) is the workflow's responsibility, so the rebuild is
	// still dispatched with an empty deployment id.
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	starter := &fakeRebuildStarter{}
	store.SetVLMConfigRebuildStarter(starter)

	store.Apply(context.Background(), rebuildConfigRequest(""))

	waitFor(t, func() bool { return starter.callCount() == 1 })
	p := starter.lastParams()
	require.NotNil(t, p)
	assert.Empty(t, p.DeploymentID)
}

func TestStore_Apply_NoRebuildWithoutStarter(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})

	// No starter injected; Apply must not panic and must not set fetching.
	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))

	assert.False(t, fetchingFlag(store))
}

func TestStore_Apply_NoRebuildWhileFetching(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	starter := &fakeRebuildStarter{}
	store.SetVLMConfigRebuildStarter(starter)

	// First apply starts a rebuild (fetching stays true, not yet finalized).
	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))
	waitFor(t, func() bool { return starter.callCount() == 1 })
	require.True(t, fetchingFlag(store))

	// Re-applying the same config while the rebuild is in flight must not start
	// a second one.
	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))

	require.Never(t, func() bool { return starter.callCount() > 1 }, 300*time.Millisecond, 20*time.Millisecond)
}

func TestStore_Apply_RetriggersStaleFetching(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	starter := &fakeRebuildStarter{}
	store.SetVLMConfigRebuildStarter(starter)

	// Start an initial rebuild, then backdate it so the in-flight attempt looks
	// like it never finalized (e.g. the worker died mid-flight).
	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))
	waitFor(t, func() bool { return starter.callCount() == 1 })
	store.mu.Lock()
	store.vlmConfigFetchingSince = time.Now().Add(-2 * vlmConfigFetchStaleTimeout)
	store.mu.Unlock()

	// Re-applying the same config retriggers once the prior attempt is stale.
	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))

	waitFor(t, func() bool { return starter.callCount() == 2 })
}

func TestStore_Apply_NoRebuildWhenConfigUnchanged(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	starter := &fakeRebuildStarter{}
	store.SetVLMConfigRebuildStarter(starter)

	// First apply triggers the rebuild; simulate its completion.
	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))
	waitFor(t, func() bool { return starter.callCount() == 1 })
	require.NoError(t, store.SetVLMConfig(context.Background(), `{"vlm_config":{}}`))

	// Re-applying the same config must not retrigger a rebuild.
	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))

	require.Never(t, func() bool { return starter.callCount() > 1 }, 300*time.Millisecond, 20*time.Millisecond)
}

func TestStore_Apply_RetriggersRebuildOnRebuildFieldChange(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	starter := &fakeRebuildStarter{}
	store.SetVLMConfigRebuildStarter(starter)

	// Settle an initial config and simulate a completed rebuild.
	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))
	waitFor(t, func() bool { return starter.callCount() == 1 })
	require.NoError(t, store.SetVLMConfig(context.Background(), `{"vlm_config":{}}`))

	// A changed rebuild-relevant field (zone2) invalidates the cached config and
	// retriggers the rebuild with the new value.
	changed := rebuildConfigRequest("dep-1")
	mutateSeed(changed, func(seed *vcmserver.SeedConfigV1) {
		seed.Zone2.SetTo("us-central1-f")
	})
	store.Apply(context.Background(), changed)

	waitFor(t, func() bool { return starter.callCount() == 2 })
	assert.Empty(t, store.GetVLMConfig(), "stale config must be cleared on a rebuild-field change")
	assert.Equal(t, "us-central1-f", starter.lastParams().Zone2)
}

func TestStore_Apply_StartErrorClearsFetching(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	starter := &fakeRebuildStarter{err: errors.New("dispatch failed")}
	store.SetVLMConfigRebuildStarter(starter)

	store.Apply(context.Background(), rebuildConfigRequest("dep-1"))

	waitFor(t, func() bool { return starter.callCount() == 1 })
	// The failed start must clear the fetching flag so a later request retries.
	waitFor(t, func() bool { return !fetchingFlag(store) })
}

func TestStore_SetVLMConfig_And_Clear(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})

	assert.False(t, store.IsVLMConfigReady())
	assert.Empty(t, store.GetVLMConfig())

	store.mu.Lock()
	store.vlmConfigFetching = true
	store.mu.Unlock()

	require.NoError(t, store.SetVLMConfig(context.Background(), `{"a":1}`))
	assert.True(t, store.IsVLMConfigReady())
	assert.Equal(t, `{"a":1}`, store.GetVLMConfig())
	assert.False(t, fetchingFlag(store))

	store.mu.Lock()
	store.vlmConfigFetching = true
	store.mu.Unlock()
	require.NoError(t, store.ClearVLMConfigFetching(context.Background()))
	assert.False(t, fetchingFlag(store))
}

func TestCloneVSANodes_PreservesEmptySlice(t *testing.T) {
	assert.Nil(t, cloneVSANodes(nil))

	empty := []vcmserver.VsaNodeV1{}
	cloned := cloneVSANodes(empty)
	require.NotNil(t, cloned)
	assert.Empty(t, cloned)

	src := []vcmserver.VsaNodeV1{{GceVmName: vcmserver.NewOptString("vm1")}}
	cloned = cloneVSANodes(src)
	require.Len(t, cloned, 1)
	cloned[0].GceVmName.SetTo("mutated")
	name, ok := src[0].GceVmName.Get()
	require.True(t, ok)
	assert.Equal(t, "vm1", name)
}

// credByType returns the credential entry of the given type, or a zero value.
func credByType(creds []commonparams.VCMOntapCredential, credType string) commonparams.VCMOntapCredential {
	for _, c := range creds {
		if c.Type == credType {
			return c
		}
	}
	return commonparams.VCMOntapCredential{}
}

func TestCloneVSANodes_DeepCopiesSanIps(t *testing.T) {
	node := vcmserver.VsaNodeV1{GceVmName: vcmserver.NewOptString("vm1")}
	node.IpConfig.SetTo(vcmserver.IpConfigV1{
		DataLif: vcmserver.NewOptDataLifV1(vcmserver.DataLifV1{
			SanIps: []string{"10.10.0.51"},
		}),
	})

	cloned := cloneVSANodes([]vcmserver.VsaNodeV1{node})
	require.Len(t, cloned, 1)
	cloned[0].IpConfig.Value.DataLif.Value.SanIps[0] = "mutated"
	assert.Equal(t, "10.10.0.51", node.IpConfig.Value.DataLif.Value.SanIps[0])
}

func TestCloneGceDisks_PreservesEmptySlice(t *testing.T) {
	assert.Nil(t, cloneGceDisks(nil))

	empty := []vcmserver.GceDiskV1{}
	cloned := cloneGceDisks(empty)
	require.NotNil(t, cloned)
	assert.Empty(t, cloned)

	src := []vcmserver.GceDiskV1{{Name: vcmserver.NewOptString("disk-1")}}
	cloned = cloneGceDisks(src)
	require.Len(t, cloned, 1)
	cloned[0].Name.SetTo("mutated")
	name, ok := src[0].Name.Get()
	require.True(t, ok)
	assert.Equal(t, "disk-1", name)
}

func TestToVCMOntapCredentials_AdminAndPasswordDefault(t *testing.T) {
	oc := vcmserver.OntapCredentialsV1{}
	oc.AdminPassSecretId.SetTo("admin-1")
	oc.DefaultModeUserCredentials.SetTo(passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", "vcm-secret"))

	creds := ToVCMOntapCredentials(vcmserver.NewOptOntapCredentialsV1(oc))
	require.Len(t, creds, 2)

	admin := credByType(creds, "admin")
	assert.Equal(t, "admin-1", admin.SecretID)
	assert.Empty(t, admin.CertificateID)

	def := credByType(creds, "default")
	assert.Equal(t, "vcm-secret", def.SecretID)
	assert.Empty(t, def.CertificateID)
	assert.Equal(t, "root", def.Username)
	assert.False(t, def.IsCertAuth)
}

func TestToVCMOntapCredentials_CertificateDefaultUsesCertAndKey(t *testing.T) {
	oc := vcmserver.OntapCredentialsV1{}
	oc.AdminPassSecretId.SetTo("admin-1")
	oc.DefaultModeUserCredentials.SetTo(certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "cert-1", "key-1"))

	creds := ToVCMOntapCredentials(vcmserver.NewOptOntapCredentialsV1(oc))
	def := credByType(creds, "default")
	assert.Equal(t, "cert-1", def.CertificateID)
	assert.Equal(t, "key-1", def.CertPrivateKeySecretID)
	assert.True(t, def.IsCertAuth)
	assert.Equal(t, "vcm-admin", def.Username)
}

func TestToVCMOntapCredentials_DefaultsToCertificateWhenAuthModeUnset(t *testing.T) {
	def := vcmserver.UserCredentialsV1{}
	def.CertId.SetTo("cert-1")
	def.CertPrivateKeySecret.SetTo("key-1")
	oc := vcmserver.OntapCredentialsV1{}
	oc.AdminPassSecretId.SetTo("admin-1")
	oc.DefaultModeUserCredentials.SetTo(def)

	creds := ToVCMOntapCredentials(vcmserver.NewOptOntapCredentialsV1(oc))
	got := credByType(creds, "default")
	assert.Equal(t, "cert-1", got.CertificateID)
	assert.Equal(t, "key-1", got.CertPrivateKeySecretID)
	assert.True(t, got.IsCertAuth)
}

func TestToVCMOntapCredentials_IncludesOntapModeUser(t *testing.T) {
	oc := vcmserver.OntapCredentialsV1{}
	oc.AdminPassSecretId.SetTo("admin-1")
	oc.DefaultModeUserCredentials.SetTo(certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "cert-1", "key-1"))
	oc.OntapModeUserCredentials.SetTo(passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEONTAPMODE, "om", "om-secret"))

	creds := ToVCMOntapCredentials(vcmserver.NewOptOntapCredentialsV1(oc))
	require.Len(t, creds, 3)
	om := credByType(creds, "ontap-mode")
	assert.Equal(t, "om-secret", om.SecretID)
	assert.Equal(t, "om", om.Username)
}

func TestStore_Apply_EmptyNodesClearsStoredNodes(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "")
	mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
		layout.Nodes = []vcmserver.VsaNodeV1{{GceVmName: vcmserver.NewOptString("vm1")}}
	})
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })
	require.Len(t, layoutOf(store.CurrentSetConfig()).Nodes, 1)

	clear := testSetConfigRequest(testDBConfig(), "")
	mutateLayout(clear, func(layout *vcmserver.ClusterLayoutV1) {
		layout.Nodes = []vcmserver.VsaNodeV1{}
	})
	store.Apply(context.Background(), clear)
	nodes := layoutOf(store.CurrentSetConfig()).Nodes
	require.NotNil(t, nodes)
	assert.Empty(t, nodes)
}

func TestStore_Apply_ResumesOngoingJobsAfterDatabaseIsReady(t *testing.T) {
	resumer := &stubOngoingJobResumer{}
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{})
	store.SetOngoingJobResumer(resumer)

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), ""))

	waitFor(t, func() bool {
		return store.Configured() && resumer.calls.Load() == 1
	})
}

func TestStore_Apply_DoesNotResumeJobsWhenDatabaseValidationFails(t *testing.T) {
	resumer := &stubOngoingJobResumer{}
	opener := &stubDBOpener{err: errors.New("connection refused")}
	store := NewStoreWithOpener(log.NewLogger(), opener)
	store.SetOngoingJobResumer(resumer)

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), ""))

	waitFor(t, func() bool {
		store.mu.RLock()
		defer store.mu.RUnlock()
		return opener.callCount() == 1 && !store.validating
	})
	assert.Zero(t, resumer.calls.Load())
}
