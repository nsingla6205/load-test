package backgroundactivities

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	vcptls "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/tlsconfig"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

// PoolContext contains shared pool information to avoid redundant database queries
type PoolContext struct {
	Pool           *datamodel.Pool
	PoolUUID       string
	OntapNodeUUIDs []string
	DesiredCAURI   string
	// Force skips remaining-lifetime eligibility for both the pool certificate and
	// the expert mode certificates. Safety checks (pool state, missing credentials)
	// still apply. Set only by ForceRotatePoolCertificateWorkflow.
	Force bool
}

// RollbackResources tracks resources created during certificate rotation for cleanup
type RollbackResources struct {
	NewCertificateID string
	NewCaURI         string
	NewSecretID      string
	DBReferenced     bool
	NewCertificate   *hyperscalermodels.CustomCertificate
	NewSecret        *hyperscalermodels.CustomSecret
	OldCertificate   *models.Certificate
	Pool             *datamodel.Pool
	GcpService       hyperscaler2.Services
}

const (
	rotatedCertificateIDTimeFormat                = "20060102-150405"
	enableCAChangeDuringCertificateRotationEnvKey = "ENABLE_CA_CHANGE_DURING_CERTIFICATE_ROTATION"
)

func rotatedCertificateID(deploymentName string, now time.Time, qualifiers ...string) string {
	parts := []string{deploymentName, "cert"}
	parts = append(parts, qualifiers...)
	parts = append(parts, now.Format(rotatedCertificateIDTimeFormat))
	return strings.Join(parts, "-")
}

func certificateRotationUsername(pool *datamodel.Pool, activeCertificate *models.Certificate) (string, error) {
	if pool == nil || pool.PoolCredentials == nil {
		return "", errors.New("pool credentials are required to resolve certificate username")
	}
	if username := strings.TrimSpace(pool.PoolCredentials.Username); username != "" {
		return username, nil
	}
	if activeCertificate != nil {
		if username := strings.TrimSpace(activeCertificate.CommonName); username != "" {
			return username, nil
		}
	}
	return "", fmt.Errorf("pool %s has no persisted certificate username or recoverable certificate common name", pool.UUID)
}

// reusableStagedCertificate reports whether certificate_id_new is an in-flight
// candidate for the target CA. After a successful promotion the previous cert is
// parked in certificate_id_new/staged_ca_uri; that pair must not be reused when the
// active cert is already current, or a same-CA forced rotation would undo itself.
func reusableStagedCertificate(stagedID, stagedCAURI, targetCAURI string, activeCertificateCurrent bool) bool {
	return stagedID != "" &&
		targetCAURI != "" &&
		stagedCAURI == targetCAURI &&
		!activeCertificateCurrent
}

type stagedCertificateCandidateConfig struct {
	stagedID                 string
	stagedCAURI              string
	fallbackCAURI            string
	targetCAURI              string
	activeCertificateCurrent bool
	mintIfMissing            bool
	newID                    func() string
	load                     func(string) (*hyperscalermodels.CustomCertificateResponse, error)
	mint                     func(string) (*hyperscalermodels.CustomCertificateResponse, error)
	stage                    func(string, string) error
	detach                   func(string, string) error
	cleanupUnstaged          func(string, string) error
}

type stagedCertificateCandidate struct {
	id       string
	response *hyperscalermodels.CustomCertificateResponse
	reused   bool
}

type stagedCertificateCandidateError struct {
	operation string
	err       error
}

func (e *stagedCertificateCandidateError) Error() string {
	return e.operation + ": " + e.err.Error()
}

func (e *stagedCertificateCandidateError) Unwrap() error {
	return e.err
}

// prepareStagedCertificateCandidate owns the common candidate lifecycle used by
// pool and expert-mode certificate rotation: discard stale staging, validate a
// reusable candidate, or mint and persist a replacement.
func prepareStagedCertificateCandidate(config stagedCertificateCandidateConfig) (*stagedCertificateCandidate, error) {
	stagedID := config.stagedID
	reuse := reusableStagedCertificate(
		stagedID,
		config.stagedCAURI,
		config.targetCAURI,
		config.activeCertificateCurrent,
	)
	if stagedID != "" && !reuse {
		staleCAURI := config.stagedCAURI
		if staleCAURI == "" {
			staleCAURI = config.fallbackCAURI
		}
		if err := config.detach(stagedID, staleCAURI); err != nil {
			return nil, &stagedCertificateCandidateError{
				operation: "detach stale staged certificate",
				err:       fmt.Errorf("%s: %w", stagedID, err),
			}
		}
		stagedID = ""
	}

	var response *hyperscalermodels.CustomCertificateResponse
	if reuse {
		var err error
		response, err = config.load(stagedID)
		if err == nil && response == nil {
			err = errors.New("staged certificate lookup returned nil response")
		}
		if err != nil {
			if detachErr := config.detach(stagedID, config.targetCAURI); detachErr != nil {
				return nil, &stagedCertificateCandidateError{
					operation: "detach invalid staged certificate",
					err:       fmt.Errorf("%s: %w", stagedID, detachErr),
				}
			}
			stagedID = ""
			response = nil
			reuse = false
		}
	}

	if stagedID == "" && config.mintIfMissing {
		stagedID = config.newID()
		var err error
		response, err = config.mint(stagedID)
		if err != nil {
			return nil, &stagedCertificateCandidateError{
				operation: "mint certificate candidate",
				err:       fmt.Errorf("%s: %w", stagedID, err),
			}
		}
		if response == nil {
			return nil, &stagedCertificateCandidateError{
				operation: "mint certificate candidate",
				err:       fmt.Errorf("%s returned an empty response", stagedID),
			}
		}
		if err := config.stage(stagedID, config.targetCAURI); err != nil {
			if cleanupErr := config.cleanupUnstaged(stagedID, config.targetCAURI); cleanupErr != nil {
				err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
			}
			return nil, &stagedCertificateCandidateError{
				operation: "stage certificate candidate",
				err:       fmt.Errorf("%s: %w", stagedID, err),
			}
		}
	}

	return &stagedCertificateCandidate{id: stagedID, response: response, reused: reuse}, nil
}

// inferIssuerForNewerLegacyStagedCert backfills staged_ca_uri only when the
// unpaired staged ID is timestamp-newer than the active cert on the same CA.
func inferIssuerForNewerLegacyStagedCert(
	activeCertificateID, stagedCertificateID, stagedCAURI, activeCAURI, targetCAURI string,
) (string, bool) {
	if stagedCertificateID == "" ||
		stagedCAURI != "" ||
		activeCAURI == "" ||
		activeCAURI != targetCAURI ||
		!legacyStagedCertificateIsNewer(activeCertificateID, stagedCertificateID) {
		return stagedCAURI, false
	}
	return activeCAURI, true
}

func legacyStagedCertificateIsNewer(activeCertificateID, stagedCertificateID string) bool {
	stagedTime, stagedTimeOK := certificateIDTimestamp(stagedCertificateID)
	if !stagedTimeOK {
		return false
	}
	activeTime, activeTimeOK := certificateIDTimestamp(activeCertificateID)
	if !activeTimeOK {
		// Create-time certificate IDs have no timestamp. A staged timestamped
		// certificate is therefore from a later rotation.
		return activeCertificateID != ""
	}
	return stagedTime.After(activeTime)
}

func certificateIDTimestamp(certificateID string) (time.Time, bool) {
	const formattedTimestampLength = len(rotatedCertificateIDTimeFormat)
	if len(certificateID) <= formattedTimestampLength ||
		certificateID[len(certificateID)-formattedTimestampLength-1] != '-' {
		return time.Time{}, false
	}
	parsed, err := time.Parse(
		rotatedCertificateIDTimeFormat,
		certificateID[len(certificateID)-formattedTimestampLength:],
	)
	return parsed, err == nil
}

func defaultCertificateCaURI() string {
	return env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName)
}

func certificateCaDrifted(credentials *datamodel.PoolCredentials) bool {
	return caChangeDuringCertificateRotationEnabled() &&
		certificateCaDriftedAgainst(credentials, defaultCertificateCaURI())
}

func certificateCaDriftedAgainst(credentials *datamodel.PoolCredentials, desiredCAURI string) bool {
	if credentials == nil {
		return false
	}
	return desiredCAURI != "" && (credentials.CaURI == "" || credentials.CaURI != desiredCAURI)
}

func validateExplicitDesiredCAURI(desiredCAURI string) error {
	parts := strings.Split(desiredCAURI, "/")
	if len(parts) != 3 {
		return fmt.Errorf("desired CA URI must contain exactly three components")
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("desired CA URI components must not be empty")
		}
	}
	return nil
}

func caChangeDuringCertificateRotationEnabled() bool {
	return env.GetBool(enableCAChangeDuringCertificateRotationEnvKey, false)
}

func desiredCAForPoolContext(poolContext *PoolContext) string {
	if poolContext != nil && poolContext.DesiredCAURI != "" {
		return poolContext.DesiredCAURI
	}
	if !caChangeDuringCertificateRotationEnabled() &&
		poolContext != nil &&
		poolContext.Pool != nil &&
		poolContext.Pool.PoolCredentials != nil &&
		poolContext.Pool.PoolCredentials.CaURI != "" {
		return poolContext.Pool.PoolCredentials.CaURI
	}
	return defaultCertificateCaURI()
}

func expertCertificateCAURIs(credentials *datamodel.PoolCredentials) (sourceCaURI, targetCaURI string) {
	if credentials == nil {
		return "", ""
	}
	targetCaURI = credentials.GetCaURIWithFallback()
	sourceCaURI = targetCaURI
	if credentials.CertificateIDNew != "" && credentials.StagedCaURI != "" {
		sourceCaURI = credentials.StagedCaURI
	}
	return sourceCaURI, targetCaURI
}

var (
	getGcpServiceForCerts                               = hyperscaler2.ResolveCloudService
	generateAndCreateCertificateForVSACluster           = vsa.GenerateAndCreateCertificateForVSACluster
	revokeCertificateAndDeleteFromCacheAndSecretManager = vsa.RevokeCertificateAndDeleteFromCacheAndSecretManager
	getCertificateFromCacheOrSecretManager              = vsa.GetCertificateFromCacheOrSecretManager
	addToCertAuthCache                                  = common.AddToCertAuthCache
	removeFromCertAuthCache                             = common.RemoveFromCertAuthCache
	ConvertPoolViewToPool                               = database.ConvertPoolViewToPool
)

// safeRecordHeartbeat safely records a heartbeat, catching panics if not in an activity context.
// This prevents panics when the function is called from non-activity contexts (e.g., unit tests).
func safeRecordHeartbeat(ctx context.Context, details ...interface{}) {
	defer func() {
		if r := recover(); r != nil {
			// Ignore panic - we're not in an activity context, so RecordHeartbeat panicked
		}
	}()
	activity.RecordHeartbeat(ctx, details...)
}

func isSVMNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "does not exist") && strings.Contains(errMsg, "svm")
}

// isDuplicateCertificateError reports whether err is ONTAP's "already installed"
// response for a server certificate (HTTP 409, body {"message":"duplicate entry"}).
// This is the signal that a prior attempt of a retried install activity already
// POSTed this exact certificate. Because the OCI cert name/serial is stable across
// versions, a Temporal retry re-POSTs the same material and ONTAP rejects it as a
// duplicate; callers treat that as an idempotent no-op rather than a failure.
func isDuplicateCertificateError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "duplicate entry") ||
		strings.Contains(errMsg, "already exists")
}

func getClusterNameFromVLMConfig(pool *datamodel.Pool) string {
	if pool.VLMConfig == "" {
		return ""
	}
	var vlmConfig vlm.VLMConfig
	if err := json.Unmarshal([]byte(pool.VLMConfig), &vlmConfig); err != nil {
		return ""
	}
	return vlmConfig.VsaCluster.ClusterName
}

// resolveVserverName returns the preferred vserver name for ONTAP API calls.
// It prefers the cluster name from VLMConfig (which includes the region suffix)
// and falls back to pool.DeploymentName if VLMConfig is unavailable or empty.
func resolveVserverName(pool *datamodel.Pool) string {
	if clusterName := getClusterNameFromVLMConfig(pool); clusterName != "" {
		return clusterName
	}
	return pool.DeploymentName
}

// GetPoolContext retrieves pool information once and returns it in a shared context
func (a *RotateVcpToVsaCertificateActivity) GetPoolContext(ctx context.Context, poolUUID string) (*PoolContext, error) {
	logger := util.GetLogger(ctx)
	logger.Infof("Getting pool context for UUID: %s", poolUUID)

	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)

	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	logger.Infof("Retrieved %d pool views for UUID %s", len(poolViews), poolUUID)

	if len(poolViews) == 0 {
		logger.Warnf("Pool %s not found", poolUUID)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("pool %s not found", poolUUID))
	}

	poolView := poolViews[0]
	pool := ConvertPoolViewToPool(poolView)

	logger.Infof("Pool Details - UUID: %s, ID: %d, Name: %s, State: %s, ExternalName: %s, DeploymentName: %s",
		pool.UUID, pool.ID, pool.Name, pool.State, pool.ClusterDetails.ExternalName, pool.DeploymentName)

	if pool.PoolCredentials != nil {
		logger.Infof("Pool Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s, CertificateID: %s, CertificateIDNew: %s",
			pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID, pool.PoolCredentials.SecretIDNew,
			pool.PoolCredentials.CertificateID, pool.PoolCredentials.CertificateIDNew)
	} else {
		logger.Warnf("Pool %s has no credentials", poolUUID)
	}

	return &PoolContext{
		Pool:     pool,
		PoolUUID: poolUUID,
	}, nil
}

// GetPoolContextForDesiredCA returns a pool context carrying an immutable desired CA from workflow history.
func (a *RotateVcpToVsaCertificateActivity) GetPoolContextForDesiredCA(ctx context.Context, poolUUID, desiredCAURI string) (*PoolContext, error) {
	if err := validateExplicitDesiredCAURI(desiredCAURI); err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}
	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return nil, err
	}
	poolContext.DesiredCAURI = desiredCAURI
	return poolContext, nil
}

// checkPoolStateBeforeCriticalOperation re-checks pool state to detect if pool delete was triggered
// during rotation. Returns true if pool is in a valid state for rotation, false otherwise.
func (a *RotateVcpToVsaCertificateActivity) checkPoolStateBeforeCriticalOperation(ctx context.Context, poolUUID string) (bool, error) {
	logger := util.GetLogger(ctx)

	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)

	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Warnf("Failed to re-check pool state for %s: %v", poolUUID, err)
		return false, vsaerrors.NewVCPError(
			vsaerrors.ErrDatabaseDataReadError,
			fmt.Errorf("re-check pool %s state: %w", poolUUID, err),
		)
	}

	if len(poolViews) == 0 {
		logger.Warnf("Pool %s not found during state re-check, aborting operation", poolUUID)
		return false, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("pool %s not found", poolUUID))
	}

	pool := ConvertPoolViewToPool(poolViews[0])

	if pool.State == "CREATING" || pool.State == "DELETING" || pool.State == "UPGRADING" {
		logger.Warnf("Pool %s is in %s state, aborting operation to avoid conflicts", poolUUID, pool.State)
		return false, nil
	}

	return true, nil
}

type RotateVcpToVsaCertificateActivity struct {
	SE database.Storage
	// deps contains narrow injectable seams for CA preflight and candidate verification.
	deps *certificateRotationDependencies
	// testPasswordConnectivityFunc is used for testing to override the default testPasswordConnectivity method
	testPasswordConnectivityFunc func(ctx context.Context, pool *datamodel.Pool, testPassword string) error
	// executeAPIRequestWithResponseFunc is used for testing to override the default executeAPIRequestWithResponse method
	executeAPIRequestWithResponseFunc func(ctx context.Context, method, url string, headers map[string]string, data interface{}, auth string) (int, string, error)
}

// RotatePoolCertificateWithContext rotates the certificate for a pool using pre-fetched pool context
func (a *RotateVcpToVsaCertificateActivity) RotatePoolCertificateWithContext(ctx context.Context, poolContext *PoolContext) error {
	return a.rotatePoolCertificateWithContext(ctx, poolContext)
}

// RotatePoolCertificateForDesiredCAWithContext rotates against the desired CA carried in PoolContext.
func (a *RotateVcpToVsaCertificateActivity) RotatePoolCertificateForDesiredCAWithContext(ctx context.Context, poolContext *PoolContext) error {
	if poolContext == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(errors.New("pool context is nil"))
	}
	if err := validateExplicitDesiredCAURI(poolContext.DesiredCAURI); err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}
	return a.rotatePoolCertificateWithContext(ctx, poolContext)
}

