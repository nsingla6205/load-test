package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestMapReplicationStateToInternalLifeCycleState(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected gcpgenserver.VolumeReplicationInternalV1betaLifeCycleState
	}{
		{"Creating", datamodel.LifeCycleStateCreating, gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateCreating},
		{"Available", datamodel.LifeCycleStateAvailable, gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateAvailable},
		{"Deleting", datamodel.LifeCycleStateDeleting, gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateDeleting},
		{"Deleted", datamodel.LifeCycleStateDeleted, gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateDeleted},
		{"Error", datamodel.LifeCycleStateError, gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateError},
		{"Disabled", datamodel.LifeCycleStateDisabled, gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateDisabled},
		{"Updating", datamodel.LifeCycleStateUpdating, gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateUpdating},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapReplicationStateToInternalLifeCycleState(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestMapEndpointTypeToInternal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected gcpgenserver.VolumeReplicationInternalV1betaEndpointType
	}{
		{"SrcEndpoint", datamodel.SrcEndpoint, gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeSrc},
		{"DstEndpoint", datamodel.DstEndpoint, gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeDst},
		{"Unknown", "unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapEndpointTypeToInternal(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestMapMirrorStateToInternal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected gcpgenserver.VolumeReplicationInternalV1betaMirrorState
	}{
		{"Uninitialized", datamodel.OntapUninitialized, gcpgenserver.VolumeReplicationInternalV1betaMirrorStatePREPARING},
		{"BrokenOff", datamodel.OntapBrokenOff, gcpgenserver.VolumeReplicationInternalV1betaMirrorStateSTOPPED},
		{"Snapmirrored", datamodel.OntapSnapmirrored, gcpgenserver.VolumeReplicationInternalV1betaMirrorStateMIRRORED},
		{"Unknown", "unknown", gcpgenserver.VolumeReplicationInternalV1betaMirrorStateMIRRORSTATEUNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapMirrorStateToInternal(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestMapRelationshipStatusToInternal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatus
	}{
		{"Idle", datamodel.SnapmirrorRelationshipIdle, gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusIdle},
		{"Transferring", datamodel.SnapmirrorRelationshipTransferring, gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusTransferring},
		{"Queued", datamodel.SnapmirrorRelationshipQueued, gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusQueued},
		{"Failed", datamodel.SnapmirrorRelationshipFailed, gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusFailed},
		{"Aborted", datamodel.SnapmirrorRelationshipAborted, gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusAborted},
		{"HardAborted", datamodel.SnapmirrorRelationshipHardAborted, gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusHardAborted},
		{"Success", datamodel.SnapmirrorRelationshipSuccess, gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusIdle},
		{"Unknown", "unknown", gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusIdle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapRelationshipStatusToInternal(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestConvertToVolumeReplicationInternalV1Beta(t *testing.T) {
	t.Run("ValidatingMirrorStateConversion", func(t *testing.T) {
		tests := []struct {
			name                string
			replicationStatus   string
			mirrorState         string
			expectedMirrorState gcpgenserver.VolumeReplicationInternalV1betaMirrorState
		}{
			{
				name:                "Transferring with Uninitialized Mirror",
				replicationStatus:   datamodel.SnapmirrorRelationshipTransferring,
				mirrorState:         datamodel.OntapUninitialized,
				expectedMirrorState: gcpgenserver.VolumeReplicationInternalV1betaMirrorStateBASELINETRANSFERRING,
			},
			{
				name:                "Transferring with Mirrored State",
				replicationStatus:   datamodel.SnapmirrorRelationshipTransferring,
				mirrorState:         datamodel.OntapSnapmirrored,
				expectedMirrorState: gcpgenserver.VolumeReplicationInternalV1betaMirrorStateTRANSFERRING,
			},
			{
				name:                "Idle with Mirrored State",
				replicationStatus:   datamodel.SnapmirrorRelationshipIdle,
				mirrorState:         datamodel.OntapSnapmirrored,
				expectedMirrorState: gcpgenserver.VolumeReplicationInternalV1betaMirrorStateMIRRORED,
			},
			{
				name:                "Unspecified",
				replicationStatus:   "",
				mirrorState:         "",
				expectedMirrorState: gcpgenserver.VolumeReplicationInternalV1betaMirrorStateMIRRORSTATEUNSPECIFIED,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				timeNow := time.Now()
				replication := &datamodel.VolumeReplication{
					BaseModel: datamodel.BaseModel{
						ID:        123,
						UUID:      "test-uuid",
						CreatedAt: timeNow,
						UpdatedAt: timeNow,
					},
					Name:  "Test Replication",
					State: datamodel.LifeCycleStateAvailable,
					ReplicationAttributes: &datamodel.ReplicationDetails{
						EndpointType:               "src",
						ReplicationSchedule:        "daily",
						SourceHostName:             "test-source-host",
						SourceSvmName:              "test-source-svm",
						SourceVolumeName:           "test-source-vol",
						DestinationHostName:        "test-dest-host",
						DestinationSvmName:         "test-dest-svm",
						DestinationVolumeName:      "test-dest-vol",
						DestinationVolumeUUID:      "test-dest-uuid",
						SourceVolumeUUID:           "test-source-uuid",
						SourcePoolUUID:             "test-source-pool",
						DestinationPoolUUID:        "test-dest-pool",
						SourceLocation:             "test-source-loc",
						DestinationLocation:        "test-dest-loc",
						SourceReplicationUUID:      "test-source-repl",
						DestinationReplicationUUID: "test-dest-repl",
					},
					RelationshipStatus: &tt.replicationStatus,
					MirrorState:        nillable.GetNilIfEmptyString(tt.mirrorState),
					TotalProgress:      100,
					Healthy:            true,
				}

				result := convertToVolumeReplicationInternalV1Beta(replication)
				if result.MirrorState.Value != tt.expectedMirrorState {
					t.Errorf("Expected MirrorState %s, got %s", tt.expectedMirrorState, result.MirrorState.Value)
				}
			})
		}
	})

	t.Run("ValidateFullReplicationObjectConversion", func(t *testing.T) {
		timeNow := time.Now()
		snapmirrored := datamodel.OntapSnapmirrored
		snapmirrorRelationshipIdle := datamodel.SnapmirrorRelationshipIdle

		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:        123,
				UUID:      "some-uuid",
				CreatedAt: timeNow,
				UpdatedAt: timeNow,
			},
			Name:         "Test Replication",
			Description:  "Test Description",
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: "Test State Details",
			Uri:          "projects/45110233509/locations/australia-southeast1/volume/godpvolume4/replications/replication-name-6",
			RemoteUri:    "projects/45110233509/locations/us-east4/volume/gosrcvolume1/replications/replication-name-6",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:               "src",
				ReplicationSchedule:        "daily",
				SourcePoolUUID:             "source-pool-uuid",
				SourceVolumeUUID:           "source-volume-uuid",
				SourceLocation:             "source-location",
				SourceHostName:             "source-hostname",
				SourceReplicationUUID:      "source-replication-uuid",
				SourceSvmName:              "source-svm-name",
				SourceVolumeName:           "source-volume-name",
				DestinationPoolUUID:        "destination-pool-uuid",
				DestinationVolumeUUID:      "destination-volume-uuid",
				DestinationLocation:        "destination-location",
				DestinationHostName:        "destination-hostname",
				DestinationReplicationUUID: "destination-replication-uuid",
				DestinationSvmName:         "destination-svm-name",
				DestinationVolumeName:      "destination-volume-name",
				ExternalUUID:               "external-uuid",
			},
			MirrorState:           &snapmirrored,
			RelationshipStatus:    &snapmirrorRelationshipIdle,
			TotalProgress:         100,
			TotalTransferBytes:    1000000,
			TotalTransferTimeSecs: 3600,
			LastTransferSize:      500000,
			LastTransferError:     "no error",
			LastTransferDuration:  1800,
			LastTransferEndTime:   &timeNow,
			ProgressLastUpdated:   &timeNow,
			LastUpdatedFromOntap:  timeNow,
			Healthy:               false,
			UnhealthyReason:       "No issues detected",
			LagTime:               30,
			AccountID:             1,
			VolumeID:              1,
		}

		result := convertToVolumeReplicationInternalV1Beta(replication)

		// Basic properties
		if result.VolumeReplicationUuid.Value != replication.UUID {
			t.Errorf("Expected UUID %s, got %s", replication.UUID, result.VolumeReplicationUuid.Value)
		}
		if result.LifeCycleState.Value != gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateCreating {
			t.Errorf("Expected LifeCycleState %s, got %s", gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateCreating, result.LifeCycleState.Value)
		}
		if result.LifeCycleStateDetails.Value != replication.StateDetails {
			t.Errorf("Expected LifeCycleStateDetails %s, got %s", replication.StateDetails, result.LifeCycleStateDetails.Value)
		}

		// Endpoint and Host properties
		if result.EndpointType != gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeSrc {
			t.Errorf("Expected EndpointType %s, got %s", gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeSrc, result.EndpointType)
		}
		if result.SourceHostName != replication.ReplicationAttributes.SourceHostName {
			t.Errorf("Expected SourceHostName %s, got %s", replication.ReplicationAttributes.SourceHostName, result.SourceHostName)
		}
		if result.SourceServerName != replication.ReplicationAttributes.SourceSvmName {
			t.Errorf("Expected SourceServerName %s, got %s", replication.ReplicationAttributes.SourceSvmName, result.SourceServerName)
		}
		if result.SourceVolumeName != replication.ReplicationAttributes.SourceVolumeName {
			t.Errorf("Expected SourceVolumeName %s, got %s", replication.ReplicationAttributes.SourceVolumeName, result.SourceVolumeName)
		}

		// Destination properties
		if result.DestinationHostName != replication.ReplicationAttributes.DestinationHostName {
			t.Errorf("Expected DestinationHostName %s, got %s", replication.ReplicationAttributes.DestinationHostName, result.DestinationHostName)
		}
		if result.DestinationServerName != replication.ReplicationAttributes.DestinationSvmName {
			t.Errorf("Expected DestinationServerName %s, got %s", replication.ReplicationAttributes.DestinationSvmName, result.DestinationServerName)
		}
		if result.DestinationVolumeName != replication.ReplicationAttributes.DestinationVolumeName {
			t.Errorf("Expected DestinationVolumeName %s, got %s", replication.ReplicationAttributes.DestinationVolumeName, result.DestinationVolumeName)
		}
		if result.DestinationVolumeUuid.Value != replication.ReplicationAttributes.DestinationVolumeUUID {
			t.Errorf("Expected DestinationVolumeUuid %s, got %s", replication.ReplicationAttributes.DestinationVolumeUUID, result.DestinationVolumeUuid.Value)
		}

		// Naming and state properties
		if result.Name.Value != replication.Name {
			t.Errorf("Expected Name %s, got %s", replication.Name, result.Name.Value)
		}
		if result.MirrorState.Value != gcpgenserver.VolumeReplicationInternalV1betaMirrorStateMIRRORED {
			t.Errorf("Expected MirrorState %s, got %s", gcpgenserver.VolumeReplicationInternalV1betaMirrorStateMIRRORED, result.MirrorState.Value)
		}

		// Progress and status
		if result.RelationshipStatus.Value != gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusIdle {
			t.Errorf("Expected RelationshipStatus %s, got %s", gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusIdle, result.RelationshipStatus.Value)
		}
		if result.TotalProgress.Value != replication.TotalProgress {
			t.Errorf("Expected TotalProgress %d, got %d", replication.TotalProgress, result.TotalProgress.Value)
		}
		if result.Healthy.Value != replication.Healthy {
			t.Errorf("Expected Healthy %t, got %t", replication.Healthy, result.Healthy.Value)
		}

		// Transfer metrics
		if result.TotalTransferBytes.Value != replication.TotalTransferBytes {
			t.Errorf("Expected TotalTransferBytes %d, got %d", replication.TotalTransferBytes, result.TotalTransferBytes.Value)
		}
		if result.LagTime.Value != replication.LagTime {
			t.Errorf("Expected LagTime %d, got %d", replication.LagTime, result.LagTime.Value)
		}
		if result.LastTransferSize.Value != replication.LastTransferSize {
			t.Errorf("Expected LastTransferSize %d, got %d", replication.LastTransferSize, result.LastTransferSize.Value)
		}
	})

	t.Run("NilReplicationObject", func(t *testing.T) {
		result := convertToVolumeReplicationInternalV1Beta(nil)

		// Should return empty object
		if result.VolumeReplicationUuid.Set {
			t.Errorf("Expected VolumeReplicationUuid to not be set for nil replication")
		}
		if result.LifeCycleState.Set {
			t.Errorf("Expected LifeCycleState to not be set for nil replication")
		}
	})

	t.Run("NilReplicationAttributes", func(t *testing.T) {
		timeNow := time.Now()
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:        123,
				UUID:      "test-uuid",
				CreatedAt: timeNow,
				UpdatedAt: timeNow,
			},
			Name:                  "Test Replication",
			State:                 datamodel.LifeCycleStateAvailable,
			ReplicationAttributes: nil, // This is nil
			RelationshipStatus:    nillable.GetNilIfEmptyString(datamodel.SnapmirrorRelationshipTransferring),
			MirrorState:           nillable.GetNilIfEmptyString(datamodel.OntapUninitialized),
			TotalProgress:         100,
			Healthy:               true,
		}

		result := convertToVolumeReplicationInternalV1Beta(replication)

		// Basic fields should still be set
		if result.VolumeReplicationUuid.Value != replication.UUID {
			t.Errorf("Expected UUID %s, got %s", replication.UUID, result.VolumeReplicationUuid.Value)
		}
		if result.Name.Value != replication.Name {
			t.Errorf("Expected Name %s, got %s", replication.Name, result.Name.Value)
		}

		// Fields that depend on ReplicationAttributes should be empty/default
		if result.SourceHostName != "" {
			t.Errorf("Expected empty SourceHostName, got %s", result.SourceHostName)
		}
		if result.DestinationHostName != "" {
			t.Errorf("Expected empty DestinationHostName, got %s", result.DestinationHostName)
		}
		if result.RemoteRegion != "" {
			t.Errorf("Expected empty RemoteRegion, got %s", result.RemoteRegion)
		}
	})

	t.Run("NilRelationshipStatusAndMirrorState", func(t *testing.T) {
		timeNow := time.Now()
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:        123,
				UUID:      "test-uuid",
				CreatedAt: timeNow,
				UpdatedAt: timeNow,
			},
			Name:  "Test Replication",
			State: datamodel.LifeCycleStateAvailable,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:               "src",
				ReplicationSchedule:        "daily",
				SourceHostName:             "test-source-host",
				SourceSvmName:              "test-source-svm",
				SourceVolumeName:           "test-source-vol",
				DestinationHostName:        "test-dest-host",
				DestinationSvmName:         "test-dest-svm",
				DestinationVolumeName:      "test-dest-vol",
				DestinationVolumeUUID:      "test-dest-uuid",
				SourceVolumeUUID:           "test-source-uuid",
				SourcePoolUUID:             "test-source-pool",
				DestinationPoolUUID:        "test-dest-pool",
				SourceLocation:             "test-source-loc",
				DestinationLocation:        "test-dest-loc",
				SourceReplicationUUID:      "test-source-repl",
				DestinationReplicationUUID: "test-dest-repl",
			},
			RelationshipStatus: nil, // This is nil
			MirrorState:        nil, // This is nil
			TotalProgress:      100,
			Healthy:            true,
		}

		result := convertToVolumeReplicationInternalV1Beta(replication)

		// Should use default mirror state when both are nil
		expectedMirrorState := gcpgenserver.VolumeReplicationInternalV1betaMirrorStateMIRRORSTATEUNSPECIFIED
		if result.MirrorState.Value != expectedMirrorState {
			t.Errorf("Expected MirrorState %s, got %s", expectedMirrorState, result.MirrorState.Value)
		}
	})
}

