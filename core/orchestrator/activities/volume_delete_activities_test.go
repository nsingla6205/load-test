package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	oModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontap_rest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"gorm.io/gorm"
)

func assertTemporalApplicationError(t *testing.T, err error, expectedMsg, expectedType string, expectedNonRetryable bool) {
	t.Helper()
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)

	var trackingID int
	var originalMsg string
	require.NoError(t, appErr.Details(&trackingID, &originalMsg))

	assert.Contains(t, originalMsg, expectedMsg)
	assert.Equal(t, expectedType, appErr.Type())
	assert.Equal(t, expectedNonRetryable, appErr.NonRetryable())
}

func assertTemporalInternalError(t *testing.T, err error, expectedMsgSubstrings []string, expectedRetryable bool) {
	t.Helper()
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr, "expected temporal ApplicationError, got %T: %v", err, err)

	var trackingID int
	var originalMsg string
	require.NoError(t, appErr.Details(&trackingID, &originalMsg))

	assert.Equal(t, vsaerrors.ErrInternalServerError, trackingID)
	if len(expectedMsgSubstrings) > 0 {
		matched := false
		for _, substr := range expectedMsgSubstrings {
			if strings.Contains(originalMsg, substr) {
				matched = true
				break
			}
		}
		assert.True(t, matched, "expected one of %v in error details %q", expectedMsgSubstrings, originalMsg)
	}
	assert.Equal(t, vsaerrors.CustomErrorType, appErr.Type())
	assert.Equal(t, expectedRetryable, !appErr.NonRetryable())
}

type fakeCifsProvider struct {
	*vsa.MockProvider
	restClient ontap_rest.RESTClient
	deleteHook func(externalSVMUUID, adUsername, adPassword string) error
	createErr  error
}

func (f *fakeCifsProvider) DeleteCIFSServer(externalSVMUUID, adUsername, adPassword string) error {
	if f.deleteHook != nil {
		return f.deleteHook(externalSVMUUID, adUsername, adPassword)
	}
	return nil
}

func (f *fakeCifsProvider) CreateRESTClient() (ontap_rest.RESTClient, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.restClient == nil {
		return nil, fmt.Errorf("rest client not configured")
	}
	return f.restClient, nil
}

func TestDeleteVolume_Success(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteVolume)

	volumeID := "test-volume-id"
	expectedVolume := &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: 10, UUID: volumeID}}

	mockStorage.On("DeleteVolumeAndChildResources", mock.Anything, volumeID).Return(expectedVolume, nil)
	mockStorage.On("DeleteSnapshot", mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil).Maybe()
	// Act
	_, err := env.ExecuteActivity(activity.DeleteVolume, expectedVolume)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVolume_Success_VolumeAlreadyDeleted(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteVolume)

	volumeID := "test-volume-id"
	expectedVolume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeID}}

	mockStorage.On("DeleteVolumeAndChildResources", mock.Anything, volumeID).Return(nil, utilErrors.NewNotFoundErr("volume", nil))

	// Act
	_, err := env.ExecuteActivity(activity.DeleteVolume, expectedVolume)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVolume_Failure(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVolume)

	volumeID := "test-volume-id"
	expectedError := errors.New("volume not found")

	mockStorage.On("DeleteVolumeAndChildResources", mock.Anything, volumeID).Return(nil, expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteVolume, &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeID}})

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVolumeInONTAP_Success(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteVolumeInONTAP)

	volumeExternalUUID := "uuid-123"
	volumeName := "test-volume"

	node := &models.Node{}

	// Mock the DeleteVolume method
	mockProvider.On("DeleteVolume", volumeExternalUUID, volumeName).Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteVolumeInONTAP, volumeExternalUUID, volumeName, node)

	// Assert
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVolumeInONTAP_Failure(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteVolumeInONTAP)

	volumeExternalUUID := "uuid-123"
	volumeName := "test-volume"

	node := &models.Node{}
	expectedError := errors.New("failed to delete volume in ONTAP")

	// Mock the DeleteVolume method
	mockProvider.On("DeleteVolume", volumeExternalUUID, volumeName).Return(expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteVolumeInONTAP, volumeExternalUUID, volumeName, node)

	// Assert
	assert.Error(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVolumeInONTAP_VolumeInUse(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteVolumeInONTAP)

	volumeExternalUUID := "uuid-123"
	volumeName := "test-volume"

	node := &models.Node{}
	expectedError := errors.New("volume is in use by a snapshot")

	// Mock the DeleteVolume method to return "volume is in use" error
	mockProvider.On("DeleteVolume", volumeExternalUUID, volumeName).Return(expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteVolumeInONTAP, volumeExternalUUID, volumeName, node)

	// Assert
	assert.Error(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVolumeInONTAP_ClusterDown(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteVolumeInONTAP)

	volumeExternalUUID := "uuid-123"
	volumeName := "test-volume"

	node := &models.Node{}
	expectedError := errors.New("Retries exhausted when attempting to reach the storage server")

	// Mock the DeleteVolume method to return "volume is in use" error
	mockProvider.On("DeleteVolume", volumeExternalUUID, volumeName).Return(expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteVolumeInONTAP, volumeExternalUUID, volumeName, node)

	// Assert
	assert.Error(t, err)
	mockProvider.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_SkipsWhenVolumesExist(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, int64(10)).
		Return([]*datamodel.Volume{{BaseModel: datamodel.BaseModel{UUID: "vol-1"}}}, nil)

	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, int64(10), &models.Node{})
	assert.NoError(t, err)
}

func TestCleanupAutoGeneratedVPG_NotFound_ReturnsNil(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, int64(10)).
		Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, int64(10)).
		Return(nil, utilErrors.NewNotFoundErr("volume performance group", nil))

	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, int64(10), &models.Node{})
	assert.NoError(t, err)
}

func TestCleanupAutoGeneratedVPG_SkipsNonAutoGen(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, int64(10)).
		Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, int64(10)).
		Return(&datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "vpg-uuid"},
			IsAutoGen: false,
		}, nil)

	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, int64(10), &models.Node{})
	assert.NoError(t, err)
}

func TestCleanupAutoGeneratedVPG_DeletesAutoGen(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{UUID: "vpg-uuid"},
		Name:             "autogen-vpg-policy",
		PoolID:           5,
		IsAutoGen:        true,
		OntapQosPolicyID: "qos-policy",
	}

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, int64(10)).
		Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, int64(10)).
		Return(vpg, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(5)).Return(nil, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.MatchedBy(func(p vsa.DeleteQoSGroupPolicyParams) bool {
		return p.UUID == "qos-policy" && p.Name == ""
	})).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, int64(10)).
		Return(errors.New("best effort error"))
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).
		Return(nil)

	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, int64(10), &models.Node{})
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_Success(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
		Svm: &datamodel.Svm{
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"},
			Name:       "svm-1",
		},
	}
	node := &models.Node{}

	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)

	ad := &datamodel.ActiveDirectory{
		BaseModel:      datamodel.BaseModel{UUID: "ad-1"},
		CredentialPath: "secret/path",
		Username:       "ad-user",
	}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSGet(mock.Anything).Return(&ontap_rest.DNS{DNS: oModels.DNS{DynamicDNS: &oModels.DNSInlineDynamicDNS{Fqdn: nillable.ToPointer("fqdn.example.com")}}}, nil)

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, node)

	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.NotNil(t, teardown)
	assert.True(t, teardown.ShouldDelete)
	assert.Equal(t, ad, teardown.ActiveDirectory)
	assert.Equal(t, "svm-uuid", teardown.SvmExternalUUID)
	assert.Equal(t, "fqdn.example.com", teardown.FQDN)
}

func TestDetermineSmbTeardownContext_SkipsWhenOtherSmbVolumeExists(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}

	otherVolume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-2"},
		PoolID:           42,
		VolumeAttributes: &datamodel.VolumeAttributes{Protocols: []string{utils.ProtocolSMB}},
		State:            datamodel.LifeCycleStateREADY,
	}

	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume, otherVolume}, nil)

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})

	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.False(t, teardown.ShouldDelete)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_MissingActiveDirectory(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-1"},
		PoolID:           42,
		VolumeAttributes: &datamodel.VolumeAttributes{Protocols: []string{utils.ProtocolSMB}},
		Svm: &datamodel.Svm{
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"},
		},
	}

	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return((*datamodel.ActiveDirectory)(nil), nil)

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})

	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.False(t, teardown.ShouldDelete)
}

func TestDetermineSmbTeardownContext_RestClientCreationFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
		Svm: &datamodel.Svm{
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"},
		},
	}

	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), createErr: fmt.Errorf("failed")}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})

	assert.Error(t, err)
	var teardown *SmbTeardownContext
	if val != nil {
		_ = val.Get(&teardown)
	}
	assert.Nil(t, teardown)
}

func TestDetermineSmbTeardownContext_DnsNotFound(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-1"},
		PoolID:           42,
		VolumeAttributes: &datamodel.VolumeAttributes{Protocols: []string{utils.ProtocolSMB}},
		Svm:              &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"}},
	}

	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSGet(mock.Anything).Return(nil, utilErrors.NewNotFoundErr("dns", nil))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})

	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.True(t, teardown.ShouldDelete)
	assert.Equal(t, "", teardown.FQDN)
}

func TestDetermineSmbTeardownContext_DnsFetchFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-1"},
		PoolID:           42,
		VolumeAttributes: &datamodel.VolumeAttributes{Protocols: []string{utils.ProtocolSMB}},
		Svm:              &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"}},
	}

	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSGet(mock.Anything).Return(nil, fmt.Errorf("dns failure"))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})

	assert.Error(t, err)
	var teardown *SmbTeardownContext
	if val != nil {
		_ = val.Get(&teardown)
	}
	assert.Nil(t, teardown)
}

func TestDeleteCifsServerIfUnused_Success(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		ActiveDirectory: &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"},
		SvmExternalUUID: "svm-uuid",
		VolumeUUID:      "vol",
	}

	restClient := ontap_rest.NewMockRESTClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	var deleteCalled bool
	fakeProvider.deleteHook = func(externalSVMUUID, adUsername, adPassword string) error {
		deleteCalled = true
		assert.Equal(t, "svm-uuid", externalSVMUUID)
		assert.Equal(t, "user", adUsername)
		assert.Equal(t, "password", adPassword)
		return nil
	}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	origGetPwd := vsa.GetPasswordFromCacheOrSecretManager
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		assert.Equal(t, "secret", secretID)
		return "password", nil
	}
	defer func() { vsa.GetPasswordFromCacheOrSecretManager = origGetPwd }()

	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})

	assert.NoError(t, err)
	assert.True(t, deleteCalled)
}