func (a *RotateVcpToVsaCertificateActivity) rotatePoolCertificateWithContext(ctx context.Context, poolContext *PoolContext) error {
	safeRecordHeartbeat(ctx, "Starting certificate rotation activity")
	logger := util.GetLogger(ctx)
	pool := poolContext.Pool
	poolUUID := poolContext.PoolUUID
	desiredCAURI := desiredCAForPoolContext(poolContext)

	logger.Infof("Starting certificate rotation for pool UUID: %s (using context)", poolUUID)
	logger.Infof("Pool Details - UUID: %s, Name: %s, State: %s, ExternalName: %s, DeploymentName: %s",
		pool.UUID, pool.Name, pool.State, pool.ClusterDetails.ExternalName, pool.DeploymentName)

	if pool.PoolCredentials != nil {
		logger.Infof("Pool Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s, CertificateID: %s, CertificateIDNew: %s",
			pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID, pool.PoolCredentials.SecretIDNew,
			pool.PoolCredentials.CertificateID, pool.PoolCredentials.CertificateIDNew)
	} else {
		logger.Warnf("Pool %s has no credentials", poolUUID)
	}

	// Check if pool is in CREATING, DELETING, or UPGRADING state - skip rotation for pools being created, deleted, or upgraded
	if pool.State == "CREATING" {
		logger.Infof("Pool %s is in CREATING state, skipping certificate rotation", poolUUID)
		return nil
	}
	if pool.State == "DELETING" {
		logger.Infof("Pool %s is in DELETING state, skipping certificate rotation", poolUUID)
		return nil
	}
	if pool.State == "UPGRADING" {
		logger.Infof("Pool %s is in UPGRADING state, skipping certificate rotation", poolUUID)
		return nil
	}

	if pool.PoolCredentials == nil || pool.PoolCredentials.CertificateID == "" {
		logger.Warnf("Pool %s has no certificate ID, skipping rotation", poolUUID)
		return vsaerrors.NewVCPError(vsaerrors.ErrPoolCredentialsMissing, fmt.Errorf("pool %s has no certificate ID", poolUUID))
	}
	safeRecordHeartbeat(ctx, "Checking and syncing password connectivity")
	err := a.checkAndSyncPasswordConnectivity(ctx, pool)
	if err != nil {
		logger.Errorf("Failed to check and sync password connectivity for pool %s: %v", poolUUID, err)
		return err
	}

	safeRecordHeartbeat(ctx, "Checking if certificate is expired")
	currentCaURI := pool.PoolCredentials.GetCaURIWithFallback()
	missingActiveIssuer := pool.PoolCredentials.CaURI == ""
	certExpired := false
	if !missingActiveIssuer {
		certExpired, err = a.isCertificateExpiredAtCA(ctx, pool.PoolCredentials.CertificateID, currentCaURI)
		if err != nil {
			logger.Warnf("Failed to check if certificate is expired: %v", err)
			certExpired = false
		}
	} else {
		logger.Warnf("Pool %s active certificate has no persisted CA URI; using password recovery for one-time CA reconciliation", poolUUID)
	}

	switch {
	case missingActiveIssuer:
		logger.Warnf("Certificate issuer is unknown for pool %s; skipping certificate connectivity check and proceeding with password recovery", poolUUID)
	case certExpired:
		logger.Warnf("Certificate %s is expired - skipping certificate connectivity check and proceeding with rotation", pool.PoolCredentials.CertificateID)
	default:
		logger.Debug("Certificate is not expired - checking certificate connectivity")
		var credentialsChanged bool
		credentialsChanged, err = a.checkAndSyncCertificateConnectivity(ctx, pool)
		if err != nil {
			logger.Errorf("Failed to check and sync certificate connectivity for pool %s: %v", poolUUID, err)
			return err
		}
		if credentialsChanged {
			refreshedPoolContext, refreshErr := a.GetPoolContext(ctx, poolUUID)
			if refreshErr != nil {
				return fmt.Errorf("refresh pool after certificate connectivity sync: %w", refreshErr)
			}
			refreshedPoolContext.DesiredCAURI = poolContext.DesiredCAURI
			refreshedPoolContext.Force = poolContext.Force
			poolContext = refreshedPoolContext
			pool = refreshedPoolContext.Pool
			if pool == nil || pool.PoolCredentials == nil {
				return vsaerrors.NewVCPError(vsaerrors.ErrPoolCredentialsMissing, fmt.Errorf("pool %s has no credentials after connectivity sync", poolUUID))
			}
			currentCaURI = pool.PoolCredentials.GetCaURIWithFallback()
			missingActiveIssuer = pool.PoolCredentials.CaURI == ""
		}
	}

	needsRotation := poolContext.Force || certificateCaDriftedAgainst(pool.PoolCredentials, desiredCAURI)
	switch {
	case poolContext.Force:
		logger.Infof("Forcing certificate rotation for pool %s on explicit request, bypassing remaining-lifetime eligibility", poolUUID)
	case needsRotation:
		logger.Infof("Forcing certificate rotation for pool %s because CA URI drifted from desired state %q", poolUUID, desiredCAURI)
	default:
		needsRotation, err = a.certificateNeedsRotation(ctx, pool, pool.PoolCredentials.CertificateID)
		if err != nil {
			logger.Errorf("Failed to check if certificate needs rotation for pool %s: %v", poolUUID, err)
			return vsaerrors.NewVCPError(vsaerrors.ErrCertificateNeedsRotationCheckFailed, err)
		}
	}

	if !needsRotation {
		logger.Infof("Certificate for pool %s does not need rotation yet", poolUUID)

		// Expert mode certificates have lifetimes independent of the pool certificate, so a
		// current pool certificate must not block their rotation.
		if pool.APIAccessMode == common.ONTAPMode {
			safeRecordHeartbeat(ctx, "Pool certificate current, checking expert mode certificate")
			if errExpert := a.rotateExpertModeCertificate(ctx, poolUUID); errExpert != nil {
				logger.Errorf("Expert mode certificate rotation failed for pool %s: %v", poolUUID, errExpert)
				return vsaerrors.NewVCPError(vsaerrors.ErrCertificateRotationFailed, errExpert)
			}
			logger.Infof("Expert mode certificate rotation check completed for pool %s", poolUUID)
		}

		safeRecordHeartbeat(ctx, "Certificate rotation not needed, completing activity")
		return nil
	}

	logger.Infof("Starting certificate rotation for pool %s with certificate ID %s", poolUUID, pool.PoolCredentials.CertificateID)
	safeRecordHeartbeat(ctx, "Certificate needs rotation, getting cloud service")

	cloudService, err := getGcpServiceForCerts(ctx)
	if err != nil {
		logger.Errorf("Failed to get cloud service: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
	}

	oldCertificate, err := getCertificateFromCacheOrSecretManager(ctx, pool.PoolCredentials)
	if err != nil {
		logger.Warnf("Failed to get old certificate for rollback: %v", err)
	}

	targetCaURI := desiredCAURI
	if targetCaURI == "" {
		targetCaURI = currentCaURI
	}

	username, err := certificateRotationUsername(pool, oldCertificate)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrPoolCredentialsMissing, err)
	}

	safeRecordHeartbeat(ctx, "Running issuing CA, trust, and security login preflight")
	if err := a.preflightCertificateIssuance(ctx, pool, targetCaURI, env.Admin, username); err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrCertificateGenerationFailed, fmt.Errorf("certificate issuance preflight: %w", err))
	}

	newCertificateID := pool.PoolCredentials.CertificateIDNew
	if inferredCAURI, inferred := inferIssuerForNewerLegacyStagedCert(
		pool.PoolCredentials.CertificateID,
		newCertificateID,
		pool.PoolCredentials.StagedCaURI,
		pool.PoolCredentials.CaURI,
		targetCaURI,
	); inferred {
		logger.Infof("Backfilling legacy staged certificate %s with CA URI %s", newCertificateID, inferredCAURI)
		if err := a.updatePoolCertificateIDNew(ctx, pool, newCertificateID, inferredCAURI); err != nil {
			return vsaerrors.NewVCPError(
				vsaerrors.ErrCertificateStagingFailed,
				fmt.Errorf("backfill legacy staged certificate CA URI: %w", err),
			)
		}
		pool.PoolCredentials.StagedCaURI = inferredCAURI
	}
	activeCertificateCurrent := false
	if pool.PoolCredentials.CertificateID != "" && pool.PoolCredentials.CaURI == targetCaURI {
		lifetimeNeedsRotation, checkErr := a.certificateNeedsRotation(ctx, pool, pool.PoolCredentials.CertificateID)
		if checkErr == nil && !lifetimeNeedsRotation {
			activeCertificateCurrent = true
		}
	}
	candidate, candidateErr := prepareStagedCertificateCandidate(stagedCertificateCandidateConfig{
		stagedID:                 newCertificateID,
		stagedCAURI:              pool.PoolCredentials.StagedCaURI,
		fallbackCAURI:            pool.PoolCredentials.CaURI,
		targetCAURI:              targetCaURI,
		activeCertificateCurrent: activeCertificateCurrent,
		mintIfMissing:            true,
		newID: func() string {
			return rotatedCertificateID(pool.DeploymentName, a.dependencies().now())
		},
		load: func(certificateID string) (*hyperscalermodels.CustomCertificateResponse, error) {
			caProjectID, caPoolName, _ := env.ParseCaURI(targetCaURI)
			response, loadErr := a.dependencies().getCertificateAndPrivateKeyByID(
				cloudService,
				caProjectID,
				env.SecretManagerProjectID,
				env.Region,
				caPoolName,
				certificateID,
			)
			if loadErr == nil && response != nil {
				loadErr = validateCertificateForReuse(
					response.Certificate,
					targetCaURI,
					env.Region,
					a.dependencies().now(),
					env.GetCertificateRotationThresholdPercentage(),
				)
			}
			return response, loadErr
		},
		mint: func(certificateID string) (*hyperscalermodels.CustomCertificateResponse, error) {
			logger.Infof("Generating new certificate %s for deployment %s", certificateID, pool.DeploymentName)
			safeRecordHeartbeat(ctx, fmt.Sprintf("Generating new certificate with ID: %s", certificateID))
			return generateAndCreateCertificateForVSACluster(
				cloudService,
				pool.DeploymentName,
				username,
				&datamodel.PoolCredentials{CertificateID: certificateID, CaURI: targetCaURI},
				true,
			)
		},
		stage: func(certificateID, caURI string) error {
			safeRecordHeartbeat(ctx, fmt.Sprintf("Staging new certificate ID %s in database", certificateID))
			return a.updatePoolCertificateIDNew(ctx, pool, certificateID, caURI)
		},
		detach: func(certificateID, caURI string) error {
			return a.detachAndRevokeStagedPoolCertificate(ctx, pool, certificateID, caURI, cloudService)
		},
		cleanupUnstaged: func(certificateID, caURI string) error {
			return revokeCertificateAndDeleteFromCacheAndSecretManager(
				cloudService,
				&datamodel.PoolCredentials{CertificateID: certificateID, CaURI: caURI},
			)
		},
	})
	if candidateErr != nil {
		var operationErr *stagedCertificateCandidateError
		if errors.As(candidateErr, &operationErr) && operationErr.operation == "mint certificate candidate" {
			return vsaerrors.NewVCPError(vsaerrors.ErrCertificateGenerationFailed, candidateErr)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrCertificateStagingFailed, candidateErr)
	}
	newCertificateID = candidate.id
	newCertResponse := candidate.response
	if candidate.reused {
		logger.Infof("Reusing valid staged certificate %s under target CA", newCertificateID)
	}
	pool.PoolCredentials.CertificateIDNew = newCertificateID
	pool.PoolCredentials.StagedCaURI = targetCaURI

	secretNameForRollback := extractSecretNameFromPath(newCertResponse.Secret.Name)

	createdResources := &RollbackResources{
		NewCertificateID: newCertificateID,
		NewCaURI:         targetCaURI,
		NewSecretID:      secretNameForRollback,
		DBReferenced:     true,
		NewCertificate:   newCertResponse.Certificate,
		NewSecret:        newCertResponse.Secret,
		OldCertificate:   oldCertificate,
		Pool:             pool,
		GcpService:       cloudService,
	}

	// State changes after minting must not orphan the candidate. Stage first,
	// then preserve the DB-referenced candidate for a later retry.
	canProceed, err := a.checkPoolStateBeforeCriticalOperation(ctx, poolUUID)
	if err != nil {
		if rollbackErr := a.rollbackCertificateRotation(ctx, createdResources); rollbackErr != nil {
			logger.Errorf("Failed to preserve staged candidate after pool-state check error: %v", rollbackErr)
		}
		return err
	}
	if !canProceed {
		logger.Warnf("Pool %s state changed to CREATING/DELETING/UPGRADING; preserving staged certificate for retry", poolUUID)
		if rollbackErr := a.rollbackCertificateRotation(ctx, createdResources); rollbackErr != nil {
			logger.Errorf("Failed to preserve staged candidate after state change: %v", rollbackErr)
		}
		return nil
	}

	hasNodes, err := a.checkPoolHasNodes(ctx, pool)
	if err != nil {
		logger.Errorf("Failed to check if pool has nodes: %v", err)
		return err
	}

	if !hasNodes {
		logger.Errorf("Pool %s (ID: %d) has no associated nodes. This indicates a database consistency issue.", pool.UUID, pool.ID)

		rollbackErr := a.rollbackCertificateRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed: %v", rollbackErr)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrPoolHasNoNodes, fmt.Errorf("pool %s has no associated nodes - database consistency issue", pool.UUID))
	}

	logger.Info("Step 3: Installing certificate using username and password authentication")
	safeRecordHeartbeat(ctx, "Installing certificate on VSA using username and password authentication")
	err = a.installCertificateOnVSAWithPasswordAuth(ctx, pool, newCertResponse.Certificate, newCertResponse.Secret)

	if err != nil {
		logger.Errorf("Failed to install new certificate on VSA for pool %s (%s): %v", poolUUID, pool.Name, err)
		rollbackErr := a.rollbackCertificateRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed for pool %s (%s): %v", poolUUID, pool.Name, rollbackErr)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrCertificateInstallationFailed, err)
	}
	logger.Info("Successfully installed new certificate on VSA cluster", "poolUUID", poolUUID, "poolName", pool.Name)

	logger.Infof("Step 4: Testing certificate connectivity with new certificate ID: %s for pool %s (%s)", newCertificateID, poolUUID, pool.Name)
	safeRecordHeartbeat(ctx, fmt.Sprintf("Testing certificate connectivity with new certificate ID: %s", newCertificateID))

	err = a.testCertificateConnectivity(ctx, pool, newCertResponse.Certificate)
	if err != nil {
		logger.Errorf("New certificate failed connectivity test for pool %s (%s): %v", poolUUID, pool.Name, err)
		rollbackErr := a.rollbackCertificateRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed for pool %s (%s): %v", poolUUID, pool.Name, rollbackErr)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrCertificateConnectivityTestFailed, err)
	}
	logger.Info("Successfully tested certificate connectivity", "poolUUID", poolUUID, "poolName", pool.Name)

	if hasExpertModeCredentials(pool) {
		if err := a.reconcileExpertModeCredentialsBeforePromotion(ctx, poolUUID, targetCaURI, poolContext.Force); err != nil {
			return vsaerrors.NewVCPError(
				vsaerrors.ErrCertificateRotationFailed,
				fmt.Errorf("reconcile expert certificates before parent promotion: %w", err),
			)
		}
	}

	// Re-check pool state before swapping certificate IDs to avoid conflicts with pool deletion
	canProceed, err = a.checkPoolStateBeforeCriticalOperation(ctx, poolUUID)
	if err != nil {
		if rollbackErr := a.rollbackCertificateRotation(ctx, createdResources); rollbackErr != nil {
			logger.Errorf("Failed to preserve staged candidate after pool-state check error: %v", rollbackErr)
		}
		return err
	}
	if !canProceed {
		logger.Warnf("Pool %s state changed to CREATING/DELETING/UPGRADING, aborting certificate ID swap", poolUUID)
		rollbackErr := a.rollbackCertificateRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed for pool %s (%s): %v", poolUUID, pool.Name, rollbackErr)
		}
		return nil // Return nil to indicate graceful skip (not an error)
	}

	// Update pool credentials to swap certificate IDs
	logger.Infof("Swapping CertificateID: %s with new CertificateID: %s for pool %s (%s)", pool.PoolCredentials.CertificateID, newCertificateID, poolUUID, pool.Name)
	safeRecordHeartbeat(ctx, fmt.Sprintf("Swapping certificate IDs in database for pool %s", poolUUID))

	err = a.swapCertificateIDs(ctx, poolUUID, newCertificateID, secretNameForRollback)
	if err != nil {
		logger.Errorf("Failed to swap certificate IDs for pool %s (%s): %v", poolUUID, pool.Name, err)
		rollbackErr := a.rollbackCertificateRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed for pool %s (%s): %v", poolUUID, pool.Name, rollbackErr)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrCertificateIDSwapFailed, err)
	}
	logger.Info("Successfully swapped certificate IDs in database", "poolUUID", poolUUID, "poolName", pool.Name)

	logger.Infof("Certificate rotation completed successfully for pool %s (%s)", poolUUID, pool.Name)
	safeRecordHeartbeat(ctx, "Certificate rotation completed successfully")

	// Parked parent and expert certificate_id_new pairs are revoked on the next
	// reconcile, not in this activity. A post-swap expert sweep would treat the
	// just-parked expert leaf as stale and revoke it immediately.
	return nil
}

// expertModeCertificateNeedsRotation reports whether any certificate-auth expert mode credential
// of an ONTAP mode pool is due for rotation. Expert mode certificates are issued separately from
// the pool certificate and therefore expire on their own schedule.
// Kept unexported: Temporal registers every exported method of this struct as an activity and
// requires an error as the second return value.
func (a *RotateVcpToVsaCertificateActivity) expertModeCertificateNeedsRotation(ctx context.Context, pool *datamodel.Pool) bool {
	logger := util.GetLogger(ctx)

	if pool == nil || pool.APIAccessMode != common.ONTAPMode || pool.PoolCredentials == nil {
		return false
	}
	if pool.ExpertModeCredentials == nil || len(pool.ExpertModeCredentials.ExpertModeCredential) == 0 {
		return false
	}

	for _, cred := range pool.ExpertModeCredentials.ExpertModeCredential {
		if cred == nil || cred.AuthType != env.USER_CERTIFICATE || cred.CertificateID == "" {
			continue
		}
		certificateCAURI := cred.CaURI
		if certificateCAURI == "" {
			certificateCAURI = pool.PoolCredentials.GetCaURIWithFallback()
		}
		needsRotation, err := a.certificateNeedsRotationWithCA(ctx, pool, cred.CertificateID, certificateCAURI)
		if err != nil {
			// Mirrors rotateExpertModeCertificate: a certificate that cannot be fetched (revoked,
			// deleted, or expired and evicted from cache) still has to be replaced.
			logger.Warnf("Expert mode cert needs-rotation check failed for pool %s cert %s (treating as needs rotation): %v", pool.UUID, cred.CertificateID, err)
			return true
		}
		if needsRotation {
			logger.Infof("Expert mode certificate %s for pool %s needs rotation", cred.CertificateID, pool.UUID)
			return true
		}
	}

	return false
}

// rotateExpertModeCertificate rotates the certificate used for expert mode (ONTAP mode pools) when it needs rotation.
// It uses the same certificate_id_new / certificate_id staging and swap pattern as the main pool credentials.
func (a *RotateVcpToVsaCertificateActivity) rotateExpertModeCertificate(ctx context.Context, poolUUID string) error {
	poolContext, err := a.GetPoolContext(ctx, poolUUID)
	if err != nil {
		return fmt.Errorf("get pool context: %w", err)
	}
	if poolContext == nil || poolContext.Pool == nil {
		return fmt.Errorf("get pool context: pool is nil")
	}
	pool := poolContext.Pool
	if pool.APIAccessMode != common.ONTAPMode ||
		pool.ExpertModeCredentials == nil ||
		len(pool.ExpertModeCredentials.ExpertModeCredential) == 0 {
		return nil
	}
	if pool.PoolCredentials == nil {
		return fmt.Errorf("pool credentials required for expert mode rotation")
	}
	// Never forced: this runs when the pool certificate is already current so
	// expert credentials can rotate on their own schedule. Forced parent
	// rotation already reconciled expert certificates before promotion.
	return a.reconcileExpertModeCredentialsBeforePromotion(
		ctx,
		poolUUID,
		pool.PoolCredentials.GetCaURIWithFallback(),
		false,
	)
}

