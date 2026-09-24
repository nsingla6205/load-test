package vcm_workflows

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

const volumeUpdateWorkflowTestTimeout = 30 * time.Second

type updateWorkflowVolumeActivity struct {
	mu sync.Mutex

	getVolumeResponses         []*vsa.VolumeResponse
	getVolumeCall              int
	getVolumeErr               error
	updateVolumeCalls          int
	updateVolumeErr            error
	updateLunCalls             int
	updateLunErr               error
	getSnapshotPolicy          *datamodel.SnapshotPolicy
	getSnapshotErr             error
	updateSnapshotErr          error
	lastUpdatingSnapshotPolicy *datamodel.SnapshotPolicy
	updateJunctionErr          error
	getExportPolicy            *datamodel.ExportPolicy
	getExportErr               error
	updateExportCalls          int
	lastExportName             string
	updateExportErr            error
}

func (s *updateWorkflowVolumeActivity) GetVolumeFromONTAP(_ context.Context, volume *datamodel.Volume, _ *models.Node) (*vsa.VolumeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getVolumeErr != nil {
		return nil, s.getVolumeErr
	}
	if len(s.getVolumeResponses) > 0 {
		idx := s.getVolumeCall
		if idx >= len(s.getVolumeResponses) {
			idx = len(s.getVolumeResponses) - 1
		}
		s.getVolumeCall++
		resp := s.getVolumeResponses[idx]
		if resp != nil {
			return resp, nil
		}
	}
	name := volume.Name
	if name == "" {
		name = "vol-1"
	}
	return &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{Name: name, ExternalUUID: "vol-uuid-1"},
		Size:                           volume.SizeInBytes,
		UsedBytes:                      1024,
		SnapReserve:                    10,
		SnapshotDirectoryAccessEnabled: true,
		SnapshotPolicyName:             models.DefaultONTAPSnapshotPolicyName,
		JunctionPath:                   "/nfs-vol",
		ExportPolicyName:               "nfs-vol",
	}, nil
}

func (s *updateWorkflowVolumeActivity) UpdateVolumeInONTAP(context.Context, *datamodel.Volume, *common.UpdateVolumeParams, *models.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateVolumeCalls++
	return s.updateVolumeErr
}

func (s *updateWorkflowVolumeActivity) UpdateLun(context.Context, *datamodel.Volume, *vsa.VolumeResponse, *models.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateLunCalls++
	return s.updateLunErr
}

func (s *updateWorkflowVolumeActivity) GetSnapshotPolicyInONTAP(context.Context, *models.Node, string) (*datamodel.SnapshotPolicy, error) {
	if s.getSnapshotErr != nil {
		return nil, s.getSnapshotErr
	}
	if s.getSnapshotPolicy != nil {
		return s.getSnapshotPolicy, nil
	}
	return &datamodel.SnapshotPolicy{
		Name:      "nfs-vol",
		IsEnabled: true,
		Schedules: []*datamodel.SnapshotPolicySchedule{{Count: 5, SnapmirrorLabel: "daily"}},
	}, nil
}

func (s *updateWorkflowVolumeActivity) UpdateSnapshotPolicyInOntap(_ context.Context, _ *models.Node, _ *datamodel.SnapshotPolicy, updating *datamodel.SnapshotPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if updating != nil {
		copied := *updating
		s.lastUpdatingSnapshotPolicy = &copied
	}
	return s.updateSnapshotErr
}

func (s *updateWorkflowVolumeActivity) UpdateJunctionPathInONTAP(context.Context, *datamodel.Volume, string, *models.Node) error {
	return s.updateJunctionErr
}

func (s *updateWorkflowVolumeActivity) GetExportPolicyInONTAP(context.Context, *models.Node, string, string) (*datamodel.ExportPolicy, error) {
	if s.getExportErr != nil {
		return nil, s.getExportErr
	}
	if s.getExportPolicy != nil {
		return s.getExportPolicy, nil
	}
	return &datamodel.ExportPolicy{
		ExportPolicyName: "nfs-vol",
		ExportRules: []*datamodel.ExportRule{
			{AllowedClients: "0.0.0.0/0", AccessType: "READ_WRITE", NFSv3: true, Index: 1},
		},
	}, nil
}

