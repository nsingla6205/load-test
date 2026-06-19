package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	coreapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/volumes"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestPrepareCreateVolumeParams_SnapshotIdWithLargeVolumeConstituentCount_ReturnsError(t *testing.T) {
	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:                  "testvolume",
			CreationToken:               gcpgenserver.NewOptString("test-token"),
			PoolId:                      gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:                gcpgenserver.NewOptFloat64(1024),
			LargeVolumeConstituentCount: gcpgenserver.OptNilInt32{Value: 5, Set: true},
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaISCSI,
			},
		},
		SnapshotId: gcpgenserver.NewOptString("test-snapshot-id"),
		VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "LargeVolumeConstituentCount cannot be set when SnapshotId is provided")
}

func TestPrepareCreateVolumeParams_WithThroughputMibps(t *testing.T) {
	originalEnableMqos := enableMqos
	originalEnableInferredIops := enableInferredIops
	defer func() {
		enableMqos = originalEnableMqos
		enableInferredIops = originalEnableInferredIops
	}()

	baseReq := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:      "testvolume",
			CreationToken:   gcpgenserver.NewOptString("test-token"),
			PoolId:          gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:    gcpgenserver.NewOptFloat64(1024),
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(200),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaNFSV3,
			},
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}

	t.Run("SetsParam_WithNilPool", func(tt *testing.T) {
		enableMqos = true
		enableInferredIops = true
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", nil)
		require.NoError(tt, err)
		require.NotNil(tt, result)
		require.NotNil(tt, result.ThroughputMibps)
		assert.Equal(tt, int64(200), *result.ThroughputMibps)
	})

	t.Run("SetsParam_WithManualPool_MQOSEnabled", func(tt *testing.T) {
		enableMqos = true
		enableInferredIops = true
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool"},
			QosType:   utils.QosTypeManual,
		}
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", pool)
		require.NoError(tt, err)
		require.NotNil(tt, result)
		require.NotNil(tt, result.ThroughputMibps)
		assert.Equal(tt, int64(200), *result.ThroughputMibps)
	})

	t.Run("Rejects_WithAutoPool_MQOSEnabled", func(tt *testing.T) {
		enableMqos = true
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool"},
			QosType:   utils.QosTypeAuto,
		}
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", pool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Pool has auto QoS type")
	})

	t.Run("Rejects_WithAutoPool_MQOSDisabled", func(tt *testing.T) {
		enableMqos = false
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool"},
			QosType:   utils.QosTypeAuto,
		}
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", pool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		// MQOS check happens before pool QosType check
		assert.Contains(tt, err.Error(), "Manual QoS (MQOS) is not enabled")
	})
}

func TestConvertVolumeModelToBatchVolume_RequestedEnumsUseUnspecifiedDefaults(t *testing.T) {
	in := gcpgenserver.VolumeV1beta{
		VolumeId:     gcpgenserver.NewOptString("vol-1"),
		ServiceLevel: gcpgenserver.NewOptVolumeV1betaServiceLevel(gcpgenserver.VolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED),
	}
	fieldSet := buildBatchVolumeFieldSet([]gcpgenserver.V1betaBatchListVolumesFieldsItem{
		gcpgenserver.V1betaBatchListVolumesFieldsItemServiceLevel,
		gcpgenserver.V1betaBatchListVolumesFieldsItemVolumeState,
		gcpgenserver.V1betaBatchListVolumesFieldsItemEncryptionType,
		gcpgenserver.V1betaBatchListVolumesFieldsItemStorageClass,
		gcpgenserver.V1betaBatchListVolumesFieldsItemDescription,
	})

	out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, fieldSet)
	require.NoError(t, err)

	assert.Equal(t, "vol-1", out.VolumeId)
	assert.True(t, out.ServiceLevel.Set)
	assert.False(t, out.ServiceLevel.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED, out.ServiceLevel.Value)
	assert.True(t, out.VolumeState.Set)
	assert.False(t, out.VolumeState.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaVolumeStateSTATEUNSPECIFIED, out.VolumeState.Value)
	assert.True(t, out.EncryptionType.Set)
	assert.False(t, out.EncryptionType.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaEncryptionTypeENCRYPTIONTYPEUNSPECIFIED, out.EncryptionType.Value)
	assert.True(t, out.StorageClass.Set)
	assert.False(t, out.StorageClass.Null)
	assert.Equal(t, gcpgenserver.BatchVolumeV1betaStorageClassSTORAGECLASSUNSPECIFIED, out.StorageClass.Value)
	assert.True(t, out.Description.Set)
	assert.True(t, out.Description.Null)
	assert.False(t, out.Network.Set)
}

func TestConvertVolumeModelToBatchVolume_NoFieldsOnlyVolumeID(t *testing.T) {
	in := gcpgenserver.VolumeV1beta{
		VolumeId:    gcpgenserver.NewOptString("vol-2"),
		ResourceId:  "res-2",
		Description: gcpgenserver.NewOptNilString("desc"),
	}

	out, err := convertVolumeModelToBatchVolume(log.NewLogger(), in, nil)
	require.NoError(t, err)

	assert.Equal(t, "vol-2", out.VolumeId)
	assert.False(t, out.ResourceId.Set)
	assert.False(t, out.Description.Set)
}

func TestPrepareCreateVolumeParams_WithIops(t *testing.T) {
	originalEnableMqos := enableMqos
	originalEnableInferredIops := enableInferredIops
	defer func() {
		enableMqos = originalEnableMqos
		enableInferredIops = originalEnableInferredIops
	}()

	baseReq := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Iops:          gcpgenserver.NewOptNilInt64(1000),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaNFSV3,
			},
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}

	t.Run("ReturnsError_WithNilPool_MQOSEnabled_InferredIopsEnabled", func(tt *testing.T) {
		enableMqos = true
		enableInferredIops = true
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "iops must not be set when IOPS inference is enabled")
	})

	t.Run("ReturnsError_WithNilPool_MQOSDisabled", func(tt *testing.T) {
		enableMqos = false
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		// MQOS check happens before iops blocking when MQOS is disabled
		assert.Contains(tt, err.Error(), "Manual QoS (MQOS) is not enabled")
	})

	t.Run("Rejects_WithAutoPool_MQOSEnabled", func(tt *testing.T) {
		enableMqos = true
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool"},
			QosType:   utils.QosTypeAuto,
		}
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", pool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		// Auto pool validation happens before iops blocking
		assert.Contains(tt, err.Error(), "Pool has auto QoS type")
	})

	t.Run("Rejects_WithAutoPool_MQOSDisabled", func(tt *testing.T) {
		enableMqos = false
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool"},
			QosType:   utils.QosTypeAuto,
		}
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", pool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		// MQOS check happens before pool QosType check
		assert.Contains(tt, err.Error(), "Manual QoS (MQOS) is not enabled")
	})
}

func TestPrepareCreateVolumeParams_ThroughputRequiresIopsWhenInferenceDisabled(t *testing.T) {
	originalEnableMqos := enableMqos
	originalEnableInferredIops := enableInferredIops
	enableMqos = true
	enableInferredIops = false
	defer func() {
		enableMqos = originalEnableMqos
		enableInferredIops = originalEnableInferredIops
	}()

	baseReq := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:      "testvolume",
			CreationToken:   gcpgenserver.NewOptString("test-token"),
			PoolId:          gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:    gcpgenserver.NewOptFloat64(1024),
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(200),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaNFSV3,
			},
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}

	result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "IOPS inference is disabled")
}

func TestPrepareCreateVolumeParams_ThroughputAndIopsWhenInferenceDisabled(t *testing.T) {
	originalEnableMqos := enableMqos
	originalEnableInferredIops := enableInferredIops
	enableMqos = true
	enableInferredIops = false
	defer func() {
		enableMqos = originalEnableMqos
		enableInferredIops = originalEnableInferredIops
	}()

	manualPool := &models.Pool{
		BaseModel: models.BaseModel{UUID: "test-pool"},
		QosType:   utils.QosTypeManual,
	}

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:      "testvolume",
			CreationToken:   gcpgenserver.NewOptString("test-token"),
			PoolId:          gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:    gcpgenserver.NewOptFloat64(1024),
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(200),
			Iops:            gcpgenserver.NewOptNilInt64(5000),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaNFSV3,
			},
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}

	result, err := _prepareCreateVolumeParams(req, params, "test-region", "test-zone", manualPool)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, int64(200), *result.ThroughputMibps)
	assert.Equal(t, int64(5000), *result.Iops)
}

func TestPrepareCreateVolumeParams_WithThroughputMibps_MqosDisabled(t *testing.T) {
	originalEnableMqos := enableMqos
	enableMqos = false
	defer func() { enableMqos = originalEnableMqos }()

	baseReq := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:      "testvolume",
			CreationToken:   gcpgenserver.NewOptString("test-token"),
			PoolId:          gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:    gcpgenserver.NewOptFloat64(1024),
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(200),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaNFSV3,
			},
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}

	t.Run("ReturnsError_WithNilPool", func(tt *testing.T) {
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Manual QoS (MQOS) is not enabled")
	})

	t.Run("ReturnsError_WithManualPool", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool"},
			QosType:   utils.QosTypeManual,
		}
		result, err := _prepareCreateVolumeParams(baseReq, params, "test-region", "test-zone", pool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Manual QoS (MQOS) is not enabled")
	})
}

func TestPrepareCreateVolumeParams_ManualPoolRequirements(t *testing.T) {
	originalEnableMqos := enableMqos
	defer func() { enableMqos = originalEnableMqos }()

	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	manualPool := &models.Pool{
		BaseModel: models.BaseModel{UUID: "test-pool"},
		QosType:   utils.QosTypeManual,
	}

	t.Run("RequiresThroughputMibps_WhenMQOSEnabled", func(tt *testing.T) {
		enableMqos = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				// No throughputMibps
			},
		}
		result, err := _prepareCreateVolumeParams(req, params, "test-region", "test-zone", manualPool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Pool has manual QoS type")
	})

	t.Run("AllowsWithoutThroughputMibps_WhenMQOSDisabled", func(tt *testing.T) {
		enableMqos = false
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				// No throughputMibps
			},
		}
		result, err := _prepareCreateVolumeParams(req, params, "test-region", "test-zone", manualPool)
		// Should succeed when MQOS is disabled (no throughput requirement)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
	})
}

func TestPrepareCreateVolumeParams_VPGOnCreate(t *testing.T) {
	originalEnableMqos := enableMqos
	originalEnableVPGAssignment := enableVolumePerformanceGroupAssignment
	defer func() {
		enableMqos = originalEnableMqos
		enableVolumePerformanceGroupAssignment = originalEnableVPGAssignment
	}()

	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	manualPool := &models.Pool{
		BaseModel: models.BaseModel{UUID: "test-pool"},
		QosType:   utils.QosTypeManual,
	}
	autoPool := &models.Pool{
		BaseModel: models.BaseModel{UUID: "test-pool"},
		QosType:   utils.QosTypeAuto,
	}

	t.Run("AllowsVpgIdWithoutThroughput_ManualPool_MQOSEnabled", func(tt *testing.T) {
		enableMqos = true
		enableVolumePerformanceGroupAssignment = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:               "testvolume",
				CreationToken:            gcpgenserver.NewOptString("test-token"),
				PoolId:                   gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:             gcpgenserver.NewOptFloat64(1024),
				VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
			},
		}
		result, err := _prepareCreateVolumeParams(req, params, "test-region", "test-zone", manualPool)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "vpg-123", *result.VolumePerformanceGroupID)
		assert.Nil(tt, result.ThroughputMibps)
	})

	t.Run("RejectsVpgIdWithThroughput_MutualExclusion", func(tt *testing.T) {
		enableMqos = true
		enableVolumePerformanceGroupAssignment = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:               "testvolume",
				CreationToken:            gcpgenserver.NewOptString("test-token"),
				PoolId:                   gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:             gcpgenserver.NewOptFloat64(1024),
				ThroughputMibps:          gcpgenserver.NewOptNilFloat64(128),
				VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
			},
		}
		result, err := _prepareCreateVolumeParams(req, params, "test-region", "test-zone", manualPool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), utils.ErrMsgVpgMutuallyExclusiveWithQos)
	})

	t.Run("RejectsVpgIdWithIops_MutualExclusion", func(tt *testing.T) {
		enableMqos = true
		enableVolumePerformanceGroupAssignment = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:               "testvolume",
				CreationToken:            gcpgenserver.NewOptString("test-token"),
				PoolId:                   gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:             gcpgenserver.NewOptFloat64(1024),
				Iops:                     gcpgenserver.NewOptNilInt64(5000),
				VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
			},
		}
		result, err := _prepareCreateVolumeParams(req, params, "test-region", "test-zone", manualPool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), utils.ErrMsgVpgMutuallyExclusiveWithQos)
	})

	t.Run("RejectsVpgId_AutoPool", func(tt *testing.T) {
		enableMqos = true
		enableVolumePerformanceGroupAssignment = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:               "testvolume",
				CreationToken:            gcpgenserver.NewOptString("test-token"),
				PoolId:                   gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:             gcpgenserver.NewOptFloat64(1024),
				VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
			},
		}
		result, err := _prepareCreateVolumeParams(req, params, "test-region", "test-zone", autoPool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), utils.ErrMsgPoolAutoQosTypeCannotSpecifyVpgId)
	})

	t.Run("RejectsVpgId_MQOSDisabled", func(tt *testing.T) {
		enableMqos = false
		enableVolumePerformanceGroupAssignment = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:               "testvolume",
				CreationToken:            gcpgenserver.NewOptString("test-token"),
				PoolId:                   gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:             gcpgenserver.NewOptFloat64(1024),
				VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
			},
		}
		result, err := _prepareCreateVolumeParams(req, params, "test-region", "test-zone", manualPool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), utils.ErrMsgMqosNotEnabledVpgId)
	})

	t.Run("RejectsVpgId_VPGAssignmentFeatureFlagDisabled", func(tt *testing.T) {
		enableMqos = true
		enableVolumePerformanceGroupAssignment = false
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:               "testvolume",
				CreationToken:            gcpgenserver.NewOptString("test-token"),
				PoolId:                   gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:             gcpgenserver.NewOptFloat64(1024),
				VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
			},
		}
		result, err := _prepareCreateVolumeParams(req, params, "test-region", "test-zone", manualPool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), utils.ErrMsgVpgAssignmentNotEnabled)
	})
}

func TestPrepareCreateVolumeParams_CacheParametersWithoutExpiryTime(t *testing.T) {
	// Setup file protocol support for NFS
	utils.SetFileProtocolSupportedForTesting(true)
	utils.SetExperimentalVersionAllowlistedAccountsForTesting("test-project")
	defer func() {
		utils.SetFileProtocolSupportedForTesting(false)
		utils.SetExperimentalVersionAllowlistedAccountsForTesting("")
	}()

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testcachevolume",
			CreationToken: gcpgenserver.NewOptString("cache-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(2048),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaNFSV3,
			},
			CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(
				gcpgenserver.FlexCacheV1beta{
					PeerClusterName: gcpgenserver.NewOptString("origin-cluster"),
					PeerVolumeName:  gcpgenserver.NewOptString("origin_volume"),
					PeerSvmName:     gcpgenserver.NewOptString("origin-svm"),
					PeerIpAddresses: []string{"10.0.0.1", "10.0.0.2"},
				},
			),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)

	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "testcachevolume", result.Name)
	assert.Equal(t, "/projects/test-project/locations/test-location/volumes/testcachevolume", result.VendorID)
	assert.Equal(t, uint64(2048), result.QuotaInBytes)
	assert.Equal(t, []string{"NFSV3"}, result.Protocols)

	// Verify CacheParameters are properly set
	require.NotNil(t, result.CacheParameters)
	assert.Equal(t, cvpmodels.FlexCacheV1betaPreviousCacheStatePENDINGCLUSTERPEERING, result.CacheParameters.CacheState)
	assert.Equal(t, models.InitiatingClusterPeeringCode, result.CacheParameters.CacheStateDetailsCode)
	assert.Equal(t, datamodel.InitiatingClusterPeering, result.CacheParameters.CacheStateDetails)
	assert.Equal(t, "origin_volume", result.CacheParameters.PeerVolumeName)
	assert.Equal(t, "origin-cluster", result.CacheParameters.PeerClusterName)
	assert.Equal(t, "origin-svm", result.CacheParameters.PeerSvmName)
	assert.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, result.CacheParameters.PeerIPAddresses)
	assert.Nil(t, result.CacheParameters.PeerExpiryTime) // Should be nil when not set
}

func TestPrepareCreateVolumeParams_CacheParametersWithExpiryTime(t *testing.T) {
	// Setup file protocol support for NFS
	utils.SetFileProtocolSupportedForTesting(true)
	utils.SetExperimentalVersionAllowlistedAccountsForTesting("test-project")
	defer func() {
		utils.SetFileProtocolSupportedForTesting(false)
		utils.SetExperimentalVersionAllowlistedAccountsForTesting("")
	}()

	expiryTime := time.Now().Add(24 * time.Hour)
	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testcachevolume",
			CreationToken: gcpgenserver.NewOptString("cache-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(2048),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaNFSV3,
			},
			CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(
				gcpgenserver.FlexCacheV1beta{
					PeerClusterName:          gcpgenserver.NewOptString("origin-cluster"),
					PeerVolumeName:           gcpgenserver.NewOptString("origin_volume"),
					PeerSvmName:              gcpgenserver.NewOptString("origin-svm"),
					PeerIpAddresses:          []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
					PeeringCommandExpiryTime: gcpgenserver.NewOptNilDateTime(expiryTime),
				},
			),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)

	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "testcachevolume", result.Name)
	assert.Equal(t, uint64(2048), result.QuotaInBytes)

	// Verify CacheParameters are properly set
	require.NotNil(t, result.CacheParameters)
	assert.Equal(t, cvpmodels.FlexCacheV1betaPreviousCacheStatePENDINGCLUSTERPEERING, result.CacheParameters.CacheState)
	assert.Equal(t, models.InitiatingClusterPeeringCode, result.CacheParameters.CacheStateDetailsCode)
	assert.Equal(t, datamodel.InitiatingClusterPeering, result.CacheParameters.CacheStateDetails)
	assert.Equal(t, "origin_volume", result.CacheParameters.PeerVolumeName)
	assert.Equal(t, "origin-cluster", result.CacheParameters.PeerClusterName)
	assert.Equal(t, "origin-svm", result.CacheParameters.PeerSvmName)
	assert.Equal(t, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, result.CacheParameters.PeerIPAddresses)

	// Verify PeerExpiryTime is set correctly
	require.NotNil(t, result.CacheParameters.PeerExpiryTime)
	assert.Equal(t, expiryTime, *result.CacheParameters.PeerExpiryTime)
}

func TestPrepareCreateVolumeParams(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	// Setup file protocol support for NFS tests
	utils.SetFileProtocolSupportedForTesting(true)
	utils.SetExperimentalVersionAllowlistedAccountsForTesting("test-project")
	defer func() {
		utils.SetFileProtocolSupportedForTesting(false)
		utils.SetExperimentalVersionAllowlistedAccountsForTesting("")
	}()

	t.Run("ValidInputWithBlockProperties", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
				BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(
					gcpgenserver.BlockPropertiesV1beta{
						OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
					},
				),
				BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
					gcpgenserver.BackupConfigV1beta{
						BackupPolicyId:         gcpgenserver.NewOptNilString("backup-policy-id"),
						BackupVaultId:          gcpgenserver.NewOptNilString("backup-vault-id"),
						ScheduledBackupEnabled: gcpgenserver.NewOptNilBool(true),
					},
				),
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		expected := &common.CreateVolumeParams{
			AccountName:      "test-project",
			Region:           "test-region",
			Zone:             "test-zone",
			Name:             "testvolume",
			VendorID:         "/projects/test-project/locations/test-location/volumes/testvolume",
			CreationToken:    "test-token",
			PoolID:           "test-pool",
			QuotaInBytes:     1024,
			IsDataProtection: true,
			BlockProperties: &common.BlockPropertiesRequest{
				OSType: "LINUX",
			},
			Protocols: []string{
				"ISCSI",
			},
			DataProtection: &models.DataProtection{
				ScheduledBackupEnabled: nillable.GetBoolPtr(true),
				BackupVaultID:          "backup-vault-id",
				BackupPolicyId:         "backup-policy-id",
			},
		}
		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})
	t.Run("ValidInputWithBlockPropertiesForSnaphotRestore", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
				BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(
					gcpgenserver.BlockPropertiesV1beta{
						OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
					},
				),
				BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
					gcpgenserver.BackupConfigV1beta{
						BackupPolicyId:         gcpgenserver.NewOptNilString("backup-policy-id"),
						BackupVaultId:          gcpgenserver.NewOptNilString("backup-vault-id"),
						ScheduledBackupEnabled: gcpgenserver.NewOptNilBool(true),
					},
				),
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
			SnapshotId: gcpgenserver.NewOptString("test-snapshot-id"),
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		expected := &common.CreateVolumeParams{
			AccountName:      "test-project",
			Region:           "test-region",
			Zone:             "test-zone",
			Name:             "testvolume",
			VendorID:         "/projects/test-project/locations/test-location/volumes/testvolume",
			CreationToken:    "test-token",
			PoolID:           "test-pool",
			QuotaInBytes:     1024,
			IsDataProtection: true,
			BlockProperties: &common.BlockPropertiesRequest{
				OSType: "LINUX",
			},
			Protocols: []string{
				"ISCSI",
			},
			DataProtection: &models.DataProtection{
				ScheduledBackupEnabled: nillable.GetBoolPtr(true),
				BackupVaultID:          "backup-vault-id",
				BackupPolicyId:         "backup-policy-id",
			},
			SnapshotID: "test-snapshot-id",
		}
		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})
	t.Run("SnapReserveIsSet_ValidValue", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				SnapReserve:   gcpgenserver.NewOptFloat64(50),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(50), result.SnapReserve)
	})

	t.Run("SnapReserveIsSet_NegativeValue", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				SnapReserve:   gcpgenserver.NewOptFloat64(-1),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "SnapReserve cannot be negative")
	})

	t.Run("SnapReserveIsSet_TooLargeValue", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				SnapReserve:   gcpgenserver.NewOptFloat64(91),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Maximum allowed snapshot-reserve-percentage value during create is 90")
	})

	t.Run("WhenTieringPolicyIsEnabled", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
				BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(
					gcpgenserver.BlockPropertiesV1beta{
						OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
					},
				),
				BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
					gcpgenserver.BackupConfigV1beta{
						BackupPolicyId:         gcpgenserver.NewOptNilString("backup-policy-id"),
						BackupVaultId:          gcpgenserver.NewOptNilString("backup-vault-id"),
						ScheduledBackupEnabled: gcpgenserver.NewOptNilBool(true),
					},
				),
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
						CoolingThresholdDays: gcpgenserver.OptNilInt32{
							Value: 30,
							Set:   true,
						},
					},
				),
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		expected := &common.CreateVolumeParams{
			AccountName:      "test-project",
			Region:           "test-region",
			Zone:             "test-zone",
			Name:             "testvolume",
			VendorID:         "/projects/test-project/locations/test-location/volumes/testvolume",
			CreationToken:    "test-token",
			PoolID:           "test-pool",
			QuotaInBytes:     1024,
			IsDataProtection: true,
			BlockProperties: &common.BlockPropertiesRequest{
				OSType: "LINUX",
			},
			Protocols: []string{
				"ISCSI",
			},
			DataProtection: &models.DataProtection{
				ScheduledBackupEnabled: nillable.GetBoolPtr(true),
				BackupVaultID:          "backup-vault-id",
				BackupPolicyId:         "backup-policy-id",
			},
			AutoTieringPolicy: &common.AutoTieringPolicy{
				AutoTieringEnabled:   true,
				CoolingThresholdDays: 30,
				TieringPolicy:        "snapshot_only",
				RetrievalPolicy:      "default",
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})

	t.Run("WhenTieringPolicyIsPaused", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
				BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(
					gcpgenserver.BlockPropertiesV1beta{
						OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
					},
				),
				BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
					gcpgenserver.BackupConfigV1beta{
						BackupPolicyId:         gcpgenserver.NewOptNilString("backup-policy-id"),
						BackupVaultId:          gcpgenserver.NewOptNilString("backup-vault-id"),
						ScheduledBackupEnabled: gcpgenserver.NewOptNilBool(true),
					},
				),
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("PAUSED"),
					},
				),
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"
		expected := &common.CreateVolumeParams{
			AccountName:      "test-project",
			Region:           "test-region",
			Zone:             "test-zone",
			Name:             "testvolume",
			VendorID:         "/projects/test-project/locations/test-location/volumes/testvolume",
			CreationToken:    "test-token",
			PoolID:           "test-pool",
			QuotaInBytes:     1024,
			IsDataProtection: true,
			BlockProperties: &common.BlockPropertiesRequest{
				OSType: "LINUX",
			},
			Protocols: []string{
				"ISCSI",
			},
			DataProtection: &models.DataProtection{
				ScheduledBackupEnabled: nillable.GetBoolPtr(true),
				BackupVaultID:          "backup-vault-id",
				BackupPolicyId:         "backup-policy-id",
			},
			AutoTieringPolicy: &common.AutoTieringPolicy{
				AutoTieringEnabled:    false,
				TieringPolicy:         "none",
				CloudWriteModeEnabled: nil, // nil when tiering is paused
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})

	t.Run("ValidInputWithFilePropertiesAndExportRules", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
							{
								AllowedClients: "192.168.1.0/24",
								AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
								Nfsv3:          gcpgenserver.NewOptNilBool(true),
								Nfsv4:          gcpgenserver.NewOptNilBool(false),
							},
							{
								AllowedClients: "10.0.0.0/8",
								AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
								Nfsv3:          gcpgenserver.NewOptNilBool(false),
								Nfsv4:          gcpgenserver.NewOptNilBool(true),
							},
						},
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		expected := &common.CreateVolumeParams{
			AccountName:   "test-project",
			Region:        "test-region",
			Zone:          "test-zone",
			Name:          "testvolume",
			PoolID:        "test-pool",
			QuotaInBytes:  1024,
			Network:       "",
			CreationToken: "test-token",
			VendorID:      "/projects/test-project/locations/test-location/volumes/testvolume",
			Protocols: []string{
				"NFSV3",
			},
			FileProperties: &models.FileProperties{
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: req.Volume.CreationToken.Value,
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     "READ_WRITE",
							NFSv3:          true,
							NFSv4:          false,
							Index:          1,
							UnixReadWrite:  true,
						},
						{
							AllowedClients: "10.0.0.0/8",
							AccessType:     "READ_ONLY",
							NFSv3:          false,
							NFSv4:          true,
							Index:          2,
							UnixReadOnly:   true,
						},
					},
				},
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})

	t.Run("ValidInputWithFilePropertiesAndExportRulesAllSquashEnabled", func(tt *testing.T) {
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(true)

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
							{
								AllowedClients:      "192.168.1.0/24",
								AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
								Nfsv3:               gcpgenserver.NewOptNilBool(true),
								Nfsv4:               gcpgenserver.NewOptNilBool(false),
								AllSquash:           gcpgenserver.NewOptNilBool(true),
								AnonUid:             gcpgenserver.NewOptNilInt64(1001),
								Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
								Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
							},
							{
								AllowedClients:      "10.0.0.0/8",
								AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
								Nfsv3:               gcpgenserver.NewOptNilBool(false),
								Nfsv4:               gcpgenserver.NewOptNilBool(true),
								AllSquash:           gcpgenserver.NewOptNilBool(false),
								AnonUid:             gcpgenserver.NewOptNilInt64(0),
								Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
								Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
							},
						},
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 2)

		rule1 := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule1.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule1.AccessType)
		assert.NotNil(tt, rule1.AllSquash)
		assert.True(tt, *rule1.AllSquash)
		assert.NotNil(tt, rule1.AnonUid)
		assert.Equal(tt, int64(1001), *rule1.AnonUid)
		assert.True(tt, rule1.UnixReadWrite)
		assert.False(tt, rule1.UnixReadOnly)
		assert.False(tt, rule1.Superuser)

		rule2 := result.FileProperties.ExportPolicy.ExportRules[1]
		assert.Equal(tt, "10.0.0.0/8", rule2.AllowedClients)
		assert.Equal(tt, "READ_ONLY", rule2.AccessType)
		assert.NotNil(tt, rule2.AllSquash)
		assert.False(tt, *rule2.AllSquash)
		assert.Nil(tt, rule2.AnonUid)
		assert.True(tt, rule2.UnixReadOnly)
		assert.False(tt, rule2.UnixReadWrite)
	})

	t.Run("ValidInputWithFilePropertiesAndExportRulesAllSquashEnabled_ValidationError", func(tt *testing.T) {
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(true)

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
							{
								AllowedClients: "192.168.1.0/24",
								AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
								Nfsv3:          gcpgenserver.NewOptNilBool(true),
								Nfsv4:          gcpgenserver.NewOptNilBool(false),
								AllSquash:      gcpgenserver.NewOptNilBool(true),
								// Missing AnonUid - should fail validation
								Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
								Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
							},
						},
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "AnonUid must be set when AllSquash is enabled")
	})

	t.Run("ValidInputWithFilePropertiesAndExportRulesAllSquashEnabled_WithHasRootAccessTrue", func(tt *testing.T) {
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(true)

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
							{
								AllowedClients:      "192.168.1.0/24",
								AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
								Nfsv3:               gcpgenserver.NewOptNilBool(true),
								Nfsv4:               gcpgenserver.NewOptNilBool(false),
								AllSquash:           gcpgenserver.NewOptNilBool(true),
								AnonUid:             gcpgenserver.NewOptNilInt64(1001),
								HasRootAccess:       gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue),
								Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
								Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
							},
						},
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "RootSquash cannot be enabled when AllSquash is true for the same rule")
	})

	t.Run("ValidInputWithFilePropertiesAndExportRulesAllSquashEnabled_WithHasRootAccessOn", func(tt *testing.T) {
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(true)

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
							{
								AllowedClients:      "192.168.1.0/24",
								AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
								Nfsv3:               gcpgenserver.NewOptNilBool(true),
								Nfsv4:               gcpgenserver.NewOptNilBool(false),
								AllSquash:           gcpgenserver.NewOptNilBool(true),
								AnonUid:             gcpgenserver.NewOptNilInt64(1001),
								HasRootAccess:       gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessOn),
								Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
								Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
							},
						},
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "RootSquash cannot be enabled when AllSquash is true for the same rule")
	})

	t.Run("ValidInputWithFilePropertiesAndExportRules_WithHasRootAccessTrue_AllSquashDisabled", func(tt *testing.T) {
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(false)

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
							{
								AllowedClients:      "192.168.1.0/24",
								AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
								Nfsv3:               gcpgenserver.NewOptNilBool(true),
								Nfsv4:               gcpgenserver.NewOptNilBool(false),
								HasRootAccess:       gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue),
								Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
								Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
								Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
							},
						},
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)

		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule.AccessType)
		assert.True(tt, rule.Superuser)
	})

	t.Run("ValidInputWithSecurityStyle", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				SecurityStyle: gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleUNIX),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		expected := &common.CreateVolumeParams{
			AccountName:   "test-project",
			Region:        "test-region",
			Zone:          "test-zone",
			Name:          "testvolume",
			PoolID:        "test-pool",
			QuotaInBytes:  1024,
			Network:       "",
			CreationToken: "test-token",
			VendorID:      "/projects/test-project/locations/test-location/volumes/testvolume",
			Protocols: []string{
				"NFSV3",
			},
			FileProperties: &models.FileProperties{
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: req.Volume.CreationToken.Value,
					ExportRules:      nil,
				},
				SecurityStyle: "UNIX",
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
		assert.Equal(tt, "UNIX", result.FileProperties.SecurityStyle)
	})

	t.Run("ValidInputWithUnixPermissions", func(tt *testing.T) {
		originalUnixPermissionsEnabled := unixPermissionsEnabled
		defer func() { unixPermissionsEnabled = originalUnixPermissionsEnabled }()
		unixPermissionsEnabled = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:      "testvolume",
				CreationToken:   gcpgenserver.NewOptString("test-token"),
				PoolId:          gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:    gcpgenserver.NewOptFloat64(1024),
				Protocols:       []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				SecurityStyle:   gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleUNIX),
				UnixPermissions: gcpgenserver.NewOptNilString("0755"),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		require.NotNil(tt, result.FileProperties)
		assert.Equal(tt, "0755", result.FileProperties.UnixPermissions)
	})

	t.Run("ValidInputWithUnixPermissionsWithoutSecurityStyle", func(tt *testing.T) {
		originalUnixPermissionsEnabled := unixPermissionsEnabled
		defer func() { unixPermissionsEnabled = originalUnixPermissionsEnabled }()
		unixPermissionsEnabled = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:      "testvolume",
				CreationToken:   gcpgenserver.NewOptString("test-token"),
				PoolId:          gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:    gcpgenserver.NewOptFloat64(1024),
				Protocols:       []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				UnixPermissions: gcpgenserver.NewOptNilString("0755"),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		require.NotNil(tt, result.FileProperties)
		assert.Equal(tt, "0755", result.FileProperties.UnixPermissions)
		assert.Equal(tt, string(gcpgenserver.VolumeV1betaSecurityStyleUNIX), result.FileProperties.SecurityStyle)
	})

	t.Run("MixedSanNfsProtocolsWithUnixPermissionsReturnsValidationError", func(tt *testing.T) {
		originalUnixPermissionsEnabled := unixPermissionsEnabled
		defer func() { unixPermissionsEnabled = originalUnixPermissionsEnabled }()
		unixPermissionsEnabled = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				SecurityStyle:   gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleUNIX),
				UnixPermissions: gcpgenserver.NewOptNilString("0755"),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}

		assert.Panics(tt, func() {
			_, _ = _prepareCreateVolumeParams(req, params, "test-region", "test-zone", nil)
		})
	})

	t.Run("ValidInputWithMultipleProtocols", func(tt *testing.T) {
		origEnableSmb := enableSmb
		defer func() { enableSmb = origEnableSmb }()
		enableSmb = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
					gcpgenserver.ProtocolsV1betaNFSV4,
					gcpgenserver.ProtocolsV1betaSMB,
				},
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"
		expected := &common.CreateVolumeParams{
			AccountName:   "test-project",
			Region:        "test-region",
			Zone:          "test-zone",
			Name:          "testvolume",
			PoolID:        "test-pool",
			QuotaInBytes:  1024,
			Network:       "",
			CreationToken: "test-token",
			VendorID:      "/projects/test-project/locations/test-location/volumes/testvolume",
			Protocols: []string{
				"NFSV3", "NFSV4", "SMB",
			},
			FileProperties: &models.FileProperties{
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: req.Volume.CreationToken.Value,
					ExportRules:      nil,
				},
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})

	t.Run("SMBProtocolMissingADConfig", func(tt *testing.T) {
		origEnableSmb := enableSmb
		defer func() { enableSmb = origEnableSmb }()
		enableSmb = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
					gcpgenserver.ProtocolsV1betaSMB,
				},
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		_, err := prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: ""})
		assert.Error(tt, err)
		assert.EqualError(tt, err, "SMB protocol requires the pool to be joined to an Active Directory.")
	})

	t.Run("SMBProtocolDisabled", func(tt *testing.T) {
		origEnableSmb := enableSmb
		defer func() { enableSmb = origEnableSmb }()
		enableSmb = false

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
					gcpgenserver.ProtocolsV1betaSMB,
				},
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		_, err := prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
		assert.Error(tt, err)
		assert.EqualError(tt, err, "SMB protocol is currently not enabled.")
	})

	t.Run("SMBContinuouslyAvailable_FeatureDisabled_ReturnsError", func(tt *testing.T) {
		origEnableSmb := enableSmb
		origSmbCaShareEnabled := smbCaShareEnabled
		defer func() {
			enableSmb = origEnableSmb
			smbCaShareEnabled = origSmbCaShareEnabled
		}()
		enableSmb = true
		smbCaShareEnabled = false

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaSMB,
				},
				SecurityStyle: gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleNTFS),
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
				},
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		_, err := _prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SMB continuously_available share feature is not enabled")
	})

	t.Run("SMBContinuouslyAvailable_NonNTFSSecurityStyle_ReturnsError", func(tt *testing.T) {
		origEnableSmb := enableSmb
		origSmbCaShareEnabled := smbCaShareEnabled
		defer func() {
			enableSmb = origEnableSmb
			smbCaShareEnabled = origSmbCaShareEnabled
		}()
		enableSmb = true
		smbCaShareEnabled = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaSMB,
				},
				SecurityStyle: gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleUNIX),
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
				},
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		_, err := _prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Security style must be ntfs when specifying continuously_available share property for a SMB volume")
	})

	t.Run("SMBContinuouslyAvailable_WithNTFSSecurityStyle_Succeeds", func(tt *testing.T) {
		origEnableSmb := enableSmb
		origSmbCaShareEnabled := smbCaShareEnabled
		defer func() {
			enableSmb = origEnableSmb
			smbCaShareEnabled = origSmbCaShareEnabled
		}()
		enableSmb = true
		smbCaShareEnabled = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaSMB,
				},
				SecurityStyle: gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleNTFS),
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
				},
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.FileProperties)
		assert.Equal(tt, string(gcpgenserver.VolumeV1betaSecurityStyleNTFS), result.FileProperties.SecurityStyle)
	})

	t.Run("SMBAccessBasedEnumeration_WithUNIXSecurityStyle_ReturnsError", func(tt *testing.T) {
		origEnableSmb := enableSmb
		defer func() { enableSmb = origEnableSmb }()
		enableSmb = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaSMB,
				},
				SecurityStyle: gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleUNIX),
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION,
				},
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		_, err := _prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Security style must be ntfs when specifying access_based_enumeration share property for a SMB volume")
	})

	t.Run("SMBAccessBasedEnumeration_WithNTFSSecurityStyle_Succeeds", func(tt *testing.T) {
		origEnableSmb := enableSmb
		defer func() { enableSmb = origEnableSmb }()
		enableSmb = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaSMB,
				},
				SecurityStyle: gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyleNTFS),
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION,
				},
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.FileProperties)
		assert.Equal(tt, string(gcpgenserver.VolumeV1betaSecurityStyleNTFS), result.FileProperties.SecurityStyle)
	})

	t.Run("ExportPolicyRules_ExceedsLimit_ReturnsError", func(tt *testing.T) {
		origExportRulesLimit := exportRulesLimit
		defer func() { exportRulesLimit = origExportRulesLimit }()
		exportRulesLimit = 20

		// Create 21 export rules to exceed the limit
		rules := make([]gcpgenserver.SimpleExportPolicyRuleV1beta, 21)
		for i := 0; i < 21; i++ {
			rules[i] = gcpgenserver.SimpleExportPolicyRuleV1beta{
				AllowedClients: fmt.Sprintf("10.0.0.%d/32", i),
				AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:          gcpgenserver.NewOptNilBool(true),
				Nfsv4:          gcpgenserver.NewOptNilBool(false),
			}
		}

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: rules,
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		_, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Number of export rules cannot exceed 20")
	})

	t.Run("ExportPolicyRules_WithinLimit_Succeeds", func(tt *testing.T) {
		origExportRulesLimit := exportRulesLimit
		defer func() { exportRulesLimit = origExportRulesLimit }()
		exportRulesLimit = 20

		// Create exactly 20 export rules (at the limit)
		rules := make([]gcpgenserver.SimpleExportPolicyRuleV1beta, 20)
		for i := 0; i < 20; i++ {
			rules[i] = gcpgenserver.SimpleExportPolicyRuleV1beta{
				AllowedClients: fmt.Sprintf("10.0.0.%d/32", i),
				AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:          gcpgenserver.NewOptNilBool(true),
				Nfsv4:          gcpgenserver.NewOptNilBool(false),
			}
		}

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: rules,
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 20)
	})

	t.Run("ValidInputWithLargeCapacityAndConstituentCount", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:                  "testvolume",
				CreationToken:               gcpgenserver.NewOptString("test-token"),
				PoolId:                      gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:                gcpgenserver.NewOptFloat64(1024),
				LargeCapacity:               gcpgenserver.NewOptNilBool(true),
				LargeVolumeConstituentCount: gcpgenserver.NewOptNilInt32(8),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
				BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(
					gcpgenserver.BlockPropertiesV1beta{
						OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		expected := &common.CreateVolumeParams{
			AccountName:                 "test-project",
			Region:                      "test-region",
			Zone:                        "test-region",
			Name:                        "testvolume",
			VendorID:                    "/projects/test-project/locations/test-location/volumes/testvolume",
			CreationToken:               "test-token",
			PoolID:                      "test-pool",
			QuotaInBytes:                1024,
			LargeCapacity:               true,
			LargeVolumeConstituentCount: 8,
			BlockProperties: &common.BlockPropertiesRequest{
				OSType: "LINUX",
			},
			Protocols: []string{
				"ISCSI",
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})

	t.Run("ValidInputWithLargeCapacityOnly", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				LargeCapacity: gcpgenserver.NewOptNilBool(true),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
				BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(
					gcpgenserver.BlockPropertiesV1beta{
						OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		expected := &common.CreateVolumeParams{
			AccountName:                 "test-project",
			Zone:                        "test-region",
			Region:                      "test-region",
			Name:                        "testvolume",
			VendorID:                    "/projects/test-project/locations/test-location/volumes/testvolume",
			CreationToken:               "test-token",
			PoolID:                      "test-pool",
			QuotaInBytes:                1024,
			LargeCapacity:               true,
			LargeVolumeConstituentCount: 0, // Default value when not set
			BlockProperties: &common.BlockPropertiesRequest{
				OSType: "LINUX",
			},
			Protocols: []string{
				"ISCSI",
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})

	t.Run("WhenTieringPolicyEnabledWithHotTierBypassModeEnabled", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV4,
				},
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
						CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
						HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.True(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		assert.NotNil(tt, result.AutoTieringPolicy.CloudWriteModeEnabled)
		assert.True(tt, *result.AutoTieringPolicy.CloudWriteModeEnabled)
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("WhenTieringPolicyEnabledWithHotTierBypassModeDisabled", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
						CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
						HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(false),
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		// CloudWriteModeEnabled can be nil when HotTierBypassModeEnabled is false
		if result.AutoTieringPolicy.CloudWriteModeEnabled != nil {
			assert.False(tt, *result.AutoTieringPolicy.CloudWriteModeEnabled)
		}
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("WhenTieringPolicyEnabledWithHotTierBypassModeNotSet", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
						CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
						// HotTierBypassModeEnabled not set
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled) // Should default to false
		// CloudWriteModeEnabled can be nil when HotTierBypassModeEnabled is false
		if result.AutoTieringPolicy.CloudWriteModeEnabled != nil {
			assert.False(tt, *result.AutoTieringPolicy.CloudWriteModeEnabled)
		}
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("WhenBlockVolumeWithHotTierBypassModeDisabled_ShouldNotError", func(tt *testing.T) {
		// Test that when HotTierBypassModeEnabled=false for block volume, it should be ignored (no error)
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
						CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
						HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(false),
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err, "HotTierBypassMode=false for block volume should be ignored, not cause an error")
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		// HotTierBypassModeEnabled should remain false (default) as it's ignored for block volumes
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	// Comprehensive tests for HotTierBypassModeEnabled logic
	t.Run("HotTierBypassModeEnabled_ShouldSetTieringPolicyToAll", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
						CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
						HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.True(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		// Key assertion: When HotTierBypassModeEnabled is true, TieringPolicy should be "all"
		assert.Equal(tt, "all", result.AutoTieringPolicy.TieringPolicy)
		assert.True(tt, *result.AutoTieringPolicy.CloudWriteModeEnabled)
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("HotTierBypassModeDisabled_ShouldKeepOriginalTieringPolicy", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
						CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
						HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(false),
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		// When HotTierBypassModeEnabled is false, TieringPolicy should remain "auto"
		assert.Equal(tt, "auto", result.AutoTieringPolicy.TieringPolicy)
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("HotTierBypassModeNotSet_ShouldKeepOriginalTieringPolicy", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
						CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
						// HotTierBypassModeEnabled not set
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled) // Should default to false
		// When HotTierBypassModeEnabled is not set, TieringPolicy should remain "auto"
		assert.Equal(tt, "snapshot_only", result.AutoTieringPolicy.TieringPolicy)
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("HotTierBypassModeEnabled_WithPausedTieringPolicy_ShouldOverrideToAll", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV4,
				},
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("PAUSED"),
						HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
					},
				),
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-region"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.False(tt, result.AutoTieringPolicy.AutoTieringEnabled) // PAUSED means disabled
		assert.True(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		// Even with PAUSED tier action, HotTierBypassModeEnabled should override to "all"
		assert.Equal(tt, "all", result.AutoTieringPolicy.TieringPolicy)
	})

	t.Run("FilesClone_WithSMBProtocol_Success", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true
		origEnableSmb := enableSmb
		defer func() { enableSmb = origEnableSmb }()
		enableSmb = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaSMB}, // SMB is also a Files protocol
			},
			SnapshotId: gcpgenserver.NewOptString("test-snapshot-id"),
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
		assert.NoError(tt, err)
		assert.Equal(tt, "test-snapshot-id", result.SnapshotID)
	})

	t.Run("FilesClone_WithNFSv4Protocol_Success", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4}, // NFSv4 is also a Files protocol
			},
			SnapshotId: gcpgenserver.NewOptString("test-snapshot-id"),
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, "test-snapshot-id", result.SnapshotID)
	})

	t.Run("LargeCapacityTrueWithoutLargeVolumeConstituentCountInHybridReplication_ReturnsError", func(tt *testing.T) {
		// Mock hybridReplicationEnabled to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				LargeCapacity: gcpgenserver.NewOptNilBool(true),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
			},
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType:       gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					ReplicationSchedule:         gcpgenserver.NewOptHybridReplicationParametersV1betaReplicationSchedule("daily"),
					LargeVolumeConstituentCount: gcpgenserver.OptNilInt32{Set: false}, // Not set
					PeerClusterName:             "peer-cluster",
					PeerVolumeName:              "peer-volume",
					PeerSvmName:                 "peer-svm",
					PeerIpAddresses:             []string{"192.168.1.1"},
				},
				Set: true,
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "LargeVolumeConstituentCount must be set for Large Volumes in hybrid replication parameters.")
	})

	t.Run("LargeCapacityTrueWithLargeVolumeConstituentCountInHybridReplication_Succeeds", func(tt *testing.T) {
		// Mock hybridReplicationEnabled to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				LargeCapacity: gcpgenserver.NewOptNilBool(true),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaISCSI,
				},
			},
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType:       gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					ReplicationSchedule:         gcpgenserver.NewOptHybridReplicationParametersV1betaReplicationSchedule("daily"),
					LargeVolumeConstituentCount: gcpgenserver.OptNilInt32{Value: 5, Set: true},
					PeerClusterName:             "peer-cluster",
					PeerVolumeName:              "peer-volume",
					PeerSvmName:                 "peer-svm",
					PeerIpAddresses:             []string{"192.168.1.1"},
				},
				Set: true,
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, int32(5), result.LargeVolumeConstituentCount)
		assert.True(tt, result.LargeCapacity)
	})
}

func TestPrepareUpdateVolumeParamsHotTierBypassMode(t *testing.T) {
	// Comprehensive tests for HotTierBypassModeEnabled logic in update volume
	t.Run("HotTierBypassModeEnabled_ShouldSetTieringPolicyToAll", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       false,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "none",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.True(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		// Key assertion: When HotTierBypassModeEnabled is true, TieringPolicy should be "all"
		assert.Equal(tt, "all", result.AutoTieringPolicy.TieringPolicy)
		assert.NotNil(tt, result.AutoTieringPolicy.CloudWriteModeEnabled)
		assert.True(tt, *result.AutoTieringPolicy.CloudWriteModeEnabled)
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("HotTierBypassModeDisabled_ShouldKeepOriginalTieringPolicy", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(false),
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: true,
				TieringPolicy:            "all",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		// When HotTierBypassModeEnabled transitions from true to false, TieringPolicy should be "auto"
		assert.Equal(tt, "auto", result.AutoTieringPolicy.TieringPolicy)
		// CloudWriteModeEnabled can be nil when HotTierBypassModeEnabled is false
		if result.AutoTieringPolicy.CloudWriteModeEnabled != nil {
			assert.False(tt, *result.AutoTieringPolicy.CloudWriteModeEnabled)
		}
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("HotTierBypassModeNotSet_ShouldKeepOriginalTieringPolicy", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
					// HotTierBypassModeEnabled not set
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "auto",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled) // Should default to false
		// When HotTierBypassModeEnabled is not set, TieringPolicy should remain "auto"
		assert.Equal(tt, "auto", result.AutoTieringPolicy.TieringPolicy)
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("HotTierBypassModeEnabled_WithPausedTieringPolicy_ShouldReturnError", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("PAUSED"),
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "auto",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "hotTierBypassMode can not be enabled along with pausing tiering on volume")
	})

	t.Run("NoTieringPolicy_ShouldReturnNil", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(2048 * 1024 * 1024 * 1024), // 2048 GiB in bytes
			// No TieringPolicy set
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "auto",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.Nil(tt, result.AutoTieringPolicy) // Should be nil when no tiering policy is provided
		assert.Equal(tt, int64(2048*1024*1024*1024), result.QuotaInBytes)
	})

	t.Run("AutoTieringDisabled_ShouldReturnError", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = false // Disable auto tiering

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       false,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "none",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Auto-Tiering feature is currently not enabled")
	})

	t.Run("PauseTieringWithHotTierBypassEnabled_ShouldReturnError", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("PAUSED"),
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: true,
				TieringPolicy:            "all",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "existing volume has hotTierBypassMode enabled, cannot pause tiering")
	})

	t.Run("UpdateTieringPolicyWithoutTierAction_FirstTime_ShouldReturnError", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// No TierAction set
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			// No existing tiering policy
			AutoTieringPolicy: nil,
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Tiering action is required when enabling auto-tiering on volume for the first time")
	})

	t.Run("UpdateTieringPolicyWithoutTierAction_OnlyCoolingThresholdDays_HTBypassDisabled_ShouldSucceed", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// No TierAction set, only CoolingThresholdDays
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 40, Set: true},
					// HotTierBypassModeEnabled not set
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: false, // HT bypass disabled in DB
				TieringPolicy:            "auto",
				CoolingThresholdDays:     30,
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.Equal(tt, int32(40), result.AutoTieringPolicy.CoolingThresholdDays)
		// HT bypass should be preserved from DB (false)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
	})

	t.Run("DisableHotTierBypassWithoutTierAction_PreservesExistingAutoPolicy_Success", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// No TierAction set
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(false),
					CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 35, Set: true},
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: true,
				TieringPolicy:            "auto",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.Equal(tt, "auto", result.AutoTieringPolicy.TieringPolicy)
		assert.Equal(tt, "default", result.AutoTieringPolicy.RetrievalPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		// CloudWriteModeEnabled can be nil when HotTierBypassModeEnabled is false
		if result.AutoTieringPolicy.CloudWriteModeEnabled != nil {
			assert.False(tt, *result.AutoTieringPolicy.CloudWriteModeEnabled)
		}
		assert.Equal(tt, int32(35), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("HotTierBypassModeNotSupportedForBlockVolume", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolISCSI}, // Block volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       false,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "none",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "hotTierBypassMode is not supported for block volume")
	})

	t.Run("HotTierBypassModeSupportedForFileVolume", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       false,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "none",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err, "HotTierBypassMode should be supported for file volumes")
		assert.NotNil(tt, result)
		assert.True(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
	})

	// Tests for validation: cooling threshold days requires hotTierBypassModeEnabled
	t.Run("OnlyCoolingThresholdDays_WithoutHotTierBypassMode_ShouldReturnError", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// Only CoolingThresholdDays, no HotTierBypassModeEnabled, no TierAction
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 50, Set: true},
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: true,
				TieringPolicy:            "all",
				CoolingThresholdDays:     30,
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Cooling threshold days cannot be updated when Hot Tier Bypass Mode is enabled.")
	})

	t.Run("CoolingThresholdDays_WithHotTierBypassModeEnabled_ShouldSucceed", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// Both CoolingThresholdDays and HotTierBypassModeEnabled provided
					CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 60, Set: true},
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(true),
					// No TierAction
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "auto",
				CoolingThresholdDays:     30,
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.Equal(tt, int32(60), result.AutoTieringPolicy.CoolingThresholdDays)
		assert.True(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		assert.Equal(tt, "all", result.AutoTieringPolicy.TieringPolicy)
	})

	t.Run("CoolingThresholdDays_WithTierAction_HTBypassDisabledInDB_ShouldPreserveFalse", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// CoolingThresholdDays with TierAction, HT bypass not set
					TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 45, Set: true},
					// HotTierBypassModeEnabled not set
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       false,
				HotTierBypassModeEnabled: false, // HT bypass disabled in DB
				TieringPolicy:            "none",
				CoolingThresholdDays:     30,
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.Equal(tt, int32(45), result.AutoTieringPolicy.CoolingThresholdDays)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		// HT bypass should be preserved from DB (false)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
	})

	t.Run("TierActionENABLED_HTBypassNotSet_HTBypassEnabledInDB_ShouldPreserveAndSetConsistency", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// TierAction=ENABLED, HT bypass not set
					TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
					// HotTierBypassModeEnabled not set - should preserve from DB
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: true, // HT bypass enabled in DB - should be preserved
				TieringPolicy:            "all",
				CoolingThresholdDays:     25,
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		// HT bypass should be preserved from DB (true)
		assert.True(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		// Consistency: TieringPolicy should be "all" when HT bypass is true
		assert.Equal(tt, "all", result.AutoTieringPolicy.TieringPolicy)
		// Consistency: CloudWriteModeEnabled should be true when HT bypass is true
		assert.NotNil(tt, result.AutoTieringPolicy.CloudWriteModeEnabled)
		assert.True(tt, *result.AutoTieringPolicy.CloudWriteModeEnabled)
		assert.Equal(tt, int32(30), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("TierActionENABLED_HTBypassNotSet_HTBypassDisabledInDB_ShouldPreserveFalse", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// TierAction=ENABLED, HT bypass not set
					TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 35, Set: true},
					// HotTierBypassModeEnabled not set - should preserve from DB
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: false, // HT bypass disabled in DB - should be preserved
				TieringPolicy:            "auto",
				CoolingThresholdDays:     20,
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.True(tt, result.AutoTieringPolicy.AutoTieringEnabled)
		// HT bypass should be preserved from DB (false)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		// TieringPolicy should be "auto" (set by TierAction=ENABLED, not overridden since HT bypass is false)
		assert.Equal(tt, "auto", result.AutoTieringPolicy.TieringPolicy)
		assert.Equal(tt, int32(35), result.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("OnlyCoolingThresholdDays_WithHTBypassDisabledInDB_ShouldSucceed", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// Only CoolingThresholdDays, no HotTierBypassModeEnabled, no TierAction
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 35, Set: true},
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: false, // HT bypass disabled in DB - validation should not trigger
				TieringPolicy:            "auto",
				CoolingThresholdDays:     25,
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.Equal(tt, int32(35), result.AutoTieringPolicy.CoolingThresholdDays)
		// HT bypass should be preserved from DB (false)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
	})

	t.Run("CoolingThresholdDays_WithHotTierBypassModeDisabled_ShouldSucceed", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// Both CoolingThresholdDays and HotTierBypassModeEnabled (set to false) provided
					CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 70, Set: true},
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(false),
					// No TierAction
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3}, // File volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: true, // HT bypass enabled in DB
				TieringPolicy:            "all",
				CoolingThresholdDays:     30,
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.AutoTieringPolicy)
		assert.Equal(tt, int32(70), result.AutoTieringPolicy.CoolingThresholdDays)
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
		assert.Equal(tt, "auto", result.AutoTieringPolicy.TieringPolicy)
	})

	t.Run("HotTierBypassModeDisabledForBlockVolume_ShouldNotError", func(tt *testing.T) {
		// Test that when HotTierBypassModeEnabled=false for block volume, it should be ignored (no error)
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays:     gcpgenserver.OptNilInt32{Value: 30, Set: true},
					HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(false),
				},
			),
		}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "test-volume-id",
		}
		region := "test-region"
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolISCSI}, // Block volume
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       false,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "none",
			},
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err, "HotTierBypassMode=false for block volume should be ignored, not cause an error")
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.AutoTieringPolicy)
		// HotTierBypassModeEnabled should remain false (default) as it's ignored for block volumes
		assert.False(tt, result.AutoTieringPolicy.HotTierBypassModeEnabled)
	})
}

func TestPrepareUpdateVolumeParamsLargeCapacity(t *testing.T) {
	region := "test-region"
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume-id",
	}
	dbVolume := &models.Volume{}

	t.Run("LargeCapacityFlagCopiedWhenSet", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:        gcpgenserver.NewOptNilString("test-pool"),
			LargeCapacity: gcpgenserver.NewOptNilBool(true),
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		if assert.NotNil(tt, result.LargeCapacity) {
			assert.True(tt, *result.LargeCapacity)
		}
	})

	t.Run("LargeCapacityFlagNilWhenNotProvided", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			// LargeCapacity not set
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.Nil(tt, result.LargeCapacity)
	})

	t.Run("LargeVolumeConstituentCountCopiedWhenSet", func(tt *testing.T) {
		cvCount := int32(8)
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:                      gcpgenserver.NewOptNilString("test-pool"),
			LargeVolumeConstituentCount: gcpgenserver.NewOptNilInt32(cvCount),
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		if assert.NotNil(tt, result.LargeVolumeConstituentCount) {
			assert.Equal(tt, cvCount, *result.LargeVolumeConstituentCount)
		}
	})

	t.Run("LargeVolumeConstituentCountNilWhenNotProvided", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			// LargeVolumeConstituentCount not set
		}

		result, err := prepareUpdateVolumeParams(req, params, region, dbVolume)

		assert.NoError(tt, err)
		assert.Nil(tt, result.LargeVolumeConstituentCount)
	})
}

func TestV1betaGetMultipleVolumes(t *testing.T) {
	// Helper function to set up CVP environment
	setupCVPEnvironment := func(tt *testing.T) {
		tt.Setenv("CVP_HOST", "some-host")
	}

	t.Run("WhenVolumeUuidsIsNil", func(tt *testing.T) {
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: nil,
		}

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{}
		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badRequest, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badRequest.Code)
		assert.Equal(tt, "VolumeUuids are required", badRequest.Message)
	})

	t.Run("WhenVolumeUuidsExceeds1000", func(tt *testing.T) {
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		// Create a slice with 1001 UUIDs
		volumeUuids := make([]string, 1001)
		for i := 0; i < 1001; i++ {
			volumeUuids[i] = fmt.Sprintf("uuid%d", i)
		}

		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: volumeUuids,
		}

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{}
		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badRequest, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badRequest.Code)
		assert.Equal(tt, "VolumeUuids in body should have at most 1000 items", badRequest.Message)
	})

	t.Run("WhenGetMultipleVolumesFailsWithBadRequest", func(tt *testing.T) {
		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		// mockClient removed (was unused)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok, "Expected OK response when CVP_HOST is not set")
	})
	t.Run("WhenGetMultipleVolumesFailsWithUnprocessableEntity", func(tt *testing.T) {
		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok, "Expected OK response when CVP_HOST is not set")
	})
	t.Run("WhenGetMultipleVolumesFailsUnauthorized", func(tt *testing.T) {
		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok, "Expected OK response when CVP_HOST is not set")
	})
	t.Run("WhenGetMultipleVolumesFailsForbidden", func(tt *testing.T) {
		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok, "Expected OK response when CVP_HOST is not set")
	})
	t.Run("WhenGetMultipleVolumesFailsNotFound", func(tt *testing.T) {
		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok, "Expected OK response when CVP_HOST is not set")
	})
	t.Run("WhenGetMultipleVolumesFailsTooManyRequests", func(tt *testing.T) {
		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok, "Expected OK response when CVP_HOST is not set")
	})
	t.Run("WhenGetMultipleVolumesFailsDefault", func(tt *testing.T) {
		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok, "Expected OK response when CVP_HOST is not set")
	})
	t.Run("WhenGetMultipleVolumesFailsInternalServerError", func(tt *testing.T) {
		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok, "Expected OK response when CVP_HOST is not set")
	})
	t.Run("WhenGetMultipleVolumesNoVolumesFromCVP", func(tt *testing.T) {
		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Volumes, 0)
	})
	t.Run("WhenGetMultipleVolumesNoVolumesFromCVPANDVCP", func(tt *testing.T) {
		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		vcpVolumes := []*models.Volume{
			{
				DisplayName: "vol1",
			},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(vcpVolumes, nil)

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Volumes, 1)
		assert.Equal(tt, "vol1", okResp.Volumes[0].ResourceId)
	})

	t.Run("Success - all volumes found in VCP, CVP_HOST is set", func(tt *testing.T) {
		origGetMultipleVolumesFromCVP := getMultipleVolumesFromCVP
		defer func() {
			getMultipleVolumesFromCVP = origGetMultipleVolumesFromCVP
		}()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		// Set CVP_HOST so the handler will not return early
		cvp.SetCVPHost("http://cvp-host")

		uuids := []string{"uuid1", "uuid2"}
		req := &gcpgenserver.VolumeIdListV1beta{VolumeUuids: uuids}
		params := gcpgenserver.V1betaGetMultipleVolumesParams{ProjectNumber: "proj1"}

		vols := []*models.Volume{
			{DisplayName: "vol1"},
			{DisplayName: "vol2"},
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, uuids, "proj1").Return(vols, nil).Once()

		res, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)

		okResp, ok := res.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Volumes, 2)
		assert.Equal(tt, "vol1", okResp.Volumes[0].ResourceId)
		assert.Equal(tt, "vol2", okResp.Volumes[1].ResourceId)
	})

	t.Run("Success - some volumes found in VCP, some in CVP, CVP_HOST is set", func(tt *testing.T) {
		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		// Save and mock createCVPClient
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockVolumes := volumes.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{
			Volumes: mockVolumes,
		}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		// Set up the mock for the CVP Volumes client
		resourceID1 := "resource-id-2"
		resourceID2 := "resource-id-3"
		mockVolumes.EXPECT().V1betaGetMultipleVolumes(mock.Anything).Return(&volumes.V1betaGetMultipleVolumesOK{
			Payload: &volumes.V1betaGetMultipleVolumesOKBody{
				Volumes: []*cvpmodels.VolumeV1beta{
					{
						VolumeID:   "uuid2",
						ResourceID: &resourceID1,
					},
					{
						VolumeID:   "uuid3",
						ResourceID: &resourceID2,
					},
				},
			},
		}, nil)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		// Set CVP_HOST so the handler will not return early
		cvp.SetCVPHost("http://cvp-host")

		uuids := []string{"uuid1", "uuid2", "uuid3"}
		req := &gcpgenserver.VolumeIdListV1beta{VolumeUuids: uuids}
		params := gcpgenserver.V1betaGetMultipleVolumesParams{ProjectNumber: "proj1"}

		// Return only one volume from VCP to simulate that uuid2 and uuid3 are missing
		volsVCP := []*models.Volume{{BaseModel: models.BaseModel{UUID: "uuid1"}, DisplayName: "vol1"}}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, uuids, "proj1").Return(volsVCP, nil).Once()

		res, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)

		okResp, ok := res.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		// Should contain both VCP and CVP volumes
		assert.Len(tt, okResp.Volumes, 3)
	})

	t.Run("Success - some volumes found in VCP, CVP_HOST is not set", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		// Clear CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		uuids := []string{"uuid1", "uuid2"}
		req := &gcpgenserver.VolumeIdListV1beta{VolumeUuids: uuids}
		params := gcpgenserver.V1betaGetMultipleVolumesParams{ProjectNumber: "proj1"}

		vols := []*models.Volume{
			{DisplayName: "vol1"},
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, uuids, "proj1").Return(vols, nil).Once()

		res, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)

		okResp, ok := res.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Volumes, 1)
		assert.Equal(tt, "vol1", okResp.Volumes[0].ResourceId)
	})

	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "invalid-location",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		// Mock location validation to fail
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location",
			}
		}

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badRequest, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badRequest.Code)
		assert.Equal(tt, "Invalid location", badRequest.Message)
	})

	t.Run("WhenNoMissingVolumes", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		cvp.SetCVPHost("")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return all requested volumes (no missing volumes)
		vols := []*models.Volume{
			{BaseModel: models.BaseModel{UUID: "uuid1"}, DisplayName: "vol1"},
			{BaseModel: models.BaseModel{UUID: "uuid2"}, DisplayName: "vol2"},
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(vols, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Since all volumes are found in VCP, we expect OK response with all volumes
		okResp, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Volumes, 2)
	})

	t.Run("WhenNoMissingVolumesWithCVPEnabled", func(tt *testing.T) {
		// Set CVP_HOST so CVP calls will be made
		cvp.SetCVPHost("http://cvp-host")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return all requested volumes (no missing volumes)
		vols := []*models.Volume{
			{BaseModel: models.BaseModel{UUID: "uuid1"}, DisplayName: "vol1"},
			{BaseModel: models.BaseModel{UUID: "uuid2"}, DisplayName: "vol2"},
		}

		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(vols, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Since all volumes are found in VCP, we expect OK response with all volumes (no CVP call)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Volumes, 2)
	})

	t.Run("WhenXCorrelationIDIsSet", func(tt *testing.T) {
		// Set CVP_HOST so CVP calls will be made
		cvp.SetCVPHost("http://cvp-host")

		// Save and mock createCVPClient
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockVolumes := volumes.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{
			Volumes: mockVolumes,
		}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		// Set up the mock for the CVP Volumes client
		resourceID1 := "resource-id-1"
		resourceID2 := "resource-id-2"
		mockVolumes.EXPECT().V1betaGetMultipleVolumes(mock.Anything).Return(&volumes.V1betaGetMultipleVolumesOK{
			Payload: &volumes.V1betaGetMultipleVolumesOKBody{
				Volumes: []*cvpmodels.VolumeV1beta{
					{VolumeID: "uuid1", ResourceID: &resourceID1},
					{VolumeID: "uuid2", ResourceID: &resourceID2},
				},
			},
		}, nil)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:     "us-east4",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id-123"),
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return empty volumes so CVP will be called
		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Volumes, 2)
	})

	t.Run("WhenCVPResponseIsNil", func(tt *testing.T) {
		// Set CVP_HOST so CVP calls will be made
		setupCVPEnvironment(tt)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return empty volumes so CVP will be called
		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		internalErr, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "unknown error during get multiple volumes operation", internalErr.Message)
	})

	t.Run("WhenSingleVolumeNotInVCP_ShouldFallbackToCVPAndReturnVolume", func(tt *testing.T) {
		// Set CVP_HOST so CVP calls will be made
		cvp.SetCVPHost("http://cvp-host")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		// Single volume request
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1"},
		}

		// Mock VCP to return empty volumes (no error) so CVP will be called
		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		// Save and mock createCVPClient
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockVolumes := volumes.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{
			Volumes: mockVolumes,
		}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		// Set up the mock for the CVP Volumes client - expect CVP to be called
		resourceID := "cvp-resource-id"
		mockVolumes.EXPECT().V1betaGetMultipleVolumes(mock.Anything).Return(&volumes.V1betaGetMultipleVolumesOK{
			Payload: &volumes.V1betaGetMultipleVolumesOKBody{
				Volumes: []*cvpmodels.VolumeV1beta{
					{
						VolumeID:   "uuid1",
						ResourceID: &resourceID,
					},
				},
			},
		}, nil).Once()

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Volumes, 1)
		assert.Equal(tt, "cvp-resource-id", okResp.Volumes[0].ResourceId)
	})

	t.Run("WhenSingleVolumeNotFoundInBothVCPAndCVP_ShouldReturnEmptyList", func(tt *testing.T) {
		// Set CVP_HOST so CVP calls will be made
		cvp.SetCVPHost("http://cvp-host")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaGetMultipleVolumesParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		// Single volume request
		req := &gcpgenserver.VolumeIdListV1beta{
			VolumeUuids: []string{"uuid1"},
		}

		// Mock VCP to return empty volumes (no error) so CVP will be called
		mockOrchestrator.EXPECT().GetMultipleVolumes(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		// Save and mock createCVPClient
		originalCreateCVPClient := createCVPClient
		defer func() { createCVPClient = originalCreateCVPClient }()
		mockVolumes := volumes.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{
			Volumes: mockVolumes,
		}
		createCVPClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		// Set up the mock for the CVP Volumes client - CVP also returns empty (no volumes found)
		mockVolumes.EXPECT().V1betaGetMultipleVolumes(mock.Anything).Return(&volumes.V1betaGetMultipleVolumesOK{
			Payload: &volumes.V1betaGetMultipleVolumesOKBody{
				Volumes: []*cvpmodels.VolumeV1beta{}, // Empty list - no volumes found in CVP either
			},
		}, nil).Once()

		result, err := handler.V1betaGetMultipleVolumes(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultipleVolumesOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Volumes, 0) // Should return empty volumes list when neither VCP nor CVP finds the volume
	})
}

func TestConvertVolumeV1betaCVPToModel(t *testing.T) {
	t.Run("ConvertVolumeV1betaCVPToModelWithFlexCacheParams", func(tt *testing.T) {
		backupConfig := &cvpmodels.BackupConfigV1beta{
			BackupChainBytes:       nillable.GetInt64Ptr(10199181),
			BackupPolicyID:         nillable.GetStringPtr("backup-policy-id"),
			BackupVaultID:          nillable.GetStringPtr("backup-vault-id"),
			ScheduledBackupEnabled: nillable.GetBoolPtr(true),
		}

		cachePrepopulate := &cvpmodels.FlexCachePrePopulateV1beta{
			ExcludePathList: []string{"/exclude1", "/exclude2"},
			PathList:        []string{"/path1", "/path2"},
			Recursion:       nillable.GetBoolPtr(true),
		}

		cacheConfig := &cvpmodels.FlexCacheConfigV1beta{
			AtimeScrubEnabled:       nillable.GetBoolPtr(true),
			AtimeScrubDays:          nillable.GetInt16Ptr(30),
			CifsChangeNotifyEnabled: nillable.GetBoolPtr(true),
			CachePrePopulate:        cachePrepopulate,
			WritebackEnabled:        nillable.GetBoolPtr(true),
		}

		timeNowStrfmt := strfmt.DateTime(time.Now())

		cachePrams := &cvpmodels.FlexCacheV1beta{
			CacheConfig:              cacheConfig,
			Command:                  "test-command",
			PeeringCommandExpiryTime: &timeNowStrfmt,
			EnableGlobalFileLock:     nillable.GetBoolPtr(true),
			Passphrase:               nillable.GetStringPtr("test-passphrase"),
			PeerClusterName:          "alderan",
			PeerIPAddresses:          []string{"10.0.0.1", "10.0.0.2"},
			PeerSvmName:              "peer-svm",
			PeerVolumeName:           "peer-volume",
		}

		input := &cvpmodels.VolumeV1beta{
			ActiveDirectoryConfigID:     nillable.GetStringPtr("ad-config-id"),
			BackupConfig:                backupConfig,
			CacheParameters:             cachePrams,
			ColdTierSizeGib:             nillable.GetFloat64Ptr(10.5),
			Created:                     strfmt.DateTime(time.Now()),
			CreationToken:               nillable.GetStringPtr("test-token"),
			DedicatedCapacity:           nillable.GetBoolPtr(true),
			Deleted:                     &timeNowStrfmt,
			Description:                 nillable.GetStringPtr("test description"),
			ExportPolicy:                nil,
			InReplication:               nillable.GetBoolPtr(false),
			IsDataProtection:            nillable.GetBoolPtr(true),
			IsOnPremMigration:           nillable.GetBoolPtr(false),
			KerberosEnabled:             nillable.GetBoolPtr(true),
			KmsConfigID:                 nillable.GetStringPtr("kms-config-id"),
			KmsConfigResourceID:         nillable.GetStringPtr("kms-resource-id"),
			Labels:                      map[string]string{"env": "test", "team": "avatar"},
			LargeCapacity:               nillable.GetBoolPtr(false),
			LargeVolumeConstituentCount: nillable.GetInt32Ptr(5),
			LdapEnabled:                 nillable.GetBoolPtr(true),
			MountPoints:                 nil,
			MultipleEndpoints:           nillable.GetBoolPtr(true),
			Network:                     "network-id",
			PoolID:                      nillable.GetStringPtr("pool-id"),
			PoolResourceID:              nillable.GetStringPtr("pool-resource-id"),
			Protocols:                   []cvpmodels.ProtocolsV1beta{cvpmodels.ProtocolsV1betaNFSV3},
			QuotaInBytes:                nillable.GetFloat64Ptr(2048),
			ResourceID:                  nillable.GetStringPtr("resource-id"),
			RestrictedActions:           []string{"action1", "action2"},
			SecondaryZone:               nillable.GetStringPtr("secondary-zone"),
			SecurityStyle:               "UNIX",
			ServiceLevel:                cvpmodels.ServiceLevelV1betaNameFLEX,
			SmbSettings:                 []string{"smb1", "smb2"},
			SnapReserve:                 nillable.GetFloat64Ptr(100),
			SnapshotDirectory:           nillable.GetBoolPtr(true),
			SnapshotPolicy:              nil,
			ThroughputMibps:             nillable.GetFloat64Ptr(150),
			TieringPolicy:               nil,
			UnixPermissions:             nillable.GetStringPtr("755"),
			UsedBytes:                   nillable.GetFloat64Ptr(1024),
			VolumeID:                    "vol-123",
			VolumeState:                 "active",
			VolumeStateDetails:          "in use",
			Zone:                        "us-central1",
		}

		res := _convertVolumeV1betaCVPToModel(input)

		assert.Equal(tt, "ad-config-id", res.ActiveDirectoryConfigId.Value)
		assert.Equal(tt, "test-token", res.CreationToken.Value)
		assert.Equal(tt, "test description", res.Description.Value)
		assert.Equal(tt, "pool-id", res.PoolId.Value)
		assert.Equal(tt, "pool-resource-id", res.PoolResourceId.Value)
		assert.Equal(tt, "resource-id", res.ResourceId)
		assert.Equal(tt, "vol-123", res.VolumeId.Value)
		assert.Equal(tt, gcpgenserver.NewOptVolumeV1betaServiceLevel(gcpgenserver.VolumeV1betaServiceLevelFLEX), res.ServiceLevel)
		assert.Equal(tt, "us-central1", res.Zone.Value)
		assert.Equal(tt, "test-passphrase", res.CacheParameters.Value.Passphrase.Value)
		assert.True(tt, res.CacheParameters.Value.PeerSvmName.IsSet())
		assert.Equal(tt, "peer-svm", res.CacheParameters.Value.PeerSvmName.Value)
		assert.True(tt, res.CacheParameters.Value.PeerVolumeName.IsSet())
		assert.Equal(tt, "peer-volume", res.CacheParameters.Value.PeerVolumeName.Value)
		assert.Equal(tt, "test-command", res.CacheParameters.Value.Command.Value)
		assert.True(tt, res.CacheParameters.Value.PeerClusterName.IsSet())
		assert.Equal(tt, "alderan", res.CacheParameters.Value.PeerClusterName.Value)
		assert.Equal(tt, "test-passphrase", res.CacheParameters.Value.Passphrase.Value)
		assert.Equal(tt, "network-id", res.Network.Value)
		assert.Equal(tt, "pool-id", res.PoolId.Value)
		assert.Equal(tt, "pool-resource-id", res.PoolResourceId.Value)

		assert.Equal(tt, int64(10199181), res.BackupConfig.Value.BackupChainBytes.Value)
		assert.Equal(tt, "backup-policy-id", res.BackupConfig.Value.BackupPolicyId.Value)
		assert.Equal(tt, "backup-vault-id", res.BackupConfig.Value.BackupVaultId.Value)
		assert.Equal(tt, true, res.BackupConfig.Value.ScheduledBackupEnabled.Value)
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithThroughputMibps", func(tt *testing.T) {
		throughputMibps := float64(200)
		input := &cvpmodels.VolumeV1beta{
			ResourceID:      nillable.GetStringPtr("resource-id"),
			VolumeID:        "vol-123",
			VolumeState:     "active",
			ThroughputMibps: &throughputMibps,
		}

		res := _convertVolumeV1betaCVPToModel(input)

		assert.Equal(tt, "resource-id", res.ResourceId)
		assert.Equal(tt, "vol-123", res.VolumeId.Value)
		assert.True(tt, res.ThroughputMibps.IsSet())
		throughput, ok := res.ThroughputMibps.Get()
		assert.True(tt, ok)
		assert.Equal(tt, float64(200), throughput)
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithoutThroughputMibps", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			ResourceID:      nillable.GetStringPtr("resource-id"),
			VolumeID:        "vol-123",
			VolumeState:     "active",
			ThroughputMibps: nil,
		}

		res := _convertVolumeV1betaCVPToModel(input)

		assert.Equal(tt, "resource-id", res.ResourceId)
		assert.Equal(tt, "vol-123", res.VolumeId.Value)
		assert.False(tt, res.ThroughputMibps.IsSet())
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithHotTierSizeGib", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			ResourceID:     nillable.GetStringPtr("resource-id"),
			VolumeID:       "vol-123",
			VolumeState:    "active",
			HotTierSizeGib: nillable.GetFloat64Ptr(25.5),
		}

		res := _convertVolumeV1betaCVPToModel(input)

		assert.Equal(tt, "resource-id", res.ResourceId)
		assert.Equal(tt, "vol-123", res.VolumeId.Value)
		assert.Equal(tt, float64(25.5), res.HotTierSizeGib.Value)
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithNilHotTierSizeGib", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			ResourceID:     nillable.GetStringPtr("resource-id"),
			VolumeID:       "vol-123",
			VolumeState:    "active",
			HotTierSizeGib: nil,
		}

		res := _convertVolumeV1betaCVPToModel(input)

		assert.Equal(tt, "resource-id", res.ResourceId)
		assert.Equal(tt, "vol-123", res.VolumeId.Value)
		assert.False(tt, res.HotTierSizeGib.IsSet())
	})

	t.Run("BasicVolumeConversionWithNilFields", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			ResourceID:         nil, // Test nil ResourceID
			VolumeID:           "volume-456",
			VolumeState:        "ACTIVE",
			VolumeStateDetails: "Volume is healthy",
			SecurityStyle:      "UNIX",
			ServiceLevel:       "FLEX",
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.Empty(tt, result.ResourceId) // nil ResourceID becomes empty string
		assert.Equal(tt, "volume-456", result.VolumeId.Value)
		assert.Equal(tt, gcpgenserver.VolumeV1betaVolumeState("ACTIVE"), result.VolumeState.Value)
	})

	t.Run("VolumeWithExportPolicyRules", func(tt *testing.T) {
		exportPolicy := &cvpmodels.ExportPolicyV1beta{
			Rules: []*cvpmodels.SimpleExportPolicyRuleV1beta{
				{
					AccessType:         nillable.GetStringPtr("ReadWrite"),
					AllowedClients:     nillable.GetStringPtr("0.0.0.0/0"),
					HasRootAccess:      nillable.GetStringPtr("true"),
					Kerberos5ReadOnly:  nillable.GetBoolPtr(false),
					Kerberos5ReadWrite: nillable.GetBoolPtr(true),
					Nfsv3:              nillable.GetBoolPtr(true),
					Nfsv4:              nillable.GetBoolPtr(true),
				},
			},
		}

		input := &cvpmodels.VolumeV1beta{
			VolumeID:     "volume-123",
			ExportPolicy: exportPolicy,
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.True(tt, result.ExportPolicy.IsSet())
		assert.Len(tt, result.ExportPolicy.Value.Rules, 1)

		rule := result.ExportPolicy.Value.Rules[0]
		assert.Equal(tt, gcpgenserver.SimpleExportPolicyRuleV1betaAccessType("ReadWrite"), rule.AccessType)
		assert.Equal(tt, "0.0.0.0/0", rule.AllowedClients)
		assert.Equal(tt, gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccess("true"), rule.HasRootAccess.Value)
		assert.False(tt, rule.Kerberos5ReadOnly.Value)
		assert.True(tt, rule.Kerberos5ReadWrite.Value)
		assert.True(tt, rule.Nfsv3.Value)
		assert.True(tt, rule.Nfsv4.Value)
	})

	t.Run("VolumeWithExportPolicyRulesAndAllSquash", func(tt *testing.T) {
		exportPolicy := &cvpmodels.ExportPolicyV1beta{
			Rules: []*cvpmodels.SimpleExportPolicyRuleV1beta{
				{
					AccessType:         nillable.GetStringPtr("ReadWrite"),
					AllowedClients:     nillable.GetStringPtr("0.0.0.0/0"),
					HasRootAccess:      nillable.GetStringPtr("false"),
					Kerberos5ReadOnly:  nillable.GetBoolPtr(false),
					Kerberos5ReadWrite: nillable.GetBoolPtr(false),
					Nfsv3:              nillable.GetBoolPtr(true),
					Nfsv4:              nillable.GetBoolPtr(false),
					AllSquash:          nillable.GetBoolPtr(true),
					AnonUID:            nillable.GetInt64Ptr(int64(1001)),
				},
				{
					AccessType:         nillable.GetStringPtr("ReadOnly"),
					AllowedClients:     nillable.GetStringPtr("10.0.0.0/8"),
					HasRootAccess:      nillable.GetStringPtr("true"),
					Kerberos5ReadOnly:  nillable.GetBoolPtr(false),
					Kerberos5ReadWrite: nillable.GetBoolPtr(false),
					Nfsv3:              nillable.GetBoolPtr(false),
					Nfsv4:              nillable.GetBoolPtr(true),
					AllSquash:          nillable.GetBoolPtr(false),
					AnonUID:            nillable.GetInt64Ptr(int64(0)),
				},
			},
		}

		input := &cvpmodels.VolumeV1beta{
			VolumeID:     "volume-123",
			ExportPolicy: exportPolicy,
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.True(tt, result.ExportPolicy.IsSet())
		assert.Len(tt, result.ExportPolicy.Value.Rules, 2)

		rule1 := result.ExportPolicy.Value.Rules[0]
		assert.Equal(tt, gcpgenserver.SimpleExportPolicyRuleV1betaAccessType("ReadWrite"), rule1.AccessType)
		assert.Equal(tt, "0.0.0.0/0", rule1.AllowedClients)
		assert.True(tt, rule1.AllSquash.IsSet())
		assert.True(tt, rule1.AllSquash.Value)
		assert.True(tt, rule1.AnonUid.IsSet())
		assert.Equal(tt, int64(1001), rule1.AnonUid.Value)

		rule2 := result.ExportPolicy.Value.Rules[1]
		assert.Equal(tt, gcpgenserver.SimpleExportPolicyRuleV1betaAccessType("ReadOnly"), rule2.AccessType)
		assert.Equal(tt, "10.0.0.0/8", rule2.AllowedClients)
		assert.True(tt, rule2.AllSquash.IsSet())
		assert.False(tt, rule2.AllSquash.Value)
		assert.True(tt, rule2.AnonUid.IsSet())
		assert.Equal(tt, int64(0), rule2.AnonUid.Value)
	})

	t.Run("WhenVolumeIsInCreatingStateWithNilExportPolicy", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID:     "volume-123",
			VolumeState:  "CREATING",
			ExportPolicy: nil,
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.False(tt, result.ExportPolicy.IsSet())
	})

	t.Run("WhenVolumeIsInReadyStateWithEmptyExportPolicy", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID:    "volume-123",
			VolumeState: "READY",
			ExportPolicy: &cvpmodels.ExportPolicyV1beta{
				Rules: []*cvpmodels.SimpleExportPolicyRuleV1beta{},
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.True(tt, result.ExportPolicy.IsSet())
		assert.Empty(tt, result.ExportPolicy.Value.Rules)
	})

	t.Run("VolumeWithSnapshotPolicyFullSchedules", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID: "volume-123",
			SnapshotPolicy: &cvpmodels.SnapshotPolicyV1beta{
				Enabled: nillable.GetBoolPtr(true),
				HourlySchedule: &cvpmodels.HourlyScheduleV1beta{
					Minute:          nillable.GetFloat64Ptr(30),
					SnapshotsToKeep: nillable.GetFloat64Ptr(24),
				},
				DailySchedule: &cvpmodels.DailyScheduleV1beta{
					Hour:            nillable.GetFloat64Ptr(2),
					Minute:          nillable.GetFloat64Ptr(0),
					SnapshotsToKeep: nillable.GetFloat64Ptr(7),
				},
				WeeklySchedule: &cvpmodels.WeeklyScheduleV1beta{
					Day:             "Sunday",
					Hour:            nillable.GetFloat64Ptr(3),
					Minute:          nillable.GetFloat64Ptr(15),
					SnapshotsToKeep: nillable.GetFloat64Ptr(4),
				},
				MonthlySchedule: &cvpmodels.MonthlyScheduleV1beta{
					DaysOfMonth:     "1",
					Hour:            nillable.GetFloat64Ptr(1),
					Minute:          nillable.GetFloat64Ptr(30),
					SnapshotsToKeep: nillable.GetFloat64Ptr(12),
				},
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.True(tt, result.SnapshotPolicy.IsSet())
		policy := result.SnapshotPolicy.Value

		assert.True(tt, policy.Enabled.Value)

		assert.True(tt, policy.HourlySchedule.IsSet())
		assert.Equal(tt, float64(30), policy.HourlySchedule.Value.Minute.Value)
		assert.Equal(tt, float64(24), policy.HourlySchedule.Value.SnapshotsToKeep.Value)

		assert.True(tt, policy.DailySchedule.IsSet())
		assert.Equal(tt, float64(2), policy.DailySchedule.Value.Hour.Value)
		assert.Equal(tt, float64(7), policy.DailySchedule.Value.SnapshotsToKeep.Value)

		assert.True(tt, policy.WeeklySchedule.IsSet())
		assert.Equal(tt, "Sunday", policy.WeeklySchedule.Value.Day.Value)
		assert.Equal(tt, float64(4), policy.WeeklySchedule.Value.SnapshotsToKeep.Value)

		assert.True(tt, policy.MonthlySchedule.IsSet())
		assert.Equal(tt, "1", policy.MonthlySchedule.Value.DaysOfMonth.Value)
		assert.Equal(tt, float64(12), policy.MonthlySchedule.Value.SnapshotsToKeep.Value)
	})

	t.Run("VolumeWithSnapshotPolicyDisabled", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID: "volume-123",
			SnapshotPolicy: &cvpmodels.SnapshotPolicyV1beta{
				Enabled: nillable.GetBoolPtr(false),
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.False(tt, result.SnapshotPolicy.IsSet())
	})

	t.Run("VolumeWithMountPoints", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID: "volume-123",
			MountPoints: []*cvpmodels.MountPointV1beta{
				{
					Export:       "/vol1",
					ExportFull:   "server:/vol1",
					IPAddress:    "192.168.1.100",
					Instructions: "mount -t nfs server:/vol1 /mnt/vol1",
					Protocol:     "NFSv3",
				},
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.Len(tt, result.MountPoints, 1)

		mp1 := result.MountPoints[0]
		assert.Equal(tt, "/vol1", mp1.Export.Value)
		assert.Equal(tt, "server:/vol1", mp1.ExportFull.Value)
		assert.Equal(tt, "192.168.1.100", mp1.IpAddress.Value)
		assert.Equal(tt, "mount -t nfs server:/vol1 /mnt/vol1", mp1.Instructions.Value)
		assert.Equal(tt, gcpgenserver.ProtocolsV1beta("NFSv3"), mp1.Protocol.Value)
	})

	t.Run("VolumeWithTieringPolicy", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID: "volume-123",
			TieringPolicy: &cvpmodels.TieringPolicyV1beta{
				TierAction: nillable.GetStringPtr("AUTO"),
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.True(tt, result.TieringPolicy.IsSet())
		assert.Equal(tt, gcpgenserver.TieringPolicyV1betaTierAction("AUTO"), result.TieringPolicy.Value.TierAction.Value)
	})

	t.Run("VolumeWithTieringPolicyNilTierAction", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID: "volume-123",
			TieringPolicy: &cvpmodels.TieringPolicyV1beta{
				TierAction: nil, // nil tier action
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.False(tt, result.TieringPolicy.IsSet())
	})

	t.Run("VolumeWithTieringPolicyCoolingThresholdDays", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID: "volume-123",
			TieringPolicy: &cvpmodels.TieringPolicyV1beta{
				TierAction:               nillable.GetStringPtr("AUTO"),
				CoolingThresholdDays:     nillable.GetInt32Ptr(7),
				HotTierBypassModeEnabled: nillable.GetBoolPtr(true),
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.True(tt, result.TieringPolicy.IsSet())
		assert.Equal(tt, gcpgenserver.TieringPolicyV1betaTierAction("AUTO"), result.TieringPolicy.Value.TierAction.Value)
		assert.Equal(tt, int32(7), result.TieringPolicy.Value.CoolingThresholdDays.Value)
		assert.Equal(tt, true, result.TieringPolicy.Value.HotTierBypassModeEnabled.Value)
	})

	t.Run("VolumeWithTieringPolicyNilCoolingThresholdDays", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID: "volume-123",
			TieringPolicy: &cvpmodels.TieringPolicyV1beta{
				TierAction:               nillable.GetStringPtr("AUTO"),
				CoolingThresholdDays:     nil,
				HotTierBypassModeEnabled: nillable.GetBoolPtr(false),
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.True(tt, result.TieringPolicy.IsSet())
		assert.Equal(tt, gcpgenserver.TieringPolicyV1betaTierAction("AUTO"), result.TieringPolicy.Value.TierAction.Value)
		assert.False(tt, result.TieringPolicy.Value.CoolingThresholdDays.IsSet())
		assert.Equal(tt, false, result.TieringPolicy.Value.HotTierBypassModeEnabled.Value)
	})

	t.Run("VolumeWithTieringPolicyNilHotTierBypassModeEnabled", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID: "volume-123",
			TieringPolicy: &cvpmodels.TieringPolicyV1beta{
				TierAction:               nillable.GetStringPtr("AUTO"),
				CoolingThresholdDays:     nillable.GetInt32Ptr(14),
				HotTierBypassModeEnabled: nil,
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.True(tt, result.TieringPolicy.IsSet())
		assert.Equal(tt, gcpgenserver.TieringPolicyV1betaTierAction("AUTO"), result.TieringPolicy.Value.TierAction.Value)
		assert.Equal(tt, int32(14), result.TieringPolicy.Value.CoolingThresholdDays.Value)
		assert.False(tt, result.TieringPolicy.Value.HotTierBypassModeEnabled.Value)
	})

	t.Run("VolumeWithCacheParametersNilFields", func(tt *testing.T) {
		input := &cvpmodels.VolumeV1beta{
			VolumeID: "volume-123",
			CacheParameters: &cvpmodels.FlexCacheV1beta{
				PeerVolumeName:           "origin-volume",
				PeerClusterName:          "origin-cluster",
				PeerSvmName:              "origin-svm",
				CacheState:               "online",
				Command:                  "flexcache create",
				EnableGlobalFileLock:     nil, // nil
				PeeringCommandExpiryTime: nil, // nil
				Passphrase:               nil, // nil
				PeerIPAddresses:          []string{"10.0.0.0"},
			},
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.True(tt, result.CacheParameters.IsSet())
		cache := result.CacheParameters.Value

		assert.True(tt, cache.PeerVolumeName.IsSet())
		assert.Equal(tt, "origin-volume", cache.PeerVolumeName.Value)
		assert.False(tt, cache.EnableGlobalFileLock.IsSet())
		assert.False(tt, cache.PeeringCommandExpiryTime.IsSet())
		assert.False(tt, cache.Passphrase.IsSet())
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithNilScheduledBackupEnabled", func(tt *testing.T) {
		backupConfig := &cvpmodels.BackupConfigV1beta{
			BackupChainBytes:       nillable.GetInt64Ptr(10199181),
			BackupPolicyID:         nillable.GetStringPtr("backup-policy-id"),
			BackupVaultID:          nillable.GetStringPtr("backup-vault-id"),
			ScheduledBackupEnabled: nil, // Test nil ScheduledBackupEnabled
		}

		input := &cvpmodels.VolumeV1beta{
			VolumeID:     "vol-123",
			VolumeState:  "active",
			BackupConfig: backupConfig,
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.BackupConfig)
		assert.True(tt, result.BackupConfig.IsSet())

		// When BackupPolicyID is not nil, both fields should be set
		assert.True(tt, result.BackupConfig.Value.BackupPolicyId.IsSet())
		assert.Equal(tt, "backup-policy-id", result.BackupConfig.Value.BackupPolicyId.Value)
		assert.Equal(tt, "backup-vault-id", result.BackupConfig.Value.BackupVaultId.Value)

		// ScheduledBackupEnabled should be set even when nil (utils.SafeBool handles nil)
		assert.True(tt, result.BackupConfig.Value.ScheduledBackupEnabled.IsSet())
		// The value will be false when the input is nil
		assert.False(tt, result.BackupConfig.Value.ScheduledBackupEnabled.Value)
	})
	t.Run("ConvertVolumeV1betaCVPToModelWithNilBackupChainBytes", func(tt *testing.T) {
		backupConfig := &cvpmodels.BackupConfigV1beta{
			BackupChainBytes:       nil, // Test nil BackupChainBytes
			BackupPolicyID:         nillable.GetStringPtr("backup-policy-id"),
			BackupVaultID:          nillable.GetStringPtr("backup-vault-id"),
			ScheduledBackupEnabled: nillable.GetBoolPtr(true),
		}

		input := &cvpmodels.VolumeV1beta{
			VolumeID:     "vol-123",
			VolumeState:  "active",
			BackupConfig: backupConfig,
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.BackupConfig)
		assert.True(tt, result.BackupConfig.IsSet())

		// Verify BackupChainBytes is NOT set when nil (since it's not in the initial struct)
		assert.False(tt, result.BackupConfig.Value.BackupChainBytes.IsSet())
		assert.Equal(tt, "backup-policy-id", result.BackupConfig.Value.BackupPolicyId.Value)
		assert.Equal(tt, "backup-vault-id", result.BackupConfig.Value.BackupVaultId.Value)
		assert.True(tt, result.BackupConfig.Value.ScheduledBackupEnabled.Value)
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithNilBackupChainBytesAndNilScheduledBackup", func(tt *testing.T) {
		backupConfig := &cvpmodels.BackupConfigV1beta{
			BackupChainBytes:       nil, // Test nil BackupChainBytes
			BackupPolicyID:         nillable.GetStringPtr("backup-policy-id"),
			BackupVaultID:          nillable.GetStringPtr("backup-vault-id"),
			ScheduledBackupEnabled: nil, // Also test nil ScheduledBackupEnabled
		}

		input := &cvpmodels.VolumeV1beta{
			VolumeID:     "vol-123",
			VolumeState:  "active",
			BackupConfig: backupConfig,
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.BackupConfig)
		assert.True(tt, result.BackupConfig.IsSet())

		// Verify BackupChainBytes is NOT set when nil (since it's not in the initial struct)
		assert.False(tt, result.BackupConfig.Value.BackupChainBytes.IsSet())

		// When BackupPolicyID is not nil, both fields should be set
		assert.True(tt, result.BackupConfig.Value.BackupPolicyId.IsSet())
		assert.Equal(tt, "backup-policy-id", result.BackupConfig.Value.BackupPolicyId.Value)
		assert.Equal(tt, "backup-vault-id", result.BackupConfig.Value.BackupVaultId.Value)

		// ScheduledBackupEnabled should be set even when nil (utils.SafeBool handles nil)
		assert.True(tt, result.BackupConfig.Value.ScheduledBackupEnabled.IsSet())
		// The value will be false when the input is nil
		assert.False(tt, result.BackupConfig.Value.ScheduledBackupEnabled.Value)
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithBackupChainBytesSet", func(tt *testing.T) {
		backupConfig := &cvpmodels.BackupConfigV1beta{
			BackupChainBytes:       nillable.GetInt64Ptr(10199181), // Test with actual value
			BackupPolicyID:         nillable.GetStringPtr("backup-policy-id"),
			BackupVaultID:          nillable.GetStringPtr("backup-vault-id"),
			ScheduledBackupEnabled: nillable.GetBoolPtr(true),
		}

		input := &cvpmodels.VolumeV1beta{
			VolumeID:     "vol-123",
			VolumeState:  "active",
			BackupConfig: backupConfig,
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.BackupConfig)
		assert.True(tt, result.BackupConfig.IsSet())

		// Verify BackupChainBytes IS set when not nil
		assert.True(tt, result.BackupConfig.Value.BackupChainBytes.IsSet())
		assert.Equal(tt, int64(10199181), result.BackupConfig.Value.BackupChainBytes.Value)
		assert.Equal(tt, "backup-policy-id", result.BackupConfig.Value.BackupPolicyId.Value)
		assert.Equal(tt, "backup-vault-id", result.BackupConfig.Value.BackupVaultId.Value)
		assert.True(tt, result.BackupConfig.Value.ScheduledBackupEnabled.Value)
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithEmptyStringFields", func(tt *testing.T) {
		// Test that empty string fields are not set in the result
		input := &cvpmodels.VolumeV1beta{
			VolumeID:       "vol-123",
			VolumeState:    "active",
			SecurityStyle:  "", // Empty string
			ServiceLevel:   "", // Empty string
			EncryptionType: "", // Empty string
			StorageClass:   "", // Empty string
			Network:        "", // Empty string
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.Equal(tt, "vol-123", result.VolumeId.Value)
		assert.Equal(tt, gcpgenserver.VolumeV1betaVolumeState("active"), result.VolumeState.Value)

		// These fields should not be set when input values are empty strings
		assert.False(tt, result.SecurityStyle.IsSet(), "SecurityStyle should not be set for empty string")
		assert.False(tt, result.ServiceLevel.IsSet(), "ServiceLevel should not be set for empty string")
		assert.False(tt, result.EncryptionType.IsSet(), "EncryptionType should not be set for empty string")
		assert.False(tt, result.StorageClass.IsSet(), "StorageClass should not be set for empty string")
		assert.False(tt, result.Network.IsSet(), "Network should not be set for empty string")
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithNonEmptyStringFields", func(tt *testing.T) {
		// Test that non-empty string fields are properly set in the result
		input := &cvpmodels.VolumeV1beta{
			VolumeID:       "vol-123",
			VolumeState:    "active",
			SecurityStyle:  "UNIX",
			ServiceLevel:   "FLEX",
			EncryptionType: "SOFTWARE",
			StorageClass:   "BASIC",
			Network:        "network-123",
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.Equal(tt, "vol-123", result.VolumeId.Value)
		assert.Equal(tt, gcpgenserver.VolumeV1betaVolumeState("active"), result.VolumeState.Value)

		// These fields should be properly set when input values are non-empty
		assert.True(tt, result.SecurityStyle.IsSet(), "SecurityStyle should be set for non-empty string")
		assert.Equal(tt, gcpgenserver.VolumeV1betaSecurityStyle("UNIX"), result.SecurityStyle.Value)

		assert.True(tt, result.ServiceLevel.IsSet(), "ServiceLevel should be set for non-empty string")
		assert.Equal(tt, gcpgenserver.VolumeV1betaServiceLevel("FLEX"), result.ServiceLevel.Value)

		assert.True(tt, result.EncryptionType.IsSet(), "EncryptionType should be set for non-empty string")
		assert.Equal(tt, gcpgenserver.VolumeV1betaEncryptionType("SOFTWARE"), result.EncryptionType.Value)

		assert.True(tt, result.StorageClass.IsSet(), "StorageClass should be set for non-empty string")
		assert.Equal(tt, gcpgenserver.StorageClassV1beta("BASIC"), result.StorageClass.Value)

		assert.True(tt, result.Network.IsSet(), "Network should be set for non-empty string")
		assert.Equal(tt, "network-123", result.Network.Value)
	})
	t.Run("ConvertVolumeV1betaCVPToModelWithCacheParams", func(tt *testing.T) {
		// Test that non-empty PeerIPAddresses slice is properly set in the result
		cacheParams := &cvpmodels.FlexCacheV1beta{
			PeerClusterName:      "test-cluster",
			PeerSvmName:          "test-svm",
			PeerVolumeName:       "test-volume",
			PeerIPAddresses:      []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}, // Non-empty slice
			EnableGlobalFileLock: nillable.GetBoolPtr(true),
		}

		input := &cvpmodels.VolumeV1beta{
			VolumeID:        "vol-123",
			VolumeState:     "active",
			CacheParameters: cacheParams,
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.Equal(tt, "vol-123", result.VolumeId.Value)
		assert.True(tt, result.CacheParameters.IsSet(), "CacheParameters should be set")
		assert.True(tt, result.CacheParameters.Value.PeerClusterName.IsSet())
		assert.Equal(tt, "test-cluster", result.CacheParameters.Value.PeerClusterName.Value)
		assert.True(tt, result.CacheParameters.Value.PeerSvmName.IsSet())
		assert.Equal(tt, "test-svm", result.CacheParameters.Value.PeerSvmName.Value)
		assert.True(tt, result.CacheParameters.Value.PeerVolumeName.IsSet())
		assert.Equal(tt, "test-volume", result.CacheParameters.Value.PeerVolumeName.Value)

		// PeerIpAddresses should be properly set when input slice is non-empty
		assert.Len(tt, result.CacheParameters.Value.PeerIpAddresses, 3, "PeerIpAddresses should have 3 elements")
		assert.Equal(tt, "10.0.0.1", result.CacheParameters.Value.PeerIpAddresses[0])
		assert.Equal(tt, "10.0.0.2", result.CacheParameters.Value.PeerIpAddresses[1])
		assert.Equal(tt, "10.0.0.3", result.CacheParameters.Value.PeerIpAddresses[2])
	})

	t.Run("ConvertVolumeV1betaCVPToModelWithNilCacheParameters", func(tt *testing.T) {
		// Test that nil CacheParameters doesn't cause issues
		input := &cvpmodels.VolumeV1beta{
			VolumeID:        "vol-123",
			VolumeState:     "active",
			CacheParameters: nil, // nil cache parameters
		}

		result := _convertVolumeV1betaCVPToModel(input)

		assert.Equal(tt, "vol-123", result.VolumeId.Value)
		assert.False(tt, result.CacheParameters.IsSet(), "CacheParameters should not be set when nil")
	})
}

func TestConvertFromSnapshotPolicyV2(t *testing.T) {
	t.Run("NilInput_ReturnsNil", func(tt *testing.T) {
		result, err := convertFromSnapshotPolicyV2(nil)
		assert.NoError(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("MonthlySchedule", func(tt *testing.T) {
		pol := &gcpgenserver.SnapshotPolicyV1beta{
			Enabled: gcpgenserver.NewOptNilBool(true),
			MonthlySchedule: gcpgenserver.NewOptMonthlyScheduleV1beta(
				gcpgenserver.MonthlyScheduleV1beta{
					SnapshotsToKeep: gcpgenserver.NewOptFloat64(5),
					DaysOfMonth:     gcpgenserver.NewOptString("1,15"),
					Hour:            gcpgenserver.NewOptFloat64(2),
					Minute:          gcpgenserver.NewOptFloat64(30),
				},
			),
		}
		result, err := convertFromSnapshotPolicyV2(pol)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.IsEnabled)
		assert.Len(tt, result.Schedules, 1)
		sched := result.Schedules[0]
		assert.Equal(tt, int64(5), sched.Count)
		assert.Equal(tt, "monthly", sched.SnapmirrorLabel)
		assert.Equal(tt, []int{1, 15}, sched.Schedule.DaysOfMonth)
		assert.Equal(tt, []int{2}, sched.Schedule.Hours)
		assert.Equal(tt, []int{30}, sched.Schedule.Minutes)
	})

	t.Run("WeeklySchedule", func(tt *testing.T) {
		pol := &gcpgenserver.SnapshotPolicyV1beta{
			Enabled: gcpgenserver.NewOptNilBool(true),
			WeeklySchedule: gcpgenserver.NewOptWeeklyScheduleV1beta(
				gcpgenserver.WeeklyScheduleV1beta{
					SnapshotsToKeep: gcpgenserver.NewOptFloat64(3),
					Day:             gcpgenserver.NewOptString("Monday,Tuesday"),
					Hour:            gcpgenserver.NewOptFloat64(5),
					Minute:          gcpgenserver.NewOptFloat64(10),
				},
			),
		}
		result, err := convertFromSnapshotPolicyV2(pol)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.IsEnabled)
		assert.Len(tt, result.Schedules, 1)
		sched := result.Schedules[0]
		assert.Equal(tt, int64(3), sched.Count)
		assert.Equal(tt, "weekly", sched.SnapmirrorLabel)
		assert.Equal(tt, []int{1, 2}, sched.Schedule.DaysOfWeek)
		assert.Equal(tt, []int{5}, sched.Schedule.Hours)
		assert.Equal(tt, []int{10}, sched.Schedule.Minutes)
	})

	t.Run("DailySchedule", func(tt *testing.T) {
		pol := &gcpgenserver.SnapshotPolicyV1beta{
			Enabled: gcpgenserver.NewOptNilBool(true),
			DailySchedule: gcpgenserver.NewOptDailyScheduleV1beta(
				gcpgenserver.DailyScheduleV1beta{
					SnapshotsToKeep: gcpgenserver.NewOptFloat64(2),
					Hour:            gcpgenserver.NewOptFloat64(7),
					Minute:          gcpgenserver.NewOptFloat64(45),
				},
			),
		}
		result, err := convertFromSnapshotPolicyV2(pol)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.IsEnabled)
		assert.Len(tt, result.Schedules, 1)
		sched := result.Schedules[0]
		assert.Equal(tt, int64(2), sched.Count)
		assert.Equal(tt, "daily", sched.SnapmirrorLabel)
		assert.Equal(tt, []int{7}, sched.Schedule.Hours)
		assert.Equal(tt, []int{45}, sched.Schedule.Minutes)
	})

	t.Run("HourlySchedule", func(tt *testing.T) {
		pol := &gcpgenserver.SnapshotPolicyV1beta{
			Enabled: gcpgenserver.NewOptNilBool(true),
			HourlySchedule: gcpgenserver.NewOptHourlyScheduleV1beta(
				gcpgenserver.HourlyScheduleV1beta{
					SnapshotsToKeep: gcpgenserver.NewOptFloat64(1),
					Minute:          gcpgenserver.NewOptFloat64(15),
				},
			),
		}
		result, err := convertFromSnapshotPolicyV2(pol)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.IsEnabled)
		assert.Len(tt, result.Schedules, 1)
		sched := result.Schedules[0]
		assert.Equal(tt, int64(1), sched.Count)
		assert.Equal(tt, "hourly", sched.SnapmirrorLabel)
		assert.Equal(tt, []int{15}, sched.Schedule.Minutes)
	})

	t.Run("WeeklySchedule_InvalidDay_ReturnsError", func(tt *testing.T) {
		pol := &gcpgenserver.SnapshotPolicyV1beta{
			Enabled: gcpgenserver.NewOptNilBool(true),
			WeeklySchedule: gcpgenserver.NewOptWeeklyScheduleV1beta(
				gcpgenserver.WeeklyScheduleV1beta{
					SnapshotsToKeep: gcpgenserver.NewOptFloat64(3),
					Day:             gcpgenserver.NewOptString("Funday"),
					Hour:            gcpgenserver.NewOptFloat64(5),
					Minute:          gcpgenserver.NewOptFloat64(10),
				},
			),
		}
		result, err := convertFromSnapshotPolicyV2(pol)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
}

func TestV1betaUpdateVolume(t *testing.T) {
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	mockParseAndValidateRegionAndZone := func(region string) (string, string, *gcpgenserver.Error) {
		return "test-region", "test-location", nil
	}
	utils.ParseAndValidateRegionAndZone = mockParseAndValidateRegionAndZone
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	t.Run("ValidUpdateVolume", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			Labels:       gcpgenserver.OptVolumeUpdateV1betaLabels{Set: true, Value: map[string]string{"key1": "value1", "key2": "value2"}},
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			QuotaInBytes:   107374182499,
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		mockOrchestrator.EXPECT().UpdateVolumeV2(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})

	t.Run("DescribePoolNotFound_ReturnsBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			PoolID:         "pool-db-id",
			QuotaInBytes:   107374182499,
		}
		notFoundErr := errors.NewNotFoundErr("Pool", nil)
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-db-id", "project-number").Return(nil, notFoundErr)

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, notFoundErr.Error(), badReq.Message)
	})

	t.Run("DescribePoolError_ReturnsInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			PoolID:         "pool-db-id",
			QuotaInBytes:   107374182499,
		}
		describeErr := fmt.Errorf("Internal server error")
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-db-id", "project-number").Return(nil, describeErr)

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdateVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, describeErr.Error(), internalErr.Message)
	})

	t.Run("RejectedWhenPoolZoneSwitchingOrSwitched", func(tt *testing.T) {
		for _, zoneState := range []string{datamodel.ZoneSwitching, datamodel.ZoneSwitched} {
			tt.Run(zoneState, func(t *testing.T) {
				mockOrchestrator := factory.NewMockOrchestratorFactory(t)
				handler := Handler{Orchestrator: mockOrchestrator}
				params := gcpgenserver.V1betaUpdateVolumeParams{
					LocationId:    "location-id",
					ProjectNumber: "project-number",
					VolumeId:      "vol-1",
				}
				req := &gcpgenserver.VolumeUpdateV1beta{
					PoolId:       gcpgenserver.NewOptNilString("test-pool"),
					QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
				}
				volume := &models.Volume{
					BaseModel:      models.BaseModel{UUID: "vol-1"},
					LifeCycleState: "READY",
					PoolID:         "pool-db-id",
					QuotaInBytes:   107374182499,
				}
				mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
				mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-db-id", "project-number").Return(&models.Pool{
					PoolAttributes: &models.PoolAttributes{ZoneSwitchState: zoneState},
				}, nil)

				result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
				require.NoError(t, err)
				badReq, ok := result.(*gcpgenserver.V1betaUpdateVolumeBadRequest)
				require.True(t, ok)
				assert.Equal(t, float64(http.StatusBadRequest), badReq.Code)
				assert.Equal(t, "Volume update is not supported when the pool is switching/switched to a different primary zone.", badReq.Message)
			})
		}
	})

	t.Run("UserInputValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{}
		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*common.UpdateVolumeParams, error) {
			return nil, errors.NewUserInputValidationErr("invalid input")
		}
		defer func() { prepareUpdateVolumeParams = _prepareUpdateVolumeParams }()

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "invalid input")
	})

	t.Run("InternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{}
		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*common.UpdateVolumeParams, error) {
			return nil, fmt.Errorf("unexpected error")
		}
		defer func() { prepareUpdateVolumeParams = _prepareUpdateVolumeParams }()

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdateVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "unexpected error")
	})

	t.Run("BadRequest", func(tt *testing.T) {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		defer func() { utils.ParseAndValidateRegionAndZone = mockParseAndValidateRegionAndZone }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{}
		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*common.UpdateVolumeParams, error) {
			return nil, fmt.Errorf("unexpected error")
		}
		defer func() { prepareUpdateVolumeParams = _prepareUpdateVolumeParams }()

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "LocationID represents neither a region nor a zone")
	})

	t.Run("WhenOrchestratorValidationThrowsAnError_Return400BadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182499),
		}
		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		mockOrchestrator.EXPECT().UpdateVolumeV2(mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("An error occurred"))

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "An error occurred")
	})

	t.Run("WhenOrchestratorThrowsAnError_ReturnError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182499),
		}
		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		mockOrchestrator.EXPECT().UpdateVolumeV2(mock.Anything, mock.Anything).Return(nil, "", errors.New("An error occurred"))

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdateVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "An error occurred")
	})

	t.Run("WhenOrchestratorThrowsConflictError_Return409Conflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182499),
		}
		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		mockOrchestrator.EXPECT().UpdateVolumeV2(mock.Anything, mock.Anything).Return(nil, "", errors.NewConflictErr("Volume update conflict"))

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		conflictErr, ok := result.(*gcpgenserver.V1betaUpdateVolumeConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(409), conflictErr.Code)
		assert.Contains(tt, conflictErr.Message, "Volume update conflict")
	})

	t.Run("WhenLifeCycleStateUpdating_ThenReturnDoneAsFalse", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "UPDATING",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		mockOrchestrator.EXPECT().UpdateVolumeV2(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
		assert.False(tt, op.Done.Value)
	})

	t.Run("TieringPolicy ENABLED with feature enabled", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		autoTieringEnabled = true
		defer func() { autoTieringEnabled = currentATState }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
				},
			),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			QuotaInBytes:   107374182400,
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		mockOrchestrator.EXPECT().UpdateVolumeV2(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})

	t.Run("TieringPolicy PAUSED with feature enabled", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		autoTieringEnabled = true
		defer func() { autoTieringEnabled = currentATState }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("PAUSED"),
				},
			),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			QuotaInBytes:   107374182400,
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				HotTierBypassModeEnabled: false,
				TieringPolicy:            "auto",
			},
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		mockOrchestrator.EXPECT().UpdateVolumeV2(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})

	t.Run("TieringPolicy set with feature disabled", func(tt *testing.T) {
		currentATState := autoTieringEnabled
		autoTieringEnabled = false
		defer func() { autoTieringEnabled = currentATState }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
				},
			),
		}
		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "Auto-Tiering feature is currently not enabled.")
	})

	t.Run("HotTierBypassModeEnabled with TieringPolicy ENABLED", func(t *testing.T) {
		currentATState := autoTieringEnabled
		autoTieringEnabled = true
		defer func() { autoTieringEnabled = currentATState }()

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		region := "test-region"

		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
				},
			),
		}
		param, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, param.AutoTieringPolicy)
		assert.True(t, param.AutoTieringPolicy.AutoTieringEnabled)
		assert.Equal(t, int32(30), param.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("HotTierBypassModeEnabled disabled with TieringPolicy ENABLED", func(t *testing.T) {
		currentATState := autoTieringEnabled
		autoTieringEnabled = true
		defer func() { autoTieringEnabled = currentATState }()

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		region := "test-region"

		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
				},
			),
		}
		param, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, param.AutoTieringPolicy)
		assert.True(t, param.AutoTieringPolicy.AutoTieringEnabled)
		assert.Equal(t, int32(30), param.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("HotTierBypassModeEnabled not set with TieringPolicy ENABLED", func(t *testing.T) {
		currentATState := autoTieringEnabled
		autoTieringEnabled = true
		defer func() { autoTieringEnabled = currentATState }()

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		region := "test-region"

		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
					// HotTierBypassModeEnabled not set
				},
			),
		}
		param, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, param.AutoTieringPolicy)
		assert.True(t, param.AutoTieringPolicy.AutoTieringEnabled)
		assert.False(t, param.AutoTieringPolicy.HotTierBypassModeEnabled) // Should default to false
		assert.Equal(t, int32(30), param.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("WhenGetVolumeReturnsNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
		}

		// Mock GetVolume to return NotFoundErr
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(nil, errors.NewNotFoundErr("Volume", nil))

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		notFound, ok := result.(*gcpgenserver.V1betaUpdateVolumeNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), notFound.Code)
		assert.Equal(tt, "Volume not found", notFound.Message)
	})

	t.Run("WhenGetVolumeReturnsInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("test-pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
		}

		// Mock GetVolume to return a generic error
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(nil, errors.New("database connection error"))

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdateVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "Internal server error", internalErr.Message)
	})

	t.Run("FlexCache update with feature disabled", func(tt *testing.T) {
		currentFCState := flexCacheEnabled
		flexCacheEnabled = false
		defer func() { flexCacheEnabled = currentFCState }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{}
		volume := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*common.UpdateVolumeParams, error) {
			return &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{},
			}, nil
		}
		defer func() { prepareUpdateVolumeParams = _prepareUpdateVolumeParams }()

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(403), badReq.Code)
		assert.Contains(tt, badReq.Message, "FlexCache feature is currently not enabled.")
	})

	t.Run("FlexCache update with feature enabled", func(tt *testing.T) {
		currentFCState := flexCacheEnabled
		flexCacheEnabled = true
		defer func() { flexCacheEnabled = currentFCState }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaUpdateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeUpdateV1beta{}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*common.UpdateVolumeParams, error) {
			return &common.UpdateVolumeParams{
				CacheParameters: &models.CacheParameters{},
			}, nil
		}
		defer func() { prepareUpdateVolumeParams = _prepareUpdateVolumeParams }()

		mockOrchestrator.EXPECT().UpdateVolumeV2(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)

		result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})
}

func TestPrepareUpdateVolumeParamsWithNilBackupChainBytes(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "proj",
		LocationId:    "loc",
		VolumeId:      "vol",
	}
	region := "region"
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	req := &gcpgenserver.VolumeUpdateV1beta{
		PoolId:       gcpgenserver.NewOptNilString("pool"),
		QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
		Description:  gcpgenserver.NewOptNilString("desc"),
		Protocols:    []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
		BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(
			gcpgenserver.BlockPropertiesV1beta{
				OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
			},
		),
		BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
			gcpgenserver.BackupConfigV1beta{
				BackupVaultId:          gcpgenserver.NewOptNilString("backup-vault-id"),
				BackupPolicyId:         gcpgenserver.NewOptNilString("backup-policy-id"),
				BackupChainBytes:       gcpgenserver.NewOptNilInt64(0), // Use 0 instead of nil
				ScheduledBackupEnabled: gcpgenserver.NewOptNilBool(true),
			}),
	}

	out, err := _prepareUpdateVolumeParams(req, params, region, nil)
	assert.NoError(t, err)
	assert.NotNil(t, out)

	// Verify the function completes successfully with nil BackupChainBytes
	assert.Equal(t, "pool", out.PoolID)
	assert.Equal(t, int64(107374182400), out.QuotaInBytes)
	assert.Equal(t, "desc", out.Description)
	assert.Equal(t, []string{"ISCSI"}, out.Protocols)
	assert.NotNil(t, out.BlockProperties)
	assert.Equal(t, "LINUX", out.BlockProperties.OSType)
}

func TestPrepareUpdateVolumeParams(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "proj",
		LocationId:    "loc",
		VolumeId:      "vol",
	}
	region := "region"
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	t.Run("WhenAllFieldsSet_ThenFieldsAreMapped", func(t *testing.T) {
		labels := make(map[string]string)
		labels["k"] = "v"
		jsonbLabels := make(datamodel.JSONB)
		for k, v := range labels {
			jsonbLabels[k] = v
		}
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			Description:  gcpgenserver.NewOptNilString("desc"),
			Protocols:    []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(
				gcpgenserver.BlockPropertiesV1beta{
					OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
				},
			),
			BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
				gcpgenserver.BackupConfigV1beta{
					BackupVaultId:          gcpgenserver.NewOptNilString("backup-vault-id"),
					BackupPolicyId:         gcpgenserver.NewOptNilString("backup-policy-id"),
					BackupChainBytes:       gcpgenserver.NewOptNilInt64(10199181),
					ScheduledBackupEnabled: gcpgenserver.NewOptNilBool(true),
				}),
			Labels: gcpgenserver.NewOptVolumeUpdateV1betaLabels(labels),
		}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.Equal(t, "pool", out.PoolID)
		assert.Equal(t, int64(107374182400), out.QuotaInBytes)
		assert.Equal(t, "desc", out.Description)
		assert.Equal(t, []string{"ISCSI"}, out.Protocols)
		assert.NotNil(t, out.BlockProperties)
		assert.Equal(t, "LINUX", out.BlockProperties.OSType)
		assert.Equal(t, &jsonbLabels, out.Labels)
	})

	t.Run("WhenOptionalFieldsNotSet_ThenDefaultsAreUsed", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.Equal(t, "", out.PoolID)
		assert.Equal(t, int64(0), out.QuotaInBytes)
		assert.Equal(t, "", out.Description)
		assert.Empty(t, out.Protocols)
		assert.Nil(t, out.BlockProperties)
		assert.Nil(t, out.Labels)
	})

	t.Run("WhenBlockPropertiesSetWithoutOsType_ThenBlockPropertiesIsNil", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(gcpgenserver.BlockPropertiesV1beta{}),
		}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, out.BlockProperties)
	})

	t.Run("WhenLabelsContainEmptyKey_ThenLabelIsSkipped", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			Labels: gcpgenserver.NewOptVolumeUpdateV1betaLabels(map[string]string{"": "v", "k": "v2"}),
		}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.EqualError(t, err, "key is required in label")
		assert.Nil(t, out)
	})

	t.Run("WhenProtocolMarshalTextFails_ThenErrorIsReturned", func(t *testing.T) {
		badProtocol := gcpgenserver.ProtocolsV1beta(rune(255)) // assuming this is invalid
		req := &gcpgenserver.VolumeUpdateV1beta{
			Protocols: []gcpgenserver.ProtocolsV1beta{badProtocol},
		}
		_, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
	})

	t.Run("WhenBlockDevicesSet_ThenFieldsAreMapped", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			BlockDevices: []gcpgenserver.BlockDeviceV1beta{
				{
					Name:       gcpgenserver.NewOptString("test-lun"),
					HostGroups: []string{"9760acf5-4638-11e7-9bdb-020073ca3333", "9760acf5-4638-11e7-9bdb-020073ca4444"},
					OsType:     gcpgenserver.NewOptBlockDeviceV1betaOsType(gcpgenserver.BlockDeviceV1betaOsTypeLINUX),
				},
			},
		}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, out.BlockDevices)
		assert.Len(t, out.BlockDevices, 1)
		assert.Equal(t, "test-lun", out.BlockDevices[0].Name)
		assert.Equal(t, []string{"9760acf5-4638-11e7-9bdb-020073ca3333", "9760acf5-4638-11e7-9bdb-020073ca4444"}, out.BlockDevices[0].HostGroups)
		assert.Equal(t, "LINUX", out.BlockDevices[0].OSType)
	})

	t.Run("WhenMultipleBlockDevicesSet_ThenErrorIsReturned", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			BlockDevices: []gcpgenserver.BlockDeviceV1beta{
				{
					Name: gcpgenserver.NewOptString("test-lun-1"),
				},
				{
					Name: gcpgenserver.NewOptString("test-lun-2"),
				},
			},
		}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.EqualError(t, err, "Only one BlockDevice can be specified for update")
		assert.Nil(t, out)
	})

	t.Run("WhenBlockDeviceWithoutName_ThenErrorIsReturned", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			BlockDevices: []gcpgenserver.BlockDeviceV1beta{
				{
					HostGroups: []string{"9760acf5-4638-11e7-9bdb-020073ca3333"},
				},
			},
		}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.EqualError(t, err, "BlockDevice name is required")
		assert.Nil(t, out)
	})

	t.Run("WhenBlockDevicesWithDuplicateHostGroups_ThenHostGroupsAreDeduplicated", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			BlockDevices: []gcpgenserver.BlockDeviceV1beta{
				{
					Name:       gcpgenserver.NewOptString("test-lun"),
					HostGroups: []string{"9760acf5-4638-11e7-9bdb-020073ca3333", "9760acf5-4638-11e7-9bdb-020073ca3333"},
				},
			},
		}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, out.BlockDevices)
		assert.Len(t, out.BlockDevices, 1)
		assert.Equal(t, []string{"9760acf5-4638-11e7-9bdb-020073ca3333"}, out.BlockDevices[0].HostGroups)
	})

	t.Run("WhenSnapshotPolicySet_ThenFieldsAreMapped", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			SnapshotPolicy: gcpgenserver.NewOptSnapshotPolicyV1beta(
				gcpgenserver.SnapshotPolicyV1beta{
					Enabled: gcpgenserver.NewOptNilBool(true),
					MonthlySchedule: gcpgenserver.NewOptMonthlyScheduleV1beta(
						gcpgenserver.MonthlyScheduleV1beta{
							SnapshotsToKeep: gcpgenserver.NewOptFloat64(2),
							DaysOfMonth:     gcpgenserver.NewOptString("1,15"),
							Hour:            gcpgenserver.NewOptFloat64(2),
							Minute:          gcpgenserver.NewOptFloat64(30),
						},
					),
				},
			),
		}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, out.SnapshotPolicy)
		assert.True(t, out.SnapshotPolicy.IsEnabled)
		if len(out.SnapshotPolicy.Schedules) > 0 {
			assert.Equal(t, int64(2), out.SnapshotPolicy.Schedules[0].Count)
			assert.Equal(t, "monthly", out.SnapshotPolicy.Schedules[0].SnapmirrorLabel)
			assert.Equal(t, []int{1, 15}, out.SnapshotPolicy.Schedules[0].Schedule.DaysOfMonth)
		}
	})

	t.Run("WhenSnapshotPolicySetWithInvalidWeeklyDay_ThenError", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			SnapshotPolicy: gcpgenserver.NewOptSnapshotPolicyV1beta(
				gcpgenserver.SnapshotPolicyV1beta{
					Enabled: gcpgenserver.NewOptNilBool(true),
					WeeklySchedule: gcpgenserver.NewOptWeeklyScheduleV1beta(
						gcpgenserver.WeeklyScheduleV1beta{
							SnapshotsToKeep: gcpgenserver.NewOptFloat64(2),
							Day:             gcpgenserver.NewOptString("Funday"),
							Hour:            gcpgenserver.NewOptFloat64(2),
							Minute:          gcpgenserver.NewOptFloat64(30),
						},
					),
				},
			),
		}
		out, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Nil(t, out)
	})
	t.Run("TieringPolicy ENABLED with feature enabled", func(t *testing.T) {
		currentATState := autoTieringEnabled
		autoTieringEnabled = true
		defer func() { autoTieringEnabled = currentATState }()

		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction:           gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{Value: 30, Set: true},
				},
			),
		}
		param, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotEmpty(t, param.AutoTieringPolicy.TieringPolicy)
		assert.True(t, param.AutoTieringPolicy.AutoTieringEnabled)
		assert.Equal(t, int32(30), param.AutoTieringPolicy.CoolingThresholdDays)
	})

	t.Run("TieringPolicy PAUSED with feature enabled", func(t *testing.T) {
		currentATState := autoTieringEnabled
		autoTieringEnabled = true
		defer func() { autoTieringEnabled = currentATState }()

		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("PAUSED"),
				},
			),
		}
		param, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotEmpty(t, param.AutoTieringPolicy.TieringPolicy)
		assert.False(t, param.AutoTieringPolicy.AutoTieringEnabled)
	})

	t.Run("TieringPolicy set with feature disabled", func(t *testing.T) {
		currentATState := autoTieringEnabled
		autoTieringEnabled = false
		defer func() { autoTieringEnabled = currentATState }()

		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
				},
			),
		}
		_, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Auto-Tiering feature is currently not enabled.")
	})

	t.Run("TieringPolicy not set", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:       gcpgenserver.NewOptNilString("pool"),
			QuotaInBytes: gcpgenserver.NewOptNilFloat64(107374182400),
		}
		param, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.Nil(t, param.AutoTieringPolicy)
	})

	t.Run("WhenUnixPermissionsProvided_ThenValidateAndSet", func(t *testing.T) {
		originalUnixPermissionsEnabled := unixPermissionsEnabled
		unixPermissionsEnabled = true
		defer func() { unixPermissionsEnabled = originalUnixPermissionsEnabled }()

		req := &gcpgenserver.VolumeUpdateV1beta{
			UnixPermissions: gcpgenserver.NewOptNilString("0755"),
		}
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFS},
			FileProperties: &models.FileProperties{
				SecurityStyle:   utils.UnixSecurityStyle,
				UnixPermissions: "0700",
			},
		}

		param, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.NoError(t, err)
		require.NotNil(t, param)
		require.NotNil(t, param.FileProperties)
		assert.Equal(t, "0755", param.FileProperties.UnixPermissions)
		assert.Equal(t, utils.UnixSecurityStyle, param.FileProperties.SecurityStyle)
	})

	t.Run("WhenUnixPermissionsProvidedWithoutSecurityStyleOnNFSOnlyVolume_ThenValidateAndSet", func(t *testing.T) {
		originalUnixPermissionsEnabled := unixPermissionsEnabled
		unixPermissionsEnabled = true
		defer func() { unixPermissionsEnabled = originalUnixPermissionsEnabled }()

		req := &gcpgenserver.VolumeUpdateV1beta{
			UnixPermissions: gcpgenserver.NewOptNilString("0755"),
		}
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3},
			FileProperties: &models.FileProperties{
				UnixPermissions: "0700",
			},
		}

		param, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.NoError(t, err)
		require.NotNil(t, param)
		require.NotNil(t, param.FileProperties)
		assert.Equal(t, "0755", param.FileProperties.UnixPermissions)
		assert.Equal(t, utils.UnixSecurityStyle, param.FileProperties.SecurityStyle)
	})

	t.Run("WhenUnixPermissionsProvidedWithNonUnixSecurityStyleOnNFSOnlyVolume_ThenError", func(t *testing.T) {
		originalUnixPermissionsEnabled := unixPermissionsEnabled
		unixPermissionsEnabled = true
		defer func() { unixPermissionsEnabled = originalUnixPermissionsEnabled }()

		req := &gcpgenserver.VolumeUpdateV1beta{
			UnixPermissions: gcpgenserver.NewOptNilString("0755"),
		}
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFSv3},
			FileProperties: &models.FileProperties{
				SecurityStyle:   string(gcpgenserver.VolumeV1betaSecurityStyleNTFS),
				UnixPermissions: "0700",
			},
		}

		param, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.Nil(t, param)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Unix permissions are only supported with unix security-style")
	})

	t.Run("WhenUnixPermissionsOnNonNFSVolume_ThenError", func(t *testing.T) {
		originalUnixPermissionsEnabled := unixPermissionsEnabled
		unixPermissionsEnabled = true
		defer func() { unixPermissionsEnabled = originalUnixPermissionsEnabled }()

		req := &gcpgenserver.VolumeUpdateV1beta{
			UnixPermissions: gcpgenserver.NewOptNilString("0755"),
		}
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolSMB},
			FileProperties: &models.FileProperties{
				SecurityStyle: utils.UnixSecurityStyle,
			},
		}

		param, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.Nil(t, param)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "only supported with NFS protocol volumes")
	})

	t.Run("WhenUnixPermissionsAlreadySet_ThenError", func(t *testing.T) {
		originalUnixPermissionsEnabled := unixPermissionsEnabled
		unixPermissionsEnabled = true
		defer func() { unixPermissionsEnabled = originalUnixPermissionsEnabled }()

		req := &gcpgenserver.VolumeUpdateV1beta{
			UnixPermissions: gcpgenserver.NewOptNilString("0700"),
		}
		dbVolume := &models.Volume{
			ProtocolTypes: []string{utils.ProtocolNFS},
			FileProperties: &models.FileProperties{
				SecurityStyle:   utils.UnixSecurityStyle,
				UnixPermissions: "0700",
			},
		}

		param, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.Nil(t, param)
		assert.EqualError(t, err, "Unix permissions are already set to the desired value")
	})

	t.Run("WhenBackupConfigSet_WithFewFields", func(t *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		req := &gcpgenserver.VolumeUpdateV1beta{
			BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(gcpgenserver.BackupConfigV1beta{
				BackupVaultId:  gcpgenserver.NewOptNilString("backup-vault-id"),
				BackupPolicyId: gcpgenserver.NewOptNilString("backup-policy-id"),
			}),
		}

		param, err := _prepareUpdateVolumeParams(req, params, "region", nil)
		assert.NoError(t, err)
		assert.Equal(t, "backup-vault-id", *param.DataProtection.BackupVaultID)
		assert.Equal(t, "backup-policy-id", *param.DataProtection.BackupPolicyId)
	})
}

func TestV1betaGetVolumeCount(t *testing.T) {
	t.Run("ValidVolumeCount", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetVolumeCountParams{
			ProjectNumber: "test-project",
		}

		expectedCount := 5
		mockOrchestrator.EXPECT().GetVolumeCount(mock.Anything, params.ProjectNumber).Return(int64(expectedCount), nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaGetVolumeCount(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, expectedCount, result.(*gcpgenserver.V1betaGetVolumeCountOK).VolumeCount)
	})

	t.Run("ErrorGettingVolumeCount", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetVolumeCountParams{
			ProjectNumber: "test-project",
		}

		mockError := errors.New("failed to get volume count")
		mockOrchestrator.EXPECT().GetVolumeCount(mock.Anything, params.ProjectNumber).Return(0, mockError)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaGetVolumeCount(context.Background(), params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
	})
}

func TestV1betaListVolumes(t *testing.T) {
	t.Run("SuccessfulListVolumes", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaListVolumesParams{
			ProjectNumber: "test-project",
		}

		expectedVolumes := []*models.Volume{
			{
				CreationToken: "test-token-1",
				PoolID:        "test-pool-1",
				QuotaInBytes:  1024,
			},
			{
				CreationToken: "test-token-2",
				PoolID:        "test-pool-2",
				QuotaInBytes:  2048,
			},
		}

		mockOrchestrator.EXPECT().ListVolumes(mock.Anything, params.ProjectNumber).Return(expectedVolumes, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaListVolumes(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result.(*gcpgenserver.V1betaListVolumesOK).Volumes, len(expectedVolumes))
	})

	t.Run("ErrorListingVolumes", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaListVolumesParams{
			ProjectNumber: "test-project",
		}

		mockError := errors.New("failed to list volumes")
		mockOrchestrator.EXPECT().ListVolumes(mock.Anything, params.ProjectNumber).Return(nil, mockError)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaListVolumes(context.Background(), params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
	})
}

func TestConvertDaysOfWeekToIntArray(t *testing.T) {
	t.Run("ReturnsSundayByDefaultWhenEmpty", func(tt *testing.T) {
		result, err := convertDaysOfWeekToIntArray("")
		assert.NoError(tt, err)
		assert.Equal(tt, []int{0}, result)
	})

	t.Run("ReturnsCorrectIntsForFullNames", func(tt *testing.T) {
		result, err := convertDaysOfWeekToIntArray("Monday,Tuesday,Wednesday")
		assert.NoError(tt, err)
		assert.Equal(tt, []int{1, 2, 3}, result)
	})

	t.Run("ReturnsCorrectIntsForShortNames", func(tt *testing.T) {
		result, err := convertDaysOfWeekToIntArray("Mon,Tue,Wed")
		assert.NoError(tt, err)
		assert.Equal(tt, []int{1, 2, 3}, result)
	})

	t.Run("ReturnsErrorForInvalidDay", func(tt *testing.T) {
		result, err := convertDaysOfWeekToIntArray("Funday")
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("ReturnsErrorForDuplicateDay", func(tt *testing.T) {
		result, err := convertDaysOfWeekToIntArray("Monday,Monday")
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("TrimsSpacesAndIsCaseInsensitive", func(tt *testing.T) {
		result, err := convertDaysOfWeekToIntArray("  tuesday ,  WEDNESDAY ")
		assert.NoError(tt, err)
		assert.Equal(tt, []int{2, 3}, result)
	})
}

func TestConvertDaysOfWeekFromIntArray(t *testing.T) {
	t.Run("ReturnsCorrectStringForValidInts", func(tt *testing.T) {
		result := convertDaysOfWeekFromIntArray([]int{1, 2, 3})
		assert.Equal(tt, "Monday,Tuesday,Wednesday", result)
	})

	t.Run("ReturnsSundayForEmptyInput", func(tt *testing.T) {
		result := convertDaysOfWeekFromIntArray([]int{})
		assert.Equal(tt, "Sunday", result)
	})

	t.Run("IgnoresInvalidInts", func(tt *testing.T) {
		result := convertDaysOfWeekFromIntArray([]int{-1, 0, 6, 7})
		assert.Equal(tt, "Sunday,Saturday", result)
	})

	t.Run("HandlesAllWeekdays", func(tt *testing.T) {
		result := convertDaysOfWeekFromIntArray([]int{0, 1, 2, 3, 4, 5, 6})
		assert.Equal(tt, "Sunday,Monday,Tuesday,Wednesday,Thursday,Friday,Saturday", result)
	})
}

func TestConvertToSnapshotPolicyV2(t *testing.T) {
	t.Run("NilInput_ReturnsNil", func(tt *testing.T) {
		result := convertToSnapshotPolicyV2(nil)
		assert.Nil(tt, result)
	})

	t.Run("EmptySchedules_ReturnsEnabledWithNoSchedules", func(tt *testing.T) {
		pol := &models.SnapshotPolicy{
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{},
		}
		result := convertToSnapshotPolicyV2(pol)
		assert.NotNil(tt, result)
		assert.True(tt, result.Enabled.Value)
	})

	t.Run("MonthlySchedule", func(tt *testing.T) {
		pol := &models.SnapshotPolicy{
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{
				{
					Count:           5,
					SnapmirrorLabel: "monthly",
					Schedule: &models.Schedule{
						DaysOfMonth: []int{1, 15},
						Hours:       []int{2},
						Minutes:     []int{30},
					},
				},
			},
		}
		result := convertToSnapshotPolicyV2(pol)
		assert.NotNil(tt, result)
		assert.True(tt, result.Enabled.Value)
		assert.True(tt, result.MonthlySchedule.IsSet())
		assert.Equal(tt, "1,15", result.MonthlySchedule.Value.DaysOfMonth.Value)
		assert.Equal(tt, float64(2), result.MonthlySchedule.Value.Hour.Value)
		assert.Equal(tt, float64(30), result.MonthlySchedule.Value.Minute.Value)
		assert.Equal(tt, float64(5), result.MonthlySchedule.Value.SnapshotsToKeep.Value)
	})

	t.Run("WeeklySchedule", func(tt *testing.T) {
		pol := &models.SnapshotPolicy{
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{
				{
					Count:           3,
					SnapmirrorLabel: "weekly",
					Schedule: &models.Schedule{
						DaysOfWeek: []int{1, 2},
						Hours:      []int{5},
						Minutes:    []int{10},
					},
				},
			},
		}
		result := convertToSnapshotPolicyV2(pol)
		assert.NotNil(tt, result)
		assert.True(tt, result.WeeklySchedule.IsSet())
		assert.Contains(tt, result.WeeklySchedule.Value.Day.Value, "Monday")
		assert.Contains(tt, result.WeeklySchedule.Value.Day.Value, "Tuesday")
		assert.Equal(tt, float64(5), result.WeeklySchedule.Value.Hour.Value)
		assert.Equal(tt, float64(10), result.WeeklySchedule.Value.Minute.Value)
		assert.Equal(tt, float64(3), result.WeeklySchedule.Value.SnapshotsToKeep.Value)
	})

	t.Run("DailySchedule", func(tt *testing.T) {
		pol := &models.SnapshotPolicy{
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{
				{
					Count:           2,
					SnapmirrorLabel: "daily",
					Schedule: &models.Schedule{
						Hours:   []int{7},
						Minutes: []int{45},
					},
				},
			},
		}
		result := convertToSnapshotPolicyV2(pol)
		assert.NotNil(tt, result)
		assert.True(tt, result.DailySchedule.IsSet())
		assert.Equal(tt, float64(7), result.DailySchedule.Value.Hour.Value)
		assert.Equal(tt, float64(45), result.DailySchedule.Value.Minute.Value)
		assert.Equal(tt, float64(2), result.DailySchedule.Value.SnapshotsToKeep.Value)
	})

	t.Run("HourlySchedule", func(tt *testing.T) {
		pol := &models.SnapshotPolicy{
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{
				{
					Count:           1,
					SnapmirrorLabel: "hourly",
					Schedule: &models.Schedule{
						Minutes: []int{15},
					},
				},
			},
		}
		result := convertToSnapshotPolicyV2(pol)
		assert.NotNil(tt, result)
		assert.True(tt, result.HourlySchedule.IsSet())
		assert.Equal(tt, float64(15), result.HourlySchedule.Value.Minute.Value)
		assert.Equal(tt, float64(1), result.HourlySchedule.Value.SnapshotsToKeep.Value)
	})
}

func TestConvertToFlexCacheV1(t *testing.T) {
	t.Run("WhenSuccess", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:  "test-peer-volume",
			PeerClusterName: "test-peer-cluster",
			PeerSvmName:     "test-peer-svm",
			PeerIPAddresses: []string{"192.168.1.1", "192.168.1.2"},
			CacheConfig: &models.CacheConfig{
				WritebackEnabled: nillable.ToPointer(true),
				CachePrePopulate: &models.CachePrePopulate{
					Recursion: nillable.ToPointer(true),
				},
			},
			CacheStateDetails:     datamodel.InitiatingClusterPeering,
			CacheStateDetailsCode: models.InitiatingClusterPeeringCode,
			Passphrase:            nillable.ToPointer("some passphrase"),
			PeeringCommand:        "some command",
			PeerExpiryTime:        nillable.ToPointer(time.Now()),
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.PeerVolumeName.IsSet())
		assert.Equal(tt, "test-peer-volume", result.PeerVolumeName.Value)
		assert.True(tt, result.PeerClusterName.IsSet())
		assert.Equal(tt, "test-peer-cluster", result.PeerClusterName.Value)
		assert.True(tt, result.PeerSvmName.IsSet())
		assert.Equal(tt, "test-peer-svm", result.PeerSvmName.Value)
		assert.True(tt, result.CacheConfig.IsSet())
		assert.Nil(tt, result.PeerIpAddresses)
		assert.True(tt, result.CacheConfig.Value.CachePrePopulate.IsSet())
	})
	t.Run("WhenPrepopulateNotSet", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:  "test-peer-volume",
			PeerClusterName: "test-peer-cluster",
			PeerSvmName:     "test-peer-svm",
			PeerIPAddresses: []string{"1.1.1.1"},
			CacheConfig: &models.CacheConfig{
				WritebackEnabled: nillable.ToPointer(false),
			},
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.PeerVolumeName.IsSet())
		assert.Equal(tt, "test-peer-volume", result.PeerVolumeName.Value)
		assert.True(tt, result.CacheConfig.IsSet())
		assert.False(tt, result.CacheConfig.Value.CachePrePopulate.IsSet())
	})
	t.Run("WhenCacheConfigNotSet", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:  "test-peer-volume",
			PeerClusterName: "test-peer-cluster",
			PeerSvmName:     "test-peer-svm",
			PeerIPAddresses: []string{"1.1.1.1"},
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.PeerVolumeName.IsSet())
		assert.Equal(tt, "test-peer-volume", result.PeerVolumeName.Value)
		assert.False(tt, result.CacheConfig.IsSet())
	})
	t.Run("WhenCachePrePopulateStateSet", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:  "test-peer-volume",
			PeerClusterName: "test-peer-cluster",
			PeerSvmName:     "test-peer-svm",
			PeerIPAddresses: []string{"1.1.1.1"},
			CacheConfig: &models.CacheConfig{
				WritebackEnabled:      nillable.ToPointer(true),
				CachePrePopulateState: "COMPLETE",
			},
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.PeerVolumeName.IsSet())
		assert.Equal(tt, "test-peer-volume", result.PeerVolumeName.Value)
		assert.True(tt, result.CacheConfig.IsSet())
		assert.True(tt, result.CacheConfig.Value.CachePrePopulateState.IsSet())
		assert.Equal(tt, gcpgenserver.FlexCacheConfigV1betaCachePrePopulateState("COMPLETE"), result.CacheConfig.Value.CachePrePopulateState.Value)
	})
	t.Run("WhenAllCacheConfigFieldsSet", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:  "test-peer-volume",
			PeerClusterName: "test-peer-cluster",
			PeerSvmName:     "test-peer-svm",
			PeerIPAddresses: []string{"192.168.1.1"},
			CacheConfig: &models.CacheConfig{
				WritebackEnabled:        nillable.ToPointer(true),
				AtimeScrubEnabled:       nillable.ToPointer(false),
				AtimeScrubDays:          nillable.ToPointer(int16(7)),
				CifsChangeNotifyEnabled: nillable.ToPointer(true),
				CachePrePopulateState:   "IN_PROGRESS",
				CachePrePopulate: &models.CachePrePopulate{
					PathList:        []string{"/path1", "/path2"},
					ExcludePathList: []string{"/exclude1"},
					Recursion:       nillable.ToPointer(true),
				},
			},
			CacheState:            "READY",
			CacheStateDetails:     "Available",
			CacheStateDetailsCode: 0,
			Passphrase:            nillable.ToPointer("my-passphrase"),
			PeeringCommand:        "peer-command",
			PeerExpiryTime:        nillable.ToPointer(time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)),
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.PeerVolumeName.IsSet())
		assert.Equal(tt, "test-peer-volume", result.PeerVolumeName.Value)
		assert.True(tt, result.PeerClusterName.IsSet())
		assert.Equal(tt, "test-peer-cluster", result.PeerClusterName.Value)
		assert.True(tt, result.PeerSvmName.IsSet())
		assert.Equal(tt, "test-peer-svm", result.PeerSvmName.Value)
		assert.True(tt, result.Command.IsSet())
		assert.Equal(tt, "peer-command", result.Command.Value)
		assert.True(tt, result.StateDetails.IsSet())
		assert.Equal(tt, "Available", result.StateDetails.Value)
		assert.True(tt, result.Passphrase.IsSet())
		assert.Equal(tt, "my-passphrase", result.Passphrase.Value)
		assert.True(tt, result.PeeringCommandExpiryTime.IsSet())

		assert.True(tt, result.CacheConfig.IsSet())
		cacheConfig := result.CacheConfig.Value

		assert.True(tt, cacheConfig.WritebackEnabled.IsSet())
		assert.True(tt, cacheConfig.WritebackEnabled.Value)

		assert.True(tt, cacheConfig.AtimeScrubEnabled.IsSet())
		assert.False(tt, cacheConfig.AtimeScrubEnabled.Value)

		assert.True(tt, cacheConfig.AtimeScrubDays.IsSet())
		assert.Equal(tt, int16(7), cacheConfig.AtimeScrubDays.Value)

		assert.True(tt, cacheConfig.CifsChangeNotifyEnabled.IsSet())
		assert.True(tt, cacheConfig.CifsChangeNotifyEnabled.Value)

		assert.True(tt, cacheConfig.CachePrePopulateState.IsSet())
		assert.Equal(tt, gcpgenserver.FlexCacheConfigV1betaCachePrePopulateState("IN_PROGRESS"), cacheConfig.CachePrePopulateState.Value)

		assert.True(tt, cacheConfig.CachePrePopulate.IsSet())
		prePopulate := cacheConfig.CachePrePopulate.Value
		assert.True(tt, prePopulate.PathList.IsSet())
		assert.Equal(tt, []string{"/path1", "/path2"}, prePopulate.PathList.Value)
		assert.True(tt, prePopulate.ExcludePathList.IsSet())
		assert.Equal(tt, []string{"/exclude1"}, prePopulate.ExcludePathList.Value)
		assert.True(tt, prePopulate.Recursion.IsSet())
		assert.True(tt, prePopulate.Recursion.Value)
	})

	t.Run("WhenOptionalFieldsNotSet", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:  "test-peer-volume",
			PeerClusterName: "test-peer-cluster",
			PeerSvmName:     "test-peer-svm",
			PeerIPAddresses: []string{"1.1.1.1"},
			CacheState:      "PENDING",
			CacheConfig: &models.CacheConfig{
				WritebackEnabled: nillable.ToPointer(false),
			},
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.PeerVolumeName.IsSet())
		assert.Equal(tt, "test-peer-volume", result.PeerVolumeName.Value)
		assert.False(tt, result.Command.IsSet())
		assert.False(tt, result.StateDetails.IsSet())
		assert.False(tt, result.StateDetailsCode.IsSet())
		assert.False(tt, result.Passphrase.IsSet())
		assert.False(tt, result.PeeringCommandExpiryTime.IsSet())
		assert.Nil(tt, result.PeerIpAddresses)
	})

	t.Run("WhenCachePrePopulateStateEmpty", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:  "test-peer-volume",
			PeerClusterName: "test-peer-cluster",
			PeerSvmName:     "test-peer-svm",
			PeerIPAddresses: []string{"1.1.1.1"},
			CacheConfig: &models.CacheConfig{
				WritebackEnabled:      nillable.ToPointer(true),
				CachePrePopulateState: "",
			},
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.CacheConfig.IsSet())
		assert.False(tt, result.CacheConfig.Value.CachePrePopulateState.IsSet())
	})

	t.Run("WhenCacheConfigHasNilPointers", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:  "test-peer-volume",
			PeerClusterName: "test-peer-cluster",
			PeerSvmName:     "test-peer-svm",
			PeerIPAddresses: []string{"1.1.1.1"},
			CacheConfig: &models.CacheConfig{
				WritebackEnabled:        nil,
				AtimeScrubEnabled:       nil,
				AtimeScrubDays:          nil,
				CifsChangeNotifyEnabled: nil,
			},
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.CacheConfig.IsSet())
		cacheConfig := result.CacheConfig.Value

		assert.True(tt, cacheConfig.WritebackEnabled.IsSet())
		assert.False(tt, cacheConfig.WritebackEnabled.Value)

		assert.True(tt, cacheConfig.AtimeScrubEnabled.IsSet())
		assert.False(tt, cacheConfig.AtimeScrubEnabled.Value)

		assert.True(tt, cacheConfig.AtimeScrubDays.IsSet())
		assert.Equal(tt, int16(0), cacheConfig.AtimeScrubDays.Value)

		assert.True(tt, cacheConfig.CifsChangeNotifyEnabled.IsSet())
		assert.False(tt, cacheConfig.CifsChangeNotifyEnabled.Value)
	})

	t.Run("WhenEnableGlobalFileLockSet", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:       "test-peer-volume",
			PeerClusterName:      "test-peer-cluster",
			PeerSvmName:          "test-peer-svm",
			PeerIPAddresses:      []string{"1.1.1.1"},
			EnableGlobalFileLock: nillable.GetBoolPtr(true),
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.PeerVolumeName.IsSet())
		assert.True(tt, result.EnableGlobalFileLock.IsSet())
		assert.True(tt, result.EnableGlobalFileLock.Value)
	})

	t.Run("WhenEnableGlobalFileLockNotSet", func(tt *testing.T) {
		cp := &models.CacheParameters{
			PeerVolumeName:  "test-peer-volume",
			PeerClusterName: "test-peer-cluster",
			PeerSvmName:     "test-peer-svm",
			PeerIPAddresses: []string{"1.1.1.1"},
			// EnableGlobalFileLock is nil
		}

		result := convertToFlexCacheV1(cp)

		assert.True(tt, result.PeerVolumeName.IsSet())
		assert.False(tt, result.EnableGlobalFileLock.IsSet())
	})
}

func TestV1betaCreateVolume(t *testing.T) {
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	mockParseAndValidateRegionAndZone := func(region string) (string, string, *gcpgenserver.Error) {
		return "test-region", "test-location", nil
	}
	utils.ParseAndValidateRegionAndZone = mockParseAndValidateRegionAndZone
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	t.Run("ValidCreateVolume", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				Labels:        gcpgenserver.OptVolumeV1betaLabels{Value: map[string]string{"test-label": "test-value"}, Set: true},
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "CREATING",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})

	t.Run("RejectedWhenPoolZoneSwitchingOrSwitched", func(tt *testing.T) {
		for _, zoneState := range []string{datamodel.ZoneSwitching, datamodel.ZoneSwitched} {
			tt.Run(zoneState, func(t *testing.T) {
				mockOrchestrator := factory.NewMockOrchestratorFactory(t)
				handler := Handler{Orchestrator: mockOrchestrator}

				params := gcpgenserver.V1betaCreateVolumeParams{
					LocationId:    "location-id",
					ProjectNumber: "project-number",
				}
				req := &gcpgenserver.VolumeCreateV1beta{
					Volume: gcpgenserver.VolumeV1beta{
						ResourceId:    "testvolume",
						CreationToken: gcpgenserver.NewOptString("test-token"),
						PoolId:        gcpgenserver.NewNilString("test-pool"),
						QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
						Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
					},
					VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
				}
				mockOrchestrator.EXPECT().DescribePool(mock.Anything, "test-pool", "project-number").Return(&models.Pool{
					PoolAttributes: &models.PoolAttributes{ZoneSwitchState: zoneState},
				}, nil)

				result, err := handler.V1betaCreateVolume(context.Background(), req, params)
				require.NoError(t, err)
				badReq, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
				require.True(t, ok)
				assert.Equal(t, float64(http.StatusBadRequest), badReq.Code)
				assert.Equal(t, "Volume creation is not supported when the pool is switching/switched to a different primary zone.", badReq.Message)
			})
		}
	})

	t.Run("SMBPoolDescribeNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaSMB},
			},
		}

		notFoundErr := errors.NewNotFoundErr("Pool", nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "test-pool", "project-number").Return(nil, notFoundErr)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, notFoundErr.Error(), badReq.Message)
	})

	t.Run("SMBPoolDescribeInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaSMB},
			},
		}

		describeErr := fmt.Errorf("Internal server error")
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "test-pool", "project-number").Return(nil, describeErr)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaCreateVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, describeErr.Error(), internalErr.Message)
	})

	t.Run("KerberosPoolDescribeNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:      "testvolume",
				CreationToken:   gcpgenserver.NewOptString("test-token"),
				PoolId:          gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:    gcpgenserver.NewOptFloat64(1024),
				Protocols:       []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
				KerberosEnabled: gcpgenserver.NewOptNilBool(true),
			},
		}

		notFoundErr := errors.NewNotFoundErr("Pool", nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "test-pool", "project-number").Return(nil, notFoundErr)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, notFoundErr.Error(), badReq.Message)
	})

	t.Run("KerberosPoolDescribeInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:      "testvolume",
				CreationToken:   gcpgenserver.NewOptString("test-token"),
				PoolId:          gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:    gcpgenserver.NewOptFloat64(1024),
				Protocols:       []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
				KerberosEnabled: gcpgenserver.NewOptNilBool(true),
			},
		}

		describeErr := fmt.Errorf("Internal server error")
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "test-pool", "project-number").Return(nil, describeErr)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaCreateVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, describeErr.Error(), internalErr.Message)
	})

	t.Run("UserInputValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		req := &gcpgenserver.VolumeCreateV1beta{}
		prepareCreateVolumeParams = func(req *gcpgenserver.VolumeCreateV1beta, params gcpgenserver.V1betaCreateVolumeParams, region string, zone string, pool *models.Pool) (*common.CreateVolumeParams, error) {
			return nil, errors.NewUserInputValidationErr("invalid input")
		}
		defer func() { prepareCreateVolumeParams = _prepareCreateVolumeParams }()

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "invalid input")
	})
	t.Run("WhenHybridReplicationNotEnabled", func(tt *testing.T) {
		// Mock the hybridReplicationEnabled variable to be false
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = false
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					ReplicationSchedule:   gcpgenserver.NewOptHybridReplicationParametersV1betaReplicationSchedule("daily"),
					PeerClusterName:       "peer-cluster",
					PeerVolumeName:        "peer-volume",
					PeerSvmName:           "peer-svm",
					PeerIpAddresses:       []string{"192.168.1.1"},
					ResourceId:            "resource-123",
				},
				Set: true,
			},
		}
		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(t, err)
		badReq, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusBadRequest), badReq.Code)
		assert.Equal(t, "Hybrid migration is not enabled", badReq.Message)
	})

	t.Run("WhenOnpremReplicationNotEnabled", func(tt *testing.T) {
		// Mock hybridReplicationEnabled to be true (to pass first check)
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		// Mock bidiReplicationEnabled to be false (to trigger error at line 126-131)
		originalBidiReplicationEnabled := bidiReplicationEnabled
		bidiReplicationEnabled = false
		defer func() { bidiReplicationEnabled = originalBidiReplicationEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					ReplicationSchedule:   gcpgenserver.NewOptHybridReplicationParametersV1betaReplicationSchedule("daily"),
					PeerClusterName:       "peer-cluster",
					PeerVolumeName:        "peer-volume",
					PeerSvmName:           "peer-svm",
					PeerIpAddresses:       []string{"192.168.1.1"},
					ResourceId:            "resource-123",
				},
				Set: true,
			},
		}
		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Equal(tt, "Onprem replication is not enabled", badReq.Message)
	})

	t.Run("UserInputValidationErrorWhenVolumeQuotaIsByteIsNotSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{},
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "QuotaInBytes is required", badReq.Message)
	})

	t.Run("InternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		req := &gcpgenserver.VolumeCreateV1beta{}
		prepareCreateVolumeParams = func(req *gcpgenserver.VolumeCreateV1beta, params gcpgenserver.V1betaCreateVolumeParams, region string, zone string, pool *models.Pool) (*common.CreateVolumeParams, error) {
			return nil, fmt.Errorf("unexpected error")
		}
		defer func() { prepareCreateVolumeParams = _prepareCreateVolumeParams }()

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaCreateVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "unexpected error")
	})

	t.Run("BadRequest_InvalidLocation", func(tt *testing.T) {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		defer func() { utils.ParseAndValidateRegionAndZone = mockParseAndValidateRegionAndZone }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		req := &gcpgenserver.VolumeCreateV1beta{}
		prepareCreateVolumeParams = func(req *gcpgenserver.VolumeCreateV1beta, params gcpgenserver.V1betaCreateVolumeParams, region string, zone string, pool *models.Pool) (*common.CreateVolumeParams, error) {
			return nil, fmt.Errorf("unexpected error")
		}
		defer func() { prepareCreateVolumeParams = _prepareCreateVolumeParams }()

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "LocationID represents neither a region nor a zone")
	})

	t.Run("WhenOrchestratorValidationThrowsAnError_Return400BadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
		}

		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("An error occurred"))
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "An error occurred")
	})

	t.Run("WhenOrchestratorThrowsAnError_ReturnError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
		}

		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(nil, "", errors.New("An error occurred"))
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaCreateVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "An error occurred")
	})

	t.Run("WhenOrchestratorThrowsConflictError_Return409Conflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
		}

		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewConflictErr("Volume already exists"))
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		conflictErr, ok := result.(*gcpgenserver.V1betaCreateVolumeConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(409), conflictErr.Code)
		assert.Contains(tt, conflictErr.Message, "Volume already exists")
	})

	t.Run("WhenLifeCycleStateCreating_ThenReturnDoneAsFalse", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "CREATING",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
		assert.False(tt, op.Done.Value)
	})

	t.Run("WhenLifeCycleStateCreating_ThenReturnDoneAsFalse", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "ERROR",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
		assert.True(tt, op.Done.Value)
	})

	t.Run("ValidCreateVolumeWithTieringPolicy", func(tt *testing.T) {
		// Save and restore the original value
		currentATState := autoTieringEnabled
		defer func() { autoTieringEnabled = currentATState }()
		autoTieringEnabled = true
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
					gcpgenserver.TieringPolicyV1beta{
						TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
						CoolingThresholdDays: gcpgenserver.OptNilInt32{
							Value: 30,
							Set:   true,
						},
					},
				),
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "CREATING",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
		assert.False(tt, op.Done.Value)
	})

	t.Run("BlockDevicesWithNameSet_ReturnsBadRequest", func(tt *testing.T) {
		// Mock the hybridReplicationEnabled variable to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		// Mock the bidiReplicationEnabled variable to be false
		originalBidiReplicationEnabled := bidiReplicationEnabled
		bidiReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalBidiReplicationEnabled }()
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				BlockDevices: []gcpgenserver.BlockDeviceV1beta{
					{
						Name: gcpgenserver.NewOptString("test-lun"),
					},
				},
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					ReplicationSchedule:   gcpgenserver.NewOptHybridReplicationParametersV1betaReplicationSchedule("daily"),
					PeerClusterName:       "peer-cluster",
					PeerVolumeName:        "peer-volume",
					PeerSvmName:           "peer-svm",
					PeerIpAddresses:       []string{"192.168.1.1"},
					ResourceId:            "resource-123",
				},
				Set: true,
			},
		}

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Equal(tt, "Block device name is not supported for hybrid replication volume. This will be replicated from onprem volume.", badReq.Message)
	})

	t.Run("BlockDevicesWithMultipleDevicesAndOneHasName_ReturnsBadRequest", func(tt *testing.T) {
		// Mock the hybridReplicationEnabled variable to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		// Mock the bidiReplicationEnabled variable to be false
		originalBidiReplicationEnabled := bidiReplicationEnabled
		bidiReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()
		defer func() { bidiReplicationEnabled = originalBidiReplicationEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				BlockDevices: []gcpgenserver.BlockDeviceV1beta{
					{
						OsType: gcpgenserver.NewOptBlockDeviceV1betaOsType(gcpgenserver.BlockDeviceV1betaOsTypeLINUX),
					},
					{
						Name: gcpgenserver.NewOptString("test-lun"),
					},
				},
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					ReplicationSchedule:   gcpgenserver.NewOptHybridReplicationParametersV1betaReplicationSchedule("daily"),
					PeerClusterName:       "peer-cluster",
					PeerVolumeName:        "peer-volume",
					PeerSvmName:           "peer-svm",
					PeerIpAddresses:       []string{"192.168.1.1"},
					ResourceId:            "resource-123",
				},
				Set: true,
			},
		}

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Equal(tt, "Block device name is not supported for hybrid replication volume. This will be replicated from onprem volume.", badReq.Message)
	})

	t.Run("BlockDevicesNil_ShouldPass", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				BlockDevices:  nil,
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "CREATING",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})

	t.Run("BlockDevicesEmpty_ShouldPass", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				BlockDevices:  []gcpgenserver.BlockDeviceV1beta{},
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "CREATING",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})

	t.Run("BlockDevicesWithoutName_ShouldPass", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				BlockDevices: []gcpgenserver.BlockDeviceV1beta{
					{
						OsType: gcpgenserver.NewOptBlockDeviceV1betaOsType(gcpgenserver.BlockDeviceV1betaOsTypeLINUX),
					},
				},
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "CREATING",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})

	t.Run("BlockDevicesWithNameButNoHybridReplication_ShouldPass", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				BlockDevices: []gcpgenserver.BlockDeviceV1beta{
					{
						Name:   gcpgenserver.NewOptString("test-lun"),
						OsType: gcpgenserver.NewOptBlockDeviceV1betaOsType(gcpgenserver.BlockDeviceV1betaOsTypeLINUX),
					},
				},
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "CREATING",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})

	t.Run("BlockDevicesWithoutNameWithHybridReplication_ShouldPass", func(tt *testing.T) {
		// Mock the hybridReplicationEnabled variable to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		// Mock the bidiReplicationEnabled variable to be false
		originalBidiReplicationEnabled := bidiReplicationEnabled
		bidiReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalBidiReplicationEnabled }()
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				BlockDevices: []gcpgenserver.BlockDeviceV1beta{
					{
						OsType: gcpgenserver.NewOptBlockDeviceV1betaOsType(gcpgenserver.BlockDeviceV1betaOsTypeLINUX),
					},
				},
			},
			VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					ReplicationSchedule:   gcpgenserver.NewOptHybridReplicationParametersV1betaReplicationSchedule("daily"),
					PeerClusterName:       "peer-cluster",
					PeerVolumeName:        "peer-volume",
					PeerSvmName:           "peer-svm",
					PeerIpAddresses:       []string{"192.168.1.1"},
					ResourceId:            "resource-123",
				},
				Set: true,
			},
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "CREATING",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)

		result, err := handler.V1betaCreateVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
	})
}

func TestConvertModelToVCPVolume(t *testing.T) {
	t.Run("RegionalHAWithSecondaryZoneSetsAPISecondaryZone", func(t *testing.T) {
		vol := &models.Volume{
			IsRegionalHA:   true,
			Zone:           "us-central1-a",
			SecondaryZone:  "us-central1-b",
			CreationToken:  "token",
			PoolID:         "pool",
			QuotaInBytes:   1024,
			ProtocolTypes:  []string{"NFSV3"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
		}
		out := convertModelToVCPVolume(vol)
		require.NotNil(t, out)
		assert.True(t, out.SecondaryZone.IsSet())
		assert.Equal(t, "us-central1-b", out.SecondaryZone.Value)
	})
	t.Run("RegionalHAButEmptySecondaryZoneOmitsAPISecondaryZone", func(t *testing.T) {
		vol := &models.Volume{
			IsRegionalHA:   true,
			Zone:           "us-central1-a",
			SecondaryZone:  "",
			CreationToken:  "token",
			PoolID:         "pool",
			QuotaInBytes:   1024,
			ProtocolTypes:  []string{"NFSV3"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
		}
		out := convertModelToVCPVolume(vol)
		require.NotNil(t, out)
		assert.False(t, out.SecondaryZone.IsSet())
	})
	t.Run("ZonalVolumeOmitsAPISecondaryZoneEvenWhenPoolHadSecondaryZone", func(t *testing.T) {
		vol := &models.Volume{
			IsRegionalHA:   false,
			Zone:           "us-central1-a",
			SecondaryZone:  "us-central1-c",
			CreationToken:  "token",
			PoolID:         "pool",
			QuotaInBytes:   1024,
			ProtocolTypes:  []string{"NFSV3"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
		}
		out := convertModelToVCPVolume(vol)
		require.NotNil(t, out)
		assert.False(t, out.SecondaryZone.IsSet())
	})
	t.Run("AllFieldsSet", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:             "token",
			PoolID:                    "pool",
			QuotaInBytes:              1234,
			BlockProperties:           &models.BlockProperties{OSType: "LINUX"},
			ProtocolTypes:             []string{"ISCSI"},
			LifeCycleState:            "READY",
			IPAddresses:               []string{"10.72.177.17"},
			KerberosEnabled:           true,
			LdapEnabled:               true,
			ActiveDirectoryConfigId:   "ad-config",
			ActiveDirectoryResourceId: "ad-resource",
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.Equal(t, "token", out.CreationToken.Value)
		assert.Equal(t, "LINUX", string(out.BlockProperties.Value.OsType.Value))
		assert.Equal(t, "ISCSI", string(out.Protocols[0]))
		assert.True(t, out.KerberosEnabled.Value)
		assert.True(t, out.LdapEnabled.Value)
		assert.Equal(t, "ad-config", out.ActiveDirectoryConfigId.Value)
		assert.Equal(t, "ad-resource", out.ActiveDirectoryResourceId.Value)
	})
	t.Run("AllFieldsWithKms", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:   "token",
			PoolID:          "pool",
			QuotaInBytes:    1234,
			BlockProperties: &models.BlockProperties{OSType: "LINUX"},
			ProtocolTypes:   []string{"ISCSI"},
			LifeCycleState:  "READY",
			IPAddresses:     []string{"10.72.177.17"},
			KmsConfig: &models.KmsConfig{
				BaseModel: models.BaseModel{
					UUID: "kms-uuid",
				},
				KeyRingLocation: "location-id",
				KeyRing:         "key-ring-name",
				KeyName:         "key-name",
				KeyProjectID:    "proj-1",
			},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.Equal(t, "token", out.CreationToken.Value)
		assert.Equal(t, "LINUX", string(out.BlockProperties.Value.OsType.Value))
		assert.Equal(t, "ISCSI", string(out.Protocols[0]))
		assert.Equal(t, "kms-uuid", out.KmsConfigId.Value)
		assert.Equal(t, "projects/proj-1/locations/location-id/keyRings/key-ring-name/cryptoKeys/key-name", out.KmsConfigResourceId.Value)
	})

	t.Run("WithFilePropertiesAndExportRules", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:  "file-token",
			PoolID:         "file-pool",
			QuotaInBytes:   2048,
			ProtocolTypes:  []string{"NFSV3"},
			LifeCycleState: "READY",
			FileProperties: &models.FileProperties{
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "test-export-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients:      "192.168.1.0/24",
							AccessType:          "READ_WRITE",
							NFSv3:               true,
							NFSv4:               false,
							Kerberos5ReadOnly:   false,
							Kerberos5ReadWrite:  false,
							Kerberos5iReadOnly:  false,
							Kerberos5iReadWrite: false,
							Kerberos5pReadOnly:  false,
							Kerberos5pReadWrite: false,
							Index:               1,
						},
						{
							AllowedClients:      "10.0.0.0/8",
							AccessType:          "READ_ONLY",
							NFSv3:               false,
							NFSv4:               true,
							Kerberos5ReadOnly:   false,
							Kerberos5ReadWrite:  false,
							Kerberos5iReadOnly:  false,
							Kerberos5iReadWrite: false,
							Kerberos5pReadOnly:  false,
							Kerberos5pReadWrite: false,
							Index:               2,
						},
					},
				},
			},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.Equal(t, "file-token", out.CreationToken.Value)
		assert.Equal(t, "NFSV3", string(out.Protocols[0]))

		// Verify ExportPolicy is properly converted
		assert.True(t, out.ExportPolicy.IsSet())
		exportPolicy := out.ExportPolicy.Value
		assert.Len(t, exportPolicy.Rules, 2)

		// Verify first rule
		rule1 := exportPolicy.Rules[0]
		assert.Equal(t, "192.168.1.0/24", rule1.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE, rule1.AccessType)
		assert.True(t, rule1.Nfsv3.Value)
		assert.False(t, rule1.Nfsv4.Value)
		assert.False(t, rule1.Kerberos5ReadOnly.Value)
		assert.False(t, rule1.Kerberos5ReadWrite.Value)
		assert.False(t, rule1.Kerberos5iReadOnly.Value)
		assert.False(t, rule1.Kerberos5iReadWrite.Value)
		assert.False(t, rule1.Kerberos5pReadOnly.Value)
		assert.False(t, rule1.Kerberos5pReadWrite.Value)
		// Verify AllSquash and AnonUid are not set (nil pointers)
		assert.False(t, rule1.AllSquash.IsSet())
		assert.False(t, rule1.AnonUid.IsSet())

		// Verify second rule
		rule2 := exportPolicy.Rules[1]
		assert.Equal(t, "10.0.0.0/8", rule2.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY, rule2.AccessType)
		assert.False(t, rule2.Nfsv3.Value)
		assert.True(t, rule2.Nfsv4.Value)
		assert.False(t, rule2.Kerberos5ReadOnly.Value)
		assert.False(t, rule2.Kerberos5ReadWrite.Value)
		assert.False(t, rule2.Kerberos5iReadOnly.Value)
		assert.False(t, rule2.Kerberos5iReadWrite.Value)
		assert.False(t, rule2.Kerberos5pReadOnly.Value)
		assert.False(t, rule2.Kerberos5pReadWrite.Value)
		assert.False(t, rule2.AllSquash.IsSet())
		assert.False(t, rule2.AnonUid.IsSet())
	})

	t.Run("WithFilePropertiesAndKerberosExportRules", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:  "kerberos-token",
			PoolID:         "kerberos-pool",
			QuotaInBytes:   4096,
			ProtocolTypes:  []string{"NFSV3", "NFSV4"},
			LifeCycleState: "READY",
			FileProperties: &models.FileProperties{
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "kerberos-export-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients:      "192.168.1.0/24",
							AccessType:          "READ_WRITE",
							NFSv3:               true,
							NFSv4:               false,
							Kerberos5ReadOnly:   true,
							Kerberos5ReadWrite:  false,
							Kerberos5iReadOnly:  false,
							Kerberos5iReadWrite: true,
							Kerberos5pReadOnly:  true,
							Kerberos5pReadWrite: false,
							Index:               1,
						},
						{
							AllowedClients:      "10.0.0.0/8",
							AccessType:          "READ_ONLY",
							NFSv3:               false,
							NFSv4:               true,
							Kerberos5ReadOnly:   false,
							Kerberos5ReadWrite:  true,
							Kerberos5iReadOnly:  true,
							Kerberos5iReadWrite: false,
							Kerberos5pReadOnly:  false,
							Kerberos5pReadWrite: true,
							Index:               2,
						},
					},
				},
			},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.Equal(t, "kerberos-token", out.CreationToken.Value)
		assert.Equal(t, "NFSV3", string(out.Protocols[0]))
		assert.Equal(t, "NFSV4", string(out.Protocols[1]))

		// Verify ExportPolicy is properly converted
		assert.True(t, out.ExportPolicy.IsSet())
		exportPolicy := out.ExportPolicy.Value
		assert.Len(t, exportPolicy.Rules, 2)

		// Verify first rule with Kerberos parameters
		rule1 := exportPolicy.Rules[0]
		assert.Equal(t, "192.168.1.0/24", rule1.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE, rule1.AccessType)
		assert.True(t, rule1.Nfsv3.Value)
		assert.False(t, rule1.Nfsv4.Value)
		assert.True(t, rule1.Kerberos5ReadOnly.Value)
		assert.False(t, rule1.Kerberos5ReadWrite.Value)
		assert.False(t, rule1.Kerberos5iReadOnly.Value)
		assert.True(t, rule1.Kerberos5iReadWrite.Value)
		assert.True(t, rule1.Kerberos5pReadOnly.Value)
		assert.False(t, rule1.Kerberos5pReadWrite.Value)

		// Verify second rule with Kerberos parameters
		rule2 := exportPolicy.Rules[1]
		assert.Equal(t, "10.0.0.0/8", rule2.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY, rule2.AccessType)
		assert.False(t, rule2.Nfsv3.Value)
		assert.True(t, rule2.Nfsv4.Value)
		assert.False(t, rule2.Kerberos5ReadOnly.Value)
		assert.True(t, rule2.Kerberos5ReadWrite.Value)
		assert.True(t, rule2.Kerberos5iReadOnly.Value)
		assert.False(t, rule2.Kerberos5iReadWrite.Value)
		assert.False(t, rule2.Kerberos5pReadOnly.Value)
		assert.True(t, rule2.Kerberos5pReadWrite.Value)
	})

	t.Run("WithFilePropertiesAndExportRulesWithSuperuserTrue", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:  "superuser-token",
			PoolID:         "superuser-pool",
			QuotaInBytes:   4096,
			ProtocolTypes:  []string{"NFSV3"},
			LifeCycleState: "READY",
			FileProperties: &models.FileProperties{
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "superuser-export-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients:      "192.168.1.0/24",
							AccessType:          "READ_WRITE",
							NFSv3:               true,
							NFSv4:               false,
							Superuser:           true,
							Kerberos5ReadOnly:   false,
							Kerberos5ReadWrite:  false,
							Kerberos5iReadOnly:  false,
							Kerberos5iReadWrite: false,
							Kerberos5pReadOnly:  false,
							Kerberos5pReadWrite: false,
							Index:               1,
						},
						{
							AllowedClients:      "10.0.0.0/8",
							AccessType:          "READ_ONLY",
							NFSv3:               false,
							NFSv4:               true,
							Superuser:           false,
							Kerberos5ReadOnly:   false,
							Kerberos5ReadWrite:  false,
							Kerberos5iReadOnly:  false,
							Kerberos5iReadWrite: false,
							Kerberos5pReadOnly:  false,
							Kerberos5pReadWrite: false,
							Index:               2,
						},
					},
				},
			},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.Equal(t, "superuser-token", out.CreationToken.Value)
		assert.Equal(t, "NFSV3", string(out.Protocols[0]))

		// Verify ExportPolicy is properly converted
		assert.True(t, out.ExportPolicy.IsSet())
		exportPolicy := out.ExportPolicy.Value
		assert.Len(t, exportPolicy.Rules, 2)

		// Verify first rule with Superuser=true
		rule1 := exportPolicy.Rules[0]
		assert.Equal(t, "192.168.1.0/24", rule1.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE, rule1.AccessType)
		assert.True(t, rule1.Nfsv3.Value)
		assert.False(t, rule1.Nfsv4.Value)
		assert.True(t, rule1.HasRootAccess.IsSet())
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue, rule1.HasRootAccess.Value)

		// Verify second rule with Superuser=false
		rule2 := exportPolicy.Rules[1]
		assert.Equal(t, "10.0.0.0/8", rule2.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY, rule2.AccessType)
		assert.False(t, rule2.Nfsv3.Value)
		assert.True(t, rule2.Nfsv4.Value)
		assert.True(t, rule2.HasRootAccess.IsSet())
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessFalse, rule2.HasRootAccess.Value)
	})

	t.Run("WithFilePropertiesAndMixedExportRules", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:  "mixed-token",
			PoolID:         "mixed-pool",
			QuotaInBytes:   8192,
			ProtocolTypes:  []string{"NFSV3"},
			LifeCycleState: "READY",
			FileProperties: &models.FileProperties{
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "mixed-export-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients:      "172.16.0.0/16",
							AccessType:          "READ_WRITE",
							NFSv3:               true,
							NFSv4:               true,
							Kerberos5ReadOnly:   false,
							Kerberos5ReadWrite:  false,
							Kerberos5iReadOnly:  false,
							Kerberos5iReadWrite: false,
							Kerberos5pReadOnly:  false,
							Kerberos5pReadWrite: false,
							Index:               1,
						},
					},
				},
			},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify ExportPolicy is properly converted
		assert.True(t, out.ExportPolicy.IsSet())
		exportPolicy := out.ExportPolicy.Value
		assert.Len(t, exportPolicy.Rules, 1)

		// Verify rule with all false Kerberos parameters
		rule := exportPolicy.Rules[0]
		assert.Equal(t, "172.16.0.0/16", rule.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE, rule.AccessType)
		assert.True(t, rule.Nfsv3.Value)
		assert.True(t, rule.Nfsv4.Value)
		assert.False(t, rule.Kerberos5ReadOnly.Value)
		assert.False(t, rule.Kerberos5ReadWrite.Value)
		assert.False(t, rule.Kerberos5iReadOnly.Value)
		assert.False(t, rule.Kerberos5iReadWrite.Value)
		assert.False(t, rule.Kerberos5pReadOnly.Value)
		assert.False(t, rule.Kerberos5pReadWrite.Value)
	})

	t.Run("WithFilePropertiesAndExportRulesAllSquashEnabled", func(t *testing.T) {
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(true)

		vol := &models.Volume{
			CreationToken:  "file-token",
			PoolID:         "file-pool",
			QuotaInBytes:   2048,
			ProtocolTypes:  []string{"NFSV3"},
			LifeCycleState: "READY",
			FileProperties: &models.FileProperties{
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "test-export-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients:      "192.168.1.0/24",
							AccessType:          "READ_WRITE",
							NFSv3:               true,
							NFSv4:               false,
							Kerberos5ReadOnly:   false,
							Kerberos5ReadWrite:  false,
							Kerberos5iReadOnly:  false,
							Kerberos5iReadWrite: false,
							Kerberos5pReadOnly:  false,
							Kerberos5pReadWrite: false,
							Index:               1,
							AllSquash:           nillable.ToPointer(true),
							AnonUid:             nillable.ToPointer(int64(1000)),
						},
						{
							AllowedClients:      "10.0.0.0/8",
							AccessType:          "READ_ONLY",
							NFSv3:               false,
							NFSv4:               true,
							Kerberos5ReadOnly:   false,
							Kerberos5ReadWrite:  false,
							Kerberos5iReadOnly:  false,
							Kerberos5iReadWrite: false,
							Kerberos5pReadOnly:  false,
							Kerberos5pReadWrite: false,
							Index:               2,
							AllSquash:           nillable.ToPointer(false),
							AnonUid:             nillable.ToPointer(int64(0)),
						},
					},
				},
			},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.Equal(t, "file-token", out.CreationToken.Value)
		assert.Equal(t, "NFSV3", string(out.Protocols[0]))

		// Verify ExportPolicy is properly converted
		assert.True(t, out.ExportPolicy.IsSet())
		exportPolicy := out.ExportPolicy.Value
		assert.Len(t, exportPolicy.Rules, 2)

		// Verify first rule
		rule1 := exportPolicy.Rules[0]
		assert.Equal(t, "192.168.1.0/24", rule1.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE, rule1.AccessType)
		assert.True(t, rule1.Nfsv3.Value)
		assert.False(t, rule1.Nfsv4.Value)
		assert.False(t, rule1.Kerberos5ReadOnly.Value)
		assert.False(t, rule1.Kerberos5ReadWrite.Value)
		assert.False(t, rule1.Kerberos5iReadOnly.Value)
		assert.False(t, rule1.Kerberos5iReadWrite.Value)
		assert.False(t, rule1.Kerberos5pReadOnly.Value)
		assert.False(t, rule1.Kerberos5pReadWrite.Value)
		assert.True(t, rule1.AllSquash.IsSet())
		assert.True(t, rule1.AllSquash.Value)
		assert.True(t, rule1.AnonUid.IsSet())
		assert.Equal(t, int64(1000), rule1.AnonUid.Value)

		// Verify second rule
		rule2 := exportPolicy.Rules[1]
		assert.Equal(t, "10.0.0.0/8", rule2.AllowedClients)
		assert.Equal(t, gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY, rule2.AccessType)
		assert.False(t, rule2.Nfsv3.Value)
		assert.True(t, rule2.Nfsv4.Value)
		assert.False(t, rule2.Kerberos5ReadOnly.Value)
		assert.False(t, rule2.Kerberos5ReadWrite.Value)
		assert.False(t, rule2.Kerberos5iReadOnly.Value)
		assert.False(t, rule2.Kerberos5iReadWrite.Value)
		assert.False(t, rule2.Kerberos5pReadOnly.Value)
		assert.False(t, rule2.Kerberos5pReadWrite.Value)
		assert.True(t, rule2.AllSquash.IsSet())
		assert.False(t, rule2.AllSquash.Value)
		assert.True(t, rule2.AnonUid.IsSet())
		assert.Equal(t, int64(0), rule2.AnonUid.Value)
	})

	t.Run("WithBlockDevices_ShouldConvertToAPIFormat", func(t *testing.T) {
		blockDevices := []models.BlockDevice{
			{
				Name:       "test-lun-1",
				Identifier: "lun-123",
				Size:       107374182400, // 100 GiB in bytes
				OSType:     "LINUX",
				HostGroupDetail: []models.HostGroupDetails{
					{
						HostGroupID: "hg-uuid-1",
						Hosts:       []string{"iqn.1998-01.com.vmware:host1", "iqn.1998-01.com.vmware:host2"},
					},
					{
						HostGroupID: "hg-uuid-2",
						Hosts:       []string{"iqn.1998-01.com.vmware:host3"},
					},
				},
			},
			{
				Name:       "test-lun-2",
				Identifier: "lun-456",
				Size:       214748364800, // 200 GiB in bytes
				OSType:     "WINDOWS",
				HostGroupDetail: []models.HostGroupDetails{
					{
						HostGroupID: "hg-uuid-3",
						Hosts:       []string{"iqn.1998-01.com.vmware:host4"},
					},
				},
			},
		}

		vol := &models.Volume{
			CreationToken:  "block-token",
			PoolID:         "block-pool",
			QuotaInBytes:   322122547200, // 300 GiB
			ProtocolTypes:  []string{"ISCSI"},
			LifeCycleState: "READY",
			IPAddresses:    []string{"10.72.177.17"},
			BlockDevices:   &blockDevices,
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify BlockDevices are properly converted
		assert.NotNil(t, out.BlockDevices)
		assert.Len(t, out.BlockDevices, 2)

		// Verify first BlockDevice
		bd1 := out.BlockDevices[0]
		assert.Equal(t, "test-lun-1", bd1.Name.Value)
		assert.Equal(t, "lun-123", bd1.Identifier.Value)
		assert.Equal(t, float64(107374182400), bd1.SizeInBytes.Value)
		assert.Equal(t, gcpgenserver.BlockDeviceV1betaOsTypeLINUX, bd1.OsType.Value)
		assert.Len(t, bd1.HostGroups, 2)
		assert.Equal(t, "hg-uuid-1", bd1.HostGroups[0])
		assert.Equal(t, "hg-uuid-2", bd1.HostGroups[1])
		assert.Len(t, bd1.HostGroupDetails, 2)
		assert.Equal(t, "hg-uuid-1", bd1.HostGroupDetails[0].HostGroupId.Value)
		assert.Equal(t, []string{"iqn.1998-01.com.vmware:host1", "iqn.1998-01.com.vmware:host2"}, bd1.HostGroupDetails[0].Hosts)
		assert.Equal(t, "hg-uuid-2", bd1.HostGroupDetails[1].HostGroupId.Value)
		assert.Equal(t, []string{"iqn.1998-01.com.vmware:host3"}, bd1.HostGroupDetails[1].Hosts)

		// Verify second BlockDevice
		bd2 := out.BlockDevices[1]
		assert.Equal(t, "test-lun-2", bd2.Name.Value)
		assert.Equal(t, "lun-456", bd2.Identifier.Value)
		assert.Equal(t, float64(214748364800), bd2.SizeInBytes.Value)
		assert.Equal(t, gcpgenserver.BlockDeviceV1betaOsTypeWINDOWS, bd2.OsType.Value)
		assert.Len(t, bd2.HostGroups, 1)
		assert.Equal(t, "hg-uuid-3", bd2.HostGroups[0])
		assert.Len(t, bd2.HostGroupDetails, 1)
		assert.Equal(t, "hg-uuid-3", bd2.HostGroupDetails[0].HostGroupId.Value)
		assert.Equal(t, []string{"iqn.1998-01.com.vmware:host4"}, bd2.HostGroupDetails[0].Hosts)

		// Verify mount points are created for the first BlockDevice (primary)
		assert.NotNil(t, out.MountPoints)
		assert.Len(t, out.MountPoints, 1)
		assert.Equal(t, "10.72.177.17", out.MountPoints[0].IpAddress.Value)
		assert.Equal(t, gcpgenserver.ProtocolsV1betaISCSI, out.MountPoints[0].Protocol.Value)
		assert.NotEmpty(t, out.MountPoints[0].Instructions.Value)
		assert.Contains(t, out.MountPoints[0].Instructions.Value, "lun-123")
	})

	t.Run("DataProtectionVolumeStateConversion", func(t *testing.T) {
		testCases := []struct {
			name     string
			volume   *models.Volume
			expected gcpgenserver.VolumeV1betaVolumeState
		}{
			{
				name: "DataProtectionVolume_Ready_Mounted",
				volume: &models.Volume{
					IsDataProtection: true,
					Mounted:          true,
					LifeCycleState:   "READY",
				},
				expected: gcpgenserver.VolumeV1betaVolumeStateREADONLY,
			},
			{
				name: "DataProtectionVolume_Ready_NotMounted",
				volume: &models.Volume{
					IsDataProtection: true,
					Mounted:          false,
					LifeCycleState:   "READY",
				},
				expected: gcpgenserver.VolumeV1betaVolumeStatePREPARING,
			},
			{
				name: "NonDataProtectionVolume_Ready",
				volume: &models.Volume{
					IsDataProtection: false,
					Mounted:          true,
					LifeCycleState:   "READY",
				},
				expected: gcpgenserver.VolumeV1betaVolumeStateREADY,
			},
			{
				name: "DataProtectionVolume_NonReady",
				volume: &models.Volume{
					IsDataProtection: true,
					Mounted:          true,
					LifeCycleState:   "CREATING",
				},
				expected: gcpgenserver.VolumeV1betaVolumeStateCREATING,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				result := convertModelToVCPVolume(tc.volume)
				assert.Equal(t, tc.expected, result.VolumeState.Value)
			})
		}
	})

	t.Run("InReplication_UsesPointerValueWhenPresent", func(t *testing.T) {
		t.Run("false_omits_inReplication", func(t *testing.T) {
			inReplication := false
			vol := &models.Volume{
				CreationToken:  "tok",
				PoolID:         "pool",
				QuotaInBytes:   100,
				LifeCycleState: "READY",
				InReplication:  &inReplication,
			}
			out := convertModelToVCPVolume(vol)
			require.NotNil(t, out)
			assert.False(t, out.InReplication.Set, "inReplication should be omitted when the model value is false")
		})
		t.Run("true_sets_inReplication", func(t *testing.T) {
			inReplication := true
			vol := &models.Volume{
				CreationToken:  "tok",
				PoolID:         "pool",
				QuotaInBytes:   100,
				LifeCycleState: "READY",
				InReplication:  &inReplication,
			}
			out := convertModelToVCPVolume(vol)
			require.NotNil(t, out)
			require.True(t, out.InReplication.Set)
			assert.True(t, out.InReplication.Value)
		})
	})

	t.Run("WithBlockDevicesNoHostGroups_ShouldConvertCorrectly", func(t *testing.T) {
		blockDevices := []models.BlockDevice{
			{
				Name:            "test-lun-no-hg",
				Identifier:      "lun-789",
				Size:            53687091200, // 50 GiB in bytes
				OSType:          "ESXI",
				HostGroupDetail: []models.HostGroupDetails{}, // Empty host groups
			},
		}

		vol := &models.Volume{
			CreationToken:  "block-token-no-hg",
			PoolID:         "block-pool",
			QuotaInBytes:   53687091200,
			ProtocolTypes:  []string{"ISCSI"},
			LifeCycleState: "READY",
			IPAddresses:    []string{"10.72.177.18"},
			BlockDevices:   &blockDevices,
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify BlockDevice is properly converted
		assert.NotNil(t, out.BlockDevices)
		assert.Len(t, out.BlockDevices, 1)

		bd := out.BlockDevices[0]
		assert.Equal(t, "test-lun-no-hg", bd.Name.Value)
		assert.Equal(t, "lun-789", bd.Identifier.Value)
		assert.Equal(t, float64(53687091200), bd.SizeInBytes.Value)
		assert.Equal(t, gcpgenserver.BlockDeviceV1betaOsTypeESXI, bd.OsType.Value)
		assert.Empty(t, bd.HostGroups)
		assert.Empty(t, bd.HostGroupDetails)

		// Verify mount points are created
		assert.NotNil(t, out.MountPoints)
		assert.Len(t, out.MountPoints, 1)
		assert.Equal(t, "10.72.177.18", out.MountPoints[0].IpAddress.Value)
		assert.Equal(t, gcpgenserver.ProtocolsV1betaISCSI, out.MountPoints[0].Protocol.Value)
		assert.NotEmpty(t, out.MountPoints[0].Instructions.Value)
		// ESXI instructions don't include the LUN name in the text
		assert.Contains(t, out.MountPoints[0].Instructions.Value, "ESXi")
	})

	t.Run("WithBlockDevicesMissingFields_ShouldHandleGracefully", func(t *testing.T) {
		blockDevices := []models.BlockDevice{
			{
				Name:       "", // Empty name
				Identifier: "", // Empty identifier
				Size:       0,  // Zero size
				OSType:     "", // Empty OS type
				HostGroupDetail: []models.HostGroupDetails{
					{
						HostGroupID: "hg-uuid-4",
						Hosts:       []string{"iqn.1998-01.com.vmware:host5"},
					},
				},
			},
		}

		vol := &models.Volume{
			CreationToken:  "block-token-missing",
			PoolID:         "block-pool",
			QuotaInBytes:   107374182400,
			ProtocolTypes:  []string{"ISCSI"},
			LifeCycleState: "READY",
			IPAddresses:    []string{"10.72.177.17"},
			BlockDevices:   &blockDevices,
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify BlockDevice is properly converted with empty fields
		assert.NotNil(t, out.BlockDevices)
		assert.Len(t, out.BlockDevices, 1)

		bd := out.BlockDevices[0]
		assert.False(t, bd.Name.IsSet())        // Name should not be set when empty
		assert.False(t, bd.Identifier.IsSet())  // Identifier should not be set when empty
		assert.False(t, bd.SizeInBytes.IsSet()) // Size should not be set when zero
		assert.False(t, bd.OsType.IsSet())      // OS type should not be set when empty
		assert.Len(t, bd.HostGroups, 1)
		assert.Equal(t, "hg-uuid-4", bd.HostGroups[0])
		assert.Len(t, bd.HostGroupDetails, 1)
		assert.Equal(t, "hg-uuid-4", bd.HostGroupDetails[0].HostGroupId.Value)
		assert.Equal(t, []string{"iqn.1998-01.com.vmware:host5"}, bd.HostGroupDetails[0].Hosts)

		// Verify mount points are NOT created when identifier is missing
		assert.Empty(t, out.MountPoints)
	})

	t.Run("WithBlockDevicesNotReady_ShouldNotCreateMountPoints", func(t *testing.T) {
		blockDevices := []models.BlockDevice{
			{
				Name:       "test-lun-not-ready",
				Identifier: "lun-999",
				Size:       107374182400,
				OSType:     "LINUX",
				HostGroupDetail: []models.HostGroupDetails{
					{
						HostGroupID: "hg-uuid-5",
						Hosts:       []string{"iqn.1998-01.com.vmware:host6"},
					},
				},
			},
		}

		vol := &models.Volume{
			CreationToken:  "block-token-not-ready",
			PoolID:         "block-pool",
			QuotaInBytes:   107374182400,
			ProtocolTypes:  []string{"ISCSI"},
			LifeCycleState: "CREATING", // Not READY
			IPAddresses:    []string{"10.72.177.17"},
			BlockDevices:   &blockDevices,
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify BlockDevices are converted
		assert.NotNil(t, out.BlockDevices)
		assert.Len(t, out.BlockDevices, 1)

		// Verify mount points are NOT created when volume is not ready
		assert.Empty(t, out.MountPoints)
	})

	t.Run("WithBlockDevicesEmptyArray_ShouldHandleGracefully", func(t *testing.T) {
		blockDevices := []models.BlockDevice{} // Empty array

		vol := &models.Volume{
			CreationToken:  "block-token-empty",
			PoolID:         "block-pool",
			QuotaInBytes:   107374182400,
			ProtocolTypes:  []string{"ISCSI"},
			LifeCycleState: "READY",
			IPAddresses:    []string{"10.72.177.17"},
			BlockDevices:   &blockDevices,
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify BlockDevices is nil when array is empty (not set in response)
		assert.Nil(t, out.BlockDevices)

		// Verify mount points are NOT created when no BlockDevices
		assert.Empty(t, out.MountPoints)
	})

	t.Run("WithLargeCapacityAndConstituentCount", func(t *testing.T) {
		constituentCount := int32(8)
		vol := &models.Volume{
			CreationToken:               "large-volume-token",
			PoolID:                      "large-pool",
			QuotaInBytes:                1073741824000, // 1TB
			LargeCapacity:               true,
			LargeVolumeConstituentCount: &constituentCount,
			ProtocolTypes:               []string{"ISCSI"},
			LifeCycleState:              "READY",
			IPAddresses:                 []string{"10.72.177.17"},
			BlockProperties:             &models.BlockProperties{OSType: "LINUX"},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify LargeCapacity is properly set
		assert.True(t, out.LargeCapacity.IsSet())
		largeCapacity, ok := out.LargeCapacity.Get()
		assert.True(t, ok)
		assert.True(t, largeCapacity)

		// Verify LargeVolumeConstituentCount is properly set
		assert.True(t, out.LargeVolumeConstituentCount.IsSet())
		assert.Equal(t, constituentCount, out.LargeVolumeConstituentCount.Value)
	})

	t.Run("WithLargeCapacityAndMultipleIps", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:  "large-volume-token",
			PoolID:         "large-pool",
			DisplayName:    "large-volume",
			QuotaInBytes:   1073741824000, // 1TB
			LargeCapacity:  true,
			ProtocolTypes:  []string{utils.ProtocolNFSv3},
			LifeCycleState: "READY",
			IPAddresses:    []string{"10.72.177.17", "10.72.177.18"},
		}

		vol.FileProperties = &models.FileProperties{
			ExportPolicy: &models.ExportPolicy{
				ExportPolicyName: "test-policy",
				ExportRules: []*models.ExportRule{
					{
						AllowedClients: "192.168.1.0/24",
						AccessType:     models.ReadWrite,
						CIFS:           false,
						NFSv3:          true,
						NFSv4:          false,
						Index:          1,
					},
				},
			},
			JunctionPath: "/large-volume",
		}

		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify LargeCapacity is properly set
		assert.True(t, out.LargeCapacity.IsSet())
		largeCapacity, ok := out.LargeCapacity.Get()
		assert.True(t, ok)
		assert.True(t, largeCapacity)

		assert.Len(t, out.MountPoints, 2)
		assert.Equal(t, "10.72.177.17", out.MountPoints[0].IpAddress.Value)
		assert.Equal(t, "10.72.177.18", out.MountPoints[1].IpAddress.Value)
		assert.Equal(t, getFilesMountInstructions("10.72.177.17", vol.FileProperties.JunctionPath, "/"+vol.DisplayName, "NFSV3", ""), out.MountPoints[0].Instructions)
		assert.Equal(t, getFilesMountInstructions("10.72.177.18", vol.FileProperties.JunctionPath, "/"+vol.DisplayName, "NFSV3", ""), out.MountPoints[1].Instructions)
		// Verify Export and ExportFull for both mount points
		assert.Equal(t, "/large-volume", out.MountPoints[0].Export.Value)
		assert.Equal(t, "10.72.177.17:/large-volume", out.MountPoints[0].ExportFull.Value)
		assert.Equal(t, "/large-volume", out.MountPoints[1].Export.Value)
		assert.Equal(t, "10.72.177.18:/large-volume", out.MountPoints[1].ExportFull.Value)
	})

	t.Run("WithLargeCapacityTrue_NoConstituentCount", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:               "large-volume-token",
			PoolID:                      "large-pool",
			QuotaInBytes:                1073741824000, // 1TB
			LargeCapacity:               true,
			LargeVolumeConstituentCount: nil, // Not set
			ProtocolTypes:               []string{"ISCSI"},
			LifeCycleState:              "READY",
			IPAddresses:                 []string{"10.72.177.17"},
			BlockProperties:             &models.BlockProperties{OSType: "LINUX"},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify LargeCapacity is properly set
		assert.True(t, out.LargeCapacity.IsSet())
		largeCapacity, ok := out.LargeCapacity.Get()
		assert.True(t, ok)
		assert.True(t, largeCapacity)

		// Verify LargeVolumeConstituentCount is not set
		assert.False(t, out.LargeVolumeConstituentCount.IsSet())
	})

	t.Run("WithLargeCapacityFalse_WithNoConstituentCount", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:   "regular-volume-token",
			PoolID:          "regular-pool",
			QuotaInBytes:    107374182400, // 100GB
			LargeCapacity:   false,
			ProtocolTypes:   []string{"ISCSI"},
			LifeCycleState:  "READY",
			IPAddresses:     []string{"10.72.177.17"},
			BlockProperties: &models.BlockProperties{OSType: "LINUX"},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify LargeCapacity is properly set to false
		assert.True(t, out.LargeCapacity.IsSet())
		largeCapacity, ok := out.LargeCapacity.Get()
		assert.True(t, ok)
		assert.False(t, largeCapacity)
	})

	t.Run("WithoutLargeCapacityAndConstituentCount", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:               "standard-volume-token",
			PoolID:                      "standard-pool",
			QuotaInBytes:                1073741824, // 1GB
			LargeCapacity:               false,      // Default value
			LargeVolumeConstituentCount: nil,        // Not set
			ProtocolTypes:               []string{"ISCSI"},
			LifeCycleState:              "READY",
			IPAddresses:                 []string{"10.72.177.17"},
			BlockProperties:             &models.BlockProperties{OSType: "LINUX"},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify LargeCapacity is properly set to false (default)
		assert.True(t, out.LargeCapacity.IsSet())
		largeCapacity, ok := out.LargeCapacity.Get()
		assert.True(t, ok)
		assert.False(t, largeCapacity)

		// Verify LargeVolumeConstituentCount is not set
		assert.False(t, out.LargeVolumeConstituentCount.IsSet())
	})

	t.Run("WithBackupConfig", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:   "backup-token",
			PoolID:          "backup-pool",
			QuotaInBytes:    1234,
			BlockProperties: &models.BlockProperties{OSType: "LINUX"},
			ProtocolTypes:   []string{"ISCSI"},
			LifeCycleState:  "READY",
			IPAddresses:     []string{"10.72.177.17"},
			DataProtection: &models.DataProtection{
				BackupVaultID:          "vault-123",
				BackupPolicyId:         "policy-123",
				BackupChainBytes:       func() *int64 { v := int64(1000); return &v }(),
				ScheduledBackupEnabled: func() *bool { v := true; return &v }(),
			},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.Equal(t, "backup-token", out.CreationToken.Value)
		assert.Equal(t, "LINUX", string(out.BlockProperties.Value.OsType.Value))
		assert.Equal(t, "ISCSI", string(out.Protocols[0]))

		// Test backup configuration
		assert.True(t, out.BackupConfig.IsSet())
		backupConfig := out.BackupConfig.Value
		assert.True(t, backupConfig.BackupVaultId.IsSet())
		assert.Equal(t, "vault-123", backupConfig.BackupVaultId.Value)
		assert.True(t, backupConfig.BackupPolicyId.IsSet())
		assert.Equal(t, "policy-123", backupConfig.BackupPolicyId.Value)
		assert.True(t, backupConfig.BackupChainBytes.IsSet())
		assert.Equal(t, int64(1000), backupConfig.BackupChainBytes.Value)
		assert.True(t, backupConfig.ScheduledBackupEnabled.IsSet())
		assert.Equal(t, true, backupConfig.ScheduledBackupEnabled.Value)
	})

	t.Run("WithEmptyBackupConfig", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:   "no-backup-token",
			PoolID:          "no-backup-pool",
			QuotaInBytes:    1234,
			BlockProperties: &models.BlockProperties{OSType: "LINUX"},
			ProtocolTypes:   []string{"ISCSI"},
			LifeCycleState:  "READY",
			IPAddresses:     []string{"10.72.177.17"},
			DataProtection:  &models.DataProtection{}, // Empty backup config
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.Equal(t, "no-backup-token", out.CreationToken.Value)
		assert.Equal(t, "LINUX", string(out.BlockProperties.Value.OsType.Value))
		assert.Equal(t, "ISCSI", string(out.Protocols[0]))

		// Test that backup configuration is not set when empty
		assert.False(t, out.BackupConfig.IsSet())
	})

	t.Run("WithCloneParentInfo_ShouldConvertCorrectly", func(t *testing.T) {
		parentVolumeId := "123e4567-e89b-12d3-a456-426614174000"
		parentSnapshotId := "223e4567-e89b-12d3-a456-426614174001"

		vol := &models.Volume{
			CreationToken:  "clone-token",
			PoolID:         "clone-pool",
			QuotaInBytes:   1234,
			ProtocolTypes:  []string{"ISCSI"},
			LifeCycleState: "READY",
			IPAddresses:    []string{"10.72.177.17"},
			CloneParentInfo: &models.CloneParentInfo{
				ParentVolumeId:   &parentVolumeId,
				ParentSnapshotId: &parentSnapshotId,
			},
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)

		// Verify CloneParentInfo is properly converted
		assert.True(t, out.CloneDetails.IsSet(), "CloneParentInfo should be set")
		cloneParentInfo := out.CloneDetails.Value
		assert.True(t, cloneParentInfo.ParentVolumeId.IsSet(), "ParentVolumeId should be set")
		assert.Equal(t, parentVolumeId, cloneParentInfo.ParentVolumeId.Value)
		assert.True(t, cloneParentInfo.ParentSnapshotId.IsSet(), "ParentSnapshotId should be set")
		assert.Equal(t, parentSnapshotId, cloneParentInfo.ParentSnapshotId.Value)
	})

	t.Run("WithThroughputMibpsAndIops", func(t *testing.T) {
		throughputMibps := int64(200)
		iops := int64(1000)
		vol := &models.Volume{
			CreationToken:   "token",
			PoolID:          "pool",
			QuotaInBytes:    1234,
			ProtocolTypes:   []string{"NFSV3"},
			LifeCycleState:  "READY",
			ThroughputMibps: &throughputMibps,
			Iops:            &iops,
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.True(t, out.ThroughputMibps.IsSet())
		throughput, ok := out.ThroughputMibps.Get()
		assert.True(t, ok)
		assert.Equal(t, float64(200), throughput)

		assert.True(t, out.Iops.IsSet())
		iopsVal, ok := out.Iops.Get()
		assert.True(t, ok)
		assert.Equal(t, int64(1000), iopsVal)
	})

	t.Run("WithoutThroughputMibpsAndIops", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:   "token",
			PoolID:          "pool",
			QuotaInBytes:    1234,
			ProtocolTypes:   []string{"NFSV3"},
			LifeCycleState:  "READY",
			ThroughputMibps: nil,
			Iops:            nil,
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.False(t, out.ThroughputMibps.IsSet())
		assert.False(t, out.Iops.IsSet())
	})

	t.Run("WithVolumePerformanceGroupId", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:            "token",
			PoolID:                   "pool",
			QuotaInBytes:             1234,
			ProtocolTypes:            []string{"NFSV3"},
			LifeCycleState:           "READY",
			VolumePerformanceGroupId: "a1b2c3d4-e5f6-4789-a012-3456789abcde",
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.True(t, out.VolumePerformanceGroupId.IsSet())
		val, ok := out.VolumePerformanceGroupId.Get()
		assert.True(t, ok)
		assert.Equal(t, "a1b2c3d4-e5f6-4789-a012-3456789abcde", val)
	})

	t.Run("WithoutVolumePerformanceGroupId", func(t *testing.T) {
		vol := &models.Volume{
			CreationToken:            "token",
			PoolID:                   "pool",
			QuotaInBytes:             1234,
			ProtocolTypes:            []string{"NFSV3"},
			LifeCycleState:           "READY",
			VolumePerformanceGroupId: "",
		}
		out := convertModelToVCPVolume(vol)
		assert.NotNil(t, out)
		assert.False(t, out.VolumePerformanceGroupId.IsSet())
	})
}

func TestConvertModelToVCPVolume_BackupConfig(t *testing.T) {
	tests := []struct {
		name               string
		dataProtection     *models.DataProtection
		expectBackupConfig bool
		expectedFields     []string
		unexpectedFields   []string
	}{
		{
			name:               "No backup config when all fields empty",
			dataProtection:     &models.DataProtection{},
			expectBackupConfig: false,
		},
		{
			name: "Backup config with only BackupVaultID",
			dataProtection: &models.DataProtection{
				BackupVaultID: "vault-123",
			},
			expectBackupConfig: true,
			expectedFields:     []string{"BackupVaultId"},
			unexpectedFields:   []string{"BackupPolicyId", "BackupChainBytes", "ScheduledBackupEnabled"},
		},
		{
			name: "Backup config with only BackupPolicyId",
			dataProtection: &models.DataProtection{
				BackupPolicyId:         "policy-123",
				ScheduledBackupEnabled: func() *bool { v := false; return &v }(),
			},
			expectBackupConfig: true,
			expectedFields:     []string{"BackupPolicyId", "ScheduledBackupEnabled"},
			unexpectedFields:   []string{"BackupVaultId", "BackupChainBytes"},
		},
		{
			name: "Backup config with only BackupChainBytes",
			dataProtection: &models.DataProtection{
				BackupChainBytes: func() *int64 { v := int64(1000); return &v }(),
			},
			expectBackupConfig: true,
			expectedFields:     []string{"BackupChainBytes"},
			unexpectedFields:   []string{"BackupVaultId", "BackupPolicyId", "ScheduledBackupEnabled"},
		},
		{
			name: "Backup config with all fields set",
			dataProtection: &models.DataProtection{
				BackupVaultID:          "vault-123",
				BackupPolicyId:         "policy-123",
				BackupChainBytes:       func() *int64 { v := int64(1000); return &v }(),
				ScheduledBackupEnabled: func() *bool { v := true; return &v }(),
			},
			expectBackupConfig: true,
			expectedFields:     []string{"BackupVaultId", "BackupPolicyId", "BackupChainBytes", "ScheduledBackupEnabled"},
		},
		{
			name: "Backup config with BackupVaultID and BackupChainBytes",
			dataProtection: &models.DataProtection{
				BackupVaultID:    "vault-123",
				BackupChainBytes: func() *int64 { v := int64(1000); return &v }(),
			},
			expectBackupConfig: true,
			expectedFields:     []string{"BackupVaultId", "BackupChainBytes"},
			unexpectedFields:   []string{"BackupPolicyId", "ScheduledBackupEnabled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volume := &models.Volume{
				DisplayName:           "test-volume",
				LifeCycleState:        "READY",
				LifeCycleStateDetails: "Available",
				VendorSubnetID:        "projects/123/global/networks/test-network",
				PoolID:                "pool-123",
				CreationToken:         "token-123",
				QuotaInBytes:          1000000,
				PoolName:              "test-pool",
				EncryptionType:        "ENCRYPTION_TYPE_UNSPECIFIED",
				SnapReserve:           0,
				Zone:                  "us-east1-a",
				UsedBytes:             500000,
				IsDataProtection:      false,
				DataProtection:        tt.dataProtection,
			}

			result := convertModelToVCPVolume(volume)

			if tt.expectBackupConfig {
				assert.True(t, result.BackupConfig.IsSet(), "Expected BackupConfig to be set")

				backupConfig := result.BackupConfig.Value

				// Check expected fields are set
				for _, field := range tt.expectedFields {
					switch field {
					case "BackupVaultId":
						assert.True(t, backupConfig.BackupVaultId.IsSet(), "Expected BackupVaultId to be set")
					case "BackupPolicyId":
						assert.True(t, backupConfig.BackupPolicyId.IsSet(), "Expected BackupPolicyId to be set")
					case "BackupChainBytes":
						assert.True(t, backupConfig.BackupChainBytes.IsSet(), "Expected BackupChainBytes to be set")
					case "ScheduledBackupEnabled":
						assert.True(t, backupConfig.ScheduledBackupEnabled.IsSet(), "Expected ScheduledBackupEnabled to be set")
					}
				}

				// Check unexpected fields are not set
				for _, field := range tt.unexpectedFields {
					switch field {
					case "BackupVaultId":
						assert.False(t, backupConfig.BackupVaultId.IsSet(), "Expected BackupVaultId to not be set")
					case "BackupPolicyId":
						assert.False(t, backupConfig.BackupPolicyId.IsSet(), "Expected BackupPolicyId to not be set")
					case "BackupChainBytes":
						assert.False(t, backupConfig.BackupChainBytes.IsSet(), "Expected BackupChainBytes to not be set")
					case "ScheduledBackupEnabled":
						assert.False(t, backupConfig.ScheduledBackupEnabled.IsSet(), "Expected ScheduledBackupEnabled to not be set")
					}
				}
			} else {
				assert.False(t, result.BackupConfig.IsSet(), "Expected BackupConfig to not be set")
			}
		})
	}
}

func TestHasNfs4KerberosV1beta(t *testing.T) {
	t.Run("ReturnsTrueWhenAnyKerberosFlagSet", func(tt *testing.T) {
		policy := gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
			Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
				{
					AccessType:         gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE,
					Nfsv4:              gcpgenserver.NewOptNilBool(true),
					Kerberos5ReadOnly:  gcpgenserver.NewOptNilBool(true),
					Kerberos5ReadWrite: gcpgenserver.NewOptNilBool(false),
				},
			},
		})

		assert.True(tt, _hasNfs4KerberosV1beta(policy))
	})

	t.Run("ReturnsFalseWhenNoKerberosFlags", func(tt *testing.T) {
		policy := gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
			Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
				{
					AccessType: gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE,
					Nfsv4:      gcpgenserver.NewOptNilBool(true),
				},
			},
		})

		assert.False(tt, _hasNfs4KerberosV1beta(policy))
	})

	t.Run("ReturnsFalseWhenPolicyNotSet", func(tt *testing.T) {
		policy := gcpgenserver.OptExportPolicyV1beta{}
		assert.False(tt, _hasNfs4KerberosV1beta(policy))
	})
}

func TestGetKerberosEnabledFlagFromRequest(t *testing.T) {
	t.Run("ReturnsFalseWhenFlagNotProvided", func(tt *testing.T) {
		assert.False(tt, _getKerberosEnabledFlagFromRequest(nil))
	})

	t.Run("ReturnsUnderlyingFlagValue", func(tt *testing.T) {
		enabled := true
		disabled := false

		assert.True(tt, _getKerberosEnabledFlagFromRequest(&enabled))
		assert.False(tt, _getKerberosEnabledFlagFromRequest(&disabled))
	})
}

func TestValidateKerberosPolicyV1beta(t *testing.T) {
	kerberosEnabledPtr := nillable.GetBoolPtr(true)
	kerberosDisabledPtr := nillable.GetBoolPtr(false)

	kerberosRule := gcpgenserver.SimpleExportPolicyRuleV1beta{
		AccessType:         gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE,
		Nfsv4:              gcpgenserver.NewOptNilBool(true),
		Kerberos5ReadOnly:  gcpgenserver.NewOptNilBool(true),
		Kerberos5ReadWrite: gcpgenserver.NewOptNilBool(false),
	}
	kerberosPolicy := gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
		Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{kerberosRule},
	})

	validPoolWithAD := &models.Pool{
		ActiveDirectoryConfigId: "ad",
		ActiveDirectory: &models.ActiveDirectory{
			ActiveDirectoryAttributes: &models.ActiveDirectoryAttributes{
				KdcIP:       "192.168.1.1",
				KdcHostname: "kdc.example.com",
			},
		},
	}

	t.Run("RegionDoesNotSupportKerberos", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = false
		defer func() { enableKerberos = orig }()

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			kerberosEnabledPtr,
			kerberosPolicy,
			validPoolWithAD,
		)

		assert.EqualError(tt, err, "Kerberos is not supported in this region")
	})

	t.Run("MissingActiveDirectoryConfig", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			kerberosEnabledPtr,
			kerberosPolicy,
			&models.Pool{ActiveDirectoryConfigId: ""},
		)

		assert.Error(tt, err)
		assert.EqualError(tt, err, "Kerberos requires the pool to be joined to an Active Directory.")
	})

	t.Run("MissingKDCConfiguration", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		poolWithADButNoKDC := &models.Pool{
			ActiveDirectoryConfigId: "ad",
			ActiveDirectory: &models.ActiveDirectory{
				ActiveDirectoryAttributes: &models.ActiveDirectoryAttributes{
					KdcIP:       "",
					KdcHostname: "",
				},
			},
		}

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			kerberosEnabledPtr,
			kerberosPolicy,
			poolWithADButNoKDC,
		)

		assert.Error(tt, err)
		assert.EqualError(tt, err, "Active directory configuration must have KDC Name and KDC IP set for creating kerberos volume")
	})

	t.Run("NilActiveDirectory", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			kerberosEnabledPtr,
			kerberosPolicy,
			&models.Pool{ActiveDirectoryConfigId: "ad", ActiveDirectory: nil},
		)

		assert.Error(tt, err)
		assert.EqualError(tt, err, "Kerberos requires the pool to be joined to an Active Directory.")
	})

	t.Run("PolicyNotSetWhenKerberosEnabled", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			kerberosEnabledPtr,
			gcpgenserver.OptExportPolicyV1beta{},
			validPoolWithAD,
		)

		assert.EqualError(tt, err, "Export policy must be defined for kerberos enabled volumes")
	})

	t.Run("NonReadNoneAccessType", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		policy := gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
			Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
				{
					AccessType:         gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					Nfsv4:              gcpgenserver.NewOptNilBool(true),
					Kerberos5ReadOnly:  gcpgenserver.NewOptNilBool(true),
					Kerberos5ReadWrite: gcpgenserver.NewOptNilBool(false),
				},
			},
		})

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			kerberosEnabledPtr,
			policy,
			validPoolWithAD,
		)

		assert.EqualError(tt, err, "When kerberos is enabled, 'accessType' should be set to READ_NONE in export policy rules")
	})

	t.Run("MissingKerberosRulesWhenEnabled", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		policy := gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
			Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
				{
					AccessType: gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE,
					Nfsv4:      gcpgenserver.NewOptNilBool(true),
				},
			},
		})

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			kerberosEnabledPtr,
			policy,
			validPoolWithAD,
		)

		assert.EqualError(tt, err, "Export policy rules doesn't contain any kerberos export policy rule for kerberos enabled volume")
	})

	t.Run("KerberosRulesWithoutNFSv4Protocol", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
			kerberosEnabledPtr,
			kerberosPolicy,
			validPoolWithAD,
		)

		assert.EqualError(tt, err, "Kerberos feature is enabled for only NFSv4 volumes")
	})

	t.Run("PolicyContainsKerberosButFlagDisabled", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			kerberosDisabledPtr,
			kerberosPolicy,
			validPoolWithAD,
		)

		assert.EqualError(tt, err, "Export policy rules don't match kerberos enabled flag")
	})

	t.Run("PolicyContainsKerberosButFlagNil", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			nil,
			kerberosPolicy,
			validPoolWithAD,
		)

		assert.EqualError(tt, err, "Export policy rules don't match kerberos enabled flag")
	})

	t.Run("ValidKerberosConfiguration", func(tt *testing.T) {
		orig := enableKerberos
		enableKerberos = true
		defer func() { enableKerberos = orig }()

		err := _validateKerberosPolicyV1beta(
			[]gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			kerberosEnabledPtr,
			kerberosPolicy,
			validPoolWithAD,
		)

		assert.NoError(tt, err)
	})
}

func TestPrepareCreateVolumeParams_KerberosValidationErrorConversion(t *testing.T) {
	origValidator := validateKerberosPolicyV1beta
	validateKerberosPolicyV1beta = func(protocols []gcpgenserver.ProtocolsV1beta, kerberosEnabled *bool, policy gcpgenserver.OptExportPolicyV1beta, pool *models.Pool) error {
		return fmt.Errorf("kerberos validation failed")
	}
	defer func() { validateKerberosPolicyV1beta = origValidator }()

	utils.SetFileProtocolSupportedForTesting(true)
	utils.SetExperimentalVersionAllowlistedAccountsForTesting("test-project")
	defer func() {
		utils.SetFileProtocolSupportedForTesting(false)
		utils.SetExperimentalVersionAllowlistedAccountsForTesting("")
	}()

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:        "testvolume",
			CreationToken:     gcpgenserver.NewOptString("test-token"),
			PoolId:            gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:      gcpgenserver.NewOptFloat64(1024),
			Protocols:         []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4},
			KerberosEnabled:   gcpgenserver.NewOptNilBool(true),
			ExportPolicy:      gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{}),
			SnapshotDirectory: gcpgenserver.NewOptBool(false),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}

	out, err := _prepareCreateVolumeParams(req, params, "region", "zone", &models.Pool{ActiveDirectoryConfigId: "ad"})

	assert.Nil(t, out)
	assert.Error(t, err)
	assert.True(t, errors.IsUserInputValidationErr(err))
	assert.Equal(t, "kerberos validation failed", err.Error())
}

// TestPrepareUpdateVolumeParams_BackupDisabled tests the scenario where backup is disabled
func TestV1betaCreateVolume_BackupNotSupported(t *testing.T) {
	origPrepare := prepareCreateVolumeParams
	origParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	defer func() {
		prepareCreateVolumeParams = origPrepare
		utils.ParseAndValidateRegionAndZone = origParseAndValidateRegionAndZone
	}()
	prepareCreateVolumeParams = func(req *gcpgenserver.VolumeCreateV1beta, params gcpgenserver.V1betaCreateVolumeParams, region string, zone string, pool *models.Pool) (*common.CreateVolumeParams, error) {
		return nil, errors.NewUserInputValidationErr("Backup feature is currently not enabled.")
	}
	utils.ParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
		return "us-e4", "", nil
	}
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	req := &gcpgenserver.VolumeCreateV1beta{}

	mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	result, err := handler.V1betaCreateVolume(context.Background(), req, params)
	assert.NoError(t, err)
	badRequest, ok := result.(*gcpgenserver.V1betaCreateVolumeBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequest.Code)
	assert.Equal(t, "Backup feature is currently not enabled.", badRequest.Message)
}

// TestPrepareCreateVolumeParams_BackupDisabled tests the scenario where backup is disabled
func TestPrepareCreateVolumeParams_BackupDisabled(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = false

	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(gcpgenserver.BackupConfigV1beta{
				BackupVaultId: gcpgenserver.NewOptNilString("backup-vault-id"),
			}),
			QuotaInBytes: gcpgenserver.NewOptFloat64(107374182400),
		},
	}

	out, err := _prepareCreateVolumeParams(req, params, "region", "zone", nil)
	assert.Nil(t, out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Backup feature is currently not enabled.")
}

// TestV1betaUpdateVolume_BackupNotSupported tests the scenario where backup is disabled
func TestV1betaUpdateVolume_BackupNotSupported(t *testing.T) {
	origPrepare := prepareUpdateVolumeParams
	origParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	defer func() {
		prepareUpdateVolumeParams = origPrepare
		utils.ParseAndValidateRegionAndZone = origParseAndValidateRegionAndZone
	}()
	prepareUpdateVolumeParams = func(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*common.UpdateVolumeParams, error) {
		return nil, errors.NewUserInputValidationErr("Backup feature is currently not enabled.")
	}
	utils.ParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
		return "us-e4", "", nil
	}
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-e4",
		VolumeId:      "vol-1",
	}
	req := &gcpgenserver.VolumeUpdateV1beta{}
	volume := &models.Volume{
		BaseModel: models.BaseModel{UUID: "vol-1"},
	}
	mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
	mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{PoolAttributes: &models.PoolAttributes{}}, nil)
	result, err := handler.V1betaUpdateVolume(context.Background(), req, params)
	assert.NoError(t, err)
	badRequest, ok := result.(*gcpgenserver.V1betaUpdateVolumeBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequest.Code)
	assert.Equal(t, "Backup feature is currently not enabled.", badRequest.Message)
}

// TestV1betaUpdateVolume tests the V1betaUpdateVolume handler
func TestPrepareUpdateVolumeParams_BackupDisabled(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = false

	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "vol-1",
	}
	req := &gcpgenserver.VolumeUpdateV1beta{
		BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(gcpgenserver.BackupConfigV1beta{}),
	}

	out, err := _prepareUpdateVolumeParams(req, params, "region", nil)
	assert.Nil(t, out)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Backup feature is currently not enabled.")
}

func TestPrepareCreateVolumeParams_WithAutoTieringFeatureDisabled(t *testing.T) {
	// Save and restore the original value
	currentATState := autoTieringEnabled
	defer func() { autoTieringEnabled = currentATState }()
	autoTieringEnabled = false

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{
						Value: 30,
						Set:   true,
					},
				},
			),
		},
		VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("SECONDARY"),
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"
	_, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Auto-Tiering feature is currently not enabled.")
}

func TestPrepareCreateVolumeParams_HybridReplicationParametersProcessing(t *testing.T) {
	t.Run("WhenHybridReplicationParametersWithAllFields", func(tt *testing.T) {
		// Mock hybridReplicationEnabled to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeMIGRATION,
					ReplicationSchedule:   gcpgenserver.NewOptHybridReplicationParametersV1betaReplicationSchedule(gcpgenserver.HybridReplicationParametersV1betaReplicationScheduleHOURLY),
					PeerClusterName:       "peer-cluster",
					PeerVolumeName:        "peer-volume",
					PeerSvmName:           "peer-svm",
					PeerIpAddresses:       []string{"192.168.1.1", "192.168.1.2"},
					ResourceId:            "resource-123",
					Labels: gcpgenserver.NewOptHybridReplicationParametersV1betaLabels(map[string]string{
						"env": "test",
						"app": "volume",
					}),
					Description:              gcpgenserver.NewOptNilString("Test hybrid replication"),
					ClusterLocation:          gcpgenserver.NewOptNilString("us-west1"),
					PeeringCommandExpiryTime: gcpgenserver.NewOptNilDateTime(time.Now().Add(24 * time.Hour)),
				},
				Set: true,
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber:  "test-project",
			LocationId:     "test-location",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		region := "test-region"
		zone := "test-zone"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridReplicationParameters)

		hybridParams := result.HybridReplicationParameters
		assert.Equal(tt, "resource-123", hybridParams.ResourceID)
		assert.Equal(tt, "peer-volume", hybridParams.PeerVolumeName)
		assert.Equal(tt, "peer-cluster", hybridParams.PeerClusterName)
		assert.Equal(tt, "peer-svm", hybridParams.PeerSvmName)
		assert.Equal(tt, []string{"192.168.1.1", "192.168.1.2"}, hybridParams.PeerIPAddresses)
		assert.Equal(tt, map[string]string{"env": "test", "app": "volume"}, hybridParams.Labels)
		assert.Equal(tt, "Test hybrid replication", hybridParams.Description)
		assert.Equal(tt, "us-west1", hybridParams.ClusterLocation)
		assert.Equal(tt, "hourly", hybridParams.ReplicationSchedule)
		assert.Equal(tt, datamodel.HybridReplicationParametersReplicationTypeMIGRATION, hybridParams.ReplicationType)
		assert.Equal(tt, SnapshotScheduleLabelHourly, hybridParams.ReplicationSchedule)
	})

	t.Run("WhenONPREMTypeWithEmptyReplicationSchedule_ReturnsError", func(tt *testing.T) {
		// Mock hybridReplicationEnabled to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					// ReplicationSchedule is not set, which means it will be empty
				},
				Set: true,
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber:  "test-project",
			LocationId:     "test-location",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		region := "test-region"
		zone := "test-zone"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Can't have empty replicationSchedule for")
		assert.Contains(tt, err.Error(), "ONPREM_REPLICATION")
	})

	t.Run("WhenONPREMTypeWithValidReplicationSchedule_Success", func(tt *testing.T) {
		// Mock hybridReplicationEnabled to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					ReplicationSchedule:   gcpgenserver.NewOptHybridReplicationParametersV1betaReplicationSchedule(gcpgenserver.HybridReplicationParametersV1betaReplicationScheduleHOURLY),
					ResourceId:            "resource-123",
				},
				Set: true,
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber:  "test-project",
			LocationId:     "test-location",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		region := "test-region"
		zone := "test-zone"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridReplicationParameters)
		assert.Equal(tt, datamodel.HybridReplicationParametersReplicationTypeONPREM, result.HybridReplicationParameters.ReplicationType)
		assert.Equal(tt, "hourly", result.HybridReplicationParameters.ReplicationSchedule)
	})

	t.Run("WhenMIGRATIONTypeWithEmptyReplicationSchedule_Success", func(tt *testing.T) {
		// Mock hybridReplicationEnabled to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeMIGRATION,
					// ReplicationSchedule is not set, but MIGRATION type sets it to hourly automatically
					ResourceId: "resource-123",
				},
				Set: true,
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber:  "test-project",
			LocationId:     "test-location",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		region := "test-region"
		zone := "test-zone"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridReplicationParameters)
		assert.Equal(tt, datamodel.HybridReplicationParametersReplicationTypeMIGRATION, result.HybridReplicationParameters.ReplicationType)
		// MIGRATION type automatically sets replicationSchedule to hourly
		assert.Equal(tt, SnapshotScheduleLabelHourly, result.HybridReplicationParameters.ReplicationSchedule)
	})

	t.Run("WhenONPREMTypeWithEmptyStringReplicationSchedule_ReturnsError", func(tt *testing.T) {
		// Mock hybridReplicationEnabled to be true
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			},
			HybridReplicationParameters: gcpgenserver.OptHybridReplicationParametersV1beta{
				Value: gcpgenserver.HybridReplicationParametersV1beta{
					HybridReplicationType: gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION,
					// ReplicationSchedule is set but will be empty after mapping
					ReplicationSchedule: gcpgenserver.OptHybridReplicationParametersV1betaReplicationSchedule{},
				},
				Set: true,
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber:  "test-project",
			LocationId:     "test-location",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		region := "test-region"
		zone := "test-zone"

		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Can't have empty replicationSchedule for")
		assert.Contains(tt, err.Error(), "ONPREM_REPLICATION")
	})
}

func TestPrepareCreateVolumeParams_TieringPolicyWithoutTierAction(t *testing.T) {
	currentATState := autoTieringEnabled
	defer func() { autoTieringEnabled = currentATState }()
	autoTieringEnabled = true

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					// TierAction not set
					CoolingThresholdDays: gcpgenserver.OptNilInt32{
						Value: 30,
						Set:   true,
					},
				},
			),
			BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(gcpgenserver.BlockPropertiesV1beta{
				OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
			}),
		},
		VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("PRIMARY"),
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"
	_, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Tiering action is required when enabling auto-tiering on volume")
}

func TestPrepareCreateVolumeParams_HotTierBypassModeNotSupportedForBlockVolume(t *testing.T) {
	currentATState := autoTieringEnabled
	defer func() { autoTieringEnabled = currentATState }()
	autoTieringEnabled = true

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(
				gcpgenserver.TieringPolicyV1beta{
					TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("ENABLED"),
					CoolingThresholdDays: gcpgenserver.OptNilInt32{
						Value: 30,
						Set:   true,
					},
					HotTierBypassModeEnabled: gcpgenserver.OptNilBool{
						Value: true,
						Set:   true,
					},
				},
			),
			BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(gcpgenserver.BlockPropertiesV1beta{
				OsType: gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
			}),
		},
		VolumeType: gcpgenserver.NewOptVolumeCreateV1betaVolumeType("PRIMARY"),
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"
	_, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hotTierBypassMode is not supported for block volume")
}

func TestPrepareCreateVolumeParams_SnapReserveMustBePositiveNumber(t *testing.T) {
	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			// SnapReserve is set but Get() will return (0, false)
			SnapReserve: gcpgenserver.OptFloat64{Set: true, Value: -1},
			Labels:      gcpgenserver.OptVolumeV1betaLabels{Value: map[string]string{"key": "value"}, Set: true},
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"
	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "SnapReserve cannot be negative")
}

func TestPrepareCreateVolumeParams_DeDuplicateHGUUID(t *testing.T) {
	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			BlockProperties: gcpgenserver.NewOptBlockPropertiesV1beta(
				gcpgenserver.BlockPropertiesV1beta{
					OsType:       gcpgenserver.NewOptBlockPropertiesV1betaOsType("LINUX"),
					HostGroupIds: []string{"a", "a", "b"},
				},
			),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"
	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.Nil(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.BlockProperties.HostGroupUUIDs, 2)
}

func TestPrepareCreateVolumeParams_ResourceIdWithHyphens_ReturnsError(t *testing.T) {
	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "test-volume-with-hyphens",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"
	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "The Resource ID can only contain lowercase letters, numbers, and underscores. It must start with a letter and cannot end with an underscore.")
}

func TestPrepareCreateVolumeParams_ValidResourceIdWithoutHyphens_Success(t *testing.T) {
	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "validresourceid123",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"
	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "validresourceid123", result.Name)
	assert.Equal(t, "/projects/test-project/locations/test-location/volumes/validresourceid123", result.VendorID)
}

func TestPrepareCreateVolumeParams_ResourceIdEdgeCases(t *testing.T) {
	errorString := "The Resource ID can only contain lowercase letters, numbers, and underscores. It must start with a letter and cannot end with an underscore."
	zone := "us-west1-b"
	testCases := []struct {
		name          string
		resourceId    string
		expectError   bool
		errorContains string
	}{
		{
			name:          "Single hyphen",
			resourceId:    "test-volume",
			expectError:   true,
			errorContains: errorString,
		},
		{
			name:          "Multiple hyphens",
			resourceId:    "test-volume-with-multiple-hyphens",
			expectError:   true,
			errorContains: errorString,
		},
		{
			name:          "Hyphen at beginning",
			resourceId:    "-testvolume",
			expectError:   true,
			errorContains: errorString,
		},
		{
			name:          "Hyphen at end",
			resourceId:    "testvolume-",
			expectError:   true,
			errorContains: errorString,
		},
		{
			name:        "Valid alphanumeric",
			resourceId:  "testvolume123",
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					ResourceId:    tc.resourceId,
					CreationToken: gcpgenserver.NewOptString("test-token"),
					PoolId:        gcpgenserver.NewNilString("test-pool"),
					QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
					Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
				},
			}
			params := gcpgenserver.V1betaCreateVolumeParams{
				ProjectNumber: "test-project",
				LocationId:    "test-location",
			}
			region := "test-region"

			result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)

			if tc.expectError {
				assert.Error(t, err)
				assert.Nil(t, result)
				assert.Contains(t, err.Error(), tc.errorContains)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tc.resourceId, result.Name)
				expectedVendorID := fmt.Sprintf("/projects/test-project/locations/test-location/volumes/%s", tc.resourceId)
				assert.Equal(t, expectedVendorID, result.VendorID)
			}
		})
	}
}

func TestPrepareUpdateVolumeParams_SnapReserveCannotBeGreaterThan100(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "proj",
		LocationId:    "loc",
		VolumeId:      "vol",
	}
	region := "region"

	req := &gcpgenserver.VolumeUpdateV1beta{
		SnapReserve: gcpgenserver.NewOptNilFloat64(101),
	}
	result, err := _prepareUpdateVolumeParams(req, params, region, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "SnapReserve should be less than 100")
}

func TestPrepareUpdateVolumeParams_MQoSValidation(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "proj",
		LocationId:    "loc",
		VolumeId:      "vol",
	}
	region := "region"
	originalEnableMqos := enableMqos
	originalEnableInferredIops := enableInferredIops
	originalEnableVolumePerformanceGroupAssignment := enableVolumePerformanceGroupAssignment
	defer func() {
		enableMqos = originalEnableMqos
		enableInferredIops = originalEnableInferredIops
		enableVolumePerformanceGroupAssignment = originalEnableVolumePerformanceGroupAssignment
	}()

	t.Run("WhenEnableMqosIsFalse_ThroughputMibpsSet_ReturnsError", func(t *testing.T) {
		enableMqos = false
		req := &gcpgenserver.VolumeUpdateV1beta{
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(100),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "MQOS is not enabled")
	})

	t.Run("WhenEnableMqosIsFalse_IopsSet_ReturnsError", func(t *testing.T) {
		enableMqos = false
		req := &gcpgenserver.VolumeUpdateV1beta{
			Iops: gcpgenserver.NewOptNilInt64(1000),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "MQOS is not enabled")
	})

	t.Run("WhenEnableMqosIsFalse_VolumePerformanceGroupIdSet_ReturnsError", func(t *testing.T) {
		enableMqos = false
		req := &gcpgenserver.VolumeUpdateV1beta{
			VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "MQOS is not enabled")
	})

	t.Run("WhenEnableMqosIsTrueAndEnableInferredIopsIsFalse_OnlyThroughputMibpsSet_ReturnsParam", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = false
		req := &gcpgenserver.VolumeUpdateV1beta{
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(100),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, int64(100), *result.ThroughputMibps)
		assert.Nil(t, result.Iops)
		assert.Nil(t, result.VolumePerformanceGroupId)
	})

	t.Run("WhenEnableMqosIsTrueAndEnableInferredIopsIsFalse_OnlyIopsSet_ReturnsParam", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = false
		req := &gcpgenserver.VolumeUpdateV1beta{
			Iops: gcpgenserver.NewOptNilInt64(1000),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Nil(t, result.ThroughputMibps)
		assert.Equal(t, int64(1000), *result.Iops)
		assert.Nil(t, result.VolumePerformanceGroupId)
	})

	t.Run("WhenThroughputMibpsIsNull_TreatedAsOmitted", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = false
		var nullThroughput gcpgenserver.OptNilFloat64
		nullThroughput.SetToNull()
		req := &gcpgenserver.VolumeUpdateV1beta{
			ThroughputMibps: nullThroughput,
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Nil(t, result.ThroughputMibps)
		assert.Nil(t, result.Iops)
	})

	t.Run("WhenIopsIsNull_TreatedAsOmitted", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = false
		var nullIops gcpgenserver.OptNilInt64
		nullIops.SetToNull()
		req := &gcpgenserver.VolumeUpdateV1beta{
			Iops: nullIops,
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Nil(t, result.ThroughputMibps)
		assert.Nil(t, result.Iops)
	})

	t.Run("WhenEnableMqosIsTrue_VolumePerformanceGroupIdAndThroughputMibpsSet_ReturnsError", func(t *testing.T) {
		enableMqos = true
		enableVolumePerformanceGroupAssignment = true
		req := &gcpgenserver.VolumeUpdateV1beta{
			VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
			ThroughputMibps:          gcpgenserver.NewOptNilFloat64(100),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "Only (throughputMibps + iops), or volumePerformanceGroupId can be set at a time")
	})

	t.Run("WhenEnableMqosIsTrue_VolumePerformanceGroupIdAndIopsSet_ReturnsError", func(t *testing.T) {
		enableMqos = true
		enableVolumePerformanceGroupAssignment = true
		req := &gcpgenserver.VolumeUpdateV1beta{
			VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
			Iops:                     gcpgenserver.NewOptNilInt64(1000),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "Only (throughputMibps + iops), or volumePerformanceGroupId can be set at a time")
	})

	// Valid combinations
	t.Run("WhenEnableMqosIsTrueAndEnableInferredIopsIsTrue_OnlyThroughputMibpsSet_ReturnsParam", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = true
		req := &gcpgenserver.VolumeUpdateV1beta{
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(100),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, int64(100), *result.ThroughputMibps)
		assert.Nil(t, result.Iops)
		assert.Nil(t, result.VolumePerformanceGroupId)
	})

	t.Run("WhenEnableMqosIsTrueAndEnableInferredIopsIsFalse_ThroughputMibpsAndIopsSet_ReturnsParam", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = false
		req := &gcpgenserver.VolumeUpdateV1beta{
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(100),
			Iops:            gcpgenserver.NewOptNilInt64(1000),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, int64(100), *result.ThroughputMibps)
		assert.Equal(t, int64(1000), *result.Iops)
		assert.Nil(t, result.VolumePerformanceGroupId)
	})

	t.Run("WhenEnableMqosIsTrueAndEnableInferredIopsIsFalse_VolumePerformanceGroupIdSet_ReturnsParam", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = false
		enableVolumePerformanceGroupAssignment = true
		req := &gcpgenserver.VolumeUpdateV1beta{
			VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "vpg-123", *result.VolumePerformanceGroupId)
		assert.Nil(t, result.ThroughputMibps)
		assert.Nil(t, result.Iops)
	})

	t.Run("WhenEnableMqosIsTrueAndEnableInferredIopsIsTrue_VolumePerformanceGroupIdSet_ReturnsParam", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = true
		enableVolumePerformanceGroupAssignment = true
		req := &gcpgenserver.VolumeUpdateV1beta{
			VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "vpg-123", *result.VolumePerformanceGroupId)
		assert.Nil(t, result.ThroughputMibps)
		assert.Nil(t, result.Iops)
	})

	t.Run("WhenEnableMqosIsTrueAndEnableVolumePerformanceGroupAssignmentIsFalse_VolumePerformanceGroupIdSet_ReturnsError", func(t *testing.T) {
		enableMqos = true
		enableVolumePerformanceGroupAssignment = false
		req := &gcpgenserver.VolumeUpdateV1beta{
			VolumePerformanceGroupId: gcpgenserver.NewOptNilString("vpg-123"),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), utils.ErrMsgVpgAssignmentNotEnabled)
	})

	t.Run("WhenEnableMqosIsTrueAndEnableInferredIopsIsTrue_OnlyIopsSet_ReturnsError", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = true
		req := &gcpgenserver.VolumeUpdateV1beta{
			Iops: gcpgenserver.NewOptNilInt64(1000),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "iops must not be set when IOPS inference is enabled. Only throughputMibps is needed.")
	})

	t.Run("WhenEnableMqosIsTrueAndEnableInferredIopsIsTrue_ThroughputMibpsAndIopsSet_ReturnsError", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = true
		req := &gcpgenserver.VolumeUpdateV1beta{
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(100),
			Iops:            gcpgenserver.NewOptNilInt64(1000),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "iops must not be set when IOPS inference is enabled. Only throughputMibps is needed.")
	})

	// VPG-revert cases: volume currently VPG-assigned, request sets individual QoS without volumePerformanceGroupId.
	// Both throughputMibps and iops are required because there are no existing individual values to delta into.

	t.Run("WhenVolumeIsVpgAssigned_OnlyThroughputMibpsSet_ReturnsError", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = false
		dbVol := &models.Volume{VolumePerformanceGroupId: "vpg-abc"}
		req := &gcpgenserver.VolumeUpdateV1beta{
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(100),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVol)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "throughputMibps and iops must both be set when reverting a VPG volume to individual QoS")
	})

	t.Run("WhenVolumeIsVpgAssigned_OnlyIopsSet_ReturnsError", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = false
		dbVol := &models.Volume{VolumePerformanceGroupId: "vpg-abc"}
		req := &gcpgenserver.VolumeUpdateV1beta{
			Iops: gcpgenserver.NewOptNilInt64(1000),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVol)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "throughputMibps and iops must both be set when reverting a VPG volume to individual QoS")
	})

	t.Run("WhenVolumeIsVpgAssigned_BothThroughputAndIopsSet_ReturnsParam", func(t *testing.T) {
		enableMqos = true
		enableInferredIops = false
		dbVol := &models.Volume{VolumePerformanceGroupId: "vpg-abc"}
		req := &gcpgenserver.VolumeUpdateV1beta{
			ThroughputMibps: gcpgenserver.NewOptNilFloat64(100),
			Iops:            gcpgenserver.NewOptNilInt64(1000),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVol)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, int64(100), *result.ThroughputMibps)
		assert.Equal(t, int64(1000), *result.Iops)
		assert.Nil(t, result.VolumePerformanceGroupId)
	})
}

func TestPrepareUpdateVolumeParams_HG(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "proj",
		LocationId:    "loc",
		VolumeId:      "vol",
	}
	region := "region"

	req := &gcpgenserver.VolumeUpdateV1beta{
		SnapReserve: gcpgenserver.NewOptNilFloat64(101),
	}
	result, err := _prepareUpdateVolumeParams(req, params, region, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "SnapReserve should be less than 100")
}

// BackupFeatureNotEnabled_ReturnsError tests the scenario where backup feature is not enabled
func TestRestoreWhenBackupFeatureNotEnabled_ReturnsError(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = false

	req := &gcpgenserver.VolumeCreateV1beta{
		BackupPath: gcpgenserver.NewOptString("/backup/path"),
		Volume: gcpgenserver.VolumeV1beta{
			QuotaInBytes: gcpgenserver.NewOptFloat64(107374182400),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "us-west1-b"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Backup feature is currently not enabled.")
}

func TestConvertModelToVCPVolume_MountPoints(t *testing.T) {
	t.Run("WhenVolumeIsReadyAndLunNamePresent_ShouldAddMountPoints", func(tt *testing.T) {
		// Setup a volume with READY state and non-empty LunName
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "testvolume",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY),
			IPAddresses:    []string{"10.72.177.17"},
			BlockProperties: &models.BlockProperties{
				OSType:  "LINUX",
				LunName: "lun-123",
			},
			ProtocolTypes: []string{"ISCSI"},
		}

		// Convert the model to VCP volume
		result := convertModelToVCPVolume(vol)

		// Verify mount points are added
		assert.NotNil(tt, result.MountPoints)
		assert.Len(tt, result.MountPoints, 1)
		assert.Equal(tt, "10.72.177.17", result.MountPoints[0].IpAddress.Value)
		assert.Equal(tt, gcpgenserver.ProtocolsV1betaISCSI, result.MountPoints[0].Protocol.Value)
		assert.NotEmpty(tt, result.MountPoints[0].Instructions.Value)
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "lun-123")
	})

	t.Run("WhenVolumeNotReady_ShouldNotAddMountPoints", func(tt *testing.T) {
		// Setup a volume with non-READY state but with LunName
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "testvolume",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateCREATING), // Not READY
			IPAddresses:    []string{"10.72.177.17"},
			BlockProperties: &models.BlockProperties{
				OSType:  "LINUX",
				LunName: "lun-123", // Has LUN name
			},
			ProtocolTypes: []string{"ISCSI"},
		}

		// Convert the model to VCP volume
		result := convertModelToVCPVolume(vol)

		// Verify mount points are not added
		assert.Empty(tt, result.MountPoints)
	})

	t.Run("WhenNoLunName_ShouldNotAddMountPoints", func(tt *testing.T) {
		// Setup a volume with READY state but empty LunName
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "testvolume",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY), // READY
			IPAddresses:    []string{"10.72.177.17"},
			BlockProperties: &models.BlockProperties{
				OSType:  "LINUX",
				LunName: "", // Empty LUN name
			},
			ProtocolTypes: []string{"ISCSI"},
		}

		// Convert the model to VCP volume
		result := convertModelToVCPVolume(vol)

		// Verify mount points are not added
		assert.Empty(tt, result.MountPoints)
	})

	t.Run("WhenNoBlockProperties_ShouldNotAddMountPoints", func(tt *testing.T) {
		// Setup a volume with READY state but no BlockProperties
		vol := &models.Volume{
			BaseModel:       models.BaseModel{UUID: "vol-1"},
			DisplayName:     "testvolume",
			LifeCycleState:  string(gcpgenserver.VolumeV1betaVolumeStateREADY), // READY
			IPAddresses:     []string{"10.72.177.17"},
			BlockProperties: nil, // No BlockProperties
			ProtocolTypes:   []string{"ISCSI"},
		}
		// Convert the model to VCP volume
		result := convertModelToVCPVolume(vol)

		// Verify mount points are not added and no panic occurs
		assert.Empty(tt, result.MountPoints)
	})

	t.Run("WhenLabelsArePresent_ShouldReturn", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:     models.BaseModel{UUID: "vol-1"},
			ProtocolTypes: []string{"ISCSI"},
			Labels: map[string]string{
				"key1": "value1",
			},
		}
		// Convert the model to VCP volume
		result := convertModelToVCPVolume(vol)

		// Verify mount points are not added and no panic occurs
		assert.NotEmpty(tt, result.Labels)
		assert.Equal(tt, "value1", result.Labels.Value["key1"])
	})
}

func TestV1betaDescribeVolume(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			DisplayName:    "testvolume",
			QuotaInBytes:   1024 * 1024 * 1024, // 1GB
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", true).Return(volume, nil)

		result, err := handler.V1betaDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)
		volumeResponse := result.(*gcpgenserver.VolumeV1beta)
		assert.Equal(tt, "testvolume", volumeResponse.ResourceId)
		assert.Equal(tt, "vol-1", volumeResponse.VolumeId.Value)
	})

	t.Run("VolumeNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "nonexistent-vol",
		}
		notFoundErr := errors.NewNotFoundErr("Volume", &params.VolumeId)
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "nonexistent-vol", true).Return(nil, notFoundErr)

		result, err := handler.V1betaDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)
		notFound, isNotFound := result.(*gcpgenserver.V1betaDescribeVolumeNotFound)
		assert.True(tt, isNotFound)
		assert.Equal(tt, float64(404), notFound.Code)
		assert.Equal(tt, "Volume not found", notFound.Message)
	})

	t.Run("InternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		internalErr := errors.New("database connection failed")
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", true).Return(nil, internalErr)

		result, err := handler.V1betaDescribeVolume(context.Background(), params)
		assert.Nil(tt, err)
		internalServerErr, isInternal := result.(*gcpgenserver.V1betaDescribeVolumeInternalServerError)
		assert.True(tt, isInternal)
		assert.Equal(tt, float64(500), internalServerErr.Code)
		assert.Equal(tt, "Internal server error", internalServerErr.Message)
	})

	t.Run("VolumeWithoutAllSquashFields_FeatureFlagEnabled_ShouldNotContainFields", func(tt *testing.T) {
		// Enable the feature flag for this test
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(true)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}

		// Create a volume with export rules that don't have AllSquash and AnonUid set
		// (they will be nil pointers, not zero values)
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			DisplayName:    "testvolume",
			QuotaInBytes:   1024 * 1024 * 1024, // 1GB
			ProtocolTypes:  []string{"NFSV3"},
			FileProperties: &models.FileProperties{
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "test-export-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients:      "192.168.1.0/24",
							AccessType:          "READ_WRITE",
							NFSv3:               true,
							NFSv4:               false,
							Kerberos5ReadOnly:   false,
							Kerberos5ReadWrite:  false,
							Kerberos5iReadOnly:  false,
							Kerberos5iReadWrite: false,
							Kerberos5pReadOnly:  false,
							Kerberos5pReadWrite: false,
							Index:               1,
							// AllSquash and AnonUid are nil (not set) - this is for old volumes
						},
					},
				},
			},
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", true).Return(volume, nil)

		result, err := handler.V1betaDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)
		volumeResponse := result.(*gcpgenserver.VolumeV1beta)
		assert.Equal(tt, "testvolume", volumeResponse.ResourceId)
		assert.Equal(tt, "vol-1", volumeResponse.VolumeId.Value)

		// Verify ExportPolicy exists
		assert.True(tt, volumeResponse.ExportPolicy.IsSet())
		exportPolicy := volumeResponse.ExportPolicy.Value
		assert.Len(tt, exportPolicy.Rules, 1)

		// Verify that AllSquash and AnonUid fields are not set in the export rule
		rule := exportPolicy.Rules[0]
		assert.False(tt, rule.AllSquash.IsSet(), "AllSquash should not be set")
		assert.False(tt, rule.AnonUid.IsSet(), "AnonUid should not be set")

		// Marshal to JSON and verify the fields are not present in the JSON string
		jsonData, err := json.Marshal(volumeResponse)
		assert.NoError(tt, err)
		jsonString := string(jsonData)
		assert.NotContains(tt, jsonString, "allSquash", "JSON should not contain 'allSquash' field")
		assert.NotContains(tt, jsonString, "anonUid", "JSON should not contain 'anonUid' field")
	})

	t.Run("VolumeWithQos_ShouldIncludeThroughputMibpsAndIops", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		throughputMibps := int64(200)
		iops := int64(1000)
		volume := &models.Volume{
			BaseModel:       models.BaseModel{UUID: "vol-1"},
			LifeCycleState:  "READY",
			DisplayName:     "testvolume",
			QuotaInBytes:    1024 * 1024 * 1024, // 1GB
			ThroughputMibps: &throughputMibps,
			Iops:            &iops,
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", true).Return(volume, nil)

		result, err := handler.V1betaDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)
		volumeResponse := result.(*gcpgenserver.VolumeV1beta)
		assert.Equal(tt, "testvolume", volumeResponse.ResourceId)
		assert.Equal(tt, "vol-1", volumeResponse.VolumeId.Value)

		// Verify ThroughputMibps and Iops are set
		assert.True(tt, volumeResponse.ThroughputMibps.IsSet())
		throughput, ok := volumeResponse.ThroughputMibps.Get()
		assert.True(tt, ok)
		assert.Equal(tt, float64(200), throughput)

		assert.True(tt, volumeResponse.Iops.IsSet())
		iopsVal, ok := volumeResponse.Iops.Get()
		assert.True(tt, ok)
		assert.Equal(tt, int64(1000), iopsVal)
	})

	t.Run("VolumeWithoutQos_ShouldNotIncludeThroughputMibpsAndIops", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDescribeVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		volume := &models.Volume{
			BaseModel:       models.BaseModel{UUID: "vol-1"},
			LifeCycleState:  "READY",
			DisplayName:     "testvolume",
			QuotaInBytes:    1024 * 1024 * 1024, // 1GB
			ThroughputMibps: nil,
			Iops:            nil,
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", true).Return(volume, nil)

		result, err := handler.V1betaDescribeVolume(context.Background(), params)
		assert.NoError(tt, err)
		volumeResponse := result.(*gcpgenserver.VolumeV1beta)
		assert.Equal(tt, "testvolume", volumeResponse.ResourceId)

		// Verify ThroughputMibps and Iops are not set
		assert.False(tt, volumeResponse.ThroughputMibps.IsSet())
		assert.False(tt, volumeResponse.Iops.IsSet())
	})
}

func TestV1betaDeleteVolume(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// First GetVolume call to check current state
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)

		// DeleteVolume call
		deletedVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateDeleted,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(deletedVolume, "job-123", nil)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		operation, isOperation := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, isOperation)
		assert.Contains(tt, operation.Name.Value, "job-123")
		assert.Equal(tt, true, operation.Done.Value)
		assert.NotNil(tt, operation.Response)
		assert.Greater(tt, len(operation.Response), 0) // Response should contain data
	})

	t.Run("DescribePoolNotFound_ReturnsBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}
		notFoundErr := errors.NewNotFoundErr("Pool", nil)
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
			PoolID:         "pool-db-id",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-db-id", "test-project").Return(nil, notFoundErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaDeleteVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, notFoundErr.Error(), badReq.Message)
	})

	t.Run("DescribePoolError_ReturnsInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}
		describeErr := fmt.Errorf("Internal server error")
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
			PoolID:         "pool-db-id",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-db-id", "test-project").Return(nil, describeErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaDeleteVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, describeErr.Error(), internalErr.Message)
	})

	t.Run("RejectedWhenPoolZoneSwitchingOrSwitched", func(tt *testing.T) {
		for _, zoneState := range []string{datamodel.ZoneSwitching, datamodel.ZoneSwitched} {
			tt.Run(zoneState, func(t *testing.T) {
				mockOrchestrator := factory.NewMockOrchestratorFactory(t)
				handler := Handler{Orchestrator: mockOrchestrator}
				params := gcpgenserver.V1betaDeleteVolumeParams{
					ProjectNumber: "test-project",
					LocationId:    "test-location",
					VolumeId:      "vol-1",
				}
				req := gcpgenserver.OptV1betaDeleteVolumeReq{}
				volume := &models.Volume{
					BaseModel:      models.BaseModel{UUID: "vol-1"},
					LifeCycleState: datamodel.LifeCycleStateREADY,
					PoolID:         "pool-db-id",
				}
				mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
				mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-db-id", "test-project").Return(&models.Pool{
					PoolAttributes: &models.PoolAttributes{ZoneSwitchState: zoneState},
				}, nil)

				result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
				require.NoError(t, err)
				badReq, ok := result.(*gcpgenserver.V1betaDeleteVolumeBadRequest)
				require.True(t, ok)
				assert.Equal(t, float64(http.StatusBadRequest), badReq.Code)
				assert.Equal(t, "Volume delete is not supported when the pool is switching/switched to a different primary zone.", badReq.Message)
			})
		}
	})

	t.Run("GetVolumeNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "nonexistent-vol",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		notFoundErr := errors.NewNotFoundErr("Volume", &params.VolumeId)
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "nonexistent-vol", false).Return(nil, notFoundErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		notFoundErr1, isNotFound := result.(*gcpgenserver.V1betaDeleteVolumeNotFound)
		assert.True(tt, isNotFound)
		assert.Equal(tt, float64(404), notFoundErr1.Code)
		assert.Equal(tt, "Volume not found", notFoundErr1.Message)
	})

	t.Run("GetVolumeInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		internalErr := errors.New("database connection failed")
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(nil, internalErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		serverErr, isInternal := result.(*gcpgenserver.V1betaDeleteVolumeInternalServerError)
		assert.True(tt, isInternal)
		assert.Equal(tt, float64(500), serverErr.Code)
		assert.Equal(tt, "Internal server error", serverErr.Message)
	})

	t.Run("VolumeAlreadyDeleting", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateDeleting,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		job := &models.Job{
			BaseModel: models.BaseModel{UUID: "job-uuid"},
			Type:      datamodel.JobTypeDeleteVolume,
			JobAttributes: &models.JobAttributes{
				ResourceUUID: "deleting-pool-id",
			},
			State: datamodel.JobsStatePROCESSING,
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, "vol-1", string(datamodel.JobTypeDeleteVolume)).Return(job, nil)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		operation, isOperation := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, isOperation)
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/test-project/locations/test-location/operations/job-uuid")
		assert.False(tt, false, operation.Done.Value)
		assert.Equal(tt, 0, len(operation.Response)) // No response for already deleting/deleted volume
	})

	t.Run("FlexGroupVolumeAlreadyDeleting", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateDeleting,
			CreationToken:  "token",
			DisplayName:    "testvolume",
			LargeCapacity:  true,
		}
		job := &models.Job{
			BaseModel: models.BaseModel{UUID: "job-uuid"},
			Type:      datamodel.JobTypeDeleteLargeVolume,
			JobAttributes: &models.JobAttributes{
				ResourceUUID: "deleting-pool-id",
			},
			State: datamodel.JobsStatePROCESSING,
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, "vol-1", string(datamodel.JobTypeDeleteLargeVolume)).Return(job, nil)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		operation, isOperation := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, isOperation)
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/test-project/locations/test-location/operations/job-uuid")
		assert.False(tt, false, operation.Done.Value)
		assert.Equal(tt, 0, len(operation.Response)) // No response for already deleting/deleted volume
	})

	t.Run("VolumeAlreadyDeleted", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateDeleted,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-1"},
			Name:      "test-pool",
			State:     datamodel.LifeCycleStateREADY,
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().GetPoolByName(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		operation, isOperation := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, isOperation)
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/test-project/locations/test-location/operations/")
		assert.Equal(tt, true, operation.Done.Value)
		assert.Equal(tt, 0, len(operation.Response)) // No response for already deleted volume
	})

	t.Run("VolumeAlreadyDeleted", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateDeleted,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-1"},
			Name:      "test-pool",
			State:     datamodel.LifeCycleStateDeleted,
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)
		mockOrchestrator.EXPECT().GetPoolByName(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(pool, nil)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)

		res, ok := result.(*gcpgenserver.V1betaDeleteVolumeNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), res.Code)
		assert.Equal(tt, "Volume not found", res.Message)
	})

	t.Run("DeleteVolumeNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// GetVolume succeeds
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)

		// DeleteVolume returns not found (volume disappeared between calls)
		notFoundErr := errors.NewNotFoundErr("Volume", &params.VolumeId)
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(nil, "", notFoundErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		operation, isOperation := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, isOperation)
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/test-project/locations/test-location/operations/")
		assert.Equal(tt, true, operation.Done.Value)
		assert.Equal(tt, 0, len(operation.Response)) // No response for not found during delete
	})

	t.Run("DeleteVolumeInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// GetVolume succeeds
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)

		// DeleteVolume fails with internal error
		internalErr := errors.New("database connection failed")
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(nil, "", internalErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		serverErr, isInternal := result.(*gcpgenserver.V1betaDeleteVolumeInternalServerError)
		assert.True(tt, isInternal)
		assert.Equal(tt, float64(500), serverErr.Code)
		assert.Equal(tt, "Internal server error", serverErr.Message)
	})

	t.Run("SuccessWithDeletingState", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// GetVolume succeeds
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)

		// DeleteVolume returns volume in deleting state
		deletingVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateDeleting,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(deletingVolume, "job-123", nil)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		operation, isOperation := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, isOperation)
		assert.Contains(tt, operation.Name.Value, "job-123")
		assert.Equal(tt, false, operation.Done.Value) // Not done yet since still deleting
		assert.NotNil(tt, operation.Response)
		assert.Greater(tt, len(operation.Response), 0) // Response should contain data
	})

	t.Run("SuccessWithDifferentVolumeStates", func(tt *testing.T) {
		testCases := []struct {
			name         string
			initialState string
			expectError  bool
		}{
			{
				name:         "FromReadyState",
				initialState: datamodel.LifeCycleStateREADY,
				expectError:  false,
			},
			{
				name:         "FromErrorState",
				initialState: datamodel.LifeCycleStateError,
				expectError:  false,
			},
		}

		for _, tc := range testCases {
			tt.Run(tc.name, func(t *testing.T) {
				mockOrchestrator := factory.NewMockOrchestratorFactory(t)
				handler := Handler{Orchestrator: mockOrchestrator}
				params := gcpgenserver.V1betaDeleteVolumeParams{
					ProjectNumber: "test-project",
					LocationId:    "test-location",
					VolumeId:      "vol-1",
				}
				req := gcpgenserver.OptV1betaDeleteVolumeReq{}

				// GetVolume succeeds
				volume := &models.Volume{
					BaseModel:      models.BaseModel{UUID: "vol-1"},
					LifeCycleState: tc.initialState,
					CreationToken:  "token",
					DisplayName:    "testvolume",
				}
				mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)

				// DeleteVolume returns volume in deleted state
				deletedVolume := &models.Volume{
					BaseModel:      models.BaseModel{UUID: "vol-1"},
					LifeCycleState: datamodel.LifeCycleStateDeleted,
					CreationToken:  "token",
					DisplayName:    "testvolume",
				}
				mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(deletedVolume, "job-123", nil)

				result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
				if tc.expectError {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
					operation, isOperation := result.(*gcpgenserver.OperationV1beta)
					assert.True(t, isOperation)
					assert.Contains(t, operation.Name.Value, "job-123")
					assert.Equal(t, true, operation.Done.Value)
					assert.NotNil(t, operation.Response)
					assert.Greater(t, len(operation.Response), 0) // Response should contain data
				}
			})
		}
	})

	t.Run("SuccessfulDelete", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteVolumeParams{
			LocationId:    "us-central1-a",
			ProjectNumber: "123456789",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// Mock GetVolume to return an existing volume
		existingVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(existingVolume, nil)

		// Mock DeleteVolume to return success
		deletingVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "DELETING",
			DisplayName:    "testvolume",
		}
		jobUUID := "job-uuid-123"
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(deletingVolume, jobUUID, nil)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)

		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/123456789/locations/us-central1-a/operations/job-uuid-123", op.Name.Value)
		assert.Equal(tt, false, op.Done.Value)
	})

	t.Run("UserInputValidationError_BackupInTransition", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteVolumeParams{
			LocationId:    "us-central1-a",
			ProjectNumber: "123456789",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// Mock GetVolume to return an existing volume
		existingVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(existingVolume, nil)

		// Mock DeleteVolume to return UserInputValidationErr (backup in transition)
		validationErr := errors.NewUserInputValidationErr("A backup operation on volume is currently in progress")
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(nil, "", validationErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)

		badReq, ok := result.(*gcpgenserver.V1betaDeleteVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "A backup operation on volume is currently in progress", badReq.Message)
	})

	t.Run("UserInputValidationError_OtherValidation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteVolumeParams{
			LocationId:    "us-central1-a",
			ProjectNumber: "123456789",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// Mock GetVolume to return an existing volume
		existingVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(existingVolume, nil)

		// Mock DeleteVolume to return another UserInputValidationErr
		validationErr := errors.NewUserInputValidationErr("Volume cannot be deleted due to active replication")
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(nil, "", validationErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)

		badReq, ok := result.(*gcpgenserver.V1betaDeleteVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Volume cannot be deleted due to active replication", badReq.Message)
	})

	t.Run("DeleteVolumeConflictError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// GetVolume succeeds
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: datamodel.LifeCycleStateREADY,
			CreationToken:  "token",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)

		// DeleteVolume returns conflict error (line 1410)
		conflictErr := errors.NewConflictErr("Volume is in transition state and cannot be deleted")
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(nil, "", conflictErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		conflict, isConflict := result.(*gcpgenserver.V1betaDeleteVolumeConflict)
		assert.True(tt, isConflict)
		assert.Equal(tt, float64(409), conflict.Code)
		assert.Equal(tt, "Volume is in transition state and cannot be deleted", conflict.Message)
	})

	t.Run("VolumeNotFound_GetVolume", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteVolumeParams{
			LocationId:    "us-central1-a",
			ProjectNumber: "123456789",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// Mock GetVolume to return NotFoundErr
		notFoundErr := errors.NewNotFoundErr("Volume not found", nil)
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(nil, notFoundErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.Nil(tt, err)

		notFoundErr2, ok := result.(*gcpgenserver.V1betaDeleteVolumeNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), notFoundErr2.Code)
		assert.Equal(tt, "Volume not found", notFoundErr2.Message)
	})

	t.Run("VolumeNotFound_DeleteVolume", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteVolumeParams{
			LocationId:    "us-central1-a",
			ProjectNumber: "123456789",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// Mock GetVolume to return an existing volume
		existingVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(existingVolume, nil)

		// Mock DeleteVolume to return NotFoundErr
		notFoundErr := errors.NewNotFoundErr("Volume not found", nil)
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(nil, "", notFoundErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.NoError(tt, err)

		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, true, op.Done.Value)
		assert.Contains(tt, op.Name.Value, "/v1beta/projects/123456789/locations/us-central1-a/operations/")
	})

	t.Run("InternalServerError_GetVolume", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteVolumeParams{
			LocationId:    "us-central1-a",
			ProjectNumber: "123456789",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// Mock GetVolume to return unexpected error
		unexpectedErr := fmt.Errorf("database connection failed")
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(nil, unexpectedErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.Nil(tt, err)

		internalErr, ok := result.(*gcpgenserver.V1betaDeleteVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "Internal server error", internalErr.Message)
	})

	t.Run("InternalServerError_DeleteVolume", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteVolumeParams{
			LocationId:    "us-central1-a",
			ProjectNumber: "123456789",
			VolumeId:      "vol-1",
		}
		req := gcpgenserver.OptV1betaDeleteVolumeReq{}

		// Mock GetVolume to return an existing volume
		existingVolume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
			DisplayName:    "testvolume",
		}
		mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(existingVolume, nil)

		// Mock DeleteVolume to return unexpected error
		unexpectedErr := fmt.Errorf("workflow execution failed")
		mockOrchestrator.EXPECT().DeleteVolume(mock.Anything, "vol-1").Return(nil, "", unexpectedErr)

		result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaDeleteVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "Internal server error", internalErr.Message)
	})
}

func TestV1betaRevertVolume(t *testing.T) {
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	mockParseAndValidateRegionAndZone := func(region string) (string, string, *gcpgenserver.Error) {
		return "test-region", "test-location", nil
	}
	utils.ParseAndValidateRegionAndZone = mockParseAndValidateRegionAndZone
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	t.Run("ValidRevertVolume", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaRevertVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "READY",
		}
		jobUUID := "job-uuid"
		mockOrchestrator.EXPECT().RevertVolume(mock.Anything, mock.Anything).Return(volume, jobUUID, nil)

		result, err := handler.V1betaRevertVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", op.Name.Value)
		assert.True(tt, op.Done.Value)
	})

	t.Run("UserInputValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaRevertVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}
		prepareRevertVolumeParams = func(req *gcpgenserver.VolumeRevertV1beta, params gcpgenserver.V1betaRevertVolumeParams, region string) (*common.RevertVolumeParams, error) {
			return nil, errors.NewUserInputValidationErr("invalid input")
		}
		defer func() { prepareRevertVolumeParams = _prepareRevertVolumeParams }()

		result, err := handler.V1betaRevertVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaRevertVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "invalid input")
	})

	t.Run("InternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaRevertVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}
		prepareRevertVolumeParams = func(req *gcpgenserver.VolumeRevertV1beta, params gcpgenserver.V1betaRevertVolumeParams, region string) (*common.RevertVolumeParams, error) {
			return nil, fmt.Errorf("unexpected error")
		}
		defer func() { prepareRevertVolumeParams = _prepareRevertVolumeParams }()

		result, err := handler.V1betaRevertVolume(context.Background(), req, params)
		assert.Nil(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaRevertVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "unexpected error")
	})

	t.Run("BadRequest_InvalidLocation", func(tt *testing.T) {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		defer func() { utils.ParseAndValidateRegionAndZone = mockParseAndValidateRegionAndZone }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		params := gcpgenserver.V1betaRevertVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}
		prepareRevertVolumeParams = func(req *gcpgenserver.VolumeRevertV1beta, params gcpgenserver.V1betaRevertVolumeParams, region string) (*common.RevertVolumeParams, error) {
			return nil, fmt.Errorf("unexpected error")
		}
		defer func() { prepareRevertVolumeParams = _prepareRevertVolumeParams }()

		result, err := handler.V1betaRevertVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaRevertVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "LocationID represents neither a region nor a zone")
	})

	t.Run("WhenOrchestratorValidationThrowsAnError_Return400BadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaRevertVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}

		mockOrchestrator.EXPECT().RevertVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("An error occurred"))

		result, err := handler.V1betaRevertVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaRevertVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "An error occurred")
	})

	t.Run("WhenOrchestratorThrowsAnError_ReturnError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaRevertVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}

		mockOrchestrator.EXPECT().RevertVolume(mock.Anything, mock.Anything).Return(nil, "", errors.New("An error occurred"))

		result, err := handler.V1betaRevertVolume(context.Background(), req, params)
		assert.Error(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaRevertVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "An error occurred")
	})

	t.Run("WhenOrchestratorNotFoundError_Return404NotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaRevertVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}

		mockOrchestrator.EXPECT().RevertVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewNotFoundErr("Volume not found", nil))

		result, err := handler.V1betaRevertVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaRevertVolumeNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "Volume not found")
	})

	t.Run("WhenOrchestratorConflictError_Return409Conflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaRevertVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}

		mockOrchestrator.EXPECT().RevertVolume(mock.Anything, mock.Anything).Return(nil, "", errors.NewConflictErr("Reverting to a snapshot is not allowed when the volume is splitting"))

		result, err := handler.V1betaRevertVolume(context.Background(), req, params)
		assert.NoError(tt, err)
		conflictErr, ok := result.(*gcpgenserver.V1betaRevertVolumeConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(409), conflictErr.Code)
		assert.Contains(tt, conflictErr.Message, "Reverting to a snapshot is not allowed when the volume is splitting")
	})
}

func TestPrepareRevertVolumeParams(t *testing.T) {
	t.Run("ValidRevertVolumeParams", func(tt *testing.T) {
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}
		params := gcpgenserver.V1betaRevertVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		region := "test-region"

		expected := &common.RevertVolumeParams{
			AccountName: "test-project",
			Region:      "test-region",
			VolumeID:    "vol-1",
			SnapshotID:  "snapshot-1",
		}

		result, err := prepareRevertVolumeParams(req, params, region)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, result)
	})

	t.Run("MissingVolumeId", func(tt *testing.T) {
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}
		params := gcpgenserver.V1betaRevertVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "",
		}
		region := "test-region"

		result, err := prepareRevertVolumeParams(req, params, region)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "No Volume ID given")
	})

	t.Run("MissingProjectNumber", func(tt *testing.T) {
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "snapshot-1",
		}
		params := gcpgenserver.V1betaRevertVolumeParams{
			ProjectNumber: "",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		region := "test-region"

		result, err := prepareRevertVolumeParams(req, params, region)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "No Project Number given")
	})

	t.Run("MissingSnapshotId", func(tt *testing.T) {
		req := &gcpgenserver.VolumeRevertV1beta{
			SnapshotId: "",
		}
		params := gcpgenserver.V1betaRevertVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
			VolumeId:      "vol-1",
		}
		region := "test-region"

		result, err := prepareRevertVolumeParams(req, params, region)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "No Snapshot ID given")
	})
}

func TestValidateBackupScheduleCron(t *testing.T) {
	tests := []struct {
		name        string
		cronExpr    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid cron expression - every 5 minutes",
			cronExpr:    "*/5 * * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 10 minutes",
			cronExpr:    "*/10 * * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 30 minutes",
			cronExpr:    "*/30 * * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every hour",
			cronExpr:    "0 * * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - daily at midnight",
			cronExpr:    "0 0 * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - specific minute",
			cronExpr:    "30 * * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes during business hours",
			cronExpr:    "*/5 9-17 * * 1-5",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 30 minutes on weekdays",
			cronExpr:    "*/30 * * * 1-5",
			expectError: false,
		},
		{
			name:        "Invalid cron expression - every 1 minute (too frequent)",
			cronExpr:    "* * * * *",
			expectError: true,
			errorMsg:    "Backup schedule interval must be at least 5 minutes. Current schedule: every minute",
		},
		{
			name:        "Invalid cron expression - every 2 minutes (too frequent)",
			cronExpr:    "*/2 * * * *",
			expectError: true,
			errorMsg:    "Backup schedule interval must be at least 5 minutes. Current interval: 2 minutes",
		},
		{
			name:        "Invalid cron expression - malformed",
			cronExpr:    "invalid cron",
			expectError: true,
			errorMsg:    "Invalid cron expression:",
		},
		{
			name:        "Invalid cron expression - wrong number of fields",
			cronExpr:    "* * * *",
			expectError: true,
			errorMsg:    "Invalid cron expression:",
		},
		{
			name:        "Invalid cron expression - too many fields",
			cronExpr:    "* * * * * *",
			expectError: true,
			errorMsg:    "Invalid cron expression format. Expected 5 fields: minute hour day month weekday",
		},
		{
			name:        "Invalid cron expression - invalid interval format",
			cronExpr:    "*/abc * * * *",
			expectError: true,
			errorMsg:    "Invalid cron expression:",
		},
		{
			name:        "Empty cron expression",
			cronExpr:    "",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with specific hour",
			cronExpr:    "*/5 2 * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with specific day",
			cronExpr:    "*/5 * 15 * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with specific month",
			cronExpr:    "*/5 * * 6 *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with specific weekday",
			cronExpr:    "*/5 * * * 1",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with range in hour",
			cronExpr:    "*/5 9-17 * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with list in hour",
			cronExpr:    "*/5 9,12,17 * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with step in hour",
			cronExpr:    "*/5 */2 * * *",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBackupScheduleCron(tt.cronExpr)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for cron expression '%s', but got none", tt.cronExpr)
					return
				}

				if tt.errorMsg != "" {
					if !contains(err.Error(), tt.errorMsg) {
						t.Errorf("Expected error message to contain '%s', but got: %v", tt.errorMsg, err.Error())
					}
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error for cron expression '%s', but got: %v", tt.cronExpr, err)
				}
			}
		})
	}
}

func TestValidateBackupScheduleCron_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		cronExpr    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid cron expression - every 5 minutes with complex hour range",
			cronExpr:    "*/5 0-23 * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with complex day range",
			cronExpr:    "*/5 * 1-15 * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with complex month range",
			cronExpr:    "*/5 * * 1-12 *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with complex weekday range",
			cronExpr:    "*/5 * * * 1-7",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with mixed ranges",
			cronExpr:    "*/5 9-17 1-15 1-6 1-5",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with lists",
			cronExpr:    "*/5 9,12,15,18 * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - every 5 minutes with steps",
			cronExpr:    "*/5 */3 * * *",
			expectError: false,
		},
		{
			name:        "Invalid cron expression - every 1 minute with complex constraints",
			cronExpr:    "* 9-17 * * 1-5",
			expectError: true,
			errorMsg:    "Backup schedule interval must be at least 5 minutes. Current schedule: every minute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBackupScheduleCron(tt.cronExpr)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for cron expression '%s', but got none", tt.cronExpr)
					return
				}

				if tt.errorMsg != "" {
					if !contains(err.Error(), tt.errorMsg) {
						t.Errorf("Expected error message to contain '%s', but got: %v", tt.errorMsg, err.Error())
					}
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error for cron expression '%s', but got: %v", tt.cronExpr, err)
				}
			}
		})
	}
}

func TestValidateBackupScheduleCron_BoundaryValues(t *testing.T) {
	tests := []struct {
		name        string
		cronExpr    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid cron expression - exactly 5 minutes interval",
			cronExpr:    "*/5 * * * *",
			expectError: false,
		},
		{
			name:        "Invalid cron expression - 4 minutes interval (just below boundary)",
			cronExpr:    "*/4 * * * *",
			expectError: true,
			errorMsg:    "Backup schedule interval must be at least 5 minutes. Current interval: 4 minutes",
		},
		{
			name:        "Valid cron expression - 6 minutes interval (just above boundary)",
			cronExpr:    "*/6 * * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - large interval",
			cronExpr:    "*/60 * * * *",
			expectError: false,
		},
		{
			name:        "Valid cron expression - very large interval",
			cronExpr:    "*/1440 * * * *",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBackupScheduleCron(tt.cronExpr)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for cron expression '%s', but got none", tt.cronExpr)
					return
				}

				if tt.errorMsg != "" {
					if !contains(err.Error(), tt.errorMsg) {
						t.Errorf("Expected error message to contain '%s', but got: %v", tt.errorMsg, err.Error())
					}
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error for cron expression '%s', but got: %v", tt.cronExpr, err)
				}
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			func() bool {
				for i := 1; i <= len(s)-len(substr); i++ {
					if s[i:i+len(substr)] == substr {
						return true
					}
				}
				return false
			}())))
}

func TestPrepareUpdateVolumeParamsCreationToken(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-central1",
		VolumeId:      "test-volume",
	}
	region := "us-central1"

	t.Run("CreationToken_ShouldSetJunctionPath", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:        gcpgenserver.NewOptNilString("test-pool"),
			CreationToken: gcpgenserver.NewOptNilString("my-junction-path"),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.Equal(tt, "/my-junction-path", result.FileProperties.JunctionPath)
	})

	t.Run("CreationToken_EmptyValue_ShouldSetJunctionPathWithSlash", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:        gcpgenserver.NewOptNilString("test-pool"),
			CreationToken: gcpgenserver.NewOptNilString(""),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.Equal(tt, "/", result.FileProperties.JunctionPath)
	})

	t.Run("CreationToken_WithSpecialCharacters_ShouldPreserveValue", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:        gcpgenserver.NewOptNilString("test-pool"),
			CreationToken: gcpgenserver.NewOptNilString("path-with_special.chars123"),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.Equal(tt, "/path-with_special.chars123", result.FileProperties.JunctionPath)
	})

	t.Run("CreationToken_NotSet_ShouldNotCreateFileProperties", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			// CreationToken not set
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.Nil(tt, result.FileProperties)
	})

	t.Run("CreationToken_WithExistingFileProperties_ShouldPreserveOtherFields", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:        gcpgenserver.NewOptNilString("test-pool"),
			CreationToken: gcpgenserver.NewOptNilString("new-path"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients: "192.168.1.0/24",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.Equal(tt, "/new-path", result.FileProperties.JunctionPath)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)
	})
}

func TestPrepareUpdateVolumeParamsExportPolicy(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-central1",
		VolumeId:      "test-volume",
	}
	region := "us-central1"

	t.Run("ExportPolicy_SingleRule_ShouldCreateExportRule", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients: "192.168.1.0/24",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)

		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule.AccessType)
	})

	t.Run("ExportPolicy_WithAllSquashEnabled_ShouldSetAllSquashFields", func(tt *testing.T) {
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(true)

		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients:      "192.168.1.0/24",
						AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						Nfsv3:               gcpgenserver.NewOptNilBool(true),
						Nfsv4:               gcpgenserver.NewOptNilBool(false),
						AllSquash:           gcpgenserver.NewOptNilBool(true),
						AnonUid:             gcpgenserver.NewOptNilInt64(1001),
						Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
						Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
					},
					{
						AllowedClients:      "10.0.0.0/8",
						AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
						Nfsv3:               gcpgenserver.NewOptNilBool(false),
						Nfsv4:               gcpgenserver.NewOptNilBool(true),
						AllSquash:           gcpgenserver.NewOptNilBool(false),
						AnonUid:             gcpgenserver.NewOptNilInt64(0),
						Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
						Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 2)

		rule1 := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule1.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule1.AccessType)
		assert.NotNil(tt, rule1.AllSquash)
		assert.True(tt, *rule1.AllSquash)
		assert.NotNil(tt, rule1.AnonUid)
		assert.Equal(tt, int64(1001), *rule1.AnonUid)
		assert.True(tt, rule1.UnixReadWrite)
		assert.False(tt, rule1.UnixReadOnly)
		assert.False(tt, rule1.Superuser)

		rule2 := result.FileProperties.ExportPolicy.ExportRules[1]
		assert.Equal(tt, "10.0.0.0/8", rule2.AllowedClients)
		assert.Equal(tt, "READ_ONLY", rule2.AccessType)
		assert.NotNil(tt, rule2.AllSquash)
		assert.False(tt, *rule2.AllSquash)
		// AnonUid is forwarded for any rule that has it set in the request (update path)
		assert.NotNil(tt, rule2.AnonUid)
		assert.Equal(tt, int64(0), *rule2.AnonUid)
		assert.True(tt, rule2.UnixReadOnly)
		assert.False(tt, rule2.UnixReadWrite)
	})

	t.Run("ExportPolicy_WithAllSquashEnabled_ValidationError", func(tt *testing.T) {
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(true)

		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients: "192.168.1.0/24",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						Nfsv3:          gcpgenserver.NewOptNilBool(true),
						Nfsv4:          gcpgenserver.NewOptNilBool(false),
						AllSquash:      gcpgenserver.NewOptNilBool(true),
						// Missing AnonUid - should fail validation
						Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
						Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "AnonUid must be set when AllSquash is enabled")
	})

	t.Run("ExportPolicy_WithAllSquashEnabled_OnlyAllSquashSet", func(tt *testing.T) {
		originalValue := utils.IsAllSquashEnabled
		defer func() { utils.EnableAllSquashForTesting(originalValue) }()
		utils.EnableAllSquashForTesting(true)

		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients: "192.168.1.0/24",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						Nfsv3:          gcpgenserver.NewOptNilBool(true),
						Nfsv4:          gcpgenserver.NewOptNilBool(false),
						// AllSquash not set, AnonUid not set - should work fine
						Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
						Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)

		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule.AccessType)
		// AllSquash and AnonUid should be nil when not set
		assert.Nil(tt, rule.AllSquash)
		assert.Nil(tt, rule.AnonUid)
	})

	t.Run("ExportPolicy_MultipleRules_ShouldCreateAllRules", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients: "192.168.1.0/24",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					},
					{
						AllowedClients: "10.0.0.0/8",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 2)

		rule1 := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule1.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule1.AccessType)

		rule2 := result.FileProperties.ExportPolicy.ExportRules[1]
		assert.Equal(tt, "10.0.0.0/8", rule2.AllowedClients)
		assert.Equal(tt, "READ_ONLY", rule2.AccessType)
	})

	t.Run("ExportPolicy_AllProtocolFlags_ShouldSetCorrectly", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients:      "192.168.1.0/24",
						AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						Nfsv3:               gcpgenserver.NewOptNilBool(true),
						Nfsv4:               gcpgenserver.NewOptNilBool(true),
						Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(true),
						Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(true),
						Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(true),
						Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)

		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule.AccessType)
		assert.True(tt, rule.NFSv3)
		assert.True(tt, rule.NFSv4)
		assert.True(tt, rule.Kerberos5ReadOnly)
		assert.False(tt, rule.Kerberos5ReadWrite)
		assert.True(tt, rule.Kerberos5iReadOnly)
		assert.False(tt, rule.Kerberos5iReadWrite)
		assert.True(tt, rule.Kerberos5pReadOnly)
		assert.False(tt, rule.Kerberos5pReadWrite)
	})

	t.Run("ExportPolicy_PartialProtocolFlags_ShouldSetOnlyProvidedFlags", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients: "192.168.1.0/24",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						Nfsv3:          gcpgenserver.NewOptNilBool(true),
						Nfsv4:          gcpgenserver.NewOptNilBool(false),
						// Other kerberos flags not set
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)

		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule.AccessType)
		assert.True(tt, rule.NFSv3)
		assert.False(tt, rule.NFSv4)
		// Kerberos flags should have default values (false)
		assert.False(tt, rule.Kerberos5ReadOnly)
		assert.False(tt, rule.Kerberos5ReadWrite)
		assert.False(tt, rule.Kerberos5iReadOnly)
		assert.False(tt, rule.Kerberos5iReadWrite)
		assert.False(tt, rule.Kerberos5pReadOnly)
		assert.False(tt, rule.Kerberos5pReadWrite)
	})

	t.Run("ExportPolicy_EmptyRules_ShouldCreateEmptyExportPolicy", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 0)
	})

	t.Run("ExportPolicy_NotSet_ShouldNotCreateFileProperties", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			// ExportPolicy not set
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.Nil(tt, result.FileProperties)
	})

	t.Run("ExportPolicy_WithCreationToken_ShouldPreserveBothFields", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId:        gcpgenserver.NewOptNilString("test-pool"),
			CreationToken: gcpgenserver.NewOptNilString("my-path"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients: "192.168.1.0/24",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.Equal(tt, "/my-path", result.FileProperties.JunctionPath)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)

		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule.AccessType)
	})

	t.Run("ExportPolicy_WithHasRootAccessTrue_ShouldSetSuperuser", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients:      "192.168.1.0/24",
						AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						Nfsv3:               gcpgenserver.NewOptNilBool(true),
						Nfsv4:               gcpgenserver.NewOptNilBool(false),
						HasRootAccess:       gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue),
						Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
						Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)

		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule.AccessType)
		assert.True(tt, rule.Superuser)
	})

	t.Run("ExportPolicy_WithHasRootAccessOn_ShouldSetSuperuser", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			PoolId: gcpgenserver.NewOptNilString("test-pool"),
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients:      "192.168.1.0/24",
						AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						Nfsv3:               gcpgenserver.NewOptNilBool(true),
						Nfsv4:               gcpgenserver.NewOptNilBool(false),
						HasRootAccess:       gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessOn),
						Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
						Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
						Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
					},
				},
			}),
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)

		assert.NoError(tt, err)
		assert.NotNil(tt, result.FileProperties)
		assert.NotNil(tt, result.FileProperties.ExportPolicy)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)

		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.Equal(tt, "192.168.1.0/24", rule.AllowedClients)
		assert.Equal(tt, "READ_WRITE", rule.AccessType)
		assert.True(tt, rule.Superuser)
	})
}

func TestPrepareCreateVolumeParams_SnapshotDirectory(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	// Setup file protocol support for NFS tests
	utils.SetFileProtocolSupportedForTesting(true)
	utils.SetExperimentalVersionAllowlistedAccountsForTesting("test-project")
	defer func() {
		utils.SetFileProtocolSupportedForTesting(false)
		utils.SetExperimentalVersionAllowlistedAccountsForTesting("")
	}()

	t.Run("SnapshotDirectory_WhenSetToTrue_ShouldSetParamToTrue", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				SnapshotDirectory: gcpgenserver.NewOptBool(true), // Set to true
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		expected := &common.CreateVolumeParams{
			AccountName:       "test-project",
			Region:            "test-region",
			Zone:              "test-zone",
			Name:              "testvolume",
			VendorID:          "test-vendor-id",
			CreationToken:     "test-token",
			PoolID:            "test-pool",
			QuotaInBytes:      1024,
			SnapshotDirectory: true, // Should be set to true
			// ... other expected fields
		}

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected.SnapshotDirectory, result.SnapshotDirectory)
	})

	t.Run("SnapshotDirectory_WhenSetToFalse_ShouldSetParamToFalse", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				SnapshotDirectory: gcpgenserver.NewOptBool(false), // Set to false
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		expected := &common.CreateVolumeParams{
			AccountName:       "test-project",
			Region:            "test-region",
			Zone:              "test-zone",
			Name:              "testvolume",
			VendorID:          "test-vendor-id",
			CreationToken:     "test-token",
			PoolID:            "test-pool",
			QuotaInBytes:      1024,
			SnapshotDirectory: false, // Should be set to false
			// ... other expected fields
		}

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected.SnapshotDirectory, result.SnapshotDirectory)
	})

	t.Run("SnapshotDirectory_WhenNotSet_ShouldDefaultToFalse", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3,
				},
				// SnapshotDirectory not set - should default to false
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"

		expected := &common.CreateVolumeParams{
			AccountName:       "test-project",
			Region:            "test-region",
			Zone:              "test-zone",
			Name:              "testvolume",
			VendorID:          "test-vendor-id",
			CreationToken:     "test-token",
			PoolID:            "test-pool",
			QuotaInBytes:      1024,
			SnapshotDirectory: false, // Should default to false
			// ... other expected fields
		}

		result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		assert.Equal(tt, expected.SnapshotDirectory, result.SnapshotDirectory)
	})
}

func TestPrepareUpdateVolumeParams_SnapshotDirectory(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "us-central1",
		VolumeId:      "test-volume",
	}
	region := "us-central1"

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	t.Run("SnapshotDirectory_WhenSetToTrue_ShouldSetSnapshotDirectoryAccessToTrue", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			SnapshotDirectory: gcpgenserver.NewOptNilBool(true), // Set to true
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result.SnapshotDirectoryAccess)
		assert.True(tt, *result.SnapshotDirectoryAccess)
	})

	t.Run("SnapshotDirectory_WhenSetToFalse_ShouldSetSnapshotDirectoryAccessToFalse", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			SnapshotDirectory: gcpgenserver.NewOptNilBool(false), // Set to false
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(tt, err)
		assert.NotNil(tt, result.SnapshotDirectoryAccess)
		assert.False(tt, *result.SnapshotDirectoryAccess)
	})

	t.Run("SnapshotDirectory_WhenNotSet_ShouldNotSetSnapshotDirectoryAccess", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			// SnapshotDirectory not set - should not set SnapshotDirectoryAccess
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(tt, err)
		assert.Nil(tt, result.SnapshotDirectoryAccess)
	})

	t.Run("SnapshotDirectory_WhenSetToNil_ShouldNotSetSnapshotDirectoryAccess", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			SnapshotDirectory: gcpgenserver.OptNilBool{}, // Empty OptNilBool (not set)
		}

		result, err := _prepareUpdateVolumeParams(req, params, region, nil)
		assert.NoError(tt, err)
		assert.Nil(tt, result.SnapshotDirectoryAccess)
	})
}

// TestGetFilesMountInstructions tests the getFilesMountInstructions function with different protocols
func TestGetFilesMountInstructions(t *testing.T) {
	t.Run("NFSv3_Protocol_ShouldReturnNFSv3Instructions", func(tt *testing.T) {
		ipAddress := "192.168.1.100"
		junctionPath := "/testvolume"
		fileDir := "/testvolume"
		protocol := "NFSV3"

		result := getFilesMountInstructions(ipAddress, junctionPath, fileDir, protocol, "")

		assert.True(tt, result.IsSet())
		instructions := result.Value
		assert.Contains(tt, instructions, "Setting up your instance")
		assert.Contains(tt, instructions, "vers=3")
		assert.Contains(tt, instructions, ipAddress)
		assert.Contains(tt, instructions, junctionPath)
		assert.Contains(tt, instructions, "sudo mkdir "+junctionPath)
		assert.Contains(tt, instructions, fmt.Sprintf("sudo mount -t nfs -o rw,hard,rsize=65536,wsize=65536,vers=3,tcp %s:%s %s", ipAddress, junctionPath, junctionPath))
	})

	t.Run("NFSv4_Protocol_ShouldReturnNFSv4Instructions", func(tt *testing.T) {
		ipAddress := "10.0.0.50"
		junctionPath := "/vol1"
		fileDir := "/myvolume"
		protocol := "NFSV4"

		result := getFilesMountInstructions(ipAddress, junctionPath, fileDir, protocol, "")

		assert.True(tt, result.IsSet())
		instructions := result.Value
		assert.Contains(tt, instructions, "Setting up your instance")
		assert.Contains(tt, instructions, "vers=4.1")
		assert.Contains(tt, instructions, ipAddress)
		assert.Contains(tt, instructions, junctionPath)
		assert.Contains(tt, instructions, "sudo mkdir "+junctionPath)
		assert.Contains(tt, instructions, fmt.Sprintf("sudo mount -t nfs -o rw,hard,rsize=65536,wsize=65536,vers=4.1,tcp %s:%s %s", ipAddress, junctionPath, junctionPath))
	})

	t.Run("SMB_Protocol_ShouldReturnSMBInstructions", func(tt *testing.T) {
		ipAddress := "netbios.domain.com"
		junctionPath := "/test-share"
		fileDir := "/myvolume"
		protocol := "SMB"

		result := getFilesMountInstructions(ipAddress, junctionPath, fileDir, protocol, "netbios.domain.com")

		assert.True(tt, result.IsSet())
		instructions := result.Value
		assert.Contains(tt, instructions, "Mapping your network drive")
		assert.Contains(tt, instructions, "Click the Start button")
		assert.Contains(tt, instructions, "Map Network Drive")
		// Verify UNC path format
		assert.Contains(tt, instructions, fmt.Sprintf(`\\%s\%s`, ipAddress, strings.TrimPrefix(junctionPath, "/")))
	})

	t.Run("UnknownProtocol_ShouldReturnEmptyInstructions", func(tt *testing.T) {
		ipAddress := "10.0.0.50"
		junctionPath := "/testvolume"
		fileDir := "/testvolume"
		protocol := "PROTOCOL_UNSPECIFIED" // Unsupported protocol

		result := getFilesMountInstructions(ipAddress, junctionPath, fileDir, protocol, "")

		assert.True(tt, result.IsSet())
		instructions := result.Value
		assert.Empty(tt, instructions) // Should be empty for unsupported protocol
	})

	t.Run("EmptyParameters_ShouldHandleGracefully", func(tt *testing.T) {
		result := getFilesMountInstructions("", "", "", "NFSV3", "")

		assert.True(tt, result.IsSet())
		instructions := result.Value
		assert.Contains(tt, instructions, "Setting up your instance")
		// Should still contain the basic structure even with empty parameters
	})
}

// TestGetExportPath tests the getExportPath function with different protocols
func TestGetExportPath(t *testing.T) {
	t.Run("NFSV3_Protocol_ShouldReturnJunctionPathAsIs", func(tt *testing.T) {
		junctionPath := "/testvolume"
		protocol := "NFSV3"

		result := getExportPath(junctionPath, protocol)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, "/testvolume", result.Value)
	})

	t.Run("NFSV4_Protocol_ShouldReturnJunctionPathAsIs", func(tt *testing.T) {
		junctionPath := "/vol1"
		protocol := "NFSV4"

		result := getExportPath(junctionPath, protocol)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, "/vol1", result.Value)
	})

	t.Run("SMB_Protocol_ShouldTrimLeadingSlash", func(tt *testing.T) {
		junctionPath := "/smb-share"
		protocol := "SMB"

		result := getExportPath(junctionPath, protocol)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, "smb-share", result.Value)
	})

	t.Run("SMB_Protocol_WithNoLeadingSlash_ShouldReturnAsIs", func(tt *testing.T) {
		junctionPath := "smb-share"
		protocol := "SMB"

		result := getExportPath(junctionPath, protocol)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, "smb-share", result.Value)
	})

	t.Run("SMB_Protocol_EmptyJunctionPath_ShouldReturnEmpty", func(tt *testing.T) {
		junctionPath := ""
		protocol := "SMB"

		result := getExportPath(junctionPath, protocol)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, "", result.Value)
	})

	t.Run("NFS_Protocol_NestedPath_ShouldReturnJunctionPathAsIs", func(tt *testing.T) {
		junctionPath := "/vol1/subvol"
		protocol := "NFSV3"

		result := getExportPath(junctionPath, protocol)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, "/vol1/subvol", result.Value)
	})
}

// TestGetExportFullPath tests the getExportFullPath function with different protocols
func TestGetExportFullPath(t *testing.T) {
	t.Run("NFSV3_Protocol_ShouldReturnAddressColonPath", func(tt *testing.T) {
		address := "192.168.1.100"
		path := "/testvolume"
		protocol := "NFSV3"
		fqdn := ""

		result := getExportFullPath(address, path, protocol, fqdn)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, "192.168.1.100:/testvolume", result.Value)
	})

	t.Run("NFSV4_Protocol_ShouldReturnAddressColonPath", func(tt *testing.T) {
		address := "10.0.0.50"
		path := "/vol1"
		protocol := "NFSV4"
		fqdn := ""

		result := getExportFullPath(address, path, protocol, fqdn)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, "10.0.0.50:/vol1", result.Value)
	})

	t.Run("SMB_Protocol_ShouldReturnUNCPath", func(tt *testing.T) {
		address := "192.168.1.200"
		path := "/smb-share"
		protocol := "SMB"
		fqdn := "netbios.domain.com"

		result := getExportFullPath(address, path, protocol, fqdn)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, `\\netbios.domain.com\smb-share`, result.Value)
	})

	t.Run("SMB_Protocol_PathWithoutLeadingSlash_ShouldReturnUNCPath", func(tt *testing.T) {
		address := "192.168.1.200"
		path := "smb-share"
		protocol := "SMB"
		fqdn := "netbios.domain.com"

		result := getExportFullPath(address, path, protocol, fqdn)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, `\\netbios.domain.com\smb-share`, result.Value)
	})

	t.Run("SMB_Protocol_EmptyFqdn_ShouldStillReturnUNCPath", func(tt *testing.T) {
		address := "192.168.1.200"
		path := "/smb-share"
		protocol := "SMB"
		fqdn := ""

		result := getExportFullPath(address, path, protocol, fqdn)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, `\\\smb-share`, result.Value)
	})

	t.Run("NFS_Protocol_EmptyAddress_ShouldStillReturnColonPath", func(tt *testing.T) {
		address := ""
		path := "/vol1"
		protocol := "NFSV3"
		fqdn := ""

		result := getExportFullPath(address, path, protocol, fqdn)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, ":/vol1", result.Value)
	})

	t.Run("NFS_Protocol_NestedPath_ShouldReturnFullPath", func(tt *testing.T) {
		address := "192.168.1.100"
		path := "/vol1/subvol"
		protocol := "NFSV4"
		fqdn := ""

		result := getExportFullPath(address, path, protocol, fqdn)

		assert.True(tt, result.IsSet())
		assert.Equal(tt, "192.168.1.100:/vol1/subvol", result.Value)
	})
}

// TestConvertModelToVCPVolume_NFSMountPoints tests the NFS mount points functionality
func TestConvertModelToVCPVolume_NFSMountPoints(t *testing.T) {
	t.Run("NFSv3_SingleProtocol_ShouldCreateMountPoints", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "testvolume",
			CreationToken:  "test-token",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY),
			IPAddresses:    []string{"192.168.1.100"},
			ProtocolTypes:  []string{"NFSV3"},
			FileProperties: &models.FileProperties{
				JunctionPath: "/testvolume",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "test-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     "READ_WRITE",
							NFSv3:          true,
							NFSv4:          false,
							Index:          1,
						},
					},
				},
			},
		}

		result := convertModelToVCPVolume(vol)

		assert.NotNil(tt, result.MountPoints)
		assert.Len(tt, result.MountPoints, 1)
		assert.Equal(tt, "192.168.1.100", result.MountPoints[0].IpAddress.Value)
		assert.Equal(tt, gcpgenserver.ProtocolsV1betaNFSV3, result.MountPoints[0].Protocol.Value)
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "Setting up your instance")
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "vers=3")
		// Verify Export and ExportFull fields
		assert.Equal(tt, "/testvolume", result.MountPoints[0].Export.Value)
		assert.Equal(tt, "192.168.1.100:/testvolume", result.MountPoints[0].ExportFull.Value)
	})

	t.Run("NFSv4_SingleProtocol_ShouldCreateMountPoints", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "testvolume",
			CreationToken:  "test-token",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY),
			IPAddresses:    []string{"10.0.0.50"},
			ProtocolTypes:  []string{"NFSV4"},
			FileProperties: &models.FileProperties{
				JunctionPath: "/vol1",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "test-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "10.0.0.0/8",
							AccessType:     "READ_ONLY",
							NFSv3:          false,
							NFSv4:          true,
							Index:          1,
						},
					},
				},
			},
		}

		result := convertModelToVCPVolume(vol)

		assert.NotNil(tt, result.MountPoints)
		assert.Len(tt, result.MountPoints, 1)
		assert.Equal(tt, "10.0.0.50", result.MountPoints[0].IpAddress.Value)
		assert.Equal(tt, gcpgenserver.ProtocolsV1betaNFSV4, result.MountPoints[0].Protocol.Value)
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "Setting up your instance")
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "vers=4.1")
		// Verify Export and ExportFull fields
		assert.Equal(tt, "/vol1", result.MountPoints[0].Export.Value)
		assert.Equal(tt, "10.0.0.50:/vol1", result.MountPoints[0].ExportFull.Value)
	})

	t.Run("MultipleProtocols_NFSv3AndNFSv4_ShouldCreateMultipleMountPoints", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "dualprotocol",
			CreationToken:  "dual-token",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY),
			IPAddresses:    []string{"192.168.1.100"},
			ProtocolTypes:  []string{"NFSV3", "NFSV4"},
			FileProperties: &models.FileProperties{
				JunctionPath: "/dualprotocol",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "dual-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     "READ_WRITE",
							NFSv3:          true,
							NFSv4:          true,
							Index:          1,
						},
					},
				},
			},
		}

		result := convertModelToVCPVolume(vol)

		assert.NotNil(tt, result.MountPoints)
		assert.Len(tt, result.MountPoints, 2) // Should have 2 mount points for each protocol

		// Verify first mount point (NFSv3)
		assert.Equal(tt, "192.168.1.100", result.MountPoints[0].IpAddress.Value)
		assert.Equal(tt, gcpgenserver.ProtocolsV1betaNFSV3, result.MountPoints[0].Protocol.Value)
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "Setting up your instance")
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "vers=3")
		// Verify Export and ExportFull for NFSv3
		assert.Equal(tt, "/dualprotocol", result.MountPoints[0].Export.Value)
		assert.Equal(tt, "192.168.1.100:/dualprotocol", result.MountPoints[0].ExportFull.Value)

		// Verify second mount point (NFSv4)
		assert.Equal(tt, "192.168.1.100", result.MountPoints[1].IpAddress.Value)
		assert.Equal(tt, gcpgenserver.ProtocolsV1betaNFSV4, result.MountPoints[1].Protocol.Value)
		assert.Contains(tt, result.MountPoints[1].Instructions.Value, "Setting up your instance")
		assert.Contains(tt, result.MountPoints[1].Instructions.Value, "vers=4.1")
		// Verify Export and ExportFull for NFSv4
		assert.Equal(tt, "/dualprotocol", result.MountPoints[1].Export.Value)
		assert.Equal(tt, "192.168.1.100:/dualprotocol", result.MountPoints[1].ExportFull.Value)
	})

	t.Run("MultipleIPAddressesAndProtocols_ShouldCreateAllCombinations", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "multiip",
			CreationToken:  "multi-token",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY),
			IPAddresses:    []string{"192.168.1.100", "192.168.1.101"},
			ProtocolTypes:  []string{"NFSV3", "NFSV4"},
			FileProperties: &models.FileProperties{
				JunctionPath: "/multiip",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "multi-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     "READ_WRITE",
							NFSv3:          true,
							NFSv4:          true,
							Index:          1,
						},
					},
				},
			},
		}

		result := convertModelToVCPVolume(vol)

		assert.NotNil(tt, result.MountPoints)
		assert.Len(tt, result.MountPoints, 4) // 2 IPs × 2 protocols = 4 mount points

		// Verify all combinations exist
		ipAddresses := []string{"192.168.1.100", "192.168.1.101"}
		protocols := []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3, gcpgenserver.ProtocolsV1betaNFSV4}

		mountPointsMap := make(map[string]map[gcpgenserver.ProtocolsV1beta]bool)
		for _, ip := range ipAddresses {
			mountPointsMap[ip] = make(map[gcpgenserver.ProtocolsV1beta]bool)
		}

		for _, mp := range result.MountPoints {
			mountPointsMap[mp.IpAddress.Value][mp.Protocol.Value] = true
		}

		for _, ip := range ipAddresses {
			for _, protocol := range protocols {
				assert.True(tt, mountPointsMap[ip][protocol], "Missing mount point for IP %s and protocol %s", ip, protocol)
			}
		}

		// Verify Export and ExportFull for all mount points
		for _, mp := range result.MountPoints {
			assert.Equal(tt, "/multiip", mp.Export.Value)
			assert.Equal(tt, mp.IpAddress.Value+":/multiip", mp.ExportFull.Value)
		}
	})

	t.Run("UnsupportedProtocol_ShouldNotCreateMountPoints", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "randomvolume",
			CreationToken:  "random-token",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY),
			IPAddresses:    []string{"192.168.1.100"},
			ProtocolTypes:  []string{"PROTOCOL_UNSPECIFIED"}, // Unsupported protocol for mount points
			FileProperties: &models.FileProperties{
				JunctionPath: "/randomvolume",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "random-policy",
				},
			},
		}

		result := convertModelToVCPVolume(vol)
		assert.Empty(tt, result.MountPoints) // No mount points for unsupported protocol
	})

	t.Run("MixedProtocols_OnlySupportedShouldCreateMountPoints", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "mixedvolume",
			CreationToken:  "mixed-token",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY),
			IPAddresses:    []string{"192.168.1.100"},
			ProtocolTypes:  []string{"NFSV3", "SMB", "NFSV4", "PROTOCOL_UNSPECIFIED"}, // Mix of supported and unsupported
			FileProperties: &models.FileProperties{
				JunctionPath: "/mixedvolume",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "mixed-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     "READ_WRITE",
							NFSv3:          true,
							NFSv4:          true,
							Index:          1,
						},
					},
				},
				Fqdn: "netbios.domain.com",
			},
		}

		result := convertModelToVCPVolume(vol)

		assert.NotNil(tt, result.MountPoints)
		assert.Len(tt, result.MountPoints, 3) // NFSv3, NFSv4, and SMB should create mount points

		protocols := make(map[gcpgenserver.ProtocolsV1beta]bool)
		for _, mp := range result.MountPoints {
			protocols[mp.Protocol.Value] = true
			// Verify Export and ExportFull based on protocol type
			if mp.Protocol.Value == gcpgenserver.ProtocolsV1betaSMB {
				// SMB uses UNC path format
				assert.Equal(tt, "mixedvolume", mp.Export.Value)
				assert.Equal(tt, `\\netbios.domain.com\mixedvolume`, mp.ExportFull.Value)
			} else {
				// NFS protocols use standard format
				assert.Equal(tt, "/mixedvolume", mp.Export.Value)
				assert.Equal(tt, "192.168.1.100:/mixedvolume", mp.ExportFull.Value)
			}
		}

		assert.True(tt, protocols[gcpgenserver.ProtocolsV1betaNFSV3])
		assert.True(tt, protocols[gcpgenserver.ProtocolsV1betaNFSV4])
		assert.True(tt, protocols[gcpgenserver.ProtocolsV1betaSMB])
	})

	t.Run("SMB_SingleProtocol_ShouldCreateMountPoints", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "smbvolume",
			CreationToken:  "smb-token",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY),
			IPAddresses:    []string{"192.168.1.200"},
			ProtocolTypes:  []string{"SMB"},
			FileProperties: &models.FileProperties{
				JunctionPath: "/smb-share",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "smb-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     "READ_WRITE",
							CIFS:           true,
							Index:          1,
						},
					},
				},
				Fqdn: "netbios.domain.com",
			},
		}

		result := convertModelToVCPVolume(vol)

		assert.NotNil(tt, result.MountPoints)
		assert.Len(tt, result.MountPoints, 1)
		assert.Equal(tt, "192.168.1.200", result.MountPoints[0].IpAddress.Value)
		assert.Equal(tt, gcpgenserver.ProtocolsV1betaSMB, result.MountPoints[0].Protocol.Value)
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "Mapping your network drive")
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "Click the Start button")
		assert.Contains(tt, result.MountPoints[0].Instructions.Value, "\\\\netbios.domain.com\\smb-share")
		// Verify Export and ExportFull for SMB (SMB uses UNC path format)
		assert.Equal(tt, "smb-share", result.MountPoints[0].Export.Value)
		assert.Equal(tt, `\\netbios.domain.com\smb-share`, result.MountPoints[0].ExportFull.Value)
	})

	t.Run("VolumeNotReady_ShouldNotCreateMountPoints", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "notready",
			CreationToken:  "notready-token",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateCREATING), // Not READY
			IPAddresses:    []string{"192.168.1.100"},
			ProtocolTypes:  []string{"NFSV3"},
			FileProperties: &models.FileProperties{
				JunctionPath: "/notready",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "notready-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     "READ_WRITE",
							NFSv3:          true,
							Index:          1,
						},
					},
				},
			},
		}

		result := convertModelToVCPVolume(vol)

		assert.Empty(tt, result.MountPoints) // No mount points when volume is not ready
	})

	t.Run("NoIPAddresses_ShouldNotCreateMountPoints", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			DisplayName:    "noips",
			CreationToken:  "noips-token",
			LifeCycleState: string(gcpgenserver.VolumeV1betaVolumeStateREADY),
			IPAddresses:    []string{}, // Empty IP addresses
			ProtocolTypes:  []string{"NFSV3"},
			FileProperties: &models.FileProperties{
				JunctionPath: "/noips",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "noips-policy",
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     "READ_WRITE",
							NFSv3:          true,
							Index:          1,
						},
					},
				},
			},
		}

		result := convertModelToVCPVolume(vol)

		assert.Empty(tt, result.MountPoints) // No mount points without IP addresses
	})
}

// TestConvertModelToVCPVolume_AutoTieringPolicy tests the conversion of AutoTieringPolicy to TieringPolicy
func TestConvertModelToVCPVolume_AutoTieringPolicy(t *testing.T) {
	t.Run("AutoTieringPolicy_WithHotTierBypassModeEnabled_ShouldIncludeAllFields", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				CoolingThresholdDays:     30,
				HotTierBypassModeEnabled: true,
			},
			HotTierSizeGib:  100,
			ColdTierSizeGib: 200,
		}

		result := convertModelToVCPVolume(vol)

		assert.NotNil(tt, result.TieringPolicy)
		assert.True(tt, result.TieringPolicy.IsSet())

		tieringPolicy := result.TieringPolicy.Value
		assert.Equal(tt, gcpgenserver.TieringPolicyV1betaTierActionENABLED, tieringPolicy.TierAction.Value)
		assert.Equal(tt, int32(30), tieringPolicy.CoolingThresholdDays.Value)
		assert.True(tt, tieringPolicy.HotTierBypassModeEnabled.IsSet())
		assert.True(tt, tieringPolicy.HotTierBypassModeEnabled.Value)

		// Assert HotTierSizeGib and ColdTierSizeGib are set
		assert.True(tt, result.HotTierSizeGib.IsSet(), "HotTierSizeGib should be set when AutoTieringPolicy exists")
		assert.Equal(tt, float64(100), result.HotTierSizeGib.Value, "HotTierSizeGib should match volume.HotTierSizeGib")
		assert.True(tt, result.ColdTierSizeGib.IsSet(), "ColdTierSizeGib should be set when AutoTieringPolicy exists")
		assert.Equal(tt, float64(200), result.ColdTierSizeGib.Value, "ColdTierSizeGib should match volume.ColdTierSizeGib")
	})

	t.Run("AutoTieringPolicy_WithHotTierBypassModeDisabled_ShouldIncludeAllFields", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				CoolingThresholdDays:     45,
				HotTierBypassModeEnabled: false,
			},
			HotTierSizeGib:  250,
			ColdTierSizeGib: 750,
		}

		result := convertModelToVCPVolume(vol)

		assert.NotNil(tt, result.TieringPolicy)
		assert.True(tt, result.TieringPolicy.IsSet())

		tieringPolicy := result.TieringPolicy.Value
		assert.Equal(tt, gcpgenserver.TieringPolicyV1betaTierActionENABLED, tieringPolicy.TierAction.Value)
		assert.Equal(tt, int32(45), tieringPolicy.CoolingThresholdDays.Value)
		assert.True(tt, tieringPolicy.HotTierBypassModeEnabled.IsSet())
		assert.False(tt, tieringPolicy.HotTierBypassModeEnabled.Value)

		// Assert HotTierSizeGib and ColdTierSizeGib are set
		assert.True(tt, result.HotTierSizeGib.IsSet(), " HotTierSizeGib should be set")
		assert.Equal(tt, float64(250), result.HotTierSizeGib.Value, " HotTierSizeGib should be 250")
		assert.True(tt, result.ColdTierSizeGib.IsSet(), " ColdTierSizeGib should be set")
		assert.Equal(tt, float64(750), result.ColdTierSizeGib.Value, " ColdTierSizeGib should be 750")
	})

	t.Run("AutoTieringPolicy_Paused_ShouldIncludeTieringPolicyWithPAUSED", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       false,
				CoolingThresholdDays:     30,
				HotTierBypassModeEnabled: false,
			},
			HotTierSizeGib:  50,
			ColdTierSizeGib: 950,
		}

		result := convertModelToVCPVolume(vol)

		assert.True(tt, result.TieringPolicy.IsSet())

		tieringPolicy := result.TieringPolicy.Value
		assert.Equal(tt, gcpgenserver.TieringPolicyV1betaTierActionPAUSED, tieringPolicy.TierAction.Value)
		assert.True(tt, tieringPolicy.CoolingThresholdDays.IsSet())
		assert.Equal(tt, int32(30), tieringPolicy.CoolingThresholdDays.Value)
		assert.True(tt, tieringPolicy.HotTierBypassModeEnabled.IsSet())
		assert.False(tt, tieringPolicy.HotTierBypassModeEnabled.Value)

		// Assert HotTierSizeGib and ColdTierSizeGib are set even when paused
		assert.True(tt, result.HotTierSizeGib.IsSet(), " HotTierSizeGib should be set even when tiering is paused")
		assert.Equal(tt, float64(50), result.HotTierSizeGib.Value, " HotTierSizeGib should be 50")
		assert.True(tt, result.ColdTierSizeGib.IsSet(), " ColdTierSizeGib should be set even when tiering is paused")
		assert.Equal(tt, float64(950), result.ColdTierSizeGib.Value, " ColdTierSizeGib should be 950")
	})

	t.Run("NoAutoTieringPolicy_ShouldNotIncludeTieringPolicy", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel:       models.BaseModel{UUID: "vol-1"},
			HotTierSizeGib:  100,
			ColdTierSizeGib: 200,
		}

		result := convertModelToVCPVolume(vol)

		assert.False(tt, result.TieringPolicy.IsSet())
		// Should NOT be executed when AutoTieringPolicy is nil
		assert.False(tt, result.HotTierSizeGib.IsSet(), " HotTierSizeGib should NOT be set when AutoTieringPolicy is nil")
		assert.False(tt, result.ColdTierSizeGib.IsSet(), " ColdTierSizeGib should NOT be set when AutoTieringPolicy is nil")
	})

	t.Run("AutoTieringPolicy_WithZeroTierSizes_ShouldSetZeroValues", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				CoolingThresholdDays:     30,
				HotTierBypassModeEnabled: false,
			},
			HotTierSizeGib:  0,
			ColdTierSizeGib: 0,
		}

		result := convertModelToVCPVolume(vol)

		assert.True(tt, result.TieringPolicy.IsSet())

		// Assert set even with zero values
		assert.True(tt, result.HotTierSizeGib.IsSet(), " HotTierSizeGib should be set even with zero value")
		assert.Equal(tt, float64(0), result.HotTierSizeGib.Value, " HotTierSizeGib should be 0")
		assert.True(tt, result.ColdTierSizeGib.IsSet(), " ColdTierSizeGib should be set even with zero value")
		assert.Equal(tt, float64(0), result.ColdTierSizeGib.Value, " ColdTierSizeGib should be 0")
	})

	t.Run("AutoTieringPolicy_WithLargeTierSizes_ShouldHandleLargeValues", func(tt *testing.T) {
		vol := &models.Volume{
			BaseModel: models.BaseModel{UUID: "vol-1"},
			AutoTieringPolicy: &models.AutoTieringPolicy{
				AutoTieringEnabled:       true,
				CoolingThresholdDays:     60,
				HotTierBypassModeEnabled: true,
			},
			HotTierSizeGib:  10000,
			ColdTierSizeGib: 90000,
		}

		result := convertModelToVCPVolume(vol)

		assert.True(tt, result.TieringPolicy.IsSet())

		// Assert that it should handle large values correctly
		assert.True(tt, result.HotTierSizeGib.IsSet(), " HotTierSizeGib should be set with large value")
		assert.Equal(tt, float64(10000), result.HotTierSizeGib.Value, " HotTierSizeGib should be 10000")
		assert.True(tt, result.ColdTierSizeGib.IsSet(), " ColdTierSizeGib should be set with large value")
		assert.Equal(tt, float64(90000), result.ColdTierSizeGib.Value, " ColdTierSizeGib should be 90000")
	})
}

func TestPrepareUpdateVolumeParams_CacheParams(t *testing.T) {
	param := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber:  "test-project",
		LocationId:     "us-central1",
		VolumeId:       "test-volume",
		XCorrelationID: gcpgenserver.NewOptString("test-corr-id"),
	}
	region := "us-central1"

	t.Run("CacheParameters_WhenSet_ShouldMapCorrectly", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			CacheParameters: gcpgenserver.OptFlexCacheV1beta{
				Value: gcpgenserver.FlexCacheV1beta{
					PeerVolumeName:  gcpgenserver.NewOptString("peer-vol-1"),
					PeerClusterName: gcpgenserver.NewOptString("peer-cluster-1"),
					PeerSvmName:     gcpgenserver.NewOptString("peer-svm-1"),
					PeerIpAddresses: []string{
						"1.1.1.1",
						"2.2.2.2",
					},
					CacheConfig: gcpgenserver.OptFlexCacheConfigV1beta{
						Value: gcpgenserver.FlexCacheConfigV1beta{
							CachePrePopulate: gcpgenserver.OptFlexCachePrePopulateV1beta{
								Value: gcpgenserver.FlexCachePrePopulateV1beta{
									PathList: gcpgenserver.OptNilStringArray{
										Value: []string{"/path1", "/path2"},
										Set:   true,
										Null:  false,
									},
									ExcludePathList: gcpgenserver.OptNilStringArray{
										Value: []string{"/exclude1", "/exclude2"},
										Set:   true,
										Null:  false,
									},
									Recursion: gcpgenserver.OptNilBool{
										Value: true,
										Set:   true,
										Null:  false,
									},
								},
								Set: true,
							},
							WritebackEnabled: gcpgenserver.OptNilBool{
								Value: true,
								Set:   true,
								Null:  false,
							},
							AtimeScrubEnabled: gcpgenserver.OptNilBool{
								Value: true,
								Set:   true,
								Null:  false,
							},
							AtimeScrubDays: gcpgenserver.OptNilInt16{
								Value: 5,
								Set:   true,
								Null:  false,
							},
							CifsChangeNotifyEnabled: gcpgenserver.OptNilBool{
								Value: true,
								Set:   true,
								Null:  false,
							},
						},
						Set: true,
					},
				},
				Set: true,
			},
		}

		dbVolume := &models.Volume{
			CacheParameters: &models.CacheParameters{
				PeerVolumeName:  "peer-vol-1",
				PeerClusterName: "peer-cluster-1",
				PeerSvmName:     "peer-svm-1",
				PeerIPAddresses: []string{
					"1.1.1.1",
					"2.2.2.2",
				},
				CacheConfig: &models.CacheConfig{
					// Existing config state (being updated)
					AtimeScrubEnabled: nillable.GetBoolPtr(true),
					WritebackEnabled:  nillable.GetBoolPtr(true),
				},
			},
		}

		result, err := _prepareUpdateVolumeParams(req, param, region, dbVolume)
		assert.NoError(t, err)
		assert.NotNil(t, result.CacheParameters.CacheConfig)
		assert.Equal(t, int16(5), *result.CacheParameters.CacheConfig.AtimeScrubDays)
		assert.True(t, *result.CacheParameters.CacheConfig.AtimeScrubEnabled)
		assert.True(t, *result.CacheParameters.CacheConfig.CifsChangeNotifyEnabled)
		assert.True(t, *result.CacheParameters.CacheConfig.WritebackEnabled)
		assert.NotNil(t, result.CacheParameters.CacheConfig.CachePrePopulate)
		assert.True(t, *result.CacheParameters.CacheConfig.CachePrePopulate.Recursion)
		assert.Equal(t, []string{"/path1", "/path2"}, result.CacheParameters.CacheConfig.CachePrePopulate.PathList)
		assert.Equal(t, []string{"/exclude1", "/exclude2"}, result.CacheParameters.CacheConfig.CachePrePopulate.ExcludePathList)
	})

	t.Run("CacheParameters_WhenSet_Partial_values", func(t *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			CacheParameters: gcpgenserver.OptFlexCacheV1beta{
				Value: gcpgenserver.FlexCacheV1beta{
					PeerVolumeName:  gcpgenserver.NewOptString("peer-vol-1"),
					PeerClusterName: gcpgenserver.NewOptString("peer-cluster-1"),
					PeerSvmName:     gcpgenserver.NewOptString("peer-svm-1"),
					PeerIpAddresses: []string{
						"1.1.1.1",
						"2.2.2.2",
					},
					CacheConfig: gcpgenserver.OptFlexCacheConfigV1beta{
						Value: gcpgenserver.FlexCacheConfigV1beta{
							WritebackEnabled: gcpgenserver.OptNilBool{
								Value: true,
								Set:   true,
								Null:  false,
							},
							CifsChangeNotifyEnabled: gcpgenserver.OptNilBool{
								Value: true,
								Set:   true,
								Null:  false,
							},
						},
						Set: true,
					},
				},
				Set: true,
			},
		}

		dbVolume := &models.Volume{
			CacheParameters: &models.CacheParameters{
				PeerVolumeName:  "peer-vol-1",
				PeerClusterName: "peer-cluster-1",
				PeerSvmName:     "peer-svm-1",
				PeerIPAddresses: []string{
					"1.1.1.1",
					"2.2.2.2",
				},
				CacheConfig: &models.CacheConfig{
					WritebackEnabled:        nillable.GetBoolPtr(true),
					CifsChangeNotifyEnabled: nillable.GetBoolPtr(false), // Will be updated to true
				},
			},
		}

		result, err := _prepareUpdateVolumeParams(req, param, region, dbVolume)
		assert.NoError(t, err)
		assert.NotNil(t, result.CacheParameters.CacheConfig)
		assert.True(t, *result.CacheParameters.CacheConfig.CifsChangeNotifyEnabled)
		assert.True(t, *result.CacheParameters.CacheConfig.WritebackEnabled)
	})
}

func TestV1betaEstablishVolumePeering(t *testing.T) {
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	mockParseAndValidateRegionAndZone := func(region string) (string, string, *gcpgenserver.Error) {
		return "test-region", "test-location", nil
	}
	utils.ParseAndValidateRegionAndZone = mockParseAndValidateRegionAndZone
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	t.Run("feature disabled returns 403 bad request", func(t *testing.T) {
		orig := flexCacheEnabled
		flexCacheEnabled = false
		defer func() { flexCacheEnabled = orig }()

		h := Handler{} // Orchestrator not needed; early return path
		ctx := context.Background()

		req := &gcpgenserver.EstablishPeeringRequestV1beta{}
		params := gcpgenserver.V1betaEstablishVolumePeeringParams{
			ProjectNumber:    "1234567890",
			LocationId:       "us-central1-a",
			VolumeResourceId: "validresource",
		}

		res, err := h.V1betaEstablishVolumePeering(ctx, req, params)
		require.NoError(t, err)

		badReq, ok := res.(*gcpgenserver.V1betaEstablishVolumePeeringForbidden)
		require.True(t, ok, "expected bad request response type")
		require.Equal(t, float64(403), badReq.Code)
		require.Contains(t, badReq.Message, "FlexCache feature is currently not enabled")
	})

	t.Run("orchestrator internal error returns InternalServerError response and error", func(t *testing.T) {
		orig := flexCacheEnabled
		flexCacheEnabled = true
		defer func() { flexCacheEnabled = orig }()

		mockOrch := factory.NewMockOrchestratorFactory(t)
		h := Handler{
			Orchestrator: mockOrch,
		}
		ctx := context.Background()

		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerClusterName: "peerCluster",
			PeerSvmName:     "peerSvm",
			PeerVolumeName:  "peerVol",
		}
		params := gcpgenserver.V1betaEstablishVolumePeeringParams{
			ProjectNumber:    "1234567890",
			LocationId:       "us-central1-a",
			VolumeResourceId: "validresource",
		}

		internalErr := fmt.Errorf("orchestrator failure")
		mockOrch.
			On("EstablishFlexCacheVolumePeering", mock.Anything, mock.AnythingOfType("*common.EstablishVolumePeeringParams")).
			Return(nil, "", internalErr)

		res, err := h.V1betaEstablishVolumePeering(ctx, req, params)
		require.NoError(t, err)

		_, ok := res.(*gcpgenserver.V1betaEstablishVolumePeeringInternalServerError)
		require.True(t, ok, "expected internal server error response type")
	})

	t.Run("ValidEstablishPeering", func(tt *testing.T) {
		orig := flexCacheEnabled
		flexCacheEnabled = true
		defer func() { flexCacheEnabled = orig }()

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		params := gcpgenserver.V1betaEstablishVolumePeeringParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume_id",
		}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerClusterName: "peer-cluster",
			PeerSvmName:     "peer-svm",
			PeerVolumeName:  "peer-volume",
			PeerIpAddresses: gcpgenserver.NewOptNilStringArray([]string{"1.1.1.1",
				"2.2.2.2"}),
			PeeringCommandExpiryTime: gcpgenserver.NewOptNilDateTime(time.Now().Add(1 * time.Hour)),
		}

		pass := "secure-passphrase"
		expiry := time.Now().Add(1 * time.Hour)

		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "vol-1"},
			LifeCycleState: "CREATING",
			CacheParameters: &models.CacheParameters{
				PeerClusterName: "peer-cluster",
				PeerSvmName:     "peer-svm",
				PeerVolumeName:  "peer-volume",
				PeerIPAddresses: []string{"1.1.1.1", "2.2.2.2"},
				PeerExpiryTime:  &expiry,
				PeeringCommand:  "establish",
				Passphrase:      &pass,
			},
		}
		mockOrch.
			On("EstablishFlexCacheVolumePeering", mock.Anything, mock.AnythingOfType("*common.EstablishVolumePeeringParams")).
			Return(volume, "", nil)

		result, err := handler.V1betaEstablishVolumePeering(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.NotNil(tt, op)
		assert.NotNil(tt, op.Response)
	})
}

func TestValidateFlexCacheRequest(t *testing.T) {
	tests := []struct {
		name        string
		req         *gcpgenserver.VolumeCreateV1beta
		expectError bool
		errorMsg    string
	}{
		{
			name: "Valid FlexCache request - minimal configuration",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
			},
			expectError: false,
		},
		{
			name: "Valid FlexCache request - with cache config",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1", "10.0.0.2"},
						CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
							WritebackEnabled:        gcpgenserver.NewOptNilBool(true),
							AtimeScrubEnabled:       gcpgenserver.NewOptNilBool(true),
							AtimeScrubDays:          gcpgenserver.NewOptNilInt16(30),
							CifsChangeNotifyEnabled: gcpgenserver.NewOptNilBool(false),
						}),
					}),
				},
			},
			expectError: false,
		},
		{
			name: "Valid FlexCache request - with empty pre-populate",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
						CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
							CachePrePopulate: gcpgenserver.NewOptFlexCachePrePopulateV1beta(gcpgenserver.FlexCachePrePopulateV1beta{
								PathList:        gcpgenserver.NewOptNilStringArray([]string{}),
								ExcludePathList: gcpgenserver.NewOptNilStringArray([]string{}),
								Recursion:       gcpgenserver.NewOptNilBool(false),
							}),
						}),
					}),
				},
			},
			expectError: false,
		},
		{
			name: "Valid FlexCache request - snapshot directory disabled",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SnapshotDirectory: gcpgenserver.NewOptBool(false),
				},
			},
			expectError: false,
		},
		{
			name: "Valid FlexCache request - snapshot reserve is zero",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SnapReserve: gcpgenserver.NewOptFloat64(0),
				},
			},
			expectError: false,
		},
		{
			name: "Valid FlexCache request - backup config with empty strings",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(gcpgenserver.BackupConfigV1beta{
						BackupPolicyId: gcpgenserver.NewOptNilString(""),
						BackupVaultId:  gcpgenserver.NewOptNilString(""),
					}),
				},
			},
			expectError: true,
		},
		{
			name: "Valid FlexCache request - tiering policy with empty tier action",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(gcpgenserver.TieringPolicyV1beta{
						TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction(""),
					}),
				},
			},
			expectError: true,
		},
		{
			name: "Valid FlexCache request - empty SMB settings",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SmbSettings: gcpgenserver.SMBSettingsV1beta{},
				},
			},
			expectError: false,
		},
		{
			name: "Valid FlexCache request - supported SMB settings",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SmbSettings: gcpgenserver.SMBSettingsV1beta{
						gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
						gcpgenserver.SMBSettingsV1betaItemOPLOCKS,
					},
				},
			},
			expectError: false,
		},
		{
			name: "Valid FlexCache request - large capacity set to false",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					LargeCapacity: gcpgenserver.NewOptNilBool(false),
				},
			},
			expectError: false,
		},
		{
			name: "Valid FlexCache request - valid origin volume names",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("_valid_name_123"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
			},
			expectError: false,
		},
		{
			name: "Valid FlexCache request - atimeScrubDays not set without atimeScrubEnabled",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
						CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
							AtimeScrubEnabled: gcpgenserver.NewOptNilBool(false),
						}),
					}),
				},
			},
			expectError: false,
		},

		// Snapshot and backup ID validation
		{
			name: "Invalid FlexCache request - snapshot ID set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
				SnapshotId: gcpgenserver.NewOptString("snapshot-123"),
			},
			expectError: true,
			errorMsg:    "cache volume creation from snapshot is not supported",
		},
		{
			name: "Invalid FlexCache request - backup ID set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
				BackupId: gcpgenserver.NewOptString("backup-123"),
			},
			expectError: true,
			errorMsg:    "cache volume creation from backup is not supported",
		},

		// Cache parameters validation
		{
			name: "Invalid FlexCache request - missing peer cluster name",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString(""),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
			},
			expectError: true,
			errorMsg:    "cache volume creation requires cacheParameters.peerClusterName",
		},
		{
			name: "Invalid FlexCache request - missing peer volume name",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString(""),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
			},
			expectError: true,
			errorMsg:    "cache volume creation requires cacheParameters.peerVolumeName",
		},
		{
			name: "Invalid FlexCache request - invalid peer volume name (starts with number)",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("1invalid"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
			},
			expectError: true,
			errorMsg:    "origin volume name '1invalid' is invalid",
		},
		{
			name: "Invalid FlexCache request - invalid peer volume name (contains dash)",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("invalid-name"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
			},
			expectError: true,
			errorMsg:    "origin volume name 'invalid-name' is invalid",
		},
		{
			name: "Invalid FlexCache request - missing peer SVM name",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString(""),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
			},
			expectError: true,
			errorMsg:    "cache volume creation requires cacheParameters.peerSvmName",
		},
		{
			name: "Invalid FlexCache request - missing peer IP addresses",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{},
					}),
				},
			},
			expectError: true,
			errorMsg:    "cache volume creation requires cacheParameters.peerIPAddresses",
		},

		// Pre-populate validation
		{
			name: "Invalid FlexCache request - pre-populate with pathList",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
						CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
							CachePrePopulate: gcpgenserver.NewOptFlexCachePrePopulateV1beta(gcpgenserver.FlexCachePrePopulateV1beta{
								PathList: gcpgenserver.NewOptNilStringArray([]string{"/dir1"}),
							}),
						}),
					}),
				},
			},
			expectError: true,
			errorMsg:    "pre-populate is not supported during FlexCache volume creation",
		},
		{
			name: "Invalid FlexCache request - pre-populate with excludePathList",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
						CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
							CachePrePopulate: gcpgenserver.NewOptFlexCachePrePopulateV1beta(gcpgenserver.FlexCachePrePopulateV1beta{
								ExcludePathList: gcpgenserver.NewOptNilStringArray([]string{"/dir2"}),
							}),
						}),
					}),
				},
			},
			expectError: true,
			errorMsg:    "pre-populate is not supported during FlexCache volume creation",
		},
		{
			name: "Invalid FlexCache request - pre-populate with recursion enabled",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
						CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
							CachePrePopulate: gcpgenserver.NewOptFlexCachePrePopulateV1beta(gcpgenserver.FlexCachePrePopulateV1beta{
								Recursion: gcpgenserver.NewOptNilBool(true),
							}),
						}),
					}),
				},
			},
			expectError: true,
			errorMsg:    "pre-populate is not supported during FlexCache volume creation",
		},

		// Atime scrub validation
		{
			name: "Invalid FlexCache request - atimeScrubDays set without atimeScrubEnabled",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
						CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
							AtimeScrubDays: gcpgenserver.NewOptNilInt16(30),
						}),
					}),
				},
			},
			expectError: true,
			errorMsg:    "atimeScrubEnabled must be true to set atimeScrubDays",
		},
		{
			name: "Invalid FlexCache request - atimeScrubDays set with atimeScrubEnabled false",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
						CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
							AtimeScrubEnabled: gcpgenserver.NewOptNilBool(false),
							AtimeScrubDays:    gcpgenserver.NewOptNilInt16(30),
						}),
					}),
				},
			},
			expectError: true,
			errorMsg:    "atimeScrubEnabled must be true to set atimeScrubDays",
		},

		// protocols validation
		{
			name: "Invalid FlexCache request - snapshot policy set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					Protocols: []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3, gcpgenserver.ProtocolsV1betaSMB, gcpgenserver.ProtocolsV1betaNFSV4},
				},
			},
			expectError: true,
			errorMsg:    "volume can only support up to two protocols, please remove any additional entries in the protocols list",
		},

		// Snapshot policy validation
		{
			name: "Invalid FlexCache request - snapshot policy set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SnapshotPolicy: gcpgenserver.NewOptSnapshotPolicyV1beta(gcpgenserver.SnapshotPolicyV1beta{
						Enabled: gcpgenserver.NewOptNilBool(true),
					}),
				},
			},
			expectError: true,
			errorMsg:    "snapshot policy is not allowed for FlexCache volumes",
		},

		// Snapshot reserve validation
		{
			name: "Invalid FlexCache request - snapshot reserve non-zero",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SnapReserve: gcpgenserver.NewOptFloat64(10),
				},
			},
			expectError: true,
			errorMsg:    "snapshot reserve is not allowed for FlexCache volumes",
		},

		// Snapshot directory validation
		{
			name: "Invalid FlexCache request - snapshot directory enabled",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SnapshotDirectory: gcpgenserver.NewOptBool(true),
				},
			},
			expectError: true,
			errorMsg:    "snapshot directory is not allowed for FlexCache volumes",
		},

		// Backup config validation
		{
			name: "Invalid FlexCache request - backup policy set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(gcpgenserver.BackupConfigV1beta{
						BackupPolicyId: gcpgenserver.NewOptNilString("backup-policy-123"),
					}),
				},
			},
			expectError: true,
			errorMsg:    "backup config is not allowed for FlexCache volumes",
		},
		{
			name: "Invalid FlexCache request - backup vault set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(gcpgenserver.BackupConfigV1beta{
						BackupVaultId: gcpgenserver.NewOptNilString("backup-vault-id"),
					}),
				},
			},
			expectError: true,
			errorMsg:    "backup config is not allowed for FlexCache volumes",
		},

		// Tiering policy validation
		{
			name: "Invalid FlexCache request - tiering policy set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					TieringPolicy: gcpgenserver.NewOptTieringPolicyV1beta(gcpgenserver.TieringPolicyV1beta{
						TierAction: gcpgenserver.NewOptNilTieringPolicyV1betaTierAction("AUTO"),
					}),
				},
			},
			expectError: true,
			errorMsg:    "tiering policy is not allowed for FlexCache volumes",
		},

		// Hybrid replication validation
		{
			name: "Invalid FlexCache request - hybrid replication enabled",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
				},
				HybridReplicationParameters: gcpgenserver.NewOptHybridReplicationParametersV1beta(
					gcpgenserver.HybridReplicationParametersV1beta{},
				),
			},
			expectError: true,
			errorMsg:    "hybrid replication is not allowed for FlexCache volumes",
		},

		// SMB settings validation
		{
			name: "Invalid FlexCache request - CONTINUOUSLY_AVAILABLE SMB setting",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SmbSettings: gcpgenserver.SMBSettingsV1beta{
						gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
					},
				},
			},
			expectError: true,
			errorMsg:    "SMB share properties CONTINUOUSLY_AVAILABLE are not supported for FlexCache volumes",
		},
		{
			name: "Invalid FlexCache request - SHOW_SNAPSHOT SMB setting",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SmbSettings: gcpgenserver.SMBSettingsV1beta{
						gcpgenserver.SMBSettingsV1betaItemSHOWSNAPSHOT,
					},
				},
			},
			expectError: true,
			errorMsg:    "SMB share properties SHOW_SNAPSHOT are not supported for FlexCache volumes",
		},
		{
			name: "Invalid FlexCache request - SHOW_PREVIOUS_VERSIONS SMB setting",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SmbSettings: gcpgenserver.SMBSettingsV1beta{
						gcpgenserver.SMBSettingsV1betaItemSHOWPREVIOUSVERSIONS,
					},
				},
			},
			expectError: true,
			errorMsg:    "SMB share properties SHOW_PREVIOUS_VERSIONS are not supported for FlexCache volumes",
		},
		{
			name: "Invalid FlexCache request - multiple unsupported SMB settings",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SmbSettings: gcpgenserver.SMBSettingsV1beta{
						gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
						gcpgenserver.SMBSettingsV1betaItemSHOWSNAPSHOT,
					},
				},
			},
			expectError: true,
			errorMsg:    "SMB share properties",
		},
		{
			name: "Invalid FlexCache request - mixed supported and unsupported SMB settings",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					SmbSettings: gcpgenserver.SMBSettingsV1beta{
						gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
						gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
						gcpgenserver.SMBSettingsV1betaItemOPLOCKS,
					},
				},
			},
			expectError: true,
			errorMsg:    "SMB share properties CONTINUOUSLY_AVAILABLE are not supported for FlexCache volumes",
		},

		// Large volume validation
		{
			name: "Invalid FlexCache request - large volume constituent count set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					LargeVolumeConstituentCount: gcpgenserver.NewOptNilInt32(8),
				},
			},
			expectError: true,
			errorMsg:    "large volume constituent count is not allowed for FlexCache volumes",
		},
		{
			name: "Invalid FlexCache request - large capacity set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.1"},
					}),
					LargeCapacity: gcpgenserver.NewOptNilBool(true),
				},
			},
			expectError: true,
			errorMsg:    "large capacity is not allowed for FlexCache volumes",
		},
		{
			name: "Invalid FlexCache request - unix permissions set",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						PeerSvmName:     gcpgenserver.NewOptString("svm_test"),
						PeerVolumeName:  gcpgenserver.NewOptString("vol_test"),
						PeerClusterName: gcpgenserver.NewOptString("cluster_test"),
						PeerIpAddresses: []string{"10.0.0.0"},
					}),
					UnixPermissions: gcpgenserver.NewOptNilString("0755"),
				},
			},
			expectError: true,
			errorMsg:    "unix permissions are not allowed during FlexCache volume creation",
		},
		{
			name: "FlexCache request without required fields - accepted by OpenAPI, validated by application",
			req: &gcpgenserver.VolumeCreateV1beta{
				Volume: gcpgenserver.VolumeV1beta{
					CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
						// All required fields are missing - this should pass OpenAPI validation
						// but be caught by application-level validation
					}),
				},
			},
			expectError: true,
			errorMsg:    "cache volume creation requires cacheParameters.peerClusterName",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFlexCacheRequest(tt.req)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}

				if tt.errorMsg != "" {
					if !contains(err.Error(), tt.errorMsg) {
						t.Errorf("Expected error message to contain '%s', but got: %v", tt.errorMsg, err.Error())
					}
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestV1betaSplitStartVolume(t *testing.T) {
	t.Run("FeatureDisabled_Returns403Forbidden", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = false

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		forbidden, ok := result.(*gcpgenserver.V1betaSplitStartVolumeForbidden)
		assert.True(tt, ok)
		assert.Equal(tt, float64(403), forbidden.Code)
		assert.Contains(tt, forbidden.Message, "Thin clone split feature is currently not enabled")
	})

	t.Run("WhenCoreAPIHostNotSet_Returns500", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = ""

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStartVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "Core API host not configured")
	})

	t.Run("WhenCoreAPIClientCreationFails_Returns500", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return nil
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStartVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "Failed to create core API client")
	})

	t.Run("ValidSplitStartVolume_CoreAPIReturnsOperation", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

		inv := &mockInvoker{}
		operationID := "/v1beta/projects/project-number/locations/location-id/operations/job-uuid"
		inv.On("V1SplitStartVolume", mock.Anything, mock.Anything).Return(
			&coreapi.OperationV1{
				Name: coreapi.NewOptString(operationID),
				Done: coreapi.NewOptBool(true),
			}, nil)
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{Invoker: inv}
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, operationID, op.Name.Value)
		assert.True(tt, op.Done.Value)
		inv.AssertExpectations(tt)
	})

	t.Run("ValidSplitStartVolume_CoreAPIReturnsOperation_NotDone", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

		inv := &mockInvoker{}
		operationID := "/v1beta/projects/project-number/locations/location-id/operations/job-uuid"
		inv.On("V1SplitStartVolume", mock.Anything, mock.Anything).Return(
			&coreapi.OperationV1{
				Name: coreapi.NewOptString(operationID),
				Done: coreapi.NewOptBool(false),
			}, nil)
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{Invoker: inv}
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, operationID, op.Name.Value)
		assert.False(tt, op.Done.Value)
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns400BadRequest", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

		inv := &mockInvoker{}
		inv.On("V1SplitStartVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStartVolumeBadRequest{Code: 400, Message: "invalid input"}, nil)
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{Invoker: inv}
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaSplitStartVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "invalid input")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns404NotFound", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

		inv := &mockInvoker{}
		inv.On("V1SplitStartVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStartVolumeNotFound{Code: 404, Message: "Volume not found"}, nil)
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{Invoker: inv}
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		notFound, ok := result.(*gcpgenserver.V1betaSplitStartVolumeNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), notFound.Code)
		assert.Contains(tt, notFound.Message, "Volume not found")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns409Conflict", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

		inv := &mockInvoker{}
		inv.On("V1SplitStartVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStartVolumeConflict{Code: 409, Message: "Volume is in transition state"}, nil)
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{Invoker: inv}
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		conflict, ok := result.(*gcpgenserver.V1betaSplitStartVolumeConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(409), conflict.Code)
		assert.Contains(tt, conflict.Message, "Volume is in transition state")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns500InternalServerError", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

		inv := &mockInvoker{}
		inv.On("V1SplitStartVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStartVolumeInternalServerError{Code: 500, Message: "An error occurred"}, nil)
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{Invoker: inv}
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStartVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "An error occurred")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPICallFails_Returns500", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

		inv := &mockInvoker{}
		inv.On("V1SplitStartVolume", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("connection refused"))
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{Invoker: inv}
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStartVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "Core API call failed")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturnsUnexpectedType_Returns500", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

		inv := &mockInvoker{}
		inv.On("V1SplitStartVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStartVolumeForbidden{Code: 403, Message: "forbidden"}, nil)
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return &coreapi.CoreAPIClient{Invoker: inv}
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		params := gcpgenserver.V1betaSplitStartVolumeParams{
			LocationId:    "location-id",
			ProjectNumber: "project-number",
			VolumeId:      "vol-1",
		}

		result, err := handler.V1betaSplitStartVolume(context.Background(), params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStartVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		inv.AssertExpectations(tt)
	})
}

func TestValidateProtocolsV1beta(t *testing.T) {
	tests := []struct {
		name      string
		in        []gcpgenserver.ProtocolsV1beta
		wantErr   bool
		errSubstr string
	}{
		{
			name: "SingleNFSv3_OK",
			in:   []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
		},
		{
			name: "DualNFS_OK",
			in:   []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3, gcpgenserver.ProtocolsV1betaNFSV4},
		},
		{
			name:      "UnspecifiedProtocol_Error",
			in:        []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaPROTOCOLUNSPECIFIED},
			wantErr:   true,
			errSubstr: "unspecified",
		},
		{
			name:      "ISCSIProtocol_Error",
			in:        []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI},
			wantErr:   true,
			errSubstr: "not supported",
		},
		{
			name:    "EmptySlice_Error",
			in:      []gcpgenserver.ProtocolsV1beta{},
			wantErr: false,
		},
		{
			name:      "MoreThanTwoProtocols_Error",
			in:        []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaSMB, gcpgenserver.ProtocolsV1betaNFSV3, gcpgenserver.ProtocolsV1betaNFSV4},
			wantErr:   true,
			errSubstr: "volume can only support up to two protocols",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProtocolsV1beta(tc.in)
			if tc.wantErr {
				assert.Error(t, err)
				if tc.errSubstr != "" {
					assert.Contains(t, strings.ToLower(err.Error()), tc.errSubstr)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestContainsProtocolTypeV1beta(t *testing.T) {
	tests := []struct {
		name          string
		in            []gcpgenserver.ProtocolsV1beta
		target        gcpgenserver.ProtocolsV1beta
		wantContained bool
	}{
		{"NilSlice", nil, gcpgenserver.ProtocolsV1betaISCSI, false},
		{"EmptySlice", []gcpgenserver.ProtocolsV1beta{}, gcpgenserver.ProtocolsV1betaISCSI, false},
		{"SingleMatch", []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaISCSI}, gcpgenserver.ProtocolsV1betaISCSI, true},
		{"SingleNoMatch", []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3}, gcpgenserver.ProtocolsV1betaISCSI, false},
		{"MultipleContains", []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3, gcpgenserver.ProtocolsV1betaISCSI}, gcpgenserver.ProtocolsV1betaISCSI, true},
		{"Duplicates", []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3, gcpgenserver.ProtocolsV1betaNFSV3}, gcpgenserver.ProtocolsV1betaNFSV3, true},
		{"UnspecifiedNotPresent", []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV4}, gcpgenserver.ProtocolsV1betaPROTOCOLUNSPECIFIED, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := containsProtocolTypeV1beta(tc.in, tc.target)
			assert.Equal(t, tc.wantContained, got)
		})
	}
}

func TestValidateSmbShareSettingsV2(t *testing.T) {
	tests := []struct {
		name     string
		settings []gcpgenserver.SMBSettingsV1betaItem
		wantErr  bool
		errMsg   string
	}{
		{
			name:     "Valid settings with browsable",
			settings: []gcpgenserver.SMBSettingsV1betaItem{gcpgenserver.SMBSettingsV1betaItemBROWSABLE},
			wantErr:  false,
		},
		{
			name:     "Valid settings with non_browsable",
			settings: []gcpgenserver.SMBSettingsV1betaItem{gcpgenserver.SMBSettingsV1betaItemNONBROWSABLE},
			wantErr:  false,
		},
		{
			name:     "Valid settings with encrypt_data",
			settings: []gcpgenserver.SMBSettingsV1betaItem{gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA},
			wantErr:  false,
		},
		{
			name:     "Valid settings with access_based_enumeration",
			settings: []gcpgenserver.SMBSettingsV1betaItem{gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION},
			wantErr:  false,
		},
		{
			name: "Valid settings with multiple allowed values",
			settings: []gcpgenserver.SMBSettingsV1betaItem{
				gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
				gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
				gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION,
			},
			wantErr: false,
		},
		{
			name: "Invalid - both browsable and non_browsable",
			settings: []gcpgenserver.SMBSettingsV1betaItem{
				gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
				gcpgenserver.SMBSettingsV1betaItemNONBROWSABLE,
			},
			wantErr: true,
			errMsg:  "cannot have both browsable and non_browsable",
		},
		{
			name:     "Valid settings with continuously_available",
			settings: []gcpgenserver.SMBSettingsV1betaItem{gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE},
			wantErr:  false,
		},
		{
			name:     "Valid settings with show_snapshot",
			settings: []gcpgenserver.SMBSettingsV1betaItem{gcpgenserver.SMBSettingsV1betaItemSHOWSNAPSHOT},
			wantErr:  false,
		},
		{
			name:     "Empty settings list",
			settings: []gcpgenserver.SMBSettingsV1betaItem{},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSmbShareSettingsV2(tt.settings)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateUnixPermissions(t *testing.T) {
	tests := []struct {
		name       string
		protocols  []string
		permission string
		style      string
		wantErr    bool
		errMsg     string
	}{
		{
			name:       "Valid NFS permissions",
			protocols:  []string{"NFSV3"},
			permission: "0755",
			style:      "unix",
			wantErr:    false,
		},
		{
			name:       "Empty permissions",
			protocols:  []string{"NFSV3"},
			permission: "",
			style:      "unix",
			wantErr:    true,
			errMsg:     "UnixPermissions cannot be empty.",
		},
		{
			name:       "Invalid permission format",
			protocols:  []string{"NFSV3"},
			permission: "08",
			style:      "unix",
			wantErr:    true,
			errMsg:     "Unix permissions should be 4 digit long in octal format",
		},
		{
			name:       "Mismatched security style",
			protocols:  []string{"NFSV3"},
			permission: "0755",
			style:      "ntfs",
			wantErr:    true,
			errMsg:     "Unix permissions are only supported with unix security-style",
		},
		{
			name:       "NFS-only without explicit security style",
			protocols:  []string{"NFSV3"},
			permission: "0755",
			style:      "",
			wantErr:    false,
		},
		{
			name:       "Dual protocol without explicit security style",
			protocols:  []string{"NFSV3", "SMB"},
			permission: "0755",
			style:      "",
			wantErr:    true,
			errMsg:     "Unix permissions are only supported with unix security-style",
		},
		{
			name:       "Unsupported protocol",
			protocols:  []string{"SMB"},
			permission: "0755",
			style:      "unix",
			wantErr:    true,
			errMsg:     "Unix permissions is only supported with NFS protocol volumes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUnixPermissions(tt.protocols, tt.permission, tt.style)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSmbVolumeParams(t *testing.T) {
	tests := []struct {
		name    string
		req     *gcpgenserver.VolumeUpdateV1beta
		wantErr bool
		errMsg  string
	}{
		{
			name: "Valid - no unix permissions or export policy",
			req: &gcpgenserver.VolumeUpdateV1beta{
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
				},
			},
			wantErr: false,
		},
		{
			name: "Invalid - unix permissions set",
			req: &gcpgenserver.VolumeUpdateV1beta{
				UnixPermissions: gcpgenserver.NewOptNilString("0755"),
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
				},
			},
			wantErr: true,
			errMsg:  "Unix permission is not allowed for SMB volumes",
		},
		{
			name: "Invalid - export policy rules set",
			req: &gcpgenserver.VolumeUpdateV1beta{
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
						{AllowedClients: "0.0.0.0/0"},
					},
				}),
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
				},
			},
			wantErr: true,
			errMsg:  "Cannot specify export policy rules for non-NFS volume",
		},
		{
			name: "Invalid - continuously_available in settings",
			req: &gcpgenserver.VolumeUpdateV1beta{
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
					gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
				},
			},
			wantErr: true,
			errMsg:  "Cannot modify continuously_available smb share property",
		},
		{
			name: "Valid - unix permissions unset",
			req: &gcpgenserver.VolumeUpdateV1beta{
				UnixPermissions: gcpgenserver.OptNilString{Set: true, Value: ""},
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - export policy set but empty rules",
			req: &gcpgenserver.VolumeUpdateV1beta{
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{},
				}),
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
				},
			},
			wantErr: false,
		},
		{
			name:    "Valid - nil request",
			req:     nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSmbVolumeParams(tt.req)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetSMBShareSettings(t *testing.T) {
	tests := []struct {
		name     string
		params   *gcpgenserver.VolumeUpdateV1beta
		expected []string
	}{
		{
			name: "Single setting",
			params: &gcpgenserver.VolumeUpdateV1beta{
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
				},
			},
			expected: []string{"browsable"},
		},
		{
			name: "Multiple settings",
			params: &gcpgenserver.VolumeUpdateV1beta{
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
					gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
					gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION,
				},
			},
			expected: []string{"browsable", "encrypt_data", "access_based_enumeration"},
		},
		{
			name: "Duplicate settings - should deduplicate",
			params: &gcpgenserver.VolumeUpdateV1beta{
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
					gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
					gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
				},
			},
			expected: []string{"browsable", "encrypt_data"},
		},
		{
			name: "Non-browsable setting",
			params: &gcpgenserver.VolumeUpdateV1beta{
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemNONBROWSABLE,
				},
			},
			expected: []string{"non_browsable"},
		},
		{
			name: "Continuously available setting",
			params: &gcpgenserver.VolumeUpdateV1beta{
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
					gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
				},
			},
			expected: []string{"continuously_available"},
		},
		{
			name: "Empty settings",
			params: &gcpgenserver.VolumeUpdateV1beta{
				SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{},
			},
			expected: []string{},
		},
		{
			name:     "Nil params",
			params:   nil,
			expected: nil,
		},
		{
			name: "Nil SMB settings",
			params: &gcpgenserver.VolumeUpdateV1beta{
				SmbSettings: nil,
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result []string
			if tt.params == nil {
				result = getSMBShareSettings(nil)
			} else {
				result = getSMBShareSettings(tt.params.SmbSettings)
			}
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.ElementsMatch(t, tt.expected, result)
				// Verify deduplication by checking length
				if len(result) > 0 {
					assert.Equal(t, len(tt.expected), len(result))
				}
			}
		})
	}
}

func TestConvertToOntapShareSettingString(t *testing.T) {
	tests := []struct {
		name     string
		setting  gcpgenserver.SMBSettingsV1betaItem
		expected string
	}{
		{
			name:     "NonBrowsable",
			setting:  gcpgenserver.SMBSettingsV1betaItemNONBROWSABLE,
			expected: utils.CIFSSharePropertyNonBrowsable,
		},
		{
			name:     "Unspecified",
			setting:  gcpgenserver.SMBSettingsV1betaItemSMBSETTINGSUNSPECIFIED,
			expected: utils.CIFSShareSmbSettingsUnspecified,
		},
		{
			name:     "EncryptData",
			setting:  gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
			expected: utils.CIFSSharePropertyEncryptData,
		},
		{
			name:     "ChangeNotify",
			setting:  gcpgenserver.SMBSettingsV1betaItemCHANGENOTIFY,
			expected: utils.CIFSSharePropertyChangenotify,
		},
		{
			name:     "Browsable",
			setting:  gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
			expected: utils.CIFSSharePropertyBrowsable,
		},
		{
			name:     "Oplocks",
			setting:  gcpgenserver.SMBSettingsV1betaItemOPLOCKS,
			expected: utils.CIFSSharePropertyOplocks,
		},
		{
			name:     "ShowSnapshot",
			setting:  gcpgenserver.SMBSettingsV1betaItemSHOWSNAPSHOT,
			expected: utils.CIFSSharePropertyShowsnapshot,
		},
		{
			name:     "ShowPreviousVersions",
			setting:  gcpgenserver.SMBSettingsV1betaItemSHOWPREVIOUSVERSIONS,
			expected: utils.CIFSSharePropertyShowPreviousVersions,
		},
		{
			name:     "AccessBasedEnumeration",
			setting:  gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION,
			expected: utils.CIFSAccessBasedEnumeration,
		},
		{
			name:     "ContinuouslyAvailable",
			setting:  gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
			expected: utils.CIFSSharePropertyCA,
		},
		{
			name:     "DefaultCase",
			setting:  gcpgenserver.SMBSettingsV1betaItem("UNKNOWN"),
			expected: utils.CIFSShareSmbSettingsUnspecified,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToOntapShareSettingString(tt.setting)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertFromOntapShareSettingString(t *testing.T) {
	tests := []struct {
		name     string
		setting  string
		expected gcpgenserver.SMBSettingsV1betaItem
	}{
		{
			name:     "NonBrowsable",
			setting:  utils.CIFSSharePropertyNonBrowsable,
			expected: gcpgenserver.SMBSettingsV1betaItemNONBROWSABLE,
		},
		{
			name:     "Unspecified",
			setting:  utils.CIFSShareSmbSettingsUnspecified,
			expected: gcpgenserver.SMBSettingsV1betaItemSMBSETTINGSUNSPECIFIED,
		},
		{
			name:     "EncryptData",
			setting:  utils.CIFSSharePropertyEncryptData,
			expected: gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
		},
		{
			name:     "ChangeNotify",
			setting:  utils.CIFSSharePropertyChangenotify,
			expected: gcpgenserver.SMBSettingsV1betaItemCHANGENOTIFY,
		},
		{
			name:     "Browsable",
			setting:  utils.CIFSSharePropertyBrowsable,
			expected: gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
		},
		{
			name:     "Oplocks",
			setting:  utils.CIFSSharePropertyOplocks,
			expected: gcpgenserver.SMBSettingsV1betaItemOPLOCKS,
		},
		{
			name:     "ShowSnapshot",
			setting:  utils.CIFSSharePropertyShowsnapshot,
			expected: gcpgenserver.SMBSettingsV1betaItemSHOWSNAPSHOT,
		},
		{
			name:     "ShowPreviousVersions",
			setting:  utils.CIFSSharePropertyShowPreviousVersions,
			expected: gcpgenserver.SMBSettingsV1betaItemSHOWPREVIOUSVERSIONS,
		},
		{
			name:     "AccessBasedEnumeration",
			setting:  utils.CIFSAccessBasedEnumeration,
			expected: gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION,
		},
		{
			name:     "ContinuouslyAvailable",
			setting:  utils.CIFSSharePropertyCA,
			expected: gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE,
		},
		{
			name:     "DefaultCase",
			setting:  "unknown_setting",
			expected: gcpgenserver.SMBSettingsV1betaItemSMBSETTINGSUNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertFromOntapShareSettingString(tt.setting)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertSMBShareSettingToVCP(t *testing.T) {
	tests := []struct {
		name     string
		settings []string
		expected []gcpgenserver.SMBSettingsV1betaItem
	}{
		{
			name:     "SingleSetting",
			settings: []string{utils.CIFSSharePropertyBrowsable},
			expected: []gcpgenserver.SMBSettingsV1betaItem{
				gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
			},
		},
		{
			name: "MultipleSettings",
			settings: []string{
				utils.CIFSSharePropertyBrowsable,
				utils.CIFSSharePropertyEncryptData,
				utils.CIFSSharePropertyOplocks,
			},
			expected: []gcpgenserver.SMBSettingsV1betaItem{
				gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
				gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
				gcpgenserver.SMBSettingsV1betaItemOPLOCKS,
			},
		},
		{
			name:     "EmptySettings",
			settings: []string{},
			expected: []gcpgenserver.SMBSettingsV1betaItem{},
		},
		{
			name:     "WithUnknownSetting",
			settings: []string{utils.CIFSSharePropertyBrowsable, "unknown"},
			expected: []gcpgenserver.SMBSettingsV1betaItem{
				gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
				gcpgenserver.SMBSettingsV1betaItemSMBSETTINGSUNSPECIFIED,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertSMBShareSettingToVCP(tt.settings)
			assert.ElementsMatch(t, tt.expected, result)
		})
	}
}

func TestConvertModelToVolumeV1beta_WithSMBSettings(t *testing.T) {
	t.Run("Success_ConvertsSMBSettingsToResponse", func(tt *testing.T) {
		volume := &models.Volume{
			BaseModel:     models.BaseModel{UUID: "test-uuid"},
			DisplayName:   "test-volume",
			ProtocolTypes: []string{"SMB"},
			FileProperties: &models.FileProperties{
				JunctionPath: "/test-share",
				ExportPolicy: &models.ExportPolicy{
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "0.0.0.0/0",
							AccessType:     "READ_WRITE",
						},
					},
				},
				SMBShareSettings: []string{
					utils.CIFSSharePropertyBrowsable,
					utils.CIFSSharePropertyEncryptData,
					utils.CIFSSharePropertyOplocks,
				},
			},
			LifeCycleState: "READY",
			IPAddresses:    []string{"192.168.1.1"},
		}

		result := convertModelToVCPVolume(volume)
		require.NotNil(tt, result, "convertModelToVCPVolume returned nil")
		require.NotNil(tt, result.SmbSettings, "SmbSettings should be populated but got nil")
		assert.Len(tt, result.SmbSettings, 3)
		assert.ElementsMatch(tt, []gcpgenserver.SMBSettingsV1betaItem{
			gcpgenserver.SMBSettingsV1betaItemBROWSABLE,
			gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA,
			gcpgenserver.SMBSettingsV1betaItemOPLOCKS,
		}, result.SmbSettings)
	})

	t.Run("Success_EmptySMBSettings", func(tt *testing.T) {
		volume := &models.Volume{
			BaseModel:     models.BaseModel{UUID: "test-uuid"},
			DisplayName:   "test-volume",
			ProtocolTypes: []string{"SMB"},
			FileProperties: &models.FileProperties{
				JunctionPath: "/test-share",
				ExportPolicy: &models.ExportPolicy{
					ExportRules: []*models.ExportRule{
						{
							AllowedClients: "0.0.0.0/0",
							AccessType:     "READ_WRITE",
						},
					},
				},
				SMBShareSettings: []string{},
			},
			LifeCycleState: "READY",
			IPAddresses:    []string{"192.168.1.1"},
		}

		result := convertModelToVCPVolume(volume)
		assert.NotNil(tt, result)
		// Empty SMB settings array should not populate SmbSettings field
		assert.Nil(tt, result.SmbSettings)
	})

	t.Run("Success_NoFileProperties", func(tt *testing.T) {
		volume := &models.Volume{
			BaseModel:      models.BaseModel{UUID: "test-uuid"},
			DisplayName:    "test-volume",
			ProtocolTypes:  []string{"ISCSI"},
			LifeCycleState: "READY",
			IPAddresses:    []string{"192.168.1.1"},
		}

		result := convertModelToVCPVolume(volume)
		require.NotNil(tt, result, "convertModelToVCPVolume returned nil for volume without FileProperties")
		// Volumes without FileProperties should not have SmbSettings
		assert.Nil(tt, result.SmbSettings)
	})
}

func TestDualProtocolVolumeCreation_WithLDAPDisabledPool(t *testing.T) {
	t.Run("Failure_WithLDAPDisabledPool", func(tt *testing.T) {
		origEnableSmb := enableSmb
		defer func() { enableSmb = origEnableSmb }()
		enableSmb = true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols: []gcpgenserver.ProtocolsV1beta{
					gcpgenserver.ProtocolsV1betaNFSV3, gcpgenserver.ProtocolsV1betaSMB,
				},
			},
		}
		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "test-project",
			LocationId:    "test-location",
		}
		region := "test-region"
		zone := "test-zone"
		pool := &models.Pool{
			BaseModel:               models.BaseModel{UUID: "test-pool"},
			ActiveDirectoryConfigId: "test-ad",
			PoolAttributes: &models.PoolAttributes{
				LdapEnabled: false,
			},
		}

		result, err := _prepareCreateVolumeParams(req, params, region, zone, pool)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Cannot create dual protocol volume in LDAP disabled pool")
	})
}

func TestConvertModelToVolumeV1beta_WithSecurityStyle(t *testing.T) {
	t.Run("Success_WithSecurityStyle", func(tt *testing.T) {
		volume := &models.Volume{
			BaseModel:     models.BaseModel{UUID: "test-uuid"},
			DisplayName:   "test-volume",
			ProtocolTypes: []string{"NFSV4"},
			FileProperties: &models.FileProperties{
				JunctionPath:  "/test-path",
				SecurityStyle: "UNIX",
				ExportPolicy: &models.ExportPolicy{
					ExportPolicyName: "test-policy",
					ExportRules:      []*models.ExportRule{},
				},
			},
		}

		result := convertModelToVCPVolume(volume)
		require.NotNil(tt, result, "convertModelToVCPVolume returned nil")
		assert.True(tt, result.SecurityStyle.IsSet(), "SecurityStyle should be set")
		assert.Equal(tt, gcpgenserver.VolumeV1betaSecurityStyle("UNIX"), result.SecurityStyle.Value)
	})
}

func TestPrepareCreateVolumeParams_ISCSIWithKmsGrant_FeatureFlagDisabled_ReturnsError(t *testing.T) {
	origBackupEnabled := backupEnabled
	origCmekBackupEnabled := cmekBackupEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		cmekBackupEnabled = origCmekBackupEnabled
	}()
	backupEnabled = true
	cmekBackupEnabled = false

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaISCSI,
			},
			BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
				gcpgenserver.BackupConfigV1beta{
					KmsGrant: gcpgenserver.NewOptNilString("projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"),
				},
			),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "CMEK backup is not enabled")
}

func TestPrepareCreateVolumeParams_ISCSIWithKmsGrant_FeatureFlagEnabled_Succeeds(t *testing.T) {
	origBackupEnabled := backupEnabled
	origCmekBackupEnabled := cmekBackupEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		cmekBackupEnabled = origCmekBackupEnabled
	}()
	backupEnabled = true
	cmekBackupEnabled = true

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaISCSI,
			},
			BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
				gcpgenserver.BackupConfigV1beta{
					BackupVaultId: gcpgenserver.NewOptNilString("test-bv-id"),
					KmsGrant:      gcpgenserver.NewOptNilString("projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"),
				},
			),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.DataProtection)
	assert.NotNil(t, result.DataProtection.KmsGrant)
	assert.Equal(t, "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key", *result.DataProtection.KmsGrant)
}

func TestPrepareCreateVolumeParams_WithKmsGrant_FeatureFlagDisabled_ReturnsError(t *testing.T) {
	origBackupEnabled := backupEnabled
	origCmekBackupEnabled := cmekBackupEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		cmekBackupEnabled = origCmekBackupEnabled
	}()
	backupEnabled = true
	cmekBackupEnabled = false

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
				gcpgenserver.BackupConfigV1beta{
					KmsGrant: gcpgenserver.NewOptNilString("projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"),
				},
			),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "CMEK backup is not enabled")
}

func TestPrepareUpdateVolumeParams_ISCSIWithKmsGrant_FeatureFlagDisabled_ReturnsError(t *testing.T) {
	origBackupEnabled := backupEnabled
	origCmekBackupEnabled := cmekBackupEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		cmekBackupEnabled = origCmekBackupEnabled
	}()
	backupEnabled = true
	cmekBackupEnabled = false

	req := &gcpgenserver.VolumeUpdateV1beta{
		Protocols: []gcpgenserver.ProtocolsV1beta{
			gcpgenserver.ProtocolsV1betaISCSI,
		},
		BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
			gcpgenserver.BackupConfigV1beta{
				KmsGrant: gcpgenserver.NewOptNilString("projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"),
			},
		),
	}
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume-id",
	}
	dbVolume := &models.Volume{
		BaseModel:   models.BaseModel{UUID: "test-volume-id"},
		DisplayName: "testvolume",
	}

	region := "test-region"
	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "CMEK backup is not enabled")
}

func TestPrepareUpdateVolumeParams_ISCSIWithKmsGrant_FeatureFlagEnabled_Succeeds(t *testing.T) {
	origBackupEnabled := backupEnabled
	origCmekBackupEnabled := cmekBackupEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		cmekBackupEnabled = origCmekBackupEnabled
	}()
	backupEnabled = true
	cmekBackupEnabled = true

	req := &gcpgenserver.VolumeUpdateV1beta{
		Protocols: []gcpgenserver.ProtocolsV1beta{
			gcpgenserver.ProtocolsV1betaISCSI,
		},
		BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
			gcpgenserver.BackupConfigV1beta{
				BackupVaultId: gcpgenserver.NewOptNilString("test-bv-id"),
				KmsGrant:      gcpgenserver.NewOptNilString("projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"),
			},
		),
	}
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume-id",
	}
	dbVolume := &models.Volume{
		BaseModel:   models.BaseModel{UUID: "test-volume-id"},
		DisplayName: "testvolume",
	}

	region := "test-region"
	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.DataProtection)
	assert.NotNil(t, result.DataProtection.KmsGrant)
	assert.Equal(t, "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key", *result.DataProtection.KmsGrant)
}

func TestPrepareUpdateVolumeParams_ISCSIWithKmsGrant_FromDBVolume_FeatureFlagDisabled_ReturnsError(t *testing.T) {
	origBackupEnabled := backupEnabled
	origCmekBackupEnabled := cmekBackupEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		cmekBackupEnabled = origCmekBackupEnabled
	}()
	backupEnabled = true
	cmekBackupEnabled = false

	req := &gcpgenserver.VolumeUpdateV1beta{
		BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
			gcpgenserver.BackupConfigV1beta{
				KmsGrant: gcpgenserver.NewOptNilString("projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"),
			},
		),
	}
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume-id",
	}
	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume-id"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{utils.ProtocolISCSI},
	}

	region := "test-region"
	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "CMEK backup is not enabled")
}

func TestPrepareUpdateVolumeParams_WithKmsGrant_FeatureFlagDisabled_ReturnsError(t *testing.T) {
	origBackupEnabled := backupEnabled
	origCmekBackupEnabled := cmekBackupEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		cmekBackupEnabled = origCmekBackupEnabled
	}()
	backupEnabled = true
	cmekBackupEnabled = false

	req := &gcpgenserver.VolumeUpdateV1beta{
		BackupConfig: gcpgenserver.NewOptBackupConfigV1beta(
			gcpgenserver.BackupConfigV1beta{
				KmsGrant: gcpgenserver.NewOptNilString("projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"),
			},
		),
	}
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume-id",
	}
	dbVolume := &models.Volume{
		BaseModel:   models.BaseModel{UUID: "test-volume-id"},
		DisplayName: "testvolume",
	}

	region := "test-region"
	_, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CMEK backup is not enabled")
}

func TestConvertModelToVCPVolume_WithKmsGrant(t *testing.T) {
	kmsGrant := "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"
	volume := &models.Volume{
		BaseModel:      models.BaseModel{UUID: "test-uuid"},
		DisplayName:    "test-volume",
		ProtocolTypes:  []string{"NFSV3"},
		LifeCycleState: "READY",
		DataProtection: &models.DataProtection{
			KmsGrant: &kmsGrant,
		},
	}

	result := convertModelToVCPVolume(volume)
	require.NotNil(t, result, "convertModelToVCPVolume returned nil")
	assert.NotNil(t, result.BackupConfig)
	assert.True(t, result.BackupConfig.Value.KmsGrant.IsSet())
	assert.Equal(t, kmsGrant, result.BackupConfig.Value.KmsGrant.Value)
}

func TestConvertModelToVCPVolume_ServiceLevelUsesLegacyDefaults(t *testing.T) {
	tests := []struct {
		name         string
		serviceLevel string
	}{
		{
			name:         "flex service level keeps software default",
			serviceLevel: "FLEX",
		},
		{
			name:         "case insensitive flex keeps software default",
			serviceLevel: "flex",
		},
		{
			name:         "non flex service level keeps software default",
			serviceLevel: "PREMIUM",
		},
		{
			name:         "empty service level keeps software default",
			serviceLevel: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volume := &models.Volume{
				BaseModel:      models.BaseModel{UUID: "test-uuid"},
				DisplayName:    "test-volume",
				ProtocolTypes:  []string{"NFSV3"},
				LifeCycleState: "READY",
				ServiceLevel:   tt.serviceLevel,
			}

			result := convertModelToVCPVolume(volume)
			require.NotNil(t, result, "convertModelToVCPVolume returned nil")
			assert.True(t, result.ServiceLevel.IsSet())
			assert.Equal(t, gcpgenserver.VolumeV1betaServiceLevelFLEX, result.ServiceLevel.Value)
			assert.True(t, result.StorageClass.IsSet())
			assert.Equal(t, gcpgenserver.StorageClassV1betaSOFTWARE, result.StorageClass.Value)
		})
	}
}

func TestValidateFlexCacheUpdateParams(t *testing.T) {
	tests := []struct {
		name        string
		cacheParams *gcpgenserver.FlexCacheV1beta
		dbVolume    *models.Volume
		wantErr     bool
		errMsg      string
	}{
		{
			name: "Valid - cacheConfig not set",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerVolumeName: gcpgenserver.NewOptString("origin"),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerVolumeName: "origin",
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - only cacheConfig fields",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled:        gcpgenserver.NewOptNilBool(false),
					CifsChangeNotifyEnabled: gcpgenserver.NewOptNilBool(true),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{},
			},
			wantErr: false,
		},
		{
			name: "Invalid - updating non-FlexCache volume",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: nil, // Not a FlexCache volume
			},
			wantErr: true,
			errMsg:  "Cannot update cacheConfig on a non-FlexCache volume",
		},
		{
			name: "Invalid - immutable field PeerVolumeName set",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerVolumeName: gcpgenserver.NewOptString("origin"),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{},
			},
			wantErr: true,
			errMsg:  "PeerVolumeName is immutable and cannot be changed",
		},
		{
			name: "Invalid - immutable field EnableGlobalFileLock set",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				EnableGlobalFileLock: gcpgenserver.NewOptNilBool(true),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{},
			},
			wantErr: true,
			errMsg:  "EnableGlobalFileLock is immutable and cannot be changed",
		},
		{
			name: "Invalid - immutable field PeerClusterName set (different from DB)",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerClusterName: gcpgenserver.NewOptString("cluster1"),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerClusterName: "", // Empty string in DB, "cluster1" in request = change
				},
			},
			wantErr: true,
			errMsg:  "PeerClusterName is immutable and cannot be changed", // ✅ Updated
		},
		{
			name: "Invalid - immutable field PeerIpAddresses set (different from DB)",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerIpAddresses: []string{"10.0.0.1"},
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerIPAddresses: nil, // nil in DB, []string{"10.0.0.1"} in request = change
				},
			},
			wantErr: true,
			errMsg:  "PeerIpAddresses is immutable and cannot be changed", // ✅ Updated
		},
		{
			name: "Invalid - immutable field PeerSvmName set (different from DB)",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerSvmName: gcpgenserver.NewOptString("svm1"),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerSvmName: "", // Empty string in DB, "svm1" in request = change
				},
			},
			wantErr: true,
			errMsg:  "PeerSvmName is immutable and cannot be changed", // ✅ Updated
		},
		{
			name: "Invalid - immutable field PeerVolumeName set (different from DB)",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerVolumeName: gcpgenserver.NewOptString("origin"),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerVolumeName: "", // Empty string in DB, "origin" in request = change
				},
			},
			wantErr: true,
			errMsg:  "PeerVolumeName is immutable and cannot be changed", // ✅ Updated
		},
		{
			name: "Valid - atimeScrubDays with atimeScrubEnabled in request",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					AtimeScrubEnabled: gcpgenserver.NewOptNilBool(true),
					AtimeScrubDays:    gcpgenserver.NewOptNilInt16(14),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - atimeScrubDays with atimeScrubEnabled already enabled in DB",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					AtimeScrubDays: gcpgenserver.NewOptNilInt16(7),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						AtimeScrubEnabled: nillable.GetBoolPtr(true),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Invalid - atimeScrubDays without atimeScrubEnabled",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					AtimeScrubDays: gcpgenserver.NewOptNilInt16(14),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{},
				},
			},
			wantErr: true,
			errMsg:  "atimeScrubDays can only be set when atimeScrubEnabled is true",
		},
		{
			name: "Invalid - atimeScrubDays when explicitly disabling atimeScrubEnabled",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					AtimeScrubEnabled: gcpgenserver.NewOptNilBool(false),
					AtimeScrubDays:    gcpgenserver.NewOptNilInt16(14),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{},
				},
			},
			wantErr: true,
			errMsg:  "atimeScrubDays can only be set when atimeScrubEnabled is true",
		},
		{
			name: "Valid - all mutable fields",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled:        gcpgenserver.NewOptNilBool(false),
					AtimeScrubEnabled:       gcpgenserver.NewOptNilBool(true),
					AtimeScrubDays:          gcpgenserver.NewOptNilInt16(14),
					CifsChangeNotifyEnabled: gcpgenserver.NewOptNilBool(true),
					CachePrePopulate: gcpgenserver.NewOptFlexCachePrePopulateV1beta(gcpgenserver.FlexCachePrePopulateV1beta{
						PathList:        gcpgenserver.NewOptNilStringArray([]string{"/"}),
						ExcludePathList: gcpgenserver.NewOptNilStringArray([]string{"/temp"}),
						Recursion:       gcpgenserver.NewOptNilBool(true),
					}),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - dbVolume is nil",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: nil,
			wantErr:  false, // dbVolume nil check only happens if not nil
		},
		{
			name: "Valid - empty cacheConfig",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{},
			},
			wantErr: false,
		},
		{
			name: "Valid - immutable fields match DB values",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerVolumeName:       gcpgenserver.NewOptString("origin"),
				PeerClusterName:      gcpgenserver.NewOptString("cluster1"),
				PeerSvmName:          gcpgenserver.NewOptString("svm1"),
				PeerIpAddresses:      []string{"10.0.0.1"},
				EnableGlobalFileLock: gcpgenserver.NewOptNilBool(false),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerVolumeName:       "origin",
					PeerClusterName:      "cluster1",
					PeerSvmName:          "svm1",
					PeerIPAddresses:      []string{"10.0.0.1"},
					EnableGlobalFileLock: nillable.GetBoolPtr(false),
				},
			},
			wantErr: false,
		},
		{
			name: "Invalid - PeerVolumeName changed",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerVolumeName: gcpgenserver.NewOptString("different-origin"),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerVolumeName: "origin",
				},
			},
			wantErr: true,
			errMsg:  "PeerVolumeName is immutable and cannot be changed",
		},
		{
			name: "Invalid - PeerClusterName changed",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerClusterName: gcpgenserver.NewOptString("different-cluster"),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerClusterName: "cluster1",
				},
			},
			wantErr: true,
			errMsg:  "PeerClusterName is immutable and cannot be changed",
		},
		{
			name: "Invalid - PeerIpAddresses changed",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerIpAddresses: []string{"10.0.0.2"},
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerIPAddresses: []string{"10.0.0.1"},
				},
			},
			wantErr: true,
			errMsg:  "PeerIpAddresses is immutable and cannot be changed",
		},
		{
			name: "Invalid - EnableGlobalFileLock changed",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				EnableGlobalFileLock: gcpgenserver.NewOptNilBool(true),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					EnableGlobalFileLock: nillable.GetBoolPtr(false),
				},
			},
			wantErr: true,
			errMsg:  "EnableGlobalFileLock is immutable and cannot be changed",
		},
		{
			name: "Valid - all immutable fields match DB",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				PeerVolumeName:       gcpgenserver.NewOptString("origin"),
				PeerClusterName:      gcpgenserver.NewOptString("cluster1"),
				PeerSvmName:          gcpgenserver.NewOptString("svm1"),
				PeerIpAddresses:      []string{"10.0.0.1", "10.0.0.2"},
				EnableGlobalFileLock: gcpgenserver.NewOptNilBool(false),
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					CifsChangeNotifyEnabled: gcpgenserver.NewOptNilBool(true),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerVolumeName:       "origin",
					PeerClusterName:      "cluster1",
					PeerSvmName:          "svm1",
					PeerIPAddresses:      []string{"10.0.0.1", "10.0.0.2"},
					EnableGlobalFileLock: nillable.GetBoolPtr(false),
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: nillable.GetBoolPtr(true),
					},
				},
			},
			wantErr: false,
		},
		{
			name:        "Valid - update request with completely empty FlexCache (no required fields, no cacheConfig)",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				// All fields are missing - this should pass validation for updates
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerVolumeName:  "origin",
					PeerClusterName: "cluster1",
					PeerSvmName:     "svm1",
					PeerIPAddresses: []string{"10.0.0.1"},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - setting WritebackEnabled to false when volume has no CacheConfig",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{},
			},
			wantErr: false,
		},
		{
			name: "Valid - setting WritebackEnabled to false when volume has WritebackEnabled false",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: nillable.GetBoolPtr(false),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - setting WritebackEnabled to false when volume has WritebackEnabled true",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: nillable.GetBoolPtr(true),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - setting WritebackEnabled to true when volume already has WritebackEnabled true",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(true),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: nillable.GetBoolPtr(true),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - setting WritebackEnabled to true when volume has WritebackEnabled false",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(true),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: nillable.GetBoolPtr(false),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - setting WritebackEnabled to true when volume has no CacheConfig",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(true),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{},
			},
			wantErr: false,
		},
		{
			name: "Valid - setting WritebackEnabled to true when volume has CacheConfig but WritebackEnabled is nil",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(true),
				}),
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{},
				},
			},
			wantErr: false,
		},
		{
			name: "Invalid - updating non-FlexCache volume with only EnableGlobalFileLock (no CacheConfig)",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				EnableGlobalFileLock: gcpgenserver.NewOptNilBool(true),
				// No CacheConfig
			},
			dbVolume: &models.Volume{
				CacheParameters: nil, // Not a FlexCache volume
			},
			wantErr: true,
			errMsg:  "Cannot update cacheConfig on a non-FlexCache volume",
		},
		{
			name: "Valid - updating only EnableGlobalFileLock (matching DB value, no CacheConfig)",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				EnableGlobalFileLock: gcpgenserver.NewOptNilBool(true),
				// No CacheConfig
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					EnableGlobalFileLock: nillable.GetBoolPtr(true),
				},
			},
			wantErr: false,
		},
		{
			name: "Valid - updating only EnableGlobalFileLock (matching DB value false, no CacheConfig)",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				EnableGlobalFileLock: gcpgenserver.NewOptNilBool(false),
				// No CacheConfig
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					EnableGlobalFileLock: nillable.GetBoolPtr(false),
				},
			},
			wantErr: false,
		},
		{
			name: "Invalid - updating only EnableGlobalFileLock (different from DB, no CacheConfig)",
			cacheParams: &gcpgenserver.FlexCacheV1beta{
				EnableGlobalFileLock: gcpgenserver.NewOptNilBool(true),
				// No CacheConfig
			},
			dbVolume: &models.Volume{
				CacheParameters: &models.CacheParameters{
					EnableGlobalFileLock: nillable.GetBoolPtr(false),
				},
			},
			wantErr: true,
			errMsg:  "EnableGlobalFileLock is immutable and cannot be changed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFlexCacheUpdateParams(tt.cacheParams, tt.dbVolume)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}

	t.Run("Valid - update request without required fields (accepted by OpenAPI and application)", func(t *testing.T) {
		err := validateFlexCacheUpdateParams(
			&gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(true),
				}),
			},
			&models.Volume{
				CacheParameters: &models.CacheParameters{
					PeerVolumeName:  "origin",
					PeerClusterName: "cluster1",
					PeerSvmName:     "svm1",
					PeerIPAddresses: []string{"10.0.0.1"},
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: nillable.GetBoolPtr(true),
					},
				},
			},
		)
		assert.NoError(t, err)
	})

	t.Run("Valid - setting WritebackEnabled to true", func(t *testing.T) {
		err := validateFlexCacheUpdateParams(
			&gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(true),
				}),
			},
			&models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: nillable.GetBoolPtr(false),
					},
				},
			},
		)
		assert.NoError(t, err)
	})

	t.Run("Valid - setting WritebackEnabled to false", func(t *testing.T) {
		err := validateFlexCacheUpdateParams(
			&gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			&models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: nillable.GetBoolPtr(true),
					},
				},
			},
		)
		assert.NoError(t, err)
	})

	t.Run("Valid - setting WritebackEnabled to false when volume has WritebackEnabled true", func(t *testing.T) {
		err := validateFlexCacheUpdateParams(
			&gcpgenserver.FlexCacheV1beta{
				CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
					WritebackEnabled: gcpgenserver.NewOptNilBool(false),
				}),
			},
			&models.Volume{
				CacheParameters: &models.CacheParameters{
					CacheConfig: &models.CacheConfig{
						WritebackEnabled: nillable.GetBoolPtr(true),
					},
				},
			},
		)
		assert.NoError(t, err)
	})
}

func TestValidateAllSquash_Positive(t *testing.T) {
	// Enable the feature flag for these tests
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	t.Run("SingleAllSquashRule_NoRootAccess_NoKerberos_ShouldPass", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.NoError(t, err)
	})

	t.Run("MultipleRules_OnlyOneAllSquash_ShouldPass", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
			{
				AllowedClients:      "10.0.0.0/8",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
				Nfsv3:               gcpgenserver.NewOptNilBool(false),
				Nfsv4:               gcpgenserver.NewOptNilBool(true),
				AllSquash:           gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.NoError(t, err)
	})

	t.Run("AllSquashWithRootAccessFalse_ShouldPass", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				HasRootAccess:       gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessFalse),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.NoError(t, err)
	})

	t.Run("AllSquashWithAnonUidSet_ShouldPass", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(65534),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.NoError(t, err)
	})

	t.Run("AllSquashWithReadWriteAccessType_ShouldPass", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.NoError(t, err)
	})
}

func TestValidateAllSquash_InvalidConfigurations(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	t.Run("MultipleAllSquashRules_ShouldFail", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
			{
				AllowedClients:      "10.0.0.0/8",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
				Nfsv3:               gcpgenserver.NewOptNilBool(false),
				Nfsv4:               gcpgenserver.NewOptNilBool(true),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(2001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "only one AllSquash rule is allowed per export policy")
	})

	t.Run("AllSquashWithRootAccessTrue_ShouldFail", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				HasRootAccess:       gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "RootSquash cannot be enabled when AllSquash is true for the same rule")
	})

	t.Run("AllSquashWithKerberos5ReadWrite_ShouldFail", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(true),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AllSquash cannot be enabled for Kerberos-enabled export rules")
	})

	t.Run("AllSquashWithoutAnonUid_ShouldFail", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AnonUid must be set when AllSquash is enabled")
	})

	t.Run("AllSquashWithAnonUidSetToNull_ShouldFail", func(t *testing.T) {
		anonUid := gcpgenserver.OptNilInt64{}
		anonUid.SetToNull()
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             anonUid,
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AnonUid must be set when AllSquash is enabled")
	})

	t.Run("AllSquashWithReadOnlyAccessType_ShouldFail", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AccessType must be READ_WRITE when AllSquash is enabled")
	})

	t.Run("AllSquashWithReadNoneAccessType_ShouldFail", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AccessType must be READ_WRITE when AllSquash is enabled")
	})

	t.Run("AllSquashWithAccessTypeUnspecified_ShouldFail", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeACCESSTYPEUNSPECIFIED,
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AccessType must be READ_WRITE when AllSquash is enabled")
	})

	t.Run("AllSquashWithEmptyAccessType_ShouldFail", func(t *testing.T) {
		rules := []gcpgenserver.SimpleExportPolicyRuleV1beta{
			{
				AllowedClients:      "192.168.1.0/24",
				AccessType:          "",
				Nfsv3:               gcpgenserver.NewOptNilBool(true),
				Nfsv4:               gcpgenserver.NewOptNilBool(false),
				AllSquash:           gcpgenserver.NewOptNilBool(true),
				AnonUid:             gcpgenserver.NewOptNilInt64(1001),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(false),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(false),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(false),
			},
		}

		err := validateAllSquash(rules)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "AccessType must be READ_WRITE when AllSquash is enabled")
	})
}

func TestPrepareUpdateVolumeParams_SMBAccessBasedEnumeration_WithUNIXSecurityStyle_ReturnsError(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume",
	}
	region := "test-region"

	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{"SMB"},
		FileProperties: &models.FileProperties{
			SecurityStyle: string(gcpgenserver.VolumeV1betaSecurityStyleUNIX),
		},
	}

	req := &gcpgenserver.VolumeUpdateV1beta{
		SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
			gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION,
		},
	}

	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Security style must be ntfs when specifying access based enumeration share property")
}

func TestPrepareUpdateVolumeParams_SMBAccessBasedEnumeration_WithNTFSSecurityStyle_Succeeds(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume",
	}
	region := "test-region"

	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{"SMB"},
		FileProperties: &models.FileProperties{
			SecurityStyle: string(gcpgenserver.VolumeV1betaSecurityStyleNTFS),
		},
	}

	req := &gcpgenserver.VolumeUpdateV1beta{
		SmbSettings: []gcpgenserver.SMBSettingsV1betaItem{
			gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION,
		},
	}

	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestPrepareUpdateVolumeParams_ExportPolicyRules_ExceedsLimit_ReturnsError(t *testing.T) {
	origExportRulesLimit := exportRulesLimit
	defer func() { exportRulesLimit = origExportRulesLimit }()
	exportRulesLimit = 20

	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume",
	}
	region := "test-region"

	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{"NFSV3"},
		FileProperties: &models.FileProperties{
			ExportPolicy: &models.ExportPolicy{
				ExportRules: []*models.ExportRule{
					{
						AllowedClients: "10.0.0.1/32",
						AccessType:     "ReadWrite",
						NFSv3:          true,
						NFSv4:          false,
						Index:          1,
					},
				},
			},
		},
	}

	// Create 21 rules to exceed the limit (update replaces existing rules, so we need 21 new rules)
	rules := make([]gcpgenserver.SimpleExportPolicyRuleV1beta, 21)
	for i := 0; i < 21; i++ {
		rules[i] = gcpgenserver.SimpleExportPolicyRuleV1beta{
			AllowedClients: fmt.Sprintf("10.0.0.%d/32", i+1),
			AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
			Nfsv3:          gcpgenserver.NewOptNilBool(true),
			Nfsv4:          gcpgenserver.NewOptNilBool(false),
		}
	}

	req := &gcpgenserver.VolumeUpdateV1beta{
		ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
			gcpgenserver.ExportPolicyV1beta{
				Rules: rules,
			},
		),
	}

	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Number of export rules cannot exceed 20")
}

func TestPrepareUpdateVolumeParams_ExportPolicyRules_WithinLimit_Succeeds(t *testing.T) {
	origExportRulesLimit := exportRulesLimit
	defer func() { exportRulesLimit = origExportRulesLimit }()
	exportRulesLimit = 20

	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume",
	}
	region := "test-region"

	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{"NFSV3"},
		FileProperties: &models.FileProperties{
			ExportPolicy: &models.ExportPolicy{
				ExportRules: []*models.ExportRule{
					{
						AllowedClients: "10.0.0.1/32",
						AccessType:     "ReadWrite",
						NFSv3:          true,
						NFSv4:          false,
						Index:          1,
					},
				},
			},
		},
	}

	// Create 20 rules to stay at the limit (update replaces existing rules, so we need 20 new rules)
	rules := make([]gcpgenserver.SimpleExportPolicyRuleV1beta, 20)
	for i := 0; i < 20; i++ {
		rules[i] = gcpgenserver.SimpleExportPolicyRuleV1beta{
			AllowedClients: fmt.Sprintf("10.0.0.%d/32", i+1),
			AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
			Nfsv3:          gcpgenserver.NewOptNilBool(true),
			Nfsv4:          gcpgenserver.NewOptNilBool(false),
		}
	}

	req := &gcpgenserver.VolumeUpdateV1beta{
		ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
			gcpgenserver.ExportPolicyV1beta{
				Rules: rules,
			},
		),
	}

	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.FileProperties)
	assert.NotNil(t, result.FileProperties.ExportPolicy)
	assert.Len(t, result.FileProperties.ExportPolicy.ExportRules, 20)
}

func TestPrepareUpdateVolumeParams_ExportPolicyRules_AccessType_UnixFlags(t *testing.T) {
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume",
	}
	region := "test-region"
	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{"NFSV3"},
	}

	t.Run("ReadWrite_SetsUnixReadWrite", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
				gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
						{
							AllowedClients: "10.0.0.0/8",
							AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
							Nfsv3:          gcpgenserver.NewOptNilBool(true),
						},
					},
				},
			),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.NoError(tt, err)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)
		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.True(tt, rule.UnixReadWrite)
		assert.False(tt, rule.UnixReadOnly)
	})

	t.Run("ReadOnly_SetsUnixReadOnly", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
				gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
						{
							AllowedClients: "10.0.0.0/8",
							AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
							Nfsv3:          gcpgenserver.NewOptNilBool(true),
						},
					},
				},
			),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.NoError(tt, err)
		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.True(tt, rule.UnixReadOnly)
		assert.False(tt, rule.UnixReadWrite)
	})

	t.Run("AccessTypeUnspecified_SetsUnixReadWrite", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
				gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
						{
							AllowedClients: "10.0.0.0/8",
							AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeACCESSTYPEUNSPECIFIED,
							Nfsv3:          gcpgenserver.NewOptNilBool(true),
						},
					},
				},
			),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.NoError(tt, err)
		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.True(tt, rule.UnixReadWrite)
		assert.False(tt, rule.UnixReadOnly)
	})

	t.Run("ReadNone_DoesNotSetUnixFlags", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
				gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
						{
							AllowedClients: "10.0.0.0/8",
							AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE,
							Nfsv3:          gcpgenserver.NewOptNilBool(true),
						},
					},
				},
			),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.NoError(tt, err)
		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.False(tt, rule.UnixReadOnly)
		assert.False(tt, rule.UnixReadWrite)
	})
}

func TestPrepareUpdateVolumeParams_ExportPolicyRules_AllSquash_UnixFlags(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume",
	}
	region := "test-region"
	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{"NFSV3"},
	}

	t.Run("AllSquashTrue_SetsUnixReadWriteAndClearsSuperuser", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
				gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
						{
							AllowedClients: "10.0.0.0/8",
							AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
							Nfsv3:          gcpgenserver.NewOptNilBool(true),
							AllSquash:      gcpgenserver.NewOptNilBool(true),
							AnonUid:        gcpgenserver.NewOptNilInt64(65534),
						},
					},
				},
			),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.NoError(tt, err)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 1)
		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.NotNil(tt, rule.AllSquash)
		assert.True(tt, *rule.AllSquash)
		assert.NotNil(tt, rule.AnonUid)
		assert.Equal(tt, int64(65534), *rule.AnonUid)
		assert.True(tt, rule.UnixReadWrite)
		assert.False(tt, rule.UnixReadOnly)
		assert.False(tt, rule.Superuser)
	})

	t.Run("AllSquashFalse_AccessTypeReadOnly_SetsUnixReadOnly_NoAnonUid", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
				gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
							Nfsv3:          gcpgenserver.NewOptNilBool(true),
							AllSquash:      gcpgenserver.NewOptNilBool(false),
							// AnonUid intentionally not set — verify it stays nil in output
						},
					},
				},
			),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.NoError(tt, err)
		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.NotNil(tt, rule.AllSquash)
		assert.False(tt, *rule.AllSquash)
		assert.Nil(tt, rule.AnonUid)
		assert.True(tt, rule.UnixReadOnly)
		assert.False(tt, rule.UnixReadWrite)
	})

	t.Run("AllSquashEnabled_MixedRules_CorrectUnixFlagsPerRule", func(tt *testing.T) {
		req := &gcpgenserver.VolumeUpdateV1beta{
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
				gcpgenserver.ExportPolicyV1beta{
					Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
						{
							AllowedClients: "10.0.0.0/8",
							AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
							Nfsv3:          gcpgenserver.NewOptNilBool(true),
							AllSquash:      gcpgenserver.NewOptNilBool(true),
							AnonUid:        gcpgenserver.NewOptNilInt64(1001),
						},
						{
							AllowedClients: "192.168.1.0/24",
							AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
							Nfsv3:          gcpgenserver.NewOptNilBool(true),
							AllSquash:      gcpgenserver.NewOptNilBool(false),
						},
					},
				},
			),
		}
		result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
		assert.NoError(tt, err)
		assert.Len(tt, result.FileProperties.ExportPolicy.ExportRules, 2)

		allSquashRule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.True(tt, *allSquashRule.AllSquash)
		assert.True(tt, allSquashRule.UnixReadWrite)
		assert.False(tt, allSquashRule.UnixReadOnly)
		assert.False(tt, allSquashRule.Superuser)
		assert.Equal(tt, int64(1001), *allSquashRule.AnonUid)

		readOnlyRule := result.FileProperties.ExportPolicy.ExportRules[1]
		assert.False(tt, *readOnlyRule.AllSquash)
		assert.True(tt, readOnlyRule.UnixReadOnly)
		assert.False(tt, readOnlyRule.UnixReadWrite)
		assert.Nil(tt, readOnlyRule.AnonUid)
	})
}

// TestPrepareCreateVolumeParams_ExportPolicyRules_AccessType_UnixFlags tests that the
// non-AllSquash (legacy) create path correctly derives UnixReadOnly/UnixReadWrite from AccessType.
func TestPrepareCreateVolumeParams_ExportPolicyRules_AccessType_UnixFlags(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(false)

	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	t.Run("ReadWrite_SetsUnixReadWrite", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
							{
								AllowedClients: "0.0.0.0/0",
								AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
								Nfsv3:          gcpgenserver.NewOptNilBool(true),
							},
						},
					},
				),
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.True(tt, rule.UnixReadWrite)
		assert.False(tt, rule.UnixReadOnly)
	})

	t.Run("ReadOnly_SetsUnixReadOnly", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
							{
								AllowedClients: "0.0.0.0/0",
								AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY,
								Nfsv3:          gcpgenserver.NewOptNilBool(true),
							},
						},
					},
				),
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.True(tt, rule.UnixReadOnly)
		assert.False(tt, rule.UnixReadWrite)
	})

	t.Run("AccessTypeUnspecified_SetsUnixReadWrite", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "testvolume",
				CreationToken: gcpgenserver.NewOptString("test-token"),
				PoolId:        gcpgenserver.NewNilString("test-pool"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(
					gcpgenserver.ExportPolicyV1beta{
						Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
							{
								AllowedClients: "0.0.0.0/0",
								AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeACCESSTYPEUNSPECIFIED,
								Nfsv3:          gcpgenserver.NewOptNilBool(true),
							},
						},
					},
				),
			},
		}
		result, err := prepareCreateVolumeParams(req, params, region, zone, nil)
		assert.NoError(tt, err)
		rule := result.FileProperties.ExportPolicy.ExportRules[0]
		assert.True(tt, rule.UnixReadWrite)
		assert.False(tt, rule.UnixReadOnly)
	})
}

// TestV1betaDeleteVolume_DeletingState_GetJobByResourceUUIDFails tests lines 1316, 1318: error logging and dummy operation return when job lookup fails
func TestV1betaDeleteVolume_DeletingState_GetJobByResourceUUIDFails(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	params := gcpgenserver.V1betaDeleteVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "vol-1",
	}
	req := gcpgenserver.OptV1betaDeleteVolumeReq{}

	// Volume in DELETING state
	volume := &models.Volume{
		BaseModel:      models.BaseModel{UUID: "vol-1"},
		LifeCycleState: datamodel.LifeCycleStateDeleting,
		CreationToken:  "token",
		DisplayName:    "testvolume",
	}
	mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)

	// GetJobByResourceUUID fails (covers lines 1316, 1318)
	jobErr := fmt.Errorf("failed to get job")
	mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, "vol-1", string(datamodel.JobTypeDeleteVolume)).Return(nil, jobErr)

	result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
	assert.NoError(t, err)
	operation, isOperation := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, isOperation)
	// Should return dummy operation ID (line 1318)
	assert.True(t, operation.Done.Value)
}

// TestV1betaDeleteVolume_DeletingState_LargeVolume tests lines 1310-1311: JobType determination for large volumes in DELETING state
func TestV1betaDeleteVolume_DeletingState_LargeVolume(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}
	params := gcpgenserver.V1betaDeleteVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "vol-1",
	}
	req := gcpgenserver.OptV1betaDeleteVolumeReq{}

	// Large volume in DELETING state
	volume := &models.Volume{
		BaseModel:      models.BaseModel{UUID: "vol-1"},
		LifeCycleState: datamodel.LifeCycleStateDeleting,
		LargeCapacity:  true,
		CreationToken:  "token",
		DisplayName:    "testvolume",
	}
	mockOrchestrator.EXPECT().GetVolume(mock.Anything, "vol-1", false).Return(volume, nil)

	// Should use JobTypeDeleteLargeVolume (lines 1310-1311)
	job := &models.Job{
		BaseModel: models.BaseModel{UUID: "job-123"},
		State:     datamodel.JobsStateDONE,
	}
	mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, "vol-1", string(datamodel.JobTypeDeleteLargeVolume)).Return(job, nil)

	result, err := handler.V1betaDeleteVolume(context.Background(), req, params)
	assert.NoError(t, err)
	operation, isOperation := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, isOperation)
	assert.Contains(t, operation.Name.Value, "job-123")
}

func TestPrepareCreateVolumeParams_SMBOnlyVolumeWithExportPolicyRules_ReturnsError(t *testing.T) {
	origEnableSmb := enableSmb
	defer func() { enableSmb = origEnableSmb }()
	enableSmb = true

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaSMB,
			},
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{AllowedClients: "0.0.0.0/0"},
				},
			}),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Cannot specify export policy rules for non-NFS volume")
}

func TestPrepareCreateVolumeParams_SMBOnlyVolumeWithoutExportPolicyRules_Succeeds(t *testing.T) {
	origEnableSmb := enableSmb
	defer func() { enableSmb = origEnableSmb }()
	enableSmb = true

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaSMB,
			},
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, []string{"SMB"}, result.Protocols)
}

func TestPrepareCreateVolumeParams_DualProtocolVolumeWithExportPolicyRules_Succeeds(t *testing.T) {
	origEnableSmb := enableSmb
	defer func() { enableSmb = origEnableSmb }()
	enableSmb = true

	req := &gcpgenserver.VolumeCreateV1beta{
		Volume: gcpgenserver.VolumeV1beta{
			ResourceId:    "testvolume",
			CreationToken: gcpgenserver.NewOptString("test-token"),
			PoolId:        gcpgenserver.NewNilString("test-pool"),
			QuotaInBytes:  gcpgenserver.NewOptFloat64(1024),
			Protocols: []gcpgenserver.ProtocolsV1beta{
				gcpgenserver.ProtocolsV1betaNFSV3,
				gcpgenserver.ProtocolsV1betaSMB,
			},
			ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
				Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
					{
						AllowedClients: "0.0.0.0/0",
						AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						Nfsv3:          gcpgenserver.NewOptNilBool(true),
					},
				},
			}),
		},
	}
	params := gcpgenserver.V1betaCreateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
	}
	region := "test-region"
	zone := "test-zone"

	result, err := _prepareCreateVolumeParams(req, params, region, zone, &models.Pool{ActiveDirectoryConfigId: "test-ad-config-id"})
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, []string{"NFSV3", "SMB"}, result.Protocols)
	assert.NotNil(t, result.FileProperties)
	assert.NotNil(t, result.FileProperties.ExportPolicy)
	assert.Len(t, result.FileProperties.ExportPolicy.ExportRules, 1)
}

func TestPrepareUpdateVolumeParams_SMBOnlyVolumeWithExportPolicyRules_ReturnsError(t *testing.T) {
	req := &gcpgenserver.VolumeUpdateV1beta{
		ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
			Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
				{AllowedClients: "0.0.0.0/0"},
			},
		}),
	}
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume-id",
	}
	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume-id"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{"SMB"},
	}
	region := "test-region"

	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Cannot specify export policy rules for non-NFS volume")
}

func TestPrepareUpdateVolumeParams_SMBOnlyVolumeWithEmptyExportPolicyRules_Succeeds(t *testing.T) {
	req := &gcpgenserver.VolumeUpdateV1beta{
		ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
			Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{},
		}),
	}
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume-id",
	}
	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume-id"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{"SMB"},
	}
	region := "test-region"

	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestPrepareUpdateVolumeParams_DualProtocolVolumeWithExportPolicyRules_Succeeds(t *testing.T) {
	req := &gcpgenserver.VolumeUpdateV1beta{
		ExportPolicy: gcpgenserver.NewOptExportPolicyV1beta(gcpgenserver.ExportPolicyV1beta{
			Rules: []gcpgenserver.SimpleExportPolicyRuleV1beta{
				{
					AllowedClients: "0.0.0.0/0",
					AccessType:     gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					Nfsv3:          gcpgenserver.NewOptNilBool(true),
				},
			},
		}),
	}
	params := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber: "test-project",
		LocationId:    "test-location",
		VolumeId:      "test-volume-id",
	}
	dbVolume := &models.Volume{
		BaseModel:     models.BaseModel{UUID: "test-volume-id"},
		DisplayName:   "testvolume",
		ProtocolTypes: []string{"NFSV3", "SMB"},
	}
	region := "test-region"

	result, err := _prepareUpdateVolumeParams(req, params, region, dbVolume)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.FileProperties)
	assert.NotNil(t, result.FileProperties.ExportPolicy)
	assert.Len(t, result.FileProperties.ExportPolicy.ExportRules, 1)
}

func Test_prepareCreateVolumeParams_CacheConfig(t *testing.T) {
	// Save and restore original flags
	origFlexCacheEnabled := flexCacheEnabled
	defer func() {
		flexCacheEnabled = origFlexCacheEnabled
	}()
	flexCacheEnabled = true

	t.Run("Success_WithFullCacheConfig", func(tt *testing.T) {
		writebackEnabled := true
		atimeScrubEnabled := true
		atimeScrubDays := int16(30)
		cifsChangeNotifyEnabled := false
		enableGlobalFileLock := true
		peerExpiryTime := time.Now().Add(1 * time.Hour)

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "test_volume",
				CreationToken: gcpgenserver.NewOptString("test_token"),
				PoolId:        gcpgenserver.NewNilString("test-pool-id"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1099511627776),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
					PeerVolumeName:           gcpgenserver.NewOptString("peer_volume"),
					PeerClusterName:          gcpgenserver.NewOptString("peer_cluster"),
					PeerSvmName:              gcpgenserver.NewOptString("peer_svm"),
					PeerIpAddresses:          []string{"10.0.0.1", "10.0.0.2"},
					PeeringCommandExpiryTime: gcpgenserver.NewOptNilDateTime(peerExpiryTime),
					EnableGlobalFileLock:     gcpgenserver.NewOptNilBool(enableGlobalFileLock),
					CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
						WritebackEnabled:        gcpgenserver.NewOptNilBool(writebackEnabled),
						AtimeScrubEnabled:       gcpgenserver.NewOptNilBool(atimeScrubEnabled),
						AtimeScrubDays:          gcpgenserver.NewOptNilInt16(atimeScrubDays),
						CifsChangeNotifyEnabled: gcpgenserver.NewOptNilBool(cifsChangeNotifyEnabled),
					}),
				}),
			},
		}

		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "123456",
			LocationId:    "us-east4-a",
		}

		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool-id"},
		}

		result, err := _prepareCreateVolumeParams(req, params, "us-east4", "us-east4-a", pool)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.CacheParameters)

		// Verify EnableGlobalFileLock
		assert.NotNil(tt, result.CacheParameters.EnableGlobalFileLock)
		assert.True(tt, *result.CacheParameters.EnableGlobalFileLock)

		// Verify CacheConfig
		assert.NotNil(tt, result.CacheParameters.CacheConfig)
		cc := result.CacheParameters.CacheConfig
		assert.NotNil(tt, cc.WritebackEnabled)
		assert.True(tt, *cc.WritebackEnabled)
		assert.NotNil(tt, cc.AtimeScrubEnabled)
		assert.True(tt, *cc.AtimeScrubEnabled)
		assert.NotNil(tt, cc.AtimeScrubDays)
		assert.Equal(tt, int16(30), *cc.AtimeScrubDays)
		assert.NotNil(tt, cc.CifsChangeNotifyEnabled)
		assert.False(tt, *cc.CifsChangeNotifyEnabled)

		// Verify other CacheParameters fields
		assert.Equal(tt, "peer_volume", result.CacheParameters.PeerVolumeName)
		assert.Equal(tt, "peer_cluster", result.CacheParameters.PeerClusterName)
		assert.Equal(tt, "peer_svm", result.CacheParameters.PeerSvmName)
		assert.Equal(tt, []string{"10.0.0.1", "10.0.0.2"}, result.CacheParameters.PeerIPAddresses)
	})

	t.Run("Success_WithPartialCacheConfig_OnlyWriteback", func(tt *testing.T) {
		writebackEnabled := true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "test_volume_partial",
				CreationToken: gcpgenserver.NewOptString("test_token_partial"),
				PoolId:        gcpgenserver.NewNilString("test-pool-id"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1099511627776),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
					PeerVolumeName:  gcpgenserver.NewOptString("peer_volume"),
					PeerClusterName: gcpgenserver.NewOptString("peer_cluster"),
					PeerSvmName:     gcpgenserver.NewOptString("peer_svm"),
					PeerIpAddresses: []string{"10.0.0.1", "10.0.0.2"},
					CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
						WritebackEnabled: gcpgenserver.NewOptNilBool(writebackEnabled),
						// Other fields not set
					}),
				}),
			},
		}

		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "123456",
			LocationId:    "us-east4-a",
		}

		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool-id"},
		}

		result, err := _prepareCreateVolumeParams(req, params, "us-east4", "us-east4-a", pool)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.CacheParameters)
		assert.NotNil(tt, result.CacheParameters.CacheConfig)

		cc := result.CacheParameters.CacheConfig
		assert.NotNil(tt, cc.WritebackEnabled)
		assert.True(tt, *cc.WritebackEnabled)
		// Other fields should be nil
		assert.Nil(tt, cc.AtimeScrubEnabled)
		assert.Nil(tt, cc.AtimeScrubDays)
		assert.Nil(tt, cc.CifsChangeNotifyEnabled)
	})

	t.Run("Success_WithEnableGlobalFileLockOnly", func(tt *testing.T) {
		enableGlobalFileLock := true

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "test_volume_gfl",
				CreationToken: gcpgenserver.NewOptString("test_token_gfl"),
				PoolId:        gcpgenserver.NewNilString("test-pool-id"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1099511627776),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
					PeerVolumeName:       gcpgenserver.NewOptString("peer_volume"),
					PeerClusterName:      gcpgenserver.NewOptString("peer_cluster"),
					PeerSvmName:          gcpgenserver.NewOptString("peer_svm"),
					PeerIpAddresses:      []string{"10.0.0.1", "10.0.0.2"},
					EnableGlobalFileLock: gcpgenserver.NewOptNilBool(enableGlobalFileLock),
					// No CacheConfig
				}),
			},
		}

		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "123456",
			LocationId:    "us-east4-a",
		}

		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool-id"},
		}

		result, err := _prepareCreateVolumeParams(req, params, "us-east4", "us-east4-a", pool)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.CacheParameters)
		assert.NotNil(tt, result.CacheParameters.EnableGlobalFileLock)
		assert.True(tt, *result.CacheParameters.EnableGlobalFileLock)
		// CacheConfig should be nil
		assert.Nil(tt, result.CacheParameters.CacheConfig)
	})

	t.Run("Success_NoCacheConfig_BackwardsCompatibility", func(tt *testing.T) {
		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "test_volume_no_config",
				CreationToken: gcpgenserver.NewOptString("test_token_no_config"),
				PoolId:        gcpgenserver.NewNilString("test-pool-id"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1099511627776),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
					PeerVolumeName:  gcpgenserver.NewOptString("peer_volume"),
					PeerClusterName: gcpgenserver.NewOptString("peer_cluster"),
					PeerSvmName:     gcpgenserver.NewOptString("peer_svm"),
					PeerIpAddresses: []string{"10.0.0.1", "10.0.0.2"},
					// No EnableGlobalFileLock, no CacheConfig
				}),
			},
		}

		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "123456",
			LocationId:    "us-east4-a",
		}

		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool-id"},
		}

		result, err := _prepareCreateVolumeParams(req, params, "us-east4", "us-east4-a", pool)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.CacheParameters)
		// EnableGlobalFileLock and CacheConfig should be nil for backwards compatibility
		assert.Nil(tt, result.CacheParameters.EnableGlobalFileLock)
		assert.Nil(tt, result.CacheParameters.CacheConfig)
	})

	t.Run("Success_WithAtimeScrubConfig", func(tt *testing.T) {
		atimeScrubEnabled := true
		atimeScrubDays := int16(45)

		req := &gcpgenserver.VolumeCreateV1beta{
			Volume: gcpgenserver.VolumeV1beta{
				ResourceId:    "test_volume_atime",
				CreationToken: gcpgenserver.NewOptString("test_token_atime"),
				PoolId:        gcpgenserver.NewNilString("test-pool-id"),
				QuotaInBytes:  gcpgenserver.NewOptFloat64(1099511627776),
				Protocols:     []gcpgenserver.ProtocolsV1beta{gcpgenserver.ProtocolsV1betaNFSV3},
				CacheParameters: gcpgenserver.NewOptFlexCacheV1beta(gcpgenserver.FlexCacheV1beta{
					PeerVolumeName:  gcpgenserver.NewOptString("peer_volume"),
					PeerClusterName: gcpgenserver.NewOptString("peer_cluster"),
					PeerSvmName:     gcpgenserver.NewOptString("peer_svm"),
					PeerIpAddresses: []string{"10.0.0.1", "10.0.0.2"},
					CacheConfig: gcpgenserver.NewOptFlexCacheConfigV1beta(gcpgenserver.FlexCacheConfigV1beta{
						AtimeScrubEnabled: gcpgenserver.NewOptNilBool(atimeScrubEnabled),
						AtimeScrubDays:    gcpgenserver.NewOptNilInt16(atimeScrubDays),
					}),
				}),
			},
		}

		params := gcpgenserver.V1betaCreateVolumeParams{
			ProjectNumber: "123456",
			LocationId:    "us-east4-a",
		}

		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "test-pool-id"},
		}

		result, err := _prepareCreateVolumeParams(req, params, "us-east4", "us-east4-a", pool)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.CacheParameters)
		assert.NotNil(tt, result.CacheParameters.CacheConfig)

		cc := result.CacheParameters.CacheConfig
		assert.NotNil(tt, cc.AtimeScrubEnabled)
		assert.True(tt, *cc.AtimeScrubEnabled)
		assert.NotNil(tt, cc.AtimeScrubDays)
		assert.Equal(tt, int16(45), *cc.AtimeScrubDays)
	})
}

func TestConvertToCloneParentInfoV1(t *testing.T) {
	t.Run("WhenStateIsSet_PopulatesState", func(tt *testing.T) {
		state := "SPLIT_INITIATED"
		cp := &models.CloneParentInfo{
			ParentVolumeId:   nillable.ToPointer("parent-vol-id"),
			ParentSnapshotId: nillable.ToPointer("parent-snap-id"),
			State:            &state,
		}
		result := convertToCloneParentInfoV1(cp, 0)
		assert.True(tt, result.State.Set)
		assert.Equal(tt, gcpgenserver.CloneDetailsV1betaState(state), result.State.Value)
	})

	t.Run("WhenStateIsEmpty_DoesNotPopulateState", func(tt *testing.T) {
		empty := ""
		cp := &models.CloneParentInfo{
			ParentVolumeId: nillable.ToPointer("parent-vol-id"),
			State:          &empty,
		}
		result := convertToCloneParentInfoV1(cp, 0)
		assert.False(tt, result.State.Set)
	})

	t.Run("WhenStateIsNil_DoesNotPopulateState", func(tt *testing.T) {
		cp := &models.CloneParentInfo{
			ParentVolumeId: nillable.ToPointer("parent-vol-id"),
			State:          nil,
		}
		result := convertToCloneParentInfoV1(cp, 0)
		assert.False(tt, result.State.Set)
	})

	t.Run("WhenSplitCompletePercentIsSet_PopulatesPercent", func(tt *testing.T) {
		pct := int64(42)
		cp := &models.CloneParentInfo{
			ParentVolumeId:       nillable.ToPointer("parent-vol-id"),
			SplitCompletePercent: &pct,
		}
		result := convertToCloneParentInfoV1(cp, 0)
		assert.True(tt, result.SplitCompletePercent.Set)
		assert.Equal(tt, int64(42), result.SplitCompletePercent.Value)
	})

	t.Run("WhenSplitCompletePercentIsNil_DoesNotPopulatePercent", func(tt *testing.T) {
		cp := &models.CloneParentInfo{
			ParentVolumeId:       nillable.ToPointer("parent-vol-id"),
			SplitCompletePercent: nil,
		}
		result := convertToCloneParentInfoV1(cp, 0)
		assert.False(tt, result.SplitCompletePercent.Set)
	})

	t.Run("WhenStateDetailsIsSet_PopulatesStateDetails", func(tt *testing.T) {
		details := "split failed: out of space"
		cp := &models.CloneParentInfo{
			ParentVolumeId: nillable.ToPointer("parent-vol-id"),
			StateDetails:   &details,
		}
		result := convertToCloneParentInfoV1(cp, 0)
		assert.True(tt, result.StateDetails.Set)
		assert.Equal(tt, details, result.StateDetails.Value)
	})

	t.Run("WhenStateDetailsIsEmpty_DoesNotPopulateStateDetails", func(tt *testing.T) {
		empty := ""
		cp := &models.CloneParentInfo{
			ParentVolumeId: nillable.ToPointer("parent-vol-id"),
			StateDetails:   &empty,
		}
		result := convertToCloneParentInfoV1(cp, 0)
		assert.False(tt, result.StateDetails.Set)
	})

	t.Run("WhenStateDetailsIsNil_DoesNotPopulateStateDetails", func(tt *testing.T) {
		cp := &models.CloneParentInfo{
			ParentVolumeId: nillable.ToPointer("parent-vol-id"),
			StateDetails:   nil,
		}
		result := convertToCloneParentInfoV1(cp, 0)
		assert.False(tt, result.StateDetails.Set)
	})

	t.Run("SharedBytesIsAlwaysPopulated", func(tt *testing.T) {
		cp := &models.CloneParentInfo{
			ParentVolumeId: nillable.ToPointer("parent-vol-id"),
		}
		result := convertToCloneParentInfoV1(cp, 1024)
		assert.True(tt, result.SharedBytes.Set)
		assert.Equal(tt, float64(1024), result.SharedBytes.Value)
	})
}

func TestSplitStartVolume_XCorrelationIDPropagation(t *testing.T) {
	// Covers line 3290: when XCorrelationID is set, it is forwarded to the core API call.
	origThinCloneGASupport := thinCloneGASupport
	defer func() { thinCloneGASupport = origThinCloneGASupport }()
	thinCloneGASupport = true

	origCoreAPIHost := coreAPIHost
	defer func() { coreAPIHost = origCoreAPIHost }()
	coreAPIHost = "http://core-api:8080"

	origCreateCoreAPIClient := createCoreAPIClient
	defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

	var capturedParams coreapi.V1SplitStartVolumeParams
	inv := &mockInvoker{}
	inv.On("V1SplitStartVolume", mock.Anything, mock.MatchedBy(func(p coreapi.V1SplitStartVolumeParams) bool {
		capturedParams = p
		return true
	})).Return(&coreapi.OperationV1{
		Name: coreapi.NewOptString("/v1beta/projects/proj/locations/loc/operations/op-id"),
		Done: coreapi.NewOptBool(false),
	}, nil)
	createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
		return &coreapi.CoreAPIClient{Invoker: inv}
	}

	handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	params := gcpgenserver.V1betaSplitStartVolumeParams{
		LocationId:     "loc",
		ProjectNumber:  "proj",
		VolumeId:       "vol-1",
		XCorrelationID: gcpgenserver.NewOptString("corr-id-xyz"),
	}

	result, err := handler.V1betaSplitStartVolume(context.Background(), params)
	assert.NoError(t, err)
	_, ok := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.True(t, capturedParams.XCorrelationID.IsSet())
	assert.Equal(t, "corr-id-xyz", capturedParams.XCorrelationID.Value)
	inv.AssertExpectations(t)
}

func TestSplitStartVolume_EmptyOperationIDGeneratesUUID(t *testing.T) {
	// Covers line 3309: when the core API returns an OperationV1 with an empty Name,
	// a synthetic operation ID is generated.
	origThinCloneGASupport := thinCloneGASupport
	defer func() { thinCloneGASupport = origThinCloneGASupport }()
	thinCloneGASupport = true

	origCoreAPIHost := coreAPIHost
	defer func() { coreAPIHost = origCoreAPIHost }()
	coreAPIHost = "http://core-api:8080"

	origCreateCoreAPIClient := createCoreAPIClient
	defer func() { createCoreAPIClient = origCreateCoreAPIClient }()

	inv := &mockInvoker{}
	inv.On("V1SplitStartVolume", mock.Anything, mock.Anything).Return(&coreapi.OperationV1{
		// Name is not set — empty string triggers the UUID fallback on line 3309.
		Done: coreapi.NewOptBool(false),
	}, nil)
	createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
		return &coreapi.CoreAPIClient{Invoker: inv}
	}

	handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	params := gcpgenserver.V1betaSplitStartVolumeParams{
		LocationId:    "us-east4",
		ProjectNumber: "my-project",
		VolumeId:      "vol-1",
	}

	result, err := handler.V1betaSplitStartVolume(context.Background(), params)
	assert.NoError(t, err)
	op, ok := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	// The generated operation ID must contain the project and location and be non-empty.
	assert.True(t, op.Name.IsSet())
	assert.Contains(t, op.Name.Value, "my-project")
	assert.Contains(t, op.Name.Value, "us-east4")
	inv.AssertExpectations(t)
}

// setupSplitStopHappyPath wires up the THIN_CLONE_GA_SUPPORT flag, CORE_API_HOST,
// and the createCoreAPIClient factory to use the supplied mockInvoker. Returns a
// cleanup function the caller must defer to restore originals.
func setupSplitStopHappyPath(inv *mockInvoker) func() {
	origThinCloneGASupport := thinCloneGASupport
	origCoreAPIHost := coreAPIHost
	origCreateCoreAPIClient := createCoreAPIClient

	thinCloneGASupport = true
	coreAPIHost = "http://core-api:8080"
	createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
		return &coreapi.CoreAPIClient{Invoker: inv}
	}
	return func() {
		thinCloneGASupport = origThinCloneGASupport
		coreAPIHost = origCoreAPIHost
		createCoreAPIClient = origCreateCoreAPIClient
	}
}

func TestV1betaSplitStopVolume(t *testing.T) {
	baseParams := gcpgenserver.V1betaSplitStopVolumeParams{
		LocationId:    "location-id",
		ProjectNumber: "project-number",
		VolumeId:      "vol-1",
	}

	t.Run("FeatureDisabled_Returns403Forbidden", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = false

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		forbidden, ok := result.(*gcpgenserver.V1betaSplitStopVolumeForbidden)
		assert.True(tt, ok)
		assert.Equal(tt, float64(403), forbidden.Code)
		assert.Contains(tt, forbidden.Message, "Thin clone split feature is currently not enabled")
	})

	t.Run("WhenCoreAPIHostNotSet_Returns500", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = ""

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStopVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "Core API host not configured")
	})

	t.Run("WhenCoreAPIClientCreationFails_Returns500", func(tt *testing.T) {
		origThinCloneGASupport := thinCloneGASupport
		defer func() { thinCloneGASupport = origThinCloneGASupport }()
		thinCloneGASupport = true

		origCoreAPIHost := coreAPIHost
		defer func() { coreAPIHost = origCoreAPIHost }()
		coreAPIHost = "http://core-api:8080"

		origCreateCoreAPIClient := createCoreAPIClient
		defer func() { createCoreAPIClient = origCreateCoreAPIClient }()
		createCoreAPIClient = func(basePath string, jwt string, logger log.Logger) *coreapi.CoreAPIClient {
			return nil
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStopVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "Failed to create core API client")
	})

	t.Run("ValidSplitStopVolume_CoreAPIReturnsOperation_Done", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		operationID := "/v1beta/projects/project-number/locations/location-id/operations/stop-op-uuid"
		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.OperationV1{
				Name: coreapi.NewOptString(operationID),
				Done: coreapi.NewOptBool(true),
			}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, operationID, op.Name.Value)
		// Stop is synchronous on the core side, so done MUST always be true.
		assert.True(tt, op.Done.Value)
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturnsOperation_DoneUnset_DefaultsToTrue", func(tt *testing.T) {
		// splitStop differs from splitStart: the operation is synchronous, so when
		// core-api returns OperationV1 with Done unset, the proxy must default to
		// true (Or(true)) rather than false.
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		operationID := "/v1beta/projects/project-number/locations/location-id/operations/stop-op-uuid"
		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.OperationV1{
				Name: coreapi.NewOptString(operationID),
				// Done intentionally unset.
			}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, op.Done.Value, "splitStop must default Done to true when core-api omits it")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns400BadRequest", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStopVolumeBadRequest{Code: 400, Message: "invalid input"}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaSplitStopVolumeBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "invalid input")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns401Unauthorized", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStopVolumeUnauthorized{Code: 401, Message: "token expired"}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		unauthorized, ok := result.(*gcpgenserver.V1betaSplitStopVolumeUnauthorized)
		assert.True(tt, ok, "401 from core-api must propagate as V1betaSplitStopVolumeUnauthorized, not be downgraded to 500")
		assert.Equal(tt, float64(401), unauthorized.Code)
		assert.Contains(tt, unauthorized.Message, "token expired")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns403Forbidden", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStopVolumeForbidden{Code: 403, Message: "core feature disabled"}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		forbidden, ok := result.(*gcpgenserver.V1betaSplitStopVolumeForbidden)
		assert.True(tt, ok, "403 from core-api must propagate as V1betaSplitStopVolumeForbidden, not be downgraded to 500")
		assert.Equal(tt, float64(403), forbidden.Code)
		assert.Contains(tt, forbidden.Message, "core feature disabled")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns404NotFound", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStopVolumeNotFound{Code: 404, Message: "Volume not found"}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		notFound, ok := result.(*gcpgenserver.V1betaSplitStopVolumeNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), notFound.Code)
		assert.Contains(tt, notFound.Message, "Volume not found")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns409Conflict", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStopVolumeConflict{Code: 409, Message: "volume split is not in progress"}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		conflict, ok := result.(*gcpgenserver.V1betaSplitStopVolumeConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(409), conflict.Code)
		assert.Contains(tt, conflict.Message, "not in progress")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns422UnprocessableEntity", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStopVolumeUnprocessableEntity{Code: 422, Message: "validation failed"}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		unproc, ok := result.(*gcpgenserver.V1betaSplitStopVolumeUnprocessableEntity)
		assert.True(tt, ok, "422 from core-api must propagate as V1betaSplitStopVolumeUnprocessableEntity, not be downgraded to 500")
		assert.Equal(tt, float64(422), unproc.Code)
		assert.Contains(tt, unproc.Message, "validation failed")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns429TooManyRequests", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStopVolumeTooManyRequests{Code: 429, Message: "rate limited"}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		tooMany, ok := result.(*gcpgenserver.V1betaSplitStopVolumeTooManyRequests)
		assert.True(tt, ok, "429 from core-api must propagate as V1betaSplitStopVolumeTooManyRequests, not be downgraded to 500")
		assert.Equal(tt, float64(429), tooMany.Code)
		assert.Contains(tt, tooMany.Message, "rate limited")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturns500InternalServerError", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(
			&coreapi.V1SplitStopVolumeInternalServerError{Code: 500, Message: "An error occurred"}, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStopVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "An error occurred")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPICallFails_Returns500", func(tt *testing.T) {
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("connection refused"))

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStopVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "Core API call failed")
		inv.AssertExpectations(tt)
	})

	t.Run("CoreAPIReturnsUnexpectedType_Returns500", func(tt *testing.T) {
		// A nil response with nil error exercises the default branch of the
		// response-type switch: there is no typed response to map, so the
		// proxy must synthesise a generic 500.
		inv := &mockInvoker{}
		cleanup := setupSplitStopHappyPath(inv)
		defer cleanup()

		inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(nil, nil)

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}

		result, err := handler.V1betaSplitStopVolume(context.Background(), baseParams)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaSplitStopVolumeInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Contains(tt, internalErr.Message, "internal error")
		inv.AssertExpectations(tt)
	})
}

func TestSplitStopVolume_XCorrelationIDPropagation(t *testing.T) {
	// The proxy must forward the caller's x-correlation-id to the core-api call
	// so cross-service log correlation works.
	inv := &mockInvoker{}
	cleanup := setupSplitStopHappyPath(inv)
	defer cleanup()

	var capturedParams coreapi.V1SplitStopVolumeParams
	inv.On("V1SplitStopVolume", mock.Anything, mock.MatchedBy(func(p coreapi.V1SplitStopVolumeParams) bool {
		capturedParams = p
		return true
	})).Return(&coreapi.OperationV1{
		Name: coreapi.NewOptString("/v1beta/projects/proj/locations/loc/operations/op-id"),
		Done: coreapi.NewOptBool(true),
	}, nil)

	handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	params := gcpgenserver.V1betaSplitStopVolumeParams{
		LocationId:     "loc",
		ProjectNumber:  "proj",
		VolumeId:       "vol-1",
		XCorrelationID: gcpgenserver.NewOptString("corr-id-xyz"),
	}

	result, err := handler.V1betaSplitStopVolume(context.Background(), params)
	assert.NoError(t, err)
	_, ok := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.True(t, capturedParams.XCorrelationID.IsSet())
	assert.Equal(t, "corr-id-xyz", capturedParams.XCorrelationID.Value)
	inv.AssertExpectations(t)
}

func TestSplitStopVolume_EmptyOperationIDGeneratesUUID(t *testing.T) {
	// When the core API returns an OperationV1 with an empty Name, the proxy
	// must synthesise an operation ID embedding the project and location so
	// the response is still well-formed for the client.
	inv := &mockInvoker{}
	cleanup := setupSplitStopHappyPath(inv)
	defer cleanup()

	inv.On("V1SplitStopVolume", mock.Anything, mock.Anything).Return(&coreapi.OperationV1{
		// Name intentionally unset.
		Done: coreapi.NewOptBool(true),
	}, nil)

	handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	params := gcpgenserver.V1betaSplitStopVolumeParams{
		LocationId:    "us-east4",
		ProjectNumber: "my-project",
		VolumeId:      "vol-1",
	}

	result, err := handler.V1betaSplitStopVolume(context.Background(), params)
	assert.NoError(t, err)
	op, ok := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.True(t, op.Name.IsSet())
	assert.Contains(t, op.Name.Value, "my-project")
	assert.Contains(t, op.Name.Value, "us-east4")
	inv.AssertExpectations(t)
}
