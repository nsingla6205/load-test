package kms_activities

import (
	"context"
	goErrors "errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/kms_configurations"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	EncryptingState                     = "encrypting"
	EncryptedState                      = "encrypted"
	PollWaitIntervalForVolumeEncryption = 30
)

// MigrateSdeKmsConfigActivity initiates migration of a CMEK policy in SDE
func (kmsActivity *KmsConfigActivity) MigrateSdeKmsConfigActivity(ctx context.Context, params *common.MigrateKmsConfigParams) (*kms_configurations.V1betaEncryptVolumesAccepted, error) {
	activity.RecordHeartbeat(ctx, "Starting MigrateSdeKmsConfigActivity")
	defer activity.RecordHeartbeat(ctx, "Finished MigrateSdeKmsConfigActivity")
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetAuthTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)

	migrateKmsConfigParams := &kms_configurations.V1betaEncryptVolumesParams{
		KmsConfigID:    params.SdeUUID,
		LocationID:     params.LocationID,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &xCorrelationID,
	}

	activity.RecordHeartbeat(ctx, "Initiating KMS configuration migration in SDE")
	cvpResponse, cvpErr := cvpClient.KmsConfigurations.V1betaEncryptVolumes(migrateKmsConfigParams)
	if cvpErr != nil {
		logger.Error("Error migrating KMS configuration: ", cvpErr)
		return nil, temporal.NewNonRetryableApplicationError("Error migrating SDE KMS Configuration", "DescribeOperationError", cvpErr)
	}
	if cvpResponse == nil || cvpResponse.Payload == nil {
		return nil, errors.New("Error encountered during SDE CMEK migration: CVP response is empty")
	}
	return cvpResponse, nil
}

// PollMigrateSdeKmsConfigActivity polls the SDE KMS migration operation until it is done.
func (j *KmsConfigActivity) PollMigrateSdeKmsConfigActivity(ctx context.Context, params *common.MigrateKmsConfigParams, response *kms_configurations.V1betaEncryptVolumesAccepted) error {
	activity.RecordHeartbeat(ctx, "Starting PollMigrateSdeKmsConfigActivity")
	defer activity.RecordHeartbeat(ctx, "Finished PollMigrateSdeKmsConfigActivity")
	if response == nil || response.Payload == nil {
		return temporal.NewNonRetryableApplicationError("Error migrating SDE KMS Configuration", "DescribeOperationError", errors.New("SDE CMEK migration error"))
	}

	if !*response.Payload.Done {
		activity.RecordHeartbeat(ctx, "Polling SDE KMS migration operation status")
		payload, err := GetResponseforPollCvpOperation(ctx, response.Payload.Name, params.ProjectNumber, params.LocationID)
		if err != nil {
			return classifyMigrationPollError(err)
		}
		response.Payload = payload
	}

	// SDE shall update the state of the CMEK policy
	return nil
}

