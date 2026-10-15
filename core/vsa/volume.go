package vsa

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	networkpriv "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/operations"
	privmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/activity"
)

// Error message constants
const (
	privateCLIVolumeCommandTimeout = 10 * time.Minute
	privateCLIHeartbeatInterval    = 1 * time.Minute
	// ErrMsgVolumeMaxSizeExceeded is the error message pattern returned by ONTAP when volume size exceeds maximum
	ErrMsgVolumeMaxSizeExceeded = "failed because the resulting volume size is greater than the maximum size"
	// ErrMsgVolumeSizeTooSmall is the error message pattern returned by ONTAP when volume size is too small to hold current data
	ErrMsgVolumeSizeTooSmall = "Selected volume size is too small to hold the current volume data"
	// ErrMsgMaximumVolumeCountOnNode is the error message pattern when ONTAP node hits per-node volume limit
	ErrMsgMaximumVolumeCountOnNode = "maximum number of volumes reached on node"
)

var (
	enableCloneInfoRefresh = env.GetBool("ENABLE_CLONE_INFO_REFRESH", false)
)

// CreateVolume creates a volume by calling the ONTAP REST Client
func (rc *OntapRestProvider) CreateVolume(params CreateVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	volumeCreateParams := &ontapRest.VolumeCreateParams{
		Name:                           params.VolumeName,
		Type:                           params.VolumeType,
		Size:                           params.Size,
		Svm:                            params.SvmName,
		Aggregates:                     params.Aggregates,
		ConstituentsPerAggregate:       params.ConstituentsPerAggregate,
		Style:                          params.Style,
		SnapshotPolicy:                 params.SnapshotPolicyName,
		SnapshotReservePercent:         params.SnapReserve,
		SnapshotDirectoryAccessEnabled: params.SnapshotDirectory,
		ExportPolicy:                   params.ExportPolicy,
		JunctionPath:                   params.JunctionPath,
		TieringSupported:               params.TieringSupported,
		LargeSizeEnabled:               params.LargeSizeEnabled,
	}
	if params.QosPolicy != nil {
		volumeCreateParams.QosPolicy = *params.QosPolicy
	}
	if params.SecurityStyle != nil && *params.SecurityStyle != "" {
		volumeCreateParams.SecurityStyle = *params.SecurityStyle
	}
	if params.UnixPermissions != nil && *params.UnixPermissions != "" {
		volumeCreateParams.UnixPermissions = params.UnixPermissions
	}
	if params.RestoreFromSnapshot != nil && params.RestoreFromSnapshot.SnapshotUUID != "" {
		volumeCreateParams.RestoreFromSnapshot = &ontapRest.RestoreFromSnapshotParams{
			ParentVolumeExternalUUID: params.RestoreFromSnapshot.ParentVolumeExternalUUID,
			ParentVolumeName:         params.RestoreFromSnapshot.ParentVolumeName,
			SnapshotUUID:             params.RestoreFromSnapshot.SnapshotUUID,
			SnapshotName:             params.RestoreFromSnapshot.SnapshotName,
			ParentVolumeSvmName:      params.RestoreFromSnapshot.ParentVolumeSvmName,
		}
		volumeCreateParams.Size = 0
	}

	if params.TieringPolicy != nil {
		volumeCreateParams.TieringPolicy = &ontapRest.TieringPolicy{
			CoolAccessTieringPolicy: params.TieringPolicy.CoolAccessTieringPolicy,
			MinCoolingDays:          params.TieringPolicy.CoolnessPeriod,
			CloudRetrievalPolicy:    params.TieringPolicy.CoolAccessRetrievalPolicy,
			CloudWriteModeEnabled:   params.TieringPolicy.CloudWriteModeEnabled,
		}
	}

	var vol *ontapRest.Volume
	var job *ontapRest.JobAccepted
	// Snapshot restore is an ONTAP FlexClone. It must use the REST clone payload.
	// An empty RestoreFromSnapshot pointer is not a restore: normal creates always
	// carry that struct, and only a snapshot UUID selects the clone path.
	snapshotRestore := params.RestoreFromSnapshot != nil && params.RestoreFromSnapshot.SnapshotUUID != ""
	if params.UsePrivateCLI && !snapshotRestore {
		vol, err = createVolumeViaPrivateCLI(rc.ClientParams.Ctx, client, volumeCreateParams)
	} else {
		vol, job, err = client.Storage().VolumeCreate(volumeCreateParams)
	}
	if err != nil {
		if strings.Contains(err.Error(), "Duplicate volume name") {
			return nil, errors.NewConflictErr(params.VolumeName + " already exists")
		}
		if strings.Contains(err.Error(), "Maximum clone hierarchy") {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrNestedCloneLimitExceeded, err))
		}
		// ONTAP error format: "Request to create volume ... failed because there is not enough space in aggregate ..."
		// This occurs when hot tier is full even though pool has capacity
		if strings.Contains(err.Error(), "not enough space") || strings.Contains(err.Error(), "insufficient space") {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrHotTierCapacityExhausted, errors.NewUserInputValidationErr("The hot tier (local storage) capacity is full. Free up space in the hot tier or enable hot tier auto-resize to automatically expand the hot tier when needed.")))
		}
		// Check for parent volume not found error when restoring from snapshot
		// ONTAP error format: "Volume \"parentvol2\" in SVM \"svm-name\" does not exist."
		if strings.Contains(err.Error(), "Volume") && strings.Contains(err.Error(), "does not exist") && params.RestoreFromSnapshot != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrParentVolumeNotFound, errors.NewUserInputValidationErr("Cannot create volume from snapshot: parent volume does not exist. Please verify that the parent volume is available and try again.")))
		}
		// ONTAP error format: "Failed to create the clone volume. Reason: Maximum number of volumes reached on node ..."
		// This error occurs when ONTAP node has hit its per-node maximum volume count
		if strings.Contains(strings.ToLower(err.Error()), ErrMsgMaximumVolumeCountOnNode) {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMaximumVolumeCount, err))
		}
		return nil, err
	}

	// Poll the job if it exists
	if job != nil {
		if err = client.Poll(job.JobUUID); err != nil {
			// ONTAP error format: "Request to create volume ... failed because there is not enough space in aggregate ..."
			// This occurs when hot tier is full even though pool has capacity
			if strings.Contains(err.Error(), "not enough space") || strings.Contains(err.Error(), "insufficient space") {
				return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrHotTierCapacityExhausted, errors.NewUserInputValidationErr("The hot tier (local storage) capacity is full. Free up space in the hot tier or enable hot tier auto-resize to automatically expand the hot tier when needed.")))
			}
			if strings.Contains(strings.ToLower(err.Error()), ErrMsgMaximumVolumeCountOnNode) {
				return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMaximumVolumeCount, err))
			}
			return nil, err
		}
	}

	// Validate the Volume response to avoid nil pointer dereferences
	if vol == nil {
		return nil, errors.New("invalid Volume response from API: volume is nil")
	}
	if vol.Name == nil {
		return nil, errors.New("invalid Volume response from API: name is nil")
	}
	if vol.UUID == nil {
		return nil, errors.New("invalid Volume response from API: UUID is nil")
	}

	// Perform additional GET call if state is nil (necessary workaround for large FlexGroup volumes with many constituents)
	if vol.State == nil {
		getVol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
			UUID: *vol.UUID,
			Name: *vol.Name,
			BaseParams: ontapRest.BaseParams{
				Fields: []string{"state", "space.*", "size", "constituents"},
			},
		})
		if err != nil {
			return nil, err
		}
		if getVol == nil || getVol.State == nil {
			return nil, errors.New("invalid Volume response from API: state is nil")
		}
		vol = getVol
	}

	// When AllSquash is enabled with an explicit AnonUid, ONTAP requires a post-create
	// VolumeModify to assign nas.uid so the volume root directory inode reflects the correct
	// owner UID. ONTAP does not accept nas.uid during volume create.
	if params.AllSquashUid != nil {
		success, job, modifyErr := client.Storage().VolumeModify(&ontapRest.VolumeModifyParams{
			UUID:   *vol.UUID,
			NasUid: params.AllSquashUid,
		})
		if modifyErr != nil {
			return nil, fmt.Errorf("volume created but failed to set NAS UID for AllSquash: %w", modifyErr)
		}
		if !success && job != nil {
			if modifyErr = client.Poll(job.JobUUID); modifyErr != nil {
				return nil, fmt.Errorf("volume created but NAS UID job failed for AllSquash: %w", modifyErr)
			}
		}
	}

	// Return the created volume
	volRes := &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		State: *vol.State,
	}

	// adding nil pointer checks as in some cases it may not be populated like FlexGroup volumes with large number of constituents
	if vol.Space != nil {
		if vol.Space.Available != nil {
			volRes.AvailableSpace = *vol.Space.Available
		}
		if vol.Space.SizeAvailableForSnapshots != nil {
			volRes.SnapReserve = *vol.Space.SizeAvailableForSnapshots
		}
		if vol.Space.AfsTotal != nil {
			volRes.AFSSize = *vol.Space.AfsTotal
		}
		if vol.Space.Metadata != nil {
			volRes.MetadataSize = *vol.Space.Metadata
		}
	}
	if vol.Size != nil {
		volRes.Size = *vol.Size
	}

	// Extract constituent count from the created volume
	if vol.ConstituentCount != nil {
		count := int32(*vol.ConstituentCount)
		volRes.ConstituentCount = &count
	} else if vol.VolumeInlineConstituents != nil {
		// If constituent_count is not available but constituents array is available (even if empty), use the length
		count := int32(len(vol.VolumeInlineConstituents))
		volRes.ConstituentCount = &count
	}

	return volRes, nil
}

