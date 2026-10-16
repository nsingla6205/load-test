package opsinventory

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"gorm.io/gorm"
)

// sentinel is written into every secret field of the fixtures. It must never
// appear in a response body or an audit log.
const sentinel = "SENTINEL-SECRET-7f3a"

// captureLogger records every With() field set and message.
type captureLogger struct {
	mu      *sync.Mutex
	entries *[]logEntry
	fields  log.Fields
}

type logEntry struct {
	msg    string
	fields log.Fields
}

func newCaptureLogger() *captureLogger {
	return &captureLogger{mu: &sync.Mutex{}, entries: &[]logEntry{}}
}

func (l *captureLogger) record(msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.entries = append(*l.entries, logEntry{msg: fmt.Sprint(append([]any{msg}, args...)...), fields: l.fields})
}

func (l *captureLogger) Errorf(f string, a ...any)                          { l.record(f, a...) }
func (l *captureLogger) Error(f string, a ...any)                           { l.record(f, a...) }
func (l *captureLogger) Warnf(f string, a ...any)                           { l.record(f, a...) }
func (l *captureLogger) Warn(f string, a ...any)                            { l.record(f, a...) }
func (l *captureLogger) Infof(f string, a ...any)                           { l.record(f, a...) }
func (l *captureLogger) Info(f string, a ...any)                            { l.record(f, a...) }
func (l *captureLogger) Debugf(f string, a ...any)                          { l.record(f, a...) }
func (l *captureLogger) Debug(f string, a ...any)                           { l.record(f, a...) }
func (l *captureLogger) InfoContext(_ context.Context, m string, a ...any)  { l.record(m, a...) }
func (l *captureLogger) WarnContext(_ context.Context, m string, a ...any)  { l.record(m, a...) }
func (l *captureLogger) ErrorContext(_ context.Context, m string, a ...any) { l.record(m, a...) }
func (l *captureLogger) DebugContext(_ context.Context, m string, a ...any) { l.record(m, a...) }
func (l *captureLogger) WithFields(_ string, f log.Fields) log.Logger       { return l.With(f) }
func (l *captureLogger) With(f log.Fields) log.Logger {
	merged := log.Fields{}
	for k, v := range l.fields {
		merged[k] = v
	}
	for k, v := range f {
		merged[k] = v
	}
	return &captureLogger{mu: l.mu, entries: l.entries, fields: merged}
}

func (l *captureLogger) all() []logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]logEntry(nil), *l.entries...)
}

func (l *captureLogger) last(t *testing.T) log.Fields {
	t.Helper()
	entries := l.all()
	require.NotEmpty(t, entries, "expected an audit log")
	return entries[len(entries)-1].fields
}

func (l *captureLogger) dump() string {
	b, _ := json.Marshal(l.all())
	var sb strings.Builder
	sb.Write(b)
	for _, e := range l.all() {
		sb.WriteString(e.msg)
		sb.WriteString(fmt.Sprintf("%v", e.fields))
	}
	return sb.String()
}

// ogenHandler adapts Handler to the generated server interface, like api.Handler does.
type ogenHandler struct {
	oasgenserver.UnimplementedHandler
	h *Handler
}

func (o ogenHandler) V1GetInternalResource(ctx context.Context, req *oasgenserver.OpsGetResourceRequestV1, p oasgenserver.V1GetInternalResourceParams) (oasgenserver.V1GetInternalResourceRes, error) {
	return o.h.Get(ctx, req, p)
}

func (o ogenHandler) V1ListInternalResources(ctx context.Context, req *oasgenserver.OpsListResourcesRequestV1, p oasgenserver.V1ListInternalResourcesParams) (oasgenserver.V1ListInternalResourcesRes, error) {
	return o.h.List(ctx, req, p)
}

// fakeDescriber records describe calls and their deadlines.
type fakeDescriber struct {
	mu        sync.Mutex
	calls     []string
	deadlines []time.Time
	fn        func(ctx context.Context, workflowID string) (*workflowservice.DescribeWorkflowExecutionResponse, error)
}

func (f *fakeDescriber) DescribeWorkflowExecution(ctx context.Context, workflowID, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, workflowID)
	deadline, _ := ctx.Deadline()
	f.deadlines = append(f.deadlines, deadline)
	fn := f.fn
	f.mu.Unlock()
	if runID != "" {
		return nil, fmt.Errorf("unexpected run id %q", runID)
	}
	if fn == nil {
		return nil, serviceerror.NewNotFound("workflow not found")
	}
	return fn(ctx, workflowID)
}

