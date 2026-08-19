package database

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// newICLifPostgresMockStore builds a DataStoreRepository backed by a Postgres
// dialect sqlmock. This lets tests exercise Postgres-only SQL (like the
// (node_attributes->>'host_id')::bigint predicate) that SQLite cannot parse.
func newICLifPostgresMockStore(t *testing.T) (*DataStoreRepository, sqlmock.Sqlmock) {
	t.Helper()
	dbSQL, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = dbSQL.Close()
	})
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	return NewDataStoreRepository(gormwrapper.New(gdb)), mock
}

func TestGetInterclusterLifs(t *testing.T) {
	t.Run("ReturnsOnlyInterclusterLifsAndEnrichesReferences", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, host := seedIPRangeReferences(t, store)
		network := "vpc-ic-1"
		ipRange := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-ic-1"},
			StartIP:   "10.0.0.10",
			EndIP:     "10.0.0.20",
			Gateway:   "10.0.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
			LifType:   LifTypeInterclusterLIF,
		}
		require.NoError(t, store.db.GORM().Create(ipRange).Error)

		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{UUID: "node-uuid-ic-1"},
			Name:      "node-1",
			AccountID: account.ID,
			NodeAttributes: &datamodel.NodeDetails{
				HostID: host.ID,
			},
		}
		require.NoError(t, store.db.GORM().Create(node).Error)

		icLif := &datamodel.Lif{
			BaseModel:  datamodel.BaseModel{UUID: "lif-ic-1"},
			Name:       "ic-lif-1",
			Type:       LifTypeInterclusterLIF,
			IPAddress:  "10.0.0.10",
			SubnetMask: "255.255.255.0",
			AccountID:  account.ID,
			NodeID:     node.ID,
			IPRangeID:  sql.NullInt64{Int64: ipRange.ID, Valid: true},
		}
		dataLif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{UUID: "lif-data-1"},
			Name:      "data-lif-1",
			Type:      LifTypeDataLIF,
			IPAddress: "10.0.0.11",
			AccountID: account.ID,
			NodeID:    node.ID,
			IPRangeID: sql.NullInt64{Int64: ipRange.ID, Valid: true},
		}
		require.NoError(t, store.db.GORM().Create(icLif).Error)
		require.NoError(t, store.db.GORM().Create(dataLif).Error)

		got, err := store.GetInterclusterLifs(context.Background(), nil, nil)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "lif-ic-1", got[0].UUID)
		assert.Equal(t, account.UUID, got[0].AccountUUID)
		assert.Equal(t, account.Name, got[0].AccountName)
		assert.Equal(t, node.UUID, got[0].NodeUUID)
		assert.Equal(t, host.UUID, got[0].HostUUID)
		assert.Equal(t, ipRange.UUID, got[0].IPRangeUUID)
	})

	t.Run("FiltersByAccountAndIPRange", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, host := seedIPRangeReferences(t, store)
		otherAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid-2"},
			Name:      "account-2",
		}
		require.NoError(t, store.db.GORM().Create(otherAccount).Error)

		network := "vpc-ic-2"
		ipRange := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-ic-2"},
			StartIP:   "10.1.0.10",
			EndIP:     "10.1.0.20",
			Gateway:   "10.1.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
			LifType:   LifTypeInterclusterLIF,
		}
		require.NoError(t, store.db.GORM().Create(ipRange).Error)

		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{UUID: "node-uuid-ic-2"},
			Name:      "node-2",
			AccountID: account.ID,
			NodeAttributes: &datamodel.NodeDetails{
				HostID: host.ID,
			},
		}
		otherNode := &datamodel.Node{
			BaseModel: datamodel.BaseModel{UUID: "node-uuid-other"},
			Name:      "node-other",
			AccountID: otherAccount.ID,
		}
		require.NoError(t, store.db.GORM().Create(node).Error)
		require.NoError(t, store.db.GORM().Create(otherNode).Error)

		matching := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{UUID: "lif-match"},
			Name:      "ic-lif-match",
			Type:      LifTypeInterclusterLIF,
			IPAddress: "10.1.0.10",
			AccountID: account.ID,
			NodeID:    node.ID,
			IPRangeID: sql.NullInt64{Int64: ipRange.ID, Valid: true},
		}
		other := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{UUID: "lif-other"},
			Name:      "ic-lif-other",
			Type:      LifTypeInterclusterLIF,
			IPAddress: "10.1.0.11",
			AccountID: otherAccount.ID,
			NodeID:    otherNode.ID,
		}
		require.NoError(t, store.db.GORM().Create(matching).Error)
		require.NoError(t, store.db.GORM().Create(other).Error)

		got, err := store.GetInterclusterLifs(context.Background(), &account.UUID, &ipRange.UUID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "lif-match", got[0].UUID)
	})

	t.Run("ReturnsNotFoundForMissingAccount", func(t *testing.T) {
		store := setupIPRangeStore(t)
		missing := "00000000-0000-0000-0000-000000000001"
		_, err := store.GetInterclusterLifs(context.Background(), &missing, nil)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrAccountNotFound))
	})
}

