package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontapModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontap_rest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
	"go.temporal.io/sdk/testsuite"
)

func TestIsLunNotFoundError(t *testing.T) {
	customErr := vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, errors.New("lun not found: svm=svm1"))
	assert.True(t, IsLunNotFoundError(customErr))
	assert.True(t, IsLunNotFoundError(apperror.WrapAsAppError(customErr, false)))
	assert.False(t, IsLunNotFoundError(errors.New("connection refused")))

	notFoundErr := vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, utilErrors.NewNotFoundErr("lun", nil))
	assert.True(t, IsLunNotFoundError(notFoundErr))
	assert.True(t, IsLunNotFoundError(apperror.WrapAsAppError(notFoundErr, false)))

	// go-workflows persists activity errors as a message-only unwrap chain.
	serializedErr := &wrappedWorkflowErr{
		msg: "An internal error occurred.",
		cause: &wrappedWorkflowErr{
			msg: "An internal error occurred.",
			cause: &wrappedWorkflowErr{
				msg: "lun not found",
			},
		},
	}
	assert.True(t, IsLunNotFoundError(serializedErr))
}

type wrappedWorkflowErr struct {
	msg   string
	cause error
}

func (e *wrappedWorkflowErr) Error() string { return e.msg }
func (e *wrappedWorkflowErr) Unwrap() error { return e.cause }

func TestPopulateVolumeFromONTAPResponse(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activity := VolumeActivity{}
	env.RegisterActivity(activity.PopulateVolumeFromONTAPResponse)

	volume := &datamodel.Volume{
		Svm: &datamodel.Svm{Name: "test-svm"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{},
			},
		},
	}
	volResponse := &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			Name:         "my-vol",
			ExternalUUID: "vol-uuid",
		},
		Size:                           1024,
		SnapReserve:                    10,
		SnapshotDirectoryAccessEnabled: true,
		ExportPolicyName:               "export-policy",
		JunctionPath:                   "/my-vol",
		UnixPermissions:                "0777",
		SecurityStyle:                  "unix",
		SnapshotPolicyName:             "default",
	}

	val, err := env.ExecuteActivity(activity.PopulateVolumeFromONTAPResponse, volume, volResponse)
	require.NoError(t, err)

	var updatedVolume datamodel.Volume
	require.NoError(t, val.Get(&updatedVolume))
	assert.Equal(t, "my-vol", updatedVolume.Name)
	assert.Equal(t, int64(1024), updatedVolume.SizeInBytes)
	assert.Equal(t, "vol-uuid", updatedVolume.VolumeAttributes.ExternalUUID)
	assert.Equal(t, int64(10), updatedVolume.VolumeAttributes.SnapReserve)
	assert.True(t, updatedVolume.VolumeAttributes.SnapshotDirectory)
	assert.Equal(t, "export-policy", updatedVolume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName)
	assert.Equal(t, "/my-vol", updatedVolume.VolumeAttributes.FileProperties.JunctionPath)
	assert.Equal(t, "0777", updatedVolume.VolumeAttributes.FileProperties.UnixPermissions)
	assert.Equal(t, "unix", updatedVolume.VolumeAttributes.FileProperties.SecurityStyle)
	require.NotNil(t, updatedVolume.SnapshotPolicy)
	assert.Equal(t, "default", updatedVolume.SnapshotPolicy.Name)
}

func TestBuildVolumeOperationResponseEnrichment_EnrichmentFails(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })

	provider := vsa.NewMockProvider(t)
	provider.On("LunList", mock.Anything).Return(nil, errors.New("lun lookup failed"))
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}

	enrichment, err := (VolumeActivity{}).BuildVolumeOperationResponseEnrichment(
		context.Background(),
		&vsa.VolumeResponse{ProviderResponse: vsa.ProviderResponse{Name: "vol-1"}},
		"svm-1",
		&models.Node{},
	)

	require.ErrorContains(t, err, "lun lookup failed")
	assert.Nil(t, enrichment)
}

