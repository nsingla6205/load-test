package backgroundactivities

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// AuthType1PoolClassification identifies whether a password-auth pool was
// created natively or was demoted from certificate authentication.
type AuthType1PoolClassification string

const (
	AuthType1PoolNative           AuthType1PoolClassification = "native"
	AuthType1PoolDemoted          AuthType1PoolClassification = "demoted"
	demotionFallbackAdminUsername                             = "admin"
)

func isDemotedExpertCredential(credential *datamodel.ExpertModeCredential) bool {
	if credential == nil || credential.AuthType != env.USERNAME_PWD_SEC_MGR {
		return false
	}
	if credential.UsernamePreCertFix != "" {
		return true
	}
	if _, inferred := inferDemotedExpertCertificateUsername(credential); inferred {
		return true
	}
	// Some production demotion rows preserved the original expert username and
	// certificate IDs but predate username_pre_cert_fix. The certificate ID plus
	// deployment-scoped _gadmin identity is sufficient certificate-era evidence.
	return credential.CertificateID != "" &&
		credential.Username != expertDemotionFallbackUsername &&
		strings.HasSuffix(credential.Username, "_"+env.ExpertModeUserSuffix)
}

func expertCertificateUsername(credential *datamodel.ExpertModeCredential) string {
	if credential == nil {
		return ""
	}
	if isDemotedExpertCredential(credential) {
		if credential.UsernamePreCertFix != "" {
			return credential.UsernamePreCertFix
		}
		if inferredUsername, inferred := inferDemotedExpertCertificateUsername(credential); inferred {
			return inferredUsername
		}
		return credential.Username
	}
	return credential.Username
}

func inferDemotedExpertCertificateUsername(credential *datamodel.ExpertModeCredential) (string, bool) {
	if credential == nil {
		return "", false
	}
	for _, certificateID := range []string{credential.CertificateID, credential.CertificateIDNew} {
		username, inferred := expertUsernameFromCertificateID(certificateID)
		if inferred {
			return username, true
		}
	}
	return "", false
}

func expertUsernameFromCertificateID(certificateID string) (string, bool) {
	if _, timestamped := certificateIDTimestamp(certificateID); !timestamped {
		return "", false
	}
	const certificateMarker = "-cert-"
	const formattedTimestampLength = len(rotatedCertificateIDTimeFormat)
	idWithoutTimestamp := certificateID[:len(certificateID)-formattedTimestampLength-1]
	markerIndex := strings.LastIndex(idWithoutTimestamp, certificateMarker)
	if markerIndex < 0 {
		return "", false
	}
	username := idWithoutTimestamp[markerIndex+len(certificateMarker):]
	expertSuffix := "_" + env.ExpertModeUserSuffix
	usernamePrefix := strings.TrimSuffix(username, expertSuffix)
	if len(usernamePrefix) != 8 ||
		username == expertDemotionFallbackUsername ||
		!strings.HasSuffix(username, expertSuffix) {
		return "", false
	}
	if _, err := hex.DecodeString(usernamePrefix); err != nil {
		return "", false
	}
	return username, true
}

func inferDemotedParentCertificateUsername(pool *datamodel.Pool) (string, error) {
	if pool == nil || pool.ExpertModeCredentials == nil {
		return "", errors.New("no expert credentials are available for username inference")
	}
	inferredUsernames := make(map[string]struct{})
	for _, credential := range pool.ExpertModeCredentials.ExpertModeCredential {
		if credential == nil ||
			credential.AuthType != env.USERNAME_PWD_SEC_MGR ||
			credential.Username != expertDemotionFallbackUsername ||
			credential.UsernamePreCertFix != "" {
			continue
		}
		expertUsername, inferred := inferDemotedExpertCertificateUsername(credential)
		if !inferred {
			continue
		}
		expertSuffix := "_" + env.ExpertModeUserSuffix
		parentUsername := strings.TrimSuffix(expertUsername, expertSuffix) + "_" + demotionFallbackAdminUsername
		inferredUsernames[parentUsername] = struct{}{}
	}
	if len(inferredUsernames) == 0 {
		return "", errors.New("no expert certificate ID contains an inferable username")
	}
	if len(inferredUsernames) > 1 {
		return "", errors.New("expert certificate IDs contain conflicting usernames")
	}
	for username := range inferredUsernames {
		return username, nil
	}
	return "", errors.New("no expert certificate ID contains an inferable username")
}

