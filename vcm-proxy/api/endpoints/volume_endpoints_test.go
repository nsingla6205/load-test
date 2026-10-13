package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
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

const testVolumeUUID = "9760acf5-4638-11e7-9bdb-020073ca0001"

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
	_, err := prepareCreateVolumeParams(testVolumeCreateRequest(), testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)

	_, err = prepareCreateVolumeParams(nil, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request body is required")

	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("my-share")
	_, err = prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)

	req = testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3, vcmserver.ProtocolsV1ISCSI}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("my-share")
	_, err = prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)

	req = testVolumeCreateRequest()
	req.SnapshotPolicy = vcmserver.NewOptSnapshotPolicyV1(vcmserver.SnapshotPolicyV1{
		Enabled: vcmserver.NewOptNilBool(true),
	})
	_, err = prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SnapshotPolicy parameter must have at least one schedule populated")

	req = testVolumeCreateRequest()
	req.SnapReserve = vcmserver.NewOptFloat64(95)
	_, err = prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Maximum allowed snapshot-reserve-percentage")
}

func TestPrepareCreateVolumeParams_ValidatesHostGroups(t *testing.T) {
	req := testVolumeCreateRequest()
	req.BlockDevices[0].Igroups = []string{""}
	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "igroups must not contain empty names")

	req.BlockDevices[0].Igroups = []string{"   "}
	_, err = prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "igroups must not contain empty names")
}

func TestPrepareCreateVolumeParams_TruncatesSizeInBytes(t *testing.T) {
	req := testVolumeCreateRequest()
	req.SizeInBytes = vcmserver.NewOptFloat64(107374182400.5)
	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	assert.Equal(t, uint64(107374182400), params.QuotaInBytes)
}

func TestV1CreateVolume_AcceptsAndDelegatesToOrchestrator(t *testing.T) {
	ctx := context.Background()
	creator := &stubVCMVolumeCreator{}
	handler := &Handler{Orchestrator: newStubVolumeOrchestrator(t, creator)}

	result, err := handler.V1CreateVolume(ctx, testVolumeCreateRequest(), testOperationRequestParams())
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

	_, err := handler.V1CreateVolume(ctx, req, testOperationRequestParams())
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
		testOperationRequestParams(),
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
		testOperationRequestParams(),
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
		testOperationRequestParams(),
	)
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1CreateVolumeInternalServerError{}, result)
}

func TestPrepareCreateVolumeParams_ValidationErrors(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Name = "bad-name"
	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Resource ID")

	req = testVolumeCreateRequest()
	req.SizeInBytes = vcmserver.OptFloat64{}
	_, err = prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SizeInBytes is required")

	req = testVolumeCreateRequest()
	req.SnapshotDirectory = vcmserver.NewOptBool(false)
	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	assert.False(t, params.SnapshotDirectory)
}

func TestPrepareCreateVolumeParams_QuotaOutOfRange(t *testing.T) {
	req := testVolumeCreateRequest()
	req.SizeInBytes = vcmserver.NewOptFloat64(1)

	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid volume capacity")
}

func TestPrepareCreateVolumeParams_ZeroQuota(t *testing.T) {
	req := testVolumeCreateRequest()
	req.SizeInBytes = vcmserver.NewOptFloat64(0)

	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid volume capacity")
}

func TestPrepareCreateVolumeParams_NFSUsesShareName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("nfs_share")

	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	assert.Equal(t, "nfs_share", params.CreationToken)
	require.NotNil(t, params.FileProperties)
	assert.Equal(t, "/nfs_share", params.FileProperties.JunctionPath)
}

func TestPrepareCreateVolumeParams_NFSRequiresShareName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil

	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shareName is required for NAS volumes")
}

func TestPrepareCreateVolumeParams_TrimsVolumeName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Name = "  my_volume  "

	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	assert.Equal(t, "my_volume", params.Name)
}

func TestPrepareCreateVolumeParams_RejectsWhitespaceVolumeName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Name = "   "

	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestPrepareCreateVolumeParams_TrimsShareName(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("  nfs_share  ")

	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	assert.Equal(t, "nfs_share", params.CreationToken)
}

