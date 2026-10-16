package opsinventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var poolRow = datamodel.Pool{
	BaseModel: datamodel.BaseModel{ID: 21, UUID: "pool-1"},
	Name:      "p1", State: "READY", StateDetails: "raw ONTAP failure " + sentinel, AccountID: 1,
	ServiceLevel: "FLEX", SizeInBytes: 1000, UsedBytes: 10, HostUUID: "host-1", APIAccessMode: "ONTAP",
	Account:         &datamodel.Account{Name: "1111"},
	KmsConfig:       &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-1"}, KmsGrant: sentinel},
	ActiveDirectory: &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{UUID: "ad-1"}, CredentialPath: sentinel},
	PoolAttributes:  &datamodel.PoolAttributes{PrimaryZone: "z1", ThroughputMibps: 128, Iops: 1000, IsTrueZonal: true},
	PoolCredentials: &datamodel.PoolCredentials{SecretID: sentinel, SecretIDNew: sentinel, Password: sentinel,
		CertificateID:    "d1-cert-" + sentinel + "-20260901-102030",
		CertificateIDNew: "d1-cert-" + sentinel + "-20261005-080000",
		CaURI:            "ca-project/ca-pool/ca-1", StagedCaURI: "ca-project/ca-pool/ca-2",
		AuthType: 2, CertPrivateKeySecretID: sentinel, Username: sentinel},
	ExpertModeCredentials:   &datamodel.ExpertModeCredentials{ExpertModeCredential: []*datamodel.ExpertModeCredential{{Password: sentinel}}},
	VLMConfig:               sentinel,
	ClusterDetails:          datamodel.ClusterDetails{ExternalName: sentinel},
	ExternalTempCredentials: &datamodel.ExternalTempCredentials{TempPasswordActive: true},
}

func TestNewPoolReader_Spec(t *testing.T) {
	spec := NewPoolReader(nil).Spec()
	assert.Equal(t, ResourceTypePool, spec.Type)
	assert.True(t, spec.GetByName)
	assert.Empty(t, spec.NameParents)
	assert.Equal(t, ParentSpec{Column: readstore.ColKmsConfigID, Owner: readstore.OwnerKmsConfig, ByID: true, CheckOwner: true}, spec.ListParents[oasgenserver.OpsParentTypeV1Cmek])
	assert.Equal(t, ParentSpec{Column: readstore.ColActiveDirectoryID, Owner: readstore.OwnerActiveDirectory, ByID: true, CheckOwner: true}, spec.ListParents[oasgenserver.OpsParentTypeV1ActiveDirectory])
	assert.Equal(t, ParentSpec{Column: readstore.ColHostUUID, Owner: readstore.OwnerHost}, spec.ListParents[oasgenserver.OpsParentTypeV1Host], "hosts are only checked to exist")
	assert.True(t, spec.NameFilter)
	assert.Contains(t, spec.States, "ERROR")
}

func TestPoolReaderGet(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenByUUID", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetPoolByUUID(mock.Anything, "pool-1").Return(&poolRow, nil)
		res, err := NewPoolReader(store).Get(ctx, GetRequest{UUID: "pool-1"})
		require.NoError(t, err)
		assert.Equal(t, int64(21), res.ID)
		assert.Equal(t, int64(1), *res.OwnerAccountID)
	})

	t.Run("WhenByName", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		scope := []readstore.Eq{{Column: readstore.ColAccountID, Value: int64(1)}}
		store.EXPECT().GetPoolByName(mock.Anything, readstore.NameQuery{Name: "p1", Eq: scope}).Return(&poolRow, nil)
		_, err := NewPoolReader(store).Get(ctx, GetRequest{Name: "p1", NameScope: scope})
		require.NoError(t, err)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetPoolByUUID(mock.Anything, "x").Return(nil, readstore.ErrNotFound)
		_, err := NewPoolReader(store).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, readstore.ErrNotFound)
	})
}

func TestPoolReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 2}

	t.Run("WhenRowsAreReturned", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListPools(mock.Anything, opts).Return([]datamodel.Pool{poolRow}, nil)
		rows, err := NewPoolReader(store).List(ctx, ListRequest{Options: opts})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.True(t, rows[0].Value.IsOpsPoolV1())
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		boom := errors.New("db down")
		store.EXPECT().ListPools(mock.Anything, opts).Return(nil, boom)
		_, err := NewPoolReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, boom)
	})
}

