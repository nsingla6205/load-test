package vsa

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontaprestmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// MockNASClient is a mock for the NASClient interface
type MockNASClient struct {
	mock.Mock
}

func (m *MockNASClient) ExportPolicyCreate(params *ontapRest.ExportPolicyCreateParams) (string, error) {
	args := m.Called(params)
	return args.String(0), args.Error(1)
}

func (m *MockNASClient) ExportPolicyGet(params *ontapRest.ExportPolicyGetParams) (*ontapRest.ExportPolicy, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ontapRest.ExportPolicy), args.Error(1)
}

func (m *MockNASClient) ExportPoliciesGet(params *ontapRest.ExportPolicyGetParams) ([]*ontapRest.ExportPolicy, error) {
	args := m.Called(params)
	return args.Get(0).([]*ontapRest.ExportPolicy), args.Error(1)
}

func (m *MockNASClient) ExportPolicyModify(params *ontapRest.ExportPolicyModifyParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) ExportPolicyDelete(params *ontapRest.ExportPolicyDeleteParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) NfsServiceGet(params *ontapRest.NfsServiceGetParams) (*ontapRest.NfsService, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ontapRest.NfsService), args.Error(1)
}

func (m *MockNASClient) NfsServiceCreate(params *ontapRest.NfsServiceCreateParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) NfsServiceModify(params *ontapRest.NfsServiceModifyParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) NfsParamsModify(ctx context.Context, params *ontapRest.NfsModifyParams) error {
	args := m.Called(ctx, params)
	return args.Error(0)
}

func (m *MockNASClient) CifsServiceGet(params *ontapRest.CifsServiceGetParams) (*ontapRest.CifsService, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ontapRest.CifsService), args.Error(1)
}

func (m *MockNASClient) CifsServiceList(params *ontapRest.CifsServiceGetParams) ([]*ontapRest.CifsService, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*ontapRest.CifsService), args.Error(1)
}

func (m *MockNASClient) CifsServiceCreate(params *ontapRest.CifsServiceCreateParams) (bool, *ontapRest.JobAccepted, error) {
	args := m.Called(params)
	var job *ontapRest.JobAccepted
	if val := args.Get(1); val != nil {
		job = val.(*ontapRest.JobAccepted)
	}
	return args.Bool(0), job, args.Error(2)
}

func (m *MockNASClient) CifsServiceModify(params *ontapRest.CifsServiceModifyParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsDomainModify(params *ontapRest.CifsDomainModifyParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsDomainGet(params *ontapRest.CifsDomainGetParams) (*ontapRest.CifsDomain, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ontapRest.CifsDomain), args.Error(1)
}

func (m *MockNASClient) CifsShareACLDelete(params *ontapRest.CifsShareACLDeleteParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsServiceAddMembers(params *ontapRest.CifsServiceModifyGroupMembersParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsServiceDelete(params *ontapRest.CifsServiceDeleteParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsServiceAddSecurityPrivilege(params *ontapRest.CifsServiceModifySecurityPrivilegeParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsShareCreate(params *ontapRest.CifsShareCreateParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsShareModify(params *ontapRest.CifsShareModifyParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsShareDelete(params *ontapRest.CifsShareDeleteParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsShareCollectionGet(params *ontapRest.CifsShareCollectionGetParams) (*ontapRest.CifsShareGetResponse, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ontapRest.CifsShareGetResponse), args.Error(1)
}

func (m *MockNASClient) DomainControllersSrvLookupGet(params *ontapRest.SrvLookupParams) ([]string, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockNASClient) CifsDomainPreferredDCDelete(params *ontapRest.CifsDomainPreferredDCDeleteParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsDomainPreferredDCCreate(params *ontapRest.CifsDomainPreferredDCCreateParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsServiceCollectionGetGroups(params *ontapRest.CifsServiceCollectionGetGroupsParams, ucbf ontapRest.UserCallbackFunc[[]*ontapRest.CifsGroup]) error {
	args := m.Called(params, ucbf)
	return args.Error(0)
}

func (m *MockNASClient) CifsServiceRemoveMembers(params *ontapRest.CifsServiceModifyGroupMembersParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) CifsServiceCollectionGetPrivilegedMembers(params *ontapRest.CifsServiceCollectionGetPrivilegedMembersParams, ucbf ontapRest.UserCallbackFunc[[]string]) error {
	args := m.Called(params, ucbf)
	return args.Error(0)
}

func (m *MockNASClient) CifsServiceRemoveSecurityPrivilege(params *ontapRest.CifsServiceModifySecurityPrivilegeParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) NfsModify(params *ontapRest.NfsModifyParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) KerberosRealmGet(params *ontapRest.KerberosRealmGetParams) ([]*ontapRest.KerberosRealm, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*ontapRest.KerberosRealm), args.Error(1)
}

func (m *MockNASClient) KerberosRealmCreate(params *ontapRest.KerberosRealmCreateParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) KerberosInterfaceCollectionGet(params *ontapRest.KerberosInterfaceCollectionGetParams) ([]*ontapRest.KerberosInterface, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*ontapRest.KerberosInterface), args.Error(1)
}

func (m *MockNASClient) KerberosInterfaceModify(params *ontapRest.KerberosInterfaceModifyParams) error {
	args := m.Called(params)
	return args.Error(0)
}

func (m *MockNASClient) NfsClientsGet(params *ontapRest.NfsClientsGetParams) ([]*ontapRest.NfsClients, error) {
	args := m.Called(params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*ontapRest.NfsClients), args.Error(1)
}

// MockRESTClientForNAS extends the existing MockRESTClient to include NAS
type MockRESTClientForNAS struct {
	ontapRest.MockRESTClient
	nasClient     *MockNASClient
	storageClient *ontapRest.MockStorageClient
}

func (m *MockRESTClientForNAS) NAS() ontapRest.NASClient {
	return m.nasClient
}

func (m *MockRESTClientForNAS) Storage() ontapRest.StorageClient {
	return m.storageClient
}

func TestConvertStorageExportPolicyRuleToONTAP(t *testing.T) {
	tests := []struct {
		name     string
		rule     ExportRule
		index    int
		expected *ontapRest.ExportRule
	}{
		{
			name: "CIFS and NFSv3 enabled with superuser",
			rule: ExportRule{
				AllowedClients: "192.168.1.0/24",
				AnonymousUser:  "",
				Index:          1,
				ChownMode:      "",
				CIFS:           true,
				NFSv3:          true,
				NFSv4:          false,
				Superuser:      true,
			},
			index: 1,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "192.168.1.0/24",
				ChownMode:        models.ChownModeRestricted,
				ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
				ReadWriteRule:    models.AnyAccessProtocol,
				SuperUserRule:    models.AnyAccessProtocol,
				Index:            1,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        []string{utils.GetOntapValue(utils.ProtocolSMB), utils.GetOntapValue(utils.ProtocolNFSv3)},
				AnonymousUser:    "",
			},
		},
		{
			name: "NFSv4 only without superuser",
			rule: ExportRule{
				AllowedClients: "10.0.0.0/8",
				AnonymousUser:  "nobody",
				Index:          2,
				ChownMode:      "unrestricted",
				CIFS:           false,
				NFSv3:          false,
				NFSv4:          true,
				Superuser:      false,
			},
			index: 2,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "10.0.0.0/8",
				ChownMode:        "unrestricted",
				ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
				ReadWriteRule:    models.AnyAccessProtocol,
				SuperUserRule:    models.NoneAccessProtocol,
				Index:            2,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        []string{utils.GetOntapValue(utils.ProtocolNFSv4)},
				AnonymousUser:    "nobody",
			},
		},
		{
			name: "No protocols enabled",
			rule: ExportRule{
				AllowedClients: "0.0.0.0/0",
				AnonymousUser:  "",
				Index:          3,
				ChownMode:      "",
				CIFS:           false,
				NFSv3:          false,
				NFSv4:          false,
				Superuser:      false,
			},
			index: 3,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "0.0.0.0/0",
				ChownMode:        models.ChownModeRestricted,
				ReadOnlyRule:     models.ExportAuthenticationFlavorNever,
				ReadWriteRule:    models.ExportAuthenticationFlavorNever,
				SuperUserRule:    models.NoneAccessProtocol,
				Index:            3,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        nil,
				AnonymousUser:    "",
			},
		},
		{
			name: "All protocols enabled",
			rule: ExportRule{
				AllowedClients: "172.16.0.0/16",
				AnonymousUser:  "guest",
				Index:          4,
				ChownMode:      "restricted",
				CIFS:           true,
				NFSv3:          true,
				NFSv4:          true,
				Superuser:      true,
			},
			index: 4,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "172.16.0.0/16",
				ChownMode:        "restricted",
				ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
				ReadWriteRule:    models.AnyAccessProtocol,
				SuperUserRule:    models.AnyAccessProtocol,
				Index:            4,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        []string{utils.GetOntapValue(utils.ProtocolSMB), utils.GetOntapValue(utils.ProtocolNFSv3), utils.GetOntapValue(utils.ProtocolNFSv4)},
				AnonymousUser:    "guest",
			},
		},
		{
			name: "Only CIFS enabled",
			rule: ExportRule{
				AllowedClients: "192.168.100.0/24",
				AnonymousUser:  "admin",
				Index:          5,
				ChownMode:      "unrestricted",
				CIFS:           true,
				NFSv3:          false,
				NFSv4:          false,
				Superuser:      false,
			},
			index: 5,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "192.168.100.0/24",
				ChownMode:        "unrestricted",
				ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
				ReadWriteRule:    models.AnyAccessProtocol,
				SuperUserRule:    models.NoneAccessProtocol,
				Index:            5,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        []string{utils.GetOntapValue(utils.ProtocolSMB)},
				AnonymousUser:    "admin",
			},
		},
		{
			name: "CIFS and NFSv4 combination",
			rule: ExportRule{
				AllowedClients: "10.10.10.0/24",
				AnonymousUser:  "",
				Index:          6,
				ChownMode:      "",
				CIFS:           true,
				NFSv3:          false,
				NFSv4:          true,
				Superuser:      true,
			},
			index: 6,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "10.10.10.0/24",
				ChownMode:        models.ChownModeRestricted,
				ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
				ReadWriteRule:    models.AnyAccessProtocol,
				SuperUserRule:    models.AnyAccessProtocol,
				Index:            6,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        []string{utils.GetOntapValue(utils.ProtocolSMB), utils.GetOntapValue(utils.ProtocolNFSv4)},
				AnonymousUser:    "",
			},
		},
		{
			name: "NFSv3 and NFSv4 combination without CIFS",
			rule: ExportRule{
				AllowedClients: "172.20.0.0/16",
				AnonymousUser:  "testuser",
				Index:          7,
				ChownMode:      "restricted",
				CIFS:           false,
				NFSv3:          true,
				NFSv4:          true,
				Superuser:      false,
			},
			index: 7,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "172.20.0.0/16",
				ChownMode:        "restricted",
				ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
				ReadWriteRule:    models.AnyAccessProtocol,
				SuperUserRule:    models.NoneAccessProtocol,
				Index:            7,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        []string{utils.GetOntapValue(utils.ProtocolNFSv3), utils.GetOntapValue(utils.ProtocolNFSv4)},
				AnonymousUser:    "testuser",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertStorageExportPolicyRuleToONTAP(tt.rule)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsDefaultRule(t *testing.T) {
	tests := []struct {
		name     string
		rule     *ontaprestmodels.ExportRules
		expected bool
	}{
		{
			name: "Valid default rule",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: true,
		},
		{
			name: "Legacy default with none ro/rw should not count as satisfied",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Wrong client match",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("192.168.1.0/24")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Wrong index",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(1)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Multiple clients",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
					{Match: nillable.ToPointer("192.168.1.0/24")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Wrong chown mode",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("unrestricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Wrong protocol",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("cifs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Multiple protocols",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs"), nillable.ToPointer("cifs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Wrong ro rule",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorAny))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Wrong rw rule",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorAny))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Wrong superuser rule",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
			},
			expected: false,
		},
		{
			name: "Multiple ro rules",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone)), (*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "No clients",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients:   []*ontaprestmodels.ExportClients{},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDefaultRule(tt.rule)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExportPolicyEnsureDefault_Success_DefaultRuleExists(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}
	rc := &OntapRestProvider{Logger: log.NewLogger()}
	svmName := "testSVM"

	// Mock export policy with default rule
	defaultRule := &ontaprestmodels.ExportRules{
		ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
			{Match: nillable.ToPointer("0.0.0.0/0")},
		},
		Index:                      nillable.ToPointer(int64(7)),
		ChownMode:                  nillable.ToPointer("restricted"),
		Protocols:                  []*string{nillable.ToPointer("nfs")},
		ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
		ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
		ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
	}

	mockExportPolicy := &ontapRest.ExportPolicy{
		ExportPolicy: ontaprestmodels.ExportPolicy{
			ID:                      nillable.ToPointer(int64(123)),
			ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{defaultRule},
		},
	}

	mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
		return *params.Name == models.DefaultExportPolicyName && *params.SvmName == svmName
	})).Return(mockExportPolicy, nil)

	err := rc.ExportPolicyEnsureDefault(svmName)

	assert.NoError(t, err)
	mockNASClient.AssertExpectations(t)
}

