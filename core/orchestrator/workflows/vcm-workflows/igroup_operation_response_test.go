package vcm_workflows

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIgroupOperationResponsePayload(t *testing.T) {
	payload, err := igroupOperationResponsePayload(
		"hgtest_linux01",
		[]string{"iqn.1998-01.com.vmware:db1", "iqn.1998-01.com.vmware:db2"},
		"LINUX",
	)
	require.NoError(t, err)
	require.NotNil(t, payload)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &body))
	assert.Equal(t, "hgtest_linux01", body["name"])
	assert.Equal(t, "LINUX", body["osType"])
	assert.NotContains(t, body, "igroups")

	hosts, ok := body["hosts"].([]any)
	require.True(t, ok)
	assert.Len(t, hosts, 2)
}
