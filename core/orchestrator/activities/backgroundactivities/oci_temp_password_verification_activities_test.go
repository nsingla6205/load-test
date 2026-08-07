package backgroundactivities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

func tempPwPoolView(uuid string, active bool, createdAt *time.Time) *datamodel.PoolView {
	return &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: uuid},
			ExternalTempCredentials: &datamodel.ExternalTempCredentials{
				TempPasswordActive:    active,
				TempPasswordCreatedAt: createdAt,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// ListOciPoolsWithActiveTempPassword
// ---------------------------------------------------------------------------

func TestListOciPoolsWithActiveTempPassword_Pagination(t *testing.T) {
	ctx := context.Background()

	t.Run("full batch reports hasMore", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		views := []*datamodel.PoolView{tempPwPoolView("a", true, nil), tempPwPoolView("b", true, nil)}
		mockSE.On("ListPoolsWithFilterAndPaginationOrderedByUUID", ctx,
			mock.AnythingOfType("*utils.Filter"), mock.AnythingOfType("*utils.Pagination")).
			Return(views, nil).Once()
		a := &OciTempPasswordVerificationActivity{SE: mockSE}

		res, err := a.ListOciPoolsWithActiveTempPassword(ctx, 0, 2)
		require.NoError(t, err)
		assert.Len(t, res.Pools, 2)
		assert.True(t, res.HasMore)
		mockSE.AssertExpectations(t)
	})

	t.Run("partial batch ends pagination", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		views := []*datamodel.PoolView{tempPwPoolView("a", true, nil)}
		mockSE.On("ListPoolsWithFilterAndPaginationOrderedByUUID", ctx,
			mock.AnythingOfType("*utils.Filter"), mock.AnythingOfType("*utils.Pagination")).
			Return(views, nil).Once()
		a := &OciTempPasswordVerificationActivity{SE: mockSE}

		res, err := a.ListOciPoolsWithActiveTempPassword(ctx, 0, 2)
		require.NoError(t, err)
		assert.Len(t, res.Pools, 1)
		assert.False(t, res.HasMore)
		mockSE.AssertExpectations(t)
	})

	t.Run("db error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPoolsWithFilterAndPaginationOrderedByUUID", ctx,
			mock.AnythingOfType("*utils.Filter"), mock.AnythingOfType("*utils.Pagination")).
			Return(nil, errors.New("db down")).Once()
		a := &OciTempPasswordVerificationActivity{SE: mockSE}

		res, err := a.ListOciPoolsWithActiveTempPassword(ctx, 0, 2)
		require.Error(t, err)
		assert.Nil(t, res)
		mockSE.AssertExpectations(t)
	})
}