func TestExportPolicyEnsureDefault_Success_CreatesDefaultRule(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	svmName := "testSVM"

	// Mock export policy without default rule
	mockExportPolicy := &ontapRest.ExportPolicy{
		ExportPolicy: ontaprestmodels.ExportPolicy{
			ID:                      nillable.ToPointer(int64(123)),
			ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{},
		},
	}

	mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
		return *params.Name == models.DefaultExportPolicyName && *params.SvmName == svmName
	})).Return(mockExportPolicy, nil)

	mockNASClient.On("ExportPolicyModify", mock.MatchedBy(func(params *ontapRest.ExportPolicyModifyParams) bool {
		return params.SvmName == svmName &&
			params.ID == 123 &&
			len(params.Rules) == 1 &&
			params.Rules[0].Index == 7 &&
			params.Rules[0].ChownMode == models.ChownModeRestricted &&
			params.Rules[0].ReadOnlyRule == models.ExportAuthenticationFlavorSys &&
			params.Rules[0].ReadWriteRule == models.ExportAuthenticationFlavorSys
	})).Return(nil)

	err := rc.ExportPolicyEnsureDefault(svmName)

	assert.NoError(t, err)
	mockNASClient.AssertExpectations(t)
}

func TestExportPolicyEnsureDefault_Error_PolicyNotFound(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	svmName := "testSVM"

	mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
		return *params.Name == models.DefaultExportPolicyName && *params.SvmName == svmName
	})).Return(nil, nil)

	err := rc.ExportPolicyEnsureDefault(svmName)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Export policy")
	mockNASClient.AssertExpectations(t)
}

func TestExportPolicyEnsureDefault_Error_GetFails(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	svmName := "testSVM"

	mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
		return *params.Name == models.DefaultExportPolicyName && *params.SvmName == svmName
	})).Return(nil, errors.New("API error"))

	err := rc.ExportPolicyEnsureDefault(svmName)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API error")
	mockNASClient.AssertExpectations(t)
}

func TestExportPolicyEnsureDefault_Error_ModifyFails(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	svmName := "testSVM"

	// Mock export policy without default rule
	mockExportPolicy := &ontapRest.ExportPolicy{
		ExportPolicy: ontaprestmodels.ExportPolicy{
			ID:                      nillable.ToPointer(int64(123)),
			ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{},
		},
	}

	mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
		return *params.Name == models.DefaultExportPolicyName && *params.SvmName == svmName
	})).Return(mockExportPolicy, nil)

	mockNASClient.On("ExportPolicyModify", mock.Anything).Return(errors.New("modify error"))

	err := rc.ExportPolicyEnsureDefault(svmName)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "modify error")
	mockNASClient.AssertExpectations(t)
}

func TestCreateExportPolicy_Success(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}

	exportPolicy := &ExportPolicy{
		ExportPolicyName: "test-policy",
		SvmName:          "testSVM",
		ExportRules: []*ExportRule{
			{
				AllowedClients: "192.168.1.0/24",
				Index:          1,
				CIFS:           true,
				NFSv3:          true,
				Superuser:      true,
			},
			{
				AllowedClients: "10.0.0.0/8",
				Index:          2,
				NFSv4:          true,
				Superuser:      false,
			},
		},
	}

	// Mock ensuring default export policy
	defaultRule := &ontaprestmodels.ExportRules{
		ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
			{Match: nillable.ToPointer("0.0.0.0/0")},
		},
		Index:                      nillable.ToPointer(int64(7)),
		ChownMode:                  nillable.ToPointer("restricted"),
		Protocols:                  []*string{nillable.ToPointer("nfs")},
		ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
		ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
		ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
	}

	mockExportPolicy := &ontapRest.ExportPolicy{
		ExportPolicy: ontaprestmodels.ExportPolicy{
			ID:                      nillable.ToPointer(int64(123)),
			ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{defaultRule},
		},
	}

	mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
		return *params.Name == models.DefaultExportPolicyName && *params.SvmName == exportPolicy.SvmName
	})).Return(mockExportPolicy, nil)

	mockNASClient.On("ExportPolicyCreate", mock.MatchedBy(func(params *ontapRest.ExportPolicyCreateParams) bool {
		return params.Name == exportPolicy.ExportPolicyName &&
			params.SvmName == exportPolicy.SvmName &&
			len(params.Rules) == 2
	})).Return("new-policy-id", nil)

	err := rc.CreateExportPolicy(exportPolicy)

	assert.NoError(t, err)
	mockNASClient.AssertExpectations(t)
}

func TestConvertStorageExportPolicyRuleToONTAP_AllSquashEnabled(t *testing.T) {
	// Enable the feature flag for this test
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	allSquashVal := true
	anonUidVal := int64(1001)
	rule := ExportRule{
		AllowedClients: "10.0.0.0/24",
		AnonymousUser:  "",
		Index:          1,
		ChownMode:      "",
		CIFS:           false,
		NFSv3:          true,
		NFSv4:          false,
		Superuser:      false,
		AllSquash:      &allSquashVal,
		AnonUid:        &anonUidVal,
	}

	got := convertStorageExportPolicyRuleToONTAP(rule)

	// AllSquash semantics in ONTAP require ro/rw/superuser=none so every UID is mapped to anon.
	expected := &ontapRest.ExportRule{
		ClientMatch:      "10.0.0.0/24",
		ChownMode:        models.ChownModeRestricted,
		ReadOnlyRule:     models.NoneAccessProtocol,
		ReadWriteRule:    models.NoneAccessProtocol,
		SuperUserRule:    models.NoneAccessProtocol,
		Index:            1,
		NtfsUnixSecurity: *nillable.ToPointer("ignore"),
		Protocols:        []string{utils.GetOntapValue(utils.ProtocolNFSv3)},
		AnonymousUser:    "1001",
	}

	assert.Equal(t, expected, got)
}

func TestConvertStorageExportPolicyRuleToONTAP_AllSquashDisabled(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	allSquashVal := false
	anonUidVal := int64(1001)
	rule := ExportRule{
		AllowedClients: "10.0.0.0/24",
		AnonymousUser:  "nobody",
		Index:          1,
		ChownMode:      "",
		CIFS:           false,
		NFSv3:          true,
		NFSv4:          true,
		Superuser:      true,
		AllSquash:      &allSquashVal,
		AnonUid:        &anonUidVal,
	}

	got := convertStorageExportPolicyRuleToONTAP(rule)

	expected := &ontapRest.ExportRule{
		ClientMatch:      "10.0.0.0/24",
		ChownMode:        models.ChownModeRestricted,
		ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
		ReadWriteRule:    models.AnyAccessProtocol,
		SuperUserRule:    models.AnyAccessProtocol,
		Index:            1,
		NtfsUnixSecurity: *nillable.ToPointer("ignore"),
		Protocols: []string{
			utils.GetOntapValue(utils.ProtocolNFSv3),
			utils.GetOntapValue(utils.ProtocolNFSv4),
		},
		AnonymousUser: "nobody",
	}
	assert.Equal(t, expected, got)
}

func TestConvertStorageExportPolicyRuleToONTAP_AllSquashEnabledWithZeroAnonUid(t *testing.T) {
	// Enable the feature flag for this test
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	allSquashVal := true
	anonUidVal := int64(0) // Explicitly set to 0 (root UID)
	rule := ExportRule{
		AllowedClients: "10.0.0.0/24",
		AnonymousUser:  "", // Empty, should be overridden by AnonUid
		Index:          1,
		ChownMode:      "",
		CIFS:           false,
		NFSv3:          true,
		NFSv4:          false,
		Superuser:      false,
		AllSquash:      &allSquashVal,
		AnonUid:        &anonUidVal,
	}

	got := convertStorageExportPolicyRuleToONTAP(rule)

	expected := &ontapRest.ExportRule{
		ClientMatch:      "10.0.0.0/24",
		ChownMode:        models.ChownModeRestricted,
		ReadOnlyRule:     models.NoneAccessProtocol,
		ReadWriteRule:    models.NoneAccessProtocol,
		SuperUserRule:    models.NoneAccessProtocol,
		Index:            1,
		NtfsUnixSecurity: *nillable.ToPointer("ignore"),
		Protocols:        []string{utils.GetOntapValue(utils.ProtocolNFSv3)},
		AnonymousUser:    "0", // Should be "0", not "root"
	}

	assert.Equal(t, expected, got)
}

