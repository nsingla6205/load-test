package workflows

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	wfutil "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	thinCloneGASupport             = env.GetBool("THIN_CLONE_GA_SUPPORT", false)
	volumeStartToCloseTimeoutSec   = env.GetUint64("VOLUME_ACTIVITIES_START_TO_CLOSE_TIMEOUT_SEC", 600)
	volumeStartToCloseTimeoutSecLV = env.GetUint64("VOLUME_ACTIVITIES_START_TO_CLOSE_TIMEOUT_SEC_LV", 1800) // 30 minutes for large volumes
	volumeHeartbeatTimeoutSec      = env.GetUint64("VOLUME_ACTIVITIES_HEARTBEAT_TIMEOUT_SEC", 300)
	dbHeartbeatTimeoutSec          = env.GetUint64("DATABASE_HEARTBEAT_TIMEOUT_SEC", 30)
	enableKerberos                 = env.GetBool("ENABLE_KERBEROS", false)
	workflowSleep                  = workflow.Sleep // Variable for testing
)

// getVolumeStartToCloseTimeout returns the appropriate start-to-close timeout based on volume characteristics.
// For large capacity volumes, it returns the LV-specific timeout (30 minutes by default).
// For regular volumes, it returns the standard timeout (10 minutes by default).
func getVolumeStartToCloseTimeout(volume *datamodel.Volume) uint64 {
	if volume != nil && volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeCapacity {
		return volumeStartToCloseTimeoutSecLV
	}
	return volumeStartToCloseTimeoutSec
}

type volumeCreateWorkflow struct {
	BaseWorkflow
}

// Volume provisioning phases
const (
	PhasePre  = "pre"  // Pre-provisioning phase
	PhasePost = "post" // Post-provisioning phase
)

const (
	CancelVolumeSignalName = "cancel-volume-creation"
)

// Enforcing the WorkflowInterface on volumeCreateWorkflow
var _ WorkflowInterface = &volumeCreateWorkflow{}

// PreVolumeProvisioningParams encapsulates parameters for pre-provisioning hooks
type PreVolumeProvisioningParams struct {
	Ctx      workflow.Context
	DBVolume *datamodel.Volume
	Node     *models.Node
}

// PostVolumeProvisioningParams encapsulates parameters for post-provisioning hooks
type PostVolumeProvisioningParams struct {
	Ctx                 workflow.Context
	DBVolume            *datamodel.Volume
	Node                *models.Node
	VolCreateResponse   *vsa.VolumeResponse
	IsRestoreFromBackup bool
	IsRestoreSnapshot   bool
}

// selectVolumeChildWorkflow selects the appropriate child workflow based on volume characteristics.
// Currently implements protocol-based selection (ISCSI for block, NFSv3/NFSv4/SMB for file protocols).
// This function is designed to be extensible for future volume attributes beyond protocols
// (e.g. performance tier, large volume, etc.).
//
// Parameters:
//   - protocols: Slice of protocol strings to determine workflow type
//   - phase: Provisioning phase (use PhasePre or PhasePost constants)
//   - ontapVersion: ONTAP version string to check file protocol support
//
// Returns:
//   - For post phase with both NFS and SMB protocols: returns a slice of workflows [PostFileVolumeWorkflow, PostFileVolumeWorkflowForSMB]
//   - For all other cases: returns a single workflow function
func selectVolumeChildWorkflow(protocols []string, phase, ontapVersion string) (interface{}, error) {
	if utils.IsSanProtocols(protocols) {
		switch phase {
		case PhasePre:
			return PreBlockVolumeWorkflow, nil
		case PhasePost:
			return PostBlockVolumeWorkflow, nil
		default:
			return nil, ConvertToVSAError(fmt.Errorf("invalid phase: %s", phase))
		}
	}
	if utils.IsNasProtocols(protocols) {
		if !utils.IsFileProtocolSupportedV2(ontapVersion) {
			return nil, ConvertToVSAError(fmt.Errorf("file protocols are not enabled"))
		}
		switch phase {
		case PhasePre:
			return PreFileVolumeWorkflow, nil
		case PhasePost:
			hasNFS := utils.IsNFSProtocols(protocols)
			hasSMB := utils.IsSMBProtocols(protocols) && enableSmb

			// If both SMB and NFS are present, return both workflows
			if hasNFS && hasSMB {
				return []interface{}{PostFileVolumeWorkflowForSMB, PostFileVolumeWorkflow}, nil
			}
			// If only SMB is present and enabled, return SMB-specific workflow
			if hasSMB {
				return PostFileVolumeWorkflowForSMB, nil
			}
			// Otherwise, return the general file workflow (for NFS-only or when SMB is disabled)
			return PostFileVolumeWorkflow, nil
		default:
			return nil, ConvertToVSAError(fmt.Errorf("invalid phase: %s", phase))
		}
	}
	return nil, ConvertToVSAError(fmt.Errorf("unsupported or unspecified protocol: %v", protocols))
}

// PreBlockVolumeWorkflow handles pre-provisioning for block volumes
func PreBlockVolumeWorkflow(ctx workflow.Context, dbVolume *datamodel.Volume, node *models.Node) (*datamodel.Volume, error) {
	// Additional pre-provisioning steps for block volumes if needed
	return dbVolume, nil
}

// PostBlockVolumeWorkflow handles post-provisioning for block volumes
func PostBlockVolumeWorkflow(ctx workflow.Context, dbVolume *datamodel.Volume, node *models.Node, hostParams []*common.HostParams, volCreateResponse *vsa.VolumeResponse, isRestoreFromBackup bool, isRestoreSnapshot bool, restoreVolCreateResponse *vsa.VolumeResponse) (*datamodel.Volume, error) {
	volumeActivity := &activities.VolumeCreateActivity{}
	var err error

	// Configure activity options for child workflow
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Create LUN for block volume
	var lun *vsa.LunResponse

	if isRestoreSnapshot {
		err = workflow.ExecuteActivity(ctx, volumeActivity.UpdateLunName, &dbVolume, &node, restoreVolCreateResponse).Get(ctx, &lun)
		if err != nil {
			return nil, err
		}
	} else if isRestoreFromBackup {
		err = workflow.ExecuteActivity(ctx, volumeActivity.UpdateLunName, &dbVolume, &node, volCreateResponse).Get(ctx, &lun)
		if err != nil {
			return nil, err
		}
	} else {
		err = workflow.ExecuteActivity(ctx, volumeActivity.CreateLun, &dbVolume, &node, volCreateResponse.AvailableSpace).Get(ctx, &lun)
		if err != nil {
			return nil, err
		}
	}

	// Create LUN map for block volume
	lunMapParams := createLunMapParams(lun.Name, dbVolume.Svm.Name, hostParams)
	err = workflow.ExecuteActivity(ctx, volumeActivity.CreateLunMap, &dbVolume, &lunMapParams, node).Get(ctx, nil)
	if err != nil {
		return nil, err
	}

	if dbVolume.VolumeAttributes.BlockDevices != nil && len(*dbVolume.VolumeAttributes.BlockDevices) > 0 {
		blockDevices := *dbVolume.VolumeAttributes.BlockDevices
		for i := range blockDevices {
			blockDevices[i].Name = utils.ExtractLunNameFromPath(lun.Name)
			blockDevices[i].Identifier = lun.SerialNumber
			blockDevices[i].Size = lun.Size
			blockDevices[i].LunUUID = lun.ExternalUUID
		}
		// Update the slice back to the volume attributes
		dbVolume.VolumeAttributes.BlockDevices = &blockDevices
	} else if dbVolume.VolumeAttributes.BlockProperties != nil {
		dbVolume.VolumeAttributes.BlockProperties.LunName = utils.ExtractLunNameFromPath(lun.Name)
		dbVolume.VolumeAttributes.BlockProperties.LunSerialNumber = lun.SerialNumber
		dbVolume.VolumeAttributes.BlockProperties.LunUUID = lun.ExternalUUID
	}

	return dbVolume, nil
}

