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
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconstants "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/constants"
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

	snapshot := store.Snapshot()
	zone1, ok := snapshot.Zone1.Get()
	require.True(t, ok)
	assert.Equal(t, "us-central1-a", zone1)
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
		snapshot := store.Snapshot()
		return snapshot.DbConfig.GetProject() == "other-project" && store.Configured()
	})

	snapshot := store.Snapshot()
	assert.Equal(t, "other-project", snapshot.DbConfig.GetProject())
	assert.Equal(t, "other-instance", snapshot.DbConfig.GetInstance())
	assert.Equal(t, "other-db", snapshot.DbConfig.GetDatabase())
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
		Type:       vcmconstants.JobTypePoolCreate,
		State:      vcmdb.JobStateNew,
		DeadlineAt: time.Now().UTC().Add(9000 * time.Second),
	}
	require.NoError(t, store.CreateJob(ctx, job))
	require.NoError(t, store.UpdateJobProgress(
		ctx,
		job.ID,
		"CREATE_CLUSTER",
		vcmdb.JobStateProcessing,
		"",
	))

	stored, err := store.GetJob(ctx, job.ID)
	require.NoError(t, err)
	assert.Equal(t, vcmdb.JobStateProcessing, stored.State)
	assert.Equal(t, "CREATE_CLUSTER", stored.CurrentStep)
}

func TestStore_CreateJobBeforeConfigurationFails(t *testing.T) {
	store := NewStore(log.NewLogger(), 0)

	err := store.CreateJob(context.Background(), &vcmdb.Job{})

	assert.ErrorIs(t, err, ErrDatabaseNotConfigured)
}

func openConfigTestStorage(t *testing.T) vcmdb.Storage {
	t.Helper()
	store, err := vcmdb.Open(context.Background(), vcmdb.Config{
		Driver: vcmdb.DriverSQLite,
		SQLite: vcmdb.SQLConfig{
			DSN: "file:vcm-config-expiry-" + t.Name() + "?mode=memory&cache=shared",
		},
	}, log.NewLogger(), true)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	return store
}

func TestStore_LoadOngoingJobs_ExpiresTimedOutJobs(t *testing.T) {
	ctx := context.Background()
	dbStore := openConfigTestStorage(t)
	now := time.Now().UTC()

	expiredJob := &vcmdb.Job{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		ResourceID: "resource-expired",
		Type:       vcmconstants.JobTypePoolCreate,
		DeadlineAt: now.Add(-time.Hour),
		CreatedAt:  now.Add(-2 * time.Hour),
	}
	freshJob := &vcmdb.Job{
		ID:         "550e8400-e29b-41d4-a716-446655440001",
		ResourceID: "resource-fresh",
		Type:       vcmconstants.JobTypePoolCreate,
		DeadlineAt: now.Add(30 * time.Minute),
		CreatedAt:  now.Add(-30 * time.Minute),
	}
	require.NoError(t, dbStore.CreateJob(ctx, expiredJob))
	require.NoError(t, dbStore.CreateJob(ctx, freshJob))

	store := NewStore(log.NewLogger(), 0)
	require.NoError(t, store.loadOngoingJobs(ctx, dbStore))

	storedExpired, err := dbStore.GetJob(ctx, expiredJob.ID)
	require.NoError(t, err)
	assert.Equal(t, vcmdb.JobStateError, storedExpired.State)
	assert.Equal(t, vcmconstants.OperationTimedOut, storedExpired.ErrorMessage)

	ongoing, err := dbStore.ListOngoingJobs(ctx)
	require.NoError(t, err)
	require.Len(t, ongoing, 1)
	assert.Equal(t, freshJob.ID, ongoing[0].ID)
}

type jobNotFoundOnUpdateStorage struct {
	vcmdb.Storage
}

func (s jobNotFoundOnUpdateStorage) UpdateJobProgress(context.Context, string, string, vcmdb.JobState, string) error {
	return vcmdb.ErrJobNotFound
}

type listOngoingJobsErrorStorage struct {
	vcmdb.Storage
}

func (s listOngoingJobsErrorStorage) ListOngoingJobs(context.Context) ([]vcmdb.Job, error) {
	return nil, errors.New("transient list timeout")
}

type updateJobProgressErrorStorage struct {
	vcmdb.Storage
}

func (s updateJobProgressErrorStorage) UpdateJobProgress(context.Context, string, string, vcmdb.JobState, string) error {
	return errors.New("transient update timeout")
}

