package cli

import (
	"context"
	"fmt"
	"strings"

	proxyutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
)

// CLICondition is a function that validates a CLI command.
// Returns (true, "") if valid, or (false, "reason") if invalid.
// Used for local validation (argument checking) before credential setup.
type CLICondition func(cmd *CLICommand) (bool, string)

// CLIExternalValidator is a function that validates a CLI command with context.
// Returns (true, "") if valid, or (false, "reason") if invalid.
// Used for external API calls (e.g., core API validation) after credential setup.
// Similar to validateVolumeCreation in rules_v2/validators.go.
type CLIExternalValidator func(ctx context.Context, cmd *CLICommand) (bool, string)

// CLIRule defines access control and validation for CLI commands.
type CLIRule struct {
	// Pattern is the command pattern to match (supports wildcards with *)
	// Examples: "volume show", "volume *", "storage aggregate *"
	Pattern string

	// Allow indicates whether the command is allowed (true) or denied (false)
	Allow bool

	// Reason is the message returned when a command is denied
	Reason string

	// Condition is an optional validation function (use CLIAnd, CLIHasArgs, CLIIfPresentThenValue, etc.)
	// Similar to the DSL conditions in rules_v2/rule_map.go
	// Evaluated BEFORE credential setup - for local argument validation only.
	Condition CLICondition

	// ExternalValidator is an optional function for external API validation.
	// Evaluated AFTER credential setup - has access to auth data in context.
	// Similar to validateVolumeCreation in rules_v2/validators.go.
	ExternalValidator CLIExternalValidator

	// InjectArguments lists arguments to inject into the command before execution.
	// Arguments are added only if not already present in the command.
	// Example: {"-is-space-enforcement-logical": "true", "-is-space-reporting-logical": "true"}
	InjectArguments map[string]string

	// RemoveFields lists fields to remove from the CLI response
	// RemoveFields lists fields to remove from the CLI response (deny-list, partial match).
	RemoveFields []string

	// KeepFields lists the only fields to retain in the CLI response (allow-list, exact
	// match); everything else is dropped. Use this where the safe default is to hide all
	// fields except an explicit set. Mutually exclusive with RemoveFields in practice.
	KeepFields []string
}

// volumeShowRemoveFields lists fields removed from volume show output.
// Field matching is case-insensitive and partial (e.g. "Physical Used" matches
// "Total Physical Used Size" and "Physical Used Percentage").
var volumeShowRemoveFields = []string{
	"Used Size",
	"Used Percentage",
	"Physical Used",
	"Physical Used Percent",
	"Footprint",
	"Total Metadata",
	"Space Guarantee",
	"Space SLO",
	"Storage Efficiency",
	"Deduplication",
	"Compression",
	"Sis Space Saved",
	"Dedupe Space Saved",
	"Dedupe Space Shared",
	"Compression Space Saved",
	"Efficiency",
	"Performance Tier Inactive User Data",
	"Volume Size Used by Snapshot Copies",
	"Over Provisioned Size",
}

// volumeShowFootprintKeepFields is the allow-list for "volume show-footprint": only the
// tiering footprint rows and the owning Vserver/Volume identifiers are exposed; every other
// row (aggregate name/UUID, hostname, dedupe/metadata/efficiency footprints, data-reduction
// savings, percentages, bin names) is dropped. These map to the REST volume space.* fields:
//   - "Total Footprint"            -> space.total_footprint
//   - "Volume Footprint for bin0"  -> space.performance_tier_footprint / space.local_tier_footprint
//   - "Volume Footprint for bin1"  -> space.capacity_tier_footprint
//
// The detailed (-instance) form is what surfaces these rows. Matching is exact (see
// KeepFieldsInCLIOutput), so percentage and data-reduction variants are not exposed; entries
// that a given ONTAP version does not emit are simply no-ops.
var volumeShowFootprintKeepFields = []string{
	"Vserver",
	"Volume Name",
	"Total Footprint",
	"Volume Footprint for bin0",
	"Volume Footprint for bin1",
}

