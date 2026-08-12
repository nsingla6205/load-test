package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoreIstioPolicy_AllowsHarvestCoreAPIEndpoints(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)

	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	policyPath := filepath.Join(
		repoRoot,
		"kubernetes",
		"vsa-control-plane",
		"charts",
		"core",
		"templates",
		"istioPolicies.yaml",
	)

	content, err := os.ReadFile(policyPath)
	require.NoError(t, err)

	policy := string(content)
	assert.Contains(t, policy, "allow-vsa-harvest-otel-replications")
	assert.Contains(t, policy, "cluster.local/ns/vcp/sa/gcnv-harvest-sa")
	assert.Contains(t, policy, `"/v1/getMultipleReplicationsByExternalUUID"`)
	assert.Contains(t, policy, `"/v1/getAllVolumeTenancy"`)

	harvestSection := policy[strings.Index(policy, "allow-vsa-harvest-otel-replications"):]
	methodsSection := harvestSection[strings.Index(harvestSection, "methods:"):]
	assert.Contains(t, methodsSection, `"GET"`)
}
