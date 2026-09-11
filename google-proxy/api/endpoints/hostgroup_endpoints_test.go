package api

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestV1betaDeleteHostGroup(t *testing.T) {
	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeleteHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "hg-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeleteHostGroup(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaDeleteHostGroupBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", result.(*gcpgenserver.V1betaDeleteHostGroupBadRequest).Message)
	})
	t.Run("WhenHostGroupDoesNotExist", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeleteHostGroupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			HostGroupId:   "hg-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(nil, errors.NewNotFoundErr("not found", nil))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeleteHostGroup(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
	})
	t.Run("WhenHostGroupHasActiveVolumes", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeleteHostGroupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			HostGroupId:   "hg-id",
		}

		existingHG := &models.HostGroup{
			BaseModel: models.BaseModel{
				UUID: "deletable-hg-id",
			},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(existingHG, nil)
		mockOrchestrator.EXPECT().DeleteHostGroup(mock.Anything, mock.MatchedBy(func(p *common.DeleteHostGroupParams) bool {
			return p != nil && p.Name == params.HostGroupId && p.AccountName == params.ProjectNumber
		})).Return(nil, errors.NewUserInputValidationErr("host group is attached to volumes"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeleteHostGroup(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaDeleteHostGroupBadRequest).Code)
		assert.Equal(tt, "host group is attached to volumes", result.(*gcpgenserver.V1betaDeleteHostGroupBadRequest).Message)
	})
	t.Run("WhenHostGroupGetFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeleteHostGroupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			HostGroupId:   "hg-id",
		}

		existingHG := &models.HostGroup{
			BaseModel: models.BaseModel{
				UUID: "deletable-hg-id",
			},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(existingHG, nil)
		mockOrchestrator.EXPECT().DeleteHostGroup(mock.Anything, mock.MatchedBy(func(p *common.DeleteHostGroupParams) bool {
			return p != nil && p.Name == params.HostGroupId && p.AccountName == params.ProjectNumber
		})).Return(nil, errors.New("some error"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeleteHostGroup(context.Background(), params)

		assert.NotNil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaDeleteHostGroupInternalServerError).Code)
		assert.Equal(tt, "Internal server error", result.(*gcpgenserver.V1betaDeleteHostGroupInternalServerError).Message)
	})
	t.Run("WhenHostGroupDeleteFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeleteHostGroupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			HostGroupId:   "hg-id",
		}

		existingHG := &models.HostGroup{
			BaseModel: models.BaseModel{
				UUID: "deletable-hg-id",
			},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(existingHG, errors.New("some error"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeleteHostGroup(context.Background(), params)

		assert.NotNil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaDeleteHostGroupInternalServerError).Code)
		assert.Equal(tt, "Internal server error", result.(*gcpgenserver.V1betaDeleteHostGroupInternalServerError).Message)
	})
	t.Run("WhenHostGroupDeletionSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeleteHostGroupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			HostGroupId:   "hg-id",
		}

		existingHG := &models.HostGroup{
			BaseModel: models.BaseModel{
				UUID: "deletable-hg-id",
			},
		}
		deletedPool := &models.HostGroup{
			BaseModel: models.BaseModel{
				UUID: "deletable-pool-id",
			},
			State: datamodel.LifeCycleStateDeleted,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(existingHG, nil)
		mockOrchestrator.EXPECT().DeleteHostGroup(mock.Anything, mock.MatchedBy(func(p *common.DeleteHostGroupParams) bool {
			return p != nil && p.Name == params.HostGroupId && p.AccountName == params.ProjectNumber
		})).Return(deletedPool, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeleteHostGroup(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
	})
}