func classifyAuthType1Pool(pool *datamodel.Pool) AuthType1PoolClassification {
	if common.ClassifyAuthType1Pool(pool) == common.AuthType1PoolDemoted {
		return AuthType1PoolDemoted
	}
	return AuthType1PoolNative
}

func authReconciliationUsernames(pool *datamodel.Pool) (bootstrapUsername, certificateUsername string, err error) {
	bootstrapUsername = demotionFallbackAdminUsername
	if pool == nil || pool.PoolCredentials == nil {
		return bootstrapUsername, "", errors.New("pool credentials are required")
	}
	credentials := pool.PoolCredentials
	if credentials.Username != "" {
		bootstrapUsername = credentials.Username
	}
	if credentials.Username == demotionFallbackAdminUsername && credentials.UsernamePreCertFix == "" {
		inferredUsername, inferErr := inferDemotedParentCertificateUsername(pool)
		if inferErr != nil {
			return "", "", fmt.Errorf(
				"demoted admin fallback is missing username_pre_cert_fix and inference failed: %w",
				inferErr,
			)
		}
		return bootstrapUsername, inferredUsername, nil
	}
	certificateUsername = credentials.UsernamePreCertFix
	if certificateUsername == "" {
		certificateUsername = credentials.Username
	}
	if certificateUsername == "" {
		certificateUsername = fmt.Sprintf("%s_admin", pool.DeploymentName)
	}
	return bootstrapUsername, certificateUsername, nil
}

func validateAuthType1ReconciliationPool(
	pool *datamodel.Pool,
	poolUUID, targetCaURI string,
) (alreadyPromoted, skip bool, err error) {
	if pool == nil || pool.PoolCredentials == nil {
		return false, false, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrPoolCredentialsMissing, fmt.Errorf("pool %s has no credentials", poolUUID)))
	}
	if pool.IsDedicatedCapacity() {
		return false, true, nil
	}
	if pool.PoolCredentials.AuthType != env.USERNAME_PWD_SEC_MGR {
		if pool.PoolCredentials.AuthType == env.USER_CERTIFICATE &&
			pool.PoolCredentials.CertificateID != "" &&
			pool.PoolCredentials.CaURI == targetCaURI {
			return true, false, nil
		}
		return false, false, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("pool %s auth type changed to %d during reconciliation", poolUUID, pool.PoolCredentials.AuthType)))
	}
	if !isAuthReconciliationStateEligible(pool.State) {
		return false, false, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("pool %s state %q is not eligible for auth reconciliation", poolUUID, pool.State)))
	}
	if classifyAuthType1Pool(pool) != AuthType1PoolDemoted {
		return false, true, nil
	}
	if targetCaURI == "" {
		return false, false, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, fmt.Errorf("desired certificate CA URI is empty")))
	}
	return false, false, nil
}

// ReconcileAuthType1Pool safely promotes one password-auth pool to certificate
// authentication. The activity owns all key material; Temporal history only
// contains the pool UUID and activity result.
func (a *RotateVcpToVsaCertificateActivity) ReconcileAuthType1Pool(ctx context.Context, poolUUID string) error {
	return a.reconcileAuthType1Pool(ctx, poolUUID, defaultCertificateCaURI())
}

// ReconcileAuthType1PoolForDesiredCA reconciles one password-auth pool against
// the immutable desired CA carried in Temporal history.
func (a *RotateVcpToVsaCertificateActivity) ReconcileAuthType1PoolForDesiredCA(
	ctx context.Context,
	poolUUID, desiredCAURI string,
) error {
	if err := validateExplicitDesiredCAURI(desiredCAURI); err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err),
		)
	}
	return a.reconcileAuthType1Pool(ctx, poolUUID, desiredCAURI)
}

