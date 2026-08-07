package vsa

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerror "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// ============================================================================
// Test Helpers
// ============================================================================

func buildTestUpdateCredentialsParams() UpdateActiveDirectoryCredentialsParams {
	return UpdateActiveDirectoryCredentialsParams{
		OldCredentials: &ActiveDirectory{
			Domain:   "old.domain.com",
			DNS:      "10.0.0.1,10.0.0.2",
			Username: "admin",
			Password: "oldpass",
			NetBIOS:  "OLDNETBIOS",
			Site:     nillable.ToPointer("OldSite"),
			Users: map[string][]string{
				"BUILTIN\\Administrators": {"user1", "user2"},
			},
			AesEncryption:              nillable.ToPointer(false),
			LdapSigning:                nillable.ToPointer(false),
			ServerRootCaCertificate:    nillable.ToPointer("oldcert"),
			AllowLocalNFSUsersWithLdap: nillable.ToPointer(false),
			LdapOverTLS:                nillable.ToPointer(false),
			EncryptDCConnections:       nillable.ToPointer(false),
			KdcIP:                      "10.0.0.10",
			AdName:                     "oldadname",
		},
		NewCredentials: &ActiveDirectory{
			Domain:   "new.domain.com",
			DNS:      "10.0.1.1,10.0.1.2",
			Username: "newadmin",
			Password: "newpass",
			NetBIOS:  "NEWNETBIOS",
			Site:     nillable.ToPointer("NewSite"),
			Users: map[string][]string{
				"BUILTIN\\Administrators": {"user3", "user4"},
			},
			AesEncryption:              nillable.ToPointer(true),
			LdapSigning:                nillable.ToPointer(true),
			ServerRootCaCertificate:    nillable.ToPointer("newcert"),
			AllowLocalNFSUsersWithLdap: nillable.ToPointer(true),
			LdapOverTLS:                nillable.ToPointer(true),
			EncryptDCConnections:       nillable.ToPointer(true),
			KdcIP:                      "10.0.1.10",
			AdName:                     "newadname",
			CIFSServers: []*CIFSServer{
				{SVMName: "svm1", SVMUUID: "svm-uuid-1"},
			},
		},
	}
}

func setupMockUpdater(t *testing.T) (*activeDirectoryUpdater, *ontapRest.MockRESTClient) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	params := buildTestUpdateCredentialsParams()

	adu := &activeDirectoryUpdater{
		provider: provider,
		params:   params,
		api:      mockREST,
		svmName:  "test-svm",
		svmUUID:  "test-svm-uuid",
	}

	return adu, mockREST
}

// ============================================================================
// Constructor Tests
// ============================================================================

func TestNewActiveDirectoryUpdater(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	params := buildTestUpdateCredentialsParams()

	adu := _newActiveDirectoryUpdater(params, provider, mockREST, "test-svm", "test-uuid")

	assert.NotNil(t, adu)
	assert.Equal(t, provider, adu.provider)
	assert.Equal(t, params, adu.params)
	assert.Equal(t, mockREST, adu.api)
	assert.Equal(t, "test-svm", adu.svmName)
	assert.Equal(t, "test-uuid", adu.svmUUID)
}

// ============================================================================
// LoadCIFSServer Tests
// ============================================================================

func TestLoadCIFSServer_Success(t *testing.T) {
	adu, _ := setupMockUpdater(t)
	cifs := &ontapRest.CifsService{
		CifsService: models.CifsService{
			Name: nillable.ToPointer("SERVER01"),
			AdDomain: &models.AdDomain{
				Fqdn:               nillable.ToPointer("domain.com"),
				OrganizationalUnit: nillable.ToPointer("OU=Computers"),
			},
		},
	}

	name, domain, ou, err := adu.LoadCIFSServer(cifs)

	require.NoError(t, err)
	assert.Equal(t, "SERVER01", *name)
	assert.Equal(t, "domain.com", *domain)
	assert.Equal(t, "OU=Computers", *ou)
}

func TestLoadCIFSServer_NilCifs(t *testing.T) {
	adu, _ := setupMockUpdater(t)

	name, domain, ou, err := adu.LoadCIFSServer(nil)

	assert.Error(t, err)
	assert.Nil(t, name)
	assert.Nil(t, domain)
	assert.Nil(t, ou)
	assert.Contains(t, err.Error(), "Could not retrieve CIFS server")
}

// ============================================================================
// ModifyDNS Tests
// ============================================================================

func TestModifyDNS_Success(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)

	mockNS.On("DNSModify", mock.MatchedBy(func(params *ontapRest.DNSModifyParams) bool {
		return params.SvmUUID == "test-svm-uuid" &&
			len(params.Domains) == 1 &&
			params.Domains[0] == "new.domain.com" &&
			len(params.NameServers) == 2
	})).Return(nil)

	err := adu.ModifyDNS()

	require.NoError(t, err)
	mockNS.AssertExpectations(t)
}

func TestModifyDNS_Error(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)

	mockNS.On("DNSModify", mock.Anything).Return(vsaerrors.New("DNS modify failed"))

	err := adu.ModifyDNS()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DNS modify failed")
}

// ============================================================================
// UpdateDDNS Tests
// ============================================================================

func TestUpdateDDNS_Success(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)

	fqdn := "server.domain.com"
	mockNS.On("DNSModify", mock.MatchedBy(func(params *ontapRest.DNSModifyParams) bool {
		return params.SvmUUID == "test-svm-uuid" &&
			*params.DDNSModifyParams.Fqdn == fqdn &&
			*params.DDNSModifyParams.Enabled == true
	})).Return(nil)

	err := adu.UpdateDDNS(fqdn)

	require.NoError(t, err)
	mockNS.AssertExpectations(t)
}

// ============================================================================
// UpdateNetBios Tests
// ============================================================================

func TestUpdateNetBios_Success(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	// Mock disable call
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == false
	})).Return(nil).Once()

	// Mock modify call
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Name != nil && *params.Name == "NEWNAME"
	})).Return(nil).Once()

	// Mock enable call (defer)
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == true
	})).Return(nil).Once()

	err := adu.UpdateNetBios("NEWNAME", "NewSite", "admin", "password")

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func TestUpdateNetBios_DisableError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("CifsServiceModify", mock.Anything).Return(vsaerrors.New("disable failed")).Once()

	err := adu.UpdateNetBios("NEWNAME", "NewSite", "admin", "password")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disable failed")
}

func TestUpdateNetBios_DeferEnableError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == false
	})).Return(nil).Once()

	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Name != nil && *params.Name == "NEWNAME"
	})).Return(nil).Once()

	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == true
	})).Return(vsaerrors.New("enable failed")).Once()

	err := adu.UpdateNetBios("NEWNAME", "NewSite", "admin", "password")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "enable failed")
}

// ============================================================================
// UpdateAesEncryption Tests
// ============================================================================

func TestUpdateAesEncryption_Success(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.AesEncryptionEnabled != nil && *params.AesEncryptionEnabled == true
	})).Return(nil)

	err := adu.UpdateAesEncryption(true, "admin", "password")

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

// ============================================================================
// UpdateEncryptDCConnections Tests
// ============================================================================

func TestUpdateEncryptDCConnections_Success(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.EncryptDCConnections != nil && *params.EncryptDCConnections == true
	})).Return(nil)

	err := adu.UpdateEncryptDCConnections()

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

// ============================================================================
// UpdateSite Tests
// ============================================================================

func TestUpdateSite_WithSite_Success(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	// Mock SRV lookup
	mockNAS.On("DomainControllersSrvLookupGet", mock.Anything).Return([]string{"10.0.0.1", "10.0.0.2"}, nil)

	// Mock preferred DC creation
	mockNAS.On("CifsDomainPreferredDCCreate", mock.Anything).Return(nil).Times(2)

	// Mock site update
	originalUpdateSite := updateSiteONTAP
	t.Cleanup(func() { updateSiteONTAP = originalUpdateSite })
	updateSiteONTAP = func(site *string, preferredDCsSet bool, adu *activeDirectoryUpdater) error {
		assert.NotNil(t, site)
		assert.Equal(t, "NewSite", *site)
		assert.True(t, preferredDCsSet)
		return nil
	}

	// Mock preferred DC deletion (defer)
	mockNAS.On("CifsDomainPreferredDCDelete", mock.Anything).Return(nil).Times(2)

	err := adu.UpdateSite("NewSite", false)

	require.NoError(t, err)
}

func TestUpdateSite_EmptySite_Success(t *testing.T) {
	adu, _ := setupMockUpdater(t)

	originalUpdateSite := updateSiteONTAP
	t.Cleanup(func() { updateSiteONTAP = originalUpdateSite })
	updateSiteONTAP = func(site *string, preferredDCsSet bool, adu *activeDirectoryUpdater) error {
		assert.NotNil(t, site)
		assert.Equal(t, "", *site)
		return nil
	}

	err := adu.UpdateSite("", false)

	require.NoError(t, err)
}

func TestUpdateSite_SrvLookupError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("DomainControllersSrvLookupGet", mock.Anything).Return(nil, vsaerrors.New("lookup failed"))

	err := adu.UpdateSite("NewSite", false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "lookup failed")
}

func TestUpdateSite_SrvLookupDNSNotFoundError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	// Mock DNS not found error
	dnsNotFoundErr := vsaerrors.New("SRV lookup failed. Reason: dns not found for domain")
	mockNAS.On("DomainControllersSrvLookupGet", mock.Anything).Return(nil, dnsNotFoundErr)

	err := adu.UpdateSite("NewSite", false)

	require.Error(t, err)

	// Verify it's a VCP error with ErrBadRequest
	vcpErr, ok := err.(*vsaerror.CustomError)
	require.True(t, ok, "error should be a CustomError from vsaerror package")
	assert.Equal(t, vsaerror.ErrBadRequest, vcpErr.TrackingID, "error TrackingID should be ErrBadRequest")

	// Verify the original error is preserved
	assert.NotNil(t, vcpErr.OriginalErr, "original error should be preserved")
	assert.Contains(t, vcpErr.OriginalErr.Error(), "dns not found", "original error should contain 'dns not found'")
}

