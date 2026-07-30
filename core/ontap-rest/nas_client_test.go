package ontap_rest

import (
	"context"
	"errors"
	"testing"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	nas "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/n_a_s"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	priv "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/operations"
	privmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// mockNASClient implements the nas.ClientService interface for testing
type mockNASClient struct {
	err       error
	response  interface{}
	responses []interface{} // For pagination tests that need multiple responses
	callCount int
}

// Implement only the methods we need for testing
func (m *mockNASClient) ExportPolicyCreate(params *nas.ExportPolicyCreateParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.ExportPolicyCreateCreated, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*nas.ExportPolicyCreateCreated); ok {
		return resp, nil
	}
	return &nas.ExportPolicyCreateCreated{}, nil
}

func (m *mockNASClient) ExportPolicyCollectionGet(params *nas.ExportPolicyCollectionGetParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.ExportPolicyCollectionGetOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*nas.ExportPolicyCollectionGetOK); ok {
		return resp, nil
	}
	return &nas.ExportPolicyCollectionGetOK{}, nil
}

func (m *mockNASClient) ExportPolicyModify(params *nas.ExportPolicyModifyParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.ExportPolicyModifyOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.ExportPolicyModifyOK{}, nil
}

func (m *mockNASClient) ExportPolicyDeleteCollection(params *nas.ExportPolicyDeleteCollectionParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.ExportPolicyDeleteCollectionOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.ExportPolicyDeleteCollectionOK{}, nil
}

func (m *mockNASClient) NfsGet(params *nas.NfsGetParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.NfsGetOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*nas.NfsGetOK); ok {
		return resp, nil
	}
	return &nas.NfsGetOK{}, nil
}

func (m *mockNASClient) NfsCreate(params *nas.NfsCreateParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.NfsCreateCreated, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.NfsCreateCreated{}, nil
}

func (m *mockNASClient) NfsModify(params *nas.NfsModifyParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.NfsModifyOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.NfsModifyOK{}, nil
}

func (m *mockNASClient) CifsServiceCollectionGet(params *nas.CifsServiceCollectionGetParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsServiceCollectionGetOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*nas.CifsServiceCollectionGetOK); ok {
		return resp, nil
	}
	return &nas.CifsServiceCollectionGetOK{}, nil
}

func (m *mockNASClient) CifsServiceCreate(params *nas.CifsServiceCreateParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsServiceCreateCreated, *nas.CifsServiceCreateAccepted, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	if resp, ok := m.response.(*nas.CifsServiceCreateCreated); ok {
		return resp, nil, nil
	}
	if resp, ok := m.response.(*nas.CifsServiceCreateAccepted); ok {
		return nil, resp, nil
	}
	return &nas.CifsServiceCreateCreated{}, nil, nil
}

func (m *mockNASClient) CifsServiceModify(params *nas.CifsServiceModifyParams, _ runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsServiceModifyOK, *nas.CifsServiceModifyAccepted, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return &nas.CifsServiceModifyOK{}, nil, nil
}

// Implement the remaining interface methods with empty implementations
func (m *mockNASClient) AuditCreate(params *nas.AuditCreateParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.AuditCreateCreated, *nas.AuditCreateAccepted, error) {
	return nil, nil, nil
}

func (m *mockNASClient) AuditDelete(params *nas.AuditDeleteParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.AuditDeleteOK, *nas.AuditDeleteAccepted, error) {
	return nil, nil, nil
}

func (m *mockNASClient) AuditLogRedirectCreate(params *nas.AuditLogRedirectCreateParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.AuditLogRedirectCreateCreated, error) {
	return nil, nil
}

func (m *mockNASClient) AuditLogRedirectDelete(params *nas.AuditLogRedirectDeleteParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.AuditLogRedirectDeleteOK, error) {
	return nil, nil
}

func (m *mockNASClient) AuditLogRedirectGet(params *nas.AuditLogRedirectGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.AuditLogRedirectGetOK, error) {
	return nil, nil
}

func (m *mockNASClient) AuditModify(params *nas.AuditModifyParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.AuditModifyOK, *nas.AuditModifyAccepted, error) {
	return nil, nil, nil
}

func (m *mockNASClient) CifsDomainGet(params *nas.CifsDomainGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsDomainGetOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*nas.CifsDomainGetOK); ok {
		return resp, nil
	}
	return &nas.CifsDomainGetOK{}, nil
}

func (m *mockNASClient) CifsDomainModify(params *nas.CifsDomainModifyParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsDomainModifyOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.CifsDomainModifyOK{}, nil
}

func (m *mockNASClient) CifsDomainPreferredDcCreate(params *nas.CifsDomainPreferredDcCreateParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsDomainPreferredDcCreateCreated, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.CifsDomainPreferredDcCreateCreated{}, nil
}

func (m *mockNASClient) CifsDomainPreferredDcDelete(params *nas.CifsDomainPreferredDcDeleteParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsDomainPreferredDcDeleteOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.CifsDomainPreferredDcDeleteOK{}, nil
}

func (m *mockNASClient) CifsServiceDelete(params *nas.CifsServiceDeleteParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsServiceDeleteOK, *nas.CifsServiceDeleteAccepted, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return &nas.CifsServiceDeleteOK{}, nil, nil
}

func (m *mockNASClient) CifsServiceModifyCollection(params *nas.CifsServiceModifyCollectionParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsServiceModifyCollectionOK, *nas.CifsServiceModifyCollectionAccepted, error) {
	return nil, nil, nil
}

func (m *mockNASClient) CifsSessionCollectionGet(params *nas.CifsSessionCollectionGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsSessionCollectionGetOK, error) {
	return nil, nil
}

func (m *mockNASClient) CifsShareACLDelete(params *nas.CifsShareACLDeleteParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsShareACLDeleteOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.CifsShareACLDeleteOK{}, nil
}

func (m *mockNASClient) CifsShareCollectionGet(params *nas.CifsShareCollectionGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsShareCollectionGetOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*nas.CifsShareCollectionGetOK); ok {
		return resp, nil
	}
	return &nas.CifsShareCollectionGetOK{}, nil
}

func (m *mockNASClient) CifsShareCreate(params *nas.CifsShareCreateParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsShareCreateCreated, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.CifsShareCreateCreated{}, nil
}

func (m *mockNASClient) CifsShareDelete(params *nas.CifsShareDeleteParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsShareDeleteOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.CifsShareDeleteOK{}, nil
}

func (m *mockNASClient) CifsShareGet(params *nas.CifsShareGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsShareGetOK, error) {
	return nil, nil
}

func (m *mockNASClient) CifsShareModify(params *nas.CifsShareModifyParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.CifsShareModifyOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.CifsShareModifyOK{}, nil
}

