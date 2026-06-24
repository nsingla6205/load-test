package database

import (
	"context"
	"errors"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

var (
	getVolumeReplicationDetails = _getVolumeReplicationDetails
	getVolumeReplication        = _getVolumeReplication
)

const (
	VolumeReplicationEndpointTypeDestination = "dst"
	VolumeReplicationEndpointTypeSource      = "src"
	QueryDepthZero                           = 0
	QueryDepthOne                            = 1
)

// CreateVolumeReplication creates a new replication in the database
func (d *DataStoreRepository) CreateVolumeReplication(ctx context.Context, replication *datamodel.VolumeReplication) (*datamodel.VolumeReplication, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	// Check if replication already exists for the volume
	dbReplicationEntry := &datamodel.VolumeReplication{}
	dbError := tx.Where("account_id = ?", replication.AccountID).Where("volume_id = ?", replication.VolumeID).First(&dbReplicationEntry).Error

	if dbReplicationEntry.ID != 0 {
		return nil, customerrors.NewUserInputValidationErr("replication already exists for this volume")
	}
	if dbError != nil && !errors.Is(dbError, gorm.ErrRecordNotFound) {
		return nil, dbError
	}

	replication.UUID = utils.RandomUUID()
	replication.State = datamodel.LifeCycleStateCreating
	replication.StateDetails = datamodel.LifeCycleStateCreatingDetails
	replication.CreatedAt = time.Now()
	replication.UpdatedAt = replication.CreatedAt
	if replication.ReplicationAttributes.EndpointType == VolumeReplicationEndpointTypeSource {
		replication.ReplicationAttributes.SourceReplicationUUID = replication.UUID
	} else {
		replication.ReplicationAttributes.DestinationReplicationUUID = replication.UUID
	}

	err = tx.Create(replication).Error
	if err != nil {
		return nil, err
	}

	dbVolumeRep, err := getVolumeReplicationDetails(tx, &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: replication.UUID}})
	if err != nil {
		return nil, err
	}
	return dbVolumeRep, nil
}

// GetVolumeReplication retrieves a replication by its UUID
func (d *DataStoreRepository) GetVolumeReplication(ctx context.Context, volumeReplicationID string) (*datamodel.VolumeReplication, error) {
	db := d.db.GORM().WithContext(ctx)
	return getVolumeReplicationDetails(db, &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: volumeReplicationID}})
}

// GetVolumeReplicationByVolumeID retrieves a replication by volume ID
func (d *DataStoreRepository) GetVolumeReplicationByVolumeID(ctx context.Context, volumeID int64) (*datamodel.VolumeReplication, error) {
	db := d.db.GORM().WithContext(ctx)
	return getVolumeReplicationDetails(db, &datamodel.VolumeReplication{VolumeID: volumeID})
}

// GetVolumeReplicationByProjectId retrieves a replication by projectId
func (d *DataStoreRepository) GetVolumeReplicationByProjectId(ctx context.Context, accountId int64) ([]*datamodel.VolumeReplication, error) {
	db := d.db.GORM().WithContext(ctx)
	var replications []*datamodel.VolumeReplication
	err := db.Preload("Volume").Find(&replications, &datamodel.VolumeReplication{AccountID: accountId}).Error
	if err != nil {
		return nil, err
	}
	return replications, nil
}

func _getVolumeReplication(db *gorm.DB, query *datamodel.VolumeReplication) (*datamodel.VolumeReplication, error) {
	vr := &datamodel.VolumeReplication{}
	err := db.First(&vr, query).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("volume replication", nil)
		}
		return nil, err
	}
	return vr, nil
}

func _getVolumeReplicationDetails(db *gorm.DB, query *datamodel.VolumeReplication) (*datamodel.VolumeReplication, error) {
	vr := &datamodel.VolumeReplication{}
	err := db.Preload("Volume").Preload("Volume.Pool").Preload("Account").Preload("ClusterPeer").First(&vr, query).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("volume replication", nil)
		}
		return nil, err
	}
	return vr, nil
}

