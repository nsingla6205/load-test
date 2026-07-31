package activities

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"gorm.io/gorm"
)

type SvmActivity struct {
	SE database.Storage
}

const (
	gcpDefaultIPSpace     = "Default"
	ociDefaultIPSpace     = "ocifsn"
	nfsTCPMaxTransferSize = 1048576
)

var defaultIPSpace = resolveDefaultIPSpace()

func resolveDefaultIPSpace() string {
	if v := env.GetString("DEFAULT_IPSPACE", ""); v != "" {
		return v
	}
	if env.GetHyperscaler() == commonparams.ProviderOCI {
		return ociDefaultIPSpace
	}
	return gcpDefaultIPSpace
}

// GetSvmAdminPasswordSecretForOCI fetches the SVM admin password secret from OCI Vault for an SVM.
func (s *SvmActivity) GetSvmAdminPasswordSecretForOCI(ctx context.Context, svm *datamodel.Svm, svmAdminPassword *commonparams.OciAdminPassword) (*vlm.OntapCredentials, error) {
	if svm == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm must not be nil")))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting GetSvmAdminPasswordSecretForOCI activity - svm Name: %s, svmOCID: %s", svm.Name, svm.SvmExternalIdentifier))
	credentials := &vlm.OntapCredentials{}

	ociService, err := hyperscaler.GetOCIService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err))
	}

	if svmAdminPassword == nil || svmAdminPassword.Ocid == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("svmAdminPassword is required for SVM admin password secret")))
	}

	ociService.GetLogger().Infof("Fetching SVM admin password from OCI Vault — secretOCID: %s, version: %d", svmAdminPassword.Ocid, svmAdminPassword.Version)
	secret, err := ociService.GetSecretWithCustomVersion(svmAdminPassword.Ocid, svmAdminPassword.Version)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, err))
	}
	if secret == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrOCIVaultSecretInactive, fmt.Errorf("secret is inactive or pending deletion in OCI Vault — OCID: %s, version: %d", svmAdminPassword.Ocid, svmAdminPassword.Version)))
	}

	credentials.AdminPassword = secret.Value
	ociService.GetLogger().Infof("SVM admin password fetched successfully from OCI Vault for svm: %s", svm.SvmExternalIdentifier)
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Finished GetSvmAdminPasswordSecretForOCI activity - svm Name: %s, svmOCID: %s", svm.Name, svm.SvmExternalIdentifier))
	return credentials, nil
}

func (j *SvmActivity) SaveSVMAndLifData(ctx context.Context, pool *datamodel.Pool, vlmConfig *vlm.VLMConfig, svmName string) (*datamodel.Svm, error) {
	return j.saveSVMAndLifData(ctx, pool, vlmConfig, svmName, "")
}

// SaveSVMAndLifDataWithOCID is an OCI-specific variant that persists svmOCID in DB.
func (j *SvmActivity) SaveSVMAndLifDataWithOCID(ctx context.Context, pool *datamodel.Pool, vlmConfig *vlm.VLMConfig, svmName string, svmOCID string) (*datamodel.Svm, error) {
	return j.saveSVMAndLifData(ctx, pool, vlmConfig, svmName, strings.TrimSpace(svmOCID))
}

// GetNodeUUIDsByNameForPool returns a lookup map used to enrich SVM workflow metadata.
func (j *SvmActivity) GetNodeUUIDsByNameForPool(ctx context.Context, poolID int64) (map[string]string, error) {
	nodes, err := j.SE.GetNodesByPoolID(ctx, poolID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	nodeUUIDsByName := make(map[string]string, len(nodes))
	for _, node := range nodes {
		if node.Name == "" {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, errors.New("node name is empty for node ID "+strconv.FormatInt(node.ID, 10)))
		}
		nodeUUIDsByName[node.Name] = node.UUID
	}
	return nodeUUIDsByName, nil
}

