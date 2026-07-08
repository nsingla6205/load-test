package flexcache_activities

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	ontaprestmodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/flexcache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vcputils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

const testFlexCacheVolumeExternalUUID = "external-uuid"

func makeResult(vol *datamodel.Volume, workflowID string) *flexcache.CreateFlexCacheResult {
	return &flexcache.CreateFlexCacheResult{
		DBVolume: vol,
		JobInput: &flexcache.JobActivityInput{
			ResourceUUID: vol.UUID,
			ResourceName: vol.Name,
			WorkflowID:   workflowID,
			AccountID:    vol.AccountID,
		},
	}
}

func minimalJob(uuid string, state string) *datamodel.Job {
	return &datamodel.Job{
		WorkflowID: "wf-id",
		BaseModel:  datamodel.BaseModel{UUID: uuid},
		State:      state,
	}
}

func allowAnyLogs(logger *log.MockLogger) {
	// Support variable arg counts for formatted log methods (2..8 args: format + N substitutions)
	for n := 2; n <= 8; n++ {
		args := make([]interface{}, n)
		for i := range args {
			args[i] = mock.Anything
		}
		logger.On("Debugf", args...).Maybe()
		logger.On("Errorf", args...).Maybe()
		logger.On("Warnf", args...).Maybe()
		logger.On("Infof", args...).Maybe()
	}
	// Non-formatted variants
	logger.On("Warn", mock.Anything).Maybe()
	logger.On("Debug", mock.Anything).Maybe()
}

func TestFlexCacheVolumeCreateActivity_CreateFlexCacheVolumeInOntap(t *testing.T) {
	dbVolume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "flexcache-create-vol-uuid"},
		Svm:       &datamodel.Svm{Name: "svm-name"},
		Account:   &datamodel.Account{Name: "account-name"},
		CacheParameters: &datamodel.CacheParameters{
			PeerSvmName:     "peer-svm",
			PeerVolumeName:  "peer-volume",
			PeerClusterName: "peer-cluster",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "policyName",
				},
			},
		},
	}

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		mockSE.On("UpdateVolumeFields", mock.Anything, dbVolume.UUID, mock.MatchedBy(func(u map[string]interface{}) bool {
			va, ok := u["volume_attributes"].(*datamodel.VolumeAttributes)
			return ok && va != nil && va.ExternalUUID == testFlexCacheVolumeExternalUUID
		})).Return(nil).Once()
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}
		ctx := context.Background()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolume}

		volumeResp := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{Name: "volume-name", ExternalUUID: testFlexCacheVolumeExternalUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateFlexCacheVolume(mock.Anything).Return(volumeResp, nil)
		logger.EXPECT().Debug("flexcache volume created successfully")

		newResult, err := activity.CreateFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, "volume-name", newResult.VolumeResponse.Name)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolume}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		_, err := activity.CreateFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
	})

	t.Run("WhenCreateFlexCacheVolumeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolume}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateFlexCacheVolume(mock.Anything).Return(nil, assert.AnError)

		_, err := activity.CreateFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
	})

	t.Run("WhenUpdateVolumeFieldsFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}
		ctx := context.Background()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolume}

		volumeResp := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{Name: "volume-name", ExternalUUID: testFlexCacheVolumeExternalUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateFlexCacheVolume(mock.Anything).Return(volumeResp, nil)
		logger.EXPECT().Debug("flexcache volume created successfully")
		mockSE.On("UpdateVolumeFields", mock.Anything, dbVolume.UUID, mock.Anything).Return(assert.AnError).Once()

		_, err := activity.CreateFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SuccessWithCacheConfig", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		ctx := context.Background()

		writebackEnabled := true
		atimeScrubEnabled := true
		atimeScrubDays := int16(30)
		cifsChangeNotifyEnabled := false

		dbVolumeWithCacheConfig := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-cache-config"},
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:     "peer-svm",
				PeerVolumeName:  "peer-volume",
				PeerClusterName: "peer-cluster",
				CacheConfig: &datamodel.CacheConfig{
					WritebackEnabled:        &writebackEnabled,
					AtimeScrubEnabled:       &atimeScrubEnabled,
					AtimeScrubDays:          &atimeScrubDays,
					CifsChangeNotifyEnabled: &cifsChangeNotifyEnabled,
				},
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				FileProperties: &datamodel.FileProperties{
					ExportPolicy: &datamodel.ExportPolicy{
						ExportPolicyName: "policyName",
					},
				},
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolumeWithCacheConfig}

		mockSE.On("UpdateVolumeFields", mock.Anything, dbVolumeWithCacheConfig.UUID, mock.MatchedBy(func(u map[string]interface{}) bool {
			va, ok := u["volume_attributes"].(*datamodel.VolumeAttributes)
			return ok && va != nil && va.ExternalUUID == testFlexCacheVolumeExternalUUID
		})).Return(nil).Once()
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}

		volumeResp := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{Name: "volume-name", ExternalUUID: testFlexCacheVolumeExternalUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateFlexCacheVolume(mock.MatchedBy(func(params vsa.CreateFlexCacheVolumeParams) bool {
			return params.WritebackEnabled != nil && *params.WritebackEnabled == true &&
				params.AtimeScrubEnabled != nil && *params.AtimeScrubEnabled == true &&
				params.AtimeScrubDays != nil && *params.AtimeScrubDays == int16(30) &&
				params.CifsChangeNotifyEnabled != nil && *params.CifsChangeNotifyEnabled == false
		})).Return(volumeResp, nil)
		logger.EXPECT().Debug("flexcache volume created successfully")

		newResult, err := activity.CreateFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, "volume-name", newResult.VolumeResponse.Name)
	})

	t.Run("SuccessWithPartialCacheConfig", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		ctx := context.Background()

		writebackEnabled := true

		dbVolumeWithPartialCacheConfig := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-partial-cache"},
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:     "peer-svm",
				PeerVolumeName:  "peer-volume",
				PeerClusterName: "peer-cluster",
				CacheConfig: &datamodel.CacheConfig{
					WritebackEnabled: &writebackEnabled,
					// Other fields are nil
				},
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				FileProperties: &datamodel.FileProperties{
					ExportPolicy: &datamodel.ExportPolicy{
						ExportPolicyName: "policyName",
					},
				},
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolumeWithPartialCacheConfig}

		mockSE.On("UpdateVolumeFields", mock.Anything, dbVolumeWithPartialCacheConfig.UUID, mock.MatchedBy(func(u map[string]interface{}) bool {
			va, ok := u["volume_attributes"].(*datamodel.VolumeAttributes)
			return ok && va != nil && va.ExternalUUID == testFlexCacheVolumeExternalUUID
		})).Return(nil).Once()
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}

		volumeResp := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{Name: "volume-name", ExternalUUID: testFlexCacheVolumeExternalUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateFlexCacheVolume(mock.MatchedBy(func(params vsa.CreateFlexCacheVolumeParams) bool {
			return params.WritebackEnabled != nil && *params.WritebackEnabled == true &&
				params.AtimeScrubEnabled == nil &&
				params.AtimeScrubDays == nil &&
				params.CifsChangeNotifyEnabled == nil
		})).Return(volumeResp, nil)
		logger.EXPECT().Debug("flexcache volume created successfully")

		newResult, err := activity.CreateFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, "volume-name", newResult.VolumeResponse.Name)
	})

	t.Run("SuccessWithNilCacheConfig", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		ctx := context.Background()

		dbVolumeWithNilCacheConfig := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-nil-cache"},
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:     "peer-svm",
				PeerVolumeName:  "peer-volume",
				PeerClusterName: "peer-cluster",
				CacheConfig:     nil, // Explicitly nil
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				FileProperties: &datamodel.FileProperties{
					ExportPolicy: &datamodel.ExportPolicy{
						ExportPolicyName: "policyName",
					},
				},
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolumeWithNilCacheConfig}

		mockSE.On("UpdateVolumeFields", mock.Anything, dbVolumeWithNilCacheConfig.UUID, mock.MatchedBy(func(u map[string]interface{}) bool {
			va, ok := u["volume_attributes"].(*datamodel.VolumeAttributes)
			return ok && va != nil && va.ExternalUUID == testFlexCacheVolumeExternalUUID
		})).Return(nil).Once()
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}

		volumeResp := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{Name: "volume-name", ExternalUUID: testFlexCacheVolumeExternalUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateFlexCacheVolume(mock.MatchedBy(func(params vsa.CreateFlexCacheVolumeParams) bool {
			// When CacheConfig is nil, all config fields should be nil
			return params.WritebackEnabled == nil &&
				params.AtimeScrubEnabled == nil &&
				params.AtimeScrubDays == nil &&
				params.CifsChangeNotifyEnabled == nil
		})).Return(volumeResp, nil)
		logger.EXPECT().Debug("flexcache volume created successfully")

		newResult, err := activity.CreateFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, "volume-name", newResult.VolumeResponse.Name)
	})

	t.Run("SuccessWithGlobalFileLockEnabled", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		ctx := context.Background()

		globalFileLock := true

		dbVolumeWithGlobalFileLock := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-global-lock"},
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:          "peer-svm",
				PeerVolumeName:       "peer-volume",
				PeerClusterName:      "peer-cluster",
				EnableGlobalFileLock: &globalFileLock,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				FileProperties: &datamodel.FileProperties{
					ExportPolicy: &datamodel.ExportPolicy{
						ExportPolicyName: "policyName",
					},
				},
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolumeWithGlobalFileLock}

		mockSE.On("UpdateVolumeFields", mock.Anything, dbVolumeWithGlobalFileLock.UUID, mock.MatchedBy(func(u map[string]interface{}) bool {
			va, ok := u["volume_attributes"].(*datamodel.VolumeAttributes)
			return ok && va != nil && va.ExternalUUID == testFlexCacheVolumeExternalUUID
		})).Return(nil).Once()
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}

		volumeResp := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{Name: "volume-name", ExternalUUID: testFlexCacheVolumeExternalUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateFlexCacheVolume(mock.MatchedBy(func(params vsa.CreateFlexCacheVolumeParams) bool {
			return params.GlobalFileLockingEnabled != nil && *params.GlobalFileLockingEnabled == true
		})).Return(volumeResp, nil)
		logger.EXPECT().Debug("flexcache volume created successfully")

		newResult, err := activity.CreateFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, "volume-name", newResult.VolumeResponse.Name)
	})

	t.Run("SuccessWithAllCacheConfigAndGlobalFileLock", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		ctx := context.Background()

		writebackEnabled := true
		atimeScrubEnabled := true
		atimeScrubDays := int16(60)
		cifsChangeNotifyEnabled := true
		globalFileLock := true

		dbVolumeWithAllConfig := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-all-config"},
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:          "peer-svm",
				PeerVolumeName:       "peer-volume",
				PeerClusterName:      "peer-cluster",
				EnableGlobalFileLock: &globalFileLock,
				CacheConfig: &datamodel.CacheConfig{
					WritebackEnabled:        &writebackEnabled,
					AtimeScrubEnabled:       &atimeScrubEnabled,
					AtimeScrubDays:          &atimeScrubDays,
					CifsChangeNotifyEnabled: &cifsChangeNotifyEnabled,
				},
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				FileProperties: &datamodel.FileProperties{
					JunctionPath: "/vol/test",
					ExportPolicy: &datamodel.ExportPolicy{
						ExportPolicyName: "policyName",
					},
				},
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolumeWithAllConfig}

		mockSE.On("UpdateVolumeFields", mock.Anything, dbVolumeWithAllConfig.UUID, mock.MatchedBy(func(u map[string]interface{}) bool {
			va, ok := u["volume_attributes"].(*datamodel.VolumeAttributes)
			return ok && va != nil && va.ExternalUUID == testFlexCacheVolumeExternalUUID
		})).Return(nil).Once()
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}

		volumeResp := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{Name: "volume-name", ExternalUUID: testFlexCacheVolumeExternalUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateFlexCacheVolume(mock.MatchedBy(func(params vsa.CreateFlexCacheVolumeParams) bool {
			return params.Name == dbVolumeWithAllConfig.Name &&
				params.OriginSVMName == "peer-svm" &&
				params.OriginVolumeName == "peer-volume" &&
				params.GlobalFileLockingEnabled != nil && *params.GlobalFileLockingEnabled == true &&
				params.WritebackEnabled != nil && *params.WritebackEnabled == true &&
				params.AtimeScrubEnabled != nil && *params.AtimeScrubEnabled == true &&
				params.AtimeScrubDays != nil && *params.AtimeScrubDays == int16(60) &&
				params.CifsChangeNotifyEnabled != nil && *params.CifsChangeNotifyEnabled == true &&
				params.JunctionPath != nil && *params.JunctionPath == "/vol/test" &&
				params.ExportPolicy != nil && *params.ExportPolicy == "policyName"
		})).Return(volumeResp, nil)
		logger.EXPECT().Debug("flexcache volume created successfully")

		newResult, err := activity.CreateFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, "volume-name", newResult.VolumeResponse.Name)
	})
}

