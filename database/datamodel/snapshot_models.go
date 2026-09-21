package datamodel

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

type Snapshot struct {
	BaseModel
	Name               string              `gorm:"column:name"`
	Description        string              `gorm:"column:description"`
	State              string              `gorm:"column:state"`
	StateDetails       string              `gorm:"column:state_details"`
	AccountID          int64               `gorm:"column:account_id"`
	VolumeID           int64               `gorm:"column:volume_id"`
	Type               string              `gorm:"column:type"`
	IsAppConsistent    bool                `gorm:"column:is_app_consistent"`
	ExternalUUID       string              `gorm:"column:external_uuid"`
	SnapshotAttributes *SnapshotAttributes `gorm:"column:snapshot_attributes;type:jsonb"`
	Account            *Account            `gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	Volume             *Volume             `gorm:"ForeignKey:VolumeID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
}

type SnapshotAttributes struct {
	SizeInBytes            int64  `json:"size_in_bytes"`
	ExternalUUID           string `json:"external_uuid"`
	LogicalSizeUsedInBytes int64  `json:"logical_size_used_in_bytes"`
}

// SetExternalUUID writes the external UUID to both the external_uuid column and the snapshot attributes JSON.
func (s *Snapshot) SetExternalUUID(externalUUID string) {
	s.ExternalUUID = externalUUID
	if s.SnapshotAttributes == nil {
		s.SnapshotAttributes = &SnapshotAttributes{}
	}
	s.SnapshotAttributes.ExternalUUID = externalUUID
}

// GetExternalUUID returns the external UUID from the snapshot attributes JSON.
func (s *Snapshot) GetExternalUUID() string {
	if s == nil || s.SnapshotAttributes == nil {
		return ""
	}
	return s.SnapshotAttributes.ExternalUUID
}

func (v *SnapshotAttributes) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, v)
}

func (v *SnapshotAttributes) Value() (driver.Value, error) {
	return json.Marshal(v)
}

// SnapshotPolicy describes the storage model for snapshot policies
type SnapshotPolicy struct {
	Name      string                    `json:"name"`
	Comment   string                    `json:"comment"`
	IsEnabled bool                      `json:"is_enabled"`
	Schedules []*SnapshotPolicySchedule `json:"snapshot_policies"`
}

// SnapshotPolicySchedule describes the storage model for snapshot policy schedules
type SnapshotPolicySchedule struct {
	DaysOfMonth     []int  `json:"days_of_month"`
	DaysOfWeek      []int  `json:"days_of_week"`
	Hours           []int  `json:"hours"`
	Minutes         []int  `json:"minutes"`
	Count           int64  `json:"count"`
	SnapmirrorLabel string `json:"snapmirror_label"`
}

func (dp *SnapshotPolicy) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, dp)
}

func (dp *SnapshotPolicy) Value() (driver.Value, error) {
	return json.Marshal(dp)
}

func (dp *SnapshotPolicySchedule) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, dp)
}

func (dp *SnapshotPolicySchedule) Value() (driver.Value, error) {
	return json.Marshal(dp)
}