func (j *SvmActivity) saveSVMAndLifData(ctx context.Context, pool *datamodel.Pool, vlmConfig *vlm.VLMConfig, svmName string, svmExternalIdentifier string) (*datamodel.Svm, error) {
	activity.RecordHeartbeat(ctx, "Starting SaveSVMAndLifData activity")
	if pool == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool must not be nil")))
	}
	if vlmConfig == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("vlmConfig must not be nil")))
	}
	se := j.SE
	svm, ok := vlmConfig.Svm[svmName]
	if !ok {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("SVM %q not found in VLM config", svmName)))
	}
	activity.RecordHeartbeat(ctx, "Getting nodes for pool to validate node count")
	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(nodes) < 2 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, errors.New("not enough nodes in the cluster to create LIFs for SVM "+svm.Svmname)))
	}

	ipSpace := vlmConfig.VsaCluster.CustIPSpace
	if ipSpace == "" {
		ipSpace = defaultIPSpace // hyperscaler-driven fallback ("Default" on GCP, "ocifsn" on OCI; overridable via DEFAULT_IPSPACE)
	}
	svmRec := &datamodel.Svm{
		Name:                  svm.Svmname,
		SvmExternalIdentifier: svmExternalIdentifier,
		AccountID:             pool.AccountID,
		PoolID:                pool.ID,
		SvmDetails: &datamodel.SvmDetails{
			ExternalUUID: svm.Svmuuid,
			IPSpace:      ipSpace,
		},
	}

	activity.RecordHeartbeat(ctx, "Creating SVM record in database")
	createdSvm, err := se.CreateSVM(ctx, svmRec)
	if err != nil {
		if !utilErrors.IsConflictErr(err) {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		createdSvm, err = se.GetSvmByNameAndPoolID(ctx, svmRec.Name, pool.ID)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}
	type nodeLookup struct {
		id int64
	}
	// create map of nodes with node name as key and node identity as value
	nodeMap := make(map[string]nodeLookup)
	for _, node := range nodes {
		if node.Name == "" {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, errors.New("node name is empty for node ID "+strconv.FormatInt(node.ID, 10)))
		}
		nodeMap[node.Name] = nodeLookup{
			id: node.ID,
		}
	}

	createLifs := func(lifType vlm.VSALIFType, protocolType string) error {
		for _, lif := range svm.SVMLIFs[lifType] {
			if lif.IP == "" {
				return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
					fmt.Errorf("LIF %s has an empty IP address", lif.Name))
			}
			ip := strings.Split(lif.IP, "/")[0]

			node, exists := nodeMap[lif.HomeNode]
			if !exists {
				return vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, fmt.Errorf("LIF %s references non-existent home node %s", lif.Name, lif.HomeNode))
			}

			lifRec := &datamodel.Lif{
				Name:      lif.Name,
				AccountID: pool.AccountID,
				NodeID:    node.id,
				LifDetails: &datamodel.LifDetails{
					ExternalUUID: lif.Uuid,
					ProtocolType: protocolType,
				},
				IPAddress:  ip,
				SubnetMask: vsa.DefaultNetmask,
			}

			activity.RecordHeartbeat(ctx, "Creating LIF record in database")
			if _, err := se.CreateLif(ctx, lifRec); err != nil && !utilErrors.IsConflictErr(err) {
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
		}
		return nil
	}

	if err := createLifs(vlm.LIFTypeSan, string(vlm.LIFTypeSan)); err != nil {
		return nil, err
	}

	if err := createLifs(vlm.LIFTypeNas, string(vlm.LIFTypeNas)); err != nil {
		return nil, err
	}

	if err := createLifs(vlm.LIFTypeIlbNas, string(vlm.LIFTypeNas)); err != nil {
		return nil, err
	}

	activity.RecordHeartbeat(ctx, "Finished SaveSVMAndLifData activity")
	return createdSvm, nil
}

// applyQoSPolicyToSVM is a utility function that applies a QoS policy to an SVM
// It handles the common logic of getting the provider and applying the policy
func applyQoSPolicyToSVM(ctx context.Context, svm *datamodel.Svm, node *models.Node, qosPolicyName string) error {
	logger := util.GetLogger(ctx)

	if svm == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm must not be nil")))
	}
	if svm.SvmDetails == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm %s has nil SvmDetails", svm.Name)))
	}

	// Get the provider for the node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Apply the QoS policy to the SVM
	modifySvmParams := vsa.ModifySVMWithQoSPolicyParams{
		SvmUUID:       svm.SvmDetails.ExternalUUID,
		QoSPolicyName: qosPolicyName,
	}

	err = provider.ModifySVMWithQoSPolicy(modifySvmParams)
	if err != nil {
		logger.Error("Failed to apply QoS policy to SVM", "error", err, "svmName", svm.Name, "policyName", qosPolicyName)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy applied to SVM successfully", "svmName", svm.Name, "policyName", qosPolicyName)
	return nil
}