func seedICLifIPRange(t *testing.T, store *DataStoreRepository) (*datamodel.IPRange, *datamodel.Account) {
	t.Helper()
	region, account, _ := seedIPRangeReferences(t, store)
	network := "vpc-ic-alloc"
	ipRange := &datamodel.IPRange{
		BaseModel: datamodel.BaseModel{UUID: "ip-range-ic-alloc"},
		StartIP:   "10.10.0.10",
		EndIP:     "10.10.0.14",
		Gateway:   "10.10.0.1",
		Netmask:   "255.255.255.0",
		Network:   &network,
		RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
		AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		LifType:   LifTypeInterclusterLIF,
	}
	require.NoError(t, store.db.GORM().Create(ipRange).Error)
	return ipRange, account
}

func TestAllocateInterclusterLifIPs(t *testing.T) {
	ctx := context.Background()

	t.Run("AllocatesInterclusterTypeWithNullSvmID", func(t *testing.T) {
		store := setupIPRangeStore(t)
		ipRange, account := seedICLifIPRange(t, store)

		created, err := store.AllocateInterclusterLifIPs(ctx, ipRange.UUID, []*datamodel.Lif{
			{Name: "ic-lif-a", AccountID: account.ID, NodeID: 1},
			{Name: "ic-lif-b", AccountID: account.ID, NodeID: 2},
		})
		require.NoError(t, err)
		require.Len(t, created, 2)

		// Design contract: every allocated IC LIF is typed correctly and unbound
		// to any SVM. This is what makes IC LIFs cluster-scoped in ONTAP.
		for _, lif := range created {
			assert.Equal(t, LifTypeInterclusterLIF, lif.Type, "type must be interclusterLIF")
			assert.False(t, lif.SvmID.Valid, "SvmID must be NULL for IC LIFs")
			assert.Equal(t, ipRange.Netmask, lif.SubnetMask)
			assert.Equal(t, ipRange.ID, lif.IPRangeID.Int64)
			assert.NotEmpty(t, lif.UUID)
		}
		// Deterministic sequential allocation.
		assert.Equal(t, "10.10.0.10", created[0].IPAddress)
		assert.Equal(t, "10.10.0.11", created[1].IPAddress)
	})

	t.Run("RejectsDataLifRangeType", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-data-1"
		dataRange := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-data-forbidden"},
			StartIP:   "10.20.0.10",
			EndIP:     "10.20.0.15",
			Gateway:   "10.20.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
			LifType:   LifTypeDataLIF,
		}
		require.NoError(t, store.db.GORM().Create(dataRange).Error)

		_, err := store.AllocateInterclusterLifIPs(ctx, dataRange.UUID, []*datamodel.Lif{
			{Name: "ic-lif-a", AccountID: account.ID, NodeID: 1},
		})
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrInputValidationError))
		assert.Contains(t, customErr.Unwrap().Error(), "lif_type")
	})

	t.Run("CountsDataLifsAgainstRangeCapacity", func(t *testing.T) {
		// The shared queryAllocatedIPsInIPRange helper must count both
		// dataLIF and interclusterLIF reservations in the same ip_range for
		// capacity checks. Even though a well-configured deployment won't
		// mix types in a range, this exercises the "any type" scan invariant.
		store := setupIPRangeStore(t)
		ipRange, account := seedICLifIPRange(t, store)

		// Reserve the first two addresses as *data* LIFs directly on the same range.
		for i, ip := range []string{"10.10.0.10", "10.10.0.11"} {
			require.NoError(t, store.db.GORM().Create(&datamodel.Lif{
				BaseModel: datamodel.BaseModel{UUID: "seed-data-" + ip},
				Name:      "data-" + ip,
				Type:      LifTypeDataLIF,
				IPAddress: ip,
				AccountID: account.ID,
				NodeID:    int64(i + 1),
				IPRangeID: sql.NullInt64{Int64: ipRange.ID, Valid: true},
			}).Error)
		}

		created, err := store.AllocateInterclusterLifIPs(ctx, ipRange.UUID, []*datamodel.Lif{
			{Name: "ic-lif-a", AccountID: account.ID, NodeID: 10},
		})
		require.NoError(t, err)
		require.Len(t, created, 1)
		// Must skip 10.10.0.10 and 10.10.0.11 (already used by data LIFs).
		assert.Equal(t, "10.10.0.12", created[0].IPAddress)
	})

	t.Run("RejectsMissingIPRange", func(t *testing.T) {
		store := setupIPRangeStore(t)
		_, account, _ := seedIPRangeReferences(t, store)

		_, err := store.AllocateInterclusterLifIPs(ctx, "does-not-exist",
			[]*datamodel.Lif{{Name: "x", AccountID: account.ID, NodeID: 1}})
		require.Error(t, err)
	})

	t.Run("RejectsWhenRangeExhausted", func(t *testing.T) {
		// A /29 range with a single usable address should exhaust after the
		// first allocation; a second reservation must surface a
		// ResourceStateConflictError, not a validation error.
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-ic-tight"
		ipRange := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-ic-tight"},
			StartIP:   "10.30.0.10",
			EndIP:     "10.30.0.10",
			Gateway:   "10.30.0.1",
			Netmask:   "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
			LifType:   LifTypeInterclusterLIF,
		}
		require.NoError(t, store.db.GORM().Create(ipRange).Error)

		_, err := store.AllocateInterclusterLifIPs(ctx, ipRange.UUID, []*datamodel.Lif{
			{Name: "ic-lif-a", AccountID: account.ID, NodeID: 1},
			{Name: "ic-lif-b", AccountID: account.ID, NodeID: 2},
		})
		require.Error(t, err)
	})
}

