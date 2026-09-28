package backgroundactivities

import (
	"context"
	"errors"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	trialclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/trial"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/metrics"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// TrialAccountSyncBatchSize is accounts per batch when activity BatchSize is zero (default 50).
var TrialAccountSyncBatchSize = env.GetUint64("TRIAL_ACCOUNT_SYNC_BATCH_SIZE", 50)

// TrialAccountSyncActivity syncs trial exit state from Google (CCFE GetInternalTrial) into account_metadata.
type TrialAccountSyncActivity struct {
	SE        database.Storage
	CCFE      *trialclient.Client // worker/main.go: trial.NewClient(auth.GenerateCallbackToken)
	BatchSize int                 // 0 → TRIAL_ACCOUNT_SYNC_BATCH_SIZE env (default 50)
}

// SyncTrialAccounts loads eligible accounts, reconciles them in batches, and persists trial metadata.
// Each account is looked up at projects/{account}/locations/{LOCAL_REGION}/trial.
// Per-account lookup or DB failures are logged; the activity still returns nil so the next run retries.
func (a *TrialAccountSyncActivity) SyncTrialAccounts(ctx context.Context) error {
	logger := util.GetLogger(ctx)
	localRegion := env.Region
	metrics.EmitTrialAccountSyncRun("started")

	filter := database.TrialSyncEligibleFilter()
	pageSize := int(env.GetUint64("TRIAL_ACCOUNT_SYNC_ACCOUNT_PAGE_SIZE", 500))

	var accounts []*datamodel.Account
	for offset := 0; ; {
		batch, err := a.SE.GetAccountsWithFilter(ctx, filter, &dbutils.Pagination{Offset: offset, Limit: pageSize})
		if err != nil {
			logger.Error("Failed to list accounts eligible for trial sync", "error", err)
			metrics.EmitTrialAccountSyncRun("failed")
			return err
		}
		if len(batch) == 0 {
			break
		}
		accounts = append(accounts, batch...)
		if len(batch) < pageSize {
			break
		}
		offset += len(batch)
	}
	if len(accounts) == 0 {
		logger.Info("No accounts eligible for trial account sync")
		metrics.EmitTrialAccountSyncRun("succeeded")
		return nil
	}

	batchSize := a.BatchSize
	if batchSize <= 0 {
		batchSize = int(TrialAccountSyncBatchSize)
	}

	var persisted, persistFailed, lookupFailed, skippedNotFound, batchCount int

	// Reconcile in slices so we do not fan out unbounded CCFE calls in one activity run.
	for _, batch := range common.ChunkSlice(accounts, batchSize) {
		batchCount++

		// Map each account to projects/{accountName}/locations/{localRegion}/trial.
		requests := make([]trialReconcileRequest, 0, len(batch))
		for _, account := range batch {
			resourceName := datamodel.TrialResourceNameForAccount(account.Name, localRegion)
			if resourceName == "" {
				continue
			}
			requests = append(requests, trialReconcileRequest{
				Account:       account,
				ResourceNames: []string{resourceName},
			})
		}
		if len(requests) == 0 {
			continue
		}

		// One GetInternalTrial per request; Google is source of truth for exitReason.
		outcomes := reconcileTrials(ctx, a.CCFE, requests)
		for _, outcome := range outcomes {
			if outcome.Err != nil {
				// VCP has trialMode but CCFE has no InternalTrial info available
				// Leave VCP metadata unchanged until Google confirms desired behavior (clear trialMode vs no-op).
				if errors.Is(outcome.Err, trialclient.ErrTrialNotFound) {
					skippedNotFound++
					metrics.EmitTrialAccountSyncAccountOutcome("skipped_not_found")
					logger.Warn("Internal trial not found in CCFE for account with trial metadata in VCP",
						"accountName", outcome.Account.Name,
						"accountUUID", outcome.Account.UUID,
						"localRegion", localRegion,
						"trialResourceName", datamodel.TrialResourceNameForAccount(outcome.Account.Name, localRegion),
					)
					continue
				}
				lookupFailed++
				metrics.EmitTrialAccountSyncAccountOutcome("lookup_failed")
				logger.Error("GetInternalTrial failed",
					"accountName", outcome.Account.Name,
					"accountUUID", outcome.Account.UUID,
					"localRegion", localRegion,
					"error", outcome.Err,
				)
				continue
			}

			// Merge start/end/exitReason into account_metadata.trialMode.
			if err := a.SE.UpdateAccountTrialMetadata(ctx, outcome.Account, outcome.Trial.ToAccountTrialMode()); err != nil {
				persistFailed++
				metrics.EmitTrialAccountSyncAccountOutcome("persist_failed")
				logger.Error("Failed to persist trial metadata from Google",
					"accountName", outcome.Account.Name,
					"accountUUID", outcome.Account.UUID,
					"trialResourceName", outcome.Trial.Name,
					"error", err,
				)
				continue
			}
			persisted++
			metrics.EmitTrialAccountSyncAccountOutcome("persisted")
		}
	}

	// Activity succeeds even when some accounts fail; the next cron run retries them.
	logger.Info("Trial account sync completed",
		"eligible", len(accounts),
		"localRegion", localRegion,
		"batchSize", batchSize,
		"batches", batchCount,
		"persisted", persisted,
		"persistFailed", persistFailed,
		"lookupFailed", lookupFailed,
		"skippedNotFound", skippedNotFound,
	)
	metrics.EmitTrialAccountSyncRun("succeeded")
	return nil
}

type trialReconcileRequest struct {
	Account       *datamodel.Account
	ResourceNames []string
}

type trialReconcileOutcome struct {
	Account *datamodel.Account
	Trial   *datamodel.InternalTrial
	Err     error
}

// reconcileTrials resolves each request with per-account GetInternalTrial calls.
func reconcileTrials(ctx context.Context, client *trialclient.Client, requests []trialReconcileRequest) []trialReconcileOutcome {
	outcomes := make([]trialReconcileOutcome, len(requests))
	for i, req := range requests {
		outcome := trialReconcileOutcome{Account: req.Account}
		if len(req.ResourceNames) == 0 {
			outcome.Err = trialclient.ErrTrialNotFound
			outcomes[i] = outcome
			continue
		}

		for _, resourceName := range req.ResourceNames {
			trial, err := client.GetInternalTrial(ctx, resourceName)
			if errors.Is(err, trialclient.ErrTrialNotFound) {
				// Try next resource name, if any; caller logs when all lookups are not found.
				continue
			}
			if err != nil {
				outcome.Err = err
				break
			}
			outcome.Trial = trial
			break
		}
		if outcome.Trial == nil && outcome.Err == nil {
			outcome.Err = trialclient.ErrTrialNotFound
		}
		outcomes[i] = outcome
	}
	return outcomes
}