func TestV1betaGetMultipleHostGroups(t *testing.T) {
	t.Run("WhenGetMultipleHostGroupsFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleHostGroupsParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.HostGroupIdListV1beta{
			HostGroupUuids: []string{"hg-id-1", "hg-id-2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetMultipleHostGroups(mock.Anything, params.ProjectNumber, req.HostGroupUuids).Return(nil, errors.New("some error"))
		result, err := handler.V1betaGetMultipleHostGroups(context.Background(), req, params)

		assert.NotNil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaGetMultipleHostGroupsInternalServerError).Code)
		assert.Equal(tt, "Internal server error", result.(*gcpgenserver.V1betaGetMultipleHostGroupsInternalServerError).Message)
	})
	t.Run("WhenGetMultipleHostGroupsReturnsZeroHGs", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleHostGroupsParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.HostGroupIdListV1beta{
			HostGroupUuids: []string{"hg-id-1", "hg-id-2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		resp := []*models.HostGroup{}
		mockOrchestrator.EXPECT().GetMultipleHostGroups(mock.Anything, params.ProjectNumber, req.HostGroupUuids).Return(resp, nil)
		result, err := handler.V1betaGetMultipleHostGroups(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result.(*gcpgenserver.V1betaGetMultipleHostGroupsOK).HostGroups, 0)
	})
	t.Run("WhenGetMultipleHostGroupsReturns2HGs", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultipleHostGroupsParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.HostGroupIdListV1beta{
			HostGroupUuids: []string{"hg-id-1", "hg-id-2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		resp := []*models.HostGroup{
			{
				BaseModel: models.BaseModel{
					UUID: "hg-id-1",
				},
			},
			{
				BaseModel: models.BaseModel{
					UUID: "hg-id-2",
				},
			},
		}
		mockOrchestrator.EXPECT().GetMultipleHostGroups(mock.Anything, params.ProjectNumber, req.HostGroupUuids).Return(resp, nil)
		result, err := handler.V1betaGetMultipleHostGroups(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result.(*gcpgenserver.V1betaGetMultipleHostGroupsOK).HostGroups, 2)
	})
}