func TestPrepareCreateVolumeParams_TrimsIgroupNames(t *testing.T) {
	req := testVolumeCreateRequest()
	req.BlockDevices[0].Igroups = []string{"  hg-db-linux  "}

	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
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

	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
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

	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
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

	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
}

func TestPrepareCreateVolumeParams_RejectsSMBProtocol(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1SMB}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("smb_share")

	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SMB protocol volumes are not supported")
}

func TestPrepareCreateVolumeParams_RejectsDualProtocol(t *testing.T) {
	req := testVolumeCreateRequest()
	req.Protocols = []vcmserver.ProtocolsV1{vcmserver.ProtocolsV1NFSV3, vcmserver.ProtocolsV1SMB}
	req.BlockDevices = nil
	req.ShareName = vcmserver.NewOptString("dual_share")

	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
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

	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exportPolicy is only supported for file protocol volumes")
}

func TestPrepareCreateVolumeParams_BlockDeviceValidation(t *testing.T) {
	req := testVolumeCreateRequest()
	req.BlockDevices = []vcmserver.BlockDeviceV1{
		{OsType: vcmserver.BlockDeviceV1OsTypeLINUX, Igroups: []string{"hg1"}},
		{OsType: vcmserver.BlockDeviceV1OsTypeLINUX, Igroups: []string{"hg2"}},
	}
	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Only one BlockDevice")

	req = testVolumeCreateRequest()
	req.BlockDevices = []vcmserver.BlockDeviceV1{{Igroups: []string{"hg1"}}}
	_, err = prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OS type is required")

	req = testVolumeCreateRequest()
	req.BlockDevices = nil
	_, err = prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Block Device is required")

	req = testVolumeCreateRequest()
	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
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

	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	require.NotNil(t, params.SnapshotPolicy)
	require.NotEmpty(t, params.SnapshotPolicy.Schedules)
}

func TestPrepareCreateVolumeParams_SnapReserveValidation(t *testing.T) {
	req := testVolumeCreateRequest()
	req.SnapReserve = vcmserver.NewOptFloat64(-1)
	_, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be negative")

	req = testVolumeCreateRequest()
	req.SnapReserve = vcmserver.NewOptFloat64(20)
	params, err := prepareCreateVolumeParams(req, testOperationRequestID, testOperationDeadline())
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
			BaseModel: models.BaseModel{
				UUID:      "9760acf5-4638-11e7-9bdb-020073ca0001",
				CreatedAt: time.Date(2016, 11, 30, 20, 32, 50, 459000000, time.UTC),
			},
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
	volume, ok := result.(*vcmserver.VolumeV1)
	require.True(t, ok)
	assert.Equal(t, "file-volume", volume.Name)
	assert.Equal(t, float64(100), volume.SizeInBytes.Value)
	assert.Equal(t, float64(25), volume.UsedBytes.Value)
	assert.Equal(t, float64(5), volume.SnapReserve.Value)
	assert.True(t, volume.SnapshotDirectory.Value)
	assert.Equal(t, vcmserver.VolumeV1SecurityStyleUNIX, volume.SecurityStyle.Value)
	assert.Equal(t, "0755", volume.UnixPermissions.Value)
	assert.Equal(t, "file-volume", volume.ShareName.Value)
	created, ok := volume.Created.Get()
	require.True(t, ok)
	assert.Equal(t, time.Date(2016, 11, 30, 20, 32, 50, 459000000, time.UTC), created)
	assert.False(t, volume.ExportPolicy.IsSet())
	assert.False(t, volume.SnapshotPolicy.IsSet())
	assert.Empty(t, volume.BlockDevices)
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
	volume, ok := result.(*vcmserver.VolumeV1)
	require.True(t, ok)
	assert.Equal(t, "block-volume", volume.Name)
	assert.False(t, volume.ShareName.IsSet())
	assert.False(t, volume.SnapReserve.IsSet())
	assert.False(t, volume.SecurityStyle.IsSet())
	assert.False(t, volume.UnixPermissions.IsSet())
	assert.False(t, volume.SnapshotDirectory.IsSet())
}

