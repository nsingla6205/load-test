package kms_activities

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	coreModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/gorm"
)

func TestConfigureKmsForSvmActivity(t *testing.T) {
	t.Run("ConfigureKmsForSvmActivityReturnsErrorWhenProviderNotFound", func(t *testing.T) {
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}

		// Patch activities.GetProviderByNode to return nil
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) {
			return nil, errors.New("provider not found")
		}
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "provider not found")
	})
	t.Run("ConfigureKmsForSvmActivityWhenEmptyKmsConfigId", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: ""}
		// Patch activities.GetProviderByNode to return a dummy provider
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()
		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("ConfigureKmsForSvmActivityReturnsErrorWhenGetKmsConfigFails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		// Patch activities.GetProviderByNode to return a dummy provider
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()
		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(nil, errors.New("db error")).Once()
		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "db error")
	})
	t.Run("ConfigureKmsForSvmActivityReturnsErrorWhenDecryptPasswordFails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}

		origDecryptPassword := utils.DecryptPassword
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return nil, errors.New("decrypt error") }
		defer func() { utils.DecryptPassword = origDecryptPassword }()
		kmsConfig := &datamodel.KmsConfig{ServiceAccount: &datamodel.ServiceAccount{}}
		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()
		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "decrypt error")
	})
	t.Run("ConfigureKmsForSvmActivityReturnsErrorWhenBase64DecodeFails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}

		origDecryptPassword := utils.DecryptPassword
		utils.DecryptPassword = func(_ log.Secret) (*string, error) {
			s := "not-base64"
			return &s, nil
		}
		defer func() { utils.DecryptPassword = origDecryptPassword }()

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()
		kmsConfig := &datamodel.KmsConfig{ServiceAccount: &datamodel.ServiceAccount{}}
		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
	t.Run("ConfigureKmsForSvmActivityReturnsErrorWhenCreateKmsConfigFails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}

		origDecryptPassword := utils.DecryptPassword
		utils.DecryptPassword = func(_ log.Secret) (*string, error) {
			s := base64.StdEncoding.EncodeToString([]byte("key"))
			return &s, nil
		}
		defer func() { utils.DecryptPassword = origDecryptPassword }()
		kmsConfig := &datamodel.KmsConfig{ServiceAccount: &datamodel.ServiceAccount{}, KmsAttributes: &datamodel.KmsAttributes{}}
		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		mockProvider.On("CreateKmsConfig", mock.Anything).Return(nil, errors.New("provider error")).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		// Error is now wrapped by WrapOntapError (classified + Temporal ApplicationError).
		// "provider error" doesn't match any KMS rule, so it falls to domain default ErrKMSConfigureEKM.
		assert.Contains(t, err.Error(), "Error while configuring key manager")
	})
	t.Run("ConfigureKmsForSvmActivityReturnsErrorWhenUpdateSvmWithKmsConfigIDsFails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		resp := &vsa.CreateKmsConfigResponse{}
		kmsConfig := &datamodel.KmsConfig{ServiceAccount: &datamodel.ServiceAccount{}, KmsAttributes: &datamodel.KmsAttributes{}}
		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		mockProvider.On("CreateKmsConfig", mock.Anything).Return(resp, nil).Once()
		mockSE.On("UpdateSvmWithKmsConfigIDs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("update error")).Once()

		origDecryptPassword := utils.DecryptPassword
		utils.DecryptPassword = func(_ log.Secret) (*string, error) {
			s := base64.StdEncoding.EncodeToString([]byte("key"))
			return &s, nil
		}
		defer func() { utils.DecryptPassword = origDecryptPassword }()

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "update error")
	})
	t.Run("ConfigureKmsForSvmActivityReturnsUpdatedSvmOnSuccess", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		resp := &vsa.CreateKmsConfigResponse{}
		kmsConfig := &datamodel.KmsConfig{ServiceAccount: &datamodel.ServiceAccount{}, KmsAttributes: &datamodel.KmsAttributes{}}
		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		mockProvider.On("CreateKmsConfig", mock.Anything).Return(resp, nil).Once()
		mockSE.On("UpdateSvmWithKmsConfigIDs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(svm, nil).Once()
		origDecryptPassword := utils.DecryptPassword
		utils.DecryptPassword = func(_ log.Secret) (*string, error) {
			s := base64.StdEncoding.EncodeToString([]byte("key"))
			return &s, nil
		}
		defer func() { utils.DecryptPassword = origDecryptPassword }()

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("VCPCreatedConfigUsesEmptyPrivilegedAccount", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		resp := &vsa.CreateKmsConfigResponse{}
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{},
			KmsAttributes: &datamodel.KmsAttributes{
				CreationMode:           datamodel.KmsCreationModeVCP,
				VcpServiceAccountEmail: "cmek-usea1-123@cmek-project.iam.gserviceaccount.com",
				SdeServiceAccountEmail: "should-not-be-used@sde.iam.gserviceaccount.com",
			},
		}
		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		// Capture the CreateKmsConfig params to verify PrivilegedAccount is empty
		mockProvider.On("CreateKmsConfig", mock.MatchedBy(func(p vsa.CreateKmsConfigParams) bool {
			return p.PrivilegedAccount == "" // VCP path: no impersonation
		})).Return(resp, nil).Once()
		mockSE.On("UpdateSvmWithKmsConfigIDs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(svm, nil).Once()
		origDecryptPassword := utils.DecryptPassword
		utils.DecryptPassword = func(_ log.Secret) (*string, error) {
			s := base64.StdEncoding.EncodeToString([]byte("key"))
			return &s, nil
		}
		defer func() { utils.DecryptPassword = origDecryptPassword }()

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockProvider.AssertExpectations(t)
	})

	t.Run("SDECreatedConfigUsesSdeServiceAccountAsPrivilegedAccount", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"}
		node := &coreModels.Node{}
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		resp := &vsa.CreateKmsConfigResponse{}
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{},
			KmsAttributes: &datamodel.KmsAttributes{
				SdeServiceAccountEmail: "sde-sa@sde-project.iam.gserviceaccount.com",
				// CreationMode empty = SDE default
			},
		}
		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		// Capture the CreateKmsConfig params to verify PrivilegedAccount is the SDE SA
		mockProvider.On("CreateKmsConfig", mock.MatchedBy(func(p vsa.CreateKmsConfigParams) bool {
			return p.PrivilegedAccount == "sde-sa@sde-project.iam.gserviceaccount.com"
		})).Return(resp, nil).Once()
		mockSE.On("UpdateSvmWithKmsConfigIDs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(svm, nil).Once()
		origDecryptPassword := utils.DecryptPassword
		utils.DecryptPassword = func(_ log.Secret) (*string, error) {
			s := base64.StdEncoding.EncodeToString([]byte("key"))
			return &s, nil
		}
		defer func() { utils.DecryptPassword = origDecryptPassword }()

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.ConfigureKmsForSvmActivity(ctx, svm, node, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockProvider.AssertExpectations(t)
	})
}