func (d *DataStoreRepository) UpdateVolumeReplication(ctx context.Context, volumeRep *datamodel.VolumeReplication) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	dbReplication, err := getVolumeReplicationDetails(tx, &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: volumeRep.UUID}})
	if err != nil {
		return err
	}

	dbReplication.UpdatedAt = time.Now()
	dbReplication.State = volumeRep.State
	dbReplication.StateDetails = volumeRep.StateDetails
	if dbReplication.ReplicationAttributes.ExternalUUID != volumeRep.ReplicationAttributes.ExternalUUID {
		dbReplication.ReplicationAttributes.ExternalUUID = volumeRep.ReplicationAttributes.ExternalUUID
	}
	if dbReplication.ReplicationAttributes.ReplicationSchedule != volumeRep.ReplicationAttributes.ReplicationSchedule {
		dbReplication.ReplicationAttributes.ReplicationSchedule = volumeRep.ReplicationAttributes.ReplicationSchedule
	}
	dbReplication.ReplicationAttributes = volumeRep.ReplicationAttributes
	dbReplication.MirrorState = volumeRep.MirrorState
	dbReplication.RelationshipStatus = volumeRep.RelationshipStatus
	dbReplication.TotalTransferBytes = volumeRep.TotalTransferBytes
	dbReplication.TotalProgress = volumeRep.TotalProgress
	dbReplication.TotalTransferTimeSecs = volumeRep.TotalTransferTimeSecs
	dbReplication.LastTransferSize = volumeRep.LastTransferSize
	dbReplication.LastTransferError = volumeRep.LastTransferError
	dbReplication.LastTransferDuration = volumeRep.LastTransferDuration
	dbReplication.LastTransferEndTime = volumeRep.LastTransferEndTime
	dbReplication.ProgressLastUpdated = volumeRep.ProgressLastUpdated
	dbReplication.Description = volumeRep.Description
	dbReplication.HybridReplicationAttributes = volumeRep.HybridReplicationAttributes
	dbReplication.LagTime = volumeRep.LagTime
	dbReplication.LastUpdatedFromOntap = volumeRep.LastUpdatedFromOntap
	dbReplication.ClusterPeerId = volumeRep.ClusterPeerId

	// Handle DeletedAt field for soft deletion
	if volumeRep.DeletedAt != nil {
		dbReplication.DeletedAt = volumeRep.DeletedAt
	}

	// Update all fields first
	err = tx.Updates(dbReplication).Error
	if err != nil {
		return err
	}

	// Explicitly update ClusterPeerId to handle NULL values
	// GORM's Updates skips zero values, so we need to explicitly update ClusterPeerId
	// when it's set to NULL (Valid: false)
	if !volumeRep.ClusterPeerId.Valid {
		err = tx.Model(&datamodel.VolumeReplication{}).Where("uuid = ?", dbReplication.UUID).Update("cluster_peer_id", nil).Error
		if err != nil {
			return err
		}
	}

	return nil
}

// UpdateVolumeReplicationStates updates state & state_details for the replication in database
func (d *DataStoreRepository) UpdateVolumeReplicationStates(ctx context.Context, volumeRep *datamodel.VolumeReplication) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	dbReplication, err := getVolumeReplication(tx, &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: volumeRep.UUID}})
	if err != nil {
		return err
	}

	dbReplication.UpdatedAt = time.Now()
	dbReplication.State = volumeRep.State
	dbReplication.StateDetails = volumeRep.StateDetails
	err = tx.Updates(dbReplication).Error
	if err != nil {
		return err
	}

	return nil
}

// UpdateVolumeReplicationTransferStats updates transfer stats for the replication
func (d *DataStoreRepository) UpdateVolumeReplicationTransferStats(ctx context.Context, volumeRep *datamodel.VolumeReplication) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	dbReplication, err := getVolumeReplicationDetails(tx, &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: volumeRep.UUID}})
	if err != nil {
		return err
	}
	if dbReplication.ReplicationAttributes.ExternalUUID == "" {
		dbReplication.ReplicationAttributes.ExternalUUID = volumeRep.ReplicationAttributes.ExternalUUID
	}
	dbReplication.LastUpdatedFromOntap = time.Now()
	dbReplication.TotalProgress = volumeRep.TotalProgress
	dbReplication.TotalTransferBytes = volumeRep.TotalTransferBytes
	dbReplication.TotalTransferTimeSecs = volumeRep.TotalTransferTimeSecs
	dbReplication.LastTransferSize = volumeRep.LastTransferSize
	dbReplication.LastTransferError = volumeRep.LastTransferError
	dbReplication.LastTransferDuration = volumeRep.LastTransferDuration
	dbReplication.LastTransferEndTime = volumeRep.LastTransferEndTime
	dbReplication.ProgressLastUpdated = volumeRep.ProgressLastUpdated
	dbReplication.LagTime = volumeRep.LagTime
	dbReplication.MirrorState = volumeRep.MirrorState
	dbReplication.RelationshipStatus = volumeRep.RelationshipStatus
	if err := tx.Updates(dbReplication).Error; err != nil {
		return err
	}
	return nil
}

// DeleteVolumeReplication deletes replication based on UUID
func (d *DataStoreRepository) DeleteVolumeReplication(ctx context.Context, replication *datamodel.VolumeReplication) (*datamodel.VolumeReplication, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)
	mirrorState := datamodel.OntapUninitialized
	replicationStatus := datamodel.SnapmirrorRelationshipIdle
	replication.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	replication.MirrorState = &mirrorState
	replication.RelationshipStatus = &replicationStatus
	replication.State = datamodel.LifeCycleStateDeleted
	replication.StateDetails = datamodel.LifeCycleStateDeletedDetails
	err = tx.Save(replication).Error
	if err != nil {
		return nil, err
	}

	return replication, nil
}

