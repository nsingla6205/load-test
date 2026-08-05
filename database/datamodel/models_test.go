package datamodel

import (
	"database/sql/driver"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestJSONB_Scan(t *testing.T) {
	var j JSONB
	err := j.Scan([]byte(`{"key": "value"}`))
	assert.NoError(t, err)
	assert.Equal(t, "value", j["key"])

	err = j.Scan(nil)
	assert.NoError(t, err)
	assert.Equal(t, JSONB{}, j)
}

func TestJSONB_Value(t *testing.T) {
	j := JSONB{"key": "value"}
	val, err := j.Value()
	assert.NoError(t, err)
	assert.Equal(t, `{"key":"value"}`, string(val.([]byte)))
}

func TestExternalTempCredentials_Value(t *testing.T) {
	ts := time.Date(2026, time.July, 22, 10, 30, 0, 0, time.UTC)
	etc := ExternalTempCredentials{TempPasswordActive: true, TempPasswordCreatedAt: &ts}

	val, err := etc.Value()
	assert.NoError(t, err)
	assert.JSONEq(t,
		`{"temp_password_active":true,"temp_password_created_at":"2026-07-22T10:30:00Z"}`,
		string(val.([]byte)))
}

func TestExternalTempCredentials_Scan(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		var etc ExternalTempCredentials
		err := etc.Scan([]byte(`{"temp_password_active":true,"temp_password_created_at":"2026-07-22T10:30:00Z"}`))
		assert.NoError(t, err)
		assert.True(t, etc.TempPasswordActive)
		if assert.NotNil(t, etc.TempPasswordCreatedAt) {
			assert.True(t, etc.TempPasswordCreatedAt.Equal(time.Date(2026, time.July, 22, 10, 30, 0, 0, time.UTC)))
		}
	})

	t.Run("nil column leaves zero value", func(t *testing.T) {
		var etc ExternalTempCredentials
		err := etc.Scan(nil)
		assert.NoError(t, err)
		assert.False(t, etc.TempPasswordActive)
		assert.Nil(t, etc.TempPasswordCreatedAt)
	})

	t.Run("wrong type errors", func(t *testing.T) {
		var etc ExternalTempCredentials
		err := etc.Scan("not-bytes")
		assert.Error(t, err)
	})
}

// TestExternalTempCredentials_RoundTrip verifies a Value()->Scan() cycle
// preserves both fields, so the reconciler reads back exactly what the reset
// workflow persisted.
func TestExternalTempCredentials_RoundTrip(t *testing.T) {
	ts := time.Date(2026, time.July, 22, 10, 30, 0, 0, time.UTC)
	original := ExternalTempCredentials{TempPasswordActive: true, TempPasswordCreatedAt: &ts}

	val, err := original.Value()
	assert.NoError(t, err)

	var scanned ExternalTempCredentials
	err = scanned.Scan(val.([]byte))
	assert.NoError(t, err)
	assert.Equal(t, original.TempPasswordActive, scanned.TempPasswordActive)
	if assert.NotNil(t, scanned.TempPasswordCreatedAt) {
		assert.True(t, original.TempPasswordCreatedAt.Equal(*scanned.TempPasswordCreatedAt))
	}
}

func TestClusterDetails_Scan(t *testing.T) {
	var cd ClusterDetails
	err := cd.Scan([]byte(`{"external_name": "test"}`))
	assert.NoError(t, err)
	assert.Equal(t, "test", cd.ExternalName)
}

func TestClusterDetails_Value(t *testing.T) {
	cd := ClusterDetails{ExternalName: "test"}
	val, err := cd.Value()
	assert.NoError(t, err)
	assert.Equal(t, `{"external_name":"test","ontap_version":"","regional_tenant_project":"","sn_host_project":"","network":"","subnet_names":null}`, string(val.([]byte)))
}

func TestVolumeAttributes_Scan(t *testing.T) {
	t.Run("WithCreationToken", func(t *testing.T) {
		var va VolumeAttributes
		err := va.Scan([]byte(`{"creation_token": "token"}`))
		assert.NoError(t, err)
		assert.Equal(t, "token", va.CreationToken)
	})

	t.Run("WithSecurityStyle", func(t *testing.T) {
		var va VolumeAttributes
		err := va.Scan([]byte(`{"creation_token": "token", "security_style": "unix"}`))
		assert.NoError(t, err)
		assert.Equal(t, "token", va.CreationToken)
		assert.Equal(t, "unix", va.SecurityStyle)
	})

	t.Run("WithAllFields", func(t *testing.T) {
		var va VolumeAttributes
		err := va.Scan([]byte(`{
			"creation_token": "token",
			"security_style": "ntfs",
			"mounted": true,
			"snap_reserve": 5
		}`))
		assert.NoError(t, err)
		assert.Equal(t, "token", va.CreationToken)
		assert.Equal(t, "ntfs", va.SecurityStyle)
		assert.True(t, va.Mounted)
		assert.Equal(t, int64(5), va.SnapReserve)
	})
	t.Run("Scan_split_regular_volume_hydration_pending", func(t *testing.T) {
		var va VolumeAttributes
		err := va.Scan([]byte(`{"creation_token":"x","split_regular_volume_hydration_pending":true}`))
		assert.NoError(t, err)
		assert.True(t, va.SplitRegularVolumeHydrationPending)
		assert.Equal(t, "x", va.CreationToken)
	})

	t.Run("WithRestrictedActions", func(t *testing.T) {
		var va VolumeAttributes
		err := va.Scan([]byte(`{"creation_token":"token","restricted_actions":["DELETE"]}`))
		assert.NoError(t, err)
		assert.Equal(t, []string{"DELETE"}, va.RestrictedActions)
	})
}

func TestVolumeAttributes_Value(t *testing.T) {
	t.Run("WithEmptySecurityStyle", func(t *testing.T) {
		va := VolumeAttributes{CreationToken: "token"}
		val, err := va.Value()
		assert.NoError(t, err)
		assert.Equal(t, `{"creation_token":"token","protocols":null,"vendor_subnet_id":"","external_uuid":"","block_properties":null,"block_devices":null,"file_properties":null,"is_data_protection":false,"mounted":false,"snap_reserve":0,"snapshot_directory":false,"kerberos_enabled":false,"ldap_enabled":false,"labels":null,"restored_backup_id":"","restored_backup_path":"","account_name":"","deployment_name":"","is_regional_ha":false,"clone_parent_info":null,"security_style":"","restricted_actions":null}`, string(val.([]byte)))
	})

	t.Run("WithRestrictedActions", func(t *testing.T) {
		va := VolumeAttributes{
			CreationToken:     "token",
			RestrictedActions: []string{"DELETE"},
		}
		val, err := va.Value()
		assert.NoError(t, err)
		assert.Contains(t, string(val.([]byte)), `"restricted_actions":["DELETE"]`)

		var va2 VolumeAttributes
		err = va2.Scan(val)
		assert.NoError(t, err)
		assert.Equal(t, []string{"DELETE"}, va2.RestrictedActions)
	})

	t.Run("WithSecurityStyle", func(t *testing.T) {
		va := VolumeAttributes{
			CreationToken: "token",
			SecurityStyle: "unix",
		}
		val, err := va.Value()
		assert.NoError(t, err)
		assert.Contains(t, string(val.([]byte)), `"security_style":"unix"`)

		// Verify round-trip
		var va2 VolumeAttributes
		err = va2.Scan(val)
		assert.NoError(t, err)
		assert.Equal(t, "unix", va2.SecurityStyle)
		assert.Equal(t, "token", va2.CreationToken)
	})
	t.Run("SplitRegularVolumeHydrationPending_roundTrip", func(t *testing.T) {
		va := VolumeAttributes{
			CreationToken:                      "token",
			SplitRegularVolumeHydrationPending: true,
		}
		val, err := va.Value()
		assert.NoError(t, err)
		assert.Contains(t, string(val.([]byte)), `"split_regular_volume_hydration_pending":true`)

		var va2 VolumeAttributes
		err = va2.Scan(val)
		assert.NoError(t, err)
		assert.True(t, va2.SplitRegularVolumeHydrationPending)
		assert.Equal(t, "token", va2.CreationToken)
	})
}