// advancedAllowedRules is the allowlist of commands permitted in advanced mode.
var advancedAllowedRules = []CLIRule{
	{
		Pattern: "statistics show",
		Allow:   true,
	},
	{
		Pattern:      "volume show",
		Allow:        true,
		RemoveFields: volumeShowRemoveFields,
	},
	{
		Pattern:      "vol show",
		Allow:        true,
		RemoveFields: volumeShowRemoveFields,
	},
}

// diagAllowedRules is the allowlist of commands permitted in diagnostic mode.
var diagAllowedRules = []CLIRule{
	// volumes
	{
		Pattern:      "volume show",
		Allow:        true,
		RemoveFields: volumeShowRemoveFields,
	},
	{
		Pattern:      "vol show",
		Allow:        true,
		RemoveFields: volumeShowRemoveFields,
	},
	// Debug
	{
		Pattern: "debug dm vserver xc",
		Allow:   true,
	},
	{
		Pattern: "debug locks persistence show",
		Allow:   true,
	},
	{
		Pattern: "debug locks persistence reconstruction show",
		Allow:   true,
	},
	{
		Pattern: "debug locks persistence reconstruction show-volume",
		Allow:   true,
	},
	{
		Pattern: "debug network tcpdump",
		Allow:   true,
	},
	{
		Pattern: "debug san lun",
		Allow:   true,
	},

	// Volume
	{
		Pattern: "volume snapshot show",
		Allow:   true,
	},

	{
		Pattern: "vol snapshot show",
		Allow:   true,
	},

	// Vserver
	{
		Pattern: "vserver nfs client",
		Allow:   true,
	},
}

