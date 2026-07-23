package resource_events_activities

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/async"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/resource_events"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func Test_HandleResourceEventForSDEActivity(t *testing.T) {
	t.Run("HandleResourceEventCheckForVCPActivity_WhenResourceTypeIsKmsConfig", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeKmsConfig,
			ResourceId:   "test-kms-config-id",
		}

		mockSE.On("GetKmsConfig", ctx, params.ResourceId).Return(&datamodel.KmsConfig{}, nil)

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})
	t.Run("HandleResourceEventCheckForVCPActivity_WhenResourceTypeIsStoragePool", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeStoragePool,
			ResourceId:    "test-storage-pool-id",
			ProjectNumber: "test-project-number",
		}

		account := datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-account-id",
				ID:   1,
			},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(&account, nil)
		mockSE.On("PoolExistsForAccount", ctx, params.ResourceId, mock.Anything).Return(true, nil)

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})
	t.Run("HandleResourceEventCheckForVCPActivity_WhenResourceTypeIsUnsupported", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: "unsupported-resource-type",
			ResourceId:   "test-resource-id",
		}

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unsupported resource type")
	})
	t.Run("HandleResourceEventForSDEActivity_SuccessCreated", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "c9a7591c-0070-5d05-9b28-c58dbc227e84",
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		created := &resource_events.V1betaResourceStateUpdateCreated{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
			},
		}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(created, nil, nil, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		result, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-operation-name", *result.Name)
		assert.Equal(tt, true, *result.Done)
	})
	t.Run("HandleResourceEventForSDEActivity_SuccessAccepted", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
		}

		accepted := &resource_events.V1betaResourceStateUpdateAccepted{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(false),
			},
		}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, accepted, nil, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		result, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-operation-name", *result.Name)
		assert.False(tt, *result.Done)
	})
	t.Run("HandleResourceEventForSDEActivity_WhenCVPClientReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
		}

		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, errors.New("Client not available"))

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Client error during HandleResourceEvent")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})
	t.Run("HandleResourceEventForSDEActivity_WhenCVPClientReturnsUnexpectedResponse", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
		}

		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		res, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NoError(tt, err)
		assert.Nil(tt, res)
	})
	t.Run("HandleResourceEventForSDEActivity_SuccessWithSnapshot", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}

		params := &common.HandleResourceEventParams{
			State:              datamodel.ResourceEventStateOff,
			LocationId:         "test-location-id",
			ProjectNumber:      "test-project-number",
			XCorrelationID:     "test-correlation-id",
			ResourceType:       common.ResourceStateV1ResourceTypeSnapshot,
			ResourceId:         "test-snapshot-id",
			ParentResourceID:   "test-parent-volume-id",
			ParentResourceType: "Volume",
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		created := &resource_events.V1betaResourceStateUpdateCreated{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
			},
		}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(created, nil, nil, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		result, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-operation-name", *result.Name)
		assert.Equal(tt, true, *result.Done)
	})
}

func Test_PollHandleResourceEventSDEOperationActivity(t *testing.T) {
	t.Run("PollHandleResourceEventSDEOperationActivity_Success", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NoError(tt, err)
	})
	t.Run("PollHandleResourceEventSDEOperationActivity_WhenJobErrorsOut", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
				Error: &cvpmodels.StatusV1Beta{
					Code:    float64(500),
					Message: "Internal Server Error",
				},
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		var applicationError *temporal.ApplicationError
		assert.NotNil(tt, err)
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
		assert.ErrorContains(tt, err, "Internal server error")
	})
	t.Run("PollHandleResourceEventSDEOperationActivity_WhenJobIsNotFinished", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Error SDE job not done")
	})
	t.Run("PollHandleResourceEventSDEOperationActivity_WhenCVPClientReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(nil, errors.New("Client not available"))

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Client error during HandleResourceEvent")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})
	t.Run("PollHandleResourceEventSDEOperationActivity_WhenOperationNameIsNil", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
		}

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "operation name is nil")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "InvalidRequestErr", applicationError.Type())
	})
	t.Run("PollHandleResourceEventSDEOperationActivity_WhenResultIsDone", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(true),
		}

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.Nil(tt, err)
	})
}

func TestHandleResourceEventsOFFForVCPActivity(t *testing.T) {
	t.Run("HandleResourceEventsOFFForVCPActivity_WhenResourceTypeIsKmsConfig", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeKmsConfig,
			ResourceId:   "test-resource-id",
			State:        datamodel.ResourceEventStateOff,
		}

		mockSE.On("UpdateKmsConfigStateForHandleResource", ctx, params.ResourceId, datamodel.LifeCycleStateDisabledDetails, datamodel.ResourceEventStateOff).Return(nil, nil)

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenResourceTypeIsUnsupported", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: "unsupported-resource-type",
			ResourceId:   "test-resource-id",
		}

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unsupported resource type")
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenResourceTypeIsSnapshot", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeSnapshot,
			ResourceId:   "test-snapshot-id",
		}

		mockSE.On("UpdateSnapshotForHandleResource", ctx, &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: params.ResourceId},
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: datamodel.LifeCycleStateDisabledDetails,
		}).Return(nil, nil)

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenResourceTypeIsHostGroup", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeHostGroup,
			ResourceId:   "test-snapshot-id",
		}

		account := datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}
		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(&account, nil)

		mockSE.On("UpdateHostGroupsStateForHandleResource", ctx, params.ResourceId, account.ID,
			datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails).Return(nil)

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenResourceTypeIsStoragePool", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeStoragePool,
			ResourceId:    "test-storage-pool-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
		}

		mockSE.On("GetAccount", ctx, "test-project-number").Return(mockAccount, nil)
		mockSE.On("PoolExistsForAccount", ctx, "test-storage-pool-id", mock.Anything).Return(true, nil)

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenResourceTypeIsAD", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockAD := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{UUID: "test-ad-id"},
		}
		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(mockAD, nil)
		// OFF event: no SVM check, state set directly to DISABLED
		mockSE.On("UpdateActiveDirectory", ctx, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
			return ad.UUID == "test-ad-id" && ad.State == datamodel.LifeCycleStateDisabled && ad.StateDetails == datamodel.LifeCycleStateDisabledDetails
		})).Return(mockAD, nil)

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenADNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(nil, nil)

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "active directory not found")
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenADGetFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(nil, utilErrors.NewNotFoundErr("active-directory", nil))

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenADUpdateFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockAD := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{UUID: "test-ad-id"},
		}
		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(mockAD, nil)
		mockSE.On("UpdateActiveDirectory", ctx, mock.Anything).Return(nil, errors.New("update failed"))

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "update failed")
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenResourceTypeIsUnsupported", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: "unsupported-resource-type",
			ResourceId:   "test-resource-id",
		}

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unsupported resource type")
	})

	t.Run("HandleResourceEventsOFFForVCPActivity_WhenUpdateFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeVolume,
			ResourceId:   "test-volume-id",
		}

		mockSE.On("UpdateVolume", ctx, &datamodel.Volume{
			BaseModel:    datamodel.BaseModel{UUID: params.ResourceId},
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: datamodel.LifeCycleStateDisabledDetails,
		}).Return(errors.New("update failed"))

		result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "update failed")
	})
}