func TestGetLunInONTAP_Found(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeActivity{}
	env.RegisterActivity(activity.GetLunInONTAP)

	mockProvider.On("LunGet", vsa.LunGetParams{
		SvmName:    "test-svm",
		VolumeName: "my-vol",
		LunName:    "lun_my-vol",
	}).Return(&vsa.LunResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "lun-uuid"},
	}, nil)

	val, err := env.ExecuteActivity(activity.GetLunInONTAP, "lun_my-vol", "my-vol", "test-svm", &models.Node{})
	require.NoError(t, err)

	var lun vsa.LunResponse
	require.NoError(t, val.Get(&lun))
	assert.Equal(t, "lun-uuid", lun.ExternalUUID)
	mockProvider.AssertExpectations(t)
}

func TestUpdateBlockDeviceHostGroups_Success(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeActivity{}
	env.RegisterActivity(activity.UpdateBlockDeviceHostGroups)

	uuid := "igroup-uuid"
	mockProvider.On("IgroupExists", "hg-old", mock.Anything).Return(true, &ontap_rest.Igroup{Igroup: ontapModels.Igroup{UUID: &uuid}}, nil)
	mockProvider.On("LunMapDelete", vsa.LunMapDeleteParams{LunUUID: "lun-uuid", IGroupUUID: uuid}).Return(nil)
	mockProvider.On("LunMapCreate", vsa.LunMapCreateParams{
		LunName:    "/vol/lun_vol-1",
		SvmName:    "test-svm",
		IGroupName: []string{"hg-new"},
	}).Return(nil)

	lun := vsa.LunResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun_vol-1", ExternalUUID: "lun-uuid"},
		IgroupNames:      []string{"hg-old"},
	}
	_, err := env.ExecuteActivity(activity.UpdateBlockDeviceHostGroups, lun, "test-svm", []string{"hg-new"}, &models.Node{})
	require.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestUpdateBlockDeviceHostGroups_ProviderAndMappingErrors(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	activity := VolumeActivity{}
	env.RegisterActivity(activity.UpdateBlockDeviceHostGroups)

	t.Run("provider lookup fails", func(t *testing.T) {
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("provider unavailable")
		}
		_, err := env.ExecuteActivity(activity.UpdateBlockDeviceHostGroups, vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun", ExternalUUID: "lun-uuid"},
		}, "test-svm", []string{"hg1"}, &models.Node{})
		require.Error(t, err)
	})

	t.Run("igroup lookup fails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		mockProvider.On("IgroupExists", "hg-old", mock.Anything).Return(false, nil, errors.New("igroup lookup failed"))
		_, err := env.ExecuteActivity(activity.UpdateBlockDeviceHostGroups, vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun", ExternalUUID: "lun-uuid"},
			IgroupNames:      []string{"hg-old"},
		}, "test-svm", []string{"hg-new"}, &models.Node{})
		require.Error(t, err)
		mockProvider.AssertExpectations(t)
	})

	t.Run("lun map delete fails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		uuid := "igroup-uuid"
		mockProvider.On("IgroupExists", "hg-old", mock.Anything).Return(true, &ontap_rest.Igroup{Igroup: ontapModels.Igroup{UUID: &uuid}}, nil)
		mockProvider.On("LunMapDelete", mock.Anything).Return(errors.New("unmap failed"))
		_, err := env.ExecuteActivity(activity.UpdateBlockDeviceHostGroups, vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun", ExternalUUID: "lun-uuid"},
			IgroupNames:      []string{"hg-old"},
		}, "test-svm", []string{"hg-new"}, &models.Node{})
		require.Error(t, err)
		mockProvider.AssertExpectations(t)
	})

	t.Run("lun map create conflict is ignored", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		mockProvider.On("LunMapCreate", mock.Anything).Return(utilErrors.NewConflictErr("mapping already exists"))
		_, err := env.ExecuteActivity(activity.UpdateBlockDeviceHostGroups, vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun", ExternalUUID: "lun-uuid"},
		}, "test-svm", []string{"hg-new"}, &models.Node{})
		require.NoError(t, err)
		mockProvider.AssertExpectations(t)
	})
}

