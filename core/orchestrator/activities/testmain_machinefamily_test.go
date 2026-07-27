package activities_test

import (
	"os"
	"testing"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
)

// TestMain disables MACHINE_FAMILY_TYPE filtering for the activities unit tests.
// Several tests run the real VMRS decision maker against fixtures that include
// cross-family rows (e.g. testdata/valid_vmrs_gcp.yaml) and were written
// assuming no family scoping, so they must not be constrained by the
// production default family (c3-lssd). MachineFamilyUnset is treated as
// "no filter" by FilterVMsByMachineFamily. Tests that specifically exercise a
// family set vmrs.MachineFamily locally and restore it.
func TestMain(m *testing.M) {
	vmrs.MachineFamily = vmrs.MachineFamilyUnset
	os.Exit(m.Run())
}
