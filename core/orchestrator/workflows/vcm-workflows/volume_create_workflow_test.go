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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

const volumeWorkflowTestTimeout = 30 * time.Second

type stubVolumeCreateBlockActivity struct {
	mu                  sync.Mutex
	createVolumeCalls   int
	createLunCalls      int
	createLunMapCalls   int
	snapshotPolicyCalls int
	createVolumeErr     error
	createLunErr        error
	createLunMapErr     error
	snapshotPolicyErr   error
	snapshotPolicyDelay time.Duration
	createVolumeDelay   time.Duration
	createLunDelay      time.Duration
}

func (s *stubVolumeCreateBlockActivity) CreateSnapshotPolicyInONTAP(context.Context, *datamodel.Volume, *models.Node) error {
	if s.snapshotPolicyDelay > 0 {
		time.Sleep(s.snapshotPolicyDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotPolicyCalls++
	if s.snapshotPolicyErr != nil {
		return nonRetryableTestErr(s.snapshotPolicyErr.Error())
	}
	return nil
}

func (s *stubVolumeCreateBlockActivity) CreateVolumeInONTAP(context.Context, *datamodel.Volume, *models.Node, *datamodel.Snapshot, *datamodel.Backup, *models.AggregateDistributionResult) (*vsa.VolumeResponse, error) {
	if s.createVolumeDelay > 0 {
		time.Sleep(s.createVolumeDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createVolumeCalls++
	if s.createVolumeErr != nil {
		return nil, nonRetryableTestErr(s.createVolumeErr.Error())
	}
	return &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "vol-uuid-1"},
		AvailableSpace:   1000,
	}, nil
}

type stubVCMHostGroupActivity struct {
	mu                     sync.Mutex
	validateHostGroupCalls int
	validateHostGroupErr   error
	validateHostGroupDelay time.Duration
}

func (s *stubVCMHostGroupActivity) ValidateHostGroupsExistInONTAP(_ context.Context, _ []string, _ string, _ *models.Node) error {
	if s.validateHostGroupDelay > 0 {
		time.Sleep(s.validateHostGroupDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validateHostGroupCalls++
	if s.validateHostGroupErr != nil {
		return nonRetryableTestErr(s.validateHostGroupErr.Error())
	}
	return nil
}

func registerBlockVolumeCreateActivities(
	reg orchestrator.WorkflowRegistrar,
	job *stubJobActivity,
	svm *stubSvmActivity,
	volume *stubVolumeCreateBlockActivity,
	hostGroup *stubVCMHostGroupActivity,
) {
	reg.RegisterActivity(job.UpdateJobProgress)
	reg.RegisterActivity(svm.GetSvmByName)
	reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
	reg.RegisterActivity(hostGroup.ValidateHostGroupsExistInONTAP)
	reg.RegisterActivity(volume.CreateVolumeInONTAP)
	reg.RegisterActivity(volume.CreateLun)
	reg.RegisterActivity(volume.CreateLunMap)
	reg.RegisterActivity((&stubVolumeUpdateActivity{}).GetVolumeFromONTAP)
	registerVcmBuildVolumeOperationEnrichment(reg)
}

func registerVcmGetVolumeFromONTAP(reg orchestrator.WorkflowRegistrar) {
	reg.RegisterActivity((&stubVolumeUpdateActivity{}).GetVolumeFromONTAP)
	registerVcmBuildVolumeOperationEnrichment(reg)
}

func registerVcmBuildVolumeOperationEnrichment(reg orchestrator.WorkflowRegistrar) {
	reg.RegisterActivity((&updateWorkflowVCMVolumeActivity{}).BuildVolumeOperationResponseEnrichment)
}

func (s *stubVolumeCreateBlockActivity) CreateLun(context.Context, *datamodel.Volume, *models.Node, int64) (*vsa.LunResponse, error) {
	if s.createLunDelay > 0 {
		time.Sleep(s.createLunDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createLunCalls++
	if s.createLunErr != nil {
		return nil, nonRetryableTestErr(s.createLunErr.Error())
	}
	return &vsa.LunResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid-1"},
		SerialNumber:     "serial-1",
		Size:             1000,
	}, nil
}

func (s *stubVolumeCreateBlockActivity) CreateLunMap(context.Context, *datamodel.Volume, *common.CreateLunMapParams, *models.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createLunMapCalls++
	if s.createLunMapErr != nil {
		return nonRetryableTestErr(s.createLunMapErr.Error())
	}
	return nil
}

type stubVolumeCreateFileActivity struct {
	mu                  sync.Mutex
	clusterHealthCalls  int
	exportPolicyCalls   int
	snapshotPolicyCalls int
	createVolumeCalls   int
	clusterHealthy      bool
	clusterHealthErr    error
	exportPolicyErr     error
	createVolumeErr     error
	snapshotPolicyErr   error
	clusterHealthDelay  time.Duration
	exportPolicyDelay   time.Duration
	createVolumeDelay   time.Duration
}

func (s *stubVolumeCreateFileActivity) CreateSnapshotPolicyInONTAP(context.Context, *datamodel.Volume, *models.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotPolicyCalls++
	if s.snapshotPolicyErr != nil {
		return nonRetryableTestErr(s.snapshotPolicyErr.Error())
	}
	return nil
}

func (s *stubVolumeCreateFileActivity) GetOntapClusterHealth(context.Context, *models.Node) (*bool, error) {
	if s.clusterHealthDelay > 0 {
		time.Sleep(s.clusterHealthDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clusterHealthCalls++
	if s.clusterHealthErr != nil {
		return nil, nonRetryableTestErr(s.clusterHealthErr.Error())
	}
	healthy := s.clusterHealthy
	return &healthy, nil
}

func (s *stubVolumeCreateFileActivity) CreateExportPolicyInOntap(context.Context, *datamodel.Volume, *models.Node) error {
	if s.exportPolicyDelay > 0 {
		time.Sleep(s.exportPolicyDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exportPolicyCalls++
	if s.exportPolicyErr != nil {
		return nonRetryableTestErr(s.exportPolicyErr.Error())
	}
	return nil
}

func (s *stubVolumeCreateFileActivity) CreateVolumeInONTAP(context.Context, *datamodel.Volume, *models.Node, *datamodel.Snapshot, *datamodel.Backup, *models.AggregateDistributionResult) (*vsa.VolumeResponse, error) {
	if s.createVolumeDelay > 0 {
		time.Sleep(s.createVolumeDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createVolumeCalls++
	if s.createVolumeErr != nil {
		return nil, nonRetryableTestErr(s.createVolumeErr.Error())
	}
	return &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "vol-uuid-1"},
	}, nil
}

func (s *stubVolumeCreateBlockActivity) snapshot() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createVolumeCalls, s.createLunCalls, s.createLunMapCalls
}

func runVcmCreateVolumeWorkflow(
	t *testing.T,
	instanceID string,
	register func(orchestrator.WorkflowRegistrar),
	volume *datamodel.Volume,
	node *models.Node,
	params *common.CreateVolumeParams,
) error {
	t.Helper()

	deadlineOffset := time.Hour
	if params != nil && !params.Deadline.IsZero() {
		deadlineOffset = time.Until(params.Deadline)
	}

	ctx, cancel := context.WithTimeout(context.Background(), volumeWorkflowTestTimeout)
	defer cancel()

	runner, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := runner.Registrar()
	reg.RegisterWorkflow(VcmCreateVolumeWorkflow)
	reg.RegisterWorkflow(VcmPreBlockVolumeWorkflow)
	reg.RegisterWorkflow(VcmPreFileVolumeWorkflow)
	reg.RegisterWorkflow(VcmPostBlockVolumeWorkflow)
	reg.RegisterWorkflow(VcmPostFileVolumeWorkflow)
	reg.RegisterWorkflow(VcmPostFileVolumeWorkflowForSMB)
	register(reg)

	require.NoError(t, runner.Start(ctx))
	t.Cleanup(func() {
		cancel()
		_ = runner.Stop()
	})

	if instanceID == "" {
		instanceID = uuid.NewString()
	}
	expectedSvmName := ""
	if volume != nil && volume.Svm != nil {
		expectedSvmName = volume.Svm.Name
	}
	deadline := time.Now().UTC().Add(deadlineOffset)
	wfi, err := runner.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: instanceID,
	}, gowfbackend.FuncName(VcmCreateVolumeWorkflow), volume, common.WorkflowInputs{
		Node:            node,
		ExpectedSvmName: expectedSvmName,
		Deadline:        deadline,
	}, params)
	require.NoError(t, err)

	_, err = cclient.GetWorkflowResult[json.RawMessage](ctx, runner.Client(), wfi, volumeWorkflowTestTimeout)
	return err
}

func testBlockCreateVolumeParams() *common.CreateVolumeParams {
	return &common.CreateVolumeParams{
		OperationID: "op-volume-success",
		Name:        "vol-1",
		Protocols:   []string{utils.ProtocolISCSI},
		BlockDevices: &[]common.BlockDevice{
			{
				Name:           "lun_vol-1",
				OSType:         "LINUX",
				HostGroupNames: []string{"hg-db-linux"},
			},
		},
	}
}

func testBlockVolume() *datamodel.Volume {
	return &datamodel.Volume{
		Name:        "vol-1",
		SizeInBytes: 107374182400,
		Svm:         &datamodel.Svm{Name: testExpectedSvmName},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolISCSI},
			BlockDevices: &[]datamodel.BlockDevice{
				{
					Name:   "lun_vol-1",
					OSType: "LINUX",
					HostGroupDetails: []datamodel.HostGroupDetail{
						{HostGroupName: "hg-db-linux"},
					},
				},
			},
		},
	}
}

func testNFSVolume() *datamodel.Volume {
	return &datamodel.Volume{
		Name:        "nfs-vol",
		SizeInBytes: 107374182400,
		Svm:         &datamodel.Svm{Name: testExpectedSvmName},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{utils.ProtocolNFSv3},
			FileProperties: &datamodel.FileProperties{
				JunctionPath: "/nfs-vol",
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "nfs-vol",
					ExportRules: []*datamodel.ExportRule{
						{
							AllowedClients: "0.0.0.0/0",
							AccessType:     "READ_WRITE",
							NFSv3:          true,
							Index:          1,
							UnixReadWrite:  true,
						},
					},
				},
			},
		},
	}
}

func testSMBVolume() *datamodel.Volume {
	volume := testNFSVolume()
	volume.Name = "smb-vol"
	volume.VolumeAttributes.Protocols = []string{utils.ProtocolSMB}
	volume.VolumeAttributes.FileProperties.JunctionPath = "/smb-vol"
	volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName = "smb-vol"
	volume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules[0].NFSv3 = false
	volume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules[0].CIFS = true
	volume.VolumeAttributes.FileProperties.SecurityStyle = "ntfs"
	return volume
}

func testDualProtocolVolume() *datamodel.Volume {
	volume := testNFSVolume()
	volume.Name = "dual-vol"
	volume.VolumeAttributes.Protocols = []string{utils.ProtocolNFSv3, utils.ProtocolSMB}
	volume.VolumeAttributes.FileProperties.JunctionPath = "/dual-vol"
	volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName = "dual-vol"
	volume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules[0].CIFS = true
	volume.VolumeAttributes.FileProperties.SecurityStyle = "ntfs"
	return volume
}

func TestVcmCreateVolumeWorkflow_InvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		volume *datamodel.Volume
		node   *models.Node
		params *common.CreateVolumeParams
	}{
		{name: "nil volume", volume: nil, node: testNode(), params: testBlockCreateVolumeParams()},
		{name: "missing SVM", volume: &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{}}, node: testNode(), params: testBlockCreateVolumeParams()},
		{name: "missing volume attributes", volume: &datamodel.Volume{Svm: &datamodel.Svm{Name: testExpectedSvmName}}, node: testNode(), params: testBlockCreateVolumeParams()},
		{name: "nil node", volume: testBlockVolume(), node: nil, params: testBlockCreateVolumeParams()},
		{name: "nil create params", volume: testBlockVolume(), node: testNode(), params: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const operationID = "op-invalid-input"
			job := &stubJobActivity{}

			err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
				reg.RegisterActivity(job.UpdateJobProgress)
			}, tt.volume, tt.node, tt.params)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing volume, node, or create parameters")
			calls := job.snapshot()
			require.Len(t, calls, 1)
			assert.Equal(t, vcmdb.JobStateError, calls[0].state)
		})
	}
}