// cliRules is the ordered list of CLI rules. First match wins.
// Uses DSL-like conditions similar to rules_v2/rule_map.go for consistency.
var cliRules = []CLIRule{
	// Storage Volumes - corresponds to /api/storage/volumes in rule_map.go
	// Supports both "volume" and shorthand "vol"
	{
		Pattern:      "volume show",
		Allow:        true,
		RemoveFields: volumeShowRemoveFields,
	},
	{
		Pattern:      "vol show",
		Allow:        true,
		RemoveFields: volumeShowRemoveFields,
	},
	{
		Pattern:    "volume show-footprint",
		Allow:      true,
		KeepFields: volumeShowFootprintKeepFields,
	},
	{
		Pattern:    "vol show-footprint",
		Allow:      true,
		KeepFields: volumeShowFootprintKeepFields,
	},
	{
		Pattern: "volume create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-volume", "-size"),
			CLIIfPresentThenValue("-autosize-mode", "off"),
			CLIRejectArgsOrFlags(
				"-max-autosize",
				"-min-autosize",
				"-autosize-grow-threshold-percent",
				"-autosize-shrink-threshold-percent",
				"-autosize-reset",
			),
			CLIIfPresentThenValue("-space-guarantee", "none"),
			// Enforce logical space settings - user cannot override
			CLIIfPresentThenEquals("-is-space-enforcement-logical", "true"),
			CLIIfPresentThenEquals("-is-space-reporting-logical", "true"),
		),
		ExternalValidator: validateVolumeCreate,
		InjectArguments: map[string]string{
			"-is-space-enforcement-logical": "true",
			"-is-space-reporting-logical":   "true",
		},
	},
	{
		Pattern: "vol create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-volume", "-size"),
			CLIIfPresentThenValue("-autosize-mode", "off"),
			CLIRejectArgsOrFlags(
				"-max-autosize",
				"-min-autosize",
				"-autosize-grow-threshold-percent",
				"-autosize-shrink-threshold-percent",
				"-autosize-reset",
			),
			CLIIfPresentThenValue("-space-guarantee", "none"),
			CLIIfPresentThenEquals("-is-space-enforcement-logical", "true"),
			CLIIfPresentThenEquals("-is-space-reporting-logical", "true"),
		),
		ExternalValidator: validateVolumeCreate,
		InjectArguments: map[string]string{
			"-is-space-enforcement-logical": "true",
			"-is-space-reporting-logical":   "true",
		},
	},
	{
		Pattern: "volume clone create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-flexclone"),
			CLIOr(
				CLIHasArgs("-parent-volume"),
				CLIHasArgs("-b"),
			),
			CLIIfPresentThenValue("-space-guarantee", "none"),
		),
		ExternalValidator: validateVolumeCloneCreate,
	},
	{
		Pattern: "vol clone create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-flexclone"),
			CLIOr(
				CLIHasArgs("-parent-volume"),
				CLIHasArgs("-b"),
			),
			CLIIfPresentThenValue("-space-guarantee", "none"),
		),
		ExternalValidator: validateVolumeCloneCreate,
	},
	{
		Pattern:           "volume clone split start",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-flexclone"),
		ExternalValidator: validateVolumeCloneSplitStart,
	},
	{
		Pattern:           "vol clone split start",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-flexclone"),
		ExternalValidator: validateVolumeCloneSplitStart,
	},
	{
		Pattern: "volume modify",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-volume"),
			CLIRejectArgs("-space-guarantee"),
			CLIIfPresentThenValue("-autosize-mode", "off"),
			CLIRejectArgsOrFlags(
				"-max-autosize",
				"-min-autosize",
				"-autosize-grow-threshold-percent",
				"-autosize-shrink-threshold-percent",
				"-autosize-reset",
			),
			CLIIfPresentThenEquals("-is-space-enforcement-logical", "true"),
			CLIIfPresentThenEquals("-is-space-reporting-logical", "true"),
		),
		ExternalValidator: validateVolumeUpdate,
	},
	{
		Pattern: "vol modify",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-volume"),
			CLIRejectArgs("-space-guarantee"),
			CLIIfPresentThenValue("-autosize-mode", "off"),
			CLIRejectArgsOrFlags(
				"-max-autosize",
				"-min-autosize",
				"-autosize-grow-threshold-percent",
				"-autosize-shrink-threshold-percent",
				"-autosize-reset",
			),
			CLIIfPresentThenEquals("-is-space-enforcement-logical", "true"),
			CLIIfPresentThenEquals("-is-space-reporting-logical", "true"),
		),
		ExternalValidator: validateVolumeUpdate,
	},
	{
		Pattern:           "volume delete",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume"),
		ExternalValidator: validateVolumeDelete,
	},
	{
		Pattern:           "vol delete",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume"),
		ExternalValidator: validateVolumeDelete,
	},
	// "volume destroy" / "vol destroy" — same as delete; triggers reconciliation on volume delete
	{
		Pattern:           "volume destroy",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume"),
		ExternalValidator: validateVolumeDelete,
	},
	{
		Pattern:           "vol destroy",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume"),
		ExternalValidator: validateVolumeDelete,
	},
	{
		Pattern:           "volume rename",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume", "-newname"),
		ExternalValidator: validateVolumeRename,
	},
	{
		Pattern:           "vol rename",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume", "-newname"),
		ExternalValidator: validateVolumeRename,
	},
	{
		Pattern:           "volume size",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume", "-new-size"),
		ExternalValidator: validateVolumeUpdate,
	},
	{
		Pattern:           "vol size",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume", "-new-size"),
		ExternalValidator: validateVolumeUpdate,
	},

	// Volume Autosize - blocked; autosize changes bypass capacity tracking.
	{
		Pattern: "volume autosize",
		Allow:   false,
	},
	{
		Pattern: "vol autosize",
		Allow:   false,
	},

	// FPolicy persistent-store creation creates a FlexVol. Keep autosize disabled so its
	// logical capacity commitment remains deterministic.
	{
		Pattern:   "vserver fpolicy persistent-store create",
		Allow:     true,
		Condition: CLIIfPresentThenValue("-autosize-mode", "off"),
	},

	// FlexCache Volumes - corresponds to /api/storage/flexcache/flexcaches in rule_map.go
	// Supports "volume flexcache", "vol flexcache", and bare "flexcache" forms
	{
		Pattern: "volume flexcache show",
		Allow:   true,
	},
	{
		Pattern: "vol flexcache show",
		Allow:   true,
	},
	{
		Pattern: "flexcache show",
		Allow:   true,
	},
	{
		Pattern: "volume flexcache create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-volume", "-origin-volume", "-size"),
			CLIIfPresentThenValue("-space-guarantee", "none"),
			CLIIfPresentThenEquals("-is-relative-size-enabled", "false"),
		),
		ExternalValidator: validateFlexCacheCreate,
	},
	{
		Pattern: "vol flexcache create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-volume", "-origin-volume", "-size"),
			CLIIfPresentThenValue("-space-guarantee", "none"),
			CLIIfPresentThenEquals("-is-relative-size-enabled", "false"),
		),
		ExternalValidator: validateFlexCacheCreate,
	},
	{
		Pattern: "flexcache create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-volume", "-origin-volume", "-size"),
			CLIIfPresentThenValue("-space-guarantee", "none"),
			CLIIfPresentThenEquals("-is-relative-size-enabled", "false"),
		),
		ExternalValidator: validateFlexCacheCreate,
	},
	{
		Pattern:           "volume flexcache delete",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume"),
		ExternalValidator: validateFlexCacheDelete,
	},
	{
		Pattern:           "vol flexcache delete",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume"),
		ExternalValidator: validateFlexCacheDelete,
	},
	{
		Pattern:           "flexcache delete",
		Allow:             true,
		Condition:         CLIHasArgs("-vserver", "-volume"),
		ExternalValidator: validateFlexCacheDelete,
	},

	// Security Certificates - corresponds to /api/security/certificates in rule_map.go
	// Supports both "security" and shorthand "sec"
	{
		Pattern: "security certificate show",
		Allow:   true,
	},
	{
		Pattern: "sec certificate show",
		Allow:   true,
	},
	{
		Pattern:   "security certificate create",
		Allow:     true,
		Condition: CLIIfPresentThenValue("-type", "server", "client"),
	},
	{
		Pattern:   "sec certificate create",
		Allow:     true,
		Condition: CLIIfPresentThenValue("-type", "server", "client"),
	},
	{
		Pattern:   "security certificate install",
		Allow:     true,
		Condition: CLIIfPresentThenValue("-type", "server", "client", "server-ca", "client-ca"),
	},
	{
		Pattern:   "sec certificate install",
		Allow:     true,
		Condition: CLIIfPresentThenValue("-type", "server", "client", "server-ca", "client-ca"),
	},
	{
		Pattern: "security certificate delete",
		Allow:   false,
		Reason:  "Certificate deletion not allowed",
	},
	{
		Pattern: "sec certificate delete",
		Allow:   false,
		Reason:  "Certificate deletion not allowed",
	},

	// Vserver - block create/delete, restrict modify to allowed fields only
	{
		Pattern: "vserver create",
		Allow:   false,
		Reason:  "SVM creation not allowed",
	},
	{
		Pattern: "vserver delete",
		Allow:   false,
		Reason:  "SVM deletion not allowed",
	},
	{
		Pattern: "vserver modify",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver"),
			CLIOnlyAllowArgs("-vserver", "-language", "-snapshot-policy", "-quota-policy"),
		),
	},

	{
		Pattern: "vserver object-store-server bucket create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-vserver", "-bucket", "-nas-path"),
			CLIArgRequiredEquals("-type", "nas"),
		),
	},

	// LUNs - block thick provisioning. -space-reserve must be disabled (non-space-reserved/thin).
	// On create, force -space-reserve disabled when omitted and reject an explicit "enabled".
	// Also force -space-allocation enabled (CLI equivalent of REST
	// space.scsi_thin_provisioning_support_enabled) so hosts can reclaim unmapped blocks.
	{
		Pattern:   "lun create",
		Allow:     true,
		Condition: CLIIfPresentThenValue("-space-reserve", "disabled"),
		InjectArguments: map[string]string{
			"-space-reserve":    "disabled",
			"-space-allocation": "enabled",
		},
	},
	// On modify, reject re-enabling space reservation; other modifications pass through.
	{
		Pattern:   "lun modify",
		Allow:     true,
		Condition: CLIIfPresentThenValue("-space-reserve", "disabled"),
	},

	// QoS policy-group management. Customers may manage their own ceiling-only (max
	// throughput/IOPS) policy groups but must never touch the reserved vserver-wide policy
	// ({svm}-qos-policy) or request a minimum/floor guarantee. show is always allowed.
	{
		Pattern: "qos policy-group show",
		Allow:   true,
	},
	{
		Pattern: "qos policy-group create",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-policy-group", "-vserver"),
			CLIRejectReservedQPG("-policy-group"),
			CLIRejectArgs("-min-throughput"),
		),
	},
	{
		Pattern: "qos policy-group modify",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-policy-group"),
			CLIRejectReservedQPG("-policy-group"),
			CLIRejectReservedQPG("-new-name"),
			CLIRejectArgs("-min-throughput"),
		),
		// -vserver is optional here; when omitted, the reserved (vserver-wide) policy is enforced
		// against the pool's SVM resolved from the deployment name in auth context.
		ExternalValidator: validateQoSPolicyGroupReserved,
	},
	{
		Pattern: "qos policy-group delete",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-policy-group"),
			CLIRejectReservedQPG("-policy-group"),
		),
		// -vserver is optional here; see modify above.
		ExternalValidator: validateQoSPolicyGroupReserved,
	},
	{
		Pattern: "qos policy-group rename",
		Allow:   true,
		Condition: CLIAnd(
			CLIHasArgs("-policy-group", "-new-name", "-vserver"),
			CLIRejectReservedQPG("-policy-group"),
			CLIRejectReservedQPG("-new-name"),
		),
	},
	{
		Pattern: "settings nested-qos-svm-vol modify",
		Allow:   false,
		Reason:  "Nested SVM volume QoS settings cannot be modified",
	},
	{
		Pattern: "qos settings nested-qos-svm-vol",
		Allow:   false,
		Reason:  "Nested SVM volume QoS settings cannot be modified",
	},

	{
		Pattern: "storage disk *",
		Allow:   false,
		Reason:  "Disk operations not allowed",
	},
	// Aggregate show is gated by dedicated-capacity (mirrors REST GET).
	// create/modify/delete and all other aggregate ops are denied (mirrors REST POST/PATCH/DELETE).
	// Exact show rules must come before the wildcards; first match wins.
	{
		Pattern:           "storage aggregate show",
		Allow:             true,
		ExternalValidator: validateAggregateShow,
	},
	{
		Pattern:           "aggr show",
		Allow:             true,
		ExternalValidator: validateAggregateShow,
	},
	{
		Pattern: "storage aggregate create",
		Allow:   false,
		Reason:  "Aggregate creation not allowed",
	},
	{
		Pattern: "aggr create",
		Allow:   false,
		Reason:  "Aggregate creation not allowed",
	},
	{
		Pattern: "storage aggregate modify",
		Allow:   false,
		Reason:  "Aggregate modification not allowed",
	},
	{
		Pattern: "aggr modify",
		Allow:   false,
		Reason:  "Aggregate modification not allowed",
	},
	{
		Pattern: "storage aggregate delete",
		Allow:   false,
		Reason:  "Aggregate deletion not allowed",
	},
	{
		Pattern: "aggr delete",
		Allow:   false,
		Reason:  "Aggregate deletion not allowed",
	},
	{
		Pattern: "storage aggregate *",
		Allow:   false,
		Reason:  "Aggregate operations not allowed",
	},
	{
		Pattern: "aggr *",
		Allow:   false,
		Reason:  "Aggregate operations not allowed",
	},
	// System node run — nodeshell/systemshell passthrough that can bypass the proxy's
	// CLI access controls entirely. Block "system node run" and its "run" alias outright.
	{
		Pattern: "system node run",
		Allow:   false,
		Reason:  "system node run (nodeshell access) is not allowed",
	},
	{
		Pattern: "run",
		Allow:   false,
		Reason:  "system node run (nodeshell access) is not allowed",
	},

	{
		Pattern: "set *",
		Allow:   false,
		Reason:  "Privilege escalation not allowed; use the chained command form (e.g. 'set diag; <command>')",
	},
}