func TestV1ListVolumes_ReturnsOntapBackedVolumes(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().ListVolumes(mock.Anything, "").Return([]*models.Volume{
		{
			BaseModel: models.BaseModel{
				UUID:      testVolumeUUID,
				CreatedAt: time.Date(2016, 11, 30, 20, 32, 50, 459000000, time.UTC),
			},
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
		},
		{
			BaseModel:         models.BaseModel{UUID: "8760acf5-4638-11e7-9bdb-020073ca0002"},
			DisplayName:       "block-volume",
			QuotaInBytes:      100,
			UsedBytes:         10,
			SnapshotDirectory: false,
		},
	}, nil)

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1ListVolumes(context.Background(), vcmserver.V1ListVolumesParams{})

	require.NoError(t, err)
	okResult, ok := result.(*vcmserver.V1ListVolumesOK)
	require.True(t, ok)
	require.Len(t, okResult.Volumes, 2)
	assert.Equal(t, "file-volume", okResult.Volumes[0].Name)
	assert.Equal(t, "file-volume", okResult.Volumes[0].ShareName.Value)
	created, ok := okResult.Volumes[0].Created.Get()
	require.True(t, ok)
	assert.Equal(t, time.Date(2016, 11, 30, 20, 32, 50, 459000000, time.UTC), created)
	assert.Equal(t, "block-volume", okResult.Volumes[1].Name)
	assert.False(t, okResult.Volumes[1].ShareName.IsSet())
}

func TestV1ListVolumes_InternalError(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().ListVolumes(mock.Anything, "").Return(nil, errors.New("ontap unavailable"))

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1ListVolumes(context.Background(), vcmserver.V1ListVolumesParams{})

	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1ListVolumesInternalServerError{}, result)
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
	result, err := handler.V1DeleteVolume(ctx, testOperationDeleteParams(testVolumeUUID))
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
	result, err := handler.V1DeleteVolume(ctx, testOperationDeleteParams("  "+testVolumeUUID+"  "))
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
	result, err := handler.V1DeleteVolume(context.Background(), testOperationDeleteParams(testVolumeUUID))
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1DeleteVolumeInternalServerError{}, result)
}

func TestV1DeleteVolume_RejectsEmptyUUID(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1DeleteVolume(context.Background(), testOperationDeleteParams(""))
	require.NoError(t, err)
	badRequest, ok := result.(*vcmserver.V1DeleteVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, "uuid is required", badRequest.Message)
}

func TestV1DeleteVolume_RejectsWhitespaceUUID(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1DeleteVolume(context.Background(), testOperationDeleteParams("   "))
	require.NoError(t, err)
	badRequest, ok := result.(*vcmserver.V1DeleteVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, "uuid is required", badRequest.Message)
}

func testVolumeUpdateRequest() *vcmserver.VolumeUpdateV1 {
	return &vcmserver.VolumeUpdateV1{
		SizeInBytes: vcmserver.NewOptNilFloat64(214748364800),
	}
}

