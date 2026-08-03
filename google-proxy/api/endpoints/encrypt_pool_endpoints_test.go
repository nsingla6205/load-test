package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	vsaCoreModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vcperrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

const (
	testEncryptPoolProject     = "123456789"
	testEncryptPoolLocation    = "local"
	testEncryptPoolID          = "bfa46013-1a1e-587b-ef9a-619bae6ba25d"
	testEncryptPoolKmsConfigID = "9760acf5-4638-11e7-9bdb-020073ca3333"
	testEncryptPoolOperationID = "6bed33e1-cc9c-e0b5-ac63-24e9410e64c1"
)

// stubValidRegionAndZone forces the location parser to succeed and returns a
// restore function to be deferred by the caller.
func stubValidRegionAndZone() func() {
	original := parseAndValidateRegionAndZone
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-east4", "us-east4", nil
	}
	return func() { parseAndValidateRegionAndZone = original }
}

func encryptPoolTestParams() gcpgenserver.V1betaEncryptPoolParams {
	return gcpgenserver.V1betaEncryptPoolParams{
		PoolId:         testEncryptPoolID,
		ProjectNumber:  testEncryptPoolProject,
		LocationId:     testEncryptPoolLocation,
		XCorrelationID: gcpgenserver.NewOptString("x-correlationId"),
	}
}

func encryptPoolTestRequest() *gcpgenserver.EncryptPoolRequestV1beta {
	return &gcpgenserver.EncryptPoolRequestV1beta{
		KmsConfigId: uuid.MustParse(testEncryptPoolKmsConfigID),
	}
}

func testAccount() *datamodel.Account {
	return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"}}
}

func testKmsConfigs() []*vsaCoreModels.KmsConfig {
	return []*vsaCoreModels.KmsConfig{
		{BaseModel: vsaCoreModels.BaseModel{UUID: testEncryptPoolKmsConfigID}, Name: "kmsConfig1", State: "created"},
	}
}

func TestV1betaEncryptPool_ParseAndValidateRegionAndZoneError(t *testing.T) {
	original := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = original }()
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "", "", &gcpgenserver.Error{Code: 400, Message: "LocationID represents neither a region nor a zone"}
	}

	handler := Handler{}
	result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

	assert.NoError(t, err)
	assert.NotNil(t, result)
	badReq, ok := result.(*gcpgenserver.V1betaEncryptPoolBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badReq.Code)
	assert.Equal(t, "LocationID represents neither a region nor a zone", badReq.Message)
}

func TestV1betaEncryptPool_GetAccountNotFound(t *testing.T) {
	defer stubValidRegionAndZone()()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().GetAccount(mock.Anything, testEncryptPoolProject).
		Return(nil, vcperrors.NewNotFoundErr("Account", nil))

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

	assert.NoError(t, err)
	notFound, ok := result.(*gcpgenserver.V1betaEncryptPoolNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusNotFound), notFound.Code)
	assert.Equal(t, "Account not found", notFound.Message)
}

func TestV1betaEncryptPool_GetAccountInternalError(t *testing.T) {
	defer stubValidRegionAndZone()()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().GetAccount(mock.Anything, testEncryptPoolProject).
		Return(nil, fmt.Errorf("db unavailable"))

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

	assert.NoError(t, err)
	serverErr, ok := result.(*gcpgenserver.V1betaEncryptPoolInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusInternalServerError), serverErr.Code)
	assert.Equal(t, "db unavailable", serverErr.Message)
}

func TestV1betaEncryptPool_HasActiveClusterUpgradeError(t *testing.T) {
	defer stubValidRegionAndZone()()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().GetAccount(mock.Anything, testEncryptPoolProject).Return(testAccount(), nil)
	mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, testEncryptPoolID).
		Return(false, fmt.Errorf("upgrade check failed"))

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

	assert.NoError(t, err)
	serverErr, ok := result.(*gcpgenserver.V1betaEncryptPoolInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusInternalServerError), serverErr.Code)
	assert.Equal(t, "upgrade check failed", serverErr.Message)
}

func TestV1betaEncryptPool_HasActiveClusterUpgradeTrue(t *testing.T) {
	defer stubValidRegionAndZone()()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().GetAccount(mock.Anything, testEncryptPoolProject).Return(testAccount(), nil)
	mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, testEncryptPoolID).Return(true, nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

	assert.NoError(t, err)
	conflict, ok := result.(*gcpgenserver.V1betaEncryptPoolConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusConflict), conflict.Code)
	assert.Equal(t, "Storage pool is temporarily unavailable, please try again later", conflict.Message)
}

func TestV1betaEncryptPool_GetMultipleKMSConfigsError(t *testing.T) {
	defer stubValidRegionAndZone()()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().GetAccount(mock.Anything, testEncryptPoolProject).Return(testAccount(), nil)
	mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, testEncryptPoolID).Return(false, nil)
	mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{testEncryptPoolKmsConfigID}).
		Return(nil, fmt.Errorf("lookup failed"))

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

	assert.NoError(t, err)
	serverErr, ok := result.(*gcpgenserver.V1betaEncryptPoolInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusInternalServerError), serverErr.Code)
	assert.Equal(t, "lookup failed", serverErr.Message)
}

func TestV1betaEncryptPool_KmsConfigNotFound(t *testing.T) {
	defer stubValidRegionAndZone()()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().GetAccount(mock.Anything, testEncryptPoolProject).Return(testAccount(), nil)
	mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, testEncryptPoolID).Return(false, nil)
	mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{testEncryptPoolKmsConfigID}).
		Return([]*vsaCoreModels.KmsConfig{}, nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

	assert.NoError(t, err)
	notFound, ok := result.(*gcpgenserver.V1betaEncryptPoolNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusNotFound), notFound.Code)
	assert.Equal(t, fmt.Sprintf("CMEK policy with UUID %s not found", testEncryptPoolKmsConfigID), notFound.Message)
}

