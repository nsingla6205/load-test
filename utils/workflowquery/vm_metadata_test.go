package workflowquery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVMMetadata_InterclusterAndNodemgmtInternal(t *testing.T) {
	t.Parallel()
	const snippet = `{
  "cloud": {
    "ha_pair": [
      {
        "vm1": {
          "lifs": {
            "intercluster": { "ip": "10.38.25.146" },
            "nodemgmtinternal": { "ip": "10.38.18.182" }
          }
        },
        "vm2": {
          "lifs": {
            "intercluster": { "ip": "10.38.1.218" },
            "nodemgmtinternal": { "ip": "10.38.5.224" }
          }
        }
      }
    ]
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(snippet), &cfg))
	require.Equal(t, []string{"10.38.25.146", "10.38.1.218"}, interclusterIPsFromEmbed(&cfg))
	require.Equal(t, []string{"10.38.18.182", "10.38.5.224"}, nodemgmtInternalIPsFromEmbed(&cfg))
}

func TestInterclusterIPsFromEmbed_NilAndDedup(t *testing.T) {
	t.Parallel()
	require.Nil(t, interclusterIPsFromEmbed(nil))
	require.Nil(t, nodemgmtInternalIPsFromEmbed(nil))
	require.Nil(t, poolVMMetadataFromEmbed(nil))

	const dupSnippet = `{
  "cloud": {
    "ha_pair": [
      {
        "vm1": {
          "lifs": {
            "intercluster": { "ip": "10.0.0.1" },
            "nodemgmtinternal": { "ip": "10.0.2.1" }
          }
        },
        "vm2": {
          "lifs": {
            "intercluster": { "ip": "10.0.0.1" },
            "nodemgmtinternal": { "ip": "10.0.2.1" }
          }
        }
      }
    ]
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(dupSnippet), &cfg))
	require.Equal(t, []string{"10.0.0.1"}, interclusterIPsFromEmbed(&cfg))
	require.Equal(t, []string{"10.0.2.1"}, nodemgmtInternalIPsFromEmbed(&cfg))
}

func TestPoolVMMetadataFromEmbed(t *testing.T) {
	t.Parallel()
	const snippet = `{
  "cloud": {
    "ha_pair": [
      {
        "vm1": {
          "name": "vm-01",
          "serial_number": "1234501",
          "vsa_management_ip": "150.136.212.147",
          "vm_instance_id": "ocid1.instance.oc1.iad.vm01",
          "lifs": {
            "intercluster": { "ip": "10.38.25.146" },
            "nodemgmtinternal": { "ip": "10.38.18.182" }
          },
          "data_disks": [
            { "size": 50, "disk_iops": 1500, "disk_throughput": 954 },
            { "size": 50, "disk_iops": 1500, "disk_throughput": 954 }
          ]
        },
        "vm2": {
          "name": "vm-02",
          "serial_number": "1234502",
          "vsa_management_ip": "158.101.109.167",
          "vm_instance_id": "ocid1.instance.oc1.iad.vm02",
          "lifs": {
            "intercluster": { "ip": "10.38.1.218" },
            "nodemgmtinternal": { "ip": "10.38.5.224" }
          },
          "data_disks": [
            { "size": 100, "disk_iops": 3000, "disk_throughput": 1908 }
          ]
        }
      }
    ]
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(snippet), &cfg))
	// Per-VM iops/throughput are no longer surfaced; only SizeInGiB is derived
	// from data_disks. iops/throughput are pool-level (from spconfig).
	require.Equal(t, []OCICreatePoolVMMetadata{
		{
			Name:            "vm-01",
			SerialNumber:    "1234501",
			VSAManagementIP: "150.136.212.147",
			InterclusterIP:  "10.38.25.146",
			HAPair:          "ha_pair-1",
			SizeInGiB:       100,
			InstanceID:      "ocid1.instance.oc1.iad.vm01",
		},
		{
			Name:            "vm-02",
			SerialNumber:    "1234502",
			VSAManagementIP: "158.101.109.167",
			InterclusterIP:  "10.38.1.218",
			HAPair:          "ha_pair-1",
			SizeInGiB:       100,
			InstanceID:      "ocid1.instance.oc1.iad.vm02",
		},
	}, poolVMMetadataFromEmbed(&cfg))
}

// TestPoolSPConfigFromEmbed pins the pool-level iops/throughput sourced from the
// VLM config's spconfig block. iops is surfaced as-is; tput (MiBps) is converted
// to GBps. These replace the former per-VM data-disk aggregates (always 0 on OCI).
func TestPoolSPConfigFromEmbed(t *testing.T) {
	t.Parallel()
	require.Zero(t, poolIOPSFromEmbed(nil))
	require.Zero(t, poolThroughputGBpsFromEmbed(nil))

	const snippet = `{
  "cloud": { "ha_pair": [] },
  "deployment": {
    "spconfig": {
      "size": "5500Gi",
      "iops": 15248,
      "tput": 954
    }
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(snippet), &cfg))
	require.Equal(t, int64(15248), poolIOPSFromEmbed(&cfg))
	require.Equal(t, float64(954)/MiBpsPerGBps, poolThroughputGBpsFromEmbed(&cfg))

	const noSP = `{ "cloud": { "ha_pair": [] } }`
	var cfg2 vlmConfig
	require.NoError(t, json.Unmarshal([]byte(noSP), &cfg2))
	require.Zero(t, poolIOPSFromEmbed(&cfg2))
	require.Zero(t, poolThroughputGBpsFromEmbed(&cfg2))
}

