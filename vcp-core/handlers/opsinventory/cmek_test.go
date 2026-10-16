package opsinventory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"gorm.io/gorm"
)

var (
	saCreated  = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	keyRotated = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
)

var cmekRow = datamodel.KmsConfig{
	BaseModel: datamodel.BaseModel{ID: 81, UUID: "kms-1"},
	AccountID: 1, State: "READY", ResourceID: "kms-res", KeyRing: "ring", KeyRingLocation: "us", KeyName: "key",
	KeyProjectID: "kp", CustomerProjectID: "cp", KmsGrant: sentinel,
	Account: &datamodel.Account{Name: "1111"},
	ServiceAccount: &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{UUID: "sa-1", CreatedAt: saCreated}, Name: "vcp-sa", ServiceName: "kms",
		ServiceAccountEmail: "vcp-sa@example.iam.gserviceaccount.com", State: "READY",
		ServiceAccountPasswordLocation: sentinel,
		ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{Keys: []datamodel.ServiceAccountKey{
			{KeyID: "key-old", KeyData: sentinel, IsPrimary: false, IsActive: false, CreatedAt: saCreated},
			{KeyID: "key-new", KeyData: sentinel, IsPrimary: true, IsActive: true, CreatedAt: keyRotated},
		}},
	},
	KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sde@example.com", SdeKmsConfigIsHealthy: true,
		SdeKmsConfigHealthError: sentinel, SdeKmsConfigOperationURI: sentinel, Instructions: sentinel},
}

func TestNewCmekReader_Spec(t *testing.T) {
	spec := NewCmekReader(nil).Spec()
	assert.Equal(t, ResourceTypeCmek, spec.Type)
	assert.False(t, spec.GetByName, "kms_configs.name is normally empty")
	assert.False(t, spec.NameFilter)
	assert.Empty(t, spec.ListParents)
	assert.Contains(t, spec.States, datamodel.ResourceEventStateOn)
}

func TestCmekReaderGet(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenByUUID", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetKmsConfigByUUID(mock.Anything, "kms-1").Return(&cmekRow, nil)
		res, err := NewCmekReader(store).Get(ctx, GetRequest{UUID: "kms-1"})
		require.NoError(t, err)
		assert.Equal(t, int64(81), res.ID)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetKmsConfigByUUID(mock.Anything, "x").Return(nil, readstore.ErrNotFound)
		_, err := NewCmekReader(store).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, readstore.ErrNotFound)
	})
}

func TestCmekReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 2}

	t.Run("WhenRowsAreReturned", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListKmsConfigs(mock.Anything, opts).Return([]datamodel.KmsConfig{cmekRow}, nil)
		rows, err := NewCmekReader(store).List(ctx, ListRequest{Options: opts})
		require.NoError(t, err)
		assert.Len(t, rows, 1)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListKmsConfigs(mock.Anything, opts).Return(nil, readstore.ErrInvalidQuery)
		_, err := NewCmekReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, readstore.ErrInvalidQuery)
	})
}

func TestCmekResource(t *testing.T) {
	t.Run("WhenCreatedBySDE", func(t *testing.T) {
		v := cmekResource(&cmekRow).Value
		got := encoded(t, v)
		assert.Equal(t, "cmek", got["resourceType"])
		assert.Equal(t, "kms-res", got["resourceId"])
		assert.Equal(t, "ring", got["keyRing"])
		assert.Equal(t, "SDE", got["creationMode"])
		assert.Equal(t, "sde@example.com", got["serviceAccountEmail"])
		assert.Equal(t, true, got["sdeHealthy"])
		assert.NotContains(t, got, "kmsGrant")
		sa := got["serviceAccount"].(map[string]any)
		assert.Equal(t, "sa-1", sa["uuid"])
		assert.Equal(t, "vcp-sa@example.iam.gserviceaccount.com", sa["email"])
		assert.Equal(t, "kms", sa["serviceName"])
		assert.Nil(t, sa["deletedAt"])
		rotation := sa["keyRotation"].(map[string]any)
		assert.Equal(t, "key-new", rotation["primaryKeyId"])
		assert.Equal(t, "2026-09-01T00:00:00Z", rotation["primaryKeyCreatedAt"])
		assert.Equal(t, false, rotation["rotationInProgress"])
		assert.Equal(t, float64(1), rotation["keysPendingDeletion"])
		assert.Len(t, rotation["keys"], 2)
		for _, key := range []string{"keyData", "serviceAccountPasswordLocation", "passwordLocation"} {
			assert.NotContains(t, string(mustJSON(t, v)), key)
		}
		assertNoSecrets(t, v)
	})

	t.Run("WhenNoServiceAccount", func(t *testing.T) {
		got := encoded(t, cmekResource(&datamodel.KmsConfig{}).Value)
		assert.NotContains(t, got, "serviceAccount")
	})

	t.Run("WhenCreatedByVCP", func(t *testing.T) {
		row := datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP, VcpServiceAccountEmail: "vcp@example.com", SdeServiceAccountEmail: "sde@example.com"}}
		got := encoded(t, cmekResource(&row).Value)
		assert.Equal(t, "VCP", got["creationMode"])
		assert.Equal(t, "vcp@example.com", got["serviceAccountEmail"])
		assert.NotContains(t, got, "sdeHealthy")
	})

	t.Run("WhenNoAttributes", func(t *testing.T) {
		got := encoded(t, cmekResource(&datamodel.KmsConfig{}).Value)
		assert.NotContains(t, got, "creationMode")
	})
}

