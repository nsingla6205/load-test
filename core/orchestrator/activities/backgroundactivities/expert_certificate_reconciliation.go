package backgroundactivities

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

func hasExpertModeCredentials(pool *datamodel.Pool) bool {
	return pool != nil &&
		pool.ExpertModeCredentials != nil &&
		len(pool.ExpertModeCredentials.ExpertModeCredential) > 0
}

// reconcileExpertModeCredentialsBeforePromotion brings every expert mode credential
// onto targetCAURI. When force is true, credentials whose certificate is still current
// are rotated anyway.
func (a *RotateVcpToVsaCertificateActivity) reconcileExpertModeCredentialsBeforePromotion(
	ctx context.Context,
	poolUUID, targetCAURI string,
	force bool,
) error {
	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return err
	}
	pool := poolContext.Pool
	if pool == nil || pool.ExpertModeCredentials == nil {
		return nil
	}
	hasCertificateCredential := false
	for _, credential := range pool.ExpertModeCredentials.ExpertModeCredential {
		if credential != nil && credential.AuthType == env.USER_CERTIFICATE {
			hasCertificateCredential = true
			break
		}
	}
	if !hasCertificateCredential {
		return nil
	}
	cloudService, err := a.dependencies().resolveCloudService(ctx)
	if err != nil {
		return fmt.Errorf("get cloud service: %w", err)
	}
	for index := range pool.ExpertModeCredentials.ExpertModeCredential {
		if err := a.reconcileOneExpertModeCredential(ctx, pool, index, targetCAURI, cloudService, force); err != nil {
			return err
		}
	}
	return nil
}