func TestFileProperties_JSON(t *testing.T) {
	t.Run("WithSecurityStyle", func(t *testing.T) {
		fp := FileProperties{
			SecurityStyle: "unix",
			JunctionPath:  "/vol1",
		}
		jsonData, err := json.Marshal(fp)
		assert.NoError(t, err)
		assert.Contains(t, string(jsonData), `"security_style":"unix"`)
		assert.Contains(t, string(jsonData), `"junction_path":"/vol1"`)

		// Verify round-trip
		var fp2 FileProperties
		err = json.Unmarshal(jsonData, &fp2)
		assert.NoError(t, err)
		assert.Equal(t, "unix", fp2.SecurityStyle)
		assert.Equal(t, "/vol1", fp2.JunctionPath)
	})

	t.Run("WithAllFields", func(t *testing.T) {
		fp := FileProperties{
			SecurityStyle:    "ntfs",
			JunctionPath:     "/vol2",
			Fqdn:             "example.com",
			SMBShareSettings: []string{"share1", "share2"},
			ExportPolicy: &ExportPolicy{
				ExportPolicyName: "policy1",
			},
		}
		jsonData, err := json.Marshal(fp)
		assert.NoError(t, err)
		jsonStr := string(jsonData)
		assert.Contains(t, jsonStr, `"security_style":"ntfs"`)
		assert.Contains(t, jsonStr, `"junction_path":"/vol2"`)
		assert.Contains(t, jsonStr, `"fqdn":"example.com"`)
		assert.Contains(t, jsonStr, `"smb_share_settings":["share1","share2"]`)

		// Verify round-trip
		var fp2 FileProperties
		err = json.Unmarshal(jsonData, &fp2)
		assert.NoError(t, err)
		assert.Equal(t, "ntfs", fp2.SecurityStyle)
		assert.Equal(t, "/vol2", fp2.JunctionPath)
		assert.Equal(t, "example.com", fp2.Fqdn)
		assert.Equal(t, []string{"share1", "share2"}, fp2.SMBShareSettings)
		assert.NotNil(t, fp2.ExportPolicy)
		assert.Equal(t, "policy1", fp2.ExportPolicy.ExportPolicyName)
	})

	t.Run("WithEmptySecurityStyle", func(t *testing.T) {
		fp := FileProperties{
			JunctionPath: "/vol3",
		}
		jsonData, err := json.Marshal(fp)
		assert.NoError(t, err)
		assert.Contains(t, string(jsonData), `"security_style":""`)
	})

	t.Run("UnmarshalFromJSON", func(t *testing.T) {
		jsonData := []byte(`{
			"security_style": "unix",
			"junction_path": "/vol4",
			"fqdn": "test.example.com"
		}`)
		var fp FileProperties
		err := json.Unmarshal(jsonData, &fp)
		assert.NoError(t, err)
		assert.Equal(t, "unix", fp.SecurityStyle)
		assert.Equal(t, "/vol4", fp.JunctionPath)
		assert.Equal(t, "test.example.com", fp.Fqdn)
	})

	t.Run("WithinVolumeAttributes", func(t *testing.T) {
		va := VolumeAttributes{
			CreationToken: "token",
			FileProperties: &FileProperties{
				SecurityStyle: "unix",
				JunctionPath:  "/vol1",
			},
		}
		val, err := va.Value()
		assert.NoError(t, err)
		jsonStr := string(val.([]byte))
		assert.Contains(t, jsonStr, `"security_style":"unix"`)
		assert.Contains(t, jsonStr, `"junction_path":"/vol1"`)

		// Verify round-trip
		var va2 VolumeAttributes
		err = va2.Scan(val)
		assert.NoError(t, err)
		assert.NotNil(t, va2.FileProperties)
		assert.Equal(t, "unix", va2.FileProperties.SecurityStyle)
		assert.Equal(t, "/vol1", va2.FileProperties.JunctionPath)
	})
}

func TestReplicationDetails_Scan(t *testing.T) {
	var rd ReplicationDetails
	err := rd.Scan([]byte(`{"endpoint_type": "type"}`))
	assert.NoError(t, err)
	assert.Equal(t, "type", rd.EndpointType)
}

func TestReplicationDetails_Value(t *testing.T) {
	rd := ReplicationDetails{EndpointType: "type"}
	val, err := rd.Value()
	assert.NoError(t, err)
	assert.Equal(t, `{"endpoint_type":"type","replication_type":"","replication_schedule":"","source_pool_uuid":"","source_volume_uuid":"","source_location":"","source_host_name":"","source_replication_uuid":"","source_svm_name":"","source_volume_name":"","destination_pool_uuid":"","destination_volume_uuid":"","destination_location":"","destination_host_name":"","destination_replication_uuid":"","destination_svm_name":"","destination_volume_name":"","external_uuid":"","labels":null}`, string(val.([]byte)))
}

func TestSnapshotAttributes_Scan(t *testing.T) {
	var sa SnapshotAttributes
	err := sa.Scan([]byte(`{"size_in_bytes": 1024}`))
	assert.NoError(t, err)
	assert.Equal(t, int64(1024), sa.SizeInBytes)
}

func TestSnapshotAttributes_Value(t *testing.T) {
	sa := SnapshotAttributes{SizeInBytes: 1024}
	val, err := sa.Value()
	assert.NoError(t, err)
	assert.Equal(t, `{"size_in_bytes":1024,"external_uuid":"","logical_size_used_in_bytes":0}`, string(val.([]byte)))
}

func TestBucketDetails_mergeNonEmptyFieldsFrom(t *testing.T) {
	t.Run("overlays tenant project and HMAC secret", func(t *testing.T) {
		existing := &BucketDetails{
			BucketName:     "bucket-1",
			VendorSubnetID: "subnet-abc",
		}
		updated := &BucketDetails{
			TenantProjectNumber: "12345",
			HmacSecretID:        "hardware-bv-hmac-sa",
			SatisfiesPzi:        true,
			SatisfiesPzs:        true,
		}

		existing.mergeNonEmptyFieldsFrom(updated)

		assert.Equal(t, "12345", existing.TenantProjectNumber)
		assert.Equal(t, "hardware-bv-hmac-sa", existing.HmacSecretID)
		assert.True(t, existing.SatisfiesPzi)
		assert.True(t, existing.SatisfiesPzs)
		assert.Equal(t, "subnet-abc", existing.VendorSubnetID)
	})

	t.Run("nil receiver or updated is no-op", func(t *testing.T) {
		var nilBucket *BucketDetails
		nilBucket.mergeNonEmptyFieldsFrom(&BucketDetails{HmacSecretID: "secret"})
		(&BucketDetails{}).mergeNonEmptyFieldsFrom(nil)
	})
}

func TestBucketDetailsArray_Scan_InvalidType(t *testing.T) {
	var bda BucketDetailsArray
	err := bda.Scan(12345)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "type assertion to []byte failed")
}

func TestBucketDetailsArray_Value(t *testing.T) {
	bda := BucketDetailsArray{{BucketName: "test-bucket"}}
	val, err := bda.Value()
	assert.NoError(t, err)
	assert.Equal(t, `[{"bucket_name":"test-bucket","service_account_name":"","vendor_subnet_id":"","tenant_project_number":"","satisfies_pzi":false,"satisfies_pzs":false}]`, string(val.([]byte)))
}

func TestBucketDetailsArray_Scan(t *testing.T) {
	tests := []struct {
		name        string
		input       interface{}
		wantErr     bool
		errContains string
		wantResult  BucketDetailsArray
	}{
		{
			name:  "success: valid JSON bytes",
			input: []byte(`[{"bucket_name":"my-bucket","service_account_name":"sa@project.iam","vendor_subnet_id":"subnet-1","tenant_project_number":"123","satisfies_pzi":true,"satisfies_pzs":false}]`),
			wantResult: BucketDetailsArray{{
				BucketName:          "my-bucket",
				ServiceAccountName:  "sa@project.iam",
				VendorSubnetID:      "subnet-1",
				TenantProjectNumber: "123",
				SatisfiesPzi:        true,
				SatisfiesPzs:        false,
			}},
		},
		{
			name:        "error: type assertion to []byte fails",
			input:       "not-a-byte-slice",
			wantErr:     true,
			errContains: "type assertion to []byte failed",
		},
		{
			name:        "error: invalid JSON bytes",
			input:       []byte(`not-valid-json`),
			wantErr:     true,
			errContains: "invalid character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bda BucketDetailsArray
			err := bda.Scan(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantResult, bda)
			}
		})
	}
}

func TestImmutableAttributes_Scan(t *testing.T) {
	var ia ImmutableAttributes
	err := ia.Scan([]byte(`{"isDailyBackupImmutable": true}`))
	assert.NoError(t, err)
	assert.True(t, ia.IsDailyBackupImmutable)
}

func TestImmutableAttributes_Value(t *testing.T) {
	ia := ImmutableAttributes{IsDailyBackupImmutable: true}
	val, err := ia.Value()
	assert.NoError(t, err)
	assert.Equal(t, `{"backupMinimumEnforcedRetentionDuration":null,"isDailyBackupImmutable":true,"isWeeklyBackupImmutable":false,"isMonthlyBackupImmutable":false,"isAdhocBackupImmutable":false}`, string(val.([]byte)))
}

func TestHosts_Scan(t *testing.T) {
	var h Hosts
	err := h.Scan([]byte(`{"hosts": ["host1", "host2"]}`))
	assert.NoError(t, err)
	assert.Equal(t, []string{"host1", "host2"}, h.Hosts)
}

func TestHosts_Value(t *testing.T) {
	h := Hosts{Hosts: []string{"host1", "host2"}}
	val, err := h.Value()
	assert.NoError(t, err)
	assert.Equal(t, `{"hosts":["host1","host2"]}`, string(val.([]byte)))
}

