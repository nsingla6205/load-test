package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	ontapModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontap_rest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/testsuite"
)

func TestUpdateIGroups(t *testing.T) {
	t.Run("WhenUpdateIGroupsSuccessfulWithMultipleVolumesInSamePool", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		oldProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = oldProviderByNode }()

		mockStorage := database.NewMockStorage(t)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		}

		volumes := []*datamodel.Volume{
			{
				Name: "volume-uuid1",
				Pool: pool,
				Svm: &datamodel.Svm{
					Name: "svm1",
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
					BlockProperties: &datamodel.BlockProperties{
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "hg-uuid1",
								HostQNs:       []string{"a1", "b1"},
							},
							{
								HostGroupUUID: "hg-uuid2",
								HostQNs:       []string{"a1", "b1"},
							},
						},
					},
				},
			},
			{
				Name: "volume-uuid2",
				Pool: pool,
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
					BlockProperties: &datamodel.BlockProperties{
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "hg-uuid1",
								HostQNs:       []string{"b1"},
							},
						},
					},
				},
			},
		}

		mockStorage.On("GetAllVolumesForHG", ctx, mock.Anything, mock.Anything).Return(volumes, nil)

		nodes := []*datamodel.Node{{
			EndpointAddress: "1.1.1.1",
		}}

		mockStorage.On("GetNodesByPoolID", ctx, mock.Anything).Return(nodes, nil)

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}
		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{
				UUID: "hg-uuid1",
			},
			Name:        "hg1",
			Description: "test-description",
			Hosts: datamodel.Hosts{
				Hosts: []string{"c1", "d1"},
			},
			AccountID: 1,
		}

		IGroupOntap := &ontap_rest.Igroup{
			Igroup: ontapModels.Igroup{
				UUID: nillable.GetStringPtr("1234"),
				IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{{
					Name: nillable.GetStringPtr("a1"),
				}, {
					Name: nillable.GetStringPtr("b1"),
				},
				},
			},
		}
		mockProvider.On("IgroupExists", hg.Name, mock.Anything).Return(true, IGroupOntap, nil)

		mockProvider.On("IgroupAddInitiator", vsa.IgroupAddInitiator{
			Initiator:  []string{"c1", "d1"},
			IgroupUUID: "1234",
		}).Return(nil)

		mockProvider.On("IgroupDeleteInitiator", vsa.IgroupDeleteInitiator{
			InitiatorName: "a1",
			IgroupUUID:    "1234",
		}).Return(nil)

		mockProvider.On("IgroupDeleteInitiator", vsa.IgroupDeleteInitiator{
			InitiatorName: "b1",
			IgroupUUID:    "1234",
		}).Return(nil)

		mockStorage.On("UpdateVolume", ctx, mock.Anything).Return(nil)

		err := activity.UpdateIGroups(ctx, hg)

		assert.NoError(t, err)
		mockProvider.AssertExpectations(t)
	})
	t.Run("WhenUpdateIGroupsSuccessfulWithMultipleVolumesInDiffPool", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		oldProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = oldProviderByNode }()

		mockStorage := database.NewMockStorage(t)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		pool1 := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid1",
			},
			Name: "test-pool1",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		}

		pool2 := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid2",
			},
			Name: "test-pool2",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		}

		volumes := []*datamodel.Volume{
			{
				Name: "volume-uuid1",
				Pool: pool1,
				Svm: &datamodel.Svm{
					Name: "svm1",
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
					BlockProperties: &datamodel.BlockProperties{
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "hg-uuid1",
								HostQNs:       []string{"a1", "b1"},
							},
						},
					},
				},
			},
			{
				Name: "volume-uuid2",
				Pool: pool2,
				Svm: &datamodel.Svm{
					Name: "svm1",
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
					BlockProperties: &datamodel.BlockProperties{
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "hg-uuid1",
								HostQNs:       []string{"a1", "b1"},
							},
						},
					},
				},
			},
		}

		mockStorage.On("GetAllVolumesForHG", ctx, mock.Anything, mock.Anything).Return(volumes, nil)

		nodes := []*datamodel.Node{{
			EndpointAddress: "1.1.1.1",
		}}

		mockStorage.On("GetNodesByPoolID", ctx, mock.Anything).Return(nodes, nil)

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}
		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{
				UUID: "hg-uuid1",
			},
			Name:        "hg1",
			Description: "test-description",
			Hosts: datamodel.Hosts{
				Hosts: []string{"c1", "d1"},
			},
			AccountID: 1,
		}

		IGroupOntap := &ontap_rest.Igroup{
			Igroup: ontapModels.Igroup{
				UUID: nillable.GetStringPtr("1234"),
				IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{{
					Name: nillable.GetStringPtr("a1"),
				}, {
					Name: nillable.GetStringPtr("b1"),
				},
				},
			},
		}
		mockProvider.On("IgroupExists", hg.Name, mock.Anything).Return(true, IGroupOntap, nil)

		mockProvider.On("IgroupAddInitiator", vsa.IgroupAddInitiator{
			Initiator:  []string{"c1", "d1"},
			IgroupUUID: "1234",
		}).Return(nil)

		mockProvider.On("IgroupDeleteInitiator", vsa.IgroupDeleteInitiator{
			InitiatorName: "a1",
			IgroupUUID:    "1234",
		}).Return(nil)

		mockProvider.On("IgroupDeleteInitiator", vsa.IgroupDeleteInitiator{
			InitiatorName: "b1",
			IgroupUUID:    "1234",
		}).Return(nil)

		mockStorage.On("UpdateVolume", ctx, mock.Anything).Return(nil)

		err := activity.UpdateIGroups(ctx, hg)

		assert.NoError(t, err)
		mockProvider.AssertExpectations(t)
	})
	t.Run("WhenUpdateIGroupsAlreadySuccessfulWithMultipleVolumesInSamePool", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		oldProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = oldProviderByNode }()

		mockStorage := database.NewMockStorage(t)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
		}

		volumes := []*datamodel.Volume{
			{
				Name: "volume-uuid1",
				Pool: pool,
				Svm: &datamodel.Svm{
					Name: "svm1",
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
					BlockProperties: &datamodel.BlockProperties{
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "hg-uuid1",
								HostQNs:       []string{"a1", "b1"},
							},
						},
					},
				},
			},
		}

		mockStorage.On("GetAllVolumesForHG", ctx, mock.Anything, mock.Anything).Return(volumes, nil)
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}
		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{
				UUID: "hg-uuid1",
			},
			Name:        "hg1",
			Description: "test-description",
			Hosts: datamodel.Hosts{
				Hosts: []string{"a1", "b1"},
			},
			AccountID: 1,
		}

		err := activity.UpdateIGroups(ctx, hg)

		assert.NoError(t, err)
		mockProvider.AssertExpectations(t)
	})
	t.Run("WhenUpdateIGroupsIsUpdatedOnOntap", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		oldProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = oldProviderByNode }()

		mockStorage := database.NewMockStorage(t)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		}

		volumes := []*datamodel.Volume{
			{
				Name: "volume-uuid1",
				Pool: pool,
				Svm: &datamodel.Svm{
					Name: "svm1",
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
					BlockProperties: &datamodel.BlockProperties{
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "hg-uuid1",
								HostQNs:       []string{"c1", "b1"},
							},
						},
					},
				},
			},
		}

		mockStorage.On("GetAllVolumesForHG", ctx, mock.Anything, mock.Anything).Return(volumes, nil)
		nodes := []*datamodel.Node{{
			EndpointAddress: "1.1.1.1",
		}}

		mockStorage.On("GetNodesByPoolID", ctx, mock.Anything).Return(nodes, nil)

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}
		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{
				UUID: "hg-uuid1",
			},
			Name:        "hg1",
			Description: "test-description",
			Hosts: datamodel.Hosts{
				Hosts: []string{"a1", "b1"},
			},
			AccountID: 1,
		}

		IGroupOntap := &ontap_rest.Igroup{
			Igroup: ontapModels.Igroup{
				UUID: nillable.GetStringPtr("1234"),
				IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{{
					Name: nillable.GetStringPtr("a1"),
				}, {
					Name: nillable.GetStringPtr("b1"),
				},
				},
			},
		}
		mockProvider.On("IgroupExists", mock.Anything, mock.Anything).Return(true, IGroupOntap, nil)

		mockStorage.On("UpdateVolume", ctx, mock.Anything).Return(nil)

		err := activity.UpdateIGroups(ctx, hg)

		assert.NoError(t, err)
		mockProvider.AssertExpectations(t)
	})
}

