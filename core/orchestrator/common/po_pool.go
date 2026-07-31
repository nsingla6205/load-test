package common

import (
	"errors"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// enablePOPremiumPools gates PO PREMIUM tier pool creation. Override in tests.
var enablePOPremiumPools = env.GetBool(env.EnvPOPremiumPoolsEnabled, false)

// enablePOSvmQoS gates per-SVM QoS policy application on PO pools. Override in tests.
var enablePOSvmQoS = env.GetBool(env.EnvPOSvmQoSEnabled, false)

// enablePOCmek gates CMEK/KMS on PO pool create. Override in tests.
var enablePOCmek = env.GetBool(env.EnvPOCmekEnabled, false)

// PO request validation messages. Exported so callers (endpoint validation,
// workflow branch) and tests can match on them consistently.
const (
	POValidationModeMsg            = "PO pools support only ONTAP mode"
	POValidationAutoTieringMsg     = "Auto-tiering is not supported for PO pools"
	POValidationManualQoSMsg       = "Manual QoS / Custom Performance is not supported for PO pools"
	POValidationRegionalOnlyMsg      = "PO Hardware support only regional pools"
	POValidationClusterNameMsg     = "clusterName is required for PO pools"
	POValidationHostUUIDMsg        = "host uuid is required for PO pools"
	POValidationServiceLevelMsg    = "PO pools support only EXTREME or PREMIUM service levels"
	POValidationPremiumDisabledMsg = "PO PREMIUM service level is not enabled"
	POValidationActiveDirectoryMsg = "Active directory cannot be assigned to PO ONTAP mode pool"
	POValidationLdapEnabledMsg     = "LDAP cannot be enabled for PO ONTAP mode pool"
	POValidationCmekMsg            = "CMEK is not enabled for PO Hardware pools"
	POValidationUpdateNotSupportedMsg = "Update operation is not supported for PO Hardware pools"
	POValidationZoneUpdateMsg      = "Zone cannot be updated for PO Hardware pools"
	POValidationLargeCapacityUpdateMsg = "Large capacity cannot be changed for PO Hardware pools"
)

// IsPOServiceLevel reports whether serviceLevel is one of the tiers supported by
// the PO path.
func IsPOServiceLevel(serviceLevel string) bool {
	return serviceLevel == ServiceLevelExtreme || serviceLevel == ServiceLevelPremium
}

// IsPORequest reports whether a create-pool request identifies a PO (Performance
// Offering) pool. PO requests are ONTAP mode with EXTREME|PREMIUM service level.
// Any other request follows the existing VLM path unchanged.
func IsPORequest(params *CreatePoolParams) bool {
	if params == nil {
		return false
	}
	return params.Mode == ONTAPMode && IsPOServiceLevel(params.ServiceLevel)
}

// IsPOModelPool reports whether an API-layer pool model is a PO pool.
// PO pools are ONTAP mode with EXTREME|PREMIUM service level.
func IsPOModelPool(pool *models.Pool) bool {
	if pool == nil {
		return false
	}
	return pool.APIAccessMode == ONTAPMode && IsPOServiceLevel(pool.ServiceLevel)
}

// IsPOPool reports whether a persisted pool is a PO (EXTREME|PREMIUM) pool.
func IsPOPool(pool *datamodel.Pool) bool {
	return IsPOServiceLevel(EffectivePoolServiceLevel(pool))
}

// EffectivePoolServiceLevel returns the persisted service level, preferring
// PoolAttributes.ServiceLevel when set.
func EffectivePoolServiceLevel(pool *datamodel.Pool) string {
	if pool == nil {
		return ""
	}
	if pool.PoolAttributes != nil && pool.PoolAttributes.ServiceLevel != "" {
		return pool.PoolAttributes.ServiceLevel
	}
	return pool.ServiceLevel
}

// IsEligibleONTAPModePool reports whether a pool is eligible for thin
// logical-capacity skip: ONTAP mode with effective EXTREME, or PREMIUM when
// enablePOPremiumPools is true. Non-ONTAP pools are never eligible.
func IsEligibleONTAPModePool(pool *datamodel.Pool) bool {
	if pool == nil || pool.APIAccessMode != ONTAPMode {
		return false
	}
	switch EffectivePoolServiceLevel(pool) {
	case ServiceLevelExtreme:
		return true
	case ServiceLevelPremium:
		return enablePOPremiumPools
	default:
		return false
	}
}

// ValidatePOCmekRestrictions rejects CMEK/KMS on PO pool create when PO_CMEK_ENABLED is false.
func ValidatePOCmekRestrictions(kmsConfigId string) error {
	if kmsConfigId != "" && !enablePOCmek {
		return errors.New(POValidationCmekMsg)
	}
	return nil
}

// ValidatePOAdLdapRestrictions rejects Active Directory and LDAP on PO pool create.
func ValidatePOAdLdapRestrictions(activeDirectoryId string, ldapEnabled bool) error {
	if activeDirectoryId != "" {
		return errors.New(POValidationActiveDirectoryMsg)
	}
	if ldapEnabled {
		return errors.New(POValidationLdapEnabledMsg)
	}
	return nil
}

// ValidatePORequest enforces the PO request constraints (Section 5.1 of the PO
// spec). A PO-identified request that violates any constraint must be rejected
// rather than silently routed to the VLM path. The returned error is suitable
// for wrapping as a non-retryable validation failure by the caller.
func ValidatePORequest(params *CreatePoolParams) error {
	if params == nil {
		return errors.New("nil create pool params")
	}
	if params.Mode != ONTAPMode {
		return errors.New(POValidationModeMsg)
	}
	if params.ClusterName == "" {
		return errors.New(POValidationClusterNameMsg)
	}
	if !IsPOServiceLevel(params.ServiceLevel) {
		return errors.New(POValidationServiceLevelMsg)
	}
	if params.ServiceLevel == ServiceLevelPremium && !enablePOPremiumPools {
		return errors.New(POValidationPremiumDisabledMsg)
	}
	// Phase 1 (PO1): auto-tiering flags are rejected until AT support lands in PO2.
	// HotTierSizeInBytes is still populated from sizeInBytes when AT is off.
	if params.AllowAutoTiering || params.EnableHotTierAutoResize {
		return errors.New(POValidationAutoTieringMsg)
	}
	if params.QosType == utils.QosTypeManual {
		return errors.New(POValidationManualQoSMsg)
	}
	if params.CustomPerformanceParams != nil && params.CustomPerformanceParams.Enabled {
		return errors.New(POValidationManualQoSMsg)
	}
	if !params.IsRegionalHA {
		return errors.New(POValidationRegionalOnlyMsg)
	}
	if err := ValidatePOAdLdapRestrictions(params.ActiveDirectoryId, params.LdapEnabled); err != nil {
		return err
	}
	if err := ValidatePOCmekRestrictions(params.KmsConfigId); err != nil {
		return err
	}
	return nil
}

// ValidatePOPoolHostUUID ensures a persisted PO pool references its ONTAP host.
func ValidatePOPoolHostUUID(pool *datamodel.Pool) error {
	if pool == nil || strings.TrimSpace(pool.HostUUID) == "" {
		return errors.New(POValidationHostUUIDMsg)
	}
	return nil
}

// DerivePOStorageClass returns HARDWARE for EXTREME|PREMIUM service levels and
// SOFTWARE otherwise. Storage class is never persisted for PO pools.
func DerivePOStorageClass(serviceLevel string) string {
	if IsPOServiceLevel(serviceLevel) {
		return StorageClassHardware
	}
	return StorageClassSoftware
}

func POSvmQoSEnabled() bool {
	return enablePOSvmQoS
}

func POCmekEnabled() bool {
	return enablePOCmek
}

// BuildPODataPathParams assembles SDE-aligned CVN datapath inputs for a PO pool
// create. MicrozoneUUID is the ONTAP host UUID; ZoneUUID is resolved in
// CVNActivity from LOCAL_REGION.
func BuildPODataPathParams(
	accountUUID, vendorSubnetID, hostUUID, serviceLevel string,
	subnetMaskConfigured int,
	poolUUID string,
	poolID int64,
) DataPathParams {
	vlanBandwidth, isFlexGroup := POCVNNetworkTier(serviceLevel)
	return DataPathParams{
		AccountUUID:     accountUUID,
		RemoteAccountID: vendorSubnetID,
		MicrozoneUUID:   hostUUID,
		SubnetMaskSize:  POPoolSubnetMaskSize(subnetMaskConfigured),
		VlanBandwidth:   vlanBandwidth,
		IsMultiLIF:      true,
		IsFlexGroup:     isFlexGroup,
		PoolUUID:        poolUUID,
		PoolID:          poolID,
	}
}

// POVlanBandwidth returns the CVN VLAN bandwidth tier for PO pools (env-configurable).
func POVlanBandwidth() string {
	if bw := strings.TrimSpace(env.GetString(env.EnvPOVlanBandwidth, VlanBandwidth50G)); bw != "" {
		return bw
	}
	return VlanBandwidth50G
}