// seedICLifWithLif provisions region/account/host/node and an active IC LIF row
// bound to a fresh IC-type ip range. It returns the ids/objects tests need to
// exercise per-account-host repo queries and the soft-delete path.
func seedICLifWithLif(t *testing.T, store *DataStoreRepository, hostUUID string) (
	account *datamodel.Account, host *datamodel.Host, node *datamodel.Node, lif *datamodel.Lif,
) {
	t.Helper()
	region := &datamodel.Region{BaseModel: datamodel.BaseModel{UUID: "region-h-" + hostUUID}, Name: "region-h"}
	require.NoError(t, store.db.GORM().Create(region).Error)
	account = &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-h-" + hostUUID}, Name: "account-h"}
	require.NoError(t, store.db.GORM().Create(account).Error)
	host = &datamodel.Host{BaseModel: datamodel.BaseModel{UUID: hostUUID}, Name: "host-" + hostUUID}
	require.NoError(t, store.db.GORM().Create(host).Error)

	network := "vpc-h-" + hostUUID
	ipRange := &datamodel.IPRange{
		BaseModel: datamodel.BaseModel{UUID: "ip-range-h-" + hostUUID},
		StartIP:   "10.40.0.10", EndIP: "10.40.0.20", Gateway: "10.40.0.1", Netmask: "255.255.255.0",
		Network:   &network,
		RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
		AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
		LifType:   LifTypeInterclusterLIF,
	}
	require.NoError(t, store.db.GORM().Create(ipRange).Error)

	node = &datamodel.Node{
		BaseModel:      datamodel.BaseModel{UUID: "node-h-" + hostUUID},
		Name:           "node-h",
		AccountID:      account.ID,
		NodeAttributes: &datamodel.NodeDetails{HostID: host.ID},
	}
	require.NoError(t, store.db.GORM().Create(node).Error)

	lif = &datamodel.Lif{
		BaseModel:  datamodel.BaseModel{UUID: "lif-h-" + hostUUID},
		Name:       "ic-lif-h",
		Type:       LifTypeInterclusterLIF,
		IPAddress:  "10.40.0.10",
		SubnetMask: "255.255.255.0",
		AccountID:  account.ID,
		NodeID:     node.ID,
		IPRangeID:  sql.NullInt64{Int64: ipRange.ID, Valid: true},
		LifDetails: &datamodel.LifDetails{},
	}
	require.NoError(t, store.db.GORM().Create(lif).Error)
	return account, host, node, lif
}

