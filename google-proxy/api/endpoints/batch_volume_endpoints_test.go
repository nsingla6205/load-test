package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func stubBatchVolumeParseRegionAndZone() func() {
	orig := parseAndValidateRegionAndZone
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		if locationID == "invalid location!" {
			return "", "", &gcpgenserver.Error{Code: 400, Message: "Invalid location"}
		}
		return locationID, "", nil
	}
	return func() { parseAndValidateRegionAndZone = orig }
}

func batchVolumeRequestContext() context.Context {
	return context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, http.Header{})
}

func makeVCPVolume(uuid, name string) *models.Volume {
	return &models.Volume{
		BaseModel:      models.BaseModel{UUID: uuid, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		DisplayName:    name,
		LifeCycleState: "READY",
		ServiceLevel:   "PREMIUM",
		Zone:           "us-east4-a",
		Region:         "us-east4",
	}
}

func convertVCPVolumeToBatchVolumeForTest(
	t *testing.T,
	volume *models.Volume,
	fieldSet map[string]bool,
) gcpgenserver.BatchVolumeV1beta {
	t.Helper()

	converted := convertModelToVCPVolume(volume)
	require.NotNil(t, converted)

	updateVCPVolumeBaseForBatch(volume, converted)

	out, err := convertVolumeModelToBatchVolume(log.NewLogger(), *converted, fieldSet)
	require.NoError(t, err)

	return out
}

func allBatchVolumeFields() []gcpgenserver.V1betaBatchListVolumesFieldsItem {
	return []gcpgenserver.V1betaBatchListVolumesFieldsItem{
		gcpgenserver.V1betaBatchListVolumesFieldsItemResourceId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemVolumeId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemCreated,
		gcpgenserver.V1betaBatchListVolumesFieldsItemCreationToken,
		gcpgenserver.V1betaBatchListVolumesFieldsItemPoolId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemKmsConfigId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemKmsConfigResourceId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemNetwork,
		gcpgenserver.V1betaBatchListVolumesFieldsItemActiveDirectoryConfigId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemActiveDirectoryResourceId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemServiceLevel,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSecurityStyle,
		gcpgenserver.V1betaBatchListVolumesFieldsItemUsedBytes,
		gcpgenserver.V1betaBatchListVolumesFieldsItemQuotaInBytes,
		gcpgenserver.V1betaBatchListVolumesFieldsItemThroughputMibps,
		gcpgenserver.V1betaBatchListVolumesFieldsItemColdTierSizeGib,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSnapReserve,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSnapshotDirectory,
		gcpgenserver.V1betaBatchListVolumesFieldsItemVolumeState,
		gcpgenserver.V1betaBatchListVolumesFieldsItemVolumeStateDetails,
		gcpgenserver.V1betaBatchListVolumesFieldsItemIsDataProtection,
		gcpgenserver.V1betaBatchListVolumesFieldsItemInReplication,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSnapshotPolicy,
		gcpgenserver.V1betaBatchListVolumesFieldsItemStorageClass,
		gcpgenserver.V1betaBatchListVolumesFieldsItemExportPolicy,
		gcpgenserver.V1betaBatchListVolumesFieldsItemBackupConfig,
		gcpgenserver.V1betaBatchListVolumesFieldsItemTieringPolicy,
		gcpgenserver.V1betaBatchListVolumesFieldsItemBlockProperties,
		gcpgenserver.V1betaBatchListVolumesFieldsItemBlockDevices,
		gcpgenserver.V1betaBatchListVolumesFieldsItemProtocols,
		gcpgenserver.V1betaBatchListVolumesFieldsItemRestrictedActions,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSmbSettings,
		gcpgenserver.V1betaBatchListVolumesFieldsItemMountPoints,
		gcpgenserver.V1betaBatchListVolumesFieldsItemLabels,
		gcpgenserver.V1betaBatchListVolumesFieldsItemKerberosEnabled,
		gcpgenserver.V1betaBatchListVolumesFieldsItemLdapEnabled,
		gcpgenserver.V1betaBatchListVolumesFieldsItemUnixPermissions,
		gcpgenserver.V1betaBatchListVolumesFieldsItemEncryptionType,
		gcpgenserver.V1betaBatchListVolumesFieldsItemDescription,
		gcpgenserver.V1betaBatchListVolumesFieldsItemZone,
		gcpgenserver.V1betaBatchListVolumesFieldsItemMultipleEndpoints,
		gcpgenserver.V1betaBatchListVolumesFieldsItemLargeCapacity,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSecondaryZone,
		gcpgenserver.V1betaBatchListVolumesFieldsItemDedicatedCapacity,
		gcpgenserver.V1betaBatchListVolumesFieldsItemLargeVolumeConstituentCount,
		gcpgenserver.V1betaBatchListVolumesFieldsItemCacheParameters,
		gcpgenserver.V1betaBatchListVolumesFieldsItemHotTierSizeGib,
		gcpgenserver.V1betaBatchListVolumesFieldsItemCloneDetails,
		gcpgenserver.V1betaBatchListVolumesFieldsItemRegion,
	}
}

func TestV1betaBatchListVolumes_AuthAndValidation(t *testing.T) {
	t.Run("NilHTTPRequest_ReturnsUnauthorized", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()

		handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		res, err := handler.V1betaBatchListVolumes(context.Background(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesUnauthorized)
		assert.True(tt, ok)
	})

	t.Run("InvalidLocation_ReturnsBadRequest", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()

		handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "invalid location!"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesBadRequest)
		assert.True(tt, ok)
	})

	t.Run("EmptyUuids_ReturnsBadRequest", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()

		handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListVolumesBadRequest)
		require.True(tt, ok)
		assert.Contains(tt, badReq.Message, "volumeUuids is required")
	})

	t.Run("TooManyUuids_ReturnsBadRequest", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()

		origMax := maxBatchVolumeUUIDs
		maxBatchVolumeUUIDs = 1
		defer func() { maxBatchVolumeUUIDs = origMax }()

		handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1", "uuid-2"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesBadRequest)
		assert.True(tt, ok)
	})
}