func (m *mockNASClient) ClientLockDeleteCollection(params *nas.ClientLockDeleteCollectionParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.ClientLockDeleteCollectionOK, error) {
	return nil, nil
}

func (m *mockNASClient) ExportPolicyGet(params *nas.ExportPolicyGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.ExportPolicyGetOK, error) {
	return nil, nil
}

func (m *mockNASClient) KerberosInterfaceCollectionGet(params *nas.KerberosInterfaceCollectionGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.KerberosInterfaceCollectionGetOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*nas.KerberosInterfaceCollectionGetOK); ok {
		return resp, nil
	}
	return &nas.KerberosInterfaceCollectionGetOK{}, nil
}

func (m *mockNASClient) KerberosInterfaceModify(params *nas.KerberosInterfaceModifyParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.KerberosInterfaceModifyOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.KerberosInterfaceModifyOK{}, nil
}

func (m *mockNASClient) KerberosRealmCreate(params *nas.KerberosRealmCreateParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.KerberosRealmCreateCreated, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.KerberosRealmCreateCreated{}, nil
}

func (m *mockNASClient) KerberosRealmDelete(params *nas.KerberosRealmDeleteParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.KerberosRealmDeleteOK, error) {
	return nil, nil
}

func (m *mockNASClient) KerberosRealmGet(params *nas.KerberosRealmGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.KerberosRealmGetOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*nas.KerberosRealmGetOK); ok {
		return resp, nil
	}
	return &nas.KerberosRealmGetOK{}, nil
}

func (m *mockNASClient) KerberosRealmModify(params *nas.KerberosRealmModifyParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.KerberosRealmModifyOK, error) {
	return nil, nil
}

func (m *mockNASClient) LocalCifsGroupCollectionGet(params *nas.LocalCifsGroupCollectionGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.LocalCifsGroupCollectionGetOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Support multiple responses for pagination tests
	if len(m.responses) > 0 {
		if m.callCount < len(m.responses) {
			resp := m.responses[m.callCount]
			m.callCount++
			if r, ok := resp.(*nas.LocalCifsGroupCollectionGetOK); ok {
				return r, nil
			}
		}
	}
	if resp, ok := m.response.(*nas.LocalCifsGroupCollectionGetOK); ok {
		return resp, nil
	}
	return &nas.LocalCifsGroupCollectionGetOK{}, nil
}

func (m *mockNASClient) LocalCifsGroupMembersBulkDelete(params *nas.LocalCifsGroupMembersBulkDeleteParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.LocalCifsGroupMembersBulkDeleteOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.LocalCifsGroupMembersBulkDeleteOK{}, nil
}

func (m *mockNASClient) LocalCifsGroupMembersCollectionGet(params *nas.LocalCifsGroupMembersCollectionGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.LocalCifsGroupMembersCollectionGetOK, error) {
	return nil, nil
}

func (m *mockNASClient) LocalCifsGroupMembersCreate(params *nas.LocalCifsGroupMembersCreateParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.LocalCifsGroupMembersCreateCreated, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.LocalCifsGroupMembersCreateCreated{}, nil
}

func (m *mockNASClient) LocalCifsGroupMembersDelete(params *nas.LocalCifsGroupMembersDeleteParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.LocalCifsGroupMembersDeleteOK, error) {
	return nil, nil
}

func (m *mockNASClient) NfsClientsGet(params *nas.NfsClientsGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.NfsClientsGetOK, error) {
	return nil, nil
}

func (m *mockNASClient) UserGroupPrivilegesCollectionGet(params *nas.UserGroupPrivilegesCollectionGetParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.UserGroupPrivilegesCollectionGetOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*nas.UserGroupPrivilegesCollectionGetOK); ok {
		return resp, nil
	}
	return &nas.UserGroupPrivilegesCollectionGetOK{}, nil
}

func (m *mockNASClient) UserGroupPrivilegesCreate(params *nas.UserGroupPrivilegesCreateParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.UserGroupPrivilegesCreateCreated, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.UserGroupPrivilegesCreateCreated{}, nil
}

func (m *mockNASClient) UserGroupPrivilegesModify(params *nas.UserGroupPrivilegesModifyParams, authInfo runtime.ClientAuthInfoWriter, opts ...nas.ClientOption) (*nas.UserGroupPrivilegesModifyOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &nas.UserGroupPrivilegesModifyOK{}, nil
}

func (m *mockNASClient) SetTransport(transport runtime.ClientTransport) {
	// No-op for testing
}

func TestExportPolicyCreate(t *testing.T) {
	policyName := "test-policy"

	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("something went wrong")}
		client := &nasClient{api: api}
		name, err := client.ExportPolicyCreate(&ExportPolicyCreateParams{})
		assert.EqualError(tt, err, api.err.Error())
		assert.Empty(tt, name)
	})

	t.Run("WhenSuccessful_ThenReturnPolicyName", func(tt *testing.T) {
		resp := &nas.ExportPolicyCreateCreated{
			Payload: &models.ExportPolicyResponse{
				ExportPolicyResponseInlineRecords: []*models.ExportPolicy{
					{Name: &policyName},
				},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		name, err := client.ExportPolicyCreate(&ExportPolicyCreateParams{})
		assert.NoError(tt, err)
		assert.Equal(tt, policyName, name)
	})
}

func TestExportPolicyGet(t *testing.T) {
	policyName := "test-policy"

	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &ExportPolicyGetParams{Name: &policyName}
		policy, err := client.ExportPolicyGet(params)
		assert.EqualError(tt, err, "api error")
		assert.Nil(tt, policy)
	})

	t.Run("WhenNoRecords_ThenReturnNotFound", func(tt *testing.T) {
		resp := &nas.ExportPolicyCollectionGetOK{
			Payload: &models.ExportPolicyResponse{
				ExportPolicyResponseInlineRecords: []*models.ExportPolicy{},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		params := &ExportPolicyGetParams{Name: &policyName}
		policy, err := client.ExportPolicyGet(params)
		assert.Error(tt, err)
		assert.Nil(tt, policy)
	})

	t.Run("WhenMultipleRecords_ThenReturnError", func(tt *testing.T) {
		resp := &nas.ExportPolicyCollectionGetOK{
			Payload: &models.ExportPolicyResponse{
				ExportPolicyResponseInlineRecords: []*models.ExportPolicy{{}, {}},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		params := &ExportPolicyGetParams{Name: &policyName}
		policy, err := client.ExportPolicyGet(params)
		assert.Error(tt, err)
		assert.Nil(tt, policy)
	})

	t.Run("WhenSingleRecord_ThenReturnPolicy", func(tt *testing.T) {
		resp := &nas.ExportPolicyCollectionGetOK{
			Payload: &models.ExportPolicyResponse{
				ExportPolicyResponseInlineRecords: []*models.ExportPolicy{{Name: &policyName}},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		params := &ExportPolicyGetParams{Name: &policyName}
		policy, err := client.ExportPolicyGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, policy)
		assert.Equal(tt, policyName, *policy.Name)
	})
}

func TestExportPolicyModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		err := client.ExportPolicyModify(&ExportPolicyModifyParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.ExportPolicyModify(&ExportPolicyModifyParams{})
		assert.NoError(tt, err)
	})
}

func TestExportPolicyDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		err := client.ExportPolicyDelete(&ExportPolicyDeleteParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.ExportPolicyDelete(&ExportPolicyDeleteParams{})
		assert.NoError(tt, err)
	})
}

func TestNfsServiceGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		nfs, err := client.NfsServiceGet(&NfsServiceGetParams{})
		assert.EqualError(tt, err, "api error")
		assert.Nil(tt, nfs)
	})

	t.Run("WhenSuccessful_ThenReturnNfsService", func(tt *testing.T) {
		val := true
		resp := &nas.NfsGetOK{Payload: &models.NfsService{Enabled: &val}}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		nfs, err := client.NfsServiceGet(&NfsServiceGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, nfs)
		assert.Equal(tt, &val, nfs.Enabled)
	})
}

func TestNfsServiceCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		err := client.NfsServiceCreate(&NfsServiceCreateParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.NfsServiceCreate(&NfsServiceCreateParams{})
		assert.NoError(tt, err)
	})
}

func TestNfsModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		err := client.NfsParamsModify(context.Background(), &NfsModifyParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.NfsParamsModify(context.Background(), &NfsModifyParams{})
		assert.NoError(tt, err)
	})

	t.Run("WhenAllParamsSet_ThenSetAllFields", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		v4IDDomain := "test-domain"
		showmountEnabled := true
		rquotaEnabled := true
		allowLocalNFSUsersWithLdap := true
		extendedGroupsLimit := int64(100)
		enabled := true
		v3Enabled := true
		v40Enabled := true
		v41Enabled := true
		vstorageEnabled := true
		fileSessionIoGroupingCount := int64(10)
		authSysExtendedGroupsEnabled := true

		params := &NfsModifyParams{
			SvmUUID:                      "test-uuid",
			V4IDDomain:                   &v4IDDomain,
			ShowmountEnabled:             &showmountEnabled,
			RquotaEnabled:                &rquotaEnabled,
			AllowLocalNFSUsersWithLdap:   &allowLocalNFSUsersWithLdap,
			ExtendedGroupsLimit:          &extendedGroupsLimit,
			Enabled:                      &enabled,
			V3Enabled:                    &v3Enabled,
			V40Enabled:                   &v40Enabled,
			V41Enabled:                   &v41Enabled,
			VstorageEnabled:              &vstorageEnabled,
			FileSessionIoGroupingCount:   &fileSessionIoGroupingCount,
			AuthSysExtendedGroupsEnabled: &authSysExtendedGroupsEnabled,
		}
		err := client.NfsModify(params)
		assert.NoError(tt, err)
	})

	t.Run("WhenParamsNil_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.NfsModify(nil)
		assert.NoError(tt, err)
	})
}

func TestNfsParamsModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		err := client.NfsParamsModify(nil, &NfsModifyParams{
			SvmUUID:       "test-svm-uuid",
			RquotaEnabled: nillable.ToPointer(true),
		})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.NfsParamsModify(nil, &NfsModifyParams{
			SvmUUID:       "test-svm-uuid",
			RquotaEnabled: nillable.ToPointer(true),
		})
		assert.NoError(tt, err)
	})

	t.Run("WhenRquotaDisabled_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.NfsParamsModify(nil, &NfsModifyParams{
			SvmUUID:       "test-svm-uuid",
			RquotaEnabled: nillable.ToPointer(false),
		})
		assert.NoError(tt, err)
	})
}

func TestNfsServiceModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		err := client.NfsServiceModify(&NfsServiceModifyParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.NfsServiceModify(&NfsServiceModifyParams{})
		assert.NoError(tt, err)
	})
}

func TestCifsServiceGet(t *testing.T) {
	cifsName := "cifs-service"
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		cifs, err := client.CifsServiceGet(&CifsServiceGetParams{})
		assert.EqualError(tt, err, "api error")
		assert.Nil(tt, cifs)
	})

	t.Run("WhenNoRecords_ThenReturnNotFound", func(tt *testing.T) {
		resp := &nas.CifsServiceCollectionGetOK{
			Payload: &models.CifsServiceResponse{
				CifsServiceResponseInlineRecords: []*models.CifsService{},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		cifs, err := client.CifsServiceGet(&CifsServiceGetParams{})
		assert.Error(tt, err)
		assert.Nil(tt, cifs)
	})

	t.Run("WhenMultipleRecords_ThenReturnError", func(tt *testing.T) {
		resp := &nas.CifsServiceCollectionGetOK{
			Payload: &models.CifsServiceResponse{
				CifsServiceResponseInlineRecords: []*models.CifsService{{}, {}},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		cifs, err := client.CifsServiceGet(&CifsServiceGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, cifs)
	})

	t.Run("WhenSingleRecord_ThenReturnCifsService", func(tt *testing.T) {
		resp := &nas.CifsServiceCollectionGetOK{
			Payload: &models.CifsServiceResponse{
				CifsServiceResponseInlineRecords: []*models.CifsService{{Name: &cifsName}},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		cifs, err := client.CifsServiceGet(&CifsServiceGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, cifs)
		assert.Equal(tt, cifsName, *cifs.Name)
	})
}

func TestCifsServiceCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		_, _, err := client.CifsServiceCreate(&CifsServiceCreateParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		_, _, err := client.CifsServiceCreate(&CifsServiceCreateParams{})
		assert.NoError(tt, err)
	})
}

func TestCifsServiceModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		err := client.CifsServiceModify(&CifsServiceModifyParams{
			SvmUUID: nillable.GetStringPtr("test-uuid"),
		})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.CifsServiceModify(&CifsServiceModifyParams{
			SvmUUID: nillable.GetStringPtr("test-uuid"),
		})
		assert.NoError(tt, err)
	})
}

