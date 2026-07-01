package vsa

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestEnsureExpertModeRole_CreatesRole(t *testing.T) {
	provider := NewMockProvider(t)
	provider.On("CreateRole", mock.MatchedBy(func(params CreateRoleParams) bool {
		return params.Name == "gcnv_r_test" && len(params.Privileges) == 1
	})).Return("location", nil).Once()

	err := EnsureExpertModeRole(context.Background(), provider, "gcnv_r_test", []*RolePrivilege{
		{Path: "vserver modify", Access: "all", Query: "svm_*"},
	})
	require.NoError(t, err)
}

func TestEnsureExpertModeRole_SkipsAlreadyExists(t *testing.T) {
	provider := NewMockProvider(t)
	provider.On("CreateRole", mock.Anything).Return("", errors.New("Role already exists")).Once()

	err := EnsureExpertModeRole(context.Background(), provider, "gcnv_r_test", []*RolePrivilege{
		{Path: "vserver modify", Access: "all"},
	})
	require.NoError(t, err)
}

func TestEnsureExpertModeRole_SkipsLegacyAlreadyExists(t *testing.T) {
	provider := NewMockProvider(t)
	provider.On("CreateRole", mock.Anything).
		Return("", errors.New("Role already exists in legacy role table")).Once()

	err := EnsureExpertModeRole(context.Background(), provider, "gcnv_r_test", []*RolePrivilege{
		{Path: "vserver modify", Access: "all"},
	})
	require.NoError(t, err)
}

func TestEnsureExpertModeRole_PropagatesError(t *testing.T) {
	provider := NewMockProvider(t)
	provider.On("CreateRole", mock.Anything).Return("", errors.New("create failed")).Once()

	err := EnsureExpertModeRole(context.Background(), provider, "gcnv_r_test", []*RolePrivilege{
		{Path: "vserver modify", Access: "all"},
	})
	assert.EqualError(t, err, "create failed")
}

func TestEnsureExpertModeRole_NilProviderAndEmptyInputs(t *testing.T) {
	err := EnsureExpertModeRole(context.Background(), nil, "role", []*RolePrivilege{{Path: "p"}})
	assert.EqualError(t, err, "expert mode provider is required")

	err = EnsureExpertModeRole(context.Background(), NewMockProvider(t), "", []*RolePrivilege{{Path: "p"}})
	assert.EqualError(t, err, "expert mode role name is required")

	err = EnsureExpertModeRole(context.Background(), NewMockProvider(t), "role", nil)
	assert.EqualError(t, err, "expert mode role privileges are required")
}

func TestDeleteExpertModeRole_DeletesRole(t *testing.T) {
	owner := "owner-uuid"
	provider := NewMockProvider(t)
	provider.On("DeleteRole", mock.MatchedBy(func(params DeleteRoleParams) bool {
		return params.Name == "gcnv_r_test" && params.OwnerUUID != nil && *params.OwnerUUID == owner
	})).Return(nil).Once()

	err := DeleteExpertModeRole(context.Background(), provider, "gcnv_r_test", owner)
	require.NoError(t, err)
}

func TestDeleteExpertModeRole_SkipsNotFound(t *testing.T) {
	provider := NewMockProvider(t)
	provider.On("DeleteRole", mock.Anything).
		Return(utilserrors.NewNotFoundErr("SecurityRole", ptr("gcnv_r_test"))).Once()

	err := DeleteExpertModeRole(context.Background(), provider, "gcnv_r_test", "owner-uuid")
	require.NoError(t, err)
}

func TestDeleteExpertModeRole_PropagatesError(t *testing.T) {
	provider := NewMockProvider(t)
	provider.On("DeleteRole", mock.Anything).Return(errors.New("delete failed")).Once()

	err := DeleteExpertModeRole(context.Background(), provider, "gcnv_r_test", "owner-uuid")
	assert.EqualError(t, err, "delete failed")
}

func TestDeleteExpertModeRole_NilProviderAndEmptyInputs(t *testing.T) {
	require.NoError(t, DeleteExpertModeRole(context.Background(), nil, "role", "owner"))
	require.NoError(t, DeleteExpertModeRole(context.Background(), NewMockProvider(t), "", "owner"))
	require.NoError(t, DeleteExpertModeRole(context.Background(), NewMockProvider(t), "role", ""))
}

func ptr(s string) *string {
	return &s
}