func TestV1betaBatchListVolumes_VCPAndParallelPaths(t *testing.T) {
	restoreParse := stubBatchVolumeParseRegionAndZone()
	defer restoreParse()

	t.Run("VCPOnlySuccess", func(tt *testing.T) {
		cvp.SetCVPHost("")
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.EXPECT().GetVolumesByUUIDs(mock.Anything, []string{"uuid-1"}, mock.Anything).Return([]*models.Volume{makeVCPVolume("uuid-1", "vol-1")}, nil).Once()
		handler := &Handler{Orchestrator: mockOrch}

		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{
			LocationId: "us-east4",
			Fields:     allBatchVolumeFields(),
		})
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListVolumesOK)
		require.True(tt, ok)
		assert.Len(tt, okRes.Volumes, 1)
	})

	t.Run("VCPOnlyError", func(tt *testing.T) {
		cvp.SetCVPHost("")
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.EXPECT().GetVolumesByUUIDs(mock.Anything, []string{"uuid-1"}, mock.Anything).Return(nil, errors.New("db error")).Once()
		handler := &Handler{Orchestrator: mockOrch}

		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("ParallelBothFail", func(tt *testing.T) {
		cvp.SetCVPHost("http://cvp-host")
		origFetch := fetchBatchVolumesFromCVPFn
		defer func() { fetchBatchVolumesFromCVPFn = origFetch }()
		fetchBatchVolumesFromCVPFn = func(ctx context.Context, volumeUUIDs []string, params gcpgenserver.V1betaBatchListVolumesParams, fieldSet map[string]bool) ([]gcpgenserver.BatchVolumeV1beta, error) {
			return nil, errors.New("cvp error")
		}

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.EXPECT().GetVolumesByUUIDs(mock.Anything, []string{"uuid-1"}, mock.Anything).Return(nil, errors.New("vcp error")).Once()
		handler := &Handler{Orchestrator: mockOrch}

		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("ParallelPartialSuccess", func(tt *testing.T) {
		cvp.SetCVPHost("http://cvp-host")
		origFetch := fetchBatchVolumesFromCVPFn
		defer func() { fetchBatchVolumesFromCVPFn = origFetch }()
		fetchBatchVolumesFromCVPFn = func(ctx context.Context, volumeUUIDs []string, params gcpgenserver.V1betaBatchListVolumesParams, fieldSet map[string]bool) ([]gcpgenserver.BatchVolumeV1beta, error) {
			return []gcpgenserver.BatchVolumeV1beta{{VolumeId: "cvp-uuid"}}, nil
		}

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.EXPECT().GetVolumesByUUIDs(mock.Anything, []string{"uuid-1"}, mock.Anything).Return([]*models.Volume{makeVCPVolume("uuid-1", "vol-1")}, nil).Once()
		handler := &Handler{Orchestrator: mockOrch}

		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListVolumesOK)
		require.True(tt, ok)
		assert.Len(tt, okRes.Volumes, 2)
	})
}

func TestFetchBatchVolumesFromCVP(t *testing.T) {
	t.Run("SuccessWithPayload", func(tt *testing.T) {
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{Batch: mockBatch}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		resourceID := "cvp-vol"
		mockBatch.EXPECT().V1betaBatchListVolumes(mock.Anything).Return(&cvpBatch.V1betaBatchListVolumesOK{
			Payload: &cvpBatch.V1betaBatchListVolumesOKBody{
				Volumes: []*cvpmodels.BatchVolumeV1beta{{
					VolumeID:   "uuid-1",
					ResourceID: &resourceID,
				}},
			},
		}, nil)

		res, err := fetchBatchVolumesFromCVP(context.Background(), []string{"uuid-1"}, gcpgenserver.V1betaBatchListVolumesParams{
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("corr-1"),
			Fields:         []gcpgenserver.V1betaBatchListVolumesFieldsItem{gcpgenserver.V1betaBatchListVolumesFieldsItemResourceId},
		}, map[string]bool{"resourceId": true})
		require.NoError(tt, err)
		require.Len(tt, res, 1)
		assert.Equal(tt, "uuid-1", res[0].VolumeId)
		assert.Equal(tt, "cvp-vol", res[0].ResourceId.Value)
	})

	t.Run("ClientError", func(tt *testing.T) {
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{Batch: mockBatch}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		mockBatch.EXPECT().V1betaBatchListVolumes(mock.Anything).Return(nil, errors.New("cvp failed"))

		res, err := fetchBatchVolumesFromCVP(context.Background(), []string{"uuid-1"}, gcpgenserver.V1betaBatchListVolumesParams{
			LocationId: "us-east4",
		}, nil)
		assert.Error(tt, err)
		assert.Nil(tt, res)
	})

	t.Run("NilPayloadReturnsEmpty", func(tt *testing.T) {
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{Batch: mockBatch}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		mockBatch.EXPECT().V1betaBatchListVolumes(mock.Anything).Return(&cvpBatch.V1betaBatchListVolumesOK{
			Payload: &cvpBatch.V1betaBatchListVolumesOKBody{},
		}, nil)

		res, err := fetchBatchVolumesFromCVP(context.Background(), []string{"uuid-1"}, gcpgenserver.V1betaBatchListVolumesParams{
			LocationId: "us-east4",
		}, nil)
		require.NoError(tt, err)
		assert.Empty(tt, res)
	})
}

func TestBatchVolumeFieldSelectionAndDefaultsHelpers(t *testing.T) {
	t.Run("ApplySelectionWithNilFieldSetResetsAllOptionalFields", func(tt *testing.T) {
		bv := gcpgenserver.BatchVolumeV1beta{
			VolumeId:    "vol-1",
			ResourceId:  gcpgenserver.NewOptNilString("res"),
			Description: gcpgenserver.NewOptNilString("desc"),
			Zone:        gcpgenserver.NewOptNilString("us-east4-a"),
		}
		applyBatchVolumeFieldSelection(&bv, nil)
		assert.Equal(tt, "vol-1", bv.VolumeId)
		assert.False(tt, bv.ResourceId.Set)
		assert.False(tt, bv.Description.Set)
		assert.False(tt, bv.Zone.Set)
	})

	t.Run("ApplySelectionFillsMissingRequestedValues", func(tt *testing.T) {
		bv := &gcpgenserver.BatchVolumeV1beta{VolumeId: "vol-1"}
		fieldSet := buildBatchVolumeFieldSet(allBatchVolumeFields())
		applyBatchVolumeFieldSelection(bv, fieldSet)
		assert.True(tt, bv.ResourceId.Null)
		assert.Equal(tt, gcpgenserver.BatchVolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED, bv.ServiceLevel.Value)
		assert.Equal(tt, gcpgenserver.BatchVolumeV1betaSecurityStyleSECURITYSTYLEUNSPECIFIED, bv.SecurityStyle.Value)
		assert.Equal(tt, gcpgenserver.BatchVolumeV1betaVolumeStateSTATEUNSPECIFIED, bv.VolumeState.Value)
		assert.Equal(tt, gcpgenserver.BatchVolumeV1betaStorageClassSTORAGECLASSUNSPECIFIED, bv.StorageClass.Value)
		assert.Equal(tt, []gcpgenserver.BatchVolumeV1betaRestrictedActionsItem{
			gcpgenserver.BatchVolumeV1betaRestrictedActionsItemRESTRICTEDACTIONUNSPECIFIED,
		}, bv.RestrictedActions.Value)
		assert.True(tt, bv.DedicatedCapacity.Null)
		assert.True(tt, bv.Region.Null)
	})

	t.Run("GetBatchVolumeHTTPRequestFromContextHandlesInvalidContext", func(tt *testing.T) {
		assert.Nil(tt, getHTTPRequestFromContext(context.Background()))
		assert.Nil(tt, getHTTPRequestFromContext(context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, "not-header")))
	})
}

func TestConvertVolumeModelToBatchVolume_RequestedMissingEnumsUseUnspecifiedDefaults(t *testing.T) {
	fieldSet := buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
		gcpgenserver.V1betaBatchListVolumesFieldsItemServiceLevel,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSecurityStyle,
		gcpgenserver.V1betaBatchListVolumesFieldsItemStorageClass,
		gcpgenserver.V1betaBatchListVolumesFieldsItemVolumeState,
		gcpgenserver.V1betaBatchListVolumesFieldsItemEncryptionType,
	})

	out, err := convertVolumeModelToBatchVolume(log.NewLogger(), gcpgenserver.VolumeV1beta{
		VolumeId: gcpgenserver.NewOptString("vol-missing-enums"),
	}, fieldSet)
	require.NoError(t, err)

	assert.Equal(t, "vol-missing-enums", out.VolumeId)
	assert.True(t, out.ServiceLevel.Set)
	assert.False(t, out.ServiceLevel.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED, out.ServiceLevel.Value)

	assert.True(t, out.SecurityStyle.Set)
	assert.False(t, out.SecurityStyle.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaSecurityStyleSECURITYSTYLEUNSPECIFIED, out.SecurityStyle.Value)

	assert.True(t, out.StorageClass.Set)
	assert.False(t, out.StorageClass.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaStorageClassSTORAGECLASSUNSPECIFIED, out.StorageClass.Value)

	assert.True(t, out.VolumeState.Set)
	assert.False(t, out.VolumeState.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaVolumeStateSTATEUNSPECIFIED, out.VolumeState.Value)

	assert.True(t, out.EncryptionType.Set)
	assert.False(t, out.EncryptionType.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaEncryptionTypeENCRYPTIONTYPEUNSPECIFIED, out.EncryptionType.Value)
}