func TestFlexCacheVolumeCreateActivity_VerifyVolumeEncryptionActivity(t *testing.T) {
	dbVolume := &datamodel.Volume{
		Svm:     &datamodel.Svm{Name: "svm-name"},
		Account: &datamodel.Account{Name: "account-name"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "volume-external-uuid",
			FileProperties: &datamodel.FileProperties{},
		},
	}

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolume}

		enabled := true
		volumeResp := &vsa.VolumeResponse{
			Encryption: vsa.Encryption{Enabled: &enabled},
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetVolumeEncryptionStatus(mock.MatchedBy(func(params vsa.GetVolumeParams) bool {
			return params.UUID == "volume-external-uuid"
		})).Return(volumeResp, nil)
		logger.EXPECT().Debug("flexcache volume encryption verified successfully")

		newResult, err := activity.VerifyVolumeEncryptionActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcacheResult, newResult)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolume}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		_, err := activity.VerifyVolumeEncryptionActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
	})

	t.Run("WhenGetVolumeEncryptionStatusFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolume}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetVolumeEncryptionStatus(mock.Anything).Return(nil, assert.AnError)

		_, err := activity.VerifyVolumeEncryptionActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
	})

	t.Run("WhenEncryptionIsDisabled", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolume}

		enabled := false
		volumeResp := &vsa.VolumeResponse{
			Encryption: vsa.Encryption{Enabled: &enabled},
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetVolumeEncryptionStatus(mock.Anything).Return(volumeResp, nil)

		_, err := activity.VerifyVolumeEncryptionActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Origin volume is not encrypted")
	})

	t.Run("WhenEncryptionEnabledIsNil", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: dbVolume}

		volumeResp := &vsa.VolumeResponse{
			Encryption: vsa.Encryption{Enabled: nil},
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetVolumeEncryptionStatus(mock.Anything).Return(volumeResp, nil)
		logger.EXPECT().Debug("flexcache volume encryption verified successfully")

		newResult, err := activity.VerifyVolumeEncryptionActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcacheResult, newResult)
	})
}

func TestFlexCacheVolumeCreateActivity_CreateClusterPeerInOntapActivity(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}
		ctx := context.Background()
		logger := log.NewMockLogger(tt)
		expiry := time.Now().Add(time.Minute * 30)
		pass := log.Secret("peer-pass")
		expDT := strfmt.DateTime(expiry)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-cluster-peer"},
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerIpAddresses:   []string{"10.0.0.1", "10.0.0.2"},
				PeerClusterName:   "peer-cluster",
				PeerSvmName:       "peer-svm",
				CommandExpiryTime: &expiry,
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: &datamodel.ClusterPeerings{BaseModel: datamodel.BaseModel{ID: 7}},
		}
		clusterPeer := &vsa.ClusterPeer{
			UUID:            "cluster-peer-uuid",
			ExternalUUID:    "ext-peer-uuid",
			Passphrase:      &pass,
			ExpiryTime:      &expDT,
			PeerClusterName: "peer-cluster",
		}
		interclusterLifs := []*vsa.InterclusterLif{{Address: ontaprestmodel.IPAddress("10.1.0.1")}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger).Times(4)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil).Times(2)

		mockProvider.EXPECT().CreateRole(mock.MatchedBy(func(params vsa.CreateRoleParams) bool {
			return params.Name == activities.OnPremPeerRoleName
		})).Return("role-location", nil)
		logger.EXPECT().Infof("Successfully created role %s", activities.OnPremPeerRoleName)

		mockProvider.EXPECT().CreateClusterPeer(mock.MatchedBy(func(params vsa.CreateClusterPeerParams) bool {
			return params.PeerName == "peer-cluster" &&
				len(params.PeerAddresses) == 2 &&
				params.PeerAddresses[0] == "10.0.0.1" &&
				params.PeerAddresses[1] == "10.0.0.2" &&
				params.IPSpace == activities.IpSpace &&
				params.LocalRole != nil &&
				*params.LocalRole == activities.OnPremPeerRoleName &&
				params.ExpiryTime != nil
		})).Return(clusterPeer, nil)
		logger.EXPECT().Infof("cluster peer created successfully with UUID: %s", clusterPeer.ExternalUUID)

		mockProvider.EXPECT().GetInterclusterLIFs(vsa.InterclusterServicePolicyName).Return(interclusterLifs, nil)
		mockSE.On("UpdateVolumeFields", ctx, vol.UUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			return updates["cache_parameters"] != nil
		})).Return(nil).Once()
		mockSE.On("UpdateClusterPeeringRow", ctx, mock.Anything).Return(nil).Once()
		logger.EXPECT().Debug("cluster peer command updated successfully")
		logger.EXPECT().Infof("Cluster peering row %s updated to state %s", mock.Anything, mock.Anything)

		res, err := activity.CreateClusterPeerInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, clusterPeer, res.ClusterPeer)
	})

	t.Run("Success_RoleAlreadyExists", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}
		ctx := context.Background()
		logger := log.NewMockLogger(tt)
		pass := log.Secret("p2")
		expiry := time.Now().Add(time.Hour)
		expDT := strfmt.DateTime(expiry)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-cluster-peer-2"},
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerIpAddresses: []string{"10.0.0.1"},
				PeerClusterName: "peer-cluster",
				PeerSvmName:     "peer-svm",
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: &datamodel.ClusterPeerings{BaseModel: datamodel.BaseModel{ID: 8}},
		}
		clusterPeer := &vsa.ClusterPeer{
			UUID:            "cluster-peer-uuid",
			ExternalUUID:    "ext-peer-uuid",
			Passphrase:      &pass,
			ExpiryTime:      &expDT,
			PeerClusterName: "peer-cluster",
		}
		interclusterLifs := []*vsa.InterclusterLif{{Address: ontaprestmodel.IPAddress("10.2.0.1")}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger).Times(4)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil).Times(2)

		mockProvider.EXPECT().CreateRole(mock.Anything).Return("", errors.New("Role already exists"))
		logger.EXPECT().Debugf("Role %s already exists, skipping creation", activities.OnPremPeerRoleName)

		mockProvider.EXPECT().CreateClusterPeer(mock.MatchedBy(func(params vsa.CreateClusterPeerParams) bool {
			return params.LocalRole != nil && *params.LocalRole == activities.OnPremPeerRoleName
		})).Return(clusterPeer, nil)
		logger.EXPECT().Infof("cluster peer created successfully with UUID: %s", clusterPeer.ExternalUUID)

		mockProvider.EXPECT().GetInterclusterLIFs(vsa.InterclusterServicePolicyName).Return(interclusterLifs, nil)
		mockSE.On("UpdateVolumeFields", ctx, vol.UUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			return updates["cache_parameters"] != nil
		})).Return(nil).Once()
		mockSE.On("UpdateClusterPeeringRow", ctx, mock.Anything).Return(nil).Once()
		logger.EXPECT().Debug("cluster peer command updated successfully")
		logger.EXPECT().Infof("Cluster peering row %s updated to state %s", mock.Anything, mock.Anything)

		res, err := activity.CreateClusterPeerInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, clusterPeer, res.ClusterPeer)
	})

	t.Run("WhenEnsureExternalPeerRoleFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		logger := log.NewMockLogger(tt)
		vol := &datamodel.Volume{
			Svm:     &datamodel.Svm{Name: "svm-name"},
			Account: &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerIpAddresses: []string{"10.0.0.1"},
				PeerClusterName: "peer-cluster",
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol}
		expectedError := errors.New("failed to create role: ONTAP connection error")

		mm.EXPECT().utilGetLogger(ctx).Return(logger).Times(2)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)

		// Mock EnsureExternalPeerRole failure (real error, not "already exists")
		mockProvider.EXPECT().CreateRole(mock.MatchedBy(func(params vsa.CreateRoleParams) bool {
			return params.Name == activities.OnPremPeerRoleName
		})).Return("", expectedError)
		logger.EXPECT().Errorf("Failed to create role %s: %v", activities.OnPremPeerRoleName, mock.Anything)

		res, err := activity.CreateClusterPeerInOntapActivity(ctx, flexcacheResult)
		assert.Nil(tt, res)
		assert.Error(tt, err)
		// Verify CreateClusterPeer was never called
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := &datamodel.Volume{CacheParameters: &datamodel.CacheParameters{PeerIpAddresses: []string{"10.0.0.1"}, PeerClusterName: "peer-cluster"}}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().utilGetLogger(ctx).Return(log.NewMockLogger(tt))
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		res, err := activity.CreateClusterPeerInOntapActivity(ctx, flexcacheResult)
		assert.Nil(tt, res)
		assert.Error(tt, err)
	})

	t.Run("WhenCreateClusterPeerFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		logger := log.NewMockLogger(tt)
		vol := &datamodel.Volume{CacheParameters: &datamodel.CacheParameters{PeerIpAddresses: []string{"10.0.0.1"}, PeerClusterName: "peer-cluster"}}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().utilGetLogger(ctx).Return(logger).Times(2)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)

		// Mock EnsureExternalPeerRole success
		mockProvider.EXPECT().CreateRole(mock.Anything).Return("role-location", nil)
		logger.EXPECT().Infof("Successfully created role %s", activities.OnPremPeerRoleName)

		mockProvider.EXPECT().CreateClusterPeer(mock.Anything).Return(nil, assert.AnError)

		res, err := activity.CreateClusterPeerInOntapActivity(ctx, flexcacheResult)
		assert.Nil(tt, res)
		assert.Error(tt, err)
		assert.Equal(tt, coremodels.ErrorDuringClusterPeerCode, vol.CacheParameters.CacheStateDetailsCode)
		assert.Equal(tt, "Cluster peering failed, please try again", vol.CacheParameters.CacheStateDetails)
	})
}

