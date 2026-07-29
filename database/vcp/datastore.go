package database

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// getVcpModels returns the list of Models to be migrated.
func getVcpModels() []interface{} {
	return []interface{}{
		&datamodel.Pool{},
		&datamodel.Volume{},
		&datamodel.VolumePerformanceGroup{},
		&datamodel.VolumeReplication{},
		&datamodel.Account{},
		&datamodel.Node{},
		&datamodel.Lif{},
		&datamodel.Svm{},
		&datamodel.Job{},
		&datamodel.Snapshot{},
		&datamodel.HostGroup{},
		&datamodel.ServiceAccount{},
		&datamodel.KmsConfig{},
		&datamodel.BackupVault{},
		&datamodel.AdminJobSpec{},
		&datamodel.Backup{},
		&datamodel.BackupPolicy{},
		&datamodel.BackupMetadata{},
		&datamodel.SfrMetadata{},
		&datamodel.NodeNodeGroupMap{},
		&datamodel.NodeGroup{},
		&datamodel.ClusterUpgradeJob{},
		&datamodel.BatchClusterUpgradeJob{},
		&datamodel.ImageVersion{},
		&datamodel.PendingResourceDeletions{},
		&datamodel.ActiveDirectory{},
		&datamodel.ClusterPeerings{},
		&datamodel.QuotaRule{},
		&datamodel.ExpertModeVolumes{},
		&datamodel.BackupChainHistory{},
		&datamodel.AppConfig{},
		&datamodel.AddressRange{},
		&datamodel.Cluster{},
		&datamodel.Region{},
		&datamodel.Zone{},
		&datamodel.Host{},
		&datamodel.IPRange{},
		&datamodel.AccountHost{},
	}
}

type Factory func(config dbutils.DbConfig, logger log.Logger) (Storage, error)

var registry = make(map[string]Factory)

func Register(dbType string, factory Factory) {
	registry[dbType] = factory
}

func New(config dbutils.DbConfig, logger log.Logger) (Storage, error) {
	factory, ok := registry[config.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported database type: %s", config.Type)
	}
	return factory(config, logger)
}
