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
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
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
	onOpen func(context.Context, vcmserver.DbConfigV1) (vcmdb.Storage, error)
}

func (s *stubDBOpener) Open(ctx context.Context, db vcmserver.DbConfigV1) (vcmdb.Storage, error) {
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

func openSQLiteStore(ctx context.Context) (vcmdb.Storage, error) {
	return vcmdb.Open(ctx, vcmdb.Config{
		Driver: vcmdb.DriverSQLite,
		SQLite: vcmdb.SQLConfig{DSN: "file:vcm-config-test?mode=memory&cache=shared"},
	}, log.NewLogger(), true)
}

func testDBConfig() vcmserver.DbConfigV1 {
	return vcmserver.DbConfigV1{
		Project:  "test-project",
		Instance: "test-instance",
		Database: "test-db",
	}
}

func testSetConfigRequest(db vcmserver.DbConfigV1, zone1 string) *vcmserver.SetConfigRequestV1 {
	req := &vcmserver.SetConfigRequestV1{DbConfig: db}
	if zone1 != "" {
		req.Zone1.SetTo(zone1)
	}
	return req
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	require.Eventually(t, condition, 2*time.Second, 10*time.Millisecond)
}

func TestStore_Apply_MergesTopLevelFieldsFirstWriteWins(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), "us-central1-a"))
	waitFor(t, func() bool { return store.Configured() })

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), "us-central1-b"))

	setConfig := store.CurrentSetConfig()
	zone1, ok := setConfig.Zone1.Get()
	require.True(t, ok)
	assert.Equal(t, "us-central1-a", zone1)
}

func TestStore_Apply_BlankOptionalStringTreatedAsUnset(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})

	blankZoneReq := testSetConfigRequest(testDBConfig(), "")
	blankZoneReq.Zone1.SetTo("   ")
	store.Apply(context.Background(), blankZoneReq)
	waitFor(t, func() bool { return store.Configured() })

	setConfig := store.CurrentSetConfig()
	assert.False(t, setConfig.Zone1.IsSet())

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), "us-central1-b"))
	setConfig = store.CurrentSetConfig()
	zone1, ok := setConfig.Zone1.Get()
	require.True(t, ok)
	assert.Equal(t, "us-central1-b", zone1)
}

func TestStore_Apply_ReplacesDbConfig(t *testing.T) {
	opener := &stubDBOpener{delay: 10 * time.Millisecond}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	first := testDBConfig()
	store.Apply(context.Background(), testSetConfigRequest(first, ""))
	waitFor(t, func() bool { return store.Configured() })

	second := vcmserver.DbConfigV1{
		Project:  "other-project",
		Instance: "other-instance",
		Database: "other-db",
	}
	store.Apply(context.Background(), testSetConfigRequest(second, ""))
	waitFor(t, func() bool {
		setConfig := store.CurrentSetConfig()
		return setConfig.DbConfig.GetProject() == "other-project" && store.Configured()
	})

	setConfig := store.CurrentSetConfig()
	assert.Equal(t, "other-project", setConfig.DbConfig.GetProject())
	assert.Equal(t, "other-instance", setConfig.DbConfig.GetInstance())
	assert.Equal(t, "other-db", setConfig.DbConfig.GetDatabase())
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
		onOpen: func(ctx context.Context, db vcmserver.DbConfigV1) (vcmdb.Storage, error) {
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
		onOpen: func(ctx context.Context, db vcmserver.DbConfigV1) (vcmdb.Storage, error) {
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
		onOpen: func(ctx context.Context, db vcmserver.DbConfigV1) (vcmdb.Storage, error) {
			startedOnce.Do(func() { close(started) })
			return openSQLiteStore(ctx)
		},
	}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), ""))
	waitFor(t, func() bool { return store.Configured() })

	store.Apply(context.Background(), testSetConfigRequest(vcmserver.DbConfigV1{
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
		onOpen: func(ctx context.Context, _ vcmserver.DbConfigV1) (vcmdb.Storage, error) {
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
		Step:      "CREATE_CLUSTER",
		State:     vcmdb.JobStateProcessing,
		Retriable: true,
	}))

	stored, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, vcmdb.JobStateProcessing, stored.State)
	assert.Equal(t, "CREATE_CLUSTER", stored.CurrentStep)
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
	req.OntapCredentials = []vcmserver.OntapCredentialsV1{
		{Type: vcmserver.OntapCredentialsV1TypeDefault, SecretId: vcmserver.NewOptString("secret-a")},
	}
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	setConfig := store.CurrentSetConfig()
	require.Len(t, setConfig.OntapCredentials, 1)

	setConfig.OntapCredentials[0].SecretId.SetTo("mutated-secret")

	internal := store.CurrentSetConfig()
	require.Len(t, internal.OntapCredentials, 1)
	secretID, ok := internal.OntapCredentials[0].SecretId.Get()
	require.True(t, ok)
	assert.Equal(t, "secret-a", secretID)
}