func TestDataProtection_Scan(t *testing.T) {
	var dp DataProtection
	err := dp.Scan([]byte(`{"scheduled_backup_enabled": true, "backup_vault_id": "vault123"}`))
	assert.NoError(t, err)
	assert.NotNil(t, dp.ScheduledBackupEnabled)
	assert.True(t, *dp.ScheduledBackupEnabled)
	assert.Equal(t, "vault123", dp.BackupVaultID)
}

func TestDataProtection_Value(t *testing.T) {
	dp := DataProtection{
		ScheduledBackupEnabled: func(b bool) *bool { return &b }(true),
		BackupVaultID:          "vault123",
	}
	val, err := dp.Value()
	assert.NoError(t, err)
	assert.Equal(t, `{"scheduled_backup_enabled":true,"backup_vault_id":"vault123","backup_policy_id":"","backup_chain_bytes":null,"kms_grant":null}`, string(val.([]byte)))
}

func TestBackupAttributes_GetTotalTransferBytes(t *testing.T) {
	t.Run("NonNilReceiver", func(t *testing.T) {
		ba := &BackupAttributes{TotalTransferBytes: 42}
		assert.Equal(t, int64(42), ba.GetTotalTransferBytes())
	})

	t.Run("NilReceiver", func(t *testing.T) {
		var ba *BackupAttributes
		assert.Equal(t, int64(0), ba.GetTotalTransferBytes())
	})
}

func TestBackupAttributes_Scan(t *testing.T) {
	t.Run("Valid JSON", func(t *testing.T) {
		var ba BackupAttributes
		err := ba.Scan([]byte(`{
			"progress_percentage": 50,
			"bytes_transferred": 1024,
			"backup_policy_name": "policy1",
			"snapshot_id": "snap123",
			"snapshot_name": "snapshot1",
			"snapshot_creation_time": "2023-01-01T00:00:00Z",
			"completion_time": "2023-01-01T01:00:00Z",
			"life_cycle_tracking_id": "track123",
			"constituent_volumes_per_aggregate": "vol1",
			"use_existing_snapshot": true,
			"number_of_aggregates": 2,
			"ontap_volume_style": "flexvol",
			"service_account_name": "service1",
			"endpoint_uuid": "endpoint123",
			"bucket_name": "bucket1",
			"protocols": ["nfs", "cifs"],
			"volume_name": "volume1",
			"account_identifier": "project123"
		}`))
		assert.NoError(t, err)
		assert.Equal(t, "policy1", ba.BackupPolicyName)
		assert.Equal(t, "snap123", ba.SnapshotID)
		assert.Equal(t, "snapshot1", ba.SnapshotName)
		assert.Equal(t, "2023-01-01T00:00:00Z", ba.SnapshotCreationTime)
		assert.Equal(t, "2023-01-01T01:00:00Z", ba.CompletionTime)
		assert.Equal(t, "track123", ba.LifeCycleTrackingID)
		assert.Equal(t, "vol1", ba.ConstituentVolumesPerAggregate)
		assert.True(t, ba.UseExistingSnapshot)
		assert.Equal(t, 2, ba.NumberOfAggregates)
		assert.Equal(t, "flexvol", ba.OntapVolumeStyle)
		assert.Equal(t, "service1", ba.ServiceAccountName)
		assert.Equal(t, "endpoint123", ba.EndpointUUID)
		assert.Equal(t, "bucket1", ba.BucketName)
		assert.Equal(t, []string{"nfs", "cifs"}, ba.Protocols)
		assert.Equal(t, "volume1", ba.VolumeName)
		assert.Equal(t, "project123", ba.AccountIdentifier)
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		var ba BackupAttributes
		err := ba.Scan([]byte(`invalid json`))
		assert.Error(t, err)
	})

	t.Run("Nil Value", func(t *testing.T) {
		var ba BackupAttributes
		err := ba.Scan(nil)
		assert.Error(t, err)
		assert.Equal(t, BackupAttributes{}, ba)
	})
}

func TestBackupAttributes_Value(t *testing.T) {
	ba := BackupAttributes{
		BackupPolicyName:               "policy1",
		SnapshotID:                     "snap123",
		SnapshotName:                   "snapshot1",
		SnapshotCreationTime:           "2023-01-01T00:00:00Z",
		CompletionTime:                 "2023-01-01T01:00:00Z",
		LifeCycleTrackingID:            "track123",
		ConstituentVolumesPerAggregate: "vol1",
		UseExistingSnapshot:            true,
		NumberOfAggregates:             2,
		OntapVolumeStyle:               "flexvol",
		ServiceAccountName:             "service1",
		EndpointUUID:                   "endpoint123",
		BucketName:                     "bucket1",
		Protocols:                      []string{"nfs", "cifs"},
		VolumeName:                     "volume1",
		AccountIdentifier:              "project123",
		EnforcedRetentionDuration:      time.Time{}, // Ensure this field is properly initialized
		ObjectStoreUUID:                "",
		SourceVolumeZone:               "us-central1-a",
		ConstituentCountOfBackup:       0,
		IsRegionalHA:                   false,
		RestoreVolumeCount:             1,
	}

	val, err := ba.Value()
	assert.NoError(t, err)

	expectedJSON := `{"backup_policy_name":"policy1","snapshot_id":"snap123","snapshot_name":"snapshot1","snapshot_creation_time":"2023-01-01T00:00:00Z","completion_time":"2023-01-01T01:00:00Z","life_cycle_tracking_id":"track123","constituent_volumes_per_aggregate":"vol1","delete_initiated":false,"use_existing_snapshot":true,"number_of_aggregates":2,"ontap_volume_style":"flexvol","service_account_name":"service1","endpoint_uuid":"endpoint123","bucket_name":"bucket1","protocols":["nfs","cifs"],"volume_name":"volume1","account_identifier":"project123","enforced_retention_duration":"0001-01-01T00:00:00Z","object_store_uuid":"","source_volume_zone":"us-central1-a","constituent_count_of_backup":0,"is_regional_ha":false,"is_expert_mode_backup":false,"restore_volume_count":1,"total_transfer_bytes":0,"volume_account_name":""}`
	assert.JSONEq(t, expectedJSON, string(val.([]byte)))
}

func TestBackupVault_ExternalUUID(t *testing.T) {
	t.Run("ExternalUUID_WithValue", func(t *testing.T) {
		externalUUID := "vault-12345-abcde-67890"
		backupVault := BackupVault{
			BaseModel:             BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:                  "test-vault",
			AccountID:             123,
			RegionName:            "us-central1",
			LifeCycleState:        "CREATING",
			LifeCycleStateDetails: "Backup vault is being created",
			BackupVaultType:       "LOCAL",
			AccountVendorID:       "project-12345",
			ExternalUUID:          &externalUUID,
		}

		// Test that ExternalUUID is properly set
		assert.NotNil(t, backupVault.ExternalUUID)
		assert.Equal(t, "vault-12345-abcde-67890", *backupVault.ExternalUUID)

		// Test JSON marshaling includes ExternalUUID with correct JSON tag
		jsonData, err := json.Marshal(backupVault)
		assert.NoError(t, err)

		var unmarshaled map[string]interface{}
		err = json.Unmarshal(jsonData, &unmarshaled)
		assert.NoError(t, err)
		assert.Equal(t, "vault-12345-abcde-67890", unmarshaled["externalUuid"])
	})

	t.Run("ExternalUUID_Nil", func(t *testing.T) {
		backupVault := BackupVault{
			BaseModel:             BaseModel{ID: 2, UUID: "backup-vault-uuid-2"},
			Name:                  "test-vault-2",
			AccountID:             456,
			RegionName:            "us-west1",
			LifeCycleState:        "ACTIVE",
			LifeCycleStateDetails: "Backup vault is active",
			BackupVaultType:       "CROSS_REGION",
			AccountVendorID:       "project-67890",
			ExternalUUID:          nil,
		}

		// Test that ExternalUUID can be nil
		assert.Nil(t, backupVault.ExternalUUID)

		// Test JSON marshaling with nil ExternalUUID
		jsonData, err := json.Marshal(backupVault)
		assert.NoError(t, err)

		var unmarshaled map[string]interface{}
		err = json.Unmarshal(jsonData, &unmarshaled)
		assert.NoError(t, err)

		// Nil pointer should serialize as null in JSON
		externalUuidValue, exists := unmarshaled["externalUuid"]
		assert.True(t, exists)
		assert.Nil(t, externalUuidValue)
	})

	t.Run("ExternalUUID_JSONUnmarshaling", func(t *testing.T) {
		// Test unmarshaling JSON with ExternalUUID
		jsonWithExternalUUID := `{
			"id": 3,
			"uuid": "backup-vault-uuid-3",
			"name": "test-vault-from-json",
			"regionName": "europe-west1",
			"lifeCycleState": "ACTIVE",
			"lifeCycleStateDetails": "Ready for backups",
			"backupVaultType": "LOCAL",
			"accountVendorID": "project-json-test",
			"externalUuid": "json-vault-uuid-12345"
		}`

		var backupVault BackupVault
		err := json.Unmarshal([]byte(jsonWithExternalUUID), &backupVault)
		assert.NoError(t, err)
		assert.NotNil(t, backupVault.ExternalUUID)
		assert.Equal(t, "json-vault-uuid-12345", *backupVault.ExternalUUID)

		// Test unmarshaling JSON with null ExternalUUID
		jsonWithNullExternalUUID := `{
			"id": 4,
			"uuid": "backup-vault-uuid-4",
			"name": "test-vault-null-external-uuid",
			"regionName": "asia-southeast1",
			"lifeCycleState": "CREATING",
			"externalUuid": null
		}`

		var backupVaultWithNull BackupVault
		err = json.Unmarshal([]byte(jsonWithNullExternalUUID), &backupVaultWithNull)
		assert.NoError(t, err)
		assert.Nil(t, backupVaultWithNull.ExternalUUID)
	})

	t.Run("ExternalUUID_FieldModification", func(t *testing.T) {
		backupVault := BackupVault{
			BaseModel:    BaseModel{ID: 5, UUID: "backup-vault-uuid-5"},
			Name:         "modifiable-vault",
			ExternalUUID: nil,
		}

		// Initially nil
		assert.Nil(t, backupVault.ExternalUUID)

		// Set a value
		newExternalUUID := "new-external-uuid-67890"
		backupVault.ExternalUUID = &newExternalUUID
		assert.NotNil(t, backupVault.ExternalUUID)
		assert.Equal(t, "new-external-uuid-67890", *backupVault.ExternalUUID)

		// Modify the value
		modifiedExternalUUID := "modified-external-uuid-12345"
		backupVault.ExternalUUID = &modifiedExternalUUID
		assert.Equal(t, "modified-external-uuid-12345", *backupVault.ExternalUUID)

		// Set back to nil
		backupVault.ExternalUUID = nil
		assert.Nil(t, backupVault.ExternalUUID)
	})
}