func TestConvertStorageExportPolicyRuleToONTAP_AllSquashEnabledAnonUidTakesPrecedence(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	allSquashVal := true
	anonUidVal := int64(0) // Should take precedence over AnonymousUser
	rule := ExportRule{
		AllowedClients: "10.0.0.0/24",
		AnonymousUser:  "nobody", // Set but should be overridden by AnonUid
		Index:          1,
		ChownMode:      "",
		CIFS:           false,
		NFSv3:          true,
		NFSv4:          false,
		Superuser:      false,
		AllSquash:      &allSquashVal,
		AnonUid:        &anonUidVal,
	}

	got := convertStorageExportPolicyRuleToONTAP(rule)

	expected := &ontapRest.ExportRule{
		ClientMatch:      "10.0.0.0/24",
		ChownMode:        models.ChownModeRestricted,
		ReadOnlyRule:     models.NoneAccessProtocol,
		ReadWriteRule:    models.NoneAccessProtocol,
		SuperUserRule:    models.NoneAccessProtocol,
		Index:            1,
		NtfsUnixSecurity: *nillable.ToPointer("ignore"),
		Protocols:        []string{utils.GetOntapValue(utils.ProtocolNFSv3)},
		AnonymousUser:    "0", // AnonUid should take precedence, not "nobody"
	}

	assert.Equal(t, expected, got)
}

func TestCreateExportPolicy_Error_EnsureDefaultFails(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}

	exportPolicy := &ExportPolicy{
		ExportPolicyName: "test-policy",
		SvmName:          "testSVM",
		ExportRules: []*ExportRule{
			{
				AllowedClients: "192.168.1.0/24",
				Index:          1,
				CIFS:           true,
			},
		},
	}

	mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
		return *params.Name == models.DefaultExportPolicyName && *params.SvmName == exportPolicy.SvmName
	})).Return(nil, errors.New("default policy error"))

	err := rc.CreateExportPolicy(exportPolicy)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to ensure default export policy")
	assert.Contains(t, err.Error(), "default policy error")
	mockNASClient.AssertExpectations(t)
}

func TestCreateExportPolicy_Error_CreateFails(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}

	exportPolicy := &ExportPolicy{
		ExportPolicyName: "test-policy",
		SvmName:          "testSVM",
		ExportRules: []*ExportRule{
			{
				AllowedClients: "192.168.1.0/24",
				Index:          1,
				CIFS:           true,
			},
		},
	}

	// Mock ensuring default export policy succeeds
	defaultRule := &ontaprestmodels.ExportRules{
		ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
			{Match: nillable.ToPointer("0.0.0.0/0")},
		},
		Index:                      nillable.ToPointer(int64(7)),
		ChownMode:                  nillable.ToPointer("restricted"),
		Protocols:                  []*string{nillable.ToPointer("nfs")},
		ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
		ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
		ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
	}

	mockExportPolicy := &ontapRest.ExportPolicy{
		ExportPolicy: ontaprestmodels.ExportPolicy{
			ID:                      nillable.ToPointer(int64(123)),
			ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{defaultRule},
		},
	}

	mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
		return *params.Name == models.DefaultExportPolicyName && *params.SvmName == exportPolicy.SvmName
	})).Return(mockExportPolicy, nil)

	mockNASClient.On("ExportPolicyCreate", mock.Anything).Return("", errors.New("create error"))

	err := rc.CreateExportPolicy(exportPolicy)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create error")
	mockNASClient.AssertExpectations(t)
}

func TestCreateExportPolicy_Success_EmptyRules(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}

	exportPolicy := &ExportPolicy{
		ExportPolicyName: "test-policy",
		SvmName:          "testSVM",
		ExportRules:      []*ExportRule{},
	}

	// Mock ensuring default export policy
	defaultRule := &ontaprestmodels.ExportRules{
		ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
			{Match: nillable.ToPointer("0.0.0.0/0")},
		},
		Index:                      nillable.ToPointer(int64(7)),
		ChownMode:                  nillable.ToPointer("restricted"),
		Protocols:                  []*string{nillable.ToPointer("nfs")},
		ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
		ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
		ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
	}

	mockExportPolicy := &ontapRest.ExportPolicy{
		ExportPolicy: ontaprestmodels.ExportPolicy{
			ID:                      nillable.ToPointer(int64(123)),
			ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{defaultRule},
		},
	}

	mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
		return *params.Name == models.DefaultExportPolicyName && *params.SvmName == exportPolicy.SvmName
	})).Return(mockExportPolicy, nil)

	mockNASClient.On("ExportPolicyCreate", mock.MatchedBy(func(params *ontapRest.ExportPolicyCreateParams) bool {
		return params.Name == exportPolicy.ExportPolicyName &&
			params.SvmName == exportPolicy.SvmName &&
			len(params.Rules) == 0
	})).Return("new-policy-id", nil)

	err := rc.CreateExportPolicy(exportPolicy)

	assert.NoError(t, err)
	mockNASClient.AssertExpectations(t)
}

// Additional comprehensive edge case tests
func TestConvertStorageExportPolicyRuleToONTAP_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		rule     ExportRule
		index    int
		expected *ontapRest.ExportRule
	}{
		{
			name: "Only S3 protocol enabled (should result in no protocols)",
			rule: ExportRule{
				AllowedClients: "192.168.1.0/24",
				AnonymousUser:  "",
				Index:          1,
				ChownMode:      "",
				CIFS:           false,
				NFSv3:          false,
				NFSv4:          false,
				S3:             true, // S3 is not handled in the current logic
				Superuser:      false,
			},
			index: 1,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "192.168.1.0/24",
				ChownMode:        models.ChownModeRestricted,
				ReadOnlyRule:     models.ExportAuthenticationFlavorNever,
				ReadWriteRule:    models.ExportAuthenticationFlavorNever,
				SuperUserRule:    models.NoneAccessProtocol,
				Index:            1,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        nil,
				AnonymousUser:    "",
			},
		},
		{
			name: "Large index value",
			rule: ExportRule{
				AllowedClients: "0.0.0.0/0",
				AnonymousUser:  "nobody",
				Index:          99999,
				ChownMode:      "restricted",
				CIFS:           true,
				NFSv3:          false,
				NFSv4:          false,
				Superuser:      true,
			},
			index: 99999,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "0.0.0.0/0",
				ChownMode:        "restricted",
				ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
				ReadWriteRule:    models.AnyAccessProtocol,
				SuperUserRule:    models.AnyAccessProtocol,
				Index:            99999,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        []string{utils.GetOntapValue(utils.ProtocolSMB)},
				AnonymousUser:    "nobody",
			},
		},
		{
			name: "Empty client match",
			rule: ExportRule{
				AllowedClients: "",
				AnonymousUser:  "",
				Index:          1,
				ChownMode:      "",
				CIFS:           true,
				NFSv3:          false,
				NFSv4:          false,
				Superuser:      false,
			},
			index: 1,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "",
				ChownMode:        models.ChownModeRestricted,
				ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
				ReadWriteRule:    models.AnyAccessProtocol,
				SuperUserRule:    models.NoneAccessProtocol,
				Index:            1,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        []string{utils.GetOntapValue(utils.ProtocolSMB)},
				AnonymousUser:    "",
			},
		},
		{
			name: "Custom chown mode override",
			rule: ExportRule{
				AllowedClients: "192.168.1.0/24",
				AnonymousUser:  "",
				Index:          1,
				ChownMode:      "unrestricted",
				CIFS:           false,
				NFSv3:          true,
				NFSv4:          false,
				Superuser:      false,
			},
			index: 1,
			expected: &ontapRest.ExportRule{
				ClientMatch:      "192.168.1.0/24",
				ChownMode:        "unrestricted",
				ReadOnlyRule:     models.ExportAuthenticationFlavorSys,
				ReadWriteRule:    models.AnyAccessProtocol,
				SuperUserRule:    models.NoneAccessProtocol,
				Index:            1,
				NtfsUnixSecurity: *nillable.ToPointer("ignore"),
				Protocols:        []string{utils.GetOntapValue(utils.ProtocolNFSv3)},
				AnonymousUser:    "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertStorageExportPolicyRuleToONTAP(tt.rule)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsDefaultRule_ExtensiveEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		rule     *ontaprestmodels.ExportRules
		expected bool
	}{
		{
			name: "Nil clients slice",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients:   nil,
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Empty clients slice",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients:   []*ontaprestmodels.ExportClients{},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Nil protocols slice",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  nil,
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Empty protocols slice",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
			},
			expected: false,
		},
		{
			name: "Nil authentication rule slices",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    nil,
				ExportRulesInlineRwRule:    nil,
				ExportRulesInlineSuperuser: nil,
			},
			expected: false,
		},
		{
			name: "Empty authentication rule slices",
			rule: &ontaprestmodels.ExportRules{
				ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
					{Match: nillable.ToPointer("0.0.0.0/0")},
				},
				Index:                      nillable.ToPointer(int64(7)),
				ChownMode:                  nillable.ToPointer("restricted"),
				Protocols:                  []*string{nillable.ToPointer("nfs")},
				ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{},
				ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{},
				ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDefaultRule(tt.rule)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExportPolicyEnsureDefault_ErrorEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*MockNASClient)
		wantErr bool
		errMsg  string
	}{
		{
			name: "Export policy has multiple rules but no default rule",
			setup: func(mockNASClient *MockNASClient) {
				nonDefaultRule := &ontaprestmodels.ExportRules{
					ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
						{Match: nillable.ToPointer("192.168.1.0/24")},
					},
					Index:                      nillable.ToPointer(int64(1)),
					ChownMode:                  nillable.ToPointer("restricted"),
					Protocols:                  []*string{nillable.ToPointer("nfs")},
					ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
					ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
					ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				}

				mockExportPolicy := &ontapRest.ExportPolicy{
					ExportPolicy: ontaprestmodels.ExportPolicy{
						ID:                      nillable.ToPointer(int64(123)),
						ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{nonDefaultRule},
					},
				}

				mockNASClient.On("ExportPolicyGet", mock.Anything).Return(mockExportPolicy, nil)
				mockNASClient.On("ExportPolicyModify", mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "Export policy has multiple rules with wrong default rule",
			setup: func(mockNASClient *MockNASClient) {
				wrongDefaultRule := &ontaprestmodels.ExportRules{
					ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
						{Match: nillable.ToPointer("0.0.0.0/0")},
					},
					Index:                      nillable.ToPointer(int64(7)),
					ChownMode:                  nillable.ToPointer("unrestricted"), // Wrong chown mode
					Protocols:                  []*string{nillable.ToPointer("nfs")},
					ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
					ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
					ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				}

				anotherRule := &ontaprestmodels.ExportRules{
					ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
						{Match: nillable.ToPointer("192.168.1.0/24")},
					},
					Index:                      nillable.ToPointer(int64(1)),
					ChownMode:                  nillable.ToPointer("restricted"),
					Protocols:                  []*string{nillable.ToPointer("nfs")},
					ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
					ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
					ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				}

				mockExportPolicy := &ontapRest.ExportPolicy{
					ExportPolicy: ontaprestmodels.ExportPolicy{
						ID:                      nillable.ToPointer(int64(123)),
						ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{wrongDefaultRule, anotherRule},
					},
				}

				mockNASClient.On("ExportPolicyGet", mock.Anything).Return(mockExportPolicy, nil)
				mockNASClient.On("ExportPolicyModify", mock.Anything).Return(nil)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNASClient := new(MockNASClient)
			mockRESTClient := &MockRESTClientForNAS{
				nasClient: mockNASClient,
			}

			getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			}

			rc := &OntapRestProvider{Logger: log.NewLogger()}
			svmName := "testSVM"

			tt.setup(mockNASClient)

			err := rc.ExportPolicyEnsureDefault(svmName)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}

			mockNASClient.AssertExpectations(t)
		})
	}
}

