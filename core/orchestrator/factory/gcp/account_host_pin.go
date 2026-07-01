package gcp

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// PinAccountHosts reconciles the desired pinned host set for an account,
// unpins omitted hosts synchronously, and starts PinAccountHostsWorkflow when credential
// provisioning is required for newly requested hosts.
func (o *GCPOrchestrator) PinAccountHosts(ctx context.Context, params *commonparams.PinAccountHostsParams) (*models.SetAccountHostsResponse, error) {
	accountName := strings.TrimSpace(params.AccountName)

	account, err := getOrCreateAccount(ctx, o.storage, accountName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve account for accountName %s: %w", accountName, err)
	}
	accountUUID := strings.TrimSpace(account.UUID)
	if accountUUID == "" {
		return nil, customerrors.NewBadRequestErr(
			fmt.Sprintf("account UUID is missing for accountName %s", accountName),
		)
	}

	if err := o.ensureNoActivePinAccountHostsJob(ctx, account.ID); err != nil {
		return nil, err
	}

	hostUUIDs := params.HostUUIDs
	if hostUUIDs == nil {
		hostUUIDs = []string{}
	}

	requestedSet := make(map[string]struct{}, len(hostUUIDs))
	for _, hostUUID := range hostUUIDs {
		requestedSet[hostUUID] = struct{}{}
	}

	if len(hostUUIDs) > 0 {
		existingHosts, lookupErr := o.storage.GetHostsByUUIDs(ctx, hostUUIDs)
		if lookupErr != nil {
			return nil, fmt.Errorf("failed to look up referenced hosts: %w", lookupErr)
		}
		if missing := missingHostUUIDs(hostUUIDs, existingHosts); len(missing) > 0 {
			return nil, customerrors.NewNotFoundErr("Host", &missing[0])
		}
	}

	currentlyPinned, err := o.storage.ListPinnedAccountHostsByAccount(ctx, account.ID)
	if err != nil {
		return nil, err
	}

	existingRequestedRows, err := o.storage.GetAccountHostsByAccountAndHostUUIDs(ctx, account.ID, hostUUIDs)
	if err != nil {
		return nil, err
	}
	existingByHost := make(map[string]*datamodel.AccountHost, len(existingRequestedRows))
	for _, row := range existingRequestedRows {
		if row != nil {
			existingByHost[row.HostID] = row
		}
	}

	for _, row := range currentlyPinned {
		if row == nil {
			continue
		}
		if _, keepPinned := requestedSet[row.HostID]; keepPinned {
			continue
		}
		if err := o.storage.SetAccountHostPinned(ctx, row.UUID, false); err != nil {
			return nil, err
		}
	}

	provisionUUIDs := make([]string, 0, len(hostUUIDs))
	for _, hostUUID := range hostUUIDs {
		existing := existingByHost[hostUUID]
		switch {
		case existing == nil:
			created, createErr := o.storage.CreateAccountHost(ctx, &datamodel.AccountHost{
				AccountID: account.ID,
				HostID:    hostUUID,
				Pinned:    false,
			})
			if createErr != nil {
				return nil, createErr
			}
			provisionUUIDs = append(provisionUUIDs, created.UUID)
		case !existing.HasProvisionedCredentials():
			provisionUUIDs = append(provisionUUIDs, existing.UUID)
		case !existing.Pinned:
			if err := o.storage.SetAccountHostPinned(ctx, existing.UUID, true); err != nil {
				return nil, err
			}
		}
	}

	if len(provisionUUIDs) > 0 && o.temporal == nil {
		return nil, fmt.Errorf("temporal client is not configured")
	}

	logger := util.GetLogger(ctx)
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypePinAccountHosts),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  accountName,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			PayloadAttributes: map[string]interface{}{
				"account_name":         accountName,
				"requested_host_uuids": hostUUIDs,
				"account_host_uuids":   provisionUUIDs,
			},
		},
	}
	if len(provisionUUIDs) > 0 {
		job.JobAttributes.ResourceUUID = provisionUUIDs[0]
	}

	createdJob, err := o.storage.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create pin account hosts job", "error", err)
		return nil, err
	}

	if len(provisionUUIDs) == 0 {
		if updateErr := o.storage.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateDONE), 0, ""); updateErr != nil {
			logger.Error("Failed to mark pin account hosts job done", "jobID", createdJob.UUID, "error", updateErr)
			return nil, updateErr
		}
		return &models.SetAccountHostsResponse{
			Status: string(datamodel.JobsStateDONE),
			JobID:  createdJob.UUID,
		}, nil
	}

	// Mark the job ERROR only when workflow start fails after the job row was created.
	defer func() {
		if err != nil {
			if updateErr := o.storage.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); updateErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", updateErr)
			}
		}
	}()

	wfParams := &commonparams.PinAccountHostsWorkflowParams{
		AccountName:      accountName,
		AccountUUID:      accountUUID,
		AccountHostUUIDs: provisionUUIDs,
		JobUUID:          createdJob.UUID,
	}

	runTimeout := workflowengine.GetWorkflowGlobalTimeout()
	_, err = o.temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    runTimeout,
		},
		workflows.PinAccountHostsWorkflow,
		wfParams,
	)
	if err != nil {
		logger.Error("Failed to start pin account hosts workflow", "error", err)
		return nil, fmt.Errorf("failed to start pin account hosts workflow: %w", err)
	}

	return &models.SetAccountHostsResponse{
		Status: string(datamodel.JobsStatePROCESSING),
		JobID:  createdJob.UUID,
	}, nil
}

func (o *GCPOrchestrator) ensureNoActivePinAccountHostsJob(ctx context.Context, accountID int64) error {
	_, err := o.storage.GetActivePinAccountHostsJobByAccount(ctx, accountID)
	if err == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("account host pinning operation already in progress for account %d", accountID))
	}
	if isPinAccountHostsJobNotFound(err) {
		return nil
	}
	return err
}

func isPinAccountHostsJobNotFound(err error) bool {
	if err == nil {
		return false
	}
	if customerrors.IsNotFoundErr(err) {
		return true
	}
	var customErr *vsaerrors.CustomError
	return vsaerrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError)
}

// missingHostUUIDs returns the requested UUIDs that are not present among the
// supplied existing host rows. Result is sorted for stable error messages.
func missingHostUUIDs(requested []string, existing []*datamodel.Host) []string {
	have := make(map[string]struct{}, len(existing))
	for _, h := range existing {
		if h != nil {
			have[h.UUID] = struct{}{}
		}
	}
	var missing []string
	for _, uuid := range requested {
		if _, ok := have[uuid]; !ok {
			missing = append(missing, uuid)
		}
	}
	sort.Strings(missing)
	return missing
}
