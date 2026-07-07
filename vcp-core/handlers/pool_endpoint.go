package api

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func (h Handler) V1GetOntapCredentials(ctx context.Context, params oasgenserver.V1GetOntapCredentialsParams) (oasgenserver.V1GetOntapCredentialsRes, error) {
	// Get account name from query parameters
	if !params.AccountName.IsSet() {
		return &oasgenserver.V1GetOntapCredentialsBadRequest{
			Message: "Account name is required",
			Code:    400,
		}, nil
	}
	accountName := params.AccountName.Value
	// x-netapp-cluster-name header: when present, overrides the pool's own cluster
	// name for dedicated-capacity credential resolution.
	clusterExternalName := params.XNetappClusterName.Value

	// Standard / expert-mode flow. Returns nil for dedicated-capacity pools so we
	// fall back to the pinned-host credential path below.
	expertModeCredential, err := h.Orchestrator.GetExpertModePoolCreds(ctx, params.PoolId, accountName, params.UserName.Value)
	if err != nil {
		if res := mapGetOntapCredentialsError(err); res != nil {
			return res, nil
		}
		return &oasgenserver.V1GetOntapCredentialsInternalServerError{
			Message: fmt.Sprintf("Failed to get pool credentials: %v", err),
			Code:    500,
		}, nil
	}
	if expertModeCredential != nil {
		return convertUserCredentialsToOntapCredentialsV1(expertModeCredential), nil
	}

	// Dedicated-capacity flow: resolve credentials from the pinned account host for
	// the target cluster.
	dedicatedCredential, err := h.Orchestrator.GetDedicatedClusterPoolCreds(ctx, params.PoolId, accountName, clusterExternalName, params.UserName.Value)
	if err != nil {
		if res := mapGetOntapCredentialsError(err); res != nil {
			return res, nil
		}
		return &oasgenserver.V1GetOntapCredentialsInternalServerError{
			Message: fmt.Sprintf("Failed to get pool credentials: %v", err),
			Code:    500,
		}, nil
	}

	if dedicatedCredential == nil {
		return &oasgenserver.V1GetOntapCredentialsNotFound{
			Message: "Pool credentials not found",
			Code:    404,
		}, nil
	}

	return convertUserCredentialsToOntapCredentialsV1(dedicatedCredential), nil
}

// mapGetOntapCredentialsError maps known VCP errors to typed ontap-credentials
// responses. It returns nil when the error is not a recognised typed error, so
// the caller can apply a generic 500 fallback.
func mapGetOntapCredentialsError(err error) oasgenserver.V1GetOntapCredentialsRes {
	var customErr *errors.CustomError
	if !errors.As(err, &customErr) {
		return nil
	}
	switch {
	case customErr.IsError(errors.ErrPoolNotFound):
		return &oasgenserver.V1GetOntapCredentialsNotFound{
			Message: "Pool not found",
			Code:    404,
		}
	case customErr.IsError(errors.ErrPoolInCreatingState):
		msg := customErr.GetMessage()
		if msg == "" {
			msg = "Pool is in creating state"
		}
		return &oasgenserver.V1GetOntapCredentialsBadRequest{
			Message: msg,
			Code:    400,
		}
	case customErr.IsError(errors.ErrPoolInDeletingState):
		msg := customErr.GetMessage()
		if msg == "" {
			msg = "Pool is in deleting state"
		}
		return &oasgenserver.V1GetOntapCredentialsBadRequest{
			Message: msg,
			Code:    400,
		}
	case customErr.IsError(errors.ErrMissingRequiredInputError):
		msg := customErr.GetMessage()
		if msg == "" {
			msg = "Cluster is not registered for this dedicated-capacity pool"
		}
		return &oasgenserver.V1GetOntapCredentialsBadRequest{
			Message: msg,
			Code:    400,
		}
	}
	return nil
}

func convertUserCredentialsToOntapCredentialsV1(expertModeCredentials *models.UserCredentials) *oasgenserver.OntapCredentialsV1 {
	if expertModeCredentials == nil {
		return nil
	}

	secretID := oasgenserver.NewOptString(expertModeCredentials.SecretID)
	certificateID := oasgenserver.NewOptString(expertModeCredentials.CertificateID)
	password := oasgenserver.NewOptString(expertModeCredentials.Password)
	authType := oasgenserver.NewOptInt(expertModeCredentials.AuthType)
	username := oasgenserver.NewOptString(expertModeCredentials.Username)

	var endpointMappings []oasgenserver.OntapEndpoint
	if expertModeCredentials.OntapEndpoints != nil {
		for _, mapping := range expertModeCredentials.OntapEndpoints {
			endpointMappings = append(endpointMappings, oasgenserver.OntapEndpoint{
				IP:  mapping.IP,
				DNS: mapping.DNS,
			})
		}
	}

	caURI := oasgenserver.NewOptString(expertModeCredentials.GetCaURIWithFallback())
	serviceLevel := oasgenserver.NewOptString(expertModeCredentials.ServiceLevel)

	return &oasgenserver.OntapCredentialsV1{
		SecretID:          secretID,
		CertificateID:     certificateID,
		Password:          password,
		AuthType:          authType,
		OntapEndpoints:    endpointMappings,
		CaURI:             caURI,
		Username:          username,
		ServiceLevel:      serviceLevel,
	}
}
