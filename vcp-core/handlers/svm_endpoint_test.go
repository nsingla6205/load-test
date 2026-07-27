package api

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func TestV1ListSVMs(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)
		list := []*datamodel.SvmView{
			{
				Svm: datamodel.Svm{
					BaseModel:  datamodel.BaseModel{UUID: "svm-1"},
					Name:       "svm-one",
					SvmDetails: &datamodel.SvmDetails{ExternalUUID: "external-uuid"},
				},
				AccountUUID:    "account-uuid",
				AccountName:    "account-name",
				ClusterUUID:    "cluster-uuid",
				VendorSubnetID: "projects/test/global/networks/netapp",
				IPRangeUUID:    "ip-range-uuid",
			},
		}

		mockOrch.EXPECT().ListSvmsForHardwareClusters(mock.Anything).Return(list, nil)

		res, err := handler.V1ListSVMs(context.Background(), oasgenserver.V1ListSVMsParams{})

		assert.NoError(t, err)
		out, ok := res.(*oasgenserver.V1ListSVMsOKApplicationJSON)
		assert.True(t, ok)
		assert.Len(t, *out, 1)
		assert.Equal(t, "svm-1", (*out)[0].UUID.Value)
		assert.Equal(t, "svm-one", (*out)[0].Name.Value)
		assert.Equal(t, "account-uuid", (*out)[0].AccountUUID.Value)
		assert.Equal(t, "account-name", (*out)[0].AccountName.Value)
		assert.Equal(t, "cluster-uuid", (*out)[0].ClusterUUID.Value)
		assert.Equal(t, "projects/test/global/networks/netapp", (*out)[0].VendorSubnetID.Value)
		assert.Equal(t, "ip-range-uuid", (*out)[0].IprangeUUID.Value)
		assert.Equal(t, "external-uuid", (*out)[0].ExternalUUID.Value)
	})

	t.Run("InternalServerError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		mockOrch.EXPECT().ListSvmsForHardwareClusters(mock.Anything).Return(nil, stderrors.New("db down"))

		res, err := handler.V1ListSVMs(context.Background(), oasgenserver.V1ListSVMsParams{})

		assert.NoError(t, err)
		internal, ok := res.(*oasgenserver.V1ListSVMsInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), internal.Code)
		assert.Contains(t, internal.Message, "Error listing SVMs")
	})
}
