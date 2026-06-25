package datamodel

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

type KmsConfig struct {
	BaseModel
	Name              string          `gorm:"column:name"`
	Description       string          `gorm:"column:description"`
	State             string          `gorm:"column:state"`
	StateDetails      string          `gorm:"column:state_details"`
	KeyRing           string          `gorm:"column:key_ring"`
	KeyRingLocation   string          `gorm:"column:key_ring_location"`
	KeyName           string          `gorm:"column:key_name"`
	AccountID         int64           `gorm:"column:account_id"`
	Account           *Account        `gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;OnUpdate:RESTRICT;"`
	CustomerProjectID string          `gorm:"column:customer_project_id"`
	KeyProjectID      string          `gorm:"column:key_project_id"`
	KmsGrant          string          `gorm:"column:kms_grant"`
	ResourceID        string          `gorm:"column:resource_id"`
	ServiceAccountID  *int64          `gorm:"column:service_account_id"`
	ServiceAccount    *ServiceAccount `gorm:"ForeignKey:ServiceAccountID;AssociationForeignKey:ID:OnUpdate:RESTRICT;"`
	KmsAttributes     *KmsAttributes  `gorm:"column:kms_attributes;type:jsonb"`
}

type KmsAttributes struct {
	SdeKmsConfigUUID          string `json:"sde_kms_config_uuid"`
	SdeServiceAccountEmail    string `json:"sde_service_account_email"`
	Instructions              string `json:"instructions"`
	SdeKmsConfigIsHealthy     bool   `json:"sde_is_healthy"`
	SdeKmsConfigHealthError   string `json:"sde_health_error"`
	SdeKmsConfigOperationURI  string `json:"sde_operation_uri"`
	SdeKmsConfigOperationDone bool   `json:"sde_operation_done"`
	CreationMode              string `json:"creation_mode,omitempty"` // "SDE" or "VCP"; empty means "SDE" (backward compat)
	VcpServiceAccountEmail    string `json:"vcp_service_account_email"`
}

const (
	KmsCreationModeSDE = "SDE"
	KmsCreationModeVCP = "VCP"
)

// GetCreationMode returns the creation mode, defaulting to "SDE" for backward compatibility.
func (a *KmsAttributes) GetCreationMode() string {
	if a == nil || a.CreationMode == "" {
		return KmsCreationModeSDE
	}
	return a.CreationMode
}

// IsVCPCreated returns true if the KMS config was created via VCP (no SDE involvement).
func (a *KmsAttributes) IsVCPCreated() bool {
	return a.GetCreationMode() == KmsCreationModeVCP
}

func (kmsAttributes *KmsAttributes) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Type assertion to []byte failed for KMS attribute value: ", value))
	}
	return json.Unmarshal(bytes, &kmsAttributes)
}

func (kmsAttributes KmsAttributes) Value() (driver.Value, error) {
	return json.Marshal(kmsAttributes)
}

type ServiceAccount struct {
	BaseModel
	Name                           string                    `gorm:"column:name"`
	Description                    string                    `gorm:"column:description"`
	State                          string                    `gorm:"column:state"`
	StateDetails                   string                    `gorm:"column:state_details"`
	AccountID                      int64                     `gorm:"column:account_id"`
	ServiceName                    string                    `gorm:"column:service_name"`
	ServiceAccountEmail            string                    `gorm:"column:service_account_email"`
	ServiceAccountPasswordLocation string                    `gorm:"column:service_account_password_location"`
	ServiceAccountAttributes       *ServiceAccountAttributes `gorm:"column:service_account_attributes;type:jsonb"`
}

type ServiceAccountAttributes struct {
	// Keys array stores multiple service account keys during rotation
	// During normal operation, this will be empty or contain only the primary key
	// During rotation, it contains both old and new keys
	Keys []ServiceAccountKey `json:"keys,omitempty"`
}

// ServiceAccountKey represents a single service account key
type ServiceAccountKey struct {
	KeyID     string    `json:"key_id"`     // GCP key ID (extracted from key data)
	KeyData   string    `json:"key_data"`   // Encrypted key data
	IsPrimary bool      `json:"is_primary"` // Is this the primary/current key
	CreatedAt time.Time `json:"created_at"` // When this key was created
	IsActive  bool      `json:"is_active"`  // Is this key still active (not deleted from GCP)
}

