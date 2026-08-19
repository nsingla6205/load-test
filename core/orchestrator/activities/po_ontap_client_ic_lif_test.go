package activities

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// TestPOClusterClient_CreateInterclusterLif_RequestShape verifies the exact
// ONTAP REST request shape produced for an intercluster LIF create against a
// pre-existing PO cluster. The design pins these fields explicitly:
//
//   - Scope MUST be "cluster" so the LIF is provisioned at cluster scope.
//   - SvmName MUST be empty; the ONTAP REST layer only serializes the SVM
//     block when the scope is "svm", so passing SvmName here would silently
//     bind an intercluster LIF to a pool SVM.
//   - IPSpace MUST be set: ONTAP requires ipspace.name or ipspace.uuid on
//     POST when scope is "cluster".
//   - ServicePolicy MUST be the caller-provided "default-intercluster"
//     policy: this is what maps to the intercluster_core service on ONTAP.
//   - HomePort MUST include the VLAN tag (e.g. "a0a-4001").
//   - IP address and netmask are passed through verbatim from the caller.
//
// The table exercises multiple representative input shapes so a regression
// in the ONTAP mapping (e.g. an accidental "svm" scope or a swapped
// address/netmask) fails deterministically.
func TestPOClusterClient_CreateInterclusterLif_RequestShape(t *testing.T) {
	cases := []struct {
		name string
		spec POInterclusterLifSpec
	}{
		{
			name: "extreme_two_node_first_lif",
			spec: POInterclusterLifSpec{
				Name:          "ic-lif-node-1",
				IPAddress:     "10.10.0.10",
				Netmask:       "255.255.255.240",
				HomeNode:      "node-1",
				HomePort:      "a0a-4001",
				ServicePolicy: common.POInterclusterServicePolicy,
				IPSpace:       "ipspace_acctuuid_4001",
			},
		},
		{
			name: "extreme_two_node_second_lif",
			spec: POInterclusterLifSpec{
				Name:          "ic-lif-node-2",
				IPAddress:     "10.10.0.11",
				Netmask:       "255.255.255.240",
				HomeNode:      "node-2",
				HomePort:      "a0a-4001",
				ServicePolicy: common.POInterclusterServicePolicy,
				IPSpace:       "ipspace_acctuuid_4001",
			},
		},
		{
			name: "premium_slash27_range",
			spec: POInterclusterLifSpec{
				Name:          "ic-lif-node-1",
				IPAddress:     "192.168.4.34",
				Netmask:       "255.255.255.224",
				HomeNode:      "node-1",
				HomePort:      "a0a-3200",
				ServicePolicy: common.POInterclusterServicePolicy,
				IPSpace:       "ipspace_acctuuid_3200",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPOClusterClientFixture(t)

			// Capture the exact NetworkIPInterfacesCreateParams so we can
			// assert on it after the call. The mock's Return path also
			// hands back a plausible ONTAP response so the client can
			// build its POInterclusterLifResult.
			returnedName := tc.spec.Name
			returnedUUID := "ext-uuid-" + tc.spec.Name
			var captured *ontapRest.NetworkIPInterfacesCreateParams
			f.net.On("NetworkIPInterfaceCreate", mock.MatchedBy(func(p *ontapRest.NetworkIPInterfacesCreateParams) bool {
				if p == nil {
					return false
				}
				captured = p
				return true
			})).Return(&ontapRest.IPInterface{
				IPInterface: models.IPInterface{Name: &returnedName, UUID: &returnedUUID},
			}, nil).Once()

			got, err := f.client.CreateInterclusterLif(tc.spec)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, returnedName, got.Name)
			assert.Equal(t, returnedUUID, got.ExternalUUID)

			require.NotNil(t, captured, "NetworkIPInterfaceCreate was not called with expected params")

			// Scope MUST be cluster; SVM name MUST be empty for cluster scope.
			assert.Equal(t, models.IPInterfaceScopeCluster, captured.Scope, "IC LIFs must be created at cluster scope")
			assert.Empty(t, captured.SvmName, "cluster-scoped LIFs must not carry an SVM name")

			// Service policy is the intercluster policy the design pins.
			assert.Equal(t, "default-intercluster", captured.ServicePolicy)
			assert.Equal(t, common.POInterclusterServicePolicy, captured.ServicePolicy)

			// Pass-through fields.
			assert.Equal(t, tc.spec.Name, captured.Name)
			assert.Equal(t, tc.spec.IPAddress, captured.IPAddress)
			assert.Equal(t, tc.spec.Netmask, captured.Netmask)
			assert.Equal(t, tc.spec.HomeNode, captured.HomeNode)
			assert.Equal(t, tc.spec.HomePort, captured.HomePort)
			assert.Equal(t, tc.spec.IPSpace, captured.IPSpace)

			f.net.AssertExpectations(t)
		})
	}
}