func TestVcmCreateVolumeWorkflow_Success(t *testing.T) {
	const operationID = "op-volume-success"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{}
	hostGroup := &stubVCMHostGroupActivity{}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerBlockVolumeCreateActivities(reg, job, svm, volume, hostGroup)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.NoError(t, err)
	assert.Equal(t, 1, svm.callCount())
	volCalls, lunCalls, mapCalls := volume.snapshot()
	assert.Equal(t, 1, volCalls)
	assert.Equal(t, 1, lunCalls)
	assert.Equal(t, 1, mapCalls)
	assert.Equal(t, 1, hostGroup.validateHostGroupCalls)

	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateDone, calls[1].state)
	assert.JSONEq(t, `{"uuid":"vol-uuid-1","name":"vol-1","sizeInBytes":107374182400,"usedBytes":1409024,"snapReserve":20,"snapshotDirectory":false,"securityStyle":"UNIX","shareName":"nfs-vol","unixPermissions":"0755"}`, calls[1].responsePayload)
}

func TestVcmCreateVolumeWorkflow_MissingHostGroupFailsBeforeVolumeCreate(t *testing.T) {
	const operationID = "op-missing-hostgroup"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{}
	hostGroup := &stubVCMHostGroupActivity{
		validateHostGroupErr: errors.New(`host group "hg-db-linux" not found on SVM "gcnv-test-svm-01"`),
	}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerBlockVolumeCreateActivities(reg, job, svm, volume, hostGroup)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), `host group "hg-db-linux" not found on SVM "gcnv-test-svm-01"`)
	volCalls, lunCalls, mapCalls := volume.snapshot()
	assert.Equal(t, 0, volCalls)
	assert.Equal(t, 0, lunCalls)
	assert.Equal(t, 0, mapCalls)
	assert.GreaterOrEqual(t, hostGroup.validateHostGroupCalls, 1)
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
	assert.Contains(t, calls[1].errorMessage, `host group "hg-db-linux" not found`)
}