type privateCLIVolumeCreateConfig struct {
	reconciliationAttempts int
	reconciliationDelay    time.Duration
}

func createVolumeViaPrivateCLI(ctx context.Context, client ontapRest.RESTClient, params *ontapRest.VolumeCreateParams) (*ontapRest.Volume, error) {
	return createVolumeViaPrivateCLIWithConfig(ctx, client, params, privateCLIVolumeCreateConfig{
		reconciliationAttempts: 5,
		reconciliationDelay:    time.Second,
	})
}

func createVolumeViaPrivateCLIWithConfig(
	ctx context.Context,
	client ontapRest.RESTClient,
	params *ontapRest.VolumeCreateParams,
	config privateCLIVolumeCreateConfig,
) (*ontapRest.Volume, error) {
	if config.reconciliationAttempts <= 0 {
		return nil, errors.New("private CLI volume create requires at least one reconciliation attempt")
	}
	command, err := buildPrivateCLIVolumeCreateCommand(params)
	if err != nil {
		return nil, err
	}

	if _, err = executeONTAPCommand(ctx, client, command); err != nil {
		if isDuplicateVolumeCreateError(err) {
			return nil, errors.NewConflictErr(params.Name + " already exists")
		}
		return nil, err
	}

	svmName := params.Svm
	var lastErr error
	for attempt := 0; attempt < config.reconciliationAttempts; attempt++ {
		vol, getErr := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
			Name:    params.Name,
			SvmName: &svmName,
			BaseParams: ontapRest.BaseParams{
				Fields: ontapRest.DefaultVolumeDetailFields,
			},
		})
		if getErr == nil && vol != nil {
			return vol, nil
		}
		if getErr != nil {
			lastErr = getErr
		} else {
			lastErr = errors.New("private CLI volume create reconciliation returned an empty volume")
		}
		if attempt < config.reconciliationAttempts-1 {
			time.Sleep(config.reconciliationDelay)
		}
	}
	return nil, fmt.Errorf("private CLI volume create succeeded but reconciliation failed: %w", lastErr)
}

func isDuplicateVolumeCreateError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate volume name") ||
		(strings.Contains(message, "volume") && strings.Contains(message, "already exists"))
}

// executeONTAPCommand blocks until ONTAP finishes the command. REST waits through
// client.Poll, which heartbeats.
func executeONTAPCommand(ctx context.Context, client ontapRest.RESTClient, command string) (string, error) {
	if ctx != nil && activity.IsActivity(ctx) {
		done := make(chan struct{})
		defer close(done)
		go func() {
			safeRecordHeartbeat(ctx, "Running ONTAP volume command")
			ticker := time.NewTicker(privateCLIHeartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					safeRecordHeartbeat(ctx, "Running ONTAP volume command")
				}
			}
		}()
	}

	privilege := "admin"
	response, err := client.Networking().CliExecute(
		networkpriv.NewCliExecuteParams().
			WithBody(&privmodels.CliExecute{Input: &command, Privilege: &privilege}).
			WithTimeout(privateCLIVolumeCommandTimeout),
	)
	if err != nil {
		var cliErr *networkpriv.CliExecuteDefault
		if stderrors.As(err, &cliErr) && cliErr.Payload != nil && cliErr.Payload.Error != nil &&
			cliErr.Payload.Error.Message != nil && *cliErr.Payload.Error.Message != "" {
			return "", errors.New(*cliErr.Payload.Error.Message)
		}
		return "", err
	}
	if response == nil || response.Payload == nil {
		return "", errors.New("invalid volume operation response")
	}
	if cmdErr := ontapCommandFailure(response.Payload.Output); cmdErr != nil {
		return "", cmdErr
	}
	return response.Payload.Output, nil
}