func TestPoolVMMetadataFromEmbed_NoDataDisks(t *testing.T) {
	t.Parallel()
	const snippet = `{
  "cloud": {
    "ha_pair": [
      {
        "vm1": {
          "name": "vm-01",
          "serial_number": "1234501",
          "vsa_management_ip": "150.136.212.147",
          "lifs": {
            "intercluster": { "ip": "10.38.25.146" },
            "nodemgmtinternal": { "ip": "10.38.18.182" }
          },
          "data_disks": null
        }
      }
    ]
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(snippet), &cfg))
	require.Equal(t, []OCICreatePoolVMMetadata{
		{
			Name:            "vm-01",
			SerialNumber:    "1234501",
			VSAManagementIP: "150.136.212.147",
			InterclusterIP:  "10.38.25.146",
			HAPair:          "ha_pair-1",
		},
	}, poolVMMetadataFromEmbed(&cfg))
}

// TestMediatorFromEmbed pins that the pool surfaces the first non-empty
// mediator name and its 1-indexed HA-pair label from cloud.ha_pair[*].mediator.
// Mediators are not VSA data nodes (excluded from vms), so only name + haPair
// are exposed.
func TestMediatorFromEmbed(t *testing.T) {
	t.Parallel()
	require.Nil(t, mediatorFromEmbed(nil), "nil cfg returns no mediator")

	// Mediator lives on the second HA pair: its label must be ha_pair-2.
	const withMediator = `{
  "cloud": {
    "ha_pair": [
      {
        "vm1": { "name": "vm-01", "lifs": { "intercluster": { "ip": "10.0.0.1" } } },
        "vm2": { "name": "vm-02", "lifs": { "intercluster": { "ip": "10.0.0.2" } } }
      },
      {
        "vm1": { "name": "vm-03", "lifs": { "intercluster": { "ip": "10.0.0.3" } } },
        "vm2": { "name": "vm-04", "lifs": { "intercluster": { "ip": "10.0.0.4" } } },
        "mediator": { "name": "FsnIdocnv-mediator-01", "is_mediator": true, "vm_instance_id": "ocid1.instance.oc1.iad.mediator01", "lifs": { "rsm": { "ip": "10.20.27.35" } } }
      }
    ]
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(withMediator), &cfg))
	mediator := mediatorFromEmbed(&cfg)
	require.NotNil(t, mediator)
	require.Equal(t, "FsnIdocnv-mediator-01", mediator.Name)
	require.Equal(t, "10.20.27.35", mediator.IP, "mediator ip must be sourced from cloud.ha_pair[*].mediator.lifs.rsm.ip")
	require.Equal(t, "ha_pair-2", mediator.HAPair, "mediator on the 2nd HA pair must report the 1-indexed label ha_pair-2")
	require.Equal(t, "ocid1.instance.oc1.iad.mediator01", mediator.InstanceID, "mediator instance OCID must be sourced from cloud.ha_pair[*].mediator.vm_instance_id")

	// Mediator must not leak into the VM list.
	for _, vm := range poolVMMetadataFromEmbed(&cfg) {
		require.NotEqual(t, "FsnIdocnv-mediator-01", vm.Name, "mediator must never appear as a VSA VM entry")
	}

	const noMediator = `{ "cloud": { "ha_pair": [ { "vm1": { "name": "vm-01" } } ] } }`
	var cfg2 vlmConfig
	require.NoError(t, json.Unmarshal([]byte(noMediator), &cfg2))
	require.Nil(t, mediatorFromEmbed(&cfg2), "missing mediator returns nil so callers omit the field")
}

