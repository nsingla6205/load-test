package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/active_directories"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	vcpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// handlerForADSyncCVPTests returns a Handler with orchestrator mocked for trial persist on sync CVP create.
func handlerForADSyncCVPTests(t *testing.T) Handler {
	t.Helper()
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().PersistAccountTrialMetadataIfSet(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	return Handler{Orchestrator: mockOrchestrator}
}

func TestV1betaCreateActiveDirectory_Success(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	mockAD := &vcpModels.ActiveDirectory{
		BaseModel: vcpModels.BaseModel{
			UUID:      "ad-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		AdName:       "ad-name",
		Username:     "user",
		Domain:       "domain",
		DNS:          "dns",
		NetBIOS:      "netbios",
		StateDetails: "details",

		ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
			SecurityOperators:          []string{"secop"},
			BackupOperators:            []string{"backupop"},
			Administrators:             []string{"admin"},
			AesEncryption:              true,
			AllowLocalNFSUsersWithLdap: true,
			EncryptDCConnections:       true,
			LdapSigning:                true,
			OrganizationalUnit:         "ou",
			Site:                       "site",
			KdcIP:                      "kdcip",
			KdcHostname:                "kdchost",
		},
	}
	mockJobID := "job-uuid"
	mockOrch := mockOrchestrator
	mockOrch.On("CreateActiveDirectory", mock.Anything, mock.Anything).Return(mockAD, mockJobID, nil)
	handler.Orchestrator = mockOrch

	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:                    "user",
		ResourceId:                  "ad-name",
		Description:                 gcpgenserver.NewOptString("desc"),
		Password:                    "pass",
		Domain:                      "domain",
		DNS:                         "dns",
		NetBIOS:                     "netbios",
		OrganizationalUnit:          gcpgenserver.NewOptString("ou"),
		Site:                        gcpgenserver.NewOptString("site"),
		KdcIP:                       gcpgenserver.NewOptString("kdcip"),
		KdcHostname:                 gcpgenserver.NewOptString("kdchost"),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString("details"),
		CreatedAt:                   gcpgenserver.NewOptDateTime(time.Now()),
		UpdatedAt:                   gcpgenserver.NewOptDateTime(time.Now()),
		DeletedAt:                   gcpgenserver.NewOptDateTime(time.Now()),
		LdapSigning:                 gcpgenserver.NewOptBool(true),
		AllowLocalNFSUsersWithLdap:  gcpgenserver.NewOptBool(true),
		EncryptDCConnections:        gcpgenserver.NewOptBool(true),
		SecurityOperators:           []string{"secop"},
		BackupOperators:             []string{"backupop"},
		Administrators:              []string{"admin"},
		AesEncryption:               gcpgenserver.NewOptBool(true),
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	op, ok := res.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.Contains(t, op.Name.Value, "job-uuid")
	assert.False(t, op.Done.Value)
	assert.NotNil(t, op.Response)
}

func TestMergeActiveDirectoryResponses(t *testing.T) {
	t.Run("ReturnsEmptySliceWhenNoCVPAds", func(t *testing.T) {
		result := mergeActiveDirectoryResponses(nil, nil)
		assert.Empty(t, result)
	})
	t.Run("PreservesCVPDataWhenNoVCPData", func(t *testing.T) {
		cvpAds := []*models.ActiveDirectoryV1beta{
			{
				ActiveDirectoryID:    "ad-1",
				ResourceID:           nillable.GetStringPtr("resource-1"),
				Username:             nillable.GetStringPtr("user"),
				Password:             nillable.GetStringPtr("pass"),
				Domain:               nillable.GetStringPtr("example.com"),
				DNS:                  nillable.GetStringPtr("10.0.0.1"),
				NetBIOS:              nillable.GetStringPtr("EXAMPLE"),
				ActiveDirectoryState: string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY),
			},
		}
		merged := mergeActiveDirectoryResponses(cvpAds, nil)
		assert.Len(t, merged, 1)
		assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY, merged[0].ActiveDirectoryState.Value)
		assert.Equal(t, "resource-1", merged[0].ResourceId)
	})
	t.Run("PrefersVCPStateHierarchy", func(t *testing.T) {
		cvpAds := []*models.ActiveDirectoryV1beta{
			{
				ActiveDirectoryID:    "ad-1",
				ResourceID:           nillable.GetStringPtr("resource-1"),
				Username:             nillable.GetStringPtr("user"),
				Password:             nillable.GetStringPtr("pass"),
				Domain:               nillable.GetStringPtr("example.com"),
				DNS:                  nillable.GetStringPtr("10.0.0.1"),
				NetBIOS:              nillable.GetStringPtr("EXAMPLE"),
				ActiveDirectoryState: string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY),
			},
		}
		vcpADMap := map[string]*vcpModels.ActiveDirectory{
			"ad-1": {
				BaseModel: vcpModels.BaseModel{
					UUID:      "ad-1",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				AdName:                    "resource-1",
				Domain:                    "example.com",
				DNS:                       "10.0.0.1",
				NetBIOS:                   "EXAMPLE",
				State:                     "UPDATING",
				ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{},
			},
		}

		merged := mergeActiveDirectoryResponses(cvpAds, vcpADMap)
		assert.Len(t, merged, 1)
		assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateUPDATING, merged[0].ActiveDirectoryState.Value)
		assert.Equal(t, "resource-1", merged[0].ResourceId)
	})
}

func TestV1betaCreateActiveDirectory_OnlyRequiredFields_Success(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	mockAD := &vcpModels.ActiveDirectory{
		BaseModel: vcpModels.BaseModel{
			UUID:      "ad-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		AdName:       "ad-name",
		Username:     "user",
		Domain:       "domain",
		DNS:          "dns",
		NetBIOS:      "netbios",
		StateDetails: "details",

		ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{},
	}
	mockJobID := "job-uuid"
	mockOrch := mockOrchestrator
	mockOrch.On("CreateActiveDirectory", mock.Anything, mock.Anything).Return(mockAD, mockJobID, nil)
	handler.Orchestrator = mockOrch

	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "ad-name",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "dns",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	op, ok := res.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.Contains(t, op.Name.Value, "job-uuid")
	assert.False(t, op.Done.Value)
	assert.NotNil(t, op.Response)
}

func TestV1betaCreateActiveDirectory_BadRequest(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	mockOrchestrator.On("CreateActiveDirectory", mock.Anything, mock.Anything).Return(nil, "", customerrors.NewUserInputValidationErr("bad request"))
	handler.Orchestrator = mockOrchestrator

	req := &gcpgenserver.ActiveDirectoryV1beta{}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	_, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryBadRequest)
	assert.True(t, ok)
}

func TestV1betaCreateActiveDirectory_InternalServerError(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	mockOrchestrator.On("CreateActiveDirectory", mock.Anything, mock.Anything).Return(nil, "", errors.New("internal error"))
	handler.Orchestrator = mockOrchestrator

	req := &gcpgenserver.ActiveDirectoryV1beta{}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	_, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryInternalServerError)
	assert.True(t, ok)
}

func TestV1betaCreateActiveDirectory_Conflict(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	mockOrchestrator.On("CreateActiveDirectory", mock.Anything, mock.Anything).Return(nil, "", customerrors.NewConflictErr("Active Directory with the given name already exists"))
	handler.Orchestrator = mockOrchestrator

	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "existing-ad-name",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	conflictRes, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), conflictRes.Code)
	assert.Contains(t, conflictRes.Message, "Active Directory with the given name already exists")
}

