package datamodel

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// InternalTrial is the CCFE GetInternalTrial response (google.cloud.netapp.v1main.InternalTrial).
type InternalTrial struct {
	// Identifier. Resource name: projects/{project}/locations/{location}/trial
	Name string `json:"name"`
	// Output only. The time when the trial started.
	StartTime time.Time `json:"startTime"`
	// Output only. The time when the trial ends.
	EndTime time.Time `json:"endTime"`
	// Output only. Exit reason enum (google.cloud.netapp.v1main.Trial.ExitReason).
	ExitReason *TrialExitReason `json:"exitReason,omitempty"`
}

// TrialExitReason is google.cloud.netapp.v1main.Trial.ExitReason from the CCFE API.
type TrialExitReason string

// UnmarshalJSON accepts enum values as JSON strings (typical) or numbers (proto JSON).
func (r *TrialExitReason) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*r = ""
		return nil
	}

	var enumName string
	if err := json.Unmarshal(data, &enumName); err == nil {
		*r = TrialExitReason(enumName)
		return nil
	}

	var enumNumber json.Number
	if err := json.Unmarshal(data, &enumNumber); err == nil {
		*r = TrialExitReason(enumNumber.String())
		return nil
	}

	return fmt.Errorf("trial exit_reason: invalid JSON value %s", string(data))
}

// String returns the enum name for logging and persistence.
func (r TrialExitReason) String() string {
	return string(r)
}

// IsSet reports whether the enum carries a non-empty, non-unspecified value.
func (r TrialExitReason) IsSet() bool {
	switch strings.TrimSpace(string(r)) {
	case "", "EXIT_REASON_UNSPECIFIED", "UNSPECIFIED":
		return false
	default:
		return true
	}
}

// StringPtr returns a string pointer for account_metadata when the enum is set.
func (r *TrialExitReason) StringPtr() *string {
	if r == nil || !r.IsSet() {
		return nil
	}
	s := r.String()
	return &s
}

// FormatInternalTrialResourceName builds projects/{project}/locations/{location}/trial.
func FormatInternalTrialResourceName(projectNumber, location string) string {
	return fmt.Sprintf("projects/%s/locations/%s/trial", projectNumber, location)
}

// ParseInternalTrialResourceName splits a trial resource name into project number and location.
func ParseInternalTrialResourceName(name string) (projectNumber, location string, err error) {
	parts := strings.Split(name, "/")
	if len(parts) != 5 || parts[0] != "projects" || parts[2] != "locations" || parts[4] != "trial" {
		return "", "", fmt.Errorf("invalid internal trial resource name: %s", name)
	}
	if parts[1] == "" || parts[3] == "" {
		return "", "", fmt.Errorf("invalid internal trial resource name: %s", name)
	}
	return parts[1], parts[3], nil
}

// TrialResourceNameForAccount builds the InternalTrial resource name using the deployment region.
func TrialResourceNameForAccount(accountName, localRegion string) string {
	accountName = strings.TrimSpace(accountName)
	localRegion = strings.TrimSpace(localRegion)
	if accountName == "" || localRegion == "" {
		return ""
	}
	return FormatInternalTrialResourceName(accountName, localRegion)
}

// ToAccountTrialMode maps CCFE trial fields into account_metadata.trialMode (camelCase JSON).
// Zero-value timestamps (unset in the CCFE response) are left nil rather than persisted as "0001-01-01T00:00:00Z".
func (t *InternalTrial) ToAccountTrialMode() *AccountTrialMode {
	if t == nil {
		return nil
	}
	mode := &AccountTrialMode{
		ExitReason: t.ExitReason.StringPtr(),
	}
	if !t.StartTime.IsZero() {
		start := t.StartTime
		mode.StartTime = &start
	}
	if !t.EndTime.IsZero() {
		end := t.EndTime
		mode.EndTime = &end
	}
	return mode
}
