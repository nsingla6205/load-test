// errors_test.go covers the Error() methods of the package's three
// public error types. The selector tests already exercise these errors
// via errors.As (type identity) but never call .Error() itself, so the
// formatted-message branches show as 0% covered. These tests anchor the
// exact wire-up of message + path/request into the formatted string so
// log scrapers / alerting that match on the [vmrs/oci] prefix can't
// silently break.
package oci_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/oci"
)

func TestConfigParseError_Error_IncludesPrefixMessageAndPath(t *testing.T) {
	err := &oci.ConfigParseError{
		Message: "boom",
		Path:    "/tmp/x.yaml",
	}
	got := err.Error()
	assert.Contains(t, got, "[vmrs/oci] ConfigParseError:")
	assert.Contains(t, got, "boom")
	assert.Contains(t, got, "/tmp/x.yaml")
}

func TestInvalidConfigError_Error_IncludesPrefixAndMessage(t *testing.T) {
	err := &oci.InvalidConfigError{Message: "config cannot be nil"}
	got := err.Error()
	assert.Contains(t, got, "[vmrs/oci] InvalidConfigError:")
	assert.Contains(t, got, "config cannot be nil")
}

func TestNoFeasibleSelectionError_Error_IncludesMessageAndRequest(t *testing.T) {
	err := &oci.NoFeasibleSelectionError{
		Message: "no tier",
		Request: oci.CustomerRequest{
			DesiredCapacityTiB:     5.0,
			DesiredThroughputMBps: 4800,
		},
	}
	got := err.Error()
	assert.Contains(t, got, "[vmrs/oci] NoFeasibleSelectionError:")
	assert.Contains(t, got, "no tier")
	// The %+v render of CustomerRequest must surface the request's
	// numeric inputs so on-call can reproduce the failure without
	// having to also recover the original request from logs.
	assert.Contains(t, got, "DesiredCapacityTiB:5")
	assert.Contains(t, got, "DesiredThroughputMBps:4800")
}
