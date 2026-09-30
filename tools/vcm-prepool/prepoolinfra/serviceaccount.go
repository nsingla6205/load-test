package prepoolinfra

import (
	"context"
	"fmt"
	"log/slog"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/names"
)

func (p *provisioner) createServiceAccount(ctx context.Context, deploymentName, tenantProjectNumber string) (string, error) {
	tenantProjectID, err := p.GCPService.ResolveProjectNumberToName(tenantProjectNumber)
	if err != nil {
		return "", fmt.Errorf("resolve tenant project ID from number %s: %w", tenantProjectNumber, err)
	}

	saID, expectedSAEmail, err := names.GenerateServiceAccountInfo(deploymentName, tenantProjectID)
	if err != nil {
		return "", fmt.Errorf("generate service account info: %w", err)
	}

	slog.Info("service account info", "serviceAccountID", saID, "serviceAccountEmail", expectedSAEmail)
	svc := p.GCPService
	createSARequest := &models.CreateServiceAccountRequest{
		AccountId: saID,
		ServiceAccount: &models.ServiceAccount{
			DisplayName: deploymentName,
		},
	}

	var sa *models.ServiceAccount
	err = retryTransient(ctx, "create service account "+saID, func() error {
		var createErr error
		sa, createErr = svc.CreateServiceAccount(createSARequest, tenantProjectNumber, expectedSAEmail)
		return createErr
	})
	if err != nil {
		return "", fmt.Errorf("create service account %s: %w", saID, err)
	}
	if sa == nil {
		return "", fmt.Errorf("create service account %s: empty response", saID)
	}
	if sa.Email == "" {
		return "", fmt.Errorf("create service account %s: response email is empty", saID)
	}

	// Google determines the canonical service-account email domain from the
	// project ID, even when the account is created under a project number.
	saEmail := sa.Email
	roles := []string{"roles/storage.objectUser"}
	err = retryTransient(ctx, "attach role to "+saEmail, func() error {
		return svc.AttachOrUpdateRolesForServiceAccounts(roles, saEmail, tenantProjectNumber)
	})
	if err != nil {
		return "", fmt.Errorf("attach role to %s: %w", saEmail, err)
	}

	slog.Info("service account created", "serviceAccountID", saID, "serviceAccountEmail", saEmail)
	return saEmail, nil
}

func (p *provisioner) deleteServiceAccount(deploymentName string, tenantProjectNumber string) error {
	tenantProjectID, err := p.GCPService.ResolveProjectNumberToName(tenantProjectNumber)
	if err != nil {
		return fmt.Errorf("resolve tenant project ID from number %s: %w", tenantProjectNumber, err)
	}

	_, saEmail, err := names.GenerateServiceAccountInfo(deploymentName, tenantProjectID)
	if err != nil {
		return fmt.Errorf("generate service account info: %w", err)
	}

	slog.Info("removing roles from service account", "serviceAccountEmail", saEmail)

	svc := p.GCPService
	roles := []string{"roles/storage.objectUser"}
	err = svc.RemoveRolesFromServiceAccounts(roles, saEmail, tenantProjectNumber)
	if err != nil {
		return fmt.Errorf("remove roles from service account: %w", err)
	}

	slog.Info("deleting service account", "serviceAccountEmail", saEmail)
	err = svc.DeleteServiceAccount(tenantProjectNumber, saEmail)
	if err != nil {
		return fmt.Errorf("delete service account: %w", err)
	}

	slog.Info("service account deleted", "serviceAccountEmail", saEmail)

	return nil
}