func TestPoolUUIDFromEmbed(t *testing.T) {
	t.Parallel()
	require.Empty(t, poolUUIDFromEmbed(nil), "nil cfg returns empty UUID")

	const withLabels = `{
  "cloud": { "ha_pair": [] },
  "deployment": {
    "labels": {
      "pool_ocid": "ocid1.pool.oc1.ashburn-1.testpool",
      "pool_uuid": "b5fb9baf-953b-9c65-19d5-31e3365cc2e3",
      "pool_name": "testpool"
    }
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(withLabels), &cfg))
	require.Equal(t, "b5fb9baf-953b-9c65-19d5-31e3365cc2e3", poolUUIDFromEmbed(&cfg))

	const withoutLabels = `{ "cloud": { "ha_pair": [] } }`
	var cfg2 vlmConfig
	require.NoError(t, json.Unmarshal([]byte(withoutLabels), &cfg2))
	require.Empty(t, poolUUIDFromEmbed(&cfg2), "missing deployment.labels returns empty UUID")
}

// TestPoolOCIDFromEmbed pins the helper that surfaces deployment.labels[pool_ocid]
// onto the OCICreatePoolMetadata.PoolOCID field. Empty/missing must return ""
// so callers can rely on `omitempty` to drop the wire field.
func TestPoolOCIDFromEmbed(t *testing.T) {
	t.Parallel()
	require.Empty(t, poolOCIDFromEmbed(nil), "nil cfg returns empty OCID")

	const withLabels = `{
  "cloud": { "ha_pair": [] },
  "deployment": {
    "labels": {
      "pool_ocid": "ocid1.pool.oc1.ashburn-1.testpool",
      "pool_uuid": "b5fb9baf-953b-9c65-19d5-31e3365cc2e3"
    }
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(withLabels), &cfg))
	require.Equal(t, "ocid1.pool.oc1.ashburn-1.testpool", poolOCIDFromEmbed(&cfg))

	const withoutLabels = `{ "cloud": { "ha_pair": [] } }`
	var cfg2 vlmConfig
	require.NoError(t, json.Unmarshal([]byte(withoutLabels), &cfg2))
	require.Empty(t, poolOCIDFromEmbed(&cfg2), "missing deployment.labels returns empty OCID")
}