func TestGetLunInONTAP_ProviderLookupFails(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider unavailable")
	}

	activity := VolumeActivity{}
	env.RegisterActivity(activity.GetLunInONTAP)
	_, err := env.ExecuteActivity(activity.GetLunInONTAP, "lun_vol", "vol", "svm", &models.Node{})
	require.Error(t, err)
}

func TestPopulateVolumeFromONTAPResponse_CreatesMissingNestedFields(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeActivity{}
	env.RegisterActivity(activity.PopulateVolumeFromONTAPResponse)

	volume := &datamodel.Volume{Svm: &datamodel.Svm{Name: "svm-1"}}
	val, err := env.ExecuteActivity(activity.PopulateVolumeFromONTAPResponse, volume, &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "vol-1", ExternalUUID: "uuid-1"},
		ExportPolicyName: "export-1",
		JunctionPath:     "/vol-1",
	})
	require.NoError(t, err)

	var updated datamodel.Volume
	require.NoError(t, val.Get(&updated))
	require.NotNil(t, updated.VolumeAttributes)
	require.NotNil(t, updated.VolumeAttributes.FileProperties)
	require.NotNil(t, updated.VolumeAttributes.FileProperties.ExportPolicy)
	assert.Equal(t, "export-1", updated.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName)
}

func TestUpdateBlockDeviceHostGroups_ValidationErrors(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeActivity{}
	env.RegisterActivity(activity.UpdateBlockDeviceHostGroups)

	_, err := env.ExecuteActivity(activity.UpdateBlockDeviceHostGroups, vsa.LunResponse{}, "test-svm", []string{"hg1"}, &models.Node{})
	require.Error(t, err)

	_, err = env.ExecuteActivity(activity.UpdateBlockDeviceHostGroups, vsa.LunResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun", ExternalUUID: "lun-uuid"},
	}, "", []string{"hg1"}, &models.Node{})
	require.Error(t, err)

	_, err = env.ExecuteActivity(activity.UpdateBlockDeviceHostGroups, vsa.LunResponse{
		ProviderResponse: vsa.ProviderResponse{Name: "/vol/lun", ExternalUUID: "lun-uuid"},
	}, "test-svm", nil, &models.Node{})
	require.Error(t, err)
}

func TestPopulateVolumeFromONTAPResponse_ValidationErrors(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activity := VolumeActivity{}
	env.RegisterActivity(activity.PopulateVolumeFromONTAPResponse)

	_, err := env.ExecuteActivity(activity.PopulateVolumeFromONTAPResponse, nil, &vsa.VolumeResponse{})
	require.Error(t, err)

	_, err = env.ExecuteActivity(activity.PopulateVolumeFromONTAPResponse, &datamodel.Volume{}, &vsa.VolumeResponse{})
	require.Error(t, err)
}

func TestIsLunNotFoundError_MessageVariants(t *testing.T) {
	customErr := vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, errors.New("other error"))
	customErr.Message = "lun not found in svm"
	assert.True(t, IsLunNotFoundError(customErr))
	assert.True(t, IsLunNotFoundError(errors.New("LUN not found")))
	assert.False(t, IsLunNotFoundError(nil))
}

func TestGetLunInONTAP_NotFoundReturnsError(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeActivity{}
	env.RegisterActivity(activity.GetLunInONTAP)

	mockProvider.On("LunGet", vsa.LunGetParams{
		SvmName:    "test-svm",
		VolumeName: "nfs-vol",
		LunName:    "lun_nfs-vol",
	}).Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, utilErrors.NewNotFoundErr("lun", nil)))

	_, err := env.ExecuteActivity(activity.GetLunInONTAP, "lun_nfs-vol", "nfs-vol", "test-svm", &models.Node{})
	require.Error(t, err)
	assert.True(t, IsLunNotFoundError(err))
	mockProvider.AssertExpectations(t)
}
