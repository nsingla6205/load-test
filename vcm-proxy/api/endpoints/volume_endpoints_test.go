package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

type stubVCMVolumeCreator struct {
	params *commonparams.CreateVolumeParams
	err    error
}

func (s *stubVCMVolumeCreator) CreateVolume(_ context.Context, params *commonparams.CreateVolumeParams) (*models.Volume, string, error) {
	s.params = params
	return nil, "", s.err
}

type stubVolumeOrchestrator struct {
	factory.OrchestratorFactory
	stub *stubVCMVolumeCreator
}

func newStubVolumeOrchestrator(t *testing.T, stub *stubVCMVolumeCreator) stubVolumeOrchestrator {
	t.Helper()
	return stubVolumeOrchestrator{
		OrchestratorFactory: factory.NewMockOrchestratorFactory(t),
		stub:                stub,
	}
}

func (s stubVolumeOrchestrator) CreateVolume(ctx context.Context, params *commonparams.CreateVolumeParams) (*models.Volume, string, error) {
	return s.stub.CreateVolume(ctx, params)
}

const testVolumeOperationID = "test-op-id"
const testVolumeUUID = "9760acf5-4638-11e7-9bdb-020073ca0001"

func testVolumeDeadline() time.Time {
	return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
}

func testVolumeCreateRequest() *vcmserver.VolumeV1 {
	return &vcmserver.VolumeV1{
		Name:        "my_volume",
		SizeInBytes: vcmserver.NewOptFloat64(107374182400),
		Protocols:   []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1ISCSI},
		BlockDevices: []vcmserver.BlockDeviceV1{
			{
				OsType:  vcmserver.BlockDeviceV1OsTypeLINUX,
				Igroups: []string{"hg-db-linux"},
			},
		},
	}
}

func TestPrepareCreateVolumeParams(t *testing.T) {
	_, err := prepareCreateVolumeParams(testVolumeCreateRequest(), testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)

	_, err = prepareCreateVolumeParams(nil, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request body is required")

	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("my-share")
	_, err = prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)

	req = testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3, vcmserver.ProtocolsV1ISCSI}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("my-share")
	_, err = prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)

	req = testVolumeCreateRequest()
	req.SnapshotPolicy = vcmserver.NewOptSnapshotPolicyV1(vcmserver.SnapshotPolicyV1{
		Enabled: vcmserver.NewOptNilBool(true),
	})
	_, err = prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SnapshotPolicy parameter must have at least one schedule populated")

	req = testVolumeCreateRequest()
	req.SnapReserve = vcmserver.NewOptFloat64(95)
	_, err = prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Maximum allowed snapshot-reserve-percentage")
}

func TestPrepareCreateVolumeParams_ValidatesHostGroups(t *testing.T) {
	req := testVolumeCreateRequest()
	req.BlockDevices[0].Igroups = []string{""}
	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "igroups must not contain empty names")

	req.BlockDevices[0].Igroups = []string{"   "}
	_, err = prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "igroups must not contain empty names")
}

func TestPrepareCreateVolumeParams_TruncatesSizeInBytes(t *testing.T) {
	req := testVolumeCreateRequest()
	req.SizeInBytes = vcmserver.NewOptFloat64(107374182400.5)
	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	assert.Equal(t, uint64(107374182400), params.QuotaInBytes)
}

func TestV1CreateVolume_AcceptsAndDelegatesToOrchestrator(t *testing.T) {
	ctx := context.Background()
	creator := &stubVCMVolumeCreator{}
	handler := &Handler{Orchestrator: newStubVolumeOrchestrator(t, creator)}

	result, err := handler.V1CreateVolume(ctx, testVolumeCreateRequest(), vcmserver.V1CreateVolumeParams{})
	require.NoError(t, err)
	accepted, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok)
	assert.False(t, accepted.Done)
	require.NotNil(t, creator.params)
	assert.Equal(t, accepted.Name, "/v1/operations/"+creator.params.OperationID)
	assert.Equal(t, "my_volume", creator.params.Name)
	assert.Empty(t, creator.params.CreationToken)
	assert.False(t, creator.params.Deadline.IsZero())
	require.NotNil(t, creator.params.BlockDevices)
	require.Len(t, *creator.params.BlockDevices, 1)
	assert.Equal(t, "LINUX", (*creator.params.BlockDevices)[0].OSType)
	assert.Equal(t, []string{"hg-db-linux"}, (*creator.params.BlockDevices)[0].HostGroupNames)
}

