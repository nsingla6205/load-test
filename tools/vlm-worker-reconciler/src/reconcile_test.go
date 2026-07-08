package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeK8s implements k8sClientInterface for tests.
type fakeK8s struct {
	deployments []deploymentItem
	listErr     error
	scaledTo    map[string]int // records name->replicas for each scaleDeployment call
	scaleErr    error
}

func (f *fakeK8s) listVLMWorkerDeployments(_ context.Context) ([]deploymentItem, error) {
	return f.deployments, f.listErr
}

func (f *fakeK8s) scaleDeployment(_ context.Context, name string, replicas int) error {
	if f.scaleErr != nil {
		return f.scaleErr
	}
	if f.scaledTo == nil {
		f.scaledTo = make(map[string]int)
	}
	f.scaledTo[name] = replicas
	return nil
}

// discardLogger returns a logger that silences all output during tests.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// versionRows builds a sqlmock row set for the active-versions query.
func versionRows(versions ...string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"ontapVersion"})
	for _, v := range versions {
		rows.AddRow(v)
	}
	return rows
}

// cfg returns a minimal config suitable for most runWith tests.
func testCfg(dryRun bool) config {
	return config{namespace: "test-ns", dryRun: dryRun}
}

// ── G4: empty active version set ──────────────────────────────────────────────