// MigrateVsaPoolActivity migrates one VSA pool over to EKM
func (j *KmsConfigActivity) MigrateVsaPoolActivity(ctx context.Context, volumes []*datamodel.Volume, node *models.Node) error {
	activity.RecordHeartbeat(ctx, "Starting MigrateVsaPoolActivity")
	defer activity.RecordHeartbeat(ctx, "Finished MigrateVsaPoolActivity")
	se := j.SE
	logger := util.GetLogger(ctx)
	var volumeMigrationFailed, volumeMigrationComplete bool

	activity.RecordHeartbeat(ctx, "Getting provider for pool migration")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Unable to get provider for pool - GetProviderByNode failure: %s", err.Error())
		return err
	}

	for i, volume := range volumes {
		safeRecordHeartbeat(ctx, fmt.Sprintf("Processing volume %d of %d for migration", i+1, len(volumes)))
		// We do not wish to encrypt volumes which are not in Ready state, or upon re-entry not in Migrating state
		if !(volume.State == datamodel.LifeCycleStateREADY || (volume.State == datamodel.LifeCycleStateUpdating && volume.StateDetails == datamodel.LifeCycleStateVolMigratingDetails)) {
			logger.Errorf("Volume %s is not in Ready state...skipping encryption for this volume; Current state is %s", volume.UUID, volume.State)
			volumeMigrationFailed = true
			continue
		}

		getVolumeParams := vsa.GetVolumeParams{VolumeName: volume.Name}
		if volume.VolumeAttributes != nil {
			getVolumeParams.UUID = volume.VolumeAttributes.ExternalUUID
		} else {
			logger.Errorf("External UUID not present in Volume attributes of volume %s in VSA", volume.UUID)
			volumeMigrationFailed = true
			continue
		}
		if volume.Svm != nil {
			getVolumeParams.SvmName = volume.Svm.Name
		} else {
			logger.Errorf("SVM name not present in Volume data-model of volume %s in VSA", volume.UUID)
			volumeMigrationFailed = true
			continue
		}

		safeRecordHeartbeat(ctx, fmt.Sprintf("Checking encryption status for volume %s", volume.UUID))
		getEncryptionStatus, errStatus := provider.GetVolumeEncryptionStatus(getVolumeParams)
		if errStatus != nil {
			logger.Errorf("Failed to get encryption status for volume %s, aborting encryption... Error: %s", volume.UUID, errStatus.Error())
			volumeMigrationFailed = true
			continue
		}
		if getEncryptionStatus == nil {
			logger.Errorf("Failed to get encryption status for volume %s, aborting encryption", volume.UUID)
			volumeMigrationFailed = true
			continue
		}
		if *getEncryptionStatus.Encryption.State == EncryptedState {
			// Check for function re-entry
			volDetailsDb, errDb := se.GetVolume(ctx, volume.UUID)
			if errDb != nil {
				logger.Errorf("Failed to get volume details from DB for encrypted volume %s in VSA: %s", volume.UUID, errDb.Error())
			} else {
				if volDetailsDb.State == datamodel.LifeCycleStateUpdating && volDetailsDb.StateDetails == datamodel.LifeCycleStateVolMigratingDetails {
					errUpdateVolume := se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
						"state":         datamodel.LifeCycleStateREADY,
						"state_details": datamodel.LifeCycleStateAvailableDetails,
					})
					if errUpdateVolume != nil {
						logger.Errorf("Unable to set state of volume %s back to its original state after encryption", volume.UUID)
					}
				}
			}
			continue
		} else if *getEncryptionStatus.Encryption.State != EncryptingState {
			// Encrypt volume
			safeRecordHeartbeat(ctx, fmt.Sprintf("Enabling encryption for volume %s", volume.UUID))
			errEnableEncryption := provider.UpdateVolumeEnableEncryption(vsa.UpdateVolumeParams{
				UUID:             getVolumeParams.UUID,
				EncryptionEnable: true,
			})
			if errEnableEncryption != nil {
				if strings.Contains(errEnableEncryption.Error(), "Volume is encrypted") {
					logger.Infof("Volume %s already found to be in encrypted format", volume.UUID)
				} else {
					logger.Errorf("Failed to initiate encryption of volume %s in VSA: %s", volume.UUID, errEnableEncryption.Error())
					volumeMigrationFailed = true
				}
				continue
			}
		}

		errUpdateVolState := se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateUpdating,
			"state_details": datamodel.LifeCycleStateVolMigratingDetails,
		})
		if errUpdateVolState != nil {
			logger.Errorf("Unable to set state of volume %s to updating before encryption", volume.UUID)
			continue
		}

		var volArrayForTimeout []*datamodel.Volume
		volArrayForTimeout = append(volArrayForTimeout, volume)
		volumePollTimeout := utils.DetermineStartToCloseTimeoutBasedOnUsedSize(volArrayForTimeout)
		pollTimeout := time.After(time.Duration(volumePollTimeout) * time.Minute)
		safeRecordHeartbeat(ctx, fmt.Sprintf("Polling encryption status for volume %s", volume.UUID))
		for !volumeMigrationFailed && !volumeMigrationComplete {
			select {
			case <-pollTimeout:
				logger.Errorf("Polling timed out for volume %s", volume.UUID)
				volumeMigrationFailed = true
			default:
				getEncryptionResponse, errEncryptStatus := provider.GetVolumeEncryptionStatus(getVolumeParams)
				if errEncryptStatus != nil {
					logger.Errorf("Failed to get encryption status for volume %s during polling: %s", volume.UUID, errEncryptStatus.Error())
					volumeMigrationFailed = true
					continue
				}
				if getEncryptionResponse != nil {
					switch *getEncryptionResponse.Encryption.State {
					case EncryptingState:
						logger.Infof("Volume encryption ongoing for %s ...", volume.UUID)
						safeRecordHeartbeat(ctx, fmt.Sprintf("Volume encryption ongoing for %s", volume.UUID))
						time.Sleep(time.Duration(PollWaitIntervalForVolumeEncryption) * time.Second)
					case EncryptedState:
						logger.Infof("Volume encryption completed for %s ...", volume.UUID)
						safeRecordHeartbeat(ctx, fmt.Sprintf("Volume encryption completed for %s", volume.UUID))
						volumeMigrationComplete = true
					default:
						logger.Errorf("Unexpected encryption state for volume %s during polling: %s", volume.UUID, *getEncryptionResponse.Encryption.State)
						volumeMigrationFailed = true
					}
				} else {
					logger.Errorf("Failed to retrieve encryption status for volume %s during polling", volume.UUID)
					volumeMigrationFailed = true
				}
			}
		}
		safeRecordHeartbeat(ctx, fmt.Sprintf("Updating volume state to ready after encryption for volume %s", volume.UUID))
		errUpdateVol := se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateREADY,
			"state_details": datamodel.LifeCycleStateAvailableDetails,
		})
		if errUpdateVol != nil {
			logger.Errorf("Unable to reset state and state-details of volume %s to ready after encryption: %s", volume.UUID, errUpdateVol.Error())
		}
	}

	if volumeMigrationFailed {
		return temporal.NewNonRetryableApplicationError("Encryption failed for one/some of the volumes", "CmekVolumeMigrationError", errors.New("Volume encryption failure"))
	}
	return nil
}