func TestV1CreateVolume_MapsShareNameToJunctionPath(t *testing.T) {
	ctx := context.Background()
	creator := &stubVCMVolumeCreator{}
	handler := &Handler{Orchestrator: newStubVolumeOrchestrator(t, creator)}

	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("my-share")

	_, err := handler.V1CreateVolume(ctx, req, vcmserver.V1CreateVolumeParams{})
	require.NoError(t, err)
	require.NotNil(t, creator.params)
	require.NotNil(t, creator.params.FileProperties)
	assert.Equal(t, "my-share", creator.params.CreationToken)
	assert.Equal(t, "/my-share", creator.params.FileProperties.JunctionPath)
	assert.Equal(t, "my_volume", creator.params.FileProperties.ExportPolicy.ExportPolicyName)
}

func TestV1CreateVolume_ReturnsBadRequestWhenValidationFails(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = nil

	handler := &Handler{Orchestrator: newStubVolumeOrchestrator(t, &stubVCMVolumeCreator{})}

	result, err := handler.V1CreateVolume(
		context.Background(),
		req,
		vcmserver.V1CreateVolumeParams{},
	)
	require.NoError(t, err)
	badRequest, ok := result.(*vcmserver.V1CreateVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, float64(400), badRequest.Code)
	assert.Contains(t, badRequest.Message, "protocols is required")
}

func TestV1CreateVolume_ReturnsInternalErrorWhenOrchestratorFails(t *testing.T) {
	creator := &stubVCMVolumeCreator{err: errors.New("boom")}
	handler := &Handler{Orchestrator: newStubVolumeOrchestrator(t, creator)}

	result, err := handler.V1CreateVolume(
		context.Background(),
		testVolumeCreateRequest(),
		vcmserver.V1CreateVolumeParams{},
	)
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1CreateVolumeInternalServerError{}, result)
}

func TestV1CreateVolume_RejectsWhenOrchestratorNotConfigured(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().CreateVolume(mock.Anything, mock.Anything).Return(nil, "", errors.New("not configured"))
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1CreateVolume(
		context.Background(),
		testVolumeCreateRequest(),
		vcmserver.V1CreateVolumeParams{},
	)
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1CreateVolumeInternalServerError{}, result)
}

func TestPrepareCreateVolumeParams_ValidationErrors(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Name = "bad-name"
	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Resource ID")

	req = testVolumeCreateRequest()
	req.SizeInBytes = vcmserver.OptFloat64{}
	_, err = prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SizeInBytes is required")

	req = testVolumeCreateRequest()
	req.SnapshotDirectory = vcmserver.NewOptBool(false)
	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	assert.False(t, params.SnapshotDirectory)
}

func TestPrepareCreateVolumeParams_QuotaOutOfRange(t *testing.T) {
	req := testVolumeCreateRequest()
	req.SizeInBytes = vcmserver.NewOptFloat64(1)

	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid volume capacity")
}

func TestPrepareCreateVolumeParams_ZeroQuota(t *testing.T) {
	req := testVolumeCreateRequest()
	req.SizeInBytes = vcmserver.NewOptFloat64(0)

	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid volume capacity")
}

func TestPrepareCreateVolumeParams_NFSUsesShareName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("nfs_share")

	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	assert.Equal(t, "nfs_share", params.CreationToken)
	require.NotNil(t, params.FileProperties)
	assert.Equal(t, "/nfs_share", params.FileProperties.JunctionPath)
}

func TestPrepareCreateVolumeParams_NFSRequiresShareName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil

	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shareName is required for NAS volumes")
}

func TestPrepareCreateVolumeParams_TrimsVolumeName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Name = "  my_volume  "

	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	assert.Equal(t, "my_volume", params.Name)
}

func TestPrepareCreateVolumeParams_RejectsWhitespaceVolumeName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Name = "   "

	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestPrepareCreateVolumeParams_TrimsShareName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("  nfs_share  ")

	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	assert.Equal(t, "nfs_share", params.CreationToken)
}

func TestPrepareCreateVolumeParams_TrimsIgroupNames(t *testing.T) {
	req := testVolumeCreateRequest()
	req.BlockDevices[0].Igroups = []string{"  hg-db-linux  "}

	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	require.NotNil(t, params.BlockDevices)
	assert.Equal(t, []string{"hg-db-linux"}, (*params.BlockDevices)[0].HostGroupNames)
}