func TestCheckVsaKmsConfigReachableActivity(t *testing.T) {
	t.Run("CheckVsaKmsConfigReachableActivityReturnsErrorWhenProviderNotFound", func(t *testing.T) {
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{}
		node := &coreModels.Node{}

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) {
			return nil, errors.New("provider not found")
		}
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		err := mockActivity.CheckVsaKmsConfigReachableActivity(ctx, svm, node)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "provider not found")
	})
	t.Run("CheckVsaKmsConfigReachableActivityReturnsNoErrorWhenKmsIsReachable", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalKmsConfigUUID: "uuid"}}
		node := &coreModels.Node{}

		mockProvider.On("IsGcpKmsReachable", mock.Anything).Return(true, nil).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		err := mockActivity.CheckVsaKmsConfigReachableActivity(ctx, svm, node)
		assert.NoError(t, err)
	})
	t.Run("CheckVsaKmsConfigReachableActivityReturnsPermissionDeniedError", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalKmsConfigUUID: "uuid"}}
		node := &coreModels.Node{}

		mockProvider.On("IsGcpKmsReachable", mock.Anything).Return(false, errors.New("permission_denied")).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		err := mockActivity.CheckVsaKmsConfigReachableActivity(ctx, svm, node)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "lacks permission")
	})
	t.Run("CheckVsaKmsConfigReachableActivityReturnsInvalidJwtSignatureError", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalKmsConfigUUID: "uuid"}}
		node := &coreModels.Node{}

		mockProvider.On("IsGcpKmsReachable", mock.Anything).Return(false, errors.New("Invalid JWT Signature")).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		err := mockActivity.CheckVsaKmsConfigReachableActivity(ctx, svm, node)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Failed to establish connectivity")
	})
	t.Run("CheckVsaKmsConfigReachableActivityReturnsNonRetryableErrorForOtherErrors", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalKmsConfigUUID: "uuid"}}
		node := &coreModels.Node{}

		mockProvider.On("IsGcpKmsReachable", mock.Anything).Return(false, errors.New("some other error")).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		err := mockActivity.CheckVsaKmsConfigReachableActivity(ctx, svm, node)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "GCP KMS key is not reachable from VSA Clusters")
	})
}