func ontapCommandFailure(output string) error {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "error:") ||
		strings.Contains(lower, "command failed") ||
		strings.Contains(lower, "job failed") {
		return errors.New(trimmed)
	}
	return nil
}

func updateVolumeViaPrivateCLI(ctx context.Context, client ontapRest.RESTClient, params UpdateVolumeParams) error {
	if params.InitiateSplit || params.NasUid != nil {
		return errors.New("clone split and NAS UID updates are separate volume operations")
	}
	if params.SvmName == "" || params.VolumeName == "" {
		return errors.New("volume name and SVM name are required")
	}

	includeCloudWrite := true
	if cloudWriteDisableComesFirst(params.TieringPolicy) {
		command := strings.Join([]string{
			"volume", "modify",
			"-vserver", quoteONTAPCLIValue(params.SvmName),
			"-volume", quoteONTAPCLIValue(params.VolumeName),
			"-is-cloud-write-enabled", "false",
			"-foreground", "true",
		}, " ")
		if _, err := executeONTAPCommand(ctx, client, command); err != nil {
			return err
		}
		includeCloudWrite = false
	}

	command, hasChanges := buildPrivateCLIVolumeModifyCommand(params, includeCloudWrite)
	if hasChanges {
		if _, err := executeONTAPCommand(ctx, client, command); err != nil {
			return err
		}
	}
	// volume modify does not accept -junction-path. Mount sets nas.path on its own.
	return mountVolumeJunctionPath(client, params)
}

func mountVolumeJunctionPath(client ontapRest.RESTClient, params UpdateVolumeParams) error {
	if params.JunctionPath == nil || *params.JunctionPath == "" {
		return nil
	}
	if params.UUID == "" {
		return errors.New("volume UUID is required")
	}
	job, err := client.Storage().VolumeMount(&ontapRest.VolumeMountParams{
		UUID:         params.UUID,
		JunctionPath: *params.JunctionPath,
	})
	if err != nil {
		return err
	}
	if job == nil || job.JobUUID == "" {
		return errors.New("invalid volume mount response")
	}
	return client.Poll(job.JobUUID)
}

func cloudWriteDisableComesFirst(policy *TieringPolicy) bool {
	return policy != nil &&
		policy.CloudWriteModeEnabled != nil &&
		!*policy.CloudWriteModeEnabled &&
		policy.CoolAccessTieringPolicy != models.VolumeInlineTieringPolicyAll
}

func buildPrivateCLIVolumeModifyCommand(params UpdateVolumeParams, includeCloudWrite bool) (string, bool) {
	commandArguments := []string{
		"volume", "modify",
		"-vserver", quoteONTAPCLIValue(params.SvmName),
		"-volume", quoteONTAPCLIValue(params.VolumeName),
	}
	var changeArguments []string
	if params.Size != 0 {
		changeArguments = append(changeArguments, "-size", fmt.Sprintf("%dB", params.Size))
	}
	if params.SnapReserve != nil {
		changeArguments = append(changeArguments, "-percent-snapshot-space", strconv.FormatInt(*params.SnapReserve, 10))
	}
	if params.SnapshotPolicyName != "" {
		changeArguments = append(changeArguments, "-snapshot-policy", quoteONTAPCLIValue(params.SnapshotPolicyName))
	}
	if params.SnapshotDirectoryAccess != nil {
		changeArguments = append(changeArguments, "-snapdir-access", strconv.FormatBool(*params.SnapshotDirectoryAccess))
	}
	if params.ExportPolicy != nil && *params.ExportPolicy != "" {
		changeArguments = append(changeArguments, "-policy", quoteONTAPCLIValue(*params.ExportPolicy))
	}
	if params.QosPolicyName != nil {
		changeArguments = append(changeArguments, "-qos-policy-group", quoteONTAPCLIValue(*params.QosPolicyName))
	}
	if params.UnixPermissions != nil {
		if unixPermissions, err := strconv.Atoi(*params.UnixPermissions); err == nil {
			changeArguments = append(changeArguments, "-unix-permissions", fmt.Sprintf("%04d", unixPermissions))
		}
	}
	changeArguments = append(changeArguments, privateCLITieringArguments(params.TieringPolicy, includeCloudWrite)...)
	if len(changeArguments) == 0 {
		return "", false
	}
	commandArguments = append(commandArguments, changeArguments...)
	// FlexGroup modifies otherwise return before ONTAP finishes the background job.
	commandArguments = append(commandArguments, "-foreground", "true")
	return strings.Join(commandArguments, " "), true
}

func privateCLITieringArguments(policy *TieringPolicy, includeCloudWrite bool) []string {
	if policy == nil {
		return nil
	}
	var arguments []string
	if policy.CoolAccessTieringPolicy != "" {
		arguments = append(arguments, "-tiering-policy", quoteONTAPCLIValue(policy.CoolAccessTieringPolicy))
		if policy.CoolnessPeriod != 0 &&
			policy.CoolAccessTieringPolicy != models.VolumeInlineTieringPolicyNone &&
			policy.CoolAccessTieringPolicy != models.VolumeInlineTieringPolicyAll {
			arguments = append(arguments, "-tiering-minimum-cooling-days", strconv.FormatInt(policy.CoolnessPeriod, 10))
		}
	}
	if policy.CoolAccessRetrievalPolicy != "" {
		arguments = append(arguments, "-cloud-retrieval-policy", quoteONTAPCLIValue(policy.CoolAccessRetrievalPolicy))
	}
	if includeCloudWrite && policy.CloudWriteModeEnabled != nil {
		arguments = append(arguments, "-is-cloud-write-enabled", strconv.FormatBool(*policy.CloudWriteModeEnabled))
	}
	return arguments
}

