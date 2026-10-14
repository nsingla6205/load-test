package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

// SetOciAdminPasswordOnVSA sets the OCI expert-mode admin (ociadmin) password on
// the pool's ONTAP cluster to tempPassword via the ONTAP private-CLI call
// (security/login/password) against the pool's primary node, authenticating with
// the pool's admin credentials (certificate or vault password, resolved by
// GetProviderByNode).
//
// clusterName is the ONTAP cluster/vserver name (VLMConfig.VsaCluster.ClusterName)
// and username is the expert-mode user to reset (typically "ociadmin").
// tempPassword is a plaintext secret and is never logged.
func (j *PoolActivity) SetOciAdminPasswordOnVSA(ctx context.Context, pool *datamodel.Pool, clusterName, username, tempPassword string) error {
	activity.RecordHeartbeat(ctx, "Starting SetOciAdminPasswordOnVSA activity")
	logger := util.GetLogger(ctx)

	if pool == nil || pool.PoolCredentials == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("pool and pool credentials are required to set the OCI admin password")))
	}
	if clusterName == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("cluster name is required to set the OCI admin password for pool %s", pool.UUID)))
	}
	if username == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("expert-mode username is required to set the OCI admin password for pool %s", pool.UUID)))
	}
	if tempPassword == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("temporary password is required to set the OCI admin password for pool %s", pool.UUID)))
	}

	pwdProvider, err := j.resolveExpertModePasswordProvider(ctx, pool)
	if err != nil {
		return err
	}

	activity.RecordHeartbeat(ctx, "Setting OCI admin password on ONTAP")
	if err := pwdProvider.ResetLoginPassword(ctx, clusterName, username, tempPassword); err != nil {
		logger.Error("Failed to set OCI admin password on ONTAP", "poolUUID", pool.UUID, "username", username,
			"error", err, "detail", vsaerrors.ExtractCustomError(err).GetDetailMessage())
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Set OCI admin password on ONTAP", "poolUUID", pool.UUID, "username", username, "cluster", clusterName)
	activity.RecordHeartbeat(ctx, "Finished SetOciAdminPasswordOnVSA activity")
	return nil
}

// ExpireOciAdminPasswordOnVSA expires the OCI expert-mode admin (ociadmin)
// password on the pool's ONTAP cluster via the ONTAP private-CLI call
// (security/login/expire-password) against the pool's primary node, forcing a
// change on next login.
//
// clusterName is the ONTAP cluster/vserver name (VLMConfig.VsaCluster.ClusterName)
// and username is the expert-mode user whose password is expired (typically
// "ociadmin").
func (j *PoolActivity) ExpireOciAdminPasswordOnVSA(ctx context.Context, pool *datamodel.Pool, clusterName, username string) error {
	activity.RecordHeartbeat(ctx, "Starting ExpireOciAdminPasswordOnVSA activity")
	logger := util.GetLogger(ctx)

	if pool == nil || pool.PoolCredentials == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("pool and pool credentials are required to expire the OCI admin password")))
	}
	if clusterName == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("cluster name is required to expire the OCI admin password for pool %s", pool.UUID)))
	}
	if username == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("expert-mode username is required to expire the OCI admin password for pool %s", pool.UUID)))
	}

	pwdProvider, err := j.resolveExpertModePasswordProvider(ctx, pool)
	if err != nil {
		return err
	}

	activity.RecordHeartbeat(ctx, "Expiring OCI admin password on ONTAP")
	if err := pwdProvider.ExpireLoginPassword(ctx, clusterName, username); err != nil {
		logger.Error("Failed to expire OCI admin password on ONTAP", "poolUUID", pool.UUID, "username", username,
			"error", err, "detail", vsaerrors.ExtractCustomError(err).GetDetailMessage())
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Expired OCI admin password on ONTAP", "poolUUID", pool.UUID, "username", username, "cluster", clusterName)
	activity.RecordHeartbeat(ctx, "Finished ExpireOciAdminPasswordOnVSA activity")
	return nil
}

// resolveExpertModePasswordProvider fetches the pool's nodes, builds an
// authenticated ONTAP REST provider for the pool's primary node, and asserts it
// supports expert-mode password operations. It is shared by
// SetOciAdminPasswordOnVSA and ExpireOciAdminPasswordOnVSA. Callers must validate
// that pool and pool.PoolCredentials are non-nil before calling.
func (j *PoolActivity) resolveExpertModePasswordProvider(ctx context.Context, pool *datamodel.Pool) (vsa.ExpertModePasswordProvider, error) {
	activity.RecordHeartbeat(ctx, "Fetching pool nodes")
	logger := util.GetLogger(ctx)

	dbNodes, err := j.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err))
	}
	if len(dbNodes) == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
				fmt.Errorf("no nodes found for pool %s (ID: %d)", pool.UUID, pool.ID)))
	}

	node := buildOCIProviderNode(pool, dbNodes)

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get VSA provider for OCI admin password operation", "poolUUID", pool.UUID, "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if provider == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
				fmt.Errorf("VSA provider is nil for pool %s", pool.UUID)))
	}

	pwdProvider, ok := provider.(vsa.ExpertModePasswordProvider)
	if !ok {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("VSA provider for pool %s does not support expert-mode password operations", pool.UUID)))
	}
	return pwdProvider, nil
}