func TestRekeyKmsForSvmActivity(t *testing.T) {
	svmWithEkm := func() *datamodel.Svm {
		return &datamodel.Svm{
			Name:       "svm1",
			SvmDetails: &datamodel.SvmDetails{ExternalKmsConfigUUID: "ext-uuid"},
		}
	}

	t.Run("ReturnsErrorWhenProviderNotFound", func(tt *testing.T) {
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) {
			return nil, errors.New("provider not found")
		}
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.RekeyKmsForSvmActivity(ctx, svmWithEkm(), &coreModels.Node{}, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "provider not found")
	})

	t.Run("ReturnsSvmWhenKmsConfigIdEmpty", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := svmWithEkm()
		params := commonparams.CreatePoolParams{KmsConfigId: ""}

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.RekeyKmsForSvmActivity(ctx, svm, &coreModels.Node{}, params)
		assert.NoError(tt, err)
		assert.Equal(tt, svm, result)
	})

	t.Run("ReturnsErrorWhenSvmHasNoExistingEkm", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{Name: "svm1"} // no SvmDetails
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.RekeyKmsForSvmActivity(ctx, svm, &coreModels.Node{}, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "does not have an existing external KMS configuration")
	})

	t.Run("ReturnsErrorWhenGetKmsConfigFails", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}

		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(nil, errors.New("db error")).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.RekeyKmsForSvmActivity(ctx, svmWithEkm(), &coreModels.Node{}, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "db error")
	})

	t.Run("ReturnsErrorWhenRekeyGcpKmsFails", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "kms-uuid"},
			KeyName:         "target-key",
			KeyRing:         "target-ring",
			KeyRingLocation: "us-east4",
			KeyProjectID:    "target-project",
		}

		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		mockProvider.On("GetOntapGcpKmsConfig", "ext-uuid").Return(&vsa.GcpKmsHierarchy{
			ProjectID:       "other-project",
			KeyRingName:     "other-ring",
			KeyRingLocation: "us-east4",
			KeyName:         "other-key",
		}, nil).Once()
		mockProvider.On("RekeyGcpKms", mock.Anything).Return(errors.New("rekey provider error")).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.RekeyKmsForSvmActivity(ctx, svmWithEkm(), &coreModels.Node{}, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenUpdateSvmWithKmsConfigIDsFails", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "kms-uuid"},
			KeyName:         "target-key",
			KeyRing:         "target-ring",
			KeyRingLocation: "us-east4",
			KeyProjectID:    "target-project",
		}

		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		mockProvider.On("GetOntapGcpKmsConfig", "ext-uuid").Return(&vsa.GcpKmsHierarchy{
			ProjectID:       "other-project",
			KeyRingName:     "other-ring",
			KeyRingLocation: "us-east4",
			KeyName:         "other-key",
		}, nil).Once()
		mockProvider.On("RekeyGcpKms", mock.Anything).Return(nil).Once()
		mockSE.On("UpdateSvmWithKmsConfigIDs", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("update error")).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.RekeyKmsForSvmActivity(ctx, svmWithEkm(), &coreModels.Node{}, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "update error")
	})

	t.Run("ReturnsUpdatedSvmOnSuccess", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "kms-uuid"},
			KeyName:         "target-key",
			KeyRing:         "target-ring",
			KeyRingLocation: "us-east4",
			KeyProjectID:    "target-project",
		}
		updatedSvm := svmWithEkm()

		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		mockProvider.On("GetOntapGcpKmsConfig", "ext-uuid").Return(&vsa.GcpKmsHierarchy{
			ProjectID:       "other-project",
			KeyRingName:     "other-ring",
			KeyRingLocation: "us-east4",
			KeyName:         "other-key",
		}, nil).Once()
		// Verify the rekey reuses the SVM's existing external config UUID and target key coordinates.
		mockProvider.On("RekeyGcpKms", mock.MatchedBy(func(p vsa.RekeyGcpKmsParams) bool {
			return p.ExternalKmsConfigID == "ext-uuid" &&
				p.KeyName == "target-key" &&
				p.KeyRingName == "target-ring" &&
				p.KeyRingLocation == "us-east4" &&
				p.ProjectID == "target-project"
		})).Return(nil).Once()
		mockSE.On("UpdateSvmWithKmsConfigIDs", mock.Anything, mock.Anything, "kms-uuid", "ext-uuid").Return(updatedSvm, nil).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.RekeyKmsForSvmActivity(ctx, svmWithEkm(), &coreModels.Node{}, params)
		assert.NoError(tt, err)
		assert.Equal(tt, updatedSvm, result)
		mockProvider.AssertExpectations(tt)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SkipsRekeyWhenOntapHierarchyAlreadyMatchesTarget", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		svm := svmWithEkm()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "kms-uuid"},
			KeyName:         "key-east4",
			KeyRing:         "kr-east4",
			KeyRingLocation: "us-east4",
			KeyProjectID:    "g1p-astral-tst-host-01",
		}
		updatedSvm := svmWithEkm()

		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		mockProvider.On("GetOntapGcpKmsConfig", "ext-uuid").Return(&vsa.GcpKmsHierarchy{
			ProjectID:       "g1p-astral-tst-host-01",
			KeyRingName:     "kr-east4",
			KeyRingLocation: "us-east4",
			KeyName:         "projects/g1p-astral-tst-host-01/locations/us-east4/keyRings/kr-east4/cryptoKeys/key-east4",
		}, nil).Once()
		mockSE.On("UpdateSvmWithKmsConfigIDs", mock.Anything, svm, "kms-uuid", "ext-uuid").Return(updatedSvm, nil).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.RekeyKmsForSvmActivity(ctx, svm, &coreModels.Node{}, params)
		assert.NoError(tt, err)
		assert.Equal(tt, updatedSvm, result)
		mockProvider.AssertNotCalled(tt, "RekeyGcpKms", mock.Anything)
		mockProvider.AssertExpectations(tt)
		mockSE.AssertExpectations(tt)
	})

	t.Run("FallsBackToRekeyWhenOntapHierarchyLookupFails", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockSE := database.NewMockStorage(t)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		ctx := context.Background()
		params := commonparams.CreatePoolParams{KmsConfigId: "kms-uuid"}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: "kms-uuid"},
			KeyName:         "target-key",
			KeyRing:         "target-ring",
			KeyRingLocation: "us-east4",
			KeyProjectID:    "target-project",
		}
		updatedSvm := svmWithEkm()

		mockSE.On("GetKmsConfig", mock.Anything, mock.Anything).Return(kmsConfig, nil).Once()
		mockProvider.On("GetOntapGcpKmsConfig", "ext-uuid").Return(nil, errors.New("lookup failed")).Once()
		mockProvider.On("RekeyGcpKms", mock.Anything).Return(nil).Once()
		mockSE.On("UpdateSvmWithKmsConfigIDs", mock.Anything, mock.Anything, "kms-uuid", "ext-uuid").Return(updatedSvm, nil).Once()
		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) { return mockProvider, nil }
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		result, err := mockActivity.RekeyKmsForSvmActivity(ctx, svmWithEkm(), &coreModels.Node{}, params)
		assert.NoError(tt, err)
		assert.Equal(tt, updatedSvm, result)
		mockProvider.AssertExpectations(tt)
		mockSE.AssertExpectations(tt)
	})
}