func TestDeleteCifsServerIfUnused_PasswordFetchFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		ActiveDirectory: &datamodel.ActiveDirectory{CredentialPath: "secret"},
		SvmExternalUUID: "svm-uuid",
	}

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t)}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	origGetPwd := vsa.GetPasswordFromCacheOrSecretManager
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		return "", fmt.Errorf("secret failure")
	}
	defer func() { vsa.GetPasswordFromCacheOrSecretManager = origGetPwd }()

	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})

	assert.Error(t, err)
}

func TestDeleteCifsServerIfUnused_DeleteReturnsNotFound(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		ActiveDirectory: &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"},
		SvmExternalUUID: "svm-uuid",
	}

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t)}
	fakeProvider.deleteHook = func(externalSVMUUID, adUsername, adPassword string) error {
		return utilErrors.NewNotFoundErr("cifs", nil)
	}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	origGetPwd := vsa.GetPasswordFromCacheOrSecretManager
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		return "pwd", nil
	}
	defer func() { vsa.GetPasswordFromCacheOrSecretManager = origGetPwd }()

	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})

	assert.NoError(t, err)
}

func TestDeleteCifsServerIfUnused_DeleteFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		ActiveDirectory: &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"},
		SvmExternalUUID: "svm-uuid",
	}

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t)}
	fakeProvider.deleteHook = func(externalSVMUUID, adUsername, adPassword string) error {
		return fmt.Errorf("delete failure")
	}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	origGetPwd := vsa.GetPasswordFromCacheOrSecretManager
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		return "pwd", nil
	}
	defer func() { vsa.GetPasswordFromCacheOrSecretManager = origGetPwd }()

	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})

	assert.Error(t, err)
}

func TestDeleteCifsShareIfSMB_SkipsWhenNilVolume(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)
	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, nil, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsShareIfSMB_SkipsWhenNoActiveDirectory(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)
	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{ActiveDirectory: nil},
		VolumeAttributes: &datamodel.VolumeAttributes{CreationToken: "share-name"},
	}
	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsShareIfSMB_SkipsWhenEmptyCreationToken(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)
	volume := &datamodel.Volume{
		Pool:             &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		VolumeAttributes: &datamodel.VolumeAttributes{CreationToken: ""},
	}
	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsShareIfSMB_SkipsWhenNFSOnlyProtocols(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)
	// Volume has AD and creation token but NFS-only protocols; CifsShareDelete must not be called.
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		Svm:  &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"}},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken: "some-share",
			Protocols:     []string{"NFSv3"},
		},
	}
	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsShareIfSMB_Success(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)

	volume := &datamodel.Volume{
		Name:   "vol1",
		PoolID: 1,
		Pool:   &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		Svm:    &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid-123"}},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken: "my-share",
			Protocols:     []string{utils.ProtocolSMB},
		},
	}

	mockNAS := ontap_rest.NewMockNASClient(t)
	mockNAS.EXPECT().CifsShareDelete(mock.MatchedBy(func(p *ontap_rest.CifsShareDeleteParams) bool {
		return p.ShareName == "my-share" && p.SvmUUID == "svm-uuid-123"
	})).Return(nil).Once()

	mockREST := ontap_rest.NewMockRESTClient(t)
	mockREST.EXPECT().NAS().Return(mockNAS).Once()

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: mockREST}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = orig }()

	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsShareIfSMB_ShareNotFoundTreatedAsSuccess(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		Svm:  &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"}},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken: "share",
			Protocols:     []string{utils.ProtocolSMB},
		},
	}

	mockNAS := ontap_rest.NewMockNASClient(t)
	mockNAS.EXPECT().CifsShareDelete(mock.Anything).Return(utilErrors.NewNotFoundErr("Share", nil)).Once()

	mockREST := ontap_rest.NewMockRESTClient(t)
	mockREST.EXPECT().NAS().Return(mockNAS).Once()

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: mockREST}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = orig }()

	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsShareIfSMB_DeleteErrorFails(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		Svm:  &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"}},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken: "share",
			Protocols:     []string{utils.ProtocolSMB},
		},
	}

	mockNAS := ontap_rest.NewMockNASClient(t)
	mockNAS.EXPECT().CifsShareDelete(mock.Anything).Return(fmt.Errorf("permission denied")).Once()

	mockREST := ontap_rest.NewMockRESTClient(t)
	mockREST.EXPECT().NAS().Return(mockNAS).Once()

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: mockREST}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = orig }()

	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.Error(t, err)
}

func TestDeleteCifsShareIfSMB_GetSvmForPoolIDError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)

	volume := &datamodel.Volume{
		PoolID: 1,
		Pool:   &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		Svm:    nil, // no Svm so we fall back to GetSvmForPoolID
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken: "share",
			Protocols:     []string{utils.ProtocolSMB},
		},
	}

	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return((*datamodel.Svm)(nil), fmt.Errorf("db error"))

	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.Error(t, err)
}

func TestDeleteCifsShareIfSMB_GetsSvmUUIDFromDBWhenSvmNil(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)

	volume := &datamodel.Volume{
		PoolID: 1,
		Pool:   &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		Svm:    nil,
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken: "share",
			Protocols:     []string{utils.ProtocolSMB},
		},
	}

	dbSvm := &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-from-db"}}
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(dbSvm, nil)

	mockNAS := ontap_rest.NewMockNASClient(t)
	mockNAS.EXPECT().CifsShareDelete(mock.MatchedBy(func(p *ontap_rest.CifsShareDeleteParams) bool {
		return p.ShareName == "share" && p.SvmUUID == "svm-from-db"
	})).Return(nil).Once()

	mockREST := ontap_rest.NewMockRESTClient(t)
	mockREST.EXPECT().NAS().Return(mockNAS).Once()

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: mockREST}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = orig }()

	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsShareIfSMB_SkipsWhenSvmUUIDNotFoundAfterDB(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)

	volume := &datamodel.Volume{
		PoolID: 1,
		Pool:   &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		Svm:    nil,
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken: "share",
			Protocols:     []string{utils.ProtocolSMB},
		},
	}

	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return((*datamodel.Svm)(nil), nil)

	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsShareIfSMB_GetCifsServerProviderError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		Svm:  &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"}},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken: "share",
			Protocols:     []string{utils.ProtocolSMB},
		},
	}

	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return nil, fmt.Errorf("provider not available")
	}
	defer func() { vsa.GetProviderByNode = orig }()

	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.Error(t, err)
}

func TestDeleteCifsShareIfSMB_CreateRESTClientError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsShareIfSMB)

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{ActiveDirectory: &datamodel.ActiveDirectory{}},
		Svm:  &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"}},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken: "share",
			Protocols:     []string{utils.ProtocolSMB},
		},
	}

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), createErr: fmt.Errorf("REST client failed")}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = orig }()

	_, err := env.ExecuteActivity(activity.DeleteCifsShareIfSMB, volume, &models.Node{})
	assert.Error(t, err)
}

func TestDeleteDnsRecordIfUnused_Success(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteDnsRecordIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		SvmExternalUUID: "svm-uuid",
		FQDN:            "fqdn.example.com",
		VolumeUUID:      "vol",
	}

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSModify(mock.MatchedBy(func(params *ontap_rest.DNSModifyParams) bool {
		return params.SvmUUID == "svm-uuid" &&
			params.DDNSModifyParams.Fqdn != nil && *params.DDNSModifyParams.Fqdn == "fqdn.example.com" &&
			params.DDNSModifyParams.Enabled != nil && !*params.DDNSModifyParams.Enabled
	})).Return(nil)

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	_, err := env.ExecuteActivity(activity.DeleteDnsRecordIfUnused, teardown, &models.Node{})

	assert.NoError(t, err)
}

func TestDeleteDnsRecordIfUnused_NotFound(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteDnsRecordIfUnused)

	teardown := &SmbTeardownContext{ShouldDelete: true, SvmExternalUUID: "svm-uuid"}

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSModify(mock.Anything).Return(utilErrors.NewNotFoundErr("dns", nil))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	_, err := env.ExecuteActivity(activity.DeleteDnsRecordIfUnused, teardown, &models.Node{})

	assert.NoError(t, err)
}

func TestDeleteDnsRecordIfUnused_ModifyFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteDnsRecordIfUnused)

	teardown := &SmbTeardownContext{ShouldDelete: true, SvmExternalUUID: "svm-uuid"}

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSModify(mock.Anything).Return(fmt.Errorf("dns modify failed"))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	_, err := env.ExecuteActivity(activity.DeleteDnsRecordIfUnused, teardown, &models.Node{})

	assert.Error(t, err)
}

func TestDeleteSnapshotPolicyInONTAP_Success(t *testing.T) {
	// Arrange
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteSnapshotPolicyInONTAP)

	volume := &datamodel.Volume{
		SnapshotPolicy: &datamodel.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*datamodel.SnapshotPolicySchedule{
				{
					DaysOfMonth:     []int{1, 15},
					DaysOfWeek:      []int{2},
					Hours:           []int{3},
					Minutes:         []int{0},
					SnapmirrorLabel: "label1",
					Count:           5,
				},
			},
		},
	}

	node := &models.Node{}

	// Mock the DeleteSnapshotPolicy method
	mockProvider.On("DeleteSnapshotPolicy", volume.SnapshotPolicy.Name).Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteSnapshotPolicyInONTAP, volume.SnapshotPolicy.Name, node)

	// Assert
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteSnapshotPolicyInONTAP_Failure(t *testing.T) {
	// Arrange
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteSnapshotPolicyInONTAP)

	volume := &datamodel.Volume{
		SnapshotPolicy: &datamodel.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*datamodel.SnapshotPolicySchedule{
				{
					DaysOfMonth:     []int{1, 15},
					DaysOfWeek:      []int{2},
					Hours:           []int{3},
					Minutes:         []int{0},
					SnapmirrorLabel: "label1",
					Count:           5,
				},
			},
		},
	}

	node := &models.Node{}
	expectedError := errors.New("failed to delete snapshotPolicy in ONTAP")

	// Mock the DeleteSnapshotPolicy method
	mockProvider.On("DeleteSnapshotPolicy", volume.SnapshotPolicy.Name).Return(expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteSnapshotPolicyInONTAP, volume.SnapshotPolicy.Name, node)

	// Assert
	assert.Error(t, err)
	mockProvider.AssertExpectations(t)
}