func TestVcmCreateVolumeWorkflow_CreateVolumeFails(t *testing.T) {
	const operationID = "op-volume-create-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{createVolumeErr: errors.New("ONTAP volume create failed")}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
	assert.Contains(t, calls[1].errorMessage, "ONTAP volume create failed")
}

func TestVcmCreateVolumeWorkflow_CreateLunFailsWithoutRollback(t *testing.T) {
	const operationID = "op-lun-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{createLunErr: errors.New("lun create failed")}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	volCalls, lunCalls, mapCalls := volume.snapshot()
	assert.Equal(t, 1, volCalls)
	assert.GreaterOrEqual(t, lunCalls, 1)
	assert.Equal(t, 0, mapCalls)
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
}

func TestVcmCreateVolumeWorkflow_NFSVolumeSuccess(t *testing.T) {
	const operationID = "op-nfs-success"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateFileActivity{clusterHealthy: true}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.GetOntapClusterHealth)
		reg.RegisterActivity(volume.CreateExportPolicyInOntap)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		registerVcmGetVolumeFromONTAP(reg)
	}, testNFSVolume(), testNode(), &common.CreateVolumeParams{
		OperationID: operationID,
		Name:        "nfs-vol",
		Protocols:   []string{utils.ProtocolNFSv3},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, volume.clusterHealthCalls)
	assert.Equal(t, 1, volume.exportPolicyCalls)
	assert.Equal(t, 1, volume.createVolumeCalls)
}

