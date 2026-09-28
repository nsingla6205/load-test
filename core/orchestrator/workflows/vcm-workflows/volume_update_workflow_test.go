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
	getVolumeDelay             time.Duration
	updateVolumeCalls          int
	updateVolumeErr            error
	updateVolumeDelay          time.Duration
	updateLunCalls             int
	updateLunErr               error
	updateLunDelay             time.Duration
	getSnapshotPolicy          *datamodel.SnapshotPolicy
	getSnapshotErr             error
	getSnapshotDelay           time.Duration
	updateSnapshotErr          error
	lastUpdatingSnapshotPolicy *datamodel.SnapshotPolicy
	updateJunctionErr          error
	updateJunctionDelay        time.Duration
	getExportPolicy            *datamodel.ExportPolicy
	getExportErr               error
	getExportDelay             time.Duration
	updateExportCalls          int
	lastExportName             string
	updateExportErr            error
}

func (s *updateWorkflowVolumeActivity) GetVolumeFromONTAP(_ context.Context, volume *datamodel.Volume, _ *models.Node) (*vsa.VolumeResponse, error) {
	if s.getVolumeDelay > 0 {
		time.Sleep(s.getVolumeDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getVolumeErr != nil {
		return nil, nonRetryableTestErr(s.getVolumeErr.Error())
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
	if s.updateVolumeDelay > 0 {
		time.Sleep(s.updateVolumeDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateVolumeCalls++
	if s.updateVolumeErr != nil {
		return nonRetryableTestErr(s.updateVolumeErr.Error())
	}
	return nil
}

func (s *updateWorkflowVolumeActivity) UpdateLun(context.Context, *datamodel.Volume, *vsa.VolumeResponse, *models.Node) error {
	if s.updateLunDelay > 0 {
		time.Sleep(s.updateLunDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateLunCalls++
	if s.updateLunErr != nil {
		return nonRetryableTestErr(s.updateLunErr.Error())
	}
	return nil
}

func (s *updateWorkflowVolumeActivity) GetSnapshotPolicyInONTAP(context.Context, *models.Node, string) (*datamodel.SnapshotPolicy, error) {
	if s.getSnapshotDelay > 0 {
		time.Sleep(s.getSnapshotDelay)
	}
	if s.getSnapshotErr != nil {
		return nil, nonRetryableTestErr(s.getSnapshotErr.Error())
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
	if s.updateSnapshotErr != nil {
		return nonRetryableTestErr(s.updateSnapshotErr.Error())
	}
	return nil
}

func (s *updateWorkflowVolumeActivity) UpdateJunctionPathInONTAP(context.Context, *datamodel.Volume, string, *models.Node) error {
	if s.updateJunctionDelay > 0 {
		time.Sleep(s.updateJunctionDelay)
	}
	if s.updateJunctionErr != nil {
		return nonRetryableTestErr(s.updateJunctionErr.Error())
	}
	return nil
}

func (s *updateWorkflowVolumeActivity) GetExportPolicyInONTAP(context.Context, *models.Node, string, string) (*datamodel.ExportPolicy, error) {
	if s.getExportDelay > 0 {
		time.Sleep(s.getExportDelay)
	}
	if s.getExportErr != nil {
		return nil, nonRetryableTestErr(s.getExportErr.Error())
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
	if s.updateExportErr != nil {
		return nonRetryableTestErr(s.updateExportErr.Error())
	}
	return nil
}

type updateWorkflowVCMVolumeActivity struct {
	lunResponse           vsa.LunResponse
	lunErr                error
	lunDelay              time.Duration
	populateDelay         time.Duration
	populateErr           error
	updateHostGroupsCalls int
	updateHostGroupsErr   error
	enrichmentErr         error
	enrichmentDelay       time.Duration
}

func (s *updateWorkflowVCMVolumeActivity) PopulateVolumeFromONTAPResponse(ctx context.Context, volume *datamodel.Volume, volResponse *vsa.VolumeResponse) (*datamodel.Volume, error) {
	if s.populateDelay > 0 {
		time.Sleep(s.populateDelay)
	}
	if s.populateErr != nil {
		return nil, nonRetryableTestErr(s.populateErr.Error())
	}
	return activities.VolumeActivity{}.PopulateVolumeFromONTAPResponse(ctx, volume, volResponse)
}

func (s *updateWorkflowVCMVolumeActivity) GetLunInONTAP(context.Context, string, string, string, *models.Node) (vsa.LunResponse, error) {
	if s.lunDelay > 0 {
		time.Sleep(s.lunDelay)
	}
	if s.lunErr != nil {
		return vsa.LunResponse{}, s.lunErr
	}
	return s.lunResponse, nil
}

func (s *updateWorkflowVCMVolumeActivity) UpdateBlockDeviceHostGroups(context.Context, vsa.LunResponse, string, []string, *models.Node) error {
	s.updateHostGroupsCalls++
	if s.updateHostGroupsErr != nil {
		return nonRetryableTestErr(s.updateHostGroupsErr.Error())
	}
	return nil
}

func (s *updateWorkflowVCMVolumeActivity) BuildVolumeOperationResponseEnrichment(context.Context, *vsa.VolumeResponse, string, *models.Node) (*activities.VolumeOperationEnrichment, error) {
	if s.enrichmentDelay > 0 {
		time.Sleep(s.enrichmentDelay)
	}
	if s.enrichmentErr != nil {
		return nil, nonRetryableTestErr(s.enrichmentErr.Error())
	}
	return &activities.VolumeOperationEnrichment{}, nil
}

type updateWorkflowSnapshotCreateActivity struct {
	calls int
	err   error
	delay time.Duration
}

func (s *updateWorkflowSnapshotCreateActivity) CreateSnapshotPolicyInONTAP(context.Context, *datamodel.Volume, *models.Node) error {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.calls++
	if s.err != nil {
		return nonRetryableTestErr(s.err.Error())
	}
	return nil
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

	var deadlineOffset time.Duration
	if params != nil && !params.Deadline.IsZero() {
		deadlineOffset = time.Until(params.Deadline)
	}

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
	if params != nil && !params.Deadline.IsZero() {
		params.Deadline = time.Now().UTC().Add(deadlineOffset)
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

func TestVcmUpdateVolumeWorkflow_MarkProcessingFails(t *testing.T) {
	job := &stubJobActivity{
		failOnState: vcmdb.JobStateProcessing,
		failErr:     errors.New("store unavailable"),
	}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, &updateWorkflowVolumeActivity{}, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, testUpdateNFSVolume(), testNode(), testUpdateVolumeParams(214748364800))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store unavailable")
}

func TestVcmUpdateVolumeWorkflow_GetVolumeFails(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{getVolumeErr: errors.New("get volume failed")}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, testUpdateNFSVolume(), testNode(), testUpdateVolumeParams(214748364800))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get volume failed")
}

func TestVcmUpdateVolumeWorkflow_PopulateFails(t *testing.T) {
	job := &stubJobActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{populateErr: errors.New("populate failed")}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, &updateWorkflowVolumeActivity{}, vcmVolume, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, testUpdateNFSVolume(), testNode(), testUpdateVolumeParams(214748364800))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "populate failed")
}

func TestVcmUpdateVolumeWorkflow_LunLookupHardFailure(t *testing.T) {
	job := &stubJobActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{lunErr: nonRetryableTestErr("lun lookup failed")}
	params := testUpdateVolumeParams(214748364800)
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, &updateWorkflowVolumeActivity{}, vcmVolume, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, testUpdateNFSVolume(), testNode(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lun lookup failed")
}

func TestVcmUpdateVolumeWorkflow_CreateSnapshotPolicyFails(t *testing.T) {
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:   vsa.ProviderResponse{Name: "nfs-vol", ExternalUUID: "vol-uuid-1"},
			Size:               107374182400,
			SnapshotPolicyName: models.DefaultONTAPSnapshotPolicyName,
		}},
	}
	snapshotCreate := &updateWorkflowSnapshotCreateActivity{err: errors.New("create snapshot policy failed")}
	params := testUpdateVolumeParams(107374182400)
	params.SnapshotPolicy = &models.SnapshotPolicy{
		IsEnabled: true,
		Schedules: []*models.SnapshotPolicySchedule{{Count: 3, SnapmirrorLabel: "daily", Schedule: &models.Schedule{Hours: []int{1}}}},
	}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, snapshotCreate, &stubVCMHostGroupActivity{})
	}, testUpdateNFSVolume(), testNode(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create snapshot policy failed")
}

func TestVcmUpdateVolumeWorkflow_GetSnapshotPolicyFails(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:   vsa.ProviderResponse{Name: volume.Name, ExternalUUID: "vol-uuid-1"},
			Size:               volume.SizeInBytes,
			SnapshotPolicyName: volume.Name,
		}},
		getSnapshotErr: errors.New("get snapshot policy failed"),
	}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.SnapshotPolicy = &models.SnapshotPolicy{IsEnabled: false}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get snapshot policy failed")
}

func TestVcmUpdateVolumeWorkflow_UpdateSnapshotPolicyFails(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:   vsa.ProviderResponse{Name: volume.Name, ExternalUUID: "vol-uuid-1"},
			Size:               volume.SizeInBytes,
			SnapshotPolicyName: volume.Name,
		}},
		updateSnapshotErr: errors.New("update snapshot policy failed"),
	}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.SnapshotPolicy = &models.SnapshotPolicy{IsEnabled: false}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update snapshot policy failed")
}