func TestSnapmirrorInONTAPDeletesWhenBackupsExist(t *testing.T) {
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-123",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-123",
		},
	}
	node := &models.Node{}

	// Mock backup vault
	mockBackupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "backup-vault-123"},
		Name:      "test-backup-vault",
		BucketDetails: []*datamodel.BucketDetails{
			{
				BucketName:     "test-bucket",
				VendorSubnetID: "test-subnet-123",
			},
		},
	}

	// Mock snapmirror relationship
	mockSnapmirror := &ontap_rest.SnapmirrorRelationship{
		SnapmirrorRelationship: oModels.SnapmirrorRelationship{
			UUID: nillable.ToPointer(strfmt.UUID("snapmirror-uuid-123")),
		},
	}

	mockStorage.On("GetBackupVault", ctx, "backup-vault-123").Return(mockBackupVault, nil)
	mockProvider.On("SnapmirrorRelationshipGet", "test-bucket:/objstore/test-volume-uuid", "test-svm:test-volume").Return(mockSnapmirror, nil)
	mockProvider.On("SnapmirrorRelationshipDelete", "snapmirror-uuid-123").Return(&vsa.OntapAsyncResponse{}, nil)

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSnapmirrorInONTAPFailsWhenDeleteFails(t *testing.T) {
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-123",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-123",
		},
	}
	node := &models.Node{}
	expectedError := errors.New("failed to delete snapmirror relationship")

	// Mock backup vault
	mockBackupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "backup-vault-123"},
		Name:      "test-backup-vault",
		BucketDetails: []*datamodel.BucketDetails{
			{
				BucketName:     "test-bucket",
				VendorSubnetID: "test-subnet-123",
			},
		},
	}

	// Mock snapmirror relationship
	mockSnapmirror := &ontap_rest.SnapmirrorRelationship{
		SnapmirrorRelationship: oModels.SnapmirrorRelationship{
			UUID: nillable.ToPointer(strfmt.UUID("snapmirror-uuid-123")),
		},
	}

	mockStorage.On("GetBackupVault", ctx, "backup-vault-123").Return(mockBackupVault, nil)
	mockProvider.On("SnapmirrorRelationshipGet", "test-bucket:/objstore/test-volume-uuid", "test-svm:test-volume").Return(mockSnapmirror, nil)
	mockProvider.On("SnapmirrorRelationshipDelete", "snapmirror-uuid-123").Return(nil, expectedError)

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete snapmirror relationship")
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSnapmirrorInONTAPFailsWhenVolumeAttributesIsNil(t *testing.T) {
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-123",
		},
		VolumeAttributes: nil, // This should cause the error
	}
	node := &models.Node{}

	// Mock backup vault
	mockBackupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "backup-vault-123"},
		Name:      "test-backup-vault",
		BucketDetails: []*datamodel.BucketDetails{
			{
				BucketName:     "test-bucket",
				VendorSubnetID: "test-subnet-123",
			},
		},
	}

	mockStorage.On("GetBackupVault", ctx, "backup-vault-123").Return(mockBackupVault, nil)

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.Error(t, err)
	assertTemporalApplicationError(t, err, "volume test-volume has no volume attributes", "CustomError", false)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSnapmirrorInONTAPSkipsWhenVolumeHasBackupsButNoDataProtection(t *testing.T) {
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	volume := &datamodel.Volume{
		BaseModel:      datamodel.BaseModel{UUID: volumeUUID},
		DataProtection: nil, // No data protection
	}
	node := &models.Node{}

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.NoError(t, err)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSnapmirrorInONTAPSkipsWhenVolumeHasBackupsButEmptyBackupVaultID(t *testing.T) {
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "", // Empty backup vault ID
		},
	}
	node := &models.Node{}

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.NoError(t, err)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSnapmirrorInONTAPSkipsWhenSnapmirrorRelationshipNotFound(t *testing.T) {
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-123",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-123",
		},
	}
	node := &models.Node{}

	// Mock backup vault
	mockBackupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "backup-vault-123"},
		Name:      "test-backup-vault",
		BucketDetails: []*datamodel.BucketDetails{
			{
				BucketName:     "test-bucket",
				VendorSubnetID: "test-subnet-123",
			},
		},
	}

	mockStorage.On("GetBackupVault", ctx, "backup-vault-123").Return(mockBackupVault, nil)
	mockProvider.On("SnapmirrorRelationshipGet", "test-bucket:/objstore/test-volume-uuid", "test-svm:test-volume").Return(nil, utilErrors.NewNotFoundErr("snapmirror relationship not found for destination: test-bucket:/objstore/test-volume-uuid and source: test-svm:test-volume", nil))

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.NoError(t, err)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSnapmirrorInONTAPSuccessfullyDeletesSnapmirror(t *testing.T) {
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-123",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-123",
		},
	}
	node := &models.Node{}

	// Mock backup vault
	mockBackupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "backup-vault-123"},
		Name:      "test-backup-vault",
		BucketDetails: []*datamodel.BucketDetails{
			{
				BucketName:     "test-bucket",
				VendorSubnetID: "test-subnet-123",
			},
		},
	}

	// Mock snapmirror relationship
	sourcePath := "test-svm:test-volume"
	destinationPath := "test-bucket:/objstore/test-volume-uuid"
	mockSnapmirror := &ontap_rest.SnapmirrorRelationship{
		SnapmirrorRelationship: oModels.SnapmirrorRelationship{
			UUID:        nillable.ToPointer(strfmt.UUID("snapmirror-uuid")),
			Source:      &oModels.SnapmirrorSourceEndpoint{Path: &sourcePath},
			Destination: &oModels.SnapmirrorEndpoint{Path: &destinationPath},
		},
	}

	mockStorage.On("GetBackupVault", ctx, "backup-vault-123").Return(mockBackupVault, nil)
	mockProvider.On("SnapmirrorRelationshipGet", "test-bucket:/objstore/test-volume-uuid", "test-svm:test-volume").Return(mockSnapmirror, nil)
	mockProvider.On("SnapmirrorRelationshipDelete", "snapmirror-uuid").Return(&vsa.OntapAsyncResponse{}, nil)

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVolumeAssociatedSnapshots_NoSnapshotsFound(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVolumeAssociatedSnapshots)
	volumeID := int64(123)

	mockStorage.On("GetSnapshotsByVolumeID", mock.Anything, volumeID).
		Return(nil, utilErrors.NewNotFoundErr("snapshot", nil))

	_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedSnapshots, volumeID)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVolumeAssociatedSnapshots_GetSnapshotsError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVolumeAssociatedSnapshots)
	volumeID := int64(123)

	mockStorage.On("GetSnapshotsByVolumeID", mock.Anything, volumeID).
		Return(nil, errors.New("db error"))

	_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedSnapshots, volumeID)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVolumeAssociatedSnapshots_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVolumeAssociatedSnapshots)
	volumeID := int64(123)
	snapshots := []*datamodel.Snapshot{
		{BaseModel: datamodel.BaseModel{UUID: "snap-1"}, Name: "snap1"},
		{BaseModel: datamodel.BaseModel{UUID: "snap-2"}, Name: "snap2"},
	}

	mockStorage.On("GetSnapshotsByVolumeID", mock.Anything, volumeID).
		Return(snapshots, nil)
	mockStorage.On("DeleteSnapshot", mock.Anything, "snap-1").Return(&datamodel.Snapshot{}, nil)
	mockStorage.On("DeleteSnapshot", mock.Anything, "snap-2").Return(&datamodel.Snapshot{}, nil)

	_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedSnapshots, volumeID)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVolumeAssociatedSnapshots_DeleteSnapshotError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVolumeAssociatedSnapshots)
	volumeID := int64(123)
	snapshots := []*datamodel.Snapshot{
		{BaseModel: datamodel.BaseModel{UUID: "snap-1"}, Name: "snap1"},
		{BaseModel: datamodel.BaseModel{UUID: "snap-2"}, Name: "snap2"},
	}

	mockStorage.On("GetSnapshotsByVolumeID", mock.Anything, volumeID).
		Return(snapshots, nil)
	mockStorage.On("DeleteSnapshot", mock.Anything, "snap-1").Return(&datamodel.Snapshot{}, errors.New("delete error"))
	mockStorage.On("DeleteSnapshot", mock.Anything, "snap-2").Return(&datamodel.Snapshot{}, nil)

	_, err := env.ExecuteActivity(activity.DeleteVolumeAssociatedSnapshots, volumeID)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteAssociatedQuotaRules_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteAssociatedQuotaRules)
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

	_, err := env.ExecuteActivity(activity.DeleteAssociatedQuotaRules, volumeID)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteAssociatedQuotaRules_NoQuotaRulesFound(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteAssociatedQuotaRules)
	volumeID := int64(123)

	mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
		Return(nil, utilErrors.NewNotFoundErr("quota rule", nil))

	_, err := env.ExecuteActivity(activity.DeleteAssociatedQuotaRules, volumeID)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteAssociatedQuotaRules_GetQuotaRulesError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteAssociatedQuotaRules)
	volumeID := int64(123)

	mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
		Return(nil, errors.New("database connection error"))

	_, err := env.ExecuteActivity(activity.DeleteAssociatedQuotaRules, volumeID)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteAssociatedQuotaRules_DeleteQuotaRuleError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteAssociatedQuotaRules)
	volumeID := int64(123)
	quotaRules := []*datamodel.QuotaRule{
		{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-1"}, Name: "quota-rule-1"},
		{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-2"}, Name: "quota-rule-2"},
		{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-3"}, Name: "quota-rule-3"},
	}

	mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
		Return(quotaRules, nil)
	deleteError := errors.New("delete error for qr-uuid-1")
	mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-1").Return(nil, deleteError)
	// Note: qr-uuid-2 and qr-uuid-3 should not be called since we fail on the first one

	_, err := env.ExecuteActivity(activity.DeleteAssociatedQuotaRules, volumeID)
	assert.Error(t, err) // Should return error when quota rule deletion fails
	assert.Contains(t, err.Error(), "failed to delete quota rule")
	assert.Contains(t, err.Error(), "quota-rule-1")
	mockStorage.AssertExpectations(t)
}

func TestDeleteAssociatedQuotaRules_EmptyQuotaRulesList(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteAssociatedQuotaRules)
	volumeID := int64(123)
	emptyQuotaRules := []*datamodel.QuotaRule{}

	mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
		Return(emptyQuotaRules, nil)

	_, err := env.ExecuteActivity(activity.DeleteAssociatedQuotaRules, volumeID)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteAssociatedQuotaRules_SingleQuotaRule(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteAssociatedQuotaRules)
	volumeID := int64(123)
	quotaRules := []*datamodel.QuotaRule{
		{BaseModel: datamodel.BaseModel{UUID: "qr-uuid-1"}, Name: "quota-rule-1"},
	}

	mockStorage.On("GetQuotaRulesByVolumeID", mock.Anything, volumeID).
		Return(quotaRules, nil)
	mockStorage.On("DeleteQuotaRule", mock.Anything, "qr-uuid-1").Return(&datamodel.QuotaRule{}, nil)

	_, err := env.ExecuteActivity(activity.DeleteAssociatedQuotaRules, volumeID)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteSnapshotPolicyInONTAP_WithNilNode(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteSnapshotPolicyInONTAP)

	_, err := env.ExecuteActivity(activity.DeleteSnapshotPolicyInONTAP, "policy1", nil)

	assert.NoError(t, err)
}

func TestDeleteSnapshotPolicyInONTAP_WithEmptyPolicyName(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteSnapshotPolicyInONTAP)
	node := &models.Node{}

	_, err := env.ExecuteActivity(activity.DeleteSnapshotPolicyInONTAP, "", node)

	assert.NoError(t, err)
}

