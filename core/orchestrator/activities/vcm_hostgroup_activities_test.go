package activities

import (
	"context"
	"errors"
	"testing"

	ontapModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/testsuite"
)

func TestValidateHostGroupsExistInONTAP_Success(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := HostGroupActivity{}
	env.RegisterActivity(activity.ValidateHostGroupsExistInONTAP)

	mockProvider.On("IgroupExists", "hg-db-linux", nillable.GetStringPtr("test-svm")).Return(true, nil, nil)

	_, err := env.ExecuteActivity(activity.ValidateHostGroupsExistInONTAP, []string{"hg-db-linux"}, "test-svm", &models.Node{})
	require.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestValidateHostGroupsExistInONTAP_NotFound(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	mockProvider := new(vsa.MockProvider)
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := HostGroupActivity{}
	env.RegisterActivity(activity.ValidateHostGroupsExistInONTAP)

	mockProvider.On("IgroupExists", "hg-missing", nillable.GetStringPtr("test-svm")).Return(false, nil, nil)

	_, err := env.ExecuteActivity(activity.ValidateHostGroupsExistInONTAP, []string{"hg-missing"}, "test-svm", &models.Node{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "All host groups could not be found.")
	mockProvider.AssertExpectations(t)
}

func TestHostGroupActivity_CreateIgroup_Success(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := HostGroupActivity{}
	node := &models.Node{}
	host := &common.HostParams{
		HostName: "host1",
		OsType:   "linux",
		HostIQNs: []string{"iqn.1993-08.org.debian:01:123456789"},
	}

	mockProvider.EXPECT().IgroupExists("host1", nillable.GetStringPtr("test-svm")).Return(false, nil, nil)
	mockProvider.EXPECT().IgroupCreate(vsa.IgroupCreateParams{
		IgroupName: "host1",
		SvmName:    "test-svm",
		OsType:     "linux",
		Initiator:  []string{"iqn.1993-08.org.debian:01:123456789"},
	}).Return("host1", nil)

	err := activity.CreateIgroup(context.Background(), "test-svm", host, node)
	require.NoError(t, err)
}

func TestHostGroupActivity_CreateIgroup_ExistsWithMatchingParams(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := HostGroupActivity{}
	host := &common.HostParams{
		HostName: "host1",
		OsType:   "linux",
		HostIQNs: []string{"iqn.1"},
	}

	mockProvider.EXPECT().IgroupExists("host1", nillable.GetStringPtr("test-svm")).Return(true, &ontapRest.Igroup{
		Igroup: ontapModels.Igroup{
			OsType: nillable.ToPointer("linux"),
			IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{
				{Name: nillable.ToPointer("iqn.1")},
			},
		},
	}, nil)

	err := activity.CreateIgroup(context.Background(), "test-svm", host, &models.Node{})
	require.NoError(t, err)
}

func TestHostGroupActivity_CreateIgroup_ExistsWithDifferentParams(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := HostGroupActivity{}
	host := &common.HostParams{HostName: "host1", OsType: "linux", HostIQNs: []string{"iqn.1"}}

	mockProvider.EXPECT().IgroupExists("host1", nillable.GetStringPtr("test-svm")).Return(true, &ontapRest.Igroup{
		Igroup: ontapModels.Igroup{
			OsType: nillable.ToPointer("windows"),
			IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{
				{Name: nillable.ToPointer("iqn.1")},
			},
		},
	}, nil)

	err := activity.CreateIgroup(context.Background(), "test-svm", host, &models.Node{})
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.Equal(t, vsaerrors.ErrHostGroupAlreadyExists, customErr.TrackingID)
}

func TestHostGroupActivity_UpdateIgroup_Success(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	const (
		igroupName = "hg-1"
		svmName    = "test-svm"
		iqn1       = "iqn.1998-01.com.vmware:db1"
		iqn2       = "iqn.1998-01.com.vmware:db2"
		igroupUUID = "igroup-uuid-1"
	)

	mockProvider.EXPECT().IgroupExists(igroupName, nillable.GetStringPtr(svmName)).Return(true, &ontapRest.Igroup{
		Igroup: ontapModels.Igroup{
			UUID:   nillable.ToPointer(igroupUUID),
			OsType: nillable.ToPointer("linux"),
			IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{
				{Name: nillable.ToPointer("iqn.1998-01.com.vmware:old")},
			},
		},
	}, nil)
	mockProvider.EXPECT().IgroupDeleteInitiator(vsa.IgroupDeleteInitiator{
		InitiatorName: "iqn.1998-01.com.vmware:old",
		IgroupUUID:    igroupUUID,
	}).Return(nil)
	mockProvider.EXPECT().IgroupAddInitiator(vsa.IgroupAddInitiator{
		Initiator:  []string{iqn1, iqn2},
		IgroupUUID: igroupUUID,
	}).Return(nil)

	osType, err := HostGroupActivity{}.UpdateIgroup(context.Background(), igroupName, []string{iqn1, iqn2}, svmName, &models.Node{})
	require.NoError(t, err)
	assert.Equal(t, "LINUX", osType)
}

func TestHostGroupActivity_UpdateIgroup_NotFound(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	mockProvider.EXPECT().IgroupExists("hg-1", nillable.GetStringPtr("test-svm")).Return(false, nil, nil)

	_, err := HostGroupActivity{}.UpdateIgroup(context.Background(), "hg-1", []string{"iqn.1"}, "test-svm", &models.Node{})
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.Equal(t, vsaerrors.ErrHostGroupNotFound, customErr.TrackingID)
}

func TestHostGroupActivity_DeleteIgroup_Success(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	const (
		hostgroupName = "hg-1"
		svmName       = "test-svm"
		igroupUUID    = "igroup-uuid-1"
	)

	mockProvider.EXPECT().IgroupGet(nillable.ToPointer(hostgroupName), nillable.ToPointer(svmName)).Return(&ontapRest.Igroup{
		Igroup: ontapModels.Igroup{UUID: nillable.ToPointer(igroupUUID)},
	}, nil)
	mockProvider.EXPECT().IgroupDelete(igroupUUID).Return(nil)

	err := HostGroupActivity{}.DeleteIgroup(context.Background(), hostgroupName, svmName, &models.Node{})
	require.NoError(t, err)
}

func TestHostGroupActivity_DeleteIgroup_LUNMapsNonRetryable(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	const (
		hostgroupName = "hg-1"
		svmName       = "test-svm"
		igroupUUID    = "igroup-uuid-1"
	)

	mockProvider.EXPECT().IgroupGet(nillable.ToPointer(hostgroupName), nillable.ToPointer(svmName)).Return(&ontapRest.Igroup{
		Igroup: ontapModels.Igroup{UUID: nillable.ToPointer(igroupUUID)},
	}, nil)
	mockProvider.EXPECT().IgroupDelete(igroupUUID).Return(
		vsaerrors.NewVCPError(vsaerrors.ErrHostGroupHasLUNMaps, errors.New("LUN maps for this initiator group exist")),
	)

	err := HostGroupActivity{}.DeleteIgroup(context.Background(), hostgroupName, svmName, &models.Node{})
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.Equal(t, vsaerrors.ErrHostGroupHasLUNMaps, customErr.TrackingID)
}

func TestValidateHostGroupsExistInONTAP_GetProviderFails(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, errors.New("provider init failed"))
	}

	err := HostGroupActivity{}.ValidateHostGroupsExistInONTAP(context.Background(), []string{"hg-1"}, "test-svm", &models.Node{})
	require.Error(t, err)
}