func TestKeyRotation(t *testing.T) {
	sa := func(keys ...datamodel.ServiceAccountKey) *datamodel.ServiceAccount {
		return &datamodel.ServiceAccount{ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{Keys: keys}}
	}

	t.Run("WhenNeverRotated", func(t *testing.T) {
		got := keyRotation(&datamodel.ServiceAccount{})
		assert.False(t, got.PrimaryKeyId.IsSet())
		assert.False(t, got.RotationInProgress)
		assert.Zero(t, got.KeysPendingDeletion)
		assert.Empty(t, got.Keys)
	})

	t.Run("WhenRotationIsInProgress", func(t *testing.T) {
		got := keyRotation(sa(
			datamodel.ServiceAccountKey{KeyID: "old", IsPrimary: true, IsActive: true, CreatedAt: saCreated},
			datamodel.ServiceAccountKey{KeyID: "new", IsActive: true, CreatedAt: keyRotated},
		))
		assert.Equal(t, "old", got.PrimaryKeyId.Or(""), "the old key stays primary until every pool moves")
		assert.True(t, got.RotationInProgress)
		assert.Zero(t, got.KeysPendingDeletion)
	})

	t.Run("WhenOldKeyWaitsForGCPDeletion", func(t *testing.T) {
		got := keyRotation(sa(
			datamodel.ServiceAccountKey{KeyID: "old", CreatedAt: saCreated},
			datamodel.ServiceAccountKey{KeyID: "new", IsPrimary: true, IsActive: true, CreatedAt: keyRotated},
		))
		assert.Equal(t, "new", got.PrimaryKeyId.Or(""))
		assert.Equal(t, keyRotated, got.PrimaryKeyCreatedAt.Value)
		assert.False(t, got.RotationInProgress)
		assert.Equal(t, int32(1), got.KeysPendingDeletion)
	})

	t.Run("WhenRotationIsDone", func(t *testing.T) {
		got := keyRotation(sa(datamodel.ServiceAccountKey{KeyID: "new", IsPrimary: true, IsActive: true, CreatedAt: keyRotated}))
		assert.Equal(t, "new", got.PrimaryKeyId.Or(""))
		assert.False(t, got.RotationInProgress)
		assert.Zero(t, got.KeysPendingDeletion)
		require.Len(t, got.Keys, 1)
		assert.Equal(t, oasgenserver.OpsServiceAccountKeyV1{KeyId: "new", IsPrimary: true, IsActive: true, CreatedAt: optTime(keyRotated)}, got.Keys[0])
	})
}

func TestServiceAccountView(t *testing.T) {
	deleted := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	got := serviceAccountView(&datamodel.ServiceAccount{
		BaseModel:    datamodel.BaseModel{UUID: "sa", DeletedAt: &gorm.DeletedAt{Time: deleted, Valid: true}},
		StateDetails: "raw error " + sentinel,
	})
	assert.Equal(t, "sa", got.UUID)
	assert.False(t, got.DeletedAt.Null)
	assert.True(t, got.StateDetailsWithheld.Or(false))
	assert.False(t, got.StateDetails.IsSet())
	assert.True(t, got.KeyRotation.IsSet())
}

func mustJSON(t *testing.T, v oasgenserver.OpsResourceV1) []byte {
	t.Helper()
	b, err := v.MarshalJSON()
	require.NoError(t, err)
	return b
}
