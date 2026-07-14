package activities

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
)

func TestPODataLifActivity_ResolvePONodeIDs(t *testing.T) {
	in := ResolvePONodeIDsInput{
		HostUUID:  "host-uuid",
		NodeNames: []string{"node-a", "node-b"},
	}
	host := &datamodel.Host{BaseModel: datamodel.BaseModel{ID: 10, UUID: in.HostUUID}}
	nodeIDs := map[string]int64{"node-a": 1, "node-b": 2}

	t.Run("resolves node ids", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetHost", mock.Anything, in.HostUUID).Return(host, nil).Once()
		mockSE.On("GetNodesByHostIDAndNames", mock.Anything, host.ID, in.NodeNames).Return(nodeIDs, nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &PODataLifActivity{SE: mockSE}
		env.RegisterActivity(act.ResolvePONodeIDs)

		val, err := env.ExecuteActivity(act.ResolvePONodeIDs, in)
		require.NoError(t, err)

		var got map[string]int64
		require.NoError(t, val.Get(&got))
		require.Equal(t, nodeIDs, got)
	})

	t.Run("storage not configured", func(t *testing.T) {
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &PODataLifActivity{}
		env.RegisterActivity(act.ResolvePONodeIDs)

		_, err := env.ExecuteActivity(act.ResolvePONodeIDs, in)
		require.Error(t, err)
	})
}

func TestPODataLifActivity_AllocatePODataLifIPs(t *testing.T) {
	in := AllocatePODataLifIPsInput{
		IPRangeUUID: "range-1",
		AccountID:   5,
		SvmID:       99,
		Reservations: []PODataLifReservationInput{
			{Name: "lif-a", NodeID: 1},
			{Name: "lif-b", NodeID: 2},
		},
	}
	created := []*datamodel.Lif{
		{BaseModel: datamodel.BaseModel{UUID: "lif-uuid-1"}, Name: "lif-a", IPAddress: "10.0.0.10"},
		{BaseModel: datamodel.BaseModel{UUID: "lif-uuid-2"}, Name: "lif-b", IPAddress: "10.0.0.11"},
	}

	t.Run("allocates reservations", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("AllocateDataLifIPs", mock.Anything, in.IPRangeUUID, mock.MatchedBy(func(reservations []*datamodel.Lif) bool {
			require.Len(t, reservations, 2)
			require.Equal(t, int64(5), reservations[0].AccountID)
			require.Equal(t, int64(1), reservations[0].NodeID)
			require.Equal(t, sql.NullInt64{Int64: 99, Valid: true}, reservations[0].SvmID)
			return true
		})).Return(created, nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &PODataLifActivity{SE: mockSE}
		env.RegisterActivity(act.AllocatePODataLifIPs)

		val, err := env.ExecuteActivity(act.AllocatePODataLifIPs, in)
		require.NoError(t, err)

		var got []*datamodel.Lif
		require.NoError(t, val.Get(&got))
		require.Equal(t, created, got)
	})

	t.Run("propagates allocation errors", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("AllocateDataLifIPs", mock.Anything, in.IPRangeUUID, mock.Anything).
			Return(nil, errors.New("range full")).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &PODataLifActivity{SE: mockSE}
		env.RegisterActivity(act.AllocatePODataLifIPs)

		_, err := env.ExecuteActivity(act.AllocatePODataLifIPs, in)
		require.Error(t, err)
	})
}

func TestPODataLifActivity_ReleaseDataLifReservations(t *testing.T) {
	in := ReleaseDataLifReservationsInput{SvmID: 99}

	t.Run("deletes lif rows", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("DeleteLifsBySvmID", mock.Anything, in.SvmID).Return(nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &PODataLifActivity{SE: mockSE}
		env.RegisterActivity(act.ReleaseDataLifReservations)

		_, err := env.ExecuteActivity(act.ReleaseDataLifReservations, in)
		require.NoError(t, err)
	})
}
