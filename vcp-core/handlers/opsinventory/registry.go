package opsinventory

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
)

// NewReaders returns one reader per v1 resource type. Readers are created once
// when Core starts; there are no package globals.
func NewReaders(store readstore.ReadStore, describer WorkflowDescriber) []Reader {
	return []Reader{
		NewPoolReader(store),
		NewVolumeReader(store),
		NewSnapshotReader(store),
		NewReplicationReader(store),
		NewBackupReader(store),
		NewBackupVaultReader(store),
		NewBackupPolicyReader(store),
		NewCmekReader(store),
		NewActiveDirectoryReader(store),
		NewJobReader(store, describer),
	}
}