func TestPrepareUpdateVolumeParams(t *testing.T) {
	params, err := prepareUpdateVolumeParams(testVolumeUpdateRequest(), testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	assert.Equal(t, testVolumeUUID, params.VolumeId)
	assert.Equal(t, int64(214748364800), params.QuotaInBytes)

	emptyParams, err := prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	assert.Equal(t, testVolumeUUID, emptyParams.VolumeId)
	assert.Zero(t, emptyParams.QuotaInBytes)

	req := testVolumeUpdateRequest()
	req.SnapReserve = vcmserver.NewOptNilFloat64(101)
	_, err = prepareUpdateVolumeParams(req, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "less than 100")
}

func TestVolumeUpdateV1_BlockDeviceRejectsOsType(t *testing.T) {
	var req vcmserver.VolumeUpdateV1
	err := json.Unmarshal([]byte(`{"blockDevices":[{"osType":"LINUX","igroups":["hg-db-linux"]}]}`), &req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected field")
}

func TestPrepareUpdateVolumeParams_BlockDeviceValidation(t *testing.T) {
	params, err := prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{
		BlockDevices: []vcmserver.BlockDeviceUpdateV1{
			{Igroups: []string{"hg-db-linux", "hg-db-windows"}},
		},
	}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	require.Len(t, params.BlockDevices, 1)
	assert.Equal(t, []string{"hg-db-linux", "hg-db-windows"}, params.BlockDevices[0].HostGroupNames)
	assert.Empty(t, params.BlockDevices[0].OSType)

	_, err = prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{
		BlockDevices: []vcmserver.BlockDeviceUpdateV1{
			{Igroups: []string{"hg1"}},
			{Igroups: []string{"hg2"}},
		},
	}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Only one BlockDevice")

	_, err = prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{
		BlockDevices: []vcmserver.BlockDeviceUpdateV1{
			{Igroups: []string{}},
		},
	}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "igroups must contain at least one")
}

func TestPrepareUpdateVolumeParams_SnapshotPolicyEnabledSet(t *testing.T) {
	hourly := vcmserver.NewOptHourlyScheduleV1(vcmserver.HourlyScheduleV1{
		SnapshotsToKeep: vcmserver.NewOptFloat64(6),
		Minute:          vcmserver.NewOptFloat64(15),
	})

	tests := []struct {
		name        string
		policy      vcmserver.OptSnapshotPolicyV1
		wantSet     bool
		wantEnabled bool
	}{
		{
			name: "omitted snapshotPolicy leaves flag unset",
		},
		{
			name: "schedules only omit enabled so current ONTAP state is preserved",
			policy: vcmserver.NewOptSnapshotPolicyV1(vcmserver.SnapshotPolicyV1{
				HourlySchedule: hourly,
			}),
			wantSet:     false,
			wantEnabled: false,
		},
		{
			name: "explicit enabled true is marked set",
			policy: vcmserver.NewOptSnapshotPolicyV1(vcmserver.SnapshotPolicyV1{
				Enabled:        vcmserver.NewOptNilBool(true),
				HourlySchedule: hourly,
			}),
			wantSet:     true,
			wantEnabled: true,
		},
		{
			name: "explicit enabled false is marked set",
			policy: vcmserver.NewOptSnapshotPolicyV1(vcmserver.SnapshotPolicyV1{
				Enabled:        vcmserver.NewOptNilBool(false),
				HourlySchedule: hourly,
			}),
			wantSet:     true,
			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &vcmserver.VolumeUpdateV1{SnapshotPolicy: tt.policy}
			params, err := prepareUpdateVolumeParams(req, testVolumeUUID, testOperationRequestID, testOperationDeadline())
			require.NoError(t, err)
			assert.Equal(t, tt.wantSet, params.SnapshotPolicyEnabledSet)
			if !tt.policy.IsSet() {
				assert.Nil(t, params.SnapshotPolicy)
				return
			}
			require.NotNil(t, params.SnapshotPolicy)
			assert.Equal(t, tt.wantEnabled, params.SnapshotPolicy.IsEnabled)
		})
	}
}

func TestPrepareUpdateVolumeParams_PreservesCombinedFileProperties(t *testing.T) {
	params, err := prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{
		ShareName: vcmserver.NewOptNilString("my-share"),
		ExportPolicy: vcmserver.NewOptExportPolicyV1(vcmserver.ExportPolicyV1{
			Rules: []vcmserver.SimpleExportPolicyRuleV1{
				{
					AllowedClients: "0.0.0.0/0",
					AccessType:     vcmserver.SimpleExportPolicyRuleV1AccessTypeREADWRITE,
					Nfsv3:          vcmserver.NewOptNilBool(true),
				},
			},
		}),
		UnixPermissions: vcmserver.NewOptNilString("0755"),
	}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
	require.NotNil(t, params.FileProperties)
	assert.Equal(t, "/my-share", params.FileProperties.JunctionPath)
	assert.Equal(t, "0755", params.FileProperties.UnixPermissions)
	require.NotNil(t, params.FileProperties.ExportPolicy)
	require.Len(t, params.FileProperties.ExportPolicy.ExportRules, 1)
}