func (s *updateWorkflowVolumeActivity) UpdateExportPolicyRulesInONTAP(_ context.Context, _ *datamodel.Volume, policy *models.ExportPolicy, _ *models.Node) error {
	s.updateExportCalls++
	s.lastExportName = policy.ExportPolicyName
	return s.updateExportErr
}

type updateWorkflowVCMVolumeActivity struct {
	lunResponse           vsa.LunResponse
	lunErr                error
	updateHostGroupsCalls int
	updateHostGroupsErr   error
}

func (s *updateWorkflowVCMVolumeActivity) PopulateVolumeFromONTAPResponse(ctx context.Context, volume *datamodel.Volume, volResponse *vsa.VolumeResponse) (*datamodel.Volume, error) {
	return activities.VolumeActivity{}.PopulateVolumeFromONTAPResponse(ctx, volume, volResponse)
}

func (s *updateWorkflowVCMVolumeActivity) GetLunInONTAP(context.Context, string, string, string, *models.Node) (vsa.LunResponse, error) {
	if s.lunErr != nil {
		return vsa.LunResponse{}, s.lunErr
	}
	return s.lunResponse, nil
}

func (s *updateWorkflowVCMVolumeActivity) UpdateBlockDeviceHostGroups(context.Context, vsa.LunResponse, string, []string, *models.Node) error {
	s.updateHostGroupsCalls++
	return s.updateHostGroupsErr
}

func (s *updateWorkflowVCMVolumeActivity) BuildVolumeOperationResponseEnrichment(context.Context, *vsa.VolumeResponse, string, *models.Node) (*vsa.VolumeOperationEnrichment, error) {
	return &vsa.VolumeOperationEnrichment{}, nil
}

type updateWorkflowSnapshotCreateActivity struct {
	calls int
	err   error
}

func (s *updateWorkflowSnapshotCreateActivity) CreateSnapshotPolicyInONTAP(context.Context, *datamodel.Volume, *models.Node) error {
	s.calls++
	return s.err
}

func registerUpdateVolumeActivities(
	reg orchestrator.WorkflowRegistrar,
	job *stubJobActivity,
	svm *stubSvmActivity,
	update *updateWorkflowVolumeActivity,
	vcmVolume *updateWorkflowVCMVolumeActivity,
	snapshotCreate *updateWorkflowSnapshotCreateActivity,
	hostGroup *stubVCMHostGroupActivity,
) {
	reg.RegisterActivity(job.UpdateJobProgress)
	reg.RegisterActivity(svm.GetSvmByName)
	reg.RegisterActivity(update.GetVolumeFromONTAP)
	reg.RegisterActivity(vcmVolume.PopulateVolumeFromONTAPResponse)
	reg.RegisterActivity(vcmVolume.BuildVolumeOperationResponseEnrichment)
	reg.RegisterActivity(vcmVolume.GetLunInONTAP)
	reg.RegisterActivity(vcmVolume.UpdateBlockDeviceHostGroups)
	reg.RegisterActivity(snapshotCreate.CreateSnapshotPolicyInONTAP)
	reg.RegisterActivity(update.GetSnapshotPolicyInONTAP)
	reg.RegisterActivity(update.UpdateSnapshotPolicyInOntap)
	reg.RegisterActivity(update.UpdateVolumeInONTAP)
	reg.RegisterActivity(update.UpdateLun)
	reg.RegisterActivity(update.UpdateJunctionPathInONTAP)
	reg.RegisterActivity(update.GetExportPolicyInONTAP)
	reg.RegisterActivity(update.UpdateExportPolicyRulesInONTAP)
	reg.RegisterActivity(hostGroup.ValidateHostGroupsExistInONTAP)
}