func TestConvertJSONBLabelsToOptLabels(t *testing.T) {
	tests := []struct {
		name     string
		labels   *datamodel.JSONB
		expected gcpgenserver.OptVolumeReplicationInternalV1betaLabels
	}{
		{
			name:     "NilLabels",
			labels:   nil,
			expected: gcpgenserver.OptVolumeReplicationInternalV1betaLabels{},
		},
		{
			name: "ValidLabels",
			labels: &datamodel.JSONB{
				"environment": "production",
				"team":        "platform",
				"cost-center": "engineering",
			},
			expected: gcpgenserver.NewOptVolumeReplicationInternalV1betaLabels(
				gcpgenserver.VolumeReplicationInternalV1betaLabels{
					"environment": "production",
					"team":        "platform",
					"cost-center": "engineering",
				},
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertJSONBLabelsToOptLabels(tt.labels)

			if result.Set != tt.expected.Set {
				t.Errorf("Expected Set %t, got %t", tt.expected.Set, result.Set)
			}

			if result.Set && tt.expected.Set {
				if len(result.Value) != len(tt.expected.Value) {
					t.Errorf("Expected %d labels, got %d", len(tt.expected.Value), len(result.Value))
				}

				for key, expectedValue := range tt.expected.Value {
					if actualValue, exists := result.Value[key]; !exists {
						t.Errorf("Expected key %s to exist", key)
					} else if actualValue != expectedValue {
						t.Errorf("Expected value %s for key %s, got %s", expectedValue, key, actualValue)
					}
				}
			}
		})
	}
}

