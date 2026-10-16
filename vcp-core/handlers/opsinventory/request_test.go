package opsinventory

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResourceTypeValues pins the API values so a spec change cannot silently
// rename a resource type or filter key.
func TestResourceTypeValues(t *testing.T) {
	for want, got := range map[string]ResourceType{
		"pool":            ResourceTypePool,
		"volume":          ResourceTypeVolume,
		"snapshot":        ResourceTypeSnapshot,
		"replication":     ResourceTypeReplication,
		"backup":          ResourceTypeBackup,
		"backupVault":     ResourceTypeBackupVault,
		"backupPolicy":    ResourceTypeBackupPolicy,
		"cmek":            ResourceTypeCmek,
		"activeDirectory": ResourceTypeActiveDirectory,
		"job":             ResourceTypeJob,
	} {
		assert.Equal(t, want, string(got))
	}
	assert.Equal(t, "correlationId", FilterCorrelationID)
	assert.Equal(t, "type", FilterType)
}