// updateExpertModeCertificateIDNew stages a paired expert certificate ID and issuer CA.
func (a *RotateVcpToVsaCertificateActivity) updateExpertModeCertificateIDNew(
	ctx context.Context,
	poolUUID string,
	pool *datamodel.Pool,
	credIndex int,
	newCertificateID, newCAURI string,
) error {
	logger := util.GetLogger(ctx)
	if pool.ExpertModeCredentials == nil || credIndex >= len(pool.ExpertModeCredentials.ExpertModeCredential) {
		return fmt.Errorf("invalid expert mode credential index %d", credIndex)
	}

	updatedExpert := a.cloneExpertModeCredentials(pool.ExpertModeCredentials)
	updatedExpert.ExpertModeCredential[credIndex].CertificateIDNew = newCertificateID
	updatedExpert.ExpertModeCredential[credIndex].StagedCaURI = newCAURI
	// Preserve existing certificate_id (active cert unchanged until swap)
	updates := map[string]interface{}{
		"expert_mode_credentials": updatedExpert,
	}
	if err := a.SE.UpdatePoolFields(ctx, poolUUID, updates); err != nil {
		logger.Errorf("Failed to stage expert mode certificate_id_new: %v", err)
		return err
	}
	return nil
}

// swapExpertModeCertificateIDs swaps certificate_id and certificate_id_new for the given expert mode credential index.
func (a *RotateVcpToVsaCertificateActivity) swapExpertModeCertificateIDs(ctx context.Context, poolUUID string, credIndex int) error {
	logger := util.GetLogger(ctx)
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil || len(poolViews) == 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("pool %s not found", poolUUID)
	}
	pool := ConvertPoolViewToPool(poolViews[0])
	if pool.ExpertModeCredentials == nil || credIndex >= len(pool.ExpertModeCredentials.ExpertModeCredential) {
		return fmt.Errorf("invalid expert mode credential index %d", credIndex)
	}

	cred := pool.ExpertModeCredentials.ExpertModeCredential[credIndex]
	oldCertID := cred.CertificateID
	oldCAURI := cred.CaURI
	newCertIDFromDB := cred.CertificateIDNew
	newCAURIFromDB := cred.StagedCaURI
	if newCertIDFromDB == "" {
		return fmt.Errorf("expert mode certificate_id_new is empty, cannot swap")
	}
	if newCAURIFromDB == "" {
		return fmt.Errorf("expert mode staged_ca_uri is empty, cannot swap")
	}
	if oldCertID == "" || oldCAURI == "" {
		oldCertID = ""
		oldCAURI = ""
	}

	updatedExpert := a.cloneExpertModeCredentials(pool.ExpertModeCredentials)
	updatedExpert.ExpertModeCredential[credIndex].CertificateID = newCertIDFromDB
	updatedExpert.ExpertModeCredential[credIndex].CaURI = newCAURIFromDB
	updatedExpert.ExpertModeCredential[credIndex].CertificateIDNew = oldCertID
	updatedExpert.ExpertModeCredential[credIndex].StagedCaURI = oldCAURI

	updates := map[string]interface{}{
		"expert_mode_credentials": updatedExpert,
	}
	if err := a.SE.UpdatePoolFields(ctx, poolUUID, updates); err != nil {
		logger.Errorf("Failed to swap expert mode certificate IDs: %v", err)
		return err
	}
	return nil
}

// cloneExpertModeCredentials returns a deep copy of expert_mode_credentials for in-place updates.
func (a *RotateVcpToVsaCertificateActivity) cloneExpertModeCredentials(emc *datamodel.ExpertModeCredentials) *datamodel.ExpertModeCredentials {
	if emc == nil {
		return nil
	}
	out := &datamodel.ExpertModeCredentials{
		ExpertModeCredential: make([]*datamodel.ExpertModeCredential, len(emc.ExpertModeCredential)),
	}
	for i, c := range emc.ExpertModeCredential {
		if c == nil {
			continue
		}
		out.ExpertModeCredential[i] = &datamodel.ExpertModeCredential{
			SecretID:           c.SecretID,
			CertificateID:      c.CertificateID,
			CertificateIDNew:   c.CertificateIDNew,
			CaURI:              c.CaURI,
			StagedCaURI:        c.StagedCaURI,
			Password:           c.Password,
			Username:           c.Username,
			UsernamePreCertFix: c.UsernamePreCertFix,
			AuthType:           c.AuthType,
		}
	}
	return out
}

// extractSecretNameFromPath extracts just the secret name from a full GCP Secret Manager path
// Input: "projects/266893635349/secrets/gcnv-46575836622ae43-secret-20250916-113000/versions/1"
// Output: "gcnv-46575836622ae43-secret-20250916-113000"
func extractSecretNameFromPath(fullPath string) string {
	if fullPath == "" {
		return ""
	}

	// Split by "/" and look for the "secrets/" part
	parts := strings.Split(fullPath, "/")
	for i, part := range parts {
		if part == "secrets" && i+1 < len(parts) {
			// The next part should be the secret name
			secretName := parts[i+1]
			// Remove any version suffix if present (e.g., "/versions/1")
			if strings.Contains(secretName, "/") {
				secretName = strings.Split(secretName, "/")[0]
			}
			return secretName
		}
	}

	// If we can't find the expected format, return the original string
	return fullPath
}

// checkPoolHasNodes checks if a pool has associated nodes in the database
func (a *RotateVcpToVsaCertificateActivity) checkPoolHasNodes(ctx context.Context, pool *datamodel.Pool) (bool, error) {
	logger := util.GetLogger(ctx)

	// Get nodes directly from database using pool ID
	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get node information from database: %v", err)
		return false, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// Check if we have any nodes
	if len(dbNodes) == 0 {
		logger.Warnf("No nodes found for pool %s (ID: %d). This indicates a database consistency issue.", pool.UUID, pool.ID)
		return false, nil
	}

	// Validate that nodes have valid endpoint addresses
	validNodes := 0
	for _, node := range dbNodes {
		if node.EndpointAddress != "" {
			validNodes++
		} else {
			logger.Warnf("Node %s (ID: %d) has empty endpoint address", node.Name, node.ID)
		}
	}

	if validNodes == 0 {
		logger.Warnf("No valid nodes found for pool %s (ID: %d) - all nodes have empty endpoint addresses", pool.UUID, pool.ID)
		return false, nil
	}

	return true, nil
}

// installCertificateOnVSA installs a new certificate on the VSA cluster using ONTAP REST API
func (a *RotateVcpToVsaCertificateActivity) installCertificateOnVSA(ctx context.Context, pool *datamodel.Pool, certificate interface{}, secret interface{}) error {
	logger := util.GetLogger(ctx)

	// Get nodes directly from database using pool ID
	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get node information from database: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// Validate that we have nodes for this pool
	if len(dbNodes) == 0 {
		logger.Errorf("No nodes found for pool %s (ID: %d). This indicates a database consistency issue.", pool.UUID, pool.ID)
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("no nodes found for pool %s (ID: %d) - database consistency issue", pool.UUID, pool.ID))
	}

	password := pool.PoolCredentials.Password
	if password == "" && pool.PoolCredentials.AuthType == env.USER_CERTIFICATE {
		common.RemoveFromUserAuthCache(pool.PoolCredentials.SecretID)

		secret, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretID)
		if err != nil {
			logger.Errorf("Failed to get password from Secret Manager: %v", err)
			return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
		}
		password = secret
	}

	// Get username from pool credentials, fallback to deployment name with admin suffix
	// This matches the logic used during certificate generation
	username := pool.PoolCredentials.Username
	if username == "" {
		// Fallback: use deployment name with admin suffix (matches certificate generation fallback)
		username = fmt.Sprintf("%s_admin", pool.DeploymentName)
		logger.Warnf("Username is empty in pool credentials, using fallback: %s", username)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:          dbNodes,
		DeploymentName: pool.DeploymentName,
		OntapCredentials: &datamodel.PoolCredentials{
			Password:      password,
			SecretID:      pool.PoolCredentials.SecretID,
			CertificateID: pool.PoolCredentials.CertificateID,
			AuthType:      pool.PoolCredentials.AuthType,
			CaURI:         pool.PoolCredentials.GetCaURIWithFallback(),
			Username:      username,
		},
	})

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		errMsg := err.Error()
		isTrulyExpired := strings.Contains(errMsg, "certificate has expired") ||
			strings.Contains(errMsg, "certificate has expired or is not yet valid")

		if isTrulyExpired {
			logger.Warnf("Certificate authentication failed with expired certificate error during provider creation - falling back to password authentication: %v", err)
			return a.installCertificateOnVSAWithPasswordAuth(ctx, pool, certificate, secret)
		}
		logger.Errorf("Failed to get VSA provider: %v", err)
		return err
	}

	if provider == nil {
		logger.Error("Provider is nil")
		return fmt.Errorf("provider is nil")
	}

	var cert *hyperscalermodels.CustomCertificate
	var secretObj *hyperscalermodels.CustomSecret
	var convertErr error

	if directCert, ok := certificate.(*hyperscalermodels.CustomCertificate); ok {
		cert = directCert
	} else if certMap, ok := certificate.(map[string]interface{}); ok {
		cert, convertErr = convertMapToCustomCertificate(certMap)
		if convertErr != nil {
			logger.Errorf("Failed to convert certificate map to struct: %v", convertErr)
			return fmt.Errorf("failed to convert certificate map to struct: %v", convertErr)
		}
	} else {
		logger.Errorf("Invalid certificate type: %T", certificate)
		return fmt.Errorf("invalid certificate type: %T", certificate)
	}

	// Try direct type assertion first
	if directSecret, ok := secret.(*hyperscalermodels.CustomSecret); ok {
		secretObj = directSecret
	} else if secretMap, ok := secret.(map[string]interface{}); ok {
		secretObj, convertErr = convertMapToCustomSecret(secretMap)
		if convertErr != nil {
			logger.Errorf("Failed to convert secret map to struct: %v", convertErr)
			return fmt.Errorf("failed to convert secret map to struct: %v", convertErr)
		}
	} else {
		logger.Errorf("Invalid secret type: %T", secret)
		return fmt.Errorf("invalid secret type: %T", secret)
	}

	vserverName := resolveVserverName(pool)
	logger.Infof("Resolved vserver name: %q (deploymentName: %q)", vserverName, pool.DeploymentName)

	intermediateCertificates, err := validatedCertificateChain(cert)
	if err != nil {
		return err
	}
	if len(intermediateCertificates) > 0 {
		if err := installIssuingClientCA(provider, cert, intermediateCertificates); err != nil {
			return fmt.Errorf("install issuing client CA: %w", err)
		}
	}

	installParams := vsa.InstallServerCertificateParams{
		SvmName:                  vserverName,
		CertificateName:          cert.CertificateID,
		Certificate:              cert.PemCertificate,
		PrivateKey:               secretObj.SecretVersion.Value,
		IntermediateCertificates: intermediateCertificates,
		CertificateType:          "server",
		CommonName:               username,
	}

	_, err = provider.InstallServerCertificate(installParams)
	if err != nil {
		if isDuplicateCertificateError(err) {
			logger.Infof("Certificate %s is already installed on VSA; continuing idempotent rotation", cert.CertificateID)
			err = nil
		}
		if isSVMNotFoundError(err) && vserverName != pool.DeploymentName {
			logger.Warnf("SVM %q not found, retrying with deployment name: %q", vserverName, pool.DeploymentName)
			vserverName = pool.DeploymentName
			installParams.SvmName = pool.DeploymentName
			_, err = provider.InstallServerCertificate(installParams)
			if isDuplicateCertificateError(err) {
				logger.Infof("Certificate %s is already installed on fallback SVM; continuing idempotent rotation", cert.CertificateID)
				err = nil
			}
		}

		if err != nil {
			errMsg := err.Error()
			isTrulyExpired := strings.Contains(errMsg, "certificate has expired") ||
				strings.Contains(errMsg, "certificate has expired or is not yet valid")

			if isTrulyExpired {
				logger.Warnf("Certificate authentication failed with expired certificate error during installation - falling back to password authentication: %v", err)
				return a.installCertificateOnVSAWithPasswordAuth(ctx, pool, certificate, secret)
			}

			logger.Errorf("Failed to install certificate on VSA: %v", err)
			return err
		}
	}
	logger.Info("Certificate installation via REST API completed successfully", "certificateID", cert.CertificateID, "vserverName", vserverName)

	logger.Debug("Configuring SSL to use the new certificate")

	if cert.SerialNumber == "" {
		logger.Warnf("Certificate serial number is empty, cannot configure SSL. Certificate ID: %s", cert.CertificateID)
		return fmt.Errorf("certificate serial number is empty, cannot configure SSL")
	}

	caForSSL := deriveCANameFromPEM(cert.PemCertificate)
	if caForSSL == "" {
		caForSSL = cert.CaName
		logger.Debugf("Using certificate CaName for SSL modify (PEM-derived CA empty, fallback): %q", caForSSL)
	} else {
		logger.Debugf("Using PEM-derived issuer CN for SSL modify (same as install): %q", caForSSL)
	}
	if caForSSL == "" {
		logger.Warnf("Certificate CA name is empty, cannot configure SSL. Certificate ID: %s", cert.CertificateID)
		return fmt.Errorf("certificate CA name is empty, cannot configure SSL")
	}

	sslParams := vsa.ModifySSLParams{
		SvmName:       vserverName,
		ServerEnabled: true,
		CA:            caForSSL,
		Serial:        strings.ToUpper(cert.SerialNumber),
	}

	sslResponse, sslErr := provider.ModifySSL(sslParams)
	if sslErr != nil {
		logger.Errorf("Failed to configure SSL with new certificate: %v", sslErr)
		return fmt.Errorf("failed to configure SSL with new certificate: %v", sslErr)
	}
	if sslResponse != nil && !sslResponse.Success {
		logger.Errorf("SSL configuration failed: %s", sslResponse.Message)
		return fmt.Errorf("SSL configuration failed: %s", sslResponse.Message)
	}
	logger.Debug("SSL configuration succeeded")

	return nil
}

// swapCertificateIDs updates the pool credentials to swap old and new certificate IDs
func (a *RotateVcpToVsaCertificateActivity) swapCertificateIDs(ctx context.Context, poolUUID, newCertificateID, newSecretID string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Starting certificate and secret ID swap for pool %s to new certificate %s (certificate secret: %s)", poolUUID, newCertificateID, newSecretID)

	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get current pool for credential preservation: %v", err)
		return err
	}
	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found for credential preservation", poolUUID)
		return fmt.Errorf("pool %s not found", poolUUID)
	}

	pool := ConvertPoolViewToPool(poolViews[0])
	if pool.PoolCredentials == nil {
		logger.Errorf("Pool %s has no credentials to preserve", poolUUID)
		return fmt.Errorf("pool %s has no credentials", poolUUID)
	}

	oldCertificateID := pool.PoolCredentials.CertificateID
	newCertificateIDFromDB := pool.PoolCredentials.CertificateIDNew
	// Park the displaced leaf under its persisted issuer. Helm fallback would
	// attribute a legacy empty ca_uri to the current desired CA and break later
	// revoke/reuse against the CA that actually minted the old certificate.
	oldCaURI := pool.PoolCredentials.CaURI
	newCaURIFromDB := pool.PoolCredentials.StagedCaURI

	if newCertificateIDFromDB == "" {
		logger.Errorf("certificate_id_new is empty, cannot swap certificate IDs")
		return fmt.Errorf("certificate_id_new is empty, cannot swap certificate IDs")
	}
	if newCaURIFromDB == "" {
		return fmt.Errorf("staged_ca_uri is empty, cannot swap certificate IDs")
	}
	if newCertificateIDFromDB != newCertificateID {
		return fmt.Errorf(
			"staged certificate changed before promotion: expected %s, found %s",
			newCertificateID,
			newCertificateIDFromDB,
		)
	}

	logger.Infof("Swapping certificate IDs: %s -> %s", oldCertificateID, newCertificateIDFromDB)

	updatedCredentials := *pool.PoolCredentials
	updatedCredentials.CertificateID = newCertificateIDFromDB
	updatedCredentials.CertificateIDNew = oldCertificateID
	updatedCredentials.CaURI = newCaURIFromDB
	updatedCredentials.StagedCaURI = oldCaURI
	err = a.SE.CompareAndSwapPoolCredentials(
		ctx,
		poolUUID,
		pool.PoolCredentials.AuthType,
		newCertificateIDFromDB,
		pool.PoolCredentials.StagedCaURI,
		env.GetCertificateRotationPoolStates(),
		&updatedCredentials,
	)
	if err != nil {
		logger.Errorf("Failed to update pool credentials in database: %v", err)
		return err
	}

	logger.Debug("Successfully swapped certificate and secret IDs in database")

	// Immediately update the certificate cache with the new certificate
	logger.Debug("Updating certificate cache with new certificate")
	err = a.updateCertificateCache(ctx, newCertificateIDFromDB, newCaURIFromDB)

	if err != nil {
		logger.Warnf("Failed to update certificate cache: %v", err)
		// Don't fail the rotation if cache update fails, but log the warning
		// This is a non-critical error, so we don't return it
	} else {
		logger.Debug("Successfully updated certificate cache with new certificate")
	}

	return nil
}

