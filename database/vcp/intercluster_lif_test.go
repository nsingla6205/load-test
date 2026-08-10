package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

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
