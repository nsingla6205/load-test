package oci

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

const (
	minNodesForSVM = 2
)

// SvmNameRegex: alphanumeric, hyphen, underscore only (ONTAP-compatible).
var svmNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ocidRegex matches a structurally valid OCI OCID.
// Format: ocid1.<resource>.<realm>.[region][.future].<uniqueID>
var ocidRegex = regexp.MustCompile(`^ocid1\.[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]*(\.[a-z0-9_-]*)?\.[a-z0-9_-]+$`)

func isValidOCID(s string) bool {
	return ocidRegex.MatchString(strings.TrimSpace(s))
}

// requireValidOCID returns a BadRequest error if the value is empty or not a structurally valid OCID.
func requireValidOCID(value, fieldName string) error {
	if strings.TrimSpace(value) == "" {
		return customerrors.NewBadRequestErr(fieldName + " is required")
	}
	if !isValidOCID(value) {
		return customerrors.NewBadRequestErr(fieldName + " is not a valid OCID")
	}
	return nil
}

// validateCreateSvmParams checks that all required OCI-specific input fields are present.
func validateCreateSvmParams(params *commonparams.CreateSvmParams) error {
	if params.PoolOCID == "" {
		return customerrors.NewBadRequestErr("PoolOCID is required")
	}
	if params.SvmExternalIdentifier == "" {
		return customerrors.NewBadRequestErr("SvmOCID is required")
	}
	if params.Name == "" {
		return customerrors.NewBadRequestErr("Name is required")
	}
	if strings.TrimSpace(params.AccountName) == "" {
		return customerrors.NewBadRequestErr("Tenancy-Ocid is required")
	}
	return nil
}

// validateUpdateSvmParams checks that all required input fields for SVM update are present and well-formed.
func validateUpdateSvmParams(params *commonparams.UpdateSvmParams) error {
	if err := requireValidOCID(params.PoolOCID, "PoolOCID"); err != nil {
		return err
	}
	if err := requireValidOCID(params.SvmExternalIdentifier, "svmOCID"); err != nil {
		return err
	}
	if err := requireValidOCID(params.AccountName, "Tenancy-Ocid"); err != nil {
		return err
	}
	if params.SvmAdminPassword == nil {
		return customerrors.NewBadRequestErr("svmAdminPassword is required")
	}
	if err := requireValidOCID(params.SvmAdminPassword.Ocid, "svmAdminPassword.ocid"); err != nil {
		return err
	}
	return nil
}

// validatePoolStateForSvmUpdate ensures the pool is in a state that allows SVM updates.
func validatePoolStateForSvmUpdate(poolState string) error {
	switch poolState {
	case datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateError:
		return nil
	case datamodel.LifeCycleStateDeleted:
		return customerrors.NewNotFoundErr("pool is deleted", nil)
	case datamodel.LifeCycleStateDisabled:
		return customerrors.NewConflictErr("SVM cannot be updated while pool is disabled")
	case datamodel.LifeCycleStateCreating,
		datamodel.LifeCycleStateUpdating,
		datamodel.LifeCycleStateDeleting,
		datamodel.LifeCycleStatePreparing,
		datamodel.LifeCycleStateMigrating:
		return customerrors.NewConflictErr(fmt.Sprintf("SVM cannot be updated: pool operation is in progress (state=%s)", poolState))
	default:
		return customerrors.NewConflictErr(fmt.Sprintf("SVM cannot be updated: pool is in state %s", poolState))
	}
}

// validateSvmUpdateState ensures the SVM is in a state that allows update.
func validateSvmUpdateState(svm *datamodel.Svm) error {
	switch svm.State {
	case datamodel.LifeCycleStateDeleted:
		return customerrors.NewNotFoundErr("svm deleted already", nil)
	case datamodel.LifeCycleStateDeleting:
		return customerrors.NewConflictErr("SVM cannot be updated while deletion is in progress")
	case datamodel.LifeCycleStateCreating:
		return customerrors.NewConflictErr("SVM cannot be updated while creation is in progress")
	case datamodel.LifeCycleStateUpdating:
		return customerrors.NewConflictErr("SVM update is already in progress")
	}
	return nil
}

