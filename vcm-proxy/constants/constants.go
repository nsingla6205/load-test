// Package constants holds shared VCM proxy constant values.
package constants

const (
	// JobTypePoolCreate is the durable job type for pool create.
	JobTypePoolCreate = "POOL_CREATE"

	// OperationTimedOut is persisted when a job expires during restart recovery.
	OperationTimedOut = "Operation timed out"
)
