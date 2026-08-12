package database

import (
	"context"
	"errors"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"gorm.io/gorm"
)

// ExpertModeVolumeTenancyRow is a read-only projection of expert-mode volume tenancy labels.
type ExpertModeVolumeTenancyRow struct {
	CursorID       int64  `gorm:"column:cursor_id"`
	VolumeUUID     string `gorm:"column:volume_uuid"`
	SVMName        string `gorm:"column:svm_name"`
	VolumeName     string `gorm:"column:volume_name"`
	Project        string `gorm:"column:project"`
	PoolName       string `gorm:"column:pool_name"`
	TenantProject  string `gorm:"column:tenant_project"`
	DeploymentName string `gorm:"column:deployment_name"`
	ClusterName    string `gorm:"column:cluster_name"`
}

// ListExpertModeVolumeTenancyByCluster returns a keyset-paginated snapshot of active
// expert-mode volumes and tenancy labels scoped to a single ONTAP cluster external name.
func (d *DataStoreRepository) ListExpertModeVolumeTenancyByCluster(
	ctx context.Context,
	clusterName string,
	afterID int64,
	limit int,
) ([]*ExpertModeVolumeTenancyRow, error) {
	if clusterName == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("clusterName is required"))
	}
	limit = commonparams.NormalizeExpertModeVolumeTenancyQueryLimit(limit)

	var rows []*ExpertModeVolumeTenancyRow
	err := d.expertModeVolumeTenancyBaseQuery(ctx, clusterName).
		Where("emv.id > ?", afterID).
		Order("emv.id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return rows, nil
}

func (d *DataStoreRepository) expertModeVolumeTenancyBaseQuery(ctx context.Context, clusterName string) *gorm.DB {
	return d.db.GORM().WithContext(ctx).
		Table("pools p").
		Select(`
			emv.id AS cursor_id,
			emv.external_uuid AS volume_uuid,
			s.name AS svm_name,
			emv.name AS volume_name,
			COALESCE(a.name, p.pool_attributes->>'account_name', '') AS project,
			p.name AS pool_name,
			p.cluster_details->>'regional_tenant_project' AS tenant_project,
			p.deployment_name,
			p.cluster_details->>'external_name' AS cluster_name
		`).
		Joins("JOIN expert_mode_volumes emv ON emv.pool_id = p.id").
		Joins("LEFT JOIN accounts a ON a.id = emv.account_id").
		Joins("JOIN svms s ON s.id = emv.svm_id AND s.pool_id = p.id").
		Where("p.deleted_at IS NULL").
		Where("p.service_level IN ?", []string{"EXTREME", "PREMIUM"}).
		Where("p.cluster_details->>'external_name' = ?", clusterName).
		Where("emv.deleted_at IS NULL").
		Where("emv.state <> ?", datamodel.LifeCycleStateCreating).
		Where("emv.external_uuid IS NOT NULL").
		Where("emv.external_uuid <> ''").
		Where("s.deleted_at IS NULL")
}