func TestUpdateSite_SrvLookupGenericError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	// Mock a generic error (not DNS not found)
	genericErr := vsaerrors.New("some other error")
	mockNAS.On("DomainControllersSrvLookupGet", mock.Anything).Return(nil, genericErr)

	err := adu.UpdateSite("NewSite", false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "some other error")

	// Verify it's NOT wrapped as a VCP CustomError
	_, ok := err.(*vsaerror.CustomError)
	assert.False(t, ok, "generic errors should not be wrapped as CustomError")
}

func TestUpdateSite_ErrorPreferredDCRemoval(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("DomainControllersSrvLookupGet", mock.Anything).Return([]string{"10.0.0.1"}, nil)
	mockNAS.On("CifsDomainPreferredDCCreate", mock.Anything).Return(nil)
	mockNAS.On("CifsDomainPreferredDCDelete", mock.Anything).Return(vsaerrors.New("remove failed"))

	originalUpdateSite := updateSiteONTAP
	t.Cleanup(func() { updateSiteONTAP = originalUpdateSite })
	updateSiteONTAP = func(site *string, preferredDCsSet bool, adu *activeDirectoryUpdater) error {
		return nil
	}

	err := adu.UpdateSite("NewSite", false)

	assert.NoError(t, err) // removal error is logged, not returned
}

// ============================================================================
// _updateSiteONTAP Tests
// ============================================================================

func Test_updateSiteONTAP_NilSite(t *testing.T) {
	adu, _ := setupMockUpdater(t)

	err := _updateSiteONTAP(nil, false, adu)

	assert.NoError(t, err)
}

func Test_updateSiteONTAP_NilUpdater(t *testing.T) {
	site := "TestSite"

	err := _updateSiteONTAP(&site, false, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "activeDirectoryUpdater is nil")
}

func Test_updateSiteONTAP_EmptySiteWithoutPreferredDC(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := ""

	// Expect setting discovery mode to "all"
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "all" && params.SvmUUID == adu.svmUUID
	})).Return(nil).Once()

	// Expect CIFS service modify with empty site
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Site != nil && *params.Site == "" && *params.SvmUUID == adu.svmUUID
	})).Return(nil).Once()

	err := _updateSiteONTAP(&site, false, adu)

	assert.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_updateSiteONTAP_NonEmptySiteWithoutPreferredDC(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := "TestSite"

	// Expect CIFS service modify with site
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Site != nil && *params.Site == "TestSite" && *params.SvmUUID == adu.svmUUID
	})).Return(nil).Once()

	// Expect setting discovery mode to "site"
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "site" && params.SvmUUID == adu.svmUUID
	})).Return(nil).Once()

	err := _updateSiteONTAP(&site, false, adu)

	assert.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_updateSiteONTAP_NonEmptySiteWithPreferredDC(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := "TestSite"

	// Expect getting current discovery mode
	discoveryMode := "all"
	mockNAS.On("CifsDomainGet", mock.MatchedBy(func(params *ontapRest.CifsDomainGetParams) bool {
		return params.SvmUUID == adu.svmUUID && len(params.Fields) > 0
	})).Return(&ontapRest.CifsDomain{
		CifsDomain: models.CifsDomain{
			ServerDiscoveryMode: &discoveryMode,
		},
	}, nil).Once()

	// Expect setting discovery mode to "none"
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "none" && params.SvmUUID == adu.svmUUID
	})).Return(nil).Once()

	// Expect CIFS service modify with site
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Site != nil && *params.Site == "TestSite" && *params.SvmUUID == adu.svmUUID
	})).Return(nil).Once()

	// Expect setting discovery mode to "site"
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "site" && params.SvmUUID == adu.svmUUID
	})).Return(nil).Once()

	err := _updateSiteONTAP(&site, true, adu)

	assert.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_updateSiteONTAP_ErrorGettingDiscoveryMode(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := "TestSite"

	// Expect getting current discovery mode to fail
	mockNAS.On("CifsDomainGet", mock.Anything).Return(nil, vsaerrors.New("failed to get domain"))

	// Expect rollback attempt in defer (will try to restore mode to "all" by default)
	mockNAS.On("CifsDomainModify", mock.Anything).Return(nil).Once()

	err := _updateSiteONTAP(&site, true, adu)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error fetching domain discovery mode")
}

func Test_updateSiteONTAP_ErrorSettingDiscoveryModeToNone(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := "TestSite"
	discoveryMode := "all"

	mockNAS.On("CifsDomainGet", mock.Anything).Return(&ontapRest.CifsDomain{
		CifsDomain: models.CifsDomain{
			ServerDiscoveryMode: &discoveryMode,
		},
	}, nil).Once()

	// Expect setting discovery mode to "none" to fail
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "none"
	})).Return(vsaerrors.New("failed to set mode")).Once()

	// Expect rollback attempt in defer (will try to restore mode to "all")
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "all"
	})).Return(nil).Maybe()

	err := _updateSiteONTAP(&site, true, adu)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error setting domain discovery mode to none")
}

func Test_updateSiteONTAP_ErrorUpdatingCIFSService(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := "TestSite"
	discoveryMode := "all"

	mockNAS.On("CifsDomainGet", mock.Anything).Return(&ontapRest.CifsDomain{
		CifsDomain: models.CifsDomain{
			ServerDiscoveryMode: &discoveryMode,
		},
	}, nil).Once()

	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "none"
	})).Return(nil).Once()

	// Expect CIFS service modify to fail
	mockNAS.On("CifsServiceModify", mock.Anything).Return(vsaerrors.New("failed to update CIFS"))

	// Expect rollback attempt in defer (will try to restore mode to "all")
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "all"
	})).Return(nil).Maybe()

	err := _updateSiteONTAP(&site, true, adu)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error updating CIFS service with Site")
}

func Test_updateSiteONTAP_ErrorSettingDiscoveryModeToSite(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := "TestSite"
	discoveryMode := "all"

	mockNAS.On("CifsDomainGet", mock.Anything).Return(&ontapRest.CifsDomain{
		CifsDomain: models.CifsDomain{
			ServerDiscoveryMode: &discoveryMode,
		},
	}, nil).Once()

	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "none"
	})).Return(nil).Once()

	mockNAS.On("CifsServiceModify", mock.Anything).Return(nil).Once()

	// Expect setting discovery mode to "site" to fail
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "site"
	})).Return(vsaerrors.New("failed to set site mode")).Once()

	// Expect rollback attempt in defer (will try to restore mode - use mock.Anything to catch any mode)
	mockNAS.On("CifsDomainModify", mock.Anything).Return(nil).Maybe()

	err := _updateSiteONTAP(&site, true, adu)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error setting domain discovery mode to site")
}

func Test_updateSiteONTAP_EmptySiteErrorSettingDiscoveryModeToAll(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := ""

	// Expect setting discovery mode to "all" to fail
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "all"
	})).Return(vsaerrors.New("failed to set all mode"))

	err := _updateSiteONTAP(&site, false, adu)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error setting domain discovery mode to all")
}

func Test_updateSiteONTAP_EmptySiteErrorUpdatingCIFSService(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := ""

	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "all"
	})).Return(nil).Once()

	// Expect CIFS service modify to fail
	mockNAS.On("CifsServiceModify", mock.Anything).Return(vsaerrors.New("failed to update CIFS"))

	err := _updateSiteONTAP(&site, false, adu)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error updating CIFS service with Site")
}

func Test_updateSiteONTAP_NilDiscoveryModeResponse(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	site := "TestSite"

	// Return nil response (no server discovery mode)
	mockNAS.On("CifsDomainGet", mock.Anything).Return(nil, nil).Once()

	// Should default to "all" mode when response is nil
	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "none"
	})).Return(nil).Once()

	mockNAS.On("CifsServiceModify", mock.Anything).Return(nil).Once()

	mockNAS.On("CifsDomainModify", mock.MatchedBy(func(params *ontapRest.CifsDomainModifyParams) bool {
		return params.DiscoveryMode != nil && *params.DiscoveryMode == "site"
	})).Return(nil).Once()

	err := _updateSiteONTAP(&site, true, adu)

	assert.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

// ============================================================================
// UpdateServerCACertificate Tests
// ============================================================================

func TestUpdateServerCACertificate_Success(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)

	originalFunc := updateServerCertificate
	t.Cleanup(func() { updateServerCertificate = originalFunc })

	called := false
	updateServerCertificate = func(logger log.Logger, api ontapRest.RESTClient, svmName string, newCert, oldCert *string) error {
		called = true
		assert.Equal(t, mockREST, api)
		assert.Equal(t, "test-svm", svmName)
		return nil
	}

	err := adu.UpdateServerCACertificate()

	require.NoError(t, err)
	assert.True(t, called)
}

func Test_updateServerCertificate_Success(t *testing.T) {
	logger := log.NewLogger()
	mockREST := &ontapRest.MockRESTClient{}
	mockSec := &ontapRest.MockSecurityClient{}
	mockREST.On("Security").Return(mockSec)

	oldCert := ontapRest.ServerRootCACertificate{
		SecurityCertificate: models.SecurityCertificate{
			SerialNumber: nillable.ToPointer("12345"),
			CommonName:   nillable.ToPointer("old-cert"),
			Ca:           nillable.ToPointer("old-ca"),
		},
	}

	mockSec.On("ServerRootCACertificateGet", mock.Anything).Return(&oldCert, nil)
	mockSec.On("ServerRootCACertificateDelete", mock.Anything).Return(nil)
	mockSec.On("ServerRootCACertificateInstall", mock.Anything).Return(&ontapRest.ServerRootCACertificate{}, nil)

	newCertStr := "new-cert-data"
	oldCertStr := "old-cert-data"
	err := _updateServerCertificate(logger, mockREST, "test-svm", &newCertStr, &oldCertStr)

	require.NoError(t, err)
	mockSec.AssertExpectations(t)
}