func (a *RotateVcpToVsaCertificateActivity) reconcileAuthType1Pool(
	ctx context.Context,
	poolUUID, targetCaURI string,
) error {
	safeRecordHeartbeat(ctx, "Starting auth type reconciliation")
	logger := util.GetLogger(ctx)
	logger.Info("AuthType 1 to 2 reconciliation activity started",
		"poolUUID", poolUUID,
		"targetCaURI", targetCaURI,
	)

	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	pool := poolContext.Pool
	alreadyPromoted, skip, err := validateAuthType1ReconciliationPool(pool, poolUUID, targetCaURI)
	if err != nil {
		return err
	}
	if alreadyPromoted {
		return vsaerrors.WrapAsTemporalApplicationError(
			a.refreshPromotedCertificateCache(ctx, pool.PoolCredentials.CertificateID, targetCaURI, pool.PoolCredentials.CertificateIDNew))
	}
	if skip {
		logger.Info("Skipping pool auth reconciliation", "poolUUID", poolUUID)
		return nil
	}
	classification := classifyAuthType1Pool(pool)
	logger.Info("Classified auth_type=1 pool", "poolUUID", poolUUID, "classification", classification)
	logger.Info("Demoted AuthType 1 pool is eligible for promotion",
		"poolUUID", poolUUID,
		"targetCaURI", targetCaURI,
	)

	nodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err))
	}
	if len(nodes) == 0 {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrPoolHasNoNodes, fmt.Errorf("pool %s has no nodes", poolUUID)))
	}

	if err := a.ensureAuthReconciliationDNS(ctx, pool, nodes); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	nodes, err = a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err))
	}

	cloudService, err := a.dependencies().resolveCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err))
	}
	bootstrapUsername, certificateUsername, err := authReconciliationUsernames(pool)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrPoolCredentialsMissing, err),
		)
	}
	if err := a.preflightCertificateIssuance(
		ctx,
		pool,
		targetCaURI,
		bootstrapUsername,
		certificateUsername,
	); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrCertificateGenerationFailed, fmt.Errorf("parent credential preflight: %w", err)))
	}
	logger.Info("AuthType promotion preflight completed",
		"poolUUID", poolUUID,
		"certificateUsername", certificateUsername,
	)
	stagedCertificateID := pool.PoolCredentials.CertificateIDNew
	stagedCaURI := pool.PoolCredentials.StagedCaURI
	activeCaURI := pool.PoolCredentials.CaURI
	if inferredCAURI, inferred := inferIssuerForNewerLegacyStagedCert(
		pool.PoolCredentials.CertificateID,
		stagedCertificateID,
		stagedCaURI,
		activeCaURI,
		targetCaURI,
	); inferred {
		if err := a.stageAuthReconciliationCertificate(
			ctx,
			poolUUID,
			stagedCertificateID,
			inferredCAURI,
			stagedCertificateID,
			stagedCaURI,
		); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(
					vsaerrors.ErrCertificateStagingFailed,
					fmt.Errorf("backfill legacy staged certificate CA URI: %w", err),
				),
			)
		}
		stagedCaURI = inferredCAURI
		pool.PoolCredentials.StagedCaURI = inferredCAURI
	}
	reuseStagedCertificate := shouldReuseAuthReconciliationCertificate(
		stagedCertificateID,
		stagedCaURI,
		targetCaURI,
	)

	if stagedCertificateID != "" && !reuseStagedCertificate {
		if err := a.rollbackAuthReconciliationStage(ctx, poolUUID, stagedCertificateID, stagedCaURI); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrCertificateStagingFailed, err))
		}
		cleanupCaURI := stagedCaURI
		if cleanupCaURI == "" {
			// A production legacy row can contain certificate_id_new without
			// staged_ca_uri. It cannot be reused across a CA change, but the active
			// issuer is the only safe location in which to attempt cleanup.
			cleanupCaURI = activeCaURI
		}
		if cleanupCaURI != "" {
			stagedCredentials := &datamodel.PoolCredentials{CertificateID: stagedCertificateID, CaURI: cleanupCaURI}
			if err := revokeCertificateAndDeleteFromCacheAndSecretManager(cloudService, stagedCredentials); err != nil {
				util.GetLogger(ctx).Warnf("Failed to revoke replaced staged certificate %s: %v", stagedCertificateID, err)
			}
		}
		stagedCertificateID = ""
		stagedCaURI = ""
	}

	caProjectID, caPoolName, _ := env.ParseCaURI(targetCaURI)
	var certificateResponse *hyperscalermodels.CustomCertificateResponse
	if reuseStagedCertificate {
		certificateResponse, err = a.fetchValidatedAuthReconciliationCandidate(
			cloudService,
			targetCaURI,
			stagedCertificateID,
		)
		if err != nil {
			invalidStagedID := stagedCertificateID
			if detachErr := a.rollbackAuthReconciliationStage(
				ctx,
				poolUUID,
				stagedCertificateID,
				stagedCaURI,
			); detachErr != nil {
				return vsaerrors.WrapAsTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrCertificateStagingFailed, detachErr),
				)
			}
			invalidCredentials := &datamodel.PoolCredentials{
				CertificateID: invalidStagedID,
				CaURI:         stagedCaURI,
			}
			if revokeErr := revokeCertificateAndDeleteFromCacheAndSecretManager(
				cloudService,
				invalidCredentials,
			); revokeErr != nil {
				logger.Warnf("Failed to revoke detached invalid auth reconciliation certificate %s: %v", invalidStagedID, revokeErr)
			}
			stagedCertificateID = ""
			stagedCaURI = ""
			reuseStagedCertificate = false
			certificateResponse = nil
		}
	}

	newCertificateID := stagedCertificateID
	if newCertificateID == "" {
		newCertificateID = rotatedCertificateID(pool.DeploymentName, a.dependencies().now())
	}
	newCredentials := &datamodel.PoolCredentials{CertificateID: newCertificateID, CaURI: targetCaURI}
	if !reuseStagedCertificate {
		if _, err = generateAndCreateCertificateForVSACluster(cloudService, pool.DeploymentName, certificateUsername, newCredentials, true); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCertificateGenerationFailed, err))
		}
		if err = a.stageAuthReconciliationCertificate(
			ctx,
			poolUUID,
			newCertificateID,
			targetCaURI,
			stagedCertificateID,
			stagedCaURI,
		); err != nil {
			_ = revokeCertificateAndDeleteFromCacheAndSecretManager(cloudService, newCredentials)
			return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCertificateStagingFailed, err))
		}
		logger.Info("Staged AuthType promotion certificate",
			"poolUUID", poolUUID,
			"certificateID", newCertificateID,
			"targetCaURI", targetCaURI,
		)
	} else {
		logger.Info("Reusing staged AuthType promotion certificate",
			"poolUUID", poolUUID,
			"certificateID", newCertificateID,
			"targetCaURI", targetCaURI,
		)
	}

	if certificateResponse == nil {
		certificateResponse, err = a.dependencies().getCertificateAndPrivateKeyByID(
			cloudService,
			caProjectID,
			env.SecretManagerProjectID,
			env.Region,
			caPoolName,
			newCertificateID,
		)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	pool.PoolCredentials.CertificateIDNew = newCertificateID
	pool.PoolCredentials.StagedCaURI = targetCaURI
	if err = a.installCertificateOnVSAWithPasswordAuth(ctx, pool, certificateResponse.Certificate, certificateResponse.Secret); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCertificateInstallationFailed, err))
	}
	logger.Info("Installed AuthType promotion certificate using password authentication",
		"poolUUID", poolUUID,
		"certificateID", newCertificateID,
	)
	if err = a.preparePoolCertificateAuthentication(
		ctx,
		pool,
		nodes,
		bootstrapUsername,
		certificateUsername,
	); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCertificateInstallationFailed, err))
	}

	verificationPool := *pool
	verificationCredentials := *pool.PoolCredentials
	verificationCredentials.CertificateID = newCertificateID
	verificationCredentials.CertificateIDNew = ""
	verificationCredentials.CaURI = targetCaURI
	verificationCredentials.StagedCaURI = ""
	verificationCredentials.AuthType = env.USER_CERTIFICATE
	verificationCredentials.Username = certificateUsername
	verificationPool.PoolCredentials = &verificationCredentials
	if err = a.testCertificateConnectivity(ctx, &verificationPool, certificateResponse.Certificate); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCertificateConnectivityTestFailed, err))
	}
	logger.Info("Verified AuthType promotion certificate connectivity",
		"poolUUID", poolUUID,
		"certificateID", newCertificateID,
		"certificateUsername", certificateUsername,
	)

	if hasExpertModeCredentials(pool) {
		logger.Info("Reconciling expert-mode credentials before pool AuthType promotion", "poolUUID", poolUUID)
		if err := a.reconcileExpertModeCredentialsForAuthType(ctx, poolUUID, targetCaURI); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrCertificateRotationFailed, fmt.Errorf("expert credential reconciliation: %w", err)))
		}
		logger.Info("Expert-mode credential reconciliation completed", "poolUUID", poolUUID)
	}

	logger.Info("Promoting pool credentials to AuthType 2",
		"poolUUID", poolUUID,
		"certificateID", newCertificateID,
		"certificateUsername", certificateUsername,
	)
	oldCertificateID, err := a.promoteAuthReconciliationCertificate(
		ctx,
		poolUUID,
		newCertificateID,
		targetCaURI,
		certificateUsername,
	)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCertificateIDSwapFailed, err))
	}
	if err = a.refreshPromotedCertificateCache(ctx, newCertificateID, targetCaURI, oldCertificateID); err != nil {
		// Promotion is intentionally not reverted. A Temporal retry detects the
		// promoted pair above and retries only this explicit cache refresh.
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Info("Pool credentials promoted to AuthType 2",
		"poolUUID", poolUUID,
		"certificateID", newCertificateID,
		"certificateUsername", certificateUsername,
	)
	safeRecordHeartbeat(ctx, "Completed auth type reconciliation")
	return nil
}