func runVcmUpdateVolumeWorkflow(
	t *testing.T,
	instanceID string,
	register func(orchestrator.WorkflowRegistrar),
	volume *datamodel.Volume,
	node *models.Node,
	params *common.UpdateVolumeParams,
) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), volumeUpdateWorkflowTestTimeout)
	defer cancel()

	runner, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := runner.Registrar()
	reg.RegisterWorkflow(VcmUpdateVolumeWorkflow)
	register(reg)

	require.NoError(t, runner.Start(ctx))
	t.Cleanup(func() {
		cancel()
		_ = runner.Stop()
	})

	if instanceID == "" {
		instanceID = uuid.NewString()
	}
	wfi, err := runner.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: instanceID,
	}, gowfbackend.FuncName(VcmUpdateVolumeWorkflow), volume, node, params)
	require.NoError(t, err)

	_, err = cclient.GetWorkflowResult[json.RawMessage](ctx, runner.Client(), wfi, volumeUpdateWorkflowTestTimeout)
	return err
}

func testUpdateNFSVolume() *datamodel.Volume {
	volume := testNFSVolume()
	volume.VolumeAttributes.ExternalUUID = "vol-uuid-1"
	return volume
}

func testUpdateVolumeParams(quota int64) *common.UpdateVolumeParams {
	return &common.UpdateVolumeParams{
		QuotaInBytes: quota,
	}
}

func TestVcmUpdateVolumeWorkflow_InvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		volume *datamodel.Volume
		node   *models.Node
		params *common.UpdateVolumeParams
	}{
		{name: "nil volume", volume: nil, node: testNode(), params: testUpdateVolumeParams(107374182400)},
		{name: "missing SVM", volume: &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{}}, node: testNode(), params: testUpdateVolumeParams(107374182400)},
		{name: "missing volume attributes", volume: &datamodel.Volume{Svm: &datamodel.Svm{Name: testExpectedSvmName}}, node: testNode(), params: testUpdateVolumeParams(107374182400)},
		{name: "nil params", volume: testUpdateNFSVolume(), node: testNode(), params: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			job := &stubJobActivity{}
			err := runVcmUpdateVolumeWorkflow(t, "op-invalid", func(reg orchestrator.WorkflowRegistrar) {
				reg.RegisterActivity(job.UpdateJobProgress)
			}, tt.volume, tt.node, tt.params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid workflow input")
			calls := job.snapshot()
			require.Len(t, calls, 1)
			assert.Equal(t, vcmdb.JobStateError, calls[0].state)
		})
	}
}

func TestVcmUpdateVolumeWorkflow_NFSSuccess(t *testing.T) {
	const operationID = "op-nfs-success"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &updateWorkflowVolumeActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	params := testUpdateVolumeParams(214748364800)
	err := runVcmUpdateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, svm, update, vcmVolume, snapshotCreate, hostGroup)
	}, testUpdateNFSVolume(), testNode(), params)

	require.NoError(t, err)
	assert.Equal(t, 1, svm.callCount())
	assert.Equal(t, 1, update.updateVolumeCalls)
	calls := job.snapshot()
	require.GreaterOrEqual(t, len(calls), 2)
	assert.Equal(t, vcmdb.JobStateDone, calls[len(calls)-1].state)
	assert.NotEmpty(t, calls[len(calls)-1].responsePayload)
}

func TestVcmUpdateVolumeWorkflow_NFSSizeIncreaseIgnoresLunNotFound(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{
		lunErr: apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, utilErrors.NewNotFoundErr("lun", nil)),
			false,
		),
	}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	volume := testUpdateNFSVolume()
	params := testUpdateVolumeParams(volume.SizeInBytes * 2)

	err := runVcmUpdateVolumeWorkflow(t, "op-nfs-resize", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, volume, testNode(), params)

	require.NoError(t, err)
	assert.Equal(t, 1, update.updateVolumeCalls)
	assert.Equal(t, 0, update.updateLunCalls)
}