// GetCLIRules returns the list of CLI rules.
// Rules are evaluated in order; first match wins.
func GetCLIRules() []CLIRule {
	return cliRules
}

// GetDiagAllowedRules returns the diagnostic mode allowlist.
func GetDiagAllowedRules() []CLIRule {
	return diagAllowedRules
}

// GetAdvancedAllowedRules returns the advanced mode allowlist.
func GetAdvancedAllowedRules() []CLIRule {
	return advancedAllowedRules
}

// MatchCLIRule finds the matching rule for a CLI command.
// Returns the matching rule and true if found, or nil and false if no rule matches.
// If no rule matches, the command should be allowed through (allow by default).
func MatchCLIRule(cmd *CLICommand) (*CLIRule, bool) {
	if cmd == nil {
		return nil, false
	}

	for i := range cliRules {
		if cmd.MatchesPattern(cliRules[i].Pattern) {
			return &cliRules[i], true
		}
	}

	// No matching rule - allow by default (return nil to indicate no rule)
	return nil, false
}

// MatchPrivilegedRule finds the matching rule for a CLI command against a
// privileged-mode allowlist (diagnostic or advanced). Only commands in the
// provided allowlist are permitted.
// Returns the matching rule and true if found, or nil and false if the command
// is not in the allowlist.
func MatchPrivilegedRule(cmd *CLICommand, allowlist []CLIRule) (*CLIRule, bool) {
	if cmd == nil {
		return nil, false
	}

	for i := range allowlist {
		if cmd.MatchesPattern(allowlist[i].Pattern) {
			return &allowlist[i], true
		}
	}

	return nil, false
}