func TestUpdateIGroups_GetProviderByNodeFailure(t *testing.T) {
	t.Run("WhenGetProviderByNodeFails", func(t *testing.T) {
		oldProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = oldProviderByNode }()

		mockStorage := database.NewMockStorage(t)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		}

		volumes := []*datamodel.Volume{
			{
				Name:   "volume-uuid1",
				Pool:   pool,
				PoolID: 1,
				Svm: &datamodel.Svm{
					Name: "svm1",
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
					BlockProperties: &datamodel.BlockProperties{
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "hg-uuid1",
								HostQNs:       []string{"a1", "b1"},
							},
						},
					},
				},
			},
		}

		mockStorage.On("GetAllVolumesForHG", ctx, mock.Anything, mock.Anything).Return(volumes, nil)

		nodes := []*datamodel.Node{{
			EndpointAddress: "1.1.1.1",
		}}

		mockStorage.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil)

		// Mock GetProviderByNode to return an error
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, assert.AnError
		}

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}
		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{
				UUID: "hg-uuid1",
			},
			Name:        "hg1",
			Description: "test-description",
			Hosts: datamodel.Hosts{
				Hosts: []string{"c1", "d1"},
			},
			AccountID: 1,
		}

		err := activity.UpdateIGroups(ctx, hg)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), assert.AnError.Error())
		mockStorage.AssertExpectations(t)
	})
}