// TestPOClusterClient_CreateInterclusterLif_PropagatesRESTError asserts an
// ONTAP-level REST failure bubbles up unchanged (no swallowing) so the
// enclosing workflow can rollback.
func TestPOClusterClient_CreateInterclusterLif_PropagatesRESTError(t *testing.T) {
	f := newPOClusterClientFixture(t)
	f.net.On("NetworkIPInterfaceCreate", mock.Anything).
		Return(nil, errors.New("ontap 500"))

	got, err := f.client.CreateInterclusterLif(POInterclusterLifSpec{
		Name:          "ic-lif-node-1",
		IPAddress:     "10.10.0.10",
		Netmask:       "255.255.255.240",
		HomeNode:      "node-1",
		HomePort:      "a0a-4001",
		ServicePolicy: common.POInterclusterServicePolicy,
		IPSpace:       "ipspace_acctuuid_4001",
	})
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "ontap 500")
}

// TestPOClusterClient_CreateInterclusterLif_RejectsInvalidONTAPResponse
// guards against a malformed ONTAP response (missing name or UUID); the
// workflow relies on ExternalUUID to persist the IC LIF row, so silently
// accepting nil pointers here would corrupt the DB.
func TestPOClusterClient_CreateInterclusterLif_RejectsInvalidONTAPResponse(t *testing.T) {
	tests := []struct {
		name     string
		response *ontapRest.IPInterface
	}{
		{name: "nil_response", response: nil},
		{name: "nil_uuid", response: &ontapRest.IPInterface{IPInterface: models.IPInterface{Name: stringPtr("ic-lif-node-1")}}},
		{name: "nil_name", response: &ontapRest.IPInterface{IPInterface: models.IPInterface{UUID: stringPtr("ext-uuid-1")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPOClusterClientFixture(t)
			f.net.On("NetworkIPInterfaceCreate", mock.Anything).Return(tt.response, nil).Once()

			got, err := f.client.CreateInterclusterLif(POInterclusterLifSpec{
				Name:          "ic-lif-node-1",
				IPAddress:     "10.10.0.10",
				Netmask:       "255.255.255.240",
				HomeNode:      "node-1",
				HomePort:      "a0a-4001",
				ServicePolicy: common.POInterclusterServicePolicy,
				IPSpace:       "ipspace_acctuuid_4001",
			})
			require.Error(t, err)
			assert.Nil(t, got)
		})
	}
}

// stringPtr returns the address of s. Named to avoid collision with any
// generic ptr helper in the activities package.
func stringPtr(s string) *string { return &s }

// TestPOClusterClient_GetInterclusterLifs is a thin pass-through test: the
// method just forwards to provider.GetInterclusterLIFs, but pinning the mock
// call and the returned slice guards against a future refactor that swaps
// providers or filters results silently.
func TestPOClusterClient_GetInterclusterLifs(t *testing.T) {
	f := newPOClusterClientFixture(t)
	want := []*vsa.InterclusterLif{{Name: "lif-1"}, {Name: "lif-2"}}
	f.provider.On("GetInterclusterLIFs", "default-intercluster").Return(want, nil).Once()

	got, err := f.client.GetInterclusterLifs("default-intercluster")
	require.NoError(t, err)
	assert.Equal(t, want, got)

	f.provider.On("GetInterclusterLIFs", "custom-policy").Return(nil, errors.New("provider off")).Once()
	_, err = f.client.GetInterclusterLifs("custom-policy")
	require.Error(t, err)
}

// TestPOClusterClient_DeleteInterclusterLif verifies the delete-by-UUID path,
// including the NotFound-is-idempotent contract.
func TestPOClusterClient_DeleteInterclusterLif(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkIPInterfaceDelete", "ext-uuid-1").Return(nil).Once()
		require.NoError(t, f.client.DeleteInterclusterLif("ext-uuid-1"))
	})

	t.Run("not-found is success", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkIPInterfaceDelete", "ext-uuid-1").
			Return(utilErrors.NewNotFoundErr("ip-interface", nil)).Once()
		require.NoError(t, f.client.DeleteInterclusterLif("ext-uuid-1"))
	})

	t.Run("other errors propagate", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkIPInterfaceDelete", "ext-uuid-1").Return(errors.New("cluster down")).Once()
		err := f.client.DeleteInterclusterLif("ext-uuid-1")
		require.Error(t, err)
	})
}