// EvaluateRule evaluates a rule against a command, including any local conditions.
// Returns (allowed, reason) where reason is empty if allowed.
// Note: This does NOT evaluate ExternalValidator - use EvaluateExternalValidator for that.
func EvaluateRule(rule *CLIRule, cmd *CLICommand) (bool, string) {
	if rule == nil {
		return false, "No rule provided"
	}

	// Check if rule allows the command
	if !rule.Allow {
		reason := rule.Reason
		if reason == "" {
			reason = "Command not allowed"
		}
		return false, reason
	}

	// Check condition if present (use CLIAnd, CLIHasArgs, etc. like DSL)
	if rule.Condition != nil {
		if ok, reason := rule.Condition(cmd); !ok {
			return false, reason
		}
	}

	return true, ""
}

// EvaluateExternalValidator evaluates a rule's external validator if present.
// This should be called AFTER credential setup, as external validators may need auth data.
// Returns (allowed, reason) where reason is empty if allowed or no validator present.
func EvaluateExternalValidator(ctx context.Context, rule *CLIRule, cmd *CLICommand) (bool, string) {
	if rule == nil || rule.ExternalValidator == nil {
		return true, "" // No external validator - pass through
	}
	return rule.ExternalValidator(ctx, cmd)
}

// HasExternalValidator returns true if the rule has an external validator.
func HasExternalValidator(rule *CLIRule) bool {
	return rule != nil && rule.ExternalValidator != nil
}

