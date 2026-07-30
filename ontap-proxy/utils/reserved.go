package utils

import "strings"

// ReservedQoSPolicySuffix is the naming convention of the VCP-managed, vserver-wide QoS policy
// group ({svm}-qos-policy, see core generateQoSPolicyName). Customers must not create, modify,
// delete, or rename into this reserved namespace.
const ReservedQoSPolicySuffix = "-qos-policy"

// IsReservedQoSPolicyName reports whether policyName is the reserved (vserver-wide) QPG for the
// given SVM, i.e. it matches the exact {svm}-qos-policy name (case-insensitive). Only that exact
// name is reserved; other names in the "-qos-policy" namespace are customer-usable. Returns false
// when svmName is unknown, since the reserved name cannot be computed without it.
func IsReservedQoSPolicyName(policyName, svmName string) bool {
	if policyName == "" || svmName == "" {
		return false
	}
	return strings.EqualFold(policyName, svmName+ReservedQoSPolicySuffix)
}