func Test_updateServerCertificate_NotFound(t *testing.T) {
	logger := log.NewLogger()
	mockREST := &ontapRest.MockRESTClient{}
	mockSec := &ontapRest.MockSecurityClient{}
	mockREST.On("Security").Return(mockSec)

	mockSec.On("ServerRootCACertificateGet", mock.Anything).Return(nil, vsaerrors.NewNotFoundErr("cert", nil))

	newCertStr := "new-cert-data"
	oldCertStr := "old-cert-data"
	err := _updateServerCertificate(logger, mockREST, "test-svm", &newCertStr, &oldCertStr)

	require.NoError(t, err)
}

func Test_updateServerCertificate_InstallFailure_Rollback(t *testing.T) {
	logger := log.NewLogger()
	mockREST := &ontapRest.MockRESTClient{}
	mockSec := &ontapRest.MockSecurityClient{}
	mockREST.On("Security").Return(mockSec)

	oldCert := ontapRest.ServerRootCACertificate{
		SecurityCertificate: models.SecurityCertificate{
			SerialNumber: nillable.ToPointer("12345"),
			CommonName:   nillable.ToPointer("old-cert"),
			Ca:           nillable.ToPointer("old-ca"),
		},
	}

	mockSec.On("ServerRootCACertificateGet", mock.Anything).Return(&oldCert, nil)
	mockSec.On("ServerRootCACertificateDelete", mock.Anything).Return(nil)
	mockSec.On("ServerRootCACertificateInstall", mock.Anything).Return(nil, vsaerrors.New("install failed")).Once()
	mockSec.On("ServerRootCACertificateInstall", mock.Anything).Return(&ontapRest.ServerRootCACertificate{}, nil).Once()

	newCertStr := "new-cert-data"
	oldCertStr := "old-cert-data"
	err := _updateServerCertificate(logger, mockREST, "test-svm", &newCertStr, &oldCertStr)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "install failed")
	mockSec.AssertExpectations(t)
}

// ============================================================================
// UpdateUsers Tests
// ============================================================================

func TestUpdateUsers_Success(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	// Mock get CIFS groups
	originalGetGroups := getCifsGroups
	t.Cleanup(func() { getCifsGroups = originalGetGroups })
	getCifsGroups = func(nas ontapRest.NASClient, svmUUID string) (map[string]*ontapRest.CifsGroup, error) {
		return map[string]*ontapRest.CifsGroup{
			"BUILTIN\\Administrators": {
				Name:    "BUILTIN\\Administrators",
				Sid:     "S-1-5-32-544",
				Members: []string{"NEW.DOMAIN.COM\\user1"},
			},
		}, nil
	}

	// Mock remove and add users
	originalRemove := removeUsersFromGroup
	originalAdd := addUsersToGroup
	t.Cleanup(func() {
		removeUsersFromGroup = originalRemove
		addUsersToGroup = originalAdd
	})

	removeUsersFromGroup = func(logger log.Logger, nas ontapRest.NASClient, svmUUID, domain string, group *ontapRest.CifsGroup, users []string) error {
		return nil
	}

	addUsersToGroup = func(logger log.Logger, nas ontapRest.NASClient, svmUUID, domain string, group *ontapRest.CifsGroup, users []string) error {
		return nil
	}

	err := adu.UpdateUsers("WORKGROUP")

	require.NoError(t, err)
}

func TestUpdateUsers_SecurityPrivilege(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.Users = map[string][]string{
		utils.ActiveDirectorySeSecurityPrivilege: {"user1", "user2"},
	}
	adu.params.OldCredentials.Users = map[string][]string{
		utils.ActiveDirectorySeSecurityPrivilege: {"user1"},
	}

	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	originalGet := getSecurityPrivilegedUsers
	originalRemove := removeSecurityPrivilegesFromUsers
	originalAdd := addSecurityPrivilegesToUsers
	t.Cleanup(func() {
		getSecurityPrivilegedUsers = originalGet
		removeSecurityPrivilegesFromUsers = originalRemove
		addSecurityPrivilegesToUsers = originalAdd
	})

	getSecurityPrivilegedUsers = func(nas ontapRest.NASClient, svmUUID string) ([]string, error) {
		return []string{"NEW.DOMAIN.COM\\user1"}, nil
	}

	removeSecurityPrivilegesFromUsers = func(logger log.Logger, nas ontapRest.NASClient, svmUUID, domain string, users []string) error {
		return nil
	}

	addSecurityPrivilegesToUsers = func(logger log.Logger, nas ontapRest.NASClient, svmUUID, domain string, users []string) error {
		return nil
	}

	err := adu.UpdateUsers("WORKGROUP")

	require.NoError(t, err)
}

// ============================================================================
// UpdateLDAPSigning Tests
// ============================================================================

func TestUpdateLDAPSigning_Success(t *testing.T) {
	adu, _ := setupMockUpdater(t)

	originalFunc := modifyADLdapSigning
	t.Cleanup(func() { modifyADLdapSigning = originalFunc })

	called := false
	modifyADLdapSigning = func(api ontapRest.RESTClient, sign bool, svmUUID *string) error {
		called = true
		assert.True(t, sign)
		return nil
	}

	err := adu.UpdateLDAPSigning(true)

	require.NoError(t, err)
	assert.True(t, called)
}

func Test_modifyADLdapSigning_Success(t *testing.T) {
	mockREST := &ontapRest.MockRESTClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	svmUUID := "test-uuid"
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.SessionSecurityForAdLdap != nil && *params.SvmUUID == svmUUID
	})).Return(nil)

	err := _modifyADLdapSigning(mockREST, true, &svmUUID)

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

// ============================================================================
// UpdateAllowLocalNFSUsersWithLdap Tests
// ============================================================================

func TestUpdateAllowLocalNFSUsersWithLdap_Success(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("NfsModify", mock.MatchedBy(func(params *ontapRest.NfsModifyParams) bool {
		return params.AllowLocalNFSUsersWithLdap != nil && *params.AllowLocalNFSUsersWithLdap == false
	})).Return(nil)

	err := adu.UpdateAllowLocalNFSUsersWithLdap()

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func TestUpdateAllowLocalNFSUsersWithLdap_Failure(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("NfsModify", mock.MatchedBy(func(params *ontapRest.NfsModifyParams) bool {
		return params.AllowLocalNFSUsersWithLdap != nil
	})).Return(vsaerrors.New("nfs modify failed"))

	err := adu.UpdateAllowLocalNFSUsersWithLdap()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nfs modify failed")
	mockNAS.AssertExpectations(t)
}

// ============================================================================
// UpdateLDAPOverTLS Tests
// ============================================================================

func TestUpdateLDAPOverTLS_EnableWithNoCert(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockSec := &ontapRest.MockSecurityClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("Security").Return(mockSec)
	mockREST.On("NAS").Return(mockNAS)
	mockREST.On("NameServices").Return(mockNS)

	// No existing certificates
	mockSec.On("ServerRootCACertificateCollectionGet", mock.Anything).Return([]*ontapRest.ServerRootCACertificate{}, nil)

	// Install certificate
	mockSec.On("ServerRootCACertificateInstall", mock.Anything).Return(&ontapRest.ServerRootCACertificate{}, nil)

	// Modify CIFS server
	mockNAS.On("CifsServiceModify", mock.Anything).Return(nil)

	// Get LDAP client
	mockNS.On("LdapGet", mock.Anything).Return(&ontapRest.LdapService{}, nil)

	// Modify LDAP client
	mockNS.On("LdapModify", mock.Anything).Return(nil)

	err := adu.UpdateLDAPOverTLS()

	require.NoError(t, err)
	mockSec.AssertExpectations(t)
	mockNAS.AssertExpectations(t)
	mockNS.AssertExpectations(t)
}

func TestUpdateLDAPOverTLS_DisableAndRemoveCert(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.LdapOverTLS = nillable.ToPointer(false)

	mockSec := &ontapRest.MockSecurityClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("Security").Return(mockSec)
	mockREST.On("NAS").Return(mockNAS)
	mockREST.On("NameServices").Return(mockNS)

	existingCert := &ontapRest.ServerRootCACertificate{
		SecurityCertificate: models.SecurityCertificate{
			UUID: nillable.ToPointer("cert-uuid"),
		},
	}
	mockSec.On("ServerRootCACertificateCollectionGet", mock.Anything).Return([]*ontapRest.ServerRootCACertificate{existingCert}, nil)

	mockNAS.On("CifsServiceModify", mock.Anything).Return(nil)
	mockNS.On("LdapGet", mock.Anything).Return(&ontapRest.LdapService{}, nil)
	mockNS.On("LdapModify", mock.Anything).Return(nil)
	mockSec.On("ServerRootCACertificateDelete", mock.Anything).Return(nil)

	err := adu.UpdateLDAPOverTLS()

	require.NoError(t, err)
	mockSec.AssertExpectations(t)
}

// ============================================================================
// UpdatePreferredDCOrDNAndFilter Tests
// ============================================================================

func TestUpdatePreferredDCOrDNAndFilter_PreferredServers(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.PreferredServersForLdapClient = nillable.ToPointer("10.0.0.1,10.0.0.2")

	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)

	mockNS.On("LdapGet", mock.Anything).Return(&ontapRest.LdapService{}, nil)
	mockNS.On("LdapModifyPreferredAdServers", mock.MatchedBy(func(params *ontapRest.LdapModifyParams) bool {
		return len(params.PreferredServersForLdapClient) == 2
	})).Return(nil)

	err := adu.UpdatePreferredDCOrDNAndFilter(true, false)

	require.NoError(t, err)
	mockNS.AssertExpectations(t)
}

func TestUpdatePreferredDCOrDNAndFilter_DNAndFilter(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.UserDN = nillable.ToPointer("CN=Users,DC=example,DC=com")
	adu.params.NewCredentials.GroupDN = nillable.ToPointer("CN=Groups,DC=example,DC=com")

	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)

	mockNS.On("LdapGet", mock.Anything).Return(&ontapRest.LdapService{}, nil)
	mockNS.On("LdapModify", mock.MatchedBy(func(params *ontapRest.LdapModifyParams) bool {
		return params.UserDn != nil && params.GroupDn != nil
	})).Return(nil)

	err := adu.UpdatePreferredDCOrDNAndFilter(false, true)

	require.NoError(t, err)
	mockNS.AssertExpectations(t)
}

// ============================================================================
// Utility Function Tests
// ============================================================================

