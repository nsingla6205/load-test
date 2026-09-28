package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
)

func TestBatchListWireJSONFieldNames(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"

	tests := []struct {
		newKey string
		oldKey string
		decode func([]byte) ([]string, error)
	}{
		{
			newKey: "volumeUuids",
			oldKey: "volumeUUIDs",
			decode: func(raw []byte) ([]string, error) {
				var body gcpgenserver.BatchVolumeUUIDListV1beta
				err := body.UnmarshalJSON(raw)
				return body.VolumeUuids, err
			},
		},
		{
			newKey: "poolUuids",
			oldKey: "poolUUIDs",
			decode: func(raw []byte) ([]string, error) {
				var body gcpgenserver.BatchPoolUUIDListV1beta
				err := body.UnmarshalJSON(raw)
				return body.PoolUuids, err
			},
		},
		{
			newKey: "snapshotUuids",
			oldKey: "snapshotUUIDs",
			decode: func(raw []byte) ([]string, error) {
				var body gcpgenserver.BatchSnapshotUUIDListV1beta
				err := body.UnmarshalJSON(raw)
				return body.SnapshotUuids, err
			},
		},
		{
			newKey: "backupUuids",
			oldKey: "backupUUIDs",
			decode: func(raw []byte) ([]string, error) {
				var body gcpgenserver.BatchBackupUUIDListV1beta
				err := body.UnmarshalJSON(raw)
				return body.BackupUuids, err
			},
		},
		{
			newKey: "backupVaultUuids",
			oldKey: "backupVaultUUIDs",
			decode: func(raw []byte) ([]string, error) {
				var body gcpgenserver.BatchBackupVaultUUIDListV1beta
				err := body.UnmarshalJSON(raw)
				return body.BackupVaultUuids, err
			},
		},
		{
			newKey: "backupPolicyUuids",
			oldKey: "backupPolicyUUIDs",
			decode: func(raw []byte) ([]string, error) {
				var body gcpgenserver.BatchBackupPolicyUUIDListV1beta
				err := body.UnmarshalJSON(raw)
				return body.BackupPolicyUuids, err
			},
		},
		{
			newKey: "activeDirectoryUuids",
			oldKey: "activeDirectoryUUIDs",
			decode: func(raw []byte) ([]string, error) {
				var body gcpgenserver.BatchActiveDirectoryUUIDListV1beta
				err := body.UnmarshalJSON(raw)
				return body.ActiveDirectoryUuids, err
			},
		},
		{
			newKey: "kmsConfigIds",
			oldKey: "kmsConfigUUIDs",
			decode: func(raw []byte) ([]string, error) {
				var body gcpgenserver.BatchKmsConfigIdListV1beta
				err := body.UnmarshalJSON(raw)
				return body.KmsConfigIds, err
			},
		},
	}

	for _, tc := range tests {
		t.Run("When_"+tc.newKey+"_decodes", func(tt *testing.T) {
			raw := []byte(`{"` + tc.newKey + `":["` + id + `"]}`)
			got, err := tc.decode(raw)
			require.NoError(tt, err)
			require.Len(tt, got, 1)
			assert.Equal(tt, id, got[0])
		})

		t.Run("When_"+tc.oldKey+"_is_rejected", func(tt *testing.T) {
			raw := []byte(`{"` + tc.oldKey + `":["` + id + `"]}`)
			_, err := tc.decode(raw)
			require.Error(tt, err)
			assert.Contains(tt, err.Error(), tc.newKey)
		})
	}

	t.Run("When_kms_body_is_marshaled_for_cvp", func(tt *testing.T) {
		raw, err := json.Marshal(cvpmodels.KmsConfigIDListV1beta{KmsConfigIDs: []string{id}})
		require.NoError(tt, err)
		assert.Contains(tt, string(raw), `"kmsConfigIDs"`)
		assert.NotContains(tt, string(raw), `"kmsConfigUUIDs"`)
	})

	t.Run("When_cvp_response_uses_kmsConfigurations", func(tt *testing.T) {
		var body cvpBatch.V1betaBatchListKmsConfigsOKBody
		require.NoError(tt, json.Unmarshal([]byte(`{"kmsConfigurations":[{}]}`), &body))
		assert.Len(tt, body.KmsConfigurations, 1)
	})

	t.Run("When_cvp_response_uses_kmsConfigs", func(tt *testing.T) {
		var body cvpBatch.V1betaBatchListKmsConfigsOKBody
		require.NoError(tt, json.Unmarshal([]byte(`{"kmsConfigs":[{}]}`), &body))
		assert.Len(tt, body.KmsConfigurations, 0)
	})
}