type testEnv struct {
	t      *testing.T
	db     *gorm.DB
	store  readstore.ReadStore
	logs   *captureLogger
	srv    *httptest.Server
	oci    bool
	desc   *fakeDescriber
	fx     fixtures
	handle *Handler
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := database.SetupTestDB()
	require.NoError(t, err)
	e := &testEnv{t: t, db: db, store: readstore.New(db), logs: newCaptureLogger(), desc: &fakeDescriber{}}
	e.fx = seedAll(t, db)
	e.handle = NewHandler(e.store, NewReaders(e.store, e.desc),
		WithCloudCheck(func() bool { return e.oci }),
		WithLocationCheck(testLocationCheck),
		WithLogger(func(context.Context) log.Logger { return e.logs }))
	server, err := oasgenserver.NewServer(ogenHandler{h: e.handle},
		oasgenserver.WithErrorHandler(e.handle.ErrorHandler(ogenerrors.DefaultErrorHandler)))
	require.NoError(t, err)
	e.srv = httptest.NewServer(server)
	t.Cleanup(e.srv.Close)
	return e
}

type response struct {
	status int
	raw    string
	body   map[string]any
}

func (e *testEnv) post(path string, body any) response {
	e.t.Helper()
	var payload []byte
	switch b := body.(type) {
	case string:
		payload = []byte(b)
	default:
		var err error
		payload, err = json.Marshal(b)
		require.NoError(e.t, err)
	}
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewReader(payload))
	require.NoError(e.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-correlation-id", "corr-test")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(e.t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(e.t, err)
	out := response{status: resp.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (e *testEnv) get(body any) response  { return e.call("resources:get", body) }
func (e *testEnv) list(body any) response { return e.call("resources:list", body) }

// call sends a request to the path for the body's "projectNumber" ("-" when
// absent) and "locationId" (the test region when absent). Both keys are moved
// from the body into the path; the caller's map is not changed.
func (e *testEnv) call(op string, body any) response {
	e.t.Helper()
	project, location := "-", testRegion
	if m, ok := body.(map[string]any); ok {
		copied := make(map[string]any, len(m))
		for k, v := range m {
			copied[k] = v
		}
		if v, ok := copied["projectNumber"].(string); ok {
			project = v
			delete(copied, "projectNumber")
		}
		if v, ok := copied["locationId"].(string); ok {
			location = v
			delete(copied, "locationId")
		}
		body = copied
	}
	return e.post(fmt.Sprintf("/v1/internal/projects/%s/locations/%s/%s", project, location, op), body)
}

const testRegion = "us-east4"

// testLocationCheck is validateLocation with the test region as the local region.
func testLocationCheck(locationID string) error {
	region, _, err := utils.ParseRegionAndZone(locationID)
	if err != nil {
		return err
	}
	if region != testRegion {
		return fmt.Errorf("Invalid region. Region can only be %s", testRegion)
	}
	return nil
}

func resources(t *testing.T, r response) []map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, r.status, r.raw)
	items, _ := r.body["resources"].([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any))
	}
	return out
}

func uuidsOf(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if job, ok := it["job"].(map[string]any); ok {
			out = append(out, job["uuid"].(string))
			continue
		}
		out = append(out, it["uuid"].(string))
	}
	return out
}

type fixtures struct {
	account, other      *datamodel.Account
	pool, otherPool     *datamodel.Pool
	deletedPool         *datamodel.Pool
	vol, vol2, delVol   *datamodel.Volume
	otherVol            *datamodel.Volume
	vault, otherVault   *datamodel.BackupVault
	policy              *datamodel.BackupPolicy
	kms                 *datamodel.KmsConfig
	ad                  *datamodel.ActiveDirectory
	svm                 *datamodel.Svm
	host                *datamodel.Host
	job, adminJob       *datamodel.Job
	seqJob              *datamodel.Job
	snapshot            *datamodel.Snapshot
	replication         *datamodel.VolumeReplication
	backup, crossBackup *datamodel.Backup
}