// HasInjectArguments returns true if the rule has arguments to inject.
func HasInjectArguments(rule *CLIRule) bool {
	return rule != nil && len(rule.InjectArguments) > 0
}

// ApplyInjectArguments injects arguments into a CLI command.
// Arguments are added only if not already present in the command.
// Returns the modified command string.
func ApplyInjectArguments(cmd *CLICommand, rule *CLIRule) string {
	if rule == nil || len(rule.InjectArguments) == 0 {
		return cmd.RawInput
	}

	// Start with original command
	result := cmd.RawInput

	// Add each argument if not already present
	var injected []string
	for argName, argValue := range rule.InjectArguments {
		if !cmd.HasArgument(argName) {
			injected = append(injected, fmt.Sprintf("%s %s", argName, argValue))
		}
	}

	// Append injected arguments to the command
	if len(injected) > 0 {
		result = result + " " + strings.Join(injected, " ")
	}

	return result
}

// GetInjectedArguments returns the list of arguments that would be injected.
// Useful for logging.
func GetInjectedArguments(cmd *CLICommand, rule *CLIRule) map[string]string {
	if rule == nil || len(rule.InjectArguments) == 0 {
		return nil
	}

	injected := make(map[string]string)
	for argName, argValue := range rule.InjectArguments {
		if !cmd.HasArgument(argName) {
			injected[argName] = argValue
		}
	}

	if len(injected) == 0 {
		return nil
	}
	return injected
}