func TestVcmUpdateVolumeWorkflow_UpdateLunFails(t *testing.T) {
	volume := testBlockVolume()
	volume.VolumeAttributes.ExternalUUID = "vol-uuid-1"
	volume.VolumeAttributes.SnapReserve = 10
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{
			{ProviderResponse: vsa.ProviderResponse{Name: "vol-1", ExternalUUID: "vol-uuid-1"}, Size: volume.SizeInBytes, SnapReserve: 10},
			{ProviderResponse: vsa.ProviderResponse{Name: "vol-1", ExternalUUID: "vol-uuid-1"}, Size: volume.SizeInBytes * 2, SnapReserve: 10},
		},
		updateLunErr: errors.New("update lun failed"),
	}
	vcmVolume := &updateWorkflowVCMVolumeActivity{
		lunResponse: vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid"}, Size: volume.SizeInBytes},
	}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, vcmVolume, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), testUpdateVolumeParams(volume.SizeInBytes*2))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update lun failed")
}

func TestVcmUpdateVolumeWorkflow_ValidateHostGroupsFails(t *testing.T) {
	volume := testBlockVolume()
	volume.VolumeAttributes.ExternalUUID = "vol-uuid-1"
	job := &stubJobActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{
		lunResponse: vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid"}},
	}
	hostGroup := &stubVCMHostGroupActivity{validateHostGroupErr: errors.New("host group missing")}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.BlockDevices = []*common.BlockDevice{{HostGroupNames: []string{"hg-new"}}}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, &updateWorkflowVolumeActivity{}, vcmVolume, &updateWorkflowSnapshotCreateActivity{}, hostGroup)
	}, volume, testNode(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host group missing")
}