func TestConvertToPoolInternalV1Beta_LargeCapacityConfig(t *testing.T) {
	tests := []struct {
		name      string
		lvHAPairs int64
		wantSet   bool
		want      gcpgenserver.PoolInternalV1betaLargeCapacityConfig
	}{
		{
			name:      "12-node pool",
			lvHAPairs: commonparams.LvHAPairsNodeCount12,
			wantSet:   true,
			want:      gcpgenserver.PoolInternalV1betaLargeCapacityConfigNODECOUNT12,
		},
		{
			name:      "24-node pool",
			lvHAPairs: commonparams.LvHAPairsNodeCount24,
			wantSet:   true,
			want:      gcpgenserver.PoolInternalV1betaLargeCapacityConfigNODECOUNT24,
		},
		{
			name:    "unset HA pairs omits the config",
			wantSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := &models.Pool{
				PoolAttributes: &models.PoolAttributes{LvHAPairs: tt.lvHAPairs},
			}

			result := convertToPoolInternalV1Beta(pool)

			assert.Equal(t, tt.wantSet, result.LargeCapacityConfig.IsSet())
			if tt.wantSet {
				assert.Equal(t, tt.want, result.LargeCapacityConfig.Value)
			}
		})
	}
}

func TestConvertToPoolInternalV1Beta(t *testing.T) {
	timenow := time.Now()
	autoTieringConfig := &models.AutoTieringConfig{
		HotTierSizeInBytes:      0,
		EnableHotTierAutoResize: false,
	}
	pool := &models.Pool{
		BaseModel: models.BaseModel{
			ID:        1,
			UUID:      "pool-uuid",
			CreatedAt: timenow,
			UpdatedAt: timenow,
			DeletedAt: nil,
		},
		Name:                    "Test Pool",
		Description:             "Test Pool Description",
		State:                   datamodel.LifeCycleStateAvailable,
		StateDetails:            "Pool is available",
		SizeInBytes:             0,
		AccountName:             "test-account",
		VendorID:                "vendor-id",
		Region:                  "us-central1",
		Zone:                    "us-central1-a",
		TotalThroughputMibps:    0,
		UtilizedThroughputMibps: 0,
		AllowAutoTiering:        false,
		AutoTieringConfig:       autoTieringConfig,
		VendorSubNetID:          "vendor-subnet-id",
		QosType:                 "none",
		PoolAttributes: &models.PoolAttributes{
			NumberOfVolumes: int64(10),
		},
	}

	result := convertToPoolInternalV1Beta(pool)

	if result.PoolId.Value != pool.UUID {
		t.Errorf("Expected PoolId %s, got %s", pool.UUID, result.PoolId.Value)
	}
	if result.ResourceId != pool.Name {
		t.Errorf("Expected ResourceId %s, got %s", pool.Name, result.ResourceId)
	}
	if result.ServiceLevel != gcpgenserver.PoolInternalV1betaServiceLevel(pool.ServiceLevel) {
		t.Errorf("Expected ServiceLevel %s, got %s", pool.ServiceLevel, result.ServiceLevel)
	}
	if result.QosType.Value != pool.QosType {
		t.Errorf("Expected QosType %s, got %s", pool.QosType, result.QosType.Value)
	}
	if result.SizeInBytes != float64(pool.SizeInBytes) {
		t.Errorf("Expected SizeInBytes %d, got %f", pool.SizeInBytes, result.SizeInBytes)
	}
	if result.TotalThroughputMibps.Value != pool.TotalThroughputMibps {
		t.Errorf("Expected TotalThroughputMibps %f, got %f", pool.TotalThroughputMibps, result.TotalThroughputMibps.Value)
	}
	if result.AvailableThroughputMibps.Value != pool.TotalThroughputMibps-pool.UtilizedThroughputMibps {
		t.Errorf("Expected AvailableThroughputMibps %f, got %f", pool.TotalThroughputMibps-pool.UtilizedThroughputMibps, result.AvailableThroughputMibps.Value)
	}
	if result.NumberOfVolumes.Value != int32(pool.PoolAttributes.NumberOfVolumes) {
		t.Errorf("Expected NumberOfVolumes %d, got %d", pool.PoolAttributes.NumberOfVolumes, result.NumberOfVolumes.Value)
	}
	if result.StoragePoolState.Value != gcpgenserver.PoolInternalV1betaStoragePoolState(pool.State) {
		t.Errorf("Expected StoragePoolState %s, got %s", gcpgenserver.PoolInternalV1betaStoragePoolState(pool.State), result.StoragePoolState.Value)
	}
	if result.StoragePoolStateDetails.Value != pool.StateDetails {
		t.Errorf("Expected StoragePoolStateDetails %s, got %s", pool.StateDetails, result.StoragePoolStateDetails.Value)
	}
	if result.CreatedAt.Value.Unix() != pool.CreatedAt.Unix() {
		t.Errorf("Expected CreatedAt %s, got %s", pool.CreatedAt, result.CreatedAt.Value)
	}
	if result.UpdatedAt.Value.Unix() != pool.UpdatedAt.Unix() {
		t.Errorf("Expected UpdatedAt %s, got %s", pool.UpdatedAt, result.UpdatedAt.Value)
	}
	if result.StateDetails.Value != pool.StateDetails {
		t.Errorf("Expected StateDetails %s, got %s", pool.StateDetails, result.StateDetails.Value)
	}
	if result.Description.Value != pool.Description {
		t.Errorf("Expected Description %s, got %s", pool.Description, result.Description.Value)
	}
	if result.Zone.Value != pool.Zone {
		t.Errorf("Expected Zone %s, got %s", pool.Zone, result.Zone.Value)
	}
	if result.AllowAutoTiering.Value != pool.AllowAutoTiering {
		t.Errorf("Expected AllowAutoTiering %t, got %t", pool.AllowAutoTiering, result.AllowAutoTiering.Value)
	}
	if result.LargeCapacity.Value != pool.LargeCapacity {
		t.Errorf("Expected LargeCapacity %t, got %t", pool.LargeCapacity, result.LargeCapacity.Value)
	}
}

