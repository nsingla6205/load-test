package utils

import (
	"errors"
	"fmt"
	"time"
)

const defaultKeysetLimit = 1000

// Keyset defines a cursor for stable, seek-based pagination.
type Keyset struct {
	Limit          int
	AfterTimestamp time.Time
	AfterID        int64
}

// Validate rejects a non-positive page size and partially populated cursors.
// A nil keyset is valid and means the first page with the default limit.
func (k *Keyset) Validate() error {
	if k == nil {
		return nil
	}

	if k.Limit <= 0 {
		return fmt.Errorf("keyset limit must be positive, got %d", k.Limit)
	}

	hasTimestamp := !k.AfterTimestamp.IsZero()
	hasID := k.AfterID != 0
	if hasTimestamp != hasID {
		return errors.New("keyset cursor requires both after timestamp and after ID")
	}

	return nil
}

// IsFirstPage reports whether the keyset has no cursor.
func (k *Keyset) IsFirstPage() bool {
	return k == nil || (k.AfterTimestamp.IsZero() && k.AfterID == 0)
}

// PageLimit returns the requested page size or the default.
func (k *Keyset) PageLimit() int {
	if k == nil || k.Limit <= 0 {
		return defaultKeysetLimit
	}
	return k.Limit
}
