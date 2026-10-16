package opsinventory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func TestNewReaders(t *testing.T) {
	readers := NewReaders(readstore.NewMockReadStore(t), nil)

	t.Run("WhenEveryAPIResourceTypeHasOneReader", func(t *testing.T) {
		require.Len(t, readers, 10)
		seen := map[ResourceType]int{}
		for _, r := range readers {
			seen[r.Spec().Type]++
		}
		for _, rt := range []ResourceType{ResourceTypePool, ResourceTypeVolume, ResourceTypeSnapshot, ResourceTypeReplication,
			ResourceTypeBackup, ResourceTypeBackupVault, ResourceTypeBackupPolicy, ResourceTypeCmek, ResourceTypeActiveDirectory, ResourceTypeJob} {
			assert.Equal(t, 1, seen[rt], rt)
		}
	})

	t.Run("WhenSpecsAreConsistent", func(t *testing.T) {
		for _, r := range readers {
			spec := r.Spec()
			assert.NotEmpty(t, spec.StateColumn, spec.Type)
			assert.NotEmpty(t, spec.States, spec.Type)
			assert.Len(t, spec.TimeFields, 2, spec.Type)
			if len(spec.NameParents) > 0 {
				assert.True(t, spec.GetByName, "%s: name parents need get by name", spec.Type)
			}
			for _, p := range spec.NameParents {
				assert.Contains(t, spec.ListParents, p, "%s: name parents are list parents", spec.Type)
			}
			for _, p := range spec.RequiredListParents {
				assert.Contains(t, spec.ListParents, p, spec.Type)
			}
			for p, ps := range spec.ListParents {
				assert.NotEmpty(t, ps.Column, "%s parent %s", spec.Type, p)
				if ps.ByID || ps.CheckOwner {
					assert.NotEmpty(t, ps.Owner, "%s parent %s needs an owner table", spec.Type, p)
				}
			}
		}
	})

	t.Run("WhenEveryReaderSetsTheDiscriminator", func(t *testing.T) {
		values := map[ResourceType]oasgenserver.OpsResourceV1{
			ResourceTypePool:         poolResource(&poolRow).Value,
			ResourceTypeVolume:       volumeResource(&volumeRow).Value,
			ResourceTypeSnapshot:     snapshotResource(&snapshotRow).Value,
			ResourceTypeReplication:  replicationResource(&replicationRow).Value,
			ResourceTypeBackup:       backupResource(&backupRow).Value,
			ResourceTypeBackupVault:  backupVaultResource(&backupVaultRow).Value,
			ResourceTypeBackupPolicy: backupPolicyResource(&backupPolicyRow).Value,
			ResourceTypeCmek:         cmekResource(&cmekRow).Value,
		}
		for rt, v := range values {
			b, err := v.MarshalJSON()
			require.NoError(t, err)
			assert.Contains(t, string(b), `"resourceType":"`+string(rt)+`"`)
			assert.Equal(t, string(rt), string(v.Type))
		}
	})
}