func TestConvertVolumeModelToBatchVolume_RequestedExplicitUnspecifiedEnumsRemainUnspecified(t *testing.T) {
	fieldSet := buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
		gcpgenserver.V1betaBatchListVolumesFieldsItemServiceLevel,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSecurityStyle,
		gcpgenserver.V1betaBatchListVolumesFieldsItemStorageClass,
		gcpgenserver.V1betaBatchListVolumesFieldsItemVolumeState,
		gcpgenserver.V1betaBatchListVolumesFieldsItemEncryptionType,
	})

	out, err := convertVolumeModelToBatchVolume(log.NewLogger(), gcpgenserver.VolumeV1beta{
		VolumeId:       gcpgenserver.NewOptString("vol-explicit-unspecified"),
		ServiceLevel:   gcpgenserver.NewOptVolumeV1betaServiceLevel(gcpgenserver.VolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED),
		SecurityStyle:  gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleSECURITYSTYLEUNSPECIFIED),
		StorageClass:   gcpgenserver.NewOptStorageClassV1beta(gcpgenserver.StorageClassV1betaSTORAGECLASSUNSPECIFIED),
		VolumeState:    gcpgenserver.NewOptVolumeV1betaVolumeState(gcpgenserver.VolumeV1betaVolumeStateSTATEUNSPECIFIED),
		EncryptionType: gcpgenserver.NewOptVolumeV1betaEncryptionType(gcpgenserver.VolumeV1betaEncryptionTypeENCRYPTIONTYPEUNSPECIFIED),
	}, fieldSet)
	require.NoError(t, err)

	assert.Equal(t, "vol-explicit-unspecified", out.VolumeId)
	assert.True(t, out.ServiceLevel.Set)
	assert.False(t, out.ServiceLevel.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED, out.ServiceLevel.Value)

	assert.True(t, out.SecurityStyle.Set)
	assert.False(t, out.SecurityStyle.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaSecurityStyleSECURITYSTYLEUNSPECIFIED, out.SecurityStyle.Value)

	assert.True(t, out.StorageClass.Set)
	assert.False(t, out.StorageClass.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaStorageClassSTORAGECLASSUNSPECIFIED, out.StorageClass.Value)

	assert.True(t, out.VolumeState.Set)
	assert.False(t, out.VolumeState.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaVolumeStateSTATEUNSPECIFIED, out.VolumeState.Value)

	assert.True(t, out.EncryptionType.Set)
	assert.False(t, out.EncryptionType.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaEncryptionTypeENCRYPTIONTYPEUNSPECIFIED, out.EncryptionType.Value)
}

func TestConvertVolumeModelToBatchVolume_RequestedExplicitEnumValuesArePreserved(t *testing.T) {
	fieldSet := buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
		gcpgenserver.V1betaBatchListVolumesFieldsItemServiceLevel,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSecurityStyle,
		gcpgenserver.V1betaBatchListVolumesFieldsItemStorageClass,
		gcpgenserver.V1betaBatchListVolumesFieldsItemVolumeState,
		gcpgenserver.V1betaBatchListVolumesFieldsItemEncryptionType,
	})

	out, err := convertVolumeModelToBatchVolume(log.NewLogger(), gcpgenserver.VolumeV1beta{
		VolumeId:       gcpgenserver.NewOptString("vol-explicit-values"),
		ServiceLevel:   gcpgenserver.NewOptVolumeV1betaServiceLevel(gcpgenserver.VolumeV1betaServiceLevelPREMIUM),
		SecurityStyle:  gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleUNIX),
		StorageClass:   gcpgenserver.NewOptStorageClassV1beta(gcpgenserver.StorageClassV1betaSOFTWARE),
		VolumeState:    gcpgenserver.NewOptVolumeV1betaVolumeState(gcpgenserver.VolumeV1betaVolumeStateREADY),
		EncryptionType: gcpgenserver.NewOptVolumeV1betaEncryptionType(gcpgenserver.VolumeV1betaEncryptionTypeSERVICEMANAGED),
	}, fieldSet)
	require.NoError(t, err)

	assert.Equal(t, "vol-explicit-values", out.VolumeId)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaServiceLevelPREMIUM, out.ServiceLevel.Value)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaSecurityStyleUNIX, out.SecurityStyle.Value)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaStorageClassSOFTWARE, out.StorageClass.Value)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaVolumeStateREADY, out.VolumeState.Value)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaEncryptionTypeSERVICEMANAGED, out.EncryptionType.Value)
}

func TestConvertVolumeModelToBatchVolume_PreservesCommonScalarFields(t *testing.T) {
	now := time.Date(2026, 3, 30, 13, 4, 4, 0, time.UTC)
	fieldSet := buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
		gcpgenserver.V1betaBatchListVolumesFieldsItemResourceId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemCreated,
		gcpgenserver.V1betaBatchListVolumesFieldsItemCreationToken,
		gcpgenserver.V1betaBatchListVolumesFieldsItemPoolId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemNetwork,
		gcpgenserver.V1betaBatchListVolumesFieldsItemActiveDirectoryConfigId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemActiveDirectoryResourceId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemQuotaInBytes,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSnapReserve,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSnapshotDirectory,
		gcpgenserver.V1betaBatchListVolumesFieldsItemKerberosEnabled,
		gcpgenserver.V1betaBatchListVolumesFieldsItemLdapEnabled,
		gcpgenserver.V1betaBatchListVolumesFieldsItemUnixPermissions,
		gcpgenserver.V1betaBatchListVolumesFieldsItemDescription,
		gcpgenserver.V1betaBatchListVolumesFieldsItemProtocols,
		gcpgenserver.V1betaBatchListVolumesFieldsItemZone,
		gcpgenserver.V1betaBatchListVolumesFieldsItemRegion,
	})

	out, err := convertVolumeModelToBatchVolume(log.NewLogger(), gcpgenserver.VolumeV1beta{
		ResourceId:                "load-test-vol-1",
		VolumeId:                  gcpgenserver.NewOptString("vol-common-fields"),
		Created:                   gcpgenserver.NewOptDateTime(now),
		CreationToken:             gcpgenserver.NewOptString("token-1"),
		PoolId:                    gcpgenserver.NewNilString("pool-1"),
		Network:                   gcpgenserver.NewOptString("projects/load-test-project/regions/us-east4/subnetworks/default"),
		ActiveDirectoryConfigId:   gcpgenserver.NewOptNilString("ad-1"),
		ActiveDirectoryResourceId: gcpgenserver.NewOptNilString("projects/test/locations/us-east4/activeDirectories/ad-1"),
		QuotaInBytes:              gcpgenserver.NewOptFloat64(1099511627776),
		SnapReserve:               gcpgenserver.NewOptFloat64(0),
		SnapshotDirectory:         gcpgenserver.NewOptBool(true),
		KerberosEnabled:           gcpgenserver.NewOptNilBool(false),
		LdapEnabled:               gcpgenserver.NewOptNilBool(false),
		UnixPermissions:           gcpgenserver.NewOptNilString("0770"),
		Description:               gcpgenserver.NewOptNilString("created by cursor for load testing - protocol NFSv4"),
		Protocols:                 []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
		Zone:                      gcpgenserver.NewOptString("us-east4-a"),
	}, fieldSet)
	require.NoError(t, err)

	assert.Equal(t, "load-test-vol-1", out.ResourceId.Value)
	assert.Equal(t, now, out.Created.Value)
	assert.Equal(t, "token-1", out.CreationToken.Value)
	assert.Equal(t, "pool-1", out.PoolId.Value)
	assert.Equal(t, "projects/load-test-project/regions/us-east4/subnetworks/default", out.Network.Value)
	assert.Equal(t, "ad-1", out.ActiveDirectoryConfigId.Value)
	assert.Equal(t, "projects/test/locations/us-east4/activeDirectories/ad-1", out.ActiveDirectoryResourceId.Value)
	assert.Equal(t, float64(1099511627776), out.QuotaInBytes.Value)
	assert.Equal(t, float64(0), out.SnapReserve.Value)
	assert.True(t, out.SnapshotDirectory.Value)
	assert.False(t, out.KerberosEnabled.Value)
	assert.False(t, out.LdapEnabled.Value)
	assert.Equal(t, "0770", out.UnixPermissions.Value)
	assert.Equal(t, "created by cursor for load testing - protocol NFSv4", out.Description.Value)
	assert.Equal(t, []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4}, out.Protocols.Value)
	assert.Equal(t, "us-east4-a", out.Zone.Value)
	assert.Equal(t, "us-east4", out.Region.Value)
}