func TestCifsServiceList(t *testing.T) {
	cifsName1 := "cifs-service-1"
	cifsName2 := "cifs-service-2"

	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		cifsList, err := client.CifsServiceList(&CifsServiceGetParams{})
		assert.EqualError(tt, err, "api error")
		assert.Nil(tt, cifsList)
	})

	t.Run("WhenNoRecords_ThenReturnEmptyList", func(tt *testing.T) {
		var zeroRecords int64 = 0
		resp := &nas.CifsServiceCollectionGetOK{
			Payload: &models.CifsServiceResponse{
				NumRecords:                       &zeroRecords,
				CifsServiceResponseInlineRecords: []*models.CifsService{},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		cifsList, err := client.CifsServiceList(&CifsServiceGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, cifsList)
		assert.Equal(tt, 0, len(cifsList))
	})

	t.Run("WhenMultipleRecords_ThenReturnList", func(tt *testing.T) {
		var numRecordsInt64 int64 = 2
		resp := &nas.CifsServiceCollectionGetOK{
			Payload: &models.CifsServiceResponse{
				NumRecords: &numRecordsInt64,
				CifsServiceResponseInlineRecords: []*models.CifsService{
					{Name: &cifsName1},
					{Name: &cifsName2},
				},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		cifsList, err := client.CifsServiceList(&CifsServiceGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, cifsList)
		assert.Equal(tt, 2, len(cifsList))
		assert.Equal(tt, cifsName1, *cifsList[0].Name)
		assert.Equal(tt, cifsName2, *cifsList[1].Name)
	})
}

func TestCifsServiceCreate_WithJobAccepted(t *testing.T) {
	jobUUID := "test-job-uuid"

	t.Run("WhenJobAccepted_ThenReturnJobAccepted", func(tt *testing.T) {
		jobUUIDFormatted := strfmt.UUID(jobUUID)
		resp := &nas.CifsServiceCreateAccepted{
			Payload: &models.CifsServiceJobLinkResponse{
				Job: &models.JobLink{
					UUID: &jobUUIDFormatted,
				},
			},
		}
		api := &mockNASClient{response: resp, err: nil}
		client := &nasClient{api: api}
		done, jobAccepted, err := client.CifsServiceCreate(&CifsServiceCreateParams{})
		assert.NoError(tt, err)
		assert.False(tt, done)
		assert.NotNil(tt, jobAccepted)
		assert.Equal(tt, jobUUID, jobAccepted.JobUUID)
	})
}

func TestCifsDomainModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		err := client.CifsDomainModify(&CifsDomainModifyParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.CifsDomainModify(&CifsDomainModifyParams{})
		assert.NoError(tt, err)
	})
}

func TestCifsShareACLDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		err := client.CifsShareACLDelete(&CifsShareACLDeleteParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.CifsShareACLDelete(&CifsShareACLDeleteParams{})
		assert.NoError(tt, err)
	})
}

func TestCifsServiceAddMembers(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsServiceModifyGroupMembersParams{
			Sid:     "test-sid",
			Members: []string{"user1", "user2"},
			SvmUUID: "test-uuid",
		}
		err := client.CifsServiceAddMembers(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		params := &CifsServiceModifyGroupMembersParams{
			Sid:     "test-sid",
			Members: []string{"user1"},
			SvmUUID: "test-uuid",
		}
		err := client.CifsServiceAddMembers(params)
		assert.NoError(tt, err)
	})
}

func TestCifsServiceDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsServiceDeleteParams{SvmUUID: "test-uuid"}
		err := client.CifsServiceDelete(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		params := &CifsServiceDeleteParams{SvmUUID: "test-uuid"}
		err := client.CifsServiceDelete(params)
		assert.NoError(tt, err)
	})
}

func TestCifsServiceAddSecurityPrivilege(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsServiceModifySecurityPrivilegeParams{
			Member:  "test-user",
			SvmUUID: "test-uuid",
		}
		err := client.CifsServiceAddSecurityPrivilege(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		params := &CifsServiceModifySecurityPrivilegeParams{
			Member:  "test-user",
			SvmUUID: "test-uuid",
		}
		err := client.CifsServiceAddSecurityPrivilege(params)
		assert.NoError(tt, err)
	})
}

func TestCifsShareCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsShareCreateParams{
			Name:    "test-share",
			Path:    "/vol/test",
			SvmName: stringPtr("test-svm"),
		}
		err := client.CifsShareCreate(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		params := &CifsShareCreateParams{
			Name:    "test-share",
			Path:    "/vol/test",
			SvmName: stringPtr("test-svm"),
		}
		err := client.CifsShareCreate(params)
		assert.NoError(tt, err)
	})
}

// Helper function to create a string pointer
func stringPtr(s string) *string {
	return &s
}

func TestCifsShareModify_Success(t *testing.T) {
	t.Run("Successfully modifies CIFS share", func(tt *testing.T) {
		mockAPI := &mockNASClient{}
		client := &nasClient{api: mockAPI}

		params := &CifsShareModifyParams{
			SvmUUID:         "test-svm-uuid",
			ShareName:       "test-share",
			ShareProperties: []string{"browsable", "encrypt_data"},
		}

		err := client.CifsShareModify(params)
		assert.NoError(tt, err)
	})

	t.Run("Returns error when API call fails", func(tt *testing.T) {
		mockAPI := &mockNASClient{
			err: errors.New("API error"),
		}
		client := &nasClient{api: mockAPI}

		params := &CifsShareModifyParams{
			SvmUUID:         "test-svm-uuid",
			ShareName:       "test-share",
			ShareProperties: []string{"browsable"},
		}

		err := client.CifsShareModify(params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "API error")
	})
}

func TestCifsShareDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsShareDeleteParams{
			ShareName: "test-share",
			SvmUUID:   "test-svm-uuid",
		}
		err := client.CifsShareDelete(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		params := &CifsShareDeleteParams{
			ShareName: "test-share",
			SvmUUID:   "test-svm-uuid",
		}
		err := client.CifsShareDelete(params)
		assert.NoError(tt, err)
	})
}

