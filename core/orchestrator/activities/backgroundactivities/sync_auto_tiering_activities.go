package backgroundactivities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	ontapModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

const (
	PoolConsumptionHotTier              = "hotTier"
	PoolConsumptionColdTier             = "coldTier"
	PoolConsumptionHotTierForAutoResize = "hotTierForAutoResize"
	PoolsToPauseKey                     = "poolsToPause"
	PoolsToResumeKey                    = "poolsToResume"
	PoolsToAutoResizeKey                = "poolsToAutoResize"
)

var (
	AutoTierHotTierAutoResizeThresholdPercent    = env.GetInt64("AUTO_TIER_HOT_TIER_AUTO_RESIZE_THRESHOLD_PERCENT", 100)
	AutoTierHotTierAutoResizeIncreasePercent     = env.GetFloat64("AUTO_TIER_HOT_TIER_AUTO_RESIZE_INCREASE_PERCENT", 10)
	autoTieringFastOntapConnection               = env.GetBool("AUTO_TIERING_FAST_ONTAP_CONNECTION", true)
	AllowAutogrowForHTBypassVolumeContainingPool = env.GetBool("ALLOW_AUTOGROW_FOR_HTBYPASS_VOLUME_CONTAINING_POOL", false)
)

type AutoTierSyncActivity struct {
	SE database.Storage
}

func (a *AutoTierSyncActivity) UpdateAggregatesInOntap(ctx context.Context, node *models.Node, tieringFullnessThreshold int64, aggrNames []string) error {
	activity.RecordHeartbeat(ctx, "Initializing aggregate update in ONTAP")
	logger := util.GetLogger(ctx)
	if len(aggrNames) == 0 {
		return nil
	}
	if node == nil {
		logger.Errorf("Node is nil")
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("node is nil")))
	}
	var provider vsa.Provider
	var err error
	if autoTieringFastOntapConnection {
		provider, err = vsa.GetProviderByNodeWithFastConnection(ctx, node)
	} else {
		provider, err = vsa.GetProviderByNode(ctx, node)
	}
	activity.RecordHeartbeat(ctx, "Retrieved provider for node")
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var errList []error
	for _, aggrName := range aggrNames {
		err = getAndUpdateAggregate(provider, aggrName, tieringFullnessThreshold)
		if err != nil {
			logger.Errorf("Failed to update aggregate %s: %v", aggrName, err)
			errList = append(errList, fmt.Errorf("failed to update aggregate %s: %w", aggrName, err))
		}
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Updated aggregate %s in ONTAP", aggrName))
	}

	if len(errList) > 0 {
		return errors.Join(errList...)
	}

	return nil
}

func getAndUpdateAggregate(provider vsa.Provider, aggrName string, tieringFullnessThreshold int64) error {
	aggr, err := provider.GetAggregateByName(aggrName)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Update aggregate on provider
	updateParams := vsa.UpdateAggregateParams{
		UUID:                     aggr.UUID,
		TieringFullnessThreshold: tieringFullnessThreshold,
	}
	if err = provider.UpdateAggregate(updateParams); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return nil
}