func (a *RotateVcpToVsaCertificateActivity) reconcileOneExpertModeCredential(
	ctx context.Context,
	pool *datamodel.Pool,
	index int,
	targetCAURI string,
	cloudService hyperscaler.Services,
	force bool,
) error {
	logger := util.GetLogger(ctx)
	var err error
	if pool == nil || pool.ExpertModeCredentials == nil ||
		index >= len(pool.ExpertModeCredentials.ExpertModeCredential) {
		return fmt.Errorf("invalid expert credential index %d", index)
	}
	poolUUID := pool.UUID
	credential := pool.ExpertModeCredentials.ExpertModeCredential[index]
	if credential == nil {
		return nil
	}
	if credential.AuthType != env.USER_CERTIFICATE {
		logger.Info("Skipping non-certificate expert-mode credential",
			"poolUUID", poolUUID,
			"credentialIndex", index,
			"authType", credential.AuthType,
		)
		return nil
	}
	certificateUsername := credential.Username
	if certificateUsername == "" {
		return fmt.Errorf("expert credential %d has an empty username", index)
	}
	logger.Info("Expert-mode certificate reconciliation started",
		"poolUUID", poolUUID,
		"credentialIndex", index,
		"certificateUsername", certificateUsername,
		"force", force,
	)
	oldCAURI := credential.CaURI
	if oldCAURI == "" && pool.PoolCredentials != nil {
		// Expert credentials created before per-credential CA persistence were
		// issued under the pool's persisted CA. Do not infer from process config:
		// it may already point at a replacement CA.
		oldCAURI = pool.PoolCredentials.CaURI
	}
	activeCertificateCurrent := false
	if credential.CertificateID != "" && credential.CaURI == "" && oldCAURI != "" {
		projectID, poolName, _ := env.ParseCaURI(oldCAURI)
		activeResponse, fetchErr := a.dependencies().getCertificateAndPrivateKeyByID(
			cloudService,
			projectID,
			env.SecretManagerProjectID,
			env.Region,
			poolName,
			credential.CertificateID,
		)
		if fetchErr == nil && activeResponse == nil {
			fetchErr = fmt.Errorf("legacy expert certificate lookup returned nil response")
		}
		if fetchErr == nil {
			fetchErr = validateCertificateForReuse(
				activeResponse.Certificate,
				oldCAURI,
				env.Region,
				a.dependencies().now(),
				env.GetCertificateRotationThresholdPercentage(),
			)
		}
		if fetchErr == nil {
			if err := a.backfillExpertModeCredentialCAURI(
				ctx,
				poolUUID,
				index,
				credential.CertificateID,
				oldCAURI,
			); err != nil {
				return fmt.Errorf("backfill expert credential issuer: %w", err)
			}
			credential.CaURI = oldCAURI
			activeCertificateCurrent = oldCAURI == targetCAURI
		}
	}
	if !activeCertificateCurrent &&
		credential.CertificateID != "" &&
		credential.CaURI == targetCAURI {
		expertPool := *pool
		expertPoolCredentials := *pool.PoolCredentials
		expertPoolCredentials.CaURI = targetCAURI
		expertPool.PoolCredentials = &expertPoolCredentials
		needsRotation, checkErr := a.certificateNeedsRotation(ctx, &expertPool, credential.CertificateID)
		if checkErr == nil && !needsRotation {
			activeCertificateCurrent = true
		}
	}

	stagedID := credential.CertificateIDNew
	if inferredCAURI, inferred := inferIssuerForNewerLegacyStagedCert(
		credential.CertificateID,
		stagedID,
		credential.StagedCaURI,
		oldCAURI,
		targetCAURI,
	); inferred {
		if err := a.updateExpertModeStaging(
			ctx,
			poolUUID,
			index,
			stagedID,
			credential.StagedCaURI,
			stagedID,
			inferredCAURI,
		); err != nil {
			return fmt.Errorf("backfill legacy expert staged certificate CA URI: %w", err)
		}
		credential.StagedCaURI = inferredCAURI
	}
	needsCandidate := !activeCertificateCurrent || force
	if needsCandidate {
		if err := a.preflightCertificateIssuance(
			ctx,
			pool,
			targetCAURI,
			env.Admin,
			certificateUsername,
		); err != nil {
			return fmt.Errorf("expert credential %q preflight: %w", certificateUsername, err)
		}
	}

	candidate, err := prepareStagedCertificateCandidate(stagedCertificateCandidateConfig{
		stagedID:                 stagedID,
		stagedCAURI:              credential.StagedCaURI,
		fallbackCAURI:            oldCAURI,
		targetCAURI:              targetCAURI,
		activeCertificateCurrent: activeCertificateCurrent,
		mintIfMissing:            needsCandidate,
		newID: func() string {
			return rotatedCertificateID(pool.DeploymentName, a.dependencies().now(), certificateUsername)
		},
		load: func(certificateID string) (*hyperscalermodels.CustomCertificateResponse, error) {
			projectID, poolName, _ := env.ParseCaURI(targetCAURI)
			response, loadErr := a.dependencies().getCertificateAndPrivateKeyByID(
				cloudService,
				projectID,
				env.SecretManagerProjectID,
				env.Region,
				poolName,
				certificateID,
			)
			if loadErr == nil && response != nil {
				loadErr = validateCertificateForReuse(
					response.Certificate,
					targetCAURI,
					env.Region,
					a.dependencies().now(),
					env.GetCertificateRotationThresholdPercentage(),
				)
			}
			return response, loadErr
		},
		mint: func(certificateID string) (*hyperscalermodels.CustomCertificateResponse, error) {
			return a.dependencies().generateExpertCertificate(
				cloudService,
				pool.DeploymentName,
				certificateUsername,
				&datamodel.PoolCredentials{CertificateID: certificateID, CaURI: targetCAURI},
				false,
			)
		},
		stage: func(certificateID, caURI string) error {
			return a.updateExpertModeStaging(ctx, poolUUID, index, "", "", certificateID, caURI)
		},
		detach: func(certificateID, caURI string) error {
			return a.detachAndRevokeStagedExpertCertificate(
				ctx,
				poolUUID,
				index,
				certificateID,
				caURI,
				cloudService,
			)
		},
		cleanupUnstaged: func(certificateID, caURI string) error {
			return revokeCertificateAndDeleteFromCacheAndSecretManager(
				cloudService,
				&datamodel.PoolCredentials{CertificateID: certificateID, CaURI: caURI},
			)
		},
	})
	if err != nil {
		return fmt.Errorf("prepare expert credential %q candidate: %w", certificateUsername, err)
	}
	stagedID = candidate.id
	credential.CertificateIDNew = stagedID
	if stagedID == "" {
		credential.StagedCaURI = ""
		logger.Info("Expert-mode certificate is current; reconciliation completed without rotation",
			"poolUUID", poolUUID,
			"credentialIndex", index,
			"certificateID", credential.CertificateID,
		)
		return nil
	}
	credential.StagedCaURI = targetCAURI
	candidateResponse := candidate.response
	if candidate.reused {
		logger.Info("Reusing staged expert-mode certificate",
			"poolUUID", poolUUID,
			"credentialIndex", index,
			"certificateID", stagedID,
		)
	} else {
		logger.Info("Staged expert-mode certificate",
			"poolUUID", poolUUID,
			"credentialIndex", index,
			"certificateID", stagedID,
		)
	}
	verificationPool := *pool
	verificationCredentials := *pool.PoolCredentials
	verificationCredentials.AuthType = env.USER_CERTIFICATE
	verificationCredentials.Username = certificateUsername
	verificationCredentials.CertificateID = stagedID
	verificationCredentials.CertificateIDNew = ""
	verificationCredentials.CaURI = targetCAURI
	verificationCredentials.StagedCaURI = ""
	verificationPool.PoolCredentials = &verificationCredentials
	if err := a.testCertificateConnectivity(ctx, &verificationPool, candidateResponse.Certificate); err != nil {
		return fmt.Errorf("expert candidate connectivity failed: %w", err)
	}
	logger.Info("Verified expert-mode certificate connectivity",
		"poolUUID", poolUUID,
		"credentialIndex", index,
		"certificateID", stagedID,
	)

	logger.Info("Promoting expert-mode certificate",
		"poolUUID", poolUUID,
		"credentialIndex", index,
		"certificateID", stagedID,
	)
	previousCertificateID := credential.CertificateID
	if err := a.promoteExpertModeCandidate(
		ctx,
		poolUUID,
		index,
		stagedID,
		targetCAURI,
		credential.CertificateID,
		oldCAURI,
	); err != nil {
		return err
	}
	credential.CertificateID = stagedID
	credential.CaURI = targetCAURI
	if previousCertificateID != "" && oldCAURI != "" {
		credential.CertificateIDNew = previousCertificateID
		credential.StagedCaURI = oldCAURI
	} else {
		credential.CertificateIDNew = ""
		credential.StagedCaURI = ""
	}
	if err := a.updateCertificateCache(ctx, stagedID, targetCAURI); err != nil {
		return fmt.Errorf("update promoted expert certificate cache: %w", err)
	}
	if credential.CertificateID != "" && credential.CertificateID != stagedID {
		removeFromCertAuthCache(credential.CertificateID)
	}
	logger.Info("Expert-mode certificate reconciliation finished",
		"poolUUID", poolUUID,
		"credentialIndex", index,
		"certificateID", stagedID,
		"outcome", "success",
	)
	return nil
}