func TestCifsShareCollectionGet_Success(t *testing.T) {
	t.Run("Successfully retrieves CIFS share with all properties", func(tt *testing.T) {
		browsable := true
		continuouslyAvailable := true
		changeNotify := true
		oplocks := true
		encryption := true
		showPreviousVersions := true
		showSnapshot := true
		accessBasedEnumeration := true

		mockAPI := &mockNASClient{
			response: &nas.CifsShareCollectionGetOK{
				Payload: &models.CifsShareResponse{
					CifsShareResponseInlineRecords: []*models.CifsShare{
						{
							Browsable:              &browsable,
							ContinuouslyAvailable:  &continuouslyAvailable,
							ChangeNotify:           &changeNotify,
							Oplocks:                &oplocks,
							Encryption:             &encryption,
							ShowPreviousVersions:   &showPreviousVersions,
							ShowSnapshot:           &showSnapshot,
							AccessBasedEnumeration: &accessBasedEnumeration,
						},
					},
				},
			},
		}
		client := &nasClient{api: mockAPI}

		params := &CifsShareCollectionGetParams{
			SvmUUID:   "test-svm-uuid",
			ShareName: "test-share",
			Fields:    []string{"continuously_available"},
		}

		result, err := client.CifsShareCollectionGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Contains(tt, result.ShareProperties, "browsable")
		assert.Contains(tt, result.ShareProperties, "continuously_available")
		assert.Contains(tt, result.ShareProperties, "changenotify")
		assert.Contains(tt, result.ShareProperties, "oplocks")
		assert.Contains(tt, result.ShareProperties, "encrypt_data")
		assert.Contains(tt, result.ShareProperties, "show_previous_versions")
		assert.Contains(tt, result.ShareProperties, "showsnapshot")
		assert.Contains(tt, result.ShareProperties, "access_based_enumeration")
	})

	t.Run("Successfully retrieves CIFS share with partial properties", func(tt *testing.T) {
		browsable := true
		encryption := false
		continuouslyAvailable := false

		mockAPI := &mockNASClient{
			response: &nas.CifsShareCollectionGetOK{
				Payload: &models.CifsShareResponse{
					CifsShareResponseInlineRecords: []*models.CifsShare{
						{
							Browsable:             &browsable,
							Encryption:            &encryption,
							ContinuouslyAvailable: &continuouslyAvailable,
						},
					},
				},
			},
		}
		client := &nasClient{api: mockAPI}

		params := &CifsShareCollectionGetParams{
			SvmUUID:   "test-svm-uuid",
			ShareName: "test-share",
			Fields:    []string{"browsable"},
		}

		result, err := client.CifsShareCollectionGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Contains(tt, result.ShareProperties, "browsable")
		assert.NotContains(tt, result.ShareProperties, "encrypt_data")
		assert.NotContains(tt, result.ShareProperties, "continuously_available")
	})

	t.Run("Returns not found error when no records returned", func(tt *testing.T) {
		mockAPI := &mockNASClient{
			response: &nas.CifsShareCollectionGetOK{
				Payload: &models.CifsShareResponse{
					CifsShareResponseInlineRecords: []*models.CifsShare{},
				},
			},
		}
		client := &nasClient{api: mockAPI}

		params := &CifsShareCollectionGetParams{
			SvmUUID:   "test-svm-uuid",
			ShareName: "non-existent-share",
			Fields:    []string{"browsable"},
		}

		result, err := client.CifsShareCollectionGet(params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "not found")
	})

	t.Run("Returns error when API call fails", func(tt *testing.T) {
		mockAPI := &mockNASClient{
			err: errors.New("API error"),
		}
		client := &nasClient{api: mockAPI}

		params := &CifsShareCollectionGetParams{
			SvmUUID:   "test-svm-uuid",
			ShareName: "test-share",
			Fields:    []string{"browsable"},
		}

		result, err := client.CifsShareCollectionGet(params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "API error")
	})
}

func Test_convertCifsShareFromREST(t *testing.T) {
	t.Run("Converts all properties correctly", func(tt *testing.T) {
		browsable := true
		continuouslyAvailable := true
		changeNotify := true
		oplocks := true
		encryption := true
		showPreviousVersions := true
		showSnapshot := true
		accessBasedEnumeration := true

		share := &models.CifsShare{
			Browsable:              &browsable,
			ContinuouslyAvailable:  &continuouslyAvailable,
			ChangeNotify:           &changeNotify,
			Oplocks:                &oplocks,
			Encryption:             &encryption,
			ShowPreviousVersions:   &showPreviousVersions,
			ShowSnapshot:           &showSnapshot,
			AccessBasedEnumeration: &accessBasedEnumeration,
		}

		result := _convertCifsShareFromREST(share)
		assert.NotNil(tt, result)
		assert.Len(tt, result.ShareProperties, 8)
		assert.Contains(tt, result.ShareProperties, "browsable")
		assert.Contains(tt, result.ShareProperties, "continuously_available")
		assert.Contains(tt, result.ShareProperties, "changenotify")
		assert.Contains(tt, result.ShareProperties, "oplocks")
		assert.Contains(tt, result.ShareProperties, "encrypt_data")
		assert.Contains(tt, result.ShareProperties, "show_previous_versions")
		assert.Contains(tt, result.ShareProperties, "showsnapshot")
		assert.Contains(tt, result.ShareProperties, "access_based_enumeration")
	})

	t.Run("Only includes true properties", func(tt *testing.T) {
		browsable := true
		encryption := false
		continuouslyAvailable := false

		share := &models.CifsShare{
			Browsable:             &browsable,
			Encryption:            &encryption,
			ContinuouslyAvailable: &continuouslyAvailable,
		}

		result := _convertCifsShareFromREST(share)
		assert.NotNil(tt, result)
		assert.Len(tt, result.ShareProperties, 1)
		assert.Contains(tt, result.ShareProperties, "browsable")
		assert.NotContains(tt, result.ShareProperties, "encrypt_data")
		assert.NotContains(tt, result.ShareProperties, "continuously_available")
	})

	t.Run("Returns empty array when all properties are false or nil", func(tt *testing.T) {
		browsable := false
		encryption := false

		share := &models.CifsShare{
			Browsable:  &browsable,
			Encryption: &encryption,
		}

		result := _convertCifsShareFromREST(share)
		assert.NotNil(tt, result)
		assert.Empty(tt, result.ShareProperties)
	})
}

// mockPrivClient implements the priv.ClientService interface for testing
type mockPrivClient struct {
	err      error
	response interface{}
}

func (m *mockPrivClient) GetGroupIDsList(params *priv.GetGroupIDsListParams, opts ...priv.ClientOption) (*priv.GetGroupIDsListOK, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) AntiRansomwareSuspectDelete(params *priv.AntiRansomwareSuspectDeleteParams, opts ...priv.ClientOption) (*priv.AntiRansomwareSuspectDeleteOK, *priv.AntiRansomwareSuspectDeleteAccepted, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) AzureKeyVaultCreate(params *priv.AzureKeyVaultCreateParams, opts ...priv.ClientOption) (*priv.AzureKeyVaultCreateCreated, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) AzureKeyVaultDelete(params *priv.AzureKeyVaultDeleteParams, opts ...priv.ClientOption) (*priv.AzureKeyVaultDeleteOK, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) AzureKeyVaultGet(params *priv.AzureKeyVaultGetParams, opts ...priv.ClientOption) (*priv.AzureKeyVaultGetOK, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) AzureKeyVaultModify(params *priv.AzureKeyVaultModifyParams, opts ...priv.ClientOption) (*priv.AzureKeyVaultModifyOK, *priv.AzureKeyVaultModifyAccepted, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) AzureUpdateConfig(params *priv.AzureUpdateConfigParams, opts ...priv.ClientOption) (*priv.AzureUpdateConfigOK, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) CifsCheck(params *priv.CifsCheckParams, opts ...priv.ClientOption) (*priv.CifsCheckOK, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) CliExecute(params *priv.CliExecuteParams, opts ...priv.ClientOption) (*priv.CliExecuteOK, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) ClusterPeerCreate(params *priv.ClusterPeerCreateParams, opts ...priv.ClientOption) (*priv.ClusterPeerCreateCreated, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) GcpKmsGet(params *priv.GcpKmsGetParams, opts ...priv.ClientOption) (*priv.GcpKmsGetOK, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) NetworkPing(params *priv.NetworkPingParams, opts ...priv.ClientOption) (*priv.NetworkPingOK, *priv.NetworkPingAccepted, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) SecurityLoginPasswordModify(params *priv.SecurityLoginPasswordModifyParams, opts ...priv.ClientOption) (*priv.SecurityLoginPasswordModifyOK, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) SecurityLoginExpirePassword(params *priv.SecurityLoginExpirePasswordParams, opts ...priv.ClientOption) (*priv.SecurityLoginExpirePasswordOK, error) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) SetTransport(transport runtime.ClientTransport) {
	// TODO implement me
	panic("implement me")
}