// SegregatePools takes a list of pools and separates them into auto-tier-resume and auto-tier-paused pools
// based on the hot-tier provisioned & cold-tier consumption.
func (a *AutoTierSyncActivity) SegregatePools(ctx context.Context, pools []*database.PoolIdentifier, poolConsumptionsMap map[string]map[string]float64) (map[string][]*database.PoolIdentifier, error) {
	activity.RecordHeartbeat(ctx, "Initializing pool segregation")
	logger := util.GetLogger(ctx)
	se := a.SE
	poolsToPause := make([]*database.PoolIdentifier, 0)
	poolsToResume := make([]*database.PoolIdentifier, 0)
	poolsToAutoResize := make([]*database.PoolIdentifier, 0)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, poolIdentifier := range pools {
		wg.Add(1)
		go func(poolIdentifier *database.PoolIdentifier) {
			defer wg.Done()
			// Fetch pool details from base pools table; this path does not require pool_view aggregates.
			pool, err := se.GetPoolByUUIDForAccount(ctx, poolIdentifier.UUID, poolIdentifier.AccountID)
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Fetched pool details for segregation: %s", poolIdentifier.UUID))
			if err != nil {
				logger.Errorf("Failed to get pool, error: %v", err)
				return
			}

			// Skip pools that are not configured for auto-tiering
			if !pool.AllowAutoTiering {
				return
			}

			poolConsumption, ok := poolConsumptionsMap[pool.UUID]
			if !ok {
				// If no consumption data is available for the pool, log it and skip it.
				logger.Errorf("Pool does not have consumption data in map, poolUUID: %s", pool.UUID)
				return
			}

			// Check if the pool is eligible for auto-tiering pause or resume
			// Conditions to pause auto-tiering:
			// 1. Hot tier size + cold tier consumption >= logical pool size.
			// 2. Auto-tiering is not yet paused.
			// Conditions to resume auto-tiering:
			// 1. Hot tier size + cold tier consumption < logical pool size.
			// 2. Auto-tiering is currently paused.
			if pool.AutoTieringConfig.HotTierSizeInBytes+int64(poolConsumption[PoolConsumptionColdTier]) >= pool.SizeInBytes {
				if pool.APIAccessMode != common.ONTAPMode && pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPaused {
					mu.Lock()
					poolsToPause = append(poolsToPause, poolIdentifier)
					mu.Unlock()
				}
			} else {
				if pool.APIAccessMode != common.ONTAPMode && pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusResumed {
					mu.Lock()
					poolsToResume = append(poolsToResume, poolIdentifier)
					mu.Unlock()
				}

				// Check if the pool is eligible for auto-resizing of hot tier
				// Conditions:
				// 1. Auto-resize flag is enabled.
				// 2. Pool is not eligible for pausing AT, since resizing will definitely exceed the logical pool size.
				// 3. Hot tier usage exceeds the defined threshold percentage.
				// 4. New hot tier provisioned size + cold tier consumption < logical pool size.
				// 5. Pool is in READY state.
				// 6. When feature flag is disabled: No volumes in the pool have bypass mode enabled.
				//    When feature flag is enabled: Uses adjusted hot tier consumption (excludes bypass-enabled volumes)
				//    to avoid false positives from temporary spikes.
				if pool.AutoTieringConfig.EnableHotTierAutoResize && pool.AutoTieringConfig.HotTierSizeInBytes != 0 && pool.State == datamodel.LifeCycleStateREADY {
					// Use different hot tier consumption based on feature flag
					hotTierForUsageCalc := poolConsumption[PoolConsumptionHotTier]
					if AllowAutogrowForHTBypassVolumeContainingPool {
						// When flag is enabled, use adjusted consumption that excludes bypass volumes
						hotTierForUsageCalc = poolConsumption[PoolConsumptionHotTierForAutoResize]
					} else {
						// When flag is disabled, check if any volume has bypass mode enabled and skip the pool if so
						exists, err := checkPoolVolumesWithBypassModeEnabled(ctx, se, pool)
						activity.RecordHeartbeat(ctx, fmt.Sprintf("Checked pool volumes for bypass mode: %s", pool.UUID))
						if err != nil {
							logger.Errorf("Failed to check pool volumes for bypass mode, poolUUID: %s, error: %v", pool.UUID, err)
							return
						}

						if exists {
							logger.Infof("Skipping hot tier autoresize for pool with volumes having bypass mode enabled, poolUUID: %s", pool.UUID)
							return
						}
					}

					usagePercent := (int64(hotTierForUsageCalc) * 100) / pool.AutoTieringConfig.HotTierSizeInBytes
					// We are increasing the hot tier size by 10%. The result is in round off GiB.
					newHotTierSizeInBytes := int64(float64(pool.AutoTieringConfig.HotTierSizeInBytes)*(1+0.01*AutoTierHotTierAutoResizeIncreasePercent)/(1<<30)) * (1 << 30)

					if usagePercent >= AutoTierHotTierAutoResizeThresholdPercent && (newHotTierSizeInBytes+int64(poolConsumption[PoolConsumptionColdTier]) < pool.SizeInBytes) {
						mu.Lock()
						poolsToAutoResize = append(poolsToAutoResize, poolIdentifier)
						mu.Unlock()
					}
				}
			}
		}(poolIdentifier)
	}
	wg.Wait()

	activity.RecordHeartbeat(ctx, "Completed pool segregation")
	return map[string][]*database.PoolIdentifier{
		PoolsToPauseKey:      poolsToPause,
		PoolsToResumeKey:     poolsToResume,
		PoolsToAutoResizeKey: poolsToAutoResize,
	}, nil
}