// PreFileVolumeWorkflow handles pre-provisioning for file volumes
func PreFileVolumeWorkflow(ctx workflow.Context, dbVolume *datamodel.Volume, node *models.Node) (*datamodel.Volume, error) {
	// Configure activity options for child workflow
	volumeActivity := &activities.VolumeCreateActivity{}
	volumeDeleteActivity := &activities.VolumeDeleteActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	log := workflow.GetLogger(ctx)

	var isOntapClusterHealthy *bool
	err = workflow.ExecuteActivity(ctx, volumeActivity.GetOntapClusterHealth, node).Get(ctx, &isOntapClusterHealthy)
	if err != nil {
		log.Error("failed to check ONTAP cluster health", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOntapClusterHealthCheckFailed, fmt.Errorf("failed to check ONTAP cluster health")))
	}

	if !*isOntapClusterHealthy {
		// Delete Volume in DB when the ONTAP Health check fails
		err := workflow.ExecuteActivity(ctx, volumeDeleteActivity.DeleteVolume, dbVolume).Get(ctx, nil)
		if err != nil {
			log.Error("Failed to delete volume", "error", err)
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDeleteVolume, fmt.Errorf("failed to delete volume")))
		}
		log.Error("ONTAP cluster is not available. Cluster is down.")
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOntapClusterNotAvailable, fmt.Errorf("ONTAP cluster is down")))
	}

	// Check if NAS LIF needs to be configured
	poolActivity := &activities.PoolActivity{}
	svmActivity := &activities.SvmActivity{}
	var vlmConfig *vlm.VLMConfig
	err = workflow.ExecuteActivity(ctx, poolActivity.ParseVlmConfig, dbVolume.Pool).Get(ctx, &vlmConfig)
	if err != nil {
		log.Error("Failed to parse VLM config for NAS LIF check", "error", err)
		return nil, WrapErrorForChildWorkflow(err)
	}

	// Check if pool details are loaded before proceeding
	if dbVolume.Pool == nil {
		err = fmt.Errorf("pool details not loaded for volume %s", dbVolume.UUID)
		log.Error("Pool details missing during NAS LIF check", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, err))
	}

	ontapVersion := activities.GetOntapVersionFromPool(dbVolume.Pool)

	// Check file support conditions: FileProtocolSupported flag + known ONTAP version
	if utils.IsFileProtocolSupportedV2(ExtractOntapVersion(ontapVersion)) {
		// Check if naslif exists in VLMConfig using activity
		var hasNasLif bool
		err = workflow.ExecuteActivity(ctx, poolActivity.HasNasLifInVLMConfig, *vlmConfig).Get(ctx, &hasNasLif)
		if err != nil {
			log.Error("Failed to check for NAS LIF in VLM config", "error", err)
			return nil, WrapErrorForChildWorkflow(err)
		}
		if !hasNasLif {
			log.Info("NAS LIF not found in VLMConfig, setting up NAS infrastructure", "pool", dbVolume.Pool.Name, "svm", dbVolume.Svm.Name)

			// Setup NAS firewalls (NFS, SMB, ILB health check) first, before configuring NAS LIF.
			// This is needed for pools upgraded from 9.17 to 9.18 where firewalls were not set up during pool creation.
			// The firewall setup is idempotent, so it's safe to call even if firewalls already exist.
			poolClusterDetails := dbVolume.Pool.ClusterDetails
			if poolClusterDetails.SnHostProject == "" || poolClusterDetails.Network == "" {
				log.Warn("Pool missing SN host project or network details, skipping NAS firewall setup", "pool", dbVolume.Pool.UUID, "snHostProject", poolClusterDetails.SnHostProject, "network", poolClusterDetails.Network)
			} else {
				log.Info("Setting up NAS firewalls for first file volume creation", "pool", dbVolume.Pool.Name, "snHostProject", poolClusterDetails.SnHostProject, "network", poolClusterDetails.Network)
				var firewallOperations *[]common.Operations
				err = workflow.ExecuteActivity(ctx, poolActivity.SetupNasFirewalls, poolClusterDetails.SnHostProject, poolClusterDetails.Network).Get(ctx, &firewallOperations)
				if err != nil {
					log.Error("Failed to setup NAS firewalls", "error", err, "pool", dbVolume.Pool.Name)
					return nil, WrapErrorForChildWorkflow(err)
				}
				// Wait for firewall operations to complete using child workflow
				if firewallOperations != nil && len(*firewallOperations) > 0 {
					// Using REQUEST_CANCEL policy so child workflow is cancelled when parent is cancelled
					firewallChildCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
						ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
					})
					err = workflow.ExecuteChildWorkflow(firewallChildCtx, WaitForGCPNetworkOperationStatusWorkflow, firewallOperations, retryPolicy.StartToCloseTimeout).Get(firewallChildCtx, nil)
					if err != nil {
						log.Error("Failed to wait for NAS firewall operations to complete", "error", err, "pool", dbVolume.Pool.Name)
						return nil, WrapErrorForChildWorkflow(err)
					}
					log.Info("Successfully set up NAS firewalls", "pool", dbVolume.Pool.Name, "operations", len(*firewallOperations))
				} else {
					log.Debug("NAS firewalls already exist, no operations needed", "pool", dbVolume.Pool.Name)
				}
			}

			// Configure NAS LIF by calling ModifyVSASVMWorkflow
			// VLM will handle the actual NAS LIF configuration
			// Get ONTAP credentials from pool
			var ontapCredentials vlm.OntapCredentials
			err = workflow.ExecuteActivity(ctx, poolActivity.GetOnTapCredentials, dbVolume.Pool).Get(ctx, &ontapCredentials)
			if err != nil {
				log.Error("Failed to get ONTAP credentials for ModifyVSASVMWorkflow", "error", err)
				return nil, WrapErrorForChildWorkflow(err)
			}
			// Update VLMConfig with EnableIlbSupport = true
			vlmConfig.Deployment.DevFlags.EnableIlbSupport = true

			modifySVMRequest := &vlm.ModifySVMRequest{
				Name:             dbVolume.Svm.Name,
				VLMConfig:        *vlmConfig,
				OntapCredentials: ontapCredentials,
			}

			vlmClient := vlm.NewVSAClientWorkflowManager()
			var modifySVMResponse *vlm.ModifySVMResponse
			modifySVMResponse, err = vlmClient.ModifyVSASVMWorkflow(ctx, modifySVMRequest)
			if err != nil {
				log.Error("Failed to configure NAS LIF via ModifyVSASVMWorkflow", "error", err)
				return nil, WrapErrorForChildWorkflow(err)
			}

			// Get the updated VLMConfig from the response
			if modifySVMResponse != nil {
				vlmConfig = &modifySVMResponse.VLMConfig
			}

			var savedSvm *datamodel.Svm
			err = workflow.ExecuteActivity(ctx, svmActivity.SaveSVMAndLifData, dbVolume.Pool, vlmConfig, dbVolume.Svm.Name).Get(ctx, &savedSvm)
			if err != nil {
				log.Error("failed to save SVM and LIFs to database", "error", err)
				return nil, WrapErrorForChildWorkflow(err)
			}

			if utils.IsNFSProtocols(dbVolume.VolumeAttributes.Protocols) {
				err = workflow.ExecuteActivity(ctx, svmActivity.UpdateNFSRWSizeOnSVM, savedSvm, node).Get(ctx, nil)
				if err != nil {
					log.Error("failed to update NFS TCP max transfer size on SVM", "error", err)
					return nil, WrapErrorForChildWorkflow(err)
				}
			}

			// Save updated VLMConfig back to pool using UpdatePoolFields
			// Marshal VLMConfig using activity
			var marshalledVlmConfig string
			err = workflow.ExecuteActivity(ctx, poolActivity.MarshalVLMConfig, *vlmConfig).Get(ctx, &marshalledVlmConfig)
			if err != nil {
				log.Error("Failed to marshal VLMConfig", "error", err)
				return nil, WrapErrorForChildWorkflow(err)
			}
			updates := map[string]interface{}{
				"vlm_config": marshalledVlmConfig,
			}

			err = workflow.ExecuteActivity(ctx, poolActivity.UpdatePoolFields, dbVolume.Pool.UUID, updates).Get(ctx, nil)
			if err != nil {
				log.Error("Failed to update pool with VLMConfig after NAS LIF configuration", "error", err)
				return nil, WrapErrorForChildWorkflow(err)
			} else {
				log.Info("Successfully configured NAS LIF and updated pool VLMConfig", "pool", dbVolume.Pool.Name)
			}
		} else {
			log.Debug("NAS LIF already exists in VLMConfig", "pool", dbVolume.Pool.Name)
		}
	} else {
		log.Debug("Skipping NAS LIF configuration due to unsupported file protocol or ONTAP version", "pool", dbVolume.Pool.Name, "ontapVersion", ontapVersion)
		return nil, ConvertToVSAError(fmt.Errorf("file protocols are not supported"))
	}

	err = workflow.ExecuteActivity(ctx, volumeActivity.CreateExportPolicyInOntap, &dbVolume, &node).Get(ctx, nil)
	if err != nil {
		return nil, WrapErrorForChildWorkflow(err)
	}
	log.Info("File pre-provisioning: create export policy, etc. (placeholder)")
	return dbVolume, nil
}

