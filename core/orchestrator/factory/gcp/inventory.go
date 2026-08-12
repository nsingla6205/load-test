package gcp

import (
	"context"
	"fmt"
	"strings"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const defaultStorageEfficiencyFactor = 1.4

type inventoryAggregate struct {
	name        string
	uuid        string
	state       string
	volumeCount int64
	size        int64
}

type inventoryVolume struct {
	name           string
	uuid           string
	svmName        string
	size           int64
	aggregateNames []string
}

type inventorySVM struct {
	name string
}

func (o *GCPOrchestrator) GetProjectInventory(ctx context.Context, params *commonparams.GetProjectInventoryParams) (*commonparams.ProjectInventoryResult, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, o.storage, params.ProjectNumber)
	if err != nil {
		if !customerrors.IsNotFoundErr(err) {
			return nil, fmt.Errorf("get account for project %s: %w", params.ProjectNumber, err)
		}
		if _, cerr := createAccount(ctx, o.storage, params.ProjectNumber); cerr != nil {
			logger.WarnContext(ctx, "Account created concurrently; returning empty inventory",
				"projectNumber", params.ProjectNumber)
		}
		// Brand-new account: no clusters linked yet.
		return &commonparams.ProjectInventoryResult{}, nil
	}

	if account.DeletedAt != nil || account.State == datamodel.AccountStateDisabled {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrAccountNotFound,
			fmt.Errorf("account for project %s is disabled or deleted", params.ProjectNumber))
	}

	hostCreds, err := o.storage.ListPinnedHostCredentials(ctx, params.ProjectNumber)
	if err != nil {
		return nil, fmt.Errorf("list pinned host credentials for project %s: %w", params.ProjectNumber, err)
	}
	if len(hostCreds) == 0 {
		return &commonparams.ProjectInventoryResult{}, nil
	}

	clusters := make([]commonparams.InventoryCluster, 0, len(hostCreds))
	for _, hc := range hostCreds {
		cluster, err := o.fetchClusterInventory(ctx, hc, params.ShowVolumes)
		if err != nil {
			// Non-fatal: log and skip the failed cluster.
			clusterName := ""
			if hc != nil && hc.Host != nil {
				clusterName = database.HostExternalName(hc.Host)
			}
			logger.ErrorContext(ctx, "Failed to fetch inventory from cluster; skipping", "cluster", clusterName, "error", err)
			continue
		}
		clusters = append(clusters, cluster)
	}

	return &commonparams.ProjectInventoryResult{Clusters: clusters}, nil
}

func (o *GCPOrchestrator) fetchClusterInventory(ctx context.Context, hc *database.HostCredential, showVolumes bool) (commonparams.InventoryCluster, error) {
	host := hc.Host
	if host == nil {
		return commonparams.InventoryCluster{}, fmt.Errorf("missing host for inventory credential")
	}

	clusterName := database.HostExternalName(host)
	ontapProvider, err := vsa.GetProviderForHost(ctx, host)
	if err != nil {
		return commonparams.InventoryCluster{}, fmt.Errorf("create VSA provider for cluster %s: %w", clusterName, err)
	}

	aggregates, err := ontapProvider.GetAggregates()
	if err != nil {
		return commonparams.InventoryCluster{}, fmt.Errorf("list aggregates from cluster %s: %w", clusterName, err)
	}

	accountUUID := strings.ReplaceAll(hc.AccountUUID, "-", "")
	svmNamePattern := fmt.Sprintf("svm_%s_*", accountUUID)
	volumes, err := ontapProvider.GetVolumesForInventory(svmNamePattern)
	if err != nil {
		return commonparams.InventoryCluster{}, fmt.Errorf("list volumes for inventory from cluster %s: %w", clusterName, err)
	}

	var svms []*ontapRest.Svm
	if showVolumes {
		svms, err = ontapProvider.ListSVMs(vsa.ListSvmsParams{
			NamePattern: &svmNamePattern,
			Fields:      []string{"name", "uuid"},
		})
		if err != nil {
			return commonparams.InventoryCluster{}, fmt.Errorf("list SVMs from cluster %s: %w", clusterName, err)
		}
	}

	poolUsedSize, err := o.storage.SumPoolSizesByClusterName(ctx, clusterName)
	if err != nil {
		util.GetLogger(ctx).WarnContext(ctx, "Failed to fetch pool used size for cluster; defaulting to 0",
			"cluster", clusterName, "error", err)
		poolUsedSize = 0
	}

	efficiencyFactor := effectiveAggregateSizeMultiplier(host.EfficiencyFactor)
	provisioningModel, err := datamodel.EffectiveHostProvisioningModel(host)
	if err != nil {
		return commonparams.InventoryCluster{}, fmt.Errorf("resolve provisioning model for cluster %s: %w", clusterName, err)
	}
	return buildClusterInventory(
		clusterName,
		string(provisioningModel),
		normalizeInventoryAggregates(aggregates),
		normalizeInventoryVolumes(volumes),
		normalizeInventorySVMs(svms),
		poolUsedSize,
		efficiencyFactor,
	), nil
}

