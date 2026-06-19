package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

func validateIAMPermissions(ctx context.Context, cfg config) error {
	slog.Info("starting IAM permissions validation",
		"vcp_enabled", cfg.vcpEnabled,
		"temporal_enabled", cfg.temporalEnabled)

	parts := strings.Split(cfg.instanceConnName, ":")
	if len(parts) != 3 {
		return fmt.Errorf("invalid INSTANCE_CONNECTION_NAME format: %s (expected project:region:instance)", cfg.instanceConnName)
	}
	projectID := parts[0]

	var serviceAccounts []struct {
		name  string
		email string
	}
	if cfg.vcpEnabled {
		serviceAccounts = append(serviceAccounts,
			struct {
				name  string
				email string
			}{"vcp-core", cfg.iamVcpCore},
			struct {
				name  string
				email string
			}{"vcp-worker", cfg.iamVcpWorker},
			struct {
				name  string
				email string
			}{"clh-sa", cfg.iamClhSA},
			struct {
				name  string
				email string
			}{"metrics-producer", cfg.iamMetricsProducer},
		)
	}
	if cfg.temporalEnabled {
		serviceAccounts = append(serviceAccounts, struct {
			name  string
			email string
		}{"temporal", cfg.iamTemporal})
	}

	if err := validateCloudSQLRoles(ctx, projectID, serviceAccounts); err != nil {
		return fmt.Errorf("cloud SQL role validation failed: %w", err)
	}

	// Service-account impersonation is only used in dual-proxy mode (legacy
	// single-job setup where vcp-core impersonates temporal-ksa via a second
	// proxy on TEMPORAL_DB_PORT). Split-mode (Temporal IAM job runs as
	// temporal-ksa directly) doesn't need impersonation, so we skip the
	// validation when TEMPORAL_DB_PORT is unset.
	if cfg.vcpEnabled && cfg.temporalEnabled && cfg.temporalDBPort != "" {
		if err := validateServiceAccountImpersonation(ctx, cfg.iamVcpCore, cfg.iamTemporal); err != nil {
			return fmt.Errorf("service account impersonation validation failed: %w", err)
		}
	} else {
		slog.Info("skipping service-account impersonation check (split-mode or out-of-scope)")
	}

	slog.Info("IAM permissions validation completed successfully")
	return nil
}