// PostFileVolumeWorkflowForSMB is a Cadence workflow that handles SMB-specific post-provisioning tasks for a volume.
// It configures activity options, retrieves SVM and Active Directory information, and ensures CIFS share creation.
// The workflow also updates Active Directory association if necessary. Returns the updated volume.
func PostFileVolumeWorkflowForSMB(ctx workflow.Context, dbVolume *datamodel.Volume, node *models.Node) (*datamodel.Volume, error) {
	// Configure activity options
	log := workflow.GetLogger(ctx)
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		log.Error("Failed to populate retry policy params during PostFileVolumeWorkflowForSMB", "error", err)
		return nil, WrapErrorForChildWorkflow(err)
	}

	ao := getActivityOptionsForEnsureCIFSShareVolumeActivity(retryPolicy)
	ctx = workflow.WithActivityOptions(ctx, ao)

	var dbSvm *datamodel.Svm
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetSVM, &dbVolume.PoolID).Get(ctx, &dbSvm)
	if err != nil {
		log.Error("Failed to get SVM info during PostFileVolumeWorkflowForSMB", "error", err)
		return nil, WrapErrorForChildWorkflow(err)
	}

	var activeDirectory *vsa.ActiveDirectory
	err = workflow.ExecuteActivity(ctx, active_directory_activities.ActiveDirectoryActivity.GetActiveDirectoryForPool, &dbVolume.PoolID).Get(ctx, &activeDirectory)
	if err != nil {
		log.Error("Failed to get active directory during PostFileVolumeWorkflowForSMB", "error", err)
		return nil, WrapErrorForChildWorkflow(err)
	}

	if dbVolume.Pool == nil {
		err = fmt.Errorf("pool details not loaded for volume %s", dbVolume.UUID)
		log.Error("Pool details missing during PostFileVolumeWorkflowForSMB", "error", err)
		return nil, WrapErrorForChildWorkflow(err)
	}

	poolClusterDetails := dbVolume.Pool.ClusterDetails
	if poolClusterDetails.SnHostProject == "" || poolClusterDetails.Network == "" {
		err = fmt.Errorf("pool %s missing SN host project or network details", dbVolume.Pool.UUID)
		log.Error("Pool network metadata missing during PostFileVolumeWorkflowForSMB", "error", err)
		return nil, WrapErrorForChildWorkflow(err)
	}

	var fqdn string
	// Use the new workflow instead of the single activity
	fqdn, err = EnsureCIFSShareWorkflow(ctx, dbVolume, node, activeDirectory, dbSvm.Name, dbSvm.SvmDetails.ExternalUUID)
	if err != nil {
		log.Error("Failed to create cifs share during PostFileVolumeWorkflowForSMB", "error", err)
		return nil, WrapErrorForChildWorkflow(err)
	}
	if fqdn != "" && dbVolume.VolumeAttributes != nil && dbVolume.VolumeAttributes.FileProperties != nil {
		log.Info("Setting the FQDN for volume", "fqdn", fqdn, "volume", dbVolume.Name)
		dbVolume.VolumeAttributes.FileProperties.Fqdn = fqdn
	}

	if !dbSvm.ActiveDirectoryID.Valid {
		var updatedSvm *datamodel.Svm
		params := activities.UpdateSvmActiveDirectoryParams{Svm: dbSvm, ActiveDirectoryUUID: activeDirectory.UUID}
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.UpdateSvmActiveDirectory, params).Get(ctx, &updatedSvm)
		if err != nil {
			log.Error("Failed to update SVM Active Directory association during PostFileVolumeWorkflowForSMB", "error", err)
			return nil, WrapErrorForChildWorkflow(err)
		}
		dbSvm = updatedSvm
	}

	if err := updateActiveDirectoryStateToInUse(ctx, activeDirectory); err != nil {
		return nil, err
	}

	log.Info("SMB post-provisioning: created CIFS share", "volume", dbVolume.Name)
	return dbVolume, nil
}

// EnsureCIFSShareWorkflow orchestrates the creation of CIFS share through individual activities
func EnsureCIFSShareWorkflow(ctx workflow.Context, volume *datamodel.Volume, node *models.Node, activeDirectory *vsa.ActiveDirectory, svmName, externalSVMUUID string) (string, error) {
	log := workflow.GetLogger(ctx)

	// Validate inputs
	if volume.VolumeAttributes == nil || volume.VolumeAttributes.FileProperties == nil {
		log.Warn("Skipping CIFS share creation for non-file volume", "name", volume.Name)
		return "", nil
	}

	activeDirectoryActivity := newActiveDirectoryActivity()
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return "", err
	}

	// Set activity options
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Step 1: Create or modify AD DNS
	log.Info("Step 1: Creating or modifying AD DNS")
	err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.CreateOrModifyADDNS, &node, &activeDirectory, svmName, externalSVMUUID).Get(ctx, nil)
	if err != nil {
		log.Error("Failed to create or modify AD DNS", "error", err)
		return "", ConvertToVSAError(err)
	}

	// Step 2: Get or create CIFS service
	log.Info("Step 2: Getting or creating CIFS service")
	var cifsServiceResult *active_directory_activities.GetOrCreateCifsServiceResult
	err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.GetOrCreateCifsService, &node, &activeDirectory, svmName, externalSVMUUID).Get(ctx, &cifsServiceResult)
	if err != nil {
		log.Error("Failed to get or create CIFS service", "error", err)
		return "", ConvertToVSAError(err)
	}

	var fqdn string
	if cifsServiceResult.FQDN != "" {
		// Service was created, FQDN is already set
		fqdn = cifsServiceResult.FQDN
		log.Info("CIFS service created with FQDN", "fqdn", fqdn)
	} else {
		// Service already existed, check if DDNS needs to be enabled
		if cifsServiceResult.NeedsDDNS {
			// Step 3: Enable DDNS
			log.Info("Step 3: Enabling DDNS for existing CIFS service")
			fqdn = cifsServiceResult.CifsServiceName + "." + cifsServiceResult.AdDomain
			err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.DdnsModify, &node, externalSVMUUID, fqdn).Get(ctx, nil)
			if err != nil {
				log.Error("Failed to enable DDNS", "error", err, "fqdn", fqdn)
				return "", ConvertToVSAError(err)
			}
			log.Info("DDNS enabled", "fqdn", fqdn)
		} else {
			// DDNS already enabled or not needed, build FQDN if we have the info
			if cifsServiceResult.CifsServiceName != "" && cifsServiceResult.AdDomain != "" {
				fqdn = cifsServiceResult.CifsServiceName + "." + cifsServiceResult.AdDomain
			}
			log.Info("DDNS already enabled or not needed", "fqdn", fqdn)
		}
	}

	// Step 4: Create junction path for CIFS share
	if utils.IsSMBProtocols(volume.VolumeAttributes.Protocols) && !volume.VolumeAttributes.IsDataProtection {
		log.Info("Step 4: Creating junction path for CIFS share", "junctionPath", volume.VolumeAttributes.FileProperties.JunctionPath)
		err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.CreateJunctionPathForCifsShare, &node, svmName, volume.VolumeAttributes.FileProperties.JunctionPath, volume.VolumeAttributes.FileProperties.SMBShareSettings).Get(ctx, nil)
		if err != nil {
			log.Error("Failed to create junction path for CIFS share", "error", err)
			return "", ConvertToVSAError(err)
		}
	}

	log.Info("CIFS share creation completed successfully", "fqdn", fqdn)
	return fqdn, nil
}

var (
	getActivityOptionsForEnsureCIFSShareVolumeActivity = _getActivityOptionsForEnsureCIFSShareVolumeActivity
	newActiveDirectoryActivity                         = func() *active_directory_activities.ActiveDirectoryActivity {
		return &active_directory_activities.ActiveDirectoryActivity{}
	}
)

func _getActivityOptionsForEnsureCIFSShareVolumeActivity(retryPolicy *WorkflowRetryPolicy) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			MaximumInterval:        60 * time.Second,
			MaximumAttempts:        int32(10),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
}

// PostFileVolumeWorkflow handles post-provisioning for file volumes
func PostFileVolumeWorkflow(ctx workflow.Context, dbVolume *datamodel.Volume, node *models.Node) (*datamodel.Volume, error) {
	log := workflow.GetLogger(ctx)
	// Configure activity options for child workflow
	volumeActivity := &activities.VolumeCreateActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, WrapErrorForChildWorkflow(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Check if Kerberos needs to be configured for NFSv4 volumes
	hasNFSv4 := false
	if dbVolume.VolumeAttributes != nil {
		for _, protocol := range dbVolume.VolumeAttributes.Protocols {
			if protocol == utils.ProtocolNFSv4 {
				hasNFSv4 = true
				break
			}
		}
	}
	hasKerberosFlags := false
	if dbVolume.VolumeAttributes != nil && dbVolume.VolumeAttributes.FileProperties != nil &&
		dbVolume.VolumeAttributes.FileProperties.ExportPolicy != nil &&
		dbVolume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules != nil {
		for _, rule := range dbVolume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules {
			if rule.Kerberos5ReadOnly || rule.Kerberos5ReadWrite || rule.Kerberos5iReadOnly ||
				rule.Kerberos5iReadWrite || rule.Kerberos5pReadOnly || rule.Kerberos5pReadWrite {
				hasKerberosFlags = true
				break
			}
		}
	}

	if enableKerberos && hasNFSv4 && hasKerberosFlags {
		log.Info("Configuring Kerberos for NFSv4 volume", "volume", dbVolume.Name)
		if dbVolume.Pool == nil {
			err = fmt.Errorf("pool details not loaded for volume %s", dbVolume.UUID)
			log.Error("Pool details missing during PostFileVolumeWorkflow", "error", err)
			return nil, WrapErrorForChildWorkflow(vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, err))
		}

		var dbSvm *datamodel.Svm
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetSVM, &dbVolume.PoolID).Get(ctx, &dbSvm)
		if err != nil {
			log.Error("Failed to get SVM info during PostFileVolumeWorkflow", "error", err)
			return nil, WrapErrorForChildWorkflow(err)
		}

		var activeDirectory *vsa.ActiveDirectory
		err = workflow.ExecuteActivity(ctx, active_directory_activities.ActiveDirectoryActivity.GetActiveDirectoryForPool, &dbVolume.PoolID).Get(ctx, &activeDirectory)
		if err != nil {
			log.Error("Failed to get active directory during PostFileVolumeWorkflow", "error", err)
			return nil, WrapErrorForChildWorkflow(err)
		}

		// Configure Kerberos for NFSv4 using workflow
		// Using REQUEST_CANCEL policy so child workflow is cancelled when parent is cancelled
		kerberosChildCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		})
		err = workflow.ExecuteChildWorkflow(kerberosChildCtx, EnsureKerberosConfigWorkflow, node, activeDirectory, dbSvm.Name, dbSvm.SvmDetails.ExternalUUID).Get(kerberosChildCtx, nil)
		if err != nil {
			log.Error("Failed to configure Kerberos during PostFileVolumeWorkflow", "error", err)
			return nil, WrapErrorForChildWorkflow(err)
		}
		log.Info("Successfully configured Kerberos for NFSv4 volume", "volume", dbVolume.Name)
	}

	if enableLdap {
		if dbVolume.Pool == nil {
			err = fmt.Errorf("pool details not loaded for volume %s", dbVolume.UUID)
			log.Error("Pool details missing during PostFileVolumeWorkflow", "error", err)
			return nil, WrapErrorForChildWorkflow(vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, err))
		}

		ldapEnabled := false
		if dbVolume.Pool.PoolAttributes != nil {
			ldapEnabled = dbVolume.Pool.PoolAttributes.LdapEnabled
		}

		if ldapEnabled {
			var dbSvm *datamodel.Svm
			err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetSVM, &dbVolume.PoolID).Get(ctx, &dbSvm)
			if err != nil {
				log.Error("Failed to get SVM info during PostFileVolumeWorkflow", "error", err)
				return nil, WrapErrorForChildWorkflow(err)
			}

			var activeDirectory *vsa.ActiveDirectory
			err = workflow.ExecuteActivity(ctx, active_directory_activities.ActiveDirectoryActivity.GetActiveDirectoryForPool, &dbVolume.PoolID).Get(ctx, &activeDirectory)
			if err != nil {
				log.Error("Failed to get active directory during PostFileVolumeWorkflow", "error", err)
				return nil, WrapErrorForChildWorkflow(err)
			}

			fqdn, err := EnsureCIFSShareWorkflow(ctx, dbVolume, node, activeDirectory, dbSvm.Name, dbSvm.SvmDetails.ExternalUUID)
			if err != nil {
				log.Error("Failed to create cifs share during PostFileVolumeWorkflow", "error", err)
				return nil, WrapErrorForChildWorkflow(err)
			}

			err = workflow.ExecuteActivity(ctx, volumeActivity.ConfigureLdap, dbVolume, node).Get(ctx, nil)
			if err != nil {
				log.Error("Failed to configure Ldap", "error", err)
				return nil, WrapErrorForChildWorkflow(err)
			}

			if fqdn != "" && dbVolume.VolumeAttributes != nil && dbVolume.VolumeAttributes.FileProperties != nil {
				dbVolume.VolumeAttributes.FileProperties.Fqdn = fqdn
			}

			if !dbSvm.ActiveDirectoryID.Valid {
				var updatedSvm *datamodel.Svm
				params := activities.UpdateSvmActiveDirectoryParams{Svm: dbSvm, ActiveDirectoryUUID: activeDirectory.UUID}
				err = workflow.ExecuteActivity(ctx, activities.CommonActivities.UpdateSvmActiveDirectory, params).Get(ctx, &updatedSvm)
				if err != nil {
					log.Error("Failed to update SVM Active Directory association during PostFileVolumeWorkflow", "error", err)
					return nil, WrapErrorForChildWorkflow(err)
				}
				dbSvm = updatedSvm
			}

			if err := updateActiveDirectoryStateToInUse(ctx, activeDirectory); err != nil {
				return nil, err
			}
		}
	}
	log.Info("File post-provisioning: anything after volume create. (placeholder)")
	return dbVolume, nil
}

