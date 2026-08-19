package rules_v2

import (
	. "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/ruleengine/dsl"
)

// GetProxyRules returns the rule map for the proxy.
// This uses the DSL-based rule engine for cleaner rule definitions.
func GetProxyRules() map[string]Rule {
	return map[string]Rule{
		// Storage Volumes - list and create
		"/api/storage/volumes": {
			GET: Allow{
				Name: "Allow volume listing",
				ModifyResponse: RemoveFields{
					Fields: []string{
						"$.efficiency",
						"$.space.physical_used",
						"$.space.physical_used_percent",
						"$.space.footprint",
						"$.space.local_tier_footprint",
						"$.space.effective_total_footprint",
						"$.space.delayed_free_footprint",
						"$.space.volume_guarantee_footprint",
						"$.space.snapmirror_destination_footprint",
						"$.space.file_operation_metadata",
						"$.space.metadata",
						"$.space.total_metadata",
						"$.space.total_metadata_footprint",
						"$.space.dedupe_metafiles_footprint",
						"$.space.dedupe_metafiles_temporary_footprint",
						"$.space.cross_volume_dedupe_metafiles_footprint",
						"$.space.cross_volume_dedupe_metafiles_temporary_footprint",
						"$.space.logical_space.enforcement",
						"$.space.logical_space.reporting",
					},
				},
			},
			POST: When{
				Name: "Volume creation validation",
				// All conditions return (bool, reason) - And() returns the first failure reason
				Condition: And(
					HasFields("name"),
					IfPresentThenValue("autosize.mode", "off"),
					volumePostCreateSizeFieldsCondition, // TODO: Implement volumePostCreateSizeFieldsCondition logic in the rule engine DSL pattern.
					IfPresentThenValue("guarantee.type", "none"),
					IfPresentThenEquals("space.logical_space.enforcement", true),
					IfPresentThenEquals("space.logical_space.reporting", true),
					validateVolumeCreation, // Returns specific error from core API
				),
				// No IsFalse needed - uses condition's reason directly
				IsTrue: Allow{
					Name: "Allow volume creation",
					ModifyRequest: SetRequestFields{
						Fields: map[string]interface{}{
							"space.logical_space.enforcement": true,
							"space.logical_space.reporting":   true,
						},
					},
					ModifyResponse: RemoveFields{
						Fields: []string{
							"$.efficiency",
							"$.space.physical_used",
							"$.space.physical_used_percent",
							"$.space.footprint",
							"$.space.local_tier_footprint",
							"$.space.effective_total_footprint",
							"$.space.delayed_free_footprint",
							"$.space.volume_guarantee_footprint",
							"$.space.snapmirror_destination_footprint",
							"$.space.file_operation_metadata",
							"$.space.metadata",
							"$.space.total_metadata",
							"$.space.total_metadata_footprint",
							"$.space.dedupe_metafiles_footprint",
							"$.space.dedupe_metafiles_temporary_footprint",
							"$.space.cross_volume_dedupe_metafiles_footprint",
							"$.space.cross_volume_dedupe_metafiles_temporary_footprint",
						},
					},
				},
			},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},

		// Storage Volumes - specific volume operations
		"/api/storage/volumes/{uuid}": {
			GET: Allow{
				Name: "Allow specific volume details",
				ModifyResponse: RemoveFields{
					Fields: []string{
						"$.efficiency",
						"$.space.physical_used",
						"$.space.physical_used_percent",
						"$.space.footprint",
						"$.space.local_tier_footprint",
						"$.space.effective_total_footprint",
						"$.space.delayed_free_footprint",
						"$.space.volume_guarantee_footprint",
						"$.space.snapmirror_destination_footprint",
						"$.space.file_operation_metadata",
						"$.space.metadata",
						"$.space.total_metadata",
						"$.space.total_metadata_footprint",
						"$.space.dedupe_metafiles_footprint",
						"$.space.dedupe_metafiles_temporary_footprint",
						"$.space.cross_volume_dedupe_metafiles_footprint",
						"$.space.cross_volume_dedupe_metafiles_temporary_footprint",
						"$.space.logical_space.enforcement",
						"$.space.logical_space.reporting",
					},
				},
			},
			POST: DenyAll{},
			PATCH: When{
				Name: "Volume modification validation",
				Condition: And(
					HasAtMostOneOf("size", "space.size", "cannot specify both 'size' and 'space.size'; use one or the other"),
					IfPresentThenValue("autosize.mode", "off"),
					IfPresentThenValue("guarantee.type", "none"),
					IfPresentThenEquals("space.logical_space.enforcement", true),
					validateVolumeModification,
				),
				// No IsFalse needed - uses condition's reason directly
				IsTrue: Allow{
					Name: "Allow volume modification",
					ModifyResponse: RemoveFields{
						Fields: []string{
							"$.efficiency",
							"$.space.physical_used",
							"$.space.physical_used_percent",
							"$.space.footprint",
							"$.space.local_tier_footprint",
							"$.space.effective_total_footprint",
							"$.space.delayed_free_footprint",
							"$.space.volume_guarantee_footprint",
							"$.space.snapmirror_destination_footprint",
							"$.space.file_operation_metadata",
							"$.space.metadata",
							"$.space.total_metadata",
							"$.space.total_metadata_footprint",
							"$.space.dedupe_metafiles_footprint",
							"$.space.dedupe_metafiles_temporary_footprint",
							"$.space.cross_volume_dedupe_metafiles_footprint",
							"$.space.cross_volume_dedupe_metafiles_temporary_footprint",
						},
					},
				},
			},
			DELETE: When{
				Name:      "Volume deletion validation",
				Condition: validateVolumeDeletion,
				IsTrue: Allow{
					Name: "Allow volume deletion",
					ModifyResponse: RemoveFields{
						Fields: []string{
							"$.efficiency",
							"$.space.physical_used",
							"$.space.physical_used_percent",
							"$.space.footprint",
							"$.space.local_tier_footprint",
							"$.space.effective_total_footprint",
							"$.space.delayed_free_footprint",
							"$.space.volume_guarantee_footprint",
							"$.space.snapmirror_destination_footprint",
							"$.space.file_operation_metadata",
							"$.space.metadata",
							"$.space.total_metadata",
							"$.space.total_metadata_footprint",
							"$.space.dedupe_metafiles_footprint",
							"$.space.dedupe_metafiles_temporary_footprint",
							"$.space.cross_volume_dedupe_metafiles_footprint",
							"$.space.cross_volume_dedupe_metafiles_temporary_footprint",
						},
					},
				},
			},
		},

		// Storage LUNs - block thick provisioning (space-reserved LUNs).
		// REST equivalent of CLI `lun create -space-reserve disabled` is space.guarantee.requested=false
		// (true => space-reserved/thick, false => thin). The proxy rejects any explicit request for a
		// space-reserved LUN and forces thin provisioning when the field is omitted. It also forces
		// space.scsi_thin_provisioning_support_enabled=true so hosts can reclaim unmapped blocks.
		"/api/storage/luns": {
			GET: Allow{
				Name: "Allow LUN listing",
			},
			POST: When{
				Name:      "LUN creation validation",
				Condition: IfPresentThenEquals("space.guarantee.requested", false),
				IsTrue: Allow{
					Name: "Allow LUN creation",
					ModifyRequest: SetRequestFields{
						Fields: map[string]interface{}{
							"space.guarantee.requested":                    false,
							"space.scsi_thin_provisioning_support_enabled": true,
						},
					},
				},
			},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},

		// Storage LUNs - specific LUN operations. Block re-enabling space reservation via modify.
		"/api/storage/luns/{uuid}": {
			GET:  Allow{Name: "Allow specific LUN details"},
			POST: DenyAll{},
			PATCH: When{
				Name:      "LUN modification validation",
				Condition: IfPresentThenEquals("space.guarantee.requested", false),
				IsTrue:    Allow{Name: "Allow LUN modification"},
			},
			DELETE: Allow{Name: "Allow LUN deletion"},
		},

		// Storage FlexCache - list and create
		"/api/storage/flexcache/flexcaches": {
			GET: Allow{
				Name: "Allow FlexCache listing",
			},
			POST: When{
				Name: "FlexCache creation validation",
				Condition: And(
					HasFields("name", "size", "svm.name"),
					IfPresentThenValue("guarantee.type", "none"),
					IfPresentThenValue("relative_size.enabled", false),
					validateFlexCacheCreation,
				),
				IsTrue: Allow{
					Name: "Allow FlexCache creation",
				},
			},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},

		// Storage FlexCache - specific flexcache operations
		"/api/storage/flexcache/flexcaches/{uuid}": {
			GET: Allow{
				Name: "Allow specific FlexCache details",
			},
			POST: DenyAll{},
			PATCH: Allow{
				Name: "Allow FlexCache modification",
			},
			DELETE: When{
				Name:      "FlexCache deletion validation",
				Condition: validateFlexCacheDeletion,
				IsTrue: Allow{
					Name: "Allow FlexCache deletion",
				},
			},
		},

		// Private CLI Volume rename - PATCH with query vserver, volume and body newname
		"/api/private/cli/volume/rename": {
			GET:  DenyAll{},
			POST: DenyAll{},
			PATCH: When{
				Name: "Private CLI volume rename validation",
				Condition: And(
					HasFields("newname"),
					validatePrivateCLIVolumeRename,
				),
				IsTrue: Allow{
					Name: "Allow private CLI volume rename",
				},
			},
			DELETE: DenyAll{},
		},

		// Private CLI Volume show-footprint - denied (exposes footprint data)
		"/api/private/cli/volume/show-footprint": {
			GET:    Deny{Name: "not allowed"},
			POST:   DenyAll{},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},

		// Private CLI Volume - same controls as standard volume CRUD
		// Must be defined before /api/private/* for exact match to take precedence
		"/api/private/cli/volume": {
			GET: Allow{
				Name: "Allow private CLI volume listing",
				ModifyResponse: RemoveFields{
					Fields: []string{
						"$.percent_used",
						"$.physical_used",
						"$.physical_used_percent",
						"$.total_metadata",
						"$.total_metadata_footprint",
						"$.space_guarantee",
						"$.space_guarantee_enabled",
						"$.space_slo",
						"$.is_space_slo_enabled",
						"$.sis_space_saved",
						"$.sis_space_saved_percent",
						"$.dedupe_space_saved",
						"$.dedupe_space_saved_percent",
						"$.dedupe_space_shared",
						"$.compression_space_saved",
						"$.compression_space_saved_percent",
						"$.performance_tier_inactive_user_data",
						"$.performance_tier_inactive_user_data_percent",
						"$.snapshot_used",
						"$.overprovisioned",
					},
				},
			},
			POST: When{
				Name: "Private CLI volume creation validation",
				Condition: And(
					HasFields("size", "volume", "vserver"),
					IfPresentThenValue("space_guarantee", "none"),
					IfPresentThenEquals("is_space_enforcement_logical", true),
					IfPresentThenEquals("is_space_reporting_logical", true),
					validatePrivateCLIVolumeCreation,
				),
				IsTrue: Allow{
					Name: "Allow private CLI volume creation",
					ModifyRequest: SetRequestFields{
						Fields: map[string]interface{}{
							"is_space_enforcement_logical": true,
							"is_space_reporting_logical":   true,
						},
					},
				},
			},
			PATCH: When{
				Name: "Private CLI volume modification validation",
				Condition: And(
					IfPresentThenValue("space_guarantee", "none"),
					IfPresentThenEquals("is_space_enforcement_logical", true),
					validatePrivateCLIVolumeModification,
				),
				IsTrue: Allow{
					Name: "Allow private CLI volume modification",
				},
			},
			DELETE: When{
				Name:      "Private CLI volume deletion validation",
				Condition: validatePrivateCLIVolumeDeletion,
				IsTrue: Allow{
					Name: "Allow private CLI volume deletion",
				},
			},
		},
		// Private CLI derived route for "volume clone create"
		"/api/private/cli/volume/clone": {
			GET: DenyAll{},
			POST: When{
				Name: "Private CLI volume clone create validation",
				Condition: And(
					HasFields("vserver", "flexclone"),
					HasAtLeastOneOf("parent_volume", "b", "missing required field(s): parent_volume or b"),
					IfPresentThenValue("space_guarantee", "none"),
					validatePrivateCLIVolumeCloneCreate,
				),
				IsTrue: Allow{
					Name: "Allow private CLI volume clone create",
				},
			},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},
		// Private CLI derived route for "volume clone split start"
		"/api/private/cli/volume/clone/split/start": {
			GET: DenyAll{},
			POST: When{
				Name: "Private CLI volume clone split start validation",
				Condition: And(
					HasFields("vserver", "flexclone"),
					validatePrivateCLIVolumeCloneSplit,
				),
				IsTrue: Allow{
					Name: "Allow private CLI volume clone split start",
				},
			},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},
		// Private CLI Vserver - block create/delete, restrict modify (same policy as /api/svm/svms and CLI vserver rules)
		"/api/private/cli/vserver": {
			// Dedicated-hardware (non-FLEX) pools: scope the vserver listing to the
			// requesting account's SVMs. FLEX pools use the plain Allow (unchanged).
			GET: When{
				Name:      "Private CLI vserver listing (account-scoped for dedicated pools)",
				Condition: isDedicatedPoolListing,
				IsTrue: Allow{
					Name:           "Allow private CLI vserver GET (account-scoped)",
					ModifyResponse: DedicatedSvmListFilter{},
				},
				IsFalse: Allow{Name: "Allow private CLI vserver GET"},
			},
			POST: Deny{
				Name: "SVM creation not allowed",
			},
			PATCH: When{
				Name:      "Private CLI vserver modification validation",
				Condition: OnlyAllowFields("language", "snapshot_policy", "quota_policy"),
				IsTrue:    Allow{Name: "Allow private CLI vserver modification"},
			},
			DELETE: Deny{
				Name: "SVM deletion not allowed",
			},
		},
		"/api/private/cli/vserver/object-store-server/bucket": {
			GET: Allow{Name: "Allow private CLI NAS bucket GET"},
			POST: When{
				Name: "Private CLI NAS bucket create validation",
				Condition: And(
					HasFieldValue("type", "nas"),
					HasFields("nas_path"),
				),
				IsTrue: Allow{Name: "Allow private CLI NAS bucket create"},
			},
			PATCH:  Allow{Name: "Allow private CLI NAS bucket PATCH"},
			DELETE: Allow{Name: "Allow private CLI NAS bucket DELETE"},
		},

		// Storage Aggregates
		"/api/storage/aggregates": {
			GET: When{
				Name:      "Aggregate listing dedicated-capacity validation",
				Condition: isDedicatedPoolListing,
				IsTrue:    Allow{Name: "Allow aggregate listing for dedicated-capacity pool"},
				IsFalse:   Deny{Name: "Aggregate listing not allowed"},
			},
			POST:   Deny{Name: "Aggregate creation not allowed"},
			PATCH:  Deny{Name: "Aggregate modification not allowed"},
			DELETE: Deny{Name: "Aggregate deletion not allowed"},
		},

		// Storage Aggregates - specific aggregate
		"/api/storage/aggregates/{uuid}": {
			GET: When{
				Name:      "Aggregate details dedicated-capacity validation",
				Condition: isDedicatedPoolListing,
				IsTrue:    Allow{Name: "Allow aggregate details for dedicated-capacity pool"},
				IsFalse:   Deny{Name: "Aggregate details not allowed"},
			},
			POST:   DenyAll{},
			PATCH:  Deny{Name: "Aggregate modification not allowed"},
			DELETE: Deny{Name: "Aggregate deletion not allowed"},
		},

		// Security Certificates
		"/api/security/certificates": {
			GET: AllowAll{},
			POST: When{
				Name:      "Certificate creation validation",
				Condition: IfPresentThenValue("type", "server", "client", "server_ca", "client_ca"),
				IsTrue:    AllowAll{},
			},
			PATCH:  Deny{Name: "Certificate collection modification not allowed"},
			DELETE: Deny{Name: "Certificate deletion not allowed"},
		},

		// Security Certificates - specific certificate
		"/api/security/certificates/{uuid}": {
			GET:    AllowAll{},
			POST:   DenyAll{},
			PATCH:  AllowAll{},
			DELETE: Deny{Name: "Certificate deletion not allowed"},
		},

		// SnapLock event retention (EBR) policies - bulk delete and bulk update not allowed
		"/api/storage/snaplock/event-retention/policies": {
			DELETE: Deny{Name: "Bulk delete of event retention policies is not allowed"},
			PATCH:  Deny{Name: "Bulk update of event retention policies is not allowed"},
		},

		"/api/protocols/s3/buckets": {
			GET: Allow{Name: "Allow S3 bucket collection GET"},
			POST: When{
				Name: "S3 NAS bucket create validation",
				Condition: And(
					HasFieldValue("type", "nas"),
					HasFields("nas_path"),
				),
				IsTrue: Allow{Name: "Allow NAS S3 bucket create"},
			},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},
		"/api/protocols/s3/services/{uuid}/buckets": {
			GET: Allow{Name: "Allow S3 buckets under SVM service GET"},
			POST: When{
				Name: "S3 NAS bucket create validation (SVM in path)",
				Condition: And(
					HasFieldValue("type", "nas"),
					HasFields("nas_path"),
				),
				IsTrue: Allow{Name: "Allow NAS S3 bucket create"},
			},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},

		// Cluster counter tables - block all (performance/diagnostic data; ONTAP REST counter_table_collection_get, counter_table_get, rows)
		"/api/cluster/counter/tables/*": {
			GET:    Deny{Name: "Cluster counter tables not allowed"},
			POST:   DenyAll{},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},

		// SVM collection - block create, allow listing
		"/api/svm/svms": {
			// Dedicated-hardware (non-FLEX) pools: scope the SVM listing to the
			// requesting account's SVMs. FLEX pools use the plain Allow (unchanged).
			GET: When{
				Name:      "SVM listing (account-scoped for dedicated pools)",
				Condition: isDedicatedPoolListing,
				IsTrue: Allow{
					Name:           "Allow SVM listing (account-scoped)",
					ModifyResponse: DedicatedSvmListFilter{},
				},
				IsFalse: Allow{Name: "Allow SVM listing"},
			},
			POST:   Deny{Name: "SVM creation not allowed"},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},

		// SVM collection alias - ONTAP also serves the SVM collection under
		// /api/svms, so it must carry the same controls as /api/svm/svms. Without
		// this entry the alias falls through to unfiltered passthrough, bypassing
		// the dedicated-pool account scoping applied on /api/svm/svms.
		"/api/svms": {
			GET: When{
				Name:      "SVM listing alias (account-scoped for dedicated pools)",
				Condition: isDedicatedPoolListing,
				IsTrue: Allow{
					Name:           "Allow SVM listing alias (account-scoped)",
					ModifyResponse: DedicatedSvmListFilter{},
				},
				IsFalse: Allow{Name: "Allow SVM listing alias"},
			},
			POST:   Deny{Name: "SVM creation not allowed"},
			PATCH:  DenyAll{},
			DELETE: DenyAll{},
		},

		// SVM instance - restrict modify to allowed fields only
		"/api/svm/svms/{uuid}": {
			GET:  Allow{Name: "Allow SVM details"},
			POST: DenyAll{},
			PATCH: When{
				Name:      "SVM modification validation",
				Condition: OnlyAllowFields("language", "snapshot_policy", "export_policy", "nsswitch"),
				IsTrue:    Allow{Name: "Allow SVM modification"},
			},
			DELETE: Deny{Name: "SVM deletion not allowed"},
		},

		// QoS policy groups. Creation rejects the reserved vserver-wide namespace
		// ({svm}-qos-policy) and fixed-policy minimum throughput. Bulk PATCH/DELETE on
		// the collection is denied (N10).
		"/api/storage/qos/policies": {
			GET: Allow{Name: "Allow QoS policy listing"},
			POST: When{
				Name: "QoS policy creation validation",
				Condition: And(
					rejectReservedQoSPolicyCreate,
					rejectFixedQoSMinimumThroughput,
				),
				IsTrue: Allow{Name: "Allow QoS policy creation"},
			},
			PATCH:  Deny{Name: "Bulk QoS policy modification not allowed"},
			DELETE: Deny{Name: "Bulk QoS policy deletion not allowed"},
		},

		// QoS policy group GET/PATCH/DELETE instance operations are handled by the ogen server.
		// Keep POST denied if it reaches passthrough because ONTAP does not support creating a
		// policy at an instance URI.
		"/api/storage/qos/policies/{uuid}": {
			GET:  Allow{Name: "Allow QoS policy details"},
			POST: DenyAll{},
		},

		// Nested SVM-volume QoS feature toggle (qos settings nested-qos-svm-vol). Customers must
		// not modify/disable this; show remains allowed. Explicit deny is required because
		// unmatched REST paths pass through by default.
		"/api/private/cli/qos/settings/nested-qos-svm-vol": {
			GET:    Allow{Name: "Allow nested SVM volume QoS settings show"},
			POST:   DenyAll{},
			PATCH:  Deny{Name: "Nested SVM volume QoS settings cannot be modified"},
			DELETE: DenyAll{},
		},
		"/api/private/cli/settings/nested-qos-svm-vol": {
			GET:    Allow{Name: "Allow nested SVM volume QoS settings show"},
			POST:   DenyAll{},
			PATCH:  Deny{Name: "Nested SVM volume QoS settings cannot be modified"},
			DELETE: DenyAll{},
		},

		// System node run (nodeshell/systemshell passthrough) — REST equivalent of the CLI
		// "system node run" command. It can bypass proxy access controls, so the private-CLI
		// route and its "run" alias are denied for all methods. Explicit deny is required
		// because unmatched REST paths pass through by default.
		"/api/private/cli/system/node/run": {
			GET:    Deny{Name: "system node run (nodeshell access) is not allowed"},
			POST:   Deny{Name: "system node run (nodeshell access) is not allowed"},
			PATCH:  Deny{Name: "system node run (nodeshell access) is not allowed"},
			DELETE: Deny{Name: "system node run (nodeshell access) is not allowed"},
		},
		"/api/private/cli/run": {
			GET:    Deny{Name: "system node run (nodeshell access) is not allowed"},
			POST:   Deny{Name: "system node run (nodeshell access) is not allowed"},
			PATCH:  Deny{Name: "system node run (nodeshell access) is not allowed"},
			DELETE: Deny{Name: "system node run (nodeshell access) is not allowed"},
		},
	}
}