// RemoveQoSPolicyFromSVM clears the QoS policy from the SVM (vserver-level).
// Used during pool qosType transition auto→manual so the pool's QPG is no longer applied at vserver level.
// Same lookup pattern as applyQoSPolicyToSVM: GetSvmForPoolID then provider.ModifySVMWithQoSPolicy with empty policy name.
func (j *SvmActivity) RemoveQoSPolicyFromSVM(ctx context.Context, pool *datamodel.Pool, node *models.Node) error {
	logger := util.GetLogger(ctx)
	if pool == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool must not be nil")))
	}
	if node == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("node must not be nil")))
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting RemoveQoSPolicyFromSVM - pool: %s, node: %s", pool.Name, node.Name))

	svm, err := j.GetSvmForPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if svm == nil || svm.SvmDetails == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("SVM or SvmDetails is nil for pool %s", pool.Name))
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	modifyParams := vsa.ModifySVMWithQoSPolicyParams{
		SvmUUID:       svm.SvmDetails.ExternalUUID,
		QoSPolicyName: "", // empty clears the policy from the SVM
	}
	if err := provider.ModifySVMWithQoSPolicy(modifyParams); err != nil {
		logger.Error("Failed to remove QoS policy from SVM", "error", err, "svmName", svm.Name)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy removed from SVM successfully", "svmName", svm.Name)
	return nil
}

// UpdateNFSRWSizeOnSVM updates the SVM NFS TCP transfer size to ONTAP's maximum.
func (j *SvmActivity) UpdateNFSRWSizeOnSVM(ctx context.Context, svm *datamodel.Svm, node *models.Node) error {
	logger := util.GetLogger(ctx)
	if svm == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm must not be nil")))
	}
	if svm.SvmDetails == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm %s has nil SvmDetails", svm.Name)))
	}
	if svm.SvmDetails.ExternalUUID == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm %s has empty external UUID", svm.Name)))
	}
	if node == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("node must not be nil")))
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting UpdateNFSRWSizeOnSVM activity - SVM: %s, node: %s", svm.Name, node.Name))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if err := provider.UpdateNFSRWSizeOnSVM(ctx, svm.SvmDetails.ExternalUUID, nfsTCPMaxTransferSize); err != nil {
		logger.Error("Failed to update NFS TCP max transfer size on SVM", "error", err, "svmName", svm.Name)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Updated NFS TCP max transfer size on SVM successfully", "svmName", svm.Name, "tcpMaxTransferSize", nfsTCPMaxTransferSize)
	return nil
}

// generateQoSPolicyName generates a consistent QoS policy name for an SVM
func generateQoSPolicyName(svmName string) string {
	return fmt.Sprintf("%s-qos-policy", svmName)
}