// updateCertificateCache updates the certificate cache with the new certificate
func (a *RotateVcpToVsaCertificateActivity) updateCertificateCache(ctx context.Context, certificateID, caURI string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Updating certificate cache for certificate ID: %s", certificateID)

	// Get the new certificate from the cloud provider
	cloudService, err := getGcpServiceForCerts(ctx)
	if err != nil {
		logger.Errorf("Failed to get cloud service for cache update: %v", err)
		return err
	}

	// Get the certificate and private key
	caProjectID, caPoolName, _ := env.ParseCaURI(caURI)
	if caProjectID == "" || caPoolName == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, fmt.Errorf("invalid CA URI %q", caURI))
	}
	certificateResponse, err := vsa.GetCertificateAndPrivateKeyByID(cloudService, caProjectID, env.SecretManagerProjectID, env.Region, caPoolName, certificateID)
	if err != nil {
		logger.Errorf("Failed to get certificate for cache update: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrCertificateCacheUpdateFailed, err)
	}

	cert := certificateResponse.Certificate

	// Convert hyperscaler certificate to core models certificate
	coreCert := &models.Certificate{
		SignedCertificate:        cert.PemCertificate,
		PrivateKey:               certificateResponse.Secret.SecretVersion.Value,
		InterMediateCertificates: cert.PemCertificateChain,
		CommonName:               cert.SubjectCommonName,
	}

	// Add the new certificate to cache
	addToCertAuthCache(certificateID, coreCert)
	logger.Debugf("Successfully added certificate %s to cache", certificateID)

	return nil
}

// passwordAuthProvider builds an ONTAP provider using the bootstrap admin
// identity and the pool password stored in Secret Manager.
func (a *RotateVcpToVsaCertificateActivity) passwordAuthProvider(ctx context.Context, pool *datamodel.Pool) (vsa.Provider, error) {
	logger := util.GetLogger(ctx)

	// Get nodes directly from database using pool ID
	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get node information from database: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	logger.Debugf("Retrieved %d nodes from database", len(dbNodes))

	// Validate that we have nodes for this pool
	if len(dbNodes) == 0 {
		logger.Errorf("No nodes found for pool %s (ID: %d). This indicates a database consistency issue.", pool.UUID, pool.ID)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("no nodes found for pool %s (ID: %d) - database consistency issue", pool.UUID, pool.ID))
	}

	// Fetch the current pool password used for ONTAP REST basic authentication.
	password := pool.PoolCredentials.Password
	if password == "" {
		// For any auth type, fetch password from the secretID field
		logger.Debugf("Password is empty in pool_credentials, fetching from SecretID: %s", pool.PoolCredentials.SecretID)

		// CRITICAL: Clear cache to ensure we get the latest password from Secret Manager
		logger.Debug("Clearing password cache to ensure fresh password fetch")
		common.RemoveFromUserAuthCache(pool.PoolCredentials.SecretID)

		secret, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretID)
		if err != nil {
			logger.Errorf("Failed to get password from Secret Manager: %v", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
		}
		password = secret
	}

	// Create node for provider using password authentication (not certificate auth)
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:          dbNodes,
		DeploymentName: pool.DeploymentName,
		OntapCredentials: &datamodel.PoolCredentials{
			Password:      password, // Use the fetched password
			SecretID:      pool.PoolCredentials.SecretID,
			CertificateID: pool.PoolCredentials.CertificateID,
			AuthType:      env.USERNAME_PWD_SEC_MGR, // Always use password authentication for certificate installation.
			CaURI:         pool.PoolCredentials.GetCaURIWithFallback(),
			Username:      env.Admin,
		},
	})
	// Create VSA provider with password authentication
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to get VSA provider for password-authenticated certificate installation: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrVSAClusterCreateError, err)
	}
	logger.Debug("Successfully created VSA provider with password authentication")

	return provider, nil
}

// installExpertClientTrustWithPasswordAuth trusts the CA chain that issued an expert-mode
// client certificate so ONTAP accepts that certificate for client authentication.
//
// Expert-mode certificates are minted with client auth only (see
// EnableServerAuthInCSRForExpMode), so they must never be installed as the cluster
// server certificate or selected by "security ssl modify": doing so makes ONTAP serve a
// certificate that TLS clients reject for incompatible key usage.
func (a *RotateVcpToVsaCertificateActivity) installExpertClientTrustWithPasswordAuth(ctx context.Context, pool *datamodel.Pool, certificate interface{}, _ interface{}) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Installing expert-mode client CA trust with username and password authentication for pool %s", pool.UUID)

	provider, err := a.passwordAuthProvider(ctx, pool)
	if err != nil {
		return err
	}

	cert, err := asCustomCertificate(certificate)
	if err != nil {
		logger.Errorf("Failed to resolve expert certificate for client CA trust: %v", err)
		return err
	}

	intermediateCertificates, err := validatedCertificateChain(cert)
	if err != nil {
		return err
	}
	if len(intermediateCertificates) == 0 {
		logger.Warnf("Expert certificate %s carries no issuing CA chain; relying on previously installed client CA trust", cert.CertificateID)
		return nil
	}
	if err := installIssuingClientCA(provider, cert, intermediateCertificates); err != nil {
		return fmt.Errorf("install issuing client CA: %w", err)
	}

	logger.Info("Installed expert-mode client CA trust", "certificateID", cert.CertificateID)
	return nil
}

// asCustomCertificate resolves a certificate that Temporal may have round-tripped
// into a map back into its concrete type.
func asCustomCertificate(certificate interface{}) (*hyperscalermodels.CustomCertificate, error) {
	if directCert, ok := certificate.(*hyperscalermodels.CustomCertificate); ok {
		return directCert, nil
	}
	certMap, ok := certificate.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid certificate type: %T", certificate)
	}
	cert, err := convertMapToCustomCertificate(certMap)
	if err != nil {
		return nil, fmt.Errorf("failed to convert certificate map to struct: %v", err)
	}
	return cert, nil
}

// installCertificateOnVSAWithPasswordAuth installs a new certificate through
// ONTAP REST using the bootstrap admin username and password.
func (a *RotateVcpToVsaCertificateActivity) installCertificateOnVSAWithPasswordAuth(ctx context.Context, pool *datamodel.Pool, certificate interface{}, secret interface{}) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Installing certificate with username and password authentication for pool %s", pool.UUID)

	provider, err := a.passwordAuthProvider(ctx, pool)
	if err != nil {
		return err
	}

	var cert *hyperscalermodels.CustomCertificate
	var secretObj *hyperscalermodels.CustomSecret
	var convertErr error

	if directCert, ok := certificate.(*hyperscalermodels.CustomCertificate); ok {
		cert = directCert
	} else if certMap, ok := certificate.(map[string]interface{}); ok {
		cert, convertErr = convertMapToCustomCertificate(certMap)
		if convertErr != nil {
			logger.Errorf("Failed to convert certificate map to struct: %v", convertErr)
			return fmt.Errorf("failed to convert certificate map to struct: %v", convertErr)
		}
	} else {
		logger.Errorf("Invalid certificate type: %T", certificate)
		return fmt.Errorf("invalid certificate type: %T", certificate)
	}

	// Try direct type assertion first
	if directSecret, ok := secret.(*hyperscalermodels.CustomSecret); ok {
		secretObj = directSecret
	} else if secretMap, ok := secret.(map[string]interface{}); ok {
		secretObj, convertErr = convertMapToCustomSecret(secretMap)
		if convertErr != nil {
			logger.Errorf("Failed to convert secret map to struct: %v", convertErr)
			return fmt.Errorf("failed to convert secret map to struct: %v", convertErr)
		}
	} else {
		logger.Errorf("Invalid secret type: %T", secret)
		return fmt.Errorf("invalid secret type: %T", secret)
	}

	vserverName := resolveVserverName(pool)
	logger.Infof("Resolved vserver name (password auth): %q (deploymentName: %q)", vserverName, pool.DeploymentName)

	intermediateCertificates, err := validatedCertificateChain(cert)
	if err != nil {
		return err
	}
	if len(intermediateCertificates) > 0 {
		if err := installIssuingClientCA(provider, cert, intermediateCertificates); err != nil {
			return fmt.Errorf("install issuing client CA: %w", err)
		}
	}

	certificateCommonName := strings.TrimSpace(cert.SubjectCommonName)
	if certificateCommonName == "" {
		certificateCommonName = strings.TrimSpace(pool.PoolCredentials.Username)
	}
	if certificateCommonName == "" {
		return fmt.Errorf("certificate %s has no subject common name", cert.CertificateID)
	}
	installParams := vsa.InstallServerCertificateParams{
		SvmName:                  vserverName,
		CertificateName:          cert.CertificateID,
		Certificate:              cert.PemCertificate,
		PrivateKey:               secretObj.SecretVersion.Value,
		IntermediateCertificates: intermediateCertificates,
		CertificateType:          "server",
		CommonName:               certificateCommonName,
	}

	_, err = provider.InstallServerCertificate(installParams)
	if err != nil {
		if isDuplicateCertificateError(err) {
			logger.Infof("Certificate %s is already installed on VSA; continuing idempotent password-auth reconciliation", cert.CertificateID)
			err = nil
		}
		if isSVMNotFoundError(err) && vserverName != pool.DeploymentName {
			logger.Warnf("SVM %q not found (password auth), retrying with deployment name: %q", vserverName, pool.DeploymentName)
			vserverName = pool.DeploymentName
			installParams.SvmName = pool.DeploymentName
			_, err = provider.InstallServerCertificate(installParams)
			if isDuplicateCertificateError(err) {
				logger.Infof("Certificate %s is already installed on fallback SVM; continuing idempotent password-auth reconciliation", cert.CertificateID)
				err = nil
			}
		}
		if err != nil {
			logger.Errorf("Failed to install certificate on VSA with password auth: %v", err)
			return err
		}
	}
	logger.Info("Certificate installation via REST API (password auth) completed successfully", "certificateID", cert.CertificateID, "vserverName", vserverName)

	logger.Debug("Configuring SSL to use the new certificate")

	if cert.SerialNumber == "" {
		logger.Warnf("Certificate serial number is empty, cannot configure SSL. Certificate ID: %s", cert.CertificateID)
		return fmt.Errorf("certificate serial number is empty, cannot configure SSL")
	}

	caForSSL := deriveCANameFromPEM(cert.PemCertificate)
	if caForSSL == "" {
		caForSSL = cert.CaName
		logger.Debugf("Using certificate CaName for SSL modify (PEM-derived CA empty, fallback): %q", caForSSL)
	} else {
		logger.Debugf("Using PEM-derived issuer CN for SSL modify (same as install): %q", caForSSL)
	}
	if caForSSL == "" {
		logger.Warnf("Certificate CA name is empty, cannot configure SSL. Certificate ID: %s", cert.CertificateID)
		return fmt.Errorf("certificate CA name is empty, cannot configure SSL")
	}

	sslParams := vsa.ModifySSLParams{
		SvmName:       vserverName,
		ServerEnabled: true,
		CA:            caForSSL,
		Serial:        strings.ToUpper(cert.SerialNumber),
	}

	sslResponse, sslErr := provider.ModifySSL(sslParams)
	if sslErr != nil {
		logger.Errorf("Failed to configure SSL with new certificate: %v", sslErr)
		return fmt.Errorf("failed to configure SSL with new certificate: %v", sslErr)
	}
	if sslResponse != nil && !sslResponse.Success {
		logger.Errorf("SSL configuration failed: %s", sslResponse.Message)
		return fmt.Errorf("SSL configuration failed: %s", sslResponse.Message)
	}
	logger.Debug("SSL configuration succeeded")

	coreCert := &models.Certificate{
		SignedCertificate:        cert.PemCertificate,
		PrivateKey:               secretObj.SecretVersion.Value,
		InterMediateCertificates: intermediateCertificates,
		CommonName:               cert.SubjectCommonName,
	}

	addToCertAuthCache(cert.CertificateID, coreCert)
	logger.Debugf("Successfully added certificate %s to cache", cert.CertificateID)

	return nil
}

// isCertificateExpired checks if a certificate is expired
func (a *RotateVcpToVsaCertificateActivity) isCertificateExpired(ctx context.Context, certificateID string) (bool, error) {
	return a.isCertificateExpiredAtCA(ctx, certificateID, defaultCertificateCaURI())
}

func (a *RotateVcpToVsaCertificateActivity) isCertificateExpiredAtCA(ctx context.Context, certificateID, caURI string) (bool, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Checking if certificate %s is expired", certificateID)

	poolCredentials := &datamodel.PoolCredentials{
		CertificateID: certificateID,
		CaURI:         caURI,
	}

	certificate, err := getCertificateFromCacheOrSecretManager(ctx, poolCredentials)
	if err != nil {
		logger.Errorf("Failed to get certificate %s: %v", certificateID, err)
		return false, err
	}

	if certificate == nil {
		logger.Warnf("Certificate %s not found, assuming expired", certificateID)
		return true, nil
	}

	if certificate.SignedCertificate == "" {
		logger.Warnf("Certificate %s has no signed certificate content, assuming expired", certificateID)
		return true, nil
	}

	expirationTime, err := parseCertificateExpiration(certificate.SignedCertificate)
	if err != nil {
		logger.Warnf("Failed to parse certificate expiration for %s: %v", certificateID, err)
		return true, nil
	}

	now := time.Now()
	isExpired := now.After(expirationTime)

	if isExpired {
		logger.Warnf("Certificate %s is expired (expired at: %v, current time: %v)", certificateID, expirationTime, now)
	} else {
		logger.Debugf("Certificate %s is valid until %v", certificateID, expirationTime)
	}

	return isExpired, nil
}

// parseCertificateExpiration parses a PEM certificate and returns its expiration time
func parseCertificateExpiration(pemCertificate string) (time.Time, error) {
	if pemCertificate == "" {
		return time.Time{}, errors.New("empty certificate")
	}

	// Decode the PEM block
	block, _ := pem.Decode([]byte(pemCertificate))
	if block == nil {
		return time.Time{}, errors.New("failed to decode PEM block")
	}

	// Parse the certificate
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert.NotAfter, nil
}

func validatedCertificateChain(cert *hyperscalermodels.CustomCertificate) ([]string, error) {
	if cert == nil {
		return nil, errors.New("certificate is nil")
	}
	chain := make([]string, 0, len(cert.PemCertificateChain))
	for _, certificatePEM := range cert.PemCertificateChain {
		if trimmed := strings.TrimSpace(certificatePEM); trimmed != "" {
			chain = append(chain, trimmed)
		}
	}
	if len(chain) == 0 {
		return nil, nil
	}

	leafBlock, _ := pem.Decode([]byte(cert.PemCertificate))
	issuerBlock, _ := pem.Decode([]byte(chain[0]))
	if leafBlock == nil || issuerBlock == nil {
		return nil, fmt.Errorf("certificate %s leaf or issuing CA PEM cannot be decoded", cert.CertificateID)
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate %s leaf: %w", cert.CertificateID, err)
	}
	issuer, err := x509.ParseCertificate(issuerBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate %s issuing CA: %w", cert.CertificateID, err)
	}
	if leaf.Issuer.String() != issuer.Subject.String() {
		return nil, fmt.Errorf("certificate %s chain ordering is invalid: first chain certificate subject %q does not match leaf issuer %q",
			cert.CertificateID, issuer.Subject.String(), leaf.Issuer.String())
	}
	child := issuer
	for index := 1; index < len(chain); index++ {
		block, _ := pem.Decode([]byte(chain[index]))
		if block == nil {
			return nil, fmt.Errorf("certificate %s chain entry %d cannot be decoded", cert.CertificateID, index)
		}
		parent, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate %s chain entry %d: %w", cert.CertificateID, index, err)
		}
		if child.Issuer.String() != parent.Subject.String() {
			return nil, fmt.Errorf("certificate %s chain ordering is invalid at entry %d", cert.CertificateID, index)
		}
		child = parent
	}
	return chain, nil
}

func installIssuingClientCA(provider vsa.Provider, cert *hyperscalermodels.CustomCertificate, chain []string) error {
	if len(chain) == 0 {
		return errors.New("issuing CA certificate is required")
	}
	for index := len(chain) - 1; index >= 0; index-- {
		commonName := strings.TrimSpace(cert.CaName)
		if block, _ := pem.Decode([]byte(chain[index])); block != nil {
			if caCertificate, err := x509.ParseCertificate(block.Bytes); err == nil && strings.TrimSpace(caCertificate.Subject.CommonName) != "" {
				commonName = strings.TrimSpace(caCertificate.Subject.CommonName)
			}
		}
		if commonName == "" {
			return fmt.Errorf("certificate %s chain entry %d common name is empty", cert.CertificateID, index)
		}
		_, err := provider.InstallClientCA(vsa.InstallClientCAParams{
			SvmName:         "",
			CertificateName: commonName,
			Certificate:     chain[index],
			CommonName:      commonName,
		})
		if err != nil && !isDuplicateCertificateError(err) {
			return err
		}
	}
	return nil
}

// deriveCANameFromPEM extracts the issuer Common Name from the certificate PEM.
// This is the same CA name ONTAP derives when it installs the cert (from the chain in the PEM),
// and should be used for "security ssl modify" so the CLI finds the cert.
// Returns empty string if parsing fails or issuer CN is empty.
func deriveCANameFromPEM(pemCertificate string) string {
	if pemCertificate == "" {
		return ""
	}
	block, _ := pem.Decode([]byte(pemCertificate))
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cert.Issuer.CommonName)
}

// serialHexFromPEM extracts the certificate serial number from the PEM and renders
// it as ONTAP expects for "security ssl modify -serial": uppercase hex, no
// separators, zero-padded to an even number of digits (whole DER bytes).
//
// The serial is read straight from the X.509 certificate (the same DER bytes ONTAP
// hashes when it stores the cert), so the value is guaranteed to match ONTAP's
// recorded serial regardless of how the issuing API formats its own serial string.
// This matters for OCI, whose certificates API returns the serial as a decimal
// string (despite documenting octet-hex), which ONTAP rejects as an invalid
// "-serial" value. Returns an error if the PEM cannot be parsed.
func serialHexFromPEM(pemCertificate string) (string, error) {
	if pemCertificate == "" {
		return "", fmt.Errorf("empty certificate PEM")
	}
	block, _ := pem.Decode([]byte(pemCertificate))
	if block == nil {
		return "", fmt.Errorf("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse certificate: %w", err)
	}
	if cert.SerialNumber == nil {
		return "", fmt.Errorf("certificate has no serial number")
	}

	hexSerial := fmt.Sprintf("%X", cert.SerialNumber)
	if len(hexSerial)%2 != 0 {
		hexSerial = "0" + hexSerial
	}
	return hexSerial, nil
}

// certificateNeedsRotation determines if a certificate needs to be rotated
// Checks both time-based rotation (configurable percentage of lifetime via CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE) and actual certificate expiration
func (a *RotateVcpToVsaCertificateActivity) certificateNeedsRotation(ctx context.Context, pool *datamodel.Pool, certificateID string) (bool, error) {
	return a.certificateNeedsRotationWithCA(
		ctx,
		pool,
		certificateID,
		pool.PoolCredentials.GetCaURIWithFallback(),
	)
}