func TestPoolNameFromEmbed(t *testing.T) {
	t.Parallel()
	require.Empty(t, poolNameFromEmbed(nil), "nil cfg returns empty name")

	const withLabels = `{
  "cloud": { "ha_pair": [] },
  "deployment": {
    "labels": {
      "pool_ocid": "ocid1.pool.oc1.ashburn-1.testpool",
      "pool_uuid": "b5fb9baf-953b-9c65-19d5-31e3365cc2e3",
      "pool_name": "testpool"
    }
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(withLabels), &cfg))
	require.Equal(t, "testpool", poolNameFromEmbed(&cfg))

	const withoutLabels = `{ "cloud": { "ha_pair": [] } }`
	var cfg2 vlmConfig
	require.NoError(t, json.Unmarshal([]byte(withoutLabels), &cfg2))
	require.Empty(t, poolNameFromEmbed(&cfg2), "missing deployment.labels returns empty name")
}

// TestClusterIPFromEmbed verifies that the cluster-scoped RBAC LIF IP is
// surfaced once at the pool level. The RBAC LIF lives on exactly one VM at a
// time (VLM sets `lifs.rbac.ip` on the active VM and leaves it empty on the
// rest), so per-VM rbac IPs would mostly be empty strings — the API contract
// returns this single value in `OCICreatePoolMetadata.ClusterIP`.
func TestClusterIPFromEmbed(t *testing.T) {
	t.Parallel()

	require.Empty(t, clusterIPFromEmbed(nil), "nil cfg returns empty so callers omit clusterIP from the response")

	const noRbacAssigned = `{"cloud":{"ha_pair":[{
		"vm1":{"lifs":{"rbac":{"ip":""}}},
		"vm2":{"lifs":{"rbac":{"ip":""}}}
	}]}}`
	var cfgEmpty vlmConfig
	require.NoError(t, json.Unmarshal([]byte(noRbacAssigned), &cfgEmpty))
	require.Empty(t, clusterIPFromEmbed(&cfgEmpty),
		"every rbac.ip empty means the LIF has not been provisioned yet — omit the field rather than emit an empty string")

	// Single-pair case: vm1 hosts the RBAC LIF, vm2 is empty (real-world
	// shape from VLM config; rbac is cluster-scoped and lives on one VM).
	const vm1Active = `{"cloud":{"ha_pair":[{
		"vm1":{"lifs":{"rbac":{"ip":"10.38.23.99"}}},
		"vm2":{"lifs":{"rbac":{"ip":""}}}
	}]}}`
	var cfg1 vlmConfig
	require.NoError(t, json.Unmarshal([]byte(vm1Active), &cfg1))
	require.Equal(t, "10.38.23.99", clusterIPFromEmbed(&cfg1))

	// Multi-pair case: across pairs only one VM holds the LIF; the helper
	// must walk every pair until it finds it.
	const lateLIF = `{"cloud":{"ha_pair":[
		{"vm1":{"lifs":{"rbac":{"ip":""}}},"vm2":{"lifs":{"rbac":{"ip":""}}}},
		{"vm1":{"lifs":{"rbac":{"ip":""}}},"vm2":{"lifs":{"rbac":{"ip":"10.0.0.5"}}}}
	]}}`
	var cfg2 vlmConfig
	require.NoError(t, json.Unmarshal([]byte(lateLIF), &cfg2))
	require.Equal(t, "10.0.0.5", clusterIPFromEmbed(&cfg2))
}

// TestPoolVMMetadataFromEmbed_HAPairIndexing verifies that HAPair labels are
// 1-indexed on the API contract (ha_pair-1, ha_pair-2, ...) even though the
// underlying `cloud.ha_pair` slice is iterated with a 0-based index, and that
// both VMs in the same pair share the same label.
func TestPoolVMMetadataFromEmbed_HAPairIndexing(t *testing.T) {
	t.Parallel()
	const snippet = `{
  "cloud": {
    "ha_pair": [
      {
        "vm1": { "name": "vm-01", "lifs": { "intercluster": { "ip": "10.0.0.1" } } },
        "vm2": { "name": "vm-02", "lifs": { "intercluster": { "ip": "10.0.0.2" } } }
      },
      {
        "vm1": { "name": "vm-03", "lifs": { "intercluster": { "ip": "10.0.0.3" } } },
        "vm2": { "name": "vm-04", "lifs": { "intercluster": { "ip": "10.0.0.4" } } }
      }
    ]
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(snippet), &cfg))
	got := poolVMMetadataFromEmbed(&cfg)
	require.Len(t, got, 4)
	require.Equal(t, "ha_pair-1", got[0].HAPair, "vm1 of slice index 0 must be ha_pair-1 (1-indexed wire format)")
	require.Equal(t, "ha_pair-1", got[1].HAPair, "vm2 of slice index 0 must be ha_pair-1")
	require.Equal(t, "ha_pair-2", got[2].HAPair, "vm1 of slice index 1 must be ha_pair-2")
	require.Equal(t, "ha_pair-2", got[3].HAPair, "vm2 of slice index 1 must be ha_pair-2")
}

