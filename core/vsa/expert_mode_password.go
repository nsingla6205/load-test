package vsa

import (
	"context"
	"time"

	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

// ExpertModePasswordProvider resets and expires the login password for an
// ONTAP user (e.g. the OCI expert-mode ociadmin user) via the ONTAP private
// CLI, and reads the audit trail to detect whether a temporary password has
// been consumed. Implemented by *OntapRestProvider.
type ExpertModePasswordProvider interface {
	// ResetLoginPassword sets a new login password for username on the given
	// vserver (cluster name for cluster-scoped users).
	ResetLoginPassword(ctx context.Context, vserver, username, newPassword string) error
	// ExpireLoginPassword expires username's password on the given vserver so
	// it must be changed on next login.
	ExpireLoginPassword(ctx context.Context, vserver, username string) error
	// HasLoginPasswordResetSuccess reports whether username's temporary password was consumed since the given time.
	HasLoginPasswordResetSuccess(ctx context.Context, username string, since time.Time) (bool, error)
}

// ResetLoginPassword sets a new login password for username via the ONTAP
// private CLI (POST /api/private/cli/security/login/password). The call is
// routed through the shared ONTAP REST client so it inherits the common
// transport stack (retry, authentication, logging, and the cert/SNI TLS setup).
func (rc *OntapRestProvider) ResetLoginPassword(ctx context.Context, vserver, username, newPassword string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	if err := client.Security().ResetLoginPassword(ctx, vserver, username, newPassword); err != nil {
		// The specific ONTAP reason (e.g. password reuse/policy violation) is
		// carried as the wrapped cause so it reaches the getWorkflow API cause.
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapExpertModePasswordError, err)
	}
	return nil
}

// ExpireLoginPassword expires username's password via the ONTAP private CLI
// (POST /api/private/cli/security/login/expire-password). The call is routed
// through the shared ONTAP REST client so it inherits the common transport
// stack (retry, authentication, logging, and the cert/SNI TLS setup).
func (rc *OntapRestProvider) ExpireLoginPassword(ctx context.Context, vserver, username string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	if err := client.Security().ExpireLoginPassword(ctx, vserver, username); err != nil {
		// The specific ONTAP reason is carried as the wrapped cause so it
		// reaches the getWorkflow API cause.
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapExpertModePasswordError, err)
	}
	return nil
}

// HasLoginPasswordResetSuccess reports whether username's temporary password was consumed since the given time.
func (rc *OntapRestProvider) HasLoginPasswordResetSuccess(ctx context.Context, username string, since time.Time) (bool, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	consumed, err := client.Security().HasLoginPasswordResetSuccess(ctx, username, since)
	if err != nil {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return consumed, nil
}
