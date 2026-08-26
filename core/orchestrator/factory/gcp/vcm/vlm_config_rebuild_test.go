package vcm

import (
	"context"
	"errors"
	"strings"
	"testing"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
)

func rebuildParams(deploymentID string) *commonparams.GetVLMConfigParams {
	return &commonparams.GetVLMConfigParams{
		DeploymentID:         deploymentID,
		ServiceTenantProject: "tenant-1",
		Zone1:                "us-central1-a",
		Zone2:                "us-central1-b",
		MediatorZone:         "us-central1-c",
	}
}

func TestStartRebuildVLMConfig_Success(t *testing.T) {
	starter := &fakeStarter{}
	d := NewVLMConfigRebuildDispatcher(starter)

	err := d.StartRebuildVLMConfig(context.Background(), rebuildParams("dep-1"))

	require.NoError(t, err)
	assert.True(t, starter.called)
	assert.True(t, strings.HasPrefix(starter.gotInstance, "dep-1"+vlmConfigRebuildInstanceSuffix),
		"instance id is prefixed by the deployment id and rebuild suffix")
}

func TestStartRebuildVLMConfig_InstanceIDIncludesConfigAndUniqueAttempt(t *testing.T) {
	original := rebuildParams("dep-1")
	changed := rebuildParams("dep-1")
	changed.Zone2 = "us-central1-f"

	s1, s2, s3 := &fakeStarter{}, &fakeStarter{}, &fakeStarter{}
	require.NoError(t, NewVLMConfigRebuildDispatcher(s1).StartRebuildVLMConfig(context.Background(), original))
	require.NoError(t, NewVLMConfigRebuildDispatcher(s2).StartRebuildVLMConfig(context.Background(), rebuildParams("dep-1")))
	require.NoError(t, NewVLMConfigRebuildDispatcher(s3).StartRebuildVLMConfig(context.Background(), changed))

	originalPrefix := "dep-1" + vlmConfigRebuildInstanceSuffix + rebuildConfigFingerprint(original) + "-"
	changedPrefix := "dep-1" + vlmConfigRebuildInstanceSuffix + rebuildConfigFingerprint(changed) + "-"
	assert.True(t, strings.HasPrefix(s1.gotInstance, originalPrefix))
	assert.True(t, strings.HasPrefix(s2.gotInstance, originalPrefix))
	assert.True(t, strings.HasPrefix(s3.gotInstance, changedPrefix))
	assert.NotEqual(t, originalPrefix, changedPrefix, "a changed rebuild field must change the config fingerprint")
	assert.NotEqual(t, s1.gotInstance, s2.gotInstance,
		"a later retry of the same config must use a new instance id after a terminal failure")
}

func TestStartRebuildVLMConfig_NilParams(t *testing.T) {
	starter := &fakeStarter{}
	d := NewVLMConfigRebuildDispatcher(starter)

	err := d.StartRebuildVLMConfig(context.Background(), nil)

	require.Error(t, err)
	assert.False(t, starter.called, "dispatch must not start without params")
}

func TestStartRebuildVLMConfig_MissingDeploymentID(t *testing.T) {
	starter := &fakeStarter{}
	d := NewVLMConfigRebuildDispatcher(starter)

	err := d.StartRebuildVLMConfig(context.Background(), rebuildParams(""))

	require.Error(t, err)
	assert.False(t, starter.called, "an empty deploymentId must fail fast before Start")
}

func TestStartRebuildVLMConfig_AlreadyExists_IsIdempotent(t *testing.T) {
	starter := &fakeStarter{err: cbackend.ErrInstanceAlreadyExists}
	d := NewVLMConfigRebuildDispatcher(starter)

	err := d.StartRebuildVLMConfig(context.Background(), rebuildParams("dep-1"))

	require.NoError(t, err, "an existing rebuild instance is a no-op success")
	assert.True(t, starter.called)
}

func TestStartRebuildVLMConfig_StartError_IsPropagated(t *testing.T) {
	starter := &fakeStarter{err: errors.New("dispatch boom")}
	d := NewVLMConfigRebuildDispatcher(starter)

	err := d.StartRebuildVLMConfig(context.Background(), rebuildParams("dep-1"))

	require.Error(t, err)
	assert.True(t, starter.called)
}