func TestFlexCacheVolumeCreateActivity_UpdateFlexCacheVolumeForClusterPeering(t *testing.T) {
	baseVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "flexcache-vol",
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:     "peer-svm",
				PeerVolumeName:  "peer-volume",
				PeerClusterName: "peer-cluster",
				CacheState:      cvpModels.FlexCacheV1betaCacheStateCACHESTATEUNSPECIFIED,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{FileProperties: &datamodel.FileProperties{}},
		}
	}

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()
		logger := log.NewMockLogger(tt)

		vol := baseVolume()
		pass := log.Secret("some-passphrase")
		externalUUID := "cluster-peer-external-uuid"
		expiry := strfmt.DateTime(time.Now().Add(time.Hour))
		clusterPeer := &vsa.ClusterPeer{ExternalUUID: externalUUID, Passphrase: &pass, ExpiryTime: &expiry}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeer: clusterPeer,
			ClusterPeeringRow: &datamodel.ClusterPeerings{OntapPeerUUID: externalUUID}}

		interclusterLifs := []*vsa.InterclusterLif{{Address: ontaprestmodel.IPAddress("10.0.0.1")}, {Address: ontaprestmodel.IPAddress("10.0.0.2")}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetInterclusterLIFs(vsa.InterclusterServicePolicyName).Return(interclusterLifs, nil)
		mockStorage.On("UpdateVolumeFields", ctx, vol.UUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			return updates["cache_parameters"] != nil
		})).Return(nil)
		logger.EXPECT().Debug("cluster peer command updated successfully")

		res, err := activity.UpdateFlexCacheVolumeForClusterPeeringActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, externalUUID, res.ClusterPeeringRow.OntapPeerUUID)
		assert.NotNil(tt, res.DBVolume.CacheParameters.Command)
		assert.Contains(tt, *res.DBVolume.CacheParameters.Command, "cluster peer create")
		assert.Equal(tt, cvpModels.FlexCacheV1betaCacheStatePENDINGCLUSTERPEERING, res.DBVolume.CacheParameters.CacheState)
		assert.Equal(tt, cvpModels.FlexCacheV1betaCacheStateCACHESTATEUNSPECIFIED, res.DBVolume.CacheParameters.PreviousCacheState)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().utilGetLogger(ctx).Return(log.NewMockLogger(tt))
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)
		_, err := activity.UpdateFlexCacheVolumeForClusterPeeringActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
	})

	t.Run("WhenGetInterclusterLIFsFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		pass := log.Secret("some-passphrase")
		expiry := strfmt.DateTime(time.Now().Add(time.Hour))
		clusterPeer := &vsa.ClusterPeer{ExternalUUID: "peer-external-uuid", Passphrase: &pass, ExpiryTime: &expiry}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeer: clusterPeer}

		mm.EXPECT().utilGetLogger(ctx).Return(log.NewMockLogger(tt))
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetInterclusterLIFs(vsa.InterclusterServicePolicyName).Return(nil, assert.AnError)
		_, err := activity.UpdateFlexCacheVolumeForClusterPeeringActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
	})

	t.Run("WhenUpdateVolumeFieldsFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		pass := log.Secret("some-passphrase")
		expiry := strfmt.DateTime(time.Now().Add(time.Hour))
		clusterPeer := &vsa.ClusterPeer{ExternalUUID: "peer-external-uuid", Passphrase: &pass, ExpiryTime: &expiry}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeer: clusterPeer}
		interclusterLifs := []*vsa.InterclusterLif{{Address: ontaprestmodel.IPAddress("10.0.0.1")}}

		mm.EXPECT().utilGetLogger(ctx).Return(log.NewMockLogger(tt))
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetInterclusterLIFs(vsa.InterclusterServicePolicyName).Return(interclusterLifs, nil)
		mockStorage.On("UpdateVolumeFields", ctx, vol.UUID, mock.Anything).Return(assert.AnError)
		_, err := activity.UpdateFlexCacheVolumeForClusterPeeringActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
	})
}

func TestFlexCacheVolumeCreateActivity_WaitForClusterPeerActivity(t *testing.T) {
	baseVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "volume-uuid"},
			Name:             "flexcache-vol",
			Svm:              &datamodel.Svm{Name: "svm-name"},
			Account:          &datamodel.Account{Name: "account-name"},
			CacheParameters:  &datamodel.CacheParameters{},
			VolumeAttributes: &datamodel.VolumeAttributes{FileProperties: &datamodel.FileProperties{}},
		}
	}
	clusterPeerUUID := "cluster-peer-uuid"
	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		peer := &vsa.ClusterPeer{
			UUID:                clusterPeerUUID,
			AuthenticationState: vsa.ClusterPeerAuthenticationStateOK,
			Availability:        vsa.ClusterPeerAvailabilityStateAvailable,
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: &datamodel.ClusterPeerings{OntapPeerUUID: clusterPeerUUID},
		}

		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetClusterPeer(clusterPeerUUID).Return(peer, nil)
		_, err := activity.WaitForClusterPeerActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol}
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)
		_, err := activity.WaitForClusterPeerActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
	})

	t.Run("WhenGetClusterPeerErrors", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: &datamodel.ClusterPeerings{OntapPeerUUID: clusterPeerUUID}}
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetClusterPeer(clusterPeerUUID).Return(nil, errors.New("any failure"))
		_, err := activity.WaitForClusterPeerActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
	})

	t.Run("WhenClusterPeerProblem", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		peer := &vsa.ClusterPeer{
			UUID:                "cluster-peer-uuid",
			AuthenticationState: vsa.ClusterPeerAuthenticationStateProblem,
			Availability:        vsa.ClusterPeerAvailabilityStateUnavailable,
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: &datamodel.ClusterPeerings{OntapPeerUUID: clusterPeerUUID}}

		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetClusterPeer(clusterPeerUUID).Return(peer, nil)
		_, err := activity.WaitForClusterPeerActivity(ctx, flexcacheResult)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Error during cluster peering")
	})

	t.Run("WhenClusterPeerNotReady", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		peer := &vsa.ClusterPeer{
			UUID:                "cluster-peer-uuid",
			AuthenticationState: vsa.ClusterPeerAuthenticationStatePending,
			Availability:        vsa.ClusterPeerAvailabilityStateUnavailable,
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: &datamodel.ClusterPeerings{OntapPeerUUID: clusterPeerUUID}}
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetClusterPeer(clusterPeerUUID).Return(peer, nil)
		_, err := activity.WaitForClusterPeerActivity(ctx, flexcacheResult)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Error during cluster peering")
	})
}

func TestFlexCacheVolumeCreateActivity_CreateSVMPeeringInOntapActivity(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}
		ctx := context.Background()
		logger := log.NewMockLogger(tt)

		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-svm-peer"},
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:     "peer-svm",
				PeerClusterName: "peer-cluster",
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol}
		svmPeer := &vsa.SvmPeer{UUID: "svm-peer-uuid", State: "peered"}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateSVMPeer(mock.Anything).Return(svmPeer, nil)
		mockSE.On("UpdateVolumeFields", ctx, vol.UUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			return updates["cache_parameters"] != nil
		})).Return(nil).Once()
		logger.EXPECT().Debug("svm peer command updated successfully")

		res, err := activity.CreateSVMPeeringInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, svmPeer, res.SVMPeer)
		assert.Equal(tt, "svm-peer-uuid", res.SVMPeer.UUID)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		vol := &datamodel.Volume{
			Svm: &datamodel.Svm{Name: "svm-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:     "peer-svm",
				PeerClusterName: "peer-cluster",
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		res, err := activity.CreateSVMPeeringInOntapActivity(ctx, flexcacheResult)
		assert.Nil(tt, res)
		assert.Error(tt, err)
	})

	t.Run("WhenCreateSVMPeerFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		vol := &datamodel.Volume{
			Svm: &datamodel.Svm{Name: "svm-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:     "peer-svm",
				PeerClusterName: "peer-cluster",
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateSVMPeer(mock.Anything).Return(nil, assert.AnError)

		res, err := activity.CreateSVMPeeringInOntapActivity(ctx, flexcacheResult)
		assert.Nil(tt, res)
		assert.Error(tt, err)
	})

	t.Run("IncludesFlexcacheAndSnapmirrorApplications", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockSE := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockSE}
		ctx := context.Background()
		logger := log.NewMockLogger(tt)

		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-svm-peer-2"},
			Svm:       &datamodel.Svm{Name: "svm-name"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:     "peer-svm",
				PeerClusterName: "peer-cluster",
			},
		}
		flexcacheResult := &flexcache.CreateFlexCacheResult{DBVolume: vol}
		svmPeer := &vsa.SvmPeer{UUID: "svm-peer-uuid"}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().CreateSVMPeer(mock.MatchedBy(func(params vsa.CreateSVMPeerParams) bool {
			return params.LocalSVMName == "svm-name" &&
				params.PeerSVMName == "peer-svm" &&
				params.PeerClusterName == "peer-cluster" &&
				len(params.Applications) == 2 &&
				params.Applications[0] == ontaprestmodel.SvmPeerApplicationsFlexcache &&
				params.Applications[1] == ontaprestmodel.SvmPeerApplicationsSnapmirror
		})).Return(svmPeer, nil)
		mockSE.On("UpdateVolumeFields", ctx, vol.UUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			return updates["cache_parameters"] != nil
		})).Return(nil).Once()
		logger.EXPECT().Debug("svm peer command updated successfully")

		res, err := activity.CreateSVMPeeringInOntapActivity(ctx, flexcacheResult)
		assert.NoError(tt, err)
		assert.Equal(tt, svmPeer, res.SVMPeer)
	})
}

func TestFlexCacheVolumeCreateActivity_UpdateFlexCacheVolumeForSVMPeeringActivity(t *testing.T) {
	baseVolume := func() *datamodel.Volume {
		pass := "old-passphrase"
		expiry := time.Now().Add(15 * time.Minute)
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid-svm"},
			Name:      "flexcache-vol",
			Svm:       &datamodel.Svm{Name: "local-svm"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName:           "peer-svm",
				PeerClusterName:       "peer-cluster",
				CacheState:            cvpModels.FlexCacheV1betaCacheStatePENDINGCLUSTERPEERING,
				PreviousCacheState:    cvpModels.FlexCacheV1betaCacheStateCACHESTATEUNSPECIFIED,
				Passphrase:            &pass,
				Command:               nillable.ToPointer("old command"),
				CommandExpiryTime:     &expiry,
				CacheStateDetails:     "some prior detail",
				CacheStateDetailsCode: coremodels.WaitingForClusterPeeringCode,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{FileProperties: &datamodel.FileProperties{}},
		}
	}

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()
		logger := log.NewMockLogger(tt)

		vol := baseVolume()
		svmPeer := &vsa.SvmPeer{UUID: "svm-peer-uuid", State: vsa.SvmPeerStatePeered}
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol, SVMPeer: svmPeer}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mockStorage.On("UpdateVolumeFields", ctx, vol.UUID, mock.Anything).Return(nil).Once()
		logger.EXPECT().Debug("svm peer command updated successfully")

		updated, err := activity.UpdateFlexCacheVolumeForSVMPeeringActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Nil(tt, updated.DBVolume.CacheParameters.Passphrase)
		assert.Nil(tt, updated.DBVolume.CacheParameters.CommandExpiryTime)
		assert.NotNil(tt, updated.DBVolume.CacheParameters.Command)
		assert.Equal(tt, "vserver peer accept -vserver peer-svm -peer-vserver local-svm", *updated.DBVolume.CacheParameters.Command)
		assert.Equal(tt, cvpModels.FlexCacheV1betaCacheStatePENDINGSVMPEERING, updated.DBVolume.CacheParameters.CacheState)
		assert.Equal(tt, cvpModels.FlexCacheV1betaCacheStatePENDINGCLUSTERPEERING, updated.DBVolume.CacheParameters.PreviousCacheState)
		assert.Equal(tt, coremodels.WaitingForSVMPeeringCode, updated.DBVolume.CacheParameters.CacheStateDetailsCode)
		assert.Equal(tt, datamodel.WaitingForSVMPeering, updated.DBVolume.CacheParameters.CacheStateDetails)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenUpdateVolumeFieldsFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		svmPeer := &vsa.SvmPeer{UUID: "svm-peer-uuid-2", State: string(vsa.SvmPeerStatePeered)}
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol, SVMPeer: svmPeer}

		mm.EXPECT().utilGetLogger(ctx).Return(log.NewMockLogger(tt))
		mockStorage.On("UpdateVolumeFields", ctx, vol.UUID, mock.Anything).Return(assert.AnError).Once()

		updated, err := activity.UpdateFlexCacheVolumeForSVMPeeringActivity(ctx, result)
		assert.Nil(tt, updated)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeCreateActivity_WaitForSVMPeeringActivity(t *testing.T) {
	baseVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid-wait-svm"},
			Name:      "flexcache-vol",
			Svm:       &datamodel.Svm{Name: "local-svm"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName: "peer-svm",
			},
			VolumeAttributes: &datamodel.VolumeAttributes{FileProperties: &datamodel.FileProperties{}},
		}
	}

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(&vsa.SvmPeer{UUID: "svm-peer-uuid", State: vsa.SvmPeerStatePeered}, nil)

		res, err := activity.WaitForSVMPeeringActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, result, res)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		res, err := activity.WaitForSVMPeeringActivity(ctx, result)
		assert.Nil(tt, res)
		assert.Error(tt, err)
	})

	t.Run("WhenGetSVMPeerErrors", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(nil, errors.New("any failure"))

		res, err := activity.WaitForSVMPeeringActivity(ctx, result)
		assert.Nil(tt, res)
		assert.Error(tt, err)
	})

	t.Run("WhenSVMPeerRejected", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(&vsa.SvmPeer{UUID: "svm-peer-uuid", State: vsa.SvmPeerStateRejected}, nil)

		res, err := activity.WaitForSVMPeeringActivity(ctx, result)
		assert.Nil(tt, res)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Error during SVM peering")
	})

	t.Run("WhenSVMPeerNotReady", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(&vsa.SvmPeer{UUID: "svm-peer-uuid", State: vsa.SvmPeerStateInitiated}, nil)

		res, err := activity.WaitForSVMPeeringActivity(ctx, result)
		assert.Nil(tt, res)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Error during SVM peering")
	})
}

