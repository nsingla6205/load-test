package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

func testIgroupCreateRequest() *vcmserver.IgroupV1 {
	return &vcmserver.IgroupV1{
		Name:   "hg-db-linux",
		OsType: vcmserver.IgroupV1OsTypeLINUX,
		Hosts:  []string{"iqn.1998-01.com.vmware:db1"},
	}
}

func TestValidateCreateIgroupParams(t *testing.T) {
	err := validateCreateIgroupParams(testIgroupCreateRequest())
	require.NoError(t, err)

	err = validateCreateIgroupParams(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request body is required")

	req := testIgroupCreateRequest()
	req.Name = ""
	err = validateCreateIgroupParams(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")

	req = testIgroupCreateRequest()
	req.OsType = vcmserver.IgroupV1OsTypeOSTYPEUNSPECIFIED
	err = validateCreateIgroupParams(req)
	require.Error(t, err)

	req = testIgroupCreateRequest()
	req.Hosts = nil
	err = validateCreateIgroupParams(req)
	require.Error(t, err)

	t.Setenv("MAX_HOSTS_PER_HOSTGROUP", "2")
	req = testIgroupCreateRequest()
	req.Hosts = []string{"iqn.1", "iqn.2", "iqn.3"}
	err = validateCreateIgroupParams(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot have more than 2 hosts")
}

func TestValidateUpdateIgroupParams(t *testing.T) {
	name, err := validateUpdateIgroupParams("hg-db-linux", &vcmserver.IgroupUpdateV1{
		Hosts: []string{"iqn.1998-01.com.vmware:db1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "hg-db-linux", name)

	name, err = validateUpdateIgroupParams("  hg-db-linux  ", &vcmserver.IgroupUpdateV1{
		Hosts: []string{"iqn.1998-01.com.vmware:db1"},
	})
	require.NoError(t, err)
	assert.Equal(t, "hg-db-linux", name)

	_, err = validateUpdateIgroupParams("   ", &vcmserver.IgroupUpdateV1{
		Hosts: []string{"iqn.1998-01.com.vmware:db1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")

	_, err = validateUpdateIgroupParams("hg-db-linux", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hosts must be non-empty")

	_, err = validateUpdateIgroupParams("hg-db-linux", &vcmserver.IgroupUpdateV1{})
	require.Error(t, err)

	t.Setenv("MAX_HOSTS_PER_HOSTGROUP", "2")
	_, err = validateUpdateIgroupParams("hg-db-linux", &vcmserver.IgroupUpdateV1{
		Hosts: []string{"iqn.1", "iqn.2", "iqn.3"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot have more than 2 hosts")
}

func TestV1CreateIgroup_TrimsIgroupName(t *testing.T) {
	ctx := context.Background()
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		CreateHostGroup(mock.Anything, mock.MatchedBy(func(params *commonparams.CreateHostGroupParams) bool {
			return params != nil && params.Name == "hg-db-linux"
		})).
		Return(&models.HostGroup{
			BaseModel: models.BaseModel{UUID: "op-create-1"},
			Name:      "hg-db-linux",
		}, nil)
	handler := &Handler{Orchestrator: orch}

	req := testIgroupCreateRequest()
	req.Name = "  hg-db-linux  "

	result, err := handler.V1CreateIgroup(ctx, req, testOperationIgroupCreateParams())
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.OperationV1{}, result)
}

func TestV1CreateIgroup_AcceptsAndDelegatesToOrchestrator(t *testing.T) {
	ctx := context.Background()
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		CreateHostGroup(mock.Anything, mock.MatchedBy(func(params *commonparams.CreateHostGroupParams) bool {
			return params != nil &&
				params.Name == "hg-db-linux" &&
				params.OSType == "LINUX" &&
				len(params.Hosts) == 1 &&
				params.Hosts[0] == "iqn.1998-01.com.vmware:db1" &&
				params.OperationID != "" &&
				!params.Deadline.IsZero()
		})).
		Return(&models.HostGroup{
			BaseModel: models.BaseModel{UUID: "op-create-1"},
			Name:      "hg-db-linux",
		}, nil)
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1CreateIgroup(ctx, testIgroupCreateRequest(), testOperationIgroupCreateParams())
	require.NoError(t, err)
	accepted, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok)
	assert.Equal(t, "/v1/operations/"+testOperationRequestID, accepted.Name)
	assert.False(t, accepted.Done)
}

func TestV1CreateIgroup_ReturnsInternalErrorWhenOrchestratorFails(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().CreateHostGroup(mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1CreateIgroup(context.Background(), testIgroupCreateRequest(), testOperationIgroupCreateParams())
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1CreateIgroupInternalServerError{}, result)
}

func TestV1DeleteIgroup_RejectsEmptyName(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1DeleteIgroup(context.Background(), testOperationIgroupDeleteParams("   "))
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1DeleteIgroupBadRequest{}, result)
}

func TestV1DeleteIgroup_AcceptsAndDelegatesToOrchestrator(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		DeleteHostGroup(mock.Anything, mock.MatchedBy(func(params *commonparams.DeleteHostGroupParams) bool {
			return params != nil && params.Name == "hg-db-linux" && params.OperationID != "" && !params.Deadline.IsZero()
		})).
		Return(&models.HostGroup{
			BaseModel: models.BaseModel{UUID: "op-delete-1"},
			Name:      "hg-db-linux",
		}, nil)

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1DeleteIgroup(context.Background(), testOperationIgroupDeleteParams("hg-db-linux"))
	require.NoError(t, err)
	accepted, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok)
	assert.False(t, accepted.Done)
	assert.Equal(t, "/v1/operations/"+testOperationRequestID, accepted.Name)
}

func TestV1DeleteIgroup_ReturnsInternalErrorWhenOrchestratorFails(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		DeleteHostGroup(mock.Anything, mock.MatchedBy(func(params *commonparams.DeleteHostGroupParams) bool {
			return params != nil && params.Name == "hg-db-linux" && params.OperationID != "" && !params.Deadline.IsZero()
		})).
		Return(nil, errors.New("boom"))

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1DeleteIgroup(context.Background(), testOperationIgroupDeleteParams("hg-db-linux"))
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1DeleteIgroupInternalServerError{}, result)
}

func TestV1UpdateIgroup_RejectsEmptyName(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1UpdateIgroup(context.Background(), &vcmserver.IgroupUpdateV1{
		Hosts: []string{"iqn.1998-01.com.vmware:db1"},
	}, testOperationIgroupUpdateParams("   "))
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1UpdateIgroupBadRequest{}, result)
}

func TestV1UpdateIgroup_RejectsEmptyHosts(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1UpdateIgroup(context.Background(), &vcmserver.IgroupUpdateV1{}, testOperationIgroupUpdateParams("hg-db-linux"))
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1UpdateIgroupBadRequest{}, result)
}

func TestV1UpdateIgroup_RejectsTooManyHosts(t *testing.T) {
	t.Setenv("MAX_HOSTS_PER_HOSTGROUP", "2")
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1UpdateIgroup(context.Background(), &vcmserver.IgroupUpdateV1{
		Hosts: []string{"iqn.1", "iqn.2", "iqn.3"},
	}, testOperationIgroupUpdateParams("hg-db-linux"))
	require.NoError(t, err)
	badRequest, ok := result.(*vcmserver.V1UpdateIgroupBadRequest)
	require.True(t, ok)
	assert.Contains(t, badRequest.Message, "cannot have more than 2 hosts")
}

func TestV1UpdateIgroup_AcceptsAndDelegatesToOrchestrator(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		UpdateHostGroup(mock.Anything, mock.MatchedBy(func(params *commonparams.UpdateHostGroupParams) bool {
			return params != nil &&
				params.Name == "hg-db-linux" &&
				params.Hosts != nil &&
				len(*params.Hosts) == 2 &&
				(*params.Hosts)[0] == "iqn.1998-01.com.vmware:db1" &&
				(*params.Hosts)[1] == "iqn.1998-01.com.vmware:db3" &&
				params.OperationID != "" &&
				!params.Deadline.IsZero()
		})).
		Return(nil, "", nil)

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1UpdateIgroup(context.Background(), &vcmserver.IgroupUpdateV1{
		Hosts: []string{"iqn.1998-01.com.vmware:db1", "iqn.1998-01.com.vmware:db3"},
	}, testOperationIgroupUpdateParams("hg-db-linux"))
	require.NoError(t, err)
	accepted, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok)
	assert.False(t, accepted.Done)
	assert.Equal(t, "/v1/operations/"+testOperationRequestID, accepted.Name)
}

func TestV1UpdateIgroup_ReturnsInternalErrorWhenOrchestratorFails(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().
		UpdateHostGroup(mock.Anything, mock.Anything).
		Return(nil, "", errors.New("boom"))

	handler := &Handler{Orchestrator: orch}
	result, err := handler.V1UpdateIgroup(context.Background(), &vcmserver.IgroupUpdateV1{
		Hosts: []string{"iqn.1998-01.com.vmware:db1"},
	}, testOperationIgroupUpdateParams("hg-db-linux"))
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1UpdateIgroupInternalServerError{}, result)
}

func TestV1DescribeIgroup_RejectsEmptyName(t *testing.T) {
	handler := &Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	result, err := handler.V1DescribeIgroup(context.Background(), vcmserver.V1DescribeIgroupParams{Name: "  "})
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1DescribeIgroupBadRequest{}, result)
}

func TestV1DescribeIgroup_ReturnsIgroup(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetHostGroup(mock.Anything, "hg-test-linux", "").Return(&models.HostGroup{
		Name:   "hg-test-linux",
		OSType: "LINUX",
		Hosts:  []string{"iqn.1998-01.com.vmware:testhost1"},
	}, nil)
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1DescribeIgroup(context.Background(), vcmserver.V1DescribeIgroupParams{Name: "hg-test-linux"})
	require.NoError(t, err)
	ig, ok := result.(*vcmserver.IgroupV1)
	require.True(t, ok)
	assert.Equal(t, "hg-test-linux", ig.Name)
	assert.Equal(t, vcmserver.IgroupV1OsTypeLINUX, ig.OsType)
	assert.Equal(t, []string{"iqn.1998-01.com.vmware:testhost1"}, ig.Hosts)
}

func TestV1DescribeIgroup_MapsOntapLinuxOsType(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetHostGroup(mock.Anything, "vcmhgtest2", "").Return(&models.HostGroup{
		Name:   "vcmhgtest2",
		OSType: "linux",
		Hosts:  []string{"iqn.1998-01.com.vmware:test-host-2"},
	}, nil)
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1DescribeIgroup(context.Background(), vcmserver.V1DescribeIgroupParams{Name: "vcmhgtest2"})
	require.NoError(t, err)
	ig, ok := result.(*vcmserver.IgroupV1)
	require.True(t, ok)
	assert.Equal(t, vcmserver.IgroupV1OsTypeLINUX, ig.OsType)
}

func TestV1DescribeIgroup_NotFound(t *testing.T) {
	id := "missing"
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetHostGroup(mock.Anything, "missing", "").Return(nil, customerrors.NewNotFoundErr("HostGroup", &id))
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1DescribeIgroup(context.Background(), vcmserver.V1DescribeIgroupParams{Name: "missing"})
	require.NoError(t, err)
	notFound, ok := result.(*vcmserver.V1DescribeIgroupNotFound)
	require.True(t, ok)
	assert.Equal(t, float64(404), notFound.Code)
	assert.Equal(t, "igroup not found", notFound.Message)
}

func TestV1DescribeIgroup_SVMNotFoundReturnsInternalError(t *testing.T) {
	svmName := "gcnv-test-svm-01"
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetHostGroup(mock.Anything, "hg-1", "").Return(nil, fmt.Errorf(
		"GetHostGroup: resolve SVM: %w",
		vsaerrors.NewVCPError(vsaerrors.ErrSVMNotFound, customerrors.NewNotFoundErr("svm", &svmName)),
	))
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1DescribeIgroup(context.Background(), vcmserver.V1DescribeIgroupParams{Name: "hg-1"})
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1DescribeIgroupInternalServerError{}, result)
}

func TestV1DescribeIgroup_InternalError(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetHostGroup(mock.Anything, "hg-1", "").Return(nil, errors.New("ontap down"))
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1DescribeIgroup(context.Background(), vcmserver.V1DescribeIgroupParams{Name: "hg-1"})
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1DescribeIgroupInternalServerError{}, result)
}

