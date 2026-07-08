package vcm

import (
	"context"
)

// Storage is the backend-agnostic persistence surface, implemented by a concrete
// driver selected via Open. It exposes no driver-specific types.
type (
	Storage interface {
		// Connect establishes the backend connection; isAdmin requests
		// admin-capable access.
		Connect(isAdmin bool) error
		Close() error
		HealthCheck() error
		Migrate(ctx context.Context) error
		SetupDatabase(ctx context.Context) error
		// DatabasePath returns a diagnostic-only identifier for the database.
		DatabasePath() string

		VCMDatastore
	}

	// VCMDatastore is the data-access surface for a domain layer to extend.
	VCMDatastore interface{}
)