func TestFlexCacheVolumeCreateActivity_UpdateFlexCacheVolumeForVolumeCreation(t *testing.T) {
	baseVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid-svm"},
			Name:      "flexcache-vol",
			Svm:       &datamodel.Svm{Name: "local-svm"},
			Account:   &datamodel.Account{Name: "account-name"},
			CacheParameters: &datamodel.CacheParameters{
				CacheState:         cvpModels.FlexCacheV1betaCacheStatePENDINGSVMPEERING,
				PreviousCacheState: cvpModels.FlexCacheV1betaCacheStatePENDINGCLUSTERPEERING,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{FileProperties: &datamodel.FileProperties{}},
		}
	}

	t.Run("SuccessWithCreating", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()

		vol := baseVolume()
		volumeResponse := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "external-volume-uuid"}}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:       vol,
			VolumeResponse: volumeResponse,
		}
		mockStorage.EXPECT().UpdateVolumeFields(ctx, vol.UUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			return updates["state"] == datamodel.LifeCycleStateCreating && updates["state_details"] == datamodel.LifeCycleStateCreatingDetails
		})).Return(nil).Once()

		updated, err := activity.UpdateFlexCacheVolumeLifecycleStateActivity(ctx, result, datamodel.LifeCycleStateCreating)
		assert.NoError(tt, err)
		assert.Equal(tt, updated.DBVolume.State, datamodel.LifeCycleStateCreating)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("SuccessWithReady", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()

		vol := baseVolume()
		volumeResponse := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "external-volume-uuid"}}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:       vol,
			VolumeResponse: volumeResponse,
		}
		mockStorage.EXPECT().UpdateVolumeFields(ctx, vol.UUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			return updates["state"] == datamodel.LifeCycleStateREADY && updates["state_details"] == datamodel.LifeCycleStateAvailableDetails
		})).Return(nil).Once()

		updated, err := activity.UpdateFlexCacheVolumeLifecycleStateActivity(ctx, result, datamodel.LifeCycleStateREADY)
		assert.NoError(tt, err)
		assert.Equal(tt, updated.DBVolume.State, datamodel.LifeCycleStateREADY)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("ErrorOnUpdateVolumeFields", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()

		vol := baseVolume()
		volumeResponse := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "external-volume-uuid"}}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:       vol,
			VolumeResponse: volumeResponse,
		}
		mockStorage.EXPECT().UpdateVolumeFields(ctx, vol.UUID, mock.Anything).Return(assert.AnError).Once()
		updated, err := activity.UpdateFlexCacheVolumeLifecycleStateActivity(ctx, result, datamodel.LifeCycleStateREADY)
		assert.Nil(tt, updated)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ErrorUnknownState", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()

		vol := baseVolume()
		volumeResponse := &vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "external-volume-uuid"}}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:       vol,
			VolumeResponse: volumeResponse,
		}

		updated, err := activity.UpdateFlexCacheVolumeLifecycleStateActivity(ctx, result, "UNKNOWN_STATE")
		assert.Nil(tt, updated)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeCreateActivity_UpdateVolumeDetailsOnErrorActivity(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()

		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "volume-uuid"},
			CacheParameters: &datamodel.CacheParameters{CacheStateDetails: "some-detail"},
		}
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mockStorage.On("UpdateVolumeFields", ctx, vol.UUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			state, sOk := updates["state"].(string)
			stateDetails, sdOk := updates["state_details"].(string)
			cacheParams, cpOk := updates["cache_parameters"].(*datamodel.CacheParameters)
			return sOk && sdOk && cpOk && state == "ERROR" && stateDetails == "Error in creating" && cacheParams == vol.CacheParameters
		})).Return(nil).Once()

		err := activity.UpdateVolumeDetailsOnErrorActivity(ctx, result)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenUpdateVolumeFieldsFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()

		vol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid-2"}, CacheParameters: &datamodel.CacheParameters{}}
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mockStorage.On("UpdateVolumeFields", ctx, vol.UUID, mock.Anything).Return(assert.AnError).Once()

		err := activity.UpdateVolumeDetailsOnErrorActivity(ctx, result)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenShouldNotSetErrorState", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		ctx := context.Background()

		vol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid-3"}, CacheParameters: &datamodel.CacheParameters{CacheStateDetailsCode: coremodels.ClusterPeeringExpiredCode}}
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mockStorage.On("UpdateVolumeFields", ctx, vol.UUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			_, stateOk := updates["state"]
			_, stateDetailsOk := updates["state_details"]
			return !stateOk && !stateDetailsOk
		})).Return(nil).Once()

		err := activity.UpdateVolumeDetailsOnErrorActivity(ctx, result)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestCompletePeeringJobActivity(t *testing.T) {
	t.Run("CompletesProcessingJob", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		clusterPeerRow := &datamodel.ClusterPeerings{
			OntapPeerUUID: "cp-uuid",
			State:         datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING,
		}

		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "res-uuid"},
			CacheParameters: &datamodel.CacheParameters{
				CacheState: cvpModels.FlexCacheV1betaCacheStatePENDINGCLUSTERPEERING,
			},
		}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume: vol,
			JobInput: &flexcache.JobActivityInput{
				ResourceUUID: vol.UUID,
			},
			ClusterPeeringRow: clusterPeerRow,
		}

		job := []*datamodel.Job{
			{
				BaseModel:    datamodel.BaseModel{UUID: "jp2"},
				Type:         string(datamodel.JobTypeFlexCacheEstablishPeering),
				State:        string(datamodel.JobsStatePROCESSING),
				TrackingID:   0,
				ErrorDetails: "",
			},
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).
			Return(job, nil).Once()
		mockStorage.On("UpdateJob", ctx, job[0].UUID, string(datamodel.JobsStateDONE), job[0].TrackingID, job[0].ErrorDetails).Return(nil).Once()

		err := act.CompletePeeringJobActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateDONE), job[0].State)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SkipsWhenCreatingState", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume: &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "res-uuid"}},
			ClusterPeeringRow: &datamodel.ClusterPeerings{
				State: datamodel.CvpClusterPeeringStatusCREATING,
			},
		}

		err := act.CompletePeeringJobActivity(ctx, result)
		assert.NoError(tt, err)
		mockStorage.AssertNotCalled(tt, "GetJobsWithCondition", mock.Anything, mock.Anything)
	})

	t.Run("CompletesWhenPeeredState", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		clusterPeerRow := &datamodel.ClusterPeerings{
			OntapPeerUUID: "cp-uuid",
			State:         datamodel.CvpClusterPeeringStatusPEERED,
		}
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "res-uuid"},
			CacheParameters: &datamodel.CacheParameters{
				CacheState: cvpModels.FlexCacheV1betaCacheStatePEERED,
			},
		}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: clusterPeerRow,
		}

		job := []*datamodel.Job{
			{
				BaseModel:    datamodel.BaseModel{UUID: "jp-peered"},
				Type:         string(datamodel.JobTypeFlexCacheEstablishPeering),
				State:        string(datamodel.JobsStatePROCESSING),
				TrackingID:   0,
				ErrorDetails: "",
			},
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return(job, nil).Once()
		mockStorage.On("UpdateJob", ctx, job[0].UUID, string(datamodel.JobsStateDONE), job[0].TrackingID, job[0].ErrorDetails).Return(nil).Once()

		err := act.CompletePeeringJobActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateDONE), job[0].State)
		mockStorage.AssertExpectations(tt)
	})
}

func TestStartInternalJobActivity(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "res-internal"},
		Svm:       &datamodel.Svm{Name: "svm-name"},
		Account:   &datamodel.Account{Name: "account-name"},
		CacheParameters: &datamodel.CacheParameters{
			PeerSvmName:     "peer-svm",
			PeerVolumeName:  "peer-volume",
			PeerClusterName: "peer-cluster",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{},
		},
	}
	result := makeResult(vol, "wf-peering")

	t.Run("ExistingProcessingReturns", func(tt *testing.T) {
		existing := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "i1"}, State: string(datamodel.JobsStatePROCESSING), Type: string(datamodel.JobTypeFlexCacheInternalPeering)}
		mockStorage.On("GetJobByResourceUUID", ctx, result.JobInput.ResourceUUID, existing.Type).Return(existing, nil).Once()
		res, err := act.StartInternalJobActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Empty(tt, res.ErrorMessage)
		assert.Equal(tt, 0, res.ErrorTrackingID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CreateInternalSuccess", func(tt *testing.T) {
		mockStorage.On("GetJobByResourceUUID", ctx, result.JobInput.ResourceUUID, string(datamodel.JobTypeFlexCacheInternalPeering)).Return(nil, errors.New("nf")).Once()
		mockStorage.On("CreateJobWithWorkflowID", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.Type == string(datamodel.JobTypeFlexCacheInternalPeering) && j.IsAdminJob
		}), "wf-peering").Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "int-created"}, Type: string(datamodel.JobTypeFlexCacheInternalPeering), State: string(datamodel.JobsStatePROCESSING)}, nil).Once()
		res, err := act.StartInternalJobActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Empty(tt, res.ErrorMessage)
		assert.Equal(tt, 0, res.ErrorTrackingID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CreateInternalJobFails", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		innerAct := &FlexCacheVolumeCreateActivity{SE: ms}
		ms.On("GetJobByResourceUUID", ctx, result.JobInput.ResourceUUID, string(datamodel.JobTypeFlexCacheInternalPeering)).Return(nil, errors.New("nf")).Once()
		ms.On("CreateJobWithWorkflowID", ctx, mock.Anything, "wf-peering").Return(nil, assert.AnError).Once()
		res, err := innerAct.StartInternalJobActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, res)
		ms.AssertExpectations(tt)
	})
}

