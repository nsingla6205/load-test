package prepoolinfra

import (
	"context"
	"fmt"
	"log/slog"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/names"
)

func (p *provisioner) createServiceAccount(ctx context.Context, deploymentName, tenantProjectNumber string) (string, error) {
	// Match pool workflow: tenant-project service account emails use the numeric
	// tenant project (e.g. vcm-r-xxx@821829061496.iam.gserviceaccount.com), not
	// the opaque tenant project ID (e.g. m453c8dff8a05cc69-tp).
	saID, expectedSAEmail, err := names.GenerateServiceAccountInfo(deploymentName, tenantProjectNumber)
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

	roles := []string{"roles/storage.objectUser"}
	err = retryTransient(ctx, "attach role to "+expectedSAEmail, func() error {
		return svc.AttachOrUpdateRolesForServiceAccounts(roles, expectedSAEmail, tenantProjectNumber)
	})
	if err != nil {
		return "", fmt.Errorf("attach role to %s: %w", expectedSAEmail, err)
	}

	slog.Info("service account created", "serviceAccountID", saID, "serviceAccountEmail", expectedSAEmail)
	return expectedSAEmail, nil
}

func (p *provisioner) deleteServiceAccount(deploymentName string, tenantProjectNumber string) error {
	_, saEmail, err := names.GenerateServiceAccountInfo(deploymentName, tenantProjectNumber)
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