func TestValidateHostGroupsExistInONTAP_IgroupExistsFails(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	mockProvider.EXPECT().IgroupExists("hg-1", nillable.GetStringPtr("test-svm")).Return(false, nil, errors.New("ONTAP unavailable"))

	err := HostGroupActivity{}.ValidateHostGroupsExistInONTAP(context.Background(), []string{"hg-1"}, "test-svm", &models.Node{})
	require.Error(t, err)
}

func TestHostGroupActivity_CreateIgroup_GetProviderFails(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider init failed")
	}

	err := HostGroupActivity{}.CreateIgroup(context.Background(), "test-svm", &common.HostParams{
		HostName: "host1",
		OsType:   "linux",
		HostIQNs: []string{"iqn.1"},
	}, &models.Node{})
	require.Error(t, err)
}

func TestHostGroupActivity_UpdateIgroup_GetProviderFails(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider init failed")
	}

	_, err := HostGroupActivity{}.UpdateIgroup(context.Background(), "hg-1", []string{"iqn.1"}, "test-svm", &models.Node{})
	require.Error(t, err)
}

func TestHostGroupActivity_UpdateIgroup_SyncFails(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	mockProvider.EXPECT().IgroupExists("hg-1", nillable.GetStringPtr("test-svm")).Return(false, nil, errors.New("ONTAP sync failed"))

	_, err := HostGroupActivity{}.UpdateIgroup(context.Background(), "hg-1", []string{"iqn.1"}, "test-svm", &models.Node{})
	require.Error(t, err)
}

func TestHostGroupActivity_DeleteIgroup_GetProviderFails(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider init failed")
	}

	err := HostGroupActivity{}.DeleteIgroup(context.Background(), "hg-1", "test-svm", &models.Node{})
	require.Error(t, err)
}

func TestHostGroupActivity_DeleteIgroup_IgroupGetFails(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	mockProvider.EXPECT().IgroupGet(nillable.ToPointer("hg-1"), nillable.ToPointer("test-svm")).Return(nil, errors.New("ONTAP unavailable"))

	err := HostGroupActivity{}.DeleteIgroup(context.Background(), "hg-1", "test-svm", &models.Node{})
	require.Error(t, err)
}

func TestHostGroupActivity_DeleteIgroup_IgroupDeleteFails(t *testing.T) {
	mockProvider := vsa.NewMockProvider(t)
	originalGetProviderByNode := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = originalGetProviderByNode })
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	const igroupUUID = "igroup-uuid-1"
	mockProvider.EXPECT().IgroupGet(nillable.ToPointer("hg-1"), nillable.ToPointer("test-svm")).Return(&ontapRest.Igroup{
		Igroup: ontapModels.Igroup{UUID: nillable.ToPointer(igroupUUID)},
	}, nil)
	mockProvider.EXPECT().IgroupDelete(igroupUUID).Return(errors.New("ONTAP delete failed"))

	err := HostGroupActivity{}.DeleteIgroup(context.Background(), "hg-1", "test-svm", &models.Node{})
	require.Error(t, err)
}
