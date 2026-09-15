package vcm_workflows

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

const volumeDeleteWorkflowTestTimeout = 30 * time.Second

type stubVolumeDeleteActivity struct {
	deleteErr                       error
	deleteVolumeInvoked             bool
	deleteExportPolicyErr           error
	deleteExportPolicyInvoked       bool
	deleteSnapshotPolicyErr         error
	deleteSnapshotPolicyInvoked     bool
	deletedSnapshotPolicyVolumeName string
}

func (s *stubVolumeDeleteActivity) DeleteVolumeInONTAP(_ context.Context, _, _ string, _ *models.Node) error {
	s.deleteVolumeInvoked = true
	return s.deleteErr
}

func (s *stubVolumeDeleteActivity) DeleteExportPolicy(_ context.Context, _ *datamodel.Volume, _ *models.Node) error {
	s.deleteExportPolicyInvoked = true
	return s.deleteExportPolicyErr
}

func (s *stubVolumeDeleteActivity) DeleteSnapshotPolicyInONTAP(_ context.Context, volumeName string, _ *models.Node) error {
	s.deleteSnapshotPolicyInvoked = true
	s.deletedSnapshotPolicyVolumeName = volumeName
	return s.deleteSnapshotPolicyErr
}

type stubVolumeUpdateActivity struct {
	externalUUID       string
	snapshotPolicyName string
	exportPolicyName   string
	volumeResponse     *vsa.VolumeResponse
	err                error
}

func (s *stubVolumeUpdateActivity) GetVolumeFromONTAP(_ context.Context, volume *datamodel.Volume, _ *models.Node) (*vsa.VolumeResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.volumeResponse != nil {
		return s.volumeResponse, nil
	}
	name := volume.Name
	if name == "" {
		name = "vol-to-delete"
	}
	externalUUID := s.externalUUID
	if externalUUID == "" {
		externalUUID = "vol-uuid-1"
	}
	snapshotDirectory := true
	if volume.VolumeAttributes != nil {
		snapshotDirectory = volume.VolumeAttributes.SnapshotDirectory
	}
	resp := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: externalUUID, Name: name},
		Size:                           volume.SizeInBytes,
		UsedBytes:                      1409024,
		SnapReserve:                    20,
		SnapshotDirectoryAccessEnabled: snapshotDirectory,
		SnapshotPolicyName:             s.snapshotPolicyName,
		ExportPolicyName:               s.exportPolicyName,
		SecurityStyle:                  "unix",
		UnixPermissions:                "0755",
		JunctionPath:                   "/nfs-vol",
	}
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.FileProperties != nil {
		if volume.VolumeAttributes.FileProperties.JunctionPath != "" {
			resp.JunctionPath = volume.VolumeAttributes.FileProperties.JunctionPath
		}
		if volume.VolumeAttributes.FileProperties.SecurityStyle != "" {
			resp.SecurityStyle = volume.VolumeAttributes.FileProperties.SecurityStyle
		}
		if volume.VolumeAttributes.FileProperties.UnixPermissions != "" {
			resp.UnixPermissions = volume.VolumeAttributes.FileProperties.UnixPermissions
		}
	}
	return resp, nil
}

func runVcmDeleteVolumeWorkflow(
	t *testing.T,
	instanceID string,
	register func(orchestrator.WorkflowRegistrar),
	volume *datamodel.Volume,
	node *models.Node,
) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), volumeDeleteWorkflowTestTimeout)
	defer cancel()

	runner, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := runner.Registrar()
	reg.RegisterWorkflow(VcmDeleteVolumeWorkflow)
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
	}, gowfbackend.FuncName(VcmDeleteVolumeWorkflow), volume, node)
	require.NoError(t, err)

	_, err = cclient.GetWorkflowResult[json.RawMessage](ctx, runner.Client(), wfi, volumeDeleteWorkflowTestTimeout)
	return err
}