func TestV1ListIgroups_ReturnsIgroups(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetMultipleHostGroups(mock.Anything, "", mock.Anything).Return([]*models.HostGroup{
		{Name: "hg-linux", OSType: "LINUX", Hosts: []string{"iqn.a"}},
		{Name: "hg-win", OSType: "WINDOWS", Hosts: []string{"iqn.b"}},
	}, nil)
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1ListIgroups(context.Background(), vcmserver.V1ListIgroupsParams{})
	require.NoError(t, err)
	list, ok := result.(*vcmserver.V1ListIgroupsOK)
	require.True(t, ok)
	require.Len(t, list.Igroups, 2)
	assert.Equal(t, "hg-linux", list.Igroups[0].Name)
	assert.Equal(t, vcmserver.IgroupV1OsTypeLINUX, list.Igroups[0].OsType)
	assert.Equal(t, []string{"iqn.a"}, list.Igroups[0].Hosts)
	assert.Equal(t, vcmserver.IgroupV1OsTypeWINDOWS, list.Igroups[1].OsType)
}

func TestV1ListIgroups_ReturnsIgroupsFromOntapOsTypes(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetMultipleHostGroups(mock.Anything, "", mock.Anything).Return([]*models.HostGroup{
		{Name: "hg-linux", OSType: "linux", Hosts: []string{"iqn.a"}},
		{Name: "hg-vmware", OSType: "vmware", Hosts: []string{"iqn.b"}},
	}, nil)
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1ListIgroups(context.Background(), vcmserver.V1ListIgroupsParams{})
	require.NoError(t, err)
	list, ok := result.(*vcmserver.V1ListIgroupsOK)
	require.True(t, ok)
	require.Len(t, list.Igroups, 2)
	assert.Equal(t, vcmserver.IgroupV1OsTypeLINUX, list.Igroups[0].OsType)
	assert.Equal(t, vcmserver.IgroupV1OsTypeESXI, list.Igroups[1].OsType)
}