func TestPrepareCreateVolumeParams_InvalidExportRules(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV4}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("nfs-vol")
	req.ExportPolicy = vcmserver.NewOptExportPolicyV1(vcmserver.ExportPolicyV1{
		Rules: []vcmserver.SimpleExportPolicyRuleV1{
			{
				AllowedClients: "10.0.0.0/8",
				AccessType:     vcmserver.SimpleExportPolicyRuleV1AccessTypeREADWRITE,
				Nfsv3:          vcmserver.NewOptNilBool(true),
			},
		},
	})

	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Cannot specify NFSv3 export policy rules for non-NFSv3 volume")
}

func TestPrepareCreateVolumeParams_NFSVolume(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("nfs_share")
	req.SecurityStyle = vcmserver.NewOptVolumeV1SecurityStyle(vcmserver.VolumeV1SecurityStyleUNIX)
	req.UnixPermissions = vcmserver.NewOptNilString("0755")
	req.ExportPolicy = vcmserver.NewOptExportPolicyV1(vcmserver.ExportPolicyV1{
		Rules: []vcmserver.SimpleExportPolicyRuleV1{
			{
				AllowedClients: "0.0.0.0/0",
				AccessType:     vcmserver.SimpleExportPolicyRuleV1AccessTypeREADWRITE,
				Nfsv3:          vcmserver.NewOptNilBool(true),
			},
		},
	})

	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	require.NotNil(t, params.FileProperties)
	assert.Equal(t, "nfs_share", params.CreationToken)
	assert.Equal(t, "/nfs_share", params.FileProperties.JunctionPath)
	assert.Equal(t, "my_volume", params.FileProperties.ExportPolicy.ExportPolicyName)
	require.NotNil(t, params.FileProperties.ExportPolicy)
	require.Len(t, params.FileProperties.ExportPolicy.ExportRules, 1)
	assert.True(t, params.FileProperties.ExportPolicy.ExportRules[0].UnixReadWrite)
}

func TestPrepareCreateVolumeParams_SMBVolume(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("smb_share")
	req.SecurityStyle = vcmserver.NewOptVolumeV1SecurityStyle(vcmserver.VolumeV1SecurityStyleNTFS)

	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
}

func TestPrepareCreateVolumeParams_RejectsSMBProtocol(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1SMB}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("smb_share")

	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SMB protocol volumes are not supported")
}

func TestPrepareCreateVolumeParams_RejectsDualProtocol(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3, vcmserver.ProtocolsV1SMB}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("dual_share")

	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Dual protocol (NFS and SMB) volumes are not supported")
}

func TestPrepareCreateVolumeParams_SANRejectsExportPolicy(t *testing.T) {
	req := testVolumeCreateRequest()
	req.ExportPolicy = vcmserver.NewOptExportPolicyV1(vcmserver.ExportPolicyV1{
		Rules: []vcmserver.SimpleExportPolicyRuleV1{
			{
				AllowedClients: "0.0.0.0/0",
				AccessType:     vcmserver.SimpleExportPolicyRuleV1AccessTypeREADWRITE,
				Nfsv3:          vcmserver.NewOptNilBool(true),
			},
		},
	})

	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exportPolicy is only supported for file protocol volumes")
}

func TestPrepareCreateVolumeParams_BlockDeviceValidation(t *testing.T) {
	req := testVolumeCreateRequest()
	req.BlockDevices = []vcmserver.BlockDeviceV1{
		{OsType: vcmserver.BlockDeviceV1OsTypeLINUX, Igroups: []string{"hg1"}},
		{OsType: vcmserver.BlockDeviceV1OsTypeLINUX, Igroups: []string{"hg2"}},
	}
	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Only one BlockDevice")

	req = testVolumeCreateRequest()
	req.BlockDevices = []vcmserver.BlockDeviceV1{{Igroups: []string{"hg1"}}}
	_, err = prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OS type is required")

	req = testVolumeCreateRequest()
	req.BlockDevices = nil
	_, err = prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Block Device is required")

	req = testVolumeCreateRequest()
	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	require.NotNil(t, params.BlockDevices)
	assert.Equal(t, "lun_my_volume", (*params.BlockDevices)[0].Name)
}