func shouldReuseAuthReconciliationCertificate(certificateID, stagedCaURI, targetCaURI string) bool {
	return certificateID != "" && stagedCaURI == targetCaURI
}

func (a *RotateVcpToVsaCertificateActivity) fetchValidatedAuthReconciliationCandidate(
	cloudService hyperscaler2.Services,
	targetCAURI, certificateID string,
) (*hyperscalermodels.CustomCertificateResponse, error) {
	projectID, poolName, _ := env.ParseCaURI(targetCAURI)
	response, err := a.dependencies().getCertificateAndPrivateKeyByID(
		cloudService,
		projectID,
		env.SecretManagerProjectID,
		env.Region,
		poolName,
		certificateID,
	)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("staged auth reconciliation certificate lookup returned nil response")
	}
	if err := validateCertificateForReuse(
		response.Certificate,
		targetCAURI,
		env.Region,
		a.dependencies().now(),
		env.GetCertificateRotationThresholdPercentage(),
	); err != nil {
		return nil, err
	}
	return response, nil
}

func (a *RotateVcpToVsaCertificateActivity) compareAndSwapAuthReconciliationCredentials(
	ctx context.Context,
	poolUUID, expectedCertificateIDNew, expectedStagedCaURI string,
	credentials *datamodel.PoolCredentials,
) error {
	return a.SE.CompareAndSwapPoolCredentials(
		ctx,
		poolUUID,
		env.USERNAME_PWD_SEC_MGR,
		expectedCertificateIDNew,
		expectedStagedCaURI,
		env.GetCertificateRotationPoolStates(),
		credentials,
	)
}

