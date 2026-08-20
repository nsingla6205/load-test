package rules_v2

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postQoSRequest(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/storage/qos/policies", bytes.NewBufferString(body))
}

func TestRejectReservedQoSPolicyCreate(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantAllow bool
	}{
		{"customer name allowed", `{"name":"cust1","svm":{"name":"svm1"}}`, true},
		{"reserved vserver-wide name denied", `{"name":"svm1-qos-policy","svm":{"name":"svm1"}}`, false},
		{"other-prefix -qos-policy allowed", `{"name":"foo-qos-policy","svm":{"name":"svm1"}}`, true},
		{"different svm -qos-policy allowed", `{"name":"svm2-qos-policy","svm":{"name":"svm1"}}`, true},
		{"reserved suffix without svm denied (fail closed)", `{"name":"foo-qos-policy"}`, false},
		{"invalid JSON denied", `{not-json`, false},
		{"empty body denied", ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := rejectReservedQoSPolicyCreate(postQoSRequest(tt.body))
			if ok != tt.wantAllow {
				t.Fatalf("ok = %v (reason=%q), want %v", ok, reason, tt.wantAllow)
			}
			if !ok && reason == "" {
				t.Error("expected non-empty reason")
			}
		})
	}
}

func TestRejectFixedQoSMinimumThroughput(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantAllow bool
	}{
		{"fixed maximum allowed", `{"fixed":{"max_throughput_iops":5000}}`, true},
		{"fixed minimum IOPS denied", `{"fixed":{"min_throughput_iops":100}}`, false},
		{"fixed minimum Mbps denied", `{"fixed":{"min_throughput_mbps":50}}`, false},
		{"adaptive absolute minimum allowed", `{"adaptive":{"absolute_min_iops":100}}`, true},
		{"no QoS settings allowed", `{"name":"cust1"}`, true},
		{"invalid JSON denied", `{not-json`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := rejectFixedQoSMinimumThroughput(postQoSRequest(tt.body))
			if ok != tt.wantAllow {
				t.Fatalf("ok = %v (reason=%q), want %v", ok, reason, tt.wantAllow)
			}
			if !ok && reason == "" {
				t.Error("expected non-empty reason")
			}
		})
	}
}

func TestQoSRulesRegistered(t *testing.T) {
	rules := GetProxyRules()
	for _, path := range []string{
		"/api/storage/qos/policies",
		"/api/storage/qos/policies/{uuid}",
		"/api/private/cli/qos/settings/nested-qos-svm-vol",
		"/api/private/cli/settings/nested-qos-svm-vol",
	} {
		if _, ok := rules[path]; !ok {
			t.Errorf("expected rule registered for %q", path)
		}
	}
}

func TestQtreeRulesPassThrough(t *testing.T) {
	// Qtree operations are intentionally unregistered so they pass through by default.
	rules := GetProxyRules()
	for _, path := range []string{
		"/api/storage/qtrees",
		"/api/storage/qtrees/{uuid}/*",
	} {
		if _, ok := rules[path]; ok {
			t.Errorf("expected no deny rule for %q (qtrees should pass through)", path)
		}
	}
}