func TestV1betaCreateActiveDirectory_SyncModeEnabled(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client for direct call
	mockClient := active_directories.NewMockClientService(t)
	done := true
	mockResponse := &active_directories.V1betaCreateActiveDirectoryAccepted{
		Payload: &models.OperationV1beta{
			Name: "operations/test-op-id",
			Done: &done,
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(mockResponse, nil)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	op, ok := res.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.Contains(t, op.Name.Value, "operations/test-op-id")
	// When sync mode is enabled and CVP returns Done=true, response should have Done=true
	assert.True(t, op.Done.Value)
}

func TestV1betaCreateActiveDirectory_VCPMode_UsesOrchestrator(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	mockAD := &vcpModels.ActiveDirectory{
		BaseModel: vcpModels.BaseModel{
			UUID:      "ad-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		AdName:       "test-ad",
		Username:     "user",
		Domain:       "domain",
		DNS:          "192.168.1.1",
		NetBIOS:      "netbios",
		State:        "READY",
		StateDetails: "Active Directory is ready",
		ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
			SecurityOperators: []string{},
			BackupOperators:   []string{},
			Administrators:    []string{},
		},
	}
	mockOrchestrator.On("CreateActiveDirectory", mock.Anything, mock.Anything).Return(mockAD, "job-uuid", nil)
	handler.Orchestrator = mockOrchestrator

	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	op, ok := res.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.Contains(t, op.Name.Value, "job-uuid")
	// In VCP mode (CVP_HOST empty), uses orchestrator path and Done should be false
	assert.False(t, op.Done.Value)
	assert.NotNil(t, op.Response)
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPBadRequest(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client to return bad request error
	mockClient := active_directories.NewMockClientService(t)
	mockError := &active_directories.V1betaCreateActiveDirectoryBadRequest{
		Payload: &models.Error{
			Code:    400,
			Message: "invalid input",
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	badReq, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badReq.Code)
	assert.Contains(t, badReq.Message, "invalid input")
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPConflict(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client to return conflict error
	mockClient := active_directories.NewMockClientService(t)
	mockError := &active_directories.V1betaCreateActiveDirectoryConflict{
		Payload: &models.Error{
			Code:    409,
			Message: "Active Directory already exists",
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	conflict, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), conflict.Code)
	assert.Contains(t, conflict.Message, "Active Directory already exists")
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPNilResponse(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client to return nil payload
	mockClient := active_directories.NewMockClientService(t)
	mockResponse := &active_directories.V1betaCreateActiveDirectoryAccepted{
		Payload: nil,
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(mockResponse, nil)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	serverErr, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), serverErr.Code)
	assert.Contains(t, serverErr.Message, "unknown error during the create active directory")
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPUnauthorized(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client to return unauthorized error
	mockClient := active_directories.NewMockClientService(t)
	mockError := &active_directories.V1betaCreateActiveDirectoryUnauthorized{
		Payload: &models.Error{
			Code:    401,
			Message: "invalid token",
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	unauthorized, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryUnauthorized)
	assert.True(t, ok)
	assert.Equal(t, float64(401), unauthorized.Code)
	assert.Contains(t, unauthorized.Message, "invalid token")
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPForbidden(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client to return forbidden error
	mockClient := active_directories.NewMockClientService(t)
	mockError := &active_directories.V1betaCreateActiveDirectoryForbidden{
		Payload: &models.Error{
			Code:    403,
			Message: "access denied",
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	forbidden, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryForbidden)
	assert.True(t, ok)
	assert.Equal(t, float64(403), forbidden.Code)
	assert.Contains(t, forbidden.Message, "access denied")
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPTooManyRequests(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client to return too many requests error
	mockClient := active_directories.NewMockClientService(t)
	mockError := &active_directories.V1betaCreateActiveDirectoryTooManyRequests{
		Payload: &models.Error{
			Code:    429,
			Message: "rate limit exceeded",
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	tooMany, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryTooManyRequests)
	assert.True(t, ok)
	assert.Equal(t, float64(429), tooMany.Code)
	assert.Contains(t, tooMany.Message, "rate limit exceeded")
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPUnknownError(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client to return unknown error
	mockClient := active_directories.NewMockClientService(t)
	mockError := errors.New("connection timeout")
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	serverErr, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), serverErr.Code)
	assert.Contains(t, serverErr.Message, "connection timeout")
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPUnprocessableEntity(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockClient := active_directories.NewMockClientService(t)
	mockError := &active_directories.V1betaCreateActiveDirectoryUnprocessableEntity{
		Payload: &models.Error{
			Code:    422,
			Message: "DNS unreachable",
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	unprocessable, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryUnprocessableEntity)
	assert.True(t, ok)
	assert.Equal(t, float64(422), unprocessable.Code)
	assert.Equal(t, "DNS unreachable", unprocessable.Message)
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPInternalServerError(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockClient := active_directories.NewMockClientService(t)
	mockError := &active_directories.V1betaCreateActiveDirectoryInternalServerError{
		Payload: &models.Error{
			Code:    500,
			Message: "internal error",
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	serverErr, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), serverErr.Code)
	assert.Equal(t, "internal error", serverErr.Message)
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPResponseWithError(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client to return response with error field set
	mockClient := active_directories.NewMockClientService(t)
	done := false
	mockResponse := &active_directories.V1betaCreateActiveDirectoryAccepted{
		Payload: &models.OperationV1beta{
			Name: "operations/test-op-id",
			Done: &done,
			Error: &models.StatusV1Beta{
				Code:    500,
				Message: "internal error from SDE",
			},
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(mockResponse, nil)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	op, ok := res.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.Contains(t, op.Name.Value, "operations/test-op-id")
	assert.False(t, op.Done.Value)
	// Verify error field is set
	assert.True(t, op.Error.Set)
	assert.Equal(t, float64(500), op.Error.Value.Code.Value)
	assert.Equal(t, "internal error from SDE", op.Error.Value.Message.Value)
}

func TestV1betaCreateActiveDirectory_SyncMode_CVPResponseWithResponseField(t *testing.T) {
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock CVP client to return response with Response field set
	mockClient := active_directories.NewMockClientService(t)
	done := true
	mockResponse := &active_directories.V1betaCreateActiveDirectoryAccepted{
		Payload: &models.OperationV1beta{
			Name: "operations/test-op-id",
			Done: &done,
			Response: map[string]interface{}{
				"activeDirectoryId": "ad-123",
				"state":             "READY",
			},
		},
	}
	mockClient.On("V1betaCreateActiveDirectory", mock.Anything).Return(mockResponse, nil)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := handlerForADSyncCVPTests(t)
	req := &gcpgenserver.ActiveDirectoryV1beta{
		Username:   "user",
		ResourceId: "test-ad",
		Password:   "pass",
		Domain:     "domain",
		DNS:        "192.168.1.1",
		NetBIOS:    "netbios",
	}
	params := gcpgenserver.V1betaCreateActiveDirectoryParams{
		ProjectNumber: "pn",
		LocationId:    "loc",
	}
	res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)
	assert.NoError(t, err)
	op, ok := res.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.Contains(t, op.Name.Value, "operations/test-op-id")
	assert.True(t, op.Done.Value)
	// Verify response field is set and contains expected data
	assert.NotNil(t, op.Response)
	assert.Contains(t, string(op.Response), "ad-123")
	assert.Contains(t, string(op.Response), "READY")
}

func TestConvertToActiveDirectoryV1Beta(t *testing.T) {
	ad := &vcpModels.ActiveDirectory{
		BaseModel: vcpModels.BaseModel{
			UUID:      "uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		AdName:       "ad-name",
		Username:     "user",
		Domain:       "domain",
		DNS:          "dns",
		NetBIOS:      "netbios",
		StateDetails: "details",
		ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
			SecurityOperators:          []string{"secop"},
			BackupOperators:            []string{"backupop"},
			Administrators:             []string{"admin"},
			AesEncryption:              true,
			AllowLocalNFSUsersWithLdap: true,
			EncryptDCConnections:       true,
			LdapSigning:                true,
			OrganizationalUnit:         "ou",
			Site:                       "site",
			KdcIP:                      "kdcip",
			KdcHostname:                "kdchost",
		},
	}
	res := convertToActiveDirectoryV1Beta(ad)
	assert.Equal(t, "uuid", res.ActiveDirectoryId.Value)
	assert.Equal(t, "ad-name", res.ResourceId)
	assert.Equal(t, "user", res.Username) // Test that username is not masked
	assert.Equal(t, "domain", res.Domain)
	assert.Equal(t, "dns", res.DNS)
	assert.Equal(t, "netbios", res.NetBIOS)
	assert.Equal(t, "details", res.ActiveDirectoryStateDetails.Value)
	assert.Equal(t, []string{"secop"}, res.SecurityOperators)
	assert.Equal(t, []string{"backupop"}, res.BackupOperators)
	assert.Equal(t, []string{"admin"}, res.Administrators)
	assert.True(t, res.AesEncryption.Value)
	assert.True(t, res.AllowLocalNFSUsersWithLdap.Value)
	assert.True(t, res.EncryptDCConnections.Value)
	assert.True(t, res.LdapSigning.Value)
	assert.Equal(t, "ou", res.OrganizationalUnit.Value)
	assert.Equal(t, "site", res.Site.Value)
	assert.Equal(t, "kdcip", res.KdcIP.Value)
	assert.Equal(t, "kdchost", res.KdcHostname.Value)
}

func TestEncodeActiveDirectoryV1(t *testing.T) {
	ad := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryId: gcpgenserver.NewOptString("id"),
		ResourceId:        "rid",
	}
	raw, err := encodeActiveDirectoryV1(ad)
	assert.NoError(t, err)
	assert.NotNil(t, raw)
	// Should be valid JSON
	assert.True(t, json.Valid(raw))
}

func TestV1betaListActiveDirectories(t *testing.T) {
	// Set CVP_HOST to localhost:8009 to use CVS path
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	// Create a mock client
	mockClient := active_directories.NewMockClientService(t)

	// Define input parameters
	params := gcpgenserver.V1betaListActiveDirectoriesParams{
		LocationId:     "test-location",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
	}

	// Define mock response
	mockResponse := &active_directories.V1betaListActiveDirectoriesOK{
		Payload: &active_directories.V1betaListActiveDirectoriesOKBody{
			ActiveDirectories: []*models.ActiveDirectoryV1beta{
				{
					ActiveDirectoryID:           "ad-1",
					ResourceID:                  nillable.GetStringPtr("resource-1"),
					Username:                    nillable.GetStringPtr("user1"),
					Password:                    nillable.GetStringPtr("pass1"),
					Domain:                      nillable.GetStringPtr("domain1"),
					DNS:                         nillable.GetStringPtr("dns1"),
					NetBIOS:                     nillable.GetStringPtr("netbios1"),
					OrganizationalUnit:          new(string),
					Site:                        new(string),
					ActiveDirectoryState:        "ACTIVE",
					ActiveDirectoryStateDetails: "Details",
					LdapSigning:                 new(bool),
					AllowLocalNFSUsersWithLdap:  new(bool),
					EncryptDCConnections:        new(bool),
					SecurityOperators:           []string{"operator1"},
					BackupOperators:             []string{"backup1"},
					Administrators:              []string{"admin1"},
					AesEncryption:               new(bool),
				},
			},
		},
	}

	// Set up the mock client behavior
	mockClient.EXPECT().
		V1betaListActiveDirectories(mock.Anything).
		Return(mockResponse, nil)
	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.On("ListActiveDirectories", mock.Anything, params.ProjectNumber).Return([]*vcpModels.ActiveDirectory{}, nil)
	handler := Handler{Orchestrator: mockOrchestrator}
	// Call the method under test
	result, err := handler.V1betaListActiveDirectories(context.Background(), params)

	// Assertions
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 1, len(result.(*gcpgenserver.V1betaListActiveDirectoriesOK).ActiveDirectories))
	assert.Equal(t, "ad-1", result.(*gcpgenserver.V1betaListActiveDirectoriesOK).ActiveDirectories[0].ActiveDirectoryId.Value)
	mockOrchestrator.AssertExpectations(t)
}

func TestV1betaListActiveDirectories_XCorrelationIDForwarding(t *testing.T) {
	// Set CVP_HOST to localhost:8009 to use CVP path
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	// Create a mock client
	mockClient := active_directories.NewMockClientService(t)

	// Define input parameters with specific XCorrelationID
	expectedCorrelationID := "test-correlation-id-12345"
	params := gcpgenserver.V1betaListActiveDirectoriesParams{
		LocationId:     "test-location",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString(expectedCorrelationID),
	}

	// Define mock response
	mockResponse := &active_directories.V1betaListActiveDirectoriesOK{
		Payload: &active_directories.V1betaListActiveDirectoriesOKBody{
			ActiveDirectories: []*models.ActiveDirectoryV1beta{
				{
					ActiveDirectoryID:           "ad-1",
					ResourceID:                  nillable.GetStringPtr("resource-1"),
					Username:                    nillable.GetStringPtr("user1"),
					Password:                    nillable.GetStringPtr("pass1"),
					Domain:                      nillable.GetStringPtr("domain1"),
					DNS:                         nillable.GetStringPtr("dns1"),
					NetBIOS:                     nillable.GetStringPtr("netbios1"),
					ActiveDirectoryState:        "READY",
					ActiveDirectoryStateDetails: "Details",
				},
			},
		},
	}

	// Set up the mock client behavior with a matcher that verifies XCorrelationID
	mockClient.On("V1betaListActiveDirectories",
		mock.MatchedBy(func(p *active_directories.V1betaListActiveDirectoriesParams) bool {
			// Verify that XCorrelationID is properly set and matches expected value
			return p != nil &&
				p.XCorrelationID != nil &&
				*p.XCorrelationID == expectedCorrelationID &&
				p.LocationID == "test-location" &&
				p.ProjectNumber == "12345"
		}),
	).Return(mockResponse, nil)

	cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.On("ListActiveDirectories", mock.Anything, params.ProjectNumber).Return([]*vcpModels.ActiveDirectory{}, nil)
	handler := Handler{Orchestrator: mockOrchestrator}

	// Call the method under test
	result, err := handler.V1betaListActiveDirectories(context.Background(), params)

	// Assertions
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 1, len(result.(*gcpgenserver.V1betaListActiveDirectoriesOK).ActiveDirectories))
	assert.Equal(t, "ad-1", result.(*gcpgenserver.V1betaListActiveDirectoriesOK).ActiveDirectories[0].ActiveDirectoryId.Value)

	// Verify that the mock was called with the correct parameters (including XCorrelationID)
	mockClient.AssertExpectations(t)
	mockOrchestrator.AssertExpectations(t)
}

// V1betaDeleteActiveDirectory unittests
func TestV1betaDeleteActiveDirectory(t *testing.T) {
	t.Run("WhenDeleteActiveDirectorySuccess", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDeleteActiveDirectoryParams{
			LocationId:        "test-location",
			ProjectNumber:     "12345",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
			ActiveDirectoryId: "ad-1",
		}

		mockJobID := "job-uuid"
		// Set up the mock orchestrator behavior
		mockOrchestrator.On("DeleteActiveDirectory", mock.Anything, mock.Anything).Return(mockJobID, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDeleteActiveDirectory(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the operation name contains the job UUID
		assert.Contains(t, result.(*gcpgenserver.OperationV1beta).Name.Value, mockJobID)
		// Check if the operation done value is false (job is running)
		assert.False(t, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})

	t.Run("WhenDeleteActiveDirectoryFailsWithBadRequest", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDeleteActiveDirectoryParams{
			LocationId:        "test-location",
			ProjectNumber:     "12345",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
			ActiveDirectoryId: "ad-1",
		}

		// Set up the mock orchestrator behavior to return user input validation error
		mockOrchestrator.On("DeleteActiveDirectory", mock.Anything, mock.Anything).Return("", customerrors.NewUserInputValidationErr("bad request"))

		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDeleteActiveDirectory(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the response is BadRequest
		_, ok := result.(*gcpgenserver.V1betaDeleteActiveDirectoryBadRequest)
		assert.True(t, ok)
	})

	t.Run("WhenDeleteActiveDirectoryFailsWithConflict", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDeleteActiveDirectoryParams{
			LocationId:        "test-location",
			ProjectNumber:     "12345",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
			ActiveDirectoryId: "ad-1",
		}

		conflictErr := customerrors.NewConflictErr("Conflict error")
		// Set up the mock orchestrator behavior to return conflict error
		mockOrchestrator.On("DeleteActiveDirectory", mock.Anything, mock.Anything).Return("", conflictErr)

		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDeleteActiveDirectory(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the response is Conflict
		_, ok := result.(*gcpgenserver.V1betaDeleteActiveDirectoryConflict)
		assert.True(t, ok)
	})

	// Note: Unprocessable, Unauthorized, Forbidden tests removed as V1betaDeleteActiveDirectory only uses orchestrator path

	t.Run("WhenDeleteActiveDirectoryFailsWithTooManyRequests", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDeleteActiveDirectoryParams{
			LocationId:        "test-location",
			ProjectNumber:     "12345",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
			ActiveDirectoryId: "ad-1",
		}

		// Set up the mock orchestrator behavior to return a generic error
		mockOrchestrator.On("DeleteActiveDirectory", mock.Anything, mock.Anything).Return("", errors.New("internal error"))

		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDeleteActiveDirectory(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the response is InternalServerError
		_, ok := result.(*gcpgenserver.V1betaDeleteActiveDirectoryInternalServerError)
		assert.True(t, ok)
	})

	// Note: Default error test removed as V1betaDeleteActiveDirectory only uses orchestrator path

	t.Run("WhenDeleteActiveDirectoryAlreadyDeleted", func(t *testing.T) {
		// Create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDeleteActiveDirectoryParams{
			LocationId:        "test-location",
			ProjectNumber:     "12345",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
			ActiveDirectoryId: "ad-1",
		}

		// Set up the mock orchestrator behavior to return empty jobUUID (already deleted)
		mockOrchestrator.On("DeleteActiveDirectory", mock.Anything, mock.Anything).Return("", nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDeleteActiveDirectory(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the operation is marked as done
		assert.True(t, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})
}

// V1betaDescribeActiveDirectory unittests

// V1betaUpdateActiveDirectory unittests
func TestV1betaUpdateActiveDirectory(t *testing.T) {
	t.Run("WhenUpdateActiveDirectorySuccess", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		mockAD := &vcpModels.ActiveDirectory{
			BaseModel: vcpModels.BaseModel{
				UUID: "ad-uuid-1",
			},
			AdName:  "updated-ad",
			Domain:  "updated.example.com",
			DNS:     "8.8.8.8",
			NetBIOS: "UPDATED",
			State:   "UPDATING",
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				Description:                "Updated description",
				SecurityOperators:          []string{"updated-secop"},
				BackupOperators:            []string{"updated-backupop"},
				Administrators:             []string{"updated-admin"},
				AesEncryption:              true,
				AllowLocalNFSUsersWithLdap: false,
				EncryptDCConnections:       true,
				LdapSigning:                false,
				OrganizationalUnit:         "updated-ou",
				Site:                       "updated-site",
				KdcIP:                      "updated-kdcip",
				KdcHostname:                "updated-kdchost",
			},
		}
		mockJobID := "update-job-uuid"

		mockOrchestrator.On("UpdateActiveDirectory", mock.Anything, mock.Anything).Return(mockAD, mockJobID, nil)

		req := &gcpgenserver.ActiveDirectoryUpdateV1beta{
			Description:                gcpgenserver.NewOptString("Updated description"),
			SecurityOperators:          []string{"updated-secop"},
			BackupOperators:            []string{"updated-backupop"},
			Administrators:             []string{"updated-admin"},
			AesEncryption:              gcpgenserver.NewOptBool(true),
			AllowLocalNFSUsersWithLdap: gcpgenserver.NewOptBool(false),
			EncryptDCConnections:       gcpgenserver.NewOptBool(true),
			LdapSigning:                gcpgenserver.NewOptBool(false),
			OrganizationalUnit:         gcpgenserver.NewOptString("updated-ou"),
			Site:                       gcpgenserver.NewOptString("updated-site"),
			KdcIP:                      gcpgenserver.NewOptString("updated-kdcip"),
			KdcHostname:                gcpgenserver.NewOptString("updated-kdchost"),
		}

		params := gcpgenserver.V1betaUpdateActiveDirectoryParams{
			ProjectNumber:     "12345",
			LocationId:        "us-central1",
			ActiveDirectoryId: "ad-uuid-1",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
		}

		res, err := handler.V1betaUpdateActiveDirectory(context.Background(), req, params)

		assert.NoError(t, err)
		op, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
		assert.Contains(t, op.Name.Value, "update-job-uuid")
		assert.False(t, op.Done.Value)
		assert.NotNil(t, op.Response)

		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUpdateActiveDirectoryFailsWithBadRequest", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		mockOrchestrator.On("UpdateActiveDirectory", mock.Anything, mock.Anything).Return(nil, "", customerrors.NewUserInputValidationErr("invalid input"))

		req := &gcpgenserver.ActiveDirectoryUpdateV1beta{
			Description: gcpgenserver.NewOptString(""),
		}

		params := gcpgenserver.V1betaUpdateActiveDirectoryParams{
			ProjectNumber:     "12345",
			LocationId:        "us-central1",
			ActiveDirectoryId: "ad-uuid-1",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
		}

		res, err := handler.V1betaUpdateActiveDirectory(context.Background(), req, params)

		assert.NoError(t, err)
		badReq, ok := res.(*gcpgenserver.V1betaUpdateActiveDirectoryBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Contains(t, badReq.Message, "invalid input")

		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUpdateActiveDirectoryFailsWithInternalServerError", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		mockOrchestrator.On("UpdateActiveDirectory", mock.Anything, mock.Anything).Return(nil, "", errors.New("internal server error"))

		req := &gcpgenserver.ActiveDirectoryUpdateV1beta{
			Description: gcpgenserver.NewOptString("Test description"),
		}

		params := gcpgenserver.V1betaUpdateActiveDirectoryParams{
			ProjectNumber:     "12345",
			LocationId:        "us-central1",
			ActiveDirectoryId: "ad-uuid-1",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
		}

		res, err := handler.V1betaUpdateActiveDirectory(context.Background(), req, params)

		assert.NoError(t, err)
		serverErr, ok := res.(*gcpgenserver.V1betaUpdateActiveDirectoryInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), serverErr.Code)
		assert.Contains(t, serverErr.Message, "internal server error")

		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUpdateActiveDirectoryOnlyRequiredFields", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		mockAD := &vcpModels.ActiveDirectory{
			BaseModel: vcpModels.BaseModel{
				UUID: "ad-uuid-1",
			},
			AdName:                    "minimal-ad",
			Domain:                    "minimal.example.com",
			DNS:                       "8.8.8.8",
			NetBIOS:                   "MINIMAL",
			State:                     "UPDATING",
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{},
		}
		mockJobID := "minimal-update-job-uuid"

		mockOrchestrator.On("UpdateActiveDirectory", mock.Anything, mock.Anything).Return(mockAD, mockJobID, nil)

		// Minimal request with only one optional field
		req := &gcpgenserver.ActiveDirectoryUpdateV1beta{
			Description: gcpgenserver.NewOptString("Minimal update"),
		}

		params := gcpgenserver.V1betaUpdateActiveDirectoryParams{
			ProjectNumber:     "12345",
			LocationId:        "us-central1",
			ActiveDirectoryId: "ad-uuid-1",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
		}

		res, err := handler.V1betaUpdateActiveDirectory(context.Background(), req, params)

		assert.NoError(t, err)
		op, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
		assert.Contains(t, op.Name.Value, "minimal-update-job-uuid")
		assert.False(t, op.Done.Value)
		assert.NotNil(t, op.Response)

		mockOrchestrator.AssertExpectations(t)
	})
}

// V1betaGetMultipleActiveDirectories unittests
func TestV1betaGetMultipleActiveDirectories(t *testing.T) {
	t.Run("WhenGetMultipleActiveDirectoriesSuccess", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()
		// Create a mock client
		mockClient := active_directories.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"AD0"},
		}

		ads := make([]*models.ActiveDirectoryV1beta, 0)
		resourceID := "AD0"
		dns := "10.20.2.3"
		domainName := "domain1.com"
		netBios := "domain1"
		userName := "user1"
		password := "pass1"
		description := "Test AD"

		ads = append(ads, &models.ActiveDirectoryV1beta{
			ActiveDirectoryID: "AD0",
			ResourceID:        &resourceID,
			DNS:               &dns,
			Domain:            &domainName,
			NetBIOS:           &netBios,
			Username:          &userName,
			Password:          &password,
			Description:       &description,
		})

		// Define mock response
		mockResponse := &active_directories.V1betaGetMultipleActiveDirectoriesOK{
			Payload: &active_directories.V1betaGetMultipleActiveDirectoriesOKBody{
				ActiveDirectories: ads,
			},
		}

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleActiveDirectories(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.On("GetMultipleActiveDirectories", mock.Anything, req.ActiveDirectoryUuids).Return([]*vcpModels.ActiveDirectory{
			{
				BaseModel: vcpModels.BaseModel{
					UUID:      "AD0",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				AdName:                    resourceID,
				Domain:                    domainName,
				DNS:                       dns,
				NetBIOS:                   netBios,
				State:                     "UPDATING",
				ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{},
			},
		}, nil)
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		resp := result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesOK)
		assert.Equal(t, "AD0", resp.ActiveDirectories[0].ActiveDirectoryId.Value)
		assert.Equal(t, 1, len(resp.ActiveDirectories))
		assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateUPDATING, resp.ActiveDirectories[0].ActiveDirectoryState.Value)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenGetMultipleActiveDirectoriesFailsWithBadRequest", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()
		// Create a mock client
		mockClient := active_directories.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"AD0"},
		}

		// Define mock error
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &active_directories.V1betaGetMultipleActiveDirectoriesBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleActiveDirectories(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesBadRequest).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesBadRequest).Message)
	})
	t.Run("WhenGetMultipleActiveDirectoriesFailsWithNotFound", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()
		// Create a mock client
		mockClient := active_directories.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"AD0"},
		}

		// Define mock error
		errorCode := float64(404)
		errorMessage := "Bad Request"
		mockError := &active_directories.V1betaGetMultipleActiveDirectoriesNotFound{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleActiveDirectories(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesNotFound).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesNotFound).Message)
	})

	t.Run("WhenGetMultipleActiveDirectoriesFailsWithUnauthorized", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()
		// Create a mock client
		mockClient := active_directories.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"AD0"},
		}

		// Define mock error
		errorMessage := "Unauthorized error"
		errorCode := float64(401)
		mockError := &active_directories.V1betaGetMultipleActiveDirectoriesUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleActiveDirectories(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesUnauthorized).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesUnauthorized).Message)
	})

	t.Run("WhenGetMultipleActiveDirectoriesFailsWithForbidden", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()
		// Create a mock client
		mockClient := active_directories.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"AD0"},
		}

		// Define mock error
		errorMessage := "Forbidden error"
		errorCode := float64(403)
		mockError := &active_directories.V1betaGetMultipleActiveDirectoriesForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleActiveDirectories(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesForbidden).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesForbidden).Message)
	})

	t.Run("WhenGetMultipleActiveDirectoriesFailsWithTooManyRequests", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()
		// Create a mock client
		mockClient := active_directories.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"AD0"},
		}

		// Define mock error
		errorMessage := "Too many requests error"
		errorCode := float64(401)
		mockError := &active_directories.V1betaGetMultipleActiveDirectoriesTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleActiveDirectories(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesTooManyRequests).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesTooManyRequests).Message)
	})

	t.Run("WhenGetMultipleActiveDirectoriesFailsWithDefault", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()
		// Create a mock client
		mockClient := active_directories.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"AD0"},
		}

		// Define mock error
		errorMessage := "default error"
		errorCode := float64(500)
		mockError := &active_directories.V1betaGetMultipleActiveDirectoriesDefault{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleActiveDirectories(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesInternalServerError).Code)
	})

	t.Run("WhenGetMultipleActiveDirectoriesFailsWithUnknownError", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVS path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()
		// Create a mock client
		mockClient := active_directories.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"AD0"},
		}
		// Define mock error
		errorMessage := "unknown error during the get multiple active directories"
		errorCode := float64(500)
		mockError := &active_directories.V1betaGetMultipleActiveDirectoriesInternalServerError{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleActiveDirectories(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesInternalServerError).Message)
	})

	t.Run("WhenGetMultipleActiveDirectories_VCPPath_Success", func(t *testing.T) {
		// Set CVP_HOST to empty to use VCP path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"ad-uuid-1", "ad-uuid-2"},
		}

		// Mock orchestrator response
		mockADs := []*vcpModels.ActiveDirectory{
			{
				BaseModel: vcpModels.BaseModel{
					UUID:      "ad-uuid-1",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				AdName:   "ad-name-1",
				Username: "user1",
				Password: "pass1",
				Domain:   "domain1.com",
				DNS:      "8.8.8.8",
				NetBIOS:  "NETBIOS1",
				State:    "READY",
				ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
					SecurityOperators: []string{"sec1"},
					BackupOperators:   []string{"backup1"},
					Administrators:    []string{"admin1"},
				},
			},
			{
				BaseModel: vcpModels.BaseModel{
					UUID:      "ad-uuid-2",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				AdName:   "ad-name-2",
				Username: "user2",
				Password: "pass2",
				Domain:   "domain2.com",
				DNS:      "8.8.4.4",
				NetBIOS:  "NETBIOS2",
				State:    "CREATING",
				ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
					SecurityOperators: []string{"sec2"},
					BackupOperators:   []string{"backup2"},
					Administrators:    []string{"admin2"},
				},
			},
		}

		mockOrchestrator.On("GetMultipleActiveDirectories", mock.Anything, req.ActiveDirectoryUuids).Return(mockADs, nil)

		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		okResult, ok := result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesOK)
		assert.True(t, ok)
		assert.Len(t, okResult.ActiveDirectories, 2)
		assert.Equal(t, "ad-uuid-1", okResult.ActiveDirectories[0].ActiveDirectoryId.Value)
		assert.Equal(t, "ad-name-1", okResult.ActiveDirectories[0].ResourceId)
		assert.Equal(t, "user1", okResult.ActiveDirectories[0].Username) // Verify username is not masked
		assert.Equal(t, "user2", okResult.ActiveDirectories[1].Username) // Verify username is not masked
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenGetMultipleActiveDirectories_VCPPath_OrchestratorError", func(t *testing.T) {
		// Set CVP_HOST to empty to use VCP path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"ad-uuid-1"},
		}

		mockOrchestrator.On("GetMultipleActiveDirectories", mock.Anything, req.ActiveDirectoryUuids).Return(nil, errors.New("orchestrator error"))

		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		errResult, ok := result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), errResult.Code)
		assert.Contains(t, errResult.Message, "orchestrator error")
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenGetMultipleActiveDirectories_XCorrelationIDForwarding", func(t *testing.T) {
		// Set CVP_HOST to localhost:8009 to use CVP path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()

		// Create a mock client
		mockClient := active_directories.NewMockClientService(t)

		// Define input parameters with specific XCorrelationID
		expectedCorrelationID := "get-multiple-correlation-id-67890"
		params := gcpgenserver.V1betaGetMultipleActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString(expectedCorrelationID),
		}

		// Define request
		req := &gcpgenserver.ActiveDirectoryIdListV1beta{
			ActiveDirectoryUuids: []string{"ad-1", "ad-2"},
		}

		// Define mock response
		dns := "10.20.2.2"
		domainName := "test-domain.com"
		netBios := "test-domain"
		userName := "test-user"
		password := "test-password"
		description := "test description"

		mockResponse := &active_directories.V1betaGetMultipleActiveDirectoriesOK{
			Payload: &active_directories.V1betaGetMultipleActiveDirectoriesOKBody{
				ActiveDirectories: []*models.ActiveDirectoryV1beta{
					{
						ActiveDirectoryID:    "ad-1",
						ResourceID:           nillable.GetStringPtr("resource-id-1"),
						DNS:                  &dns,
						Domain:               &domainName,
						NetBIOS:              &netBios,
						Username:             &userName,
						Password:             &password,
						Description:          &description,
						ActiveDirectoryState: "READY",
					},
				},
			},
		}

		// Set up the mock client behavior with a matcher that verifies XCorrelationID
		mockClient.On("V1betaGetMultipleActiveDirectories",
			mock.MatchedBy(func(p *active_directories.V1betaGetMultipleActiveDirectoriesParams) bool {
				// Verify that XCorrelationID is properly set and matches expected value
				return p != nil &&
					p.XCorrelationID != nil &&
					*p.XCorrelationID == expectedCorrelationID &&
					p.LocationID == "test-location" &&
					p.ProjectNumber == "12345" &&
					p.Body != nil &&
					len(p.Body.ActiveDirectoryUUIDs) == 2
			}),
		).Return(mockResponse, nil)

		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Set up mock orchestrator for VCP data merging
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.On("GetMultipleActiveDirectories", mock.Anything, req.ActiveDirectoryUuids).Return([]*vcpModels.ActiveDirectory{}, nil)
		handler := Handler{Orchestrator: mockOrchestrator}

		// Call the method under test
		result, err := handler.V1betaGetMultipleActiveDirectories(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		okResult, ok := result.(*gcpgenserver.V1betaGetMultipleActiveDirectoriesOK)
		assert.True(t, ok)
		assert.Equal(t, 1, len(okResult.ActiveDirectories))
		assert.Equal(t, "ad-1", okResult.ActiveDirectories[0].ActiveDirectoryId.Value)

		// Verify that the mock was called with the correct parameters (including XCorrelationID)
		mockClient.AssertExpectations(t)
		mockOrchestrator.AssertExpectations(t)
	})
}