func TestGetInterclusterLifsForAccountHost(t *testing.T) {
	ctx := context.Background()

	// The production query uses a Postgres JSON operator on nodes.node_attributes,
	// which SQLite cannot parse. Exercise the query with a Postgres sqlmock so we
	// still cover the SQL construction path, enrichment glue, and error return.
	t.Run("EmitsPostgresJSONPredicateAndReturnsRows", func(t *testing.T) {
		store, mock := newICLifPostgresMockStore(t)

		// Primary query: IC LIFs joined against nodes by host_id.
		lifRows := sqlmock.NewRows([]string{
			"id", "uuid", "created_at", "updated_at", "deleted_at",
			"name", "description", "lif_details", "account_id", "type",
			"ip_address", "node_id", "subnet_mask", "ip_range_id", "svm_id",
		}).AddRow(
			int64(11), "lif-uuid-1", nil, nil, nil,
			"ic-lif-1", "", []byte(`{}`), int64(1), LifTypeInterclusterLIF,
			"10.40.0.10", int64(101), "255.255.255.0", nil, nil,
		)

		// The `->>'host_id'::bigint` predicate is the essence of this query —
		// pin the ExpectQuery pattern to that operator so a regression that
		// silently rewrites the SQL (or drops the JSON cast) fails here.
		mock.ExpectQuery(regexp.QuoteMeta(`node_attributes->>'host_id')::bigint`)).
			WithArgs(int64(1), LifTypeInterclusterLIF, int64(2)).
			WillReturnRows(lifRows)

		// Enrichment: accounts + nodes fire (rows have non-zero ids). ip_ranges
		// is skipped because the primary rows have no ip_range_id; hosts fires
		// because the nodes response carries a JSON host_id attribute.
		mock.ExpectQuery(`FROM "accounts"`).WillReturnRows(
			sqlmock.NewRows([]string{"id", "uuid", "name"}).
				AddRow(int64(1), "account-uuid-mock", "account-mock"),
		)
		mock.ExpectQuery(`FROM "nodes"`).WillReturnRows(
			sqlmock.NewRows([]string{"id", "uuid", "name", "account_id", "node_attributes"}).
				AddRow(int64(101), "node-uuid-mock", "node-1", int64(1), []byte(`{"host_id":2}`)),
		)
		mock.ExpectQuery(`FROM "hosts"`).WillReturnRows(
			sqlmock.NewRows([]string{"id", "uuid", "name"}).
				AddRow(int64(2), "host-uuid-mock", "host-1"),
		)

		got, err := store.GetInterclusterLifsForAccountHost(ctx, 1, 2)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "lif-uuid-1", got[0].UUID)
		assert.Equal(t, "account-uuid-mock", got[0].AccountUUID)
		assert.Equal(t, "node-uuid-mock", got[0].NodeUUID)
		assert.Equal(t, "host-uuid-mock", got[0].HostUUID)
	})

	t.Run("WrapsPrimaryQueryError", func(t *testing.T) {
		store, mock := newICLifPostgresMockStore(t)

		mock.ExpectQuery(regexp.QuoteMeta(`node_attributes->>'host_id')::bigint`)).
			WithArgs(int64(7), LifTypeInterclusterLIF, int64(9)).
			WillReturnError(errors.New("boom"))

		_, err := store.GetInterclusterLifsForAccountHost(ctx, 7, 9)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
	})

	t.Run("ReturnsEmptyWithoutEnrichmentWhenNoRows", func(t *testing.T) {
		// Empty rowset must short-circuit enrichment (len==0 fast path).
		store, mock := newICLifPostgresMockStore(t)

		mock.ExpectQuery(regexp.QuoteMeta(`node_attributes->>'host_id')::bigint`)).
			WithArgs(int64(1), LifTypeInterclusterLIF, int64(2)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "uuid"}))

		got, err := store.GetInterclusterLifsForAccountHost(ctx, 1, 2)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestGetInterclusterLifIPRanges(t *testing.T) {
	ctx := context.Background()

	t.Run("PropagatesAccountLookupError", func(t *testing.T) {
		store := setupIPRangeStore(t)
		_, err := store.GetInterclusterLifIPRanges(ctx, "no-such-account", "vpc-1")
		require.Error(t, err)
	})

	t.Run("ReturnsEmptyWhenNoRanges", func(t *testing.T) {
		store := setupIPRangeStore(t)
		_, account, _ := seedIPRangeReferences(t, store)

		got, err := store.GetInterclusterLifIPRanges(ctx, account.UUID, "vpc-empty")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("ReturnsAndEnrichesForMatchingAccountAndNetwork", func(t *testing.T) {
		store := setupIPRangeStore(t)
		region, account, _ := seedIPRangeReferences(t, store)
		network := "vpc-ic-list"

		ipRange := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-ic-list-1"},
			StartIP:   "10.50.0.10", EndIP: "10.50.0.20", Gateway: "10.50.0.1", Netmask: "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
			LifType:   LifTypeInterclusterLIF,
		}
		require.NoError(t, store.db.GORM().Create(ipRange).Error)

		// Data-LIF ip_range on the same account+network must NOT be returned.
		dataRange := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "ip-range-data-list-1"},
			StartIP:   "10.51.0.10", EndIP: "10.51.0.20", Gateway: "10.51.0.1", Netmask: "255.255.255.0",
			Network:   &network,
			RegionID:  sql.NullInt64{Int64: region.ID, Valid: true},
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
			LifType:   LifTypeDataLIF,
		}
		require.NoError(t, store.db.GORM().Create(dataRange).Error)

		got, err := store.GetInterclusterLifIPRanges(ctx, account.UUID, network)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "ip-range-ic-list-1", got[0].UUID)
		assert.Equal(t, account.UUID, got[0].AccountUUID)
		assert.Equal(t, account.Name, got[0].AccountName)
		assert.Equal(t, region.UUID, got[0].RegionUUID)
	})
}

