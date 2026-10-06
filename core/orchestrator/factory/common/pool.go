package common

import (
	"context"
	"database/sql"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// CreatePoolJob creates a job for pool creation workflow
func CreatePoolJob(ctx context.Context, params *commonparams.CreatePoolParams, account *datamodel.Account, dbPool *datamodel.Pool) *datamodel.Job {
	poolCategory := models.GetPoolCategory(params.LargeCapacity)
	jobType := string(models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationCreate, poolCategory))
	return &datamodel.Job{
		Type:          jobType,
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbPool.UUID,
		},
	}
}

// HandleCreatePoolError handles errors during pool creation by updating job status
func HandleCreatePoolError(ctx context.Context, se database.Storage, createdJob *datamodel.Job, err error) {
	logger := util.GetLogger(ctx)
	if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
		logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
	}
}

// CleanupPoolOnError deletes the pool if an error occurred during creation
func CleanupPoolOnError(ctx context.Context, se database.Storage, dbPool *datamodel.Pool, err error) {
	if err != nil && dbPool != nil {
		logger := util.GetLogger(ctx)
		if poolDeleteErr := se.DeletePool(ctx, dbPool); poolDeleteErr != nil {
			logger.Error("Failed to delete pool", "PoolID", dbPool.UUID, "error", poolDeleteErr)
		}
	}
}

// PoolCredentialSetupFunc is a function type for vendor-specific pool credential setup
type PoolCredentialSetupFunc func(poolObj *datamodel.Pool, params *commonparams.CreatePoolParams, accountName string)

// CreatePoolInDB creates a pool object in the database
// It accepts vendor-specific function for credential setup
func CreatePoolInDB(
	ctx context.Context,
	se database.Storage,
	params *commonparams.CreatePoolParams,
	account *datamodel.Account,
	logger log.Logger,
	setupCredentials PoolCredentialSetupFunc,
	tieringFullnessThreshold int,
) (*datamodel.Pool, error) {
	var throughputMibps, iops int64
	if params.CustomPerformanceParams != nil {
		throughputMibps = params.CustomPerformanceParams.ThroughputMibps
		if params.CustomPerformanceParams.Iops != nil {
			iops = *params.CustomPerformanceParams.Iops
		}
	}

	poolAttrs := &datamodel.PoolAttributes{
		ThroughputMibps: throughputMibps,
		Iops:            iops,
		PrimaryZone:     params.PrimaryZone,
		SecondaryZone:   params.SecondaryZone,
		Labels:          params.Labels,
		IsRegionalHA:    params.IsRegionalHA,
		LdapEnabled:     params.LdapEnabled,
		AccountName:     account.Name,
		IsTrueZonal:     params.IsTrueZonal,
		ServiceLevel:    params.ServiceLevel,
		LvHAPairs:       int64(params.LvHAPairs),
	}
	if !commonparams.IsPORequest(params) {
		poolAttrs.IsZoneSwitched = false
		poolAttrs.ZoneSwitchState = datamodel.ZonePrimary
	}

	poolObj := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: utils.RandomUUID(),
		},
		Name:             params.Name,
		Account:          account,
		AccountID:        account.ID,
		VendorID:         params.VendorID,
		Network:          params.VendorSubNetID,
		SizeInBytes:      int64(params.SizeInBytes),
		AllowAutoTiering: params.AllowAutoTiering,
		Description:      params.Description,
		ServiceLevel:     params.ServiceLevel,
		QosType:          params.QosType,
		LargeCapacity:    params.LargeCapacity,
		IsPreview:        params.IsPreview,
		AutoTieringConfig: &datamodel.AutoTieringConfig{
			HotTierSizeInBytes:       int64(params.HotTierSizeInBytes),
			EnableHotTierAutoResize:  params.EnableHotTierAutoResize,
			TieringStatus:            datamodel.TieringStatusResumed,
			TieringFullnessThreshold: int64(tieringFullnessThreshold),
		},
		PoolAttributes: poolAttrs,
		APIAccessMode:  params.Mode,
	}
	if params.ClusterName != "" {
		poolObj.ClusterDetails = datamodel.ClusterDetails{ExternalName: params.ClusterName}
	}

	if params.HostUUID != "" {
		poolObj.HostUUID = params.HostUUID
	}

	if params.KmsConfig != nil {
		poolObj.KmsConfigID = sql.NullInt64{
			Int64: params.KmsConfig.ID,
			Valid: true,
		}
	}

	if params.ActiveDirectoryId != "" && params.ADExistsInVCP {
		poolObj.ActiveDirectoryID = sql.NullInt64{
			Int64: params.ActiveDirectory.ID,
			Valid: true,
		}
	}

	poolObj.DeploymentName = utils.GenerateDeterministicDeploymentName(poolObj.AccountID, poolObj.UUID, params.Region)
	logger.Infof("generated deployment name: %s", poolObj.DeploymentName)

	// Call vendor-specific credential setup if provided
	if setupCredentials != nil {
		setupCredentials(poolObj, params, account.Name)
	}

	dbPool, err := se.CreatingPool(ctx, poolObj)
	if err != nil {
		logger.Error("Failed to create pool in database", "error", err)
		return nil, err
	}
	return dbPool, nil
}