// Condition builder functions for CLI rules
// These mirror the DSL conditions in rules_v2/rule_map.go for consistency

// CLIHasArgs checks that all specified arguments are present.
// Similar to HasFields() in the REST DSL.
// Example: CLIHasArgs("-vserver", "-volume")
func CLIHasArgs(argNames ...string) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		for _, argName := range argNames {
			if !cmd.HasArgument(argName) {
				return false, fmt.Sprintf("Missing required argument: %s", argName)
			}
		}
		return true, ""
	}
}

// CLIIfPresentThenValue validates an argument value only if the argument is present.
// Similar to IfPresentThenValue() in the REST DSL.
// Example: CLIIfPresentThenValue("-space-guarantee", "none", "volume")
func CLIIfPresentThenValue(argName string, allowedValues ...string) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		if !cmd.HasArgument(argName) {
			return true, "" // Argument not present, condition passes
		}
		value := cmd.GetArgument(argName)
		for _, allowed := range allowedValues {
			if strings.EqualFold(value, allowed) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("Invalid value for %s: %q (allowed: %v)", argName, value, allowedValues)
	}
}

// CLIIfPresentThenEquals validates an argument equals a specific value if present.
// Similar to IfPresentThenEquals() in the REST DSL.
// Example: CLIIfPresentThenEquals("-force", "true")
func CLIIfPresentThenEquals(argName, expectedValue string) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		if !cmd.HasArgument(argName) {
			return true, "" // Argument not present, condition passes
		}
		value := cmd.GetArgument(argName)
		if strings.EqualFold(value, expectedValue) {
			return true, ""
		}
		return false, fmt.Sprintf("Argument %s must be %q, got %q", argName, expectedValue, value)
	}
}

// CLIArgRequiredEquals requires the argument to be present and equal to expectedValue (case-insensitive).
func CLIArgRequiredEquals(argName, expectedValue string) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		if !cmd.HasArgument(argName) {
			return false, fmt.Sprintf("Missing required argument: %s", argName)
		}
		value := cmd.GetArgument(argName)
		if strings.EqualFold(value, expectedValue) {
			return true, ""
		}
		return false, fmt.Sprintf("argument %s must be %q, got %q", argName, expectedValue, value)
	}
}

// CLIAnd combines multiple conditions with AND logic.
// Similar to And() in the REST DSL.
// Example: CLIAnd(CLIHasArgs("-vserver"), CLIIfPresentThenValue("-type", "dp", "rw"))
func CLIAnd(conditions ...CLICondition) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		for _, cond := range conditions {
			if ok, reason := cond(cmd); !ok {
				return false, reason
			}
		}
		return true, ""
	}
}

