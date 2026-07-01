package vsa

import (
	"context"
	"fmt"
	"strings"

	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// EnsureExpertModeRole creates the expert-mode ONTAP role if it does not already exist.
func EnsureExpertModeRole(ctx context.Context, provider Provider, roleName string, privileges []*RolePrivilege) error {
	logger := util.GetLogger(ctx)
	if provider == nil {
		return fmt.Errorf("expert mode provider is required")
	}
	if strings.TrimSpace(roleName) == "" {
		return fmt.Errorf("expert mode role name is required")
	}
	if len(privileges) == 0 {
		return fmt.Errorf("expert mode role privileges are required")
	}

	_, err := provider.CreateRole(CreateRoleParams{
		Name:       roleName,
		Privileges: privileges,
	})
	if err == nil {
		logger.Infof("Successfully created ONTAP role %s", roleName)
		return nil
	}
	if isIgnorableExpertModeRoleCreateError(err) {
		logger.Infof("ONTAP role %s already exists, skipping creation", roleName)
		return nil
	}
	logger.Errorf("Failed to create ONTAP role %s: %v", roleName, err)
	return err
}

func isIgnorableExpertModeRoleCreateError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Role already exists") ||
		strings.Contains(msg, "Role already exists in legacy role table")
}

// DeleteExpertModeRole deletes the expert-mode ONTAP role if it exists.
func DeleteExpertModeRole(ctx context.Context, provider Provider, roleName, ownerUUID string) error {
	logger := util.GetLogger(ctx)
	if provider == nil {
		return nil
	}
	roleName = strings.TrimSpace(roleName)
	ownerUUID = strings.TrimSpace(ownerUUID)
	if roleName == "" || ownerUUID == "" {
		return nil
	}

	owner := ownerUUID
	err := provider.DeleteRole(DeleteRoleParams{
		Name:      roleName,
		OwnerUUID: &owner,
	})
	if err == nil {
		logger.Infof("Deleted ONTAP role %s", roleName)
		return nil
	}
	if utilserrors.IsNotFoundErr(err) || strings.Contains(strings.ToLower(err.Error()), "not found") {
		logger.Infof("ONTAP role %s not found, skipping delete", roleName)
		return nil
	}
	logger.Errorf("Failed to delete ONTAP role %s: %v", roleName, err)
	return err
}