func TestHandleQNsInHostGroup_IgroupAddInitiatorFailure(t *testing.T) {
	t.Run("WhenIgroupAddInitiatorFails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{
				UUID: "hg-uuid1",
			},
			Name:        "hg1",
			Description: "test-description",
			Hosts: datamodel.Hosts{
				Hosts: []string{"c1", "d1"},
			},
			AccountID: 1,
		}

		IGroupOntap := &ontap_rest.Igroup{
			Igroup: ontapModels.Igroup{
				UUID: nillable.GetStringPtr("1234"),
				IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{{
					Name: nillable.GetStringPtr("a1"),
				}},
			},
		}

		mockProvider.On("IgroupExists", hg.Name, mock.Anything).Return(true, IGroupOntap, nil)
		mockProvider.On("IgroupAddInitiator", vsa.IgroupAddInitiator{
			Initiator:  []string{"c1", "d1"},
			IgroupUUID: "1234",
		}).Return(assert.AnError)

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		// Call the private function through reflection or create a test helper
		// For now, we'll test this through the public UpdateIGroups method
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		}

		volumes := []*datamodel.Volume{
			{
				Name:   "volume-uuid1",
				Pool:   pool,
				PoolID: 1,
				Svm: &datamodel.Svm{
					Name: "svm1",
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
					BlockProperties: &datamodel.BlockProperties{
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "hg-uuid1",
								HostQNs:       []string{"a1"},
							},
						},
					},
				},
			},
		}

		mockStorage.On("GetAllVolumesForHG", ctx, mock.Anything, mock.Anything).Return(volumes, nil)

		nodes := []*datamodel.Node{{
			EndpointAddress: "1.1.1.1",
		}}

		mockStorage.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil)

		oldProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = oldProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		err := activity.UpdateIGroups(ctx, hg)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), assert.AnError.Error())
		mockProvider.AssertExpectations(t)
		mockStorage.AssertExpectations(t)
	})
}