func TestDeleteSnapshotPolicyInONTAP_SkipsDefaultNonePolicy(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteSnapshotPolicyInONTAP)
	node := &models.Node{}

	_, err := env.ExecuteActivity(activity.DeleteSnapshotPolicyInONTAP, models.DefaultONTAPSnapshotPolicyName, node)

	assert.NoError(t, err)
}

func TestDeleteSnapshotPolicyInONTAP_GetProviderByNodeFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	expectedError := errors.New("failed to get provider")
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, expectedError
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteSnapshotPolicyInONTAP)
	node := &models.Node{}

	_, err := env.ExecuteActivity(activity.DeleteSnapshotPolicyInONTAP, "policy1", node)

	assert.Error(t, err)
}

func TestDeleteIgroups_Success(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := vsa.NewMockProvider(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	// Create test volume with block devices
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
						{
							HostGroupUUID: "hostgroup-uuid-2",
							HostQNs:       []string{"iqn.example.2"},
						},
					},
				},
				{
					Name: "block-device-2",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-3",
							HostQNs:       []string{"iqn.example.3"},
						},
					},
				},
			},
		},
		AccountID: 1,
		Svm:       &datamodel.Svm{Name: "test-svm"},
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	// Mock SE.GetAllVolumesForHG to return empty volumes (no usage)
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-1", int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-2", int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-3", int64(1)).Return([]*datamodel.Volume{}, nil)

	// Mock SE.GetHostGroup to return host groups
	hostgroup1 := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "hostgroup-uuid-1"},
		Name:      "hostgroup-name-1",
		AccountID: 1,
	}
	hostgroup2 := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "hostgroup-uuid-2"},
		Name:      "hostgroup-name-2",
		AccountID: 1,
	}
	hostgroup3 := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "hostgroup-uuid-3"},
		Name:      "hostgroup-name-3",
		AccountID: 1,
	}
	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-1", int64(1)).Return(hostgroup1, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-2", int64(1)).Return(hostgroup2, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-3", int64(1)).Return(hostgroup3, nil)

	// Mock provider.IgroupGet to return igroups
	igroup1 := &ontap_rest.Igroup{
		Igroup: oModels.Igroup{
			UUID: nillable.GetStringPtr("ontap-igroup-uuid-1"),
			Name: &hostgroup1.Name,
		},
	}
	igroup2 := &ontap_rest.Igroup{
		Igroup: oModels.Igroup{
			UUID: nillable.GetStringPtr("ontap-igroup-uuid-2"),
			Name: &hostgroup2.Name,
		},
	}
	igroup3 := &ontap_rest.Igroup{
		Igroup: oModels.Igroup{
			UUID: nillable.GetStringPtr("ontap-igroup-uuid-3"),
			Name: &hostgroup3.Name,
		},
	}
	mockProvider.On("IgroupGet", &hostgroup1.Name, mock.Anything).Return(igroup1, nil)
	mockProvider.On("IgroupGet", &hostgroup2.Name, mock.Anything).Return(igroup2, nil)
	mockProvider.On("IgroupGet", &hostgroup3.Name, mock.Anything).Return(igroup3, nil)

	// Mock provider.IgroupDelete calls
	mockProvider.On("IgroupDelete", *igroup1.UUID).Return(nil)
	mockProvider.On("IgroupDelete", *igroup2.UUID).Return(nil)
	mockProvider.On("IgroupDelete", *igroup3.UUID).Return(nil)

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroups_OneHostGroupInUse(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := vsa.NewMockProvider(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	// Create test volume with block devices
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
						{
							HostGroupUUID: "hostgroup-uuid-2",
							HostQNs:       []string{"iqn.example.2"},
						},
					},
				},
			},
		},
		AccountID: 1,
		Svm:       &datamodel.Svm{Name: "test-svm"},
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	// Mock SE.GetAllVolumesForHG to return:
	// - hostgroup-uuid-1: no other volumes (should be deleted)
	// - hostgroup-uuid-2: has another volume (should NOT be deleted)
	otherVolume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "other-volume-uuid"},
		Name:      "other-volume",
	}
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-1", int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-2", int64(1)).Return([]*datamodel.Volume{volume, otherVolume}, nil)

	// Mock SE.GetHostGroup to return host groups - only for hostgroup-uuid-1 since hostgroup-uuid-2 won't be processed
	hostgroup1 := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "hostgroup-uuid-1"},
		Name:      "hostgroup-name-1",
		AccountID: 1,
	}

	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-1", int64(1)).Return(hostgroup1, nil)

	// Mock provider.IgroupGet to return igroups - only for hostgroup-uuid-1 since hostgroup-uuid-2 won't be processed
	igroup1 := &ontap_rest.Igroup{
		Igroup: oModels.Igroup{
			UUID: nillable.GetStringPtr("ontap-igroup-uuid-1"),
			Name: &hostgroup1.Name,
		},
	}
	mockProvider.On("IgroupGet", &hostgroup1.Name, mock.Anything).Return(igroup1, nil)

	// Mock provider.IgroupDelete call - only for the unused hostgroup
	mockProvider.On("IgroupDelete", *igroup1.UUID).Return(nil)

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroups_GetProviderByNodeFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	expectedError := errors.New("failed to get provider")
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, expectedError
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
					},
				},
			},
		},
		AccountID: 1,
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.Error(t, err)
}

func TestDeleteIgroups_GetAllVolumesForHGFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := vsa.NewMockProvider(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
					},
				},
			},
		},
		AccountID: 1,
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	expectedError := errors.New("failed to get volumes for host group")
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-1", int64(1)).Return(nil, expectedError)

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteIgroups_GetHostGroupFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := vsa.NewMockProvider(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
					},
				},
			},
		},
		AccountID: 1,
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	expectedError := errors.New("failed to get host group")
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-1", int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-1", int64(1)).Return(nil, expectedError)

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteIgroups_IgroupGetNotFoundError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := vsa.NewMockProvider(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
					},
				},
			},
		},
		AccountID: 1,
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	hostgroup1 := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "hostgroup-uuid-1"},
		Name:      "hostgroup-name-1",
		AccountID: 1,
	}

	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-1", int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-1", int64(1)).Return(hostgroup1, nil)

	// Mock IgroupGet to return not found error
	notFoundError := utilErrors.NewNotFoundErr("igroup", nil)
	mockProvider.On("IgroupGet", &hostgroup1.Name, mock.Anything).Return(nil, notFoundError)

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.NoError(t, err) // Should continue and not return error for not found
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroups_IgroupGetOtherError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := vsa.NewMockProvider(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
					},
				},
			},
		},
		AccountID: 1,
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	hostgroup1 := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "hostgroup-uuid-1"},
		Name:      "hostgroup-name-1",
		AccountID: 1,
	}

	expectedError := errors.New("unexpected error getting igroup")
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-1", int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-1", int64(1)).Return(hostgroup1, nil)
	mockProvider.On("IgroupGet", &hostgroup1.Name, mock.Anything).Return(nil, expectedError)

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroups_IgroupDeleteFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := vsa.NewMockProvider(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
					},
				},
			},
		},
		AccountID: 1,
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	hostgroup1 := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "hostgroup-uuid-1"},
		Name:      "hostgroup-name-1",
		AccountID: 1,
	}

	igroup1 := &ontap_rest.Igroup{
		Igroup: oModels.Igroup{
			UUID: nillable.GetStringPtr("ontap-igroup-uuid-1"),
			Name: &hostgroup1.Name,
		},
	}

	expectedError := errors.New("failed to delete igroup")
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-1", int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-1", int64(1)).Return(hostgroup1, nil)
	mockProvider.On("IgroupGet", &hostgroup1.Name, mock.Anything).Return(igroup1, nil)
	mockProvider.On("IgroupDelete", *igroup1.UUID).Return(expectedError)

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroups_IgroupWithNilUUID(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := vsa.NewMockProvider(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
					},
				},
			},
		},
		AccountID: 1,
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	hostgroup1 := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "hostgroup-uuid-1"},
		Name:      "hostgroup-name-1",
		AccountID: 1,
	}

	// Create igroup with nil UUID
	igroup1 := &ontap_rest.Igroup{
		Igroup: oModels.Igroup{
			UUID: nil,
			Name: &hostgroup1.Name,
		},
	}

	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-1", int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-1", int64(1)).Return(hostgroup1, nil)
	mockProvider.On("IgroupGet", &hostgroup1.Name, mock.Anything).Return(igroup1, nil)

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.NoError(t, err) // Should continue when UUID is nil
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroups_IgroupWithNilIgroup(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := vsa.NewMockProvider(t)

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{
		SE: mockStorage,
	}
	env.RegisterActivity(activity.DeleteIgroups)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name: "block-device-1",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "hostgroup-uuid-1",
							HostQNs:       []string{"iqn.example.1"},
						},
					},
				},
			},
		},
		AccountID: 1,
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	hostgroup1 := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "hostgroup-uuid-1"},
		Name:      "hostgroup-name-1",
		AccountID: 1,
	}

	// Mock IgroupGet to return nil igroup
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "hostgroup-uuid-1", int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "hostgroup-uuid-1", int64(1)).Return(hostgroup1, nil)
	mockProvider.On("IgroupGet", &hostgroup1.Name, mock.Anything).Return(nil, nil)

	_, err := env.ExecuteActivity(activity.DeleteIgroups, volume, node)

	assert.NoError(t, err) // Should continue when igroup is nil
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVolumeInONTAP_GetProviderByNodeFailure(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	expectedError := errors.New("failed to get provider")
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, expectedError
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteVolumeInONTAP)

	volumeExternalUUID := "uuid-123"
	volumeName := "test-volume"
	node := &models.Node{}

	_, err := env.ExecuteActivity(activity.DeleteVolumeInONTAP, volumeExternalUUID, volumeName, node)

	assert.Error(t, err)
}

func TestSnapmirrorInONTAPSkipsWhenNodeIsNil(t *testing.T) {
	activity := VolumeDeleteActivity{}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"}}

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, nil)

	assert.NoError(t, err)
	assert.Nil(t, resp)
}

func TestSnapmirrorInONTAPSkipsWhenVolumeUUIDIsEmpty(t *testing.T) {
	activity := VolumeDeleteActivity{}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: ""}}
	node := &models.Node{}

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.NoError(t, err)
	assert.Nil(t, resp)
}

func TestSnapmirrorInONTAPFailsWhenGetProviderByNodeFails(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	expectedError := errors.New("failed to get provider")
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, expectedError
	}

	activity := VolumeDeleteActivity{}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"}}
	node := &models.Node{}

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestSnapmirrorInONTAPFailsWhenGetBackupVaultFails(t *testing.T) {
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-123",
		},
	}
	node := &models.Node{}
	expectedError := errors.New("failed to get backup vault")

	mockStorage.On("GetBackupVault", ctx, "backup-vault-123").Return(nil, expectedError)

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get backup vault")
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
}

