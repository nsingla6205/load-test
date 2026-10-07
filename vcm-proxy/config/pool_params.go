package config

import (
	"fmt"
	"strings"
	"time"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// SeedConfig returns a copy of the seed configuration currently held in memory.
// It is the zero value until CLH pushes a clusterConfig through POST /v1/config.
func (s *Store) SeedConfig() vcmserver.SeedConfigV1 {
	return currentSeedConfig(s.CurrentSetConfig())
}

// DeploymentID returns the cluster identity seeded through POST /v1/config.
func (s *Store) DeploymentID() (string, error) {
	deploymentID := s.SeedConfig().DeploymentId.Or("")
	if deploymentID == "" {
		return "", fmt.Errorf("%w: VCM deploymentId is not configured", datamodel.ErrInvalidConfig)
	}
	return deploymentID, nil
}

// SeedPoolParams projects the in-memory seed configuration onto the VCM pool
// parameters. It supplies every input owned by configuration rather than by the
// request: cluster identity, zones, projects, networking and ONTAP credentials.
// Callers overlay the request-specific values with ApplyClusterShape before
// dispatching to the orchestrator.
func (s *Store) SeedPoolParams() (*commonparams.VCMCreatePoolParams, error) {
	seed := s.SeedConfig()
	if err := validateSeedForPoolCreate(seed); err != nil {
		return nil, fmt.Errorf("%w: %v", datamodel.ErrInvalidConfig, err)
	}

	netConfig := seed.NetConfig.Value
	params := &commonparams.VCMCreatePoolParams{
		DeploymentID:          seed.DeploymentId.Value,
		PrimaryZone:           seed.Zone1.Value,
		SecondaryZone:         seed.Zone2.Or(""),
		MediatorZone:          seed.MediatorZone.Or(""),
		ProjectID:             optionalInt64String(seed.ConsumerProjectNum),
		ServiceTenantProject:  optionalInt64String(seed.ServiceTenantProjectNum),
		SNHostProject:         optionalInt64String(seed.SnHostProjectNum),
		ServiceAccountEmail:   seed.ServiceAccountEmail.Or(""),
		CaURI:                 seed.CaUri.Or(""),
		OntapLicenseSecretURI: seed.OntapLicenseSecret.Or(""),
		NodeMgmt:              toVCMNetConfig(netConfig.Nodemgmt.Value),
		OntapCredentials:      ToVCMOntapCredentials(seed.OntapCredentials),
	}
	if data, ok := netConfig.Data.Get(); ok {
		dataNet := toVCMNetConfig(data)
		params.Data = &dataNet
	}
	return params, nil
}

// applyOntapModeRbac copies the RBAC bootstrap pair from the request's cluster
// shape. The request is the only source of the pair.
func applyOntapModeRbac(params *commonparams.VCMCreatePoolParams, shape vcmserver.ClusterShapeV1) {
	rbac, ok := shape.OntapModeRbac.Get()
	if !ok {
		return
	}
	params.OntapModeRbacFileURL = strings.TrimSpace(rbac.RbacFileUrl.Or(""))
	params.OntapModeRbacChecksum = strings.TrimSpace(rbac.RbacFileCheckSum.Or(""))
}

// ValidateOntapModeRbac rejects a cluster shape in mode ONTAP that does not
// carry both halves of the RBAC pair, which the ONTAP-mode user is created with.
func ValidateOntapModeRbac(shape vcmserver.ClusterShapeV1) error {
	if shape.Mode.Or(vcmserver.ClusterShapeV1ModeDEFAULT) != vcmserver.ClusterShapeV1ModeONTAP {
		return nil
	}
	rbac := shape.OntapModeRbac.Or(vcmserver.OntapModeRbacV1{})
	if strings.TrimSpace(rbac.RbacFileUrl.Or("")) == "" {
		return fmt.Errorf("clusterShape.ontapModeRbac.rbacFileUrl is required when clusterShape.mode is ONTAP")
	}
	if strings.TrimSpace(rbac.RbacFileCheckSum.Or("")) == "" {
		return fmt.Errorf("clusterShape.ontapModeRbac.rbacFileCheckSum is required when clusterShape.mode is ONTAP")
	}
	return nil
}

// ApplyClusterShape overlays the CLH-computed shape from a create-cluster
// request onto pool parameters sourced from configuration. An unset shape field
// leaves the configured value untouched; mode falls back to DEFAULT.
func ApplyClusterShape(params *commonparams.VCMCreatePoolParams, shape vcmserver.ClusterShapeV1) {
	if params == nil {
		return
	}
	params.Mode = string(shape.Mode.Or(vcmserver.ClusterShapeV1ModeDEFAULT))
	params.QosType = shape.QosType.Or("")
	params.ThroughputMibps = shape.ProvisionedThroughputMibps.Or(0)
	params.Iops = shape.ProvisionedIops.Or(0)
	params.SizeInBytes = shape.ProvisionedSizeInBytes.Or(0)
	if regionIdentifier, ok := shape.RegionIdentifier.Get(); ok {
		params.RegionIdentifier = regionIdentifier
	}
	if autoTiering, ok := shape.AutoTieringConfig.Get(); ok {
		params.AutoTierBucketName = autoTiering.AutoTierBucketName.Or("")
	}
	applyOntapModeRbac(params, shape)
}

// CreatePoolParams builds the orchestrator input for a create-cluster request,
// merging the request-level controls and cluster shape with the configuration
// held in memory from POST /v1/config.
func (s *Store) CreatePoolParams(requestID string, deadline time.Time, shape vcmserver.ClusterShapeV1) (*commonparams.CreatePoolParams, error) {
	vcm, err := s.SeedPoolParams()
	if err != nil {
		return nil, err
	}
	vcm.RequestID = requestID
	vcm.Deadline = deadline
	ApplyClusterShape(vcm, shape)
	if err := validateOntapModeCreateParams(vcm); err != nil {
		return nil, fmt.Errorf("%w: %v", datamodel.ErrInvalidConfig, err)
	}
	return &commonparams.CreatePoolParams{VCM: vcm}, nil
}

// UpdatePoolParams builds the orchestrator input for an update-cluster request,
// merging the request-level controls and complete target cluster shape with the
// configuration held in memory from POST /v1/config.
func (s *Store) UpdatePoolParams(requestID string, deadline time.Time, shape vcmserver.ClusterShapeV1, updateMask, changeType string) (*commonparams.UpdatePoolParams, error) {
	vcm, err := s.SeedPoolParams()
	if err != nil {
		return nil, err
	}
	vcm.RequestID = requestID
	vcm.Deadline = deadline
	ApplyClusterShape(vcm, shape)
	return &commonparams.UpdatePoolParams{
		VCM: &commonparams.VCMUpdatePoolParams{
			VCMCreatePoolParams: *vcm,
			UpdateMask:          updateMask,
			ChangeType:          changeType,
		},
	}, nil
}

// validateOntapModeCreateParams rejects an ONTAP-mode create request that
// cannot provision the dedicated ONTAP-mode user and its restricted role.
//
// validateOntapCredentials treats ontapModeUserCredentials as optional because
// a default-mode cluster does not need it. For mode ONTAP it is mandatory: a
// pool that reaches ready without that user answers every ONTAP-mode request
// with a 401.
func validateOntapModeCreateParams(params *commonparams.VCMCreatePoolParams) error {
	if params == nil || !strings.EqualFold(strings.TrimSpace(params.Mode), string(vcmserver.ClusterShapeV1ModeONTAP)) {
		return nil
	}

	var modeCredential *commonparams.VCMOntapCredential
	for i := range params.OntapCredentials {
		if strings.EqualFold(strings.TrimSpace(params.OntapCredentials[i].Type), vcmCredentialTypeOntapMode) {
			modeCredential = &params.OntapCredentials[i]
			break
		}
	}
	if modeCredential == nil {
		return fmt.Errorf("VCM ontapCredentials.ontapModeUserCredentials is required when clusterShape.mode is ONTAP")
	}
	if !modeCredential.IsCertAuth && strings.TrimSpace(modeCredential.Username) == "" {
		return fmt.Errorf("VCM ontapCredentials.ontapModeUserCredentials.username is required for password authentication")
	}
	// A password ONTAP-mode user with no secret has nothing to authenticate
	// with: DeriveOntapModeCredentials maps it to USERNAME_PWD with an empty
	// password, and resolveOntapModeCredentials later fails the create job with
	// "credential secret id is missing". Reject it here for a crisper error.
	// Local is exempt: pool create provisions this user from the VCM_LOCAL_*
	// flags, so a local create carries no secret by design.
	if !modeCredential.IsCertAuth && !env.IsLocalEnv() && strings.TrimSpace(modeCredential.SecretID) == "" {
		return fmt.Errorf("VCM ontapCredentials.ontapModeUserCredentials.secretId is required for password authentication")
	}
	return nil
}

// validateSeedForPoolCreate rejects a seed configuration that cannot produce a
// dispatchable pool-create request. Runtime addressing (ipConfig) is
// deliberately not required: the cluster does not exist yet at create time.
func validateSeedForPoolCreate(seed vcmserver.SeedConfigV1) error {
	if seed.DeploymentId.Or("") == "" {
		return fmt.Errorf("VCM deploymentId is not configured")
	}
	if seed.Zone1.Or("") == "" {
		return fmt.Errorf("VCM zone1 is not configured")
	}
	netConfig, ok := seed.NetConfig.Get()
	if !ok {
		return fmt.Errorf("VCM netConfig is not configured")
	}
	if !netConfig.Nodemgmt.IsSet() {
		return fmt.Errorf("VCM netConfig.nodemgmt is not configured")
	}
	ontapCreds, ok := seed.OntapCredentials.Get()
	if !ok {
		return fmt.Errorf("VCM ontapCredentials are not configured")
	}
	if err := validateOntapCredentials(ontapCreds); err != nil {
		return err
	}
	// Certificate auth requires VSA_DEPLOYED_DNS_NAME for TLS SNI.
	if def, ok := ontapCreds.DefaultModeUserCredentials.Get(); ok && userAuthIsCert(def) && env.VsaDeployedDnsName == "" {
		return fmt.Errorf("certificate auth requires VSA_DEPLOYED_DNS_NAME")
	}
	return nil
}

func toVCMNetConfig(cfg vcmserver.NetInterfaceConfigV1) commonparams.VCMNetConfig {
	return commonparams.VCMNetConfig{VPC: cfg.Vpc, Subnet: cfg.Subnet}
}