// CreateQoSPolicyAndApplyToSVM creates a QoS policy group and applies it to the SVM.
// This activity is idempotent - it will check if the QoS policy already exists before creating.
// Non-ONTAP manual QoS pools skip this (no vserver-wide QPG). ONTAP-mode pools always install
// the reserved {svm}-qos-policy as the pool ceiling, including when qosType is manual.
func (j *SvmActivity) CreateQoSPolicyAndApplyToSVM(ctx context.Context, pool *datamodel.Pool, svm *datamodel.Svm, node *models.Node) error {
	logger := util.GetLogger(ctx)
	if pool == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool must not be nil")))
	}
	if svm == nil || svm.SvmDetails == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("SVM or SvmDetails is nil for pool %s", pool.Name)))
	}
	if node == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("node must not be nil")))
	}
	// Skip SVM-level QPG only for non-ONTAP manual pools. ONTAP-mode always installs the
	// reserved vserver-wide policy ({svm}-qos-policy) as the pool ceiling (nested volume QoS).
	if pool.QosType == utils.QosTypeManual && pool.APIAccessMode != commonparams.ONTAPMode {
		logger.Info("QoS type is manual, skipping creating QoS policy assigned to the SVM", "poolName", pool.Name)
		return nil
	}

	logger.Info("Creating QoS policy and applying to SVM", "svmName", svm.Name, "poolName", pool.Name, "qosType", pool.QosType, "apiAccessMode", pool.APIAccessMode)

	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting CreateQoSPolicyAndApplyToSVM activity - pool: %s, SVM: %s, node: %s", pool.Name, svm.Name, node.Name))
	// Get the provider for the node - CA fields are already in the node struct from CreateNodeForProvider()
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Create QoS policy group with default values
	// These values can be made configurable in the future
	qosPolicyName := generateQoSPolicyName(svm.Name)
	if pool.PoolAttributes == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool attributes cannot be nil"))
	}
	maxThroughput := pool.PoolAttributes.ThroughputMibps
	maxIOPS := pool.PoolAttributes.Iops

	// Check if the QoS policy already exists (idempotent behavior)
	findQosPolicyParams := vsa.FindQoSGroupPolicyParams{
		Name:    qosPolicyName,
		SvmName: svm.Name,
	}

	activity.RecordHeartbeat(ctx, "Checking for existing QoS policy group")
	existingQosPolicy, err := provider.FindQoSGroupPolicy(findQosPolicyParams)
	if err == nil {
		// QoS policy already exists, check if it matches our requirements
		if existingQosPolicy.MaxThroughput == maxThroughput && existingQosPolicy.MaxIOPS == maxIOPS {
			logger.Info("QoS policy already exists and matches requirements, skipping creation",
				"policyName", qosPolicyName,
				"throughput", existingQosPolicy.MaxThroughput,
				"iops", existingQosPolicy.MaxIOPS)

			activity.RecordHeartbeat(ctx, "Applying QoS policy to SVM")
			// Apply the existing QoS policy to the SVM using the utility function
			return applyQoSPolicyToSVM(ctx, svm, node, existingQosPolicy.Name)
		} else {
			logger.Info("QoS policy already exists but with different values, updating instead",
				"policyName", qosPolicyName,
				"existingThroughput", existingQosPolicy.MaxThroughput,
				"newThroughput", maxThroughput,
				"existingIOPS", existingQosPolicy.MaxIOPS,
				"newIOPS", maxIOPS)

			// Update the existing QoS policy with new values (omit Name so ONTAP does not treat it as a rename)
			updateQosPolicyParams := vsa.UpdateQoSGroupPolicyParams{
				UUID:          existingQosPolicy.UUID,
				SvmName:       existingQosPolicy.SvmName,
				MaxThroughput: maxThroughput,
				MaxIOPS:       maxIOPS,
			}

			activity.RecordHeartbeat(ctx, "Updating existing QoS policy group")
			err = provider.UpdateQoSGroupPolicy(updateQosPolicyParams)
			if err != nil {
				logger.Error("Failed to update existing QoS policy group", "error", err, "policyName", qosPolicyName)
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}

			logger.Info("QoS policy group updated successfully", "policyName", existingQosPolicy.Name, "policyUUID", existingQosPolicy.UUID)

			activity.RecordHeartbeat(ctx, "Applying QoS policy to SVM")
			// Apply the updated QoS policy to the SVM using the utility function
			return applyQoSPolicyToSVM(ctx, svm, node, existingQosPolicy.Name)
		}
	}

	// QoS policy doesn't exist, create it
	logger.Info("QoS policy does not exist, creating new one", "policyName", qosPolicyName)

	// Create the QoS policy group
	// Default to IsShared=true for backward compatibility (shared capacity policy)
	isShared := true
	qosPolicyParams := vsa.CreateQoSGroupPolicyParams{
		Name:          qosPolicyName,
		SvmName:       svm.Name,
		MaxThroughput: maxThroughput,
		MaxIOPS:       maxIOPS,
		IsShared:      &isShared,
	}

	activity.RecordHeartbeat(ctx, "Creating QoS policy group")
	qosPolicyResponse, err := provider.CreateQoSGroupPolicy(qosPolicyParams)
	if err != nil {
		logger.Error("Failed to create QoS policy group", "error", err, "policyName", qosPolicyName)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy group created successfully", "policyName", qosPolicyResponse.Name, "policyUUID", qosPolicyResponse.UUID)

	activity.RecordHeartbeat(ctx, "Applying QoS policy to SVM")
	// Apply the QoS policy to the SVM using the utility function
	return applyQoSPolicyToSVM(ctx, svm, node, qosPolicyResponse.Name)
}