func TestVcmUpdateVolumeWorkflow_UpdateHostGroupsFails(t *testing.T) {
	volume := testBlockVolume()
	volume.VolumeAttributes.ExternalUUID = "vol-uuid-1"
	job := &stubJobActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{
		lunResponse:         vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid"}},
		updateHostGroupsErr: errors.New("update host groups failed"),
	}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.BlockDevices = []*common.BlockDevice{{HostGroupNames: []string{"hg-new"}}}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, &updateWorkflowVolumeActivity{}, vcmVolume, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update host groups failed")
}

func TestVcmUpdateVolumeWorkflow_UpdateJunctionPathFails(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{updateJunctionErr: errors.New("junction update failed")}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.FileProperties = &models.FileProperties{JunctionPath: "/new-share"}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "junction update failed")
}

func TestVcmUpdateVolumeWorkflow_GetExportPolicyFails(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{getExportErr: errors.New("get export policy failed")}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.FileProperties = &models.FileProperties{
		ExportPolicy: &models.ExportPolicy{ExportRules: []*models.ExportRule{{AllowedClients: "10.0.0.0/8", AccessType: models.ReadWrite, NFSv3: true, Index: 1}}},
	}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get export policy failed")
}

func TestVcmUpdateVolumeWorkflow_UpdateExportPolicyFails(t *testing.T) {
	volume := testUpdateNFSVolume()
	job := &stubJobActivity{}
	update := &updateWorkflowVolumeActivity{updateExportErr: errors.New("update export policy failed")}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.FileProperties = &models.FileProperties{
		ExportPolicy: &models.ExportPolicy{ExportRules: []*models.ExportRule{{AllowedClients: "10.0.0.0/8", AccessType: models.ReadWrite, NFSv3: true, Index: 1}}},
	}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update export policy failed")
}