func TestConvertToVolumeReplicationsInternalV1Beta(t *testing.T) {
	timeNow := time.Now()
	snapmirrored := datamodel.OntapSnapmirrored
	snapmirrorRelationshipIdle := datamodel.SnapmirrorRelationshipIdle

	replications := []*datamodel.VolumeReplication{
		{
			BaseModel: datamodel.BaseModel{
				ID:        123,
				UUID:      "some-uuid",
				CreatedAt: timeNow,
				UpdatedAt: timeNow,
				DeletedAt: nil,
			},
			Name:         "Test Replication",
			Description:  "Test Description",
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: "Test State Details",
			Uri:          "projects/45110233509/locations/australia-southeast1/volume/godpvolume4/replications/replication-name-6",
			RemoteUri:    "projects/45110233509/locations/us-east4/volume/gosrcvolume1/replications/replication-name-6",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:               "src",
				ReplicationSchedule:        "daily",
				SourcePoolUUID:             "source-pool-uuid",
				SourceVolumeUUID:           "source-volume-uuid",
				SourceLocation:             "source-location",
				SourceHostName:             "source-hostname",
				SourceReplicationUUID:      "source-replication-uuid",
				SourceSvmName:              "source-svm-name",
				SourceVolumeName:           "source-volume-name",
				DestinationPoolUUID:        "destination-pool-uuid",
				DestinationVolumeUUID:      "destination-volume-uuid",
				DestinationLocation:        "destination-location",
				DestinationHostName:        "destination-hostname",
				DestinationReplicationUUID: "destination-replication-uuid",
				DestinationSvmName:         "destination-svm-name",
				DestinationVolumeName:      "destination-volume-name",
				ExternalUUID:               "external-uuid",
			},
			MirrorState:           &snapmirrored,
			RelationshipStatus:    &snapmirrorRelationshipIdle,
			TotalProgress:         100,
			TotalTransferBytes:    1000000,
			TotalTransferTimeSecs: 3600,
			LastTransferSize:      500000,
			LastTransferError:     "no error",
			LastTransferDuration:  1800,
			LastTransferEndTime:   &timeNow,
			ProgressLastUpdated:   &timeNow,
			LastUpdatedFromOntap:  timeNow,
			Healthy:               false,
			UnhealthyReason:       "No issues detected",
			LagTime:               30,
			AccountID:             1,
			VolumeID:              1,
		},
	}

	result := convertToVolumeReplicationsInternalV1Beta(replications)

	if len(result) != 1 {
		t.Errorf("Expected 1 replication, got %d", len(result))
	}
	if result[0].VolumeReplicationUuid.Value != replications[0].UUID {
		t.Errorf("Expected UUID %s, got %s", replications[0].UUID, result[0].VolumeReplicationUuid.Value)
	}
	if result[0].Name.Value != replications[0].Name {
		t.Errorf("Expected Name %s, got %s", replications[0].Name, result[0].Name.Value)
	}
	if result[0].SourceHostName != replications[0].ReplicationAttributes.SourceHostName {
		t.Errorf("Expected SourceHostName %s, got %s", replications[0].ReplicationAttributes.SourceHostName, result[0].SourceHostName)
	}
	if result[0].DestinationHostName != replications[0].ReplicationAttributes.DestinationHostName {
		t.Errorf("Expected DestinationHostName %s, got %s", replications[0].ReplicationAttributes.DestinationHostName, result[0].DestinationHostName)
	}
}