// ModifyQoSPolicyAndApplyToSVM modifies an existing QoS policy group and applies it to the SVM if changes are needed.
// When switching from manual to auto (updateParams.QosType == auto while pool.QosType is manual), the activity
// finds or creates the pool's QoS policy and applies it to the SVM so the vserver gets the pool qos-policy-group.
// ONTAP-mode pools always keep/update the vserver-wide QPG even when qosType is manual.
func (j *SvmActivity) ModifyQoSPolicyAndApplyToSVM(ctx context.Context, pool *datamodel.Pool, node *models.Node, updateParams *commonparams.UpdatePoolParams) error {
	logger := util.GetLogger(ctx)
	if pool == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool must not be nil")))
	}
	if node == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("node must not be nil")))
	}

	// Skip only when pool is manual (non-ONTAP) and we are not being asked to switch to auto.
	// ONTAP-mode pools always keep/update the vserver-wide QPG, including when qosType is manual.
	// Nil or empty updateParams.QosType means "leave qosType unchanged"; only explicit QosTypeAuto means switch to auto.
	switchingToAuto := updateParams != nil && updateParams.QosType == utils.QosTypeAuto
	if pool.QosType == utils.QosTypeManual && !switchingToAuto && pool.APIAccessMode != commonparams.ONTAPMode {
		logger.Info("QoS type is manual, no modification needed for QoS policy as no QoS policy is assigned to the SVM for manual QoS type", "poolName", pool.Name)
		return nil
	}

	logger.Info("Modifying QoS policy and applying to SVM", "poolName", pool.Name, "qosType", pool.QosType, "apiAccessMode", pool.APIAccessMode)

	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting ModifyQoSPolicyAndApplyToSVM activity - pool: %s, node: %s", pool.Name, node.Name))
	// Get the provider for the node - CA fields are already in the node struct from CreateNodeForProvider()
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finding SVM for pool")
	// Find the SVM related to the pool
	svm, err := j.GetSvmForPoolID(ctx, pool.ID)
	if err != nil {
		logger.Error("Failed to get SVM for pool", "error", err, "poolID", pool.ID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if svm == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm not found for pool %d", pool.ID)))
	}

	// Construct the QoS policy name (same format as CreateQoSPolicyAndApplyToSVM)
	qosPolicyName := generateQoSPolicyName(svm.Name)

	// Get the new requirements from the update parameters, or from pool when switching to auto with nil/partial params
	newMaxThroughput := int64(0)
	newMaxIOPSVal := int64(0)
	if updateParams != nil {
		newMaxThroughput = updateParams.TotalThroughputMibps
		if updateParams.TotalIops != nil {
			newMaxIOPSVal = *updateParams.TotalIops
		}
	}
	if newMaxThroughput == 0 && pool.PoolAttributes != nil {
		newMaxThroughput = pool.PoolAttributes.ThroughputMibps
		newMaxIOPSVal = pool.PoolAttributes.Iops
	}

	// Find the existing QoS policy
	findQosPolicyParams := vsa.FindQoSGroupPolicyParams{
		Name:    qosPolicyName,
		SvmName: svm.Name,
	}

	activity.RecordHeartbeat(ctx, "Finding existing QoS policy group")
	existingQosPolicy, err := provider.FindQoSGroupPolicy(findQosPolicyParams)
	if err != nil {
		// Policy may not exist yet when switching to auto (manual→auto) or on ONTAP-mode pools
		// that always keep a vserver-wide QPG; create only when the find error is a definite "not found".
		shouldCreate := utilErrors.IsNotFoundErr(err) &&
			(switchingToAuto || pool.APIAccessMode == commonparams.ONTAPMode)
		if shouldCreate {
			logger.Info("QoS policy not found, creating and applying",
				"policyName", qosPolicyName, "switchingToAuto", switchingToAuto, "apiAccessMode", pool.APIAccessMode)
			isShared := true
			qosPolicyParams := vsa.CreateQoSGroupPolicyParams{
				Name:          qosPolicyName,
				SvmName:       svm.Name,
				MaxThroughput: newMaxThroughput,
				MaxIOPS:       newMaxIOPSVal,
				IsShared:      &isShared,
			}
			activity.RecordHeartbeat(ctx, "Creating QoS policy group")
			qosPolicyResponse, createErr := provider.CreateQoSGroupPolicy(qosPolicyParams)
			if createErr != nil {
				logger.Error("Failed to create QoS policy", "error", createErr, "policyName", qosPolicyName)
				return vsaerrors.WrapAsTemporalApplicationError(createErr)
			}
			logger.Info("QoS policy created", "policyName", qosPolicyResponse.Name, "policyUUID", qosPolicyResponse.UUID)
			activity.RecordHeartbeat(ctx, "Applying QoS policy to SVM")
			return applyQoSPolicyToSVM(ctx, svm, node, qosPolicyResponse.Name)
		}
		logger.Error("Failed to find existing QoS policy", "error", err, "policyName", qosPolicyName)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Check if the QoS policy needs to be updated
	if existingQosPolicy.MaxThroughput == newMaxThroughput && existingQosPolicy.MaxIOPS == newMaxIOPSVal {
		logger.Info("QoS policy already matches the new requirements, no update needed",
			"policyName", qosPolicyName,
			"currentThroughput", existingQosPolicy.MaxThroughput,
			"newThroughput", newMaxThroughput,
			"currentIOPS", existingQosPolicy.MaxIOPS,
			"newIOPS", newMaxIOPSVal)
		// When switching to auto, or for ONTAP-mode pools, ensure the policy is applied to the SVM.
		if switchingToAuto || pool.APIAccessMode == commonparams.ONTAPMode {
			activity.RecordHeartbeat(ctx, "Applying existing QoS policy to SVM")
			return applyQoSPolicyToSVM(ctx, svm, node, existingQosPolicy.Name)
		}
		return nil
	}

	logger.Info("QoS policy needs to be updated",
		"policyName", qosPolicyName,
		"currentThroughput", existingQosPolicy.MaxThroughput,
		"newThroughput", newMaxThroughput,
		"currentIOPS", existingQosPolicy.MaxIOPS,
		"newIOPS", newMaxIOPSVal)

	// Update the QoS policy with new values (omit Name so ONTAP does not treat it as a rename)
	updateQosPolicyParams := vsa.UpdateQoSGroupPolicyParams{
		UUID:          existingQosPolicy.UUID,
		SvmName:       existingQosPolicy.SvmName,
		MaxThroughput: newMaxThroughput,
		MaxIOPS:       newMaxIOPSVal,
	}

	activity.RecordHeartbeat(ctx, "Updating QoS policy group")
	err = provider.UpdateQoSGroupPolicy(updateQosPolicyParams)
	if err != nil {
		logger.Error("Failed to update QoS policy group", "error", err, "policyName", qosPolicyName)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy group updated successfully", "policyName", existingQosPolicy.Name, "policyUUID", existingQosPolicy.UUID)

	// Apply the updated QoS policy to the SVM using the utility function
	res := applyQoSPolicyToSVM(ctx, svm, node, existingQosPolicy.Name)
	activity.RecordHeartbeat(ctx, "Finished ModifyQoSPolicyAndApplyToSVM activity")
	return res
}

func (j *SvmActivity) GetSvmForPoolID(ctx context.Context, poolID int64) (*datamodel.Svm, error) {
	se := j.SE
	svm, err := se.GetSvmForPoolID(ctx, poolID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return svm, nil
}

// GetPOSvmForPoolID returns the PO pool SVM for a pool, or nil when no row exists
// (not-found is not an error).
func (j *SvmActivity) GetPOSvmForPoolID(ctx context.Context, poolID int64) (*datamodel.Svm, error) {
	svm, err := j.SE.GetSvmForPoolID(ctx, poolID)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) {
			return nil, nil
		}
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return svm, nil
}

// deletingSVMs updates svm status to deleting.
func _deletingSVMs(ctx context.Context, se database.Storage, pool *datamodel.Pool) error {
	// Retrieve the svms associated with the pool
	svms, err := se.GetSvmsByPoolID(ctx, pool.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return vsaerrors.NewVCPError(vsaerrors.ErrSVMNotFound, errors.New("SVM not found"))
		}
		return err
	}
	for _, svm := range svms {
		// Check if the SVM is already marked for deletion
		if svm.State == datamodel.LifeCycleStateDeleting {
			continue
		}
		if err = se.DeletingSVM(ctx, svm); err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("failed to update SVM record to deleting %s: %w", svm.Name, err))
		}
	}

	return nil
}

