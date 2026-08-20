package utils

import "strings"

// ReservedQoSPolicySuffix is the naming convention of the VCP-managed, vserver-wide QoS policy
// group ({svm}-qos-policy, see core generateQoSPolicyName). Customers must not create, modify,
// delete, or rename into this reserved namespace.
const ReservedQoSPolicySuffix = "-qos-policy"

// DefaultSVMSequenceSuffix is the sequence suffix of a pool's data SVM. SVM names follow
// {deploymentName}-svm-NN (see core AllocateSVMName); today there is exactly one SVM per pool,
// so -svm-01. Revisit if/when multi-SVM support lands.
const DefaultSVMSequenceSuffix = "-svm-01"

// SVMNameForFlexDeployment returns the pool's data SVM name derived from its deployment name
// ({deploymentName}-svm-01), or "" when deploymentName is unknown.
func SVMNameForFlexDeployment(deploymentName string) string {
	if deploymentName == "" {
		return ""
	}
	return deploymentName + DefaultSVMSequenceSuffix
}

// IsReservedQoSPolicyName reports whether policyName is the reserved (vserver-wide) QPG.
//
// When svmName is known, only the exact {svm}-qos-policy name (case-insensitive) is reserved;
// other names in the "-qos-policy" namespace are customer-usable.
//
// When svmName is unknown (empty), the reserved name cannot be computed, so the check fails
// CLOSED: any name in the reserved "-qos-policy" namespace is treated as reserved. This prevents
// bypassing reserved-policy protection by omitting the SVM (e.g. a CLI "qos policy-group modify"
// without -vserver), where ONTAP would resolve the policy in the caller's default vserver context.
func IsReservedQoSPolicyName(policyName, svmName string) bool {
	if policyName == "" {
		return false
	}
	if svmName == "" {
		return strings.HasSuffix(strings.ToLower(policyName), ReservedQoSPolicySuffix)
	}
	return strings.EqualFold(policyName, svmName+ReservedQoSPolicySuffix)
}