func (a *RotateVcpToVsaCertificateActivity) certificateNeedsRotationWithCA(
	ctx context.Context,
	pool *datamodel.Pool,
	certificateID, certificateCAURI string,
) (bool, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Checking if certificate %s needs rotation for pool %s", certificateID, pool.UUID)

	// Create PoolCredentials for certificate lookup using the certificate's
	// persisted issuer. Expert credentials can have a different active CA.
	poolCredentials := &datamodel.PoolCredentials{
		CertificateID: certificateID,
		CaURI:         certificateCAURI,
	}

	// Get certificate from cache or secret manager
	logger.Debugf("Retrieving certificate %s from cache or secret manager", certificateID)
	certificate, err := getCertificateFromCacheOrSecretManager(ctx, poolCredentials)
	if err != nil {
		// Check if the error indicates a revoked certificate
		if strings.Contains(err.Error(), "is revoked and cannot be used") {
			logger.Warnf("Certificate %s is revoked, needs rotation", certificateID)
			return true, nil
		}
		logger.Errorf("Failed to get certificate %s: %v", certificateID, err)
		return false, err
	}

	if certificate == nil {
		logger.Warnf("Certificate %s not found, needs rotation", certificateID)
		return true, nil
	}
	logger.Debug("Certificate found in cache or secret manager")

	// Check if force rotation of expired certificates is enabled
	forceExpiredRotation := env.GetBool("ENABLE_VSA_EXPIRED_CERTIFICATES_ROTATION", false)
	if forceExpiredRotation {
		logger.Debug("Force rotation of expired certificates is enabled")

		// Check certificate expiration when force rotation is enabled
		if certificate.SignedCertificate != "" {
			expirationTime, err := parseCertificateExpiration(certificate.SignedCertificate)
			if err != nil {
				logger.Warnf("Failed to parse certificate expiration for %s: %v", certificateID, err)
				// If we can't parse expiration, assume it needs rotation for safety
				return true, nil
			}

			now := time.Now()
			isExpired := now.After(expirationTime)

			if isExpired {
				logger.Warnf("Force rotation enabled - Certificate %s is expired (expired at: %v, current time: %v), needs rotation",
					certificateID, expirationTime, now)
				return true, nil
			}

			logger.Debugf("Force rotation enabled - Certificate %s is valid until %v", certificateID, expirationTime)
		}
	}

	// Check if certificate exists and has valid content
	if certificate.SignedCertificate == "" {
		logger.Warnf("Certificate %s has no signed certificate content, needs rotation", certificateID)
		return true, nil
	}

	// Parse the actual certificate expiration time
	expirationTime, err := parseCertificateExpiration(certificate.SignedCertificate)
	if err != nil {
		logger.Warnf("Failed to parse certificate expiration for %s: %v", certificateID, err)
		// If we can't parse expiration, assume it needs rotation for safety
		return true, nil
	}

	now := time.Now()
	logger.Debugf("Certificate %s expires at: %v, current time: %v", certificateID, expirationTime, now)

	// Check if certificate is already expired
	if now.After(expirationTime) {
		logger.Warnf("Certificate %s is expired (expired at: %v, current time: %v), needs rotation", certificateID, expirationTime, now)
		return true, nil
	}

	// Calculate the actual certificate lifetime (from NotBefore to NotAfter)
	// We need to get the NotBefore time from the certificate
	block, _ := pem.Decode([]byte(certificate.SignedCertificate))
	if block == nil {
		logger.Warnf("Failed to decode PEM block for certificate %s, needs rotation", certificateID)
		return true, nil
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		logger.Warnf("Failed to parse certificate %s: %v, needs rotation", certificateID, err)
		return true, nil
	}

	// Calculate the actual certificate lifetime
	certificateLifetime := cert.NotAfter.Sub(cert.NotBefore)
	logger.Debugf("Certificate lifetime (from NotBefore to NotAfter): %v", certificateLifetime)

	// Calculate rotation threshold based on environment variable (reads as percentage, converts to decimal)
	// Default: 75% of lifetime
	rotationThresholdDecimal := env.GetCertificateRotationThresholdPercentage()
	rotationThreshold := time.Duration(float64(certificateLifetime) * rotationThresholdDecimal)
	logger.Debugf("Rotation threshold (%.1f%% of certificate lifetime): %v", rotationThresholdDecimal*100, rotationThreshold)

	// Calculate time elapsed since certificate was issued
	timeElapsed := now.Sub(cert.NotBefore)
	logger.Debugf("Time elapsed since certificate was issued: %v (Certificate issued at: %v)", timeElapsed, cert.NotBefore)

	// Check if the configured percentage of the certificate lifetime has elapsed since it was issued
	needsRotation := timeElapsed >= rotationThreshold
	logger.Debugf("Certificate rotation needed: %t (elapsed: %v >= threshold: %v)", needsRotation, timeElapsed, rotationThreshold)

	// Also check if certificate exists in cache - if not, it might be old and should be rotated
	_, exists := common.GetCertAuthCache(certificateID)
	if !exists {
		return true, nil
	}

	return needsRotation, nil
}

// testCertificateConnectivity tests certificate connectivity with the VSA cluster
// It can work with either a certificate object (for new certificates) or a certificate ID (for existing certificates)
// Parameters:
//   - certificate: Can be a *hyperscalermodels.CustomCertificate, map[string]interface{}, or nil
//   - If certificate is nil, uses certificate ID from pool credentials
func isCertificateValidityError(err error) bool {
	var certificateError x509.CertificateInvalidError
	return errors.As(err, &certificateError) && certificateError.Reason == x509.Expired
}

func (a *RotateVcpToVsaCertificateActivity) testCertificateConnectivity(ctx context.Context, pool *datamodel.Pool, certificate interface{}) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Starting certificate connectivity test for pool %s", pool.UUID)

	var certificateID string
	var cert *hyperscalermodels.CustomCertificate

	// Determine certificate ID to use
	if certificate != nil {
		// Extract certificate details for logging - handle both direct struct and map from Temporal serialization
		var convertErr error

		// Try direct type assertion first
		if directCert, ok := certificate.(*hyperscalermodels.CustomCertificate); ok {
			cert = directCert
			certificateID = cert.CertificateID
		} else if certMap, ok := certificate.(map[string]interface{}); ok {
			// Handle Temporal serialization - convert map back to struct
			cert, convertErr = convertMapToCustomCertificate(certMap)
			if convertErr != nil {
				logger.Errorf("Failed to convert certificate map to struct for connectivity test: %v", convertErr)
				return fmt.Errorf("failed to convert certificate map to struct for connectivity test: %v", convertErr)
			}
			certificateID = cert.CertificateID
		} else {
			logger.Errorf("Invalid certificate type for connectivity test: %T", certificate)
			return fmt.Errorf("invalid certificate type for connectivity test: %T", certificate)
		}
		logger.Debugf("Testing connectivity with certificate object: ID=%s, SubjectCN=%s", cert.CertificateID, cert.SubjectCommonName)
	} else {
		// Use certificate ID from pool credentials
		certificateID = pool.PoolCredentials.CertificateID
		if certificateID == "" {
			return fmt.Errorf("certificate ID is empty")
		}
		logger.Debugf("Testing connectivity with certificate ID: %s", certificateID)
	}

	// Log pool credentials for connectivity test
	logger.Debugf("Pool credentials for connectivity test - SecretID: %s, CertificateID: %s, CertificateIDNew: %s, AuthType: %d",
		pool.PoolCredentials.SecretID, pool.PoolCredentials.CertificateID, pool.PoolCredentials.CertificateIDNew, pool.PoolCredentials.AuthType)
	logger.Debugf("Testing connectivity with certificate ID: %s (from certificate object: %v)", certificateID, certificate != nil)

	// Get nodes for the pool
	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get nodes for connectivity test: %v", err)
		return fmt.Errorf("failed to get nodes for connectivity test: %v", err)
	}
	logger.Debugf("Retrieved %d nodes for connectivity test", len(dbNodes))

	// Get username from pool credentials, fallback to deployment name with admin suffix
	// This matches the logic used during certificate generation and installation
	username := pool.PoolCredentials.Username
	if username == "" {
		// Fallback: use deployment name with admin suffix (matches certificate generation fallback)
		username = fmt.Sprintf("%s_admin", pool.DeploymentName)
		logger.Warnf("Username is empty in pool credentials for connectivity test, using fallback: %s", username)
	}

	// Create node for provider using the certificate
	// Note: Password is not required for certificate-based REST API connectivity testing.
	// Password is only needed for SSH operations (like ModifySSL), not for REST API calls.
	// The provider will use certificate authentication for REST API calls.
	certificateCaURI := pool.PoolCredentials.GetCaURIWithFallback()
	if certificate != nil && pool.PoolCredentials.StagedCaURI != "" {
		certificateCaURI = pool.PoolCredentials.StagedCaURI
	}
	testNode := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:          dbNodes,
		DeploymentName: pool.DeploymentName,
		OntapCredentials: &datamodel.PoolCredentials{
			Password:      pool.PoolCredentials.Password,
			SecretID:      pool.PoolCredentials.SecretID,
			CertificateID: certificateID,
			AuthType:      pool.PoolCredentials.AuthType,
			CaURI:         certificateCaURI,
			Username:      username,
		},
	})
	logger.Debugf("Created test node with certificate ID: %s", certificateID)

	common.RemoveFromCertAuthCache(certificateID)

	testProvider, err := vsa.GetProviderByNode(ctx, testNode)
	if err != nil {
		logger.Errorf("Failed to create VSA provider with certificate: %v", err)
		return fmt.Errorf("failed to create VSA provider with certificate: %v", err)
	}

	version, err := testProvider.GetONTAPVersion()
	if err == nil && version != nil {
		logger.Debugf("Successfully retrieved ONTAP version: %s", *version)
		logger.Debugf("Certificate connectivity test passed for pool: %s with certificate: %s", pool.UUID, certificateID)
		return nil
	}

	if err != nil {
		if isCertificateValidityError(err) && certificate == nil {
			logger.Warnf("Certificate %s is expired - falling back to password authentication for connectivity test", certificateID)
			passwordErr := a.testPasswordConnectivity(ctx, pool, "")
			if passwordErr == nil {
				logger.Infof("Connectivity test succeeded using password authentication (certificate %s is expired)", certificateID)
				return nil
			}
			logger.Errorf("Connectivity test failed with both expired certificate and password authentication: certificate error: %v, password error: %v", err, passwordErr)
			return fmt.Errorf("connectivity test failed - certificate expired and password authentication also failed: certificate error: %v, password error: %v", err, passwordErr)
		}

		logger.Errorf("Certificate connectivity test failed: %v", err)
		return fmt.Errorf("connectivity test failed - cannot get ONTAP version: %v", err)
	}

	logger.Errorf("Certificate connectivity test failed: ONTAP version is nil")
	return fmt.Errorf("connectivity test failed - ONTAP version is nil")
}

// revokeOldCertificate revokes the old certificate and cleans up resources
func (a *RotateVcpToVsaCertificateActivity) revokeOldCertificate(ctx context.Context, certificateID, caURI string, cloudService hyperscaler2.Services) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Starting revocation of old certificate: %s", certificateID)

	// Remove from cache first
	logger.Debug("Removing certificate from cache")
	removeFromCertAuthCache(certificateID)
	logger.Debug("Successfully removed certificate from cache")

	poolCredentials := &datamodel.PoolCredentials{
		CertificateID: certificateID,
		CaURI:         caURI,
	}

	// Revoke certificate and delete from secret manager
	logger.Debug("Revoking certificate and deleting from secret manager")
	err := revokeCertificateAndDeleteFromCacheAndSecretManager(cloudService, poolCredentials)
	if err != nil {
		logger.Errorf("Failed to revoke certificate %s: %v", certificateID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrCertificateRevocationFailed, err)
	}
	logger.Debug("Successfully revoked certificate and deleted from secret manager")

	return nil
}

// GetCertificateExpirationInfo retrieves expiration information for a certificate
func (a *RotateVcpToVsaCertificateActivity) GetCertificateExpirationInfo(ctx context.Context, certificateID string) (*CertificateExpirationInfo, error) {
	logger := util.GetLogger(ctx)

	// Create minimal PoolCredentials for certificate lookup (CaURI will fallback to env vars)
	poolCredentials := &datamodel.PoolCredentials{
		CertificateID: certificateID,
	}

	certificate, err := getCertificateFromCacheOrSecretManager(ctx, poolCredentials)
	if err != nil {
		logger.Errorf("Failed to get certificate %s: %v", certificateID, err)
		return nil, err
	}

	if certificate == nil {
		return &CertificateExpirationInfo{
			CertificateID: certificateID,
			Exists:        false,
			NeedsRotation: true,
		}, nil
	}

	// Parse the actual certificate expiration date
	var expirationTime time.Time
	var needsRotation bool

	if certificate.SignedCertificate != "" {
		expTime, err := parseCertificateExpiration(certificate.SignedCertificate)
		if err != nil {
			logger.Warnf("Failed to parse certificate expiration: %v", err)
			needsRotation = true // If we can't parse, assume it needs rotation
		} else {
			expirationTime = expTime
			now := time.Now()
			needsRotation = now.After(expirationTime) || expirationTime.Before(now.AddDate(0, 0, 30))
		}
	} else {
		needsRotation = true // No certificate content, needs rotation
	}

	return &CertificateExpirationInfo{
		CertificateID: certificateID,
		Exists:        true,
		NeedsRotation: needsRotation,
		ExpiresAt:     expirationTime,
	}, nil
}

// CertificateExpirationInfo contains information about certificate expiration
type CertificateExpirationInfo struct {
	CertificateID string
	Exists        bool
	NeedsRotation bool
	ExpiresAt     time.Time
}

// rollbackCertificateRotation performs comprehensive cleanup of resources created during failed certificate rotation
func (a *RotateVcpToVsaCertificateActivity) rollbackCertificateRotation(ctx context.Context, resources *RollbackResources) error {
	logger := util.GetLogger(ctx)

	var rollbackErrors []string

	// A candidate referenced by certificate_id_new/staged_ca_uri is deliberately
	// retained for retry after any post-stage failure.
	if resources.DBReferenced {
		logger.Infof("Preserving staged certificate %s for retry", resources.NewCertificateID)
		return nil
	}

	// Revoke only a candidate that failed before the database referenced it.
	if resources.NewCertificateID != "" {
		logger.Debugf("Revoking and deleting new certificate: %s", resources.NewCertificateID)
		// Create PoolCredentials for certificate revocation (use pool's CaURI if available)
		poolCredentials := &datamodel.PoolCredentials{
			CertificateID: resources.NewCertificateID,
			CaURI:         resources.NewCaURI,
		}
		if poolCredentials.CaURI == "" && resources.Pool != nil && resources.Pool.PoolCredentials != nil {
			poolCredentials.CaURI = resources.Pool.PoolCredentials.GetCaURIWithFallback()
		}
		err := revokeCertificateAndDeleteFromCacheAndSecretManager(resources.GcpService, poolCredentials)
		if err != nil {
			logger.Errorf("Failed to revoke new certificate %s: %v", resources.NewCertificateID, err)
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("certificate revocation failed: %v", err))
		} else {
			logger.Debugf("Successfully revoked new certificate: %s", resources.NewCertificateID)
		}
	}

	// 2. Delete the new secret from Secret Manager
	if resources.NewSecretID != "" {
		logger.Debugf("Deleting new secret: %s", resources.NewSecretID)
		// Note: Secret deletion is typically handled by the certificate revocation function
		// But we can add explicit secret deletion here if needed
		logger.Debugf("Secret %s will be cleaned up as part of certificate revocation", resources.NewSecretID)
	}

	// 3. Remove new certificate from cache
	if resources.NewCertificateID != "" {
		logger.Debugf("Removing new certificate from cache: %s", resources.NewCertificateID)
		removeFromCertAuthCache(resources.NewCertificateID)
	}

	// 4. Restore old certificate to cache if available
	if resources.OldCertificate != nil {
		logger.Debugf("Restoring old certificate to cache: %s", resources.Pool.PoolCredentials.CertificateID)
		addToCertAuthCache(resources.Pool.PoolCredentials.CertificateID, resources.OldCertificate)
	}

	// 5. Attempt to remove the new certificate from VSA cluster (if it was installed)
	if resources.NewCertificate != nil {
		logger.Debug("Attempting to remove new certificate from VSA cluster")
		err := a.removeCertificateFromVSA(ctx, resources.Pool, resources.NewCertificate)
		if err != nil {
			logger.Warnf("Failed to remove new certificate from VSA cluster: %v", err)
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("VSA certificate removal failed: %v", err))
		} else {
			logger.Debug("Successfully removed new certificate from VSA cluster")
		}
	}

	// 6. Log rollback summary
	if len(rollbackErrors) > 0 {
		logger.Warnf("Rollback completed with %d errors: %v", len(rollbackErrors), rollbackErrors)
		return vsaerrors.NewVCPError(vsaerrors.ErrRotationRollbackFailed, fmt.Errorf("rollback completed with errors: %v", rollbackErrors))
	}

	return nil
}