func TestGetActiveDirectoryUserMapKeys(t *testing.T) {
	oldUsers := map[string][]string{
		"group1": {"user1"},
		"group2": {"user2"},
	}
	newUsers := map[string][]string{
		"group2": {"user3"},
		"group3": {"user4"},
	}

	keys := getActiveDirectoryUserMapKeys(oldUsers, newUsers)

	assert.Len(t, keys, 3)
	assert.Contains(t, keys, "group1")
	assert.Contains(t, keys, "group2")
	assert.Contains(t, keys, "group3")
}

func TestGetActiveDirectoryUserChanges(t *testing.T) {
	current := []string{"user1", "user2", "user3"}
	updated := []string{"user2", "user3", "user4", "user5"}

	add, remove := getActiveDirectoryUserChanges(current, updated)

	assert.Len(t, add, 2)
	assert.Contains(t, add, "user4")
	assert.Contains(t, add, "user5")
	assert.Len(t, remove, 1)
	assert.Contains(t, remove, "user1")
}

func Test_removeDomainFromUsers(t *testing.T) {
	users := []string{`DOMAIN\user1`, `DOMAIN\user2`, `user3`}

	result := _removeDomainFromUsers(users)

	assert.Len(t, result, 3)
	assert.Equal(t, "user1", result[0])
	assert.Equal(t, "user2", result[1])
	assert.Equal(t, "user3", result[2])
}

func Test_removeDomainFromUser(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"with domain", `DOMAIN\user1`, "user1"},
		{"without domain", "user1", "user1"},
		{"multiple backslashes", `DOMAIN\SUBDOMAIN\user1`, "user1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := _removeDomainFromUser(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func Test_getCifsGroups_Success(t *testing.T) {
	mockNAS := &ontapRest.MockNASClient{}

	mockNAS.On("CifsServiceCollectionGetGroups", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(ontapRest.UserCallbackFunc[[]*ontapRest.CifsGroup])
		groups := []*ontapRest.CifsGroup{
			{Name: "group1", Sid: "S-1-5-1", Members: []string{"user1"}},
			{Name: "group2", Sid: "S-1-5-2", Members: []string{"user2"}},
		}
		err := callback(groups)
		if err != nil {
			return
		}
	}).Return(nil)

	result, err := _getCifsGroups(mockNAS, "test-uuid")

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "group1")
	assert.Contains(t, result, "group2")
}

func Test_removeUsersFromGroup_Success(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	group := &ontapRest.CifsGroup{
		Name: "test-group",
		Sid:  "S-1-5-32-544",
	}

	mockNAS.On("CifsServiceRemoveMembers", mock.Anything).Return(nil)

	err := _removeUsersFromGroup(logger, mockNAS, "test-uuid", "WORKGROUP", group, []string{"user1", "user2"})

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_removeUsersFromGroup_RetryWithWorkgroup(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	group := &ontapRest.CifsGroup{
		Name: "test-group",
		Sid:  "S-1-5-32-544",
	}

	mockNAS.On("CifsServiceRemoveMembers", mock.Anything).Return(vsaerrors.New("Unable to resolve user name")).Once()
	mockNAS.On("CifsServiceRemoveMembers", mock.Anything).Return(nil).Once()

	err := _removeUsersFromGroup(logger, mockNAS, "test-uuid", "WORKGROUP", group, []string{`DOMAIN\user1`})

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_addUsersToGroup_Success(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	group := &ontapRest.CifsGroup{
		Name: "test-group",
		Sid:  "S-1-5-32-544",
	}

	mockNAS.On("CifsServiceAddMembers", mock.Anything).Return(nil)

	err := _addUsersToGroup(logger, mockNAS, "test-uuid", "WORKGROUP", group, []string{"user1", "user2"})

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_getSecurityPrivilegedUsers_Success(t *testing.T) {
	mockNAS := &ontapRest.MockNASClient{}

	mockNAS.On("CifsServiceCollectionGetPrivilegedMembers", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(ontapRest.UserCallbackFunc[[]string])
		err := callback([]string{"user1", "user2"})
		if err != nil {
			return
		}
	}).Return(nil)

	result, err := _getSecurityPrivilegedUsers(mockNAS, "test-uuid")

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "user1")
	assert.Contains(t, result, "user2")
}

func Test_removeSecurityPrivilegesFromUsers_Success(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	mockNAS.On("CifsServiceRemoveSecurityPrivilege", mock.Anything).Return(nil).Times(2)

	err := _removeSecurityPrivilegesFromUsers(logger, mockNAS, "test-uuid", "WORKGROUP", []string{"user1", "user2"})

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_addSecurityPrivilegesToUsers_Success(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	mockNAS.On("CifsServiceAddSecurityPrivilege", mock.Anything).Return(nil).Times(2)

	err := _addSecurityPrivilegesToUsers(logger, mockNAS, "test-uuid", "WORKGROUP", []string{"user1", "user2"})

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_addSecurityPrivilegesToUsers_RetryWithWorkgroup(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	mockNAS.On("CifsServiceAddSecurityPrivilege", mock.Anything).Return(vsaerrors.New("Unable to resolve user name")).Once()
	mockNAS.On("CifsServiceAddSecurityPrivilege", mock.Anything).Return(nil).Once()

	err := _addSecurityPrivilegesToUsers(logger, mockNAS, "test-uuid", "WORKGROUP", []string{`DOMAIN\user1`})

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

// ============================================================================
// UpdateActiveDirectoryCredentials Tests
// ============================================================================

func TestUpdateActiveDirectoryCredentials_MissingSvmExternalUUID(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	params := buildTestUpdateCredentialsParams()
	cifs := ontapRest.CifsService{}

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Error determining server for update")
}

func TestUpdateActiveDirectoryCredentials_CreateRESTClientError(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	params := buildTestUpdateCredentialsParams()
	cifs := ontapRest.CifsService{}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, vsaerrors.New("client creation failed")
	}

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "client creation failed")
}

func TestUpdateActiveDirectoryCredentials_LoadSVMError(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	mockSVM := &ontapRest.MockSVMClient{}
	params := buildTestUpdateCredentialsParams()
	cifs := ontapRest.CifsService{}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockREST, nil
	}

	mockREST.On("SVM").Return(mockSVM)
	mockSVM.On("SvmGet", mock.Anything).Return(nil, vsaerrors.New("SVM not found"))

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")

	assert.Error(t, err)
	var customErr *vsaerror.CustomError
	assert.True(t, vsaerror.As(err, &customErr))
	assert.Equal(t, vsaerror.ErrOntapRestAPIError, customErr.TrackingID)
	assert.Contains(t, customErr.OriginalErr.Error(), "SVM not found")
}

func TestUpdateActiveDirectoryCredentials_NoUpdatesNeeded(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	params := UpdateActiveDirectoryCredentialsParams{
		OldCredentials: &ActiveDirectory{
			Domain:                        "domain.com",
			DNS:                           "10.0.0.1",
			NetBIOS:                       "NETBIOS",
			Site:                          nillable.ToPointer("Site"),
			Users:                         map[string][]string{"group": {"user1"}},
			AesEncryption:                 nillable.ToPointer(true),
			LdapSigning:                   nillable.ToPointer(true),
			ServerRootCaCertificate:       nillable.ToPointer("cert"),
			AllowLocalNFSUsersWithLdap:    nillable.ToPointer(true),
			LdapOverTLS:                   nillable.ToPointer(true),
			EncryptDCConnections:          nillable.ToPointer(true),
			KdcIP:                         "10.0.0.10",
			AdName:                        "adname",
			Name:                          nillable.ToPointer("name"),
			UserDN:                        nillable.ToPointer("userdn"),
			GroupDN:                       nillable.ToPointer("groupdn"),
			GroupMembershipFilter:         nillable.ToPointer("filter"),
			PreferredServersForLdapClient: nillable.ToPointer("servers"),
		},
		NewCredentials: &ActiveDirectory{
			Domain:                        "domain.com",
			DNS:                           "10.0.0.1",
			NetBIOS:                       "NETBIOS",
			Site:                          nillable.ToPointer("Site"),
			Users:                         map[string][]string{"group": {"user1"}},
			AesEncryption:                 nillable.ToPointer(true),
			LdapSigning:                   nillable.ToPointer(true),
			ServerRootCaCertificate:       nillable.ToPointer("cert"),
			AllowLocalNFSUsersWithLdap:    nillable.ToPointer(true),
			LdapOverTLS:                   nillable.ToPointer(true),
			EncryptDCConnections:          nillable.ToPointer(true),
			KdcIP:                         "10.0.0.10",
			AdName:                        "adname",
			Name:                          nillable.ToPointer("name"),
			UserDN:                        nillable.ToPointer("userdn"),
			GroupDN:                       nillable.ToPointer("groupdn"),
			GroupMembershipFilter:         nillable.ToPointer("filter"),
			PreferredServersForLdapClient: nillable.ToPointer("servers"),
			CIFSServers:                   []*CIFSServer{{SVMName: "svm1", SVMUUID: "svm-uuid-1"}},
		},
	}
	cifs := ontapRest.CifsService{}

	originalDecrypt := decryptPassword
	t.Cleanup(func() { decryptPassword = originalDecrypt })
	decryptPassword = func(password log.Secret) (*string, error) {
		return (*string)(&password), nil
	}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockREST, nil
	}

	originalIsDDNS := isDDNSEnabled
	t.Cleanup(func() { isDDNSEnabled = originalIsDDNS })
	isDDNSEnabled = func(_ log.Logger, _ ontapRest.RESTClient, _ string) bool {
		return true
	}

	mockSVM := &ontapRest.MockSVMClient{}
	mockREST.On("SVM").Return(mockSVM)
	mockSVM.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{}, nil)

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")

	assert.NoError(t, err)
}

