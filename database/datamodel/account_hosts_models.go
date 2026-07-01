package datamodel

import (
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// AccountHost links an account to an external cluster host with a pinned flag and
// optional ONTAP expert-mode credentials.
//
// Pin invariant: pinned=true requires valid OntapModeCredentials (HasProvisionedCredentials).
// Unpinned rows may retain credentials for fast re-pin.
// Table columns are created by GORM AutoMigrate; post-migrations add partial unique indexes.
type AccountHost struct {
	BaseModel
	AccountID            int64                 `gorm:"column:account_id;index;not null"`
	HostID               string                `gorm:"column:host_id;index;not null"`
	Pinned               bool                  `gorm:"column:pinned;not null;default:false;index"`
	OntapModeCredentials *ExpertModeCredential `gorm:"column:service_credentials;type:jsonb"`
}

// TableName returns the account_hosts table name.
func (AccountHost) TableName() string {
	return "account_hosts"
}

// GetServiceUsername returns the ONTAP service username from credential metadata.
func (h *AccountHost) GetServiceUsername() string {
	if h == nil || h.OntapModeCredentials == nil {
		return ""
	}
	return h.OntapModeCredentials.Username
}

// HasProvisionedCredentials reports whether completed ONTAP service credentials exist for this row.
func (h *AccountHost) HasProvisionedCredentials() bool {
	if h == nil || h.OntapModeCredentials == nil {
		return false
	}
	cred := h.OntapModeCredentials
	if strings.TrimSpace(cred.Username) == "" {
		return false
	}
	switch cred.AuthType {
	case env.USERNAME_PWD:
		return strings.TrimSpace(cred.Password) != ""
	case env.USERNAME_PWD_SEC_MGR:
		return strings.TrimSpace(cred.SecretID) != ""
	case env.USER_CERTIFICATE:
		return strings.TrimSpace(cred.CertificateID) != "" || strings.TrimSpace(cred.CertificateIDNew) != ""
	default:
		return false
	}
}

// IsPinnedWithCredentials reports whether the row satisfies the pin invariant.
func (h *AccountHost) IsPinnedWithCredentials() bool {
	return h != nil && h.Pinned && h.HasProvisionedCredentials()
}