func normalizeInventoryAggregates(aggregates []*vsa.Aggregate) []inventoryAggregate {
	result := make([]inventoryAggregate, 0, len(aggregates))
	for _, aggregate := range aggregates {
		if aggregate == nil {
			continue
		}
		result = append(result, inventoryAggregate{
			name:        aggregate.Name,
			uuid:        aggregate.UUID,
			state:       aggregate.State,
			volumeCount: aggregate.VolumeCount,
			size:        aggregate.Size,
		})
	}
	return result
}

func normalizeInventoryVolumes(volumes []*vsa.Volume) []inventoryVolume {
	result := make([]inventoryVolume, 0, len(volumes))
	for _, volume := range volumes {
		if volume == nil {
			continue
		}
		normalized := inventoryVolume{
			uuid: volume.ExternalUUID,
		}
		normalized.name = nillable.GetString(volume.Name, "")
		if volume.Svm != nil {
			normalized.svmName = nillable.GetString(volume.Svm.Name, "")
		}
		if volume.Space != nil {
			normalized.size = nillable.GetInt64(volume.Space.PhysicalUsed, 0)
		}
		for _, aggregate := range volume.VolumeInlineAggregates {
			if aggregate == nil {
				continue
			}
			if name := nillable.GetString(aggregate.Name, ""); name != "" {
				normalized.aggregateNames = append(normalized.aggregateNames, name)
			}
		}
		result = append(result, normalized)
	}
	return result
}

func normalizeInventorySVMs(svms []*ontapRest.Svm) []inventorySVM {
	result := make([]inventorySVM, 0, len(svms))
	for _, svm := range svms {
		if svm == nil {
			continue
		}
		result = append(result, inventorySVM{name: nillable.GetString(svm.Name, "")})
	}
	return result
}

func effectiveAggregateSizeMultiplier(hostEfficiencyFactor float64) float64 {
	if hostEfficiencyFactor > 0 {
		return hostEfficiencyFactor
	}
	return defaultStorageEfficiencyFactor
}

// buildClusterInventory assembles an InventoryCluster from raw ONTAP and VCP DB data.
//
// Aggregate sizes:
//   - size          = ONTAP block_storage.size * host efficiency factor
//   - used_size     = sum of physical used space of volumes on this aggregate
//   - available_size = size - used_size
//
// Cluster sizes:
//   - size      = sum of aggregate sizes
//   - used_size = poolUsedSize from VCP DB
func buildClusterInventory(clusterName, provisioningModel string, aggregates []inventoryAggregate, inventoryVolumes []inventoryVolume, svms []inventorySVM, poolUsedSize int64, efficiencyFactor float64) commonparams.InventoryCluster {
	// Per-aggregate volume size map: aggregate name → sum of volume sizes.
	// FlexGroup volumes span multiple aggregates; attribute an equal share of
	// the volume's physical used space to each backing aggregate so used_size is
	// not over-counted.
	volSizeByAggr := make(map[string]int64)
	for i := range inventoryVolumes {
		v := &inventoryVolumes[i]
		aggregateNames := make([]string, 0, len(v.aggregateNames))
		for _, aggregateName := range v.aggregateNames {
			if aggregateName != "" {
				aggregateNames = append(aggregateNames, aggregateName)
			}
		}
		if len(aggregateNames) == 0 {
			continue
		}
		share := v.size / int64(len(aggregateNames))
		remainder := v.size % int64(len(aggregateNames))
		for i, aggregateName := range aggregateNames {
			contrib := share
			if int64(i) < remainder {
				contrib++
			}
			volSizeByAggr[aggregateName] += contrib
		}
	}

	// SVM → volumes map for the showVolumes tree.
	volsBySVM := make(map[string][]commonparams.InventoryVolume)
	for i := range inventoryVolumes {
		v := &inventoryVolumes[i]
		if v.uuid == "" {
			continue
		}
		volsBySVM[v.svmName] = append(volsBySVM[v.svmName], commonparams.InventoryVolume{
			Name: v.name,
			UUID: v.uuid,
		})
	}

	var clusterTotalSize int64
	inventoryAggregates := make([]commonparams.InventoryAggregate, 0, len(aggregates))
	for _, a := range aggregates {
		aggrSize := int64(float64(a.size) * efficiencyFactor)
		aggrUsed := volSizeByAggr[a.name]

		inventoryAggregates = append(inventoryAggregates, commonparams.InventoryAggregate{
			Name:          a.name,
			UUID:          a.uuid,
			State:         a.state,
			VolumeCount:   a.volumeCount,
			Size:          aggrSize,
			UsedSize:      aggrUsed,
			AvailableSize: aggrSize - aggrUsed,
		})
		clusterTotalSize += aggrSize
	}

	inventorySVMs := make([]commonparams.InventorySVM, 0, len(svms))
	for _, s := range svms {
		inventorySVMs = append(inventorySVMs, commonparams.InventorySVM{
			Name:    s.name,
			Volumes: volsBySVM[s.name],
		})
	}

	return commonparams.InventoryCluster{
		Name:              clusterName,
		ProvisioningModel: provisioningModel,
		Aggregates:        inventoryAggregates,
		SVMs:              inventorySVMs,
		Size:              clusterTotalSize,
		UsedSize:          poolUsedSize,
	}
}