func mapVolumeModifyError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), ErrMsgVolumeMaxSizeExceeded) {
		maxSizeStart := strings.Index(err.Error(), "The maximum possible size is ")
		if maxSizeStart > 0 {
			maxSizeStr := err.Error()[maxSizeStart:]
			sanitizedError := fmt.Sprintf("Volume size exceeds the maximum allowed size. %s", maxSizeStr)
			return vsaerrors.NewVCPError(vsaerrors.ErrVolumeExceedsMaximumSize,
				errors.NewUserInputValidationErr(sanitizedError))
		}
	}
	if strings.Contains(err.Error(), ErrMsgVolumeSizeTooSmall) {
		minSizeStart := strings.Index(err.Error(), "New volume size must be at least ")
		var sanitizedError string
		if minSizeStart >= 0 {
			minSizeEnd := strings.Index(err.Error()[minSizeStart:], " to hold")
			if minSizeEnd > 0 {
				minSizeStr := err.Error()[minSizeStart : minSizeStart+minSizeEnd]
				sanitizedError = fmt.Sprintf("Selected volume size is too small to hold the current volume data. %s", minSizeStr)
			} else {
				sanitizedError = "Selected volume size is too small to hold the current volume data. Please increase the volume size."
			}
		} else {
			sanitizedError = "Selected volume size is too small to hold the current volume data. Please increase the volume size."
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrVolumeSizeTooSmall,
			errors.NewUserInputValidationErr(sanitizedError))
	}
	return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
}

func mapPrivateCLIVolumeModifyError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "is too small") || strings.Contains(message, "new volume size must be at least") {
		return vsaerrors.NewVCPError(vsaerrors.ErrVolumeSizeTooSmall,
			errors.NewUserInputValidationErr("Selected volume size is too small. Please increase the volume size."))
	}
	if strings.Contains(message, "is too large") || strings.Contains(message, "greater than the maximum size") {
		return vsaerrors.NewVCPError(vsaerrors.ErrVolumeExceedsMaximumSize,
			errors.NewUserInputValidationErr("Volume size exceeds the maximum allowed size."))
	}
	return mapVolumeModifyError(err)
}

func buildPrivateCLIVolumeCreateCommand(params *ontapRest.VolumeCreateParams) (string, error) {
	if params == nil {
		return "", errors.New("volume create parameters are required for private CLI volume create")
	}
	if params.Name == "" || params.Svm == "" {
		return "", errors.New("volume name and SVM name are required for private CLI volume create")
	}
	if params.Style == nil || *params.Style != ontapRest.VolumeStyleFlexGroup {
		return "", errors.New("private CLI volume create is only supported for FlexGroup volumes")
	}
	if params.Size <= 0 || params.Type == "" {
		return "", errors.New("positive volume size and volume type are required for private CLI volume create")
	}
	if len(params.Aggregates) == 0 || params.ConstituentsPerAggregate == nil || *params.ConstituentsPerAggregate <= 0 {
		return "", errors.New("aggregate list and positive aggregate multiplier are required for private CLI volume create")
	}

	aggregateList := strings.Join(params.Aggregates, ",")
	arguments := []string{
		"volume", "create",
		"-vserver", quoteONTAPCLIValue(params.Svm),
		"-volume", quoteONTAPCLIValue(params.Name),
		"-aggr-list", quoteONTAPCLIValue(aggregateList),
		"-size", fmt.Sprintf("%dB", params.Size),
		"-state", ontapRest.VolumeStateOnline,
		"-type", quoteONTAPCLIValue(params.Type),
		"-aggr-list-multiplier", strconv.FormatInt(*params.ConstituentsPerAggregate, 10),
		"-granular-data", ontapRest.GranularDataModeAdvanced,
		"-space-guarantee", ontapRest.GuaranteeTypeNone,
		"-autosize-mode", "off",
		"-is-space-enforcement-logical", "true",
		"-is-space-reporting-logical", "true",
		"-percent-snapshot-space", strconv.FormatInt(params.SnapshotReservePercent, 10),
		"-snapdir-access", strconv.FormatBool(params.SnapshotDirectoryAccessEnabled),
		"-foreground", "true",
	}
	if params.SnapshotPolicy != "" {
		arguments = append(arguments, "-snapshot-policy", quoteONTAPCLIValue(params.SnapshotPolicy))
	}
	appendCLIArgument := func(flag string, value *string) {
		if value != nil && *value != "" {
			arguments = append(arguments, flag, quoteONTAPCLIValue(*value))
		}
	}
	appendCLIArgument("-policy", params.ExportPolicy)
	appendCLIArgument("-junction-path", params.JunctionPath)
	if params.SecurityStyle != "" {
		securityStyle := strings.ToLower(params.SecurityStyle)
		appendCLIArgument("-security-style", &securityStyle)
	}
	if params.UnixPermissions != nil {
		if unixPermissions, err := strconv.Atoi(*params.UnixPermissions); err == nil {
			// The CLI expects the four-digit octal form (0770) REST takes the integer 770.
			normalizedPermissions := fmt.Sprintf("%04d", unixPermissions)
			appendCLIArgument("-unix-permissions", &normalizedPermissions)
		}
	}
	if params.QosPolicy != "" {
		appendCLIArgument("-qos-policy-group", &params.QosPolicy)
	}
	if params.LargeSizeEnabled != nil && *params.LargeSizeEnabled {
		arguments = append(arguments, "-is-large-size-enabled", "true")
	}
	if params.TieringPolicy != nil {
		tieringPolicy := params.TieringPolicy.CoolAccessTieringPolicy
		if tieringPolicy != "" {
			arguments = append(arguments, "-tiering-policy", quoteONTAPCLIValue(tieringPolicy))
		}
		if tieringPolicy == models.VolumeInlineTieringPolicyAuto ||
			tieringPolicy == models.VolumeInlineTieringPolicySnapshotOnly {
			arguments = append(arguments, "-tiering-minimum-cooling-days", strconv.FormatInt(params.TieringPolicy.MinCoolingDays, 10))
			if params.TieringPolicy.CloudRetrievalPolicy != "" {
				arguments = append(arguments, "-cloud-retrieval-policy", quoteONTAPCLIValue(params.TieringPolicy.CloudRetrievalPolicy))
			}
		}
		if params.TieringPolicy.CloudWriteModeEnabled != nil {
			arguments = append(arguments, "-is-cloud-write-enabled", strconv.FormatBool(*params.TieringPolicy.CloudWriteModeEnabled))
		}
	}

	return strings.Join(arguments, " "), nil
}

func quoteONTAPCLIValue(value string) string {
	if strings.ContainsAny(value, ";\r\n") {
		return strconv.Quote(value)
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			!strings.ContainsRune("-_.,:/*+@", r)
	}) == -1 {
		return value
	}
	return strconv.Quote(value)
}