func TestDeleteSnapmirrorInONTAP_BackupVaultNotFound_WrappedNotFoundErr(t *testing.T) {
	// Test that when GetBackupVault returns a wrapped NotFoundErr, deletion skips gracefully
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	backupVaultID := "backup-vault-not-found"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: backupVaultID,
		},
	}
	node := &models.Node{}

	// Return wrapped NotFoundErr (as some database methods do)
	notFoundErr := utilErrors.NewNotFoundErr("backup vault", &backupVaultID)
	mockStorage.On("GetBackupVault", ctx, backupVaultID).Return(nil, notFoundErr)

	// Act
	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	// Assert - should skip gracefully without error
	assert.NoError(t, err)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
}

func TestDeleteSnapmirrorInONTAP_BackupVaultNotFound_GormRecordNotFound(t *testing.T) {
	// Test that when GetBackupVault returns NotFoundErr, deletion skips gracefully
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	backupVaultID := "backup-vault-gorm-not-found"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: backupVaultID,
		},
	}
	node := &models.Node{}

	mockStorage.On("GetBackupVault", ctx, backupVaultID).Return(nil, utilErrors.NewNotFoundErr("backup vault", &backupVaultID))

	// Act
	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	// Assert - should skip gracefully without error
	assert.NoError(t, err)
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
}

func TestSnapmirrorInONTAPFailsWhenSnapmirrorRelationshipGetFails(t *testing.T) {
	mockProvider := new(vsa.MockProvider)
	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	volumeUUID := "test-volume-uuid"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-123",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			VendorSubnetID: "test-subnet-123",
		},
	}
	node := &models.Node{}
	expectedError := errors.New("failed to get snapmirror relationship")

	// Mock backup vault
	mockBackupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "backup-vault-123"},
		Name:      "test-backup-vault",
		BucketDetails: []*datamodel.BucketDetails{
			{
				BucketName:     "test-bucket",
				VendorSubnetID: "test-subnet-123",
			},
		},
	}

	mockStorage.On("GetBackupVault", ctx, "backup-vault-123").Return(mockBackupVault, nil)
	mockProvider.On("SnapmirrorRelationshipGet", "test-bucket:/objstore/test-volume-uuid", "test-svm:test-volume").Return(nil, expectedError)

	resp, err := activity.DeleteSnapmirrorInONTAP(ctx, volume, node)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get snapmirror relationship")
	assert.Nil(t, resp)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroupsFromBlockProperties_Success(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	node := &models.Node{}

	hostgroupDB := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "test-hostgroup-uuid"},
		Name:      "test-hostgroup",
	}

	igroup := &ontap_rest.Igroup{
		Igroup: oModels.Igroup{
			UUID: nillable.GetStringPtr("test-igroup-uuid"),
		},
	}

	mockStorage.On("GetAllVolumesForHG", mock.Anything, "test-hostgroup-uuid", int64(1)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "test-hostgroup-uuid", int64(1)).Return(hostgroupDB, nil)
	mockProvider.On("IgroupGet", &hostgroupDB.Name, (*string)(nil)).Return(igroup, nil)
	mockProvider.On("IgroupDelete", "test-igroup-uuid").Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroupsFromBlockProperties_GetProviderByNodeFailure(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	expectedError := errors.New("provider not found")
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, expectedError
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	node := &models.Node{}

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.Error(t, err)
}

func TestDeleteIgroupsFromBlockProperties_GetAllVolumesForHGFailure(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	node := &models.Node{}

	expectedError := errors.New("database error")
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "test-hostgroup-uuid", int64(1)).Return(nil, expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteIgroupsFromBlockProperties_HostGroupInUse(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	otherVolume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "other-volume-uuid"},
		AccountID: 1,
	}

	node := &models.Node{}

	mockStorage.On("GetAllVolumesForHG", mock.Anything, "test-hostgroup-uuid", int64(1)).Return([]*datamodel.Volume{volume, otherVolume}, nil)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteIgroupsFromBlockProperties_GetHostGroupFailure(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	node := &models.Node{}

	expectedError := errors.New("hostgroup not found")
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "test-hostgroup-uuid", int64(1)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "test-hostgroup-uuid", int64(1)).Return(nil, expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteIgroupsFromBlockProperties_IgroupGetNotFoundError(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	node := &models.Node{}

	hostgroupDB := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "test-hostgroup-uuid"},
		Name:      "test-hostgroup",
	}

	notFoundError := utilErrors.NewNotFoundErr("igroup", nil)
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "test-hostgroup-uuid", int64(1)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "test-hostgroup-uuid", int64(1)).Return(hostgroupDB, nil)
	mockProvider.On("IgroupGet", &hostgroupDB.Name, (*string)(nil)).Return(nil, notFoundError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroupsFromBlockProperties_IgroupGetOtherError(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	node := &models.Node{}

	hostgroupDB := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "test-hostgroup-uuid"},
		Name:      "test-hostgroup",
	}

	expectedError := errors.New("network error")
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "test-hostgroup-uuid", int64(1)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "test-hostgroup-uuid", int64(1)).Return(hostgroupDB, nil)
	mockProvider.On("IgroupGet", &hostgroupDB.Name, (*string)(nil)).Return(nil, expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroupsFromBlockProperties_IgroupDeleteFailure(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	node := &models.Node{}

	hostgroupDB := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "test-hostgroup-uuid"},
		Name:      "test-hostgroup",
	}

	igroup := &ontap_rest.Igroup{
		Igroup: oModels.Igroup{
			UUID: nillable.GetStringPtr("test-igroup-uuid"),
		},
	}

	expectedError := errors.New("delete failed")
	mockStorage.On("GetAllVolumesForHG", mock.Anything, "test-hostgroup-uuid", int64(1)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "test-hostgroup-uuid", int64(1)).Return(hostgroupDB, nil)
	mockProvider.On("IgroupGet", &hostgroupDB.Name, (*string)(nil)).Return(igroup, nil)
	mockProvider.On("IgroupDelete", "test-igroup-uuid").Return(expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroupsFromBlockProperties_IgroupWithNilUUID(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	node := &models.Node{}

	hostgroupDB := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "test-hostgroup-uuid"},
		Name:      "test-hostgroup",
	}

	igroup := &ontap_rest.Igroup{
		Igroup: oModels.Igroup{
			UUID: nil, // Nil UUID
		},
	}

	mockStorage.On("GetAllVolumesForHG", mock.Anything, "test-hostgroup-uuid", int64(1)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "test-hostgroup-uuid", int64(1)).Return(hostgroupDB, nil)
	mockProvider.On("IgroupGet", &hostgroupDB.Name, (*string)(nil)).Return(igroup, nil)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteIgroupsFromBlockProperties_IgroupWithNilIgroup(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteIgroupsFromBlockProperties)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		AccountID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "test-hostgroup-uuid"},
				},
			},
		},
	}

	node := &models.Node{}

	hostgroupDB := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: "test-hostgroup-uuid"},
		Name:      "test-hostgroup",
	}

	mockStorage.On("GetAllVolumesForHG", mock.Anything, "test-hostgroup-uuid", int64(1)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetHostGroup", mock.Anything, "test-hostgroup-uuid", int64(1)).Return(hostgroupDB, nil)
	mockProvider.On("IgroupGet", &hostgroupDB.Name, (*string)(nil)).Return(nil, nil) // Nil igroup

	// Act
	_, err := env.ExecuteActivity(activity.DeleteIgroupsFromBlockProperties, volume, node)

	// Assert
	assert.NoError(t, err) // Should continue when igroup is nil
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteExportPolicy_Success(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteExportPolicy)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "test-export-policy",
				},
			},
		},
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	expectedExportPolicy := &vsa.ExportPolicy{
		ExportPolicyName: "test-export-policy",
		SvmName:          "test-svm",
	}

	mockProvider.On("DeleteExportPolicy", expectedExportPolicy).Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteExportPolicy, volume, node)

	// Assert
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteExportPolicy_SkipsWhenExportPolicyNameIsEmpty(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteExportPolicy)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "", // Empty export policy name
				},
			},
		},
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	// Act
	_, err := env.ExecuteActivity(activity.DeleteExportPolicy, volume, node)

	// Assert
	assert.NoError(t, err) // Should skip deletion and return no error
	mockProvider.AssertExpectations(t)
}

func TestDeleteExportPolicy_GetProviderByNodeFailure(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	expectedError := errors.New("failed to get provider")
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, expectedError
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteExportPolicy)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "test-export-policy",
				},
			},
		},
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	// Act
	_, err := env.ExecuteActivity(activity.DeleteExportPolicy, volume, node)

	// Assert
	assert.Error(t, err)
}

func TestDeleteExportPolicy_DeleteExportPolicyFailure(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteExportPolicy)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "test-export-policy",
				},
			},
		},
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	expectedExportPolicy := &vsa.ExportPolicy{
		ExportPolicyName: "test-export-policy",
		SvmName:          "test-svm",
	}

	expectedError := errors.New("failed to delete export policy")
	mockProvider.On("DeleteExportPolicy", expectedExportPolicy).Return(expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteExportPolicy, volume, node)

	// Assert
	assert.Error(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteExportPolicy_ExportPolicyNotFound(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteExportPolicy)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "test-export-policy",
				},
			},
		},
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	expectedExportPolicy := &vsa.ExportPolicy{
		ExportPolicyName: "test-export-policy",
		SvmName:          "test-svm",
	}

	notFoundError := utilErrors.NewNotFoundErr("export policy", nil)
	mockProvider.On("DeleteExportPolicy", expectedExportPolicy).Return(notFoundError)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteExportPolicy, volume, node)

	// Assert
	assert.NoError(t, err) // Should skip deletion and return no error when export policy is not found
	mockProvider.AssertExpectations(t)
}

func TestDeleteExportPolicy_NilVolumeAttributes(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteExportPolicy)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		VolumeAttributes: nil, // Nil volume attributes
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	// Act & Assert
	_, err := env.ExecuteActivity(activity.DeleteExportPolicy, volume, node)
	assert.NoError(t, err)
}

func TestDeleteExportPolicy_NilFileProperties(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteExportPolicy)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: nil, // Nil file properties
		},
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	// Act & Assert
	_, err := env.ExecuteActivity(activity.DeleteExportPolicy, volume, node)
	assert.NoError(t, err)
}

func TestDeleteExportPolicy_NilExportPolicy(t *testing.T) {
	// Arrange
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: nil, // Nil export policy
			},
		},
	}

	node := &models.Node{
		Name:            "test-node",
		EndpointAddress: "192.168.1.1",
	}

	// Act & Assert
	err := activity.DeleteExportPolicy(ctx, volume, node)
	assert.NoError(t, err)
}

func TestDetermineIfVolumeIsLastFilesVolume_NilVolume(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	isLastVolume, err := activity.DetermineIfVolumeIsLastFilesVolume(ctx, nil, &models.Node{})

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "volume/node is nil")
	assert.False(t, isLastVolume)
}