func TestPrepareCreateVolumeParams_SnapshotPolicySchedules(t *testing.T) {
	req := testVolumeCreateRequest()
	req.SnapshotPolicy = vcmserver.NewOptSnapshotPolicyV1(vcmserver.SnapshotPolicyV1{
		Enabled: vcmserver.NewOptNilBool(true),
		HourlySchedule: vcmserver.NewOptHourlyScheduleV1(vcmserver.HourlyScheduleV1{
			SnapshotsToKeep: vcmserver.NewOptFloat64(6),
			Minute:          vcmserver.NewOptFloat64(15),
		}),
	})

	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	require.NotNil(t, params.SnapshotPolicy)
	require.NotEmpty(t, params.SnapshotPolicy.Schedules)
}

func TestPrepareCreateVolumeParams_SnapReserveValidation(t *testing.T) {
	req := testVolumeCreateRequest()
	req.SnapReserve = vcmserver.NewOptFloat64(-1)
	_, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be negative")

	req = testVolumeCreateRequest()
	req.SnapReserve = vcmserver.NewOptFloat64(20)
	params, err := prepareCreateVolumeParams(req, testVolumeOperationID, testVolumeDeadline())
	require.NoError(t, err)
	assert.Equal(t, int64(20), params.SnapReserve)
}

func TestValidateAllSquashV1(t *testing.T) {
	err := validateAllSquashV1([]vcmserver.SimpleExportPolicyRuleV1{
		{
			AccessType: vcmserver.SimpleExportPolicyRuleV1AccessTypeREADWRITE,
			AllSquash:  vcmserver.NewOptNilBool(true),
		},
	})
	require.Error(t, err)
}