func TestV1betaDescribeActiveDirectory_VCPPath(t *testing.T) {
	t.Run("WhenDescribeActiveDirectory_VCPPath_Success", func(t *testing.T) {
		// Set CVP_HOST to empty to use VCP path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDescribeActiveDirectoryParams{
			LocationId:        "test-location",
			ProjectNumber:     "12345",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
			ActiveDirectoryId: "ad-uuid-1",
		}

		mockAD := &vcpModels.ActiveDirectory{
			BaseModel: vcpModels.BaseModel{
				UUID:      "ad-uuid-1",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			AdName:       "test-ad",
			Username:     "testuser",
			Password:     "testpass",
			Domain:       "example.com",
			DNS:          "8.8.8.8",
			NetBIOS:      "EXAMPLE",
			State:        "READY",
			StateDetails: "Active Directory is ready",
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				OrganizationalUnit:   "OU=Test",
				Site:                 "Default-Site",
				SecurityOperators:    []string{"sec-op"},
				BackupOperators:      []string{"backup-op"},
				Administrators:       []string{"admin"},
				KdcIP:                "1.2.3.4",
				AesEncryption:        true,
				EncryptDCConnections: true,
				LdapSigning:          false,
			},
		}

		mockOrchestrator.On("GetActiveDirectory", mock.Anything, mock.MatchedBy(func(p *common.GetADParams) bool {
			return p.UUID == "ad-uuid-1" &&
				p.ProjectNumber == "12345" &&
				p.LocationID == "test-location" &&
				p.CorrelationID == "test-correlation-id"
		})).Return(mockAD, nil)

		result, err := handler.V1betaDescribeActiveDirectory(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		adResult, ok := result.(*gcpgenserver.ActiveDirectoryV1beta)
		assert.True(t, ok)
		assert.Equal(t, "ad-uuid-1", adResult.ActiveDirectoryId.Value)
		assert.Equal(t, "test-ad", adResult.ResourceId)
		assert.Equal(t, "testuser", adResult.Username) // Verify username is not masked
		assert.Equal(t, "example.com", adResult.Domain)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenDescribeActiveDirectory_VCPPath_NotFound", func(t *testing.T) {
		// Set CVP_HOST to empty to use VCP path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDescribeActiveDirectoryParams{
			LocationId:        "test-location",
			ProjectNumber:     "12345",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
			ActiveDirectoryId: "non-existent",
		}

		mockOrchestrator.On("GetActiveDirectory", mock.Anything, mock.MatchedBy(func(p *common.GetADParams) bool {
			return p.UUID == "non-existent" &&
				p.ProjectNumber == "12345" &&
				p.LocationID == "test-location" &&
				p.CorrelationID == "test-correlation-id"
		})).Return(nil, customerrors.NewNotFoundErr("AD", nil))

		result, err := handler.V1betaDescribeActiveDirectory(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(404), result.(*gcpgenserver.V1betaDescribeActiveDirectoryNotFound).Code)
		assert.Equal(t, "AD not found", result.(*gcpgenserver.V1betaDescribeActiveDirectoryNotFound).Message)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenDescribeActiveDirectory_VCPPath_InternalServerError", func(t *testing.T) {
		// Set CVP_HOST to empty to use VCP path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDescribeActiveDirectoryParams{
			LocationId:        "test-location",
			ProjectNumber:     "12345",
			XCorrelationID:    gcpgenserver.NewOptString("test-correlation-id"),
			ActiveDirectoryId: "error-ad",
		}

		mockOrchestrator.On("GetActiveDirectory", mock.Anything, mock.MatchedBy(func(p *common.GetADParams) bool {
			return p.UUID == "error-ad"
		})).Return(nil, errors.New("database connection error"))

		result, err := handler.V1betaDescribeActiveDirectory(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		internalErr, ok := result.(*gcpgenserver.V1betaDescribeActiveDirectoryInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, "internal error during the describe active directory", internalErr.Message)
		mockOrchestrator.AssertExpectations(t)
	})
}