// ConvertDatastorePoolToModel converts a datastore pool view to a model pool
func ConvertDatastorePoolToModel(pool *datamodel.PoolView, accountName string) *models.Pool {
	labels := make(map[string]string)
	if pool.PoolAttributes != nil && pool.PoolAttributes.Labels != nil {
		labels = utils.ConvertJSONBToMap(pool.PoolAttributes.Labels)
	}

	var autoTieringConfig *models.AutoTieringConfig
	if pool.AutoTieringConfig != nil {
		autoTieringConfig = &models.AutoTieringConfig{
			HotTierSizeInBytes:      uint64(pool.AutoTieringConfig.HotTierSizeInBytes),
			EnableHotTierAutoResize: pool.AutoTieringConfig.EnableHotTierAutoResize,
			HotTierConsumption:      pool.AutoTieringConfig.HotTierConsumption,
			ColdTierConsumption:     pool.AutoTieringConfig.ColdTierConsumption,
		}
	}

	ldapEnabled := false
	if pool.PoolAttributes != nil {
		ldapEnabled = pool.PoolAttributes.LdapEnabled
	}

	poolRes := &models.Pool{
		BaseModel: models.BaseModel{
			UUID:      pool.UUID,
			CreatedAt: pool.CreatedAt,
			UpdatedAt: pool.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(pool.DeletedAt),
		},
		AccountName:      accountName,
		Name:             pool.Name,
		Description:      pool.Description,
		SizeInBytes:      uint64(pool.SizeInBytes),
		State:            pool.State,
		StateDetails:     pool.StateDetails,
		AllowAutoTiering: pool.AllowAutoTiering,
		VendorSubNetID:   pool.Network,
		ServiceLevel:     pool.ServiceLevel,
		QosType:          pool.QosType,
		DeploymentName:   pool.DeploymentName,
		LargeCapacity:    pool.LargeCapacity,
		IsPreview:        pool.IsPreview,
		PoolAttributes: &models.PoolAttributes{
			AllocatedBytes:  float64(pool.QuotaInBytes),
			NumberOfVolumes: pool.VolumeCount,
			PrimaryZone:     pool.PoolAttributes.PrimaryZone,
			SecondaryZone:   pool.PoolAttributes.SecondaryZone,
			Labels:          labels,
			IsRegionalHA:    pool.PoolAttributes.IsRegionalHA,
			IsZoneSwitched:  pool.PoolAttributes.IsZoneSwitched,
			ZoneSwitchState: pool.PoolAttributes.ZoneSwitchState,
			LdapEnabled:     ldapEnabled,
			IsTrueZonal:     pool.PoolAttributes.IsTrueZonal,
			LvHAPairs:       pool.PoolAttributes.LvHAPairs,
		},
		AutoTieringConfig: autoTieringConfig,
		CustomPerformanceParams: &models.CustomPerformanceParams{
			Enabled:    true,
			Throughput: float64(pool.PoolAttributes.ThroughputMibps),
			Iops:       pool.PoolAttributes.Iops,
		},
		Account: &models.Account{
			Name: accountName,
		},
		SatisfiesPzi:            pool.SatisfyZI,
		SatisfiesPzs:            pool.SatisfyZS,
		APIAccessMode:           pool.APIAccessMode,
		TotalThroughputMibps:    float64(pool.PoolAttributes.ThroughputMibps),
		UtilizedThroughputMibps: float64(pool.Throughput),
		TotalIops:               pool.PoolAttributes.Iops,
		UtilizedIops:            pool.Iops,
		HostUUID:                pool.HostUUID,
	}
	if pool.ClusterDetails.ExternalName != "" {
		poolRes.ClusterDetails = &models.ClusterDetails{
			ExternalName: pool.ClusterDetails.ExternalName,
		}
	}

	if pool.Account != nil && &pool.Account.ID != nil {
		poolRes.Account.ID = pool.Account.ID
	}

	if pool.ActiveDirectory != nil {
		poolRes.ActiveDirectoryConfigId = pool.ActiveDirectory.UUID
		poolRes.ActiveDirectoryResourceId = pool.ActiveDirectory.AdName
		poolRes.ActiveDirectory = ConvertDatastoreActiveDirectoryToModel(pool.ActiveDirectory)
	}

	if pool.KmsConfig != nil {
		poolRes.KmsConfig = &models.KmsConfig{
			BaseModel: models.BaseModel{
				UUID:      pool.KmsConfig.UUID,
				CreatedAt: pool.KmsConfig.CreatedAt,
				UpdatedAt: pool.KmsConfig.UpdatedAt,
				DeletedAt: utils.DeletedAtOrNil(pool.KmsConfig.DeletedAt),
			},
			Name:              pool.KmsConfig.Name,
			Description:       pool.KmsConfig.Description,
			State:             pool.KmsConfig.State,
			StateDetails:      pool.KmsConfig.StateDetails,
			KeyRing:           pool.KmsConfig.KeyRing,
			KeyRingLocation:   pool.KmsConfig.KeyRingLocation,
			KeyName:           pool.KmsConfig.KeyName,
			AccountID:         pool.KmsConfig.AccountID,
			CustomerProjectID: pool.KmsConfig.CustomerProjectID,
			KeyProjectID:      pool.KmsConfig.KeyProjectID,
			ResourceID:        pool.KmsConfig.ResourceID,
		}
	}

	if pool.AssetMetadata != nil {
		poolRes.AssetMetadata = &models.AssetMetadata{}
		for _, originalPoolAssetMetadata := range pool.AssetMetadata.ChildAssets {
			var assetMetadata models.ChildAsset
			assetMetadata.AssetNames = originalPoolAssetMetadata.AssetNames
			assetMetadata.AssetType = originalPoolAssetMetadata.AssetType
			poolRes.AssetMetadata.ChildAssets = append(poolRes.AssetMetadata.ChildAssets, assetMetadata)
		}
	}

	return poolRes
}

