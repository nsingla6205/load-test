package metadata

import (
	"fmt"
	"strings"
)

// MeasuredType comment
type MeasuredType string

var CombinedKeyResourceTypeMeasuredTypeMap map[string]CombinedKeyResourceTypeMeasuredType

var validMeasuredTypeValues map[string]struct{}

type CombinedKeyResourceTypeMeasuredType struct {
	ResourceType ResourceType
	MeasuredType MeasuredType
}

func (mt MeasuredType) String() string {
	return string(mt)
}

const (
	UnknownMeasuredType                                  MeasuredType = "UNKNOWN_MEASURED_TYPE"
	PoolAllocatedSize                                    MeasuredType = "POOL_ALLOCATED_SIZE"
	AllocatedUsed                                        MeasuredType = "ALLOCATED_USED"
	PoolTotalThroughputMibps                             MeasuredType = "POOL_TOTAL_THROUGHPUT_MIBPS"
	PoolTotalIops                                        MeasuredType = "POOL_TOTAL_IOPS"
	LogicalSize                                          MeasuredType = "LOGICAL_SIZE"
	SnapshotSize                                         MeasuredType = "SNAPSHOT_SIZE"
	AllocatedSize                                        MeasuredType = "ALLOCATED_SIZE"
	XregionReplicationHealthy                            MeasuredType = "XREGION_REPLICATION_HEALTHY"
	XregionReplicationLagTime                            MeasuredType = "XREGION_REPLICATION_LAG_TIME"
	XregionReplicationLastTransferDuration               MeasuredType = "XREGION_REPLICATION_LAST_TRANSFER_DURATION"
	XregionReplicationLastTransferSize                   MeasuredType = "XREGION_REPLICATION_LAST_TRANSFER_SIZE"
	XregionReplicationRelationshipConcurrentTransferring MeasuredType = "XREGION_REPLICATION_RELATIONSHIP_CONCURRENT_TRANSFERRING"
	XregionReplicationRelationshipProgress               MeasuredType = "XREGION_REPLICATION_RELATIONSHIP_PROGRESS"
	XregionReplicationRelationshipTransferring           MeasuredType = "XREGION_REPLICATION_RELATIONSHIP_TRANSFERRING"
	XregionReplicationReplicationSchedule                MeasuredType = "XREGION_REPLICATION_REPLICATION_SCHEDULE"
	XregionReplicationTotalTransferBytes                 MeasuredType = "XREGION_REPLICATION_TOTAL_TRANSFER_BYTES"
	BackupLogicalSize                                    MeasuredType = "VOLUME_BACKUP_SIZE"
	BackupEnabledVolumeAllocatedSize                     MeasuredType = "BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE"
	TotalLogicalSize                                     MeasuredType = "TOTAL_LOGICAL_SIZE"
	VolumeAllocatedThroughput                            MeasuredType = "VOLUME_ALLOCATED_THROUGHPUT"
	SFRTotalSizeRestoredBytes                            MeasuredType = "SFR_TOTAL_SIZE_RESTORED_BYTES"
	SFRTotalFilesRestoredCount                           MeasuredType = "SFR_TOTAL_FILES_RESTORED_COUNT"
	AverageReadLatency                                   MeasuredType = "AVERAGE_READ_LATENCY"
	AverageWriteLatency                                  MeasuredType = "AVERAGE_WRITE_LATENCY"
	AverageOtherLatency                                  MeasuredType = "AVERAGE_OTHER_LATENCY"
	ReadIo                                               MeasuredType = "READ_IO"
	WriteIo                                              MeasuredType = "WRITE_IO"
	OtherIo                                              MeasuredType = "OTHER_IO"
	CoolTierDataReadSize                                 MeasuredType = "COOL_TIER_DATA_READ_SIZE"
	CoolTierDataWriteSize                                MeasuredType = "COOL_TIER_DATA_WRITE_SIZE"
	CoolTierDataReadSizeRaw                              MeasuredType = "COOL_TIER_DATA_READ_SIZE_RAW"
	CoolTierDataWriteSizeRaw                             MeasuredType = "COOL_TIER_DATA_WRITE_SIZE_RAW"
	PoolHotTierProvisionedSize                           MeasuredType = "POOL_HOT_TIER_PROVISIONED_SIZE"
	PoolCapacityTierLogicalFootprint                     MeasuredType = "POOL_CAPACITY_TIER_LOGICAL_FOOTPRINT"
	CMEKBackupKeyRotationState                           MeasuredType = "CMEK_BACKUP_KEY_ROTATION_STATE"
	CbsCrossRegionVolumeRestoreTransferBytes             MeasuredType = "CBS_CROSS_REGION_VOLUME_RESTORE_TRANSFER_BYTES"
	CbsCrossRegionVolumeBackupTransferBytes              MeasuredType = "CBS_CROSS_REGION_VOLUME_BACKUP_TRANSFER_BYTES"
)