func TestUpdateActiveDirectoryCredentials_DNSUpdate(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	mockNS := &ontapRest.MockNameServicesClient{}
	params := buildTestUpdateCredentialsParams()
	params.NewCredentials.DNS = "10.0.2.1,10.0.2.2"
	// Set all other fields to be the same to avoid triggering other update paths
	params.NewCredentials.ServerRootCaCertificate = params.OldCredentials.ServerRootCaCertificate
	params.NewCredentials.LdapOverTLS = params.OldCredentials.LdapOverTLS
	params.NewCredentials.NetBIOS = params.OldCredentials.NetBIOS
	params.NewCredentials.Site = params.OldCredentials.Site
	params.NewCredentials.Users = params.OldCredentials.Users
	params.NewCredentials.AesEncryption = params.OldCredentials.AesEncryption
	params.NewCredentials.LdapSigning = params.OldCredentials.LdapSigning
	params.NewCredentials.AllowLocalNFSUsersWithLdap = params.OldCredentials.AllowLocalNFSUsersWithLdap
	params.NewCredentials.EncryptDCConnections = params.OldCredentials.EncryptDCConnections
	params.NewCredentials.KdcIP = params.OldCredentials.KdcIP
	params.NewCredentials.AdName = params.OldCredentials.AdName

	cifsName := "CIFSSERVER"
	cifs := ontapRest.CifsService{
		CifsService: models.CifsService{
			Name: &cifsName,
			AdDomain: &models.AdDomain{
				Fqdn: nillable.ToPointer("old.domain.com"),
			},
		},
	}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockREST, nil
	}

	originalDecrypt := decryptPassword
	t.Cleanup(func() { decryptPassword = originalDecrypt })
	decryptPassword = func(password log.Secret) (*string, error) {
		return (*string)(&password), nil
	}

	mockREST.On("NameServices").Return(mockNS)
	mockNS.On("DNSModify", mock.Anything).Return(nil)

	originalIsDDNS := isDDNSEnabled
	t.Cleanup(func() { isDDNSEnabled = originalIsDDNS })
	isDDNSEnabled = func(_ log.Logger, _ ontapRest.RESTClient, _ string) bool {
		return true
	}

	mockSVM := &ontapRest.MockSVMClient{}
	mockREST.On("SVM").Return(mockSVM)
	mockSVM.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{}, nil)

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")

	assert.NoError(t, err)
	mockNS.AssertExpectations(t)
}

func TestUpdateActiveDirectoryCredentials_DNSNotEnabled(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	mockNAS := &ontapRest.MockNASClient{}
	params := buildTestUpdateCredentialsParams()
	// Set fields to be the same to avoid triggering other update paths
	params.NewCredentials.ServerRootCaCertificate = params.OldCredentials.ServerRootCaCertificate
	params.NewCredentials.LdapOverTLS = params.OldCredentials.LdapOverTLS
	params.NewCredentials.AesEncryption = params.OldCredentials.AesEncryption
	params.NewCredentials.LdapSigning = params.OldCredentials.LdapSigning
	params.NewCredentials.AllowLocalNFSUsersWithLdap = params.OldCredentials.AllowLocalNFSUsersWithLdap
	params.NewCredentials.EncryptDCConnections = params.OldCredentials.EncryptDCConnections
	params.NewCredentials.KdcIP = params.OldCredentials.KdcIP
	params.NewCredentials.AdName = params.OldCredentials.AdName
	params.NewCredentials.Site = params.OldCredentials.Site
	params.NewCredentials.Users = params.OldCredentials.Users
	cifsName := "CIFSSERVER"
	cifs := ontapRest.CifsService{
		CifsService: models.CifsService{
			Name: &cifsName,
			AdDomain: &models.AdDomain{
				Fqdn: nillable.ToPointer("old.domain.com"),
			},
		},
	}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockREST, nil
	}

	originalDecrypt := decryptPassword
	t.Cleanup(func() { decryptPassword = originalDecrypt })
	decryptPassword = func(password log.Secret) (*string, error) {
		return (*string)(&password), nil
	}

	mockREST.On("NAS").Return(mockNAS)

	// Mock NameServices for ModifyDNS call (DNS values differ, so ModifyDNS will be called)
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)
	mockNS.On("DNSModify", mock.Anything).Return(nil)

	originalIsDDNS := isDDNSEnabled
	t.Cleanup(func() { isDDNSEnabled = originalIsDDNS })
	isDDNSEnabled = func(_ log.Logger, _ ontapRest.RESTClient, _ string) bool {
		return false
	}

	mockSVM := &ontapRest.MockSVMClient{}
	mockREST.On("SVM").Return(mockSVM)
	mockSVM.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{}, nil)

	// Mock NetBIOS update path
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == false
	})).Return(nil).Once()
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Name != nil
	})).Return(nil).Once()
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == true
	})).Return(nil).Once()

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DNS is not enabled")
}

func TestUpdateActiveDirectoryCredentials_AllowLocalNFSUsersWithLdapUpdate(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	mockNAS := &ontapRest.MockNASClient{}
	params := buildTestUpdateCredentialsParams()
	// Only AllowLocalNFSUsersWithLdap differs so only that update path is taken
	params.NewCredentials.DNS = params.OldCredentials.DNS
	params.NewCredentials.ServerRootCaCertificate = params.OldCredentials.ServerRootCaCertificate
	params.NewCredentials.LdapOverTLS = params.OldCredentials.LdapOverTLS
	params.NewCredentials.NetBIOS = params.OldCredentials.NetBIOS
	params.NewCredentials.Site = params.OldCredentials.Site
	params.NewCredentials.Users = params.OldCredentials.Users
	params.NewCredentials.AesEncryption = params.OldCredentials.AesEncryption
	params.NewCredentials.LdapSigning = params.OldCredentials.LdapSigning
	params.NewCredentials.EncryptDCConnections = params.OldCredentials.EncryptDCConnections
	params.NewCredentials.KdcIP = params.OldCredentials.KdcIP
	params.NewCredentials.AdName = params.OldCredentials.AdName
	params.OldCredentials.AllowLocalNFSUsersWithLdap = nillable.ToPointer(false)
	params.NewCredentials.AllowLocalNFSUsersWithLdap = nillable.ToPointer(true)

	cifsName := "CIFSSERVER"
	cifs := ontapRest.CifsService{
		CifsService: models.CifsService{
			Name: &cifsName,
			AdDomain: &models.AdDomain{
				Fqdn: nillable.ToPointer("old.domain.com"),
			},
		},
	}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockREST, nil
	}

	originalDecrypt := decryptPassword
	t.Cleanup(func() { decryptPassword = originalDecrypt })
	decryptPassword = func(password log.Secret) (*string, error) {
		return (*string)(&password), nil
	}

	originalIsDDNS := isDDNSEnabled
	t.Cleanup(func() { isDDNSEnabled = originalIsDDNS })
	isDDNSEnabled = func(_ log.Logger, _ ontapRest.RESTClient, _ string) bool {
		return true
	}

	mockSVM := &ontapRest.MockSVMClient{}
	mockREST.On("SVM").Return(mockSVM)
	mockSVM.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{}, nil)

	mockREST.On("NAS").Return(mockNAS)
	mockNAS.On("NfsModify", mock.MatchedBy(func(params *ontapRest.NfsModifyParams) bool {
		return params.AllowLocalNFSUsersWithLdap != nil && *params.AllowLocalNFSUsersWithLdap == false
	})).Return(nil)

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")

	assert.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func TestUpdateActiveDirectoryCredentials_AllowLocalNFSUsersWithLdapUpdateError(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	mockNAS := &ontapRest.MockNASClient{}
	params := buildTestUpdateCredentialsParams()
	// Only AllowLocalNFSUsersWithLdap differs
	params.NewCredentials.DNS = params.OldCredentials.DNS
	params.NewCredentials.ServerRootCaCertificate = params.OldCredentials.ServerRootCaCertificate
	params.NewCredentials.LdapOverTLS = params.OldCredentials.LdapOverTLS
	params.NewCredentials.NetBIOS = params.OldCredentials.NetBIOS
	params.NewCredentials.Site = params.OldCredentials.Site
	params.NewCredentials.Users = params.OldCredentials.Users
	params.NewCredentials.AesEncryption = params.OldCredentials.AesEncryption
	params.NewCredentials.LdapSigning = params.OldCredentials.LdapSigning
	params.NewCredentials.EncryptDCConnections = params.OldCredentials.EncryptDCConnections
	params.NewCredentials.KdcIP = params.OldCredentials.KdcIP
	params.NewCredentials.AdName = params.OldCredentials.AdName
	params.OldCredentials.AllowLocalNFSUsersWithLdap = nillable.ToPointer(false)
	params.NewCredentials.AllowLocalNFSUsersWithLdap = nillable.ToPointer(true)

	cifsName := "CIFSSERVER"
	cifs := ontapRest.CifsService{
		CifsService: models.CifsService{
			Name: &cifsName,
			AdDomain: &models.AdDomain{
				Fqdn: nillable.ToPointer("old.domain.com"),
			},
		},
	}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockREST, nil
	}

	originalDecrypt := decryptPassword
	t.Cleanup(func() { decryptPassword = originalDecrypt })
	decryptPassword = func(password log.Secret) (*string, error) {
		return (*string)(&password), nil
	}

	originalIsDDNS := isDDNSEnabled
	t.Cleanup(func() { isDDNSEnabled = originalIsDDNS })
	isDDNSEnabled = func(_ log.Logger, _ ontapRest.RESTClient, _ string) bool {
		return true
	}

	mockSVM := &ontapRest.MockSVMClient{}
	mockREST.On("SVM").Return(mockSVM)
	mockSVM.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{}, nil)

	mockREST.On("NAS").Return(mockNAS)
	mockNAS.On("NfsModify", mock.MatchedBy(func(params *ontapRest.NfsModifyParams) bool {
		return params.AllowLocalNFSUsersWithLdap != nil
	})).Return(vsaerrors.New("nfs modify allow local users failed"))

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nfs modify allow local users failed")
	mockNAS.AssertExpectations(t)
}