func TestCompleteInternalJobActivity(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "res-internal"},
		Svm:       &datamodel.Svm{Name: "svm-name"},
		Account:   &datamodel.Account{Name: "account-name"},
		CacheParameters: &datamodel.CacheParameters{
			PeerSvmName:     "peer-svm",
			PeerVolumeName:  "peer-volume",
			PeerClusterName: "peer-cluster",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{},
		},
	}
	result := makeResult(vol, "wf-peering")
	jobType := string(datamodel.JobTypeFlexCacheInternalPeering)

	t.Run("NotPeeredKeepsProcessing", func(tt *testing.T) {
		job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "ic1"}, Type: jobType, State: string(datamodel.JobsStatePROCESSING)}
		err := act.CompleteInternalJobActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStatePROCESSING), job.State)
	})

	t.Run("PeeredCompletes", func(tt *testing.T) {
		ctx = context.Background()
		mockStorage = database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}

		vol = &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "res-internal"},
			Name:      "res-internal",
			CacheParameters: &datamodel.CacheParameters{
				CacheState: cvpModels.FlexCacheV1betaCacheStatePEERED,
			},
		}
		result = makeResult(vol, "wf-internal")
		job := []*datamodel.Job{
			{
				BaseModel:    datamodel.BaseModel{UUID: "ic2"},
				Type:         string(datamodel.JobTypeFlexCacheInternalPeering),
				State:        string(datamodel.JobsStatePROCESSING),
				TrackingID:   0,
				ErrorDetails: "",
			},
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).
			Return(job, nil).Once()
		mockStorage.On("UpdateJob", ctx, job[0].UUID, string(datamodel.JobsStateDONE), job[0].TrackingID, job[0].ErrorDetails).Return(nil).Once()

		err := act.CompleteInternalJobActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateDONE), job[0].State)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFailJobActivity(t *testing.T) {
	t.Run("GetJobError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		resUUID := "res-fail"
		jobs := []*datamodel.Job{
			{
				BaseModel: datamodel.BaseModel{UUID: "cj2"},
				Type:      string(datamodel.JobTypeFlexCacheEstablishPeering),
				State:     string(datamodel.JobsStatePROCESSING),
			},
		}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:      &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: resUUID}},
			ActiveJobType: datamodel.JobTypeFlexCacheEstablishPeering,
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return(jobs, assert.AnError).Once()

		err := act.FailJobActivity(ctx, result)
		assert.Error(tt, err)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("JobNil", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		resUUID := "res-fail"

		result := &flexcache.CreateFlexCacheResult{
			DBVolume:      &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: resUUID}},
			ActiveJobType: datamodel.JobTypeFlexCacheEstablishPeering,
		}

		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return(nil, nil).Once()

		err := act.FailJobActivity(ctx, result)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FailProcessingExplicit", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		jobs := []*datamodel.Job{
			{
				BaseModel: datamodel.BaseModel{UUID: "cj2"},
				Type:      string(datamodel.JobTypeFlexCacheEstablishPeering),
				State:     string(datamodel.JobsStatePROCESSING),
			},
		}
		resUUID := "res-fail"
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:        &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: resUUID}},
			ActiveJobType:   datamodel.JobTypeFlexCacheEstablishPeering,
			ErrorTrackingID: 999,
			ErrorMessage:    "boom",
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return(jobs, nil).Once()
		mockStorage.On("UpdateJob", ctx, jobs[0].UUID, string(datamodel.JobsStateERROR), 999, "boom").Return(nil).Once()

		err := act.FailJobActivity(ctx, result)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FailProcessingImplicitDefaults", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		jobs := []*datamodel.Job{
			{
				BaseModel:    datamodel.BaseModel{UUID: "cj2"},
				Type:         string(datamodel.JobTypeFlexCacheEstablishPeering),
				State:        string(datamodel.JobsStatePROCESSING),
				TrackingID:   77,
				ErrorDetails: "prev",
			},
		}
		resUUID := "res-fail"
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:      &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: resUUID}},
			ActiveJobType: datamodel.JobTypeFlexCacheEstablishPeering,
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return(jobs, nil).Once()
		mockStorage.On("UpdateJob", ctx, jobs[0].UUID, string(datamodel.JobsStateERROR), jobs[0].TrackingID, jobs[0].ErrorDetails).Return(nil).Once()

		err := act.FailJobActivity(ctx, result)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		jobs := []*datamodel.Job{
			{
				BaseModel: datamodel.BaseModel{UUID: "cj-fail-update"},
				Type:      string(datamodel.JobTypeFlexCacheEstablishPeering),
				State:     string(datamodel.JobsStatePROCESSING),
			},
		}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:        &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "res-fail-update"}},
			ActiveJobType:   datamodel.JobTypeFlexCacheEstablishPeering,
			ErrorTrackingID: 123,
			ErrorMessage:    "fail reason",
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return(jobs, nil).Once()
		mockStorage.On("UpdateJob", ctx, jobs[0].UUID, string(datamodel.JobsStateERROR), 123, "fail reason").Return(assert.AnError).Once()

		err := act.FailJobActivity(ctx, result)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UsesUnspecifiedErrorWhenEmptyDetails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: mockStorage}
		jobs := []*datamodel.Job{
			{
				BaseModel:    datamodel.BaseModel{UUID: "cj-unspecified"},
				Type:         string(datamodel.JobTypeFlexCacheEstablishPeering),
				State:        string(datamodel.JobsStatePROCESSING),
				TrackingID:   0,
				ErrorDetails: "",
			},
		}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:      &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "res-unspecified"}},
			ActiveJobType: datamodel.JobTypeFlexCacheEstablishPeering,
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).Return(jobs, nil).Once()
		mockStorage.On("UpdateJob", ctx, jobs[0].UUID, string(datamodel.JobsStateERROR), jobs[0].TrackingID, "unspecified error").Return(nil).Once()

		err := act.FailJobActivity(ctx, result)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestCompleteJobInternalPaths(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	act := &FlexCacheVolumeCreateActivity{SE: mockStorage}

	t.Run("InvalidOptions", func(tt *testing.T) {
		_, err := act.completeJob(ctx, completeJobOpts{
			GetErrCode: vsaerrors.ErrInternalPeeringJobFailed,
		})
		assert.Error(tt, err)
	})

	t.Run("WarnsOnMultipleActiveJobs", func(tt *testing.T) {
		ctx := context.Background()
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}

		resourceUUID := "vol-123"
		jobType := string(datamodel.JobTypeFlexCacheCreateVolume)

		j1 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-1"},
			Type:      jobType,
			State:     string(datamodel.JobsStatePROCESSING),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: resourceUUID,
			},
			TrackingID:   42,
			ErrorDetails: "prev-error",
		}
		j2 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-2"},
			Type:      jobType,
			State:     string(datamodel.JobsStatePROCESSING),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: resourceUUID,
			},
		}

		ms.
			On("GetJobsWithCondition", ctx, mock.Anything).
			Return([]*datamodel.Job{j1, j2}, nil).Once()

		ms.
			On("UpdateJob", ctx, j1.UUID, string(datamodel.JobsStateDONE), j1.TrackingID, j1.ErrorDetails).
			Return(nil).Once()

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		allowAnyLogs(logger)

		logger.On(
			"Warnf",
			mock.MatchedBy(func(format string) bool {
				return strings.Contains(format, "multiple active jobs found")
			}),
		).Maybe()

		out, err := act.completeJob(ctx, completeJobOpts{
			ResourceUUID:  resourceUUID,
			JobType:       jobType,
			GetErrCode:    vsaerrors.ErrCreatingFlexCacheVolume,
			UpdateErrCode: vsaerrors.ErrCreatingFlexCacheVolume,
		})
		assert.NoError(tt, err)
		assert.NotNil(tt, out)
		assert.Equal(tt, j1.UUID, out.UUID)

		ms.AssertExpectations(tt)
		logger.AssertExpectations(tt)
	})

	t.Run("UpdateFails", func(tt *testing.T) {
		job := []*datamodel.Job{
			{
				BaseModel: datamodel.BaseModel{UUID: "cj2"},
				Type:      string(datamodel.JobTypeFlexCacheInternalPeering),
				State:     string(datamodel.JobsStatePROCESSING),
			},
		}
		mockStorage.On("GetJobsWithCondition", ctx, mock.Anything).
			Return(job, nil).Once()
		mockStorage.On("UpdateJob", ctx, job[0].UUID, string(datamodel.JobsStateDONE), job[0].TrackingID, job[0].ErrorDetails).Return(assert.AnError).Once()
		res, err := act.completeJob(ctx, completeJobOpts{
			ResourceUUID:  "res-y",
			JobType:       job[0].Type,
			GetErrCode:    vsaerrors.ErrInternalPeeringJobFailed,
			UpdateErrCode: vsaerrors.ErrInternalPeeringJobFailed,
		})
		assert.Nil(tt, res)
		assert.Error(tt, err)
	})

	t.Run("GetActiveJobsError", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		innerAct := &FlexCacheVolumeCreateActivity{SE: ms}
		ms.On("GetJobsWithCondition", ctx, mock.Anything).Return(nil, assert.AnError).Once()
		res, err := innerAct.completeJob(ctx, completeJobOpts{
			ResourceUUID:  "res-get-jobs-err",
			JobType:       string(datamodel.JobTypeFlexCacheInternalPeering),
			GetErrCode:    vsaerrors.ErrInternalPeeringJobFailed,
			UpdateErrCode: vsaerrors.ErrInternalPeeringJobFailed,
		})
		assert.Nil(tt, res)
		assert.Error(tt, err)
		ms.AssertExpectations(tt)
	})

	t.Run("JobNotFound", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		innerAct := &FlexCacheVolumeCreateActivity{SE: ms}
		ms.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{}, nil).Once()
		res, err := innerAct.completeJob(ctx, completeJobOpts{
			ResourceUUID:  "res-no-job",
			JobType:       string(datamodel.JobTypeFlexCacheInternalPeering),
			GetErrCode:    vsaerrors.ErrInternalPeeringJobFailed,
			UpdateErrCode: vsaerrors.ErrInternalPeeringJobFailed,
		})
		assert.Nil(tt, res)
		assert.Error(tt, err)
		ms.AssertExpectations(tt)
	})

	t.Run("AlreadyDone", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		innerAct := &FlexCacheVolumeCreateActivity{SE: ms}
		doneJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-done"},
			Type:      string(datamodel.JobTypeFlexCacheInternalPeering),
			State:     string(datamodel.JobsStateDONE),
		}
		ms.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{doneJob}, nil).Once()
		res, err := innerAct.completeJob(ctx, completeJobOpts{
			ResourceUUID:  "res-done",
			JobType:       string(datamodel.JobTypeFlexCacheInternalPeering),
			GetErrCode:    vsaerrors.ErrInternalPeeringJobFailed,
			UpdateErrCode: vsaerrors.ErrInternalPeeringJobFailed,
		})
		assert.NoError(tt, err)
		assert.Equal(tt, doneJob, res)
		ms.AssertExpectations(tt)
	})

	t.Run("AlreadyError", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		innerAct := &FlexCacheVolumeCreateActivity{SE: ms}
		errorJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-error"},
			Type:      string(datamodel.JobTypeFlexCacheInternalPeering),
			State:     string(datamodel.JobsStateERROR),
		}
		ms.On("GetJobsWithCondition", ctx, mock.Anything).Return([]*datamodel.Job{errorJob}, nil).Once()
		res, err := innerAct.completeJob(ctx, completeJobOpts{
			ResourceUUID:  "res-error",
			JobType:       string(datamodel.JobTypeFlexCacheInternalPeering),
			GetErrCode:    vsaerrors.ErrInternalPeeringJobFailed,
			UpdateErrCode: vsaerrors.ErrInternalPeeringJobFailed,
		})
		assert.NoError(tt, err)
		assert.Equal(tt, errorJob, res)
		ms.AssertExpectations(tt)
	})
}

func TestMapJobTypeToError(t *testing.T) {
	assert.Equal(t, vsaerrors.ErrEstablishPeeringJobFailed, mapJobTypeToError(string(datamodel.JobTypeFlexCacheEstablishPeering)))
	assert.Equal(t, vsaerrors.ErrInternalPeeringJobFailed, mapJobTypeToError(string(datamodel.JobTypeFlexCacheInternalPeering)))
	assert.Equal(t, vsaerrors.ErrCreatingFlexCacheVolume, mapJobTypeToError(string(datamodel.JobTypeFlexCacheCreateVolume)))
	assert.Equal(t, vsaerrors.ErrInternalPeeringJobFailed, mapJobTypeToError("unknown"))
}

