package vsa

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// withOntapClient swaps the package-level getOntapClientFunc for the duration
// of a test.
func withOntapClient(client ontaprest.RESTClient, err error) func() {
	orig := getOntapClientFunc
	getOntapClientFunc = func(ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return client, err
	}
	return func() { getOntapClientFunc = orig }
}

// --- GetKerberosInterfacesForSVM -------------------------------------------

func TestGetKerberosInterfacesForSVM(t *testing.T) {
	t.Run("client error", func(tt *testing.T) {
		defer withOntapClient(nil, errors.New("client error"))()
		rc := &OntapRestProvider{}
		_, err := rc.GetKerberosInterfacesForSVM("svm-uuid", "svm")
		require.Error(tt, err)
	})

	t.Run("success requests all fields", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockNas := new(ontaprest.MockNASClient)
		mockClient.On("NAS").Return(mockNas)
		expected := []*ontaprest.KerberosInterface{{}}
		mockNas.On("KerberosInterfaceCollectionGet", mock.MatchedBy(func(p *ontaprest.KerberosInterfaceCollectionGetParams) bool {
			return p.SvmUUID != nil && *p.SvmUUID == "svm-uuid" &&
				p.SvmName != nil && *p.SvmName == "svm" &&
				len(p.Fields) == 1 && p.Fields[0] == "*"
		})).Return(expected, nil)

		defer withOntapClient(mockClient, nil)()
		rc := &OntapRestProvider{}
		got, err := rc.GetKerberosInterfacesForSVM("svm-uuid", "svm")
		require.NoError(tt, err)
		assert.Equal(tt, expected, got)
		mockNas.AssertExpectations(tt)
	})
}

// --- DisableKerberosOnInterface --------------------------------------------

func TestDisableKerberosOnInterface(t *testing.T) {
	params := KerberosInterfaceModifyParams{
		SvmUUID:        "svm-uuid",
		InterfaceUUID:  "if-uuid",
		MachineAccount: "machine",
		AdminUsername:  "admin",
		AdminPassword:  "pw",
		OU:             "OU=test",
	}

	t.Run("client error", func(tt *testing.T) {
		defer withOntapClient(nil, errors.New("client error"))()
		rc := &OntapRestProvider{}
		require.Error(tt, rc.DisableKerberosOnInterface(params))
	})

	t.Run("success disables with force", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockNas := new(ontaprest.MockNASClient)
		mockClient.On("NAS").Return(mockNas)
		mockNas.On("KerberosInterfaceModify", mock.MatchedBy(func(p *ontaprest.KerberosInterfaceModifyParams) bool {
			return p.SvmUUID == "svm-uuid" &&
				p.InterfaceUUID != nil && *p.InterfaceUUID == "if-uuid" &&
				p.IsKerberosEnabled != nil && !*p.IsKerberosEnabled &&
				p.Force != nil && *p.Force
		})).Return(nil)

		defer withOntapClient(mockClient, nil)()
		rc := &OntapRestProvider{}
		require.NoError(tt, rc.DisableKerberosOnInterface(params))
		mockNas.AssertExpectations(tt)
	})

	t.Run("modify error", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockNas := new(ontaprest.MockNASClient)
		mockClient.On("NAS").Return(mockNas)
		mockNas.On("KerberosInterfaceModify", mock.Anything).Return(errors.New("modify failed"))

		defer withOntapClient(mockClient, nil)()
		rc := &OntapRestProvider{}
		require.Error(tt, rc.DisableKerberosOnInterface(params))
		mockNas.AssertExpectations(tt)
	})
}

// --- ModifyNetworkInterfaceServicePolicy ------------------------------------