func Test_HandleResourceEventCheckForVCPActivity(t *testing.T) {
	t.Run("HandleResourceEventCheckForVCPActivity_KmsConfigExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeKmsConfig,
			ResourceId:   "test-kms-config-id",
		}

		mockSE.On("GetKmsConfig", ctx, params.ResourceId).Return(&datamodel.KmsConfig{}, nil)

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventCheckForVCPActivity_StoragePoolExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeStoragePool,
			ResourceId:    "test-storage-pool-id",
			ProjectNumber: "test-project-number",
		}
		account := datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-account-id",
				ID:   1,
			},
		}
		pool := datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-storage-pool-id",
				ID:   1,
			},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(&datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "test-account-id", ID: 1}}, nil)
		pool.AccountID = account.ID
		mockSE.On("PoolExistsForAccount", ctx, params.ResourceId, mock.Anything).Return(true, nil)

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventCheckForVCPActivity_SnapshotExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType:     common.ResourceStateV1ResourceTypeSnapshot,
			ResourceId:       "test-snapshot-id",
			ProjectNumber:    "test-project-number",
			ParentResourceID: "test-volume-id",
		}
		account := datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-account-id",
				ID:   1,
			},
		}
		volume := datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
				ID:   1,
			},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(&account, nil)
		mockSE.On("GetVolumeWithAccountID", ctx, params.ParentResourceID, account.ID).Return(&volume, nil)
		mockSE.On("GetSnapshotByUUID", ctx, params.ResourceId, account.ID, volume.ID).Return(&datamodel.Snapshot{}, nil)

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventCheckForVCPActivity_VolumeExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeVolume,
			ResourceId:   "test-volume-id",
		}

		mockSE.On("GetVolume", ctx, params.ResourceId).Return(&datamodel.Volume{}, nil)

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventCheckForVCPActivity_BackupPolicyExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeBackupPolicy,
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-123",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}
		mockBackupPolicy := &datamodel.BackupPolicy{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-policy-id"},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(mockBackupPolicy, nil)

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventCheckForVCPActivity_ActiveDirectoryExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(&datamodel.ActiveDirectory{}, nil)

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventCheckForVCPActivity_ActiveDirectoryNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(nil, nil)

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "active directory not found")
	})

	t.Run("HandleResourceEventCheckForVCPActivity_UnsupportedResourceType", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: "unsupported-resource-type",
			ResourceId:   "test-resource-id",
		}

		result, err := activity.HandleResourceEventCheckForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unsupported resource type")
	})
}

func TestHandleResourceEventsONForVCPActivity(t *testing.T) {
	t.Run("HandleResourceEventsONForVCPActivity_WhenResourceTypeIsVolume", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeVolume,
			ResourceId:   "test-resource-id",
		}

		mockSE.On("UpdateVolume", ctx, &datamodel.Volume{
			BaseModel:    datamodel.BaseModel{UUID: params.ResourceId},
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}).Return(nil)

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenResourceTypeIsUnsupported", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: "unsupported-resource-type",
			ResourceId:   "test-resource-id",
		}

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unsupported resource type")
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenResourceTypeIsSnapshot", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeSnapshot,
			ResourceId:   "test-snapshot-id",
		}

		mockSE.On("UpdateSnapshotForHandleResource", ctx, &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: params.ResourceId},
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}).Return(nil, nil)

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenResourceTypeIsHostGroup", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeHostGroup,
			ResourceId:   "test-snapshot-id",
		}

		account := datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}}
		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(&account, nil)

		mockSE.On("UpdateHostGroupsStateForHandleResource", ctx, params.ResourceId, account.ID,
			datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(nil)

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenResourceTypeIsStoragePool", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeStoragePool,
			ResourceId:    "test-storage-pool-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
		}

		mockSE.On("GetAccount", ctx, "test-project-number").Return(mockAccount, nil)
		mockSE.On("PoolExistsForAccount", ctx, "test-storage-pool-id", mock.Anything).Return(true, nil)

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenResourceTypeIsAD_NoSVMs_Ready", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockAD := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{UUID: "test-ad-id"},
		}
		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(mockAD, nil)
		mockSE.On("GetSVMsUsingActiveDirectory", ctx, mockAD.ID).Return([]*datamodel.Svm{}, nil)
		mockSE.On("UpdateActiveDirectory", ctx, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
			return ad.UUID == "test-ad-id" && ad.State == datamodel.LifeCycleStateREADY && ad.StateDetails == datamodel.LifeCycleStateReadyDetails
		})).Return(mockAD, nil)

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenResourceTypeIsAD_WithSVMs_InUse", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockAD := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{UUID: "test-ad-id"},
		}
		mockSVM := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-1"},
		}
		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(mockAD, nil)
		mockSE.On("GetSVMsUsingActiveDirectory", ctx, mockAD.ID).Return([]*datamodel.Svm{mockSVM}, nil)
		mockSE.On("UpdateActiveDirectory", ctx, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
			return ad.UUID == "test-ad-id" && ad.State == datamodel.LifeCycleStateInUse && ad.StateDetails == datamodel.LifeCycleStateInUseDetails
		})).Return(mockAD, nil)

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenADNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(nil, nil)

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "active directory not found")
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenADGetFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(nil, utilErrors.NewNotFoundErr("active-directory", nil))

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenADUpdateFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockAD := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{UUID: "test-ad-id"},
		}
		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(mockAD, nil)
		mockSE.On("GetSVMsUsingActiveDirectory", ctx, mockAD.ID).Return([]*datamodel.Svm{}, nil)
		mockSE.On("UpdateActiveDirectory", ctx, mock.Anything).Return(nil, errors.New("update failed"))

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "update failed")
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenADSVMLookupFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeAD,
			ResourceId:   "test-ad-id",
		}

		mockAD := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{UUID: "test-ad-id"},
		}
		mockSE.On("GetActiveDirectoryByUUID", ctx, params.ResourceId).Return(mockAD, nil)
		mockSE.On("GetSVMsUsingActiveDirectory", ctx, mockAD.ID).Return(nil, errors.New("svm db error"))

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "svm db error")
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenResourceTypeIsUnsupported", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: "unsupported-resource-type",
			ResourceId:   "test-resource-id",
		}

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unsupported resource type")
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenUpdateFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeVolume,
			ResourceId:   "test-volume-id",
		}

		mockSE.On("UpdateVolume", ctx, &datamodel.Volume{
			BaseModel:    datamodel.BaseModel{UUID: params.ResourceId},
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}).Return(errors.New("update failed"))

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "update failed")
	})

	t.Run("HandleResourceEventsONForVCPActivity_WhenResourceTypeIsKmsConfig", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceType: common.ResourceStateV1ResourceTypeKmsConfig,
			ResourceId:   "test-kms-config-id",
		}

		mockSE.On("UpdateKmsConfigStateForHandleResource", ctx, params.ResourceId, common.ResourceLifeCycleStateEnabledDetails, params.State).Return(nil, nil)

		result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})
}