func TestV1UpdateVolume_AcceptsAndDelegatesToOrchestrator(t *testing.T) {
	ctx := context.Background()
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		UpdateVolumeV2(mock.Anything, mock.MatchedBy(func(params *commonparams.UpdateVolumeParams) bool {
			return params.VolumeId == testVolumeUUID &&
				params.OperationID != "" &&
				!params.Deadline.IsZero() &&
				params.QuotaInBytes == 214748364800
		})).
		RunAndReturn(func(_ context.Context, params *commonparams.UpdateVolumeParams) (*models.Volume, string, error) {
			return nil, params.OperationID, nil
		})

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1UpdateVolume(ctx, testVolumeUpdateRequest(), testOperationUpdateParams())
	require.NoError(t, err)

	accepted, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok)
	assert.Contains(t, accepted.Name, "/v1/operations/")
	assert.False(t, accepted.Done)
}

func TestV1UpdateVolume_RejectsMaskedFieldMissingFromBody(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1UpdateVolume(context.Background(), &vcmserver.VolumeUpdateV1{}, testOperationUpdateParams())
	require.NoError(t, err)

	badRequest, ok := result.(*vcmserver.V1UpdateVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, `updateMask names "sizeInBytes" but the field is missing from the request body`, badRequest.Message)
}

func TestV1UpdateVolume_RejectsMissingUpdateMask(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	params := testOperationUpdateParams()
	params.UpdateMask = ""

	result, err := handler.V1UpdateVolume(context.Background(), testVolumeUpdateRequest(), params)
	require.NoError(t, err)

	badRequest, ok := result.(*vcmserver.V1UpdateVolumeBadRequest)
	require.True(t, ok)
	assert.Contains(t, badRequest.Message, "updateMask must include at least one field")
}

func TestV1UpdateVolume_RejectsUnsupportedUpdateMaskField(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1UpdateVolume(
		context.Background(),
		testVolumeUpdateRequest(),
		testOperationUpdateParamsWithMask("name"),
	)
	require.NoError(t, err)

	badRequest, ok := result.(*vcmserver.V1UpdateVolumeBadRequest)
	require.True(t, ok)
	assert.Contains(t, badRequest.Message, "unsupported field")
}

func TestV1UpdateVolume_IgnoresBodyFieldsNotInUpdateMask(t *testing.T) {
	ctx := context.Background()
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		UpdateVolumeV2(mock.Anything, mock.MatchedBy(func(params *commonparams.UpdateVolumeParams) bool {
			return params.QuotaInBytes == 214748364800 &&
				params.FileProperties == nil
		})).
		RunAndReturn(func(_ context.Context, params *commonparams.UpdateVolumeParams) (*models.Volume, string, error) {
			return nil, params.OperationID, nil
		})

	req := testVolumeUpdateRequest()
	req.ShareName = vcmserver.NewOptNilString("ignored-share")

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1UpdateVolume(ctx, req, testOperationUpdateParamsWithMask("sizeInBytes"))
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.OperationV1{}, result)
}

func TestV1UpdateVolume_RejectsMaskWhenBodyOmitsNamedField(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1UpdateVolume(
		context.Background(),
		testVolumeUpdateRequest(),
		testOperationUpdateParamsWithMask("shareName"),
	)
	require.NoError(t, err)

	badRequest, ok := result.(*vcmserver.V1UpdateVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, `updateMask names "shareName" but the field is missing from the request body`, badRequest.Message)
}

func TestV1UpdateVolume_RejectsNilRequestBody(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1UpdateVolume(context.Background(), nil, testOperationUpdateParams())
	require.NoError(t, err)
	badRequest, ok := result.(*vcmserver.V1UpdateVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, "request body is required", badRequest.Message)
}