// CreateVolumeWorkflow processes a volume create request. New callers pass only the volume UUID and
// the workflow fetches the full volume in its first activity so the sequencer's ContinueAsNew payload
// does not include the large Pool.VLMConfig blob. The *datamodel.Volume input remains temporarily
// accepted to allow existing executions to replay while they drain.
func CreateVolumeWorkflow(ctx workflow.Context, params *common.CreateVolumeParams, volumeReference interface{}) (gcpgenserver.V1betaDescribeVolumeRes, error) {
	// customerID is known before the volume is loaded. volumeUUID is added
	// after the fetch below. The returned context must be passed onward;
	// a reassignment inside Setup does not escape that function.
	ctx = wfutil.AddExtraLoggerFields(ctx, map[string]interface{}{
		"customerID": params.AccountName,
	})
	log := workflow.GetLogger(ctx)
	volumeWf := new(volumeCreateWorkflow)
	err := volumeWf.Setup(ctx, params)
	if err != nil {
		log.Error("Failed to setup CreateVolumeWorkflow", "error", err)
		return nil, err
	}

	var volume *datamodel.Volume
	switch input := volumeReference.(type) {
	case string:
		// The LV-specific timeout cannot be derived until the volume is loaded. Run() applies the
		// appropriate LV-aware ActivityOptions after this standard database fetch completes.
		fetchAO := workflow.ActivityOptions{
			StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
			HeartbeatTimeout:    time.Duration(dbHeartbeatTimeoutSec) * time.Second,
		}
		fetchCtx := workflow.WithActivityOptions(ctx, fetchAO)

		volumeActivity := &activities.VolumeCreateActivity{}
		if err = workflow.ExecuteActivity(fetchCtx, volumeActivity.GetVolumeByVolumeID, input).Get(fetchCtx, &volume); err != nil {
			log.Error("Failed to fetch volume for CreateVolumeWorkflow", "input", input, "error", err)
			return nil, err
		}
	case *datamodel.Volume:
		volume = input
	case map[string]interface{}:
		// Temporal decodes a previously serialized concrete volume as a map when the
		// workflow argument type is interface{}. Preserve compatibility while queued
		// pre-change signals drain.
		volumeJSON, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal legacy CreateVolumeWorkflow volume input: %w", marshalErr)
		}
		volume = &datamodel.Volume{}
		if unmarshalErr := json.Unmarshal(volumeJSON, volume); unmarshalErr != nil {
			return nil, fmt.Errorf("unmarshal legacy CreateVolumeWorkflow volume input: %w", unmarshalErr)
		}
	default:
		return nil, fmt.Errorf("invalid CreateVolumeWorkflow volume input type %T", volumeReference)
	}

	ctx = wfutil.AddExtraLoggerFields(ctx, map[string]interface{}{
		"volumeUUID": volume.UUID,
	})
	return runVolumeCreate(ctx, volumeWf, params, volume)
}

// runVolumeCreate drives the job-status lifecycle around volumeCreateWorkflow.Run.
func runVolumeCreate(ctx workflow.Context, volumeWf *volumeCreateWorkflow, params *common.CreateVolumeParams, volume *datamodel.Volume) (gcpgenserver.V1betaDescribeVolumeRes, error) {
	log := workflow.GetLogger(ctx)
	if err := volumeWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, err
	}

	volumeWf.Status = WorkflowStatusRunning
	err := volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Error("Failed to update job status to Processing for CreateVolumeWorkflow", "error", err)
		return nil, err
	}
	_, customErr := volumeWf.Run(ctx, volume, params)
	if customErr != nil {
		log.Error("CreateVolumeWorkflow completed with error", "error", customErr)
		volumeWf.Status = WorkflowStatusFailed
		err2 := volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Error("Failed to update job status to Done with error for CreateVolumeWorkflow", "error", err2)
			return nil, err2
		}
		return nil, customErr
	}
	volumeWf.Status = WorkflowStatusCompleted
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Error("Failed to update job status to Done for CreateVolumeWorkflow", "error", err)
	}
	return nil, err
}

