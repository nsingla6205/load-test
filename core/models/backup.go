package models

import "time"

// VolumeBackupConfig represents the backup configuration for an expert mode volume
type ExpertModeVolumeBackupConfig struct {
	VolumeResourceID       string  // Volume name
	BackupVaultPath        *string // e.g. projects/{project}/locations/{location}/backupVaults/{id}
	BackupPolicyPath       *string // e.g. projects/{project}/locations/{location}/backupPolicies/{id}
	ScheduledBackupEnabled *bool   // Whether scheduled backup is enabled for this volume
	BackupChainBytes       *int64  // Size of the backup chain in bytes
}

// Backup describes a backup in the cloud volumes model
type Backup struct {
	OwnerID                          string
	BackupID                         string
	VolumeID                         string
	UseExistingSnapshot              bool
	Region                           string
	Name                             string
	Tag                              string
	Type                             string
	LifeCycleState                   string
	LifeCycleStateDetails            string
	LifeCycleTrackingID              int32
	SizeInBytes                      uint64
	CreationTime                     time.Time
	SnapshotCreationTime             *time.Time
	CompletionTime                   *time.Time
	Jobs                             []*Job
	ProgressPercentage               uint64
	BytesTransferred                 uint64
	EndpointUUID                     string
	StorageClass                     string
	ExternalUUID                     string
	VolumeName                       string
	BackupVaultID                    string
	SnapshotName                     string
	VolumeVendorID                   string
	BackupPolicyName                 string
	Description                      *string
	BackupVaultName                  string
	BackupsLogicalSize               *int64
	AccountName                      *string
	ShouldHydrate                    bool
	ConstituentVolumesPerAggregate   int
	NumberOfAggregates               int
	OntapVolumeStyle                 string
	StorageAccountUUID               string
	SatisfiesPzs                     bool
	SatisfiesPzi                     bool
	BucketName                       string
	IsRemoteBackup                   bool
	IsBackupImmutable                bool
	MinimumEnforcedRetentionDuration *int64
	SourceRegion                     *string
	BackupRegion                     *string
	Protocols                        []string
}

const (
	BackupHydrationModeDefault = "DEFAULT"
	BackupHydrationModeONTAP   = "ONTAP"
)

type SourceVolumeDetails struct {
	VolumeProtocols []string `json:"volumeProtocols,omitempty"`
}

type HydrateBackup struct {
	ResourceId            string                 `json:"name"`
	BackupId              string                 `json:"netapp_uuid"`
	VolumeUsageBytes      *uint64                `json:"volume_usage_bytes"`
	AssetLocationMetadata *AssetLocationMetadata `json:"asset_location_metadata"`
	SourceVolume          string                 `json:"source_volume"`
	SourceVolumeDetails   *SourceVolumeDetails   `json:"sourceVolumeDetails,omitempty"`
	SourceStoragePool     string                 `json:"sourceStoragePool,omitempty"`
	Mode                  string                 `json:"mode,omitempty"`
}

type AssetLocationMetadata struct {
	ChildAssets []*ChildAsset `json:"child_assets"`
}

type ChildAsset struct {
	AssetType  string   `json:"asset_type"`
	AssetNames []string `json:"asset_names"`
}

type HydrateBackupVault struct {
	ResourceId            string  `json:"name"`
	BackupVaultId         string  `json:"netapp_uuid"`
	BackupVaultType       string  `json:"backup_vault_type"`
	BackupRegion          string  `json:"backup_region"`
	BackupsKmsKey         *string `json:"backups_crypto_key_version"`
	KmsConfigResourcePath *string `json:"kms_config_id"`
}

// BackupVaultCrossRegionNetappUuidRequest is the PATCH body sent to CCFE
// to set the cross_region_netapp_uuid on a destination backup vault
// for cross-project cross-region backup.
type BackupVaultCrossRegionNetappUuidRequest struct {
	CrossRegionNetappUuid string `json:"cross_region_netapp_uuid"`
}