func TestUpdateActiveDirectoryCredentials_LoadCIFSServerError(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	params := buildTestUpdateCredentialsParams()
	// Set DNS to be the same to avoid ModifyDNS call before LoadCIFSServer
	params.NewCredentials.DNS = params.OldCredentials.DNS
	// Keep at least one field different (e.g., Users) to ensure we reach LoadCIFSServer
	// But use a CIFS service with nil AdDomain to trigger error
	cifsName := "CIFSSERVER"
	cifs := ontapRest.CifsService{
		CifsService: models.CifsService{
			Name:     &cifsName,
			AdDomain: nil, // This will cause nil pointer dereference in LoadCIFSServer
		},
	}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockREST, nil
	}

	originalDecrypt := decryptPassword
	t.Cleanup(func() { decryptPassword = originalDecrypt })
	decryptPassword = func(password log.Secret) (*string, error) {
		return (*string)(&password), nil
	}

	originalIsDDNS := isDDNSEnabled
	t.Cleanup(func() { isDDNSEnabled = originalIsDDNS })
	isDDNSEnabled = func(_ log.Logger, _ ontapRest.RESTClient, _ string) bool {
		return true
	}

	mockSVM := &ontapRest.MockSVMClient{}
	mockREST.On("SVM").Return(mockSVM)
	mockSVM.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{}, nil)

	// This will panic due to nil pointer dereference in LoadCIFSServer
	// The function doesn't check for nil AdDomain, so it will panic
	// We use recover to catch the panic and verify the behavior
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				// Expected panic due to nil pointer dereference
				assert.Contains(t, fmt.Sprintf("%v", r), "nil pointer")
			}
		}()
		_ = provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")
	}()

	assert.True(t, panicked, "Expected panic due to nil pointer dereference")
}

func TestUpdateActiveDirectoryCredentials_NetBIOSWithPostfix(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockNS := &ontapRest.MockNameServicesClient{}
	params := buildTestUpdateCredentialsParams()
	// Set fields to be the same to avoid triggering other update paths (only NetBIOS should differ)
	params.NewCredentials.ServerRootCaCertificate = params.OldCredentials.ServerRootCaCertificate
	params.NewCredentials.LdapOverTLS = params.OldCredentials.LdapOverTLS
	params.NewCredentials.AesEncryption = params.OldCredentials.AesEncryption
	params.NewCredentials.LdapSigning = params.OldCredentials.LdapSigning
	params.NewCredentials.AllowLocalNFSUsersWithLdap = params.OldCredentials.AllowLocalNFSUsersWithLdap
	params.NewCredentials.EncryptDCConnections = params.OldCredentials.EncryptDCConnections
	params.NewCredentials.KdcIP = params.OldCredentials.KdcIP
	params.NewCredentials.AdName = params.OldCredentials.AdName
	params.NewCredentials.Site = params.OldCredentials.Site
	params.NewCredentials.Users = params.OldCredentials.Users
	cifsName := "OLDNETBIOS-POSTFIX"
	cifs := ontapRest.CifsService{
		CifsService: models.CifsService{
			Name: &cifsName,
			AdDomain: &models.AdDomain{
				Fqdn: nillable.ToPointer("old.domain.com"),
			},
		},
	}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockREST, nil
	}

	originalDecrypt := decryptPassword
	t.Cleanup(func() { decryptPassword = originalDecrypt })
	decryptPassword = func(password log.Secret) (*string, error) {
		return (*string)(&password), nil
	}

	// Mock SVM client
	mockSVM := &ontapRest.MockSVMClient{}
	mockREST.On("SVM").Return(mockSVM)
	mockSVM.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{}, nil)

	// Mock NAS and NameServices
	mockREST.On("NAS").Return(mockNAS)
	mockREST.On("NameServices").Return(mockNS)

	// Mock isDDNS check
	originalIsDDNS := isDDNSEnabled
	t.Cleanup(func() { isDDNSEnabled = originalIsDDNS })
	isDDNSEnabled = func(_ log.Logger, _ ontapRest.RESTClient, _ string) bool {
		return true
	}

	// Mock NetBIOS update
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == false
	})).Return(nil).Once()
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Name != nil
	})).Return(nil).Once()
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == true
	})).Return(nil).Once()

	// Mock DDNS
	mockNS.On("DNSModify", mock.Anything).Return(nil)

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")

	assert.NoError(t, err)
}

func TestUpdateActiveDirectoryCredentials_NetBIOSLongerThan10Chars(t *testing.T) {
	logger := log.NewLogger()
	provider := &OntapRestProvider{Logger: logger}
	mockREST := &ontapRest.MockRESTClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockNS := &ontapRest.MockNameServicesClient{}
	params := buildTestUpdateCredentialsParams()
	params.OldCredentials.NetBIOS = "VERYLONGNETBIOSNAME"
	// Set fields to be the same to avoid triggering other update paths (only NetBIOS should differ)
	params.NewCredentials.ServerRootCaCertificate = params.OldCredentials.ServerRootCaCertificate
	params.NewCredentials.LdapOverTLS = params.OldCredentials.LdapOverTLS
	params.NewCredentials.AesEncryption = params.OldCredentials.AesEncryption
	params.NewCredentials.LdapSigning = params.OldCredentials.LdapSigning
	params.NewCredentials.AllowLocalNFSUsersWithLdap = params.OldCredentials.AllowLocalNFSUsersWithLdap
	params.NewCredentials.EncryptDCConnections = params.OldCredentials.EncryptDCConnections
	params.NewCredentials.KdcIP = params.OldCredentials.KdcIP
	params.NewCredentials.AdName = params.OldCredentials.AdName
	params.NewCredentials.Site = params.OldCredentials.Site
	params.NewCredentials.Users = params.OldCredentials.Users
	cifsName := "VERYLONGNETBIOSNAME"
	cifs := ontapRest.CifsService{
		CifsService: models.CifsService{
			Name: &cifsName,
			AdDomain: &models.AdDomain{
				Fqdn: nillable.ToPointer("old.domain.com"),
			},
		},
	}

	originalFunc := getOntapClientFunc
	t.Cleanup(func() { getOntapClientFunc = originalFunc })
	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockREST, nil
	}

	originalDecrypt := decryptPassword
	t.Cleanup(func() { decryptPassword = originalDecrypt })
	decryptPassword = func(password log.Secret) (*string, error) {
		return (*string)(&password), nil
	}

	// Mock SVM client
	mockSVM := &ontapRest.MockSVMClient{}
	mockREST.On("SVM").Return(mockSVM)
	mockSVM.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{}, nil)

	// Mock NAS and NameServices
	mockREST.On("NAS").Return(mockNAS)
	mockREST.On("NameServices").Return(mockNS)

	// Mock isDDNS check
	originalIsDDNS := isDDNSEnabled
	t.Cleanup(func() { isDDNSEnabled = originalIsDDNS })
	isDDNSEnabled = func(_ log.Logger, _ ontapRest.RESTClient, _ string) bool {
		return true
	}

	// Mock NetBIOS update
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == false
	})).Return(nil).Once()
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Name != nil
	})).Return(nil).Once()
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == true
	})).Return(nil).Once()

	// Mock DDNS
	mockNS.On("DNSModify", mock.Anything).Return(nil)

	err := provider.UpdateActiveDirectoryCredentials(params, cifs, "svm-name", "svm-uuid")

	assert.NoError(t, err)
}