// removeCertificateFromVSA attempts to remove a certificate from the VSA cluster
func (a *RotateVcpToVsaCertificateActivity) removeCertificateFromVSA(ctx context.Context, pool *datamodel.Pool, certificate *hyperscalermodels.CustomCertificate) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Attempting to remove certificate %s from VSA cluster", certificate.CertificateID)

	// Get nodes for the pool
	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get nodes for certificate removal: %v", err)
		return err
	}

	// Get username from pool credentials, fallback to deployment name with admin suffix
	username := pool.PoolCredentials.Username
	if username == "" {
		// Fallback: use deployment name with admin suffix (matches certificate generation fallback)
		username = fmt.Sprintf("%s_admin", pool.DeploymentName)
		logger.Warnf("Username is empty in pool credentials for certificate removal, using fallback: %s", username)
	}

	// Create node for provider
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:          dbNodes,
		DeploymentName: pool.DeploymentName,
		OntapCredentials: &datamodel.PoolCredentials{
			Password:      pool.PoolCredentials.Password,
			SecretID:      pool.PoolCredentials.SecretID,
			CertificateID: pool.PoolCredentials.CertificateID,
			AuthType:      pool.PoolCredentials.AuthType,
			CaURI:         pool.PoolCredentials.GetCaURIWithFallback(),
			Username:      username, // Use username with fallback if needed
		},
	})

	// Get VSA provider
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to get VSA provider for certificate removal: %v", err)
		return err
	}

	vserverName := resolveVserverName(pool)
	logger.Infof("Resolved vserver name for removal: %q (deploymentName: %q)", vserverName, pool.DeploymentName)

	logger.Debugf("Attempting to delete certificate %s from ONTAP using REST API", certificate.CertificateID)

	if ontapProvider, ok := provider.(*vsa.OntapRestProvider); ok {
		err := a.deleteCertificateFromONTAP(ctx, ontapProvider, certificate, vserverName)
		if err != nil {
			if isSVMNotFoundError(err) && vserverName != pool.DeploymentName {
				logger.Warnf("SVM %q not found during certificate removal, retrying with deployment name: %q", vserverName, pool.DeploymentName)
				err = a.deleteCertificateFromONTAP(ctx, ontapProvider, certificate, pool.DeploymentName)
			}
			if err != nil {
				logger.Warnf("Failed to delete certificate from ONTAP: %v", err)
				return err
			}
		}
	} else {
		logger.Warnf("Provider is not OntapRestProvider type: %T, cannot delete certificate via REST API", provider)
		logger.Info("Certificate will remain in the cluster but will not be used after rollback")
	}

	return nil
}

// deleteCertificateFromONTAP deletes a certificate from ONTAP using the REST API
func (a *RotateVcpToVsaCertificateActivity) deleteCertificateFromONTAP(ctx context.Context, provider *vsa.OntapRestProvider, certificate *hyperscalermodels.CustomCertificate, vserverName string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Deleting certificate %s from ONTAP cluster vserver %s", certificate.CertificateID, vserverName)

	// Get the ONTAP REST client
	client, err := ontaprest.NewOntapRestClient(provider.ClientParams)
	if err != nil {
		logger.Errorf("Failed to get ONTAP client: %v", err)
		return fmt.Errorf("failed to get ONTAP client: %v", err)
	}
	if client == nil {
		return fmt.Errorf("ONTAP client is nil")
	}

	// Create delete parameters using the correct import
	deleteParams := &ontaprest.SecurityCertificateDeleteCollectionParams{}

	// Set filters to identify the specific certificate
	deleteParams.Name = &certificate.CertificateID
	deleteParams.SvmName = &vserverName
	serverType := "server"
	deleteParams.Type = &serverType // Server certificate type

	// If we have serial number, use it for more precise identification
	if certificate.SerialNumber != "" {
		deleteParams.SerialNumber = &certificate.SerialNumber
	}

	// Execute the delete operation
	logger.Debugf("Executing certificate deletion with params: Name=%s, SvmName=%s, Type=server, SerialNumber=%s",
		certificate.CertificateID, vserverName, certificate.SerialNumber)

	err = client.Security().SecurityCertificateDeleteCollection(deleteParams)
	if err != nil {
		logger.Errorf("Failed to delete certificate %s from ONTAP: %v", certificate.CertificateID, err)
		return fmt.Errorf("failed to delete certificate from ONTAP: %v", err)
	}

	logger.Debugf("Successfully deleted certificate %s from ONTAP cluster vserver %s", certificate.CertificateID, vserverName)
	return nil
}

// PasswordRotationResources tracks resources created during password rotation for cleanup
type PasswordRotationResources struct {
	NewSecretID          string
	NewPassword          string
	OldSecretID          string
	Pool                 *datamodel.Pool
	GcpService           hyperscaler2.Services
	CacheUpdated         bool
	OntapPasswordUpdated bool // Indicates if ONTAP password was updated (Step 3+)
}

// rotatePasswordForPool rotates the password for a pool with auth type USERNAME_PWD_SEC_MGR or USER_CERTIFICATE
func (a *RotateVcpToVsaCertificateActivity) rotatePasswordForPool(ctx context.Context, pool *datamodel.Pool, cloudService hyperscaler2.Services) error {
	safeRecordHeartbeat(ctx, fmt.Sprintf("Starting password rotation for pool %s", pool.UUID))
	logger := util.GetLogger(ctx)

	logger.Infof("Starting password rotation for pool %s (%s) (AuthType: %d)", pool.UUID, pool.Name, pool.PoolCredentials.AuthType)
	logger.Infof("Pool Details - UUID: %s, Name: %s, State: %s, DeploymentName: %s",
		pool.UUID, pool.Name, pool.State, pool.DeploymentName)

	// Log current credentials
	if pool.PoolCredentials != nil {
		logger.Infof("Current Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s, CertificateID: %s, CertificateIDNew: %s",
			pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID, pool.PoolCredentials.SecretIDNew,
			pool.PoolCredentials.CertificateID, pool.PoolCredentials.CertificateIDNew)
	}

	// Generate new password in deployment-secret format
	timestamp := time.Now().Format("20060102-150405")
	logger.Infof("Generating new password with timestamp: %s", timestamp)

	newPassword, err := utils.GenerateStrongPassword(16)
	if err != nil {
		logger.Errorf("Failed to generate new password: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordRotationFailed, err)
	}
	// Add timestamp suffix to ensure uniqueness and avoid password history conflicts
	newPassword = newPassword + timestamp[len(timestamp)-4:]

	// Note: Old secret cleanup moved to AFTER connectivity checks
	// This ensures we don't remove old secret before confirming new one works

	// Create new secret ID with the SAME timestamp to ensure consistency
	newSecretID := fmt.Sprintf("%s-secret-%s", pool.DeploymentName, timestamp)
	logger.Infof("Created new secret ID: %s", newSecretID)

	// Track resources for potential rollback
	createdResources := &PasswordRotationResources{
		NewSecretID:          newSecretID,
		NewPassword:          newPassword,
		OldSecretID:          pool.PoolCredentials.SecretID,
		Pool:                 pool,
		GcpService:           cloudService,
		CacheUpdated:         false,
		OntapPasswordUpdated: false, // Will be set to true after Step 3
	}

	// Track resources for potential rollback
	createdResources.NewSecretID = newSecretID

	// Step 1: Check password connectivity with secret_id and secret_id_new
	// This ensures we're using the correct password before proceeding with password rotation
	// This is especially important for AuthType 1 to handle cases where previous rotations left inconsistent state
	logger.Infof("Checking connectivity with current SecretID: %s", pool.PoolCredentials.SecretID)
	if pool.PoolCredentials.SecretIDNew != "" {
		logger.Infof("Also checking connectivity with SecretIDNew: %s", pool.PoolCredentials.SecretIDNew)
	}
	safeRecordHeartbeat(ctx, "Step 1: Checking and syncing password connectivity")

	err = a.checkAndSyncPasswordConnectivity(ctx, pool)
	if err != nil {
		logger.Errorf("Failed to check and sync password connectivity for pool %s: %v", pool.UUID, err)
		rollbackErr := a.rollbackPasswordRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed: %v", rollbackErr)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordConnectivityTestFailed, err)
	}

	// Step 1.5: Clean up old secret from Secret Manager after successful connectivity checks
	// This ensures we only remove the old secret after confirming connectivity works
	// Note: oldSecretID was already declared earlier, so we use a different variable name here
	previousOldSecretID := pool.PoolCredentials.SecretIDNew // This is the old secret ID from previous rotation
	if previousOldSecretID != "" {
		logger.Infof("Step 1.5: Cleaning up old secret from Secret Manager: %s", previousOldSecretID)
		err := a.cleanupPreviousSecret(ctx, cloudService, previousOldSecretID)
		if err != nil {
			logger.Warnf("Failed to cleanup old secret %s: %v", previousOldSecretID, err)
			// Don't fail the rotation if cleanup fails, but log the error
		} else {
			logger.Infof("Successfully cleaned up old secret: %s", previousOldSecretID)
		}
	}

	// Create new secret in Secret Manager and update database BEFORE updating VSA
	logger.Infof("Step 2: Creating new secret in Secret Manager and updating database with ID: %s", newSecretID)
	safeRecordHeartbeat(ctx, fmt.Sprintf("Step 2: Creating new secret in Secret Manager with ID: %s", newSecretID))

	err = a.createNewSecretAndUpdateDatabase(ctx, cloudService, pool, newSecretID, newPassword)
	if err != nil {
		logger.Errorf("Failed to create new secret and update database: %v", err)
		rollbackErr := a.rollbackPasswordRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed: %v", rollbackErr)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordSecretCreationFailed, err)
	}
	logger.Info("Successfully created new secret and updated database")

	// Update VSA cluster with new password
	logger.Infof("Step 3: Updating VSA cluster %s with new password", pool.ClusterDetails.ExternalName)
	safeRecordHeartbeat(ctx, fmt.Sprintf("Step 3: Updating VSA cluster %s with new password", pool.ClusterDetails.ExternalName))

	err = a.updateVSAPassword(ctx, pool, newPassword)
	if err != nil {
		logger.Errorf("Failed to update VSA cluster with new password: %v", err)
		// Mark that ONTAP password may have been partially updated
		createdResources.OntapPasswordUpdated = true
		rollbackErr := a.rollbackPasswordRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed: %v", rollbackErr)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrVSAClusterPasswordUpdateFailed, err)
	}
	logger.Info("Successfully updated VSA cluster with new password")
	// Mark that ONTAP password was successfully updated
	createdResources.OntapPasswordUpdated = true

	// Test connection with new password to ensure it works
	logger.Info("Step 4: Testing connectivity with new password")
	safeRecordHeartbeat(ctx, "Step 4: Testing connectivity with new password")

	err = a.testPasswordConnectivity(ctx, pool, newPassword)
	if err != nil {
		logger.Errorf("New password failed connectivity test: %v", err)

		// ONTAP password was updated in Step 3, so we need to revert it first
		// Rollback will handle the revert attempt and only cleanup GCP resources if revert succeeds
		rollbackErr := a.rollbackPasswordRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed: %v", rollbackErr)
		}

		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordConnectivityTestFailed, fmt.Errorf("new password validation failed: %v", err))
	}

	logger.Info("Step 5: Swapping secret IDs in database")
	logger.Infof("Swapping SecretID: %s with SecretIDNew: %s", pool.PoolCredentials.SecretID, newSecretID)
	safeRecordHeartbeat(ctx, "Step 5: Swapping secret IDs in database")

	err = a.swapSecretIDs(ctx, pool)
	if err != nil {
		logger.Errorf("Failed to swap secret IDs in database: %v", err)
		// ONTAP password was updated in Step 3, so we need to revert it first
		// Rollback will handle the revert attempt and only cleanup GCP resources if revert succeeds
		rollbackErr := a.rollbackPasswordRotation(ctx, createdResources)
		if rollbackErr != nil {
			logger.Errorf("Rollback failed: %v", rollbackErr)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordSecretIDSwapFailed, err)
	}
	logger.Info("Successfully swapped secret IDs in database")

	// Update cache to use new secret ID
	logger.Info("Step 6: Updating authentication cache")
	logger.Infof("Adding new secret to cache - SecretID: %s", newSecretID)
	safeRecordHeartbeat(ctx, "Step 6: Updating authentication cache")
	common.AddToUserAuthCache(newSecretID, newPassword)

	oldSecretID := pool.PoolCredentials.SecretIDNew
	logger.Infof("Removing old secret from cache: %s", oldSecretID)
	common.RemoveFromUserAuthCache(oldSecretID)

	createdResources.CacheUpdated = true
	logger.Infof("Password rotation completed successfully for pool %s", pool.UUID)
	logger.Infof("Final state - New SecretID: %s", newSecretID)
	safeRecordHeartbeat(ctx, "Password rotation completed successfully")

	return nil
}

// checkAndSyncPasswordConnectivity checks password connectivity with secret_id and secret_id_new
func (a *RotateVcpToVsaCertificateActivity) checkAndSyncPasswordConnectivity(ctx context.Context, pool *datamodel.Pool) error {
	logger := util.GetLogger(ctx)

	err := a.testPasswordConnectivity(ctx, pool, "")
	if err == nil {
		logger.Debug("Connectivity test passed with secret_id - no action needed")
		return nil
	}
	logger.Warnf("Connectivity test failed with secret_id: %v", err)

	if pool.PoolCredentials.SecretIDNew == "" {
		logger.Warnf("No secret_id_new available for pool %s, cannot test alternative password", pool.UUID)
		return vsaerrors.NewVCPError(vsaerrors.ErrPoolConnectivityNoStagedCredential, fmt.Errorf("password connectivity failed with secret_id and no secret_id_new available: %v", err))
	}

	logger.Debug("Testing connectivity with secret_id_new (staging secret)")

	tempPool := *pool
	tempPool.PoolCredentials = &datamodel.PoolCredentials{
		SecretID:      pool.PoolCredentials.SecretIDNew,
		SecretIDNew:   pool.PoolCredentials.SecretID,
		CertificateID: pool.PoolCredentials.CertificateID,
		AuthType:      pool.PoolCredentials.AuthType,
		Password:      pool.PoolCredentials.Password,
	}

	err = a.testPasswordConnectivity(ctx, &tempPool, "")
	if err != nil {
		logger.Errorf("Connectivity test also failed with secret_id_new: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordConnectivityTestFailed, fmt.Errorf("password connectivity failed with both secret_id and secret_id_new: %v", err))
	}

	logger.Info("Connectivity test passed with secret_id_new - swapping secret_id and secret_id_new")

	err = a.swapSecretIDs(ctx, pool)
	if err != nil {
		logger.Errorf("Failed to swap secret_id and secret_id_new: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrPasswordSecretIDSwapFailed, fmt.Errorf("failed to swap secret IDs after connectivity test: %v", err))
	}

	logger.Info("Successfully swapped secret_id and secret_id_new based on connectivity test")
	return nil
}

// checkAndSyncCertificateConnectivity checks certificate connectivity with certificate_id and certificate_id_new
func (a *RotateVcpToVsaCertificateActivity) checkAndSyncCertificateConnectivity(
	ctx context.Context,
	pool *datamodel.Pool,
) (bool, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Checking certificate connectivity for pool %s", pool.UUID)

	err := a.testCertificateConnectivity(ctx, pool, nil)
	if err == nil {
		logger.Debug("Connectivity test passed with certificate_id - no action needed")
		return false, nil
	}
	logger.Warnf("Connectivity test failed with certificate_id: %v", err)

	if pool.PoolCredentials.CertificateIDNew == "" {
		logger.Warnf("No certificate_id_new available for pool %s, cannot test alternative certificate", pool.UUID)
		return false, vsaerrors.NewVCPError(vsaerrors.ErrPoolConnectivityNoStagedCredential, fmt.Errorf("certificate connectivity failed with certificate_id and no certificate_id_new available: %v", err))
	}

	stagedCAURI := pool.PoolCredentials.StagedCaURI
	if stagedCAURI == "" {
		inferredCAURI, inferred := inferIssuerForNewerLegacyStagedCert(
			pool.PoolCredentials.CertificateID,
			pool.PoolCredentials.CertificateIDNew,
			"",
			pool.PoolCredentials.CaURI,
			defaultCertificateCaURI(),
		)
		if !inferred {
			return false, vsaerrors.NewVCPError(
				vsaerrors.ErrPoolConnectivityNoStagedCredential,
				fmt.Errorf(
					"unpaired certificate_id_new %s cannot be safely associated with the desired CA",
					pool.PoolCredentials.CertificateIDNew,
				),
			)
		}
		stagedCAURI = inferredCAURI
		if updateErr := a.updatePoolCertificateIDNew(
			ctx,
			pool,
			pool.PoolCredentials.CertificateIDNew,
			stagedCAURI,
		); updateErr != nil {
			return false, vsaerrors.NewVCPError(
				vsaerrors.ErrCertificateStagingFailed,
				fmt.Errorf("backfill staged certificate issuer before connectivity fallback: %w", updateErr),
			)
		}
		pool.PoolCredentials.StagedCaURI = stagedCAURI
	}

	logger.Debug("Testing connectivity with certificate_id_new (staging certificate)")

	tempPool := *pool
	tempCredentials := *pool.PoolCredentials
	tempCredentials.CertificateID = pool.PoolCredentials.CertificateIDNew
	tempCredentials.CaURI = stagedCAURI
	tempPool.PoolCredentials = &tempCredentials

	err = a.testCertificateConnectivity(ctx, &tempPool, nil)
	if err != nil {
		logger.Errorf("Connectivity test also failed with certificate_id_new: %v", err)
		return false, vsaerrors.NewVCPError(vsaerrors.ErrCertificateConnectivityTestFailed, fmt.Errorf("certificate connectivity failed with both certificate_id and certificate_id_new: %v", err))
	}

	logger.Info("Connectivity test passed with certificate_id_new - swapping certificate_id and certificate_id_new")

	err = a.swapCertificateIDs(ctx, pool.UUID, pool.PoolCredentials.CertificateIDNew, pool.PoolCredentials.SecretID)
	if err != nil {
		logger.Errorf("Failed to swap certificate_id and certificate_id_new: %v", err)
		return false, vsaerrors.NewVCPError(vsaerrors.ErrCertificateIDSwapFailed, fmt.Errorf("failed to swap certificate IDs after connectivity test: %v", err))
	}

	logger.Info("Successfully swapped certificate_id and certificate_id_new based on connectivity test")
	return true, nil
}

// testPasswordConnectivity tests connectivity to VSA cluster with specified password
func (a *RotateVcpToVsaCertificateActivity) testPasswordConnectivity(ctx context.Context, pool *datamodel.Pool, testPassword string) error {
	// Use test function if set (for testing)
	if a.testPasswordConnectivityFunc != nil {
		return a.testPasswordConnectivityFunc(ctx, pool, testPassword)
	}
	logger := util.GetLogger(ctx)
	logger.Infof("Testing password connectivity for pool: %s", pool.UUID)

	var passwordToTest string
	if testPassword == "" {
		passwordToTest = pool.PoolCredentials.Password

		if passwordToTest == "" {
			logger.Infof("Pool password is empty, fetching from Secret Manager with secret_id: %s", pool.PoolCredentials.SecretID)
			secret, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretID)
			if err != nil {
				logger.Warnf("Failed to get current password from Secret Manager with secret_id: %v", err)

				if pool.PoolCredentials.SecretIDNew != "" {
					logger.Infof("Attempting to fetch password from secret_id_new as fallback: %s", pool.PoolCredentials.SecretIDNew)
					secret, err = vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretIDNew)
					if err != nil {
						logger.Errorf("Failed to get current password from both secret_id and secret_id_new: %v", err)
						return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, fmt.Errorf("failed to get current password from both secret_id and secret_id_new: %v", err))
					}
					logger.Infof("Successfully fetched password from secret_id_new fallback")
				} else {
					logger.Errorf("No secret_id_new available for fallback, cannot test connectivity")
					return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, fmt.Errorf("failed to get current password from Secret Manager and no fallback available: %v", err))
				}
			} else {
				logger.Infof("Successfully fetched password from secret_id: %s", pool.PoolCredentials.SecretID)
			}
			passwordToTest = secret
		} else {
			logger.Infof("Using password from pool credentials (not empty)")
		}
	} else {
		logger.Infof("Using provided test password")
		passwordToTest = testPassword
	}

	// Get nodes for the pool
	logger.Infof("Getting nodes for pool ID: %d", pool.ID)
	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get node information: %v", err)
		return err
	}
	logger.Infof("Retrieved %d nodes for pool", len(dbNodes))

	// Get username from pool credentials, fallback to deployment name with admin suffix
	// This matches the logic used during certificate generation
	username := pool.PoolCredentials.Username
	if username == "" {
		// Fallback: use deployment name with admin suffix (matches certificate generation fallback)
		username = fmt.Sprintf("%s_admin", pool.DeploymentName)
		logger.Warnf("Username is empty in pool credentials for password connectivity test, using fallback: %s", username)
	}

	// Create temporary node with PASSWORD-ONLY authentication
	logger.Infof("Creating temporary node for password-only authentication test")
	tempNode := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:          dbNodes,
		DeploymentName: pool.DeploymentName,
		OntapCredentials: &datamodel.PoolCredentials{
			Password:      passwordToTest,
			SecretID:      pool.PoolCredentials.SecretID,
			CertificateID: "",
			AuthType:      env.USERNAME_PWD,
			CaURI:         pool.PoolCredentials.GetCaURIWithFallback(),
			Username:      username,
		},
	})
	logger.Infof("Temporary node created with auth type: %d (USERNAME_PWD)", env.USERNAME_PWD)

	provider, err := vsa.GetProviderByNode(ctx, tempNode)
	if err != nil {
		logger.Errorf("Failed to get VSA provider: %v", err)
		return err
	}

	ontapVersion, err := provider.GetONTAPVersion()
	if err != nil {
		logger.Errorf("Password connectivity test failed: %v", err)
		return err
	}
	ontapVersionStr := ""
	if ontapVersion != nil {
		ontapVersionStr = *ontapVersion
	}
	logger.Infof("Password connectivity test passed, ONTAP version: %s", ontapVersionStr)
	logger.Infof("Password connectivity test passed for pool: %s", pool.UUID)
	return nil
}

