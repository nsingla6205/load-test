package rules_v2

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// These tests exercise the QoS rules end-to-end through the registered actions
// (rule.GetAction -> action.ShouldAllow), complementing the validator-level unit tests in
// qos_validators_test.go. They verify method wiring and the And() condition composition.

func decide(t *testing.T, key, method, path, body string) (bool, string) {
	t.Helper()
	rule := GetProxyRules()[key]
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	}
	action := rule.GetAction(req)
	if action == nil {
		t.Fatalf("no action for %s %s", method, key)
	}
	return action.ShouldAllow(req)
}

func TestQoSPoliciesCollectionRuleBehavior(t *testing.T) {
	const key = "/api/storage/qos/policies"

	t.Run("GET listing allowed", func(t *testing.T) {
		allowed, _ := decide(t, key, http.MethodGet, key, "")
		assert.True(t, allowed)
	})

	t.Run("POST ceiling-only allowed", func(t *testing.T) {
		allowed, reason := decide(t, key, http.MethodPost, key,
			`{"name":"cust1","svm":{"name":"svm1"},"fixed":{"max_throughput_iops":5000}}`)
		assert.True(t, allowed, "reason: %s", reason)
	})

	t.Run("POST reserved name denied", func(t *testing.T) {
		allowed, reason := decide(t, key, http.MethodPost, key,
			`{"name":"svm1-qos-policy","svm":{"name":"svm1"}}`)
		assert.False(t, allowed)
		assert.NotEmpty(t, reason)
	})

	t.Run("POST fixed min-floor QoS denied", func(t *testing.T) {
		allowed, reason := decide(t, key, http.MethodPost, key,
			`{"name":"cust1","fixed":{"min_throughput_iops":1000}}`)
		assert.False(t, allowed)
		assert.NotEmpty(t, reason)
	})

	t.Run("POST adaptive absolute minimum allowed", func(t *testing.T) {
		allowed, reason := decide(t, key, http.MethodPost, key,
			`{"name":"cust1","adaptive":{"absolute_min_iops":1000}}`)
		assert.True(t, allowed, "reason: %s", reason)
	})

	t.Run("PATCH bulk denied", func(t *testing.T) {
		allowed, _ := decide(t, key, http.MethodPatch, key, `{"fixed":{"max_throughput_iops":1}}`)
		assert.False(t, allowed)
	})

	t.Run("DELETE bulk denied", func(t *testing.T) {
		allowed, _ := decide(t, key, http.MethodDelete, key, "")
		assert.False(t, allowed)
	})
}

func TestQoSPolicyInstanceRuleBehavior(t *testing.T) {
	const key = "/api/storage/qos/policies/{uuid}"
	path := "/api/storage/qos/policies/11111111-2222-3333-4444-555555555555"
	allowed, _ := decide(t, key, http.MethodPost, path, `{"name":"x"}`)
	assert.False(t, allowed)
}

func TestNestedQoSSVMVolumeSettingsRESTDenied(t *testing.T) {
	paths := []string{
		"/api/private/cli/qos/settings/nested-qos-svm-vol",
		"/api/private/cli/settings/nested-qos-svm-vol",
	}
	for _, key := range paths {
		t.Run(key+" GET allowed", func(t *testing.T) {
			allowed, _ := decide(t, key, http.MethodGet, key, "")
			assert.True(t, allowed)
		})
		t.Run(key+" PATCH denied", func(t *testing.T) {
			allowed, reason := decide(t, key, http.MethodPatch, key, `{"enable":false}`)
			assert.False(t, allowed)
			assert.NotEmpty(t, reason)
		})
		t.Run(key+" POST denied", func(t *testing.T) {
			allowed, _ := decide(t, key, http.MethodPost, key, `{"enable":false}`)
			assert.False(t, allowed)
		})
		t.Run(key+" DELETE denied", func(t *testing.T) {
			allowed, _ := decide(t, key, http.MethodDelete, key, "")
			assert.False(t, allowed)
		})
	}
}