func TestV1ListIgroups_Empty(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetMultipleHostGroups(mock.Anything, "", mock.Anything).Return([]*models.HostGroup{}, nil)
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1ListIgroups(context.Background(), vcmserver.V1ListIgroupsParams{})
	require.NoError(t, err)
	list, ok := result.(*vcmserver.V1ListIgroupsOK)
	require.True(t, ok)
	assert.Empty(t, list.Igroups)
}

func TestV1ListIgroups_InternalError(t *testing.T) {
	orch := factory.NewMockOrchestratorFactory(t)
	orch.EXPECT().GetMultipleHostGroups(mock.Anything, "", mock.Anything).Return(nil, errors.New("ontap down"))
	handler := &Handler{Orchestrator: orch}

	result, err := handler.V1ListIgroups(context.Background(), vcmserver.V1ListIgroupsParams{})
	require.NoError(t, err)
	assert.IsType(t, &vcmserver.V1ListIgroupsInternalServerError{}, result)
}

func TestConvertToIgroupV1(t *testing.T) {
	assert.Nil(t, convertToIgroupV1(nil))

	ig := convertToIgroupV1(&models.HostGroup{
		Name:   "hg-1",
		OSType: "ESXI",
		Hosts:  []string{"iqn.a", "iqn.b"},
	})
	assert.Equal(t, "hg-1", ig.Name)
	assert.Equal(t, vcmserver.IgroupV1OsTypeESXI, ig.OsType)
	assert.Equal(t, []string{"iqn.a", "iqn.b"}, ig.Hosts)
}