func TestStore_LoadOngoingJobs_SkipsMissingExpiredJobs(t *testing.T) {
	ctx := context.Background()
	dbStore := openConfigTestStorage(t)
	now := time.Now().UTC()

	expiredJob := &vcmdb.Job{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		ResourceID: "resource-expired",
		Type:       vcmconstants.JobTypePoolCreate,
		DeadlineAt: now.Add(-time.Hour),
		CreatedAt:  now.Add(-2 * time.Hour),
	}
	freshJob := &vcmdb.Job{
		ID:         "550e8400-e29b-41d4-a716-446655440001",
		ResourceID: "resource-fresh",
		Type:       vcmconstants.JobTypePoolCreate,
		DeadlineAt: now.Add(30 * time.Minute),
		CreatedAt:  now.Add(-30 * time.Minute),
	}
	require.NoError(t, dbStore.CreateJob(ctx, expiredJob))
	require.NoError(t, dbStore.CreateJob(ctx, freshJob))

	store := NewStore(log.NewLogger(), 0)
	require.NoError(t, store.loadOngoingJobs(ctx, jobNotFoundOnUpdateStorage{Storage: dbStore}))

	// Expired job still ongoing in the real store because the update was skipped.
	ongoing, err := dbStore.ListOngoingJobs(ctx)
	require.NoError(t, err)
	require.Len(t, ongoing, 2)
}

func TestStore_LoadOngoingJobs_ContinuesAfterUpdateFailure(t *testing.T) {
	ctx := context.Background()
	dbStore := openConfigTestStorage(t)
	now := time.Now().UTC()

	expiredJob := &vcmdb.Job{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		ResourceID: "resource-expired",
		Type:       vcmconstants.JobTypePoolCreate,
		DeadlineAt: now.Add(-time.Hour),
		CreatedAt:  now.Add(-2 * time.Hour),
	}
	freshJob := &vcmdb.Job{
		ID:         "550e8400-e29b-41d4-a716-446655440001",
		ResourceID: "resource-fresh",
		Type:       vcmconstants.JobTypePoolCreate,
		DeadlineAt: now.Add(30 * time.Minute),
		CreatedAt:  now.Add(-30 * time.Minute),
	}
	require.NoError(t, dbStore.CreateJob(ctx, expiredJob))
	require.NoError(t, dbStore.CreateJob(ctx, freshJob))

	store := NewStore(log.NewLogger(), 0)
	require.NoError(t, store.loadOngoingJobs(ctx, updateJobProgressErrorStorage{Storage: dbStore}))

	ongoing, err := dbStore.ListOngoingJobs(ctx)
	require.NoError(t, err)
	require.Len(t, ongoing, 2)
}

func TestStore_Apply_ConfiguresWhenJobRecoveryFails(t *testing.T) {
	opener := &stubDBOpener{
		onOpen: func(ctx context.Context, _ vcmserver.DbConfigV1) (vcmdb.Storage, error) {
			base, err := openSQLiteStore(ctx)
			if err != nil {
				return nil, err
			}
			return listOngoingJobsErrorStorage{Storage: base}, nil
		},
	}
	store := NewStoreWithOpener(log.NewLogger(), opener)

	store.Apply(context.Background(), testSetConfigRequest(testDBConfig(), ""))
	waitFor(t, func() bool { return store.Configured() })

	require.NoError(t, store.CreateJob(context.Background(), &vcmdb.Job{
		ID:         "550e8400-e29b-41d4-a716-446655440099",
		ResourceID: "resource-after-recovery-failure",
		Type:       vcmconstants.JobTypePoolCreate,
		DeadlineAt: time.Now().UTC().Add(time.Hour),
	}))
}

func TestStore_Snapshot_DeepCopyIsolatedFromStore(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})

	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	req.OntapCredentials = []vcmserver.OntapCredentialsV1{
		{Type: vcmserver.OntapCredentialsV1TypeDefault, SecretId: vcmserver.NewOptString("secret-a")},
	}
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	snapshot := store.Snapshot()
	require.Len(t, snapshot.OntapCredentials, 1)

	snapshot.OntapCredentials[0].SecretId.SetTo("mutated-secret")

	internal := store.Snapshot()
	secretID, ok := internal.OntapCredentials[0].SecretId.Get()
	require.True(t, ok)
	assert.Equal(t, "secret-a", secretID)
}

func TestStore_Apply_IgnoresReadOnlyIPConfig(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "")
	req.IpConfig.SetTo(vcmserver.IpConfigV1{
		Lifs: []vcmserver.VmLifsV1{{Name: vcmserver.NewOptString("vm1")}},
	})

	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	assert.False(t, store.Snapshot().IpConfig.IsSet())
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