func (saAttributes *ServiceAccountAttributes) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New(fmt.Sprint("Type assertion to []byte failed for Service account value: ", value))
	}
	return json.Unmarshal(bytes, &saAttributes)
}

func (saAttributes ServiceAccountAttributes) Value() (driver.Value, error) {
	return json.Marshal(saAttributes)
}

// Helper functions for managing ServiceAccount keys

// GetPrimaryKey returns the primary key from the keys array
func (sa *ServiceAccount) GetPrimaryKey() *ServiceAccountKey {
	if sa.ServiceAccountAttributes == nil {
		return nil
	}
	for i := range sa.ServiceAccountAttributes.Keys {
		if sa.ServiceAccountAttributes.Keys[i].IsPrimary {
			return &sa.ServiceAccountAttributes.Keys[i]
		}
	}
	return nil
}

// GetKeyByID returns a key by its key ID
func (sa *ServiceAccount) GetKeyByID(keyID string) *ServiceAccountKey {
	if sa.ServiceAccountAttributes == nil {
		return nil
	}
	for i := range sa.ServiceAccountAttributes.Keys {
		if sa.ServiceAccountAttributes.Keys[i].KeyID == keyID {
			return &sa.ServiceAccountAttributes.Keys[i]
		}
	}
	return nil
}

// AddKey adds a new key to the keys array
func (sa *ServiceAccount) AddKey(key ServiceAccountKey) {
	if sa.ServiceAccountAttributes == nil {
		sa.ServiceAccountAttributes = &ServiceAccountAttributes{
			Keys: []ServiceAccountKey{},
		}
	}
	sa.ServiceAccountAttributes.Keys = append(sa.ServiceAccountAttributes.Keys, key)
}

// RemoveKey removes a key by its key ID
func (sa *ServiceAccount) RemoveKey(keyID string) bool {
	if sa.ServiceAccountAttributes == nil {
		return false
	}
	for i, key := range sa.ServiceAccountAttributes.Keys {
		if key.KeyID == keyID {
			sa.ServiceAccountAttributes.Keys = append(
				sa.ServiceAccountAttributes.Keys[:i],
				sa.ServiceAccountAttributes.Keys[i+1:]...,
			)
			return true
		}
	}
	return false
}

// SetPrimaryKey sets a key as primary and marks others as non-primary
func (sa *ServiceAccount) SetPrimaryKey(keyID string) bool {
	if sa.ServiceAccountAttributes == nil {
		return false
	}
	found := false
	for i := range sa.ServiceAccountAttributes.Keys {
		if sa.ServiceAccountAttributes.Keys[i].KeyID == keyID {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	// Key was found, now set it as primary and others as non-primary
	for i := range sa.ServiceAccountAttributes.Keys {
		if sa.ServiceAccountAttributes.Keys[i].KeyID == keyID {
			sa.ServiceAccountAttributes.Keys[i].IsPrimary = true
		} else {
			sa.ServiceAccountAttributes.Keys[i].IsPrimary = false
		}
	}
	return true
}

// GetAllActiveKeys returns all active keys
func (sa *ServiceAccount) GetAllActiveKeys() []ServiceAccountKey {
	if sa.ServiceAccountAttributes == nil {
		return []ServiceAccountKey{}
	}
	var activeKeys []ServiceAccountKey
	for _, key := range sa.ServiceAccountAttributes.Keys {
		if key.IsActive {
			activeKeys = append(activeKeys, key)
		}
	}
	return activeKeys
}

// GetKeysMarkedForDeletion returns all keys that are marked for deletion (IsPrimary=false AND IsActive=false)
// These keys should be deleted from GCP by DeleteOldSAKeyFromGCPActivity
func (sa *ServiceAccount) GetKeysMarkedForDeletion() []ServiceAccountKey {
	if sa.ServiceAccountAttributes == nil {
		return []ServiceAccountKey{}
	}
	var keysToDelete []ServiceAccountKey
	for _, key := range sa.ServiceAccountAttributes.Keys {
		if !key.IsPrimary && !key.IsActive {
			keysToDelete = append(keysToDelete, key)
		}
	}
	return keysToDelete
}
