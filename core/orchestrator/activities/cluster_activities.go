package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

var (
	IpSpace = env.GetString("VSA_IC_LIF_IPSPACE", "Gcnv")
)

type ClusterPeerActivity struct {
	SE database.Storage
}

func (j *ClusterPeerActivity) AcceptClusterPeer(ctx context.Context, params *commonparams.ClusterPeerParams, node *models.Node) (*commonparams.ClusterPeerParams, error) {
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	var expiryTime *strfmt.DateTime
	if params.ExpiryTime != nil {
		convertedTime := strfmt.DateTime(*params.ExpiryTime)
		expiryTime = &convertedTime
	}
	clusterPeers, err := provider.ListClusterPeers()
	if err != nil {
		return nil, err
	}

	// Check for existing peer that matches name and IPs
	var existingPeer *vsa.ClusterPeer
	for _, peer := range clusterPeers {
		if peer.PeerClusterName == params.PeerName && areIPsMatching(peer.PeerAddresses, params.PeerAddresses) {
			existingPeer = peer
			break
		}
	}

	if existingPeer != nil {
		// If existing peer is available, reuse it
		if existingPeer.Availability == vsa.ClusterPeerAvailabilityStateAvailable || existingPeer.Availability == vsa.ClusterPeerAvailabilityStatePartial {
			params.UUID = existingPeer.ExternalUUID
			return params, nil
		} else if existingPeer.Availability == vsa.ClusterPeerAvailabilityStatePending {
			// If existing peer is available, wait for it to move into available state
			logger.Infof("Found existing cluster peer %s in pending state. Retrying", existingPeer.ExternalUUID)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrClusterPeerNotAvailable, err)
		} else {
			// If existing peer is not available, delete it
			logger.Warnf("Found existing cluster peer %s with non-available state: %s", existingPeer.ExternalUUID, existingPeer.Availability)

			// Delete the existing non-available peer
			logger.Infof("Deleting existing non-available cluster peer: %s", existingPeer.ExternalUUID)
			err = provider.DeleteClusterPeer(existingPeer.ExternalUUID)
			if err != nil {
				logger.Errorf("Failed to delete existing cluster peer %s: %v", existingPeer.ExternalUUID, err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrDeletingClusterPeer, err)
			}
			logger.Infof("Successfully deleted existing cluster peer: %s", existingPeer.ExternalUUID)
		}
	}

	clusterPeer, err := provider.AcceptClusterPeer(vsa.CreateClusterPeerParams{
		PeerAddresses: params.PeerAddresses,
		PeerName:      params.PeerName,
		Passphrase:    params.Passphrase,
		ExpiryTime:    expiryTime,
		IPSpace:       IpSpace,
	})
	if err != nil {
		return nil, err
	}
	params.UUID = clusterPeer.ExternalUUID
	return params, nil
}

func areIPsMatching(existingIPs, newIPs []string) bool {
	ipSet := make(map[string]struct{})
	for _, ip := range existingIPs {
		ipSet[ip] = struct{}{}
	}
	for _, ip := range newIPs {
		if _, exists := ipSet[ip]; !exists {
			return false
		}
	}
	return true
}

func (j *ClusterPeerActivity) CreateClusterPeer(ctx context.Context, params *commonparams.ClusterPeerParams, node *models.Node) (*commonparams.ClusterPeerParams, error) {
	return CreateClusterPeer(ctx, params, node)
}