// DeleteVolume deletes a volume by calling the ONTAP REST Client
// It polls the job internally if the deletion is async, similar to CreateVolume
func (rc *OntapRestProvider) DeleteVolume(volumeUUID, volumeName string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	// First, get the volume details to check if it's in use for splitting or has clones
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID: volumeUUID,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"uuid", "name", "clone.*", "snapmirror.*"},
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "entry doesn't exist") || strings.Contains(err.Error(), "entry not found") || strings.Contains(err.Error(), "UUID and Name parameters cannot be empty when querying for a volume") {
			return nil
		}
		return err
	}
	// Check if the volume is in use for splitting or has clones
	if vol != nil && vol.Clone != nil {
		// Check if volume has FlexClone volumes or a split is initiated
		if (vol.Clone.HasFlexclone != nil && *vol.Clone.HasFlexclone) ||
			(vol.Clone.SplitInitiated != nil && *vol.Clone.SplitInitiated) {
			return vsaerrors.NewVCPError(vsaerrors.ErrDeleteVolumeWhenInSplitState, errors.New("Cannot delete a volume that is being actively used in a Volume Replication relationship or a file clone split triggered by Snapshot RestoreFiles operation or used as a reference snapshot for a backup"))
		}
	}

	// If all checks pass, proceed with volume deletion
	job, err := client.Storage().VolumeDelete(&ontapRest.VolumeDeleteParams{
		UUID: volumeUUID,
		Name: volumeName,
	})

	if err != nil {
		return err
	}

	// Poll the job if it exists
	if job != nil {
		if err = client.Poll(job.JobUUID); err != nil {
			return err
		}
	}

	return nil
}

func (rc *OntapRestProvider) UnmountVolume(volumeUUID string) (*OntapAsyncResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	jobAccepted, err := client.Storage().VolumeUnmount(&ontapRest.VolumeUnmountParams{
		UUID: volumeUUID,
	})
	if err != nil {
		return nil, err
	}

	if jobAccepted != nil {
		return &OntapAsyncResponse{JobUUID: jobAccepted.JobUUID}, nil
	}

	return nil, nil
}

func (rc *OntapRestProvider) MountVolume(params MountVolumeParams) (*OntapAsyncResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	jobAccepted, err := client.Storage().VolumeMount(&ontapRest.VolumeMountParams{
		UUID:         params.UUID,
		JunctionPath: params.JunctionPath,
	})
	if err != nil {
		return nil, err
	}

	// Poll the job if it exists
	if jobAccepted != nil {
		if err = client.Poll(jobAccepted.JobUUID); err != nil {
			return nil, err
		}
	}

	return &OntapAsyncResponse{JobUUID: jobAccepted.JobUUID}, nil
}

// GetVolume returns a volume by calling the ONTAP REST Client
func (rc *OntapRestProvider) GetVolume(params GetVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID:                           params.UUID,
		Name:                           params.VolumeName,
		SvmName:                        &params.SvmName,
		SnapshotDirectoryAccessEnabled: &params.SnapshotDirectory,
		BaseParams: ontapRest.BaseParams{
			Fields: ontapRest.DefaultVolumeDetailFields,
		},
	})
	if err != nil {
		return nil, err
	}
	// Match ListVolumesForSVM: do not treat missing logical_space as not-found. ONTAP
	// VolumeGet may omit space.logical_space even when the volume exists.
	return volumeResponseFromONTAPVol(vol, false)
}

func volumeResponseFromONTAPVol(vol *ontapRest.Volume, requireLogicalSpace bool) (*VolumeResponse, error) {
	if vol == nil {
		return nil, errors.NewNotFoundErr("volume", nil)
	}
	if vol.Name == nil || *vol.Name == "" || vol.UUID == nil || *vol.UUID == "" || vol.Space == nil || vol.State == nil {
		return nil, fmt.Errorf("incomplete ONTAP volume response: missing required fields (name/uuid/space/state)")
	}
	if requireLogicalSpace && vol.Space.LogicalSpace == nil {
		return nil, fmt.Errorf("incomplete ONTAP volume response: missing required fields (logical_space)")
	}
	usedBytes := int64(0)
	if vol.Space.LogicalSpace != nil && vol.Space.LogicalSpace.Used != nil {
		usedBytes = nillable.FromPointer(vol.Space.LogicalSpace.Used)
	}
	volType := ""
	if vol.Type != nil {
		volType = *vol.Type
	}
	res := &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		State:     *vol.State,
		UsedBytes: usedBytes,
		Type:      volType,
	}
	if vol.Space.Available != nil {
		res.AvailableSpace = *vol.Space.Available
	}
	if vol.Space.Size != nil {
		res.Size = *vol.Space.Size
	}
	if vol.SnapshotPolicy != nil && vol.SnapshotPolicy.Name != nil {
		res.SnapshotPolicyName = *vol.SnapshotPolicy.Name
	}
	if vol.SnapshotDirectoryAccessEnabled != nil {
		res.SnapshotDirectoryAccessEnabled = *vol.SnapshotDirectoryAccessEnabled
	}
	if vol.Qos != nil && vol.Qos.Policy != nil {
		if vol.Qos.Policy.UUID != nil {
			res.QoSPolicyUUID = *vol.Qos.Policy.UUID
		}
		if vol.Qos.Policy.Name != nil {
			res.QoSPolicyName = *vol.Qos.Policy.Name
		}
	}
	if vol.Space.Snapshot != nil && vol.Space.Snapshot.ReservePercent != nil {
		res.SnapReserve = *vol.Space.Snapshot.ReservePercent
	}
	if vol.Space.Metadata != nil {
		res.MetadataSize = *vol.Space.Metadata
	}
	if vol.Space.AfsTotal != nil {
		res.AFSSize = *vol.Space.AfsTotal
	}
	if vol.VolumeInlineConstituents != nil {
		if len(vol.VolumeInlineConstituents) > 0 {
			count := int32(len(vol.VolumeInlineConstituents))
			res.ConstituentCount = &count
		} else {
			zero := int32(0)
			res.ConstituentCount = &zero
		}
	}
	if vol.Nas != nil {
		if vol.Nas.Path != nil {
			res.JunctionPath = *vol.Nas.Path
		}
		if vol.Nas.SecurityStyle != nil {
			res.SecurityStyle = *vol.Nas.SecurityStyle
		}
		if vol.Nas.UnixPermissions != nil {
			res.UnixPermissions = formatUnixPermissionsOctal(*vol.Nas.UnixPermissions)
		}
		if vol.Nas.ExportPolicy != nil && vol.Nas.ExportPolicy.Name != nil {
			res.ExportPolicyName = *vol.Nas.ExportPolicy.Name
		}
	}
	if vol.CreateTime != nil {
		res.Created = time.Time(*vol.CreateTime)
	}
	return res, nil
}