func TestVcmUpdateVolumeWorkflow_RefreshVolumeFails(t *testing.T) {
	job := &stubJobActivity{}
	failing := &updateGetVolumeNthFail{failOnCall: 2, failErr: errors.New("refresh volume failed")}
	update := &updateWorkflowVolumeActivity{}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity((&stubSvmActivity{}).GetSvmByName)
		reg.RegisterActivity(failing.GetVolumeFromONTAP)
		reg.RegisterActivity((&updateWorkflowVCMVolumeActivity{}).PopulateVolumeFromONTAPResponse)
		reg.RegisterActivity((&updateWorkflowVCMVolumeActivity{}).BuildVolumeOperationResponseEnrichment)
		reg.RegisterActivity((&updateWorkflowVCMVolumeActivity{}).GetLunInONTAP)
		reg.RegisterActivity((&updateWorkflowVCMVolumeActivity{}).UpdateBlockDeviceHostGroups)
		reg.RegisterActivity((&updateWorkflowSnapshotCreateActivity{}).CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity(update.GetSnapshotPolicyInONTAP)
		reg.RegisterActivity(update.UpdateSnapshotPolicyInOntap)
		reg.RegisterActivity(update.UpdateVolumeInONTAP)
		reg.RegisterActivity(update.UpdateLun)
		reg.RegisterActivity(update.UpdateJunctionPathInONTAP)
		reg.RegisterActivity(update.GetExportPolicyInONTAP)
		reg.RegisterActivity(update.UpdateExportPolicyRulesInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
	}, testUpdateNFSVolume(), testNode(), testUpdateVolumeParams(214748364800))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refresh volume failed")
}

type updateGetVolumeNthFail struct {
	mu         sync.Mutex
	calls      int
	failOnCall int
	failErr    error
}

func (s *updateGetVolumeNthFail) GetVolumeFromONTAP(_ context.Context, volume *datamodel.Volume, _ *models.Node) (*vsa.VolumeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == s.failOnCall {
		return nil, nonRetryableTestErr(s.failErr.Error())
	}
	return &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{Name: volume.Name, ExternalUUID: "vol-uuid-1"},
		Size:                           volume.SizeInBytes,
		UsedBytes:                      1024,
		SnapReserve:                    10,
		SnapshotDirectoryAccessEnabled: true,
		SnapshotPolicyName:             models.DefaultONTAPSnapshotPolicyName,
		JunctionPath:                   "/nfs-vol",
		ExportPolicyName:               "nfs-vol",
	}, nil
}

func TestVcmUpdateVolumeWorkflow_BuildEnrichmentFails(t *testing.T) {
	job := &stubJobActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{enrichmentErr: errors.New("enrichment failed")}
	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, job, &stubSvmActivity{}, &updateWorkflowVolumeActivity{}, vcmVolume, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, testUpdateNFSVolume(), testNode(), testUpdateVolumeParams(214748364800))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enrichment failed")
}

func TestVcmUpdateVolumeWorkflow_DeadlineChecks(t *testing.T) {
	tests := []struct {
		name           string
		jobDelay       time.Duration
		svmDelay       time.Duration
		getVolDelay    time.Duration
		deadlineOffset time.Duration
		paramsFn       func() *common.UpdateVolumeParams
	}{
		{name: "before mark-processing", deadlineOffset: -time.Second, paramsFn: func() *common.UpdateVolumeParams { return testUpdateVolumeParams(214748364800) }},
		{name: "before get-svm", jobDelay: 100 * time.Millisecond, deadlineOffset: 50 * time.Millisecond, paramsFn: func() *common.UpdateVolumeParams { return testUpdateVolumeParams(214748364800) }},
		{name: "before get-volume", svmDelay: 1500 * time.Millisecond, deadlineOffset: time.Second, paramsFn: func() *common.UpdateVolumeParams { return testUpdateVolumeParams(214748364800) }},
		{name: "before populate-volume", getVolDelay: 1500 * time.Millisecond, deadlineOffset: time.Second, paramsFn: func() *common.UpdateVolumeParams { return testUpdateVolumeParams(214748364800) }},
		{name: "before update-volume", getVolDelay: 300 * time.Millisecond, deadlineOffset: 150 * time.Millisecond, paramsFn: func() *common.UpdateVolumeParams { return testUpdateVolumeParams(214748364800) }},
		{name: "before refresh-volume", getVolDelay: 400 * time.Millisecond, deadlineOffset: 200 * time.Millisecond, paramsFn: func() *common.UpdateVolumeParams {
			// No size change so IsVolumeUpdateRequired may be false; still hits refresh at end after populate.
			return testUpdateVolumeParams(107374182400)
		}},
		{name: "before build-response", getVolDelay: 500 * time.Millisecond, deadlineOffset: 250 * time.Millisecond, paramsFn: func() *common.UpdateVolumeParams { return testUpdateVolumeParams(107374182400) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &stubJobActivity{processingDelay: tt.jobDelay}
			svm := &stubSvmActivity{delay: tt.svmDelay}
			update := &updateWorkflowVolumeActivity{getVolumeDelay: tt.getVolDelay}
			params := tt.paramsFn()
			params.Deadline = time.Now().UTC().Add(tt.deadlineOffset)

			err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
				registerUpdateVolumeActivities(reg, job, svm, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
			}, testUpdateNFSVolume(), testNode(), params)

			assertOperationDeadlineExceeded(t, err)
		})
	}
}