func TestConvertVolumeModelToBatchVolume_PreservesSecondaryZoneFromModelConversion(t *testing.T) {
	fieldSet := buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
		gcpgenserver.V1betaBatchListVolumesFieldsItemZone,
		gcpgenserver.V1betaBatchListVolumesFieldsItemSecondaryZone,
		gcpgenserver.V1betaBatchListVolumesFieldsItemRegion,
	})

	vol := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "vol-secondary-zone"},
		DisplayName:   "vol-secondary-zone",
		Zone:          "us-east4-a",
		SecondaryZone: "us-east4-b",
	}

	out := convertVCPVolumeToBatchVolumeForTest(t, vol, fieldSet)

	assert.Equal(t, "us-east4-a", out.Zone.Value)
	assert.Equal(t, "us-east4-b", out.SecondaryZone.Value)
	assert.Equal(t, "us-east4", out.Region.Value)
}

func TestConvertVolumeModelToBatchVolume_FullFilePayload_SkipsNestedRemapFailures(t *testing.T) {
	now := time.Date(2026, 3, 30, 13, 4, 4, 0, time.UTC)
	scheduledBackupEnabled := true
	backupChainBytes := int64(12345)
	enableGlobalFileLock := true
	writebackEnabled := true
	recursion := true
	parentVolumeID := "parent-volume-id-12345678901234567890"
	parentSnapshotID := "parent-snap-id-123456789012345678901"
	allSquash := true
	anonUID := int64(2000)
	throughput := int64(128)
	inReplication := true

	volume := &models.Volume{
		BaseModel: models.BaseModel{
			UUID:      "file-volume-uuid",
			CreatedAt: now,
		},
		ServiceLevel:              "PREMIUM",
		DisplayName:               "file-volume",
		Description:               "file volume description",
		CreationToken:             "file-token",
		PoolID:                    "pool-uuid",
		PoolName:                  "pool-name",
		VendorSubnetID:            "projects/test/global/networks/default",
		ActiveDirectoryConfigId:   "ad-uuid",
		ActiveDirectoryResourceId: "projects/test/locations/us-east4/activeDirectories/ad-1",
		QuotaInBytes:              1099511627776,
		UsedBytes:                 2048,
		SnapReserve:               10,
		SnapshotDirectory:         true,
		LifeCycleState:            "READY",
		LifeCycleStateDetails:     "ready for use",
		IsDataProtection:          false,
		InReplication:             &inReplication,
		ProtocolTypes:             []string{"NFSV4", "SMB"},
		Labels:                    map[string]string{"k1": "v1"},
		KerberosEnabled:           true,
		LdapEnabled:               true,
		EncryptionType:            "CLOUD_KMS",
		Zone:                      "us-east4-a",
		SecondaryZone:             "us-east4-b",
		Region:                    "us-east4",
		LargeCapacity:             false,
		ThroughputMibps:           &throughput,
		KmsConfig: &models.KmsConfig{
			BaseModel:       models.BaseModel{UUID: "kms-uuid"},
			KeyProjectID:    "proj-1",
			KeyRingLocation: "us-east4",
			KeyRing:         "kr-1",
			KeyName:         "ck-1",
		},
		SnapshotPolicy: &models.SnapshotPolicy{
			IsEnabled: true,
		},
		DataProtection: &models.DataProtection{
			BackupVaultID:          "bv-1",
			BackupPolicyId:         "bp-1",
			ScheduledBackupEnabled: &scheduledBackupEnabled,
			BackupChainBytes:       &backupChainBytes,
		},
		AutoTieringPolicy: &models.AutoTieringPolicy{
			AutoTieringEnabled:       true,
			CoolingThresholdDays:     7,
			HotTierBypassModeEnabled: true,
		},
		HotTierSizeGib:  100,
		ColdTierSizeGib: 200,
		FileProperties: &models.FileProperties{
			JunctionPath:    "/file-volume",
			Fqdn:            "svm.example.com",
			SecurityStyle:   "UNIX",
			UnixPermissions: "0770",
			SMBShareSettings: []string{
				"SHOW_SNAPSHOT",
			},
			ExportPolicy: &models.ExportPolicy{
				ExportRules: []*models.ExportRule{
					{
						AllowedClients:      "0.0.0.0/0",
						AccessType:          "READ_WRITE",
						NFSv4:               true,
						Kerberos5ReadOnly:   true,
						Kerberos5ReadWrite:  true,
						Kerberos5iReadOnly:  true,
						Kerberos5iReadWrite: true,
						Kerberos5pReadOnly:  true,
						Kerberos5pReadWrite: true,
						Superuser:           true,
						AllSquash:           &allSquash,
						AnonUid:             &anonUID,
					},
				},
			},
		},
		IPAddresses: []string{"10.0.0.10"},
		CacheParameters: &models.CacheParameters{
			PeerClusterName:      "peer-cluster",
			PeerSvmName:          "peer-svm",
			PeerVolumeName:       "peer-volume",
			PeerIPAddresses:      []string{"10.0.0.11"},
			EnableGlobalFileLock: &enableGlobalFileLock,
			CacheState:           "PEERED",
			PreviousCacheState:   "CACHE_STATE_UNSPECIFIED",
			PeeringCommand:       "cmd",
			CacheConfig: &models.CacheConfig{
				WritebackEnabled: &writebackEnabled,
				CachePrePopulate: &models.CachePrePopulate{
					PathList:  []string{"/a"},
					Recursion: &recursion,
				},
			},
		},
		CloneSharedBytes: 4096,
		CloneParentInfo: &models.CloneParentInfo{
			ParentVolumeId:   &parentVolumeID,
			ParentSnapshotId: &parentSnapshotID,
		},
	}

	out := convertVCPVolumeToBatchVolumeForTest(t, volume, buildBatchVolumeFieldSet(allBatchVolumeFields()))
	assert.Equal(t, "file-volume-uuid", out.VolumeId)
	assert.True(t, out.ResourceId.Set)
	assert.True(t, out.BackupConfig.Set)
	assert.True(t, out.BackupConfig.Null)
	assert.True(t, out.CacheParameters.Set)
	assert.True(t, out.CacheParameters.Null)
	assert.True(t, out.CloneDetails.Set)
	assert.True(t, out.CloneDetails.Null)
}

func TestConvertVolumeModelToBatchVolume_FullBlockPayload(t *testing.T) {
	now := time.Date(2026, 3, 30, 13, 4, 4, 0, time.UTC)
	inReplication := false
	constituentCount := int32(24)

	volume := &models.Volume{
		BaseModel: models.BaseModel{
			UUID:      "block-volume-uuid",
			CreatedAt: now,
		},
		ServiceLevel:                "FLEX",
		DisplayName:                 "block-volume",
		Description:                 "block volume description",
		CreationToken:               "block-token",
		PoolID:                      "pool-block",
		PoolName:                    "pool-block-name",
		VendorSubnetID:              "projects/test/global/networks/default",
		QuotaInBytes:                5497558138880,
		UsedBytes:                   1024,
		SnapReserve:                 0,
		SnapshotDirectory:           true,
		LifeCycleState:              "READY",
		LifeCycleStateDetails:       "ready",
		IsDataProtection:            false,
		InReplication:               &inReplication,
		ProtocolTypes:               []string{"ISCSI"},
		EncryptionType:              "SERVICE_MANAGED",
		Zone:                        "us-east4-a",
		SecondaryZone:               "us-east4-b",
		Region:                      "us-east4",
		LargeCapacity:               true,
		LargeVolumeConstituentCount: &constituentCount,
		BlockProperties: &models.BlockProperties{
			OSType:          "LINUX",
			LunName:         "lun-1",
			LunSerialNumber: "serial-1",
			HostGroupDetail: []models.HostGroupDetails{{HostGroupID: "hg-1", Hosts: []string{"iqn.1"}}},
		},
		BlockDevices: &[]models.BlockDevice{
			{
				Name:       "dev-1",
				Identifier: "naa.123",
				Size:       100,
				OSType:     "LINUX",
				HostGroupDetail: []models.HostGroupDetails{
					{HostGroupID: "hg-1", Hosts: []string{"iqn.1"}},
				},
			},
		},
		IPAddresses: []string{"10.0.0.20", "10.0.0.21"},
	}

	out := convertVCPVolumeToBatchVolumeForTest(t, volume, buildBatchVolumeFieldSet(allBatchVolumeFields()))

	assert.Equal(t, "block-volume", out.ResourceId.Value)
	assert.Equal(t, "block-volume-uuid", out.VolumeId)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaServiceLevelFLEX, out.ServiceLevel.Value)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaStorageClassSOFTWARE, out.StorageClass.Value)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaSecurityStyleSECURITYSTYLEUNSPECIFIED, out.SecurityStyle.Value)
	assert.False(t, out.InReplication.Value)
	assert.True(t, out.BlockProperties.Set)
	assert.True(t, out.BlockDevices.Set)
	assert.True(t, out.MountPoints.Set)
	assert.False(t, out.MultipleEndpoints.Value)
	assert.True(t, out.LargeCapacity.Value)
	assert.Equal(t, int32(24), out.LargeVolumeConstituentCount.Value)
	assert.True(t, out.RestrictedActions.Set)
	assert.Equal(t, []gcpgenserver.BatchVolumeV1betaRestrictedActionsItem{
		gcpgenserver.BatchVolumeV1betaRestrictedActionsItemRESTRICTEDACTIONUNSPECIFIED,
	}, out.RestrictedActions.Value)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaEncryptionTypeSERVICEMANAGED, out.EncryptionType.Value)
	assert.Equal(t, "us-east4-b", out.SecondaryZone.Value)
	assert.True(t, out.DedicatedCapacity.Set)
	assert.True(t, out.DedicatedCapacity.Null)
}