// ListVolumesForSVM returns volume details for all volumes on the specified SVM.
func (rc *OntapRestProvider) ListVolumesForSVM(svmName string) ([]*VolumeResponse, error) {
	var result []*VolumeResponse

	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	collectVolumes := func(volumes []*ontapRest.Volume) error {
		for _, vol := range volumes {
			if vol == nil || (vol.IsSvmRoot != nil && *vol.IsSvmRoot) {
				continue
			}
			mapped, err := volumeResponseFromONTAPVol(vol, false)
			if err != nil {
				if rc.Logger != nil {
					rc.Logger.Warn("skipping ONTAP volume that could not be mapped", "volume", ontapVolumeIdentifier(vol), "svm", svmName, "error", err)
				}
				continue
			}
			result = append(result, mapped)
		}
		return nil
	}

	err = client.Storage().VolumeCollectionGet(&ontapRest.VolumeCollectionGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: ontapRest.DefaultVolumeDetailFields,
		},
		SvmName: &svmName,
	}, collectVolumes)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func ontapVolumeIdentifier(vol *ontapRest.Volume) string {
	if vol == nil {
		return ""
	}
	if vol.UUID != nil && *vol.UUID != "" {
		return *vol.UUID
	}
	if vol.Name != nil {
		return *vol.Name
	}
	return ""
}

func formatUnixPermissionsOctal(permissions int64) string {
	// ONTAP stores unix_permissions as the decimal form of the octal digit string
	// (e.g. "0755" -> 755), not as the octal numeric value.
	return fmt.Sprintf("%04d", permissions)
}

func (rc *OntapRestProvider) GetVolumeForExpertMode(params GetVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID:    params.UUID,
		Name:    params.VolumeName,
		SvmName: &params.SvmName,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"uuid", "name", "state", "type", "size", "style"},
		},
	})
	if err != nil {
		return nil, err
	}
	if vol == nil || vol.Name == nil || vol.UUID == nil || vol.Size == nil || vol.State == nil {
		return nil, fmt.Errorf("GetVolumeForExpertMode: incomplete ONTAP volume response: missing required fields (name/uuid/size/state)")
	}
	volStyle := ""
	if vol.Style != nil {
		volStyle = *vol.Style
	}
	res := &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		Size:  *vol.Size,
		State: *vol.State,
		Style: volStyle,
	}
	return res, nil
}

// GetCloneVolumeForExpertMode fetches clone metadata for expert mode workflows.
func (rc *OntapRestProvider) GetCloneVolumeForExpertMode(params GetVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID:    params.UUID,
		Name:    params.VolumeName,
		SvmName: &params.SvmName,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"uuid", "name", "state", "type", "size", "style", "clone.*"},
		},
	})
	if err != nil {
		return nil, err
	}
	if vol.Name == nil || vol.UUID == nil || vol.Size == nil || vol.State == nil {
		return nil, fmt.Errorf("GetCloneVolumeForExpertMode: incomplete ONTAP volume response: missing required fields (name/uuid/size/state)")
	}
	volStyle := ""
	if vol.Style != nil {
		volStyle = *vol.Style
	}
	res := &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		Size:  *vol.Size,
		State: *vol.State,
		Style: volStyle,
	}
	if vol.Clone != nil {
		res.Clone = &VolumeResponseClone{}
		if vol.Clone.ParentVolume != nil {
			res.Clone.ParentVolumeName = nillable.FromPointer(vol.Clone.ParentVolume.Name)
			res.Clone.ParentVolumeUUID = nillable.FromPointer(vol.Clone.ParentVolume.UUID)
		}
		if vol.Clone.ParentSnapshot != nil {
			res.Clone.ParentSnapshotName = nillable.FromPointer(vol.Clone.ParentSnapshot.Name)
			res.Clone.ParentSnapshotUUID = nillable.FromPointer(vol.Clone.ParentSnapshot.UUID)
		}
		if vol.Clone.IsFlexclone != nil {
			res.Clone.IsFlexclone = vol.Clone.IsFlexclone
		}
		if vol.Clone.SplitInitiated != nil {
			res.Clone.SplitInitiated = vol.Clone.SplitInitiated
		}
		if vol.Clone.SplitCompletePercent != nil {
			res.Clone.SplitCompletePercent = vol.Clone.SplitCompletePercent
		}
	}
	return res, nil
}

func (rc *OntapRestProvider) GetVolumes() ([]*Volume, error) {
	var resultVolumes []*Volume

	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	ucbf := func(volumes []*ontapRest.Volume) error {
		for _, volume := range volumes {
			vol := &Volume{
				Volume: models.Volume{
					Name:      volume.Name,
					UUID:      volume.UUID,
					Svm:       volume.Svm,
					IsSvmRoot: volume.IsSvmRoot,
					Style:     volume.Style,
					Space:     volume.Space,
					Type:      volume.Type,
					Clone:     volume.Clone,
				},
				ExternalUUID: *volume.UUID,
			}
			resultVolumes = append(resultVolumes, vol)
		}
		return nil
	}

	// Build base fields list
	fields := []string{"uuid", "name", "space.*", "svm", "is_svm_root", "style", "type", "clone.split_complete_percent", "clone.is_flexclone"}

	// Conditionally add clone fields if feature flag is enabled
	if enableCloneInfoRefresh {
		fields = append(fields, "clone.parent_snapshot.name", "clone.parent_volume.name")
	}

	err = client.Storage().VolumeCollectionGet(&ontapRest.VolumeCollectionGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: fields,
		},
	}, ucbf)

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return resultVolumes, nil
}

