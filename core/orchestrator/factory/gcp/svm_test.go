package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

func TestGCPOrchestrator_ListSvmsForHardwareClusters(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		orch := NewGCPOrchestrator(mockStorage, nil)
		expected := []*datamodel.SvmView{{
			Svm: datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-1"}},
		}}

		mockStorage.EXPECT().ListSvmsForHardwareClusters(ctx).Return(expected, nil).Once()

		got, err := orch.ListSvmsForHardwareClusters(ctx)

		assert.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("Error", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		orch := NewGCPOrchestrator(mockStorage, nil)
		expectedErr := errors.New("read failed")

		mockStorage.EXPECT().ListSvmsForHardwareClusters(ctx).Return(nil, expectedErr).Once()

		got, err := orch.ListSvmsForHardwareClusters(ctx)

		assert.ErrorIs(t, err, expectedErr)
		assert.Nil(t, got)
	})
}
