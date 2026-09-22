package common

import "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"

type Triple struct {
	Left   string
	Middle string
	Right  string
}

func CreateMetricsMappingMap() map[metadata.CombinedKeyResourceTypeMeasuredType]Triple {
	metricsMappingMap := map[metadata.CombinedKeyResourceTypeMeasuredType]Triple{
		{ResourceType: metadata.VolumePool, MeasuredType: metadata.PoolAllocatedSize}: {
			Left: "capacity", Middle: "", Right: "",
		},
		{ResourceType: metadata.VolumePool, MeasuredType: metadata.AllocatedUsed}: {
			Left: "allocated", Middle: "", Right: "",
		},
		{ResourceType: metadata.VolumePool, MeasuredType: metadata.PoolTotalThroughputMibps}: {
			Left: "throughput_limit", Middle: "type", Right: "allocated",
		},
		{ResourceType: metadata.VolumePool, MeasuredType: metadata.PoolTotalIops}: {
			Left: "iops_limit", Middle: "type", Right: "allocated",
		},
		{ResourceType: metadata.VolumePool, MeasuredType: metadata.PoolUsedThroughputMibps}: {
			Left: "throughput_limit", Middle: "type", Right: "used",
		},
		{ResourceType: metadata.VolumePool, MeasuredType: metadata.PoolUsedIops}: {
			Left: "iops_limit", Middle: "type", Right: "used",
		},
		{ResourceType: metadata.VolumePoolRegionalHA, MeasuredType: metadata.PoolAllocatedSize}: {
			Left: "capacity", Middle: "", Right: "",
		},
		{ResourceType: metadata.VolumePoolRegionalHA, MeasuredType: metadata.AllocatedUsed}: {
			Left: "allocated", Middle: "", Right: "",
		},
		{ResourceType: metadata.VolumePoolRegionalHA, MeasuredType: metadata.PoolTotalThroughputMibps}: {
			Left: "throughput_limit", Middle: "type", Right: "allocated",
		},
		{ResourceType: metadata.VolumePoolRegionalHA, MeasuredType: metadata.PoolTotalIops}: {
			Left: "iops_limit", Middle: "type", Right: "allocated",
		},
		{ResourceType: metadata.VolumePoolRegionalHA, MeasuredType: metadata.PoolUsedThroughputMibps}: {
			Left: "throughput_limit", Middle: "type", Right: "used",
		},
		{ResourceType: metadata.VolumePoolRegionalHA, MeasuredType: metadata.PoolUsedIops}: {
			Left: "iops_limit", Middle: "type", Right: "used",
		},
		{ResourceType: metadata.Backup, MeasuredType: metadata.BackupLogicalSize}: {
			Left: "backup_used", Middle: "", Right: "",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.VolumeAllocatedThroughput}: {
			Left: "throughput_limit", Middle: "", Right: "",
		},
		{ResourceType: metadata.VolumeRegionalHA, MeasuredType: metadata.VolumeAllocatedThroughput}: {
			Left: "throughput_limit", Middle: "", Right: "",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.SFRTotalSizeRestoredBytes}: {
			Left: "files_restored_bytes", Middle: "", Right: "",
		},
		{ResourceType: metadata.VolumeRegionalHA, MeasuredType: metadata.SFRTotalSizeRestoredBytes}: {
			Left: "files_restored_bytes", Middle: "", Right: "",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.SFRTotalFilesRestoredCount}: {
			Left: "files_restored_count", Middle: "", Right: "",
		},
		{ResourceType: metadata.VolumeRegionalHA, MeasuredType: metadata.SFRTotalFilesRestoredCount}: {
			Left: "files_restored_count", Middle: "", Right: "",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.AverageReadLatency}: {
			Left: "average_latency", Middle: "method", Right: "read",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.AverageWriteLatency}: {
			Left: "average_latency", Middle: "method", Right: "write",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.AverageOtherLatency}: {
			Left: "average_latency", Middle: "method", Right: "metadata",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.ReadIo}: {
			Left: "throughput", Middle: "type", Right: "read",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.WriteIo}: {
			Left: "throughput", Middle: "type", Right: "write",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.OtherIo}: {
			Left: "throughput", Middle: "type", Right: "metadata",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.CoolTierDataReadSize}: {
			Left: "auto_tiering/cold_tier_read_byte_count", Middle: "", Right: "",
		},
		{ResourceType: metadata.Volume, MeasuredType: metadata.CoolTierDataWriteSize}: {
			Left: "auto_tiering/cold_tier_write_byte_count", Middle: "", Right: "",
		},
		{ResourceType: metadata.VolumePool, MeasuredType: metadata.CoolTierDataReadSize}: {
			Left: "auto_tiering/cold_tier_read_byte_count", Middle: "", Right: "",
		},
		{ResourceType: metadata.VolumePool, MeasuredType: metadata.CoolTierDataWriteSize}: {
			Left: "auto_tiering/cold_tier_write_byte_count", Middle: "", Right: "",
		},
		{ResourceType: metadata.BackupVault, MeasuredType: metadata.CMEKBackupKeyRotationState}: {
			Left: "cmek_backup_rotation_state", Middle: "", Right: "",
		},
	}
	return metricsMappingMap
}
