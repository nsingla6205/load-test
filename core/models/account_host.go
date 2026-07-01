package models

// SetAccountHostsResponse is returned when async account-host pinning is initiated.
type SetAccountHostsResponse struct {
	JobID  string `json:"jobId"`
	Status string `json:"status"`
}