// deleteSVMs deletes all SVMs and their associated database records.
func _deleteSVMs(ctx context.Context, se database.Storage, pool *datamodel.Pool) error {
	// Get SVMs by pool ID
	svms, err := se.GetSvmsByPoolID(ctx, pool.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return vsaerrors.NewVCPError(vsaerrors.ErrSVMNotFound, errors.New("SVM not found"))
		}
		return err
	}

	for _, svm := range svms {
		// Delete the SVM record from the database
		if svm.DeletedAt != nil && svm.DeletedAt.Valid {
			continue
		}
		if err := se.DeleteSVM(ctx, svm); err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("failed to delete SVM record %s: %w", pool.Name, err))
		}
	}
	return nil
}

// _failedSVMs updates svm status to error.
func _failedSVMs(ctx context.Context, se database.Storage, pool *datamodel.Pool) error {
	// Retrieve the svms associated with the pool
	svms, err := se.GetSvmsByPoolID(ctx, pool.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return vsaerrors.NewVCPError(vsaerrors.ErrSVMNotFound, errors.New("SVM not found"))
		}
		return err
	}
	for _, svm := range svms {
		// Check if the SVM is already marked for deletion
		if svm.State == datamodel.LifeCycleStateDeleting {
			svm.State = datamodel.LifeCycleStateError
			svm.StateDetails = datamodel.LifeCycleStateDeletionErrorDetails
			err = se.ErroredSVM(ctx, svm, datamodel.LifeCycleStateDeletionErrorDetails)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (j *SvmActivity) AllocateSVMName(ctx context.Context, pool *datamodel.Pool) (string, error) {
	// TODO: This function currently just adds a sequence to the SVM name.
	// It will be enhanced later when multiple SVM support is added to handle
	// more sophisticated naming strategies and SVM allocation logic.
	activity.RecordHeartbeat(ctx, "Starting AllocateSVMName activity")
	if pool == nil {
		return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool must not be nil")))
	}
	se := j.SE

	activity.RecordHeartbeat(ctx, "Getting next SVM index for pool")
	// Get the next SVM index directly from the database
	nextSequence, err := se.GetNextSVMIndexByPoolID(ctx, pool.ID)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Format the sequence with leading zeros (01, 02, 03, etc.)
	sequenceStr := fmt.Sprintf("%02d", nextSequence)

	// SVM name with sequence
	svmName := fmt.Sprintf("%s-svm-%s", pool.DeploymentName, sequenceStr)

	activity.RecordHeartbeat(ctx, "Finished AllocateSVMName activity")
	return svmName, nil
}

// AllocatePOSVMName allocates an SVM name for the PO (dedicated-cluster) path
// following SDE's convention: "svm_<accountUUID>_<8 hex>" (account UUID with
// dashes removed). The postfix is deterministic per pool UUID so Temporal
// activity retries reuse the same name after partial provisioning.
func (j *SvmActivity) AllocatePOSVMName(ctx context.Context, pool *datamodel.Pool) (string, error) {
	activity.RecordHeartbeat(ctx, "Starting AllocatePOSVMName activity")
	if pool == nil {
		return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool must not be nil")))
	}
	if pool.UUID == "" {
		return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool uuid must not be empty")))
	}
	if pool.Account == nil || pool.Account.UUID == "" {
		return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool account uuid must not be empty")))
	}
	prefix := commonparams.GetSVMPrefix(pool.Account.UUID)
	postfix := commonparams.DeterministicPOSVMPostfix(pool.UUID)

	// Collect postfixes already used by SVMs on this pool so a collision is
	// detected before ONTAP create (should not happen for a given pool UUID).
	existing, err := j.SE.GetSvmsByPoolID(ctx, pool.ID)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}
	for _, svm := range existing {
		if svm == nil {
			continue
		}
		if idx := strings.LastIndex(svm.Name, "_"); idx > -1 {
			if svm.Name[idx+1:] == postfix {
				return "", vsaerrors.WrapAsTemporalApplicationError(
					fmt.Errorf("SVM name postfix %q already used for pool %s", postfix, pool.UUID))
			}
		}
	}

	svmName := prefix + postfix
	activity.RecordHeartbeat(ctx, "Finished AllocatePOSVMName activity")
	return svmName, nil
}

// SavePOSVMRecordInput is the input to SavePOSVMRecord.
type SavePOSVMRecordInput struct {
	PoolID    int64
	AccountID int64
	SvmName   string
	SvmUUID   string
	IPSpace   string
}

// SavePOSVMRecord persists the PO SVM row in the svms table after ONTAP creation.
func (j *SvmActivity) SavePOSVMRecord(ctx context.Context, in SavePOSVMRecordInput) (*datamodel.Svm, error) {
	activity.RecordHeartbeat(ctx, "Starting SavePOSVMRecord activity")
	if in.PoolID == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool id must not be zero")))
	}
	if in.AccountID == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("account id must not be zero")))
	}
	if strings.TrimSpace(in.SvmName) == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm name must not be empty")))
	}
	if j.SE == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("storage is not configured for SVM activities")))
	}

	svmRec := &datamodel.Svm{
		Name:      in.SvmName,
		AccountID: in.AccountID,
		PoolID:    in.PoolID,
		SvmDetails: &datamodel.SvmDetails{
			ExternalUUID: in.SvmUUID,
			IPSpace:      in.IPSpace,
		},
	}
	createdSvm, err := j.SE.CreateSVM(ctx, svmRec)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished SavePOSVMRecord activity")
	return createdSvm, nil
}