// GetVolumesForInventory returns the volume fields needed to build project
// inventory, scoped to SVMs matching svmNamePattern.
func (rc *OntapRestProvider) GetVolumesForInventory(svmNamePattern string) ([]*Volume, error) {
	var resultVolumes []*Volume

	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	collectVolumes := func(volumes []*ontapRest.Volume) error {
		for _, volume := range volumes {
			if volume == nil {
				continue
			}
			resultVolumes = append(resultVolumes, &Volume{
				Volume:       volume.Volume,
				ExternalUUID: nillable.GetString(volume.UUID, ""),
			})
		}
		return nil
	}

	const maxInventoryRecords int64 = 10000
	err = client.Storage().VolumeCollectionGet(&ontapRest.VolumeCollectionGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields:     []string{"name", "uuid", "svm.name", "svm.uuid", "space.physical_used", "space.size", "aggregates.name"},
			MaxRecords: nillable.ToPointer(maxInventoryRecords),
		},
		SvmName: nillable.ToPointer(svmNamePattern),
	}, collectVolumes)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return resultVolumes, nil
}

func (rc *OntapRestProvider) UpdateVolume(params UpdateVolumeParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	if params.UsePrivateCLI {
		return mapPrivateCLIVolumeModifyError(updateVolumeViaPrivateCLI(rc.ClientParams.Ctx, client, params))
	}
	volumeModifyParams := &ontapRest.VolumeModifyParams{
		UUID:        params.UUID,
		SnapReserve: params.SnapReserve,
	}

	if params.TieringPolicy != nil {
		volumeModifyParams.TieringPolicy = &ontapRest.TieringPolicy{
			CoolAccessTieringPolicy: params.TieringPolicy.CoolAccessTieringPolicy,
			MinCoolingDays:          params.TieringPolicy.CoolnessPeriod,
			CloudRetrievalPolicy:    params.TieringPolicy.CoolAccessRetrievalPolicy,
			CloudWriteModeEnabled:   params.TieringPolicy.CloudWriteModeEnabled,
		}
	}

	if params.InitiateSplit {
		volumeModifyParams.SplitInitiated = &params.InitiateSplit
		volumeModifyParams.MatchParentStorageTier = false // TODO: add this `params.TieringPolicy == "auto"`, when autotier is supported
	} else {
		if params.Size != 0 {
			volumeModifyParams.Size = nillable.ToPointer(uint64(params.Size))
		}
		volumeModifyParams.SnapshotPolicyName = nillable.GetStringPtr(params.SnapshotPolicyName)
	}

	if params.ExportPolicy != nil {
		volumeModifyParams.ExportPolicy = params.ExportPolicy
	}
	if params.JunctionPath != nil {
		volumeModifyParams.Path = params.JunctionPath
	}
	if params.SnapshotDirectoryAccess != nil {
		volumeModifyParams.SnapshotDirectoryAccessEnabled = params.SnapshotDirectoryAccess
	}

	// Handle QoS policy assignment/unassignment
	// If QosPolicyName is provided (not nil), set it:
	// - Use "none" to unassign (no policy) per ONTAP API specification
	// - Use policy name to assign that policy
	// - Empty string will be rejected by ONTAP with an appropriate error
	// If QosPolicyName is nil, don't change the policy
	if params.QosPolicyName != nil {
		volumeModifyParams.QosPolicy = params.QosPolicyName
	}

	if params.UnixPermissions != nil {
		volumeModifyParams.UnixPermissions = params.UnixPermissions
	}

	if params.NasUid != nil {
		volumeModifyParams.NasUid = params.NasUid
	}

	err = handleVolumeCloudWriteModeDisableIfProvided(client, volumeModifyParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	success, job, err := client.Storage().VolumeModify(volumeModifyParams)
	if err != nil {
		return mapVolumeModifyError(err)
	}
	if success || params.InitiateSplit { // Split operation can run in background without polling
		return nil
	}
	return client.Poll(job.JobUUID)
}

// InitiateSplitVolume sends the split-initiation request to ONTAP for the given volume UUID
// and returns the ONTAP job UUID that tracks the background data-movement operation.
// this method does NOT poll the job to completion — the caller is
// responsible for polling via GetOntapJob / WaitForONTAPJob.
func (rc *OntapRestProvider) InitiateSplitVolume(volumeUUID string) (string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return "", err
	}
	splitInitiated := true
	volumeModifyParams := &ontapRest.VolumeModifyParams{
		UUID:                   volumeUUID,
		SplitInitiated:         &splitInitiated,
		MatchParentStorageTier: false,
	}
	success, job, err := client.Storage().VolumeModify(volumeModifyParams)
	if err != nil {
		return "", err
	}
	if success || job == nil {
		// ONTAP completed synchronously or returned no job — treat as success with no job UUID.
		return "", nil
	}
	return job.JobUUID, nil
}

// StopSplitVolume sends a stop-split request to ONTAP for the given volume UUID by
// PATCHing volume.split_initiated=false. The operation is synchronous from ONTAP's
// perspective: ONTAP halts the background data-movement, partial split progress is
// preserved, and the volume remains a thin clone of its parent. Callers should
// validate that a split is currently in progress before invoking this; ONTAP will
// return an error like "Operation is not in progress" otherwise.
func (rc *OntapRestProvider) StopSplitVolume(volumeUUID string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	splitInitiated := false
	volumeModifyParams := &ontapRest.VolumeModifyParams{
		UUID:                   volumeUUID,
		SplitInitiated:         &splitInitiated,
		MatchParentStorageTier: false,
	}
	_, _, err = client.Storage().VolumeModify(volumeModifyParams)
	if err != nil {
		return err
	}
	return nil
}

// GetVolumeCloneInfo fetches just the clone-related fields for a volume identified
// by its UUID. Used by the synchronous splitStop path to read split progress
// (split_complete_percent, split_initiated) immediately before issuing the stop so
// the caller can return a meaningful CloneDetails in the API response.
func (rc *OntapRestProvider) GetVolumeCloneInfo(volumeUUID string) (*VolumeResponseClone, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID: volumeUUID,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"clone.*"},
		},
	})
	if err != nil {
		return nil, err
	}
	if vol == nil || vol.Clone == nil {
		return nil, nil
	}
	res := &VolumeResponseClone{
		SplitInitiated:       vol.Clone.SplitInitiated,
		SplitCompletePercent: vol.Clone.SplitCompletePercent,
	}
	if vol.Clone.ParentVolume != nil {
		res.ParentVolumeName = nillable.FromPointer(vol.Clone.ParentVolume.Name)
		res.ParentVolumeUUID = nillable.FromPointer(vol.Clone.ParentVolume.UUID)
	}
	if vol.Clone.ParentSnapshot != nil {
		res.ParentSnapshotName = nillable.FromPointer(vol.Clone.ParentSnapshot.Name)
		res.ParentSnapshotUUID = nillable.FromPointer(vol.Clone.ParentSnapshot.UUID)
	}
	if vol.Clone.IsFlexclone != nil {
		isFlexclone := *vol.Clone.IsFlexclone
		res.IsFlexclone = &isFlexclone
	}
	return res, nil
}

