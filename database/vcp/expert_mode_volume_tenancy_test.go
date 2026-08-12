package database

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newExpertModeVolumeTenancySQLMockStore(t *testing.T) (*DataStoreRepository, sqlmock.Sqlmock) {
	t.Helper()

	dbSQL, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
	})

	dialector := postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true})
	gormDB, err := gorm.Open(dialector, &gorm.Config{})
	require.NoError(t, err)

	return NewDataStoreRepository(gormwrapper.New(gormDB)), mock
}

func TestListExpertModeVolumeTenancyByCluster_MissingClusterName(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	_, err := store.ListExpertModeVolumeTenancyByCluster(ctx, "", 0, 10)
	require.Error(t, err)

	var vcpErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &vcpErr))
	assert.Equal(t, vsaerrors.ErrMissingRequiredInputError, vcpErr.TrackingID)
}

func TestListExpertModeVolumeTenancyByCluster_ProjectFromAccountsJoin(t *testing.T) {
	store, mock := newExpertModeVolumeTenancySQLMockStore(t)

	rows := sqlmock.NewRows([]string{
		"cursor_id",
		"volume_uuid",
		"svm_name",
		"volume_name",
		"project",
		"pool_name",
		"tenant_project",
		"deployment_name",
		"cluster_name",
	}).AddRow(
		int64(42),
		"uuid-1",
		"svm_a",
		"vol_1",
		"consumer-project-123",
		"pool-a",
		"tenant-project-456",
		"deploy-a",
		"cluster-01",
	)

	mock.ExpectQuery("LEFT JOIN accounts a ON a.id = emv.account_id").
		WithArgs("EXTREME", "PREMIUM", "cluster-01", datamodel.LifeCycleStateCreating, int64(0), 10).
		WillReturnRows(rows)

	got, err := store.ListExpertModeVolumeTenancyByCluster(context.Background(), "cluster-01", 0, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "consumer-project-123", got[0].Project)
	assert.Equal(t, "tenant-project-456", got[0].TenantProject)
}

func TestListExpertModeVolumeTenancyByCluster_EmptyResultForOtherCluster(t *testing.T) {
	store, mock := newExpertModeVolumeTenancySQLMockStore(t)

	mock.ExpectQuery("LEFT JOIN accounts a ON a.id = emv.account_id").
		WithArgs("EXTREME", "PREMIUM", "cluster-other", datamodel.LifeCycleStateCreating, int64(0), 10).
		WillReturnRows(sqlmock.NewRows([]string{
			"cursor_id", "volume_uuid", "svm_name", "volume_name", "project",
			"pool_name", "tenant_project", "deployment_name", "cluster_name",
		}))

	got, err := store.ListExpertModeVolumeTenancyByCluster(context.Background(), "cluster-other", 0, 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestListExpertModeVolumeTenancyByCluster_AppliesActiveVolumeFilters(t *testing.T) {
	store, mock := newExpertModeVolumeTenancySQLMockStore(t)

	mock.ExpectQuery("emv.deleted_at IS NULL").
		WithArgs("EXTREME", "PREMIUM", "cluster-01", datamodel.LifeCycleStateCreating, int64(5), 10).
		WillReturnRows(sqlmock.NewRows([]string{
			"cursor_id", "volume_uuid", "svm_name", "volume_name", "project",
			"pool_name", "tenant_project", "deployment_name", "cluster_name",
		}))

	_, err := store.ListExpertModeVolumeTenancyByCluster(context.Background(), "cluster-01", 5, 10)
	require.NoError(t, err)
}

func TestListExpertModeVolumeTenancyByCluster_DefaultLimit(t *testing.T) {
	store, mock := newExpertModeVolumeTenancySQLMockStore(t)

	mock.ExpectQuery("LEFT JOIN accounts a ON a.id = emv.account_id").
		WithArgs("EXTREME", "PREMIUM", "cluster-01", datamodel.LifeCycleStateCreating, int64(0), commonparams.DefaultExpertModeVolumeTenancyPageLimit).
		WillReturnRows(sqlmock.NewRows([]string{
			"cursor_id", "volume_uuid", "svm_name", "volume_name", "project",
			"pool_name", "tenant_project", "deployment_name", "cluster_name",
		}))

	_, err := store.ListExpertModeVolumeTenancyByCluster(context.Background(), "cluster-01", 0, 0)
	require.NoError(t, err)
}

func TestListExpertModeVolumeTenancyByCluster_MaxLimit(t *testing.T) {
	store, mock := newExpertModeVolumeTenancySQLMockStore(t)

	mock.ExpectQuery("LEFT JOIN accounts a ON a.id = emv.account_id").
		WithArgs("EXTREME", "PREMIUM", "cluster-01", datamodel.LifeCycleStateCreating, int64(0), commonparams.MaxExpertModeVolumeTenancyQueryLimit).
		WillReturnRows(sqlmock.NewRows([]string{
			"cursor_id", "volume_uuid", "svm_name", "volume_name", "project",
			"pool_name", "tenant_project", "deployment_name", "cluster_name",
		}))

	_, err := store.ListExpertModeVolumeTenancyByCluster(context.Background(), "cluster-01", 0, 10000)
	require.NoError(t, err)
}

func TestListExpertModeVolumeTenancyByCluster_DatabaseError(t *testing.T) {
	store, mock := newExpertModeVolumeTenancySQLMockStore(t)

	mock.ExpectQuery("LEFT JOIN accounts a ON a.id = emv.account_id").
		WithArgs("EXTREME", "PREMIUM", "cluster-01", datamodel.LifeCycleStateCreating, int64(0), 10).
		WillReturnError(fmt.Errorf("connection refused"))

	_, err := store.ListExpertModeVolumeTenancyByCluster(context.Background(), "cluster-01", 0, 10)
	require.Error(t, err)

	var vcpErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &vcpErr))
	assert.Equal(t, vsaerrors.ErrDatabaseDataReadError, vcpErr.TrackingID)
}

func TestExpertModeVolumeTenancyBaseQuery_ExcludesCreatingAndMissingUUID(t *testing.T) {
	store, mock := newExpertModeVolumeTenancySQLMockStore(t)

	mock.ExpectQuery("emv.external_uuid IS NOT NULL").
		WithArgs("EXTREME", "PREMIUM", "cluster-01", datamodel.LifeCycleStateCreating, int64(0), 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"cursor_id", "volume_uuid", "svm_name", "volume_name", "project",
			"pool_name", "tenant_project", "deployment_name", "cluster_name",
		}))

	_, err := store.ListExpertModeVolumeTenancyByCluster(context.Background(), "cluster-01", 0, 1)
	require.NoError(t, err)
}

func TestExpertModeVolumeTenancyBaseQuery_ExcludesSoftDeletedRows(t *testing.T) {
	store, mock := newExpertModeVolumeTenancySQLMockStore(t)

	mock.ExpectQuery("p.deleted_at IS NULL").
		WithArgs("EXTREME", "PREMIUM", "cluster-01", datamodel.LifeCycleStateCreating, int64(0), 1).
		WillReturnRows(sqlmock.NewRows([]string{
			"cursor_id", "volume_uuid", "svm_name", "volume_name", "project",
			"pool_name", "tenant_project", "deployment_name", "cluster_name",
		}))

	_, err := store.ListExpertModeVolumeTenancyByCluster(context.Background(), "cluster-01", 0, 1)
	require.NoError(t, err)
}