func TestFinalizePreviousKmsConfigStateActivity(t *testing.T) {
	t.Run("ReturnsNilWhenPreviousKmsConfigIDIsZero", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockActivity := &KmsConfigActivity{SE: mockSE}

		err := mockActivity.FinalizePreviousKmsConfigStateActivity(context.Background(), 0)
		assert.NoError(tt, err)
		// No storage calls should be made for a zero (absent) previous config.
		mockSE.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenGetMultipleKmsConfigsFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockActivity := &KmsConfigActivity{SE: mockSE}

		mockSE.On("GetMultipleKmsConfigs", mock.Anything, mock.Anything).Return(nil, errors.New("db error")).Once()

		err := mockActivity.FinalizePreviousKmsConfigStateActivity(context.Background(), 42)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "db error")
	})

	t.Run("ReturnsNilWhenPreviousKmsConfigNoLongerExists", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockActivity := &KmsConfigActivity{SE: mockSE}

		mockSE.On("GetMultipleKmsConfigs", mock.Anything, mock.Anything).Return([]*datamodel.KmsConfig{}, nil).Once()

		err := mockActivity.FinalizePreviousKmsConfigStateActivity(context.Background(), 42)
		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenIsKmsConfigInUseFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		previous := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{ID: 42, UUID: "prev-uuid"}}

		mockSE.On("GetMultipleKmsConfigs", mock.Anything, mock.Anything).Return([]*datamodel.KmsConfig{previous}, nil).Once()
		mockSE.On("IsKmsConfigInUse", mock.Anything, "prev-uuid").Return(false, errors.New("in-use error")).Once()

		err := mockActivity.FinalizePreviousKmsConfigStateActivity(context.Background(), 42)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "in-use error")
	})

	t.Run("KeepsPreviousConfigInUseWhenOtherPoolsReferenceIt", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		previous := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{ID: 42, UUID: "prev-uuid"}}

		mockSE.On("GetMultipleKmsConfigs", mock.Anything, mock.Anything).Return([]*datamodel.KmsConfig{previous}, nil).Once()
		mockSE.On("IsKmsConfigInUse", mock.Anything, "prev-uuid").Return(true, nil).Once()
		mockSE.On("UpdateKmsConfigState", mock.Anything, "prev-uuid", datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails).Return(previous, nil).Once()

		err := mockActivity.FinalizePreviousKmsConfigStateActivity(context.Background(), 42)
		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("MovesPreviousConfigToReadyWhenNoPoolsReferenceIt", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		previous := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{ID: 42, UUID: "prev-uuid"}}

		mockSE.On("GetMultipleKmsConfigs", mock.Anything, mock.Anything).Return([]*datamodel.KmsConfig{previous}, nil).Once()
		mockSE.On("IsKmsConfigInUse", mock.Anything, "prev-uuid").Return(false, nil).Once()
		mockSE.On("UpdateKmsConfigState", mock.Anything, "prev-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(previous, nil).Once()

		err := mockActivity.FinalizePreviousKmsConfigStateActivity(context.Background(), 42)
		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenUpdateKmsConfigStateFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		mockActivity := &KmsConfigActivity{SE: mockSE}
		previous := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{ID: 42, UUID: "prev-uuid"}}

		mockSE.On("GetMultipleKmsConfigs", mock.Anything, mock.Anything).Return([]*datamodel.KmsConfig{previous}, nil).Once()
		mockSE.On("IsKmsConfigInUse", mock.Anything, "prev-uuid").Return(false, nil).Once()
		mockSE.On("UpdateKmsConfigState", mock.Anything, "prev-uuid", mock.Anything, mock.Anything).Return(nil, errors.New("state error")).Once()

		err := mockActivity.FinalizePreviousKmsConfigStateActivity(context.Background(), 42)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "state error")
	})
}