func (a *RotateVcpToVsaCertificateActivity) backfillExpertModeCredentialCAURI(
	ctx context.Context,
	poolUUID string,
	index int,
	expectedCertificateID, caURI string,
) error {
	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return err
	}
	pool := poolContext.Pool
	if pool == nil || pool.ExpertModeCredentials == nil ||
		index >= len(pool.ExpertModeCredentials.ExpertModeCredential) {
		return fmt.Errorf("invalid expert credential index %d", index)
	}
	current := pool.ExpertModeCredentials.ExpertModeCredential[index]
	if current == nil ||
		current.CertificateID != expectedCertificateID ||
		current.CaURI != "" {
		return fmt.Errorf("expert credential %d changed before issuer backfill", index)
	}
	expected := a.cloneExpertModeCredentials(pool.ExpertModeCredentials)
	replacement := a.cloneExpertModeCredentials(expected)
	replacement.ExpertModeCredential[index].CaURI = caURI
	return a.SE.CompareAndSwapExpertModeCredentials(
		ctx,
		poolUUID,
		expected,
		replacement,
		env.GetCertificateRotationPoolStates(),
	)
}

func (a *RotateVcpToVsaCertificateActivity) detachAndRevokeStagedExpertCertificate(
	ctx context.Context,
	poolUUID string,
	index int,
	certificateID, caURI string,
	cloudService hyperscaler.Services,
) error {
	if err := a.updateExpertModeStaging(
		ctx,
		poolUUID,
		index,
		certificateID,
		caURI,
		"",
		"",
	); err != nil {
		return err
	}
	if caURI == "" {
		return nil
	}
	return a.revokeOldCertificate(ctx, certificateID, caURI, cloudService)
}