func TestV1betaListActiveDirectories_VCPPath(t *testing.T) {
	t.Run("WhenListActiveDirectories_VCPPath_Success", func(t *testing.T) {
		// Set CVP_HOST to empty to use VCP path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaListActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockADs := []*vcpModels.ActiveDirectory{
			{
				BaseModel: vcpModels.BaseModel{
					UUID:      "ad-1",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				AdName:   "ad-name-1",
				Username: "user1",
				Password: "pass1",
				Domain:   "domain1.com",
				DNS:      "8.8.8.8",
				NetBIOS:  "NET1",
				State:    "READY",
				ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
					SecurityOperators: []string{"sec1"},
					BackupOperators:   []string{"backup1"},
					Administrators:    []string{"admin1"},
				},
			},
			{
				BaseModel: vcpModels.BaseModel{
					UUID:      "ad-2",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				AdName:   "ad-name-2",
				Username: "user2",
				Password: "pass2",
				Domain:   "domain2.com",
				DNS:      "8.8.4.4",
				NetBIOS:  "NET2",
				State:    "CREATING",
				ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
					SecurityOperators: []string{"sec2"},
					BackupOperators:   []string{"backup2"},
					Administrators:    []string{"admin2"},
				},
			},
		}

		mockOrchestrator.On("ListActiveDirectories", mock.Anything, "12345").Return(mockADs, nil)

		result, err := handler.V1betaListActiveDirectories(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		okResult, ok := result.(*gcpgenserver.V1betaListActiveDirectoriesOK)
		assert.True(t, ok)
		assert.Len(t, okResult.ActiveDirectories, 2)
		assert.Equal(t, "ad-1", okResult.ActiveDirectories[0].ActiveDirectoryId.Value)
		assert.Equal(t, "ad-name-1", okResult.ActiveDirectories[0].ResourceId)
		assert.Equal(t, "user1", okResult.ActiveDirectories[0].Username) // Verify username is not masked
		assert.Equal(t, "ad-2", okResult.ActiveDirectories[1].ActiveDirectoryId.Value)
		assert.Equal(t, "user2", okResult.ActiveDirectories[1].Username) // Verify username is not masked
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenListActiveDirectories_VCPPath_EmptyList", func(t *testing.T) {
		// Set CVP_HOST to empty to use VCP path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaListActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator.On("ListActiveDirectories", mock.Anything, "12345").Return([]*vcpModels.ActiveDirectory{}, nil)

		result, err := handler.V1betaListActiveDirectories(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		okResult, ok := result.(*gcpgenserver.V1betaListActiveDirectoriesOK)
		assert.True(t, ok)
		assert.Len(t, okResult.ActiveDirectories, 0)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenListActiveDirectories_VCPPath_OrchestratorError", func(t *testing.T) {
		// Set CVP_HOST to empty to use VCP path
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaListActiveDirectoriesParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator.On("ListActiveDirectories", mock.Anything, "12345").Return(nil, errors.New("orchestrator error"))

		result, err := handler.V1betaListActiveDirectories(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		errResult, ok := result.(*gcpgenserver.V1betaListActiveDirectoriesInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), errResult.Code)
		assert.Contains(t, errResult.Message, "orchestrator error")
		mockOrchestrator.AssertExpectations(t)
	})
}

func TestConvertOrchestratorActiveDirectoryToV1Beta(t *testing.T) {
	t.Run("WhenConvertingWithAllFields", func(t *testing.T) {
		now := time.Now()
		deletedAt := time.Now().Add(time.Hour)
		ad := &vcpModels.ActiveDirectory{
			BaseModel: vcpModels.BaseModel{
				UUID:      "test-uuid",
				CreatedAt: now,
				UpdatedAt: now,
				DeletedAt: &deletedAt,
			},
			AdName:       "test-ad",
			Username:     "testuser",
			Password:     "testpass",
			Domain:       "example.com",
			DNS:          "8.8.8.8",
			NetBIOS:      "EXAMPLE",
			State:        "READY",
			StateDetails: "Ready",
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				SecurityOperators:          []string{"sec1", "sec2"},
				BackupOperators:            []string{"backup1"},
				Administrators:             []string{"admin1", "admin2"},
				AesEncryption:              true,
				AllowLocalNFSUsersWithLdap: true,
				EncryptDCConnections:       true,
				LdapSigning:                true,
				OrganizationalUnit:         "",
				Site:                       "example",
				KdcIP:                      "10.0.0.0",
				KdcHostname:                "",
				Description:                "",
			},
		}

		result := convertOrchestratorActiveDirectoryToV1Beta(ad)

		assert.Equal(t, "test-uuid", result.ActiveDirectoryId.Value)
		assert.Equal(t, "test-ad", result.ResourceId)
		assert.Equal(t, "example.com", result.Domain)
		assert.Equal(t, "8.8.8.8", result.DNS)
		assert.Equal(t, "EXAMPLE", result.NetBIOS)
		assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY, result.ActiveDirectoryState.Value)
		assert.Equal(t, "Ready", result.ActiveDirectoryStateDetails.Value)
		assert.Equal(t, []string{"sec1", "sec2"}, result.SecurityOperators)
		assert.Equal(t, []string{"backup1"}, result.BackupOperators)
		assert.Equal(t, []string{"admin1", "admin2"}, result.Administrators)
		assert.True(t, result.DeletedAt.IsSet())
	})

	t.Run("WhenConvertingWithNilAttributes", func(t *testing.T) {
		ad := &vcpModels.ActiveDirectory{
			BaseModel: vcpModels.BaseModel{
				UUID: "test-uuid",
			},
			AdName:                    "test-ad",
			State:                     "CREATING",
			ActiveDirectoryAttributes: nil,
		}

		result := convertOrchestratorActiveDirectoryToV1Beta(ad)

		assert.Equal(t, "test-uuid", result.ActiveDirectoryId.Value)
		assert.Equal(t, "test-ad", result.ResourceId)
		assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING, result.ActiveDirectoryState.Value)
		assert.Empty(t, result.SecurityOperators)
		assert.Empty(t, result.BackupOperators)
		assert.Empty(t, result.Administrators)
	})

	t.Run("WhenConvertingDifferentStates", func(t *testing.T) {
		stateTests := []struct {
			inputState    string
			expectedState gcpgenserver.ActiveDirectoryV1betaActiveDirectoryState
		}{
			{"CREATING", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING},
			{"READY", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY},
			{"UPDATING", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateUPDATING},
			{"IN_USE", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateINUSE},
			{"DELETING", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateDELETING},
			{"ERROR", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR},
			{"DIAGNOSING", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateDIAGNOSING},
			{"UNKNOWN", gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY}, // Default
		}

		for _, tt := range stateTests {
			ad := &vcpModels.ActiveDirectory{
				BaseModel:                 vcpModels.BaseModel{UUID: "test"},
				AdName:                    "test",
				State:                     tt.inputState,
				ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{},
			}

			result := convertOrchestratorActiveDirectoryToV1Beta(ad)
			assert.Equal(t, tt.expectedState, result.ActiveDirectoryState.Value, "Failed for state: %s", tt.inputState)
		}
	})

	// Positive Test Cases
	t.Run("Positive_AllFieldsPopulated", func(t *testing.T) {
		ad := &vcpModels.ActiveDirectory{
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				SecurityOperators:          []string{"sec1"},
				BackupOperators:            []string{"bak1"},
				Administrators:             []string{"adm1"},
				AesEncryption:              true,
				AllowLocalNFSUsersWithLdap: true,
				EncryptDCConnections:       true,
				LdapSigning:                true,
				OrganizationalUnit:         "OU=Test",
				Site:                       "TestSite",
				KdcIP:                      "1.2.3.4",
				KdcHostname:                "kdc.test.com",
				Description:                "Test Description",
			},
		}
		result := convertOrchestratorActiveDirectoryToV1Beta(ad)
		assert.Equal(t, []string{"sec1"}, result.SecurityOperators)
		assert.Equal(t, []string{"bak1"}, result.BackupOperators)
		assert.Equal(t, []string{"adm1"}, result.Administrators)
		assert.True(t, result.AesEncryption.Value)
		assert.True(t, result.AllowLocalNFSUsersWithLdap.Value)
		assert.True(t, result.EncryptDCConnections.Value)
		assert.True(t, result.LdapSigning.Value)
		assert.Equal(t, "OU=Test", result.OrganizationalUnit.Value)
		assert.Equal(t, "TestSite", result.Site.Value)
		assert.Equal(t, "1.2.3.4", result.KdcIP.Value)
		assert.Equal(t, "kdc.test.com", result.KdcHostname.Value)
		assert.Equal(t, "Test Description", result.Description.Value)
	})

	t.Run("Positive_OptionalFieldsEmpty", func(t *testing.T) {
		ad := &vcpModels.ActiveDirectory{
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				SecurityOperators: []string{},
				BackupOperators:   []string{},
				Administrators:    []string{},
			},
		}
		result := convertOrchestratorActiveDirectoryToV1Beta(ad)
		assert.Empty(t, result.SecurityOperators)
		assert.Empty(t, result.BackupOperators)
		assert.Empty(t, result.Administrators)
		assert.False(t, result.AesEncryption.Value)
		assert.False(t, result.AllowLocalNFSUsersWithLdap.Value)
		assert.False(t, result.EncryptDCConnections.Value)
		assert.False(t, result.LdapSigning.Value)
		assert.Empty(t, result.OrganizationalUnit.Value)
		assert.Empty(t, result.Site.Value)
		assert.Empty(t, result.KdcIP.Value)
		assert.Empty(t, result.KdcHostname.Value)
		assert.Empty(t, result.Description.Value)
	})

	t.Run("Positive_MultipleOperatorsAndAdmins", func(t *testing.T) {
		ad := &vcpModels.ActiveDirectory{
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				SecurityOperators: []string{"sec1", "sec2"},
				BackupOperators:   []string{"bak1", "bak2"},
				Administrators:    []string{"adm1", "adm2"},
			},
		}
		result := convertOrchestratorActiveDirectoryToV1Beta(ad)
		assert.Equal(t, []string{"sec1", "sec2"}, result.SecurityOperators)
		assert.Equal(t, []string{"bak1", "bak2"}, result.BackupOperators)
		assert.Equal(t, []string{"adm1", "adm2"}, result.Administrators)
	})

	// Negative Test-cases
	t.Run("Negative_NilSlices", func(t *testing.T) {
		ad := &vcpModels.ActiveDirectory{
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				SecurityOperators: nil,
				BackupOperators:   nil,
				Administrators:    nil,
			},
		}
		result := convertOrchestratorActiveDirectoryToV1Beta(ad)
		assert.NotNil(t, result.SecurityOperators)
		assert.Empty(t, result.SecurityOperators)
		assert.NotNil(t, result.BackupOperators)
		assert.Empty(t, result.BackupOperators)
		assert.NotNil(t, result.Administrators)
		assert.Empty(t, result.Administrators)
	})

	t.Run("Negative_WhitespaceAndSpecialChars", func(t *testing.T) {
		ad := &vcpModels.ActiveDirectory{
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				OrganizationalUnit: " OU=Test ",
				Site:               " Test Site ",
				Description:        " \tTest\nDescription ",
			},
		}
		result := convertOrchestratorActiveDirectoryToV1Beta(ad)
		assert.Equal(t, " OU=Test ", result.OrganizationalUnit.Value)
		assert.Equal(t, " Test Site ", result.Site.Value)
		assert.Equal(t, " \tTest\nDescription ", result.Description.Value)
	})

	t.Run("Negative_BooleanDefaults", func(t *testing.T) {
		ad := &vcpModels.ActiveDirectory{
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{},
		}
		result := convertOrchestratorActiveDirectoryToV1Beta(ad)
		assert.False(t, result.AesEncryption.Value)
		assert.False(t, result.AllowLocalNFSUsersWithLdap.Value)
		assert.False(t, result.EncryptDCConnections.Value)
		assert.False(t, result.LdapSigning.Value)
	})

	t.Run("UsernameIsNotMasked", func(t *testing.T) {
		ad := &vcpModels.ActiveDirectory{
			BaseModel: vcpModels.BaseModel{
				UUID: "test-uuid",
			},
			AdName:                    "test-ad",
			Username:                  "actualUsername",
			Password:                  "actualPassword",
			State:                     "READY",
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{},
		}
		result := convertOrchestratorActiveDirectoryToV1Beta(ad)
		// Verify that username is NOT masked and shows actual value
		assert.Equal(t, "actualUsername", result.Username)
		// Password should still be masked with log.Secret wrapper
		assert.NotEqual(t, "actualPassword", result.Password)
	})
}

