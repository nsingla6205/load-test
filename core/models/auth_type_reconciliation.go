package models

import "time"

// AuthTypeReconciliationSelectionType controls how demoted pools are selected.
type AuthTypeReconciliationSelectionType string

const (
	// AuthTypeReconciliationSelectionAll selects every eligible demoted pool.
	AuthTypeReconciliationSelectionAll AuthTypeReconciliationSelectionType = "all"
	// AuthTypeReconciliationSelectionPoolList selects an explicit list of pool UUIDs.
	AuthTypeReconciliationSelectionPoolList AuthTypeReconciliationSelectionType = "poolList"
)

// AuthTypeReconciliationPoolResult describes dispatch or execution for one pool.
type AuthTypeReconciliationPoolResult struct {
	PoolUUID string `json:"poolUuid"`
	Status   string `json:"status"`
	JobID    string `json:"jobId,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// AuthTypeReconciliationJobPayload is the persisted state for an on-demand
// auth-type reconciliation request.
type AuthTypeReconciliationJobPayload struct {
	SelectionType string                             `json:"selectionType"`
	FromAuthType  int                                `json:"fromAuthType"`
	ToAuthType    int                                `json:"toAuthType"`
	DesiredCAURI  string                             `json:"desiredCaUri"`
	BatchSize     int                                `json:"batchSize"`
	Results       []AuthTypeReconciliationPoolResult `json:"results"`
}

// Attributes converts the typed payload to the database JobAttributes map.
func (payload AuthTypeReconciliationJobPayload) Attributes() map[string]interface{} {
	results := make([]interface{}, 0, len(payload.Results))
	for _, result := range payload.Results {
		results = append(results, map[string]interface{}{
			"poolUuid": result.PoolUUID,
			"status":   result.Status,
			"jobId":    result.JobID,
			"reason":   result.Reason,
		})
	}
	return map[string]interface{}{
		"selectionType": payload.SelectionType,
		"fromAuthType":  payload.FromAuthType,
		"toAuthType":    payload.ToAuthType,
		"desiredCaUri":  payload.DesiredCAURI,
		"batchSize":     payload.BatchSize,
		"results":       results,
	}
}

// AuthTypeReconciliationResponse describes an accepted reconciliation request.
type AuthTypeReconciliationResponse struct {
	JobID        string
	Status       string
	TotalPools   int
	QueuedPools  int
	SkippedPools int
	Results      []AuthTypeReconciliationPoolResult
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AuthTypeReconciliationStatus aggregates per-pool results from the parent job.
type AuthTypeReconciliationStatus struct {
	JobID           string
	Status          string
	TotalPools      int
	QueuedPools     int
	ProcessingPools int
	SucceededPools  int
	FailedPools     int
	SkippedPools    int
	Results         []AuthTypeReconciliationPoolResult
}