// MarkSvmDeleting transitions an SVM to DELETING.
func (j *SvmActivity) MarkSvmDeleting(ctx context.Context, svm *datamodel.Svm) error {
	activity.RecordHeartbeat(ctx, "Marking SVM as DELETING")
	if svm == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm must not be nil")))
	}
	if err := j.SE.DeletingSVM(ctx, svm); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// SoftDeleteSvm soft-deletes an SVM (DELETED + DeletedAt). Expected to run after MarkSvmDeleting.
func (j *SvmActivity) SoftDeleteSvm(ctx context.Context, svm *datamodel.Svm) error {
	activity.RecordHeartbeat(ctx, "Soft-deleting SVM")
	if svm == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm must not be nil")))
	}
	if err := j.SE.DeleteSVM(ctx, svm); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// MarkSvmAsErroredForDeletion is the deletion-flow rollback activity:
// it transitions the SVM to the ERROR state and records errMessage as the failure reason.
func (j *SvmActivity) MarkSvmAsErroredForDeletion(ctx context.Context, svm *datamodel.Svm, errMessage string) error {
	activity.RecordHeartbeat(ctx, "Rolling back SVM DELETING state to ERROR")
	if svm == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm must not be nil")))
	}
	if errMessage == "" {
		errMessage = datamodel.LifeCycleStateDeletionErrorDetails
	}
	if err := j.SE.ErroredSVM(ctx, svm, errMessage); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// CreateSvmInCreatingState pre-allocates the SVM DB row in CREATING r