func testDeleteVolume() *datamodel.Volume {
	return &datamodel.Volume{
		Svm: &datamodel.Svm{Name: testExpectedSvmName},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "vol-uuid-1",
			Protocols:    []string{"ISCSI"},
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{},
			},
		},
	}
}

func registerDeleteVolumeActivities(reg orchestrator.WorkflowRegistrar, job *stubJobActivity, svm *stubSvmActivity, update *stubVolumeUpdateActivity, deleteAct *stubVolumeDeleteActivity) {
	reg.RegisterActivity(job.UpdateJobProgress)
	reg.RegisterActivity(job.GetJob)
	reg.RegisterActivity(svm.GetSvmByName)
	reg.RegisterActivity(update.GetVolumeFromONTAP)
	reg.RegisterActivity(deleteAct.DeleteVolumeInONTAP)
	reg.RegisterActivity(deleteAct.DeleteExportPolicy)
	reg.RegisterActivity(deleteAct.DeleteSnapshotPolicyInONTAP)
}

func TestVcmDeleteVolumeWorkflow_InvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		volume *datamodel.Volume
	}{
		{
			name:   "nil volume",
			volume: nil,
		},
		{
			name: "missing volume identity",
			volume: &datamodel.Volume{
				Svm: &datamodel.Svm{Name: testExpectedSvmName},
				VolumeAttributes: &datamodel.VolumeAttributes{
					FileProperties: &datamodel.FileProperties{
						ExportPolicy: &datamodel.ExportPolicy{},
					},
				},
			},
		},
		{
			name: "missing SVM",
			volume: &datamodel.Volume{
				Name:             "vol-to-delete",
				VolumeAttributes: &datamodel.VolumeAttributes{},
			},
		},
		{
			name: "missing volume attributes",
			volume: &datamodel.Volume{
				Name: "vol-to-delete",
				Svm:  &datamodel.Svm{Name: testExpectedSvmName},
			},
		},
		{
			name: "missing file properties",
			volume: &datamodel.Volume{
				Name:             "vol-to-delete",
				Svm:              &datamodel.Svm{Name: testExpectedSvmName},
				VolumeAttributes: &datamodel.VolumeAttributes{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const operationID = "op-invalid-input"
			job := &stubJobActivity{}

			err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
				registerDeleteVolumeActivities(reg, job, &stubSvmActivity{}, &stubVolumeUpdateActivity{}, &stubVolumeDeleteActivity{})
			}, tt.volume, testNode())

			require.Error(t, err)
			assert.Contains(t, err.Error(), "missing volume/SVM attributes")
			calls := job.snapshot()
			require.Len(t, calls, 1)
			assert.Equal(t, vcmdb.JobStateError, calls[0].state)
		})
	}
}

func TestVcmDeleteVolumeWorkflow_Success(t *testing.T) {
	const operationID = "op-delete-success"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{externalUUID: "ontap-vol-uuid"}
	deleteAct := &stubVolumeDeleteActivity{}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.NoError(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 3)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[0].state)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[1].state)
	assert.JSONEq(t, `{"volumeName":"vol-to-delete"}`, calls[1].recoveryPayload)
	assert.Equal(t, vcmdb.JobStateDone, calls[2].state)
	assert.True(t, deleteAct.deleteExportPolicyInvoked)
	assert.True(t, deleteAct.deleteSnapshotPolicyInvoked)
	assert.Equal(t, "vol-to-delete", deleteAct.deletedSnapshotPolicyVolumeName)
}

func TestVcmDeleteVolumeWorkflow_GetVolumeFromONTAPFails(t *testing.T) {
	const operationID = "op-get-volume-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{err: errors.New("ONTAP connection timeout")}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, &stubVolumeDeleteActivity{})
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
}