func TestDetermineIfVolumeIsLastFilesVolume_NilNode(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-1"}}
	isLastVolume, err := activity.DetermineIfVolumeIsLastFilesVolume(ctx, volume, nil)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "volume/node is nil")
	assert.False(t, isLastVolume)
}

func TestDetermineIfVolumeIsLastFilesVolume_NilVolumeAttributes(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: nil,
	}
	isLastVolume, err := activity.DetermineIfVolumeIsLastFilesVolume(ctx, volume, &models.Node{})

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "volume attributes is nil for volume")
	assert.False(t, isLastVolume)
}

func TestDetermineIfVolumeIsLastFilesVolume_NonNASProtocol(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{"ISCSI"},
		},
	}
	isLastVolume, err := activity.DetermineIfVolumeIsLastFilesVolume(ctx, volume, &models.Node{})

	// Assert
	assert.NoError(t, err)
	assert.False(t, isLastVolume)
}

func TestDetermineIfVolumeIsLastFilesVolume_GetVolumesError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
	}
	mockStorage.On("GetVolumesByPoolID", ctx, int64(42)).Return(nil, errors.New("db error"))

	isLastVolume, err := activity.DetermineIfVolumeIsLastFilesVolume(ctx, volume, &models.Node{})
	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
	assert.False(t, isLastVolume)
	mockStorage.AssertExpectations(t)
}

func TestDetermineIfVolumeIsLastFilesVolume_OtherVolumeNil(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
	}
	mockStorage.On("GetVolumesByPoolID", ctx, int64(42)).Return([]*datamodel.Volume{volume, nil}, nil)

	isLastVolume, err := activity.DetermineIfVolumeIsLastFilesVolume(ctx, volume, &models.Node{})
	// Assert
	assert.NoError(t, err)
	assert.True(t, isLastVolume)
	mockStorage.AssertExpectations(t)
}

func TestDetermineIfVolumeIsLastFilesVolume_OtherVolumeNilAttributes(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
	}
	otherVolume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-2"},
		VolumeAttributes: nil,
	}
	mockStorage.On("GetVolumesByPoolID", ctx, int64(42)).Return([]*datamodel.Volume{volume, otherVolume}, nil)

	isLastVolume, err := activity.DetermineIfVolumeIsLastFilesVolume(ctx, volume, &models.Node{})
	// Assert
	assert.NoError(t, err)
	assert.True(t, isLastVolume)
	mockStorage.AssertExpectations(t)
}

func TestDetermineIfVolumeIsLastFilesVolume_OtherVolumeIsNASProtocol(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
	}
	otherVolume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-2"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv4},
		},
	}
	mockStorage.On("GetVolumesByPoolID", ctx, int64(42)).Return([]*datamodel.Volume{volume, otherVolume}, nil)

	isLastVolume, err := activity.DetermineIfVolumeIsLastFilesVolume(ctx, volume, &models.Node{})
	// Assert
	assert.NoError(t, err)
	assert.False(t, isLastVolume)
	mockStorage.AssertExpectations(t)
}

func TestDeleteLDAPConfiguration_NilVolume(t *testing.T) {
	// Mock setup
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	// Mock provider setup
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	// Execute test
	err := activity.DeleteLDAPConfiguration(ctx, nil, &models.Node{})

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "volume/node is nil")
}

func TestDeleteLDAPConfiguration_NilVolumeAttributes(t *testing.T) {
	// Mock setup
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: nil,
	}

	// Mock provider setup
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	// Execute test
	err := activity.DeleteLDAPConfiguration(ctx, volume, &models.Node{})

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "volume attributes is nil for volume")
}

func TestDeleteLDAPConfiguration_NilSVM(t *testing.T) {
	// Mock setup
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
	}

	// Mock provider setup
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	// Execute test
	err := activity.DeleteLDAPConfiguration(ctx, volume, &models.Node{})

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "volume SVM details is nil for volume")
}

func TestDeleteLDAPConfiguration_ProviderError(t *testing.T) {
	// Mock setup
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
	}

	// Mock provider setup
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider")
	}

	// Execute test
	err := activity.DeleteLDAPConfiguration(ctx, volume, &models.Node{})

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get provider")
}

func TestDeleteLDAPConfiguration_LdapClientConfigurationNotFound(t *testing.T) {
	// Mock setup
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
		Svm: &datamodel.Svm{
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "external-uuid",
			},
		},
	}

	// Mock provider setup
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	mockProvider.EXPECT().DeleteLdap(mock.Anything).Return(utilErrors.NewNotFoundErr("Ldap client configuration not found", nil))

	// Execute test
	err := activity.DeleteLDAPConfiguration(ctx, volume, &models.Node{})

	// Assertions
	assert.NoError(t, err)
}

func TestDeleteLDAPConfiguration_FailedToDeleteLDAPConfig(t *testing.T) {
	// Mock setup
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
		Svm: &datamodel.Svm{
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "external-uuid",
			},
		},
	}

	// Mock provider setup
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	mockProvider.EXPECT().DeleteLdap(mock.Anything).Return(errors.New("failed to delete LDAP config for volume"))

	// Execute test
	err := activity.DeleteLDAPConfiguration(ctx, volume, &models.Node{})

	// Assertions
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "An error occurred during LDAP configuration. Please verify your settings and try again")
}

func TestDeleteLDAPConfiguration_Success(t *testing.T) {
	// Mock setup
	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
		Svm: &datamodel.Svm{
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "external-uuid",
			},
		},
	}

	// Mock provider setup
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	mockProvider.EXPECT().DeleteLdap(mock.Anything).Return(nil)

	// Execute test
	err := activity.DeleteLDAPConfiguration(ctx, volume, &models.Node{})

	// Assertions
	assert.NoError(t, err)
}

func TestDetermineSmbTeardownContext_NilVolume(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, nil, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.NotNil(t, teardown)
	assert.False(t, teardown.ShouldDelete)
}

func TestDetermineSmbTeardownContext_NilNode(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-1"}}
	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, nil)
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.NotNil(t, teardown)
	assert.False(t, teardown.ShouldDelete)
}

func TestDetermineSmbTeardownContext_NilVolumeAttributes(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: nil,
	}
	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.NotNil(t, teardown)
	assert.False(t, teardown.ShouldDelete)
}

func TestDetermineSmbTeardownContext_NonNASProtocol(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{"ISCSI"},
		},
	}
	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.NotNil(t, teardown)
	assert.False(t, teardown.ShouldDelete)
}

func TestDetermineSmbTeardownContext_GetVolumesError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return(nil, errors.New("db error"))

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.Error(t, err)
	var teardown *SmbTeardownContext
	if val != nil {
		_ = val.Get(&teardown)
	}
	assert.Nil(t, teardown)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_OtherVolumeNil(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume, nil}, nil)

	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)
	dbSvm := &datamodel.Svm{
		SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-external-uuid"},
	}
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(42)).Return(dbSvm, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSGet(mock.Anything).Return(nil, utilErrors.NewNotFoundErr("dns", nil))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.True(t, teardown.ShouldDelete)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_OtherVolumeDeletedAt(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	otherVolume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-2"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	deletedAt := gorm.DeletedAt{Time: time.Now(), Valid: true}
	otherVolume.DeletedAt = &deletedAt
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume, otherVolume}, nil)

	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)
	dbSvm := &datamodel.Svm{
		SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-external-uuid"},
	}
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(42)).Return(dbSvm, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSGet(mock.Anything).Return(nil, utilErrors.NewNotFoundErr("dns", nil))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.True(t, teardown.ShouldDelete)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_OtherVolumeStateDeleted(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	otherVolume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-2"},
		State:     datamodel.LifeCycleStateDeleted,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume, otherVolume}, nil)

	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)
	dbSvm := &datamodel.Svm{
		SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-external-uuid"},
	}
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(42)).Return(dbSvm, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSGet(mock.Anything).Return(nil, utilErrors.NewNotFoundErr("dns", nil))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.True(t, teardown.ShouldDelete)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_OtherVolumeNilAttributes(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	otherVolume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-2"},
		VolumeAttributes: nil,
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume, otherVolume}, nil)

	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)
	dbSvm := &datamodel.Svm{
		SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-external-uuid"},
	}
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(42)).Return(dbSvm, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSGet(mock.Anything).Return(nil, utilErrors.NewNotFoundErr("dns", nil))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.True(t, teardown.ShouldDelete)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_LDAPEnabledNFSVolumePresent(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	volume2 := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-2"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
		},
		Pool: &datamodel.Pool{
			PoolAttributes: &datamodel.PoolAttributes{
				LdapEnabled: true,
			},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume, volume2}, nil)

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.NotNil(t, teardown)
	assert.False(t, teardown.ShouldDelete)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_GetADError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(nil, errors.New("ad error"))

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.Error(t, err)
	var teardown *SmbTeardownContext
	if val != nil {
		_ = val.Get(&teardown)
	}
	assert.Nil(t, teardown)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_EmptyCredentialPath(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.Error(t, err)
	var teardown *SmbTeardownContext
	if val != nil {
		_ = val.Get(&teardown)
	}
	assert.Nil(t, teardown)
	assert.Contains(t, err.Error(), "active directory credential path is empty")
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_GetSvmForPoolIDError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(42)).Return(nil, errors.New("svm error"))

	restClient := ontap_rest.NewMockRESTClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.Error(t, err)
	var teardown *SmbTeardownContext
	if val != nil {
		_ = val.Get(&teardown)
	}
	assert.Nil(t, teardown)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_GetSvmForPoolIDWithDetails(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)
	dbSvm := &datamodel.Svm{
		SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-external-uuid"},
	}
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(42)).Return(dbSvm, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSGet(mock.Anything).Return(nil, utilErrors.NewNotFoundErr("dns", nil))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.True(t, teardown.ShouldDelete)
	assert.Equal(t, "svm-external-uuid", teardown.SvmExternalUUID)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_EmptySvmExternalUUID(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(42)).Return(&datamodel.Svm{}, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.NoError(t, err)
	var teardown *SmbTeardownContext
	_ = val.Get(&teardown)
	assert.False(t, teardown.ShouldDelete)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_GetCifsServerProviderError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		Svm: &datamodel.Svm{
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider error")
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.Error(t, err)
	var teardown *SmbTeardownContext
	if val != nil {
		_ = val.Get(&teardown)
	}
	assert.Nil(t, teardown)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_CreateRESTClientError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		Svm: &datamodel.Svm{
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), createErr: fmt.Errorf("rest client error")}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.Error(t, err)
	var teardown *SmbTeardownContext
	if val != nil {
		_ = val.Get(&teardown)
	}
	assert.Nil(t, teardown)
	mockStorage.AssertExpectations(t)
}

func TestDetermineSmbTeardownContext_DNSGetError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DetermineSmbTeardownContext)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-1"},
		PoolID:    42,
		Svm: &datamodel.Svm{
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolSMB},
		},
	}
	mockStorage.On("GetVolumesByPoolID", mock.Anything, int64(42)).Return([]*datamodel.Volume{volume}, nil)
	ad := &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"}
	mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, int64(42)).Return(ad, nil)

	restClient := ontap_rest.NewMockRESTClient(t)
	nameClient := ontap_rest.NewMockNameServicesClient(t)
	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: restClient}

	restClient.EXPECT().NameServices().Return(nameClient)
	nameClient.EXPECT().DNSGet(mock.Anything).Return(nil, fmt.Errorf("dns error"))

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	val, err := env.ExecuteActivity(activity.DetermineSmbTeardownContext, volume, &models.Node{})
	assert.Error(t, err)
	var teardown *SmbTeardownContext
	if val != nil {
		_ = val.Get(&teardown)
	}
	assert.Nil(t, teardown)
	mockStorage.AssertExpectations(t)
}