func (j *SvmActivity) CreateSvmInCreatingState(ctx context.Context, pool *datamodel.Pool, svmName, svmExternalIdentifier string) (*datamodel.Svm, error) {
	activity.RecordHeartbeat(ctx, "Starting CreateSvmInCreatingState activity")
	if pool == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool must not be nil")))
	}
	if svmName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svmName must not be empty")))
	}
	svmRec := &datamodel.Svm{
		Name:                  svmName,
		SvmExternalIdentifier: strings.TrimSpace(svmExternalIdentifier),
		AccountID:             pool.AccountID,
		PoolID:                pool.ID,
	}
	createdSvm, err := j.SE.CreateSvmInCreatingState(ctx, svmRec)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished CreateSvmInCreatingState activity")
	return createdSvm, nil
}

// MarkSvmAsErroredForCreation is the rollback .it moves an SVM from CREATING to ERROR.
func (j *SvmActivity) MarkSvmAsErroredForCreation(ctx context.Context, svm *datamodel.Svm, errMessage string) error {
	activity.RecordHeartbeat(ctx, "Rolling back SVM CREATING state to ERROR")
	if svm == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm must not be nil")))
	}
	if errMessage == "" {
		errMessage = datamodel.LifeCycleStateCreationErrorDetails
	}
	if err := j.SE.ErroredSVM(ctx, svm, errMessage); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// TransitionSvmState transitions an SVM to the given lifecycle state, using a sensible default for stateDetails when empty; it is rollback-manager compatible.
func (j *SvmActivity) TransitionSvmState(ctx context.Context, svm *datamodel.Svm, targetState string, stateDetails string) error {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Transitioning SVM to %s state", targetState))
	if svm == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("svm must not be nil")))
	}
	if stateDetails == "" {
		stateDetails = defaultSvmStateDetails(targetState)
	}
	if err := j.SE.UpdateSvmState(ctx, svm, targetState, stateDetails); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// defaultSvmStateDetails returns the default state-details string for a given target state.
func defaultSvmStateDetails(targetState string) string {
	switch targetState {
	case datamodel.LifeCycleStateError:
		return datamodel.LifeCycleStateUpdateErrorDetails
	case datamodel.LifeCycleStateREADY:
		return datamodel.LifeCycleStateReadyDetails
	default:
		return ""
	}
}