func TestPoolUniqueConstraint(t *testing.T) {
	// Test that the GORM tags are set up correctly for composite unique constraint
	pool1 := &Pool{
		AccountID:      123,
		DeploymentName: "gcp-a1b2c3d4e5f67890",
	}

	pool2 := &Pool{
		AccountID:      456,                    // Different account
		DeploymentName: "gcp-a1b2c3d4e5f67890", // Same deployment name - should be allowed
	}

	pool3 := &Pool{
		AccountID:      123,                    // Same account as pool1
		DeploymentName: "gcp-b2c3d4e5f6789012", // Different deployment name - should be allowed
	}

	// Verify the structures are set up correctly
	assert.Equal(t, int64(123), pool1.AccountID)
	assert.Equal(t, "gcp-a1b2c3d4e5f67890", pool1.DeploymentName)

	assert.Equal(t, int64(456), pool2.AccountID)
	assert.Equal(t, "gcp-a1b2c3d4e5f67890", pool2.DeploymentName)

	assert.Equal(t, int64(123), pool3.AccountID)
	assert.Equal(t, "gcp-b2c3d4e5f6789012", pool3.DeploymentName)

	// The actual uniqueness constraint will be enforced by the database
	// This test just verifies the structure is correct
}

func TestNodeNodeGroupMap_Fields(t *testing.T) {
	createdAt := time.Now()
	updatedAt := createdAt.Add(time.Hour)
	m := NodeNodeGroupMap{
		BaseModel:   BaseModel{ID: 1, UUID: "uuid-1", CreatedAt: createdAt, UpdatedAt: updatedAt},
		NodeID:      123,
		NodeGroupID: 456,
		NodeGroup:   &NodeGroup{},
	}
	assert.Equal(t, int64(1), m.ID)
	assert.Equal(t, "uuid-1", m.UUID)
	assert.Equal(t, int64(123), m.NodeID)
	assert.Equal(t, int64(456), m.NodeGroupID)
	assert.Equal(t, createdAt, m.CreatedAt)
	assert.Equal(t, updatedAt, m.UpdatedAt)
}

func TestCacheParameters(t *testing.T) {
	t.Run("ValueSuccess", func(tt *testing.T) {
		cp := CacheParameters{
			PeerClusterName: "cluster-name",
		}
		val, err := cp.Value()
		assert.NoError(tt, err)
		assert.Equal(tt, `{"peer_cluster_name":"cluster-name","peer_svm_name":"","peer_volume_name":"","peer_ip_addresses":null,"cache_state":"","previous_cache_state":"","cache_state_details":"","cache_state_details_code":0,"passphrase":null,"command":null,"peer_command_expiry":null}`, string(val.([]byte)))
	})
	t.Run("ScanSuccess", func(tt *testing.T) {
		var cp CacheParameters
		err := cp.Scan([]byte(`{"peer_cluster_name": "cluster-name"}`))
		assert.NoError(tt, err)
		assert.Equal(tt, "cluster-name", cp.PeerClusterName)
	})
	t.Run("ScanError", func(tt *testing.T) {
		var cp CacheParameters
		err := cp.Scan([]byte(`invalid json`))
		assert.Error(tt, err)
		assert.Equal(tt, CacheParameters{}, cp)
	})
	t.Run("ScanNil", func(tt *testing.T) {
		var cp CacheParameters
		err := cp.Scan(nil)
		assert.NoError(tt, err)
		assert.Equal(tt, CacheParameters{}, cp)
	})
}

func TestUpgradeErrorDetails_Scan(t *testing.T) {
	t.Run("ValidJSON", func(t *testing.T) {
		var ued UpgradeErrorDetails
		jsonData := []byte(`{"errorCode": "UPGRADE_FAILED", "errorMessage": "Test error", "errorType": "UPGRADE_ERROR", "retryable": true, "stackTrace": "trace"}`)
		err := ued.Scan(jsonData)
		assert.NoError(t, err)
		assert.Equal(t, "UPGRADE_FAILED", ued.ErrorCode)
		assert.Equal(t, "Test error", ued.ErrorMessage)
		assert.Equal(t, "UPGRADE_ERROR", ued.ErrorType)
		assert.True(t, ued.Retryable)
		assert.Equal(t, "trace", ued.StackTrace)
	})

	t.Run("ScanNil", func(t *testing.T) {
		var ued UpgradeErrorDetails
		err := ued.Scan(nil)
		assert.NoError(t, err)
		assert.Equal(t, UpgradeErrorDetails{}, ued)
	})

	t.Run("InvalidType", func(t *testing.T) {
		var ued UpgradeErrorDetails
		err := ued.Scan("not a byte slice")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "type assertion to []byte failed")
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		var ued UpgradeErrorDetails
		invalidJSON := []byte(`{"invalid": json}`)
		err := ued.Scan(invalidJSON)
		assert.Error(t, err)
	})
}

func TestUpgradeErrorDetails_Value(t *testing.T) {
	t.Run("ValidData", func(t *testing.T) {
		ued := UpgradeErrorDetails{
			ErrorCode:    "UPGRADE_FAILED",
			ErrorMessage: "Test error",
			ErrorType:    "UPGRADE_ERROR",
			Retryable:    true,
			StackTrace:   "trace",
		}
		val, err := ued.Value()
		assert.NoError(t, err)
		assert.NotNil(t, val)

		// Verify it's a byte slice
		bytes, ok := val.([]byte)
		assert.True(t, ok)
		assert.Contains(t, string(bytes), "UPGRADE_FAILED")
		assert.Contains(t, string(bytes), "Test error")
	})

	t.Run("EmptyData", func(t *testing.T) {
		ued := UpgradeErrorDetails{}
		val, err := ued.Value()
		assert.NoError(t, err)
		assert.NotNil(t, val)

		// Should still produce valid JSON
		bytes, ok := val.([]byte)
		assert.True(t, ok)
		assert.Equal(t, `{"errorCode":"","errorMessage":"","errorType":"","retryable":false}`, string(bytes))
	})
}

func TestResourceAttributes_Scan(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected ResourceAttributes
		wantErr  bool
	}{
		{
			name:  "valid JSON bytes",
			input: []byte(`{"pool_id": 123}`),
			expected: ResourceAttributes{
				PoolID: 123,
			},
			wantErr: false,
		},
		{
			name:     "nil input",
			input:    nil,
			expected: ResourceAttributes{},
			wantErr:  false,
		},
		{
			name:     "invalid type",
			input:    "not bytes",
			expected: ResourceAttributes{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ra ResourceAttributes
			err := ra.Scan(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, ra)
			}
		})
	}
}

func TestResourceAttributes_Value(t *testing.T) {
	ra := ResourceAttributes{
		PoolID: 123,
	}

	value, err := ra.Value()
	assert.NoError(t, err)
	assert.NotNil(t, value)

	// Verify it can be unmarshaled back
	var result ResourceAttributes
	err = json.Unmarshal(value.([]byte), &result)
	assert.NoError(t, err)
	assert.Equal(t, ra, result)
}