// CheckActiveUpgradeJob returns the active upgrade job for a cluster, or nil
// if none is in PENDING / IN_PROGRESS state.
func CheckActiveUpgradeJob(ctx context.Context, se database.Storage, clusterID string) (*datamodel.ClusterUpgradeJob, error) {
	jobs, err := se.GetClusterUpgradeJobsByClusterID(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.Status == string(models.UpgradeStatusPending) || job.Status == string(models.UpgradeStatusInProgress) {
			return job, nil
		}
	}
	return nil, nil
}

// HasActiveClusterUpgrade returns true if the given cluster (pool UUID) has an
// active upgrade job (PENDING or IN_PROGRESS).
func HasActiveClusterUpgrade(ctx context.Context, se database.Storage, clusterID string) (bool, error) {
	job, err := CheckActiveUpgradeJob(ctx, se, clusterID)
	return job != nil, err
}

// UpdateUpgradeJobStatus updates the status of an upgrade job.
func UpdateUpgradeJobStatus(ctx context.Context, se database.Storage, jobUUID, status, errorMessage string) error {
	upgradeJob, err := se.GetClusterUpgradeJobByUUID(ctx, jobUUID)
	if err != nil {
		return err
	}

	upgradeJob.Status = status
	upgradeJob.UpdatedAt = time.Now()

	upgradeJob.ErrorDetails = nil
	if errorMessage != "" {
		upgradeJob.ErrorDetails = &datamodel.UpgradeErrorDetails{
			ErrorCode:    "UPGRADE_FAILED",
			ErrorMessage: errorMessage,
			ErrorType:    "UPGRADE_ERROR",
			Retryable:    true,
		}
	}

	if status == string(models.UpgradeStatusCompleted) {
		now := time.Now()
		upgradeJob.CompletedAt = &now
	} else if status == string(models.UpgradeStatusInProgress) {
		now := time.Now()
		upgradeJob.StartedAt = &now
	}

	return se.UpdateClusterUpgradeJob(ctx, upgradeJob)
}

// ConvertMetadataToJSONB converts map[string]string to *datamodel.JSONB.
func ConvertMetadataToJSONB(metadata map[string]string) *datamodel.JSONB {
	if metadata == nil {
		return nil
	}
	result := make(datamodel.JSONB)
	for k, v := range metadata {
		result[k] = v
	}
	return &result
}