func TestCreateExportPolicy_ExtensiveEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		policy  *ExportPolicy
		setup   func(*MockNASClient)
		wantErr bool
		errMsg  string
	}{
		{
			name: "Export policy with nil export rules",
			policy: &ExportPolicy{
				ExportPolicyName: "test-policy",
				SvmName:          "testSVM",
				ExportRules:      nil,
			},
			setup: func(mockNASClient *MockNASClient) {
				// Mock ensuring default export policy
				defaultRule := &ontaprestmodels.ExportRules{
					ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
						{Match: nillable.ToPointer("0.0.0.0/0")},
					},
					Index:                      nillable.ToPointer(int64(7)),
					ChownMode:                  nillable.ToPointer("restricted"),
					Protocols:                  []*string{nillable.ToPointer("nfs")},
					ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
					ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
					ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				}

				mockExportPolicy := &ontapRest.ExportPolicy{
					ExportPolicy: ontaprestmodels.ExportPolicy{
						ID:                      nillable.ToPointer(int64(123)),
						ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{defaultRule},
					},
				}

				mockNASClient.On("ExportPolicyGet", mock.Anything).Return(mockExportPolicy, nil)
				mockNASClient.On("ExportPolicyCreate", mock.MatchedBy(func(params *ontapRest.ExportPolicyCreateParams) bool {
					return params.Name == "test-policy" &&
						params.SvmName == "testSVM" &&
						len(params.Rules) == 0
				})).Return("new-policy-id", nil)
			},
			wantErr: false,
		},
		{
			name: "Export policy with large number of rules",
			policy: &ExportPolicy{
				ExportPolicyName: "test-policy",
				SvmName:          "testSVM",
				ExportRules: func() []*ExportRule {
					rules := make([]*ExportRule, 100)
					for i := 0; i < 100; i++ {
						rules[i] = &ExportRule{
							AllowedClients: fmt.Sprintf("192.168.%d.0/24", i+1),
							Index:          i + 1,
							CIFS:           i%2 == 0,
							NFSv3:          i%3 == 0,
							NFSv4:          i%4 == 0,
							Superuser:      i%5 == 0,
						}
					}
					return rules
				}(),
			},
			setup: func(mockNASClient *MockNASClient) {
				// Mock ensuring default export policy
				defaultRule := &ontaprestmodels.ExportRules{
					ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
						{Match: nillable.ToPointer("0.0.0.0/0")},
					},
					Index:                      nillable.ToPointer(int64(7)),
					ChownMode:                  nillable.ToPointer("restricted"),
					Protocols:                  []*string{nillable.ToPointer("nfs")},
					ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
					ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys))},
					ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone))},
				}

				mockExportPolicy := &ontapRest.ExportPolicy{
					ExportPolicy: ontaprestmodels.ExportPolicy{
						ID:                      nillable.ToPointer(int64(123)),
						ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{defaultRule},
					},
				}

				mockNASClient.On("ExportPolicyGet", mock.Anything).Return(mockExportPolicy, nil)
				mockNASClient.On("ExportPolicyCreate", mock.MatchedBy(func(params *ontapRest.ExportPolicyCreateParams) bool {
					return params.Name == "test-policy" &&
						params.SvmName == "testSVM" &&
						len(params.Rules) == 100
				})).Return("new-policy-id", nil)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNASClient := new(MockNASClient)
			mockRESTClient := &MockRESTClientForNAS{
				nasClient: mockNASClient,
			}

			getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			}

			rc := &OntapRestProvider{Logger: log.NewLogger()}

			tt.setup(mockNASClient)

			err := rc.CreateExportPolicy(tt.policy)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}

			mockNASClient.AssertExpectations(t)
		})
	}
}

// Test for verifying behavior with nil export policy
func TestCreateExportPolicy_NilPolicy(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}

	// This should cause a panic if we try to access params fields
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Expected panic occurred when passing nil policy: %v", r)
		}
	}()

	// Test with nil policy - this should panic
	err := rc.CreateExportPolicy(nil)
	assert.Error(t, err)
}

// Test to verify all protocol combinations are handled correctly
func TestConvertStorageExportPolicyRuleToONTAP_AllProtocolCombinations(t *testing.T) {
	tests := []struct {
		name     string
		rule     ExportRule
		expected []string
	}{
		{
			name:     "No protocols",
			rule:     ExportRule{CIFS: false, NFSv3: false, NFSv4: false},
			expected: nil,
		},
		{
			name:     "Only CIFS",
			rule:     ExportRule{CIFS: true, NFSv3: false, NFSv4: false},
			expected: []string{utils.GetOntapValue(utils.ProtocolSMB)},
		},
		{
			name:     "Only NFSv3",
			rule:     ExportRule{CIFS: false, NFSv3: true, NFSv4: false},
			expected: []string{utils.GetOntapValue(utils.ProtocolNFSv3)},
		},
		{
			name:     "Only NFSv4",
			rule:     ExportRule{CIFS: false, NFSv3: false, NFSv4: true},
			expected: []string{utils.GetOntapValue(utils.ProtocolNFSv4)},
		},
		{
			name:     "CIFS and NFSv3",
			rule:     ExportRule{CIFS: true, NFSv3: true, NFSv4: false},
			expected: []string{utils.GetOntapValue(utils.ProtocolSMB), utils.GetOntapValue(utils.ProtocolNFSv3)},
		},
		{
			name:     "CIFS and NFSv4",
			rule:     ExportRule{CIFS: true, NFSv3: false, NFSv4: true},
			expected: []string{utils.GetOntapValue(utils.ProtocolSMB), utils.GetOntapValue(utils.ProtocolNFSv4)},
		},
		{
			name:     "NFSv3 and NFSv4",
			rule:     ExportRule{CIFS: false, NFSv3: true, NFSv4: true},
			expected: []string{utils.GetOntapValue(utils.ProtocolNFSv3), utils.GetOntapValue(utils.ProtocolNFSv4)},
		},
		{
			name:     "All protocols",
			rule:     ExportRule{CIFS: true, NFSv3: true, NFSv4: true},
			expected: []string{utils.GetOntapValue(utils.ProtocolSMB), utils.GetOntapValue(utils.ProtocolNFSv3), utils.GetOntapValue(utils.ProtocolNFSv4)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertStorageExportPolicyRuleToONTAP(tt.rule)
			assert.Equal(t, tt.expected, result.Protocols)
		})
	}
}

// Test to verify authentication rule combinations
func TestConvertStorageExportPolicyRuleToONTAP_AuthenticationRules(t *testing.T) {
	tests := []struct {
		name       string
		rule       ExportRule
		expectedRO string
		expectedRW string
		expectedSU string
	}{
		{
			name:       "No protocols, no superuser",
			rule:       ExportRule{CIFS: false, NFSv3: false, NFSv4: false, Superuser: false},
			expectedRO: models.ExportAuthenticationFlavorNever,
			expectedRW: models.ExportAuthenticationFlavorNever,
			expectedSU: models.NoneAccessProtocol,
		},
		{
			name:       "With protocols, no superuser",
			rule:       ExportRule{CIFS: true, NFSv3: false, NFSv4: false, Superuser: false},
			expectedRO: models.ExportAuthenticationFlavorSys,
			expectedRW: models.AnyAccessProtocol,
			expectedSU: models.NoneAccessProtocol,
		},
		{
			name:       "No protocols, with superuser",
			rule:       ExportRule{CIFS: false, NFSv3: false, NFSv4: false, Superuser: true},
			expectedRO: models.ExportAuthenticationFlavorNever,
			expectedRW: models.ExportAuthenticationFlavorNever,
			expectedSU: models.AnyAccessProtocol,
		},
		{
			name:       "With protocols and superuser",
			rule:       ExportRule{CIFS: true, NFSv3: true, NFSv4: true, Superuser: true},
			expectedRO: models.ExportAuthenticationFlavorSys,
			expectedRW: models.AnyAccessProtocol,
			expectedSU: models.AnyAccessProtocol,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertStorageExportPolicyRuleToONTAP(tt.rule)
			assert.Equal(t, tt.expectedRO, result.ReadOnlyRule)
			assert.Equal(t, tt.expectedRW, result.ReadWriteRule)
			assert.Equal(t, tt.expectedSU, result.SuperUserRule)
		})
	}
}