func init() {
	CombinedKeyResourceTypeMeasuredTypeMap = make(map[string]CombinedKeyResourceTypeMeasuredType)
	CombinedKeyResourceTypeMeasuredTypeMap["unknown_measured_type"] = CombinedKeyResourceTypeMeasuredType{
		MeasuredType: UnknownMeasuredType,
	}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_allocated_size"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: PoolAllocatedSize}
	CombinedKeyResourceTypeMeasuredTypeMap["allocated_used"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: AllocatedUsed}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_total_throughput_mibps"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: PoolTotalThroughputMibps}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_total_iops"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: PoolTotalIops}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_allocated_size_regional_ha"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePoolRegionalHA, MeasuredType: PoolAllocatedSize}
	CombinedKeyResourceTypeMeasuredTypeMap["allocated_used_regional_ha"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePoolRegionalHA, MeasuredType: AllocatedUsed}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_total_throughput_mibps_regional_ha"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePoolRegionalHA, MeasuredType: PoolTotalThroughputMibps}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_total_iops_regional_ha"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePoolRegionalHA, MeasuredType: PoolTotalIops}
	CombinedKeyResourceTypeMeasuredTypeMap["volume_space_logical_used"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: LogicalSize}
	CombinedKeyResourceTypeMeasuredTypeMap["volume_snapshot_reserve_used"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: SnapshotSize}
	CombinedKeyResourceTypeMeasuredTypeMap["backup_logical_size"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Backup, MeasuredType: BackupLogicalSize}
	CombinedKeyResourceTypeMeasuredTypeMap["backup_volume_allocated_size"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: BackupEnabledVolumeAllocatedSize}
	CombinedKeyResourceTypeMeasuredTypeMap["backup_volume_allocated_size_regional_ha"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumeRegionalHA, MeasuredType: BackupEnabledVolumeAllocatedSize}
	CombinedKeyResourceTypeMeasuredTypeMap["volume_capacity"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: AllocatedSize}
	CombinedKeyResourceTypeMeasuredTypeMap["snapmirror_total_transfer_bytes"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumeReplicationRelationship, MeasuredType: XregionReplicationTotalTransferBytes}
	CombinedKeyResourceTypeMeasuredTypeMap["throughput_limit"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: VolumeAllocatedThroughput}
	CombinedKeyResourceTypeMeasuredTypeMap["throughput_limit_regional_ha"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumeRegionalHA, MeasuredType: VolumeAllocatedThroughput}
	CombinedKeyResourceTypeMeasuredTypeMap["volume_read_latency"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: AverageReadLatency}
	CombinedKeyResourceTypeMeasuredTypeMap["volume_write_latency"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: AverageWriteLatency}
	CombinedKeyResourceTypeMeasuredTypeMap["volume_other_latency"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: AverageOtherLatency}
	CombinedKeyResourceTypeMeasuredTypeMap["volume_read_data"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: ReadIo}
	CombinedKeyResourceTypeMeasuredTypeMap["volume_write_data"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: WriteIo}
	CombinedKeyResourceTypeMeasuredTypeMap["volume_other_data"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: OtherIo}
	CombinedKeyResourceTypeMeasuredTypeMap["wafl_volume_client_protocol_reads"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: CoolTierDataReadSize}
	CombinedKeyResourceTypeMeasuredTypeMap["wafl_volume_cloud_bin_operation_size"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: CoolTierDataWriteSize}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_client_protocol_reads"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: CoolTierDataReadSize}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_cloud_bin_operation_size"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: CoolTierDataWriteSize}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_client_protocol_reads_raw"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: CoolTierDataReadSizeRaw}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_cloud_bin_operation_size_raw"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: CoolTierDataWriteSizeRaw}
	CombinedKeyResourceTypeMeasuredTypeMap["wafl_volume_client_protocol_reads_raw"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: CoolTierDataReadSizeRaw}
	CombinedKeyResourceTypeMeasuredTypeMap["wafl_volume_cloud_bin_operation_size_raw"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: CoolTierDataWriteSizeRaw}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_capacity_tier_logical_footprint"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: PoolCapacityTierLogicalFootprint}
	CombinedKeyResourceTypeMeasuredTypeMap["pool_hot_tier_provisioned_size"] = CombinedKeyResourceTypeMeasuredType{ResourceType: VolumePool, MeasuredType: PoolHotTierProvisionedSize}
	CombinedKeyResourceTypeMeasuredTypeMap["cmek_backup_rotation_state"] = CombinedKeyResourceTypeMeasuredType{ResourceType: BackupVault, MeasuredType: CMEKBackupKeyRotationState}
	CombinedKeyResourceTypeMeasuredTypeMap["cbs_cross_region_volume_restore_transfer_bytes"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Volume, MeasuredType: CbsCrossRegionVolumeRestoreTransferBytes}
	CombinedKeyResourceTypeMeasuredTypeMap["cbs_cross_region_volume_backup_transfer_bytes"] = CombinedKeyResourceTypeMeasuredType{ResourceType: Backup, MeasuredType: CbsCrossRegionVolumeBackupTransferBytes}

	validMeasuredTypeValues = make(map[string]struct{}, len(allPersistedMeasuredTypeValues()))
	for _, mt := range allPersistedMeasuredTypeValues() {
		validMeasuredTypeValues[string(mt)] = struct{}{}
	}
}

