package ontap_rest

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/networking"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestNetworkIPInterfaceModify(t *testing.T) {
	uuid := "lif-uuid"
	policy := "default-data-files"

	t.Run("error from transport", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("modify failed")}
		client := &networkingClient{api: networking.New(transport, nil)}
		err := client.NetworkIPInterfaceModify(&NetworkIPInterfaceModifyParams{UUID: &uuid, ServicePolicyName: &policy})
		assert.EqualError(tt, err, "modify failed")
	})

	t.Run("success with service policy", func(tt *testing.T) {
		transport := &mockTransport{response: &networking.NetworkIPInterfaceModifyOK{}}
		client := &networkingClient{api: networking.New(transport, nil)}
		err := client.NetworkIPInterfaceModify(&NetworkIPInterfaceModifyParams{UUID: &uuid, ServicePolicyName: &policy})
		assert.NoError(tt, err)
	})

	t.Run("success without service policy", func(tt *testing.T) {
		transport := &mockTransport{response: &networking.NetworkIPInterfaceModifyOK{}}
		client := &networkingClient{api: networking.New(transport, nil)}
		err := client.NetworkIPInterfaceModify(&NetworkIPInterfaceModifyParams{UUID: &uuid})
		assert.NoError(tt, err)
	})
}

func TestKerberosInterfaceCollectionGetParamsToONTAP(t *testing.T) {
	t.Run("nil params", func(tt *testing.T) {
		got := kerberosInterfaceCollectionGetParamsToONTAP(nil)
		require.NotNil(tt, got)
	})

	t.Run("sets fields and identifiers", func(tt *testing.T) {
		svmUUID := "svm-uuid"
		svmName := "svm"
		ifaceName := "lif1"
		got := kerberosInterfaceCollectionGetParamsToONTAP(&KerberosInterfaceCollectionGetParams{
			SvmUUID:       &svmUUID,
			SvmName:       &svmName,
			InterfaceName: &ifaceName,
			Fields:        []string{"*"},
		})
		require.NotNil(tt, got)
		assert.Equal(tt, []string{"*"}, got.Fields)
		require.NotNil(tt, got.SvmUUID)
		assert.Equal(tt, svmUUID, *got.SvmUUID)
		require.NotNil(tt, got.SvmName)
		assert.Equal(tt, svmName, *got.SvmName)
	})

	t.Run("no fields when empty", func(tt *testing.T) {
		got := kerberosInterfaceCollectionGetParamsToONTAP(&KerberosInterfaceCollectionGetParams{})
		require.NotNil(tt, got)
		assert.Nil(tt, got.Fields)
	})
}

func TestKerberosInterfaceModifyParamsToONTAP(t *testing.T) {
	t.Run("nil params", func(tt *testing.T) {
		got := kerberosInterfaceModifyParamsToONTAP(nil)
		require.NotNil(tt, got)
	})

	t.Run("propagates force and disable flags", func(tt *testing.T) {
		uuid := "if-uuid"
		enabled := false
		force := true
		got := kerberosInterfaceModifyParamsToONTAP(&KerberosInterfaceModifyParams{
			InterfaceUUID:     &uuid,
			IsKerberosEnabled: &enabled,
			Force:             &force,
			AdminUsername:     nillable.ToPointer("admin"),
			AdminPassword:     nillable.ToPointer("pw"),
			OU:                nillable.ToPointer("OU=test"),
		})
		require.NotNil(tt, got)
		require.NotNil(tt, got.Info)
		require.NotNil(tt, got.Info.Force)
		assert.True(tt, *got.Info.Force)
		require.NotNil(tt, got.Info.Enabled)
		assert.False(tt, *got.Info.Enabled)
	})
}

func TestNfsServiceModifyParamsToONTAP_AllowLocalNFSUsersOutsideProtocolBlock(t *testing.T) {
	t.Run("nil params", func(tt *testing.T) {
		got := nfsServiceModifyParamsToONTAP(nil)
		require.NotNil(tt, got)
	})

	// AllowLocalNFSUsersWithLdap / ExtendedGroupsLimit must be applied even when
	// no protocol version fields are set (regression: previously nested inside
	// the protocol block).
	t.Run("applies auth-sys extended groups without protocol versions", func(tt *testing.T) {
		allow := true
		limit := int64(32)
		got := nfsServiceModifyParamsToONTAP(&NfsServiceModifyParams{
			SvmUUID:                    "svm-uuid",
			AllowLocalNFSUsersWithLdap: &allow,
			ExtendedGroupsLimit:        &limit,
		})
		require.NotNil(tt, got)
		require.NotNil(tt, got.Info)
		require.NotNil(tt, got.Info.AuthSysExtendedGroupsEnabled)
		assert.True(tt, *got.Info.AuthSysExtendedGroupsEnabled)
		require.NotNil(tt, got.Info.ExtendedGroupsLimit)
		assert.Equal(tt, int64(32), *got.Info.ExtendedGroupsLimit)
		// No protocol block should be created when no version flags are set.
		assert.Nil(tt, got.Info.Protocol)
	})
}