// TestConvertStorageExportPolicyRuleToONTAP_UnixReadWriteRules verifies that the
// read-write flavor is derived from the read-write flag only: a UNIX read-only
// rule must never receive a read-write flavor (which would grant AUTH_SYS clients
// write access on a read-only export), while a UNIX read-write rule does.
func TestConvertStorageExportPolicyRuleToONTAP_UnixReadWriteRules(t *testing.T) {
	tests := []struct {
		name       string
		rule       ExportRule
		expectedRO string
		expectedRW string
	}{
		{
			name:       "UNIX read-only over NFSv3 does not grant read-write",
			rule:       ExportRule{NFSv3: true, UnixReadOnly: true, UnixReadWrite: false},
			expectedRO: models.ExportAuthenticationFlavorSys,
			expectedRW: models.ExportAuthenticationFlavorNever,
		},
		{
			name:       "UNIX read-write over NFSv3 grants read-write",
			rule:       ExportRule{NFSv3: true, UnixReadOnly: false, UnixReadWrite: true},
			expectedRO: models.ExportAuthenticationFlavorSys,
			expectedRW: models.ExportAuthenticationFlavorSys,
		},
		{
			name:       "UNIX read-only over NFSv4 without kerberos does not grant read-write",
			rule:       ExportRule{NFSv4: true, UnixReadOnly: true, UnixReadWrite: false},
			expectedRO: models.ExportAuthenticationFlavorSys,
			expectedRW: models.ExportAuthenticationFlavorNever,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertStorageExportPolicyRuleToONTAP(tt.rule)
			assert.Equal(t, tt.expectedRO, result.ReadOnlyRule)
			assert.Equal(t, tt.expectedRW, result.ReadWriteRule)
		})
	}
}

func TestOntapRestProvider_UpdateExportPolicyRules(t *testing.T) {
	t.Run("WhenGetOntapClientFuncFails_ShouldReturnError", func(t *testing.T) {
		provider := &OntapRestProvider{Logger: log.NewLogger()}

		// Mock getOntapClientFunc to return error
		originalFunc := getOntapClientFunc
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return nil, errors.New("client creation failed")
		}
		defer func() { getOntapClientFunc = originalFunc }()

		params := UpdateExportPolicyRulesParams{
			VolumeName: "test-volume",
			SvmName:    "test-svm",
			ExportPolicy: &ExportPolicy{
				ExportPolicyName: "test-policy",
				ExportRules:      []*ExportRule{},
			},
		}

		err := provider.UpdateExportPolicyRules(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get ONTAP client")
	})

	t.Run("WhenExportPolicyGetFails_ShouldReturnError", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockStorageClient := new(ontapRest.MockStorageClient)
		mockRESTClient := &MockRESTClientForNAS{
			nasClient:     mockNASClient,
			storageClient: mockStorageClient,
		}

		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()

		mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
			return params.Name != nil && *params.Name == "test-policy" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(nil, errors.New("export policy get failed"))

		provider := &OntapRestProvider{Logger: log.NewLogger()}

		params := UpdateExportPolicyRulesParams{
			VolumeName: "test-volume",
			SvmName:    "test-svm",
			ExportPolicy: &ExportPolicy{
				ExportPolicyName: "test-policy",
				ExportRules:      []*ExportRule{},
			},
		}

		err := provider.UpdateExportPolicyRules(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get export policy")
	})

	t.Run("WhenExportPolicyDoesNotExist_ShouldSkipUpdate", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockStorageClient := new(ontapRest.MockStorageClient)
		mockRESTClient := &MockRESTClientForNAS{
			nasClient:     mockNASClient,
			storageClient: mockStorageClient,
		}

		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()

		// Mock export policy not found
		mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
			return params.Name != nil && *params.Name == "test-policy" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(nil, nil) // Return nil for both policy and error to indicate policy doesn't exist

		provider := &OntapRestProvider{Logger: log.NewLogger()}

		params := UpdateExportPolicyRulesParams{
			VolumeName: "test-volume",
			SvmName:    "test-svm",
			ExportPolicy: &ExportPolicy{
				ExportPolicyName: "test-policy",
				ExportRules:      []*ExportRule{},
			},
		}

		err := provider.UpdateExportPolicyRules(params)
		assert.NoError(t, err) // Should not error when policy doesn't exist (since export policy rules are optional)
	})

	t.Run("WhenExportPolicyNameIsDifferent_AndCreatePolicyFails_ShouldReturnError", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockStorageClient := new(ontapRest.MockStorageClient)
		mockRESTClient := &MockRESTClientForNAS{
			nasClient:     mockNASClient,
			storageClient: mockStorageClient,
		}

		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()

		volumeResp := &ontapRest.Volume{
			Volume: ontaprestmodels.Volume{
				Nas: &ontaprestmodels.VolumeInlineNas{
					ExportPolicy: &ontaprestmodels.VolumeInlineNasInlineExportPolicy{
						Name: nillable.ToPointer("current-policy"),
					},
				},
			},
		}
		mockStorageClient.On("VolumeGet", mock.MatchedBy(func(params *ontapRest.VolumeGetParams) bool {
			return params.Name == "test-volume" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(volumeResp, nil)

		// Mock target policy doesn't exist
		mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
			return params.Name != nil && *params.Name == "new-policy" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(nil, errors.New("policy not found"))

		// Mock create policy fails
		mockNASClient.On("ExportPolicyCreate", mock.Anything).
			Return("", errors.New("create failed"))

		provider := &OntapRestProvider{Logger: log.NewLogger()}

		params := UpdateExportPolicyRulesParams{
			VolumeName: "test-volume",
			SvmName:    "test-svm",
			ExportPolicy: &ExportPolicy{
				ExportPolicyName: "new-policy",
				ExportRules: []*ExportRule{
					{
						AllowedClients: "192.168.1.0/24",
						NFSv3:          true,
					},
				},
			},
		}

		err := provider.UpdateExportPolicyRules(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get export policy")
	})

	t.Run("WhenExportPolicyNameIsSame_AndCurrentPolicyGetFails_ShouldReturnError", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockStorageClient := new(ontapRest.MockStorageClient)
		mockRESTClient := &MockRESTClientForNAS{
			nasClient:     mockNASClient,
			storageClient: mockStorageClient,
		}

		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()

		volumeResp := &ontapRest.Volume{
			Volume: ontaprestmodels.Volume{
				Nas: &ontaprestmodels.VolumeInlineNas{
					ExportPolicy: &ontaprestmodels.VolumeInlineNasInlineExportPolicy{
						Name: nillable.ToPointer("same-policy"),
					},
				},
			},
		}
		mockStorageClient.On("VolumeGet", mock.MatchedBy(func(params *ontapRest.VolumeGetParams) bool {
			return params.Name == "test-volume" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(volumeResp, nil)

		// Mock current policy get fails
		mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
			return params.Name != nil && *params.Name == "same-policy" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(nil, errors.New("get policy failed"))

		provider := &OntapRestProvider{Logger: log.NewLogger()}

		params := UpdateExportPolicyRulesParams{
			VolumeName: "test-volume",
			SvmName:    "test-svm",
			ExportPolicy: &ExportPolicy{
				ExportPolicyName: "same-policy",
				ExportRules: []*ExportRule{
					{
						AllowedClients: "192.168.1.0/24",
						NFSv3:          true,
					},
				},
			},
		}

		err := provider.UpdateExportPolicyRules(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get export policy")
	})

	t.Run("WhenExportPolicyNameIsSame_AndPolicyModifyFails_ShouldReturnError", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockStorageClient := new(ontapRest.MockStorageClient)
		mockRESTClient := &MockRESTClientForNAS{
			nasClient:     mockNASClient,
			storageClient: mockStorageClient,
		}

		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()

		volumeResp := &ontapRest.Volume{
			Volume: ontaprestmodels.Volume{
				Nas: &ontaprestmodels.VolumeInlineNas{
					ExportPolicy: &ontaprestmodels.VolumeInlineNasInlineExportPolicy{
						Name: nillable.ToPointer("same-policy"),
					},
				},
			},
		}
		mockStorageClient.On("VolumeGet", mock.MatchedBy(func(params *ontapRest.VolumeGetParams) bool {
			return params.Name == "test-volume" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(volumeResp, nil)

		// Mock current policy get
		currentPolicy := &ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{
				ID: nillable.ToPointer(int64(123)),
			},
		}
		mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
			return params.Name != nil && *params.Name == "same-policy" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(currentPolicy, nil)

		// Mock policy modify fails
		mockNASClient.On("ExportPolicyModify", mock.Anything).
			Return(errors.New("modify failed"))

		provider := &OntapRestProvider{Logger: log.NewLogger()}

		params := UpdateExportPolicyRulesParams{
			VolumeName: "test-volume",
			SvmName:    "test-svm",
			ExportPolicy: &ExportPolicy{
				ExportPolicyName: "same-policy",
				ExportRules: []*ExportRule{
					{
						AllowedClients: "192.168.1.0/24",
						NFSv3:          true,
					},
				},
			},
		}

		err := provider.UpdateExportPolicyRules(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update export policy")
	})

	t.Run("WhenSuccess_ShouldUpdatePolicyRules", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockStorageClient := new(ontapRest.MockStorageClient)
		mockRESTClient := &MockRESTClientForNAS{
			nasClient:     mockNASClient,
			storageClient: mockStorageClient,
		}

		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}
		defer func() { getOntapClientFunc = originalGetOntapClientFunc }()

		volumeResp := &ontapRest.Volume{
			Volume: ontaprestmodels.Volume{
				Nas: &ontaprestmodels.VolumeInlineNas{
					ExportPolicy: &ontaprestmodels.VolumeInlineNasInlineExportPolicy{
						Name: nillable.ToPointer("test-policy"),
					},
				},
			},
		}
		mockStorageClient.On("VolumeGet", mock.MatchedBy(func(params *ontapRest.VolumeGetParams) bool {
			return params.Name == "test-volume" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(volumeResp, nil)

		// Mock current policy get
		currentPolicy := &ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{
				ID: nillable.ToPointer(int64(123)),
			},
		}
		mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
			return params.Name != nil && *params.Name == "test-policy" && params.SvmName != nil && *params.SvmName == "test-svm"
		})).Return(currentPolicy, nil)

		// Mock policy modify success
		mockNASClient.On("ExportPolicyModify", mock.Anything).
			Return(nil)

		provider := &OntapRestProvider{Logger: log.NewLogger()}

		params := UpdateExportPolicyRulesParams{
			VolumeName: "test-volume",
			SvmName:    "test-svm",
			ExportPolicy: &ExportPolicy{
				ExportPolicyName: "test-policy",
				ExportRules: []*ExportRule{
					{
						AllowedClients: "192.168.1.0/24",
						NFSv3:          true,
						Superuser:      true,
					},
				},
			},
		}

		err := provider.UpdateExportPolicyRules(params)
		assert.NoError(t, err)

		mockNASClient.AssertCalled(t, "ExportPolicyModify", mock.Anything)
	})
}

var originalGetOntapClientFunc = getOntapClientFunc

func TestDeleteExportPolicy_Success(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}

	exportPolicy := &ExportPolicy{
		ExportPolicyName: "test-policy",
		SvmName:          "testSVM",
	}

	mockNASClient.On("ExportPolicyDelete", mock.MatchedBy(func(params *ontapRest.ExportPolicyDeleteParams) bool {
		return params.Name == exportPolicy.ExportPolicyName && params.SvmName == exportPolicy.SvmName
	})).Return(nil)

	err := rc.DeleteExportPolicy(exportPolicy)

	assert.NoError(t, err)
	mockNASClient.AssertExpectations(t)
}

func TestDeleteExportPolicy_GetClientFailure(t *testing.T) {
	originalGetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalGetOntapClientFunc }()

	expectedError := errors.New("failed to get client")
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, expectedError
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}

	exportPolicy := &ExportPolicy{
		ExportPolicyName: "test-policy",
		SvmName:          "testSVM",
	}

	err := rc.DeleteExportPolicy(exportPolicy)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get ONTAP client")
	assert.Contains(t, err.Error(), expectedError.Error())
}

func TestDeleteExportPolicy_DeleteFailure(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{
		nasClient: mockNASClient,
	}

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}

	exportPolicy := &ExportPolicy{
		ExportPolicyName: "test-policy",
		SvmName:          "testSVM",
	}

	expectedError := errors.New("delete failed")
	mockNASClient.On("ExportPolicyDelete", mock.MatchedBy(func(params *ontapRest.ExportPolicyDeleteParams) bool {
		return params.Name == exportPolicy.ExportPolicyName && params.SvmName == exportPolicy.SvmName
	})).Return(expectedError)

	err := rc.DeleteExportPolicy(exportPolicy)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete export policy test-policy")
	assert.Contains(t, err.Error(), expectedError.Error())
	mockNASClient.AssertExpectations(t)
}