func TestV1betaEncryptPool_EncryptPoolErrorMappings(t *testing.T) {
	cases := []struct {
		name       string
		orchErr    error
		wantResult func(t *testing.T, res gcpgenserver.V1betaEncryptPoolRes)
	}{
		{
			name:    "NotFound",
			orchErr: vcperrors.NewNotFoundErr("Pool", nil),
			wantResult: func(t *testing.T, res gcpgenserver.V1betaEncryptPoolRes) {
				r, ok := res.(*gcpgenserver.V1betaEncryptPoolNotFound)
				assert.True(t, ok)
				assert.Equal(t, float64(http.StatusNotFound), r.Code)
			},
		},
		{
			name:    "BadRequest",
			orchErr: vcperrors.NewBadRequestErr("invalid pool"),
			wantResult: func(t *testing.T, res gcpgenserver.V1betaEncryptPoolRes) {
				r, ok := res.(*gcpgenserver.V1betaEncryptPoolBadRequest)
				assert.True(t, ok)
				assert.Equal(t, float64(http.StatusBadRequest), r.Code)
			},
		},
		{
			name:    "Conflict",
			orchErr: vcperrors.NewConflictErr("pool busy"),
			wantResult: func(t *testing.T, res gcpgenserver.V1betaEncryptPoolRes) {
				r, ok := res.(*gcpgenserver.V1betaEncryptPoolConflict)
				assert.True(t, ok)
				assert.Equal(t, float64(http.StatusConflict), r.Code)
			},
		},
		{
			name:    "InternalServerError",
			orchErr: fmt.Errorf("unexpected failure"),
			wantResult: func(t *testing.T, res gcpgenserver.V1betaEncryptPoolRes) {
				r, ok := res.(*gcpgenserver.V1betaEncryptPoolInternalServerError)
				assert.True(t, ok)
				assert.Equal(t, float64(http.StatusInternalServerError), r.Code)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer stubValidRegionAndZone()()

			mockOrch := factory.NewMockOrchestratorFactory(t)
			mockOrch.EXPECT().GetAccount(mock.Anything, testEncryptPoolProject).Return(testAccount(), nil)
			mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, testEncryptPoolID).Return(false, nil)
			mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{testEncryptPoolKmsConfigID}).
				Return(testKmsConfigs(), nil)
			mockOrch.EXPECT().EncryptPool(mock.Anything, mock.Anything).Return(nil, "", tc.orchErr)

			handler := Handler{Orchestrator: mockOrch}
			result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

			assert.NoError(t, err)
			assert.NotNil(t, result)
			tc.wantResult(t, result)
		})
	}
}

func TestV1betaEncryptPool_Success(t *testing.T) {
	defer stubValidRegionAndZone()()

	updatedPool := &vsaCoreModels.Pool{
		BaseModel:    vsaCoreModels.BaseModel{UUID: testEncryptPoolID},
		Name:         "pool-resource-id",
		State:        "updating",
		StateDetails: "encrypting pool",
	}

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().GetAccount(mock.Anything, testEncryptPoolProject).Return(testAccount(), nil)
	mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, testEncryptPoolID).Return(false, nil)
	mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{testEncryptPoolKmsConfigID}).
		Return(testKmsConfigs(), nil)
	mockOrch.EXPECT().EncryptPool(mock.Anything, mock.MatchedBy(func(p interface{}) bool {
		return true
	})).Return(updatedPool, testEncryptPoolOperationID, nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

	assert.NoError(t, err)
	op, ok := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.Equal(t, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", testEncryptPoolProject, testEncryptPoolLocation, testEncryptPoolOperationID), op.Name.Value)
	assert.False(t, op.Done.Value)

	var poolResp encryptPoolResponse
	assert.NoError(t, json.Unmarshal(op.Response, &poolResp))
	assert.Equal(t, testEncryptPoolID, poolResp.PoolId)
	assert.Equal(t, "pool-resource-id", poolResp.ResourceId)
	assert.Equal(t, "updating", poolResp.StoragePoolState)
	assert.Equal(t, "encrypting pool", poolResp.StoragePoolStateDetails)
	assert.Equal(t, testEncryptPoolKmsConfigID, poolResp.KmsConfigId)
}

func TestV1betaEncryptPool_SuccessNilPool(t *testing.T) {
	defer stubValidRegionAndZone()()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().GetAccount(mock.Anything, testEncryptPoolProject).Return(testAccount(), nil)
	mockOrch.EXPECT().HasActiveClusterUpgrade(mock.Anything, testEncryptPoolID).Return(false, nil)
	mockOrch.EXPECT().GetMultipleKMSConfigs(mock.Anything, []string{testEncryptPoolKmsConfigID}).
		Return(testKmsConfigs(), nil)
	mockOrch.EXPECT().EncryptPool(mock.Anything, mock.Anything).Return(nil, testEncryptPoolOperationID, nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaEncryptPool(context.Background(), encryptPoolTestRequest(), encryptPoolTestParams())

	assert.NoError(t, err)
	op, ok := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.Equal(t, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", testEncryptPoolProject, testEncryptPoolLocation, testEncryptPoolOperationID), op.Name.Value)

	var poolResp encryptPoolResponse
	assert.NoError(t, json.Unmarshal(op.Response, &poolResp))
	assert.Empty(t, poolResp.PoolId)
	assert.Equal(t, testEncryptPoolKmsConfigID, poolResp.KmsConfigId)
}