func TestVcmUpdateVolumeWorkflow_DeadlineBeforeCreateSnapshotPolicy(t *testing.T) {
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:   vsa.ProviderResponse{Name: "nfs-vol", ExternalUUID: "vol-uuid-1"},
			Size:               107374182400,
			SnapshotPolicyName: models.DefaultONTAPSnapshotPolicyName,
		}},
	}
	params := testUpdateVolumeParams(107374182400)
	params.SnapshotPolicy = &models.SnapshotPolicy{
		IsEnabled: true,
		Schedules: []*models.SnapshotPolicySchedule{{Count: 3, SnapmirrorLabel: "daily", Schedule: &models.Schedule{Hours: []int{1}}}},
	}
	params.Deadline = time.Now().UTC().Add(3 * time.Second)

	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, &stubJobActivity{}, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{populateDelay: 4 * time.Second}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, testUpdateNFSVolume(), testNode(), params)
	assertOperationDeadlineExceeded(t, err)
}

func TestVcmUpdateVolumeWorkflow_DeadlineBeforeUpdateSnapshotPolicy(t *testing.T) {
	volume := testUpdateNFSVolume()
	update := &updateWorkflowVolumeActivity{
		getVolumeResponses: []*vsa.VolumeResponse{{
			ProviderResponse:   vsa.ProviderResponse{Name: volume.Name, ExternalUUID: "vol-uuid-1"},
			Size:               volume.SizeInBytes,
			SnapshotPolicyName: volume.Name,
		}},
		getSnapshotDelay: 4 * time.Second,
	}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.SnapshotPolicy = &models.SnapshotPolicy{IsEnabled: false}
	params.Deadline = time.Now().UTC().Add(3 * time.Second)

	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, &stubJobActivity{}, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)
	assertOperationDeadlineExceeded(t, err)
}

func TestVcmUpdateVolumeWorkflow_DeadlineBeforeGetLun(t *testing.T) {
	vcmVolume := &updateWorkflowVCMVolumeActivity{populateDelay: 4 * time.Second}
	params := testUpdateVolumeParams(214748364800)
	params.Deadline = time.Now().UTC().Add(3 * time.Second)

	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, &stubJobActivity{}, &stubSvmActivity{}, &updateWorkflowVolumeActivity{}, vcmVolume, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, testUpdateNFSVolume(), testNode(), params)
	assertOperationDeadlineExceeded(t, err)
}

func TestVcmUpdateVolumeWorkflow_DeadlineBeforeJunctionAndExport(t *testing.T) {
	volume := testUpdateNFSVolume()
	update := &updateWorkflowVolumeActivity{updateJunctionDelay: 200 * time.Millisecond}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.FileProperties = &models.FileProperties{
		JunctionPath: "/new-share",
		ExportPolicy: &models.ExportPolicy{ExportRules: []*models.ExportRule{{AllowedClients: "10.0.0.0/8", AccessType: models.ReadWrite, NFSv3: true, Index: 1}}},
	}
	params.Deadline = time.Now().UTC().Add(100 * time.Millisecond)

	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, &stubJobActivity{}, &stubSvmActivity{}, update, &updateWorkflowVCMVolumeActivity{}, &updateWorkflowSnapshotCreateActivity{}, &stubVCMHostGroupActivity{})
	}, volume, testNode(), params)
	assertOperationDeadlineExceeded(t, err)
}

func TestVcmUpdateVolumeWorkflow_DeadlineBeforeHostGroupUpdate(t *testing.T) {
	volume := testBlockVolume()
	volume.VolumeAttributes.ExternalUUID = "vol-uuid-1"
	vcmVolume := &updateWorkflowVCMVolumeActivity{
		lunResponse: vsa.LunResponse{ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid"}},
	}
	hostGroup := &stubVCMHostGroupActivity{validateHostGroupDelay: 200 * time.Millisecond}
	params := testUpdateVolumeParams(volume.SizeInBytes)
	params.BlockDevices = []*common.BlockDevice{{HostGroupNames: []string{"hg-new"}}}
	params.Deadline = time.Now().UTC().Add(100 * time.Millisecond)

	err := runVcmUpdateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		registerUpdateVolumeActivities(reg, &stubJobActivity{}, &stubSvmActivity{}, &updateWorkflowVolumeActivity{}, vcmVolume, &updateWorkflowSnapshotCreateActivity{}, hostGroup)
	}, volume, testNode(), params)
	assertOperationDeadlineExceeded(t, err)
}
