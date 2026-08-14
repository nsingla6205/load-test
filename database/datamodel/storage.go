package datamodel

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
)

// Region describes the storage model for regions
type Region struct {
	BaseModel
	Name string `gorm:"column:name"`
}

// Zone describes the storage model for zones
type Zone struct {
	BaseModel
	Name     string        `gorm:"column:name;index"`
	Timezone string        `gorm:"column:timezone"`
	RegionID sql.NullInt64 `gorm:"column:region_id;index"`
}

// Host stores external storage host connection details and lifecycle state.
// Additional operational metadata is persisted in the host_attributes JSONB column.
type Host struct {
	BaseModel
	ExternalUUID      string             `gorm:"column:external_uuid;index"`
	ExternalName      string             `gorm:"column:external_name;type:text"`
	Name              string             `gorm:"column:name;type:text"`
	VendorID          *string            `gorm:"column:vendor_id;type:varchar(2048)"`
	Port              *int               `gorm:"column:port"`
	Protocol          string             `gorm:"column:protocol;default:'INSECURE_HTTPS'"`
	Username          string             `gorm:"column:username;type:text"`
	Password          string             `gorm:"column:password;type:text"` // encrypted before persistence
	State             string             `gorm:"column:state;type:text"`
	StateDetails      string             `gorm:"column:state_details;type:text"`
	Label             string             `gorm:"column:label;type:text"`
	IPAddress         string             `gorm:"column:ip_address;type:text"`
	Capacity          int64              `gorm:"column:capacity;not null;default:0"` // total nfsaas aggregate size in bytes
	EfficiencyFactor  float64            `gorm:"column:efficiency_factor;type:double precision;not null;default:1.4"`
	ProvisioningModel *ProvisioningModel `gorm:"column:provisioning_model;type:text;default:'THICK'"`
	Attributes        *HostAttributes    `gorm:"column:host_attributes;type:jsonb"`
	HostCredentials   *PoolCredentials   `gorm:"column:host_credentials;type:jsonb"`
}

// ProvisioningModel controls logical capacity enforcement for eligible pools on a host.
type ProvisioningModel string

const (
	// DefaultHostEfficiencyFactor is the default placement efficiency factor for hosts.
	DefaultHostEfficiencyFactor = 1.4

	// ProvisioningModelThick enforces logical pool capacity.
	ProvisioningModelThick ProvisioningModel = "THICK"
	// ProvisioningModelThin delegates physical capacity enforcement to ONTAP.
	ProvisioningModelThin ProvisioningModel = "THIN"

	// VCP_PASSPHRASE_V0 is the default encryption passphrase version for host credentials.
	VCP_PASSPHRASE_V0 = "VCP_PASSPHRASE_V0"
)

// HostEfficiencyFactorOrDefault returns the requested efficiency factor or the host default.
func HostEfficiencyFactorOrDefault(value *float64) float64 {
	if value != nil {
		return *value
	}
	return DefaultHostEfficiencyFactor
}

// HostProvisioningModelOrDefault validates a requested model and defaults omitted values to THICK.
func HostProvisioningModelOrDefault(value *ProvisioningModel) (ProvisioningModel, error) {
	if value == nil || *value == "" {
		return ProvisioningModelThick, nil
	}
	switch *value {
	case ProvisioningModelThick, ProvisioningModelThin:
		return *value, nil
	default:
		return "", fmt.Errorf("unknown provisioning model %q", *value)
	}
}

// EffectiveHostProvisioningModel returns the validated model for a persisted host.
func EffectiveHostProvisioningModel(host *Host) (ProvisioningModel, error) {
	if host == nil {
		return "", errors.New("host is required")
	}
	return HostProvisioningModelOrDefault(host.ProvisioningModel)
}

// HostAttributes contains host metadata stored as JSONB attributes.
type HostAttributes struct {
	MaintenanceMode   bool   `json:"maintenance_mode"`
	Version           string `json:"version,omitempty"`
	VersionGeneration int    `json:"versionGeneration"`
	VersionMajor      int    `json:"versionMajor"`
	VersionMinor      int    `json:"versionMinor"`
	PassphraseVersion string `json:"passphrase_version" gorm:"default:'VCP_PASSPHRASE_V0'"`
	Capabilities      string `json:"capabilities"`
	PublicAccess      bool   `json:"publicAccess" gorm:"default:false"`
}

// Scan implements the sql.Scanner interface for HostAttributes.
func (a *HostAttributes) Scan(value interface{}) error {
	if value == nil {
		*a = HostAttributes{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, a)
}

// Value implements the driver.Valuer interface for HostAttributes.
func (a HostAttributes) Value() (driver.Value, error) {
	return json.Marshal(a)
}