func TestCheckKmsConfigExistence(t *testing.T) {
	t.Run("KmsConfigExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-kms-config-id",
		}

		mockSE.On("GetKmsConfig", ctx, params.ResourceId).Return(&datamodel.KmsConfig{}, nil)

		result, err := activity.checkKmsConfigExistence(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("KmsConfigNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-kms-config-id",
		}

		mockSE.On("GetKmsConfig", ctx, params.ResourceId).Return(nil, utilErrors.NewNotFoundErr("KmsConfig", nil))

		result, err := activity.checkKmsConfigExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "KmsConfig not found")
		assert.IsType(tt, &temporal.ApplicationError{}, err)
	})

	t.Run("KmsConfigUnexpectedError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-kms-config-id",
		}

		mockSE.On("GetKmsConfig", ctx, params.ResourceId).Return(nil, errors.New("unexpected error"))

		result, err := activity.checkKmsConfigExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unexpected error")
	})
}

func TestCheckStoragePoolExistence(t *testing.T) {
	t.Run("StoragePoolExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-storage-pool-id",
			ProjectNumber: "test-project-number",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-project-number"}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("PoolExistsForAccount", ctx, params.ResourceId, mock.Anything).Return(true, nil)

		result, err := activity.checkStoragePoolExistence(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("StoragePoolNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-storage-pool-id",
			ProjectNumber: "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-project-number"}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("PoolExistsForAccount", ctx, params.ResourceId, mock.Anything).Return(false, nil)

		result, err := activity.checkStoragePoolExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "not found")
		assert.IsType(tt, &temporal.ApplicationError{}, err)
	})

	t.Run("GetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-storage-pool-id",
			ProjectNumber: "test-project-number",
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(nil, errors.New("account retrieval failed"))

		result, err := activity.checkStoragePoolExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "account retrieval failed")
	})

	t.Run("GetPoolUnexpectedError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-storage-pool-id",
			ProjectNumber: "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-project-number"}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("PoolExistsForAccount", ctx, params.ResourceId, mock.Anything).Return(false, errors.New("unexpected error"))

		result, err := activity.checkStoragePoolExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unexpected error")
	})

	t.Run("StoragePoolAccountIDMismatch", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-storage-pool-id",
			ProjectNumber: "test-project-number",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-project-number"}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("PoolExistsForAccount", ctx, params.ResourceId, account.ID).Return(false, nil)

		result, err := activity.checkStoragePoolExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "not found")
		assert.IsType(tt, &temporal.ApplicationError{}, err)
	})
}

func TestHandleHostGroups(t *testing.T) {
	t.Run("HandleHostGroup_Success", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-hostgroup-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("UpdateHostGroupsStateForHandleResource", ctx, params.ResourceId, mockAccount.ID, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(nil)

		result, err := activity.handleHostGroup(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("HandleHostGroup_GetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-hostgroup-id",
			ProjectNumber: "test-project-number",
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(nil, errors.New("account not found"))

		result, err := activity.handleHostGroup(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "account not found")
	})

	t.Run("HandleHostGroup_UpdateHostGroupsStateForHandleResourceFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-hostgroup-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("UpdateHostGroupsStateForHandleResource", ctx, params.ResourceId, mockAccount.ID, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails).Return(errors.New("update failed"))

		result, err := activity.handleHostGroup(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "update failed")
	})

	t.Run("HandleHostGroup_UpdateHostGroupsStateNotFoundErr", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-hostgroup-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("UpdateHostGroupsStateForHandleResource", ctx, params.ResourceId, mockAccount.ID, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails).Return(utilErrors.NewNotFoundErr("HostGroup", nil))

		result, err := activity.handleHostGroup(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, ErrTypeResourceNotFound, applicationError.Type())
	})

	t.Run("HandleHostGroup_SuccessWithDifferentStates", func(tt *testing.T) {
		testCases := []struct {
			name         string
			state        string
			stateDetails string
		}{
			{
				name:         "EnabledState",
				state:        datamodel.LifeCycleStateREADY,
				stateDetails: datamodel.LifeCycleStateAvailableDetails,
			},
			{
				name:         "DisabledState",
				state:        datamodel.LifeCycleStateDisabled,
				stateDetails: datamodel.LifeCycleStateDisabledDetails,
			},
		}

		for _, tc := range testCases {
			tt.Run(tc.name, func(ttt *testing.T) {
				ctx := context.Background()
				mockSE := database.NewMockStorage(ttt)
				activity := &ResourceEventsActivity{SE: mockSE}

				params := &common.HandleResourceEventParams{
					ResourceId:    "test-hostgroup-id",
					ProjectNumber: "test-project-number",
				}

				mockAccount := &datamodel.Account{
					BaseModel: datamodel.BaseModel{ID: 123},
				}

				mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
				mockSE.On("UpdateHostGroupsStateForHandleResource", ctx, params.ResourceId, mockAccount.ID, tc.state, tc.stateDetails).Return(nil)

				result, err := activity.handleHostGroup(ctx, params, tc.state, tc.stateDetails)
				assert.True(ttt, result)
				assert.Nil(ttt, err)
			})
		}
	})
}

// Unit tests for checkSnapshotExistence
func TestCheckSnapshotExistence(t *testing.T) {
	t.Run("SnapshotExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:       "test-snapshot-id",
			ParentResourceID: "test-volume-id",
			ProjectNumber:    "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-project-number"}
		volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: 2}}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetVolumeWithAccountID", ctx, params.ParentResourceID, account.ID).Return(volume, nil)
		mockSE.On("GetSnapshotByUUID", ctx, params.ResourceId, account.ID, volume.ID).Return(&datamodel.Snapshot{}, nil)

		result, err := activity.checkSnapshotExistence(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("SnapshotNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:       "test-snapshot-id",
			ParentResourceID: "test-volume-id",
			ProjectNumber:    "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-project-number"}
		volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: 2}}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetVolumeWithAccountID", ctx, params.ParentResourceID, account.ID).Return(volume, nil)
		mockSE.On("GetSnapshotByUUID", ctx, params.ResourceId, account.ID, volume.ID).Return(nil, utilErrors.NewNotFoundErr("Snapshot", nil))

		result, err := activity.checkSnapshotExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Snapshot not found")
		assert.IsType(tt, &temporal.ApplicationError{}, err)
	})

	t.Run("ParentVolumeNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:       "test-snapshot-id",
			ParentResourceID: "test-volume-id",
			ProjectNumber:    "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-project-number"}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetVolumeWithAccountID", ctx, params.ParentResourceID, account.ID).Return(nil, utilErrors.NewNotFoundErr("Volume", nil))

		result, err := activity.checkSnapshotExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Volume not found")
	})

	t.Run("UnexpectedErrorWhileRetrievingSnapshot", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:       "test-snapshot-id",
			ParentResourceID: "test-volume-id",
			ProjectNumber:    "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-project-number"}
		volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: 2}}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetVolumeWithAccountID", ctx, params.ParentResourceID, account.ID).Return(volume, nil)
		mockSE.On("GetSnapshotByUUID", ctx, params.ResourceId, account.ID, volume.ID).Return(nil, errors.New("unexpected error"))

		result, err := activity.checkSnapshotExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unexpected error")
	})
}