func TestRunWith_G4_EmptyActiveVersionSet(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnRows(versionRows())

	k8s := &fakeK8s{}
	result, outcome := runWith(context.Background(), testCfg(false), k8s, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomeSkipped, outcome)
	assert.Empty(t, k8s.scaledTo, "no scale calls when active set is empty")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── G5: all workers would scale to zero ───────────────────────────────────────

func TestRunWith_G5_AllWorkersWouldScaleToZero(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Active set: only 9.12.1
	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnRows(versionRows("9.12.1"))

	// Only deployment: 9.11.0 — would normally be scaled to 0,
	// leaving keepActive empty and triggering G5.
	k8s := &fakeK8s{
		deployments: []deploymentItem{
			{Name: "vlm-worker-9-11-0", Replicas: 1},
		},
	}

	result, outcome := runWith(context.Background(), testCfg(false), k8s, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomeSkipped, outcome)
	assert.Empty(t, k8s.scaledTo, "G5 must abort before any scale calls")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── Correct keep/scale classification ─────────────────────────────────────────

func TestRunWith_ScalesInactiveKeepsActive(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Active versions: 9.12.1 and 9.13.0P2
	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnRows(versionRows("9.12.1", "9.13.0P2"))

	k8s := &fakeK8s{
		deployments: []deploymentItem{
			// Rule 1 direct match → KEEP
			{Name: "vlm-worker-9-12-1", Replicas: 1},
			// Rule 1 direct match → KEEP
			{Name: "vlm-worker-9-13-0p2", Replicas: 1},
			// Rule 3 patch ladder: same line (9.13.0), level 3 >= min level 2 → KEEP
			{Name: "vlm-worker-9-13-0p3", Replicas: 1},
			// Rule 2 higher-line migration: 9.12.1 < 9.14.0 → KEEP
			{Name: "vlm-worker-9-14-0", Replicas: 1},
			// No rule applies: 9.11.0 is lower than active set, not in it → SCALE
			{Name: "vlm-worker-9-11-0", Replicas: 1},
			// Rule 3 below min level on 9.12.x line: 9.12.0 has patch 0, not same lineKey as 9.12.1 → SCALE
			// (9.12.0 lineKey is {9,12,0}, 9.12.1 lineKey is {9,12,1} — different, no rule matches)
			{Name: "vlm-worker-9-12-0", Replicas: 1},
		},
	}

	result, outcome := runWith(context.Background(), testCfg(false), k8s, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomeSuccess, outcome)
	assert.Equal(t, map[string]int{
		"vlm-worker-9-11-0": 0,
		"vlm-worker-9-12-0": 0,
	}, k8s.scaledTo)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── Dry-run: no scale calls made ──────────────────────────────────────────────

func TestRunWith_DryRun_NoScaleCalls(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnRows(versionRows("9.12.1"))

	k8s := &fakeK8s{
		deployments: []deploymentItem{
			{Name: "vlm-worker-9-12-1", Replicas: 1}, // KEEP
			{Name: "vlm-worker-9-11-0", Replicas: 1}, // would scale
		},
	}

	result, outcome := runWith(context.Background(), testCfg(true), k8s, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomeSuccess, outcome)
	assert.Empty(t, k8s.scaledTo, "dry-run must not call scaleDeployment")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── Already at zero: skip without calling scaleDeployment ─────────────────────

func TestRunWith_AlreadyAtZero_Skipped(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnRows(versionRows("9.12.1"))

	k8s := &fakeK8s{
		deployments: []deploymentItem{
			{Name: "vlm-worker-9-12-1", Replicas: 1}, // KEEP
			{Name: "vlm-worker-9-11-0", Replicas: 0}, // already at 0 — skip
		},
	}

	result, outcome := runWith(context.Background(), testCfg(false), k8s, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomeSuccess, outcome)
	assert.Empty(t, k8s.scaledTo, "scaleDeployment must not be called for already-zero deployments")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── Scale failure: continues to next deployment ───────────────────────────────

func TestRunWith_ScaleFailure_ContinuesAndCompletes(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnRows(versionRows("9.12.1"))

	// Both 9.11.0 and 9.10.0 should scale; first one errors.
	callCount := 0
	k8s := &fakeK8s{
		deployments: []deploymentItem{
			{Name: "vlm-worker-9-12-1", Replicas: 1}, // KEEP
			{Name: "vlm-worker-9-11-0", Replicas: 1}, // SCALE — will error
			{Name: "vlm-worker-9-10-0", Replicas: 1}, // SCALE — must still be attempted
		},
	}

	// Override scaleDeployment to fail only on the first call.
	k8sPartial := &partialFailK8s{inner: k8s, failFirst: true, callCount: &callCount}

	result, outcome := runWith(context.Background(), testCfg(false), k8sPartial, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomePartialFailure, outcome)
	assert.Equal(t, 2, callCount, "both scale targets must be attempted")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// partialFailK8s wraps fakeK8s and fails the first scaleDeployment call.
type partialFailK8s struct {
	inner     *fakeK8s
	failFirst bool
	callCount *int
}

func (p *partialFailK8s) listVLMWorkerDeployments(ctx context.Context) ([]deploymentItem, error) {
	return p.inner.listVLMWorkerDeployments(ctx)
}

func (p *partialFailK8s) scaleDeployment(ctx context.Context, name string, replicas int) error {
	*p.callCount++
	if p.failFirst && *p.callCount == 1 {
		return errors.New("transient scale error")
	}
	return p.inner.scaleDeployment(ctx, name, replicas)
}

// ── DB query error: exits gracefully ──────────────────────────────────────────

func TestRunWith_DBQueryError_ExitsGracefully(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnError(errors.New("connection refused"))

	k8s := &fakeK8s{}
	result, outcome := runWith(context.Background(), testCfg(false), k8s, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomeSkipped, outcome)
	assert.Empty(t, k8s.scaledTo)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── No deployments found ──────────────────────────────────────────────────────

func TestRunWith_NoDeployments_NothingToReconcile(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnRows(versionRows("9.12.1"))

	k8s := &fakeK8s{deployments: []deploymentItem{}}
	result, outcome := runWith(context.Background(), testCfg(false), k8s, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomeSuccess, outcome)
	assert.Empty(t, k8s.scaledTo)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── List deployments error ────────────────────────────────────────────────────

func TestRunWith_ListDeploymentsError_ExitsGracefully(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnRows(versionRows("9.12.1"))

	k8s := &fakeK8s{listErr: errors.New("forbidden")}
	result, outcome := runWith(context.Background(), testCfg(false), k8s, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomeSkipped, outcome)
	assert.Empty(t, k8s.scaledTo)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ── buildDSN ──────────────────────────────────────────────────────────────────

// TestBuildDSN_EmptyPasswordDoesNotCorruptSSLMode guards against a pq DSN
// parser bug: pq calls SkipSpaces() after '=' before reading the value, so
// "password= sslmode=disable" is parsed as password="sslmode=disable" with
// sslmode unset (defaults to "require"). Omitting the password field entirely
// when it is empty prevents this.
func TestBuildDSN_EmptyPasswordDoesNotCorruptSSLMode(t *testing.T) {
	cfg := config{
		dbHost:                 "127.0.0.1",
		dbPort:                 "5432",
		dbName:                 "vcp",
		dbUser:                 "user@project.iam",
		dbPassword:             "",
		dbSSLMode:              "disable",
		cloudSqlIAMAuthEnabled: true,
	}
	dsn := buildDSN(cfg)
	assert.Contains(t, dsn, "sslmode=disable", "sslmode must be present and correct")
	assert.NotContains(t, dsn, "password=", "empty password must be omitted to avoid pq parser bug")
}

func TestBuildDSN_NonEmptyPasswordIsIncluded(t *testing.T) {
	cfg := config{
		dbHost:     "db.example.com",
		dbPort:     "5432",
		dbName:     "vcp",
		dbUser:     "admin",
		dbPassword: "s3cr3t",
		dbSSLMode:  "require",
	}
	dsn := buildDSN(cfg)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	pw, set := u.User.Password()
	assert.True(t, set, "password must be present in URL userinfo")
	assert.Equal(t, "s3cr3t", pw)
	assert.Contains(t, dsn, "sslmode=require")
}

func TestBuildDSN_IAMAuthForcesSSLDisable(t *testing.T) {
	cfg := config{
		dbHost:                 "127.0.0.1",
		dbPort:                 "5432",
		dbName:                 "vcp",
		dbUser:                 "user@project.iam",
		dbSSLMode:              "require", // would normally require SSL
		cloudSqlIAMAuthEnabled: true,
	}
	dsn := buildDSN(cfg)
	assert.Contains(t, dsn, "sslmode=disable", "IAM auth must force sslmode=disable")
}

// ── waitForCloudSQLProxy ──────────────────────────────────────────────────────

// startFakeTCPListener starts a TCP listener on a random port and returns the
// port string and a stop function. Used to simulate the proxy DB port being
// open and ready.
func startFakeTCPListener(t *testing.T) (port string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port, func() { _ = ln.Close() }
}

func TestWaitForCloudSQLProxy_ReadyImmediately(t *testing.T) {
	port, stop := startFakeTCPListener(t)
	defer stop()
	err := waitForCloudSQLProxy(context.Background(), port, 3, discardLogger())
	assert.NoError(t, err, "should succeed when proxy port is listening")
}

// freePort binds a listener on an OS-assigned port, records the port, then
// immediately closes the listener. The returned port is very unlikely to be
// rebound in the brief window before the caller uses it.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	return port
}

func TestWaitForCloudSQLProxy_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitForCloudSQLProxy(ctx, freePort(t), 5, discardLogger())
	assert.ErrorIs(t, err, context.Canceled, "cancelled context should surface immediately")
}

func TestWaitForCloudSQLProxy_ExhaustsRetries(t *testing.T) {
	err := waitForCloudSQLProxy(context.Background(), freePort(t), 1, discardLogger())
	assert.Error(t, err)
}

// ── Unparseable deployment name: safe default (keep) ─────────────────────────

func TestRunWith_UnparseableDeploymentName_KeptActive(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT DISTINCT`).WillReturnRows(versionRows("9.12.1"))

	k8s := &fakeK8s{
		deployments: []deploymentItem{
			{Name: "vlm-worker-9-12-1", Replicas: 1},   // normal KEEP
			{Name: "vlm-worker-badname", Replicas: 1},   // unparseable → safe default KEEP
		},
	}

	result, outcome := runWith(context.Background(), testCfg(false), k8s, db, discardLogger())

	assert.Equal(t, 0, result)
	assert.Equal(t, outcomeSuccess, outcome)
	assert.Empty(t, k8s.scaledTo, "unparseable deployment must not be scaled")
	assert.NoError(t, mock.ExpectationsWereMet())
}