func TestModifyNetworkInterfaceServicePolicy(t *testing.T) {
	t.Run("client error", func(tt *testing.T) {
		defer withOntapClient(nil, errors.New("client error"))()
		rc := &OntapRestProvider{}
		require.Error(tt, rc.ModifyNetworkInterfaceServicePolicy("lif-uuid", "default-data-files"))
	})

	t.Run("success", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockNet := new(ontaprest.MockNetworkingClient)
		mockClient.On("Networking").Return(mockNet)
		mockNet.On("NetworkIPInterfaceModify", mock.MatchedBy(func(p *ontaprest.NetworkIPInterfaceModifyParams) bool {
			return p.UUID != nil && *p.UUID == "lif-uuid" &&
				p.ServicePolicyName != nil && *p.ServicePolicyName == "default-data-files"
		})).Return(nil)

		defer withOntapClient(mockClient, nil)()
		rc := &OntapRestProvider{}
		require.NoError(tt, rc.ModifyNetworkInterfaceServicePolicy("lif-uuid", "default-data-files"))
		mockNet.AssertExpectations(tt)
	})

	t.Run("modify error", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockNet := new(ontaprest.MockNetworkingClient)
		mockClient.On("Networking").Return(mockNet)
		mockNet.On("NetworkIPInterfaceModify", mock.Anything).Return(errors.New("modify failed"))

		defer withOntapClient(mockClient, nil)()
		rc := &OntapRestProvider{}
		require.Error(tt, rc.ModifyNetworkInterfaceServicePolicy("lif-uuid", "default-data-files"))
		mockNet.AssertExpectations(tt)
	})
}

// --- CreateLdapForSVM / CreateLdap ------------------------------------------

func TestCreateLdapForSVM(t *testing.T) {
	ad := &datamodel.ActiveDirectory{
		Domain:                    "example.com",
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{},
	}

	t.Run("client error", func(tt *testing.T) {
		defer withOntapClient(nil, errors.New("client error"))()
		rc := &OntapRestProvider{}
		require.Error(tt, rc.CreateLdapForSVM(ad, "svm-uuid", nil))
	})

	t.Run("empty svm uuid", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		defer withOntapClient(mockClient, nil)()
		rc := &OntapRestProvider{}
		err := rc.CreateLdapForSVM(ad, "", nil)
		require.Error(tt, err)
		assert.Equal(tt, "svm uuid cannot be empty", err.Error())
	})

	t.Run("existing ldap config is a no-op success", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockNS := new(ontaprest.MockNameServicesClient)
		mockClient.On("NameServices").Return(mockNS)
		mockNS.On("LdapGet", mock.Anything).Return(&ontaprest.LdapService{}, nil)

		defer withOntapClient(mockClient, nil)()
		rc := &OntapRestProvider{}
		require.NoError(tt, rc.CreateLdapForSVM(ad, "svm-uuid", nil))
		mockNS.AssertExpectations(tt)
	})

	t.Run("ldap get non-notfound error is returned", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockNS := new(ontaprest.MockNameServicesClient)
		mockClient.On("NameServices").Return(mockNS)
		mockNS.On("LdapGet", mock.Anything).Return((*ontaprest.LdapService)(nil), errors.New("ldap get boom"))

		defer withOntapClient(mockClient, nil)()
		rc := &OntapRestProvider{}
		err := rc.CreateLdapForSVM(ad, "svm-uuid", nil)
		require.Error(tt, err)
		assert.Equal(tt, "ldap get boom", err.Error())
		mockNS.AssertExpectations(tt)
	})
}

func TestCreateLdap(t *testing.T) {
	ad := &datamodel.ActiveDirectory{
		Domain:                    "example.com",
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{},
	}

	t.Run("nil volume errors", func(tt *testing.T) {
		rc := &OntapRestProvider{}
		err := rc.CreateLdap(ad, nil)
		require.Error(tt, err)
		assert.Equal(tt, "volume/svm details cannot be nil", err.Error())
	})

	t.Run("nil svm details errors", func(tt *testing.T) {
		rc := &OntapRestProvider{}
		err := rc.CreateLdap(ad, &datamodel.Volume{})
		require.Error(tt, err)
		assert.Equal(tt, "volume/svm details cannot be nil", err.Error())
	})

	t.Run("delegates to CreateLdapForSVM", func(tt *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockNS := new(ontaprest.MockNameServicesClient)
		mockClient.On("NameServices").Return(mockNS)
		mockNS.On("LdapGet", mock.MatchedBy(func(p *ontaprest.LdapGetParams) bool {
			return p.SvmUUID == "ext-uuid"
		})).Return(&ontaprest.LdapService{}, nil)

		defer withOntapClient(mockClient, nil)()
		rc := &OntapRestProvider{}
		volume := &datamodel.Volume{
			Svm: &datamodel.Svm{SvmDetails: &datamodel.SvmDetails{ExternalUUID: "ext-uuid"}},
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSv4"},
			},
		}
		require.NoError(tt, rc.CreateLdap(ad, volume))
		mockNS.AssertExpectations(tt)
	})
}
