package vcm_workflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

func TestSelectVcmVolumeChildWorkflow_PrePhase(t *testing.T) {
	preWorkflow, err := selectVcmVolumeChildWorkflow([]string{utils.ProtocolISCSI}, PhasePre)
	require.NoError(t, err)
	assert.Equal(t, gowfbackend.FuncName(VcmPreBlockVolumeWorkflow), gowfbackend.FuncName(preWorkflow))

	preWorkflow, err = selectVcmVolumeChildWorkflow([]string{utils.ProtocolNFSv3}, PhasePre)
	require.NoError(t, err)
	assert.Equal(t, gowfbackend.FuncName(VcmPreFileVolumeWorkflow), gowfbackend.FuncName(preWorkflow))
}

func TestSelectVcmVolumeChildWorkflow_PostPhase(t *testing.T) {
	postWorkflow, err := selectVcmVolumeChildWorkflow([]string{utils.ProtocolISCSI}, PhasePost)
	require.NoError(t, err)
	assert.Equal(t, gowfbackend.FuncName(VcmPostBlockVolumeWorkflow), gowfbackend.FuncName(postWorkflow))

	postWorkflow, err = selectVcmVolumeChildWorkflow([]string{utils.ProtocolSMB}, PhasePost)
	require.NoError(t, err)
	assert.Equal(t, gowfbackend.FuncName(VcmPostFileVolumeWorkflowForSMB), gowfbackend.FuncName(postWorkflow))

	postWorkflow, err = selectVcmVolumeChildWorkflow([]string{utils.ProtocolNFSv3}, PhasePost)
	require.NoError(t, err)
	assert.Equal(t, gowfbackend.FuncName(VcmPostFileVolumeWorkflow), gowfbackend.FuncName(postWorkflow))

	postWorkflow, err = selectVcmVolumeChildWorkflow([]string{utils.ProtocolNFSv3, utils.ProtocolSMB}, PhasePost)
	require.NoError(t, err)
	workflowSlice, ok := postWorkflow.([]interface{})
	require.True(t, ok)
	require.Len(t, workflowSlice, 2)
	assert.Equal(t, gowfbackend.FuncName(VcmPostFileVolumeWorkflowForSMB), gowfbackend.FuncName(workflowSlice[0]))
	assert.Equal(t, gowfbackend.FuncName(VcmPostFileVolumeWorkflow), gowfbackend.FuncName(workflowSlice[1]))
}

func TestSelectVcmVolumeChildWorkflow_InvalidPhase(t *testing.T) {
	_, err := selectVcmVolumeChildWorkflow([]string{utils.ProtocolISCSI}, "invalid")
	require.Error(t, err)
}

func TestSelectVcmVolumeChildWorkflow_UnsupportedProtocol(t *testing.T) {
	_, err := selectVcmVolumeChildWorkflow([]string{"INVALID"}, PhasePre)
	require.Error(t, err)
}

func TestSelectVcmVolumeChildWorkflow_InvalidPhaseForNAS(t *testing.T) {
	_, err := selectVcmVolumeChildWorkflow([]string{utils.ProtocolNFSv3}, "invalid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid phase")
}

func TestHostGroupNamesFromCreateParams(t *testing.T) {
	params := &common.CreateVolumeParams{
		BlockDevices: &[]common.BlockDevice{
			{HostGroupNames: []string{"hg-1", "hg-2"}},
			{HostGroupNames: []string{"hg-3"}},
		},
	}
	assert.Equal(t, []string{"hg-1", "hg-2", "hg-3"}, hostGroupNamesFromCreateParams(params))
	assert.Nil(t, hostGroupNamesFromCreateParams(nil))
	assert.Nil(t, hostGroupNamesFromCreateParams(&common.CreateVolumeParams{}))
}