func validateCloudSQLRoles(ctx context.Context, projectID string, serviceAccounts []struct {
	name  string
	email string
}) error {
	slog.Info("validating Cloud SQL connection roles", "project", projectID)

	if projectID == "" {
		return fmt.Errorf("project ID cannot be empty")
	}
	if len(serviceAccounts) == 0 {
		return fmt.Errorf("service accounts list cannot be empty")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before validation: %w", err)
	}

	var crmService *cloudresourcemanager.Service
	var policy *cloudresourcemanager.Policy

	err := retryAPICall(ctx, "create resource manager client", 3, func() error {
		var err error
		crmService, err = cloudresourcemanager.NewService(ctx, option.WithScopes(
			"https://www.googleapis.com/auth/cloud-platform.read-only",
		))
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to create resource manager client: %w", err)
	}

	err = retryAPICall(ctx, "get project IAM policy", 3, func() error {
		var err error
		policy, err = crmService.Projects.GetIamPolicy("projects/"+projectID, &cloudresourcemanager.GetIamPolicyRequest{}).Context(ctx).Do()
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to get project IAM policy: %w", err)
	}

	if policy == nil || policy.Bindings == nil {
		return fmt.Errorf("project %s has no IAM policy bindings", projectID)
	}

	requiredRoles := []string{
		"roles/cloudsql.client",
		"roles/cloudsql.instanceUser",
	}

	for _, sa := range serviceAccounts {
		if sa.name == "" {
			return fmt.Errorf("service account name cannot be empty")
		}
		if sa.email == "" {
			return fmt.Errorf("service account email cannot be empty for %s", sa.name)
		}

		slog.Info("validating Cloud SQL roles", "service_account", sa.name, "email", sa.email)

		gcpServiceAccount := strings.Replace(sa.email, ".iam", ".iam.gserviceaccount.com", 1)
		memberString := "serviceAccount:" + gcpServiceAccount
		foundRoles := make(map[string]bool)

		for _, binding := range policy.Bindings {
			for _, member := range binding.Members {
				if member == memberString {
					foundRoles[binding.Role] = true
					break
				}
			}
		}

		rolesToCheck := requiredRoles
		// vcp-core and temporal run IAM lifecycle jobs and call Resource Manager /
		// IAM APIs during pre-flight (GetIamPolicy on project and service accounts).
		if sa.name == "vcp-core" || sa.name == "temporal" {
			rolesToCheck = append(rolesToCheck, "roles/browser", "roles/iam.serviceAccountViewer")
		}

		var missingRoles []string
		for _, role := range rolesToCheck {
			if !foundRoles[role] {
				missingRoles = append(missingRoles, role)
			}
		}

		if len(missingRoles) > 0 {
			return fmt.Errorf(
				"service account %s (%s) is missing required Cloud SQL roles: %v\n"+
					"Please grant these roles using:\n"+
					"  gcloud projects add-iam-policy-binding %s \\\n"+
					"    --member='serviceAccount:%s' \\\n"+
					"    --role='%s'",
				sa.name, gcpServiceAccount, missingRoles, projectID, gcpServiceAccount, missingRoles[0],
			)
		}

		slog.Info("Cloud SQL roles validated", "service_account", sa.name, "roles", requiredRoles)
	}

	return nil
}

func validateServiceAccountImpersonation(ctx context.Context, granterEmail, targetEmail string) error {
	slog.Info("validating service account impersonation", "granter", granterEmail, "target", targetEmail)

	if granterEmail == "" {
		return fmt.Errorf("granter email cannot be empty")
	}
	if targetEmail == "" {
		return fmt.Errorf("target email cannot be empty")
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before validation: %w", err)
	}

	var iamService *iam.Service
	var policy *iam.Policy

	err := retryAPICall(ctx, "create IAM service", 3, func() error {
		var err error
		iamService, err = iam.NewService(ctx, option.WithScopes(
			"https://www.googleapis.com/auth/cloud-platform.read-only",
		))
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to create IAM service: %w", err)
	}

	targetGcpServiceAccount := strings.Replace(targetEmail, ".iam", ".iam.gserviceaccount.com", 1)
	resourceName := "projects/-/serviceAccounts/" + targetGcpServiceAccount
	err = retryAPICall(ctx, "get service account IAM policy", 3, func() error {
		var err error
		policy, err = iamService.Projects.ServiceAccounts.GetIamPolicy(resourceName).Context(ctx).Do()
		return err
	})
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "404") || strings.Contains(errStr, "notfound") || strings.Contains(errStr, "not found") {
			slog.Info("target service account does not exist yet, will be created by IAM lifecycle job", "target", targetGcpServiceAccount)
			return nil
		}
		if strings.Contains(errStr, "403") || strings.Contains(errStr, "forbidden") || strings.Contains(errStr, "permission") {
			slog.Info("cannot verify impersonation permission (insufficient permissions to read SA IAM policy), will be validated at runtime", "target", targetGcpServiceAccount)
			return nil
		}
		return fmt.Errorf("failed to get service account IAM policy for %s: %w", targetGcpServiceAccount, err)
	}

	if policy == nil || policy.Bindings == nil {
		return fmt.Errorf("service account %s has no IAM policy bindings", targetGcpServiceAccount)
	}

	granterGcpServiceAccount := strings.Replace(granterEmail, ".iam", ".iam.gserviceaccount.com", 1)
	memberString := "serviceAccount:" + granterGcpServiceAccount
	hasRole := false

	for _, binding := range policy.Bindings {
		if binding.Role == "roles/iam.serviceAccountTokenCreator" {
			for _, member := range binding.Members {
				if member == memberString {
					hasRole = true
					break
				}
			}
		}
		if hasRole {
			break
		}
	}

	if !hasRole {
		parts := strings.Split(targetGcpServiceAccount, "@")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("invalid target email format: %s (expected format: name@project.iam.gserviceaccount.com)", targetGcpServiceAccount)
		}
		projectDomain := strings.TrimSuffix(parts[1], ".iam.gserviceaccount.com")
		if projectDomain == "" || projectDomain == parts[1] {
			return fmt.Errorf("invalid service account email domain: %s (expected *.iam.gserviceaccount.com)", targetGcpServiceAccount)
		}

		return fmt.Errorf(
			"service account %s cannot impersonate %s (missing roles/iam.serviceAccountTokenCreator)\n"+
				"This is required for the dual Cloud SQL Proxy setup with temporal impersonation.\n"+
				"Please grant this permission using:\n"+
				"  gcloud iam service-accounts add-iam-policy-binding %s \\\n"+
				"    --member='serviceAccount:%s' \\\n"+
				"    --role='roles/iam.serviceAccountTokenCreator' \\\n"+
				"    --project='%s'",
			granterGcpServiceAccount, targetGcpServiceAccount, targetGcpServiceAccount, granterGcpServiceAccount, projectDomain,
		)
	}

	slog.Info("service account impersonation validated", "granter", granterGcpServiceAccount, "target", targetGcpServiceAccount)
	return nil
}

func retryAPICall(ctx context.Context, operation string, maxRetries int, fn func() error) error {
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during %s: %w", operation, err)
		}

		err := fn()
		if err == nil {
			return nil
		}

		if isRetryableValidationError(err) {
			lastErr = err
			if attempt < maxRetries {
				backoff := time.Duration(attempt*attempt) * time.Second
				slog.Warn("retryable error during IAM validation, retrying",
					"operation", operation,
					"attempt", attempt,
					"max_retries", maxRetries,
					"backoff", backoff,
					"error", err)

				select {
				case <-time.After(backoff):
					continue
				case <-ctx.Done():
					return fmt.Errorf("context cancelled during backoff: %w", ctx.Err())
				}
			}
		} else {
			return err
		}
	}

	return fmt.Errorf("%s failed after %d retries: %w", operation, maxRetries, lastErr)
}

func isRetryableValidationError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()

	nonRetryableErrors := []string{
		"403", "401", "404", "400",
		"PERMISSION_DENIED", "NOT_FOUND", "INVALID_ARGUMENT",
	}

	for _, nonRetryable := range nonRetryableErrors {
		if strings.Contains(errMsg, nonRetryable) {
			return false
		}
	}

	retryableErrors := []string{
		"429", "500", "502", "503", "504",
		"UNAVAILABLE", "DEADLINE_EXCEEDED", "RESOURCE_EXHAUSTED",
		"temporarily unavailable", "timeout", "connection reset", "connection refused",
	}

	for _, retryable := range retryableErrors {
		if strings.Contains(errMsg, retryable) {
			return true
		}
	}

	return false
}