func TestConvertONTAPExportRuleToVSA_AllSquash(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	none := ontaprestmodels.ExportAuthenticationFlavorNone
	ontapRule := &ontaprestmodels.ExportRules{
		ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
			{Match: nillable.ToPointer("0.0.0.0/0")},
		},
		ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{&none},
		ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{&none},
		ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{&none},
		Protocols:                  []*string{nillable.ToPointer("nfs3")},
		AnonymousUser:              nillable.ToPointer("65534"),
		Index:                      nillable.ToPointer(int64(1)),
	}

	got := convertONTAPExportRuleToVSA(ontapRule)

	require.NotNil(t, got.AllSquash)
	assert.True(t, *got.AllSquash)
	assert.Equal(t, models.ReadWrite, got.AccessType)
	require.NotNil(t, got.AnonUid)
	assert.Equal(t, int64(65534), *got.AnonUid)
	assert.Equal(t, "0.0.0.0/0", got.AllowedClients)
	assert.True(t, got.NFSv3)
}

func TestGetExportPolicy_Success(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

	origFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = origFunc }()
	getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	none := ontaprestmodels.ExportAuthenticationFlavorNone
	mockNASClient.On("ExportPolicyGet", mock.Anything).Return(&ontapRest.ExportPolicy{
		ExportPolicy: ontaprestmodels.ExportPolicy{
			ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{
				{
					ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
						{Match: nillable.ToPointer("0.0.0.0/0")},
					},
					ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{&none},
					ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{&none},
					ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{&none},
					Protocols:                  []*string{nillable.ToPointer("nfs3")},
					AnonymousUser:              nillable.ToPointer("0"),
				},
			},
		},
	}, nil)

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	policy, err := rc.GetExportPolicy("test-policy", "test-svm")

	require.NoError(t, err)
	require.NotNil(t, policy)
	assert.Equal(t, "test-policy", policy.ExportPolicyName)
	assert.Equal(t, "test-svm", policy.SvmName)
	require.Len(t, policy.ExportRules, 1)
	require.NotNil(t, policy.ExportRules[0].AllSquash)
	assert.True(t, *policy.ExportRules[0].AllSquash)
	assert.Equal(t, models.ReadWrite, policy.ExportRules[0].AccessType)
	mockNASClient.AssertExpectations(t)
}

func TestGetExportPolicyProtocols(t *testing.T) {
	t.Run("WhenGetOntapClientFails_ThenReturnError", func(t *testing.T) {
		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return nil, errors.New("connection refused")
		}

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		protocols, err := rc.GetExportPolicyProtocols("test-policy", "test-svm")

		assert.Nil(t, protocols)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get ONTAP client")
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("WhenExportPolicyGetFails_ThenReturnError", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}

		mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
			return *params.Name == "test-policy" && *params.SvmName == "test-svm"
		})).Return(nil, errors.New("policy not found"))

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		protocols, err := rc.GetExportPolicyProtocols("test-policy", "test-svm")

		assert.Nil(t, protocols)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "policy not found")
		mockNASClient.AssertExpectations(t)
	})

	t.Run("WhenPassesCorrectParams_ThenFieldsContainRulesProtocols", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}

		mockNASClient.On("ExportPolicyGet", mock.MatchedBy(func(params *ontapRest.ExportPolicyGetParams) bool {
			return *params.Name == "my-policy" &&
				*params.SvmName == "my-svm" &&
				len(params.Fields) == 1 &&
				params.Fields[0] == "rules.protocols"
		})).Return(&ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{},
		}, nil)

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		_, err := rc.GetExportPolicyProtocols("my-policy", "my-svm")

		assert.NoError(t, err)
		mockNASClient.AssertExpectations(t)
	})

	t.Run("WhenSingleRuleSingleProtocol_ThenReturnProtocol", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}

		nfs3 := "nfs3"
		mockNASClient.On("ExportPolicyGet", mock.Anything).Return(&ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{
				ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{
					{Protocols: []*string{&nfs3}},
				},
			},
		}, nil)

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		protocols, err := rc.GetExportPolicyProtocols("policy", "svm")

		assert.NoError(t, err)
		assert.Equal(t, []string{"nfs3"}, protocols)
	})

	t.Run("WhenSingleRuleMultipleProtocols_ThenReturnAll", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}

		nfs3 := "nfs3"
		cifs := "cifs"
		mockNASClient.On("ExportPolicyGet", mock.Anything).Return(&ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{
				ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{
					{Protocols: []*string{&nfs3, &cifs}},
				},
			},
		}, nil)

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		protocols, err := rc.GetExportPolicyProtocols("policy", "svm")

		assert.NoError(t, err)
		assert.Equal(t, []string{"nfs3", "cifs"}, protocols)
	})

	t.Run("WhenMultipleRulesMultipleProtocols_ThenReturnAllFlattened", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}

		nfs3 := "nfs3"
		nfs4 := "nfs4"
		cifs := "cifs"
		mockNASClient.On("ExportPolicyGet", mock.Anything).Return(&ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{
				ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{
					{Protocols: []*string{&nfs3}},
					{Protocols: []*string{&nfs4, &cifs}},
				},
			},
		}, nil)

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		protocols, err := rc.GetExportPolicyProtocols("policy", "svm")

		assert.NoError(t, err)
		assert.Equal(t, []string{"nfs3", "nfs4", "cifs"}, protocols)
	})

	t.Run("WhenNoRules_ThenReturnNil", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}

		mockNASClient.On("ExportPolicyGet", mock.Anything).Return(&ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{
				ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{},
			},
		}, nil)

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		protocols, err := rc.GetExportPolicyProtocols("policy", "svm")

		assert.NoError(t, err)
		assert.Nil(t, protocols)
	})

	t.Run("WhenRulesHaveNilProtocolPointers_ThenSkipNils", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}

		nfs3 := "nfs3"
		mockNASClient.On("ExportPolicyGet", mock.Anything).Return(&ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{
				ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{
					{Protocols: []*string{nil, &nfs3, nil}},
				},
			},
		}, nil)

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		protocols, err := rc.GetExportPolicyProtocols("policy", "svm")

		assert.NoError(t, err)
		assert.Equal(t, []string{"nfs3"}, protocols)
	})

	t.Run("WhenRulesHaveEmptyProtocolSlice_ThenReturnNil", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}

		mockNASClient.On("ExportPolicyGet", mock.Anything).Return(&ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{
				ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{
					{Protocols: []*string{}},
					{Protocols: []*string{}},
				},
			},
		}, nil)

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		protocols, err := rc.GetExportPolicyProtocols("policy", "svm")

		assert.NoError(t, err)
		assert.Nil(t, protocols)
	})

	t.Run("WhenRulesHaveOnlyNilProtocolPointers_ThenReturnNil", func(t *testing.T) {
		mockNASClient := new(MockNASClient)
		mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

		origFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = origFunc }()
		getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockRESTClient, nil
		}

		mockNASClient.On("ExportPolicyGet", mock.Anything).Return(&ontapRest.ExportPolicy{
			ExportPolicy: ontaprestmodels.ExportPolicy{
				ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{
					{Protocols: []*string{nil, nil}},
				},
			},
		}, nil)

		rc := &OntapRestProvider{Logger: log.NewLogger()}
		protocols, err := rc.GetExportPolicyProtocols("policy", "svm")

		assert.NoError(t, err)
		assert.Nil(t, protocols)
	})
}

// squashRuleExpectation pins the ONTAP fields that decide NFS UID-mapping.
type squashRuleExpectation struct {
	roRule    string
	rwRule    string
	superuser string
	anonUser  string
}

type squashCase struct {
	name      string
	superuser bool
	allSquash *bool
	anonUid   *int64
	anonUser  string // legacy AnonymousUser, used only when AllSquash != true
	want      squashRuleExpectation
}

func ptrBool(v bool) *bool    { return &v }
func ptrInt64(v int64) *int64 { return &v }

func squashCases() []squashCase {
	return []squashCase{
		{
			name:      "NO_ROOT_SQUASH",
			superuser: true,
			want: squashRuleExpectation{
				roRule:    models.ExportAuthenticationFlavorSys,
				rwRule:    models.AnyAccessProtocol,
				superuser: models.AnyAccessProtocol,
				anonUser:  "",
			},
		},
		{
			name:      "ROOT_SQUASH",
			superuser: false,
			want: squashRuleExpectation{
				roRule:    models.ExportAuthenticationFlavorSys,
				rwRule:    models.AnyAccessProtocol,
				superuser: models.NoneAccessProtocol,
				anonUser:  "",
			},
		},
		{
			name:      "ALL_SQUASH",
			superuser: false,
			allSquash: ptrBool(true),
			anonUid:   ptrInt64(2000),
			want: squashRuleExpectation{
				roRule:    models.NoneAccessProtocol,
				rwRule:    models.NoneAccessProtocol,
				superuser: models.NoneAccessProtocol,
				anonUser:  "2000",
			},
		},
		{
			name:      "LEGACY_ANON_USER_FALLBACK",
			superuser: true,
			anonUser:  "nobody",
			want: squashRuleExpectation{
				roRule:    models.ExportAuthenticationFlavorSys,
				rwRule:    models.AnyAccessProtocol,
				superuser: models.AnyAccessProtocol,
				anonUser:  "nobody",
			},
		},
	}
}