// Unit tests for checkVolumeExistence
func TestCheckVolumeExistence(t *testing.T) {
	t.Run("VolumeExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-volume-id",
			ProjectNumber: "test-project-number",
		}

		mockSE.On("GetVolume", ctx, params.ResourceId).Return(&datamodel.Volume{}, nil)

		result, err := activity.checkVolumeExistence(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("VolumeNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-volume-id",
			ProjectNumber: "test-project-number",
		}

		mockSE.On("GetVolume", ctx, params.ResourceId).Return(nil, utilErrors.NewNotFoundErr("Volume", nil))

		result, err := activity.checkVolumeExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Volume not found")
		assert.IsType(tt, &temporal.ApplicationError{}, err)
	})

	t.Run("UnexpectedErrorWhileRetrievingVolume", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-volume-id",
			ProjectNumber: "test-project-number",
		}

		mockSE.On("GetVolume", ctx, params.ResourceId).Return(nil, errors.New("unexpected error"))

		result, err := activity.checkVolumeExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unexpected error")
	})
}

func TestDeleteVolumeForPool(t *testing.T) {
	t.Run("DeleteVolumeForPool_Success", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-volume-id",
			ProjectNumber: "test-project-number",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-pool-id",
				ID:   1,
			},
			AccountID: 1,
			State:     common.ResourceStateEnabled,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: params.ResourceId,
				ID:   1,
			},
			AccountID: 1,
			PoolID:    pool.ID,
		}

		mockSE.On("DeleteVolumeAndChildResources", ctx, params.ResourceId).Return(&datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: params.ResourceId,
				ID:   1,
			},
			AccountID: 1,
			PoolID:    1,
			State:     common.ResourceStateEnabled,
		}, nil)

		err := activity.DeleteVolumeForPool(ctx, volume)
		assert.Nil(tt, err)
	})

	t.Run("DeleteVolumeForPool_DeleteVolumeAndChildResourcesFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-volume-id",
			ProjectNumber: "test-project-number",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "test-pool-id",
				ID:   1,
			},
			AccountID: 1,
			State:     common.ResourceStateEnabled,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: params.ResourceId,
				ID:   1,
			},
			AccountID: 1,
			PoolID:    pool.ID,
		}

		mockSE.On("DeleteVolumeAndChildResources", ctx, params.ResourceId).Return(nil, errors.New("deletion failed"))

		err := activity.DeleteVolumeForPool(ctx, volume)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "deletion failed")
	})
}

func TestDeleteReplicationsForVolume(t *testing.T) {
	t.Run("DeleteReplicationsForVolume_Success", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
				ID:   1,
			},
			AccountID: 1,
			PoolID:    1,
			State:     common.ResourceStateEnabled,
		}

		volumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "test-replication-id",
				ID:   1,
			},
			VolumeID:  volume.ID,
			AccountID: volume.AccountID,
			State:     common.ResourceStateEnabled,
		}

		filter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("account_id", "=", volume.AccountID),
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))

		mockSE.On("ListVolumeReplications", ctx, *filter, 0).Return([]*datamodel.VolumeReplication{volumeReplication}, nil)
		mockSE.On("DeleteVolumeReplication", ctx, volumeReplication).Return(volumeReplication, nil)

		err := activity.DeleteReplicationsForVolume(ctx, volume)
		assert.Nil(tt, err)
	})

	t.Run("DeleteReplicationsForVolume_ListVolumeReplicationsFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
				ID:   1,
			},
			AccountID: 1,
			PoolID:    1,
			State:     common.ResourceStateEnabled,
		}

		filter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("account_id", "=", volume.AccountID),
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))

		mockSE.On("ListVolumeReplications", ctx, *filter, 0).Return(nil, errors.New("listing failed"))

		err := activity.DeleteReplicationsForVolume(ctx, volume)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "listing failed")
	})

	t.Run("DeleteReplicationsForVolume_DeleteVolumeReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
				ID:   1,
			},
			AccountID: 1,
			PoolID:    1,
			State:     common.ResourceStateEnabled,
		}

		volumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "test-replication-id",
				ID:   1,
			},
			VolumeID:  volume.ID,
			AccountID: volume.AccountID,
			State:     common.ResourceStateEnabled,
		}

		filter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("account_id", "=", volume.AccountID),
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))

		mockSE.On("ListVolumeReplications", ctx, *filter, 0).Return([]*datamodel.VolumeReplication{volumeReplication}, nil)
		mockSE.On("DeleteVolumeReplication", ctx, volumeReplication).Return(nil, errors.New("deletion failed"))

		err := activity.DeleteReplicationsForVolume(ctx, volume)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "deletion failed")
	})

	t.Run("DeleteReplicationsForVolume_DeletesClusterPeeringWhenPresent", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid", ID: 1},
			AccountID: 1,
			PoolID:    1,
			State:     common.ResourceStateEnabled,
		}
		replicationWithPeer := &datamodel.VolumeReplication{
			BaseModel:     datamodel.BaseModel{UUID: "repl-uuid", ID: 1},
			VolumeID:      volume.ID,
			AccountID:     volume.AccountID,
			State:         common.ResourceStateEnabled,
			ClusterPeerId: sql.NullInt64{Int64: 42, Valid: true},
		}

		filter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("account_id", "=", volume.AccountID),
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))

		mockSE.On("ListVolumeReplications", ctx, *filter, 0).Return([]*datamodel.VolumeReplication{replicationWithPeer}, nil)
		mockSE.On("DeleteVolumeReplication", ctx, replicationWithPeer).Return(replicationWithPeer, nil)
		mockSE.On("DeleteClusterPeeringRow", ctx, mock.MatchedBy(func(row *datamodel.ClusterPeerings) bool { return row.ID == 42 })).Return(nil)

		err := activity.DeleteReplicationsForVolume(ctx, volume)
		assert.Nil(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeleteReplicationsForVolume_ClusterPeeringAlreadyDeleted", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid", ID: 1},
			AccountID: 1,
			PoolID:    1,
			State:     common.ResourceStateEnabled,
		}
		replicationWithPeer := &datamodel.VolumeReplication{
			BaseModel:     datamodel.BaseModel{UUID: "repl-uuid", ID: 1},
			VolumeID:      volume.ID,
			AccountID:     volume.AccountID,
			State:         common.ResourceStateEnabled,
			ClusterPeerId: sql.NullInt64{Int64: 42, Valid: true},
		}

		filter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("account_id", "=", volume.AccountID),
			dbutils.NewFilterCondition("volume_id", "=", volume.ID))

		mockSE.On("ListVolumeReplications", ctx, *filter, 0).Return([]*datamodel.VolumeReplication{replicationWithPeer}, nil)
		mockSE.On("DeleteVolumeReplication", ctx, replicationWithPeer).Return(replicationWithPeer, nil)
		mockSE.On("DeleteClusterPeeringRow", ctx, mock.MatchedBy(func(row *datamodel.ClusterPeerings) bool { return row.ID == 42 })).
			Return(utilErrors.NewNotFoundErr("cluster peer", nil))

		err := activity.DeleteReplicationsForVolume(ctx, volume)
		assert.Nil(tt, err)
		mockSE.AssertExpectations(tt)
	})
}