func TestStore_NodeProviderInput(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD_SEC_MGR)

	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})

	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	req.DeploymentId = vcmserver.NewOptString("gcnv-test")
	req.OntapCredentials = []vcmserver.OntapCredentialsV1{
		{
			Type:          vcmserver.OntapCredentialsV1TypeDefault,
			SecretId:      vcmserver.NewOptString("secret-a"),
			CertificateId: vcmserver.NewOptString("cert-a"),
		},
	}
	req.IpConfig.SetTo(vcmserver.IpConfigV1{
		Lifs: []vcmserver.VmLifsV1{
			{
				Name:     vcmserver.NewOptString("vm1"),
				Nodemgmt: vcmserver.NewOptString("198.18.0.5"),
			},
		},
	})
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	assert.Equal(t, "gcnv-test", input.DeploymentName)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, "secret-a", input.OntapCredentials.SecretID)
	assert.Equal(t, "", input.OntapCredentials.CertificateID) // ignored for secret-manager auth
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, input.OntapCredentials.AuthType)
	require.Len(t, input.Nodes, 1)
	assert.Equal(t, "vm1", input.Nodes[0].Name)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
}

func TestStore_Apply_ReplacesIpConfigWhenPresent(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "")
	req.IpConfig.SetTo(vcmserver.IpConfigV1{
		Lifs: []vcmserver.VmLifsV1{
			{Name: vcmserver.NewOptString("vm1"), Nodemgmt: vcmserver.NewOptString("198.18.0.5")},
		},
	})

	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	setConfig := store.CurrentSetConfig()
	require.True(t, setConfig.IpConfig.IsSet())
	require.Len(t, setConfig.IpConfig.Value.Lifs, 1)
	ip, ok := setConfig.IpConfig.Value.Lifs[0].Nodemgmt.Get()
	require.True(t, ok)
	assert.Equal(t, "198.18.0.5", ip)

	// Runtime LIF IPs can change; later SetConfig replaces ipConfig when present.
	req2 := testSetConfigRequest(testDBConfig(), "")
	req2.IpConfig.SetTo(vcmserver.IpConfigV1{
		Lifs: []vcmserver.VmLifsV1{
			{Name: vcmserver.NewOptString("vm1"), Nodemgmt: vcmserver.NewOptString("198.18.0.99")},
		},
	})
	store.Apply(context.Background(), req2)
	ip, ok = store.CurrentSetConfig().IpConfig.Value.Lifs[0].Nodemgmt.Get()
	require.True(t, ok)
	assert.Equal(t, "198.18.0.99", ip)
}

func TestStore_Apply_PreservesIpConfigWhenOmitted(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "")
	req.IpConfig.SetTo(vcmserver.IpConfigV1{
		Lifs: []vcmserver.VmLifsV1{
			{Name: vcmserver.NewOptString("vm1"), Nodemgmt: vcmserver.NewOptString("198.18.0.5")},
		},
	})

	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), "us-central1-a"))

	ip, ok := store.CurrentSetConfig().IpConfig.Value.Lifs[0].Nodemgmt.Get()
	require.True(t, ok)
	assert.Equal(t, "198.18.0.5", ip)
}

func TestStore_Apply_StaleValidationDoesNotUnsetConfigured(t *testing.T) {
	var startedOnce sync.Once
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	opener := &stubDBOpener{
		onOpen: func(ctx context.Context, db vcmserver.DbConfigV1) (vcmdb.Storage, error) {
			if db.GetProject() == "slow-project" {
				startedOnce.Do(func() { close(firstStarted) })
				<-releaseFirst
			}
			return openSQLiteStore(ctx)
		},
	}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	store.Apply(context.Background(), testSetConfigRequest(vcmserver.DbConfigV1{
		Project:  "slow-project",
		Instance: "slow-instance",
		Database: "slow-db",
	}, ""))
	<-firstStarted

	store.Apply(context.Background(), testSetConfigRequest(vcmserver.DbConfigV1{
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
		onOpen: func(openCtx context.Context, db vcmserver.DbConfigV1) (vcmdb.Storage, error) {
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