func (wf *volumeCreateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createPoolParams := input.(*common.CreateVolumeParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createPoolParams.AccountName
	wf.Status = WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *volumeCreateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	dbVolume := args[0].(*datamodel.Volume)
	createVolumeParams := args[1].(*common.CreateVolumeParams)
	region := createVolumeParams.Region
	var snapshot *datamodel.Snapshot
	if createVolumeParams.Snapshot != nil {
		snapshot = createVolumeParams.Snapshot
	}
	isRestoreSnapshot := createVolumeParams.SnapshotID != "" && snapshot != nil
	isRestoreFromBackup := createVolumeParams.BackupPath != ""
	volumeActivity := &activities.VolumeCreateActivity{}
	volumeDeleteActivity := &activities.VolumeDeleteActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	// Use LV-specific timeout for large capacity volumes
	startToCloseTimeout := getVolumeStartToCloseTimeout(dbVolume)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(startToCloseTimeout) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	dbHbCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)

	rollbackManager := common.NewRollbackManager()

	// Set up cancellation handler using common framework
	cancellationHandler := common.NewWorkflowCancellationHandler(ctx, CancelVolumeSignalName, dbVolume.UUID, "volume")

	defer func() {
		common.ExecuteDeferredCleanup(ctx, cancellationHandler, rollbackManager, err, log, "volume", dbVolume.UUID,
			func(disconnectedCtx workflow.Context) error {
				var volume *datamodel.Volume
				err := workflow.ExecuteActivity(disconnectedCtx, volumeActivity.GetVolumeByVolumeID, dbVolume.UUID).Get(disconnectedCtx, &volume)
				if err != nil {
					log.Error("Failed to describe volume", "uuid", dbVolume.UUID, "error", err)
				}
				// Update volume state in DB before rollback
				if volume != nil && volume.State != datamodel.LifeCycleStateDeleted {
					err2 := workflow.ExecuteActivity(disconnectedCtx, volumeActivity.UpdateVolumeStateInDB, dbVolume.UUID, datamodel.LifeCycleStateError, datamodel.LifeCycleStateCreationErrorDetails).Get(disconnectedCtx, nil)
					if err2 != nil {
						log.Error("Failed to update volume state in DB to error", "error", err2)
						return err2
					}
				}
				return nil
			},
			nil, // onCancellationCallback
			nil) // shouldRollbackOnError
	}()

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	// Validate pool state: do not proceed if pool is in Creating, Deleting, or Deleted
	if dbVolume.Pool != nil {
		err = workflow.ExecuteActivity(dbHbCtx, volumeActivity.ValidatePoolStateForVolumeCreate, dbVolume.Pool, dbVolume.UUID).Get(dbHbCtx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// Fail fast if pool KMS config is not reachable, matching pool create workflow behavior.
	// This ensures key disabled / EKM unreachable errors surface early.
	if dbVolume.Pool != nil && dbVolume.Pool.KmsConfig != nil {
		kmsConfigActivity := &kms_activities.KmsConfigActivity{}
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		// Access a crypto key using the KMS config in the VSA database to make sure key is reachable and update the kms config state based on the reachability
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.VerifyVsaKmsReachabilityActivity, dbVolume.Pool.KmsConfig.UUID, true).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	var backupVault *datamodel.BackupVault
	var backup *datamodel.Backup

	if isRestoreFromBackup {
		log.Info("Fetching backup metadata from CVP/SDE for backup path", "backupPath", createVolumeParams.BackupPath)

		// Get authentication token for CVP API calls
		if !env.IsLocalEnv() {
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			var token string
			err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetAuthJWTToken, createVolumeParams.AccountName).Get(ctx, &token)
			if err != nil {
				log.Error("Failed to get token for account", "accountName", createVolumeParams.AccountName, "error", err)
				return nil, ConvertToVSAError(err)
			}
			ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, token)
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		// FetchBackupVaultMetadataForRestore activity fetches backup vault metadata for restore
		err = workflow.ExecuteActivity(ctx, volumeActivity.FetchBackupVaultMetadataForRestore, createVolumeParams.BackupPath, dbVolume, region).Get(ctx, &backupVault)
		if err != nil {
			log.Error("Failed to fetch backup vault metadata for restore", "error", err)
			return nil, ConvertToVSAError(err)
		}

		if backupVault == nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to fetch backup vault metadata: received nil response"))
		}

		// FetchBackupMetadataForRestore activity fetches backup metadata for restore
		err = workflow.ExecuteActivity(ctx, volumeActivity.FetchBackupMetadataForRestore, createVolumeParams.BackupPath, backupVault, dbVolume, region).Get(ctx, &backup)
		if err != nil {
			log.Error("Failed to fetch backup metadata for restore", "error", err)
			return nil, ConvertToVSAError(err)
		}

		if backup == nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to fetch backup metadata: received nil response"))
		}

		log.Info("Successfully fetched backup metadata", "name", backup.Name)

		// Validate backup is in available or ready state before proceeding with restore
		// SDE backups use READY state, VCP backups use AVAILABLE state
		log.Info("Validating backup state for restore", "name", backup.Name, "state", backup.State)
		if backup.State != datamodel.LifeCycleStateAvailable && backup.State != datamodel.LifeCycleStateREADY {
			err = fmt.Errorf("cannot restore from backup '%s' which is not in available or ready state (current state: %s)",
				backup.Name, backup.State)
			log.Error("Backup state validation failed", "error", err)
			return nil, ConvertToVSAError(err)
		}

		// FetchBucketMetadataForRestore activity fetches bucket metadata and returns updated vault
		err = workflow.ExecuteActivity(ctx, volumeActivity.FetchBucketMetadataForRestore, backup, backupVault).Get(ctx, &backupVault)
		if err != nil {
			log.Error("Failed to fetch bucket metadata for restore", "error", err)
			return nil, ConvertToVSAError(err)
		}

		// Update backup.BackupVault to reference the updated backupVault with populated bucket details
		backup.BackupVault = backupVault
		log.Info("Backup with all required metadata fetched successfully", "backup", backup, "backupVault", backupVault)

		// Validate volume size against backup size
		dbVolume.VolumeAttributes.RestoredBackupID = backup.UUID
		var backupAttributes datamodel.BackupAttributes
		if backup.Attributes != nil {
			backupAttributes = *backup.Attributes
		}
		requiredVolumeSize := utils.CalculateRequiredVolumeSize(backup.SizeInBytes, backupAttributes)
		if dbVolume.SizeInBytes < requiredVolumeSize {
			errmsg := fmt.Sprintf("restored volume size should be greater than or equal to the logical size of the backup: %d bytes", requiredVolumeSize)
			log.Error("restore failed", "error", errmsg)
			err = fmt.Errorf("%s", errmsg)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrInsufficientRestoreVolumeSize, err)
		}

		// Verify backup restore protocol compatibility
		log.Info("Verifying backup restore protocol compatibility")
		err = verifyBackupRestoreCompatibilityForVolumes(backup, createVolumeParams)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Verify backup restore compatibility for large volumes
		if dbVolume.LargeVolumeAttributes != nil && dbVolume.LargeVolumeAttributes.LargeCapacity {
			log.Info("Verifying backup restore compatibility for large volume")
			createVolumeParams, err = _verifyBackupRestoreCompatibilityForLargeVolumes(backup, createVolumeParams)
			if err != nil {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrLargeVolumeBackupRestoreValidation, err)
			}
		}
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &dbVolume.Pool.ID).Get(ctx, &dbNodes)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   dbVolume.Pool.DeploymentName,
		OntapCredentials: dbVolume.Pool.PoolCredentials,
	})

	if createVolumeParams.LargeVolumeConstituentCount > 0 {
		dbVolume.LargeVolumeAttributes.LargeVolumeConstituentCount = &createVolumeParams.LargeVolumeConstituentCount
		err = workflow.ExecuteActivity(dbHbCtx, volumeActivity.UpdateVolumeLargeConstituentInDB, dbVolume.UUID, dbVolume.LargeVolumeAttributes).Get(dbHbCtx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// Get Aggregates from ONTAP if the volume is large capacity
	var aggregateResult *models.AggregateDistributionResult
	if dbVolume.LargeVolumeAttributes != nil && dbVolume.LargeVolumeAttributes.LargeCapacity && dbVolume.LargeVolumeAttributes.LargeVolumeConstituentCount != nil {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(ctx, volumeActivity.GetAggregatesFromOntap, &dbVolume, &node, len(dbNodes)).Get(ctx, &aggregateResult)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	// Pre-provisioning child workflow
	ontapVersion := activities.GetOntapVersionFromPool(dbVolume.Pool)
	preWorkflowFunc, preErr := selectVolumeChildWorkflow(dbVolume.VolumeAttributes.Protocols, PhasePre, ontapVersion)
	if preErr != nil {
		err = preErr
		return nil, ConvertToVSAError(err)
	}
	preChildCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
	})
	var preUpdatedVolume *datamodel.Volume
	err = workflow.ExecuteChildWorkflow(preChildCtx, preWorkflowFunc, dbVolume, node).Get(preChildCtx, &preUpdatedVolume)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Update the dbVolume with any changes from the pre-workflow
	if preUpdatedVolume != nil {
		dbVolume = preUpdatedVolume
	}
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	rollbackManager.AddActivity(volumeDeleteActivity.DeleteSnapshotPolicyInONTAP, getSnapshotPolicyName(dbVolume), &node) // This will delete the snapshotPolicy if exists
	err = workflow.ExecuteActivity(ctx, volumeActivity.CreateSnapshotPolicyInONTAP, &dbVolume, &node).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Create VPG and QoS policy if throughputMibps was provided
	var createdVPG *datamodel.VolumePerformanceGroup
	if createVolumeParams.ThroughputMibps != nil {
		log.Info("Creating VPG and QoS policy for volume", "name", dbVolume.Name)
		vpgActivity := &activities.VolumePerformanceGroupActivity{}
		if createVolumeParams.Iops == nil {
			err = fmt.Errorf("missing auto-generated VPG details for volume %s", dbVolume.Name)
			return nil, ConvertToVSAError(err)
		}

		// Step 1: Create QoS policy in ONTAP first to get the QPG UUID
		var qosPolicyID string
		vpgName := fmt.Sprintf("autoGenerated-%s-%s", dbVolume.Name, dbVolume.UUID)
		vpg := &datamodel.VolumePerformanceGroup{
			Name:            vpgName,
			PoolID:          dbVolume.Pool.ID,
			ThroughputMibps: *createVolumeParams.ThroughputMibps,
			Iops:            *createVolumeParams.Iops,
			AllocationType:  models.AllocationTypeShared,
			IsAutoGen:       true,
		}
		err = workflow.ExecuteActivity(ctx, vpgActivity.CreateQoSPolicyInONTAP, vpg, node).Get(ctx, &qosPolicyID)
		if err != nil {
			log.Error("Failed to create QoS policy in ONTAP", "error", err)
			return nil, ConvertToVSAError(err)
		}
		rollbackManager.AddActivity(vpgActivity.DeleteQoSPolicyInONTAP, qosPolicyID, "", dbVolume.Pool.ID, &node)

		// Step 2: Set the QPG UUID on the VPG object before creating it in DB
		vpg.OntapQosPolicyID = qosPolicyID

		// Step 3: Create VPG in DB with QPG UUID already set
		err = workflow.ExecuteActivity(ctx, vpgActivity.CreateVPGInDB, vpg).Get(ctx, &createdVPG)
		if err != nil {
			log.Error("Failed to create VPG in DB", "error", err)
			return nil, ConvertToVSAError(err)
		}
		rollbackManager.AddActivity(volumeDeleteActivity.CleanupAutoGeneratedVPG, createdVPG.ID, &node)

		// Update volume with VPG ID
		dbVolume.VolumePerformanceGroupID = sql.NullInt64{Int64: createdVPG.ID, Valid: true}
		dbVolume.VolumePerformanceGroup = createdVPG
		log.Info("VPG created with QoS policy", "vpg_uuid", createdVPG.UUID, "qos_policy", createdVPG.OntapQosPolicyID)
	}

	// If the volume already exists in DB (created before workflow), update its VPG assignment
	if dbVolume.ID != 0 && createdVPG != nil {
		updateActivity := &activities.VolumeUpdateActivity{}
		err = workflow.ExecuteActivity(dbHbCtx, updateActivity.UpdateVolumePerformanceGroupInDBForVolume, dbVolume.UUID, createdVPG).Get(dbHbCtx, nil)
		if err != nil {
			log.Error("Failed to update volume with VPG", "uuid", dbVolume.UUID, "error", err)
			return nil, ConvertToVSAError(err)
		}
		log.Info("Updated existing volume with VPG", "volume_uuid", dbVolume.UUID, "vpg_uuid", createdVPG.UUID)
	}
	// Do not create the volume in DB here; it should already exist.
	// Refresh from DB to ensure we have DB-generated fields and VPG association.
	previousDataProtection := dbVolume.DataProtection
	var createdVolume *datamodel.Volume
	err = workflow.ExecuteActivity(dbHbCtx, volumeActivity.GetVolumeByVolumeID, dbVolume.UUID).Get(dbHbCtx, &createdVolume)
	if err != nil {
		log.Error("Failed to fetch existing volume", "uuid", dbVolume.UUID, "error", err)
		return nil, ConvertToVSAError(err)
	}
	dbVolume = createdVolume
	if dbVolume.DataProtection == nil && previousDataProtection != nil {
		dbVolume.DataProtection = previousDataProtection
	}
	if createdVPG != nil {
		dbVolume.VolumePerformanceGroup = createdVPG
	}
	log.Info("Volume created in DB with VPG", "volume_uuid", dbVolume.UUID, "vpg_id", dbVolume.VolumePerformanceGroupID.Int64, "vpg_id_valid", dbVolume.VolumePerformanceGroupID.Valid)
	if dbVolume.VolumePerformanceGroupID.Valid && dbVolume.VolumePerformanceGroup != nil {
		log.Info("Volume VPG details", "vpg_uuid", dbVolume.VolumePerformanceGroup.UUID, "qos_policy", dbVolume.VolumePerformanceGroup.OntapQosPolicyID)
	}

	// Add volume deletion to rollback manager in case workflow fails after this point
	rollbackManager.AddActivity(volumeDeleteActivity.DeleteVolume, dbVolume)

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	var volCreateResponse *vsa.VolumeResponse
	err = workflow.ExecuteActivity(ctx, volumeActivity.CreateVolumeInONTAP, &dbVolume, &node, &snapshot, backup, aggregateResult).Get(ctx, &volCreateResponse)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(volumeDeleteActivity.DeleteVolumeInONTAP, volCreateResponse.ExternalUUID, dbVolume.Name, &node) // This will delete the lunMap & lun if exists

	// Persisting ExternalUUID in the database to ensure it is available for ONTAP volume deletion during cleanup triggered by CCFE/VCP
	dbVolume.VolumeAttributes.ExternalUUID = volCreateResponse.ExternalUUID
	common.ApplyRestrictedActionsToVolume(dbVolume, createVolumeParams.RestrictedActions)
	err = workflow.ExecuteActivity(dbHbCtx, volumeActivity.UpdateVolumeAttributesInDB, dbVolume.UUID, dbVolume.VolumeAttributes).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Update CV count for auto-provisioned large volumes from the CreateVolume response
	// Only execute if the volume is large capacity AND the customer didn't specify the constituent count
	// (i.e., it was auto-provisioned by ONTAP) AND we got the count from the creation response
	if dbVolume.LargeVolumeAttributes != nil && dbVolume.LargeVolumeAttributes.LargeCapacity &&
		dbVolume.LargeVolumeAttributes.LargeVolumeConstituentCount == nil && volCreateResponse.ConstituentCount != nil {
		log.Debug("Updating CV count for auto-provisioned volume", "uuid", dbVolume.UUID, "constituentCount", *volCreateResponse.ConstituentCount)

		// Update the dbVolume struct with the actual CV count from ONTAP creation response
		dbVolume.LargeVolumeAttributes.LargeVolumeConstituentCount = volCreateResponse.ConstituentCount
		log.Debug("Successfully updated CV count for auto-provisioned volume to", "uuid", dbVolume.UUID, "constituentCount", *volCreateResponse.ConstituentCount)
	}

	// Calculate the available LUN space by subtracting the reserved space for snapshots
	var restoreVolCreateResponse *vsa.VolumeResponse
	if isRestoreSnapshot {
		if utils.IsSanProtocols(dbVolume.VolumeAttributes.Protocols) {
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			err = workflow.ExecuteActivity(ctx, volumeActivity.LunSizeUpdateValidation, &dbVolume, &node).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		// TODO: [VSCP-1435] To remove 'Split' keywords as split operation is removed from create volume workflow
		err = workflow.ExecuteActivity(ctx, volumeActivity.UpdateClonedVolumeBeforeSplit, &dbVolume, &node).Get(ctx, &restoreVolCreateResponse)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Update auto tiering policy after clone creation
		// ONTAP doesn't accept auto tiering policy during clone creation, so we update it after creation
		// This also handles the case where AT is disabled - we explicitly set it to "none" to prevent
		// ONTAP from inheriting the parent volume's AT policy
		log.Debug("Updating auto tiering policy for clone volume after creation", "name", dbVolume.Name)
		err = workflow.ExecuteActivity(ctx, volumeActivity.UpdateVolumeAutoTieringPolicyInONTAP, &dbVolume, &node).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to update auto tiering policy for clone volume: %w", err))
		}
		log.Debug("Successfully updated auto tiering policy for clone volume", "name", dbVolume.Name)
	}

	var hostGroups []*datamodel.HostGroup
	var hostParams []*common.HostParams
	if !dbVolume.VolumeAttributes.IsDataProtection && utils.IsSanProtocols(dbVolume.VolumeAttributes.Protocols) {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		// Get host groups for block volume
		err = workflow.ExecuteActivity(ctx, volumeActivity.GetHosts, &dbVolume).Get(ctx, &hostGroups)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		hostParams = createHostParamsFromHostGroups(hostGroups, dbVolume)

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		// Create igroup for block volume
		err = workflow.ExecuteActivity(ctx, volumeActivity.CreateIgroup, &dbVolume, &hostParams, node).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to create igroup: %w", err))
		}
	}
	// If isRestoreFromBackup is true, we will restore the volume from the backup
	// backup path example: "projects/123456789/locations/us-e4/backupVaults/bv1/backups/backupName"
	if isRestoreFromBackup {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(ctx, volumeActivity.CreateRestoreWorkflow, createVolumeParams, &dbVolume, &hostParams, backupVault, backup, &volCreateResponse).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(fmt.Errorf("failed to create Restore Workflow: %w", err))
		}
	}

	if !isRestoreFromBackup {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		postWorkflowFunc, postErr := selectVolumeChildWorkflow(dbVolume.VolumeAttributes.Protocols, PhasePost, ontapVersion)
		if postErr != nil {
			err = postErr
			return nil, ConvertToVSAError(err)
		}
		postChildCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		})
		// Handle both single workflow and slice of workflows (for NFS+SMB combination)
		var updatedVolume *datamodel.Volume
		if workflowSlice, ok := postWorkflowFunc.([]interface{}); ok {
			// Execute multiple workflows sequentially
			for _, workflowFunc := range workflowSlice {
				if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
					return nil, cancelErr
				}
				err = workflow.ExecuteChildWorkflow(postChildCtx, workflowFunc, dbVolume, node, hostParams, volCreateResponse, isRestoreFromBackup, isRestoreSnapshot, restoreVolCreateResponse).Get(postChildCtx, &updatedVolume)
				if err != nil {
					return nil, ConvertToVSAError(err)
				}
				// Update the dbVolume with the changes from each child workflow
				if updatedVolume != nil {
					dbVolume = updatedVolume
				}
			}
		} else {
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			// Single workflow execution
			err = workflow.ExecuteChildWorkflow(postChildCtx, postWorkflowFunc, dbVolume, node, hostParams, volCreateResponse, isRestoreFromBackup, isRestoreSnapshot, restoreVolCreateResponse).Get(postChildCtx, &updatedVolume)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			// Update the dbVolume with the changes from the child workflow
			if updatedVolume != nil {
				dbVolume = updatedVolume
			}
		}
	}

	if dbVolume.DataProtection != nil && dbVolume.DataProtection.BackupVaultID != "" {
		if !env.IsLocalEnv() {
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			var token string
			err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetAuthJWTToken, createVolumeParams.AccountName).Get(ctx, &token)
			if err != nil {
				log.Error("Failed to get token for account", "accountName", createVolumeParams.AccountName, "error", err)
				return nil, ConvertToVSAError(err)
			}
			ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, token)
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		var backupVault *datamodel.BackupVault
		err = workflow.ExecuteActivity(ctx, volumeActivity.CheckBackupVaultExistsInVCP, &dbVolume, &region).Get(ctx, &backupVault)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		var remoteBV *datamodel.BackupVault
		err = workflow.ExecuteActivity(ctx, volumeActivity.CheckOrCreateRemoteBackupVaultInVCP, &dbVolume, backupVault, nil).Get(ctx, &remoteBV)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		backupRegion := region
		if backupVault.BackupVaultType == activities.CrossRegionBackupType && *backupVault.BackupRegionName != "" {
			backupRegion = *backupVault.BackupRegionName
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		tenancyDetails := &common.TenancyInfo{}
		if backupVault.ServiceType != activities.GCBDRServiceType {
			err = workflow.ExecuteActivity(ctx, volumeActivity.FindTenancy, dbVolume.VolumeAttributes.VendorSubnetID, dbVolume.Account.Name, backupRegion).Get(ctx, &tenancyDetails)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		} else {
			if backupVault.BucketDetails != nil && len(backupVault.BucketDetails) > 0 {
				tenancyDetails.RegionalTenantProject = backupVault.BucketDetails[0].TenantProjectNumber
			} else {
				log.Error("GCBDR vault has no bucket details with tenant project", "uuid", backupVault.UUID)
				return nil, ConvertToVSAError(fmt.Errorf("GCBDR vault has no tenant project information"))
			}
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		bucketDetails := &common.BucketDetails{}
		err = workflow.ExecuteActivity(ctx, volumeActivity.CheckForBucketResourceName, &dbVolume).Get(ctx, &bucketDetails)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		if bucketDetails.BucketName == "" && bucketDetails.ServiceAccountName == "" && bucketDetails.TenantProjectNumber == "" {
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			resourceName := &common.ResourceNames{}
			err = workflow.ExecuteActivity(ctx, volumeActivity.GenerateResourceNames, &dbVolume, &tenancyDetails, region).Get(ctx, &resourceName)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Extract kmsGrant from volume's DataProtection
			var kmsGrant *string
			if dbVolume.DataProtection != nil && !nillable.IsNilOrEmpty(dbVolume.DataProtection.KmsGrant) {
				kmsGrant = dbVolume.DataProtection.KmsGrant
			}
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			err = workflow.ExecuteActivity(ctx, volumeActivity.CreateBucket, &resourceName, &tenancyDetails, backupRegion, kmsGrant).Get(ctx, &bucketDetails)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			// For GCBDR vaults, leave VendorSubnetID empty (cross-project bucket)
			if backupVault.ServiceType != activities.GCBDRServiceType {
				bucketDetails.VendorSubnetID = dbVolume.VolumeAttributes.VendorSubnetID
			}
			// Setting the 'satisfiesPzi' and 'satisfiesPzs' fields in bucketDetails by fetching the latest info from GCP
			err = syncBucketDetailsWithGCP(ctx, bucketDetails)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			err = workflow.ExecuteActivity(ctx, volumeActivity.UpdateBackupVaultWithBucketDetails, &dbVolume, &bucketDetails).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(ctx, volumeActivity.UpdateRemoteBackupVaultWithBucketDetails, &dbVolume, backupVault, remoteBV, &bucketDetails).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Grant pool SA access to GCBDR bucket unconditionally — runs on every volume operation
		// so that a pool attaching to an already-provisioned vault still receives the IAM grant.
		if backupVault.ServiceType == activities.GCBDRServiceType {
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			poolDetails := dbVolume.Pool
			if poolDetails == nil {
				log.Error("Pool details not available for volume", "uuid", dbVolume.UUID)
				return nil, ConvertToVSAError(fmt.Errorf("pool details required for GCBDR bucket permissions"))
			}
			err = workflow.ExecuteActivity(ctx, volumeActivity.SetupCrossProjectBackupPermissions, poolDetails, &bucketDetails).Get(ctx, nil)
			if err != nil {
				log.Error("Failed to setup cross-project backup permissions", "error", err)
				return nil, ConvertToVSAError(err)
			}
			log.Info("Successfully granted pool SA access to GCBDR bucket", "bucketName", bucketDetails.BucketName)
		}

		// TODO: Optimize this to avoid running for each volume call.
		// This is currently unoptimized and runs for every volume operation.
		// Consider optimizing to run once per pool or caching the results.
		if backupVault.BackupVaultType == activities.CrossRegionBackupType && backupVault.BackupRegionName != nil && *backupVault.BackupRegionName != "" {
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			err = workflow.ExecuteActivity(ctx, volumeActivity.SetupCrossRegionBackupPermissionsActivity, backupVault, &dbVolume.Pool, &bucketDetails).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Wait for service account to be ready
			err = workflowSleep(ctx, time.Second*90)
			if err != nil {
				log.Error("Failed to sleep after cross-region backup permissions are created", "error", err)
			}
		}
	}

	if dbVolume.DataProtection != nil && dbVolume.DataProtection.BackupPolicyID != "" {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		var backupPolicyExists bool
		err = workflow.ExecuteActivity(ctx, volumeActivity.CheckIfBackupPolicyExistsInVCP, dbVolume.DataProtection.BackupPolicyID, dbVolume.AccountID).Get(ctx, &backupPolicyExists)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		if !backupPolicyExists {
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			if !utils.IsCVPHostConfigured() {
				log.Warn("Backup policy does not exist in VCP in a VCP-only region (SDE unavailable); volume creation requires an existing VCP backup policy", "backupPolicyID", dbVolume.DataProtection.BackupPolicyID)
				return nil, ConvertToVSAError(customerrors.NewNotFoundErr(
					fmt.Sprintf("Backup policy %s not found", dbVolume.DataProtection.BackupPolicyID),
					nil,
				))
			}
			backupPolicyActivity := &activities.BackupPolicyActivity{}
			var vcpBackupPolicy *datamodel.BackupPolicy
			err = workflow.ExecuteActivity(ctx, volumeActivity.CreateBackupPolicyFetchedFromSDE, &dbVolume, region).Get(ctx, &vcpBackupPolicy)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			err = workflow.ExecuteActivity(ctx, volumeActivity.CreateBackupPolicySchedule, &vcpBackupPolicy, createVolumeParams.BackupSchedule).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			if !vcpBackupPolicy.PolicyEnabled {
				if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
					return nil, cancelErr
				}
				err = workflow.ExecuteActivity(ctx, backupPolicyActivity.PauseBackupPolicySchedule, vcpBackupPolicy).Get(ctx, nil)
				if err != nil {
					return nil, ConvertToVSAError(err)
				}
			}
		} else {
			// Backup policy exists in VCP - ensure schedule exists (create only once)
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			backupPolicyActivity := &activities.BackupPolicyActivity{}
			// Get the backup policy from VCP
			var vcpBackupPolicy *datamodel.BackupPolicy
			err = workflow.ExecuteActivity(ctx, backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, dbVolume.DataProtection.BackupPolicyID, dbVolume.AccountID).Get(ctx, &vcpBackupPolicy)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Check if schedule already exists
			var scheduleExists bool
			err = workflow.ExecuteActivity(ctx, backupPolicyActivity.CheckIfBackupPolicyScheduleExists, vcpBackupPolicy.UUID).Get(ctx, &scheduleExists)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			// Create schedule only if it doesn't exist
			if !scheduleExists {
				if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
					return nil, cancelErr
				}
				err = workflow.ExecuteActivity(ctx, volumeActivity.CreateBackupPolicySchedule, &vcpBackupPolicy, createVolumeParams.BackupSchedule).Get(ctx, nil)
				if err != nil {
					return nil, ConvertToVSAError(err)
				}
			}

			// Handle policy enabled/disabled state
			if !vcpBackupPolicy.PolicyEnabled {
				if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
					return nil, cancelErr
				}
				err = workflow.ExecuteActivity(ctx, backupPolicyActivity.PauseBackupPolicySchedule, vcpBackupPolicy).Get(ctx, nil)
				if err != nil {
					return nil, ConvertToVSAError(err)
				}
			} else {
				// If policy is enabled, ensure schedule is unpaused
				if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
					return nil, cancelErr
				}
				err = workflow.ExecuteActivity(ctx, backupPolicyActivity.UnpauseBackupPolicySchedule, vcpBackupPolicy).Get(ctx, nil)
				if err != nil {
					return nil, ConvertToVSAError(err)
				}
			}
		}
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	common.ApplyRestrictedActionsToVolume(dbVolume, createVolumeParams.RestrictedActions)
	err = workflow.ExecuteActivity(dbHbCtx, volumeActivity.UpdateVolumeDetails, &dbVolume, &volCreateResponse).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}