func TestExpertModeVolumeAttributes_ScanAndValue(t *testing.T) {
	t.Run("ScanNilResetsStruct", func(t *testing.T) {
		attr := ExpertModeVolumeAttributes{
			IsFlexclone: true,
		}
		err := attr.Scan(nil)
		assert.NoError(t, err)
		assert.Equal(t, ExpertModeVolumeAttributes{}, attr)
	})

	t.Run("ScanInvalidTypeReturnsError", func(t *testing.T) {
		var attr ExpertModeVolumeAttributes
		err := attr.Scan(12345)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "type assertion to []byte failed")
	})

	t.Run("ScanStringJSONIgnoresLegacySharedBytesKey", func(t *testing.T) {
		var attr ExpertModeVolumeAttributes
		err := attr.Scan(`{"isFlexclone":true,"sharedBytes":7}`)
		assert.NoError(t, err)
		assert.True(t, attr.IsFlexclone)
	})

	t.Run("ScanValidJSONAndValueRoundTrip", func(t *testing.T) {
		var attr ExpertModeVolumeAttributes
		input := []byte(`{"isFlexclone":true,"sharedBytes":42,"clone":{"parentVolume":{"uuid":"pv-1","name":"pv"},"parentSnapshot":{"uuid":"ps-1","name":"snap"}}}`)
		err := attr.Scan(input)
		assert.NoError(t, err)
		assert.True(t, attr.IsFlexclone)
		assert.Equal(t, "pv-1", attr.Clone.ParentVolume.UUID)
		assert.Equal(t, "ps-1", attr.Clone.ParentSnapshot.UUID)

		val, err := attr.Value()
		assert.NoError(t, err)
		assert.NotNil(t, val)
	})
}

func TestPoolBuildInfo_Scan(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected PoolBuildInfo
		wantErr  bool
	}{
		{
			name:  "valid JSON bytes with vmrsVersion",
			input: []byte(`{"vsaBuildImage": "vsa-image:latest", "mediatorBuildImage": "mediator-image:latest", "ontapVersion": "9.18.1", "vmrsVersion": "vmrs_gcp.yaml", "buildTimestamp": "2023-01-01T00:00:00Z"}`),
			expected: PoolBuildInfo{
				VSABuildImage:      "vsa-image:latest",
				MediatorBuildImage: "mediator-image:latest",
				OntapVersion:       "9.18.1",
				VMRSVersion:        "vmrs_gcp.yaml",
				BuildTimestamp:     time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			wantErr: false,
		},
		{
			name:  "valid JSON bytes",
			input: []byte(`{"vsaBuildImage": "vsa-image:latest", "mediatorBuildImage": "mediator-image:latest", "ontapVersion": "9.17.1", "buildTimestamp": "2023-01-01T00:00:00Z"}`),
			expected: PoolBuildInfo{
				VSABuildImage:      "vsa-image:latest",
				MediatorBuildImage: "mediator-image:latest",
				OntapVersion:       "9.17.1",
				BuildTimestamp:     time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			wantErr: false,
		},
		{
			name:     "nil input",
			input:    nil,
			expected: PoolBuildInfo{},
			wantErr:  false,
		},
		{
			name:     "invalid type",
			input:    "not bytes",
			expected: PoolBuildInfo{},
			wantErr:  true,
		},
		{
			name:     "invalid JSON",
			input:    []byte(`{"invalid": json}`),
			expected: PoolBuildInfo{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pbi PoolBuildInfo
			err := pbi.Scan(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, pbi)
			}
		})
	}
}

func TestPoolBuildInfo_Value(t *testing.T) {
	pbi := PoolBuildInfo{
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		OntapVersion:       "9.17.1",
		VMRSVersion:        "vmrs_gcp_v2.yaml",
		BuildTimestamp:     time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		RbacFileHash:       "hash123",
		RbacFileUrl:        "gs://example.com/rbac",
	}

	value, err := pbi.Value()
	assert.NoError(t, err)
	assert.NotNil(t, value)

	// Verify it can be unmarshaled back
	var result PoolBuildInfo
	err = json.Unmarshal(value.([]byte), &result)
	assert.NoError(t, err)
	assert.Equal(t, pbi, result)

	// Test with empty struct
	emptyPbi := PoolBuildInfo{}
	value, err = emptyPbi.Value()
	assert.NoError(t, err)
	assert.NotNil(t, value)

	// Should produce valid JSON
	bytes, ok := value.([]byte)
	assert.True(t, ok)
	assert.Equal(t, `{"vsaBuildImage":"","mediatorBuildImage":"","ontapVersion":"","buildTimestamp":"0001-01-01T00:00:00Z","rbacFileHash":"","rbacFileUrl":""}`, string(bytes))
}

func TestClusterPeeringAttributes_Scan(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected ClusterPeeringAttributes
		wantErr  bool
	}{
		{
			name:  "valid JSON bytes with all fields",
			input: []byte(`{"pass_phrase": "secret123", "command": "create", "expiry_time": "2023-12-31T23:59:59Z", "cluster_location": "us-west-1"}`),
			expected: ClusterPeeringAttributes{
				PassPhrase:      stringPtr("secret123"),
				Command:         stringPtr("create"),
				ExpiryTime:      timePtr(time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC)),
				ClusterLocation: stringPtr("us-west-1"),
			},
			wantErr: false,
		},
		{
			name:  "valid JSON bytes with partial fields",
			input: []byte(`{"command": "delete", "cluster_location": "us-east-1"}`),
			expected: ClusterPeeringAttributes{
				PassPhrase:      nil,
				Command:         stringPtr("delete"),
				ExpiryTime:      nil,
				ClusterLocation: stringPtr("us-east-1"),
			},
			wantErr: false,
		},
		{
			name:  "valid JSON bytes with empty object",
			input: []byte(`{}`),
			expected: ClusterPeeringAttributes{
				PassPhrase:      nil,
				Command:         nil,
				ExpiryTime:      nil,
				ClusterLocation: nil,
			},
			wantErr: false,
		},
		{
			name:     "nil input",
			input:    nil,
			expected: ClusterPeeringAttributes{},
			wantErr:  false,
		},
		{
			name:     "invalid type - string instead of bytes",
			input:    "not bytes",
			expected: ClusterPeeringAttributes{},
			wantErr:  true,
		},
		{
			name:     "invalid JSON format",
			input:    []byte(`{"invalid": json}`),
			expected: ClusterPeeringAttributes{},
			wantErr:  true,
		},
		{
			name:     "invalid JSON - missing closing brace",
			input:    []byte(`{"command": "test"`),
			expected: ClusterPeeringAttributes{},
			wantErr:  true,
		},
		{
			name:  "valid JSON with null values",
			input: []byte(`{"pass_phrase": null, "command": "test", "expiry_time": null, "cluster_location": null}`),
			expected: ClusterPeeringAttributes{
				PassPhrase:      nil,
				Command:         stringPtr("test"),
				ExpiryTime:      nil,
				ClusterLocation: nil,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cpr ClusterPeeringAttributes
			err := cpr.Scan(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, cpr)
			}
		})
	}
}

func TestClusterPeeringAttributes_Value(t *testing.T) {
	tests := []struct {
		name    string
		input   ClusterPeeringAttributes
		wantErr bool
	}{
		{
			name: "all fields populated",
			input: ClusterPeeringAttributes{
				PassPhrase:      stringPtr("secret123"),
				Command:         stringPtr("create"),
				ExpiryTime:      timePtr(time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC)),
				ClusterLocation: stringPtr("us-west-1"),
			},
			wantErr: false,
		},
		{
			name: "partial fields populated",
			input: ClusterPeeringAttributes{
				Command:         stringPtr("delete"),
				ClusterLocation: stringPtr("us-east-1"),
			},
			wantErr: false,
		},
		{
			name:    "empty struct",
			input:   ClusterPeeringAttributes{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := tt.input.Value()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, value)

				// Verify it can be scanned back
				var result ClusterPeeringAttributes
				err = result.Scan(value)
				assert.NoError(t, err)
				assert.Equal(t, tt.input, result)
			}
		})
	}
}

