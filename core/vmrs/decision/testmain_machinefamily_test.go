package decision

import (
	"os"
	"testing"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
)

// TestMain disables MACHINE_FAMILY_TYPE filtering for the decision unit tests.
// These tests load self-contained catalogs (including c4-only fixtures) and
// assert against the raw config, so they must not be scoped to the production
// default family (c3-lssd). MachineFamilyUnset is treated as "no filter" by
// FilterVMsByMachineFamily. Tests that specifically exercise family filtering
// should set vmrs.MachineFamily locally and restore it.
func TestMain(m *testing.M) {
	vmrs.MachineFamily = vmrs.MachineFamilyUnset
	os.Exit(m.Run())
}