func TestVcmCreateVolumeWorkflow_SMBVolumeSuccess(t *testing.T) {
	const operationID = "op-smb-success"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateFileActivity{clusterHealthy: true}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.GetOntapClusterHealth)
		reg.RegisterActivity(volume.CreateExportPolicyInOntap)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		registerVcmGetVolumeFromONTAP(reg)
	}, testSMBVolume(), testNode(), &common.CreateVolumeParams{
		OperationID: operationID,
		Name:        "smb-vol",
		Protocols:   []string{utils.ProtocolSMB},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, volume.createVolumeCalls)
}

func TestVcmCreateVolumeWorkflow_WithSnapshotPolicy(t *testing.T) {
	const operationID = "op-snapshot-success"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	blockVolume := testBlockVolume()
	blockVolume.SnapshotPolicy = &datamodel.SnapshotPolicy{
		Name:      "vol-1",
		IsEnabled: true,
		Schedules: []*datamodel.SnapshotPolicySchedule{
			{
				Count:           6,
				SnapmirrorLabel: "hourly",
				Minutes:         []int{15},
			},
		},
	}
	volume := &stubVolumeCreateBlockActivity{}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
		registerVcmGetVolumeFromONTAP(reg)
	}, blockVolume, testNode(), func() *common.CreateVolumeParams {
		params := testBlockCreateVolumeParams()
		params.OperationID = operationID
		return params
	}())

	require.NoError(t, err)
	assert.Equal(t, 1, volume.snapshotPolicyCalls)
}