func TestVcmUpdateVolumeWorkflow_InvalidQuota(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	err := runVcmUpdateVolumeWorkflow(t, "op-invalid-quota", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, testUpdateNFSVolume(), testNode(), testUpdateVolumeParams(100))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid volume capacity")
}

func TestVcmUpdateVolumeWorkflow_NFSReduceUsedBytesTooHigh(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:               vsa.ProviderResponse{Name: "nfs-vol", ExternalUUID: "vol-uuid-1"},
			Size:                           107374182400,
			UsedBytes:                      80 * 1024 * 1024 * 1024,
			SnapshotDirectoryAccessEnabled: true,
		}},
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	err := runVcmUpdateVolumeWorkflow(t, "op-reduce", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, testUpdateNFSVolume(), testNode(), testUpdateVolumeParams(50*1024*1024*1024))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "too small")
}

func TestVcmUpdateVolumeWorkflow_SANReduceRejected(t *testing.T) {
	volume := testBlockVolume()
	volume.VolumeAttributes.ExternalUUID = "vol-uuid-1"
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse: vsa.ProviderResponse{Name: "vol-1", ExternalUUID: "vol-uuid-1"},
			Size:             volume.SizeInBytes,
		}},
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{
		lunResponse: vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid"},
		},
	}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	err := runVcmUpdateVolumeWorkflow(t, "op-san-reduce", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, volume, testNode(), testUpdateVolumeParams(volume.SizeInBytes/2))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume size cannot be reduced")
}

func TestVcmUpdateVolumeWorkflow_CreateSnapshotPolicy(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:               vsa.ProviderResponse{Name: "nfs-vol", ExternalUUID: "vol-uuid-1"},
			Size:                           107374182400,
			SnapshotPolicyName:             models.DefaultONTAPSnapshotPolicyName,
			SnapshotDirectoryAccessEnabled: true,
		}},
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	params := testUpdateVolumeParams(107374182400)
	params.SnapshotPolicy = &models.SnapshotPolicy{
		IsEnabled: true,
		Schedules: []*models.SnapshotPolicySchedule{{
			Count:           3,
			SnapmirrorLabel: "daily",
			Schedule:        &models.Schedule{Hours: []int{1}},
		}},
	}

	err := runVcmUpdateVolumeWorkflow(t, "op-snap-create", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, testUpdateNFSVolume(), testNode(), params)

	require.NoError(t, err)
	assert.Equal(t, 1, snapshotCreate.calls)
}

func TestVcmUpdateVolumeWorkflow_UpdateSnapshotPolicy(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:               vsa.ProviderResponse{Name: volume.Name, ExternalUUID: "vol-uuid-1"},
			Size:                           volume.SizeInBytes,
			SnapshotPolicyName:             volume.Name,
			SnapshotDirectoryAccessEnabled: true,
		}},
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.SnapshotPolicy = &models.SnapshotPolicy{IsEnabled: false}

	err := runVcmUpdateVolumeWorkflow(t, "op-snap-update", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, volume, testNode(), params)

	require.NoError(t, err)
	assert.Equal(t, 0, snapshotCreate.calls)
}

func TestVcmUpdateVolumeWorkflow_SnapshotPolicyEnabledSetPreservesCurrentEnabled(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:   vsa.ProviderResponse{Name: volume.Name, ExternalUUID: "vol-uuid-1"},
			Size:               volume.SizeInBytes,
			SnapshotPolicyName: volume.Name,
		}},
		getSnapshotPolicy: &datamodel.SnapshotPolicy{
			Name:      volume.Name,
			IsEnabled: true,
			Schedules: []*datamodel.SnapshotPolicySchedule{{Count: 5, SnapmirrorLabel: "daily"}},
		},
	}

	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.SnapshotPolicy = &models.SnapshotPolicy{IsEnabled: false}
	params.SnapshotPolicyEnabledSet = false

	err := runVcmUpdateVolumeWorkflow(t, "op-snap-enabled-unset", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)

	require.NoError(t, err)
	require.NotNil(t, update.lastUpdatingSnapshotPolicy)
	assert.True(t, update.lastUpdatingSnapshotPolicy.IsEnabled)
}