func TestV1betaCreateActiveDirectory_PasswordEncryption(t *testing.T) {
	t.Run("Successfully encrypts password before creating AD", func(t *testing.T) {
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		// Mock successful AD creation
		mockAD := &vcpModels.ActiveDirectory{
			BaseModel: vcpModels.BaseModel{UUID: "ad-uuid"},
			AdName:    "test-ad",
			Username:  "testuser",
			Domain:    "test.com",
			DNS:       "192.168.1.1",
			NetBIOS:   "TESTNET",
			State:     "READY",
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				Description: "Test AD",
			},
		}
		mockJobID := "job-123"

		// Capture the params passed to CreateActiveDirectory to verify password is encrypted
		mockOrchestrator.On("CreateActiveDirectory", mock.Anything, mock.MatchedBy(func(params interface{}) bool {
			// Verify that the password passed is not the plain text password
			// The encrypted password should be different from the original
			return true
		})).Return(mockAD, mockJobID, nil)

		req := &gcpgenserver.ActiveDirectoryV1beta{
			Username:   "testuser",
			ResourceId: "test-ad",
			Password:   "plaintext-password",
			Domain:     "test.com",
			DNS:        "192.168.1.1",
			NetBIOS:    "TESTNET",
		}
		params := gcpgenserver.V1betaCreateActiveDirectoryParams{
			ProjectNumber: "project-123",
			LocationId:    "us-west1",
		}

		res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, res)
		op, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
		assert.Contains(t, op.Name.Value, mockJobID)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("Returns error when password encryption fails", func(t *testing.T) {
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		// Override the EncryptPassword function to simulate encryption failure
		originalEncryptPassword := utils.EncryptPassword
		utils.EncryptPassword = func(password log.Secret) (*string, error) {
			return nil, errors.New("encryption failed")
		}
		defer func() { utils.EncryptPassword = originalEncryptPassword }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.ActiveDirectoryV1beta{
			Username:   "testuser",
			ResourceId: "test-ad",
			Password:   "plaintext-password",
			Domain:     "test.com",
			DNS:        "192.168.1.1",
			NetBIOS:    "TESTNET",
		}
		params := gcpgenserver.V1betaCreateActiveDirectoryParams{
			ProjectNumber: "project-123",
			LocationId:    "us-west1",
		}

		res, err := handler.V1betaCreateActiveDirectory(context.Background(), req, params)

		assert.NoError(t, err) // HTTP error is returned as response, not Go error
		errResp, ok := res.(*gcpgenserver.V1betaCreateActiveDirectoryInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), errResp.Code)
		assert.Contains(t, errResp.Message, "encryption failed")
	})
}