func (d *DataStoreRepository) GetVolumeReplicationCount(ctx context.Context, accountName string) (int64, error) {
	var count int64
	account, err := d.GetAccount(ctx, accountName)
	if err != nil {
		return 0, err
	}
	err = d.db.GORM().WithContext(ctx).Model(&datamodel.VolumeReplication{}).Where("account_id = ?", account.ID).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) GetVolumeReplicationCountByClusterPeerID(ctx context.Context, clusterPeerID int64) (int64, error) {
	var count int64
	err := d.db.GORM().
		WithContext(ctx).
		Model(&datamodel.VolumeReplication{}).
		Joins("JOIN cluster_peerings ON cluster_peerings.id = volume_replications.cluster_peer_id AND cluster_peerings.deleted_at IS NULL").
		Where("volume_replications.cluster_peer_id = ?", clusterPeerID).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) GetVolumeReplicationCountByPeerDetails(ctx context.Context, accountName string, peerSvmName string, peerVolumeName string) (int64, error) {
	var count int64
	account, err := d.GetAccount(ctx, accountName)
	if err != nil {
		return 0, err
	}
	err = d.db.GORM().WithContext(ctx).Model(&datamodel.VolumeReplication{}).Where("account_id = ? AND hybrid_replication_attributes->>'peer_volume_name' = ? AND hybrid_replication_attributes->>'peer_svm_name' = ?", account.ID, peerVolumeName, peerSvmName).Count(&count).Error

	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) GetVolumeReplicationCountByVolumeID(ctx context.Context, volumeID int64) (int64, error) {
	var count int64
	err := d.db.GORM().WithContext(ctx).Model(&datamodel.VolumeReplication{}).Where("volume_id = ?", volumeID).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) GetReplicatedVolumeUUIDs(ctx context.Context, volumeUUIDs []string) ([]string, error) {
	var replicatedUUIDs []string
	err := d.db.GORM().
		WithContext(ctx).
		Table("volume_replications").
		Distinct("volumes.uuid").
		Joins("JOIN volumes ON volumes.id = volume_replications.volume_id").
		Where("volume_replications.deleted_at IS NULL").
		Where("volumes.deleted_at IS NULL").
		Where("volumes.uuid IN ?", volumeUUIDs).
		Pluck("volumes.uuid", &replicatedUUIDs).Error
	if err != nil {
		return nil, err
	}
	return replicatedUUIDs, nil
}

func (d *DataStoreRepository) ListVolumeReplications(ctx context.Context, filter utils2.Filter, queryDepth int) ([]*datamodel.VolumeReplication, error) {
	if len(filter.Conditions) == 0 {
		return nil, customerrors.NewUserInputValidationErr("no filter conditions provided for listing volume replications")
	}
	var err error
	db := d.db.ApplyFilter(filter.Apply()).GORM().WithContext(ctx)
	var volumeReplications []*datamodel.VolumeReplication
	db = db.Preload("Volume").Preload("Volume.Pool")
	if queryDepth == 1 {
		db = db.Preload("Volume.Svm").Preload("ClusterPeer")
	}
	err = db.Find(&volumeReplications).Error
	if err != nil {
		return nil, err
	}
	return volumeReplications, nil
}
func (d *DataStoreRepository) ListVolumeReplicationsWithPagination(ctx context.Context, conditions [][]interface{}, pagination *utils2.Pagination) ([]*datamodel.VolumeReplication, error) {
	return _listVolumeReplicationsWithDetailsPagination(d.db.ApplyFilter(conditions).Unscoped().GORM().WithContext(ctx), pagination)
}

func _listVolumeReplicationsWithDetailsPagination(db *gorm.DB, pagination *utils2.Pagination) ([]*datamodel.VolumeReplication, error) {
	var volumeReplications []*datamodel.VolumeReplication
	err := db.
		Preload("Account").
		Preload("Volume", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, pool_id, volume_attributes, large_volume_attributes")
		}).
		Preload("Volume.Pool", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, deployment_name")
		}).
		Scopes(utils2.Paginate(pagination)).
		Find(&volumeReplications).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return volumeReplications, nil
}

func (d *DataStoreRepository) UpdateVolumeReplicationFields(ctx context.Context, volumeRepUUID string, updates map[string]interface{}) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	dbVolumeReplication, err := getVolumeReplicationDetails(tx, &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: volumeRepUUID}})
	if err != nil {
		return err
	}

	err = tx.Model(&dbVolumeReplication).Updates(updates).Error
	if err != nil {
		return err
	}

	return nil
}