func softDeleted(ts time.Time) *gorm.DeletedAt {
	d := gorm.DeletedAt{Time: ts, Valid: true}
	return &d
}

func strPtr(s string) *string { return &s }

// seedAll writes one row of every type, with every secret field set to sentinel.
func seedAll(t *testing.T, db *gorm.DB) fixtures {
	t.Helper()
	mk := func(v any) {
		t.Helper()
		require.NoError(t, db.Create(v).Error)
	}
	f := fixtures{}
	f.account = &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-1"}, Name: "1111"}
	f.other = &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-2"}, Name: "2222"}
	mk(f.account)
	mk(f.other)

	sa := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{UUID: "sa-1"}, Name: "vcp-sa", AccountID: f.account.ID, State: "READY",
		ServiceAccountEmail: "vcp-sa@example.iam.gserviceaccount.com", ServiceAccountPasswordLocation: sentinel,
		ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{Keys: []datamodel.ServiceAccountKey{
			{KeyID: "key-new", KeyData: sentinel, IsPrimary: true, IsActive: true, CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
			{KeyID: "key-next", KeyData: sentinel, IsActive: true, CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		}},
	}
	mk(sa)
	f.kms = &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-1"}, AccountID: f.account.ID, State: "READY", ResourceID: "kms-res", ServiceAccountID: &sa.ID,
		KeyRing: "ring", KeyName: "key", KmsGrant: sentinel,
		KmsAttributes: &datamodel.KmsAttributes{SdeKmsConfigHealthError: sentinel, SdeKmsConfigOperationURI: sentinel, Instructions: sentinel, SdeServiceAccountEmail: "sa@example.com"},
	}
	mk(f.kms)
	f.ad = &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-1"}, AdName: "ad1", AccountId: f.account.ID, State: "READY",
		Username: "admin", Domain: "corp.example", CredentialPath: sentinel,
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{ServerRootCaCertificate: sentinel, Site: "site-a"},
	}
	mk(f.ad)
	f.host = &datamodel.Host{BaseModel: datamodel.BaseModel{UUID: "host-1"}, Name: "h1", Password: sentinel}
	mk(f.host)

	creds := &datamodel.PoolCredentials{SecretID: sentinel, SecretIDNew: sentinel, CertificateID: sentinel, Password: sentinel, AuthType: 2, CertPrivateKeySecretID: sentinel}
	f.pool = &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"}, Name: "p1", DeploymentName: "d1", AccountID: f.account.ID, State: "READY",
		ServiceLevel: "FLEX", PoolCredentials: creds, VLMConfig: sentinel, HostUUID: f.host.UUID,
		KmsConfigID: sql.NullInt64{Int64: f.kms.ID, Valid: true}, ActiveDirectoryID: sql.NullInt64{Int64: f.ad.ID, Valid: true},
		VendorID:              "/projects/1111/locations/us-east4/pools/p1",
		ExpertModeCredentials: &datamodel.ExpertModeCredentials{ExpertModeCredential: []*datamodel.ExpertModeCredential{{Password: sentinel}}},
		StateDetails:          "raw ONTAP failure " + sentinel,
	}
	f.deletedPool = &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-del", DeletedAt: softDeleted(time.Now())}, Name: "pdel", DeploymentName: "d2", AccountID: f.account.ID, State: "DELETED"}
	f.otherPool = &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-other"}, Name: "p1", DeploymentName: "d3", AccountID: f.other.ID, State: "READY"}
	mk(f.pool)
	mk(f.deletedPool)
	mk(f.otherPool)

	f.svm = &datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-1"}, Name: "svm1", AccountID: f.account.ID, PoolID: f.pool.ID}
	mk(f.svm)

	f.vault = &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "vault-1"}, Name: "bv1", AccountID: f.account.ID, LifeCycleState: "READY",
		BucketDetails: datamodel.BucketDetailsArray{{BucketName: "bucket-1", HmacAccessKeyID: sentinel, HmacSecretID: sentinel}},
	}
	f.otherVault = &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "vault-2"}, Name: "bv2", AccountID: f.other.ID, LifeCycleState: "READY"}
	mk(f.vault)
	mk(f.otherVault)
	f.policy = &datamodel.BackupPolicy{BaseModel: datamodel.BaseModel{UUID: "bp-1"}, Name: "bp1", AccountID: f.account.ID, LifeCycleState: "READY"}
	mk(f.policy)

	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f.vol = &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1", CreatedAt: base}, Name: "v1", State: "READY", AccountID: f.account.ID, PoolID: f.pool.ID, SvmID: f.svm.ID,
		StateDetails:     datamodel.LifeCycleStateAvailableDetails,
		VolumeAttributes: &datamodel.VolumeAttributes{CreationToken: "tok", Protocols: []string{"NFSV3"}},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: f.vault.UUID, BackupPolicyID: f.policy.UUID, KmsGrant: strPtr(sentinel)},
	}
	f.vol2 = &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-2", CreatedAt: base.Add(24 * time.Hour)}, Name: "v2", State: "CREATING", AccountID: f.account.ID, PoolID: f.pool.ID}
	f.delVol = &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-del", CreatedAt: base.Add(48 * time.Hour), DeletedAt: softDeleted(base.Add(72 * time.Hour))}, Name: "v1", State: "DELETED", AccountID: f.account.ID, PoolID: f.pool.ID}
	f.otherVol = &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-other"}, Name: "vo", State: "READY", AccountID: f.other.ID, PoolID: f.otherPool.ID}
	mk(f.vol)
	mk(f.vol2)
	mk(f.delVol)
	mk(f.otherVol)

	f.snapshot = &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: "snap-1", DeletedAt: softDeleted(base)}, Name: "s1", AccountID: f.account.ID, VolumeID: f.delVol.ID, State: "DELETED", StateDetails: sentinel}
	mk(f.snapshot)
	f.replication = &datamodel.VolumeReplication{
		BaseModel: datamodel.BaseModel{UUID: "rep-1"}, Name: "r1", AccountID: f.account.ID, VolumeID: f.vol.ID, State: "READY",
		LastTransferError: sentinel, UnhealthyReason: sentinel,
		ReplicationAttributes:       &datamodel.ReplicationDetails{SourceVolumeUUID: "remote-src", DestinationVolumeUUID: f.vol.UUID, SourceHostName: sentinel},
		HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{SvmPeerCommand: strPtr(sentinel)},
	}
	mk(f.replication)
	f.backup = &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: "bk-1"}, Name: "b1", BackupVaultID: f.vault.ID, VolumeUUID: f.vol.UUID, State: "READY"}
	// A GCBDR-style backup in our vault whose source volume belongs to another project.
	f.crossBackup = &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: "bk-2"}, Name: "b2", BackupVaultID: f.vault.ID, VolumeUUID: f.otherVol.UUID, State: "READY"}
	mk(f.backup)
	mk(f.crossBackup)

	f.job = &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-1"}, Type: "CREATE_POOL", State: "PROCESSING", CorrelationID: "corr-1",
		AccountID: sql.NullInt64{Int64: f.account.ID, Valid: true}, WorkflowID: "wf-1", TrackingID: 1003, ErrorDetails: sentinel,
		JobAttributes: &datamodel.JobAttributes{PoolUUID: f.pool.UUID, PreviousStateDetails: sentinel, PayloadAttributes: map[string]any{"password": sentinel}},
	}
	f.adminJob = &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-admin"}, Type: "RECONCILE_POOL_CERTIFICATE_AUTHORITY", State: "DONE", CorrelationID: "corr-admin", IsAdminJob: true}
	f.seqJob = &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-seq"}, Type: string(datamodel.JobTypeCreateVolume), State: "NEW", CorrelationID: "corr-1",
		AccountID: sql.NullInt64{Int64: f.account.ID, Valid: true}, WorkflowID: "wf-seq",
		JobAttributes: &datamodel.JobAttributes{PoolUUID: f.pool.UUID},
	}
	mk(f.job)
	mk(f.adminJob)
	mk(f.seqJob)

	return f
}

// assertNoSecrets checks that the encoded resource does not contain the sentinel.
func assertNoSecrets(t *testing.T, v oasgenserver.OpsResourceV1) {
	t.Helper()
	b, err := v.MarshalJSON()
	require.NoError(t, err)
	assert.NotContains(t, string(b), sentinel)
}

// encoded returns the resource as a JSON map, the way a caller sees it.
func encoded(t *testing.T, v oasgenserver.OpsResourceV1) map[string]any {
	t.Helper()
	b, err := v.MarshalJSON()
	require.NoError(t, err)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}