func TestDeleteClusterPeeringsForVolume(t *testing.T) {
	t.Run("Success_DeletesClusterPeering", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		clusterPeerID := int64(42)
		volume := &datamodel.Volume{
			BaseModel:     datamodel.BaseModel{UUID: "vol-uuid", ID: 1},
			AccountID:     1,
			ClusterPeerID: sql.NullInt64{Int64: clusterPeerID, Valid: true},
		}

		mockSE.On("DeleteClusterPeeringRow", ctx, mock.MatchedBy(func(row *datamodel.ClusterPeerings) bool {
			return row != nil && row.ID == clusterPeerID
		})).Return(nil)

		err := activity.DeleteClusterPeeringsForVolume(ctx, volume)
		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("Success_SkipsWhenNoClusterPeerID", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		volume := &datamodel.Volume{
			BaseModel:     datamodel.BaseModel{UUID: "vol-uuid", ID: 1},
			AccountID:     1,
			ClusterPeerID: sql.NullInt64{Valid: false},
		}

		err := activity.DeleteClusterPeeringsForVolume(ctx, volume)
		assert.NoError(tt, err)
		mockSE.AssertNotCalled(tt, "DeleteClusterPeeringRow")
	})

	t.Run("Success_SkipsWhenNilVolume", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		err := activity.DeleteClusterPeeringsForVolume(ctx, nil)
		assert.NoError(tt, err)
		mockSE.AssertNotCalled(tt, "DeleteClusterPeeringRow")
	})

	t.Run("DeleteClusterPeeringRowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		clusterPeerID := int64(42)
		volume := &datamodel.Volume{
			BaseModel:     datamodel.BaseModel{UUID: "vol-uuid", ID: 1},
			AccountID:     1,
			ClusterPeerID: sql.NullInt64{Int64: clusterPeerID, Valid: true},
		}

		mockSE.On("DeleteClusterPeeringRow", ctx, mock.MatchedBy(func(row *datamodel.ClusterPeerings) bool {
			return row != nil && row.ID == clusterPeerID
		})).Return(errors.New("delete failed"))

		err := activity.DeleteClusterPeeringsForVolume(ctx, volume)
		assert.Error(tt, err)
		assert.ErrorContains(tt, err, "delete failed")
	})

	t.Run("Success_ClusterPeeringAlreadyDeleted", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		clusterPeerID := int64(42)
		volume := &datamodel.Volume{
			BaseModel:     datamodel.BaseModel{UUID: "vol-uuid", ID: 1},
			AccountID:     1,
			ClusterPeerID: sql.NullInt64{Int64: clusterPeerID, Valid: true},
		}

		mockSE.On("DeleteClusterPeeringRow", ctx, mock.MatchedBy(func(row *datamodel.ClusterPeerings) bool {
			return row != nil && row.ID == clusterPeerID
		})).Return(utilErrors.NewNotFoundErr("cluster peer", nil))

		err := activity.DeleteClusterPeeringsForVolume(ctx, volume)
		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})
}

// Unit tests for checkBackupPolicyExistence
func TestCheckBackupPolicyExistence(t *testing.T) {
	t.Run("BackupPolicyExists", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}
		mockBackupPolicy := &datamodel.BackupPolicy{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-policy-id"},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(mockBackupPolicy, nil)

		result, err := activity.checkBackupPolicyExistence(ctx, params)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("BackupPolicyNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(nil, utilErrors.NewNotFoundErr("BackupPolicy", nil))

		result, err := activity.checkBackupPolicyExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "BackupPolicy not found")
		assert.IsType(tt, &temporal.ApplicationError{}, err)
	})

	t.Run("GetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(nil, errors.New("account error"))

		result, err := activity.checkBackupPolicyExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "account error")
	})

	t.Run("UnexpectedErrorWhileRetrievingBackupPolicy", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(nil, errors.New("unexpected error"))

		result, err := activity.checkBackupPolicyExistence(ctx, params)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unexpected error")
	})
}

func Test_HandleBackupPolicyResourceEvent(t *testing.T) {
	t.Run("HandleBackupPolicyResourceEventOFF_PausesSchedule", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeBackupPolicy,
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		backupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: true,
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, account.ID).Return(backupPolicy, nil)

		// Mock the scheduler describe call to return that it's currently not paused
		mockScheduler.On("Describe", ctx, mock.AnythingOfType("scheduler.DescribeScheduleParams")).Return(&scheduler.ScheduleDescription{Paused: false}, nil)
		// Mock the scheduler pause call
		mockScheduler.On("Pause", ctx, mock.AnythingOfType("scheduler.PauseScheduleParams")).Return(&scheduler.ScheduleResponse{}, nil)

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.True(tt, result)
		assert.Nil(tt, err)
		mockScheduler.AssertExpectations(tt)
	})

	t.Run("HandleBackupPolicyResourceEventON_UnpausesScheduleWhenEnabled", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeBackupPolicy,
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		backupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: true,
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, account.ID).Return(backupPolicy, nil)

		// Mock the scheduler describe call to return that it's currently paused
		mockScheduler.On("Describe", ctx, mock.AnythingOfType("scheduler.DescribeScheduleParams")).Return(&scheduler.ScheduleDescription{Paused: true}, nil)
		// Mock the scheduler unpause call
		mockScheduler.On("Unpause", ctx, mock.AnythingOfType("scheduler.UnpauseScheduleParams")).Return(&scheduler.ScheduleResponse{}, nil)

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
		assert.True(tt, result)
		assert.Nil(tt, err)
		mockScheduler.AssertExpectations(tt)
	})

	t.Run("HandleBackupPolicyResourceEventON_SkipsUnpauseWhenDisabled", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeBackupPolicy,
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		backupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: false, // Policy is disabled in DB
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, account.ID).Return(backupPolicy, nil)

		// Scheduler unpause should NOT be called - no mock setup for Unpause

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
		assert.True(tt, result)
		assert.Nil(tt, err)
		mockScheduler.AssertExpectations(tt) // Should pass because Unpause was not called
	})

	t.Run("HandleBackupPolicyResourceEventOFF_SkipsPauseWhenDisabled", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeBackupPolicy,
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		backupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: false, // Policy is disabled in DB
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, account.ID).Return(backupPolicy, nil)

		// Scheduler pause should NOT be called - no mock setup for Pause

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.True(tt, result)
		assert.Nil(tt, err)
		mockScheduler.AssertExpectations(tt) // Should pass because Pause was not called
	})

	t.Run("HandleBackupPolicyResourceEventOFF_SkipsPauseWhenAlreadyPaused", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeBackupPolicy,
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		backupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: true, // Policy is enabled in DB
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, account.ID).Return(backupPolicy, nil)

		// Mock the scheduler describe call to return that it's already paused
		mockScheduler.On("Describe", ctx, mock.AnythingOfType("scheduler.DescribeScheduleParams")).Return(&scheduler.ScheduleDescription{Paused: true}, nil)
		// Scheduler pause should NOT be called because it's already paused

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.True(tt, result)
		assert.Nil(tt, err)
		mockScheduler.AssertExpectations(tt)
	})

	t.Run("HandleBackupPolicyResourceEventON_SkipsUnpauseWhenAlreadyActive", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceType:  common.ResourceStateV1ResourceTypeBackupPolicy,
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		backupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: true, // Policy is enabled in DB
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(account, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, account.ID).Return(backupPolicy, nil)

		// Mock the scheduler describe call to return that it's already active (not paused)
		mockScheduler.On("Describe", ctx, mock.AnythingOfType("scheduler.DescribeScheduleParams")).Return(&scheduler.ScheduleDescription{Paused: false}, nil)
		// Scheduler unpause should NOT be called because it's already active

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
		assert.True(tt, result)
		assert.Nil(tt, err)
		mockScheduler.AssertExpectations(tt)
	})
}