// checkPoolVolumesWithBypassModeEnabled checks if any volume in the pool has hot tier bypass mode enabled.
// Used when the feature flag is disabled to skip pools with bypass-enabled volumes from auto-resize.
func checkPoolVolumesWithBypassModeEnabled(ctx context.Context, se database.Storage, pool *datamodel.Pool) (bool, error) {
	logger := util.GetLogger(ctx)
	if pool.APIAccessMode == common.ONTAPMode {
		logger.Infof("Skipping check for volumes with bypass mode enabled since pool is in ONTAP mode, poolUUID: %s", pool.UUID)
		return false, nil
	}
	volumes, err := se.GetVolumesByPoolIDNoPreload(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to list volumes for pool: %s, error: %v", pool.UUID, err)
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	for _, vol := range volumes {
		if vol.AutoTieringEnabled && vol.AutoTieringPolicy != nil && vol.AutoTieringPolicy.HotTierBypassModeEnabled {
			return true, nil
		}
	}

	return false, nil
}

func parseVlmConfig(pool *datamodel.Pool) (*vlm.VLMConfig, error) {
	currentVlmConfig := &vlm.VLMConfig{}

	if err := json.Unmarshal([]byte(pool.VLMConfig), currentVlmConfig); err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError, err))
	}
	return currentVlmConfig, nil
}