func TestDeleteCifsServerIfUnused_NilTeardownCtx(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, nil, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsServerIfUnused_NilNode(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{ShouldDelete: true}
	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, nil)
	assert.NoError(t, err)
}

func TestDeleteCifsServerIfUnused_ShouldDeleteFalse(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{ShouldDelete: false}
	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteCifsServerIfUnused_NilActiveDirectory(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		ActiveDirectory: nil,
		SvmExternalUUID: "svm-uuid",
	}
	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "active directory not provided")
}

func TestDeleteCifsServerIfUnused_EmptyCredentialPath(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		ActiveDirectory: &datamodel.ActiveDirectory{CredentialPath: "", Username: "user"},
		SvmExternalUUID: "svm-uuid",
	}
	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "active directory credential path is empty")
}

func TestDeleteCifsServerIfUnused_GetPasswordError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		ActiveDirectory: &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"},
		SvmExternalUUID: "svm-uuid",
	}

	origGetPwd := vsa.GetPasswordFromCacheOrSecretManager
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		return "", errors.New("password error")
	}
	defer func() { vsa.GetPasswordFromCacheOrSecretManager = origGetPwd }()

	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "password error")
}

func TestDeleteCifsServerIfUnused_GetCifsServerProviderError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		ActiveDirectory: &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"},
		SvmExternalUUID: "svm-uuid",
	}

	origGetPwd := vsa.GetPasswordFromCacheOrSecretManager
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		return "password", nil
	}
	defer func() { vsa.GetPasswordFromCacheOrSecretManager = origGetPwd }()

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider error")
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})
	assert.Error(t, err)
}

func TestDeleteCifsServerIfUnused_EmptySvmExternalUUID(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteCifsServerIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		ActiveDirectory: &datamodel.ActiveDirectory{CredentialPath: "secret", Username: "user"},
		SvmExternalUUID: "",
	}

	origGetPwd := vsa.GetPasswordFromCacheOrSecretManager
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		return "password", nil
	}
	defer func() { vsa.GetPasswordFromCacheOrSecretManager = origGetPwd }()

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t)}, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	_, err := env.ExecuteActivity(activity.DeleteCifsServerIfUnused, teardown, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteDnsRecordIfUnused_NilTeardownCtx(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteDnsRecordIfUnused)

	_, err := env.ExecuteActivity(activity.DeleteDnsRecordIfUnused, nil, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteDnsRecordIfUnused_NilNode(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteDnsRecordIfUnused)

	teardown := &SmbTeardownContext{ShouldDelete: true}
	_, err := env.ExecuteActivity(activity.DeleteDnsRecordIfUnused, teardown, nil)
	assert.NoError(t, err)
}

func TestDeleteDnsRecordIfUnused_ShouldDeleteFalse(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteDnsRecordIfUnused)

	teardown := &SmbTeardownContext{ShouldDelete: false}
	_, err := env.ExecuteActivity(activity.DeleteDnsRecordIfUnused, teardown, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteDnsRecordIfUnused_EmptySvmExternalUUID(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteDnsRecordIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		SvmExternalUUID: "",
	}
	_, err := env.ExecuteActivity(activity.DeleteDnsRecordIfUnused, teardown, &models.Node{})
	assert.NoError(t, err)
}

func TestDeleteDnsRecordIfUnused_GetCifsServerProviderError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteDnsRecordIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		SvmExternalUUID: "svm-uuid",
	}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider error")
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	_, err := env.ExecuteActivity(activity.DeleteDnsRecordIfUnused, teardown, &models.Node{})
	assert.Error(t, err)
}

func TestDeleteDnsRecordIfUnused_CreateRESTClientError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.DeleteDnsRecordIfUnused)

	teardown := &SmbTeardownContext{
		ShouldDelete:    true,
		SvmExternalUUID: "svm-uuid",
	}

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), createErr: fmt.Errorf("rest client error")}

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	_, err := env.ExecuteActivity(activity.DeleteDnsRecordIfUnused, teardown, &models.Node{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rest client error")
}

func TestGetCifsServerProvider_GetProviderByNodeError(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider error")
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	provider, err := getCifsServerProvider(ctx, &models.Node{})
	assert.Error(t, err)
	assert.Nil(t, provider)
}

func TestGetCifsServerProvider_ProviderTypeMismatch(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	originalGetProvider := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return &vsa.MockProvider{}, nil
	}
	defer func() { vsa.GetProviderByNode = originalGetProvider }()

	provider, err := getCifsServerProvider(ctx, &models.Node{})
	assert.Error(t, err)
	assert.Nil(t, provider)
	assert.Contains(t, err.Error(), "provider does not support CIFS operations")
}

func TestDeleteVPGByID_Success(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		PoolID:           1,
		OntapQosPolicyID: "qos-policy-uuid",
	}
	volumes := []*datamodel.Volume{
		{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "ontap-vol-1"}},
	}
	svm := &datamodel.Svm{Name: "vserver-1"}

	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)
	mockProvider.On("UpdateVolume", mock.AnythingOfType("vsa.UpdateVolumeParams")).Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, "vol-1", mock.MatchedBy(func(m map[string]interface{}) bool {
		return m["volume_performance_group_id"] == nil
	})).Return(nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.MatchedBy(func(p vsa.DeleteQoSGroupPolicyParams) bool {
		return p.UUID == "qos-policy-uuid" && p.SvmName == "vserver-1"
	})).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVPGByID_VPGNotFound_ReturnsNil(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(999)
	node := &models.Node{ExternalUUID: "node-uuid"}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(nil, utilErrors.NewNotFoundErr("vpg", nil))

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVPGByID_GetVPGError_ReturnsError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(nil, errors.New("db error"))

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVPGByID_GetVolumesError_ReturnsError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"}, PoolID: 1}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(nil, errors.New("get volumes failed"))

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVPGByID_GetProviderError_ReturnsError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider error")
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"}, PoolID: 1}
	volumes := []*datamodel.Volume{{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "ontap-1"}}}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVPGByID_VolumeWithoutExternalUUID_SkipsONTAPStillUpdatesDB(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		PoolID:           1,
		OntapQosPolicyID: "qos-uuid",
	}
	// Volume with no ExternalUUID: skip ONTAP unassign, still call UpdateVolumeFields
	volumes := []*datamodel.Volume{
		{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, VolumeAttributes: nil},
	}
	svm := &datamodel.Svm{Name: "vserver-1"}

	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, "vol-1", mock.MatchedBy(func(m map[string]interface{}) bool {
		return m["volume_performance_group_id"] == nil
	})).Return(nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.Anything).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVPGByID_UpdateVolumeError_ReturnsError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"}, PoolID: 1}
	volumes := []*datamodel.Volume{
		{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "ontap-1"}},
	}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)
	mockProvider.On("UpdateVolume", mock.AnythingOfType("vsa.UpdateVolumeParams")).Return(errors.New("update volume failed"))

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVPGByID_UpdateVolumeFieldsError_ReturnsError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"}, PoolID: 1}
	volumes := []*datamodel.Volume{
		{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "ontap-1"}},
	}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)
	mockProvider.On("UpdateVolume", mock.AnythingOfType("vsa.UpdateVolumeParams")).Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, "vol-1", mock.Anything).Return(errors.New("update fields failed"))

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVPGByID_GetSvmFails_DeletesQPGWithoutSvmName(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		PoolID:           1,
		OntapQosPolicyID: "qos-uuid",
	}
	volumes := []*datamodel.Volume{}
	svmErr := errors.New("svm not found")
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(nil, svmErr)
	mockProvider.On("DeleteQoSGroupPolicy", mock.MatchedBy(func(p vsa.DeleteQoSGroupPolicyParams) bool {
		return p.UUID == "qos-uuid" && p.SvmName == ""
	})).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVPGByID_DeleteQoSPolicyFails_ContinuesAndDeletesVPG(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		PoolID:           1,
		OntapQosPolicyID: "qos-uuid",
	}
	volumes := []*datamodel.Volume{}
	svm := &datamodel.Svm{Name: "vserver-1"}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.Anything).Return(errors.New("delete qos failed"))
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVPGByID_DereferenceVPGFromDeletedVolumesError_ReturnsError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		PoolID:           1,
		OntapQosPolicyID: "qos-uuid",
	}
	volumes := []*datamodel.Volume{}
	svm := &datamodel.Svm{Name: "vserver-1"}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.Anything).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(errors.New("dereference failed"))

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDeleteVPGByID_HardDeleteNotFound_ReturnsNil(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"}, PoolID: 1}
	volumes := []*datamodel.Volume{}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)
	// GetSvmForPoolID is only called when vpg.OntapQosPolicyID != "" (to delete QPG from ONTAP); this VPG has no OntapQosPolicyID
	mockProvider.On("DeleteQoSGroupPolicy", mock.Anything).Return(nil).Maybe()
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(utilErrors.NewNotFoundErr("vpg", nil))

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteVPGByID_HardDeleteError_ReturnsError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DeleteVPGByID)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		PoolID:           1,
		OntapQosPolicyID: "qos-uuid",
	}
	volumes := []*datamodel.Volume{}
	svm := &datamodel.Svm{Name: "vserver-1"}
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.Anything).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(errors.New("hard delete failed"))

	_, err := env.ExecuteActivity(activity.DeleteVPGByID, vpgID, node)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestDereferencePoolVolumesFromVPGs_Success(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DereferencePoolVolumesFromVPGs)

	poolID := int64(100)
	mockStorage.On("DereferencePoolVolumesFromVPGs", mock.Anything, poolID).Return(int64(3), nil)

	_, err := env.ExecuteActivity(activity.DereferencePoolVolumesFromVPGs, poolID)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDereferencePoolVolumesFromVPGs_StorageError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.DereferencePoolVolumesFromVPGs)

	poolID := int64(100)
	expectedErr := errors.New("database error")
	mockStorage.On("DereferencePoolVolumesFromVPGs", mock.Anything, poolID).Return(int64(0), expectedErr)

	_, err := env.ExecuteActivity(activity.DereferencePoolVolumesFromVPGs, poolID)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_Success(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "autogen-vpg-policy",
		PoolID:           1,
		IsAutoGen:        true,
		OntapQosPolicyID: "qos-policy-uuid",
	}
	svm := &datamodel.Svm{Name: "vserver-1"}
	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(svm, nil)
	// Bug 4: OntapQosPolicyID is the policy UUID; delete must be called with UUID, not Name
	mockProvider.On("DeleteQoSGroupPolicy", mock.MatchedBy(func(p vsa.DeleteQoSGroupPolicyParams) bool {
		return p.UUID == "qos-policy-uuid" && p.Name == "" && p.SvmName == "vserver-1"
	})).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_VolumesStillUsingVPG(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	volumes := []*datamodel.Volume{
		{BaseModel: datamodel.BaseModel{UUID: "vol-1"}},
	}

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(volumes, nil)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_GetVolumesError(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	expectedError := errors.New("failed to get volumes")

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return(nil, expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_VPGNotFound(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	notFoundError := utilErrors.NewNotFoundErr("vpg", nil)

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(nil, notFoundError)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err) // NotFound errors should be handled gracefully
	mockStorage.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_GetVPGError(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	expectedError := errors.New("failed to get VPG")

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(nil, expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_NotAutoGenerated(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		IsAutoGen: false,
	}

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_GetSvmError(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		PoolID:    1,
		IsAutoGen: true,
	}

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err) // GetSvmForPoolID is no longer called, so this test should succeed
	mockStorage.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_NoQosPolicyID(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		PoolID:           1,
		IsAutoGen:        true,
		OntapQosPolicyID: "", // No QoS policy ID
	}

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_GetProviderError(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider")
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		PoolID:           1,
		IsAutoGen:        true,
		OntapQosPolicyID: "qos-policy-uuid",
	}

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err) // Provider error should be logged but not fail the cleanup
	mockStorage.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_DeleteQosPolicyError(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "autogen-vpg-policy",
		PoolID:           1,
		IsAutoGen:        true,
		OntapQosPolicyID: "qos-policy-uuid",
	}
	deleteError := errors.New("failed to delete QoS policy")

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(nil, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.AnythingOfType("vsa.DeleteQoSGroupPolicyParams")).Return(deleteError)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err) // QoS policy deletion error should be logged but not fail the cleanup
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_DereferenceError(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "autogen-vpg-policy",
		PoolID:           1,
		IsAutoGen:        true,
		OntapQosPolicyID: "qos-policy-uuid",
	}
	dereferenceError := errors.New("failed to dereference")

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(nil, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.AnythingOfType("vsa.DeleteQoSGroupPolicyParams")).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(dereferenceError)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(nil)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err) // Dereference error should be logged but not fail the cleanup
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_HardDeleteError(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "autogen-vpg-policy",
		PoolID:           1,
		IsAutoGen:        true,
		OntapQosPolicyID: "qos-policy-uuid",
	}
	hardDeleteError := errors.New("failed to hard delete")

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(nil, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.AnythingOfType("vsa.DeleteQoSGroupPolicyParams")).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(hardDeleteError)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestCleanupAutoGeneratedVPG_HardDeleteNotFound(t *testing.T) {
	// Arrange
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeDeleteActivity{SE: mockStorage}
	env.RegisterActivity(activity.CleanupAutoGeneratedVPG)

	vpgID := int64(1)
	node := &models.Node{ExternalUUID: "node-uuid"}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "autogen-vpg-policy",
		PoolID:           1,
		IsAutoGen:        true,
		OntapQosPolicyID: "qos-policy-uuid",
	}
	notFoundError := utilErrors.NewNotFoundErr("vpg", nil)

	mockStorage.On("GetVolumesByVolumePerformanceGroupID", mock.Anything, vpgID).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetVolumePerformanceGroupByID", mock.Anything, vpgID).Return(vpg, nil)
	mockStorage.On("GetSvmForPoolID", mock.Anything, int64(1)).Return(nil, nil)
	mockProvider.On("DeleteQoSGroupPolicy", mock.AnythingOfType("vsa.DeleteQoSGroupPolicyParams")).Return(nil)
	mockStorage.On("DereferenceVPGFromDeletedVolumes", mock.Anything, vpgID).Return(nil)
	mockStorage.On("HardDeleteVolumePerformanceGroup", mock.Anything, vpg).Return(notFoundError)

	// Act
	_, err := env.ExecuteActivity(activity.CleanupAutoGeneratedVPG, vpgID, node)

	// Assert
	assert.NoError(t, err) // NotFound errors should be handled gracefully
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestEnforceDeleteProtection_NoVolumeAttributes_Skips(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.EnforceDeleteProtection)

	volume := &datamodel.Volume{Name: "vol-1"}

	_, err := env.ExecuteActivity(activity.EnforceDeleteProtection, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestEnforceDeleteProtection_SMBWithRestriction_Denies(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.EnforceDeleteProtection)

	volume := &datamodel.Volume{
		Name: "smb-vol",
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols:         []string{utils.ProtocolSMB},
			RestrictedActions: []string{RestrictedActionDelete},
		},
	}

	_, err := env.ExecuteActivity(activity.EnforceDeleteProtection, volume, &models.Node{})
	assertTemporalApplicationError(t, err, "not supported for SMB", "CustomError", true)
}