// updateVSAPassword updates the VSA cluster with the new password
func (a *RotateVcpToVsaCertificateActivity) updateVSAPassword(ctx context.Context, pool *datamodel.Pool, newPassword string) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Updating VSA password for pool: %s", pool.UUID)

	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get node information: %v", err)
		return err
	}
	logger.Infof("Retrieved %d nodes for pool", len(dbNodes))

	currentPassword := pool.PoolCredentials.Password

	if currentPassword == "" {
		// For any auth type, fetch password from the secretID field if pool password is empty
		logger.Infof("Pool password is empty, fetching from Secret Manager with secret_id: %s", pool.PoolCredentials.SecretID)
		secret, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretID)
		if err != nil {
			logger.Errorf("Failed to get current password from Secret Manager: %v", err)
			return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
		}
		currentPassword = secret
		logger.Infof("Successfully fetched current password from Secret Manager")
	}

	err = a.updateAdminPasswordOnAllNodes(ctx, dbNodes, newPassword, currentPassword)
	if err != nil {
		logger.Errorf("Failed to update admin password on all nodes: %v", err)
		return err
	}

	return nil
}

// updateAdminPasswordOnAllNodes updates the admin password on all nodes in the VSA cluster
func (a *RotateVcpToVsaCertificateActivity) updateAdminPasswordOnAllNodes(ctx context.Context, dbNodes []*datamodel.Node, newPassword, currentPassword string) error {
	logger := util.GetLogger(ctx)

	nodeIPs := make([]string, 0, len(dbNodes))
	for _, node := range dbNodes {
		if node.EndpointAddress != "" {
			nodeIPs = append(nodeIPs, node.EndpointAddress)
		}
	}

	if len(nodeIPs) == 0 {
		return fmt.Errorf("no valid node IPs found for password update")
	}

	primaryNodeIP := nodeIPs[0]

	err := a.updatePasswordOnNode(ctx, primaryNodeIP, newPassword, currentPassword)
	if err != nil {
		logger.Errorf("Failed to update password on primary node %s: %v", primaryNodeIP, err)
		return fmt.Errorf("failed to update password on primary node %s: %w", primaryNodeIP, err)
	}

	return nil
}

// updatePasswordOnNode updates the admin password on a specific node using ONTAP REST API
func (a *RotateVcpToVsaCertificateActivity) updatePasswordOnNode(ctx context.Context, nodeIP, newPassword, currentPassword string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Updating password on node %s using ONTAP REST API", nodeIP)

	data := map[string]string{
		"username":     "admin",
		"new-password": newPassword,
	}

	url := fmt.Sprintf("https://%s/api/private/cli/security/login/password", nodeIP)
	logger.Debugf("API URL: %s", url)

	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:"+currentPassword))

	statusCode, responseBody, err := a.executeAPIRequestWithResponse(ctx, "POST", url, headers, data, auth)
	if err != nil {
		logger.Errorf("Failed to update password on node %s (statusCode: %v): %v", nodeIP, statusCode, err)
		return fmt.Errorf("failed to update password on node %s: %w", nodeIP, err)
	}

	if statusCode >= 200 && statusCode < 300 {
		logger.Debugf("Password update successful on node %s", nodeIP)
		return nil
	} else {
		logger.Errorf("Password update failed on node %s (statusCode: %v): %s", nodeIP, statusCode, responseBody)

		if strings.Contains(responseBody, "New password must be different from last") {
			logger.Errorf("ONTAP password history policy violation: %s", responseBody)
			logger.Debugf("=== END UPDATING PASSWORD ON VSA NODE ===")
			return vsaerrors.NewVCPError(vsaerrors.ErrPasswordHistoryPolicyViolation, fmt.Errorf("password update failed due to ONTAP password history policy: %s", responseBody))
		}

		if strings.Contains(responseBody, "User is not authorized") {
			logger.Errorf("ONTAP authorization error: %s", responseBody)
			logger.Debugf("=== END UPDATING PASSWORD ON VSA NODE ===")
			return vsaerrors.NewVCPError(vsaerrors.ErrPasswordAuthorizationFailed, fmt.Errorf("password update failed due to authorization error: %s", responseBody))
		}

		return fmt.Errorf("password update failed on node %s with status code %v", nodeIP, statusCode)
	}
}

// executeAPIRequestWithResponse executes an HTTP API request and returns status code, response body, and error
func (a *RotateVcpToVsaCertificateActivity) executeAPIRequestWithResponse(ctx context.Context, method, url string, headers map[string]string, data interface{}, auth string) (int, string, error) {
	// Use mock function if provided for testing
	if a.executeAPIRequestWithResponseFunc != nil {
		return a.executeAPIRequestWithResponseFunc(ctx, method, url, headers, data, auth)
	}

	logger := util.GetLogger(ctx)
	logger.Debugf("Executing %s request to %s", method, url)

	// This legacy password-rotation path addresses ONTAP by management IP and
	// uses a self-signed server certificate. Keep the verification exception
	// until a trusted DNS identity is available, but enforce the common
	// CP-to-DP protocol, cipher-suite, and curve policy.
	tlsConfig := vcptls.NewFIPSClientConfig()
	tlsConfig.InsecureSkipVerify = true
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	// Marshal data to JSON if provided
	var jsonData []byte
	var err error
	if data != nil {
		jsonData, err = json.Marshal(data)
		if err != nil {
			return 0, "", fmt.Errorf("failed to marshal request data: %w", err)
		}
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return 0, "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Set authorization header
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Log error but don't fail the function
			util.GetLogger(ctx).Warnf("Failed to close response body: %v", closeErr)
		}
	}()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("failed to read response body: %w", err)
	}

	logger.Debugf("API request completed with status code: %d", resp.StatusCode)
	return resp.StatusCode, string(body), nil
}

// revertVSAPassword reverts the VSA password back to the old password
func (a *RotateVcpToVsaCertificateActivity) revertVSAPassword(ctx context.Context, pool *datamodel.Pool, currentNewPassword, targetOldPassword string) error {
	logger := util.GetLogger(ctx)
	logger.Debug("Reverting VSA password back to old password using REST API")

	// Get nodes for the pool
	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get node information for revert: %v", err)
		return err
	}

	// Revert password on primary node - cluster will auto-sync to all nodes
	err = a.updateAdminPasswordOnAllNodes(ctx, dbNodes, targetOldPassword, currentNewPassword)
	if err != nil {
		logger.Errorf("Failed to revert password on primary node: %v", err)
		return err
	}

	return nil
}

// createNewSecretAndUpdateDatabase creates a new secret and updates database using secret_id_new field
// This approach provides zero downtime by keeping both old and new secrets available
func (a *RotateVcpToVsaCertificateActivity) createNewSecretAndUpdateDatabase(ctx context.Context, cloudService hyperscaler2.Services, pool *datamodel.Pool, newSecretID, newPassword string) error {
	logger := util.GetLogger(ctx)

	// Validate inputs before creating secret
	logger.Infof("Creating new secret with ID: %s", newSecretID)

	// Validate that password is not empty
	if newPassword == "" {
		logger.Errorf("New password is empty - cannot create secret")
		return fmt.Errorf("new password is empty - cannot create secret")
	}

	// Create new secret in Secret Manager
	logger.Infof("Calling cloudService.CreateSecret with project: %s, region: %s, secretID: %s", env.SecretManagerProjectID, env.Region, newSecretID)
	_, err := cloudService.CreateSecret(env.SecretManagerProjectID, env.Region, newSecretID, newPassword)
	if err != nil {
		logger.Errorf("Failed to create new secret: %v", err)
		return fmt.Errorf("failed to create new secret: %w", err)
	}

	// Update database to store new secret ID in secret_id_new field
	err = a.updatePoolSecretIDNew(ctx, pool, newSecretID)
	if err != nil {
		logger.Errorf("Failed to update database with new secret ID: %v", err)
		// Clean up the created secret
		cleanupErr := cloudService.DeleteSecret(env.SecretManagerProjectID, newSecretID)
		if cleanupErr != nil {
			logger.Errorf("Failed to cleanup new secret after database update failure: %v", cleanupErr)
		}
		return fmt.Errorf("failed to update database with new secret ID: %w", err)
	}

	return nil
}

// updatePoolSecretIDNew updates the pool's secret_id_new field in the database
func (a *RotateVcpToVsaCertificateActivity) updatePoolSecretIDNew(ctx context.Context, pool *datamodel.Pool, newSecretID string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Updating pool %s secret_id_new to %s", pool.UUID, newSecretID)

	// Get the current pool to preserve all existing pool_credentials fields
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", pool.UUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get current pool for secret_id_new update: %v", err)
		return err
	}
	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found for secret_id_new update", pool.UUID)
		return fmt.Errorf("pool %s not found", pool.UUID)
	}

	currentPool := ConvertPoolViewToPool(poolViews[0])
	if currentPool.PoolCredentials == nil {
		logger.Errorf("Pool %s has no credentials for secret_id_new update", pool.UUID)
		return fmt.Errorf("pool %s has no credentials", pool.UUID)
	}

	// Preserve all existing pool_credentials fields and only update the secret_id_new
	updatedCredentials := map[string]interface{}{
		"secret_id_new":      newSecretID,                                  // Update to new secret ID
		"secret_id":          currentPool.PoolCredentials.SecretID,         // Preserve existing secret ID
		"certificate_id":     currentPool.PoolCredentials.CertificateID,    // Preserve existing certificate ID
		"certificate_id_new": currentPool.PoolCredentials.CertificateIDNew, // Preserve existing fields
		"auth_type":          currentPool.PoolCredentials.AuthType,
		"password":           currentPool.PoolCredentials.Password,
		"username":           currentPool.PoolCredentials.Username, // Preserve username field
		"ca_uri":             currentPool.PoolCredentials.CaURI,    // Preserve ca_uri field
		"staged_ca_uri":      currentPool.PoolCredentials.StagedCaURI,
	}

	// Save the updated pool using UpdatePoolFields to update only the secret_id_new
	updates := map[string]interface{}{
		"pool_credentials": updatedCredentials,
	}
	err = a.SE.UpdatePoolFields(ctx, pool.UUID, updates)
	if err != nil {
		logger.Errorf("Failed to update pool secret_id_new in database: %v", err)
		return err
	}

	logger.Debug("Successfully updated pool secret_id_new in database")
	return nil
}

// updatePoolCertificateIDNew updates the pool's certificate_id_new field in the database
func (a *RotateVcpToVsaCertificateActivity) updatePoolCertificateIDNew(ctx context.Context, pool *datamodel.Pool, newCertificateID, newCaURI string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Updating pool %s certificate_id_new to %s", pool.UUID, newCertificateID)

	// Get the current pool to preserve all existing pool_credentials fields
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", pool.UUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get current pool for certificate_id_new update: %v", err)
		return err
	}
	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found for certificate_id_new update", pool.UUID)
		return fmt.Errorf("pool %s not found", pool.UUID)
	}

	currentPool := ConvertPoolViewToPool(poolViews[0])
	if currentPool.PoolCredentials == nil {
		logger.Errorf("Pool %s has no credentials for certificate_id_new update", pool.UUID)
		return fmt.Errorf("pool %s has no credentials", pool.UUID)
	}

	updatedCredentials := *currentPool.PoolCredentials
	updatedCredentials.CertificateIDNew = newCertificateID
	updatedCredentials.StagedCaURI = newCaURI
	err = a.SE.CompareAndSwapPoolCredentials(
		ctx,
		pool.UUID,
		currentPool.PoolCredentials.AuthType,
		currentPool.PoolCredentials.CertificateIDNew,
		currentPool.PoolCredentials.StagedCaURI,
		env.GetCertificateRotationPoolStates(),
		&updatedCredentials,
	)
	if err != nil {
		logger.Errorf("Failed to update pool certificate_id_new in database: %v", err)
		return err
	}

	logger.Debug("Successfully updated pool certificate_id_new in database")
	return nil
}

// swapSecretIDs swaps secret_id and secret_id_new in the database
func (a *RotateVcpToVsaCertificateActivity) swapSecretIDs(ctx context.Context, pool *datamodel.Pool) error {
	logger := util.GetLogger(ctx)

	// Get the current pool to get both secret IDs
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", pool.UUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get current pool for secret ID swap: %v", err)
		return err
	}
	if len(poolViews) == 0 {
		logger.Errorf("Pool %s not found for secret ID swap", pool.UUID)
		return fmt.Errorf("pool %s not found", pool.UUID)
	}

	currentPool := ConvertPoolViewToPool(poolViews[0])
	if currentPool.PoolCredentials == nil {
		logger.Errorf("Pool %s has no credentials for secret ID swap", pool.UUID)
		return fmt.Errorf("pool %s has no credentials", pool.UUID)
	}

	oldSecretID := currentPool.PoolCredentials.SecretID
	newSecretID := currentPool.PoolCredentials.SecretIDNew
	logger.Infof("Current secret_id_new: %s", newSecretID)

	if newSecretID == "" {
		logger.Errorf("secret_id_new is empty, cannot swap")
		return fmt.Errorf("secret_id_new is empty, cannot swap")
	}

	// Swap the secret IDs - CORRECT FIX: Keep old secret in secret_id_new for next rotation cleanup

	updatedCredentials := map[string]interface{}{
		"secret_id":          newSecretID,                                  // New secret becomes active
		"secret_id_new":      oldSecretID,                                  // ✅ CORRECT: Keep old secret for next rotation cleanup
		"certificate_id":     currentPool.PoolCredentials.CertificateID,    // Preserve existing certificate ID
		"certificate_id_new": currentPool.PoolCredentials.CertificateIDNew, // Preserve existing fields
		"auth_type":          currentPool.PoolCredentials.AuthType,
		"password":           currentPool.PoolCredentials.Password,
		"username":           currentPool.PoolCredentials.Username, // Preserve username field
		"ca_uri":             currentPool.PoolCredentials.CaURI,    // Preserve ca_uri field
		"staged_ca_uri":      currentPool.PoolCredentials.StagedCaURI,
	}

	// Save the updated pool using UpdatePoolFields
	updates := map[string]interface{}{
		"pool_credentials": updatedCredentials,
	}
	err = a.SE.UpdatePoolFields(ctx, pool.UUID, updates)
	if err != nil {
		logger.Errorf("Failed to swap secret IDs in database: %v", err)
		return err
	}

	// Update the pool object in memory to reflect the changes
	pool.PoolCredentials.SecretID = newSecretID
	pool.PoolCredentials.SecretIDNew = oldSecretID // ✅ CORRECT: Keep old secret for next rotation cleanup

	logger.Infof("Successfully swapped secret IDs: %s -> %s, old secret kept in secret_id_new for next rotation cleanup", oldSecretID, newSecretID)
	return nil
}

// cleanupPreviousSecret cleans up a secret from Secret Manager and cache
func (a *RotateVcpToVsaCertificateActivity) cleanupPreviousSecret(ctx context.Context, cloudService hyperscaler2.Services, secretID string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Cleaning up previous secret: %s", secretID)

	// Delete from Secret Manager and cache
	err := vsa.DeletePasswordFromCacheAndSecretManager(cloudService, secretID)
	if err != nil {
		return fmt.Errorf("failed to cleanup previous secret %s: %w", secretID, err)
	}

	logger.Debugf("Successfully cleaned up previous secret: %s", secretID)
	return nil
}