func (a *RotateVcpToVsaCertificateActivity) updateExpertModeStaging(
	ctx context.Context,
	poolUUID string,
	index int,
	expectedID, expectedCAURI, newID, newCAURI string,
) error {
	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return err
	}
	pool := poolContext.Pool
	if pool == nil || pool.ExpertModeCredentials == nil ||
		index >= len(pool.ExpertModeCredentials.ExpertModeCredential) {
		return fmt.Errorf("invalid expert credential index %d", index)
	}
	current := pool.ExpertModeCredentials.ExpertModeCredential[index]
	if current == nil || current.CertificateIDNew != expectedID || current.StagedCaURI != expectedCAURI {
		return fmt.Errorf("expert credential %d staged pair changed", index)
	}
	expected := a.cloneExpertModeCredentials(pool.ExpertModeCredentials)
	updated := a.cloneExpertModeCredentials(expected)
	updated.ExpertModeCredential[index].CertificateIDNew = newID
	updated.ExpertModeCredential[index].StagedCaURI = newCAURI
	return a.SE.CompareAndSwapExpertModeCredentials(
		ctx,
		poolUUID,
		expected,
		updated,
		env.GetCertificateRotationPoolStates(),
	)
}

func (a *RotateVcpToVsaCertificateActivity) promoteExpertModeCandidate(
	ctx context.Context,
	poolUUID string,
	index int,
	expectedID, expectedCAURI, oldID, oldCAURI string,
) error {
	if oldID == "" || oldCAURI == "" {
		// Never leave a certificate ID in the deferred-cleanup slot without
		// the issuer required to fetch and revoke it safely.
		oldID = ""
		oldCAURI = ""
	}
	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return err
	}
	pool := poolContext.Pool
	if pool == nil || pool.ExpertModeCredentials == nil ||
		index >= len(pool.ExpertModeCredentials.ExpertModeCredential) {
		return fmt.Errorf("invalid expert credential index %d", index)
	}
	current := pool.ExpertModeCredentials.ExpertModeCredential[index]
	if current == nil || current.CertificateIDNew != expectedID || current.StagedCaURI != expectedCAURI {
		return fmt.Errorf("expert credential %d staged pair changed before promotion", index)
	}
	expected := a.cloneExpertModeCredentials(pool.ExpertModeCredentials)
	updated := a.cloneExpertModeCredentials(expected)
	promoted := updated.ExpertModeCredential[index]
	promoted.CertificateID = expectedID
	promoted.CaURI = expectedCAURI
	promoted.CertificateIDNew = oldID
	promoted.StagedCaURI = oldCAURI
	return a.SE.CompareAndSwapExpertModeCredentials(
		ctx,
		poolUUID,
		expected,
		updated,
		env.GetCertificateRotationPoolStates(),
	)
}