func TestConvertHostGroupsToIgroupV1(t *testing.T) {
	assert.Empty(t, convertHostGroupsToIgroupV1(nil))
	assert.Empty(t, convertHostGroupsToIgroupV1([]*models.HostGroup{nil}))

	converted := convertHostGroupsToIgroupV1([]*models.HostGroup{
		{Name: "hg-1", OSType: "LINUX", Hosts: []string{"iqn.a"}},
		nil,
	})
	require.Len(t, converted, 1)
	assert.Equal(t, "hg-1", converted[0].Name)
}

func TestMapCanonicalOSTypeToV1(t *testing.T) {
	assert.Equal(t, vcmserver.IgroupV1OsTypeLINUX, mapCanonicalOSTypeToV1("LINUX"))
	assert.Equal(t, vcmserver.IgroupV1OsTypeWINDOWS, mapCanonicalOSTypeToV1("WINDOWS"))
	assert.Equal(t, vcmserver.IgroupV1OsTypeESXI, mapCanonicalOSTypeToV1("ESXI"))
	assert.Equal(t, vcmserver.IgroupV1OsTypeESXI, mapCanonicalOSTypeToV1("VMWARE"))
	assert.Equal(t, vcmserver.IgroupV1OsTypeESXI, mapCanonicalOSTypeToV1("vmware"))
	assert.Equal(t, vcmserver.IgroupV1OsTypeLINUX, mapCanonicalOSTypeToV1("linux"))
	assert.Equal(t, vcmserver.IgroupV1OsTypeOSTYPEUNSPECIFIED, mapCanonicalOSTypeToV1(""))
}