func TestVcmUpdateVolumeWorkflow_SnapshotPolicyEnabledSetAppliesExplicitEnabled(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:   vsa.ProviderResponse{Name: volume.Name, ExternalUUID: "vol-uuid-1"},
			Size:               volume.SizeInBytes,
			SnapshotPolicyName: volume.Name,
		}},
		getSnapshotPolicy: &datamodel.SnapshotPolicy{
			Name:      volume.Name,
			IsEnabled: true,
			Schedules: []*datamodel.SnapshotPolicySchedule{{Count: 5, SnapmirrorLabel: "daily"}},
		},
	}

	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.SnapshotPolicy = &models.SnapshotPolicy{IsEnabled: false}
	params.SnapshotPolicyEnabledSet = true

	err := runVcmUpdateVolumeWorkflow(t, "op-snap-enabled-set", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)

	require.NoError(t, err)
	require.NotNil(t, update.lastUpdatingSnapshotPolicy)
	assert.False(t, update.lastUpdatingSnapshotPolicy.IsEnabled)
}

func TestVcmUpdateVolumeWorkflow_CreateSnapshotPolicyWithoutSchedulesFails(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:   vsa.ProviderResponse{Name: "nfs-vol", ExternalUUID: "vol-uuid-1"},
			Size:               107374182400,
			SnapshotPolicyName: "",
		}},
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	params := testUpdateVolumeParams(107374182400)
	params.SnapshotPolicy = &models.SnapshotPolicy{IsEnabled: true}

	err := runVcmUpdateVolumeWorkflow(t, "op-snap-no-sched", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, testUpdateNFSVolume(), testNode(), params)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no schedules provided")
}

func TestVcmUpdateVolumeWorkflow_SANResizeAndHostGroups(t *testing.T) {
	volume := testBlockVolume()
	volume.VolumeAttributes.ExternalUUID = "vol-uuid-1"
	volume.VolumeAttributes.SnapReserve = 10
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{
			{
				ProviderResponse: vsa.ProviderResponse{Name: "vol-1", ExternalUUID: "vol-uuid-1"},
				Size:             volume.SizeInBytes,
				SnapReserve:      10,
			},
			{
				ProviderResponse: vsa.ProviderResponse{Name: "vol-1", ExternalUUID: "vol-uuid-1"},
				Size:             volume.SizeInBytes * 2,
				SnapReserve:      10,
			},
			{
				ProviderResponse: vsa.ProviderResponse{Name: "vol-1", ExternalUUID: "vol-uuid-1"},
				Size:             volume.SizeInBytes * 2,
				SnapReserve:      10,
			},
		},
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{
		lunResponse: vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid"},
			SerialNumber:     "serial-1",
			Size:             volume.SizeInBytes,
		},
	}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	params := testUpdateVolumeParams(volume.SizeInBytes * 2)
	params.BlockDevices = []*common.BlockDevice{{HostGroupNames: []string{"hg-new"}}}

	err := runVcmUpdateVolumeWorkflow(t, "op-san-resize", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, volume, testNode(), params)

	require.NoError(t, err)
	assert.Equal(t, 1, update.updateLunCalls)
	assert.Equal(t, 1, vcmVolume.updateHostGroupsCalls)
	assert.Equal(t, 1, hostGroup.validateHostGroupCalls)
}

func TestVcmUpdateVolumeWorkflow_HostGroupsOnNonSANFails(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	params := testUpdateVolumeParams(107374182400)
	params.BlockDevices = []*common.BlockDevice{{HostGroupNames: []string{"hg-db-linux"}}}

	err := runVcmUpdateVolumeWorkflow(t, "op-nfs-hg", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, testUpdateNFSVolume(), testNode(), params)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "only supported for SAN volumes")
}