func (m *mockPrivClient) SrvLookup(params *priv.SrvLookupParams, opts ...priv.ClientOption) (*priv.SrvLookupOK, error) {
	if m.err != nil {
		return nil, m.err
	}
	if resp, ok := m.response.(*priv.SrvLookupOK); ok {
		return resp, nil
	}
	return &priv.SrvLookupOK{}, nil
}

func TestDomainControllersSrvLookupGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		mockClient := &mockPrivClient{err: errors.New("api error")}
		var apiPriv priv.ClientService = mockClient
		client := &nasClient{apiPriv: &apiPriv}
		params := &SrvLookupParams{
			LookupString: "test-lookup",
			SVMName:      "test-svm",
		}
		result, err := client.DomainControllersSrvLookupGet(params)
		assert.EqualError(tt, err, "api error")
		assert.Nil(tt, result)
	})

	t.Run("WhenValidOutput_ThenReturnIPList", func(tt *testing.T) {
		cliOutput := "Got 2 Ip Addresses\n10.193.224.112\n10.193.215.176\n"
		resp := &priv.SrvLookupOK{
			Payload: &privmodels.SrvLookupResponse{
				CliOutput: cliOutput,
			},
		}
		mockClient := &mockPrivClient{response: resp}
		var apiPriv priv.ClientService = mockClient
		client := &nasClient{apiPriv: &apiPriv}
		params := &SrvLookupParams{
			LookupString: "test-lookup",
			SVMName:      "test-svm",
		}
		result, err := client.DomainControllersSrvLookupGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 2, len(result))
		assert.Equal(tt, "10.193.224.112", result[0])
		assert.Equal(tt, "10.193.215.176", result[1])
	})
}

func TestCifsDomainPreferredDCDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsDomainPreferredDCDeleteParams{
			SvmUUID:  "test-uuid",
			ServerIP: stringPtr("10.0.0.1"),
			Fqdn:     stringPtr("test.domain.com"),
		}
		err := client.CifsDomainPreferredDCDelete(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		params := &CifsDomainPreferredDCDeleteParams{
			SvmUUID:  "test-uuid",
			ServerIP: stringPtr("10.0.0.1"),
			Fqdn:     stringPtr("test.domain.com"),
		}
		err := client.CifsDomainPreferredDCDelete(params)
		assert.NoError(tt, err)
	})

	t.Run("WhenParamsNil_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.CifsDomainPreferredDCDelete(nil)
		assert.NoError(tt, err)
	})
}

func TestCifsDomainPreferredDCCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		fqdn := "test.domain.com"
		serverIP := "10.0.0.1"
		params := &CifsDomainPreferredDCCreateParams{
			SvmUUID: "test-uuid",
			CifsDomainPreferredDC: &CifsDomainPreferredDC{
				Fqdn:     &fqdn,
				ServerIP: &serverIP,
			},
		}
		err := client.CifsDomainPreferredDCCreate(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		fqdn := "test.domain.com"
		serverIP := "10.0.0.1"
		params := &CifsDomainPreferredDCCreateParams{
			SvmUUID: "test-uuid",
			CifsDomainPreferredDC: &CifsDomainPreferredDC{
				Fqdn:     &fqdn,
				ServerIP: &serverIP,
			},
		}
		err := client.CifsDomainPreferredDCCreate(params)
		assert.NoError(tt, err)
	})

	t.Run("WhenParamsNil_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		err := client.CifsDomainPreferredDCCreate(nil)
		assert.NoError(tt, err)
	})
}

func TestCifsServiceCollectionGetGroups(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsServiceCollectionGetGroupsParams{
			SvmUUID: "test-uuid",
		}
		callback := func(groups []*CifsGroup) error {
			return nil
		}
		err := client.CifsServiceCollectionGetGroups(params, callback)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenProcessGroups", func(tt *testing.T) {
		groupName1 := "group1"
		groupSid1 := "S-1-1-1"
		memberName1 := "domain\\user1"
		groupName2 := "group2"
		groupSid2 := "S-1-1-2"
		memberName2 := "domain\\user2"

		resp := &nas.LocalCifsGroupCollectionGetOK{
			Payload: &models.LocalCifsGroupResponse{
				LocalCifsGroupResponseInlineRecords: []*models.LocalCifsGroup{
					{
						Name: &groupName1,
						Sid:  &groupSid1,
						LocalCifsGroupInlineMembers: []*models.LocalCifsGroupInlineMembersInlineArrayItem{
							{Name: &memberName1},
						},
					},
					{
						Name: &groupName2,
						Sid:  &groupSid2,
						LocalCifsGroupInlineMembers: []*models.LocalCifsGroupInlineMembersInlineArrayItem{
							{Name: &memberName2},
						},
					},
				},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		params := &CifsServiceCollectionGetGroupsParams{
			SvmUUID: "test-uuid",
		}
		var receivedGroups []*CifsGroup
		callback := func(groups []*CifsGroup) error {
			receivedGroups = append(receivedGroups, groups...)
			return nil
		}
		err := client.CifsServiceCollectionGetGroups(params, callback)
		assert.NoError(tt, err)
		assert.Equal(tt, 2, len(receivedGroups))
		assert.Equal(tt, groupName1, receivedGroups[0].Name)
		assert.Equal(tt, groupSid1, receivedGroups[0].Sid)
		assert.Equal(tt, 1, len(receivedGroups[0].Members))
		assert.Equal(tt, "user1", receivedGroups[0].Members[0])
	})

	t.Run("WhenPagination_ThenHandleNextLink", func(tt *testing.T) {
		groupName := "group1"
		groupSid := "S-1-1-1"
		memberName := "domain\\user1"
		nextHref := "http://next-page"

		// First response with next link
		firstResp := &nas.LocalCifsGroupCollectionGetOK{
			Payload: &models.LocalCifsGroupResponse{
				LocalCifsGroupResponseInlineRecords: []*models.LocalCifsGroup{
					{
						Name: &groupName,
						Sid:  &groupSid,
						LocalCifsGroupInlineMembers: []*models.LocalCifsGroupInlineMembersInlineArrayItem{
							{Name: &memberName},
						},
					},
				},
				Links: &models.LocalCifsGroupResponseInlineLinks{
					Next: &models.Href{Href: &nextHref},
				},
			},
		}
		// Second response without next link (end of pagination)
		secondResp := &nas.LocalCifsGroupCollectionGetOK{
			Payload: &models.LocalCifsGroupResponse{
				LocalCifsGroupResponseInlineRecords: []*models.LocalCifsGroup{},
			},
		}

		// Use responses array for pagination
		api := &mockNASClient{
			responses: []interface{}{firstResp, secondResp},
		}

		client := &nasClient{api: api}
		params := &CifsServiceCollectionGetGroupsParams{
			SvmUUID: "test-uuid",
		}
		receivedGroups := 0
		callback := func(groups []*CifsGroup) error {
			receivedGroups += len(groups)
			return nil
		}
		err := client.CifsServiceCollectionGetGroups(params, callback)
		assert.NoError(tt, err)
		assert.Equal(tt, 2, api.callCount, "Should make exactly 2 API calls for pagination")
		assert.Equal(tt, 1, receivedGroups, "Should receive 1 group from pagination")
	})
}