// validateDeleteSvmParams checks that all required input fields for SVM deletion are present.
func validateDeleteSvmParams(params *commonparams.DeleteSvmParams) error {
	if params.SvmID == "" {
		return customerrors.NewBadRequestErr("svmOCID is required")
	}
	if params.AccountName == "" {
		return customerrors.NewBadRequestErr("Tenancy-Ocid is required")
	}
	if strings.TrimSpace(params.PoolOCID) == "" {
		return customerrors.NewBadRequestErr("PoolOCID is required")
	}
	return nil
}

// validateCreateSvm runs all pre-create checks: required params, cluster state/capacity, SVM name convention,
// (data LIFs per node based on protocols).
func validateCreateSvm(ctx context.Context, se database.Storage, params *commonparams.CreateSvmParams, pool *datamodel.Pool) error {
	if err := validateCreateSvmClusterStateAndCapacity(ctx, se, pool); err != nil {
		return err
	}
	if err := validateSvmName(params.Name); err != nil {
		return err
	}
	if err := validateSvmNameNotInUseInPool(ctx, se, params.Name, pool.ID); err != nil {
		return err
	}
	return validateCreateSvmIPRequirements(ctx, se, params, pool)
}

// validateSvmNameNotInUseInPool rejects creation when a non-DELETED SVM with the same name already exists in the same pool.
func validateSvmNameNotInUseInPool(ctx context.Context, se database.Storage, name string, poolID int64) error {
	existing, err := se.GetSvmByNameAndPoolID(ctx, name, poolID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil
		}
		return err
	}
	if existing.State == datamodel.LifeCycleStateDeleted {
		return nil
	}
	return customerrors.NewConflictErr(fmt.Sprintf("svm with name %q already exists in this pool", name))
}

// validateCreateSvmClusterStateAndCapacity ensures the cluster (pool) is in a valid state and has capacity for a new SVM.
func validateCreateSvmClusterStateAndCapacity(ctx context.Context, se database.Storage, pool *datamodel.Pool) error {
	if pool.State != string(datamodel.LifeCycleStateREADY) {
		return customerrors.NewConflictErr("pool is not available for SVM creation")
	}
	if pool.VLMConfig == "" {
		return customerrors.NewUserInputValidationErr("pool does not have cluster config; ensure pool is available")
	}

	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return err
	}
	if len(nodes) < minNodesForSVM {
		return customerrors.NewUserInputValidationErr(fmt.Sprintf("cluster must have at least %d nodes to create an SVM", minNodesForSVM))
	}
	for _, node := range nodes {
		if node.State != datamodel.LifeCycleStateREADY && node.State != datamodel.LifeCycleStateAvailable {
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("node %s is not ready (state: %s)", node.Name, node.State))
		}
	}
	return nil
}

func validateSvmName(name string) error {
	if name == "" {
		return customerrors.NewUserInputValidationErr("SVM name is required")
	}
	if !svmNameRegex.MatchString(name) {
		return customerrors.NewUserInputValidationErr("SVM name must contain only letters, numbers, hyphens, and underscores")
	}
	return nil
}

// requiredDataLifCount returns the number of data LIFs (IPs) needed for the new SVM based on protocols and node count.
// 1 data LIF per node for SAN (iSCSI); 1 per node for NAS (NFS). So e.g. 2 nodes + iSCSI+NFS = 4 LIFs.
func requiredDataLifCount(enableIscsi, enableNfs bool, nodeCount int) int {
	if nodeCount <= 0 {
		return 0
	}
	lifsPerNode := 0
	if enableIscsi {
		lifsPerNode++
	}
	if enableNfs {
		lifsPerNode++
	}
	if lifsPerNode == 0 {
		lifsPerNode = 1 // default at least one data path (e.g. iSCSI)
	}
	return lifsPerNode * nodeCount
}

// validateCreateSvmIPRequirements checks that if Ips are provided, the count matches required data LIFs.
func validateCreateSvmIPRequirements(ctx context.Context, se database.Storage, params *commonparams.CreateSvmParams, pool *datamodel.Pool) error {
	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return err
	}
	required := requiredDataLifCount(params.EnableIscsi, params.EnableNfs, len(nodes))
	if len(params.Ips) == 0 {
		return nil // no IPs provided; allocation may happen later or elsewhere
	}
	if len(params.Ips) != required {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("Ips count must be %d (data LIFs for %d nodes with selected protocols), got %d",
				required, len(nodes), len(params.Ips)))
	}
	return nil
}