// Helper functions for creating pointers
func stringPtr(s string) *string {
	return &s
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func TestExpertModeCredentials_ValueAndScan(t *testing.T) {
	emc := ExpertModeCredentials{
		ExpertModeCredential: []*ExpertModeCredential{
			{
				SecretID:      "secret1",
				CertificateID: "cert1",
				Password:      "pass",
				Username:      "user",
				AuthType:      1,
			},
		},
	}
	val, err := emc.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	var emc2 ExpertModeCredentials
	err = emc2.Scan(val)
	assert.NoError(t, err)
	assert.Equal(t, emc, emc2)

	// Scan nil
	var emc3 ExpertModeCredentials
	err = emc3.Scan(nil)
	assert.NoError(t, err)
	assert.Equal(t, ExpertModeCredentials{}, emc3)

	// Scan wrong type
	err = emc3.Scan("not bytes")
	assert.Error(t, err)
}

// Unit tests for ExpertModeCredential's Value and Scan methods
func TestExpertModeCredential_ValueAndScan(t *testing.T) {
	cred := ExpertModeCredential{
		SecretID:      "secret1",
		CertificateID: "cert1",
		Password:      "pass",
		Username:      "user",
		AuthType:      1,
	}
	val, err := cred.Value()
	assert.NoError(t, err)
	assert.NotNil(t, val)

	var cred2 ExpertModeCredential
	err = cred2.Scan(val)
	assert.NoError(t, err)
	assert.Equal(t, cred, cred2)

	// Scan nil
	var cred3 ExpertModeCredential
	err = cred3.Scan(nil)
	assert.NoError(t, err)
	assert.Equal(t, ExpertModeCredential{}, cred3)

	// Scan wrong type
	err = cred3.Scan("not bytes")
	assert.Error(t, err)
}

func TestAccountMetadata_Scan(t *testing.T) {
	t.Run("WhenValueIsNil", func(tt *testing.T) {
		var am AccountMetadata
		err := am.Scan(nil)

		assert.NoError(tt, err)
		assert.Equal(tt, AccountMetadata{}, am)
		assert.True(tt, am.VolumeRefreshWorkflowLastCompletionAt.IsZero())
	})

	t.Run("WhenValueIsValidJSON", func(tt *testing.T) {
		now := time.Now()
		jsonData := map[string]interface{}{
			"volumeRefreshWorkflowLastCompletionAt": now.Format(time.RFC3339Nano),
		}
		jsonBytes, err := json.Marshal(jsonData)
		assert.NoError(tt, err)

		var am AccountMetadata
		err = am.Scan(jsonBytes)

		assert.NoError(tt, err)
		assert.False(tt, am.VolumeRefreshWorkflowLastCompletionAt.IsZero())
		// Compare with a small tolerance for time parsing
		assert.WithinDuration(tt, now, am.VolumeRefreshWorkflowLastCompletionAt, time.Second)
	})

	t.Run("WhenValueIsEmptyJSON", func(tt *testing.T) {
		jsonBytes := []byte("{}")

		var am AccountMetadata
		err := am.Scan(jsonBytes)

		assert.NoError(tt, err)
		assert.True(tt, am.VolumeRefreshWorkflowLastCompletionAt.IsZero())
	})

	t.Run("WhenValueIsInvalidJSON", func(tt *testing.T) {
		invalidJSON := []byte("invalid json")

		var am AccountMetadata
		err := am.Scan(invalidJSON)

		assert.Error(tt, err)
	})

	t.Run("WhenValueIsNotByteSlice", func(tt *testing.T) {
		var am AccountMetadata
		err := am.Scan("not a byte slice")

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "type assertion to []byte failed")
	})

	t.Run("WhenValueIsInteger", func(tt *testing.T) {
		var am AccountMetadata
		err := am.Scan(12345)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "type assertion to []byte failed")
	})
}

func TestAccountMetadata_Value(t *testing.T) {
	t.Run("WhenAccountMetadataIsEmpty", func(tt *testing.T) {
		am := AccountMetadata{}

		value, err := am.Value()

		assert.NoError(tt, err)
		assert.NotNil(tt, value)

		// Verify it's valid JSON
		var result map[string]interface{}
		err = json.Unmarshal(value.([]byte), &result)
		assert.NoError(tt, err)
	})

	t.Run("WhenAccountMetadataHasTimestamp", func(tt *testing.T) {
		now := time.Now()
		am := AccountMetadata{
			VolumeRefreshWorkflowLastCompletionAt: now,
		}

		value, err := am.Value()

		assert.NoError(tt, err)
		assert.NotNil(tt, value)

		// Verify it's valid JSON and contains the timestamp
		var result map[string]interface{}
		err = json.Unmarshal(value.([]byte), &result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "volumeRefreshWorkflowLastCompletionAt")
	})

	t.Run("WhenAccountMetadataHasZeroTimestamp", func(tt *testing.T) {
		am := AccountMetadata{
			VolumeRefreshWorkflowLastCompletionAt: time.Time{},
		}

		value, err := am.Value()

		assert.NoError(tt, err)
		assert.NotNil(tt, value)

		// Verify it's valid JSON
		var result map[string]interface{}
		err = json.Unmarshal(value.([]byte), &result)
		assert.NoError(tt, err)
	})

	t.Run("RoundTripScanAndValue", func(tt *testing.T) {
		// Test that Scan and Value are inverse operations
		now := time.Now().UTC().Truncate(time.Millisecond) // Truncate for comparison
		original := AccountMetadata{
			VolumeRefreshWorkflowLastCompletionAt: now,
		}

		// Convert to driver.Value
		value, err := original.Value()
		assert.NoError(tt, err)

		// Scan back
		var scanned AccountMetadata
		err = scanned.Scan(value)
		assert.NoError(tt, err)

		// Compare timestamps (with small tolerance for JSON serialization)
		assert.WithinDuration(tt, original.VolumeRefreshWorkflowLastCompletionAt,
			scanned.VolumeRefreshWorkflowLastCompletionAt, time.Second)
	})
}

func TestAccountMetadata_DriverValue(t *testing.T) {
	t.Run("WhenUsedAsDriverValue", func(tt *testing.T) {
		now := time.Now()
		am := AccountMetadata{
			VolumeRefreshWorkflowLastCompletionAt: now,
		}

		// Ensure it implements driver.Valuer
		var _ driver.Valuer = am

		value, err := am.Value()
		assert.NoError(tt, err)
		assert.NotNil(tt, value)

		// Verify the value is []byte
		_, ok := value.([]byte)
		assert.True(tt, ok, "Value should return []byte")
	})
}

func TestAccountMetadata_ScanInterface(t *testing.T) {
	t.Run("WhenUsedAsSQLScanner", func(tt *testing.T) {
		var am AccountMetadata

		// Ensure it implements sql.Scanner
		// sql.Scanner interface is satisfied by the Scan method
		jsonData := []byte(`{"volumeRefreshWorkflowLastCompletionAt":"2024-01-01T12:00:00Z"}`)
		err := am.Scan(jsonData)

		assert.NoError(tt, err)
		assert.False(tt, am.VolumeRefreshWorkflowLastCompletionAt.IsZero())
	})
}

func TestPoolCredentials_GetCaURIWithFallback(t *testing.T) {
	t.Run("WhenPoolCredentialsIsNil_ShouldReturnEnvFallback", func(t *testing.T) {
		var pc *PoolCredentials = nil
		result := pc.GetCaURIWithFallback()
		// Should return result from env.BuildCaURI("", "", "")
		assert.NotNil(t, result)
	})

	t.Run("WhenCaURIIsEmpty_ShouldReturnEnvFallback", func(t *testing.T) {
		pc := &PoolCredentials{
			CaURI: "",
		}
		result := pc.GetCaURIWithFallback()
		// Should return result from env.BuildCaURI("", "", "")
		assert.NotNil(t, result)
	})

	t.Run("WhenCaURIHasValue_ShouldReturnCaURI", func(t *testing.T) {
		pc := &PoolCredentials{
			CaURI: "project-123/pool-456/ca-789",
		}
		result := pc.GetCaURIWithFallback()
		assert.Equal(t, "project-123/pool-456/ca-789", result)
	})
}

func TestPoolCredentials_ParseCaURIWithFallback(t *testing.T) {
	t.Run("WhenPoolCredentialsIsNil_ShouldReturnEnvFallback", func(t *testing.T) {
		var pc *PoolCredentials = nil
		projectID, poolName, caName := pc.ParseCaURIWithFallback()
		// Should return env vars directly
		assert.NotNil(t, projectID)
		assert.NotNil(t, poolName)
		assert.NotNil(t, caName)
	})

	t.Run("WhenCaURIIsEmpty_ShouldReturnEnvFallback", func(t *testing.T) {
		pc := &PoolCredentials{
			CaURI: "",
		}
		projectID, poolName, caName := pc.ParseCaURIWithFallback()
		// Should return env vars directly
		assert.NotNil(t, projectID)
		assert.NotNil(t, poolName)
		assert.NotNil(t, caName)
	})

	t.Run("WhenCaURIHasValue_ShouldParseCaURI", func(t *testing.T) {
		pc := &PoolCredentials{
			CaURI: "project-123/pool-456/ca-789",
		}
		projectID, poolName, caName := pc.ParseCaURIWithFallback()
		assert.Equal(t, "project-123", projectID)
		assert.Equal(t, "pool-456", poolName)
		assert.Equal(t, "ca-789", caName)
	})
}