func TestGetOntapRestProviderForPoolActivity(t *testing.T) {
	t.Run("WhenGetOntapRestProviderForPoolReturnsError", func(t *testing.T) {
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		pool := &datamodel.Pool{}

		origGetOntapRestProviderForPool := _getOntapRestProviderForPool
		getOntapRestProviderForPool = func(_ context.Context, _ database.Storage, _ *datamodel.Pool) (vsa.Provider, error) {
			return nil, errors.New("Unable to provision provider")
		}
		defer func() { getOntapRestProviderForPool = origGetOntapRestProviderForPool }()

		provider, err := mockActivity.GetOntapRestProviderForPoolActivity(ctx, pool)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Unable to provision provider")
		assert.Nil(t, provider)
	})
	t.Run("WhenGetOntapRestProviderForPoolReturnsProvider", func(t *testing.T) {
		mockActivity := &KmsConfigActivity{}
		ctx := context.Background()
		pool := &datamodel.Pool{}
		providerVSA := vsa.NewProvider(ctx, vsa.ProviderDetails{})

		getOntapRestProviderForPool = func(_ context.Context, _ database.Storage, _ *datamodel.Pool) (vsa.Provider, error) {
			return providerVSA, nil
		}
		defer func() { getOntapRestProviderForPool = _getOntapRestProviderForPool }()

		provider, err := mockActivity.GetOntapRestProviderForPoolActivity(ctx, pool)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})
}