func CreateClusterPeer(ctx context.Context, params *commonparams.ClusterPeerParams, node *models.Node) (*commonparams.ClusterPeerParams, error) {
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	clusterPeers, err := provider.ListClusterPeers()
	if err != nil {
		return nil, err
	}

	// Check for existing peer that matches name and IPs
	var existingPeer *vsa.ClusterPeer
	for _, peer := range clusterPeers {
		if peer.PeerClusterName == params.PeerName && areIPsMatching(peer.PeerAddresses, params.PeerAddresses) {
			existingPeer = peer
			break
		}
	}

	if existingPeer != nil {
		// If existing peer is available, reuse it
		if existingPeer.Availability == vsa.ClusterPeerAvailabilityStateAvailable || existingPeer.Availability == vsa.ClusterPeerAvailabilityStatePartial {
			params.UUID = existingPeer.ExternalUUID
			return params, nil
		} else if existingPeer.Availability == vsa.ClusterPeerAvailabilityStatePending {
			// If existing peer is available, wait for it to move into available state
			logger.Infof("Found existing cluster peer %s in pending state. Retrying", existingPeer.ExternalUUID)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrClusterPeerNotAvailable, err)
		} else {
			// If existing peer is not available, delete it
			logger.Warnf("Found existing cluster peer %s with non-available state: %s", existingPeer.ExternalUUID, existingPeer.Availability)

			// Delete the existing non-available peer
			logger.Infof("Deleting existing non-available cluster peer: %s", existingPeer.ExternalUUID)
			err = provider.DeleteClusterPeer(existingPeer.ExternalUUID)
			if err != nil {
				logger.Errorf("Failed to delete existing cluster peer %s: %v", existingPeer.ExternalUUID, err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrDeletingClusterPeer, err)
			}
			logger.Infof("Successfully deleted existing cluster peer: %s", existingPeer.ExternalUUID)
		}
	}

	var expiryTime *strfmt.DateTime
	if params.ExpiryTime != nil {
		convertedTime := strfmt.DateTime(*params.ExpiryTime)
		expiryTime = &convertedTime
	}
	clusterPeer, err := provider.CreateClusterPeer(vsa.CreateClusterPeerParams{
		PeerAddresses: params.PeerAddresses,
		PeerName:      params.PeerName,
		ExpiryTime:    expiryTime,
		IPSpace:       IpSpace,
	})
	if err != nil {
		return nil, err
	}
	params.UUID = clusterPeer.ExternalUUID
	params.Passphrase = (*string)(clusterPeer.Passphrase)
	return params, nil
}

type ClusterUpgradeActivity struct {
	SE database.Storage
}

// UpdateClusterUpgradeJobStatusActivity updates the status of a cluster upgrade job
func (j *ClusterUpgradeActivity) UpdateClusterUpgradeJobStatusActivity(ctx context.Context, jobUUID, status, errorMessage string) error {
	logger := util.GetLogger(ctx)
	logger.Info("Updating cluster upgrade job status", "jobUUID", jobUUID, "status", status)

	se := j.SE
	if status == string(models.UpgradeStatusInProgress) {
		return claimPendingUpgradeJob(ctx, se, upgradeJobRef{
			Kind:   database.UpgradeJobKindSingle,
			UUID:   jobUUID,
			LogKey: "jobUUID",
			Label:  "cluster upgrade job",
		})
	}

	// Get the upgrade job
	upgradeJob, err := se.GetClusterUpgradeJobByUUID(ctx, jobUUID)
	if err != nil {
		logger.Error("Failed to get cluster upgrade job", "jobUUID", jobUUID, "error", err)
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return upgradeJobNotFoundError(fmt.Errorf("cluster upgrade job %s not found: %w", jobUUID, err))
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if upgradeJob == nil {
		err := fmt.Errorf("cluster upgrade job %s not found", jobUUID)
		logger.Error("Failed to get cluster upgrade job", "jobUUID", jobUUID, "error", err)
		return upgradeJobNotFoundError(err)
	}

	// Update the status
	upgradeJob.Status = status
	upgradeJob.UpdatedAt = time.Now()

	// Set error details if provided
	upgradeJob.ErrorDetails = nil
	if errorMessage != "" {
		upgradeJob.ErrorDetails = &datamodel.UpgradeErrorDetails{
			ErrorCode:    "UPGRADE_FAILED",
			ErrorMessage: errorMessage,
			ErrorType:    "UPGRADE_ERROR",
			Retryable:    true,
		}
	}

	// Set timestamps based on status (IN_PROGRESS is handled by the atomic claim above)
	if status == string(models.UpgradeStatusCompleted) || status == string(models.UpgradeStatusFailed) {
		now := time.Now()
		upgradeJob.CompletedAt = &now
		upgradeJob.DeletedAt = &gorm.DeletedAt{Time: now, Valid: true}
	}

	// Save the updated job
	err = se.UpdateClusterUpgradeJob(ctx, upgradeJob)
	if err != nil {
		logger.Error("Failed to update cluster upgrade job", "jobUUID", jobUUID, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Successfully updated cluster upgrade job status", "jobUUID", jobUUID, "status", status)
	return nil
}