func TestCmekAttributes_Scan(t *testing.T) {
	t.Run("Valid JSON bytes", func(t *testing.T) {
		var cmek CmekAttributes
		err := cmek.Scan([]byte(`{"kmsConfigResourcePath": "projects/test/locations/us/keyRings/kr/cryptoKeys/key", "encryptionState": "ENABLED", "backupsPrimaryKeyVersion": "1"}`))
		assert.NoError(t, err)
		assert.NotNil(t, cmek.KmsConfigResourcePath)
		assert.Equal(t, "projects/test/locations/us/keyRings/kr/cryptoKeys/key", *cmek.KmsConfigResourcePath)
		assert.NotNil(t, cmek.EncryptionState)
		assert.Equal(t, "ENABLED", *cmek.EncryptionState)
		assert.NotNil(t, cmek.BackupsPrimaryKeyVersion)
		assert.Equal(t, "1", *cmek.BackupsPrimaryKeyVersion)
	})

	t.Run("Nil value", func(t *testing.T) {
		var cmek CmekAttributes
		err := cmek.Scan(nil)
		assert.NoError(t, err)
		assert.Nil(t, cmek.KmsConfigResourcePath)
		assert.Nil(t, cmek.EncryptionState)
		assert.Nil(t, cmek.BackupsPrimaryKeyVersion)
	})

	t.Run("Invalid type", func(t *testing.T) {
		var cmek CmekAttributes
		err := cmek.Scan("not bytes")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "type assertion to []byte failed")
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		var cmek CmekAttributes
		err := cmek.Scan([]byte(`{"invalid": json}`))
		assert.Error(t, err)
	})
}

func TestCmekAttributes_Value(t *testing.T) {
	t.Run("With all fields", func(t *testing.T) {
		kmsPath := "projects/test/locations/us/keyRings/kr/cryptoKeys/key"
		encryptionState := "ENABLED"
		keyVersion := "1"
		cmek := CmekAttributes{
			KmsConfigResourcePath:    &kmsPath,
			EncryptionState:          &encryptionState,
			BackupsPrimaryKeyVersion: &keyVersion,
		}
		val, err := cmek.Value()
		assert.NoError(t, err)
		assert.NotNil(t, val)

		// Verify it can be scanned back
		var result CmekAttributes
		err = result.Scan(val)
		assert.NoError(t, err)
		assert.Equal(t, cmek, result)
	})

	t.Run("With partial fields", func(t *testing.T) {
		kmsPath := "projects/test/locations/us/keyRings/kr/cryptoKeys/key"
		cmek := CmekAttributes{
			KmsConfigResourcePath: &kmsPath,
		}
		val, err := cmek.Value()
		assert.NoError(t, err)
		assert.NotNil(t, val)

		// Verify it can be scanned back
		var result CmekAttributes
		err = result.Scan(val)
		assert.NoError(t, err)
		assert.Equal(t, cmek, result)
	})

	t.Run("Empty struct", func(t *testing.T) {
		cmek := CmekAttributes{}
		val, err := cmek.Value()
		assert.NoError(t, err)
		assert.NotNil(t, val)

		// Should produce valid JSON
		bytes, ok := val.([]byte)
		assert.True(t, ok)
		assert.Equal(t, `{"kmsConfigResourcePath":null,"encryptionState":null,"backupsPrimaryKeyVersion":null}`, string(bytes))
	})
}

func TestJobAttributes_Scan(t *testing.T) {
	t.Run("Valid JSON bytes with NewKmsKeyURL", func(t *testing.T) {
		var ja JobAttributes
		err := ja.Scan([]byte(`{"resource_uuid": "vault-uuid", "location": "us-central1", "kms_attributes": {"new_kms_key_url": "projects/test/locations/us/keyRings/test/cryptoKeys/key1"}}`))
		assert.NoError(t, err)
		assert.Equal(t, "vault-uuid", ja.ResourceUUID)
		assert.Equal(t, "us-central1", ja.Location)
		assert.NotNil(t, ja.KmsAttributes)
		assert.Equal(t, "projects/test/locations/us/keyRings/test/cryptoKeys/key1", ja.KmsAttributes.NewKmsKeyURL)
	})
	t.Run("Valid JSON bytes without NewKmsKeyURL", func(t *testing.T) {
		var ja JobAttributes
		err := ja.Scan([]byte(`{"resource_uuid": "vault-uuid", "location": "us-central1"}`))
		assert.NoError(t, err)
		assert.Equal(t, "vault-uuid", ja.ResourceUUID)
		assert.Equal(t, "us-central1", ja.Location)
		assert.Nil(t, ja.KmsAttributes)
	})
	t.Run("Nil value", func(t *testing.T) {
		var ja JobAttributes
		err := ja.Scan(nil)
		assert.NoError(t, err)
		assert.Equal(t, JobAttributes{}, ja)
	})
	t.Run("Invalid type", func(t *testing.T) {
		var ja JobAttributes
		err := ja.Scan("not bytes")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "type assertion to []byte failed")
	})
	t.Run("Invalid JSON", func(t *testing.T) {
		var ja JobAttributes
		err := ja.Scan([]byte(`{"invalid": json}`))
		assert.Error(t, err)
	})
}

func TestJobAttributes_Value(t *testing.T) {
	t.Run("With NewKmsKeyURL", func(t *testing.T) {
		ja := JobAttributes{
			ResourceUUID: "vault-uuid",
			Location:     "us-central1",
			KmsAttributes: &JobKmsAttributes{
				NewKmsKeyURL: "projects/test/locations/us/keyRings/test/cryptoKeys/key1",
			},
		}
		val, err := ja.Value()
		assert.NoError(t, err)
		assert.NotNil(t, val)
		var result JobAttributes
		err = result.Scan(val)
		assert.NoError(t, err)
		assert.Equal(t, ja, result)
	})
	t.Run("Without NewKmsKeyURL", func(t *testing.T) {
		ja := JobAttributes{
			ResourceUUID: "vault-uuid",
			Location:     "us-central1",
		}
		val, err := ja.Value()
		assert.NoError(t, err)
		assert.NotNil(t, val)
		var result JobAttributes
		err = result.Scan(val)
		assert.NoError(t, err)
		assert.Equal(t, ja, result)
	})
	t.Run("Empty struct", func(t *testing.T) {
		ja := JobAttributes{}
		val, err := ja.Value()
		assert.NoError(t, err)
		assert.NotNil(t, val)
		var result JobAttributes
		err = result.Scan(val)
		assert.NoError(t, err)
		assert.Equal(t, ja, result)
	})
}

func TestAutoTieringConfig_Scan(t *testing.T) {
	t.Run("nil value", func(t *testing.T) {
		var atc AutoTieringConfig
		err := atc.Scan(nil)
		assert.NoError(t, err)
		assert.Equal(t, AutoTieringConfig{}, atc)
	})

	t.Run("byte slice", func(t *testing.T) {
		var atc AutoTieringConfig
		err := atc.Scan([]byte(`{"hot_tier_size_in_bytes":42,"enable_hot_tier_auto_resize":true}`))
		assert.NoError(t, err)
		assert.Equal(t, int64(42), atc.HotTierSizeInBytes)
		assert.True(t, atc.EnableHotTierAutoResize)
	})

	t.Run("string", func(t *testing.T) {
		var atc AutoTieringConfig
		err := atc.Scan(`{"hot_tier_size_in_bytes":99}`)
		assert.NoError(t, err)
		assert.Equal(t, int64(99), atc.HotTierSizeInBytes)
	})

	t.Run("invalid type", func(t *testing.T) {
		var atc AutoTieringConfig
		err := atc.Scan(12345)
		assert.Error(t, err)
	})
}