func TestVcmCreateVolumeWorkflow_DualProtocolSuccess(t *testing.T) {
	const operationID = "op-dual-success"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateFileActivity{clusterHealthy: true}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.GetOntapClusterHealth)
		reg.RegisterActivity(volume.CreateExportPolicyInOntap)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		registerVcmGetVolumeFromONTAP(reg)
	}, testDualProtocolVolume(), testNode(), &common.CreateVolumeParams{
		OperationID: operationID,
		Name:        "dual-vol",
		Protocols:   []string{utils.ProtocolNFSv3, utils.ProtocolSMB},
	})

	require.NoError(t, err)
	assert.Equal(t, 1, volume.createVolumeCalls)
}

func TestVcmCreateVolumeWorkflow_GetSvmByNameFails(t *testing.T) {
	const operationID = "op-svm-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{err: errors.New("SVM not found")}
	volume := &stubVolumeCreateBlockActivity{}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
}

func TestVcmCreateVolumeWorkflow_MarkProcessingFails(t *testing.T) {
	const operationID = "op-processing-fail"
	job := &stubJobActivity{
		failOnState: vcmdb.JobStateProcessing,
		failErr:     errors.New("store unavailable"),
	}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	assert.Equal(t, 0, svm.callCount())
}

func TestVcmCreateVolumeWorkflow_SnapshotPolicyFails(t *testing.T) {
	const operationID = "op-snapshot-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{snapshotPolicyErr: errors.New("snapshot policy failed")}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
}

func TestVcmCreateVolumeWorkflow_CreateLunMapFails(t *testing.T) {
	const operationID = "op-lunmap-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{createLunMapErr: errors.New("lun map failed")}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	_, _, mapCalls := volume.snapshot()
	assert.GreaterOrEqual(t, mapCalls, 1)
}

func TestVcmCreateVolumeWorkflow_NFSClusterUnhealthy(t *testing.T) {
	const operationID = "op-nfs-unhealthy"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateFileActivity{clusterHealthy: false}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.GetOntapClusterHealth)
		reg.RegisterActivity(volume.CreateExportPolicyInOntap)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
	}, testNFSVolume(), testNode(), &common.CreateVolumeParams{
		OperationID: operationID,
		Name:        "nfs-vol",
		Protocols:   []string{utils.ProtocolNFSv3},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ONTAP cluster is down")
}

func TestVcmCreateVolumeWorkflow_NFSExportPolicyFails(t *testing.T) {
	const operationID = "op-export-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateFileActivity{
		clusterHealthy:  true,
		exportPolicyErr: errors.New("export policy failed"),
	}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.GetOntapClusterHealth)
		reg.RegisterActivity(volume.CreateExportPolicyInOntap)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
	}, testNFSVolume(), testNode(), &common.CreateVolumeParams{
		OperationID: operationID,
		Name:        "nfs-vol",
		Protocols:   []string{utils.ProtocolNFSv3},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "export policy failed")
}

func TestVcmCreateVolumeWorkflow_UnsupportedProtocol(t *testing.T) {
	const operationID = "op-bad-protocol"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{}

	badVolume := testBlockVolume()
	badVolume.VolumeAttributes.Protocols = []string{"INVALID"}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
	}, badVolume, testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
}

func TestVcmCreateVolumeWorkflow_MarkDoneFailsAfterSuccess(t *testing.T) {
	const operationID = "op-done-fail"
	job := &stubJobActivity{
		failOnState: vcmdb.JobStateDone,
		failErr:     errors.New("store unavailable on done"),
	}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{}

	err := runVcmCreateVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
		registerVcmGetVolumeFromONTAP(reg)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "store unavailable on done")
}

func TestVcmCreateVolumeWorkflow_GetVolumeFromONTAPFails(t *testing.T) {
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{}
	getVol := &stubVolumeUpdateActivity{err: nonRetryableTestErr("get volume failed")}

	err := runVcmCreateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
		reg.RegisterActivity(getVol.GetVolumeFromONTAP)
		registerVcmBuildVolumeOperationEnrichment(reg)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get volume failed")
	calls := job.snapshot()
	require.GreaterOrEqual(t, len(calls), 2)
	assert.Equal(t, vcmdb.JobStateError, calls[len(calls)-1].state)
}