func (a *AutoTierSyncActivity) FetchAndSavePoolsTieringInfo(ctx context.Context, pools []*database.PoolIdentifier) (map[string]map[string]float64, error) {
	activity.RecordHeartbeat(ctx, "Initializing pool tiering info fetch and save")
	logger := util.GetLogger(ctx)
	se := a.SE
	poolsConsumptionsMap := make(map[string]map[string]float64)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, poolIdentifier := range pools {
		wg.Add(1)
		go func(poolIdentifier *database.PoolIdentifier) {
			defer wg.Done()
			// Fetch pool details from base pools table; this path does not require pool_view aggregates.
			pool, err := se.GetPoolByUUIDForAccount(ctx, poolIdentifier.UUID, poolIdentifier.AccountID)
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Fetched pool details for pool: %s", poolIdentifier.UUID))
			if err != nil {
				logger.Errorf("Failed to get pool, error: %v", err)
				return
			}

			// Skip pools that are not configured for auto-tiering
			if !pool.AllowAutoTiering {
				return
			}

			var provider vsa.Provider
			if autoTieringFastOntapConnection {
				provider, err = GetOntapRestProviderForPoolFastConn(ctx, se, pool)
			} else {
				provider, err = GetOntapRestProviderForPool(ctx, se, pool)
			}
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Retrieved ONTAP provider for pool: %s", pool.UUID))
			if err != nil || provider == nil {
				logger.Errorf("Failed to get ONTAP rest provider for pool %v: %v", pool.UUID, err)
				return
			}

			// Check and collect pools which have a tiering fullness threshold set as 50 and not
			// paused/partially-paused. These pools need to have their tiering fullness threshold set to 0
			if pool.AutoTieringConfig != nil && pool.AutoTieringConfig.TieringFullnessThreshold == 50 && pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPaused && pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPartiallyPaused {
				// Parse vlm config to find all the aggregates in a cluster
				config, err := parseVlmConfig(pool)
				if err != nil {
					logger.Errorf("Failed to parse vlm config for pool %s, Error: %v", pool.Name, err)
					return
				}
				// Update all aggregates in the cluster
				allAggregatesUpdated := true
				for _, aggr := range config.DataAggr {
					activity.RecordHeartbeat(ctx, fmt.Sprintf("Updating aggregate threshold for pool: %s, aggregate: %s", pool.UUID, aggr.Name))
					err = getAndUpdateAggregate(provider, aggr.Name, 0)
					if err != nil {
						allAggregatesUpdated = false
						logger.Errorf("Failed to set aggregate threshold to 0 in ontap for pool %s, aggregate: %s, Error: %v", pool.Name, aggr.Name, err)
					}
				}

				activity.RecordHeartbeat(ctx, fmt.Sprintf("Updated aggregate threshold for pool: %s", pool.UUID))
				if allAggregatesUpdated {
					err = se.UpdatePoolTieringConfig(ctx, poolIdentifier.UUID, nil, nil, nillable.GetInt64Ptr(0), nil)
					activity.RecordHeartbeat(ctx, fmt.Sprintf("Updated pool tiering config in database for pool: %s", pool.UUID))
					if err != nil {
						// Logging error and skipping. Will retry in next sync.
						logger.Warnf("Failed to set thresholdPercentage field to 0 in db for pool %s, Error: %v", pool.Name, err)
					}
				}
			}

			ontapVolumes, err := provider.GetVolumes()
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Retrieved volumes from ONTAP for pool: %s", pool.UUID))
			if err != nil {
				logger.Errorf("Failed to get ONTAP volumes for the pool: %s, %v", pool.UUID, err)
				return
			}

			// Get DB volumes and calculate hot/cold tier consumption (ONTAP vs non-ONTAP use different volume types; persistence only for non-ONTAP)
			var hotTierConsumption, coldTierConsumption, hotTierConsumptionForAutoResize int64
			var calcErr error
			if pool.APIAccessMode == common.ONTAPMode {
				dbVolumes, err := se.ListExpertModeVolumesByPoolID(ctx, pool.ID)
				if err != nil {
					logger.Errorf("Failed to get volumes from database for pool %s: %v", pool.UUID, err)
					return
				}
				activity.RecordHeartbeat(ctx, fmt.Sprintf("Retrieved volumes from database for pool: %s", pool.UUID))
				dbVolumeMap := make(map[string]*datamodel.ExpertModeVolumes, len(dbVolumes))
				for _, v := range dbVolumes {
					if v.ExternalUUID != "" {
						dbVolumeMap[v.ExternalUUID] = v
					}
				}
				getDBUUID := func(extUUID string) (string, bool) {
					v, ok := dbVolumeMap[extUUID]
					if !ok || v == nil {
						return "", false
					}
					return v.UUID, true
				}
				hotTierConsumption, coldTierConsumption, hotTierConsumptionForAutoResize, calcErr = calculateAndUpdateHotColdTierConsumption(ctx, ontapVolumes, int64(len(dbVolumes)), getDBUUID, nil, false, nil)
			} else {
				dbVolumes, err := se.GetVolumesByPoolIDNoPreload(ctx, pool.ID)
				if err != nil {
					logger.Errorf("Failed to get volumes from database for pool %s: %v", pool.UUID, err)
					return
				}
				activity.RecordHeartbeat(ctx, fmt.Sprintf("Retrieved volumes from database for pool: %s", pool.UUID))
				dbVolumeMap := make(map[string]*datamodel.Volume, len(dbVolumes))
				for _, v := range dbVolumes {
					if v.VolumeAttributes != nil {
						dbVolumeMap[v.VolumeAttributes.ExternalUUID] = v
					}
				}
				getDBUUID := func(extUUID string) (string, bool) {
					v, ok := dbVolumeMap[extUUID]
					if !ok || v == nil {
						return "", false
					}
					return v.UUID, true
				}
				// Only exclude bypass-enabled volumes from hot tier consumption when the feature flag is enabled.
				// When the flag is disabled, we use the old behavior where bypass volumes block auto-resize entirely.
				var shouldExcludeFromHTConsumption shouldExcludeFromHTConsumptionFunc
				if AllowAutogrowForHTBypassVolumeContainingPool {
					shouldExcludeFromHTConsumption = func(extUUID string) bool {
						v, ok := dbVolumeMap[extUUID]
						if !ok || v == nil || v.AutoTieringPolicy == nil {
							return false
						}
						return v.AutoTieringEnabled && v.AutoTieringPolicy.HotTierBypassModeEnabled
					}
				}
				hotTierConsumption, coldTierConsumption, hotTierConsumptionForAutoResize, calcErr = calculateAndUpdateHotColdTierConsumption(ctx, ontapVolumes, int64(len(dbVolumes)), getDBUUID, shouldExcludeFromHTConsumption, true, se)
			}
			if calcErr != nil {
				logger.Errorf("Failed to calculate hot/cold tier consumption for the pool: %s, %v", pool.UUID, calcErr)
				return
			}
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Calculated hot/cold tier consumption for pool: %s", pool.UUID))

			logger.Infof("Fetched pool tier consumption from ONTAP, poolUUID: %s, hotTierConsumption: %d, coldTierConsumption: %d", pool.UUID, hotTierConsumption, coldTierConsumption)

			mu.Lock()
			poolsConsumptionsMap[pool.UUID] = map[string]float64{
				PoolConsumptionHotTier:              float64(hotTierConsumption),
				PoolConsumptionColdTier:             float64(coldTierConsumption),
				PoolConsumptionHotTierForAutoResize: float64(hotTierConsumptionForAutoResize),
			}
			mu.Unlock()
		}(poolIdentifier)
	}
	wg.Wait()

	activity.RecordHeartbeat(ctx, "Completed fetching and saving pool tiering info")
	return poolsConsumptionsMap, nil
}