func isAuthReconciliationStateEligible(state string) bool {
	for _, eligibleState := range env.GetCertificateRotationPoolStates() {
		if state == eligibleState {
			return true
		}
	}
	return false
}

func (a *RotateVcpToVsaCertificateActivity) ensureAuthReconciliationDNS(ctx context.Context, pool *datamodel.Pool, nodes []*datamodel.Node) error {
	hostNamesByEndpoint := make(map[string]string)
	sortedNodes := append([]*datamodel.Node(nil), nodes...)
	sort.Slice(sortedNodes, func(i, j int) bool {
		if sortedNodes[i] == nil {
			return false
		}
		if sortedNodes[j] == nil {
			return true
		}
		return sortedNodes[i].Name < sortedNodes[j].Name
	})
	cloudService, err := getGcpServiceForCerts(ctx)
	if err != nil {
		return err
	}
	for index, node := range sortedNodes {
		if node == nil || node.EndpointAddress == "" {
			return fmt.Errorf("pool %s has a node without an endpoint address", pool.UUID)
		}
		if node.HostDNSName != "" && node.HostDNSName != node.EndpointAddress {
			hostNamesByEndpoint[node.EndpointAddress] = node.HostDNSName
			continue
		}
		recordName := fmt.Sprintf("dns-%d.%s.%s.", index+1, pool.DeploymentName, env.VsaDeployedDnsName)
		record, err := vsa.GetOrCreateCloudDNSRecord(cloudService, recordName, node.EndpointAddress)
		if err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
		}
		hostNamesByEndpoint[node.EndpointAddress] = record.RecordName
		node.HostDNSName = record.RecordName
	}
	return a.SE.UpdateNodeHostDNSNames(ctx, pool.ID, hostNamesByEndpoint)
}