func TestConvertBackupDataModelToInternalBackupsV1beta(t *testing.T) {
	t.Run("BasicConversionWithAllFields", func(t *testing.T) {
		sourceRegionName := "us-central1"
		createdAt := time.Now().AddDate(0, 0, -5)
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid-123",
				CreatedAt: createdAt,
			},
			Name:                    "test-backup",
			VolumeUUID:              "volume-uuid-456",
			State:                   datamodel.LifeCycleStateAvailable,
			SizeInBytes:             1024,
			Description:             "Test backup description",
			Type:                    "MANUAL",
			LatestLogicalBackupSize: 2048,
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier:   "123456789",
				BucketName:          "test-bucket",
				VolumeName:          "test-volume",
				SnapshotName:        "test-snapshot",
				UseExistingSnapshot: true,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid-789",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{
						BucketName:   "test-bucket",
						SatisfiesPzi: true,
						SatisfiesPzs: false,
					},
				},
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		assert.True(t, result.ResourceId.Set)
		assert.Equal(t, "volume-uuid-456", result.VolumeId.Value)
		assert.True(t, result.VolumeId.Set)
		assert.Equal(t, gcpgenserver.InternalBackupV1betaStateREADY, result.State.Value)
		assert.True(t, result.State.Set)
		assert.Equal(t, createdAt, result.Created.Value)
		assert.True(t, result.Created.Set)
		assert.Equal(t, "backup-uuid-123", result.BackupId.Value)
		assert.True(t, result.BackupId.Set)
		assert.Equal(t, int64(1024), result.VolumeUsageBytes.Value)
		assert.True(t, result.VolumeUsageBytes.Set)
		assert.Equal(t, "vault-uuid-789", result.BackupVaultId.Value)
		assert.True(t, result.BackupVaultId.Set)
		assert.Equal(t, "Test backup description", result.Description.Value)
		assert.True(t, result.Description.Set)
		assert.Equal(t, gcpgenserver.InternalBackupV1betaBackupTypeMANUAL, result.BackupType.Value)
		assert.True(t, result.BackupType.Set)
		assert.True(t, result.SourceSnapshot.Set)
		assert.Contains(t, result.SourceSnapshot.Value, "test-snapshot")
		assert.True(t, result.SourceVolume.Set)
		assert.Contains(t, result.SourceVolume.Value, "test-volume")
		assert.Equal(t, "us-central1", result.BackupRegion.Value)
		assert.True(t, result.BackupRegion.Set)
		assert.Equal(t, "us-central1", result.VolumeRegion.Value)
		assert.True(t, result.VolumeRegion.Set)
		assert.True(t, result.SatisfiesPzi.Value)
		assert.True(t, result.SatisfiesPzi.Set)
		assert.False(t, result.SatisfiesPzs.Value)
		assert.True(t, result.SatisfiesPzs.Set)
		assert.Equal(t, int64(2048), result.BackupChainBytes.Value)
		assert.True(t, result.BackupChainBytes.Set)
		assert.False(t, result.IsRestoring.Value)
		assert.True(t, result.IsRestoring.Set)
		assert.False(t, result.AssetLocationMetadata.Set)
	})

	t.Run("StateConversion_READY", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.Equal(t, gcpgenserver.InternalBackupV1betaStateREADY, result.State.Value)
	})

	t.Run("StateConversion_UPDATING", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateUpdating,
			Type:       "MANUAL",
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.Equal(t, gcpgenserver.InternalBackupV1betaStateUPDATING, result.State.Value)
	})

	t.Run("StateConversion_DefaultState", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      "IN_PROGRESS",
			Type:       "MANUAL",
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.Equal(t, gcpgenserver.InternalBackupV1betaState("IN_PROGRESS"), result.State.Value)
	})

	t.Run("IsRestoring_True", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, true)

		assert.True(t, result.IsRestoring.Value)
		assert.True(t, result.IsRestoring.Set)
	})

	t.Run("IsRestoring_False", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.False(t, result.IsRestoring.Value)
		assert.True(t, result.IsRestoring.Set)
	})

	t.Run("WithSnapshot_UseExistingSnapshotTrue", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier:   "123456789",
				BucketName:          "test-bucket",
				VolumeName:          "test-volume",
				SnapshotName:        "snapshot-123",
				UseExistingSnapshot: true,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.True(t, result.SourceSnapshot.Set)
		assert.Contains(t, result.SourceSnapshot.Value, "snapshot-123")
	})

	t.Run("WithoutSnapshot_UseExistingSnapshotFalse", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				VolumeName:          "test-volume",
				SnapshotName:        "snapshot-123",
				UseExistingSnapshot: false,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.False(t, result.SourceSnapshot.Set)
	})

	t.Run("WithoutSnapshot_EmptySnapshotName", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				VolumeName:          "test-volume",
				SnapshotName:        "",
				UseExistingSnapshot: true,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.False(t, result.SourceSnapshot.Set)
	})

	t.Run("PZIAndPZSFlags_FromBucketDetails", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName: "matching-bucket",
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{
						BucketName:   "other-bucket",
						SatisfiesPzi: false,
						SatisfiesPzs: false,
					},
					{
						BucketName:   "matching-bucket",
						SatisfiesPzi: true,
						SatisfiesPzs: true,
					},
				},
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.True(t, result.SatisfiesPzi.Value)
		assert.True(t, result.SatisfiesPzs.Value)
	})

	t.Run("BackupChainBytes_SetWhenNonZero", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:                    "test-backup",
			VolumeUUID:              "volume-uuid",
			State:                   datamodel.LifeCycleStateAvailable,
			Type:                    "MANUAL",
			LatestLogicalBackupSize: 5000,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.True(t, result.BackupChainBytes.Set)
		assert.Equal(t, int64(5000), result.BackupChainBytes.Value)
	})

	t.Run("BackupChainBytes_UnsetWhenZero", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:                    "test-backup",
			VolumeUUID:              "volume-uuid",
			State:                   datamodel.LifeCycleStateAvailable,
			Type:                    "MANUAL",
			LatestLogicalBackupSize: 0,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.False(t, result.BackupChainBytes.Set)
	})

	t.Run("ImmutableBackup_RetentionNotExpired_ShouldSetEnforcedRetentionEndTime", func(t *testing.T) {
		sourceRegionName := "us-east1"
		retentionDays := int64(30)
		createdAt := time.Now().AddDate(0, 0, -10) // Created 10 days ago
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: createdAt,
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       utils.BackupTypeMANUAL,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &retentionDays,
					IsAdhocBackupImmutable:                 true,
				},
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.True(t, result.EnforcedRetentionEndTime.Set)
		expectedExpiration := createdAt.AddDate(0, 0, 30)
		assert.Equal(t, expectedExpiration.Year(), result.EnforcedRetentionEndTime.Value.Year())
		assert.Equal(t, expectedExpiration.Month(), result.EnforcedRetentionEndTime.Value.Month())
		assert.Equal(t, expectedExpiration.Day(), result.EnforcedRetentionEndTime.Value.Day())
	})

	t.Run("ImmutableBackup_RetentionExpired_ShouldNotSetEnforcedRetentionEndTime", func(t *testing.T) {
		sourceRegionName := "us-east1"
		retentionDays := int64(30)
		createdAt := time.Now().AddDate(0, 0, -40) // Created 40 days ago (expired)
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: createdAt,
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       utils.BackupTypeMANUAL,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &retentionDays,
					IsAdhocBackupImmutable:                 true,
				},
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.False(t, result.EnforcedRetentionEndTime.Set)
	})

	t.Run("ImmutableBackup_NotImmutable_ShouldNotSetEnforcedRetentionEndTime", func(t *testing.T) {
		sourceRegionName := "us-east1"
		retentionDays := int64(30)
		createdAt := time.Now().AddDate(0, 0, -10)
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: createdAt,
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       utils.BackupTypeMANUAL,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &retentionDays,
					IsAdhocBackupImmutable:                 false, // Not immutable
				},
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.False(t, result.EnforcedRetentionEndTime.Set)
	})

	t.Run("ImmutableBackup_NilImmutableAttributes_ShouldNotSetEnforcedRetentionEndTime", func(t *testing.T) {
		sourceRegionName := "us-east1"
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       utils.BackupTypeMANUAL,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName:    &sourceRegionName,
				ImmutableAttributes: nil,
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.False(t, result.EnforcedRetentionEndTime.Set)
	})

	t.Run("ImmutableBackup_ZeroRetentionDuration_ShouldNotSetEnforcedRetentionEndTime", func(t *testing.T) {
		sourceRegionName := "us-east1"
		retentionDays := int64(0)
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "volume-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       utils.BackupTypeMANUAL,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: "vault-uuid",
				},
				SourceRegionName: &sourceRegionName,
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &retentionDays,
					IsAdhocBackupImmutable:                 true,
				},
				BucketDetails: datamodel.BucketDetailsArray{
					{BucketName: "test-bucket"},
				},
			},
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket",
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.False(t, result.EnforcedRetentionEndTime.Set)
	})

	t.Run("OntapBackup_ReplacesVolumeNameWithUUID", func(t *testing.T) {
		sourceRegionName := "us-east1"
		const volumeName = "ontap-vol-name"
		const volumeUUID = "vol-uuid-abc123"

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: volumeUUID,
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier:  "test-account",
				VolumeName:         volumeName,
				SourceVolumeZone:   "us-east1-a",
				IsExpertModeBackup: true,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "vault-uuid"},
				SourceRegionName: &sourceRegionName,
				BucketDetails:    datamodel.BucketDetailsArray{},
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.True(t, result.SourceVolume.Set)
		assert.Contains(t, result.SourceVolume.Value, volumeUUID)
		assert.NotContains(t, result.SourceVolume.Value, volumeName)
	})

	t.Run("NonOntapBackup_UsesVolumeName", func(t *testing.T) {
		sourceRegionName := "us-east1"
		const volumeName = "regular-vol-name"

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "vol-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier:  "test-account",
				VolumeName:         volumeName,
				SourceVolumeZone:   "us-east1-a",
				IsExpertModeBackup: false,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "vault-uuid"},
				SourceRegionName: &sourceRegionName,
				BucketDetails:    datamodel.BucketDetailsArray{},
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.True(t, result.SourceVolume.Set)
		assert.Contains(t, result.SourceVolume.Value, volumeName)
	})

	t.Run("OntapBackup_EmptyVolumeName_PathUnchanged", func(t *testing.T) {
		sourceRegionName := "us-east1"

		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "backup-uuid",
				CreatedAt: time.Now(),
			},
			Name:       "test-backup",
			VolumeUUID: "vol-uuid",
			State:      datamodel.LifeCycleStateAvailable,
			Type:       "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier:  "test-account",
				VolumeName:         "", // empty — rewrite must be skipped
				SourceVolumeZone:   "us-east1-a",
				IsExpertModeBackup: true,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{UUID: "vault-uuid"},
				SourceRegionName: &sourceRegionName,
				BucketDetails:    datamodel.BucketDetailsArray{},
			},
		}

		result := convertBackupDataModelToInternalBackupsV1beta(backup, false)

		assert.True(t, result.SourceVolume.Set)
		assert.Contains(t, result.SourceVolume.Value, "projects/test-account/locations/us-east1-a/volumes/")
	})
}