func TestFlexCacheVolumeCreateActivity_HydrateFlexCacheState(t *testing.T) {
	baseVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-hydrate"},
			Name:      "flexcache-vol-hydrate",
			State:     datamodel.LifeCycleStateREADY,
			CacheParameters: &datamodel.CacheParameters{
				CacheState: cvpModels.FlexCacheV1betaCacheStatePENDINGCLUSTERPEERING,
			},
			Svm: &datamodel.Svm{Name: "svm-hydrate"},
		}
	}

	makeResultWithEvent := func(vol *datamodel.Volume) *flexcache.CreateFlexCacheResult {
		return &flexcache.CreateFlexCacheResult{
			DBVolume: vol,
			Event: &flexcache.CreateFlexCacheEvent{
				LocationID:    "us-test1",
				ProjectNumber: "123456789",
			},
		}
	}

	t.Run("HydrationDisabled", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(mock.Anything).Return(logger)
		mm.EXPECT().isHydrationEnabled().Return(false)
		logger.EXPECT().Debugf("hydration is disabled, skipping HydrateFlexCacheState")

		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		vol := baseVolume()
		result := makeResultWithEvent(vol)

		res, err := activity.HydrateFlexCacheState(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, result, res)
	})

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		ctx := context.Background()
		vol := baseVolume()
		result := makeResultWithEvent(vol)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}

		mm.EXPECT().isHydrationEnabled().Return(true)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().authGenerateCallbackToken(ctx).Return("tok-123", nil)
		mm.EXPECT().commonHydrateFlexCacheState(ctx, logger, result.Event.LocationID, result.Event.ProjectNumber, vol.Name, vol.CacheParameters.CacheState, vol.State, "tok-123").Return(nil)
		logger.EXPECT().Debugf("hydration completed successfully for volume: %s", vol.Name)

		res, err := activity.HydrateFlexCacheState(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, result, res)
	})

	t.Run("TokenGenerationError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		ctx := context.Background()
		mm.EXPECT().isHydrationEnabled().Return(true)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().authGenerateCallbackToken(ctx).Return("", assert.AnError)
		logger.EXPECT().Error("Error when getting callback token", assert.AnError)

		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		vol := baseVolume()
		result := makeResultWithEvent(vol)

		res, err := activity.HydrateFlexCacheState(ctx, result)
		assert.Error(tt, err)
		assert.Equal(tt, result, res, "should return original result on token error")
	})

	t.Run("HydrationFailure", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		ctx := context.Background()
		mm.EXPECT().isHydrationEnabled().Return(true)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().authGenerateCallbackToken(ctx).Return("tok-456", nil)

		vol := baseVolume()
		result := makeResultWithEvent(vol)
		activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}

		mm.EXPECT().commonHydrateFlexCacheState(ctx, logger, result.Event.LocationID, result.Event.ProjectNumber, vol.Name, vol.CacheParameters.CacheState, vol.State, "tok-456").Return(assert.AnError)
		logger.EXPECT().Errorf("Error when hydrating flexcache state: %v", assert.AnError)

		res, err := activity.HydrateFlexCacheState(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, res, "result should be nil on hydration failure")
	})
}

func TestFlexCacheVolumeCreateActivity_EnsureClusterPeerActivity(t *testing.T) {
	newVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			CacheParameters: &datamodel.CacheParameters{},
		}
	}

	clusterPeeringRow := &datamodel.ClusterPeerings{
		OntapPeerUUID: "peer-uuid",
	}

	t.Run("WhenClusterPeerUUIDAbsent", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: &datamodel.ClusterPeerings{}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		logger.EXPECT().Debug("cluster peer UUID absent; will create")

		out, err := act.EnsureClusterPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.ClusterPeerAction)
		assert.Nil(tt, out.ClusterPeer)
	})

	t.Run("WhenGetClusterPeerErrors", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		uuid := "peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: clusterPeeringRow}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetClusterPeer(uuid).Return(nil, assert.AnError)
		logger.EXPECT().Infof(mock.MatchedBy(func(format string) bool {
			return true
		}), mock.Anything)

		out, err := act.EnsureClusterPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.ClusterPeerAction)
		assert.Nil(tt, out.ClusterPeer)
	})

	t.Run("WhenClusterPeerNilWithoutError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		uuid := "peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: clusterPeeringRow}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetClusterPeer(uuid).Return(nil, nil)
		logger.EXPECT().Infof(mock.Anything, mock.Anything)

		out, err := act.EnsureClusterPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.ClusterPeerAction)
	})

	t.Run("WhenClusterPeerInvalidStateProblem", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		uuid := "peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: clusterPeeringRow}

		cp := &vsa.ClusterPeer{
			UUID:                uuid,
			AuthenticationState: vsa.ClusterPeerAuthenticationStateProblem,
			Availability:        vsa.ClusterPeerAvailabilityStateUnavailable,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetClusterPeer(uuid).Return(cp, nil)
		logger.EXPECT().Warnf(mock.Anything, cp.AuthenticationState, cp.Availability)

		out, err := act.EnsureClusterPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.ClusterPeerAction)
	})

	t.Run("WhenCommandExpiredRecreate", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		uuid := "peer-uuid"
		expired := time.Now().Add(-5 * time.Minute)
		expiredFmt := strfmt.DateTime(expired)
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: clusterPeeringRow}

		cp := &vsa.ClusterPeer{
			UUID:                uuid,
			AuthenticationState: vsa.ClusterPeerAuthenticationStatePending,
			Availability:        vsa.ClusterPeerAvailabilityStatePending,
			ExpiryTime:          &expiredFmt,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetClusterPeer(uuid).Return(cp, nil)
		logger.EXPECT().Infof(mock.Anything, uuid)

		out, err := act.EnsureClusterPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.ClusterPeerAction)
	})

	t.Run("WhenClusterPeerReady", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		uuid := "peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: clusterPeeringRow}

		cp := &vsa.ClusterPeer{
			UUID:                uuid,
			AuthenticationState: vsa.ClusterPeerAuthenticationStateOK,
			Availability:        vsa.ClusterPeerAvailabilityStateAvailable,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetClusterPeer(uuid).Return(cp, nil)
		logger.EXPECT().Debug("reusing existing cluster peer (ready)")

		out, err := act.EnsureClusterPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionReady, out.ClusterPeerAction)
		assert.Equal(tt, cp, out.ClusterPeer)
	})

	t.Run("WhenClusterPeerWait", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		uuid := "peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: clusterPeeringRow}

		cp := &vsa.ClusterPeer{
			UUID:                uuid,
			AuthenticationState: vsa.ClusterPeerAuthenticationStatePending,
			Availability:        vsa.ClusterPeerAvailabilityStatePartial,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetClusterPeer(uuid).Return(cp, nil)
		logger.EXPECT().Debugf(mock.Anything, cp.AuthenticationState, cp.Availability)

		out, err := act.EnsureClusterPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionWait, out.ClusterPeerAction)
		assert.Equal(tt, cp, out.ClusterPeer)
	})

	t.Run("WhenClusterPeerUnknownFallback", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		uuid := "peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: clusterPeeringRow}

		cp := &vsa.ClusterPeer{
			UUID:                uuid,
			AuthenticationState: vsa.ClusterPeerAuthenticationStateOK,
			Availability:        vsa.ClusterPeerAvailabilityStateUnavailable,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetClusterPeer(uuid).Return(cp, nil)
		logger.EXPECT().Warnf(mock.Anything, cp.AuthenticationState, cp.Availability)

		out, err := act.EnsureClusterPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.ClusterPeerAction)
	})
}

func TestFlexCacheVolumeCreateActivity_EnsureSVMPeerActivity(t *testing.T) {
	newVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			Svm: &datamodel.Svm{Name: "local-svm"},
			CacheParameters: &datamodel.CacheParameters{
				PeerSvmName: "peer-svm",
			},
		}
	}

	t.Run("WhenGetSVMPeerErrors", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(nil, assert.AnError)

		out, err := act.EnsureSVMPeerInOntapActivity(ctx, res)
		assert.Error(tt, err)
		assert.Nil(tt, out)
	})

	t.Run("WhenSVMPeerNilWithoutError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(nil,
			customerrors.NewNotFoundErr("svmPeer", nil))
		logger.EXPECT().Infof(mock.Anything, mock.Anything)

		out, err := act.EnsureSVMPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.SVMPeerAction)
	})

	t.Run("WhenSVMPeerInvalidStateRejected", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		u := "svm-peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		sp := &vsa.SvmPeer{
			UUID:  u,
			State: vsa.SvmPeerStateRejected,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(sp, nil)
		logger.EXPECT().Warnf(mock.Anything, mock.Anything, mock.Anything).Maybe() // depending on implementation

		out, err := act.EnsureSVMPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.SVMPeerAction)
	})

	t.Run("WhenSVMPeerUnknownStateRecreate", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		u := "svm-peer-uuid"
		vol := newVolume()
		// (Optional) expired time retained but not used by logic
		expired := time.Now().Add(-10 * time.Minute)
		vol.CacheParameters.CommandExpiryTime = &expired
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		sp := &vsa.SvmPeer{
			UUID:  u,
			State: vsa.SvmPeerStateInitiated, // unrecognized -> fallback recreate
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(sp, nil)
		logger.EXPECT().Warnf(mock.Anything, sp.State)

		out, err := act.EnsureSVMPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.SVMPeerAction)
	})

	t.Run("WhenSVMPeerReady", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		u := "svm-peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		sp := &vsa.SvmPeer{
			UUID:  u,
			State: vsa.SvmPeerStatePeered,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(sp, nil)
		logger.EXPECT().Debug(mock.Anything)

		out, err := act.EnsureSVMPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionReady, out.SVMPeerAction)
		assert.Equal(tt, sp, out.SVMPeer)
	})

	t.Run("WhenSVMPeerWait", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		u := "svm-peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		sp := &vsa.SvmPeer{
			UUID:  u,
			State: vsa.SvmPeerStatePending, // updated: triggers wait branch
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(sp, nil)
		logger.EXPECT().Debugf(mock.Anything, sp.State)

		out, err := act.EnsureSVMPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionWait, out.SVMPeerAction)
		assert.Equal(tt, sp, out.SVMPeer)
	})

	t.Run("WhenSVMPeerUnknownFallback", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()

		u := "svm-peer-uuid"
		vol := newVolume()
		res := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		sp := &vsa.SvmPeer{
			UUID:  u,
			State: "RANDOM_STATE",
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).Return(sp, nil)
		logger.EXPECT().Warnf(mock.Anything, mock.Anything, mock.Anything)

		out, err := act.EnsureSVMPeerInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, flexcache.ActionCreate, out.SVMPeerAction)
	})
}