func TestV1betaCreateHostGroup(t *testing.T) {
	t.Run("WhenCreateHostGroupFailsWithConflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.HostGroupV1beta{
			ResourceId:  "test-host-group",
			Description: gcpgenserver.NewOptString("test description"),
			OsType:      gcpgenserver.HostGroupV1betaOsTypeLINUX,
			Type:        gcpgenserver.NewOptHostGroupV1betaType(gcpgenserver.HostGroupV1betaTypeISCSIINITIATOR),
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		createHGParams := &common.CreateHostGroupParams{Name: "test-host-group", Description: "test description", HostGroupType: string(gcpgenserver.HostGroupV1betaTypeISCSIINITIATOR), Hosts: []string{}, OSType: "LINUX", AccountName: "project-number"}
		mockOrchestrator.EXPECT().CreateHostGroup(mock.Anything, createHGParams).Return(nil, errors.NewConflictErr("Host group already exists"))

		result, err := handler.V1betaCreateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(409), result.(*gcpgenserver.V1betaCreateHostGroupConflict).Code)
		assert.Equal(tt, "Host group already exists", result.(*gcpgenserver.V1betaCreateHostGroupConflict).Message)
	})
	t.Run("WhenCreateHostGroupFailsWithISE", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.HostGroupV1beta{
			ResourceId:  "test-host-group",
			Description: gcpgenserver.NewOptString("test description"),
			OsType:      gcpgenserver.HostGroupV1betaOsTypeLINUX,
			Type:        gcpgenserver.NewOptHostGroupV1betaType(gcpgenserver.HostGroupV1betaTypeISCSIINITIATOR),
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		createHGParams := &common.CreateHostGroupParams{Name: "test-host-group", Description: "test description", HostGroupType: "ISCSI_INITIATOR", Hosts: []string{}, OSType: "LINUX", AccountName: "project-number"}
		mockOrchestrator.EXPECT().CreateHostGroup(mock.Anything, createHGParams).Return(nil, errors.New("some error"))

		result, err := handler.V1betaCreateHostGroup(context.Background(), req, params)

		assert.NotNil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaCreateHostGroupInternalServerError).Code)
		assert.Equal(tt, "Internal server error", result.(*gcpgenserver.V1betaCreateHostGroupInternalServerError).Message)
	})
	t.Run("WhenCreateHostGroupWithHosts>128", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		// Create 129 hosts (one more than maxHostsPerHG limit of 128)
		hosts := make([]string, 129)
		for i := 0; i < 129; i++ {
			hosts[i] = fmt.Sprintf("host%d", i+1)
		}

		req := &gcpgenserver.HostGroupV1beta{
			ResourceId:  "test-host-group",
			Description: gcpgenserver.NewOptString("test description"),
			OsType:      gcpgenserver.HostGroupV1betaOsTypeLINUX,
			Type:        gcpgenserver.NewOptHostGroupV1betaType(gcpgenserver.HostGroupV1betaTypeISCSIINITIATOR),
			Hosts:       hosts,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaCreateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreateHostGroupBadRequest).Code)
		assert.Equal(tt, fmt.Sprintf("Host group cannot have more than %d hosts", maxHostsPerHG), result.(*gcpgenserver.V1betaCreateHostGroupBadRequest).Message)
	})
	t.Run("WhenCreateHostGroupWithSuccess", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.HostGroupV1beta{
			ResourceId:  "test-host-group",
			Description: gcpgenserver.NewOptString("test description"),
			OsType:      gcpgenserver.HostGroupV1betaOsTypeLINUX,
			Type:        gcpgenserver.NewOptHostGroupV1betaType(gcpgenserver.HostGroupV1betaTypeISCSIINITIATOR),
			Hosts:       []string{"host1", "host2", "host1"},
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		createHGParams := &common.CreateHostGroupParams{Name: "test-host-group", Description: "test description", HostGroupType: "ISCSI_INITIATOR", Hosts: []string{"host1", "host2"}, OSType: "LINUX", AccountName: "project-number"}
		mockOrchestrator.EXPECT().CreateHostGroup(mock.Anything, createHGParams).Return(&models.HostGroup{
			BaseModel:     models.BaseModel{},
			Name:          "abcd",
			Description:   "abcd",
			State:         "READY",
			StateDetails:  "READY",
			OSType:        "linux",
			Hosts:         []string{"host1", "host2"},
			HostGroupType: "",
			AccountName:   "abcd",
		}, nil)

		result, err := handler.V1betaCreateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, true, result.(*gcpgenserver.V1betaCreateHostGroupOK).Done.Value)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/invalid-location-id/operations/00000000-0000-0000-0000-000000000000", result.(*gcpgenserver.V1betaCreateHostGroupOK).Name.Value)
	})
	t.Run("WhenCreateHostGroupWithSuccessWithTypeDefault", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.HostGroupV1beta{
			ResourceId:  "test-host-group",
			Description: gcpgenserver.NewOptString("test description"),
			OsType:      gcpgenserver.HostGroupV1betaOsTypeLINUX,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		createHGParams := &common.CreateHostGroupParams{Name: "test-host-group", Description: "test description", HostGroupType: "UNSPECIFIED", Hosts: []string{}, OSType: "LINUX", AccountName: "project-number"}
		mockOrchestrator.EXPECT().CreateHostGroup(mock.Anything, createHGParams).Return(&models.HostGroup{
			BaseModel:     models.BaseModel{},
			Name:          "abcd",
			Description:   "test description",
			State:         "READY",
			StateDetails:  "READY",
			OSType:        "linux",
			Hosts:         []string{"a", "b"},
			HostGroupType: "UNSPECIFIED",
			AccountName:   "abcd",
		}, nil)

		result, err := handler.V1betaCreateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, true, result.(*gcpgenserver.V1betaCreateHostGroupOK).Done.Value)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/invalid-location-id/operations/00000000-0000-0000-0000-000000000000", result.(*gcpgenserver.V1betaCreateHostGroupOK).Name.Value)
	})
}