func (a *RotateVcpToVsaCertificateActivity) preparePoolCertificateAuthentication(
	ctx context.Context,
	pool *datamodel.Pool,
	nodes []*datamodel.Node,
	bootstrapUsername, certificateUsername string,
) error {
	password := pool.PoolCredentials.Password
	if password == "" {
		common.RemoveFromUserAuthCache(pool.PoolCredentials.SecretID)
		var err error
		password, err = vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretID)
		if err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
		}
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:          nodes,
		DeploymentName: pool.DeploymentName,
		OntapCredentials: &datamodel.PoolCredentials{
			Password: password,
			SecretID: pool.PoolCredentials.SecretID,
			AuthType: env.USERNAME_PWD_SEC_MGR,
			Username: bootstrapUsername,
		},
	})
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return fmt.Errorf("create password-auth provider for certificate account preparation: %w", err)
	}
	return ensureAndVerifyCertificateLogin(provider, certificateUsername)
}

func (a *RotateVcpToVsaCertificateActivity) stageAuthReconciliationCertificate(
	ctx context.Context,
	poolUUID, certificateID, caURI, expectedCertificateIDNew, expectedStagedCaURI string,
) error {
	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return err
	}
	if poolContext.Pool == nil || poolContext.Pool.PoolCredentials == nil {
		return fmt.Errorf("pool %s has no credentials before staging", poolUUID)
	}
	if poolContext.Pool.PoolCredentials.AuthType != env.USERNAME_PWD_SEC_MGR {
		return fmt.Errorf("pool %s auth type changed before staging", poolUUID)
	}
	if !isAuthReconciliationStateEligible(poolContext.Pool.State) {
		return fmt.Errorf("pool %s state changed to %q before staging", poolUUID, poolContext.Pool.State)
	}
	if poolContext.Pool.PoolCredentials.CertificateIDNew != expectedCertificateIDNew ||
		poolContext.Pool.PoolCredentials.StagedCaURI != expectedStagedCaURI {
		return fmt.Errorf("pool %s staged certificate/CA pair changed before staging", poolUUID)
	}
	credentials := *poolContext.Pool.PoolCredentials
	credentials.CertificateIDNew = certificateID
	credentials.StagedCaURI = caURI
	return a.compareAndSwapAuthReconciliationCredentials(
		ctx,
		poolUUID,
		expectedCertificateIDNew,
		expectedStagedCaURI,
		&credentials,
	)
}