// RecordTempPasswordState persists the temporary-credential state on the pool
// when the OCI expert-mode admin (ociadmin) password is a temporary password that
// must be changed within its window. It is invoked both after a reset on an
// existing pool and during create-pool when a caller-supplied temporary password
// is used as the expert-mode credential. It marks TempPasswordActive=true and
// stamps TempPasswordCreatedAt so a reconciler can detect temporary passwords that
// are never consumed within the allowed window. The write uses the map-based
// UpdatePoolFields path so the boolean and timestamp are always persisted (GORM's
// struct updates skip zero values).
//
// createdAt should be captured before the password is set on ONTAP, so a change
// the caller makes before this activity runs (or while it is retried) still falls
// inside the audit query window. A nil or zero createdAt falls back to the current
// time; this also covers activity tasks scheduled before the parameter existed,
// which the Temporal SDK decodes with the missing argument left nil.
func (j *PoolActivity) RecordTempPasswordState(ctx context.Context, pool *datamodel.Pool, createdAt *time.Time) error {
	activity.RecordHeartbeat(ctx, "Starting RecordTempPasswordState activity")
	logger := util.GetLogger(ctx)

	if pool == nil || pool.UUID == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("pool with a valid UUID is required to record the temporary password state")))
	}

	recordedAt := time.Now().UTC()
	if createdAt != nil && !createdAt.IsZero() {
		recordedAt = createdAt.UTC()
	}
	updates := map[string]interface{}{
		"external_temp_credentials": datamodel.ExternalTempCredentials{
			TempPasswordActive:    true,
			TempPasswordCreatedAt: &recordedAt,
		},
	}

	activity.RecordHeartbeat(ctx, "Recording temporary password state on pool")
	if err := j.SE.UpdatePoolFields(ctx, pool.UUID, updates); err != nil {
		logger.Error("Failed to record temporary password state on pool", "poolUUID", pool.UUID, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Recorded temporary password state on pool", "poolUUID", pool.UUID)
	activity.RecordHeartbeat(ctx, "Finished RecordTempPasswordState activity")
	return nil
}

// buildOCIProviderNode assembles the models.Node used to build an authenticated
// ONTAP REST provider for a pool. It mirrors the certificate-auth node
// construction used by the OCI certificate-rotation activities while also
// covering the vault/password auth types: for USER_CERTIFICATE the endpoint map
// values are the host DNS names (needed for SNI/hostname verification), for all
// other auth types the value mirrors the IP (matching CreateNodeForProvider).
func buildOCIProviderNode(pool *datamodel.Pool, dbNodes []*datamodel.Node) *models.Node {
	certAuth := pool.PoolCredentials != nil && pool.PoolCredentials.AuthType == env.USER_CERTIFICATE

	endpointMap := make(map[string]string)
	for _, n := range dbNodes {
		if n == nil || n.EndpointAddress == "" {
			continue
		}
		if certAuth {
			endpointMap[n.EndpointAddress] = n.HostDNSName
		} else {
			endpointMap[n.EndpointAddress] = n.EndpointAddress
		}
	}

	node := &models.Node{
		EndpointAddressesToHostNameMap: endpointMap,
		DeploymentName:                 pool.DeploymentName,
		// Opt the reset-password ONTAP client into two-phase fast host selection.
		// This only takes effect when ONTAP_REST_FAST_HOST_SELECT is enabled
		// (OCI deployments), so a single unhealthy management LIF is skipped
		// quickly instead of consuming the full retry budget before failover.
		FastHostSelect: true,
	}
	if pool.PoolCredentials != nil {
		node.AuthType = pool.PoolCredentials.AuthType
		node.Password = pool.PoolCredentials.Password
		node.CaURI = pool.PoolCredentials.GetCaURIWithFallback()
		node.CertificateID = pool.PoolCredentials.CertificateID
		node.SecretID = pool.PoolCredentials.SecretID
		node.ExternalCertificate = pool.PoolCredentials.ExternalCertificate
		node.ExternalSecret = pool.PoolCredentials.ExternalSecret
		node.ExternalClientCertificate = pool.PoolCredentials.ExternalClientCertificate
	}
	return node
}