func TestV1betaUpdateActiveDirectory_PasswordEncryption(t *testing.T) {
	t.Run("Successfully encrypts password when updating AD", func(t *testing.T) {
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		mockAD := &vcpModels.ActiveDirectory{
			BaseModel: vcpModels.BaseModel{UUID: "ad-uuid"},
			AdName:    "test-ad",
			State:     "READY",
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				Description: "Test AD",
			},
		}
		mockJobID := "job-123"

		mockOrchestrator.On("UpdateActiveDirectory", mock.Anything, mock.MatchedBy(func(params interface{}) bool {
			// Verify password is encrypted (not plain text)
			return true
		})).Return(mockAD, mockJobID, nil)

		req := &gcpgenserver.ActiveDirectoryUpdateV1beta{
			Password: gcpgenserver.NewOptString("new-password"),
		}
		params := gcpgenserver.V1betaUpdateActiveDirectoryParams{
			ProjectNumber:     "project-123",
			LocationId:        "us-west1",
			ActiveDirectoryId: "test-ad",
		}

		res, err := handler.V1betaUpdateActiveDirectory(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, res)
		op, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
		assert.Contains(t, op.Name.Value, mockJobID)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("Returns error when password encryption fails during update", func(t *testing.T) {
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		// Override the EncryptPassword function to simulate encryption failure
		originalEncryptPassword := utils.EncryptPassword
		utils.EncryptPassword = func(password log.Secret) (*string, error) {
			return nil, errors.New("encryption failed")
		}
		defer func() { utils.EncryptPassword = originalEncryptPassword }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.ActiveDirectoryUpdateV1beta{
			Password: gcpgenserver.NewOptString("new-password"),
		}
		params := gcpgenserver.V1betaUpdateActiveDirectoryParams{
			ProjectNumber:     "project-123",
			LocationId:        "us-west1",
			ActiveDirectoryId: "test-ad",
		}

		res, err := handler.V1betaUpdateActiveDirectory(context.Background(), req, params)

		assert.NoError(t, err) // HTTP error is returned as response, not Go error
		errResp, ok := res.(*gcpgenserver.V1betaUpdateActiveDirectoryInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), errResp.Code)
		assert.Contains(t, errResp.Message, "encryption failed")
	})

	t.Run("Does not encrypt password when password is not provided", func(t *testing.T) {
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		mockAD := &vcpModels.ActiveDirectory{
			BaseModel: vcpModels.BaseModel{UUID: "ad-uuid"},
			AdName:    "test-ad",
			State:     "READY",
			ActiveDirectoryAttributes: &vcpModels.ActiveDirectoryAttributes{
				Description: "Test AD",
			},
		}
		mockJobID := "job-123"

		mockOrchestrator.On("UpdateActiveDirectory", mock.Anything, mock.Anything).Return(mockAD, mockJobID, nil)

		req := &gcpgenserver.ActiveDirectoryUpdateV1beta{
			Description: gcpgenserver.NewOptString("new description"),
		}
		params := gcpgenserver.V1betaUpdateActiveDirectoryParams{
			ProjectNumber:     "project-123",
			LocationId:        "us-west1",
			ActiveDirectoryId: "test-ad",
		}

		res, err := handler.V1betaUpdateActiveDirectory(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, res)
		_, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok)
		mockOrchestrator.AssertExpectations(t)
	})
}