func TestConvertVolumeModelToBatchVolume_RemapJSONFieldFailuresSetNull(t *testing.T) {
	tests := []struct {
		name        string
		field       gcpgenserver.V1betaBatchListVolumesFieldsItem
		apply       func(*gcpgenserver.VolumeV1beta)
		assertField func(*testing.T, gcpgenserver.BatchVolumeV1beta)
	}{
		{
			name:  "snapshotPolicy remap failure sets null",
			field: gcpgenserver.V1betaBatchListVolumesFieldsItemSnapshotPolicy,
			apply: func(v *gcpgenserver.VolumeV1beta) {
				v.SnapshotPolicy = gcpgenserver.NewOptSnapshotPolicyV1beta(gcpgenserver.SnapshotPolicyV1beta{})
			},
			assertField: func(t *testing.T, out gcpgenserver.BatchVolumeV1beta) {
				assert.True(t, out.SnapshotPolicy.Set)
				assert.True(t, out.SnapshotPolicy.Null)
			},
		},
		{
			name:  "exportPolicy remap succeeds",
			field: gcpgenserver.V1betaBatchListVolumesFieldsItemExportPolicy,
			apply: func(v *gcpgenserver.VolumeV1beta) {
				v.ExportPolicy = gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{{
						AllowedClients: "0.0.0.0/0",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					}},
				})
			},
			assertField: func(t *testing.T, out gcpgenserver.BatchVolumeV1beta) {
				assert.True(t, out.ExportPolicy.Set)
				assert.False(t, out.ExportPolicy.Null)
				assert.Len(t, out.ExportPolicy.Value.Rules, 1)
				assert.Equal(t, "0.0.0.0/0", out.ExportPolicy.Value.Rules[0].AllowedClients)
			},
		},
		{
			name:  "backupConfig remap failure sets null",
			field: gcpgenserver.V1betaBatchListVolumesFieldsItemBackupConfig,
			apply: func(v *gcpgenserver.VolumeV1beta) {
				v.BackupConfig = gcpgenserver.NewOptBackupConfigV1beta(gcpgenserver.BackupConfigV1beta{})
			},
			assertField: func(t *testing.T, out gcpgenserver.BatchVolumeV1beta) {
				assert.True(t, out.BackupConfig.Set)
				assert.True(t, out.BackupConfig.Null)
			},
		},
		{
			name:  "tieringPolicy remap failure sets null",
			field: gcpgenserver.V1betaBatchListVolumesFieldsItemTieringPolicy,
			apply: func(v *gcpgenserver.VolumeV1beta) {
				v.TieringPolicy = gcpgenserver.NewOptTieringPolicyV1beta(gcpgenserver.TieringPolicyV1beta{})
			},
			assertField: func(t *testing.T, out gcpgenserver.BatchVolumeV1beta) {
				assert.True(t, out.TieringPolicy.Set)
				assert.True(t, out.TieringPolicy.Null)
			},
		},
		{
			name:  "blockProperties remap failure sets null",
			field: gcpgenserver.V1betaBatchListVolumesFieldsItemBlockProperties,
			apply: func(v *gcpgenserver.VolumeV1beta) {
				v.BlockProperties = gcpgenserver.NewOptBlockPropertiesV1beta(gcpgenserver.BlockPropertiesV1beta{})
			},
			assertField: func(t *testing.T, out gcpgenserver.BatchVolumeV1beta) {
				assert.True(t, out.BlockProperties.Set)
				assert.True(t, out.BlockProperties.Null)
			},
		},
		{
			name:  "cacheParameters remap failure sets null",
			field: gcpgenserver.V1betaBatchListVolumesFieldsItemCacheParameters,
			apply: func(v *gcpgenserver.VolumeV1beta) {
				v.CacheParameters = gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{})
			},
			assertField: func(t *testing.T, out gcpgenserver.BatchVolumeV1beta) {
				assert.True(t, out.CacheParameters.Set)
				assert.True(t, out.CacheParameters.Null)
			},
		},
		{
			name:  "cloneDetails remap failure sets null",
			field: gcpgenserver.V1betaBatchListVolumesFieldsItemCloneDetails,
			apply: func(v *gcpgenserver.VolumeV1beta) {
				v.CloneDetails = gcpgenserver.NewOptCloneDetailsV1beta(gcpgenserver.CloneDetailsV1beta{})
			},
			assertField: func(t *testing.T, out gcpgenserver.BatchVolumeV1beta) {
				assert.True(t, out.CloneDetails.Set)
				assert.True(t, out.CloneDetails.Null)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := gcpgenserver.VolumeV1beta{
				VolumeId: gcpgenserver.NewOptString("vol-remap"),
			}
			tt.apply(&in)

			out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{tt.field}))
			require.NoError(t, err)
			assert.Equal(t, "vol-remap", out.VolumeId)
			tt.assertField(t, out)
		})
	}
}