func TestVcmCreateVolumeWorkflow_BuildEnrichmentFails(t *testing.T) {
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{}
	vcmVolume := &updateWorkflowVCMVolumeActivity{enrichmentErr: errors.New("enrichment failed")}

	err := runVcmCreateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
		reg.RegisterActivity((&stubVolumeUpdateActivity{}).GetVolumeFromONTAP)
		reg.RegisterActivity(vcmVolume.BuildVolumeOperationResponseEnrichment)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "enrichment failed")
}

func TestVcmCreateVolumeWorkflow_NilONTAPVolumeResponseFailsPayload(t *testing.T) {
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volume := &stubVolumeCreateBlockActivity{}
	getVol := &stubVolumeUpdateActivity{returnNil: true}

	err := runVcmCreateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(job.UpdateJobProgress)
		reg.RegisterActivity(svm.GetSvmByName)
		reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
		reg.RegisterActivity((&stubVCMHostGroupActivity{}).ValidateHostGroupsExistInONTAP)
		reg.RegisterActivity(volume.CreateVolumeInONTAP)
		reg.RegisterActivity(volume.CreateLun)
		reg.RegisterActivity(volume.CreateLunMap)
		reg.RegisterActivity(getVol.GetVolumeFromONTAP)
		registerVcmBuildVolumeOperationEnrichment(reg)
	}, testBlockVolume(), testNode(), testBlockCreateVolumeParams())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ONTAP volume response is required")
}

func TestVcmCreateVolumeWorkflow_DeadlineChecks(t *testing.T) {
	tests := []struct {
		name           string
		jobDelay       time.Duration
		svmDelay       time.Duration
		hostGroupDelay time.Duration
		snapshotDelay  time.Duration
		createVolDelay time.Duration
		createLunDelay time.Duration
		getVolDelay    time.Duration
		deadlineOffset time.Duration
	}{
		{name: "before mark-processing", deadlineOffset: -time.Second},
		{name: "before get-svm", jobDelay: 100 * time.Millisecond, deadlineOffset: 50 * time.Millisecond},
		{name: "before pre-provision", svmDelay: 100 * time.Millisecond, deadlineOffset: 50 * time.Millisecond},
		{name: "before create-snapshot-policy", hostGroupDelay: 3 * time.Second, deadlineOffset: 2 * time.Second},
		{name: "before create-volume", snapshotDelay: 3 * time.Second, deadlineOffset: 2 * time.Second},
		{name: "before post-provision", createVolDelay: 3 * time.Second, deadlineOffset: 2 * time.Second},
		{name: "before get-volume", createLunDelay: 12 * time.Second, deadlineOffset: 10 * time.Second},
		{name: "before build-response", getVolDelay: 6 * time.Second, deadlineOffset: 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := &stubJobActivity{processingDelay: tt.jobDelay}
			svm := &stubSvmActivity{delay: tt.svmDelay}
			volume := &stubVolumeCreateBlockActivity{
				snapshotPolicyDelay: tt.snapshotDelay,
				createVolumeDelay:   tt.createVolDelay,
				createLunDelay:      tt.createLunDelay,
			}
			hostGroup := &stubVCMHostGroupActivity{validateHostGroupDelay: tt.hostGroupDelay}
			getVol := &stubVolumeUpdateActivity{delay: tt.getVolDelay}
			params := testBlockCreateVolumeParams()
			params.Deadline = time.Now().UTC().Add(tt.deadlineOffset)

			err := runVcmCreateVolumeWorkflow(t, uuid.NewString(), func(reg orchestrator.WorkflowRegistrar) {
				reg.RegisterActivity(job.UpdateJobProgress)
				reg.RegisterActivity(svm.GetSvmByName)
				reg.RegisterActivity(volume.CreateSnapshotPolicyInONTAP)
				reg.RegisterActivity(hostGroup.ValidateHostGroupsExistInONTAP)
				reg.RegisterActivity(volume.CreateVolumeInONTAP)
				reg.RegisterActivity(volume.CreateLun)
				reg.RegisterActivity(volume.CreateLunMap)
				reg.RegisterActivity(getVol.GetVolumeFromONTAP)
				registerVcmBuildVolumeOperationEnrichment(reg)
			}, testBlockVolume(), testNode(), params)

			assertOperationDeadlineExceeded(t, err)
		})
	}
}
