// This file defines the MachineFamilyType selector used to scope VMRS VM
// catalog rows to a specific GCP machine family in a region.
//
// Background: a region's PE deployment decides which family of GCP machines
// (C3 / C4, with or without local SSD) to provision into. The selection is
// communicated to the worker via the MACHINE_FAMILY_TYPE config variable,
// which maps onto one of the constants in this file. See the C4 / C4-LSSD
// support spec for the precedence matrix that PE uses.

package vmrs

import (
	"log/slog"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// MachineFamilyType is the typed value of the MACHINE_FAMILY_TYPE config
// variable. It scopes VMRS catalog selection so cross-family rows (e.g. a
// c3-* row in a c4-* region) are filtered out before the cost-based search
// runs. Empty (MachineFamilyUnset) preserves legacy "no filter" behavior so
// regions that have not yet been migrated keep working exactly as before.
type MachineFamilyType string

// MachineFamily is the process-wide MACHINE_FAMILY_TYPE selector, read once at
// startup. It is the single source of truth shared by the VMRS decision layer
// (which filters catalog rows to this family) and the deployment/activities
// layer (which sets the non-LSSD VLM flag), so the two cannot drift. PE sets
// MACHINE_FAMILY_TYPE per region per the C4 deployment matrix. The value is
// normalized via resolveMachineFamily so it is never empty or invalid at
// runtime: an unset/empty/unrecognized config falls back to
// DefaultMachineFamily (c3-lssd).
var MachineFamily = resolveMachineFamily(env.GetString("MACHINE_FAMILY_TYPE", string(DefaultMachineFamily)))

const (
	// MachineFamilyUnset is the zero value. It is NOT a valid runtime value;
	// resolveMachineFamily maps it to DefaultMachineFamily. It is retained as
	// a sentinel that FilterVMsByMachineFamily treats as "no filter" (used by
	// unit tests that exercise the raw catalog).
	MachineFamilyUnset MachineFamilyType = ""

	// MachineFamilyC3LSSD restricts VMRS selection to c3-* qualified VMs that
	// carry the -lssd suffix. This is the original GA configuration and the
	// default family (see DefaultMachineFamily).
	MachineFamilyC3LSSD MachineFamilyType = "c3-lssd"

	// MachineFamilyC3NonLSSD restricts VMRS selection to the c3-* qualified
	// VMs that do NOT carry the -lssd suffix. The catalog has native non-LSSD
	// c3-* rows, so the chosen VM type is sent to VLM as-is.
	MachineFamilyC3NonLSSD MachineFamilyType = "c3-non-lssd"

	// MachineFamilyC4LSSD restricts VMRS selection to c4-* qualified VMs that
	// carry the -lssd suffix.
	MachineFamilyC4LSSD MachineFamilyType = "c4-lssd"

	// MachineFamilyC4NonLSSD restricts VMRS selection to c4-* qualified VMs
	// that do NOT carry the -lssd suffix. Catalog rows are natively named
	// without -lssd so no suffix stripping is performed.
	MachineFamilyC4NonLSSD MachineFamilyType = "c4-non-lssd"

	// DefaultMachineFamily is used when MACHINE_FAMILY_TYPE is unset, empty, or
	// set to an unrecognized value. Production always runs with an explicit
	// family; this default guarantees MachineFamily is never empty and never
	// silently falls back to the unfiltered legacy behavior at runtime.
	DefaultMachineFamily MachineFamilyType = MachineFamilyC3LSSD
)

// IsValid reports whether f is one of the supported (non-empty) machine
// families that may be configured via MACHINE_FAMILY_TYPE.
func (f MachineFamilyType) IsValid() bool {
	switch f {
	case MachineFamilyC3LSSD, MachineFamilyC3NonLSSD, MachineFamilyC4LSSD, MachineFamilyC4NonLSSD:
		return true
	default:
		return false
	}
}

// resolveMachineFamily normalizes a raw MACHINE_FAMILY_TYPE value (trimming
// surrounding whitespace and lower-casing so casing like "C4-LSSD" is accepted),
// returning DefaultMachineFamily when raw is empty or not a recognized family so
// the effective selector is never empty/invalid in production.
//
// A non-empty but unrecognized value is almost always an operator
// misconfiguration (e.g. a typo like "c4-lssdd"), so a warning is logged to make
// the silent fallback to DefaultMachineFamily noticeable. An empty/unset value is
// the legitimate default path and stays quiet to avoid startup log noise in
// regions that have not set the variable.
func resolveMachineFamily(raw string) MachineFamilyType {
	trimmed := strings.TrimSpace(raw)
	f := MachineFamilyType(strings.ToLower(trimmed))
	if f.IsValid() {
		return f
	}
	if trimmed != "" {
		slog.Warn("unrecognized MACHINE_FAMILY_TYPE; falling back to default machine family",
			"configured", trimmed,
			"fallback", string(DefaultMachineFamily),
		)
	}
	return DefaultMachineFamily
}

// MachineFamilyForVMType infers the MachineFamilyType that a concrete instance
// type string belongs to (e.g. "c3-standard-8-lssd" -> MachineFamilyC3LSSD,
// "c4-standard-4" -> MachineFamilyC4NonLSSD). It is the inverse of the prefix +
// -lssd-suffix convention used by FilterVMsByMachineFamily.
//
// This is used to keep an EXISTING pool on its original machine family during
// updates: the global MACHINE_FAMILY_TYPE (MachineFamily) only governs which
// family NEW pools are provisioned into, so when a region is re-pointed (e.g.
// c3-lssd -> c4-lssd) a pool created as c3-lssd must continue to resolve c3-lssd
// VMs instead of being migrated across families. Returns MachineFamilyUnset when
// vmType is empty or does not carry a recognized family prefix.
func MachineFamilyForVMType(vmType string) MachineFamilyType {
	vmType = strings.TrimSpace(vmType)
	if vmType == "" {
		return MachineFamilyUnset
	}
	hasLssd := strings.HasSuffix(vmType, LssdVMTypeSuffix)
	switch {
	case strings.HasPrefix(vmType, "c3-"):
		if hasLssd {
			return MachineFamilyC3LSSD
		}
		return MachineFamilyC3NonLSSD
	case strings.HasPrefix(vmType, "c4-"):
		if hasLssd {
			return MachineFamilyC4LSSD
		}
		return MachineFamilyC4NonLSSD
	default:
		return MachineFamilyUnset
	}
}

// IsNonLssd returns true when the family is one of the non-LSSD variants.
// The deployment layer uses this to set EnableNonLssdInstanceType on the VLM
// config so VLM provisions non-LSSD machines.
func (f MachineFamilyType) IsNonLssd() bool {
	return f == MachineFamilyC3NonLSSD || f == MachineFamilyC4NonLSSD
}

// vmFamilyPrefix returns the "<family>-" prefix VMRS catalog rows must carry
// to belong to this machine family, or "" if the family does not constrain
// the prefix (i.e. MachineFamilyUnset or any unknown value).
func (f MachineFamilyType) vmFamilyPrefix() string {
	switch f {
	case MachineFamilyC3LSSD, MachineFamilyC3NonLSSD:
		return "c3-"
	case MachineFamilyC4LSSD, MachineFamilyC4NonLSSD:
		return "c4-"
	default:
		return ""
	}
}

// requiresLssdSuffixOnRow reports whether qualified_vms rows for this family
// must carry the -lssd suffix. Only the LSSD families do; the non-LSSD
// families select native bare-named catalog rows that are sent to VLM as-is.
func (f MachineFamilyType) requiresLssdSuffixOnRow() bool {
	switch f {
	case MachineFamilyC3LSSD, MachineFamilyC4LSSD:
		return true
	default:
		return false
	}
}

// FilterVMsByMachineFamily returns the subset of vms that match family. When
// family is MachineFamilyUnset (or unrecognised) the input slice is returned
// unchanged so legacy callers keep their behavior (and the returned slice may
// alias the input). Filtering is done by (a) the canonical "<family>-" prefix
// on VMType and (b) presence/absence of the -lssd suffix per the family.
func FilterVMsByMachineFamily(vms []VMPerfLimit, family MachineFamilyType) []VMPerfLimit {
	if family == MachineFamilyUnset {
		return vms
	}
	prefix := family.vmFamilyPrefix()
	if prefix == "" {
		return vms
	}
	wantLssd := family.requiresLssdSuffixOnRow()
	filtered := make([]VMPerfLimit, 0, len(vms))
	for _, vm := range vms {
		if !strings.HasPrefix(vm.VMType, prefix) {
			continue
		}
		hasLssd := strings.HasSuffix(vm.VMType, LssdVMTypeSuffix)
		if hasLssd != wantLssd {
			continue
		}
		filtered = append(filtered, vm)
	}
	return filtered
}