func (a *RotateVcpToVsaCertificateActivity) rollbackAuthReconciliationStage(ctx context.Context, poolUUID, certificateID, caURI string) error {
	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return err
	}
	if poolContext == nil || poolContext.Pool == nil || poolContext.Pool.PoolCredentials == nil {
		return fmt.Errorf("pool %s has no credentials before rollback", poolUUID)
	}
	credentials := *poolContext.Pool.PoolCredentials
	if credentials.CertificateIDNew != certificateID || credentials.StagedCaURI != caURI {
		return fmt.Errorf("pool %s staged certificate/CA pair changed before rollback", poolUUID)
	}
	credentials.CertificateIDNew = ""
	credentials.StagedCaURI = ""
	return a.compareAndSwapAuthReconciliationCredentials(ctx, poolUUID, certificateID, caURI, &credentials)
}

func (a *RotateVcpToVsaCertificateActivity) cleanupFailedAuthReconciliation(
	ctx context.Context,
	poolUUID string,
	credentials *datamodel.PoolCredentials,
	cloudService hyperscaler2.Services,
) error {
	if err := a.rollbackAuthReconciliationStage(ctx, poolUUID, credentials.CertificateID, credentials.CaURI); err != nil {
		// Keep the cloud certificate live while the database still references
		// it so the next activity attempt can safely resume.
		return err
	}
	if err := revokeCertificateAndDeleteFromCacheAndSecretManager(cloudService, credentials); err != nil {
		util.GetLogger(ctx).Warnf(
			"Failed to revoke rolled-back auth reconciliation certificate %s: %v",
			credentials.CertificateID,
			err,
		)
	}
	return nil
}

func (a *RotateVcpToVsaCertificateActivity) promoteAuthReconciliationCertificate(ctx context.Context, poolUUID, certificateID, caURI, username string) (string, error) {
	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return "", err
	}
	if poolContext.Pool == nil || poolContext.Pool.PoolCredentials == nil {
		return "", fmt.Errorf("pool %s has no credentials before promotion", poolUUID)
	}
	if poolContext.Pool.PoolCredentials.AuthType != env.USERNAME_PWD_SEC_MGR {
		return "", fmt.Errorf("pool %s auth type changed before promotion", poolUUID)
	}
	if !isAuthReconciliationStateEligible(poolContext.Pool.State) {
		return "", fmt.Errorf("pool %s state changed to %q before promotion", poolUUID, poolContext.Pool.State)
	}
	credentials := *poolContext.Pool.PoolCredentials
	if credentials.CertificateIDNew != certificateID || credentials.StagedCaURI != caURI {
		return "", fmt.Errorf("staged certificate/CA pair changed before promotion")
	}
	oldCertificateID := credentials.CertificateID
	oldCaURI := credentials.CaURI
	if oldCertificateID == "" || oldCaURI == "" {
		// Never retain an unpaired stale certificate reference: later cleanup
		// must not guess its CA from the current environment.
		oldCertificateID = ""
		oldCaURI = ""
	}
	credentials.CertificateID = certificateID
	credentials.CaURI = caURI
	credentials.CertificateIDNew = oldCertificateID
	credentials.StagedCaURI = oldCaURI
	credentials.Username = username
	credentials.UsernamePreCertFix = ""
	credentials.AuthType = env.USER_CERTIFICATE
	if err := a.compareAndSwapAuthReconciliationCredentials(
		ctx,
		poolUUID,
		certificateID,
		caURI,
		&credentials,
	); err != nil {
		return "", err
	}
	// The old certificate and its old CA remain paired in certificate_id_new /
	// staged_ca_uri for the existing deferred cleanup path.
	return oldCertificateID, nil
}

func (a *RotateVcpToVsaCertificateActivity) refreshPromotedCertificateCache(ctx context.Context, certificateID, caURI, oldCertificateID string) error {
	if err := a.updateCertificateCache(ctx, certificateID, caURI); err != nil {
		return fmt.Errorf("refresh promoted certificate cache: %w", err)
	}
	if oldCertificateID != "" && oldCertificateID != certificateID {
		removeFromCertAuthCache(oldCertificateID)
	}
	return nil
}