// UnassignQoSPolicyFromVolume unassigns the QoS policy from a volume by setting it to "none".
// This is a convenience function that wraps UpdateVolume with the correct "none" value
// as required by the ONTAP API specification.
func (rc *OntapRestProvider) UnassignQoSPolicyFromVolume(volumeUUID string) error {
	none := "none"
	params := UpdateVolumeParams{
		UUID:          volumeUUID,
		QosPolicyName: &none,
	}
	return rc.UpdateVolume(params)
}

func handleVolumeCloudWriteModeDisableIfProvided(client ontapRest.RESTClient, params *ontapRest.VolumeModifyParams) error {
	// For files volume, when changing ontap auto-tiering policy from all to auto/none/snapshot_only
	// policy along with disabling cloud write mode, ontap throws error. It is required to
	// disable the cloud write mode first and then change the policy.
	if params.TieringPolicy != nil &&
		params.TieringPolicy.CloudWriteModeEnabled != nil &&
		!*params.TieringPolicy.CloudWriteModeEnabled &&
		params.TieringPolicy.CoolAccessTieringPolicy != models.VolumeInlineTieringPolicyAll {
		success, job, err := client.Storage().VolumeModifyCloudWriteMode(params)
		if err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
		// Omit cloud_write_enabled from the subsequent VolumeModify. Re-sending it is rejected
		// on SnapMirror DP read-only volumes (ONTAP error 917628) even after a successful
		// VolumeModifyCloudWriteMode call.
		params.TieringPolicy.CloudWriteModeEnabled = nil
		if success {
			return nil
		}
		return client.Poll(job.JobUUID)
	}

	return nil
}

func (rc *OntapRestProvider) RevertVolume(params RevertVolumeParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	revertParams := &ontapRest.VolumeModifyParams{
		UUID:                  params.VolumeID,
		RestoreToSnapshotUUID: nillable.GetStringPtr(params.SnapshotID),
	}

	done, jj, err := client.Storage().VolumeModify(revertParams)
	if err != nil {
		if strings.Contains(err.Error(), "Failed to restore snapshot") || strings.Contains(err.Error(), "Volume snap restore error") {
			return vsaerrors.NewVCPError(vsaerrors.ErrRevertVolumeWhenSnapshotInUse, errors.NewUserInputValidationErr("Cannot revert a Volume when snapshot is in use by the cloned volume"))
		}
		if errors.IsNotFoundErr(err) {
			return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("Volume", nil))
		}
		if strings.Contains(err.Error(), "Only a Snapshot copy of a read/write volume can be promoted") {
			return vsaerrors.NewVCPError(vsaerrors.ErrRevertReplicationDestinationVolume, errors.NewUserInputValidationErr("Cannot revert a Volume Replication Destination Volume"))
		}
		if strings.Contains(err.Error(), "entry doesn't exist") || strings.Contains(err.Error(), "entry not found") {
			return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("Snapshot", nil))
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	if !done {
		if err = client.Poll(jj.JobUUID); err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
	}
	return nil
}

func (rc *OntapRestProvider) UpdateVolumeEnableEncryption(params UpdateVolumeParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	success, job, err := client.Storage().VolumeModify(
		&ontapRest.VolumeModifyParams{
			UUID:             params.UUID,
			EncryptionEnable: &params.EncryptionEnable,
		})
	if err != nil {
		return err
	}
	if success {
		return nil
	}
	return client.Poll(job.JobUUID)
}

// GetVolumeNASDetails fetches NAS-related properties (path, security style, export policy name)
// for a volume identified by its UUID.
func (rc *OntapRestProvider) GetVolumeNASDetails(volumeUUID string) (*VolumeNASDetails, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID: volumeUUID,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"nas.path", "nas.security_style", "nas.export_policy"},
		},
	})
	if err != nil {
		return nil, err
	}

	result := &VolumeNASDetails{}
	if vol != nil && vol.Nas != nil {
		if vol.Nas.Path != nil {
			result.NASPath = *vol.Nas.Path
		}
		if vol.Nas.SecurityStyle != nil {
			result.SecurityStyle = *vol.Nas.SecurityStyle
		}
		if vol.Nas.ExportPolicy != nil && vol.Nas.ExportPolicy.Name != nil {
			result.ExportPolicyName = *vol.Nas.ExportPolicy.Name
		}
	}
	return result, nil
}

// GetVolumeSANDetails fetches SAN-related properties (LUN and NVMe namespace presence)
// for a volume identified by its SVM name and volume name.
func (rc *OntapRestProvider) GetVolumeSANDetails(svmName, volumeName string) (*VolumeSANDetails, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	result := &VolumeSANDetails{}

	luns, lunErr := client.SAN().LunGet(&ontapRest.LunGetParams{
		SvmName:    &svmName,
		VolumeName: &volumeName,
	})
	if lunErr != nil && !errors.IsNotFoundErrForObjectTypeInChain(lunErr, "lun") {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, lunErr)
	}
	result.HasLUNs = len(luns) > 0

	namespaces, nsErr := client.NVMe().NamespaceGet(&ontapRest.NvmeNamespaceGetParams{
		SvmName:    &svmName,
		VolumeName: &volumeName,
	})
	if nsErr != nil && !errors.IsNotFoundErrForObjectTypeInChain(nsErr, "nvme namespace") {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, nsErr)
	}
	result.HasNamespaces = len(namespaces) > 0

	return result, nil
}

func (rc *OntapRestProvider) GetVolumeEncryptionStatus(params GetVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	volumeGetParams := &ontapRest.VolumeGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"encryption.*"}},
		UUID:       params.UUID,
		Name:       params.VolumeName,
		SvmName:    &params.SvmName,
	}
	vol, err := client.Storage().VolumeGet(volumeGetParams)
	if err != nil {
		return nil, err
	}
	if vol == nil || vol.Name == nil || vol.UUID == nil {
		return nil, errors.NewNotFoundErr("volume", nil)
	}
	if vol.Encryption == nil {
		return nil, errors.New("Encryption field is not populated in Get Volume from VSA")
	}

	return &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		Encryption: Encryption{
			Enabled: vol.Encryption.Enabled,
			State:   vol.Encryption.State,
			Type:    vol.Encryption.Type,
		},
	}, nil
}