func createHostParamsFromHostGroups(hostGroups []*datamodel.HostGroup, volume *datamodel.Volume) []*common.HostParams {
	var hostParamsArray []*common.HostParams
	osType := ""
	if volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0 {
		osType = (*volume.VolumeAttributes.BlockDevices)[0].OSType
	} else {
		osType = volume.VolumeAttributes.BlockProperties.OSType
	}

	for _, hostGroup := range hostGroups {
		hostParams := &common.HostParams{
			HostName: hostGroup.Name,
			HostIQNs: hostGroup.Hosts.Hosts,
			OsType:   osType,
		}
		hostParamsArray = append(hostParamsArray, hostParams)
	}

	return hostParamsArray
}

func createLunMapParams(lunName string, svmName string, hostParams []*common.HostParams) *common.CreateLunMapParams {
	var hostNames []string

	for _, hostParam := range hostParams {
		hostNames = append(hostNames, hostParam.HostName)
	}

	lunMapParam := &common.CreateLunMapParams{
		LunName:   lunName,
		SvmName:   svmName,
		HostNames: hostNames,
	}

	return lunMapParam
}

// SyncBucketDetailsWithGCP is the exported form of syncBucketDetailsWithGCP, accessible from sub-packages.
func SyncBucketDetailsWithGCP(ctx workflow.Context, bucketDetails *common.BucketDetails) error {
	return syncBucketDetailsWithGCP(ctx, bucketDetails)
}