func Test_HandleResourceEventsOFFForVCPActivity_UnsupportedType(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	activity := &ResourceEventsActivity{SE: mockSE}

	params := &common.HandleResourceEventParams{
		ResourceType: "UnsupportedType",
	}

	result, err := activity.HandleResourceEventsOFFForVCPActivity(ctx, params)
	assert.False(t, result)
	assert.NotNil(t, err)
	assert.ErrorContains(t, err, "unsupported resource type")
}

func Test_HandleResourceEventsONForVCPActivity_UnsupportedType(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	activity := &ResourceEventsActivity{SE: mockSE}

	params := &common.HandleResourceEventParams{
		ResourceType: "UnsupportedType",
	}

	result, err := activity.HandleResourceEventsONForVCPActivity(ctx, params)
	assert.False(t, result)
	assert.NotNil(t, err)
	assert.ErrorContains(t, err, "unsupported resource type")
}

func Test_HandleBackupPolicyErrorCases(t *testing.T) {
	t.Run("GetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(nil, errors.New("account error"))

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "account error")
	})

	t.Run("GetBackupPolicyFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(nil, errors.New("backup policy error"))

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "backup policy error")
	})

	t.Run("GetBackupPolicyNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(nil, utilErrors.NewNotFoundErr("BackupPolicy", nil))

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.True(tt, utilErrors.IsNotFoundErr(err))
	})

	t.Run("PauseBackupPolicyScheduleFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}
		mockBackupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: true,
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(mockBackupPolicy, nil)

		// Mock scheduler calls for pause operation
		mockScheduler.On("Describe", ctx, mock.Anything).Return(&scheduler.ScheduleDescription{Paused: false}, nil)
		mockScheduler.On("Pause", ctx, mock.Anything).Return(nil, errors.New("pause failed"))

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "pause failed")
	})

	t.Run("UnpauseBackupPolicyScheduleFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}
		mockBackupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: true,
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(mockBackupPolicy, nil)

		// Mock scheduler calls for unpause operation
		mockScheduler.On("Describe", ctx, mock.Anything).Return(&scheduler.ScheduleDescription{Paused: true}, nil)
		mockScheduler.On("Unpause", ctx, mock.Anything).Return(nil, errors.New("unpause failed"))

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unpause failed")
	})

	t.Run("UnknownState", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}
		mockBackupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: true,
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(mockBackupPolicy, nil)

		result, err := activity.handleBackupPolicy(ctx, params, "unknown-state", "unknown-details")
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "unknown state for backup policy resource event")
	})

	t.Run("BackupPolicyDisabledInDatabase_SkipPause", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}
		mockBackupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: false, // Disabled in database
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(mockBackupPolicy, nil)

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})

	t.Run("BackupPolicyDisabledInDatabase_SkipUnpause", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		mockScheduler := scheduler.NewMockScheduler(tt)
		activity := &ResourceEventsActivity{SE: mockSE, Scheduler: mockScheduler}

		params := &common.HandleResourceEventParams{
			ResourceId:    "test-backup-policy-id",
			ProjectNumber: "test-project-number",
		}

		mockAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 123},
		}
		mockBackupPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "test-backup-policy-id"},
			PolicyEnabled: false, // Disabled in database
		}

		mockSE.On("GetAccount", ctx, params.ProjectNumber).Return(mockAccount, nil)
		mockSE.On("GetBackupPolicyByUUIDAndOwnerID", ctx, params.ResourceId, mockAccount.ID).Return(mockBackupPolicy, nil)

		result, err := activity.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
		assert.True(tt, result)
		assert.Nil(tt, err)
	})
}

// Test HTTP status code constants usage in handle resource events
func Test_HTTPStatusConstants_HandleResourceEvent(t *testing.T) {
	// Verify that constants from common package are used correctly
	testCases := []struct {
		name             string
		statusCode       int32
		expectedConstant int
	}{
		{"BadRequest", 400, common.HTTPStatusBadRequest},
		{"Unauthorized", 401, common.HTTPStatusUnauthorized},
		{"Forbidden", 403, common.HTTPStatusForbidden},
		{"NotFound", 404, common.HTTPStatusNotFound},
		{"TooManyRequests", 429, common.HTTPStatusTooManyRequests},
		{"InternalServerError", 500, common.HTTPStatusInternalServerError},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(tt *testing.T) {
			assert.Equal(tt, int(tc.statusCode), tc.expectedConstant,
				"Constant value should match HTTP status code")
		})
	}
}

// Test coverage for missing lines in HandleResourceEventsForSDEActivity error handling
func Test_HandleResourceEventForSDEActivity_ErrorCoverage(t *testing.T) {
	t.Run("HandleResourceEventForSDEActivity_BadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "test-resource-id",
		}

		badRequestErr := &resource_events.V1betaResourceStateUpdateBadRequest{}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, badRequestErr)

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Bad request error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("HandleResourceEventForSDEActivity_UnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "test-resource-id",
		}

		unauthorizedErr := &resource_events.V1betaResourceStateUpdateUnauthorized{}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, unauthorizedErr)

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Unauthorised error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("HandleResourceEventForSDEActivity_ForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "test-resource-id",
		}

		forbiddenErr := &resource_events.V1betaResourceStateUpdateForbidden{}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, forbiddenErr)

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Forbidden error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("HandleResourceEventForSDEActivity_NotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "test-resource-id",
		}

		notFoundErr := &resource_events.V1betaResourceStateUpdateNotFound{}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, notFoundErr)

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Resource not found")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("HandleResourceEventForSDEActivity_ConflictError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "test-resource-id",
		}

		conflictErr := &resource_events.V1betaResourceStateUpdateConflict{}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, conflictErr)

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Conflict error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("HandleResourceEventForSDEActivity_InternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "test-resource-id",
		}

		serverErr := &resource_events.V1betaResourceStateUpdateInternalServerError{}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, serverErr)

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Internal server error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("HandleResourceEventForSDEActivity_TooManyRequestsError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "test-resource-id",
		}

		tooManyRequestsErr := &resource_events.V1betaResourceStateUpdateTooManyRequests{}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, tooManyRequestsErr)

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Too many requests error")

		// This should be retryable error, not non-retryable
		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.False(tt, applicationError.NonRetryable()) // Should be retryable
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("HandleResourceEventForSDEActivity_NotImplementedError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockClient := resource_events.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{ResourceEvents: mockClient}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, JWT string) cvpapi.Cvp {
			return *cvpClient
		}
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "test-resource-id",
		}

		notImplementedErr := &resource_events.V1betaResourceStateUpdateNotImplemented{}
		mockClient.EXPECT().V1betaResourceStateUpdate(mock.Anything).Return(nil, nil, nil, notImplementedErr)

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Not implemented yet error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("HandleResourceEventForSDEActivity_TokenFailure", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "", errors.New("token generation failed")
		}

		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceType:   common.ResourceStateV1ResourceTypeVolume,
			ResourceId:     "test-resource-id",
		}

		activity := &ResourceEventsActivity{SE: mockSE}
		_, err := activity.HandleResourceEventsForSDEActivity(ctx, params)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "token generation failed")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, ErrSignedTokenFailed, applicationError.Type())
	})
}

