package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
	vcmconstants "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/utils/constants"
)

type stubDBOpener struct {
	dsn string
}

func (s *stubDBOpener) Open(ctx context.Context, _ vcmserver.DbConfigV1) (vcmdb.Storage, error) {
	return vcmdb.Open(ctx, vcmdb.Config{
		Driver: vcmdb.DriverSQLite,
		SQLite: vcmdb.SQLConfig{DSN: s.dsn},
	}, log.NewLogger(), true)
}

func waitForConfigured(t *testing.T, store *vcmconfig.Store) {
	t.Helper()
	require.Eventually(t, store.Configured, 2*time.Second, 10*time.Millisecond)
}

func configuredPoolHandler(t *testing.T) (*Handler, *vcmconfig.Store) {
	t.Helper()
	store := vcmconfig.NewStoreWithOpener(log.NewLogger(), &stubDBOpener{
		dsn: fmt.Sprintf("file:vcm-pool-endpoint-%s?mode=memory&cache=shared", t.Name()),
	})
	store.Apply(context.Background(), &vcmserver.SetConfigRequestV1{
		DbConfig: vcmserver.DbConfigV1{
			Project:  "test-project",
			Instance: "test-instance",
			Database: "test-db",
		},
	})
	waitForConfigured(t, store)
	return &Handler{ConfigStore: store}, store
}

func testPoolCreateRequest(requestID, deploymentID string, deadline time.Time) *vcmserver.PoolRequestV1 {
	return &vcmserver.PoolRequestV1{
		RequestId: requestID,
		Deadline:  deadline,
		Pool: vcmserver.PoolV1{
			DeploymentId: deploymentID,
		},
	}
}

func TestPoolCreateV1PersistsJob(t *testing.T) {
	handler, store := configuredPoolHandler(t)
	ctx := context.Background()
	requestID := uuid.NewString()
	deploymentID := "gcnv-8d0ff6a0b456f30"
	deadline := time.Now().UTC().Add(2 * time.Hour)

	result, err := handler.PoolCreateV1(ctx, testPoolCreateRequest(requestID, deploymentID, deadline))
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.PoolCreateV1OK{}, result)

	stored, err := store.GetJob(ctx, requestID)
	require.NoError(t, err)
	assert.Equal(t, requestID, stored.ID)
	assert.Equal(t, deploymentID, stored.ResourceID)
	assert.Equal(t, vcmconstants.JobTypePoolCreate, stored.Type)
	assert.Equal(t, vcmdb.JobStateNew, stored.State)
	assert.True(t, stored.DeadlineAt.Equal(deadline))
	assert.Empty(t, stored.CurrentStep)
}

func TestPoolCreateV1RequiresRequestFields(t *testing.T) {
	handler, _ := configuredPoolHandler(t)
	future := time.Now().UTC().Add(time.Hour)

	for _, tc := range []struct {
		name string
		req  *vcmserver.PoolRequestV1
	}{
		{name: "nil request", req: nil},
		{name: "empty requestId", req: testPoolCreateRequest("", "gcnv-8d0ff6a0b456f30", future)},
		{name: "empty deploymentId", req: testPoolCreateRequest(uuid.NewString(), "", future)},
		{name: "zero deadline", req: testPoolCreateRequest(uuid.NewString(), "gcnv-8d0ff6a0b456f30", time.Time{})},
		{name: "past deadline", req: testPoolCreateRequest(uuid.NewString(), "gcnv-8d0ff6a0b456f30", time.Now().UTC().Add(-time.Minute))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := handler.PoolCreateV1(context.Background(), tc.req)
			require.NoError(t, err)

			badRequest, ok := result.(*vcmserver.PoolCreateV1BadRequest)
			require.True(t, ok)
			assert.EqualValues(t, 400, badRequest.Code)
		})
	}
}

func TestPoolCreateV1PersistenceFailure(t *testing.T) {
	handler := &Handler{
		ConfigStore: vcmconfig.NewStore(log.NewLogger(), 0),
	}

	result, err := handler.PoolCreateV1(context.Background(), testPoolCreateRequest(
		uuid.NewString(),
		"gcnv-8d0ff6a0b456f30",
		time.Now().UTC().Add(time.Hour),
	))
	require.NoError(t, err)

	_, ok := result.(*vcmserver.PoolCreateV1InternalServerError)
	assert.True(t, ok)
}

func TestPoolCreateV1RequiresConfigStore(t *testing.T) {
	result, err := (Handler{}).PoolCreateV1(context.Background(), testPoolCreateRequest(
		uuid.NewString(),
		"gcnv-8d0ff6a0b456f30",
		time.Now().UTC().Add(time.Hour),
	))
	require.NoError(t, err)

	_, ok := result.(*vcmserver.PoolCreateV1InternalServerError)
	assert.True(t, ok)
}
