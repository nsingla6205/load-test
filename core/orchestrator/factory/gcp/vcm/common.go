package vcm

import (
	"context"
	"errors"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
)

var getProviderByNode = vsa.GetProviderByNode

// jobStepFailedToStart is the job step recorded when workflow dispatch fails before the job leaves NEW.
const jobStepFailedToStart = "failed-to-start"

// createJobOrGet creates the job or returns alreadyExists=true when the same operation ID is present.
func createJobOrGet(ctx context.Context, store Store, job *vcmdb.Job) (bool, error) {
	if err := store.CreateJob(ctx, job); err != nil {
		if !errors.Is(err, vcmdb.ErrJobAlreadyExists) {
			return false, fmt.Errorf("persist job: %w", err)
		}
		if _, getErr := store.GetJob(ctx, job.ID); getErr != nil {
			return false, fmt.Errorf("get existing job: %w", getErr)
		}
		return true, nil
	}
	return false, nil
}

// nodeAndSvmFromStore builds the workflow node and deployment SVM name from VCM config.
func nodeAndSvmFromStore(store Store) (*models.Node, string, error) {
	input, err := store.NodeProviderInput()
	if err != nil {
		return nil, "", fmt.Errorf("build ONTAP node: %w", err)
	}
	node := vsa.CreateNodeForProvider(input)
	// VCM stores credentials as fully qualified Secret Manager resource names,
	// but vsa.GetProviderByNode re-prefixes projects/{p}/secrets/ around the
	// node identifiers. Reduce to the bare secret id so the resulting lookup is
	// projects/{p}/secrets/{id} rather than a doubled path that 404s.
	node.SecretID = utils.BareSecretName(node.SecretID)
	node.CertificateID = utils.BareSecretName(node.CertificateID)
	svmName, err := orchcommon.DeriveVCMSvmName(node.DeploymentName)
	if err != nil {
		return nil, "", fmt.Errorf("derive SVM name: %w", err)
	}
	return node, svmName, nil
}

// ontapProviderFromStore builds an ONTAP provider for the deployment SVM and verifies the SVM exists.
func ontapProviderFromStore(ctx context.Context, store Store) (string, vsa.Provider, error) {
	node, svmName, err := nodeAndSvmFromStore(store)
	if err != nil {
		return "", nil, err
	}
	provider, err := getProviderByNode(ctx, node)
	if err != nil {
		return "", nil, fmt.Errorf("create ONTAP provider: %w", err)
	}
	if _, err := provider.GetSVM(vsa.GetSvmParams{Name: svmName}); err != nil {
		return "", nil, fmt.Errorf("resolve SVM: %w", err)
	}
	return svmName, provider, nil
}