// syncBucketDetailsWithGCP syncs bucket details with GCP to get the latest ZiZs information
func syncBucketDetailsWithGCP(ctx workflow.Context, bucketDetails *common.BucketDetails) error {
	logger := workflow.GetLogger(ctx)
	existingBucketDetails := &datamodel.BucketDetails{
		BucketName:          bucketDetails.BucketName,
		TenantProjectNumber: bucketDetails.TenantProjectNumber,
		ServiceAccountName:  bucketDetails.ServiceAccountName,
		VendorSubnetID:      bucketDetails.VendorSubnetID,
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(volumeStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)
	var updatedBucketDetails *datamodel.BucketDetails
	syncBackupZiZsActivity := &backgroundactivities.SyncBackupZiZsActivity{}
	err := workflow.ExecuteActivity(actCtx, syncBackupZiZsActivity.SyncBucketDetails, existingBucketDetails).Get(actCtx, &updatedBucketDetails)
	if err != nil {
		// Log the error, but do not fail the workflow
		logger.Error("Failed to sync bucket details for bucket", "bucketName", existingBucketDetails.BucketName, "error", err)
	} else if updatedBucketDetails != nil {
		bucketDetails.SatisfiesPzi = updatedBucketDetails.SatisfiesPzi
		bucketDetails.SatisfiesPzs = updatedBucketDetails.SatisfiesPzs
	}
	return nil // Always return nil to not fail the workflow
}

// protocolInfo holds protocol classification information for validation
type protocolInfo struct {
	isSAN  bool
	hasSMB bool
	hasNFS bool
}

// analyzeProtocols analyzes a list of protocols and returns classification information
func analyzeProtocols(protocols []string) protocolInfo {
	return protocolInfo{
		isSAN:  utils.IsSanProtocols(protocols),
		hasSMB: utils.IsSMBProtocols(protocols),
		hasNFS: utils.IsNFSProtocols(protocols),
	}
}

// verifyBackupRestoreCompatibilityForVolumes verifies that backup protocols are compatible with volume protocols
// for SDE backup restore to VCP volumes. This function implements protocol compatibility rules:
// - SDE supports: NFS, NFSv3, NFSv4, SMB (NAS protocols only)
// - VCP supports: NFS, NFSv3, NFSv4, SMB, iSCSI (NAS + SAN)
// - Only SDE → VCP backup restore is supported (not VCP → SDE)
// Protocol compatibility rules:
// - iSCSI: Not applicable for SDE backups (SDE doesn't support iSCSI)
// - SMB: backup can only be restored to SMB volume (exact match required)
// - NFS protocols (NFSv3 and NFSv4) are compatible with each other (NFSv3 ↔ NFSv4 cross-restore allowed)
// - Mixed protocols must be compatible (e.g., NFS+SMB backup requires NFS+SMB volume)
func verifyBackupRestoreCompatibilityForVolumes(backup *datamodel.Backup, params *common.CreateVolumeParams) error {
	if backup == nil || backup.Attributes == nil {
		return customerrors.NewUserInputValidationErr("Backup or backup attributes are nil")
	}

	backupProtocols := backup.Attributes.Protocols
	volumeProtocols := params.Protocols

	// If backup has no protocols, skip validation (legacy backup)
	if len(backupProtocols) == 0 {
		return nil
	}

	// If volume has no protocols, that's an error
	if len(volumeProtocols) == 0 {
		return customerrors.NewUserInputValidationErr("Volume protocols must be specified for backup restore")
	}

	// Analyze backup and volume protocols
	backupInfo := analyzeProtocols(backupProtocols)
	volumeInfo := analyzeProtocols(volumeProtocols)

	// Check SAN protocol compatibility (iSCSI)
	if backupInfo.isSAN && !volumeInfo.isSAN {
		return customerrors.NewUserInputValidationErr("Cannot restore a SAN (iSCSI) backup to a NAS volume. The restore volume must use iSCSI protocol")
	}

	if !backupInfo.isSAN && volumeInfo.isSAN {
		return customerrors.NewUserInputValidationErr("Cannot restore a NAS backup to a SAN (iSCSI) volume. The restore volume must use NAS protocols")
	}

	// For SAN protocols, exact match is required (iSCSI to iSCSI)
	// If both backup and volume are SAN, they are compatible (IsSanProtocols ensures all protocols are SAN, and currently only iSCSI is SAN)
	if backupInfo.isSAN && volumeInfo.isSAN {
		return nil
	}

	// SMB protocol must match exactly (backup with SMB requires volume with SMB)
	if backupInfo.hasSMB && !volumeInfo.hasSMB {
		return customerrors.NewUserInputValidationErr("Cannot restore a SMB backup to a volume without SMB protocol. The restore volume must include SMB protocol")
	}

	// NFS protocols are compatible with each other (NFSv3 ↔ NFSv4 cross-restore allowed, matching cloud-volumes-service behavior)
	// If backup has NFS but volume doesn't, that's an error
	if backupInfo.hasNFS && !volumeInfo.hasNFS {
		return customerrors.NewUserInputValidationErr("Cannot restore a NFS backup to a volume without NFS protocol. The restore volume must include NFS protocol (NFSv3 or NFSv4)")
	}

	// If volume has SMB but backup doesn't (and backup has NFS), that's an error
	if !backupInfo.hasSMB && volumeInfo.hasSMB && backupInfo.hasNFS {
		return customerrors.NewUserInputValidationErr("Cannot restore a NFS-only backup to a volume with SMB protocol. The restore volume must match the backup protocols")
	}

	// If volume has NFS but backup doesn't (and backup has SMB), that's an error
	if !backupInfo.hasNFS && volumeInfo.hasNFS && backupInfo.hasSMB {
		return customerrors.NewUserInputValidationErr("Cannot restore a SMB-only backup to a volume with NFS protocol. The restore volume must match the backup protocols")
	}

	// All protocol checks passed
	return nil
}

// for Large Volume creation, we store CV count for auto-provision volumes and customer given CV count. from volume we fetch the CV and store
// in backup at the time of backup Creation, so for large volume backups, we will have CV count in backup attributes.
// case 1 : Customer created volume with CV and took backup -> proceed with restore wihout CV, we have to pass backup CV to Volume.
// case 2: Customer  created volume with CV and took backup -> proceed with restore with CV, we have to validate backup CV and customer provided CV matches, then proceed with restore.
// case 3: Customer created volume without CV and took backup -> proceed with restore without CV, we have to pass backup CV to Volume.
// case 4: Customer created volume without CV and took backup -> proceed with restore with CV, we have to validate backup CV and customer provided CV matches, then proceed with restore.
func _verifyBackupRestoreCompatibilityForLargeVolumes(backup *datamodel.Backup, params *common.CreateVolumeParams) (*common.CreateVolumeParams, error) {
	if params.LargeCapacity && backup.Attributes.OntapVolumeStyle != "flexgroup" {
		return nil, customerrors.NewUserInputValidationErr("Cannot restore a large capacity volume from a backup that is not a large volume backup")
	}

	if backup.Attributes.OntapVolumeStyle != "flexgroup" {
		return params, nil
	}

	if params.BackupPath != "" && params.LargeCapacity && params.LargeVolumeConstituentCount == 0 {
		params.LargeVolumeConstituentCount = backup.Attributes.ConstituentCountOfBackup
		return params, nil
	}

	// Handle large volume backup cases
	backupConstituentCount := backup.Attributes.ConstituentCountOfBackup
	customerConstituentCount := params.LargeVolumeConstituentCount

	// Customer provided count
	if customerConstituentCount > 0 && customerConstituentCount != backupConstituentCount {
		return nil, customerrors.NewUserInputValidationErr(fmt.Sprintf("Constituent count provided (%d) does not match with that of backup (%d)", customerConstituentCount, backupConstituentCount))
	}
	return params, nil
}

// WaitForGCPNetworkOperationStatusWorkflow is a wrapper workflow that calls WaitForGCPNetworkOperationStatus
// This allows it to be called as a child workflow to prevent deadlocks
func WaitForGCPNetworkOperationStatusWorkflow(ctx workflow.Context, operations *[]common.Operations, timeout time.Duration) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: utils.GetStartToCloseTimeoutHyperscaler(),
		HeartbeatTimeout:    utils.GetHeartbeatTimeoutForHyperscaler(),
		RetryPolicy:         utils.GetHyperscalerLRORetryPolicy(),
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	poolActivity := &activities.PoolActivity{}
	return WaitForGCPNetworkOperationStatus(ctx, poolActivity, operations, timeout)
}

// updateActiveDirectoryStateToInUse updates the Active Directory state to IN_USE if it is currently READY.
func updateActiveDirectoryStateToInUse(
	ctx workflow.Context,
	activeDirectory *vsa.ActiveDirectory,
) error {
	log := workflow.GetLogger(ctx)
	if activeDirectory == nil {
		return ConvertToVSAError(fmt.Errorf("active Directory is nil"))
	}
	if activeDirectory.Status == datamodel.LifeCycleStateREADY {
		log.Info("First SMB volume/LDAP enabled NFS volume created, updating Active Directory state to IN_USE")
		err := workflow.ExecuteActivity(ctx, active_directory_activities.ActiveDirectoryActivity.UpdateActiveDirectoryState,
			activeDirectory.UUID, datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails).Get(ctx, nil)
		if err != nil {
			log.Error("Failed to update Active Directory state to IN_USE", "error", err)
			return ConvertToVSAError(err)
		}
	}
	return nil
}
