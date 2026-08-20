package utils

import "testing"

func TestIsReservedQoSPolicyName(t *testing.T) {
	tests := []struct {
		name     string
		policy   string
		svm      string
		reserved bool
	}{
		{"empty is not reserved", "", "svm1", false},
		{"exact vserver-wide name", "svm1-qos-policy", "svm1", true},
		{"exact name case-insensitive", "SVM1-QOS-POLICY", "svm1", true},
		{"unknown svm fails closed for reserved suffix", "anything-qos-policy", "", true},
		{"unknown svm case-insensitive suffix", "ANYTHING-QOS-POLICY", "", true},
		{"unknown svm non-suffix name allowed", "cust1", "", false},
		{"customer name allowed", "cust1", "svm1", false},
		{"other-prefix -qos-policy allowed", "foo-qos-policy", "svm1", false},
		{"different svm name allowed", "svm2-qos-policy", "svm1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReservedQoSPolicyName(tt.policy, tt.svm); got != tt.reserved {
				t.Errorf("IsReservedQoSPolicyName(%q,%q) = %v, want %v", tt.policy, tt.svm, got, tt.reserved)
			}
		})
	}
}