func TestConvertVolumeModelToBatchVolume_RemapJSONFieldSuccesses(t *testing.T) {
	t.Run("snapshotPolicy converts all fields", func(t *testing.T) {
		in := gcpgenserver.VolumeV1beta{
			VolumeId: gcpgenserver.NewOptString("vol-snapshot"),
			SnapshotPolicy: gcpgenserver.NewOptSnapshotPolicyV1beta(gcpgenserver.SnapshotPolicyV1beta{
				Enabled: gcpgenserver.NewOptNilBool(true),
				HourlySchedule: gcpgenserver.NewOptHourlyScheduleV1beta(gcpgenserver.HourlyScheduleV1beta{
					SnapshotsToKeep: gcpgenserver.NewOptFloat64(4),
					Minute:          gcpgenserver.NewOptFloat64(15),
				}),
				DailySchedule: gcpgenserver.NewOptDailyScheduleV1beta(gcpgenserver.DailyScheduleV1beta{
					SnapshotsToKeep: gcpgenserver.NewOptFloat64(7),
					Hour:            gcpgenserver.NewOptFloat64(2),
					Minute:          gcpgenserver.NewOptFloat64(30),
				}),
				WeeklySchedule: gcpgenserver.NewOptWeeklyScheduleV1beta(gcpgenserver.WeeklyScheduleV1beta{
					SnapshotsToKeep: gcpgenserver.NewOptFloat64(8),
					Day:             gcpgenserver.NewOptString("Sunday"),
					Hour:            gcpgenserver.NewOptFloat64(3),
					Minute:          gcpgenserver.NewOptFloat64(45),
				}),
				MonthlySchedule: gcpgenserver.NewOptMonthlyScheduleV1beta(gcpgenserver.MonthlyScheduleV1beta{
					SnapshotsToKeep: gcpgenserver.NewOptFloat64(12),
					DaysOfMonth:     gcpgenserver.NewOptString("1,15,31"),
					Hour:            gcpgenserver.NewOptFloat64(4),
					Minute:          gcpgenserver.NewOptFloat64(50),
				}),
			}),
		}

		out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
			gcpgenserver.V1betaBatchListVolumesFieldsItemSnapshotPolicy,
		}))
		require.NoError(t, err)
		require.True(t, out.SnapshotPolicy.Set)
		require.False(t, out.SnapshotPolicy.Null)
		assert.True(t, out.SnapshotPolicy.Value.Enabled.Value)
		assert.Equal(t, float64(4), out.SnapshotPolicy.Value.HourlySchedule.Value.SnapshotsToKeep.Value)
		assert.Equal(t, float64(15), out.SnapshotPolicy.Value.HourlySchedule.Value.Minute.Value)
		assert.Equal(t, float64(7), out.SnapshotPolicy.Value.DailySchedule.Value.SnapshotsToKeep.Value)
		assert.Equal(t, float64(2), out.SnapshotPolicy.Value.DailySchedule.Value.Hour.Value)
		assert.Equal(t, float64(30), out.SnapshotPolicy.Value.DailySchedule.Value.Minute.Value)
		assert.Equal(t, float64(8), out.SnapshotPolicy.Value.WeeklySchedule.Value.SnapshotsToKeep.Value)
		assert.Equal(t, "Sunday", out.SnapshotPolicy.Value.WeeklySchedule.Value.Day.Value)
		assert.Equal(t, float64(3), out.SnapshotPolicy.Value.WeeklySchedule.Value.Hour.Value)
		assert.Equal(t, float64(45), out.SnapshotPolicy.Value.WeeklySchedule.Value.Minute.Value)
		assert.Equal(t, float64(12), out.SnapshotPolicy.Value.MonthlySchedule.Value.SnapshotsToKeep.Value)
		assert.Equal(t, "1,15,31", out.SnapshotPolicy.Value.MonthlySchedule.Value.DaysOfMonth.Value)
		assert.Equal(t, float64(4), out.SnapshotPolicy.Value.MonthlySchedule.Value.Hour.Value)
		assert.Equal(t, float64(50), out.SnapshotPolicy.Value.MonthlySchedule.Value.Minute.Value)
	})

	t.Run("exportPolicy converts all rule fields", func(t *testing.T) {
		in := gcpgenserver.VolumeV1beta{
			VolumeId: gcpgenserver.NewOptString("vol-export"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{{
					AllowedClients:      "0.0.0.0/0",
					HasRootAccess:       gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue),
					AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					Nfsv3:               gcpgenserver.NewOptNilBool(true),
					Nfsv4:               gcpgenserver.NewOptNilBool(true),
					Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(true),
					Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(true),
					Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(true),
					Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(true),
					Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(true),
					Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(true),
					AllSquash:           gcpgenserver.NewOptNilBool(true),
					AnonUid:             gcpgenserver.NewOptNilInt64(2000),
				}},
			}),
		}

		out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
			gcpgenserver.V1betaBatchListVolumesFieldsItemExportPolicy,
		}))
		require.NoError(t, err)
		require.True(t, out.ExportPolicy.Set)
		require.False(t, out.ExportPolicy.Null)
		require.Len(t, out.ExportPolicy.Value.Rules, 1)
		rule := out.ExportPolicy.Value.Rules[0]
		assert.Equal(t, "0.0.0.0/0", rule.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue, rule.HasRootAccess.Value)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE, rule.AccessType)
		assert.True(t, rule.Nfsv3.Value)
		assert.True(t, rule.Nfsv4.Value)
		assert.True(t, rule.Kerberos5ReadOnly.Value)
		assert.True(t, rule.Kerberos5ReadWrite.Value)
		assert.True(t, rule.Kerberos5iReadOnly.Value)
		assert.True(t, rule.Kerberos5iReadWrite.Value)
		assert.True(t, rule.Kerberos5pReadOnly.Value)
		assert.True(t, rule.Kerberos5pReadWrite.Value)
		assert.True(t, rule.AllSquash.Value)
		assert.Equal(t, int64(2000), rule.AnonUid.Value)
	})

	t.Run("backupConfig converts all fields", func(t *testing.T) {
		in := gcpgenserver.VolumeV1beta{
			VolumeId: gcpgenserver.NewOptString("vol-backup"),
			BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(gcpgenserver.BackupConfigV1beta{
				BackupPolicyId:         gcpgenserver.NewOptNilString("bp-1"),
				BackupVaultId:          gcpgenserver.NewOptNilString("bv-1"),
				ScheduledBackupEnabled: gcpgenserver.NewOptNilBool(true),
				BackupChainBytes:       gcpgenserver.NewOptNilInt64(12345),
				KmsGrant:               gcpgenserver.NewOptNilString("kms-grant"),
			}),
		}

		out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
			gcpgenserver.V1betaBatchListVolumesFieldsItemBackupConfig,
		}))
		require.NoError(t, err)
		require.True(t, out.BackupConfig.Set)
		require.False(t, out.BackupConfig.Null)
		assert.Equal(t, "bp-1", out.BackupConfig.Value.BackupPolicyId.Value)
		assert.Equal(t, "bv-1", out.BackupConfig.Value.BackupVaultId.Value)
		assert.True(t, out.BackupConfig.Value.ScheduledBackupEnabled.Value)
		assert.Equal(t, int64(12345), out.BackupConfig.Value.BackupChainBytes.Value)
		assert.Equal(t, "kms-grant", out.BackupConfig.Value.KmsGrant.Value)
	})

	t.Run("tieringPolicy converts all fields", func(t *testing.T) {
		in := gcpgenserver.VolumeV1beta{
			VolumeId: gcpgenserver.NewOptString("vol-tiering"),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(gcpgenserver.TieringPolicyV1beta{
				TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction(gcpgenserver.TieringPolicyV1betaTierActionENABLED),
				CoolingThresholdDays:     gcpgenserver.NewOptNilInt32(30),
				HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
			}),
		}

		out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
			gcpgenserver.V1betaBatchListVolumesFieldsItemTieringPolicy,
		}))
		require.NoError(t, err)
		require.True(t, out.TieringPolicy.Set)
		require.False(t, out.TieringPolicy.Null)
		assert.Equal(t, gcpgenserver.BatchVolumeV1betaTieringPolicyTierActionENABLED, out.TieringPolicy.Value.TierAction.Value)
		assert.Equal(t, int32(30), out.TieringPolicy.Value.CoolingThresholdDays.Value)
		assert.True(t, out.TieringPolicy.Value.HotTierBypassModeEnabled.Value)
	})

	t.Run("blockProperties converts all fields", func(t *testing.T) {
		in := gcpgenserver.VolumeV1beta{
			VolumeId: gcpgenserver.NewOptString("vol-block-props"),
			BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(gcpgenserver.BlockPropertiesV1beta{
				OsType:           gcpgenserver.NewOptBlockPropertiesV1betaOsType(gcpgenserver.BlockPropertiesV1betaOsTypeLINUX),
				HostGroupIds:     []string{"hg-1", "hg-2"},
				HostGroupDetails: []gcpgenserver.HostGroupDetail{{HostGroupId: gcpgenserver.NewOptString("hg-1"), Hosts: []string{"iqn.1"}}},
				LunSerialNumber:  gcpgenserver.NewOptString("serial-1"),
			}),
		}

		out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
			gcpgenserver.V1betaBatchListVolumesFieldsItemBlockProperties,
		}))
		require.NoError(t, err)
		require.True(t, out.BlockProperties.Set)
		require.False(t, out.BlockProperties.Null)
		assert.Equal(t, gcpgenserver.BatchVolumeV1betaBlockPropertiesOsTypeLINUX, out.BlockProperties.Value.OsType.Value)
		assert.Equal(t, []string{"hg-1", "hg-2"}, out.BlockProperties.Value.HostGroupIds)
		require.Len(t, out.BlockProperties.Value.HostGroupDetails, 1)
		assert.Equal(t, "hg-1", out.BlockProperties.Value.HostGroupDetails[0].HostGroupId.Value)
		assert.Equal(t, []string{"iqn.1"}, out.BlockProperties.Value.HostGroupDetails[0].Hosts)
		assert.Equal(t, "serial-1", out.BlockProperties.Value.LunSerialNumber.Value)
	})

	t.Run("cacheParameters converts all fields", func(t *testing.T) {
		now := time.Date(2026, 4, 1, 10, 30, 0, 0, time.UTC)
		in := gcpgenserver.VolumeV1beta{
			VolumeId: gcpgenserver.NewOptString("vol-cache"),
			CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
				PeerVolumeName:       gcpgenserver.NewOptString("peer-volume"),
				PeerClusterName:      gcpgenserver.NewOptString("peer-cluster"),
				PeerSvmName:          gcpgenserver.NewOptString("peer-svm"),
				PeerIpAddresses:      []string{"10.0.0.1", "10.0.0.2"},
				EnableGlobalFileLock: gcpgenserver.NewOptNilBool(true),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					CachePrePopulate: gcpgenserver.NewOptFlexCachePrePopulateV1beta(gcpgenserver.FlexCachePrePopulateV1beta{
						PathList:        gcpgenserver.NewOptNilStringArray([]string{"/a", "/b"}),
						ExcludePathList: gcpgenserver.NewOptNilStringArray([]string{"/tmp"}),
						Recursion:       gcpgenserver.NewOptNilBool(true),
					}),
					WritebackEnabled:        gcpgenserver.NewOptNilBool(true),
					AtimeScrubEnabled:       gcpgenserver.NewOptNilBool(true),
					AtimeScrubDays:          gcpgenserver.NewOptNilInt16(12),
					CifsChangeNotifyEnabled: gcpgenserver.NewOptNilBool(true),
					CachePrePopulateState:   gcpgenserver.NewOptFlexCacheConfigV1betaCachePrePopulateState(gcpgenserver.FlexCacheConfigV1betaCachePrePopulateStateCOMPLETE),
				}),
				CacheState:               gcpgenserver.NewOptFlexCacheV1betaCacheState(gcpgenserver.FlexCacheV1betaCacheStatePEERED),
				StateDetails:             gcpgenserver.NewOptString("stable"),
				StateDetailsCode:         gcpgenserver.NewOptInt32(42),
				PreviousCacheState:       gcpgenserver.NewOptFlexCacheV1betaPreviousCacheState(gcpgenserver.FlexCacheV1betaPreviousCacheStatePENDINGCLUSTERPEERING),
				Command:                  gcpgenserver.NewOptString("cluster peer create"),
				PeeringCommandExpiryTime: gcpgenserver.NewOptNilDateTime(now),
				Passphrase:               gcpgenserver.NewOptNilString("secret"),
			}),
		}

		out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
			gcpgenserver.V1betaBatchListVolumesFieldsItemCacheParameters,
		}))
		require.NoError(t, err)
		require.True(t, out.CacheParameters.Set)
		require.False(t, out.CacheParameters.Null)
		assert.Equal(t, "peer-volume", out.CacheParameters.Value.PeerVolumeName.Value)
		assert.Equal(t, "peer-cluster", out.CacheParameters.Value.PeerClusterName.Value)
		assert.Equal(t, "peer-svm", out.CacheParameters.Value.PeerSvmName.Value)
		assert.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, out.CacheParameters.Value.PeerIpAddresses)
		assert.True(t, out.CacheParameters.Value.EnableGlobalFileLock.Value)
		assert.Equal(t, []string{"/a", "/b"}, out.CacheParameters.Value.CacheConfig.Value.CachePrePopulate.Value.PathList.Value)
		assert.Equal(t, []string{"/tmp"}, out.CacheParameters.Value.CacheConfig.Value.CachePrePopulate.Value.ExcludePathList.Value)
		assert.True(t, out.CacheParameters.Value.CacheConfig.Value.CachePrePopulate.Value.Recursion.Value)
		assert.True(t, out.CacheParameters.Value.CacheConfig.Value.WritebackEnabled.Value)
		assert.True(t, out.CacheParameters.Value.CacheConfig.Value.AtimeScrubEnabled.Value)
		assert.Equal(t, int16(12), out.CacheParameters.Value.CacheConfig.Value.AtimeScrubDays.Value)
		assert.True(t, out.CacheParameters.Value.CacheConfig.Value.CifsChangeNotifyEnabled.Value)
		assert.Equal(t, gcpgenserver.FlexCacheConfigV1betaCachePrePopulateStateCOMPLETE, out.CacheParameters.Value.CacheConfig.Value.CachePrePopulateState.Value)
		assert.Equal(t, gcpgenserver.BatchVolumeV1betaCacheParametersCacheStatePEERED, out.CacheParameters.Value.CacheState.Value)
		assert.Equal(t, "stable", out.CacheParameters.Value.StateDetails.Value)
		assert.Equal(t, int32(42), out.CacheParameters.Value.StateDetailsCode.Value)
		assert.Equal(t, gcpgenserver.BatchVolumeV1betaCacheParametersPreviousCacheStatePENDINGCLUSTERPEERING, out.CacheParameters.Value.PreviousCacheState.Value)
		assert.Equal(t, "cluster peer create", out.CacheParameters.Value.Command.Value)
		assert.Equal(t, now, out.CacheParameters.Value.PeeringCommandExpiryTime.Value)
		assert.Equal(t, "secret", out.CacheParameters.Value.Passphrase.Value)
	})

	t.Run("cloneDetails converts all fields", func(t *testing.T) {
		in := gcpgenserver.VolumeV1beta{
			VolumeId: gcpgenserver.NewOptString("vol-clone"),
			CloneDetails: gcpgenserver.NewOptCloneDetailsV1beta(gcpgenserver.CloneDetailsV1beta{
				ParentVolumeId:       gcpgenserver.NewOptString("parent-vol"),
				ParentSnapshotId:     gcpgenserver.NewOptString("parent-snap"),
				SharedBytes:          gcpgenserver.NewOptNilFloat64(4096),
				State:                gcpgenserver.NewOptNilCloneDetailsV1betaState(gcpgenserver.CloneDetailsV1betaStateSPLITSTATENOTSPLITTING),
				StateDetails:         gcpgenserver.NewOptNilString("clone complete"),
				SplitCompletePercent: gcpgenserver.NewOptNilInt64(100),
			}),
		}

		out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
			gcpgenserver.V1betaBatchListVolumesFieldsItemCloneDetails,
		}))
		require.NoError(t, err)
		require.True(t, out.CloneDetails.Set)
		require.False(t, out.CloneDetails.Null)
		assert.Equal(t, "parent-vol", out.CloneDetails.Value.ParentVolumeId.Value)
		assert.Equal(t, "parent-snap", out.CloneDetails.Value.ParentSnapshotId.Value)
		assert.Equal(t, float64(4096), out.CloneDetails.Value.SharedBytes.Value)
		assert.Equal(t, gcpgenserver.BatchVolumeV1betaCloneDetailsStateSPLITSTATENOTSPLITTING, out.CloneDetails.Value.State.Value)
		assert.Equal(t, "clone complete", out.CloneDetails.Value.StateDetails.Value)
		assert.Equal(t, int64(100), out.CloneDetails.Value.SplitCompletePercent.Value)
	})
}