// sdeMigrationStatusRegex matches the error produced by _pollCvpOperationForWorkflow
// when CVP's terminal operation carries a non-nil StatusV1Beta, which %v formats
// as "operation failed: &{<code> [<details>] <message>}".
var sdeMigrationStatusRegex = regexp.MustCompile(
	`operation failed: &\{(\d+(?:\.\d+)?)\s+\[[^\]]*\]\s+(.*)\}`,
)

// classifyMigrationPollError rewraps an SDE-reported HTTP 4xx on the terminal
// CMEK-migration operation as a non-retryable CustomError (tracking ID
// ErrKMSMigrationSdeClientError / HTTP 400) so SDE's Message is surfaced
// verbatim; any other error is returned unchanged.
func classifyMigrationPollError(err error) error {
	if err == nil {
		return nil
	}

	code, sdeMsg, ok := extractSdeMigrationStatus(err)
	if !ok {
		return err
	}

	if code < 400 || code >= 500 {
		return err
	}

	msg := strings.TrimSpace(sdeMsg)
	if msg == "" {
		msg = "SDE reported a 4xx client error during CMEK migration"
	}

	return errors2.WrapAsNonRetryableTemporalApplicationError(
		errors2.NewVCPError(errors2.ErrKMSMigrationSdeClientError, goErrors.New(msg)).
			WithArgs(msg),
	)
}

func extractSdeMigrationStatus(err error) (int, string, bool) {
	for e := err; e != nil; e = goErrors.Unwrap(e) {
		matches := sdeMigrationStatusRegex.FindStringSubmatch(e.Error())
		if len(matches) != 3 {
			continue
		}
		f, parseErr := strconv.ParseFloat(matches[1], 64)
		if parseErr != nil {
			continue
		}
		return int(f), strings.TrimSpace(matches[2]), true
	}
	return 0, "", false
}