func TestCifsServiceRemoveMembers(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsServiceModifyGroupMembersParams{
			Sid:     "test-sid",
			Members: []string{"user1", "user2"},
			SvmUUID: "test-uuid",
		}
		err := client.CifsServiceRemoveMembers(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		params := &CifsServiceModifyGroupMembersParams{
			Sid:     "test-sid",
			Members: []string{"user1"},
			SvmUUID: "test-uuid",
		}
		err := client.CifsServiceRemoveMembers(params)
		assert.NoError(tt, err)
	})
}

func TestCifsServiceCollectionGetPrivilegedMembers(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsServiceCollectionGetPrivilegedMembersParams{
			SvmUUID: "test-uuid",
		}
		callback := func(members []string) error {
			return nil
		}
		err := client.CifsServiceCollectionGetPrivilegedMembers(params, callback)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenProcessMembers", func(tt *testing.T) {
		memberName1 := "domain\\user1"
		memberName2 := "domain\\user2"
		resp := &nas.UserGroupPrivilegesCollectionGetOK{
			Payload: &models.UserGroupPrivilegesResponse{
				UserGroupPrivilegesResponseInlineRecords: []*models.UserGroupPrivileges{
					{Name: &memberName1},
					{Name: &memberName2},
				},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		params := &CifsServiceCollectionGetPrivilegedMembersParams{
			SvmUUID: "test-uuid",
		}
		var receivedMembers []string
		callback := func(members []string) error {
			receivedMembers = append(receivedMembers, members...)
			return nil
		}
		err := client.CifsServiceCollectionGetPrivilegedMembers(params, callback)
		assert.NoError(tt, err)
		assert.Equal(tt, 2, len(receivedMembers))
		assert.Equal(tt, "user1", receivedMembers[0])
		assert.Equal(tt, "user2", receivedMembers[1])
	})
}

func TestCifsServiceRemoveSecurityPrivilege(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsServiceModifySecurityPrivilegeParams{
			Member:  "test-user",
			SvmUUID: "test-uuid",
		}
		err := client.CifsServiceRemoveSecurityPrivilege(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		params := &CifsServiceModifySecurityPrivilegeParams{
			Member:  "test-user",
			SvmUUID: "test-uuid",
		}
		err := client.CifsServiceRemoveSecurityPrivilege(params)
		assert.NoError(tt, err)
	})
}

func TestCifsDomainGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &CifsDomainGetParams{
			SvmUUID: "test-uuid",
		}
		result, err := client.CifsDomainGet(params)
		assert.EqualError(tt, err, "api error")
		assert.Nil(tt, result)
	})

	t.Run("WhenSuccessful_ThenReturnCifsDomain", func(tt *testing.T) {
		discoveryMode := "site"
		resp := &nas.CifsDomainGetOK{
			Payload: &models.CifsDomain{
				ServerDiscoveryMode: &discoveryMode,
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		params := &CifsDomainGetParams{
			SvmUUID: "test-uuid",
		}
		result, err := client.CifsDomainGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, discoveryMode, *result.ServerDiscoveryMode)
	})

	t.Run("WhenSuccessfulWithFields_ThenReturnCifsDomain", func(tt *testing.T) {
		discoveryMode := "all"
		resetDiscoveredServers := true
		rediscoverTrusts := true
		resp := &nas.CifsDomainGetOK{
			Payload: &models.CifsDomain{
				ServerDiscoveryMode: &discoveryMode,
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		params := &CifsDomainGetParams{
			BaseParams: BaseParams{
				Fields: []string{"server_discovery_mode"},
			},
			SvmUUID:                "test-uuid",
			ResetDiscoveredServers: &resetDiscoveredServers,
			RediscoverTrusts:       &rediscoverTrusts,
		}
		result, err := client.CifsDomainGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, discoveryMode, *result.ServerDiscoveryMode)
	})
}

func TestKerberosRealmGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		params := &KerberosRealmGetParams{
			SvmUUID: "test-uuid",
			Realm:   "EXAMPLE.COM",
		}
		result, err := client.KerberosRealmGet(params)
		assert.Nil(tt, result)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenPayloadIsNil_ThenReturnEmptySlice", func(tt *testing.T) {
		api := &mockNASClient{response: &nas.KerberosRealmGetOK{Payload: nil}}
		client := &nasClient{api: api}
		params := &KerberosRealmGetParams{
			SvmUUID: "test-uuid",
			Realm:   "EXAMPLE.COM",
		}
		result, err := client.KerberosRealmGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 0)
	})

	t.Run("WhenRealmExists_ThenReturnRealm", func(tt *testing.T) {
		realmName := "EXAMPLE.COM"
		resp := &nas.KerberosRealmGetOK{
			Payload: &models.KerberosRealm{
				Name: &realmName,
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		params := &KerberosRealmGetParams{
			SvmUUID: "test-uuid",
			Realm:   "EXAMPLE.COM",
		}
		result, err := client.KerberosRealmGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 1)
		assert.Equal(tt, realmName, *result[0].Name)
	})

	t.Run("WhenRealmNameIsNil_ThenReturnEmptySlice", func(tt *testing.T) {
		resp := &nas.KerberosRealmGetOK{
			Payload: &models.KerberosRealm{
				Name: nil,
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		params := &KerberosRealmGetParams{
			SvmUUID: "test-uuid",
			Realm:   "EXAMPLE.COM",
		}
		result, err := client.KerberosRealmGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 0)
	})
}

func TestKerberosRealmCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		kdcPort := int64(88)
		clockSkew := int64(5)
		kdcVendor := "microsoft"
		adminPort := int64(749)
		passwordPort := int64(464)
		kdcIP := "192.168.1.1"
		adName := "ad-server"
		params := &KerberosRealmCreateParams{
			SvmUUID:            "test-uuid",
			Realm:              "EXAMPLE.COM",
			KdcIP:              kdcIP,
			RealmKDCPort:       &kdcPort,
			RealmClockSkew:     &clockSkew,
			RealmKDCVendor:     &kdcVendor,
			AdminServerIP:      &kdcIP,
			AdminServerPort:    &adminPort,
			PasswordServerIP:   &kdcIP,
			PasswordServerPort: &passwordPort,
			ADServerIP:         &kdcIP,
			ADServerName:       &adName,
		}
		err := client.KerberosRealmCreate(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		kdcPort := int64(88)
		clockSkew := int64(5)
		kdcVendor := "microsoft"
		adminPort := int64(749)
		passwordPort := int64(464)
		kdcIP := "192.168.1.1"
		adName := "ad-server"
		params := &KerberosRealmCreateParams{
			SvmUUID:            "test-uuid",
			Realm:              "EXAMPLE.COM",
			KdcIP:              kdcIP,
			RealmKDCPort:       &kdcPort,
			RealmClockSkew:     &clockSkew,
			RealmKDCVendor:     &kdcVendor,
			AdminServerIP:      &kdcIP,
			AdminServerPort:    &adminPort,
			PasswordServerIP:   &kdcIP,
			PasswordServerPort: &passwordPort,
			ADServerIP:         &kdcIP,
			ADServerName:       &adName,
		}
		err := client.KerberosRealmCreate(params)
		assert.NoError(tt, err)
	})
}

func TestKerberosInterfaceCollectionGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		svmUUID := "test-uuid"
		svmName := "test-svm"
		interfaceName := "test-interface"
		params := &KerberosInterfaceCollectionGetParams{
			SvmUUID:       &svmUUID,
			SvmName:       &svmName,
			InterfaceName: &interfaceName,
		}
		result, err := client.KerberosInterfaceCollectionGet(params)
		assert.Nil(tt, result)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenPayloadIsNil_ThenReturnEmptySlice", func(tt *testing.T) {
		api := &mockNASClient{response: &nas.KerberosInterfaceCollectionGetOK{Payload: nil}}
		client := &nasClient{api: api}
		svmUUID := "test-uuid"
		svmName := "test-svm"
		interfaceName := "test-interface"
		params := &KerberosInterfaceCollectionGetParams{
			SvmUUID:       &svmUUID,
			SvmName:       &svmName,
			InterfaceName: &interfaceName,
		}
		result, err := client.KerberosInterfaceCollectionGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 0)
	})

	t.Run("WhenRecordsIsNil_ThenReturnEmptySlice", func(tt *testing.T) {
		resp := &nas.KerberosInterfaceCollectionGetOK{
			Payload: &models.KerberosInterfaceResponse{
				KerberosInterfaceResponseInlineRecords: nil,
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		svmUUID := "test-uuid"
		svmName := "test-svm"
		interfaceName := "test-interface"
		params := &KerberosInterfaceCollectionGetParams{
			SvmUUID:       &svmUUID,
			SvmName:       &svmName,
			InterfaceName: &interfaceName,
		}
		result, err := client.KerberosInterfaceCollectionGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 0)
	})

	t.Run("WhenInterfacesExist_ThenReturnInterfaces", func(tt *testing.T) {
		enabled := true
		interfaceUUID := "interface-uuid"
		interfaceNameStr := "test-interface"
		resp := &nas.KerberosInterfaceCollectionGetOK{
			Payload: &models.KerberosInterfaceResponse{
				KerberosInterfaceResponseInlineRecords: []*models.KerberosInterface{
					{
						Enabled: &enabled,
						Interface: &models.KerberosInterfaceInlineInterface{
							UUID: &interfaceUUID,
							Name: &interfaceNameStr,
						},
					},
				},
			},
		}
		api := &mockNASClient{response: resp}
		client := &nasClient{api: api}
		svmUUID := "test-uuid"
		svmName := "test-svm"
		interfaceName := "test-interface"
		params := &KerberosInterfaceCollectionGetParams{
			SvmUUID:       &svmUUID,
			SvmName:       &svmName,
			InterfaceName: &interfaceName,
		}
		result, err := client.KerberosInterfaceCollectionGet(params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 1)
		assert.Equal(tt, enabled, *result[0].Enabled)
		assert.Equal(tt, interfaceUUID, *result[0].Interface.UUID)
	})
}

func TestKerberosInterfaceModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		api := &mockNASClient{err: errors.New("api error")}
		client := &nasClient{api: api}
		interfaceUUID := "interface-uuid"
		enabled := true
		spn := "nfs/server.example.com@EXAMPLE.COM"
		machineAccount := "NFS-SERVER"
		svmUUID := "test-uuid"
		adminUsername := "admin"
		adminPassword := "password"
		ou := "OU=test"
		params := &KerberosInterfaceModifyParams{
			SvmUUID:           svmUUID,
			InterfaceUUID:     &interfaceUUID,
			IsKerberosEnabled: &enabled,
			Spn:               &spn,
			MachineAccount:    &machineAccount,
			AdminUsername:     &adminUsername,
			AdminPassword:     &adminPassword,
			OU:                &ou,
		}
		err := client.KerberosInterfaceModify(params)
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		api := &mockNASClient{}
		client := &nasClient{api: api}
		interfaceUUID := "interface-uuid"
		enabled := true
		spn := "nfs/server.example.com@EXAMPLE.COM"
		machineAccount := "NFS-SERVER"
		svmUUID := "test-uuid"
		adminUsername := "admin"
		adminPassword := "password"
		ou := "OU=test"
		params := &KerberosInterfaceModifyParams{
			SvmUUID:           svmUUID,
			InterfaceUUID:     &interfaceUUID,
			IsKerberosEnabled: &enabled,
			Spn:               &spn,
			MachineAccount:    &machineAccount,
			AdminUsername:     &adminUsername,
			AdminPassword:     &adminPassword,
			OU:                &ou,
		}
		err := client.KerberosInterfaceModify(params)
		assert.NoError(tt, err)
	})
}