func TestIsVolumeNotFoundOnONTAP(t *testing.T) {
	t.Parallel()

	assert.True(t, isVolumeNotFoundOnONTAP(utilErrors.NewNotFoundErr("volume", nillable.ToPointer("voliscsi01"))))
	assert.True(t, isVolumeNotFoundOnONTAP(errors.New("volume 'voliscsi01' not found")))
	assert.True(t, isVolumeNotFoundOnONTAP(errors.New("entry doesn't exist")))
	assert.True(t, isVolumeNotFoundOnONTAP(errors.New("volume 'voliscsi01' does not exist")))
	assert.False(t, isVolumeNotFoundOnONTAP(errors.New("certificate does not exist")))
	assert.False(t, isVolumeNotFoundOnONTAP(errors.New("ONTAP connection timeout")))
}

func TestVcmDeleteVolumeWorkflow_VolumeAlreadyDeletedOnONTAP(t *testing.T) {
	const operationID = "op-volume-already-deleted"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	volName := "vol-to-delete"
	update := &stubVolumeUpdateActivity{
		err: utilErrors.NewNotFoundErr("volume", nillable.ToPointer(volName)),
	}
	deleteAct := &stubVolumeDeleteActivity{}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ONTAP volume name could not be resolved")
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
	assert.False(t, deleteAct.deleteVolumeInvoked)
}

func TestVcmDeleteVolumeWorkflow_VolumeAlreadyDeletedOnONTAP_QuotedNameError(t *testing.T) {
	const operationID = "op-volume-already-deleted-quoted"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{err: errors.New("volume 'vol-to-delete' not found")}
	deleteAct := &stubVolumeDeleteActivity{}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ONTAP volume name could not be resolved")
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
}

func TestVcmDeleteVolumeWorkflow_VolumeNotFoundLoadsNameFromJobContext(t *testing.T) {
	const operationID = "op-volume-delete-retry-from-job"
	recoveryPayload, err := vcmdb.MarshalPayload(vcmdb.RecoveryPayload{VolumeName: "persisted-vol-name"})
	require.NoError(t, err)
	job := &stubJobActivity{recoveryPayload: recoveryPayload}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{
		err: utilErrors.NewNotFoundErr("volume", nillable.ToPointer("persisted-vol-name")),
	}
	deleteAct := &stubVolumeDeleteActivity{}

	err = runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.NoError(t, err)
	assert.Equal(t, "persisted-vol-name", deleteAct.deletedSnapshotPolicyVolumeName)
	assert.True(t, deleteAct.deleteExportPolicyInvoked)
	assert.True(t, deleteAct.deleteSnapshotPolicyInvoked)
}

func TestVcmDeleteVolumeWorkflow_VolumeNotFoundRunsCleanupByName(t *testing.T) {
	const operationID = "op-volume-delete-retry-cleanup"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{
		err: utilErrors.NewNotFoundErr("volume", nillable.ToPointer("vol-to-delete")),
	}
	deleteAct := &stubVolumeDeleteActivity{}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ONTAP volume name could not be resolved")
	assert.False(t, deleteAct.deleteVolumeInvoked)
}

func TestVcmDeleteVolumeWorkflow_GetVolumeFromONTAPEmptyName(t *testing.T) {
	const operationID = "op-get-volume-empty-name"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{
		volumeResponse: &vsa.VolumeResponse{
			ProviderResponse: vsa.ProviderResponse{ExternalUUID: "ontap-vol-uuid", Name: ""},
		},
	}
	deleteAct := &stubVolumeDeleteActivity{}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ONTAP volume name could not be resolved")
	calls := job.snapshot()
	require.Len(t, calls, 3)
	assert.Equal(t, vcmdb.JobStateProcessing, calls[1].state)
	assert.Equal(t, vcmdb.JobStateError, calls[2].state)
	assert.False(t, deleteAct.deleteVolumeInvoked)
}

func TestVcmDeleteVolumeWorkflow_DeleteVolumeInONTAPFails(t *testing.T) {
	const operationID = "op-delete-ontap-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{externalUUID: "ontap-vol-uuid"}
	deleteAct := &stubVolumeDeleteActivity{deleteErr: errors.New("ONTAP delete failed")}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 3)
	assert.Equal(t, vcmdb.JobStateError, calls[2].state)
}