func TestEnforceDeleteProtection_SANWithRestriction_NoHostGroup_Allows(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.EnforceDeleteProtection)

	volume := &datamodel.Volume{
		Name: "san-vol",
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols:         []string{utils.ProtocolISCSI},
			RestrictedActions: []string{RestrictedActionDelete},
		},
	}

	_, err := env.ExecuteActivity(activity.EnforceDeleteProtection, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestEnforceDeleteProtection_NFSWithConnectedClients_Denies(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.EnforceDeleteProtection)

	externalUUID := "vol-ext-uuid"
	volume := &datamodel.Volume{
		Name: "nfs-vol",
		Svm:  &datamodel.Svm{Name: "svm-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols:         []string{utils.ProtocolNFSv3},
			RestrictedActions: []string{RestrictedActionDelete},
			ExternalUUID:      externalUUID,
		},
	}

	mockNAS := ontap_rest.NewMockNASClient(t)
	mockNAS.On("NfsClientsGet", mock.MatchedBy(func(p *ontap_rest.NfsClientsGetParams) bool {
		return p != nil && p.VolumeUUID != nil && *p.VolumeUUID == externalUUID && p.SvmName != nil && *p.SvmName == "svm-1"
	})).Return([]*ontap_rest.NfsClients{{}}, nil).Once()

	mockREST := ontap_rest.NewMockRESTClient(t)
	mockREST.EXPECT().NAS().Return(mockNAS).Once()

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: mockREST}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = orig }()

	_, err := env.ExecuteActivity(activity.EnforceDeleteProtection, volume, &models.Node{})
	assertTemporalApplicationError(t, err, "52 hours", "CustomError", true)
}

func TestEnforceDeleteProtection_NFSNoClients_Allows(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.EnforceDeleteProtection)

	volume := &datamodel.Volume{
		Name: "nfs-vol",
		Svm:  &datamodel.Svm{Name: "svm-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols:         []string{utils.ProtocolNFSv3},
			RestrictedActions: []string{RestrictedActionDelete},
			ExternalUUID:      "vol-ext-uuid",
		},
	}

	mockNAS := ontap_rest.NewMockNASClient(t)
	mockNAS.On("NfsClientsGet", mock.Anything).Return([]*ontap_rest.NfsClients{}, nil).Once()

	mockREST := ontap_rest.NewMockRESTClient(t)
	mockREST.EXPECT().NAS().Return(mockNAS).Once()

	fakeProvider := &fakeCifsProvider{MockProvider: vsa.NewMockProvider(t), restClient: mockREST}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, n *models.Node) (vsa.Provider, error) {
		return fakeProvider, nil
	}
	defer func() { vsa.GetProviderByNode = orig }()

	_, err := env.ExecuteActivity(activity.EnforceDeleteProtection, volume, &models.Node{})
	assert.NoError(t, err)
}

func TestEnforceDeleteProtection_SANHostGroup_Denies(t *testing.T) {
	orig := enableSanVolDeleteProtection
	enableSanVolDeleteProtection = true
	t.Cleanup(func() { enableSanVolDeleteProtection = orig })

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.EnforceDeleteProtection)

	volume := &datamodel.Volume{
		Name: "san-vol",
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols:         []string{utils.ProtocolISCSI},
			RestrictedActions: []string{RestrictedActionDelete},
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{{HostGroupUUID: "hg-1"}},
			},
		},
	}

	_, err := env.ExecuteActivity(activity.EnforceDeleteProtection, volume, &models.Node{})
	assertTemporalApplicationError(t, err, "host group", "CustomError", true)
}

func TestEnforceDeleteProtection_NFSNotProvisioned_ReturnsInternalError(t *testing.T) {
	tests := []struct {
		name            string
		volume          *datamodel.Volume
		expectedDetails string
	}{
		{
			name: "missing SVM",
			volume: &datamodel.Volume{
				Name: "nfs-vol",
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols:         []string{utils.ProtocolNFSv3},
					RestrictedActions: []string{RestrictedActionDelete},
					ExternalUUID:      "ext-uuid",
				},
			},
			expectedDetails: "volume SVM is not set",
		},
		{
			name: "missing external UUID",
			volume: &datamodel.Volume{
				Name: "nfs-vol",
				Svm:  &datamodel.Svm{Name: "svm-1"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols:         []string{utils.ProtocolNFSv3},
					RestrictedActions: []string{RestrictedActionDelete},
				},
			},
			expectedDetails: "volume external UUID is not set",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testSuite := &testsuite.WorkflowTestSuite{}
			env := testSuite.NewTestActivityEnvironment()
			activity := VolumeDeleteActivity{}
			env.RegisterActivity(activity.EnforceDeleteProtection)

			_, err := env.ExecuteActivity(activity.EnforceDeleteProtection, tc.volume, &models.Node{})
			assertTemporalInternalError(t, err, []string{tc.expectedDetails}, true)
		})
	}
}

func TestEnforceDeleteProtection_NFSProviderError_Retryable(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeDeleteActivity{}
	env.RegisterActivity(activity.EnforceDeleteProtection)

	volume := &datamodel.Volume{
		Name: "nfs-vol",
		Svm:  &datamodel.Svm{Name: "svm-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols:         []string{utils.ProtocolNFSv3},
			RestrictedActions: []string{RestrictedActionDelete},
			ExternalUUID:      "ext-uuid",
		},
	}

	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider unavailable")
	}
	defer func() { vsa.GetProviderByNode = orig }()

	_, err := env.ExecuteActivity(activity.EnforceDeleteProtection, volume, &models.Node{})
	assertTemporalInternalError(t, err, []string{"provider unavailable"}, true)
}