// Test compareADStateHierarchy in endpoints layer
func Test_compareADStateHierarchy_ErrorFromVCPWins(t *testing.T) {
	sdeAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateReadyDetails),
	}
	vcpAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
	}

	compareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR, sdeAD.ActiveDirectoryState.Value)
	assert.Equal(t, datamodel.LifeCycleStateDeletionErrorDetails, sdeAD.ActiveDirectoryStateDetails.Value,
		"StateDetails should come from VCP (the winner with ERROR state)")
}

func Test_compareADStateHierarchy_ErrorFromSDEWins(t *testing.T) {
	sdeAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateCreationErrorDetails),
	}
	vcpAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateReadyDetails),
	}

	compareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR, sdeAD.ActiveDirectoryState.Value)
	assert.Equal(t, datamodel.LifeCycleStateCreationErrorDetails, sdeAD.ActiveDirectoryStateDetails.Value,
		"StateDetails should come from SDE (the winner with ERROR state)")
}

func Test_compareADStateHierarchy_UpdatingWins(t *testing.T) {
	sdeAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateUPDATING),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateUpdatingDetails),
	}
	vcpAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateCreationErrorDetails),
	}

	compareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateUPDATING, sdeAD.ActiveDirectoryState.Value)
	assert.Equal(t, datamodel.LifeCycleStateUpdatingDetails, sdeAD.ActiveDirectoryStateDetails.Value,
		"StateDetails should come from SDE (the winner with UPDATING state)")
}