func TestV1UpdateVolume_RejectsEmptyUUID(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	params := testOperationUpdateParams()
	params.UUID = ""
	result, err := handler.V1UpdateVolume(context.Background(), testVolumeUpdateRequest(), params)
	require.NoError(t, err)
	badRequest, ok := result.(*vcmserver.V1UpdateVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, "uuid is required", badRequest.Message)
}

func TestV1UpdateVolume_RejectsWhitespaceUUID(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	params := testOperationUpdateParams()
	params.UUID = "   "
	result, err := handler.V1UpdateVolume(context.Background(), testVolumeUpdateRequest(), params)
	require.NoError(t, err)
	badRequest, ok := result.(*vcmserver.V1UpdateVolumeBadRequest)
	require.True(t, ok)
	assert.Equal(t, "uuid is required", badRequest.Message)
}

func TestV1UpdateVolume_TrimsPathUUID(t *testing.T) {
	ctx := context.Background()
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		UpdateVolumeV2(mock.Anything, mock.MatchedBy(func(params *commonparams.UpdateVolumeParams) bool {
			return params.VolumeId == testVolumeUUID
		})).
		RunAndReturn(func(_ context.Context, params *commonparams.UpdateVolumeParams) (*models.Volume, string, error) {
			return nil, params.OperationID, nil
		})

	params := testOperationUpdateParams()
	params.UUID = "  " + testVolumeUUID + "  "
	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1UpdateVolume(ctx, testVolumeUpdateRequest(), params)
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.OperationV1{}, result)
}

func TestV1UpdateVolume_OrchestratorErrorReturns500(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		UpdateVolumeV2(mock.Anything, mock.Anything).
		Return(nil, "", errors.New("workflow start failed"))

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1UpdateVolume(context.Background(), testVolumeUpdateRequest(), testOperationUpdateParams())
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1UpdateVolumeInternalServerError{}, result)
}

func TestPrepareUpdateVolumeParams_ValidationErrors(t *testing.T) {
	_, err := prepareUpdateVolumeParams(testVolumeUpdateRequest(), testVolumeUUID, "", testOperationDeadline())
	require.Error(t, err)

	_, err = prepareUpdateVolumeParams(testVolumeUpdateRequest(), testVolumeUUID, testOperationRequestID, time.Time{})
	require.Error(t, err)

	_, err = prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{
		SizeInBytes: vcmserver.NewOptNilFloat64(0),
	}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive number")

	_, err = prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{
		SnapReserve: vcmserver.NewOptNilFloat64(-1),
	}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)

	_, err = prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{
		ShareName: vcmserver.NewOptNilString("   "),
	}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)

	_, err = prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{
		UnixPermissions: vcmserver.NewOptNilString(""),
	}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.Error(t, err)

	_, err = prepareUpdateVolumeParams(&vcmserver.VolumeUpdateV1{
		SnapshotDirectory: vcmserver.NewOptNilBool(false),
	}, testVolumeUUID, testOperationRequestID, testOperationDeadline())
	require.NoError(t, err)
}