func TestExportPolicyV1_RequiresRules(t *testing.T) {
	t.Run("validate rejects nil rules", func(t *testing.T) {
		err := (&vcmserver.ExportPolicyV1{}).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rules")
	})

	t.Run("decode rejects missing rules field", func(t *testing.T) {
		var policy vcmserver.ExportPolicyV1
		err := policy.UnmarshalJSON([]byte(`{}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rules")
	})
}

func TestProtocolEnumsToStringsV1(t *testing.T) {
	out := protocolEnumsToStringsV1([]vcmserver.ProtocolsV1{
		vcmserver.ProtocolsV1ISCSI,
		vcmserver.ProtocolsV1NFSV3,
	})
	assert.Equal(t, []string{"ISCSI", "NFSV3"}, out)
}

func TestConvertFromSnapshotPolicyV1_Nil(t *testing.T) {
	result, err := convertFromSnapshotPolicyV1(nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestConvertFromSnapshotPolicyV1_AllSchedules(t *testing.T) {
	policy := &vcmserver.SnapshotPolicyV1{
		Enabled: vcmserver.NewOptNilBool(true),
		MonthlySchedule: vcmserver.NewOptMonthlyScheduleV1(vcmserver.MonthlyScheduleV1{
			SnapshotsToKeep: vcmserver.NewOptFloat64(3),
			DaysOfMonth:     vcmserver.NewOptString("1,15"),
		}),
		WeeklySchedule: vcmserver.NewOptWeeklyScheduleV1(vcmserver.WeeklyScheduleV1{
			SnapshotsToKeep: vcmserver.NewOptFloat64(4),
			Day:             vcmserver.NewOptString("Monday"),
		}),
		DailySchedule: vcmserver.NewOptDailyScheduleV1(vcmserver.DailyScheduleV1{
			SnapshotsToKeep: vcmserver.NewOptFloat64(5),
			Hour:            vcmserver.NewOptFloat64(4),
		}),
		HourlySchedule: vcmserver.NewOptHourlyScheduleV1(vcmserver.HourlyScheduleV1{
			SnapshotsToKeep: vcmserver.NewOptFloat64(6),
			Minute:          vcmserver.NewOptFloat64(10),
		}),
	}
	result, err := convertFromSnapshotPolicyV1(policy)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Schedules, 4)
}

func TestV1DescribeVolume_FileVolumeReturnsONTAPBackedFields(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		GetVolume(mock.Anything, testVolumeUUID, false).
		Return(&models.Volume{
			BaseModel:         models.BaseModel{UUID: "9760acf5-4638-11e7-9bdb-020073ca0001"},
			DisplayName:       "file-volume",
			QuotaInBytes:      100,
			UsedBytes:         25,
			SnapReserve:       5,
			SnapshotDirectory: true,
			FileProperties: &models.FileProperties{
				JunctionPath:    "/file-volume",
				SecurityStyle:   "unix",
				UnixPermissions: "0755",
			},
		}, nil)

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1DescribeVolume(context.Background(), vcmserver.V1DescribeVolumeParams{
		UUID: testVolumeUUID,
	})

	require.NoError(t, err)
	volume, ok := result.(*vcmserver.VolumeReadV1)
	require.True(t, ok)
	assert.Equal(t, "file-volume", volume.Name)
	assert.Equal(t, float64(100), volume.SizeInBytes.Value)
	assert.Equal(t, float64(25), volume.UsedBytes.Value)
	assert.Equal(t, float64(5), volume.SnapReserve.Value)
	assert.True(t, volume.SnapshotDirectory.Value)
	assert.Equal(t, vcmserver.VolumeReadV1SecurityStyleUNIX, volume.SecurityStyle.Value)
	assert.Equal(t, "0755", volume.UnixPermissions.Value)
	assert.Equal(t, "/file-volume", volume.ShareName.Value)
}

func TestV1DescribeVolume_BlockVolumeReturnsONTAPBackedFields(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetVolume(mock.Anything, testVolumeUUID, false).Return(&models.Volume{
		BaseModel:         models.BaseModel{UUID: "9760acf5-4638-11e7-9bdb-020073ca0001"},
		DisplayName:       "block-volume",
		QuotaInBytes:      100,
		UsedBytes:         10,
		SnapshotDirectory: false,
	}, nil)

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1DescribeVolume(context.Background(), vcmserver.V1DescribeVolumeParams{
		UUID: testVolumeUUID,
	})

	require.NoError(t, err)
	volume, ok := result.(*vcmserver.VolumeReadV1)
	require.True(t, ok)
	assert.Equal(t, "block-volume", volume.Name)
	assert.False(t, volume.ShareName.IsSet())
}

func TestV1DescribeVolume_NotFound(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetVolume(mock.Anything, mock.Anything, false).
		Return(nil, utilErrors.NewNotFoundErr("volume", nil))

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1DescribeVolume(context.Background(), vcmserver.V1DescribeVolumeParams{
		UUID: testVolumeUUID,
	})

	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1DescribeVolumeNotFound{}, result)
}

func TestV1DeleteVolume_AcceptsAndDelegatesToOrchestrator(t *testing.T) {
	ctx := context.Background()
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		DeleteVolume(mock.Anything, mock.MatchedBy(func(params *commonparams.DeleteVolumeParams) bool {
			return params.VolumeUUID == testVolumeUUID &&
				params.OperationID != "" &&
				!params.Deadline.IsZero()
		})).
		RunAndReturn(func(_ context.Context, params *commonparams.DeleteVolumeParams) (*models.Volume, string, error) {
			return nil, params.OperationID, nil
		})

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1DeleteVolume(ctx, vcmserver.V1DeleteVolumeParams{UUID: testVolumeUUID})
	require.NoError(t, err)

	accepted, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok)
	assert.Contains(t, accepted.Name, "/v1/operations/")
	assert.False(t, accepted.Done)
}

func TestV1DeleteVolume_TrimsUUID(t *testing.T) {
	ctx := context.Background()
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		DeleteVolume(mock.Anything, mock.MatchedBy(func(params *commonparams.DeleteVolumeParams) bool {
			return params.VolumeUUID == testVolumeUUID
		})).
		Return(nil, "op-id", nil)

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1DeleteVolume(ctx, vcmserver.V1DeleteVolumeParams{UUID: "  " + testVolumeUUID + "  "})
	require.NoError(t, err)
	_, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok)
}

func TestV1DeleteVolume_OrchestratorErrorReturns500(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		DeleteVolume(mock.Anything, mock.Anything).
		Return(nil, "", errors.New("workflow start failed"))

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1DeleteVolume(context.Background(), vcmserver.V1DeleteVolumeParams{UUID: testVolumeUUID})
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1DeleteVolumeInternalServerError{}, result)
}

func TestV1DeleteVolume_RejectsEmptyUUID(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1DeleteVolume(context.Background(), vcmserver.V1DeleteVolumeParams{UUID: ""})
	require.NoError(t, err)
	badRequest, ok := result.(*vcmserver.V1DeleteVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, "uuid is required", badRequest.Message)
}

func TestV1DeleteVolume_RejectsWhitespaceUUID(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1DeleteVolume(context.Background(), vcmserver.V1DeleteVolumeParams{UUID: "   "})
	require.NoError(t, err)
	badRequest, ok := result.(*vcmserver.V1DeleteVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, "uuid is required", badRequest.Message)
}