// getDBVolumeUUIDFunc returns the database volume UUID for a given external (ONTAP) volume UUID.
// Used to unify hot/cold tier consumption logic for both Volume and ExpertModeVolumes.
type getDBVolumeUUIDFunc func(externalUUID string) (dbUUID string, ok bool)

// shouldExcludeFromHTConsumptionFunc checks if a volume should be excluded from hot tier consumption calculation.
// Used to exclude bypass-enabled volumes from hot tier consumption to avoid false positives in auto-resize decisions
// (bypass volumes temporarily spike hot tier usage as data first goes to hot tier before moving to cold tier).
type shouldExcludeFromHTConsumptionFunc func(externalUUID string) bool

// calculateAndUpdateHotColdTierConsumption computes hot/cold tier consumption from ONTAP volumes.
// When persistTieringToDB is true, tiering fields are bulk-updated via se; for ONTAP mode pools
// pass false (expert mode volumes do not use this update path).
// When shouldExcludeFromHTConsumption is provided and returns true, the volume's hot tier footprint is
// excluded from the adjusted hot tier consumption (used for auto-resize decisions to avoid triggers from temporary spikes).
// Returns: totalHotTier (for DB storage), coldTier, adjustedHotTier (for auto-resize decisions), error
func calculateAndUpdateHotColdTierConsumption(
	ctx context.Context,
	ontapVolumes []*vsa.Volume,
	expectedVolCount int64,
	getDBVolumeUUID getDBVolumeUUIDFunc,
	shouldExcludeFromHTConsumption shouldExcludeFromHTConsumptionFunc,
	persistTieringToDB bool,
	se database.Storage,
) (int64, int64, int64, error) {
	logger := util.GetLogger(ctx)
	hotTierConsumption := int64(0)
	hotTierConsumptionForAutoResize := int64(0)
	coldTierConsumption := int64(0)
	volCount := 0
	tieringUpdates := make(map[string]datamodel.VolumeTieringUpdate)

	for _, volume := range ontapVolumes {
		if volume == nil || volume.IsSvmRoot == nil || *volume.IsSvmRoot {
			continue
		}
		if volume.Space == nil ||
			volume.Space.CapacityTierFootprint == nil || volume.Space.PerformanceTierFootprint == nil ||
			volume.Space.LogicalSpace == nil || volume.Space.LogicalSpace.Used == nil {
			continue
		}

		volCount++

		coldTierFootprint := float64(*volume.Space.CapacityTierFootprint)
		hotTierFootprint := float64(*volume.Space.PerformanceTierFootprint)
		logicalSpaceUsed := float64(*volume.Space.LogicalSpace.Used)

		denominator := coldTierFootprint + hotTierFootprint
		if denominator == 0 {
			continue
		}
		ratio := coldTierFootprint / denominator

		// Correcting the cold and hot tier consumption based on logical space used
		// to avoid over counting where data reduction/compression is applied via ONTAP
		logicalColdTierConsumption := logicalSpaceUsed * ratio
		logicalHotTierConsumption := logicalSpaceUsed - logicalColdTierConsumption

		dbUUID, ok := getDBVolumeUUID(*volume.UUID)
		if !ok {
			logger.Errorf("Volume with external UUID %s not found in database volume map", *volume.UUID)
			continue
		}

		tieringUpdates[dbUUID] = datamodel.VolumeTieringUpdate{
			HotTierSizeGib:  uint64(utils.ConvertBytesToGib(logicalHotTierConsumption)),
			ColdTierSizeGib: uint64(utils.ConvertBytesToGib(logicalColdTierConsumption)),
		}

		coldTierConsumption += int64(logicalColdTierConsumption)
		hotTierConsumption += int64(logicalHotTierConsumption)
		// Exclude bypass-enabled volumes from auto-resize hot tier calculation to avoid false positives
		// (bypass volumes temporarily spike hot tier usage as data goes to hot tier first)
		if shouldExcludeFromHTConsumption == nil || !shouldExcludeFromHTConsumption(*volume.UUID) {
			hotTierConsumptionForAutoResize += int64(logicalHotTierConsumption)
		}
	}

	if volCount != int(expectedVolCount) {
		logger.Warnf("mismatch in vol count fetched from db and ontap, expectedDBCount: %d, ontapCount: %d", expectedVolCount, volCount)
	}

	if persistTieringToDB && se != nil && len(tieringUpdates) > 0 {
		if err := se.BatchUpdateVolumeTieringFields(ctx, tieringUpdates); err != nil {
			logger.Errorf("Failed to bulk update volume tiering footprints in DB: %v", err)
		} else {
			logger.Infof("Successfully bulk updated tiering fields for %d volumes", len(tieringUpdates))
		}
	}

	return hotTierConsumption, coldTierConsumption, hotTierConsumptionForAutoResize, nil
}

