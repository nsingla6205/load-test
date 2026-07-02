package metadata

import "fmt"

// ResourceType represents the type of resource.
type ResourceType string

var validResourceTypeValues map[string]struct{}

func init() {
	validResourceTypeValues = make(map[string]struct{}, len(allPersistedResourceTypeValues()))
	for _, rt := range allPersistedResourceTypeValues() {
		validResourceTypeValues[string(rt)] = struct{}{}
	}
}

func allPersistedResourceTypeValues() []ResourceType {
	return []ResourceType{
		Volume,
		VolumePool,
		VolumeRegionalHA,
		VolumePoolRegionalHA,
		VolumeReplicationRelationship,
		Backup,
		BackupVault,
		CBS,
	}
}

// String converts the ResourceType to a string.
func (rt ResourceType) String() string {
	return string(rt)
}

// ResourceType constants
const (
	Volume                                 ResourceType = "VOLUME"
	VolumePool                             ResourceType = "VOLUME_POOL"
	VolumeRegionalHA                       ResourceType = "VOLUME_REGIONAL_HA"
	VolumePoolRegionalHA                   ResourceType = "VOLUME_POOL_REGIONAL_HA"
	VolumeReplicationRelationship          ResourceType = "VOLUME_REPLICATION_RELATIONSHIP"
	Backup                                 ResourceType = "BACKUP"
	BackupVault                            ResourceType = "BACKUP_VAULT"
	CBS                                    ResourceType = "CBS"
	MetricsNamePrefixPoolFirstParty                     = "netapp.googleapis.com/storage_pool/"
	MetricsNamePrefixVolumeFirstParty                   = "netapp.googleapis.com/volume/"
	MetricsNamePrefixBackupVaultFirstParty              = "netapp.googleapis.com/backup_vault/"
)

// ResourceTypeValue returns the persisted string value for a resource type enum.
func ResourceTypeValue(rt ResourceType) string {
	return string(rt)
}

// ResourceTypeFromValue parses a persisted resource type string into the enum type.
func ResourceTypeFromValue(s string) ResourceType {
	return ResourceType(s)
}

// ResourceTypeEquals reports whether a persisted string equals the enum value.
func ResourceTypeEquals(s string, rt ResourceType) bool {
	return s == string(rt)
}

// IsValidResourceTypeValue reports whether s is a known persisted resource type value.
func IsValidResourceTypeValue(s string) bool {
	_, ok := validResourceTypeValues[s]
	return ok
}

// ValidateResourceTypeValue returns an error when s is not a known persisted resource type.
func ValidateResourceTypeValue(s string) error {
	if !IsValidResourceTypeValue(s) {
		return fmt.Errorf("invalid resource_type %q", s)
	}
	return nil
}