func TestMergeBucketDetails(t *testing.T) {
	bucket := func(name string, pzi, pzs bool) *BucketDetails {
		return &BucketDetails{BucketName: name, SatisfiesPzi: pzi, SatisfiesPzs: pzs}
	}

	t.Run("NilReceiver_NoOp", func(t *testing.T) {
		var b *BackupVault
		// Must not panic.
		b.UpdateBucketDetails(BucketDetailsArray{bucket("b1", true, false)})
		assert.Nil(t, b)
	})

	t.Run("EmptyUpdate_NoOp", func(t *testing.T) {
		b := &BackupVault{BucketDetails: BucketDetailsArray{bucket("b1", false, false)}}
		b.UpdateBucketDetails(BucketDetailsArray{})
		assert.Len(t, b.BucketDetails, 1)
		assert.Equal(t, "b1", b.BucketDetails[0].BucketName)
	})

	// -----------------------------------------------------------------------
	// CrossProject path
	// -----------------------------------------------------------------------

	t.Run("CrossProject_NilFirstEntry_NoOp", func(t *testing.T) {
		existing := BucketDetailsArray{bucket("orig", false, false)}
		b := &BackupVault{ServiceType: ServiceTypeCrossProject, BucketDetails: existing}
		b.UpdateBucketDetails(BucketDetailsArray{nil})
		assert.Equal(t, existing, b.BucketDetails)
	})

	t.Run("CrossProject_EmptyBucketName_NoOp", func(t *testing.T) {
		existing := BucketDetailsArray{bucket("orig", false, false)}
		b := &BackupVault{ServiceType: ServiceTypeCrossProject, BucketDetails: existing}
		b.UpdateBucketDetails(BucketDetailsArray{{BucketName: ""}})
		assert.Equal(t, existing, b.BucketDetails)
	})

	t.Run("CrossProject_ValidFirstEntry_ReplacesAll", func(t *testing.T) {
		b := &BackupVault{
			ServiceType:   ServiceTypeCrossProject,
			BucketDetails: BucketDetailsArray{bucket("old", false, false)},
		}
		replacement := BucketDetailsArray{bucket("new", true, true), bucket("extra", false, false)}
		b.UpdateBucketDetails(replacement)
		assert.Equal(t, replacement, b.BucketDetails)
	})

	// -----------------------------------------------------------------------
	// Normal (non-CrossProject) merge path
	// -----------------------------------------------------------------------

	t.Run("EmptyExisting_NewBucketAppended", func(t *testing.T) {
		b := &BackupVault{BucketDetails: BucketDetailsArray{}}
		b.UpdateBucketDetails(BucketDetailsArray{bucket("b1", true, false)})
		assert.Len(t, b.BucketDetails, 1)
		assert.Equal(t, "b1", b.BucketDetails[0].BucketName)
		assert.True(t, b.BucketDetails[0].SatisfiesPzi)
	})

	t.Run("MatchingBucket_UpdatesZiZsFieldsOnly", func(t *testing.T) {
		orig := &BucketDetails{
			BucketName:          "b1",
			ServiceAccountName:  "sa@project.iam",
			VendorSubnetID:      "subnet-abc",
			TenantProjectNumber: "12345",
			SatisfiesPzi:        false,
			SatisfiesPzs:        false,
		}
		b := &BackupVault{BucketDetails: BucketDetailsArray{orig}}
		b.UpdateBucketDetails(BucketDetailsArray{bucket("b1", true, true)})

		assert.Len(t, b.BucketDetails, 1)
		got := b.BucketDetails[0]
		assert.True(t, got.SatisfiesPzi)
		assert.True(t, got.SatisfiesPzs)
		// Non-ZiZs fields must be preserved.
		assert.Equal(t, "sa@project.iam", got.ServiceAccountName)
		assert.Equal(t, "subnet-abc", got.VendorSubnetID)
		assert.Equal(t, "12345", got.TenantProjectNumber)
	})

	t.Run("MatchingBucket_EnrichesHMACAndServiceAccount", func(t *testing.T) {
		orig := &BucketDetails{
			BucketName:          "b1",
			VendorSubnetID:      "subnet-abc",
			TenantProjectNumber: "12345",
			SatisfiesPzi:        true,
			SatisfiesPzs:        false,
		}
		b := &BackupVault{BucketDetails: BucketDetailsArray{orig}}
		b.UpdateBucketDetails(BucketDetailsArray{{
			BucketName:          "b1",
			ServiceAccountName:  "sa@project.iam",
			HmacAccessKeyID:     "ackey",
			HmacSecretID:        "hardware-bv-hmac-sa",
			SatisfiesPzi:        true,
			SatisfiesPzs:        false,
		}})

		assert.Len(t, b.BucketDetails, 1)
		got := b.BucketDetails[0]
		assert.Equal(t, "sa@project.iam", got.ServiceAccountName)
		assert.Equal(t, "ackey", got.HmacAccessKeyID)
		assert.Equal(t, "hardware-bv-hmac-sa", got.HmacSecretID)
		assert.Equal(t, "subnet-abc", got.VendorSubnetID)
		assert.Equal(t, "12345", got.TenantProjectNumber)
	})

	t.Run("NewBucket_Appended", func(t *testing.T) {
		b := &BackupVault{BucketDetails: BucketDetailsArray{bucket("b1", false, false)}}
		b.UpdateBucketDetails(BucketDetailsArray{bucket("b2", true, true)})
		assert.Len(t, b.BucketDetails, 2)
		assert.Equal(t, "b1", b.BucketDetails[0].BucketName)
		assert.Equal(t, "b2", b.BucketDetails[1].BucketName)
	})

	t.Run("InsertionOrder_ExistingBeforeNew", func(t *testing.T) {
		b := &BackupVault{BucketDetails: BucketDetailsArray{bucket("b1", false, false), bucket("b2", false, false)}}
		b.UpdateBucketDetails(BucketDetailsArray{bucket("b3", true, false), bucket("b1", true, true)})
		assert.Len(t, b.BucketDetails, 3)
		assert.Equal(t, "b1", b.BucketDetails[0].BucketName)
		assert.Equal(t, "b2", b.BucketDetails[1].BucketName)
		assert.Equal(t, "b3", b.BucketDetails[2].BucketName)
	})

	t.Run("InvalidEntryInExisting_Skipped", func(t *testing.T) {
		b := &BackupVault{BucketDetails: BucketDetailsArray{
			nil,
			{BucketName: ""},
			bucket("b1", false, false),
		}}
		b.UpdateBucketDetails(BucketDetailsArray{bucket("b2", true, false)})
		// nil and empty-name entries are dropped; only b1 and new b2 remain.
		assert.Len(t, b.BucketDetails, 2)
		assert.Equal(t, "b1", b.BucketDetails[0].BucketName)
		assert.Equal(t, "b2", b.BucketDetails[1].BucketName)
	})

	t.Run("InvalidEntryInUpdate_Skipped", func(t *testing.T) {
		b := &BackupVault{BucketDetails: BucketDetailsArray{bucket("b1", false, false)}}
		b.UpdateBucketDetails(BucketDetailsArray{nil, {BucketName: ""}, bucket("b2", true, false)})
		// nil and empty-name update entries are ignored; only b1 unchanged and new b2.
		assert.Len(t, b.BucketDetails, 2)
		assert.Equal(t, "b1", b.BucketDetails[0].BucketName)
		assert.False(t, b.BucketDetails[0].SatisfiesPzi)
		assert.Equal(t, "b2", b.BucketDetails[1].BucketName)
	})

	t.Run("DuplicateBucketInExisting_OnlyFirstKept", func(t *testing.T) {
		b := &BackupVault{BucketDetails: BucketDetailsArray{
			bucket("b1", false, false),
			bucket("b1", true, true), // duplicate — should be dropped
		}}
		b.UpdateBucketDetails(BucketDetailsArray{bucket("b2", true, false)})
		assert.Len(t, b.BucketDetails, 2)
		assert.Equal(t, "b1", b.BucketDetails[0].BucketName)
		assert.False(t, b.BucketDetails[0].SatisfiesPzi, "second duplicate must not overwrite first")
		assert.Equal(t, "b2", b.BucketDetails[1].BucketName)
	})

	t.Run("MixedUpdate_SomeMatchSomeNew", func(t *testing.T) {
		b := &BackupVault{BucketDetails: BucketDetailsArray{
			bucket("b1", false, false),
			bucket("b2", false, false),
		}}
		b.UpdateBucketDetails(BucketDetailsArray{
			bucket("b2", true, true),  // existing — ZiZs updated
			bucket("b3", true, false), // new — appended
		})
		assert.Len(t, b.BucketDetails, 3)
		assert.Equal(t, "b1", b.BucketDetails[0].BucketName)
		assert.False(t, b.BucketDetails[0].SatisfiesPzi)

		assert.Equal(t, "b2", b.BucketDetails[1].BucketName)
		assert.True(t, b.BucketDetails[1].SatisfiesPzi)
		assert.True(t, b.BucketDetails[1].SatisfiesPzs)

		assert.Equal(t, "b3", b.BucketDetails[2].BucketName)
		assert.True(t, b.BucketDetails[2].SatisfiesPzi)
	})
}

func TestHarvestRetryState_ScanAndValue(t *testing.T) {
	t.Run("ScanNilResetsStruct", func(t *testing.T) {
		var state HarvestRetryState
		state.Create = &HarvestFlowRetryState{Retries: 2}
		err := state.Scan(nil)
		assert.NoError(t, err)
		assert.Nil(t, state.Create)
	})

	t.Run("ScanInvalidTypeReturnsError", func(t *testing.T) {
		var state HarvestRetryState
		err := state.Scan(12345)
		assert.Error(t, err)
	})

	t.Run("ScanAndValueRoundTrip", func(t *testing.T) {
		original := HarvestRetryState{
			Create: &HarvestFlowRetryState{
				Retries:         2,
				LastRunMsg:      "upload failed",
				ShouldRetry:     true,
				LastSourceRunID: "run-123",
				LastAttemptAt:   time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC),
			},
			Delete: &HarvestFlowRetryState{
				Retries:     1,
				LastRunMsg:  "delete failed",
				ShouldRetry: true,
			},
		}
		val, err := original.Value()
		assert.NoError(t, err)

		var decoded HarvestRetryState
		err = decoded.Scan(val.([]byte))
		assert.NoError(t, err)
		assert.NotNil(t, decoded.Create)
		assert.Equal(t, 2, decoded.Create.Retries)
		assert.Equal(t, "upload failed", decoded.Create.LastRunMsg)
		assert.Equal(t, "run-123", decoded.Create.LastSourceRunID)
		assert.NotNil(t, decoded.Delete)
		assert.Equal(t, 1, decoded.Delete.Retries)
	})
}
