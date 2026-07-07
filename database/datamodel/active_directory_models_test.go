package datamodel

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActiveDirectory_ModelFields(t *testing.T) {
	ad := ActiveDirectory{
		AdName:         "test-ad",
		Username:       "admin",
		CredentialPath: "/path/to/creds",
		Domain:         "example.com",
		DNS:            "8.8.8.8",
		NetBIOS:        "EXAMPLE",
		State:          "active",
		StateDetails:   "running",
		AccountId:      123,
		ActiveDirectoryAttributes: &ActiveDirectoryAttributes{
			PrimaryAD:          true,
			OrganizationalUnit: "OU=Test,DC=example,DC=com",
		},
	}

	assert.Equal(t, "test-ad", ad.AdName)
	assert.Equal(t, "admin", ad.Username)
	assert.Equal(t, "/path/to/creds", ad.CredentialPath)
	assert.Equal(t, "example.com", ad.Domain)
	assert.Equal(t, "8.8.8.8", ad.DNS)
	assert.Equal(t, "EXAMPLE", ad.NetBIOS)
	assert.Equal(t, "active", ad.State)
	assert.Equal(t, "running", ad.StateDetails)
	assert.True(t, ad.ActiveDirectoryAttributes.PrimaryAD)
	assert.Equal(t, "OU=Test,DC=example,DC=com", ad.ActiveDirectoryAttributes.OrganizationalUnit)
	assert.Equal(t, int64(123), ad.AccountId)
}

func TestActiveDirectoryAttributes_Scan(t *testing.T) {
	t.Run("Valid JSON", func(t *testing.T) {
		var attrs ActiveDirectoryAttributes
		input := []byte(`{
			"primary_ad": true,
			"managed_ad": false,
			"organizational_unit": "OU=Users,DC=example,DC=com",
			"site": "site1",
			"account_id": "acc123",
			"svm_ids": ["svm1", "svm2"],
			"ad_users": {"group1": ["user1", "user2"]},
			"kdc_ip": "10.0.0.1",
			"user_dn": "cn=admin,dc=example,dc=com",
			"group_dn": "cn=group,dc=example,dc=com",
			"group_membership_filter": "filter",
			"aes_encryption": true,
			"encrypt_dc_connections": false,
			"server_root_ca_certificate": "cert",
			"ldap_signing": true,
			"allow_local_nfs_users_with_ldap": false,
			"ldap_over_tls": true,
			"preferred_servers_for_ldap_client": "ldap.example.com",
			"description": "desc"
		}`)
		err := attrs.Scan(input)
		assert.NoError(t, err)
		assert.Equal(t, "OU=Users,DC=example,DC=com", attrs.OrganizationalUnit)
		assert.Equal(t, []string{"svm1", "svm2"}, attrs.SvmIds)
		assert.True(t, attrs.PrimaryAD)
		assert.False(t, attrs.ManagedAD)
		assert.Equal(t, "site1", attrs.Site)
		assert.Equal(t, map[string][]string{"group1": {"user1", "user2"}}, attrs.AdUsers)
		assert.Equal(t, "10.0.0.1", attrs.KdcIP)
		assert.Equal(t, "cn=admin,dc=example,dc=com", attrs.UserDN)
		assert.Equal(t, "cn=group,dc=example,dc=com", attrs.GroupDN)
		assert.Equal(t, "filter", attrs.GroupMembershipFilter)
		assert.True(t, attrs.AesEncryption)
		assert.False(t, attrs.EncryptDCConnections)
		assert.Equal(t, "cert", attrs.ServerRootCaCertificate)
		assert.True(t, attrs.LdapSigning)
		assert.False(t, attrs.AllowLocalNFSUsersWithLdap)
		assert.True(t, attrs.LdapOverTLS)
		assert.Equal(t, "ldap.example.com", attrs.PreferredServersForLdapClient)
		assert.Equal(t, "desc", attrs.Description)
	})

	t.Run("Nil Value", func(t *testing.T) {
		var attrs ActiveDirectoryAttributes
		err := attrs.Scan(nil)
		assert.NoError(t, err)
		assert.Equal(t, ActiveDirectoryAttributes{}, attrs)
	})

	t.Run("Invalid Type", func(t *testing.T) {
		var attrs ActiveDirectoryAttributes
		err := attrs.Scan(123)
		assert.Error(t, err)
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		var attrs ActiveDirectoryAttributes
		err := attrs.Scan([]byte(`invalid json`))
		assert.Error(t, err)
	})
}

func TestIsAllowedADRollbackState(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  bool
	}{
		{name: "READY allowed", state: LifeCycleStateREADY, want: true},
		{name: "IN_USE allowed", state: LifeCycleStateInUse, want: true},
		{name: "ERROR allowed", state: LifeCycleStateError, want: true},
		{name: "DELETING rejected", state: LifeCycleStateDeleting, want: false},
		{name: "DELETED rejected", state: LifeCycleStateDeleted, want: false},
		{name: "CREATING rejected", state: LifeCycleStateCreating, want: false},
		{name: "empty rejected", state: "", want: false},
		{name: "unknown rejected", state: "BOGUS", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsAllowedADRollbackState(tt.state))
		})
	}
}

func TestActiveDirectoryAttributes_Value(t *testing.T) {
	attrs := ActiveDirectoryAttributes{
		PrimaryAD:                     true,
		ManagedAD:                     false,
		OrganizationalUnit:            "OU=Users,DC=example,DC=com",
		Site:                          "site1",
		SvmIds:                        []string{"svm1", "svm2"},
		AdUsers:                       map[string][]string{"group1": {"user1", "user2"}},
		KdcIP:                         "10.0.0.1",
		UserDN:                        "cn=admin,dc=example,dc=com",
		GroupDN:                       "cn=group,dc=example,dc=com",
		GroupMembershipFilter:         "filter",
		AesEncryption:                 true,
		EncryptDCConnections:          false,
		ServerRootCaCertificate:       "cert",
		LdapSigning:                   true,
		AllowLocalNFSUsersWithLdap:    false,
		LdapOverTLS:                   true,
		PreferredServersForLdapClient: "ldap.example.com",
		Description:                   "desc",
	}
	val, err := attrs.Value()
	assert.NoError(t, err)
	b, ok := val.([]byte)
	assert.True(t, ok)
	var out map[string]interface{}
	assert.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, "OU=Users,DC=example,DC=com", out["organizational_unit"])
	assert.Equal(t, true, out["primary_ad"])
	assert.Equal(t, false, out["managed_ad"])
	assert.Equal(t, "site1", out["site"])
	assert.Equal(t, []interface{}{"svm1", "svm2"}, out["svm_ids"])
	assert.Equal(t, map[string]interface{}{"group1": []interface{}{"user1", "user2"}}, out["ad_users"])
	assert.Equal(t, "10.0.0.1", out["kdc_ip"])
	assert.Equal(t, "cn=admin,dc=example,dc=com", out["user_dn"])
	assert.Equal(t, "cn=group,dc=example,dc=com", out["group_dn"])
	assert.Equal(t, "filter", out["group_membership_filter"])
	assert.Equal(t, true, out["aes_encryption"])
	assert.Equal(t, false, out["encrypt_dc_connections"])
	assert.Equal(t, "cert", out["server_root_ca_certificate"])
	assert.Equal(t, true, out["ldap_signing"])
	assert.Equal(t, false, out["allow_local_nfs_users_with_ldap"])
	assert.Equal(t, true, out["ldap_over_tls"])
	assert.Equal(t, "ldap.example.com", out["preferred_servers_for_ldap_client"])
	assert.Equal(t, "desc", out["description"])
}
