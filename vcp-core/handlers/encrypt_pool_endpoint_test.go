package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func TestV1EncryptPool(t *testing.T) {
	const (
		poolID      = "bfa46013-1a1e-587b-ef9a-619bae6ba25d"
		ownerID     = "8342acf5-8939-0900-9bdb-a45b2f3423ac"
		kmsConfigID = "5f2f4860-daa8-4015-b4a8-3099f8dd95df"
		operationID = "6bed33e1-cc9c-e0b5-ac63-24e9410e64c1"
	)

	buildRequest := func() *oasgenserver.EncryptPoolRequestV1 {
		return &oasgenserver.EncryptPoolRequestV1{
			OwnerID:     ownerID,
			KmsConfigId: uuid.MustParse(kmsConfigID),
		}
	}

	t.Run("Success", func(tt *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1EncryptPoolParams{PoolId: poolID}

		mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, poolID).Return(false, nil)
		mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{kmsConfigID}).Return([]*models.KmsConfig{
			{BaseModel: models.BaseModel{UUID: kmsConfigID}, State: "ready"},
		}, nil)
		mockOrch.EXPECT().EncryptPool(mock.Anything, &common.EncryptPoolParams{
			PoolUUID:       poolID,
			KmsConfigID:    kmsConfigID,
			KmsConfigState: "ready",
			AccountName:    ownerID,
		}).Return(&models.Pool{
			BaseModel:     models.BaseModel{UUID: poolID},
			Name:          "pool-name",
			State:         "updating",
			StateDetails:  "encryption in progress",
		}, operationID, nil)

		res, err := handler.V1EncryptPool(context.Background(), buildRequest(), params)
		assert.NoError(tt, err)

		op, ok := res.(*oasgenserver.OperationV1)
		assert.True(tt, ok)
		assert.True(tt, op.Name.IsSet())
		assert.Equal(tt, fmt.Sprintf("/v1/pools/%s/operations/%s", poolID, operationID), op.Name.Value)
		assert.True(tt, op.Done.IsSet())
		assert.False(tt, op.Done.Value)

		var responseBody map[string]string
		assert.NoError(tt, json.Unmarshal(op.Response, &responseBody))
		assert.Equal(tt, poolID, responseBody["poolId"])
		assert.Equal(tt, "pool-name", responseBody["resourceId"])
		assert.Equal(tt, "updating", responseBody["storagePoolState"])
		assert.Equal(tt, "encryption in progress", responseBody["storagePoolStateDetails"])
		assert.Equal(tt, kmsConfigID, responseBody["kmsConfigId"])
	})

	t.Run("ClusterUpgradeInProgressReturnsConflict", func(tt *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1EncryptPoolParams{PoolId: poolID}

		mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, poolID).Return(true, nil)

		res, err := handler.V1EncryptPool(context.Background(), buildRequest(), params)
		assert.NoError(tt, err)
		_, ok := res.(*oasgenserver.V1EncryptPoolConflict)
		assert.True(tt, ok)
	})

	t.Run("ClusterUpgradeCheckErrorReturnsInternalServerError", func(tt *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1EncryptPoolParams{PoolId: poolID}

		mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, poolID).Return(false, assert.AnError)

		res, err := handler.V1EncryptPool(context.Background(), buildRequest(), params)
		assert.NoError(tt, err)
		_, ok := res.(*oasgenserver.V1EncryptPoolInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("MissingKmsConfigReturnsNotFound", func(tt *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1EncryptPoolParams{PoolId: poolID}

		mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, poolID).Return(false, nil)
		mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{kmsConfigID}).Return([]*models.KmsConfig{}, nil)

		res, err := handler.V1EncryptPool(context.Background(), buildRequest(), params)
		assert.NoError(tt, err)
		_, ok := res.(*oasgenserver.V1EncryptPoolNotFound)
		assert.True(tt, ok)
	})

	t.Run("GetMultipleKMSConfigsErrorReturnsInternalServerError", func(tt *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1EncryptPoolParams{PoolId: poolID}

		mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, poolID).Return(false, nil)
		mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{kmsConfigID}).Return(nil, assert.AnError)

		res, err := handler.V1EncryptPool(context.Background(), buildRequest(), params)
		assert.NoError(tt, err)
		_, ok := res.(*oasgenserver.V1EncryptPoolInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("EncryptPoolConflictErrorReturnsConflict", func(tt *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := NewHandler(mockOrch)
		params := oasgenserver.V1EncryptPoolParams{PoolId: poolID}

		mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, poolID).Return(false, nil)
		mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{kmsConfigID}).Return([]*models.KmsConfig{
			{BaseModel: models.BaseModel{UUID: kmsConfigID}, State: "ready"},
		}, nil)
		mockOrch.EXPECT().EncryptPool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewConflictErr("already encrypted"))

		res, err := handler.V1EncryptPool(context.Background(), buildRequest(), params)
		assert.NoError(tt, err)
		conflictRes, ok := res.(*oasgenserver.V1EncryptPoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, "already encrypted", conflictRes.Message)
	})

	t.Run("EncryptPoolErrorMappings", func(tt *testing.T) {
		baseSetup := func() (*factory.MockOrchestratorFactory, *Handler, oasgenserver.V1EncryptPoolParams) {
			mockOrch := factory.NewMockOrchestratorFactory(tt)
			handler := NewHandler(mockOrch)
			params := oasgenserver.V1EncryptPoolParams{
				PoolId:         poolID,
				XCorrelationID: oasgenserver.NewOptString("corr-id-1"),
			}
			mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, poolID).Return(false, nil)
			mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{kmsConfigID}).Return([]*models.KmsConfig{
				{BaseModel: models.BaseModel{UUID: kmsConfigID}, State: "ready"},
			}, nil)
			return mockOrch, handler, params
		}

		tt.Run("NotFoundMapsTo404", func(ttt *testing.T) {
			mockOrch, handler, params := baseSetup()
			mockOrch.EXPECT().EncryptPool(mock.Anything, mock.MatchedBy(func(p *common.EncryptPoolParams) bool {
				return p != nil && p.XCorrelationID == "corr-id-1"
			})).Return(nil, "", utilserrors.NewNotFoundErr("Pool", nil))
			res, err := handler.V1EncryptPool(context.Background(), buildRequest(), params)
			assert.NoError(ttt, err)
			_, ok := res.(*oasgenserver.V1EncryptPoolNotFound)
			assert.True(ttt, ok)
		})

		tt.Run("BadRequestMapsTo400", func(ttt *testing.T) {
			mockOrch, handler, params := baseSetup()
			mockOrch.EXPECT().EncryptPool(mock.Anything, mock.Anything).Return(nil, "", utilserrors.NewBadRequestErr("bad input"))
			res, err := handler.V1EncryptPool(context.Background(), buildRequest(), params)
			assert.NoError(ttt, err)
			_, ok := res.(*oasgenserver.V1EncryptPoolBadRequest)
			assert.True(ttt, ok)
		})

		tt.Run("InternalErrorMapsTo500", func(ttt *testing.T) {
			mockOrch, handler, params := baseSetup()
			mockOrch.EXPECT().EncryptPool(mock.Anything, mock.Anything).Return(nil, "", assert.AnError)
			res, err := handler.V1EncryptPool(context.Background(), buildRequest(), params)
			assert.NoError(ttt, err)
			_, ok := res.(*oasgenserver.V1EncryptPoolInternalServerError)
			assert.True(ttt, ok)
		})
	})
}

