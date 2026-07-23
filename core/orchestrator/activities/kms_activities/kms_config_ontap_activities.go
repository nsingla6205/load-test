package kms_activities

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-openapi/strfmt"
	coreModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"gorm.io/gorm"
)

var (
	getOntapRestProviderForPool = _getOntapRestProviderForPool
)

func (j *KmsConfigActivity) ConfigureKmsForSvmActivity(ctx context.Context, svm *datamodel.Svm, node *coreModels.Node, params commonparams.CreatePoolParams) (*datamodel.Svm, error) {
	safeRecordHeartbeat(ctx, fmt.Sprintf("Initializing KMS configuration for SVM %s", svm.Name))
	se := j.SE
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if provider == nil {
		return nil, errors.New("provider not found")
	}
	if params.KmsConfigId == "" {
		return svm, nil
	}
	// Fetch the KMS config from the database
	safeRecordHeartbeat(ctx, fmt.Sprintf("Fetching KMS config %s from database", params.KmsConfigId))
	kmsConfig, err := se.GetKmsConfig(ctx, params.KmsConfigId)
	if err != nil {
		return nil, err
	}

	// Decode the base64 encoded key
	decodedKey, err := utils.DecryptAndDecodeCredentials(kmsConfig.ServiceAccount.ServiceAccountPasswordLocation)
	if err != nil {
		return nil, err
	}

	// VCP-created configs: use direct access — PrivilegedAccount is empty (no impersonation).
	// The VCP SA has direct access to the customer's KMS key.
	// SDE-created configs: use SDE service account email for impersonation via PrivilegedAccount.
	privilegedAccount := kmsConfig.KmsAttributes.SdeServiceAccountEmail
	if kmsConfig.KmsAttributes != nil && kmsConfig.KmsAttributes.IsVCPCreated() {
		privilegedAccount = "" // No impersonation needed for VCP-created configs
	}

	// Create the KMS configuration using the provider i.e ONTAP REST client on vsa cluster
	safeRecordHeartbeat(ctx, fmt.Sprintf("Configuring KMS on SVM %s via ONTAP REST - kmsConfig: %s", svm.Name, params.KmsConfigId))
	res, err := provider.CreateKmsConfig(vsa.CreateKmsConfigParams{
		SvmName:           svm.Name,
		KeyName:           kmsConfig.KeyName,
		KeyRingLocation:   kmsConfig.KeyRingLocation,
		KeyRingName:       kmsConfig.KeyRing,
		ProjectID:         kmsConfig.KeyProjectID,                          // project id of keyfull path
		Credentials:       nillable.ToPointer(strfmt.Password(decodedKey)), // Use the long-term VCP service account key
		PrivilegedAccount: privilegedAccount,
	})
	if err != nil {
		// Wrap the ONTAP error as a Temporal application error so the tracking ID survives serialization.
		return nil, vsaerrors.WrapOntapError(err, vsaerrors.DomainKMS)
	}

	// Update the SVM with the KMS configuration IDs
	safeRecordHeartbeat(ctx, fmt.Sprintf("Persisting KMS config IDs to database for SVM %s", svm.Name))
	updatedSvm, err := se.UpdateSvmWithKmsConfigIDs(ctx, svm, kmsConfig.UUID, res.ExternalUUID)
	if err != nil {
		return nil, err
	}

	_, err = se.UpdateKmsConfigState(ctx, kmsConfig.UUID, datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails)
	if err != nil {
		return nil, err
	}

	safeRecordHeartbeat(ctx, fmt.Sprintf("Finished ConfigureKmsForSvmActivity for SVM %s", svm.Name))
	return updatedSvm, nil
}

func (j *KmsConfigActivity) CheckVsaKmsConfigReachableActivity(ctx context.Context, svm *datamodel.Svm, node *coreModels.Node) error {
	safeRecordHeartbeat(ctx, fmt.Sprintf("Checking GCP KMS reachability from ONTAP - SVM: %s", svm.Name))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if provider == nil {
		return errors.New("provider not found")
	}

	// Check the KMS configuration using the provider i.e ONTAP REST client on vsa cluster
	safeRecordHeartbeat(ctx, fmt.Sprintf("Verifying KMS reachability - externalKmsConfigUUID: %s", svm.SvmDetails.ExternalKmsConfigUUID))
	_, err = provider.IsGcpKmsReachable(vsa.GetKmsConfigParams{
		ExternalKmsConfigID: svm.SvmDetails.ExternalKmsConfigUUID,
	})

	if err != nil {
		if strings.Contains(err.Error(), "permission_denied") {
			return errors.New("GCP KMS key is not reachable from ONTAP - Service account lacks permission, retrying again")
		}
		if strings.Contains(err.Error(), "Invalid JWT Signature") || strings.Contains(err.Error(), "InvalidJWTSignature") {
			return errors.New("GCP KMS key is not reachable from ONTAP - Failed to establish connectivity" +
				" with the cloud key management service, retrying again")
		}
		return temporal.NewNonRetryableApplicationError("GCP KMS key is not reachable from VSA Clusters", ErrTypeKmsConfigNotReachableVsaCluster, err)
	}
	return err
}

// GetOntapRestProviderForPool retrieves the provider for the pool
func (j *KmsConfigActivity) GetOntapRestProviderForPoolActivity(ctx context.Context, pool *datamodel.Pool) (vsa.Provider, error) {
	logger := util.GetLogger(ctx)
	se := j.SE

	provider, errGetProvider := getOntapRestProviderForPool(ctx, se, pool)
	if errGetProvider != nil {
		logger.Errorf("Failed to get provider for pool with UUID %s in VSA: %v", pool.UUID, errGetProvider)
		return nil, errGetProvider
	}
	return provider, nil
}

func (j *KmsConfigActivity) DeleteEkmConfigActivity(ctx context.Context, node *coreModels.Node, svm *datamodel.Svm) error {
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return err
	}

	var ekmUUID string
	if svm.SvmDetails != nil {
		ekmUUID = svm.SvmDetails.ExternalKmsConfigUUID
	} else {
		return errors.New("Unable to determine External-UUID of EKM since SvmDetails field of Svm DataModel is nil")
	}
	params := vsa.DeleteKmsConfigParams{
		ExternalKmsConfigID: ekmUUID,
	}

	errDelete := provider.DeleteEkmConfig(params)
	if errDelete != nil {
		logger.Errorf("Failed to delete EKM, id: %s", ekmUUID)
		return errDelete
	}
	return nil
}

func _getOntapRestProviderForPool(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            nodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	// Node now contains CA fields from PoolCredentials, so we can use GetProviderByNode directly
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return provider, nil
}