// Test coverage for missing lines in PollHandleResourceEventSDEOperationActivity error handling
func Test_PollHandleResourceEventSDEOperationActivity_ErrorCoverage(t *testing.T) {
	t.Run("PollHandleResourceEventSDEOperationActivity_BadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceId:     "test-resource-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
				Error: &cvpmodels.StatusV1Beta{
					Code:    float64(common.HTTPStatusBadRequest),
					Message: "Bad Request Message",
				},
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Bad request error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("PollHandleResourceEventSDEOperationActivity_UnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceId:     "test-resource-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
				Error: &cvpmodels.StatusV1Beta{
					Code:    float64(common.HTTPStatusUnauthorized),
					Message: "Unauthorized Message",
				},
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Unauthorised error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("PollHandleResourceEventSDEOperationActivity_ForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceId:     "test-resource-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
				Error: &cvpmodels.StatusV1Beta{
					Code:    float64(common.HTTPStatusForbidden),
					Message: "Forbidden Message",
				},
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Forbidden error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("PollHandleResourceEventSDEOperationActivity_NotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceId:     "test-resource-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
				Error: &cvpmodels.StatusV1Beta{
					Code:    float64(common.HTTPStatusNotFound),
					Message: "Not Found Message",
				},
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Resource not found")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("PollHandleResourceEventSDEOperationActivity_TooManyRequestsError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceId:     "test-resource-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
				Error: &cvpmodels.StatusV1Beta{
					Code:    float64(common.HTTPStatusTooManyRequests),
					Message: "Too Many Requests Message",
				},
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Too many requests error")

		// This should be retryable error, not non-retryable
		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.False(tt, applicationError.NonRetryable()) // Should be retryable
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("PollHandleResourceEventSDEOperationActivity_NotImplementedError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceId:     "test-resource-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
				Error: &cvpmodels.StatusV1Beta{
					Code:    float64(common.HttpStatusNotImplemented),
					Message: "Not Implemented Message",
				},
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Not implemented yet error")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("PollHandleResourceEventSDEOperationActivity_UnknownErrorCode", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		mockAsync := &async.MockClientService{}
		mockCVP := &cvpapi.Cvp{Async: mockAsync}
		originalCreateClient := cvp.CreateClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockCVP }
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "test-jwt-token", nil
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceId:     "test-resource-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		response := &async.V1betaDescribeOperationOK{
			Payload: &cvpmodels.OperationV1beta{
				Name: "test-operation-name",
				Done: nillable.GetBoolPtr(true),
				Error: &cvpmodels.StatusV1Beta{
					Code:    float64(999), // Unknown error code
					Message: "Unknown Error Message",
				},
			},
		}
		mockAsync.EXPECT().V1betaDescribeOperation(mock.Anything).Return(response, nil)

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Client error during HandleResourceEvent")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, "CustomError", applicationError.Type())
	})

	t.Run("PollHandleResourceEventSDEOperationActivity_TokenFailure", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(t)
		originalToken := auth.GetSignedJwtToken
		defer func() { getSignedToken = originalToken }()
		getSignedToken = func(projectNumber string) (string, error) {
			return "", errors.New("token generation failed")
		}
		params := &common.HandleResourceEventParams{
			State:          datamodel.ResourceEventStateOff,
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: "test-correlation-id",
			ResourceId:     "test-resource-id",
		}
		result := &common.HandleResourceEventResult{
			Done: nillable.GetBoolPtr(false),
			Name: nillable.GetStringPtr("test-operation-name"),
		}

		activity := &ResourceEventsActivity{SE: mockSE}
		err := activity.PollHandleResourceEventSDEOperationActivity(ctx, params, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "token generation failed")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, ErrSignedTokenFailed, applicationError.Type())
	})
}

// Test coverage for handleKmsConfig error paths
func Test_HandleKmsConfig_ErrorCoverage(t *testing.T) {
	t.Run("HandleKmsConfig_NotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-kms-config-id",
			State:      datamodel.ResourceEventStateOff,
		}

		mockSE.On("UpdateKmsConfigStateForHandleResource", ctx, params.ResourceId, datamodel.LifeCycleStateDisabledDetails, params.State).Return(nil, utilErrors.NewNotFoundErr("KmsConfig", nil))

		result, err := activity.handleKmsConfig(ctx, params, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "KmsConfig not found")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, ErrTypeResourceNotFound, applicationError.Type())
	})

	t.Run("HandleKmsConfig_UserInputValidationError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-kms-config-id",
			State:      datamodel.ResourceEventStateOff,
		}

		mockSE.On("UpdateKmsConfigStateForHandleResource", ctx, params.ResourceId, datamodel.LifeCycleStateDisabledDetails, params.State).Return(nil, utilErrors.NewUserInputValidationErr("validation failed"))

		result, err := activity.handleKmsConfig(ctx, params, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "validation failed")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, ErrTypeResourceNotFound, applicationError.Type())
	})

	t.Run("HandleKmsConfig_GenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-kms-config-id",
			State:      datamodel.ResourceEventStateOff,
		}

		mockSE.On("UpdateKmsConfigStateForHandleResource", ctx, params.ResourceId, datamodel.LifeCycleStateDisabledDetails, params.State).Return(nil, errors.New("generic error"))

		result, err := activity.handleKmsConfig(ctx, params, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "generic error")

		// This should not be converted to ApplicationError for generic errors
		var applicationError *temporal.ApplicationError
		assert.False(tt, errors2.As(err, &applicationError))
	})
}

// Test coverage for handleSnapshot error paths
func Test_HandleSnapshot_ErrorCoverage(t *testing.T) {
	t.Run("HandleSnapshot_NotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-snapshot-id",
		}

		expectedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				UUID: params.ResourceId,
			},
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: datamodel.LifeCycleStateDisabledDetails,
		}

		mockSE.On("UpdateSnapshotForHandleResource", ctx, expectedSnapshot).Return(nil, utilErrors.NewNotFoundErr("Snapshot", nil))

		result, err := activity.handleSnapshot(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Snapshot not found")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, ErrTypeResourceNotFound, applicationError.Type())
	})

	t.Run("HandleSnapshot_GenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-snapshot-id",
		}

		expectedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				UUID: params.ResourceId,
			},
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: datamodel.LifeCycleStateDisabledDetails,
		}

		mockSE.On("UpdateSnapshotForHandleResource", ctx, expectedSnapshot).Return(nil, errors.New("generic error"))

		result, err := activity.handleSnapshot(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "generic error")

		// This should not be converted to ApplicationError for generic errors
		var applicationError *temporal.ApplicationError
		assert.False(tt, errors2.As(err, &applicationError))
	})
}