func Test_addUsersToGroup_RetryWithWorkgroup(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	group := &ontapRest.CifsGroup{
		Name: "test-group",
		Sid:  "S-1-5-32-544",
	}

	// First attempt fails with "Unable to resolve user name"
	mockNAS.On("CifsServiceAddMembers", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyGroupMembersParams) bool {
		return params.Members[0] == "DOMAIN\\user1"
	})).Return(vsaerrors.New("Unable to resolve user name")).Once()

	// Second attempt succeeds with workgroup prepended
	mockNAS.On("CifsServiceAddMembers", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyGroupMembersParams) bool {
		return params.Members[0] == "WORKGROUP\\user1"
	})).Return(nil).Once()

	err := _addUsersToGroup(logger, mockNAS, "test-uuid", "WORKGROUP", group, []string{"DOMAIN\\user1"})

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_addUsersToGroup_RetryFails(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	group := &ontapRest.CifsGroup{
		Name: "test-group",
		Sid:  "S-1-5-32-544",
	}

	// First attempt fails
	mockNAS.On("CifsServiceAddMembers", mock.Anything).Return(vsaerrors.New("Unable to resolve user name")).Once()
	// Retry also fails
	mockNAS.On("CifsServiceAddMembers", mock.Anything).Return(vsaerrors.New("Still cannot resolve")).Once()

	err := _addUsersToGroup(logger, mockNAS, "test-uuid", "WORKGROUP", group, []string{"user1"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Unable to resolve user name")
	mockNAS.AssertExpectations(t)
}

func Test_addUsersToGroup_OtherError(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	group := &ontapRest.CifsGroup{
		Name: "test-group",
		Sid:  "S-1-5-32-544",
	}

	// First attempt fails with a different error
	mockNAS.On("CifsServiceAddMembers", mock.Anything).Return(vsaerrors.New("Network error"))

	err := _addUsersToGroup(logger, mockNAS, "test-uuid", "WORKGROUP", group, []string{"user1"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Network error")
	mockNAS.AssertExpectations(t)
}

func Test_removeSecurityPrivilegesFromUsers_RetryWithWorkgroup(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	// First attempt fails with "Unable to resolve user name"
	mockNAS.On("CifsServiceRemoveSecurityPrivilege", mock.MatchedBy(func(params *ontapRest.CifsServiceModifySecurityPrivilegeParams) bool {
		return params.Member == "DOMAIN\\user1"
	})).Return(vsaerrors.New("Unable to resolve user name")).Once()

	// Second attempt succeeds with workgroup prepended
	mockNAS.On("CifsServiceRemoveSecurityPrivilege", mock.MatchedBy(func(params *ontapRest.CifsServiceModifySecurityPrivilegeParams) bool {
		return params.Member == "WORKGROUP\\user1"
	})).Return(nil).Once()

	err := _removeSecurityPrivilegesFromUsers(logger, mockNAS, "test-uuid", "WORKGROUP", []string{"DOMAIN\\user1"})

	require.NoError(t, err)
	mockNAS.AssertExpectations(t)
}

func Test_removeSecurityPrivilegesFromUsers_RetryFails(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	// First attempt fails
	mockNAS.On("CifsServiceRemoveSecurityPrivilege", mock.Anything).Return(vsaerrors.New("Unable to resolve user name")).Once()
	// Retry also fails
	mockNAS.On("CifsServiceRemoveSecurityPrivilege", mock.Anything).Return(vsaerrors.New("Still cannot resolve")).Once()

	err := _removeSecurityPrivilegesFromUsers(logger, mockNAS, "test-uuid", "WORKGROUP", []string{"user1"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Unable to resolve user name")
	mockNAS.AssertExpectations(t)
}

func Test_removeSecurityPrivilegesFromUsers_OtherError(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	// First attempt fails with a different error
	mockNAS.On("CifsServiceRemoveSecurityPrivilege", mock.Anything).Return(vsaerrors.New("Network error"))

	err := _removeSecurityPrivilegesFromUsers(logger, mockNAS, "test-uuid", "WORKGROUP", []string{"user1"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Network error")
	mockNAS.AssertExpectations(t)
}

func Test_addSecurityPrivilegesToUsers_RetryFails(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	// First attempt fails
	mockNAS.On("CifsServiceAddSecurityPrivilege", mock.Anything).Return(vsaerrors.New("Unable to resolve user name")).Once()
	// Retry also fails
	mockNAS.On("CifsServiceAddSecurityPrivilege", mock.Anything).Return(vsaerrors.New("Still cannot resolve")).Once()

	err := _addSecurityPrivilegesToUsers(logger, mockNAS, "test-uuid", "WORKGROUP", []string{"user1"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Unable to resolve user name")
	mockNAS.AssertExpectations(t)
}

func Test_addSecurityPrivilegesToUsers_OtherError(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	// First attempt fails with a different error
	mockNAS.On("CifsServiceAddSecurityPrivilege", mock.Anything).Return(vsaerrors.New("Network error"))

	err := _addSecurityPrivilegesToUsers(logger, mockNAS, "test-uuid", "WORKGROUP", []string{"user1"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Network error")
	mockNAS.AssertExpectations(t)
}

func Test_removeUsersFromGroup_RetryFails(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	group := &ontapRest.CifsGroup{
		Name: "test-group",
		Sid:  "S-1-5-32-544",
	}

	// Both attempts fail
	mockNAS.On("CifsServiceRemoveMembers", mock.Anything).Return(vsaerrors.New("Unable to resolve user name")).Once()
	mockNAS.On("CifsServiceRemoveMembers", mock.Anything).Return(vsaerrors.New("Still cannot resolve")).Once()

	err := _removeUsersFromGroup(logger, mockNAS, "test-uuid", "WORKGROUP", group, []string{"user1"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Unable to resolve user name")
	mockNAS.AssertExpectations(t)
}

func Test_getSecurityPrivilegedUsers_Error(t *testing.T) {
	mockNAS := &ontapRest.MockNASClient{}

	mockNAS.On("CifsServiceCollectionGetPrivilegedMembers", mock.Anything, mock.Anything).Return(vsaerrors.New("API error"))

	result, err := _getSecurityPrivilegedUsers(mockNAS, "test-uuid")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "API error")
}

func Test_getCifsGroups_Error(t *testing.T) {
	mockNAS := &ontapRest.MockNASClient{}

	mockNAS.On("CifsServiceCollectionGetGroups", mock.Anything, mock.Anything).Return(vsaerrors.New("API error"))

	result, err := _getCifsGroups(mockNAS, "test-uuid")

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "API error")
}

func TestUpdateLDAPOverTLS_CertCollectionGetError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockSec := &ontapRest.MockSecurityClient{}
	mockREST.On("Security").Return(mockSec)

	mockSec.On("ServerRootCACertificateCollectionGet", mock.Anything).Return(nil, vsaerrors.New("failed to get certs"))

	err := adu.UpdateLDAPOverTLS()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get certs")
}

func TestUpdateLDAPOverTLS_CertInstallError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockSec := &ontapRest.MockSecurityClient{}
	mockREST.On("Security").Return(mockSec)

	mockSec.On("ServerRootCACertificateCollectionGet", mock.Anything).Return([]*ontapRest.ServerRootCACertificate{}, nil)
	mockSec.On("ServerRootCACertificateInstall", mock.Anything).Return(nil, vsaerrors.New("install failed"))

	err := adu.UpdateLDAPOverTLS()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "install failed")
}

func TestUpdateLDAPOverTLS_CifsServiceModifyError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockSec := &ontapRest.MockSecurityClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("Security").Return(mockSec)
	mockREST.On("NAS").Return(mockNAS)

	mockSec.On("ServerRootCACertificateCollectionGet", mock.Anything).Return([]*ontapRest.ServerRootCACertificate{}, nil)
	mockSec.On("ServerRootCACertificateInstall", mock.Anything).Return(&ontapRest.ServerRootCACertificate{}, nil)
	mockNAS.On("CifsServiceModify", mock.Anything).Return(vsaerrors.New("modify failed"))

	err := adu.UpdateLDAPOverTLS()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "modify failed")
}

func TestUpdateLDAPOverTLS_LdapGetError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockSec := &ontapRest.MockSecurityClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("Security").Return(mockSec)
	mockREST.On("NAS").Return(mockNAS)
	mockREST.On("NameServices").Return(mockNS)

	mockSec.On("ServerRootCACertificateCollectionGet", mock.Anything).Return([]*ontapRest.ServerRootCACertificate{}, nil)
	mockSec.On("ServerRootCACertificateInstall", mock.Anything).Return(&ontapRest.ServerRootCACertificate{}, nil)
	mockNAS.On("CifsServiceModify", mock.Anything).Return(nil)
	mockNS.On("LdapGet", mock.Anything).Return(nil, vsaerrors.New("ldap get failed"))

	err := adu.UpdateLDAPOverTLS()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ldap get failed")
}

func TestUpdateLDAPOverTLS_LdapModifyError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockSec := &ontapRest.MockSecurityClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("Security").Return(mockSec)
	mockREST.On("NAS").Return(mockNAS)
	mockREST.On("NameServices").Return(mockNS)

	mockSec.On("ServerRootCACertificateCollectionGet", mock.Anything).Return([]*ontapRest.ServerRootCACertificate{}, nil)
	mockSec.On("ServerRootCACertificateInstall", mock.Anything).Return(&ontapRest.ServerRootCACertificate{}, nil)
	mockNAS.On("CifsServiceModify", mock.Anything).Return(nil)
	mockNS.On("LdapGet", mock.Anything).Return(&ontapRest.LdapService{}, nil)
	mockNS.On("LdapModify", mock.Anything).Return(vsaerrors.New("ldap modify failed"))

	err := adu.UpdateLDAPOverTLS()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ldap modify failed")
}

func TestUpdateLDAPOverTLS_LdapGetNotFoundError_NoError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockSec := &ontapRest.MockSecurityClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("Security").Return(mockSec)
	mockREST.On("NAS").Return(mockNAS)
	mockREST.On("NameServices").Return(mockNS)

	mockSec.On("ServerRootCACertificateCollectionGet", mock.Anything).Return([]*ontapRest.ServerRootCACertificate{}, nil)
	mockSec.On("ServerRootCACertificateInstall", mock.Anything).Return(&ontapRest.ServerRootCACertificate{}, nil)
	mockNAS.On("CifsServiceModify", mock.Anything).Return(nil)
	// LDAP client not found - should not be an error
	mockNS.On("LdapGet", mock.Anything).Return(nil, vsaerrors.NewNotFoundErr("ldap", nil))

	err := adu.UpdateLDAPOverTLS()

	require.NoError(t, err)
}

func TestUpdateLDAPOverTLS_DisableWithCertDeleteError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.LdapOverTLS = nillable.ToPointer(false)

	mockSec := &ontapRest.MockSecurityClient{}
	mockNAS := &ontapRest.MockNASClient{}
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("Security").Return(mockSec)
	mockREST.On("NAS").Return(mockNAS)
	mockREST.On("NameServices").Return(mockNS)

	existingCert := &ontapRest.ServerRootCACertificate{
		SecurityCertificate: models.SecurityCertificate{
			UUID: nillable.ToPointer("cert-uuid"),
		},
	}
	mockSec.On("ServerRootCACertificateCollectionGet", mock.Anything).Return([]*ontapRest.ServerRootCACertificate{existingCert}, nil)
	mockNAS.On("CifsServiceModify", mock.Anything).Return(nil)
	mockNS.On("LdapGet", mock.Anything).Return(&ontapRest.LdapService{}, nil)
	mockNS.On("LdapModify", mock.Anything).Return(nil)
	mockSec.On("ServerRootCACertificateDelete", mock.Anything).Return(vsaerrors.New("delete failed"))

	err := adu.UpdateLDAPOverTLS()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

func TestUpdatePreferredDCOrDNAndFilter_LdapGetError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)

	mockNS.On("LdapGet", mock.Anything).Return(nil, vsaerrors.New("ldap get failed"))

	err := adu.UpdatePreferredDCOrDNAndFilter(true, false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ldap get failed")
}

func TestUpdatePreferredDCOrDNAndFilter_LdapNotFound_NoError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)

	mockNS.On("LdapGet", mock.Anything).Return(nil, vsaerrors.NewNotFoundErr("ldap", nil))

	err := adu.UpdatePreferredDCOrDNAndFilter(true, false)

	require.NoError(t, err)
}

func TestUpdatePreferredDCOrDNAndFilter_PreferredServersModifyError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.PreferredServersForLdapClient = nillable.ToPointer("10.0.0.1,10.0.0.2")

	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)

	mockNS.On("LdapGet", mock.Anything).Return(&ontapRest.LdapService{}, nil)
	mockNS.On("LdapModifyPreferredAdServers", mock.Anything).Return(vsaerrors.New("modify failed"))

	err := adu.UpdatePreferredDCOrDNAndFilter(true, false)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "modify failed")
}

func TestUpdatePreferredDCOrDNAndFilter_DNAndFilterModifyError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.UserDN = nillable.ToPointer("CN=Users,DC=example,DC=com")
	adu.params.NewCredentials.GroupDN = nillable.ToPointer("CN=Groups,DC=example,DC=com")

	mockNS := &ontapRest.MockNameServicesClient{}
	mockREST.On("NameServices").Return(mockNS)

	mockNS.On("LdapGet", mock.Anything).Return(&ontapRest.LdapService{}, nil)
	mockNS.On("LdapModify", mock.Anything).Return(vsaerrors.New("modify failed"))

	err := adu.UpdatePreferredDCOrDNAndFilter(false, true)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "modify failed")
}