func TestPoolResource(t *testing.T) {
	t.Run("WhenCredentialsExist", func(t *testing.T) {
		v := poolResource(&poolRow).Value
		got := encoded(t, v)
		assert.Equal(t, "pool", got["resourceType"])
		assert.Equal(t, "1111", got["projectNumber"])
		assert.Equal(t, "kms-1", got["kmsConfigUuid"])
		assert.Equal(t, "ad-1", got["activeDirectoryUuid"])
		assert.Equal(t, "host-1", got["hostUuid"])
		assert.Equal(t, "z1", got["primaryZone"])
		assert.Equal(t, float64(128), got["throughputMibps"])
		assert.Equal(t, true, got["isTrueZonal"])
		assert.Equal(t, float64(2), got["authType"])
		assert.Equal(t, true, got["credentialConfigured"])
		assert.Equal(t, true, got["stateDetailsWithheld"])
		assert.NotContains(t, got, "stateDetails")
		for _, key := range []string{"poolCredentials", "password", "secretId", "certificateId", "vlmConfig", "clusterDetails", "expertModeCredentials"} {
			assert.NotContains(t, got, key)
		}
		rotation := got["certificateRotation"].(map[string]any)
		assert.Equal(t, "2026-09-01T10:20:30Z", rotation["activeCertificateRotatedAt"])
		assert.Equal(t, "2026-10-05T08:00:00Z", rotation["stagedCertificateCreatedAt"])
		assert.Equal(t, true, rotation["rotationStaged"])
		assert.Equal(t, "ca-project/ca-pool/ca-1", rotation["caUri"])
		assert.Equal(t, "ca-project/ca-pool/ca-2", rotation["stagedCaUri"])
		assertNoSecrets(t, v)
	})

	t.Run("WhenNoCredentials", func(t *testing.T) {
		got := encoded(t, poolResource(&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "p"}}).Value)
		assert.Equal(t, false, got["credentialConfigured"])
		assert.NotContains(t, got, "authType")
		assert.NotContains(t, got, "certificateRotation")
		assert.NotContains(t, got, "kmsConfigUuid")
	})

	t.Run("WhenOnlyExternalSecretIsSet", func(t *testing.T) {
		row := datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{AuthType: 1, ExternalSecret: &datamodel.ExternalCredRef{}}}
		assert.Equal(t, true, encoded(t, poolResource(&row).Value)["credentialConfigured"])
	})
}

func TestCertificateRotation(t *testing.T) {
	t.Run("WhenNeverRotated", func(t *testing.T) {
		got := certificateRotation(&datamodel.PoolCredentials{CertificateID: "d1-cert", CaURI: "ca-project/ca-pool/ca-1"})
		assert.False(t, got.ActiveCertificateRotatedAt.IsSet(), "create-time certificate IDs carry no time")
		assert.False(t, got.RotationStaged)
		assert.False(t, got.StagedCertificateCreatedAt.IsSet())
		assert.Equal(t, "ca-project/ca-pool/ca-1", got.CaUri.Or(""))
	})

	t.Run("WhenRotatedAndPromoted", func(t *testing.T) {
		got := certificateRotation(&datamodel.PoolCredentials{CertificateID: "d1-cert-20260901-102030"})
		assert.Equal(t, time.Date(2026, 9, 1, 10, 20, 30, 0, time.UTC), got.ActiveCertificateRotatedAt.Value)
		assert.False(t, got.RotationStaged)
	})

	t.Run("WhenCertificateIsStaged", func(t *testing.T) {
		got := certificateRotation(&datamodel.PoolCredentials{CertificateID: "d1-cert", CertificateIDNew: "d1-cert-20261005-080000"})
		assert.True(t, got.RotationStaged)
		assert.Equal(t, time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC), got.StagedCertificateCreatedAt.Value)
	})

	t.Run("WhenOnlySecretOrCAIsStaged", func(t *testing.T) {
		assert.True(t, certificateRotation(&datamodel.PoolCredentials{SecretIDNew: "s"}).RotationStaged)
		assert.True(t, certificateRotation(&datamodel.PoolCredentials{StagedCaURI: "ca-project/ca-pool/ca-2"}).RotationStaged)
	})
}
