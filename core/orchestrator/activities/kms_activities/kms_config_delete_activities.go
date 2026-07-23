package kms_activities

import (
	"context"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/sde"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

var (
	sdeDeleteSDEKmsConfiguration = sde.DeleteSDEKmsConfiguration
	describeSDEJob               = sde.DescribeSDEJob
)

func (a *KmsConfigActivity) DeleteSDEKmsConfig(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.DeleteKmsConfigParams) (*string, error) {
	activity.RecordHeartbeat(ctx, "Starting DeleteSDEKmsConfig activity")
	defer activity.RecordHeartbeat(ctx, "Finished DeleteSDEKmsConfig activity")
	logger := util.GetLogger(ctx)

	activity.RecordHeartbeat(ctx, "Initiating KMS configuration deletion in SDE")
	resp, err := sdeDeleteSDEKmsConfiguration(ctx, kmsConfig, params)
	if err != nil {
		return nil, err
	}
	logger.Debugf("KmsConfig:%s delete sent to sde", kmsConfig.ResourceID)

	operation, ok := resp.(*gcpserver.OperationV1beta)
	if ok && !operation.Done.Value {
		return &strings.Split(operation.Name.Value, "/")[7], nil
	}
	return nil, nil
}

func (a *KmsConfigActivity) DescribeSDEDeleteJob(ctx context.Context, jobUuid *string, params *common.DeleteKmsConfigParams) error {
	activity.RecordHeartbeat(ctx, "Starting DescribeSDEDeleteJob activity")
	defer activity.RecordHeartbeat(ctx, "Finished DescribeSDEDeleteJob activity")
	if nillable.IsNilOrEmpty(jobUuid) {
		return nil
	}
	activity.RecordHeartbeat(ctx, "Checking SDE delete job status")
	return describeSDEJob(ctx, *jobUuid, params.Region, params.AccountName, params.XCorrelationID)
}

// DisableKmsServiceAccount updates the KMS ServiceAccounts as disabled in the database.
// Multiple CMEK:  Disable the SA when no other live config in this account uses it.
func (j *KmsConfigActivity) DisableKmsServiceAccount(ctx context.Context, kmsConfig *datamodel.KmsConfig) error {
	activity.RecordHeartbeat(ctx, "Starting DisableKmsServiceAccount activity")
	defer activity.RecordHeartbeat(ctx, "Finished DisableKmsServiceAccount activity")
	se := j.SE
	logger := util.GetLogger(ctx)
	// it's possible that the KMS config was created without a service account
	// in that case, there's nothing to disable
	if kmsConfig.ServiceAccount == nil {
		return nil
	}

	activity.RecordHeartbeat(ctx, "Checking for sibling KMS configs sharing this service account")
	siblings, err := se.ListKmsConfigByAccountID(ctx, kmsConfig.AccountID)
	if err != nil {
		return err
	}
	saID := kmsConfig.ServiceAccount.ID
	for _, other := range siblings {
		if other.UUID == kmsConfig.UUID {
			continue
		}
		// A sibling already being deleted is not a live reference. Ignoring it ensures the "last one out" still disables the SA
		if other.State == datamodel.LifeCycleStateDeleting {
			continue
		}
		if other.ServiceAccountID != nil && *other.ServiceAccountID == saID {
			logger.Info("Skipping service account disable: still referenced by sibling KMS config",
				"kms_config_uuid", kmsConfig.UUID,
				"sibling_kms_config_uuid", other.UUID,
				"service_account_uuid", kmsConfig.ServiceAccount.UUID)
			return nil
		}
	}

	activity.RecordHeartbeat(ctx, "Disabling KMS service account")
	_, err = se.UpdateServiceAccountState(ctx, kmsConfig.ServiceAccount.UUID, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
	return err
}

func (a *KmsConfigActivity) DeleteKmsConfig(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.DeleteKmsConfigParams) error {
	activity.RecordHeartbeat(ctx, "Starting DeleteKmsConfig activity")
	defer activity.RecordHeartbeat(ctx, "Finished DeleteKmsConfig activity")
	logger := util.GetLogger(ctx)
	se := a.SE

	activity.RecordHeartbeat(ctx, "Deleting KMS configuration from database")
	_, err := se.DeleteKmsConfig(ctx, params.KmsConfigID, datamodel.LifeCycleStateDeleted, datamodel.LifeCycleStateDeletedDetails)
	if err != nil {
		// Idempotent delete: When a CREATE workflow is cancelled and rolled back, it may delete the KMS config record before the DELETE
		// workflow runs. In this case, the record won't exist in the database, but the delete operation should still succeed since the
		// end state (record deleted) is achieved. This prevents the DELETE job from failing with errors when racing with CREATE cancellation rollback.
		if errors.IsNotFoundErr(err) {
			logger.Info("KMS config already deleted, treating as success", "kms_config_uuid", params.KmsConfigID)
			return nil
		}
		return err
	}
	logger.Debug("KmsConfig:%s deleted successfully in the db", kmsConfig.Name)

	return nil
}