// CLIOr combines multiple conditions with OR logic.
// Similar to Or() in the REST DSL.
func CLIOr(conditions ...CLICondition) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		var lastReason string
		for _, cond := range conditions {
			if ok, reason := cond(cmd); ok {
				return true, ""
			} else {
				lastReason = reason
			}
		}
		return false, lastReason
	}
}

// CLIRejectArgs denies the command if any of the specified arguments are present.
// Used to block modifications to fields that should not be changed (e.g. -space-guarantee).
// Example: CLIRejectArgs("-space-guarantee")
func CLIRejectArgs(argNames ...string) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		for _, argName := range argNames {
			if cmd.HasArgument(argName) {
				return false, fmt.Sprintf("Argument %s is not allowed", argName)
			}
		}
		return true, ""
	}
}

// CLIRejectArgsOrFlags denies the command if any specified name is present as either a
// named argument (e.g. "-max-autosize 500g") or a boolean flag (e.g. "-autosize-reset").
// The deny reason distinguishes the two forms so it is not misleading when a flag is rejected.
func CLIRejectArgsOrFlags(argNames ...string) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		for _, argName := range argNames {
			if cmd.HasArgument(argName) {
				return false, fmt.Sprintf("Argument %s is not allowed", argName)
			}
			if cmd.HasFlag(argName) {
				return false, fmt.Sprintf("Flag %s is not allowed", argName)
			}
		}
		return true, ""
	}
}

// CLIOnlyAllowArgs ensures that the command contains ONLY arguments and flags from the allowed list.
// Any argument or flag not in the allowlist causes the condition to fail.
// Example: CLIOnlyAllowArgs("-vserver", "-security-style", "-language", "-snapshot-policy", "-quota-policy")
func CLIOnlyAllowArgs(allowedArgs ...string) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		allowed := make(map[string]bool, len(allowedArgs))
		for _, a := range allowedArgs {
			allowed[a] = true
		}
		for argName := range cmd.Arguments {
			if !allowed[argName] {
				return false, fmt.Sprintf("argument %s is not allowed", argName)
			}
		}
		for _, flag := range cmd.Flags {
			if !allowed[flag] {
				return false, fmt.Sprintf("flag %s is not allowed", flag)
			}
		}
		return true, ""
	}
}

// CLIHasFlag checks that a specific flag is present.
// Example: CLIHasFlag("-force")
func CLIHasFlag(flagName string) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		if cmd.HasFlag(flagName) {
			return true, ""
		}
		return false, fmt.Sprintf("Missing required flag: %s", flagName)
	}
}

// CLIRejectReservedQPG denies the command when the value of argName is the reserved
// (vserver-wide) QPG. The owning SVM is taken from the "-vserver" argument when present. If
// argName is absent the condition passes (nothing to reject). Used for -policy-group on
// create/modify/delete and for -new-name on rename so customers cannot target the reserved QPG.
func CLIRejectReservedQPG(argName string) CLICondition {
	return func(cmd *CLICommand) (bool, string) {
		name := cmd.GetArgument(argName)
		if name == "" {
			return true, ""
		}
		vserver := cmd.GetArgument("-vserver")
		if vserver == "" {
			// SVM is unknown at local-eval time (create/rename always supply -vserver; modify/delete
			// may omit it). Defer reserved-name enforcement to the external validator
			// (_validateQoSPolicyGroupReserved), which resolves the SVM from the deployment name in
			// auth context. Without deferring here, the external validator could never allow legit
			// "*-qos-policy" customer names.
			return true, ""
		}
		if proxyutils.IsReservedQoSPolicyName(name, vserver) {
			return false, fmt.Sprintf("QoS policy %q is reserved (vserver-wide) and cannot be created, modified, deleted, or used as a rename target", name)
		}
		return true, ""
	}
}