func Test_compareADStateHierarchy_InUseWins(t *testing.T) {
	sdeAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateReadyDetails),
	}
	vcpAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateINUSE),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateInUseDetails),
	}

	compareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateINUSE, sdeAD.ActiveDirectoryState.Value)
	assert.Equal(t, datamodel.LifeCycleStateInUseDetails, sdeAD.ActiveDirectoryStateDetails.Value,
		"StateDetails should come from VCP (the winner with IN_USE state)")
}

func Test_compareADStateHierarchy_BothError(t *testing.T) {
	sdeAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateCreationErrorDetails),
	}
	vcpAD := &gcpgenserver.ActiveDirectoryV1beta{
		ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR),
		ActiveDirectoryStateDetails: gcpgenserver.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
	}

	compareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR, sdeAD.ActiveDirectoryState.Value)
	assert.Equal(t, datamodel.LifeCycleStateCreationErrorDetails, sdeAD.ActiveDirectoryStateDetails.Value,
		"StateDetails should come from SDE (equal priority, SDE wins)")
}

func Test_compareADStateHierarchy_AllStateCombinations(t *testing.T) {
	tests := []struct {
		name             string
		sdeState         gcpgenserver.ActiveDirectoryV1betaActiveDirectoryState
		sdeStateDetails  string
		vcpState         gcpgenserver.ActiveDirectoryV1betaActiveDirectoryState
		vcpStateDetails  string
		wantState        gcpgenserver.ActiveDirectoryV1betaActiveDirectoryState
		wantStateDetails string
	}{
		{
			"UPDATING vs ERROR",
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateUPDATING, datamodel.LifeCycleStateUpdatingDetails,
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR, datamodel.LifeCycleStateCreationErrorDetails,
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateUPDATING, datamodel.LifeCycleStateUpdatingDetails,
		},
		{
			"READY vs ERROR",
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY, datamodel.LifeCycleStateReadyDetails,
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR, datamodel.LifeCycleStateDeletionErrorDetails,
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR, datamodel.LifeCycleStateDeletionErrorDetails,
		},
		{
			"READY vs IN_USE",
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY, datamodel.LifeCycleStateReadyDetails,
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateINUSE, datamodel.LifeCycleStateInUseDetails,
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateINUSE, datamodel.LifeCycleStateInUseDetails,
		},
		{
			"ERROR vs IN_USE",
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR, datamodel.LifeCycleStateUpdateErrorDetails,
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateINUSE, datamodel.LifeCycleStateInUseDetails,
			gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateERROR, datamodel.LifeCycleStateUpdateErrorDetails,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdeAD := &gcpgenserver.ActiveDirectoryV1beta{
				ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(tt.sdeState),
				ActiveDirectoryStateDetails: gcpgenserver.NewOptString(tt.sdeStateDetails),
			}
			vcpAD := &gcpgenserver.ActiveDirectoryV1beta{
				ActiveDirectoryState:        gcpgenserver.NewOptActiveDirectoryV1betaActiveDirectoryState(tt.vcpState),
				ActiveDirectoryStateDetails: gcpgenserver.NewOptString(tt.vcpStateDetails),
			}

			compareADStateHierarchy(sdeAD, vcpAD)

			assert.Equal(t, tt.wantState, sdeAD.ActiveDirectoryState.Value,
				"State should match expected value")
			assert.Equal(t, tt.wantStateDetails, sdeAD.ActiveDirectoryStateDetails.Value,
				"StateDetails should match expected value")
		})
	}
}
