package vsa

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/security"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
)

func TestGetClusterUUID_Success(t *testing.T) {
	mockClient := new(ontaprest.MockRESTClient)
	mockCluster := new(ontaprest.MockClusterClient)
	mockClient.On("Cluster").Return(mockCluster)
	mockCluster.On("GetClusterUUID").Return("cluster-uuid", nil).Once()

	orig := getOntapClientFunc
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	t.Cleanup(func() { getOntapClientFunc = orig })

	uuid, err := (&OntapRestProvider{}).GetClusterUUID()
	require.NoError(t, err)
	assert.Equal(t, "cluster-uuid", uuid)
}

func TestGetClusterUUID_APIError(t *testing.T) {
	mockClient := new(ontaprest.MockRESTClient)
	mockCluster := new(ontaprest.MockClusterClient)
	mockClient.On("Cluster").Return(mockCluster)
	mockCluster.On("GetClusterUUID").Return("", errors.New("cluster api failed")).Once()

	orig := getOntapClientFunc
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	t.Cleanup(func() { getOntapClientFunc = orig })

	_, err := (&OntapRestProvider{}).GetClusterUUID()
	assert.EqualError(t, err, "cluster api failed")
}

func TestGetRoleOwnerUUID_Success(t *testing.T) {
	mockSecurity := new(ontaprest.MockSecurityClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Security").Return(mockSecurity)

	roleName := "gcnv_r_test"
	ownerUUID := "95be11a7-0000-0000-0000-000000000001"
	mockSecurity.On("RoleCollectionGet", mock.Anything).Return(&ontaprest.RoleCollectionGetResponse{
		RoleCollectionGetOK: &security.RoleCollectionGetOK{
			Payload: &models.RoleResponse{
				RoleResponseInlineRecords: []*models.Role{{
					Name:  &roleName,
					Owner: &models.RoleInlineOwner{UUID: &ownerUUID},
				}},
			},
		},
	}, nil).Once()

	orig := getOntapClientFunc
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	t.Cleanup(func() { getOntapClientFunc = orig })

	got, err := (&OntapRestProvider{}).GetRoleOwnerUUID("gcnv_r_test")
	require.NoError(t, err)
	assert.Equal(t, ownerUUID, got)
}

func TestGetRoleOwnerUUID_EmptyName(t *testing.T) {
	_, err := (&OntapRestProvider{}).GetRoleOwnerUUID("  ")
	assert.EqualError(t, err, "role name is required")
}

func TestGetRoleOwnerUUID_NotFound(t *testing.T) {
	mockSecurity := new(ontaprest.MockSecurityClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Security").Return(mockSecurity)
	mockSecurity.On("RoleCollectionGet", mock.Anything).Return(&ontaprest.RoleCollectionGetResponse{
		RoleCollectionGetOK: &security.RoleCollectionGetOK{Payload: &models.RoleResponse{}},
	}, nil).Once()

	orig := getOntapClientFunc
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	t.Cleanup(func() { getOntapClientFunc = orig })

	_, err := (&OntapRestProvider{}).GetRoleOwnerUUID("missing-role")
	assert.EqualError(t, err, `role "missing-role" not found`)
}

func TestGetRoleOwnerUUID_NoOwner(t *testing.T) {
	roleName := "gcnv_r_test"
	mockSecurity := new(ontaprest.MockSecurityClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Security").Return(mockSecurity)
	mockSecurity.On("RoleCollectionGet", mock.Anything).Return(&ontaprest.RoleCollectionGetResponse{
		RoleCollectionGetOK: &security.RoleCollectionGetOK{
			Payload: &models.RoleResponse{
				RoleResponseInlineRecords: []*models.Role{{Name: &roleName}},
			},
		},
	}, nil).Once()

	orig := getOntapClientFunc
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	t.Cleanup(func() { getOntapClientFunc = orig })

	_, err := (&OntapRestProvider{}).GetRoleOwnerUUID("gcnv_r_test")
	assert.EqualError(t, err, `role "gcnv_r_test" has no owner uuid`)
}

func TestCreateSecurityAccount_DelegatesToSecurityClient(t *testing.T) {
	mockSecurity := new(ontaprest.MockSecurityClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Security").Return(mockSecurity)
	mockSecurity.On("AccountCreate", mock.MatchedBy(func(params *ontaprest.AccountCreateParams) bool {
		return params.Name == "svc-user" && params.Password == "secret" &&
			params.RoleName == "gcnv_r_test" && params.OwnerUUID == "cluster-uuid"
	})).Return(nil).Once()

	orig := getOntapClientFunc
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	t.Cleanup(func() { getOntapClientFunc = orig })

	err := (&OntapRestProvider{}).CreateSecurityAccount(CreateSecurityAccountParams{
		Name: "svc-user", Password: "secret", RoleName: "gcnv_r_test", ClusterUUID: "cluster-uuid",
	})
	require.NoError(t, err)
}

func TestGetSecurityAccount_MapsOwnerAndRole(t *testing.T) {
	mockSecurity := new(ontaprest.MockSecurityClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Security").Return(mockSecurity)
	mockSecurity.On("AccountGet", mock.MatchedBy(func(params *ontaprest.AccountGetParams) bool {
		return params.Name == "svc-user" &&
			assert.ElementsMatch(t, []string{"name", "applications", "owner", "role"}, params.Fields)
	})).Return(&ontaprest.SecurityAccount{
		Name:  "svc-user",
		Owner: &ontaprest.SecurityAccountOwner{UUID: "owner-uuid"},
		Role:  &ontaprest.SecurityAccountRole{Name: "gcnv_r_test"},
	}, nil).Once()

	orig := getOntapClientFunc
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	t.Cleanup(func() { getOntapClientFunc = orig })

	account, err := (&OntapRestProvider{}).GetSecurityAccount(GetSecurityAccountParams{Name: "svc-user"})
	require.NoError(t, err)
	require.NotNil(t, account)
	assert.Equal(t, "svc-user", account.Name)
	assert.Equal(t, "owner-uuid", account.OwnerUUID)
	require.NotNil(t, account.Role)
	assert.Equal(t, "gcnv_r_test", account.Role.Name)
}

func TestGetSecurityAccount_NilAccount(t *testing.T) {
	mockSecurity := new(ontaprest.MockSecurityClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Security").Return(mockSecurity)
	mockSecurity.On("AccountGet", mock.Anything).Return(nil, nil).Once()

	orig := getOntapClientFunc
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	t.Cleanup(func() { getOntapClientFunc = orig })

	account, err := (&OntapRestProvider{}).GetSecurityAccount(GetSecurityAccountParams{Name: "svc-user"})
	require.NoError(t, err)
	assert.Nil(t, account)
}

func TestDeleteSecurityAccount_DelegatesToSecurityClient(t *testing.T) {
	mockSecurity := new(ontaprest.MockSecurityClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Security").Return(mockSecurity)
	mockSecurity.On("AccountDelete", mock.MatchedBy(func(params *ontaprest.AccountDeleteParams) bool {
		return params.Name == "svc-user" && params.OwnerUUID == "owner-uuid"
	})).Return(nil).Once()

	orig := getOntapClientFunc
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	t.Cleanup(func() { getOntapClientFunc = orig })

	err := (&OntapRestProvider{}).DeleteSecurityAccount(DeleteSecurityAccountParams{
		Name: "svc-user", OwnerUUID: "owner-uuid",
	})
	require.NoError(t, err)
}