func TestConvertCVPBatchVolumeToGCPBatchVolume_RemapJSONSuccess(t *testing.T) {
	fieldSet := buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
		gcpgenserver.V1betaBatchListVolumesFieldsItemResourceId,
		gcpgenserver.V1betaBatchListVolumesFieldsItemServiceLevel,
	})

	resourceID := "cvp-vol"
	serviceLevel := "PREMIUM"
	in := &cvpmodels.BatchVolumeV1beta{
		VolumeID:     "cvp-vol-1",
		ResourceID:   &resourceID,
		ServiceLevel: &serviceLevel,
	}

	out, err := convertCVPBatchVolumeToGCPBatchVolume(log.NewLogger(), in, fieldSet)
	require.NoError(t, err)
	assert.Equal(t, "cvp-vol-1", out.VolumeId)
	assert.True(t, out.ResourceId.Set)
	assert.Equal(t, "cvp-vol", out.ResourceId.Value)
	assert.True(t, out.ServiceLevel.Set)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaServiceLevelPREMIUM, out.ServiceLevel.Value)
}

func TestV1betaBatchListVolumes_HandlerCoverage(t *testing.T) {
	t.Run("NilHTTPRequest_ReturnsUnauthorized", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()

		handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		res, err := handler.V1betaBatchListVolumes(context.Background(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesUnauthorized)
		assert.True(tt, ok)
	})

	t.Run("InvalidLocation_ReturnsBadRequest", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()

		handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "invalid location!"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesBadRequest)
		assert.True(tt, ok)
	})

	t.Run("EmptyUuids_ReturnsBadRequest", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()

		handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListVolumesBadRequest)
		require.True(tt, ok)
		assert.Contains(tt, badReq.Message, "volumeUuids is required")
	})

	t.Run("TooManyUuids_ReturnsBadRequest", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()
		origMax := maxBatchVolumeUUIDs
		maxBatchVolumeUUIDs = 1
		defer func() { maxBatchVolumeUUIDs = origMax }()

		handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1", "uuid-2"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesBadRequest)
		assert.True(tt, ok)
	})

	t.Run("VCPOnlySuccess", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()
		cvp.SetCVPHost("")

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.EXPECT().GetVolumesByUUIDs(mock.Anything, []string{"uuid-1"}, mock.Anything).Return([]*models.Volume{makeVCPVolume("uuid-1", "vol-1")}, nil).Once()
		handler := &Handler{Orchestrator: mockOrch}

		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListVolumesOK)
		require.True(tt, ok)
		assert.Len(tt, okRes.Volumes, 1)
	})

	t.Run("VCPOnlyError", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()
		cvp.SetCVPHost("")

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.EXPECT().GetVolumesByUUIDs(mock.Anything, []string{"uuid-1"}, mock.Anything).Return(nil, errors.New("db error")).Once()
		handler := &Handler{Orchestrator: mockOrch}

		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("ParallelBothFail", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()
		cvp.SetCVPHost("http://cvp-host")
		origFetch := fetchBatchVolumesFromCVPFn
		defer func() { fetchBatchVolumesFromCVPFn = origFetch }()
		fetchBatchVolumesFromCVPFn = func(ctx context.Context, volumeUUIDs []string, params gcpgenserver.V1betaBatchListVolumesParams, fieldSet map[string]bool) ([]gcpgenserver.BatchVolumeV1beta, error) {
			return nil, errors.New("cvp error")
		}

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.EXPECT().GetVolumesByUUIDs(mock.Anything, []string{"uuid-1"}, mock.Anything).Return(nil, errors.New("vcp error")).Once()
		handler := &Handler{Orchestrator: mockOrch}

		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListVolumesInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("ParallelPartialSuccess", func(tt *testing.T) {
		restoreParse := stubBatchVolumeParseRegionAndZone()
		defer restoreParse()
		cvp.SetCVPHost("http://cvp-host")
		origFetch := fetchBatchVolumesFromCVPFn
		defer func() { fetchBatchVolumesFromCVPFn = origFetch }()
		fetchBatchVolumesFromCVPFn = func(ctx context.Context, volumeUUIDs []string, params gcpgenserver.V1betaBatchListVolumesParams, fieldSet map[string]bool) ([]gcpgenserver.BatchVolumeV1beta, error) {
			return []gcpgenserver.BatchVolumeV1beta{{VolumeId: "cvp-uuid"}}, nil
		}

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		mockOrch.EXPECT().GetVolumesByUUIDs(mock.Anything, []string{"uuid-1"}, mock.Anything).Return([]*models.Volume{makeVCPVolume("uuid-1", "vol-1")}, nil).Once()
		handler := &Handler{Orchestrator: mockOrch}

		res, err := handler.V1betaBatchListVolumes(batchVolumeRequestContext(), &gcpgenserver.BatchVolumeUUIDListV1beta{VolumeUuids: []string{"uuid-1"}}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"})
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListVolumesOK)
		require.True(tt, ok)
		assert.Len(tt, okRes.Volumes, 2)
	})
}

func TestFetchBatchVolumesFromCVP_Coverage(t *testing.T) {
	t.Run("SuccessWithPayload", func(tt *testing.T) {
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{Batch: mockBatch}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockClient }

		resourceID := "cvp-vol"
		mockBatch.EXPECT().V1betaBatchListVolumes(mock.Anything).Return(&cvpBatch.V1betaBatchListVolumesOK{
			Payload: &cvpBatch.V1betaBatchListVolumesOKBody{
				Volumes: []*cvpmodels.BatchVolumeV1beta{{VolumeID: "uuid-1", ResourceID: &resourceID}},
			},
		}, nil)

		res, err := fetchBatchVolumesFromCVP(batchVolumeRequestContext(), []string{"uuid-1"}, gcpgenserver.V1betaBatchListVolumesParams{
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("corr-1"),
			Fields:         []gcpgenserver.V1betaBatchListVolumesFieldsItem{gcpgenserver.V1betaBatchListVolumesFieldsItemResourceId},
		}, map[string]bool{"resourceId": true})
		require.NoError(tt, err)
		require.Len(tt, res, 1)
		assert.Equal(tt, "cvp-vol", res[0].ResourceId.Value)
	})

	t.Run("ClientError", func(tt *testing.T) {
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{Batch: mockBatch}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockClient }

		mockBatch.EXPECT().V1betaBatchListVolumes(mock.Anything).Return(nil, errors.New("cvp failed"))

		res, err := fetchBatchVolumesFromCVP(batchVolumeRequestContext(), []string{"uuid-1"}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"}, nil)
		assert.Error(tt, err)
		assert.Nil(tt, res)
	})

	t.Run("NilPayloadReturnsEmpty", func(tt *testing.T) {
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{Batch: mockBatch}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *mockClient }

		mockBatch.EXPECT().V1betaBatchListVolumes(mock.Anything).Return(&cvpBatch.V1betaBatchListVolumesOK{
			Payload: &cvpBatch.V1betaBatchListVolumesOKBody{},
		}, nil)

		res, err := fetchBatchVolumesFromCVP(batchVolumeRequestContext(), []string{"uuid-1"}, gcpgenserver.V1betaBatchListVolumesParams{LocationId: "us-east4"}, nil)
		require.NoError(tt, err)
		assert.Empty(tt, res)
	})
}