func TestFlexCacheVolumeCreateActivity_GetClusterPeeringRowFromDBActivity(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	act := &FlexCacheVolumeCreateActivity{SE: mockStorage}

	makeVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Account:   &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 11}},
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 22}},
			CacheParameters: &datamodel.CacheParameters{
				PeerClusterName: "peer-cluster-A",
			},
		}
	}

	t.Run("SuccessExistingRow", func(tt *testing.T) {
		vol := makeVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		existing := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: "peer-row-1"},
			AccountID:      vol.Account.ID,
			PoolID:         vol.Pool.ID,
			OnprempCluster: vol.CacheParameters.PeerClusterName,
		}

		mockStorage.
			On("GetClusterPeerByAccountIDExternalClusterAndPoolID", ctx,
				vol.Account.ID, vol.CacheParameters.PeerClusterName, vol.Pool.ID).
			Return(existing, nil).Once()

		out, err := act.GetClusterPeeringRowFromDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, existing, out.ClusterPeeringRow)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("NotFoundReturnsNilRow", func(tt *testing.T) {
		vol := makeVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		notFoundErr := customerrors.NewNotFoundErr("cluster peering row", nil)
		mockStorage.
			On("GetClusterPeerByAccountIDExternalClusterAndPoolID", ctx,
				vol.Account.ID, vol.CacheParameters.PeerClusterName, vol.Pool.ID).
			Return(nil, notFoundErr).Once()

		out, err := act.GetClusterPeeringRowFromDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, out)
		assert.Nil(tt, out.ClusterPeeringRow)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetError", func(tt *testing.T) {
		vol := makeVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mockStorage.
			On("GetClusterPeerByAccountIDExternalClusterAndPoolID", ctx,
				vol.Account.ID, vol.CacheParameters.PeerClusterName, vol.Pool.ID).
			Return(nil, assert.AnError).Once()

		out, err := act.GetClusterPeeringRowFromDBActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, out)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ErrorStateClearsRow", func(tt *testing.T) {
		vol := makeVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		existing := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: "peer-row-error"},
			AccountID:      vol.Account.ID,
			PoolID:         vol.Pool.ID,
			OnprempCluster: vol.CacheParameters.PeerClusterName,
			State:          datamodel.CvpClusterPeeringStatusERROR,
		}

		mockStorage.
			On("GetClusterPeerByAccountIDExternalClusterAndPoolID", ctx,
				vol.Account.ID, vol.CacheParameters.PeerClusterName, vol.Pool.ID).
			Return(existing, nil).Once()

		out, err := act.GetClusterPeeringRowFromDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, out)
		assert.Nil(tt, out.ClusterPeeringRow)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeCreateActivity_CreateClusterPeeringRowInDBActivity(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	act := &FlexCacheVolumeCreateActivity{SE: mockStorage}

	makeVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-create"},
			Account:   &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 55}},
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 77}},
			CacheParameters: &datamodel.CacheParameters{
				PeerClusterName: "peer-cluster-B",
			},
		}
	}

	t.Run("NoOpWhenExistingRow", func(tt *testing.T) {
		vol := makeVolume()
		existing := &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{UUID: "existing-row"},
			AccountID: vol.Account.ID,
			PoolID:    vol.Pool.ID,
		}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: existing,
		}

		// Expect no CreateClusterPeeringRow call.
		out, err := act.CreateClusterPeeringRowInDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, existing, out.ClusterPeeringRow)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CreateSuccess", func(tt *testing.T) {
		vol := makeVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mockStorage.
			On("CreateClusterPeeringRow", ctx, mock.MatchedBy(func(row *datamodel.ClusterPeerings) bool {
				return row.AccountID == vol.Account.ID &&
					row.PoolID == vol.Pool.ID &&
					row.OnprempCluster == vol.CacheParameters.PeerClusterName &&
					row.UUID != ""
			})).
			Return(func(_ context.Context, row *datamodel.ClusterPeerings) *datamodel.ClusterPeerings {
				// Simulate DB assigning same UUID passed in
				return row
			}, nil).Once()

		out, err := act.CreateClusterPeeringRowInDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, out.ClusterPeeringRow)
		assert.Equal(tt, vol.Account.ID, out.ClusterPeeringRow.AccountID)
		assert.Equal(tt, vol.Pool.ID, out.ClusterPeeringRow.PoolID)
		assert.Equal(tt, vol.CacheParameters.PeerClusterName, out.ClusterPeeringRow.OnprempCluster)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CreateError", func(tt *testing.T) {
		vol := makeVolume()
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol}

		mockStorage.
			On("CreateClusterPeeringRow", ctx, mock.Anything).
			Return(nil, assert.AnError).Once()

		out, err := act.CreateClusterPeeringRowInDBActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, out)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeCreateActivity_UpdateClusterPeeringInVolume(t *testing.T) {
	ctx := context.Background()
	activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(t)}

	baseVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "vol-upd-peering"},
			CacheParameters: &datamodel.CacheParameters{},
		}
	}

	t.Run("SuccessWithClusterPeeringRow", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity.SE = mockStorage
		vol := baseVolume()
		row := &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{ID: 321, UUID: "row-uuid"},
		}
		result := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: row,
		}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		allowAnyLogs(logger)

		mockStorage.
			On("UpdateVolumeFields", ctx, vol.UUID, mock.MatchedBy(func(upd map[string]interface{}) bool {
				cp, ok := upd["cluster_peer_id"].(sql.NullInt64)
				return ok && cp.Valid && cp.Int64 == int64(row.ID)
			})).
			Return(nil).Once()

		updated, err := activity.UpdateClusterPeeringInVolume(ctx, result)
		assert.NoError(tt, err)
		assert.Equal(tt, result, updated)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdateFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity.SE = mockStorage
		vol := baseVolume()
		row := &datamodel.ClusterPeerings{BaseModel: datamodel.BaseModel{ID: 777, UUID: "row-fail"}}
		result := &flexcache.CreateFlexCacheResult{DBVolume: vol, ClusterPeeringRow: row}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		allowAnyLogs(logger)

		mockStorage.
			On("UpdateVolumeFields", ctx, vol.UUID, mock.MatchedBy(func(upd map[string]interface{}) bool {
				cp, ok := upd["cluster_peer_id"].(sql.NullInt64)
				return ok && cp.Valid && cp.Int64 == int64(row.ID)
			})).
			Return(errors.New("db error")).Once()

		updated, err := activity.UpdateClusterPeeringInVolume(ctx, result)
		assert.Nil(tt, updated)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func Test_updateClusterPeeringRowStateInDBActivity(t *testing.T) {
	ctx := context.Background()

	newVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "vol-uuid"},
			CacheParameters: &datamodel.CacheParameters{}, // kept minimal
		}
	}
	newRow := func(uuid string) *datamodel.ClusterPeerings {
		return &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{UUID: uuid, ID: 1},
			State:     datamodel.CvpClusterPeeringStatusCREATING,
		}
	}
	newPeer := func() *vsa.ClusterPeer {
		return &vsa.ClusterPeer{UUID: "peer-uuid"}
	}

	t.Run("PendingWithClusterPeer", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}
		row := newRow("row-pending-peer")
		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          newVolume(),
			ClusterPeeringRow: row,
			ClusterPeer:       newPeer(),
		}
		ms.On("UpdateClusterPeeringRow", ctx, mock.MatchedBy(func(r *datamodel.ClusterPeerings) bool {
			return r.State == datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING
		})).Return(nil).Once()

		out, err := act.updateClusterPeeringRowStateInDBActivity(ctx, res, datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING)
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING, out.ClusterPeeringRow.State)
		ms.AssertExpectations(tt)
	})

	t.Run("PendingWithoutClusterPeer", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}
		row := newRow("row-pending-np")
		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          newVolume(),
			ClusterPeeringRow: row,
		}
		ms.On("UpdateClusterPeeringRow", ctx, mock.MatchedBy(func(r *datamodel.ClusterPeerings) bool {
			return r.State == datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING
		})).Return(nil).Once()

		out, err := act.updateClusterPeeringRowStateInDBActivity(ctx, res, datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING)
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING, out.ClusterPeeringRow.State)
		ms.AssertExpectations(tt)
	})

	t.Run("PeeredState", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}
		row := newRow("row-peered")
		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          newVolume(),
			ClusterPeeringRow: row,
		}
		ms.On("UpdateClusterPeeringRow", ctx, mock.MatchedBy(func(r *datamodel.ClusterPeerings) bool {
			return r.State == datamodel.CvpClusterPeeringStatusPEERED
		})).Return(nil).Once()

		out, err := act.updateClusterPeeringRowStateInDBActivity(ctx, res, datamodel.CvpClusterPeeringStatusPEERED)
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.CvpClusterPeeringStatusPEERED, out.ClusterPeeringRow.State)
		ms.AssertExpectations(tt)
	})

	t.Run("ErrorState", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}
		row := newRow("row-error")
		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          newVolume(),
			ClusterPeeringRow: row,
			ClusterPeer:       newPeer(), // should not affect overwrite logic
		}
		ms.On("UpdateClusterPeeringRow", ctx, mock.MatchedBy(func(r *datamodel.ClusterPeerings) bool {
			return r.State == datamodel.CvpClusterPeeringStatusERROR
		})).Return(nil).Once()

		out, err := act.updateClusterPeeringRowStateInDBActivity(ctx, res, datamodel.CvpClusterPeeringStatusERROR)
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.CvpClusterPeeringStatusERROR, out.ClusterPeeringRow.State)
		ms.AssertExpectations(tt)
	})

	t.Run("UpdateFails", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}
		row := newRow("row-update-fail")
		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          newVolume(),
			ClusterPeeringRow: row,
		}
		ms.On("UpdateClusterPeeringRow", ctx, mock.Anything).Return(errors.New("db fail")).Once()

		out, err := act.updateClusterPeeringRowStateInDBActivity(ctx, res, datamodel.CvpClusterPeeringStatusPEERED)
		assert.Nil(tt, out)
		assert.Error(tt, err)
		ms.AssertExpectations(tt)
	})

	t.Run("InvalidState", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}
		row := newRow("row-invalid")
		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          newVolume(),
			ClusterPeeringRow: row,
		}
		out, err := act.updateClusterPeeringRowStateInDBActivity(ctx, res, datamodel.ClusterPeeringStatus("BAD_STATE"))
		assert.Nil(tt, out)
		assert.Error(tt, err)
		ms.AssertExpectations(tt)
	})
}

func TestUpdateClusterPeeringRowStatePendingInDBActivity(t *testing.T) {
	ctx := context.Background()
	activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(t)}

	newVolume := func() *datamodel.Volume {
		pass := nillable.ToPointer("passphrase-1")
		cmd := nillable.ToPointer("cluster peer create ...")
		exp := time.Now().Add(10 * time.Minute)
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-1"},
			CacheParameters: &datamodel.CacheParameters{
				Passphrase:        pass,
				Command:           cmd,
				CommandExpiryTime: &exp,
			},
		}
	}

	t.Run("SuccessWithClusterPeer", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		activity.SE = ms
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		allowAnyLogs(logger)

		vol := newVolume()
		row := &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{UUID: "row-pending-peer"},
			State:     datamodel.ClusterPeeringStatus(""),
		}
		clusterPeer := &vsa.ClusterPeer{
			UUID:            "cp-uuid",
			ExternalUUID:    "ext-peer-uuid",
			PeerClusterName: "onprem-cluster-A",
		}

		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: row,
			ClusterPeer:       clusterPeer,
		}

		ms.
			On("UpdateClusterPeeringRow", ctx, mock.MatchedBy(func(r *datamodel.ClusterPeerings) bool {
				assert.Equal(tt, clusterPeer.ExternalUUID, r.OntapPeerUUID)
				assert.Equal(tt, clusterPeer.PeerClusterName, r.OnprempCluster)
				assert.Equal(tt, datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING, r.State)
				if assert.NotNil(tt, r.ClusterPeeringAttributes) {
					assert.Equal(tt, vol.CacheParameters.Passphrase, r.ClusterPeeringAttributes.PassPhrase)
					assert.Equal(tt, vol.CacheParameters.Command, r.ClusterPeeringAttributes.Command)
					assert.Equal(tt, vol.CacheParameters.CommandExpiryTime, r.ClusterPeeringAttributes.ExpiryTime)
				}
				return true
			})).
			Return(nil).Once()

		out, err := activity.UpdateClusterPeeringRowStatePendingInDBActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING, out.ClusterPeeringRow.State)
		ms.AssertExpectations(tt)
	})

	t.Run("UpdateFails", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		activity.SE = ms

		vol := newVolume()
		row := &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{UUID: "row-update-fail"},
		}
		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: row,
		}

		ms.On("UpdateClusterPeeringRow", ctx, mock.Anything).Return(errors.New("db failure")).Once()

		out, err := activity.UpdateClusterPeeringRowStatePendingInDBActivity(ctx, res)
		assert.Nil(tt, out)
		assert.Error(tt, err)
		ms.AssertExpectations(tt)
	})
}