func TestUpdateInterclusterLifExternalUUID(t *testing.T) {
	ctx := context.Background()

	t.Run("RejectsMissingInputs", func(t *testing.T) {
		store := setupIPRangeStore(t)
		require.Error(t, store.UpdateInterclusterLifExternalUUID(ctx, "", "ext"))
		require.Error(t, store.UpdateInterclusterLifExternalUUID(ctx, "lif", "  "))
	})

	t.Run("ReturnsNotFoundForMissingLif", func(t *testing.T) {
		store := setupIPRangeStore(t)
		err := store.UpdateInterclusterLifExternalUUID(ctx, "no-such-lif", "ext-uuid-1")
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})

	t.Run("PersistsExternalUUIDOnLifDetails", func(t *testing.T) {
		store := setupIPRangeStore(t)
		_, _, _, lif := seedICLifWithLif(t, store, "host-upd")

		require.NoError(t, store.UpdateInterclusterLifExternalUUID(ctx, lif.UUID, "ext-uuid-42"))

		var reread datamodel.Lif
		require.NoError(t, store.db.GORM().Where("uuid = ?", lif.UUID).First(&reread).Error)
		require.NotNil(t, reread.LifDetails)
		assert.Equal(t, "ext-uuid-42", reread.LifDetails.ExternalUUID)
	})

	t.Run("SkipsDataLifs", func(t *testing.T) {
		// UpdateInterclusterLifExternalUUID scopes by type=interclusterLIF; a
		// data LIF with the same UUID must yield NotFound rather than silently
		// mutate an unrelated row.
		store := setupIPRangeStore(t)
		_, account, _ := seedIPRangeReferences(t, store)
		dataLif := &datamodel.Lif{
			BaseModel:  datamodel.BaseModel{UUID: "lif-data-scope"},
			Name:       "data-lif",
			Type:       LifTypeDataLIF,
			AccountID:  account.ID,
			LifDetails: &datamodel.LifDetails{},
		}
		require.NoError(t, store.db.GORM().Create(dataLif).Error)

		err := store.UpdateInterclusterLifExternalUUID(ctx, dataLif.UUID, "ext-1")
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})
}