func TestBatchVolumeHelperCoverage(t *testing.T) {
	t.Run("ApplySelectionWithNilFieldSetResetsOptionals", func(tt *testing.T) {
		bv := gcpgenserver.BatchVolumeV1beta{
			VolumeId:    "vol-1",
			ResourceId:  gcpgenserver.NewOptNilString("res"),
			Description: gcpgenserver.NewOptNilString("desc"),
			Zone:        gcpgenserver.NewOptNilString("us-east4-a"),
		}
		applyBatchVolumeFieldSelection(&bv, nil)
		assert.Equal(tt, "vol-1", bv.VolumeId)
		assert.False(tt, bv.ResourceId.Set)
		assert.False(tt, bv.Description.Set)
		assert.False(tt, bv.Zone.Set)
	})

	t.Run("ApplySelectionFillsMissingRequestedValues", func(tt *testing.T) {
		bv := &gcpgenserver.BatchVolumeV1beta{VolumeId: "vol-1"}
		applyBatchVolumeFieldSelection(bv, buildBatchVolumeFieldSet(allBatchVolumeFields()))
		assert.True(tt, bv.ResourceId.Null)
		assert.Equal(tt, gcpgenserver.BatchVolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED, bv.ServiceLevel.Value)
		assert.Equal(tt, gcpgenserver.BatchVolumeV1betaSecurityStyleSECURITYSTYLEUNSPECIFIED, bv.SecurityStyle.Value)
		assert.Equal(tt, gcpgenserver.BatchVolumeV1betaVolumeStateSTATEUNSPECIFIED, bv.VolumeState.Value)
		assert.Equal(tt, gcpgenserver.BatchVolumeV1betaStorageClassSTORAGECLASSUNSPECIFIED, bv.StorageClass.Value)
		assert.Equal(tt, []gcpgenserver.BatchVolumeV1betaRestrictedActionsItem{gcpgenserver.BatchVolumeV1betaRestrictedActionsItemRESTRICTEDACTIONUNSPECIFIED}, bv.RestrictedActions.Value)
		assert.True(tt, bv.DedicatedCapacity.Null)
		assert.True(tt, bv.Region.Null)
	})

	t.Run("GetHTTPRequestFromContextInvalidCases", func(tt *testing.T) {
		assert.Nil(tt, getHTTPRequestFromContext(context.Background()))
		assert.Nil(tt, getHTTPRequestFromContext(context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, "not-header")))
	})
}