func TestConvertModelToVolumeV1(t *testing.T) {
	assert.Nil(t, convertModelToVolumeV1(nil))

	volume := &models.Volume{
		BaseModel: models.BaseModel{
			UUID:      testVolumeUUID,
			CreatedAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		},
		DisplayName:       "my-volume",
		QuotaInBytes:      100,
		UsedBytes:         25,
		SnapReserve:       5,
		SnapshotDirectory: true,
		FileProperties: &models.FileProperties{
			SecurityStyle:   utils.UnixSecurityStyle,
			UnixPermissions: "0755",
			JunctionPath:    "/my-volume",
			ExportPolicy: &models.ExportPolicy{
				ExportRules: []*models.ExportRule{{
					AllowedClients: "0.0.0.0/0",
					AccessType:     models.ReadWrite,
					NFSv3:          true,
					Superuser:      true,
					AllSquash:      boolPtr(true),
					AnonUid:        int64Ptr(65534),
				}},
			},
		},
		SnapshotPolicy: &models.SnapshotPolicy{
			IsEnabled: true,
			Schedules: []*models.SnapshotPolicySchedule{{
				Count:           6,
				SnapmirrorLabel: utils.SnapshotScheduleLabelHourly,
				Schedule:        &models.Schedule{Minutes: []int{15}},
			}},
		},
	}

	converted := convertModelToVolumeV1(volume)
	require.NotNil(t, converted)
	assert.Equal(t, "my-volume", converted.Name)
	assert.True(t, converted.SecurityStyle.IsSet())
	assert.True(t, converted.ExportPolicy.IsSet())
	assert.True(t, converted.SnapshotPolicy.IsSet())
	assert.True(t, converted.SnapReserve.IsSet())
	assert.Empty(t, converted.BlockDevices)

	blockVolume := &models.Volume{
		BaseModel:    models.BaseModel{UUID: testVolumeUUID},
		DisplayName:  "block-volume",
		QuotaInBytes: 100,
		UsedBytes:    10,
		BlockDevices: &[]models.BlockDevice{{
			OSType: "linux",
			HostGroupDetail: []models.HostGroupDetails{
				{HostGroupID: "hg1"},
			},
		}},
	}
	blockConverted := convertModelToVolumeV1(blockVolume)
	require.NotNil(t, blockConverted)
	assert.False(t, blockConverted.SnapReserve.IsSet())
	assert.False(t, blockConverted.SecurityStyle.IsSet())
	assert.False(t, blockConverted.UnixPermissions.IsSet())
	assert.False(t, blockConverted.SnapshotDirectory.IsSet())
	require.Len(t, blockConverted.BlockDevices, 1)
	assert.Equal(t, vcmserver.BlockDeviceV1OsTypeLINUX, blockConverted.BlockDevices[0].OsType)
	assert.Equal(t, []string{"hg1"}, blockConverted.BlockDevices[0].Igroups)

	esxiVolume := &models.Volume{
		BaseModel:    models.BaseModel{UUID: testVolumeUUID},
		DisplayName:  "esxi-volume",
		QuotaInBytes: 100,
		BlockDevices: &[]models.BlockDevice{{OSType: "vmware"}},
	}
	esxiConverted := convertModelToVolumeV1(esxiVolume)
	require.NotNil(t, esxiConverted)
	require.Len(t, esxiConverted.BlockDevices, 1)
	assert.Equal(t, vcmserver.BlockDeviceV1OsTypeESXI, esxiConverted.BlockDevices[0].OsType)
	_, err := esxiConverted.BlockDevices[0].OsType.MarshalText()
	require.NoError(t, err)
}

func TestConvertModelToSnapshotPolicyV1_AllScheduleTypes(t *testing.T) {
	policy := convertModelToSnapshotPolicyV1(&models.SnapshotPolicy{
		IsEnabled: true,
		Schedules: []*models.SnapshotPolicySchedule{
			{Count: 1, SnapmirrorLabel: utils.SnapshotScheduleLabelMonthly, Schedule: &models.Schedule{DaysOfMonth: []int{1, 15}, Hours: []int{3}, Minutes: []int{30}}},
			{Count: 2, SnapmirrorLabel: utils.SnapshotScheduleLabelWeekly, Schedule: &models.Schedule{DaysOfWeek: []int{1}, Hours: []int{4}, Minutes: []int{0}}},
			{Count: 3, SnapmirrorLabel: utils.SnapshotScheduleLabelDaily, Schedule: &models.Schedule{Hours: []int{5}, Minutes: []int{45}}},
			{Count: 4, SnapmirrorLabel: utils.SnapshotScheduleLabelHourly, Schedule: &models.Schedule{Minutes: []int{10}}},
		},
	})
	require.NotNil(t, policy)
	assert.True(t, policy.MonthlySchedule.IsSet())
	assert.True(t, policy.WeeklySchedule.IsSet())
	assert.True(t, policy.DailySchedule.IsSet())
	assert.True(t, policy.HourlySchedule.IsSet())
}

func boolPtr(v bool) *bool    { return &v }
func int64Ptr(v int64) *int64 { return &v }