func TestDeleteEkmConfigActivity(t *testing.T) {
	t.Run("WhenProviderNotFound", func(tt *testing.T) {
		kmsConfigActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{}
		node := &coreModels.Node{}

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) {
			return nil, errors.New("provider not found")
		}
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		err := kmsConfigActivity.DeleteEkmConfigActivity(ctx, node, svm)
		assert.Error(tt, err)
		assert.Errorf(tt, err, "provider not found")
	})
	t.Run("WhenSvmDetailsIsNil", func(tt *testing.T) {
		kmsConfigActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{}
		node := &coreModels.Node{}
		mockProvider := new(vsa.MockProvider)

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()
		err := kmsConfigActivity.DeleteEkmConfigActivity(ctx, node, svm)
		assert.Error(tt, err)
		assert.Errorf(tt, err, "Unable to determine External-UUID of EKM since SvmDetails field of Svm DataModel is nil")
	})
	t.Run("WhenDeleteEkmConfigReturnsError", func(tt *testing.T) {
		kmsConfigActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalKmsConfigUUID: "externalUUID1"}}
		node := &coreModels.Node{}
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("DeleteEkmConfig", mock.Anything).Return(errors.New("ekm deletion failed"))

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

		err := kmsConfigActivity.DeleteEkmConfigActivity(ctx, node, svm)

		assert.Error(tt, err)
		assert.Errorf(tt, err, "ekm deletion failed")
	})
	t.Run("WhenActivityIsSuccessful", func(tt *testing.T) {
		kmsConfigActivity := &KmsConfigActivity{}
		ctx := context.Background()
		svm := &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalKmsConfigUUID: "externalUUID1"}}
		node := &coreModels.Node{}
		mockProvider := new(vsa.MockProvider)

		origGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(_ context.Context, _ *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = origGetProviderByNode }()
		mockProvider.On("DeleteEkmConfig", mock.Anything).Return(nil)

		err := kmsConfigActivity.DeleteEkmConfigActivity(ctx, node, svm)

		assert.NoError(tt, err)
		assert.Nil(tt, err)
	})
}

func TestGetOntapRestProviderForPool(t *testing.T) {
	ctx := context.TODO()

	t.Run("GetOntapRestProviderForPool_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "abcd",
			},
		}
		node := &datamodel.Node{
			EndpointAddress: "1.2.3.4",
		}
		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return([]*datamodel.Node{node}, nil)

		// Patch activities.GetProviderByNode to return a mock provider
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		mockProvider := new(vsa.MockProvider)
		vsa.GetProviderByNode = func(ctx context.Context, node *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		provider, err := getOntapRestProviderForPool(ctx, mockStorage, pool)
		assert.NoError(t, err)
		assert.Equal(t, mockProvider, provider)
		mockStorage.AssertExpectations(t)
	})
	t.Run("GetOntapRestProviderForPool_PoolNotFoundError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}

		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return(nil, gorm.ErrRecordNotFound)
		provider, err := getOntapRestProviderForPool(ctx, mockStorage, pool)
		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
	t.Run("GetOntapRestProviderForPool_CouldNotGetNodes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}

		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return(nil, errors.New("could not get nodes"))
		provider, err := getOntapRestProviderForPool(ctx, mockStorage, pool)
		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}