func TestUpdateClusterPeeringRowStatePeeredInDBActivity(t *testing.T) {
	ctx := context.Background()
	activity := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(t)}

	newVolume := func() *datamodel.Volume {
		pass := nillable.ToPointer("passphrase-1")
		cmd := nillable.ToPointer("cluster peer create ...")
		exp := time.Now().Add(10 * time.Minute)
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-1"},
			CacheParameters: &datamodel.CacheParameters{
				Passphrase:        pass,
				Command:           cmd,
				CommandExpiryTime: &exp,
			},
		}
	}

	t.Run("SuccessWithClusterPeer", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		activity.SE = ms

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		allowAnyLogs(logger)

		exp := time.Now().Add(2 * time.Minute)

		vol := newVolume()
		row := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: "row-peered-success"},
			OntapPeerUUID:  "ontap-peer-uuid-old",
			OnprempCluster: "onprem-cluster-old",
			ClusterPeeringAttributes: &datamodel.ClusterPeeringAttributes{
				PassPhrase: nillable.ToPointer("row-pass-old"),
				Command:    nillable.ToPointer("row-cmd-old"),
				ExpiryTime: &exp,
			},
		}
		clusterPeer := &vsa.ClusterPeer{
			UUID:                "ontap-peer-uuid-new",
			AuthenticationState: vsa.ClusterPeerAuthenticationStateOK,
			Availability:        vsa.ClusterPeerAvailabilityStateAvailable,
		}
		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: row,
			ClusterPeer:       clusterPeer,
		}

		ms.
			On("UpdateClusterPeeringRow", ctx, mock.MatchedBy(func(r *datamodel.ClusterPeerings) bool {
				return r.UUID == row.UUID &&
					r.State == datamodel.CvpClusterPeeringStatusPEERED &&
					r.OntapPeerUUID == row.OntapPeerUUID &&
					r.ClusterPeeringAttributes.PassPhrase == row.ClusterPeeringAttributes.PassPhrase
			})).
			Return(nil).
			Once()

		out, err := activity.UpdateClusterPeeringRowStatePeeredInDBActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.CvpClusterPeeringStatusPEERED, out.ClusterPeeringRow.State)
		ms.AssertExpectations(tt)
	})

	t.Run("UpdateFails", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		activity.SE = ms
		exp := time.Now().Add(5 * time.Minute)

		vol := newVolume()
		row := &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{UUID: "row-peered-fail"},
			ClusterPeeringAttributes: &datamodel.ClusterPeeringAttributes{
				PassPhrase: nillable.ToPointer("row-pass"),
				Command:    nillable.ToPointer("row-cmd"),
				ExpiryTime: &exp,
			},
		}
		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: row,
		}

		ms.On("UpdateClusterPeeringRow", ctx, mock.Anything).Return(errors.New("db failure")).Once()

		out, err := activity.UpdateClusterPeeringRowStatePeeredInDBActivity(ctx, res)
		assert.Nil(tt, out)
		assert.Error(tt, err)
		ms.AssertExpectations(tt)
	})
}

func TestUpdateClusterPeeringRowStateErrorInDBActivity(t *testing.T) {
	ctx := context.Background()
	act := &FlexCacheVolumeCreateActivity{SE: database.NewMockStorage(t)}
	exp := time.Now().Add(30 * time.Minute)

	newVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-error"},
			CacheParameters: &datamodel.CacheParameters{
				Passphrase:        nillable.ToPointer("vol-pass"),
				Command:           nillable.ToPointer("vol-cmd"),
				CommandExpiryTime: &exp,
			},
		}
	}

	timePtr := func(t time.Time) *time.Time { return &t }

	t.Run("SuccessWithClusterPeer", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act.SE = ms
		vol := newVolume()

		// Existing row already has attributes that must remain unchanged.
		pass := nillable.ToPointer("row-pass")
		cmd := nillable.ToPointer("row-cmd")
		exp := time.Now().Add(10 * time.Minute)
		row := &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{UUID: "row-error-with-peer"},
			State:     datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING,
			ClusterPeeringAttributes: &datamodel.ClusterPeeringAttributes{
				PassPhrase: pass,
				Command:    cmd,
				ExpiryTime: &exp,
			},
		}

		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: row,
			ClusterPeer:       &vsa.ClusterPeer{UUID: "peer-uuid"},
		}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		allowAnyLogs(logger)

		ms.On("UpdateClusterPeeringRow", ctx, mock.MatchedBy(func(r *datamodel.ClusterPeerings) bool {
			return r.UUID == row.UUID &&
				r.State == datamodel.CvpClusterPeeringStatusERROR &&
				r.ClusterPeeringAttributes == row.ClusterPeeringAttributes &&
				r.ClusterPeeringAttributes.PassPhrase == pass &&
				r.ClusterPeeringAttributes.Command == cmd &&
				r.ClusterPeeringAttributes.ExpiryTime.Equal(exp)
		})).Return(nil).Once()

		out, err := act.UpdateClusterPeeringRowStateErrorInDBActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.CvpClusterPeeringStatusERROR, out.ClusterPeeringRow.State)
		ms.AssertExpectations(tt)
	})

	t.Run("UpdateFails", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act.SE = ms
		vol := newVolume()

		row := &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{UUID: "row-error-update-fail"},
			State:     datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING,
			ClusterPeeringAttributes: &datamodel.ClusterPeeringAttributes{
				PassPhrase: nillable.ToPointer("keep-pass"),
				Command:    nillable.ToPointer("keep-cmd"),
				ExpiryTime: timePtr(time.Now().Add(5 * time.Minute)),
			},
		}

		res := &flexcache.CreateFlexCacheResult{
			DBVolume:          vol,
			ClusterPeeringRow: row,
		}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		allowAnyLogs(logger)

		ms.On("UpdateClusterPeeringRow", ctx, mock.Anything).Return(errors.New("db failure")).Once()

		out, err := act.UpdateClusterPeeringRowStateErrorInDBActivity(ctx, res)
		assert.Nil(tt, out)
		assert.Error(tt, err)
		ms.AssertExpectations(tt)
	})
}

func Test_getActiveJobs(t *testing.T) {
	ctx := context.Background()
	resourceUUID := "res-123"
	jobType := datamodel.JobTypeFlexCacheCreateVolume
	testJob := func(id string, state datamodel.JobState, resourceUUID string, jt datamodel.JobType) *datamodel.Job {
		return &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: id},
			Type:      string(jt),
			State:     string(state),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: resourceUUID,
			},
		}
	}

	t.Run("SuccessActiveJobsReturned", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}

		active1 := testJob("j1", datamodel.JobsStatePROCESSING, resourceUUID, jobType)
		active2 := testJob("j2", datamodel.JobsStateDONE, resourceUUID, jobType)
		// Terminal states should be filtered out by implementation, but we return only actives here.
		ms.
			On("GetJobsWithCondition", ctx, mock.Anything).
			Return([]*datamodel.Job{active1, active2}, nil).Once()

		got, err := act.getActiveJobs(ctx, resourceUUID, jobType)
		assert.NoError(tt, err)
		assert.Equal(tt, []*datamodel.Job{active1, active2}, got)
		ms.AssertExpectations(tt)
	})

	t.Run("NoActiveJobs", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}

		ms.
			On("GetJobsWithCondition", ctx, mock.Anything).
			Return([]*datamodel.Job{}, nil).Once()

		got, err := act.getActiveJobs(ctx, resourceUUID, jobType)
		assert.NoError(tt, err)
		assert.Len(tt, got, 0)
		ms.AssertExpectations(tt)
	})

	t.Run("StorageError", func(tt *testing.T) {
		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}

		ms.
			On("GetJobsWithCondition", ctx, mock.Anything).
			Return(nil, assert.AnError).Once()

		got, err := act.getActiveJobs(ctx, resourceUUID, jobType)
		assert.Error(tt, err)
		assert.Nil(tt, got)
		ms.AssertExpectations(tt)
	})

	t.Run("WhenEnableJobResourceUUIDIndex_UsesColumnFilter", func(tt *testing.T) {
		vcputils.EnableJobResourceUUIDIndex = true
		defer func() { vcputils.EnableJobResourceUUIDIndex = false }()

		ms := database.NewMockStorage(tt)
		act := &FlexCacheVolumeCreateActivity{SE: ms}

		active1 := testJob("j1", datamodel.JobsStatePROCESSING, resourceUUID, jobType)
		ms.
			On("GetJobsWithCondition", ctx, mock.Anything).
			Return([]*datamodel.Job{active1}, nil).Once()

		got, err := act.getActiveJobs(ctx, resourceUUID, jobType)
		assert.NoError(tt, err)
		assert.Equal(tt, []*datamodel.Job{active1}, got)
		ms.AssertExpectations(tt)
	})
}

func TestEnsureExternalPeerRole(t *testing.T) {
	ctx := context.Background()

	t.Run("Success_RoleCreated", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mockProvider.EXPECT().CreateRole(mock.MatchedBy(func(params vsa.CreateRoleParams) bool {
			return params.Name == activities.OnPremPeerRoleName &&
				len(params.Privileges) == 3 &&
				params.Privileges[0].Path == "DEFAULT" &&
				params.Privileges[0].Access == "none" &&
				params.Privileges[1].Path == "debug" &&
				params.Privileges[1].Access == "none" &&
				params.Privileges[2].Path == "system capability clusterset show" &&
				params.Privileges[2].Access == "readonly" &&
				params.Privileges[2].Query == "-capability DATA_ONTAP.9.2.0"
		})).Return("role-location", nil)
		logger.EXPECT().Infof("Successfully created role %s", activities.OnPremPeerRoleName)

		err := EnsureExternalPeerRole(ctx, mockProvider)
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Success_RoleAlreadyExists_StandardError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mockProvider.EXPECT().CreateRole(mock.Anything).Return("", errors.New("Role already exists"))
		logger.EXPECT().Debugf("Role %s already exists, skipping creation", activities.OnPremPeerRoleName)

		err := EnsureExternalPeerRole(ctx, mockProvider)
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Success_RoleAlreadyExists_LegacyTableError", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mockProvider.EXPECT().CreateRole(mock.Anything).Return("", errors.New("Role already exists in legacy role table"))
		logger.EXPECT().Debugf("Role %s already exists, skipping creation", activities.OnPremPeerRoleName)

		err := EnsureExternalPeerRole(ctx, mockProvider)
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_ProviderCreateRoleFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)

		expectedError := errors.New("failed to connect to ONTAP")

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mockProvider.EXPECT().CreateRole(mock.Anything).Return("", expectedError)
		logger.EXPECT().Errorf("Failed to create role %s: %v", activities.OnPremPeerRoleName, mock.Anything)

		err := EnsureExternalPeerRole(ctx, mockProvider)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_ProviderCreateRoleReturnsEmptyString", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)

		expectedError := errors.New("invalid role parameters")

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mockProvider.EXPECT().CreateRole(mock.Anything).Return("", expectedError)
		logger.EXPECT().Errorf("Failed to create role %s: %v", activities.OnPremPeerRoleName, mock.Anything)

		err := EnsureExternalPeerRole(ctx, mockProvider)
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Idempotent_MultipleCalls", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)

		mm.EXPECT().utilGetLogger(ctx).Return(logger).Times(2)

		// First call - role doesn't exist, create it
		mockProvider.EXPECT().CreateRole(mock.Anything).Return("role-location", nil).Once()
		logger.EXPECT().Infof("Successfully created role %s", activities.OnPremPeerRoleName).Once()

		// Second call - role already exists
		mockProvider.EXPECT().CreateRole(mock.Anything).Return("", errors.New("Role already exists")).Once()
		logger.EXPECT().Debugf("Role %s already exists, skipping creation", activities.OnPremPeerRoleName).Once()

		// First call
		err1 := EnsureExternalPeerRole(ctx, mockProvider)
		assert.NoError(tt, err1)

		// Second call
		err2 := EnsureExternalPeerRole(ctx, mockProvider)
		assert.NoError(tt, err2)

		mockProvider.AssertExpectations(tt)
		logger.AssertExpectations(tt)
	})
}