func assertSquashRule(t *testing.T, got *ontapRest.ExportRule, want squashRuleExpectation, msg string) {
	t.Helper()
	assert.Equal(t, want.roRule, got.ReadOnlyRule, "%s: ro_rule", msg)
	assert.Equal(t, want.rwRule, got.ReadWriteRule, "%s: rw_rule", msg)
	assert.Equal(t, want.superuser, got.SuperUserRule, "%s: superuser", msg)
	assert.Equal(t, want.anonUser, got.AnonymousUser, "%s: anonymous_user", msg)
}

func TestConvertStorageExportPolicyRuleToONTAP_SquashModeMatrix(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	protocolMixes := []struct {
		name  string
		nfsv3 bool
		nfsv4 bool
	}{
		{"NFSv3", true, false},
		{"NFSv4", false, true},
		{"NFSv3_and_NFSv4", true, true},
	}

	for _, pm := range protocolMixes {
		for _, sc := range squashCases() {
			t.Run(pm.name+"/"+sc.name, func(t *testing.T) {
				rule := ExportRule{
					AllowedClients: "0.0.0.0/0",
					Index:          1,
					NFSv3:          pm.nfsv3,
					NFSv4:          pm.nfsv4,
					Superuser:      sc.superuser,
					AllSquash:      sc.allSquash,
					AnonUid:        sc.anonUid,
					AnonymousUser:  sc.anonUser,
				}
				got := convertStorageExportPolicyRuleToONTAP(rule)
				assertSquashRule(t, got, sc.want, sc.name)
				// The emitted rule must be NFS-only; SMB access is governed
				// by share ACLs, not by export policy.
				assert.NotContains(t, got.Protocols, utils.GetOntapValue(utils.ProtocolSMB))
			})
		}
	}
}

func TestConvertStorageExportPolicyRuleToONTAP_AllSquashDualProtocolEmitsNFSOnlyRule(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	rule := ExportRule{
		AllowedClients: "10.0.0.0/24",
		Index:          1,
		NFSv3:          true,
		CIFS:           false,
		Superuser:      false,
		AllSquash:      ptrBool(true),
		AnonUid:        ptrInt64(2000),
	}

	got := convertStorageExportPolicyRuleToONTAP(rule)

	assertSquashRule(t, got, squashRuleExpectation{
		roRule:    models.NoneAccessProtocol,
		rwRule:    models.NoneAccessProtocol,
		superuser: models.NoneAccessProtocol,
		anonUser:  "2000",
	}, "dual-protocol all_squash")

	assert.ElementsMatch(t,
		[]string{utils.GetOntapValue(utils.ProtocolNFSv3)},
		got.Protocols,
	)
}

func TestConvertStorageExportPolicyRuleToONTAP_AllSquashOverridesSuperuser(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	rule := ExportRule{
		AllowedClients: "0.0.0.0/0",
		Index:          1,
		NFSv3:          true,
		Superuser:      true,
		AllSquash:      ptrBool(true),
		AnonUid:        ptrInt64(2000),
	}
	got := convertStorageExportPolicyRuleToONTAP(rule)

	assert.Equal(t, models.NoneAccessProtocol, got.SuperUserRule)
	assert.Equal(t, models.NoneAccessProtocol, got.ReadOnlyRule)
	assert.Equal(t, models.NoneAccessProtocol, got.ReadWriteRule)
	assert.Equal(t, "2000", got.AnonymousUser)
}

// With IS_ALL_SQUASH_ENABLED off, AllSquash is ignored and the rule
// reverts to legacy semantics (ro=sys, rw=any, AnonymousUser used as-is).
func TestConvertStorageExportPolicyRuleToONTAP_AllSquashFlagDisabled(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(false)

	rule := ExportRule{
		AllowedClients: "0.0.0.0/0",
		Index:          1,
		NFSv3:          true,
		Superuser:      false,
		AllSquash:      ptrBool(true),
		AnonUid:        ptrInt64(2000),
		AnonymousUser:  "nobody",
	}
	got := convertStorageExportPolicyRuleToONTAP(rule)

	assert.Equal(t, models.ExportAuthenticationFlavorSys, got.ReadOnlyRule)
	assert.Equal(t, models.AnyAccessProtocol, got.ReadWriteRule)
	assert.Equal(t, models.NoneAccessProtocol, got.SuperUserRule)
	assert.Equal(t, "nobody", got.AnonymousUser)
}

func TestConvertStorageExportPolicyRuleToONTAP_ReadNoneEmitsNever(t *testing.T) {
	tests := []struct {
		name string
		rule ExportRule
	}{
		{
			name: "READ_NONE with NFSv3 set",
			rule: ExportRule{
				AllowedClients: "10.0.0.0/8",
				AccessType:     models.ReadNone,
				NFSv3:          true,
				Index:          1,
			},
		},
		{
			name: "READ_NONE with NFSv4 set",
			rule: ExportRule{
				AllowedClients: "10.0.0.0/8",
				AccessType:     models.ReadNone,
				NFSv4:          true,
				Index:          1,
			},
		},
		{
			name: "READ_NONE with both NFSv3 and NFSv4 set",
			rule: ExportRule{
				AllowedClients: "10.0.0.0/8",
				AccessType:     models.ReadNone,
				NFSv3:          true,
				NFSv4:          true,
				Index:          1,
			},
		},
		{
			name: "READ_NONE with no protocols set",
			rule: ExportRule{
				AllowedClients: "10.0.0.0/8",
				AccessType:     models.ReadNone,
				Index:          1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertStorageExportPolicyRuleToONTAP(tt.rule)
			assert.Equal(t, models.ExportAuthenticationFlavorNever, got.ReadOnlyRule, "ReadOnlyRule must be never for READ_NONE")
			assert.Equal(t, models.ExportAuthenticationFlavorNever, got.ReadWriteRule, "ReadWriteRule must be never for READ_NONE")
		})
	}
}

// stubDefaultExportPolicy returns a minimal default ExportPolicy that
// satisfies ExportPolicyEnsureDefault.
func stubDefaultExportPolicy() *ontapRest.ExportPolicy {
	return &ontapRest.ExportPolicy{
		ExportPolicy: ontaprestmodels.ExportPolicy{
			ID: nillable.ToPointer(int64(123)),
			ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{
				{
					ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
						{Match: nillable.ToPointer(models.AllowedAllClients)},
					},
					Index:     nillable.ToPointer(int64(models.DefaultIndexExportPolicyRule)),
					ChownMode: nillable.ToPointer(models.ChownModeRestricted),
					Protocols: []*string{nillable.ToPointer(utils.GetOntapValue(utils.ProtocolNFS))},
					ExportRulesInlineRoRule: []*ontaprestmodels.ExportAuthenticationFlavor{
						(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys)),
					},
					ExportRulesInlineRwRule: []*ontaprestmodels.ExportAuthenticationFlavor{
						(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorSys)),
					},
					ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{
						(*ontaprestmodels.ExportAuthenticationFlavor)(nillable.ToPointer(ontaprestmodels.ExportAuthenticationFlavorNone)),
					},
				},
			},
		},
	}
}

// *ontapRest.ExportPolicyCreateParams handed to the ONTAP REST client for each squash mode.
func TestCreateExportPolicy_SquashModeMatrix(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	for _, sc := range squashCases() {
		t.Run(sc.name, func(t *testing.T) {
			mockNASClient := new(MockNASClient)
			mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

			origFunc := getOntapClientFunc
			defer func() { getOntapClientFunc = origFunc }()
			getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			}

			mockNASClient.On("ExportPolicyGet", mock.Anything).Return(stubDefaultExportPolicy(), nil)

			var captured *ontapRest.ExportPolicyCreateParams
			mockNASClient.On("ExportPolicyCreate", mock.Anything).Run(func(args mock.Arguments) {
				captured = args.Get(0).(*ontapRest.ExportPolicyCreateParams)
			}).Return("policy-id", nil)

			rc := &OntapRestProvider{Logger: log.NewLogger()}
			err := rc.CreateExportPolicy(&ExportPolicy{
				ExportPolicyName: "p",
				SvmName:          "svm",
				ExportRules: []*ExportRule{
					{
						AllowedClients: "0.0.0.0/0",
						Index:          1,
						NFSv3:          true,
						Superuser:      sc.superuser,
						AllSquash:      sc.allSquash,
						AnonUid:        sc.anonUid,
						AnonymousUser:  sc.anonUser,
					},
				},
			})

			assert.NoError(t, err)
			if !assert.NotNil(t, captured, "ExportPolicyCreate must be invoked") {
				return
			}
			if !assert.Len(t, captured.Rules, 1) {
				return
			}
			assertSquashRule(t, captured.Rules[0], sc.want, sc.name)
			mockNASClient.AssertExpectations(t)
		})
	}
}

// modify-path counterpart to TestCreateExportPolicy_SquashModeMatrix.
func TestOntapRestProvider_UpdateExportPolicyRules_SquashModeMatrix(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	for _, sc := range squashCases() {
		t.Run(sc.name, func(t *testing.T) {
			mockNASClient := new(MockNASClient)
			mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

			origFunc := getOntapClientFunc
			defer func() { getOntapClientFunc = origFunc }()
			getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			}

			existing := &ontapRest.ExportPolicy{
				ExportPolicy: ontaprestmodels.ExportPolicy{
					ID: nillable.ToPointer(int64(456)),
				},
			}
			mockNASClient.On("ExportPolicyGet", mock.Anything).Return(existing, nil)

			var captured *ontapRest.ExportPolicyModifyParams
			mockNASClient.On("ExportPolicyModify", mock.Anything).Run(func(args mock.Arguments) {
				captured = args.Get(0).(*ontapRest.ExportPolicyModifyParams)
			}).Return(nil)

			rc := &OntapRestProvider{Logger: log.NewLogger()}
			err := rc.UpdateExportPolicyRules(UpdateExportPolicyRulesParams{
				SvmName: "svm",
				ExportPolicy: &ExportPolicy{
					ExportPolicyName: "p",
					ExportRules: []*ExportRule{
						{
							AllowedClients: "0.0.0.0/0",
							Index:          1,
							NFSv3:          true,
							Superuser:      sc.superuser,
							AllSquash:      sc.allSquash,
							AnonUid:        sc.anonUid,
							AnonymousUser:  sc.anonUser,
						},
					},
				},
			})

			assert.NoError(t, err)
			if !assert.NotNil(t, captured, "ExportPolicyModify must be invoked") {
				return
			}
			if !assert.Len(t, captured.Rules, 1) {
				return
			}
			assertSquashRule(t, captured.Rules[0], sc.want, sc.name)
			mockNASClient.AssertExpectations(t)
		})
	}
}