func TestVcmDeleteVolumeWorkflow_GetSvmByNameFails(t *testing.T) {
	const operationID = "op-svm-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{err: errors.New("SVM not found")}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, &stubVolumeUpdateActivity{}, &stubVolumeDeleteActivity{})
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 2)
	assert.Equal(t, vcmdb.JobStateError, calls[1].state)
}

func TestVcmDeleteVolumeWorkflow_DeletesCustomExportPolicy(t *testing.T) {
	const operationID = "op-delete-export-policy"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{
		externalUUID:     "ontap-vol-uuid",
		exportPolicyName: "my-share-name",
	}
	deleteAct := &stubVolumeDeleteActivity{}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.NoError(t, err)
	assert.True(t, deleteAct.deleteExportPolicyInvoked)
}

func TestVcmDeleteVolumeWorkflow_DeletesCustomSnapshotPolicy(t *testing.T) {
	const operationID = "op-delete-snapshot-policy"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{
		externalUUID: "ontap-vol-uuid",
	}
	deleteAct := &stubVolumeDeleteActivity{}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.NoError(t, err)
	assert.True(t, deleteAct.deleteSnapshotPolicyInvoked)
}

func TestVcmDeleteVolumeWorkflow_DeleteSnapshotPolicyFails(t *testing.T) {
	const operationID = "op-delete-snapshot-policy-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{externalUUID: "ontap-vol-uuid"}
	deleteAct := &stubVolumeDeleteActivity{
		deleteSnapshotPolicyErr: errors.New("snapshot policy delete failed"),
	}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 3)
	assert.Equal(t, vcmdb.JobStateError, calls[2].state)
	assert.True(t, deleteAct.deleteSnapshotPolicyInvoked)
}

func TestVcmDeleteVolumeWorkflow_DeleteExportPolicyFails(t *testing.T) {
	const operationID = "op-delete-export-policy-fail"
	job := &stubJobActivity{}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{
		externalUUID:     "ontap-vol-uuid",
		exportPolicyName: "my-share-name",
	}
	deleteAct := &stubVolumeDeleteActivity{
		deleteExportPolicyErr: errors.New("export policy delete failed"),
	}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	calls := job.snapshot()
	require.Len(t, calls, 3)
	assert.Equal(t, vcmdb.JobStateError, calls[2].state)
	assert.True(t, deleteAct.deleteExportPolicyInvoked)
}

func TestVcmDeleteVolumeWorkflow_FinishJobUpdateFails(t *testing.T) {
	const operationID = "op-delete-finish-fails"
	job := &stubJobActivity{
		failOnState: vcmdb.JobStateDone,
		failErr:     errors.New("final job update failed"),
	}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{externalUUID: "ontap-vol-uuid"}
	deleteAct := &stubVolumeDeleteActivity{}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "final job update failed")
	assert.True(t, deleteAct.deleteVolumeInvoked)
}

func TestVcmDeleteVolumeWorkflow_InvalidRecoveryPayload(t *testing.T) {
	const operationID = "op-delete-invalid-recovery"
	job := &stubJobActivity{recoveryPayload: `{invalid`}
	svm := &stubSvmActivity{}
	update := &stubVolumeUpdateActivity{
		err: utilErrors.NewNotFoundErr("volume", nillable.ToPointer("vol-to-delete")),
	}
	deleteAct := &stubVolumeDeleteActivity{}

	err := runVcmDeleteVolumeWorkflow(t, operationID, func(reg orchestrator.WorkflowRegistrar) {
		registerDeleteVolumeActivities(reg, job, svm, update, deleteAct)
	}, testDeleteVolume(), testNode())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal payload")
	assert.False(t, deleteAct.deleteVolumeInvoked)
}