func TestPoolVMMetadataFromEmbed_RBACOnlyVMOmitted(t *testing.T) {
	t.Parallel()
	const snippet = `{
  "cloud": {
    "ha_pair": [
      {
        "vm1": {
          "name": "vm-01",
          "serial_number": "1234501",
          "vsa_management_ip": "150.136.212.147",
          "lifs": {
            "intercluster": { "ip": "10.38.25.146" },
            "nodemgmtinternal": { "ip": "10.38.18.182" },
            "rbac": { "ip": "10.38.23.99" }
          }
        },
        "vm2": {
          "lifs": {
            "rbac": { "ip": "" }
          }
        }
      }
    ]
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(snippet), &cfg))

	got := poolVMMetadataFromEmbed(&cfg)
	require.Len(t, got, 1,
		"VM2 carries no per-VM signal (name/serial/intercluster/nodemgmt/disks all empty) — even with RBAC tracked at pool level, it must not be promoted to a per-VM entry")
	require.Equal(t, "vm-01", got[0].Name,
		"VM1 must still be emitted because it carries real per-VM data; RBAC presence is irrelevant to per-VM emptiness")

	require.Equal(t, "10.38.23.99", clusterIPFromEmbed(&cfg),
		"RBAC LIF IP belongs at pool level; ClusterIP must be populated from whichever VM hosts the active RBAC LIF")
}

func TestVMMetadataIsEmpty_RBACDoesNotMakeVMNonEmpty(t *testing.T) {
	t.Parallel()
	vm := vmMetadata{}
	vm.Lifs.Rbac.IP = "10.38.23.99"
	require.True(t, vmMetadataIsEmpty(vm),
		"RBAC LIF IP alone must not flip a VM from empty to non-empty; RBAC is surfaced at pool level via clusterIPFromEmbed")
}

// TestVMMetadataIsEmpty_InstanceIDMakesVMNonEmpty pins that a node carrying only
// its OCI instance OCID (no name/serial/IP/disks yet) is still surfaced as a
// per-VM entry, so the instanceId is not silently dropped early in provisioning.
func TestVMMetadataIsEmpty_InstanceIDMakesVMNonEmpty(t *testing.T) {
	t.Parallel()
	vm := vmMetadata{VMInstanceID: "ocid1.instance.oc1.iad.vm01"}
	require.False(t, vmMetadataIsEmpty(vm),
		"a VM with only oci_vm_instance_id must be treated as non-empty so the instance OCID reaches the response")

	const snippet = `{
  "cloud": {
    "ha_pair": [
      {
        "vm1": { "vm_instance_id": "ocid1.instance.oc1.iad.vm01" }
      }
    ]
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(snippet), &cfg))
	require.Equal(t, []OCICreatePoolVMMetadata{
		{
			HAPair:     "ha_pair-1",
			InstanceID: "ocid1.instance.oc1.iad.vm01",
		},
	}, poolVMMetadataFromEmbed(&cfg))
}

func TestPoolVMMetadataFromEmbed_VM2Omitted(t *testing.T) {
	t.Parallel()
	const snippet = `{
  "cloud": {
    "ha_pair": [
      {
        "vm1": {
          "name": "vm-01",
          "serial_number": "1234501",
          "vsa_management_ip": "150.136.212.147",
          "lifs": {
            "intercluster": { "ip": "10.38.25.146" },
            "nodemgmtinternal": { "ip": "10.38.18.182" }
          }
        }
      }
    ]
  }
}`
	var cfg vlmConfig
	require.NoError(t, json.Unmarshal([]byte(snippet), &cfg))
	require.Equal(t, []OCICreatePoolVMMetadata{
		{
			Name:            "vm-01",
			SerialNumber:    "1234501",
			VSAManagementIP: "150.136.212.147",
			InterclusterIP:  "10.38.25.146",
			HAPair:          "ha_pair-1",
		},
	}, poolVMMetadataFromEmbed(&cfg))
}