// cleanupPreviousCertificate cleans up a certificate from cache
// Note: GCP CAS certificates are immutable and cannot be deleted once created, only revoked
func (a *RotateVcpToVsaCertificateActivity) cleanupPreviousCertificate(ctx context.Context, certificateID string) error {
	logger := util.GetLogger(ctx)
	logger.Debugf("Cleaning up previous certificate: %s", certificateID)

	// Note: GCP CAS certificates are immutable and cannot be deleted once created, only revoked
	// We can only remove them from cache
	logger.Debugf("Removing certificate %s from cache (GCP CAS certificates cannot be deleted)", certificateID)

	// Remove from certificate cache
	common.RemoveFromCertAuthCache(certificateID)

	logger.Debugf("Successfully cleaned up previous certificate from cache: %s", certificateID)
	return nil
}

// rollbackPasswordRotation cleans up all resources created during password rotation
// IMPORTANT: If ONTAP password was updated, we MUST revert it FIRST before cleaning up GCP resources.
// If revert fails (network issue), we preserve GCP resources so they can be reused in the next rotation cycle.
// During next rotation, Step 1 will test connectivity with secret_id and secret_id_new, and if secret_id_new
// works (because it contains the password active on ONTAP), it will be swapped and used.
func (a *RotateVcpToVsaCertificateActivity) rollbackPasswordRotation(ctx context.Context, resources *PasswordRotationResources) error {
	logger := util.GetLogger(ctx)

	// Step 1: If ONTAP password was updated, try to revert it FIRST
	if resources.OntapPasswordUpdated {
		logger.Info("ONTAP password was updated, attempting to revert it first before cleaning up resources")

		currentPassword := resources.Pool.PoolCredentials.Password
		if currentPassword == "" {
			logger.Infof("Pool password is empty, fetching from Secret Manager with secret_id: %s", resources.Pool.PoolCredentials.SecretID)
			secret, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, resources.Pool.PoolCredentials.SecretID)
			if err != nil {
				logger.Errorf("Failed to get current password for revert: %v", err)
				logger.Warnf("Cannot revert ONTAP password (failed to get old password). Preserving GCP resources (secret_id_new: %s) for next rotation cycle.", resources.NewSecretID)
				return vsaerrors.NewVCPError(vsaerrors.ErrPasswordRevertFailed, fmt.Errorf("cannot revert ONTAP password and cannot get old password - GCP resources preserved for next rotation: %v", err))
			}
			currentPassword = secret
		}

		revertErr := a.revertVSAPassword(ctx, resources.Pool, resources.NewPassword, currentPassword)
		if revertErr != nil {
			logger.Errorf("Failed to revert ONTAP password back to old password: %v", revertErr)
			logger.Warnf("Cannot revert ONTAP password (network issue or other error). Preserving GCP resources (secret_id_new: %s) for next rotation cycle. Next rotation will detect and use this secret.", resources.NewSecretID)
			return vsaerrors.NewVCPError(vsaerrors.ErrPasswordRevertFailed, fmt.Errorf("cannot revert ONTAP password - GCP resources preserved for next rotation: %v", revertErr))
		}

		logger.Info("Successfully reverted ONTAP password back to old password")
		common.AddToUserAuthCache(resources.Pool.PoolCredentials.SecretID, currentPassword)
	}

	var rollbackErrors []error

	if resources.CacheUpdated {
		common.RemoveFromUserAuthCache(resources.NewSecretID)
	}

	if resources.NewSecretID != "" {
		err := resources.GcpService.DeleteSecret(env.SecretManagerProjectID, resources.NewSecretID)
		if err != nil {
			logger.Errorf("Failed to delete new secret during rollback: %v", err)
			rollbackErrors = append(rollbackErrors, fmt.Errorf("failed to delete new secret: %v", err))
		} else {
			logger.Infof("Successfully deleted new secret from the cloud provider: %s", resources.NewSecretID)
		}
	}

	if len(rollbackErrors) > 0 {
		logger.Errorf("Password rotation rollback completed with %d errors", len(rollbackErrors))
		for i, err := range rollbackErrors {
			logger.Errorf("Rollback error %d: %v", i+1, err)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrRotationRollbackFailed, fmt.Errorf("rollback completed with %d errors", len(rollbackErrors)))
	}

	logger.Info("Password rotation rollback completed successfully")
	return nil
}

// RotatePoolPassword rotates the password for a specific pool with password authentication
func (a *RotateVcpToVsaCertificateActivity) RotatePoolPassword(ctx context.Context, poolUUID string) error {
	safeRecordHeartbeat(ctx, "Starting password rotation activity")
	logger := util.GetLogger(ctx)
	se := a.SE

	logger.Info("Starting password rotation", "poolUUID", poolUUID)

	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)

	poolViews, err := se.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(poolViews) == 0 {
		logger.Warnf("Pool %s not found, skipping password rotation", poolUUID)
		return nil
	}

	poolView := poolViews[0]
	pool := ConvertPoolViewToPool(poolView)

	logger.Info("Retrieved pool details for password rotation", "poolUUID", poolUUID, "poolName", pool.Name)
	safeRecordHeartbeat(ctx, fmt.Sprintf("Retrieved pool details for password rotation: %s", poolUUID))

	// Check if pool is in CREATING, DELETING, or UPGRADING state - skip rotation for pools being created, deleted, or upgraded
	if pool.State == "CREATING" {
		logger.Infof("Pool %s (%s) is in CREATING state, skipping password rotation", poolUUID, pool.Name)
		return nil
	}
	if pool.State == "DELETING" {
		logger.Infof("Pool %s (%s) is in DELETING state, skipping password rotation", poolUUID, pool.Name)
		return nil
	}
	if pool.State == "UPGRADING" {
		logger.Infof("Pool %s (%s) is in UPGRADING state, skipping password rotation", poolUUID, pool.Name)
		return nil
	}

	// Check if password rotation is enabled via feature flag
	passwordRotationEnabled := env.GetBool("ENABLE_VSA_PASSWORD_ROTATION", false)
	if !passwordRotationEnabled {
		logger.Infof("Password rotation is disabled via feature flag, skipping for pool %s (%s)", poolUUID, pool.Name)
		return nil
	}

	// Handle password rotation for both AuthType USERNAME_PWD_SEC_MGR and USER_CERTIFICATE
	if pool.PoolCredentials.AuthType == env.USERNAME_PWD_SEC_MGR || pool.PoolCredentials.AuthType == env.USER_CERTIFICATE {
		// Both AuthType 1 and 2 use Secret Manager storage - use existing complex rotation
		if pool.PoolCredentials.SecretID == "" {
			logger.Warnf("Pool %s has no secret ID, skipping password rotation", poolUUID)
			return vsaerrors.NewVCPError(vsaerrors.ErrPoolCredentialsMissing, fmt.Errorf("pool %s has no secret ID", poolUUID))
		}

		logger.Infof("Starting password rotation for AuthType %d pool %s with secret ID %s", pool.PoolCredentials.AuthType, poolUUID, pool.PoolCredentials.SecretID)

		// Get cloud service
		safeRecordHeartbeat(ctx, "Getting cloud service for password rotation")
		cloudService, err := getGcpServiceForCerts(ctx)
		if err != nil {
			logger.Errorf("Failed to get cloud service: %v", err)
			return vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
		}

		safeRecordHeartbeat(ctx, "Checking if pool has nodes")
		hasNodes, err := a.checkPoolHasNodes(ctx, pool)
		if err != nil {
			logger.Errorf("Failed to check if pool has nodes for password rotation: %v", err)
			// Don't fail the entire rotation if node check fails, but log the error
		} else if !hasNodes {
			logger.Warnf("Skipping password rotation for pool %s due to missing nodes", poolUUID)
			return nil
		}

		safeRecordHeartbeat(ctx, "Starting password rotation for pool")
		err = a.rotatePasswordForPool(ctx, pool, cloudService)
		if err != nil {
			logger.Errorf("Password rotation failed for pool %s (%s): %v", poolUUID, pool.Name, err)
			return err
		}
	} else {
		logger.Warnf("Pool %s (%s) has unsupported auth type %d for password rotation", poolUUID, pool.Name, pool.PoolCredentials.AuthType)
		return fmt.Errorf("unsupported auth type %d for password rotation", pool.PoolCredentials.AuthType)
	}

	logger.Infof("Final pool state - UUID: %s, Name: %s, Auth Type: %d, Secret ID: %s",
		pool.UUID, pool.Name, pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID)
	logger.Infof("Password rotation completed successfully for pool %s (%s)", poolUUID, pool.Name)
	safeRecordHeartbeat(ctx, "Password rotation completed successfully")
	return nil
}

// RotatePoolPasswordWithContext rotates the password for a pool using pre-fetched pool context
func (a *RotateVcpToVsaCertificateActivity) RotatePoolPasswordWithContext(ctx context.Context, poolContext *PoolContext) error {
	safeRecordHeartbeat(ctx, "Starting password rotation activity with context")
	logger := util.GetLogger(ctx)
	pool := poolContext.Pool
	poolUUID := poolContext.PoolUUID

	logger.Infof("Starting password rotation for pool UUID: %s (using context)", poolUUID)
	logger.Infof("Pool Details - UUID: %s, Name: %s, State: %s, ExternalName: %s, DeploymentName: %s",
		pool.UUID, pool.Name, pool.State, pool.ClusterDetails.ExternalName, pool.DeploymentName)

	// Log comprehensive credentials information for password rotation
	if pool.PoolCredentials != nil {
		logger.Infof("Pool Credentials - AuthType: %d, SecretID: %s, SecretIDNew: %s, CertificateID: %s, CertificateIDNew: %s",
			pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID, pool.PoolCredentials.SecretIDNew,
			pool.PoolCredentials.CertificateID, pool.PoolCredentials.CertificateIDNew)
	} else {
		logger.Warnf("Pool %s has no credentials", poolUUID)
	}

	// Check if pool is in CREATING, DELETING, or UPGRADING state - skip rotation for pools being created, deleted, or upgraded
	if pool.State == "CREATING" {
		logger.Infof("Pool %s (%s) is in CREATING state, skipping password rotation", poolUUID, pool.Name)
		return nil
	}
	if pool.State == "DELETING" {
		logger.Infof("Pool %s (%s) is in DELETING state, skipping password rotation", poolUUID, pool.Name)
		return nil
	}
	if pool.State == "UPGRADING" {
		logger.Infof("Pool %s (%s) is in UPGRADING state, skipping password rotation", poolUUID, pool.Name)
		return nil
	}

	// Check if password rotation is enabled via feature flag
	passwordRotationEnabled := env.GetBool("ENABLE_VSA_PASSWORD_ROTATION", false)
	if !passwordRotationEnabled {
		logger.Infof("Password rotation is disabled via feature flag, skipping for pool %s (%s)", poolUUID, pool.Name)
		return nil
	}

	// Handle password rotation for both AuthType USERNAME_PWD_SEC_MGR and USER_CERTIFICATE
	if pool.PoolCredentials.AuthType == env.USERNAME_PWD_SEC_MGR || pool.PoolCredentials.AuthType == env.USER_CERTIFICATE {
		// Both AuthType 1 and 2 use Secret Manager storage - use existing complex rotation
		if pool.PoolCredentials.SecretID == "" {
			logger.Warnf("Pool %s has no secret ID, skipping password rotation", poolUUID)
			return vsaerrors.NewVCPError(vsaerrors.ErrPoolCredentialsMissing, fmt.Errorf("pool %s has no secret ID", poolUUID))
		}

		logger.Infof("Starting password rotation for AuthType %d pool %s with secret ID %s", pool.PoolCredentials.AuthType, poolUUID, pool.PoolCredentials.SecretID)

		// Get cloud service
		safeRecordHeartbeat(ctx, "Getting cloud service for password rotation")
		cloudService, err := getGcpServiceForCerts(ctx)
		if err != nil {
			logger.Errorf("Failed to get cloud service: %v", err)
			return vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
		}

		// Reuse the existing rotatePasswordForPool logic
		safeRecordHeartbeat(ctx, "Starting password rotation for pool")
		err = a.rotatePasswordForPool(ctx, pool, cloudService)
		if err != nil {
			logger.Errorf("Password rotation failed for pool %s: %v", poolUUID, err)
			return err
		}
	} else {
		logger.Warnf("Pool %s has unsupported auth type %d for password rotation", poolUUID, pool.PoolCredentials.AuthType)
		return fmt.Errorf("unsupported auth type %d for password rotation", pool.PoolCredentials.AuthType)
	}

	logger.Infof("Final pool state - UUID: %s, Auth Type: %d, Secret ID: %s",
		pool.UUID, pool.PoolCredentials.AuthType, pool.PoolCredentials.SecretID)
	logger.Infof("Password rotation completed successfully for pool %s", poolUUID)
	safeRecordHeartbeat(ctx, "Password rotation completed successfully")
	return nil
}

// PopulateMissingCaURI populates ca_uri for pools that don't have it in their pool_credentials.
// It reads CA params from environment variables, forms ca_uri, and stores it in the database.
func (a *RotateVcpToVsaCertificateActivity) PopulateMissingCaURI(ctx context.Context, pools []*datamodel.Pool) error {
	logger := util.GetLogger(ctx)
	logger.Info("Starting to populate missing ca_uri for pools")

	if len(pools) == 0 {
		logger.Debug("No pools provided, skipping ca_uri population")
		return nil
	}

	// Filter pools that don't have ca_uri
	// Only process certificate authentication pools (auth_type = USER_CERTIFICATE)
	var poolsNeedingCaURI []*datamodel.Pool
	for _, pool := range pools {
		if pool.PoolCredentials == nil {
			logger.Debugf("Pool %s has nil PoolCredentials, skipping", pool.UUID)
			continue
		}
		// Only process certificate authentication pools
		if pool.PoolCredentials.AuthType != env.USER_CERTIFICATE {
			logger.Debugf("Pool %s (name: %s) has auth_type %d, not certificate auth. Skipping ca_uri population",
				pool.UUID, pool.Name, pool.PoolCredentials.AuthType)
			continue
		}
		if pool.PoolCredentials.CaURI == "" {
			poolsNeedingCaURI = append(poolsNeedingCaURI, pool)
			logger.Debugf("Pool %s (name: %s) missing ca_uri, will populate from env", pool.UUID, pool.Name)
		}
	}

	if len(poolsNeedingCaURI) == 0 {
		logger.Info("All pools already have ca_uri, no updates needed")
		return nil
	}

	logger.Infof("Found %d pools without ca_uri, populating from environment variables", len(poolsNeedingCaURI))

	// Build ca_uri from environment variables
	caURI := env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName)
	if caURI == "" {
		logger.Warn("Cannot build ca_uri from environment variables - all CA env vars are empty")
		return fmt.Errorf("cannot build ca_uri: CA_POOL_DEPLOYED_PROJECT_ID, CA_POOL_NAME, and CA_NAME are all empty")
	}

	logger.Infof("Built ca_uri from environment: %s", caURI)

	// Update each pool that needs ca_uri
	successCount := 0
	for _, pool := range poolsNeedingCaURI {
		// Get current pool credentials to preserve all existing fields
		filter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("uuid", "=", pool.UUID),
		)
		poolViews, err := a.SE.ListPools(ctx, filter)
		if err != nil {
			logger.Errorf("Failed to get current pool %s for ca_uri update: %v", pool.UUID, err)
			continue
		}
		if len(poolViews) == 0 {
			logger.Errorf("Pool %s not found for ca_uri update", pool.UUID)
			continue
		}

		currentPool := ConvertPoolViewToPool(poolViews[0])
		if currentPool.PoolCredentials == nil {
			logger.Errorf("Pool %s has no credentials for ca_uri update", pool.UUID)
			continue
		}

		// Preserve all existing pool_credentials fields and add ca_uri
		updatedCredentials := map[string]interface{}{
			"ca_uri":             caURI, // Add ca_uri
			"staged_ca_uri":      currentPool.PoolCredentials.StagedCaURI,
			"secret_id":          currentPool.PoolCredentials.SecretID,         // Preserve existing
			"secret_id_new":      currentPool.PoolCredentials.SecretIDNew,      // Preserve existing
			"certificate_id":     currentPool.PoolCredentials.CertificateID,    // Preserve existing
			"certificate_id_new": currentPool.PoolCredentials.CertificateIDNew, // Preserve existing
			"auth_type":          currentPool.PoolCredentials.AuthType,         // Preserve existing
			"password":           currentPool.PoolCredentials.Password,         // Preserve existing
		}

		// Add username if it exists
		if currentPool.PoolCredentials.Username != "" {
			updatedCredentials["username"] = currentPool.PoolCredentials.Username
		}

		// Update the pool using UpdatePoolFields
		updates := map[string]interface{}{
			"pool_credentials": updatedCredentials,
		}
		err = a.SE.UpdatePoolFields(ctx, pool.UUID, updates)
		if err != nil {
			logger.Errorf("Failed to update pool %s with ca_uri: %v", pool.UUID, err)
			continue
		}

		successCount++
		logger.Infof("Successfully populated ca_uri for pool %s (name: %s): %s", pool.UUID, pool.Name, caURI)
	}

	logger.Infof("Completed populating ca_uri: %d/%d pools updated successfully", successCount, len(poolsNeedingCaURI))

	if successCount < len(poolsNeedingCaURI) {
		logger.Warnf("Some pools failed to update: %d succeeded, %d failed", successCount, len(poolsNeedingCaURI)-successCount)
		// Don't return error if at least some succeeded - this is a best-effort operation
	}

	return nil
}

// convertMapToCustomCertificate converts a map[string]interface{} back to *hyperscalermodels.CustomCertificate
// This handles Temporal serialization/deserialization
func convertMapToCustomCertificate(certMap map[string]interface{}) (*hyperscalermodels.CustomCertificate, error) {
	// Convert map to JSON and then back to struct
	jsonData, err := json.Marshal(certMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal certificate map to JSON: %v", err)
	}

	var cert hyperscalermodels.CustomCertificate
	err = json.Unmarshal(jsonData, &cert)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal certificate JSON to struct: %v", err)
	}

	return &cert, nil
}

// convertMapToCustomSecret converts a map[string]interface{} back to *hyperscalermodels.CustomSecret
// This handles Temporal serialization/deserialization
func convertMapToCustomSecret(secretMap map[string]interface{}) (*hyperscalermodels.CustomSecret, error) {
	// Convert map to JSON and then back to struct
	jsonData, err := json.Marshal(secretMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal secret map to JSON: %v", err)
	}

	var secret hyperscalermodels.CustomSecret
	err = json.Unmarshal(jsonData, &secret)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal secret JSON to struct: %v", err)
	}

	return &secret, nil
}