// TestPOClusterClient_DeleteIPSpaceDefaultRoute pins the best-effort no-op
// contract documented on the method: ONTAP has no single-route delete API, so
// rollback callers must not receive an error for a route that logically
// "does not exist".
func TestPOClusterClient_DeleteIPSpaceDefaultRoute(t *testing.T) {
	f := newPOClusterClientFixture(t)
	// No mock expectations: implementation must NOT touch the REST client.
	require.NoError(t, f.client.DeleteIPSpaceDefaultRoute("ipspace-1", "10.0.0.1"))
	require.NoError(t, f.client.DeleteIPSpaceDefaultRoute("", ""))
}

// TestPOClusterClient_DeleteVlanNetPorts verifies the ipspace-scoped VLAN
// port cleanup path, including error propagation from the initial ports-get
// call and skipping nil / empty-UUID entries so we don't call delete on
// garbage.
func TestPOClusterClient_DeleteVlanNetPorts(t *testing.T) {
	portUUID := "port-uuid-1"

	t.Run("deletes every vlan port in ipspace", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetPortsGet", mock.MatchedBy(func(p *ontapRest.NetworkEthernetPortsGetParams) bool {
			return p != nil && p.IPSpace == "ipspace-1" && p.Type == "vlan"
		})).Return([]*ontapRest.NetworkPort{
			{Port: models.Port{UUID: &portUUID}},
		}, nil).Once()
		f.net.On("NetworkEthernetPortDelete", mock.Anything).Return("", nil).Once()

		require.NoError(t, f.client.DeleteVlanNetPorts("ipspace-1", "bd", "a0a", 100, []string{"n1"}))
	})

	t.Run("skips nil and empty-uuid ports", func(t *testing.T) {
		empty := "  "
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetPortsGet", mock.Anything).Return([]*ontapRest.NetworkPort{
			nil,
			{Port: models.Port{UUID: &empty}},
			{Port: models.Port{UUID: &portUUID}},
		}, nil).Once()
		f.net.On("NetworkEthernetPortDelete", mock.Anything).Return("", nil).Once()

		require.NoError(t, f.client.DeleteVlanNetPorts("ipspace-1", "bd", "a0a", 100, []string{"n1"}))
	})

	t.Run("ports-get error propagates", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetPortsGet", mock.Anything).Return(nil, errors.New("get failed")).Once()
		err := f.client.DeleteVlanNetPorts("ipspace-1", "bd", "a0a", 100, []string{"n1"})
		require.Error(t, err)
	})

	t.Run("port-delete error propagates", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetPortsGet", mock.Anything).Return([]*ontapRest.NetworkPort{
			{Port: models.Port{UUID: &portUUID}},
		}, nil).Once()
		f.net.On("NetworkEthernetPortDelete", mock.Anything).Return("", errors.New("delete failed")).Once()
		err := f.client.DeleteVlanNetPorts("ipspace-1", "bd", "a0a", 100, []string{"n1"})
		require.Error(t, err)
	})
}

// TestPOClusterClient_DeleteBroadcastDomain verifies the delete-by-name path
// and the ONTAP 404-is-success contract.
func TestPOClusterClient_DeleteBroadcastDomain(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetBroadcastDomainDelete", mock.MatchedBy(func(p *ontapRest.NetworkEthernetBroadcastDomainDeleteParams) bool {
			return p != nil && p.Name == "bd-1"
		})).Return(nil).Once()
		require.NoError(t, f.client.DeleteBroadcastDomain("ipspace-1", "bd-1"))
	})

	t.Run("not-found is success", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetBroadcastDomainDelete", mock.Anything).
			Return(utilErrors.NewNotFoundErr("bdomain", nil)).Once()
		require.NoError(t, f.client.DeleteBroadcastDomain("ipspace-1", "bd-1"))
	})

	t.Run("other errors propagate", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetBroadcastDomainDelete", mock.Anything).Return(errors.New("cluster off")).Once()
		err := f.client.DeleteBroadcastDomain("ipspace-1", "bd-1")
		require.Error(t, err)
	})
}

// TestPOClusterClient_DeleteIPSpace mirrors the broadcast-domain delete
// contract but for the ipspace resource.
func TestPOClusterClient_DeleteIPSpace(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("IpspaceDelete", mock.MatchedBy(func(p *ontapRest.IpspaceDeleteParams) bool {
			return p != nil && p.Name == "ipspace-1"
		})).Return(nil).Once()
		require.NoError(t, f.client.DeleteIPSpace("ipspace-1"))
	})

	t.Run("not-found is success", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("IpspaceDelete", mock.Anything).
			Return(utilErrors.NewNotFoundErr("ipspace", nil)).Once()
		require.NoError(t, f.client.DeleteIPSpace("ipspace-1"))
	})

	t.Run("other errors propagate", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("IpspaceDelete", mock.Anything).Return(errors.New("cluster off")).Once()
		err := f.client.DeleteIPSpace("ipspace-1")
		require.Error(t, err)
	})
}
