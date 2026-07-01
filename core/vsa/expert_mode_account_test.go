package vsa

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

type mockExpertModeAccountProvider struct {
	clusterUUID   string
	roleOwnerUUID string
	getAccount    func(name string) (*SecurityAccount, error)
	create        func(params CreateSecurityAccountParams) error
	deleteAccount func(params DeleteSecurityAccountParams) error
}

func (m *mockExpertModeAccountProvider) GetRoleOwnerUUID(roleName string) (string, error) {
	if m.roleOwnerUUID == "" {
		return "role-owner-uuid", nil
	}
	return m.roleOwnerUUID, nil
}

func (m *mockExpertModeAccountProvider) GetClusterUUID() (string, error) {
	if m.clusterUUID == "" {
		return "cluster-uuid", nil
	}
	return m.clusterUUID, nil
}

func (m *mockExpertModeAccountProvider) GetSecurityAccount(params GetSecurityAccountParams) (*SecurityAccount, error) {
	if m.getAccount != nil {
		return m.getAccount(params.Name)
	}
	return nil, utilserrors.NewNotFoundErr("SecurityAccount", &params.Name)
}

func (m *mockExpertModeAccountProvider) CreateSecurityAccount(params CreateSecurityAccountParams) error {
	if m.create != nil {
		return m.create(params)
	}
	return nil
}

func (m *mockExpertModeAccountProvider) DeleteSecurityAccount(params DeleteSecurityAccountParams) error {
	if m.deleteAccount != nil {
		return m.deleteAccount(params)
	}
	return nil
}

func TestEnsureExpertModeAccount_SkipsWhenAccountExists(t *testing.T) {
	provider := &mockExpertModeAccountProvider{
		getAccount: func(name string) (*SecurityAccount, error) {
			return &SecurityAccount{Name: name}, nil
		},
		create: func(params CreateSecurityAccountParams) error {
			t.Fatal("CreateSecurityAccount should not be called when account exists")
			return nil
		},
	}

	err := EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Username: "svc-user_gcnv",
		Password: "secret",
		RoleName: "gcnvadmin",
	})
	require.NoError(t, err)
}

func TestEnsureExpertModeAccount_CreatesAccountWithRoleOwner(t *testing.T) {
	created := false
	provider := &mockExpertModeAccountProvider{
		roleOwnerUUID: "role-owner-uuid",
		create: func(params CreateSecurityAccountParams) error {
			created = true
			assert.Equal(t, "svc-user_gcnv", params.Name)
			assert.Equal(t, "secret", params.Password)
			assert.Equal(t, "gcnvadmin", params.RoleName)
			assert.Equal(t, "role-owner-uuid", params.ClusterUUID)
			return nil
		},
	}

	err := EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Username: "svc-user_gcnv",
		Password: "secret",
		RoleName: "gcnvadmin",
	})
	require.NoError(t, err)
	assert.True(t, created)
}

func TestEnsureExpertModeAccount_CreatesAccount(t *testing.T) {
	created := false
	provider := &mockExpertModeAccountProvider{
		create: func(params CreateSecurityAccountParams) error {
			created = true
			assert.Equal(t, "svc-user_gcnv", params.Name)
			assert.Equal(t, "secret", params.Password)
			assert.Equal(t, "gcnvadmin", params.RoleName)
			assert.Equal(t, "cluster-uuid", params.ClusterUUID)
			return nil
		},
	}

	err := EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Username:    "svc-user_gcnv",
		Password:    "secret",
		RoleName:    "gcnvadmin",
		ClusterUUID: "cluster-uuid",
	})
	require.NoError(t, err)
	assert.True(t, created)
}

func TestEnsureExpertModeAccount_TreatsAlreadyExistsAsSuccess(t *testing.T) {
	provider := &mockExpertModeAccountProvider{
		create: func(params CreateSecurityAccountParams) error {
			return errors.New("Entry with the same name already exists")
		},
	}

	err := EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Username:    "svc-user_gcnv",
		Password:    "secret",
		RoleName:    "gcnvadmin",
		ClusterUUID: "cluster-uuid",
	})
	require.NoError(t, err)
}

func TestEnsureExpertModeAccount_PropagatesLookupError(t *testing.T) {
	provider := &mockExpertModeAccountProvider{
		getAccount: func(name string) (*SecurityAccount, error) {
			return nil, errors.New("lookup failed")
		},
	}

	err := EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Username: "svc-user_gcnv",
		Password: "secret",
		RoleName: "gcnvadmin",
	})
	assert.EqualError(t, err, "lookup failed")
}