func TestV1betaUpdateHostGroup(t *testing.T) {
	t.Run("WhenParseAndValidateRegionAndZoneFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "non-existent-host-group-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{Message: "some error", Code: 400}
		}

		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		req := &gcpgenserver.HostGroupUpdateV1beta{
			Description: gcpgenserver.NewOptString("updated description"),
			Hosts:       []string{"host1", "host2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaUpdateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaUpdateHostGroupBadRequest).Code)
		assert.Equal(tt, "some error", result.(*gcpgenserver.V1betaUpdateHostGroupBadRequest).Message)
	})
	t.Run("WhenGetHostGroupFailsWithNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "non-existent-host-group-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", nil
		}

		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		req := &gcpgenserver.HostGroupUpdateV1beta{
			Description: gcpgenserver.NewOptString("updated description"),
			Hosts:       []string{"host1", "host2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(nil, errors.NewNotFoundErr("host group not found", nil))

		result, err := handler.V1betaUpdateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(404), result.(*gcpgenserver.V1betaUpdateHostGroupNotFound).Code)
		assert.Equal(tt, "host group not found", result.(*gcpgenserver.V1betaUpdateHostGroupNotFound).Message)
	})
	t.Run("WhenGetHostGroupFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "non-existent-host-group-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", nil
		}

		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		req := &gcpgenserver.HostGroupUpdateV1beta{
			Description: gcpgenserver.NewOptString("updated description"),
			Hosts:       []string{"host1", "host2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(nil, errors.New("some error"))

		result, err := handler.V1betaUpdateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaUpdateHostGroupInternalServerError).Code)
		assert.Equal(tt, "Internal server error", result.(*gcpgenserver.V1betaUpdateHostGroupInternalServerError).Message)
	})
	t.Run("WhenUpdateHostGroupFailsWithValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "host-group-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", nil
		}

		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		req := &gcpgenserver.HostGroupUpdateV1beta{
			Description: gcpgenserver.NewOptString("updated description"),
			Hosts:       []string{"host1", "host2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(&models.HostGroup{}, nil)
		mockOrchestrator.EXPECT().UpdateHostGroup(mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("invalid input"))

		result, err := handler.V1betaUpdateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaUpdateHostGroupBadRequest).Code)
		assert.Equal(tt, "invalid input", result.(*gcpgenserver.V1betaUpdateHostGroupBadRequest).Message)
	})
	t.Run("WhenUpdateHostGroupFailsWithError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdateHostGroupParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "host-group-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", nil
		}

		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		req := &gcpgenserver.HostGroupUpdateV1beta{
			Description: gcpgenserver.NewOptString("updated description"),
			Hosts:       []string{"host1", "host2"},
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(&models.HostGroup{}, nil)
		mockOrchestrator.EXPECT().UpdateHostGroup(mock.Anything, mock.Anything).Return(nil, "", errors.Errorf("invalid input"))

		result, err := handler.V1betaUpdateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaUpdateHostGroupInternalServerError).Code)
		assert.Equal(tt, "Internal server error", result.(*gcpgenserver.V1betaUpdateHostGroupInternalServerError).Message)
	})
	t.Run("WhenUpdateHostGroupHosts>128Fails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdateHostGroupParams{
			LocationId:    "valid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "host-group-id",
		}

		// Create 129 hosts (one more than maxHostsPerHG limit of 128)
		hosts := make([]string, 129)
		for i := 0; i < 129; i++ {
			hosts[i] = fmt.Sprintf("host%d", i+1)
		}

		req := &gcpgenserver.HostGroupUpdateV1beta{
			Description: gcpgenserver.NewOptString("updated description"),
			Hosts:       hosts,
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", nil
		}

		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(&models.HostGroup{}, nil)

		result, err := handler.V1betaUpdateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaUpdateHostGroupBadRequest).Code)
		assert.Equal(tt, fmt.Sprintf("Host group cannot have more than %d hosts", maxHostsPerHG), result.(*gcpgenserver.V1betaUpdateHostGroupBadRequest).Message)
	})
	t.Run("WhenUpdateHostGroupFailsWithNoIQN", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdateHostGroupParams{
			LocationId:    "valid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "host-group-id",
		}

		req := &gcpgenserver.HostGroupUpdateV1beta{
			Description: gcpgenserver.NewOptString("updated description"),
			Hosts:       []string{},
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", nil
		}

		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(&models.HostGroup{}, nil)

		result, err := handler.V1betaUpdateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaUpdateHostGroupBadRequest).Code)
		assert.Equal(tt, "Host group should have at least one IQN", result.(*gcpgenserver.V1betaUpdateHostGroupBadRequest).Message)
	})
	t.Run("WhenUpdateHostGroupSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdateHostGroupParams{
			LocationId:    "valid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "host-group-id",
		}

		req := &gcpgenserver.HostGroupUpdateV1beta{
			Description: gcpgenserver.NewOptString("updated description"),
			Hosts:       []string{"host1", "host2"},
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", nil
		}

		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(&models.HostGroup{}, nil)
		mockOrchestrator.EXPECT().UpdateHostGroup(mock.Anything, mock.Anything).Return(&models.HostGroup{}, "job-id", nil)

		result, err := handler.V1betaUpdateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/valid-location-id/operations/job-id", result.(*gcpgenserver.OperationV1beta).Name.Value)
		assert.Equal(tt, false, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})
	t.Run("WhenUpdateHostGroupSucceedsWithOnlyDescriptions", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdateHostGroupParams{
			LocationId:    "valid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "host-group-id",
		}

		req := &gcpgenserver.HostGroupUpdateV1beta{
			Description: gcpgenserver.NewOptString("updated description"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", nil
		}

		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(&models.HostGroup{}, nil)
		mockOrchestrator.EXPECT().UpdateHostGroup(mock.Anything, mock.Anything).Return(&models.HostGroup{}, "job-id", nil)

		result, err := handler.V1betaUpdateHostGroup(context.Background(), req, params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/valid-location-id/operations/job-id", result.(*gcpgenserver.OperationV1beta).Name.Value)
		assert.Equal(tt, false, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})
}

// File: google-proxy/api/endpoints/hostgroup_endpoints_test.go

func TestV1betaDescribeHostGroup(t *testing.T) {
	t.Run("WhenHostGroupNotFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDescribeHostGroupParams{
			LocationId:    "valid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "non-existent-host-group-id",
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(nil, errors.NewNotFoundErr("host group not found", nil))

		result, err := handler.V1betaDescribeHostGroup(context.Background(), params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(404), result.(*gcpgenserver.V1betaDescribeHostGroupNotFound).Code)
		assert.Equal(tt, "host group not found", result.(*gcpgenserver.V1betaDescribeHostGroupNotFound).Message)
	})

	t.Run("WhenGetHostGroupFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDescribeHostGroupParams{
			LocationId:    "valid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "host-group-id",
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(nil, errors.New("some error"))

		result, err := handler.V1betaDescribeHostGroup(context.Background(), params)

		assert.Error(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaDescribeHostGroupInternalServerError).Code)
	})

	t.Run("WhenHostGroupFound", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDescribeHostGroupParams{
			LocationId:    "valid-location-id",
			ProjectNumber: "project-number",
			HostGroupId:   "host-group-id",
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		hostGroup := &models.HostGroup{
			BaseModel: models.BaseModel{
				ID:        1,
				UUID:      "host-group-id",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:         "test-host-group",
			State:        "Ready",
			StateDetails: "Available",
			Description:  "Test host group",
			Hosts:        []string{"host1", "host2"},
			OSType:       "LINUX",
		}

		mockOrchestrator.EXPECT().GetHostGroup(mock.Anything, params.HostGroupId, params.ProjectNumber).Return(hostGroup, nil)

		result, err := handler.V1betaDescribeHostGroup(context.Background(), params)

		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "host-group-id", result.(*gcpgenserver.HostGroupV1beta).HostGroupId.Value)
		assert.Equal(tt, "Test host group", result.(*gcpgenserver.HostGroupV1beta).Description.Value)
	})
}