// NewMeasuredType takes a string and converts it to the defined MeasuredType. If the string is not in the map of available measured types, exists is false and the result is nil.
// If the input string is a legal measured type, the result is the measured type for that string and exists is true.
func NewMeasuredType(input string) (MeasuredType, bool) {
	var result MeasuredType
	combined, exists := CombinedKeyResourceTypeMeasuredTypeMap[strings.ToLower(input)]
	if exists {
		return combined.MeasuredType, exists
	}
	return result, exists
}

func allPersistedMeasuredTypeValues() []MeasuredType {
	return []MeasuredType{
		PoolAllocatedSize,
		AllocatedUsed,
		PoolTotalThroughputMibps,
		PoolTotalIops,
		LogicalSize,
		SnapshotSize,
		AllocatedSize,
		XregionReplicationHealthy,
		XregionReplicationLagTime,
		XregionReplicationLastTransferDuration,
		XregionReplicationLastTransferSize,
		XregionReplicationRelationshipConcurrentTransferring,
		XregionReplicationRelationshipProgress,
		XregionReplicationRelationshipTransferring,
		XregionReplicationReplicationSchedule,
		XregionReplicationTotalTransferBytes,
		BackupLogicalSize,
		BackupEnabledVolumeAllocatedSize,
		TotalLogicalSize,
		VolumeAllocatedThroughput,
		SFRTotalSizeRestoredBytes,
		SFRTotalFilesRestoredCount,
		AverageReadLatency,
		AverageWriteLatency,
		AverageOtherLatency,
		ReadIo,
		WriteIo,
		OtherIo,
		CoolTierDataReadSize,
		CoolTierDataWriteSize,
		CoolTierDataReadSizeRaw,
		CoolTierDataWriteSizeRaw,
		PoolHotTierProvisionedSize,
		PoolCapacityTierLogicalFootprint,
		CMEKBackupKeyRotationState,
		CbsCrossRegionVolumeRestoreTransferBytes,
		CbsCrossRegionVolumeBackupTransferBytes,
	}
}

// MeasuredTypeValue returns the persisted string value for a measured type enum.
func MeasuredTypeValue(mt MeasuredType) string {
	return string(mt)
}

// MeasuredTypeFromValue parses a persisted measured type string into the enum type.
func MeasuredTypeFromValue(s string) MeasuredType {
	return MeasuredType(s)
}

// MeasuredTypeEquals reports whether a persisted string equals the enum value.
func MeasuredTypeEquals(s string, mt MeasuredType) bool {
	return s == string(mt)
}

// IsValidMeasuredTypeValue reports whether s is a known persisted measured type value.
func IsValidMeasuredTypeValue(s string) bool {
	_, ok := validMeasuredTypeValues[s]
	return ok
}

// ValidateMeasuredTypeValue returns an error when s is not a known persisted measured type.
func ValidateMeasuredTypeValue(s string) error {
	if !IsValidMeasuredTypeValue(s) {
		return fmt.Errorf("invalid measured_type %q", s)
	}
	return nil
}