func TestHandleQNsInHostGroup_IgroupDeleteInitiatorFailure(t *testing.T) {
	t.Run("WhenIgroupDeleteInitiatorFails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{
				UUID: "hg-uuid1",
			},
			Name:        "hg1",
			Description: "test-description",
			Hosts: datamodel.Hosts{
				Hosts: []string{"c1"},
			},
			AccountID: 1,
		}

		IGroupOntap := &ontap_rest.Igroup{
			Igroup: ontapModels.Igroup{
				UUID: nillable.GetStringPtr("1234"),
				IgroupInlineInitiators: []*ontapModels.IgroupInlineInitiatorsInlineArrayItem{
					{Name: nillable.GetStringPtr("a1")},
					{Name: nillable.GetStringPtr("b1")},
				},
			},
		}

		mockProvider.On("IgroupExists", hg.Name, mock.Anything).Return(true, IGroupOntap, nil)
		mockProvider.On("IgroupAddInitiator", vsa.IgroupAddInitiator{
			Initiator:  []string{"c1"},
			IgroupUUID: "1234",
		}).Return(nil)
		mockProvider.On("IgroupDeleteInitiator", vsa.IgroupDeleteInitiator{
			InitiatorName: "a1",
			IgroupUUID:    "1234",
		}).Return(assert.AnError)

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		}

		volumes := []*datamodel.Volume{
			{
				Name:   "volume-uuid1",
				Pool:   pool,
				PoolID: 1,
				Svm: &datamodel.Svm{
					Name: "svm1",
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
					BlockProperties: &datamodel.BlockProperties{
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "hg-uuid1",
								HostQNs:       []string{"a1", "b1"},
							},
						},
					},
				},
			},
		}

		mockStorage.On("GetAllVolumesForHG", ctx, mock.Anything, mock.Anything).Return(volumes, nil)

		nodes := []*datamodel.Node{{
			EndpointAddress: "1.1.1.1",
		}}

		mockStorage.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil)

		oldProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = oldProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		err := activity.UpdateIGroups(ctx, hg)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), assert.AnError.Error())
		mockProvider.AssertExpectations(t)
		mockStorage.AssertExpectations(t)
	})
}

// Test cases for ListHostGroups method
func TestListHostGroups(t *testing.T) {
	t.Run("ListHostGroups_Success", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		// Use a mock account with proper fields based on the actual datamodel
		accountID := int64(123)
		projectNumber := "test-project-123"

		// Create account struct - adjust fields based on actual datamodel.Account structure
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: accountID,
			},
		}

		expectedHostGroups := []*datamodel.HostGroup{
			{
				BaseModel: datamodel.BaseModel{
					UUID: "hg-uuid-1",
				},
				Name:      "host-group-1",
				AccountID: 123,
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "hg-uuid-2",
				},
				Name:      "host-group-2",
				AccountID: 123,
			},
		}

		mockStorage.On("GetAccount", ctx, projectNumber).Return(account, nil)
		mockStorage.On("ListHostGroupsByAccountID", ctx, accountID).Return(expectedHostGroups, nil)

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		result, err := activity.ListHostGroups(ctx, projectNumber)

		assert.NoError(t, err)
		assert.Equal(t, expectedHostGroups, result)
		assert.Len(t, result, 2)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ListHostGroups_EmptyList", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		accountID := int64(123)
		projectNumber := "test-project-123"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: accountID,
			},
		}

		var expectedHostGroups []*datamodel.HostGroup

		mockStorage.On("GetAccount", ctx, projectNumber).Return(account, nil)
		mockStorage.On("ListHostGroupsByAccountID", ctx, accountID).Return(expectedHostGroups, nil)

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		result, err := activity.ListHostGroups(ctx, projectNumber)

		assert.NoError(t, err)
		assert.Empty(t, result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ListHostGroups_GetAccountError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		projectNumber := "test-project-123"
		expectedError := errors.New("account not found")

		mockStorage.On("GetAccount", ctx, projectNumber).Return(nil, expectedError)

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		result, err := activity.ListHostGroups(ctx, projectNumber)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, expectedError, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ListHostGroups_ListError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		accountID := int64(123)
		projectNumber := "test-project-123"

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: accountID,
			},
		}

		expectedError := errors.New("database connection error")

		mockStorage.On("GetAccount", ctx, projectNumber).Return(account, nil)
		mockStorage.On("ListHostGroupsByAccountID", ctx, accountID).Return(nil, expectedError)

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		result, err := activity.ListHostGroups(ctx, projectNumber)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, expectedError, err)
		mockStorage.AssertExpectations(t)
	})
}