func (a *AutoTierSyncActivity) ToggleHotTierBypassModeForPoolVolumes(ctx context.Context, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Initializing hot tier bypass mode toggle for pool volumes")
	logger := util.GetLogger(ctx)
	se := a.SE
	var errList []error

	var provider vsa.Provider
	var err error
	if autoTieringFastOntapConnection {
		provider, err = GetOntapRestProviderForPoolFastConn(ctx, se, pool)
	} else {
		provider, err = GetOntapRestProviderForPool(ctx, se, pool)
	}
	activity.RecordHeartbeat(ctx, "Retrieved ONTAP provider for pool")
	if err != nil || provider == nil {
		logger.Errorf("Failed to get ONTAP rest provider for pool %v: %v", pool.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	volumes, err := se.GetVolumesByPoolIDNoPreload(ctx, pool.ID)
	activity.RecordHeartbeat(ctx, "Retrieved volumes from database for pool")
	if err != nil {
		logger.Errorf("Failed to list volumes for pool: %s, error: %v", pool.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	for _, vol := range volumes {
		if vol.AutoTieringEnabled && vol.AutoTieringPolicy != nil && vol.AutoTieringPolicy.HotTierBypassModeEnabled {
			updateParams := vsa.UpdateVolumeParams{
				UUID: vol.VolumeAttributes.ExternalUUID,
				TieringPolicy: &vsa.TieringPolicy{
					CoolAccessTieringPolicy: ontapModels.VolumeInlineTieringPolicyNone,
					CloudWriteModeEnabled:   nillable.GetBoolPtr(false),
				},
			}

			if pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPaused && pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPartiallyPaused {
				updateParams.TieringPolicy.CoolAccessTieringPolicy = ontapModels.VolumeInlineTieringPolicyAll
				updateParams.TieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(true)
			}

			if err = provider.UpdateVolume(updateParams); err != nil {
				// Logging error, adding it to the list and moving on to the next volume.
				// Using the error list, the retry will happen at the end.
				logger.Errorf("Failed to change tiering policy to: %s for volume: %s, error: %v", updateParams.TieringPolicy.CoolAccessTieringPolicy, vol.Name, err)
				errList = append(errList, fmt.Errorf("failed to change tiering policy to: %s for volume: %s, error: %s", updateParams.TieringPolicy.CoolAccessTieringPolicy, vol.Name, err.Error()))
			} else {
				activity.RecordHeartbeat(ctx, fmt.Sprintf("Updated tiering policy for volume: %s", vol.UUID))
				logger.Infof("Tiering policy changed to: %s for volume: %s", updateParams.TieringPolicy.CoolAccessTieringPolicy, vol.UUID)
			}
		}
	}

	if len(errList) > 0 {
		var finalError error
		for _, er := range errList {
			finalError = errors.Join(finalError, er)
		}
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, finalError),
		)
	}
	activity.RecordHeartbeat(ctx, "Completed hot tier bypass mode toggle for pool volumes")
	return nil
}

func (a *AutoTierSyncActivity) UpdatePoolTieringConsumptionInDB(ctx context.Context, poolsConsumptionsMap map[string]map[string]float64) error {
	activity.RecordHeartbeat(ctx, "Initializing pool tiering consumption update in database")
	logger := util.GetLogger(ctx)
	se := a.SE

	for poolUUID, consumptionMap := range poolsConsumptionsMap {
		hotTierConsumption := int64(consumptionMap[PoolConsumptionHotTier])
		coldTierConsumption := int64(consumptionMap[PoolConsumptionColdTier])

		err := se.UpdatePoolTieringConfig(ctx, poolUUID, nillable.GetInt64Ptr(hotTierConsumption), nillable.GetInt64Ptr(coldTierConsumption), nil, nil)
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Updated pool tiering consumption in DB for pool: %s", poolUUID))
		if err != nil {
			return fmt.Errorf("failed to update pool tiering consumption in DB for poolUUID: %s, error: %v", poolUUID, err)
		}

		logger.Infof("Updated pool tiering consumption in DB, poolUUID: %s, hotTierConsumption: %d, coldTierConsumption: %d", poolUUID, hotTierConsumption, coldTierConsumption)
	}
	activity.RecordHeartbeat(ctx, "Completed updating pool tiering consumption in database")
	return nil
}

func (a *AutoTierSyncActivity) UpdatePoolTieringThresholdAndStatus(ctx context.Context, poolUUID string, tieringThreshold int64, tieringStatus datamodel.TieringStatus) error {
	se := a.SE

	err := se.UpdatePoolTieringConfig(ctx, poolUUID, nil, nil, nillable.GetInt64Ptr(tieringThreshold), &tieringStatus)
	if err != nil {
		return fmt.Errorf("failed to update pool tiering consumption in DB for poolUUID: %s, error: %v", poolUUID, err)
	}
	return nil
}