func TestDeleteInterclusterLifsForAccountHost(t *testing.T) {
	ctx := context.Background()

	// The production Postgres path is a single bulk UPDATE with a node_id
	// subquery. Pin the exact SQL: the predicates it carries are what
	// guarantee callers do not touch (a) LIFs of other accounts, (b) LIFs of
	// other hosts under the same account, (c) data LIFs, or (d) rows that
	// are already soft-deleted, and that (e) LIFs whose owning node is
	// itself soft-deleted stay out of the update set. Any regression that
	// rewrites the query and drops one of those predicates fails here.
	t.Run("EmitsBulkUpdateWithAccountHostTypeAndSoftDeleteFilters", func(t *testing.T) {
		store, mock := newICLifPostgresMockStore(t)

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE "lifs" SET "deleted_at"=$1 WHERE account_id = $2 AND type = $3 AND node_id IN (SELECT "id" FROM "nodes" WHERE (node_attributes->>'host_id')::bigint = $4 AND "nodes"."deleted_at" IS NULL) AND "lifs"."deleted_at" IS NULL`,
		)).
			WithArgs(sqlmock.AnyArg(), int64(3), LifTypeInterclusterLIF, int64(4)).
			WillReturnResult(sqlmock.NewResult(0, 5))
		mock.ExpectCommit()

		require.NoError(t, store.DeleteInterclusterLifsForAccountHost(ctx, 3, 4))
	})

	t.Run("NoopWhenNoRows", func(t *testing.T) {
		// RowsAffected==0 must not be treated as an error: an empty (account,
		// host) pair is a valid caller state (e.g. IC LIFs never provisioned).
		store, mock := newICLifPostgresMockStore(t)

		mock.ExpectBegin()
		mock.ExpectExec(`UPDATE "lifs"`).
			WithArgs(sqlmock.AnyArg(), int64(5), LifTypeInterclusterLIF, int64(6)).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()

		require.NoError(t, store.DeleteInterclusterLifsForAccountHost(ctx, 5, 6))
	})

	t.Run("PropagatesUpdateError", func(t *testing.T) {
		store, mock := newICLifPostgresMockStore(t)

		mock.ExpectBegin()
		mock.ExpectExec(`UPDATE "lifs"`).
			WithArgs(sqlmock.AnyArg(), int64(3), LifTypeInterclusterLIF, int64(4)).
			WillReturnError(errors.New("db offline"))
		mock.ExpectRollback()

		err := store.DeleteInterclusterLifsForAccountHost(ctx, 3, 4)
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataUpdateError))
	})
}