func TestVcmUpdateVolumeWorkflow_JunctionPathAndExportPolicy(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.FileProperties = &models.FileProperties{
		JunctionPath: "/new-share",
		ExportPolicy: &models.ExportPolicy{
			ExportRules: []*models.ExportRule{{
				AllowedClients: "10.0.0.0/8",
				AccessType:     models.ReadWrite,
				NFSv3:          true,
				Index:          1,
			}},
		},
	}

	err := runVcmUpdateVolumeWorkflow(t, "op-file-update", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, volume, testNode(), params)

	require.NoError(t, err)
}

func TestVcmUpdateVolumeWorkflow_DefaultExportPolicyIsNotMutated(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:               vsa.ProviderResponse{Name: volume.Name, ExternalUUID: "vol-uuid-1"},
			Size:                           volume.SizeInBytes,
			SnapshotDirectoryAccessEnabled: true,
			JunctionPath:                   "/nfs-vol",
			ExportPolicyName:               models.DefaultExportPolicyName,
		}},
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.FileProperties = &models.FileProperties{
		ExportPolicy: &models.ExportPolicy{
			ExportRules: []*models.ExportRule{{
				AllowedClients: "10.0.0.0/8",
				AccessType:     models.ReadWrite,
				NFSv3:          true,
				Index:          1,
			}},
		},
	}

	err := runVcmUpdateVolumeWorkflow(t, "op-default-export-policy", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, volume, testNode(), params)

	require.NoError(t, err)
	assert.Equal(t, 0, update.updateExportCalls)
}

func TestVcmUpdateVolumeWorkflow_UpdateVolumeFailure(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		updateVolumeErr: errors.New("ONTAP update failed"),
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	params := testUpdateVolumeParams(214748364800)
	err := runVcmUpdateVolumeWorkflow(t, "op-update-fail", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, testUpdateNFSVolume(), testNode(), params)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ONTAP update failed")
}

func TestVcmUpdateVolumeWorkflow_SvmFailure(t *testing.T) {
	job := &stubJobActivity{}
	err := runVcmUpdateVolumeWorkflow(t, "op-svm-fail", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{err: errors.New("SVM not found")}, &updateWorkflowVolumeActivity{}, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, testUpdateNFSVolume(), testNode(), testUpdateVolumeParams(214748364800))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SVM not found")
}

func TestVcmUpdateVolumeWorkflow_LunLookupForSnapReserveChangeOnNFS(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	snapReserve := int64(15)
	params := testUpdateVolumeParams(107374182400)
	params.SnapReserve = &snapReserve

	err := runVcmUpdateVolumeWorkflow(t, "op-lun-lookup", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, testUpdateNFSVolume(), testNode(), params)

	require.NoError(t, err)
}

func TestIsLunResizeRequired(t *testing.T) {
	volume := testBlockVolume()
	assert.True(t, isLunResizeRequired(&common.UpdateVolumeParams{QuotaInBytes: volume.SizeInBytes + 1}, volume))
	snapReserve := int64(20)
	assert.True(t, isLunResizeRequired(&common.UpdateVolumeParams{SnapReserve: &snapReserve}, volume))
	assert.False(t, isLunResizeRequired(nil, volume))
	assert.False(t, isLunResizeRequired(&common.UpdateVolumeParams{}, nil))
}

func TestVcmUpdateVolumeWorkflow_SANSnapReserveIncreaseValidation(t *testing.T) {
	volume := testBlockVolume()
	volume.VolumeAttributes.ExternalUUID = "vol-uuid-1"
	volume.VolumeAttributes.SnapReserve = 10
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse: vsa.ProviderResponse{Name: "vol-1", ExternalUUID: "vol-uuid-1"},
			Size:             volume.SizeInBytes,
			SnapReserve:      10,
		}},
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{
		lunResponse: vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid"},
		},
	}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	snapReserve := int64(50)
	params := &common.UpdateVolumeParams{SnapReserve: &snapReserve}

	err := runVcmUpdateVolumeWorkflow(t, "op-snap-reserve", func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, snapshotCreate, hostGroup)
	}, volume, testNode(), params)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Cannot increase SnapReserve")
}