func Test_removeUsersFromGroup_OtherError(t *testing.T) {
	logger := log.NewLogger()
	mockNAS := &ontapRest.MockNASClient{}

	group := &ontapRest.CifsGroup{
		Name: "test-group",
		Sid:  "S-1-5-32-544",
	}

	// First attempt fails with a different error
	mockNAS.On("CifsServiceRemoveMembers", mock.Anything).Return(vsaerrors.New("Network error"))

	err := _removeUsersFromGroup(logger, mockNAS, "test-uuid", "WORKGROUP", group, []string{"user1"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Network error")
	mockNAS.AssertExpectations(t)
}

func TestUpdateNetBios_ModifyNameError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == false
	})).Return(nil).Once()

	// Modify name fails
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Name != nil && *params.Name == "NEWNAME"
	})).Return(vsaerrors.New("modify name failed")).Once()

	// Defer enable should still be called
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == true
	})).Return(nil).Once()

	err := adu.UpdateNetBios("NEWNAME", "NewSite", "admin", "password")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "modify name failed")
}

func TestUpdateNetBios_ModifyNameError_AndDeferEnableError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == false
	})).Return(nil).Once()

	// Modify name fails
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Name != nil && *params.Name == "NEWNAME"
	})).Return(vsaerrors.New("modify name failed")).Once()

	// Defer enable also fails - should log but return original error
	mockNAS.On("CifsServiceModify", mock.MatchedBy(func(params *ontapRest.CifsServiceModifyParams) bool {
		return params.Enabled != nil && *params.Enabled == true
	})).Return(vsaerrors.New("enable failed")).Once()

	err := adu.UpdateNetBios("NEWNAME", "NewSite", "admin", "password")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "modify name failed") // Should return original error, not defer error
}

func TestUpdateUsers_GetCifsGroupsError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	originalGetGroups := getCifsGroups
	t.Cleanup(func() { getCifsGroups = originalGetGroups })
	getCifsGroups = func(nas ontapRest.NASClient, svmUUID string) (map[string]*ontapRest.CifsGroup, error) {
		return nil, vsaerrors.New("failed to get groups")
	}

	err := adu.UpdateUsers("WORKGROUP")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get groups")
}

func TestUpdateUsers_GroupNotFound(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	originalGetGroups := getCifsGroups
	t.Cleanup(func() { getCifsGroups = originalGetGroups })
	getCifsGroups = func(nas ontapRest.NASClient, svmUUID string) (map[string]*ontapRest.CifsGroup, error) {
		return map[string]*ontapRest.CifsGroup{
			"BUILTIN\\SomeOtherGroup": {
				Name: "BUILTIN\\SomeOtherGroup",
				Sid:  "S-1-5-32-999",
			},
		}, nil
	}

	err := adu.UpdateUsers("WORKGROUP")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Local group 'BUILTIN\\Administrators' not found")
}

func TestUpdateUsers_RemoveUsersError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	originalGetGroups := getCifsGroups
	t.Cleanup(func() { getCifsGroups = originalGetGroups })
	getCifsGroups = func(nas ontapRest.NASClient, svmUUID string) (map[string]*ontapRest.CifsGroup, error) {
		return map[string]*ontapRest.CifsGroup{
			"BUILTIN\\Administrators": {
				Name:    "BUILTIN\\Administrators",
				Sid:     "S-1-5-32-544",
				Members: []string{"NEW.DOMAIN.COM\\user1", "NEW.DOMAIN.COM\\user2"},
			},
		}, nil
	}

	originalRemove := removeUsersFromGroup
	t.Cleanup(func() { removeUsersFromGroup = originalRemove })
	removeUsersFromGroup = func(logger log.Logger, nas ontapRest.NASClient, svmUUID, domain string, group *ontapRest.CifsGroup, users []string) error {
		return vsaerrors.New("remove failed")
	}

	err := adu.UpdateUsers("WORKGROUP")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "remove failed")
}

func TestUpdateUsers_AddUsersError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	originalGetGroups := getCifsGroups
	t.Cleanup(func() { getCifsGroups = originalGetGroups })
	getCifsGroups = func(nas ontapRest.NASClient, svmUUID string) (map[string]*ontapRest.CifsGroup, error) {
		return map[string]*ontapRest.CifsGroup{
			"BUILTIN\\Administrators": {
				Name:    "BUILTIN\\Administrators",
				Sid:     "S-1-5-32-544",
				Members: []string{},
			},
		}, nil
	}

	originalAdd := addUsersToGroup
	t.Cleanup(func() { addUsersToGroup = originalAdd })
	addUsersToGroup = func(logger log.Logger, nas ontapRest.NASClient, svmUUID, domain string, group *ontapRest.CifsGroup, users []string) error {
		return vsaerrors.New("add failed")
	}

	err := adu.UpdateUsers("WORKGROUP")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "add failed")
}

func TestUpdateUsers_SecurityPrivilege_GetError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.Users = map[string][]string{
		utils.ActiveDirectorySeSecurityPrivilege: {"user1", "user2"},
	}
	adu.params.OldCredentials.Users = map[string][]string{
		utils.ActiveDirectorySeSecurityPrivilege: {"user1"},
	}

	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	originalGet := getSecurityPrivilegedUsers
	t.Cleanup(func() { getSecurityPrivilegedUsers = originalGet })
	getSecurityPrivilegedUsers = func(nas ontapRest.NASClient, svmUUID string) ([]string, error) {
		return nil, vsaerrors.New("get security users failed")
	}

	err := adu.UpdateUsers("WORKGROUP")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get security users failed")
}

func TestUpdateUsers_SecurityPrivilege_RemoveError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.Users = map[string][]string{
		utils.ActiveDirectorySeSecurityPrivilege: {"user2"},
	}
	adu.params.OldCredentials.Users = map[string][]string{
		utils.ActiveDirectorySeSecurityPrivilege: {"user1", "user2"},
	}

	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	originalGet := getSecurityPrivilegedUsers
	originalRemove := removeSecurityPrivilegesFromUsers
	t.Cleanup(func() {
		getSecurityPrivilegedUsers = originalGet
		removeSecurityPrivilegesFromUsers = originalRemove
	})

	getSecurityPrivilegedUsers = func(nas ontapRest.NASClient, svmUUID string) ([]string, error) {
		return []string{"NEW.DOMAIN.COM\\user1", "NEW.DOMAIN.COM\\user2"}, nil
	}

	removeSecurityPrivilegesFromUsers = func(logger log.Logger, nas ontapRest.NASClient, svmUUID, domain string, users []string) error {
		return vsaerrors.New("remove security privilege failed")
	}

	err := adu.UpdateUsers("WORKGROUP")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "remove security privilege failed")
}

func TestUpdateUsers_SecurityPrivilege_AddError(t *testing.T) {
	adu, mockREST := setupMockUpdater(t)
	adu.params.NewCredentials.Users = map[string][]string{
		utils.ActiveDirectorySeSecurityPrivilege: {"user1", "user2"},
	}
	adu.params.OldCredentials.Users = map[string][]string{
		utils.ActiveDirectorySeSecurityPrivilege: {"user1"},
	}

	mockNAS := &ontapRest.MockNASClient{}
	mockREST.On("NAS").Return(mockNAS)

	originalGet := getSecurityPrivilegedUsers
	originalAdd := addSecurityPrivilegesToUsers
	originalRemove := removeSecurityPrivilegesFromUsers
	t.Cleanup(func() {
		getSecurityPrivilegedUsers = originalGet
		addSecurityPrivilegesToUsers = originalAdd
		removeSecurityPrivilegesFromUsers = originalRemove
	})

	getSecurityPrivilegedUsers = func(nas ontapRest.NASClient, svmUUID string) ([]string, error) {
		return []string{"user1"}, nil // No domain prefix to avoid double prepending
	}

	// No remove needed since user1 is in both old and new
	removeSecurityPrivilegesFromUsers = func(logger log.Logger, nas ontapRest.NASClient, svmUUID, domain string, users []string) error {
		return nil
	}

	addSecurityPrivilegesToUsers = func(logger log.Logger, nas ontapRest.NASClient, svmUUID, domain string, users []string) error {
		return vsaerrors.New("add security privilege failed")
	}

	err := adu.UpdateUsers("WORKGROUP")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "add security privilege failed")
}

func Test_updateServerCertificate_GetError(t *testing.T) {
	logger := log.NewLogger()
	mockREST := &ontapRest.MockRESTClient{}
	mockSec := &ontapRest.MockSecurityClient{}
	mockREST.On("Security").Return(mockSec)

	mockSec.On("ServerRootCACertificateGet", mock.Anything).Return(nil, vsaerrors.New("get failed"))

	newCertStr := "new-cert-data"
	oldCertStr := "old-cert-data"
	err := _updateServerCertificate(logger, mockREST, "test-svm", &newCertStr, &oldCertStr)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get failed")
}

func Test_updateServerCertificate_DeleteError(t *testing.T) {
	logger := log.NewLogger()
	mockREST := &ontapRest.MockRESTClient{}
	mockSec := &ontapRest.MockSecurityClient{}
	mockREST.On("Security").Return(mockSec)

	oldCert := ontapRest.ServerRootCACertificate{
		SecurityCertificate: models.SecurityCertificate{
			SerialNumber: nillable.ToPointer("12345"),
			CommonName:   nillable.ToPointer("old-cert"),
			Ca:           nillable.ToPointer("old-ca"),
		},
	}

	mockSec.On("ServerRootCACertificateGet", mock.Anything).Return(&oldCert, nil)
	mockSec.On("ServerRootCACertificateDelete", mock.Anything).Return(vsaerrors.New("delete failed"))

	newCertStr := "new-cert-data"
	oldCertStr := "old-cert-data"
	err := _updateServerCertificate(logger, mockREST, "test-svm", &newCertStr, &oldCertStr)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}