// asserts that anonUid=0 (a valid UID) is honored as anonymous_user="0", not replaced by "root".
func TestCreateExportPolicy_AllSquashAnonUidZero(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}
	origFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = origFunc }()
	getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	mockNASClient.On("ExportPolicyGet", mock.Anything).Return(stubDefaultExportPolicy(), nil)

	var captured *ontapRest.ExportPolicyCreateParams
	mockNASClient.On("ExportPolicyCreate", mock.Anything).Run(func(args mock.Arguments) {
		captured = args.Get(0).(*ontapRest.ExportPolicyCreateParams)
	}).Return("policy-id", nil)

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	err := rc.CreateExportPolicy(&ExportPolicy{
		ExportPolicyName: "p",
		SvmName:          "svm",
		ExportRules: []*ExportRule{{
			AllowedClients: "0.0.0.0/0",
			Index:          1,
			NFSv3:          true,
			Superuser:      false,
			AllSquash:      ptrBool(true),
			AnonUid:        ptrInt64(0),
		}},
	})

	assert.NoError(t, err)
	if assert.NotNil(t, captured) && assert.Len(t, captured.Rules, 1) {
		assertSquashRule(t, captured.Rules[0], squashRuleExpectation{
			roRule:    models.NoneAccessProtocol,
			rwRule:    models.NoneAccessProtocol,
			superuser: models.NoneAccessProtocol,
			anonUser:  "0",
		}, "anonUid=0")
	}
}

func TestCreateExportPolicy_AllSquashAnonUidNil(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}
	origFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = origFunc }()
	getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	mockNASClient.On("ExportPolicyGet", mock.Anything).Return(stubDefaultExportPolicy(), nil)

	var captured *ontapRest.ExportPolicyCreateParams
	mockNASClient.On("ExportPolicyCreate", mock.Anything).Run(func(args mock.Arguments) {
		captured = args.Get(0).(*ontapRest.ExportPolicyCreateParams)
	}).Return("policy-id", nil)

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	err := rc.CreateExportPolicy(&ExportPolicy{
		ExportPolicyName: "p",
		SvmName:          "svm",
		ExportRules: []*ExportRule{{
			AllowedClients: "0.0.0.0/0",
			Index:          1,
			NFSv3:          true,
			Superuser:      false,
			AllSquash:      ptrBool(true),
			AnonUid:        nil,
		}},
	})

	assert.NoError(t, err)
	if assert.NotNil(t, captured) && assert.Len(t, captured.Rules, 1) {
		assert.Equal(t, models.NoneAccessProtocol, captured.Rules[0].ReadOnlyRule, "ro_rule")
		assert.Equal(t, models.NoneAccessProtocol, captured.Rules[0].ReadWriteRule, "rw_rule")
		assert.Equal(t, models.NoneAccessProtocol, captured.Rules[0].SuperUserRule, "superuser")
		assert.Equal(t, "", captured.Rules[0].AnonymousUser, "anonymous_user")
	}
}

func TestCreateExportPolicy_MultiRuleWithSingleAllSquash(t *testing.T) {
	originalValue := utils.IsAllSquashEnabled
	defer func() { utils.EnableAllSquashForTesting(originalValue) }()
	utils.EnableAllSquashForTesting(true)

	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}
	origFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = origFunc }()
	getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	mockNASClient.On("ExportPolicyGet", mock.Anything).Return(stubDefaultExportPolicy(), nil)

	var captured *ontapRest.ExportPolicyCreateParams
	mockNASClient.On("ExportPolicyCreate", mock.Anything).Run(func(args mock.Arguments) {
		captured = args.Get(0).(*ontapRest.ExportPolicyCreateParams)
	}).Return("policy-id", nil)

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	err := rc.CreateExportPolicy(&ExportPolicy{
		ExportPolicyName: "p",
		SvmName:          "svm",
		ExportRules: []*ExportRule{
			{
				AllowedClients: "10.0.0.0/8",
				Index:          1,
				NFSv3:          true,
				Superuser:      true,
			},
			{
				AllowedClients: "0.0.0.0/0",
				Index:          2,
				NFSv3:          true,
				Superuser:      false,
				AllSquash:      ptrBool(true),
				AnonUid:        ptrInt64(2000),
			},
		},
	})

	assert.NoError(t, err)
	if !assert.NotNil(t, captured) || !assert.Len(t, captured.Rules, 2) {
		return
	}

	assertSquashRule(t, captured.Rules[0], squashRuleExpectation{
		roRule:    models.ExportAuthenticationFlavorSys,
		rwRule:    models.AnyAccessProtocol,
		superuser: models.AnyAccessProtocol,
		anonUser:  "",
	}, "rule[0] no_root_squash")
	assert.Equal(t, "10.0.0.0/8", captured.Rules[0].ClientMatch)
	assert.EqualValues(t, 1, captured.Rules[0].Index)

	assertSquashRule(t, captured.Rules[1], squashRuleExpectation{
		roRule:    models.NoneAccessProtocol,
		rwRule:    models.NoneAccessProtocol,
		superuser: models.NoneAccessProtocol,
		anonUser:  "2000",
	}, "rule[1] all_squash")
	assert.Equal(t, "0.0.0.0/0", captured.Rules[1].ClientMatch)
	assert.EqualValues(t, 2, captured.Rules[1].Index)
}

func TestGetExportPolicy_SkipsNilRules(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

	origFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = origFunc }()
	getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	sys := ontaprestmodels.ExportAuthenticationFlavorSys
	mockNASClient.On("ExportPolicyGet", mock.Anything).Return(&ontapRest.ExportPolicy{
		ExportPolicy: ontaprestmodels.ExportPolicy{
			ExportPolicyInlineRules: []*ontaprestmodels.ExportRules{
				nil,
				{
					ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
						{Match: nillable.ToPointer("0.0.0.0/0")},
					},
					ExportRulesInlineRoRule: []*ontaprestmodels.ExportAuthenticationFlavor{&sys},
					ExportRulesInlineRwRule: []*ontaprestmodels.ExportAuthenticationFlavor{&sys},
					Protocols:               []*string{nillable.ToPointer("nfs3")},
				},
			},
		},
	}, nil)

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	policy, err := rc.GetExportPolicy("test-policy", "test-svm")
	require.NoError(t, err)
	require.Len(t, policy.ExportRules, 1)
}

func TestGetExportPolicy_ClientError(t *testing.T) {
	origFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = origFunc }()
	getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, errors.New("connection refused")
	}

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	_, err := rc.GetExportPolicy("test-policy", "test-svm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get ONTAP client")
}

func TestGetExportPolicy_NotFound(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

	origFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = origFunc }()
	getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	mockNASClient.On("ExportPolicyGet", mock.Anything).Return(nil, errors.New("policy missing"))

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	_, err := rc.GetExportPolicy("missing-policy", "test-svm")
	require.Error(t, err)
}

func TestGetExportPolicy_NilPolicyResponse(t *testing.T) {
	mockNASClient := new(MockNASClient)
	mockRESTClient := &MockRESTClientForNAS{nasClient: mockNASClient}

	origFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = origFunc }()
	getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockRESTClient, nil
	}

	mockNASClient.On("ExportPolicyGet", mock.Anything).Return(nil, nil)

	rc := &OntapRestProvider{Logger: log.NewLogger()}
	_, err := rc.GetExportPolicy("missing-policy", "test-svm")
	require.Error(t, err)
}

func TestConvertONTAPExportRuleToVSA_NonAllSquashPath(t *testing.T) {
	sys := ontaprestmodels.ExportAuthenticationFlavorSys
	never := ontaprestmodels.ExportAuthenticationFlavorNever
	krb5 := ontaprestmodels.ExportAuthenticationFlavorKrb5
	any := ontaprestmodels.ExportAuthenticationFlavorAny

	rule := convertONTAPExportRuleToVSA(&ontaprestmodels.ExportRules{
		ExportRulesInlineClients: []*ontaprestmodels.ExportClients{
			{Match: nillable.ToPointer("10.0.0.0/8")},
		},
		ExportRulesInlineRoRule:    []*ontaprestmodels.ExportAuthenticationFlavor{&sys},
		ExportRulesInlineRwRule:    []*ontaprestmodels.ExportAuthenticationFlavor{&never},
		ExportRulesInlineSuperuser: []*ontaprestmodels.ExportAuthenticationFlavor{&any},
		Protocols:                  []*string{nillable.ToPointer("nfs4"), nillable.ToPointer("cifs")},
		AnonymousUser:              nillable.ToPointer("root"),
		ChownMode:                  nillable.ToPointer("restricted"),
		Index:                      nillable.ToPointer(int64(2)),
	})

	require.NotNil(t, rule)
	assert.Equal(t, "10.0.0.0/8", rule.AllowedClients)
	assert.True(t, rule.NFSv4)
	assert.True(t, rule.CIFS)
	assert.True(t, rule.UnixReadOnly)
	assert.True(t, rule.Superuser)
	assert.Equal(t, models.ReadOnly, rule.AccessType)
	assert.Equal(t, "restricted", rule.ChownMode)
	assert.Equal(t, 2, rule.Index)

	readNone := convertONTAPExportRuleToVSA(&ontaprestmodels.ExportRules{
		ExportRulesInlineClients: []*ontaprestmodels.ExportClients{{Match: nillable.ToPointer("0.0.0.0/0")}},
		ExportRulesInlineRoRule:  []*ontaprestmodels.ExportAuthenticationFlavor{&never},
		ExportRulesInlineRwRule:  []*ontaprestmodels.ExportAuthenticationFlavor{&never},
	})
	assert.Equal(t, models.ReadNone, readNone.AccessType)

	readWrite := convertONTAPExportRuleToVSA(&ontaprestmodels.ExportRules{
		ExportRulesInlineClients: []*ontaprestmodels.ExportClients{{Match: nillable.ToPointer("0.0.0.0/0")}},
		ExportRulesInlineRoRule:  []*ontaprestmodels.ExportAuthenticationFlavor{&never},
		ExportRulesInlineRwRule:  []*ontaprestmodels.ExportAuthenticationFlavor{&krb5},
	})
	assert.True(t, readWrite.Kerberos5ReadWrite)
	assert.Equal(t, models.ReadWrite, readWrite.AccessType)
}