func TestDeleteExpertModeAccount_SkipsWhenAccountMissing(t *testing.T) {
	deleteCalled := false
	provider := &mockExpertModeAccountProvider{
		getAccount: func(name string) (*SecurityAccount, error) {
			return nil, utilserrors.NewNotFoundErr("SecurityAccount", &name)
		},
		deleteAccount: func(params DeleteSecurityAccountParams) error {
			deleteCalled = true
			return nil
		},
	}

	err := DeleteExpertModeAccount(context.Background(), provider, "svc-user", "")
	require.NoError(t, err)
	assert.False(t, deleteCalled)
}

func TestDeleteExpertModeAccount_ResolvesOwnerFromLookup(t *testing.T) {
	var deletedOwner string
	provider := &mockExpertModeAccountProvider{
		getAccount: func(name string) (*SecurityAccount, error) {
			return &SecurityAccount{Name: name, OwnerUUID: "role-owner-uuid"}, nil
		},
		deleteAccount: func(params DeleteSecurityAccountParams) error {
			deletedOwner = params.OwnerUUID
			assert.Equal(t, "svc-user", params.Name)
			return nil
		},
	}

	err := DeleteExpertModeAccount(context.Background(), provider, "svc-user", "")
	require.NoError(t, err)
	assert.Equal(t, "role-owner-uuid", deletedOwner)
}

func TestDeleteExpertModeAccount_WithExplicitOwnerUUID(t *testing.T) {
	var deletedOwner string
	provider := &mockExpertModeAccountProvider{
		deleteAccount: func(params DeleteSecurityAccountParams) error {
			deletedOwner = params.OwnerUUID
			return nil
		},
		getAccount: func(name string) (*SecurityAccount, error) {
			t.Fatal("GetSecurityAccount should not be called when owner UUID is provided")
			return nil, nil
		},
	}

	err := DeleteExpertModeAccount(context.Background(), provider, "svc-user", "explicit-owner")
	require.NoError(t, err)
	assert.Equal(t, "explicit-owner", deletedOwner)
}

func TestEnsureExpertModeAccount_PropagatesCreateError(t *testing.T) {
	provider := &mockExpertModeAccountProvider{
		create: func(params CreateSecurityAccountParams) error {
			return errors.New("create failed")
		},
	}

	err := EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Username:    "svc-user_gcnv",
		Password:    "secret",
		RoleName:    "gcnvadmin",
		ClusterUUID: "cluster-uuid",
	})
	assert.EqualError(t, err, "create failed")
}

func TestEnsureExpertModeAccount_InvalidInputs(t *testing.T) {
	err := EnsureExpertModeAccount(context.Background(), nil, CreateExpertModeAccountParams{
		Username: "svc-user",
		Password: "secret",
		RoleName: "gcnvadmin",
	})
	assert.EqualError(t, err, "expert mode account provider is required")

	provider := &mockExpertModeAccountProvider{}
	err = EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Password: "secret",
		RoleName: "gcnvadmin",
	})
	assert.EqualError(t, err, "expert mode account username is required")

	err = EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Username: "svc-user",
		RoleName: "gcnvadmin",
	})
	assert.EqualError(t, err, "expert mode account password is required")

	err = EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Username: "svc-user",
		Password: "secret",
	})
	assert.EqualError(t, err, "expert mode account role name is required")
}

func TestEnsureExpertModeAccount_GetClusterUUIDError(t *testing.T) {
	provider := &clusterUUIDErrorProvider{
		mockExpertModeAccountProvider: &mockExpertModeAccountProvider{
			getAccount: func(name string) (*SecurityAccount, error) {
				return nil, utilserrors.NewNotFoundErr("SecurityAccount", &name)
			},
		},
	}

	err := EnsureExpertModeAccount(context.Background(), provider, CreateExpertModeAccountParams{
		Username: "svc-user_gcnv",
		Password: "secret",
		RoleName: "gcnvadmin",
	})
	assert.EqualError(t, err, "cluster uuid lookup failed")
}

type clusterUUIDErrorProvider struct {
	*mockExpertModeAccountProvider
}

func (p *clusterUUIDErrorProvider) GetRoleOwnerUUID(roleName string) (string, error) {
	return "", nil
}

func (p *clusterUUIDErrorProvider) GetClusterUUID() (string, error) {
	return "", errors.New("cluster uuid lookup failed")
}