// Test cases for DeleteHostGroup method
func TestDeleteHostGroup(t *testing.T) {
	t.Run("DeleteHostGroup_Success", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		hostGroupUUID := "hg-uuid-123"
		accountID := int64(123)

		expectedHostGroup := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{
				UUID: hostGroupUUID,
			},
			AccountID: accountID,
			Name:      "test-host-group",
		}

		mockStorage.On("DeleteHostGroup", ctx, hostGroupUUID, accountID).Return(expectedHostGroup, nil)

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		result, err := activity.DeleteHostGroup(ctx, hostGroupUUID, accountID)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, expectedHostGroup, result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DeleteHostGroup_NotFound", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		hostGroupUUID := "non-existent-hg-uuid"
		accountID := int64(123)
		expectedError := errors.New("host group not found")

		mockStorage.On("DeleteHostGroup", ctx, hostGroupUUID, accountID).Return(nil, expectedError)

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		result, err := activity.DeleteHostGroup(ctx, hostGroupUUID, accountID)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, expectedError, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DeleteHostGroup_DatabaseError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		hostGroupUUID := "hg-uuid-123"
		accountID := int64(123)
		expectedError := errors.New("database deletion failed")

		mockStorage.On("DeleteHostGroup", ctx, hostGroupUUID, accountID).Return(nil, expectedError)

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		result, err := activity.DeleteHostGroup(ctx, hostGroupUUID, accountID)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, expectedError, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DeleteHostGroup_EmptyUUID", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		hostGroupUUID := ""
		accountID := int64(123)

		mockStorage.On("DeleteHostGroup", ctx, hostGroupUUID, accountID).Return(nil, errors.New("invalid host group UUID"))

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		result, err := activity.DeleteHostGroup(ctx, hostGroupUUID, accountID)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid host group UUID")
		mockStorage.AssertExpectations(t)
	})

	t.Run("DeleteHostGroup_ZeroAccountID", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		hostGroupUUID := "hg-uuid-123"
		accountID := int64(0)

		mockStorage.On("DeleteHostGroup", ctx, hostGroupUUID, accountID).Return(nil, errors.New("invalid account ID"))

		activity := HostGroupUpdateActivity{
			SE: mockStorage,
		}

		result, err := activity.DeleteHostGroup(ctx, hostGroupUUID, accountID)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "invalid account ID")
		mockStorage.AssertExpectations(t)
	})
}

func TestUpdateHostGroupState(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()
		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "hg-uuid"},
			AccountID: 5,
		}

		mockStorage.On("UpdateHostGroupsState", ctx, []string{"hg-uuid"}, int64(5),
			datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(nil)

		activity := HostGroupUpdateActivity{SE: mockStorage}
		err := activity.UpdateHostGroupState(ctx, hg, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)

		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("Error", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()
		hg := &datamodel.HostGroup{
			BaseModel: datamodel.BaseModel{UUID: "hg-uuid"},
			AccountID: 5,
		}

		mockStorage.On("UpdateHostGroupsState", ctx, []string{"hg-uuid"}, int64(5),
			datamodel.LifeCycleStateError, datamodel.LifeCycleStateUpdateErrorDetails).Return(errors.New("db error"))

		activity := HostGroupUpdateActivity{SE: mockStorage}
		err := activity.UpdateHostGroupState(ctx, hg, datamodel.LifeCycleStateError, datamodel.LifeCycleStateUpdateErrorDetails)

		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestRecordHeartbeat(t *testing.T) {
	t.Run("NoOpOutsideActivityContext", func(t *testing.T) {
		// Called with a plain context (as in direct-call unit tests) it must be a
		// no-op and must not panic.
		assert.NotPanics(t, func() {
			recordHeartbeat(context.Background(), "noop")
		})
	})

	t.Run("RecordsInsideActivityContext", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestActivityEnvironment()
		hb := func(ctx context.Context) error {
			recordHeartbeat(ctx, "beat")
			return nil
		}
		env.RegisterActivity(hb)

		_, err := env.ExecuteActivity(hb)
		assert.NoError(t, err)
	})
}