// Test coverage for handleVolume error paths
func Test_HandleVolume_ErrorCoverage(t *testing.T) {
	t.Run("HandleVolume_NotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-volume-id",
		}

		expectedVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: params.ResourceId,
			},
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: datamodel.LifeCycleStateDisabledDetails,
		}

		mockSE.On("UpdateVolume", ctx, expectedVolume).Return(utilErrors.NewNotFoundErr("Volume", nil))

		result, err := activity.handleVolume(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "Volume not found")

		var applicationError *temporal.ApplicationError
		assert.True(tt, errors2.As(err, &applicationError))
		assert.True(tt, applicationError.NonRetryable())
		assert.Equal(tt, ErrTypeResourceNotFound, applicationError.Type())
	})

	t.Run("HandleVolume_GenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		params := &common.HandleResourceEventParams{
			ResourceId: "test-volume-id",
		}

		expectedVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: params.ResourceId,
			},
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: datamodel.LifeCycleStateDisabledDetails,
		}

		mockSE.On("UpdateVolume", ctx, expectedVolume).Return(errors.New("generic error"))

		result, err := activity.handleVolume(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
		assert.False(tt, result)
		assert.NotNil(tt, err)
		assert.ErrorContains(tt, err, "generic error")

		// This should not be converted to ApplicationError for generic errors
		var applicationError *temporal.ApplicationError
		assert.False(tt, errors2.As(err, &applicationError))
	})
}

func TestResourceEventsActivity_DeleteVolumeAssociatedQuotaRules(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteVolumeAssociatedQuotaRules)
		volumeID := int64(123)
		quotaRules := []*datamodel.QuotaRule{
			{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-1"}, Name: "quota-rule-1"},
			{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-2"}, Name: "quota-rule-2"},
			{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-3"}, Name: "quota-rule-3"},
		}

		mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
			Return(quotaRules, nil)
		mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-1").Return(&datamodel.QuotaRule{}, nil)
		mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-2").Return(&datamodel.QuotaRule{}, nil)
		mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-3").Return(&datamodel.QuotaRule{}, nil)

		_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedQuotaRules, volumeID)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("NoQuotaRulesFound", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteVolumeAssociatedQuotaRules)
		volumeID := int64(123)

		mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
			Return(nil, utilErrors.NewNotFoundErr("quota rule", nil))

		_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedQuotaRules, volumeID)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetQuotaRulesError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteVolumeAssociatedQuotaRules)
		volumeID := int64(123)

		mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
			Return(nil, errors.New("database connection error"))

		_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedQuotaRules, volumeID)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DeleteQuotaRuleError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteVolumeAssociatedQuotaRules)
		volumeID := int64(123)
		quotaRules := []*datamodel.QuotaRule{
			{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-1"}, Name: "quota-rule-1"},
			{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-2"}, Name: "quota-rule-2"},
			{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-3"}, Name: "quota-rule-3"},
		}

		mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
			Return(quotaRules, nil)
		mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-1").Return(nil, errors.New("delete error for qr-uuid-1"))
		mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-2").Return(&datamodel.QuotaRule{}, nil)
		mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-3").Return(nil, errors.New("delete error for qr-uuid-3"))

		_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedQuotaRules, volumeID)
		assert.NoError(tt, err) // Should continue even if some deletions fail
		mockStorage.AssertExpectations(tt)
	})

	t.Run("EmptyQuotaRulesList", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteVolumeAssociatedQuotaRules)
		volumeID := int64(123)
		emptyQuotaRules := []*datamodel.QuotaRule{}

		mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
			Return(emptyQuotaRules, nil)

		_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedQuotaRules, volumeID)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SingleQuotaRule", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteVolumeAssociatedQuotaRules)
		volumeID := int64(123)
		quotaRules := []*datamodel.QuotaRule{
			{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-1"}, Name: "quota-rule-1"},
		}

		mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
			Return(quotaRules, nil)
		mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-1").Return(&datamodel.QuotaRule{}, nil)

		_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedQuotaRules, volumeID)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("AllDeleteQuotaRuleErrors", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()

		mockStorage := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockStorage}
		env.RegisterActivity(activity.DeleteVolumeAssociatedQuotaRules)
		volumeID := int64(123)
		quotaRules := []*datamodel.QuotaRule{
			{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-1"}, Name: "quota-rule-1"},
			{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-2"}, Name: "quota-rule-2"},
		}

		mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
			Return(quotaRules, nil)
		mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-1").Return(nil, errors.New("delete error for qr-uuid-1"))
		mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-2").Return(nil, errors.New("delete error for qr-uuid-2"))

		_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedQuotaRules, volumeID)
		assert.NoError(tt, err) // Should continue even if all deletions fail
		mockStorage.AssertExpectations(tt)
	})
}

func TestDeleteExpertModeVolumesForPool(t *testing.T) {
	t.Run("Success_DeletesAllExpertModeVolumes", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		poolID := int64(42)
		volumes := []*datamodel.ExpertModeVolumes{
			{BaseModel: datamodel.BaseModel{UUID: "em-vol-1"}, Name: "vol1", PoolID: poolID},
			{BaseModel: datamodel.BaseModel{UUID: "em-vol-2"}, Name: "vol2", PoolID: poolID},
		}

		mockSE.On("ListExpertModeVolumesByPoolID", ctx, poolID).Return(volumes, nil)
		mockSE.On("DeleteExpertModeVolume", ctx, "em-vol-1").Return(nil)
		mockSE.On("DeleteExpertModeVolume", ctx, "em-vol-2").Return(nil)

		err := activity.DeleteExpertModeVolumesForPool(ctx, poolID)
		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("Success_NoVolumesInPool", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		poolID := int64(42)
		mockSE.On("ListExpertModeVolumesByPoolID", ctx, poolID).Return([]*datamodel.ExpertModeVolumes{}, nil)

		err := activity.DeleteExpertModeVolumesForPool(ctx, poolID)
		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
		mockSE.AssertNotCalled(tt, "DeleteExpertModeVolume", mock.Anything, mock.Anything)
	})

	t.Run("Error_ListExpertModeVolumesFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		poolID := int64(42)
		mockSE.On("ListExpertModeVolumesByPoolID", ctx, poolID).Return(nil, errors.New("db connection error"))

		err := activity.DeleteExpertModeVolumesForPool(ctx, poolID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "db connection error")
		mockSE.AssertExpectations(tt)
	})

	t.Run("Error_DeleteExpertModeVolumeFails", func(tt *testing.T) {
		ctx := context.Background()
		mockSE := database.NewMockStorage(tt)
		activity := &ResourceEventsActivity{SE: mockSE}

		poolID := int64(42)
		volumes := []*datamodel.ExpertModeVolumes{
			{BaseModel: datamodel.BaseModel{UUID: "em-vol-1"}, Name: "vol1", PoolID: poolID},
			{BaseModel: datamodel.BaseModel{UUID: "em-vol-2"}, Name: "vol2", PoolID: poolID},
		}

		mockSE.On("ListExpertModeVolumesByPoolID", ctx, poolID).Return(volumes, nil)
		mockSE.On("DeleteExpertModeVolume", ctx, "em-vol-1").Return(errors.New("delete failed"))

		err := activity.DeleteExpertModeVolumesForPool(ctx, poolID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "delete failed")
		mockSE.AssertExpectations(tt)
		mockSE.AssertNotCalled(tt, "DeleteExpertModeVolume", ctx, "em-vol-2")
	})
}
