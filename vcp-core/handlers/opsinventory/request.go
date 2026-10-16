// Package opsinventory implements the read-only ops inventory API:
// POST /v1/internal/projects/{projectNumber}/locations/{locationId}/resources:get and :list.
//
// The shared Handler parses and validates requests, resolves the project and
// parents, maps errors and writes the audit log. Each resource type has its
// own Reader that declares what it accepts (Spec) and maps rows into the
// reviewed ops inventory response schema.
package opsinventory

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// ResourceType is the API resourceType value.
type ResourceType = oasgenserver.OpsResourceTypeV1

// ParentType is the API parent type value.
type ParentType = oasgenserver.OpsParentTypeV1

// TimeField is the API time filter field.
type TimeField = oasgenserver.OpsTimeFilterV1Field

const (
	ResourceTypePool            = oasgenserver.OpsResourceTypeV1Pool
	ResourceTypeVolume          = oasgenserver.OpsResourceTypeV1Volume
	ResourceTypeSnapshot        = oasgenserver.OpsResourceTypeV1Snapshot
	ResourceTypeReplication     = oasgenserver.OpsResourceTypeV1Replication
	ResourceTypeBackup          = oasgenserver.OpsResourceTypeV1Backup
	ResourceTypeBackupVault     = oasgenserver.OpsResourceTypeV1BackupVault
	ResourceTypeBackupPolicy    = oasgenserver.OpsResourceTypeV1BackupPolicy
	ResourceTypeCmek            = oasgenserver.OpsResourceTypeV1Cmek
	ResourceTypeActiveDirectory = oasgenserver.OpsResourceTypeV1ActiveDirectory
	ResourceTypeJob             = oasgenserver.OpsResourceTypeV1Job
)

// Filter keys accepted in filters, besides name and state.
const (
	FilterCorrelationID = "correlationId"
	FilterType          = "type"
)

// ParentSpec says how a list parent filters a resource table.
type ParentSpec struct {
	// Column is the column compared with the parent.
	Column readstore.Column
	// Owner is the parent table. Empty means the uuid is compared directly
	// without any lookup (for example a remote replication volume).
	Owner readstore.OwnerKind
	// ByID compares Column with the parent's internal id instead of its uuid.
	ByID bool
	// CheckOwner rejects a parent that belongs to another project.
	CheckOwner bool
}

// Spec is the contract a Reader declares. The shared handler validates every
// request against it before calling the reader.
type Spec struct {
	Type ResourceType
	// AccountColumn is the column that holds the owning account.
	// Empty for tables with no direct account (backup).
	AccountColumn readstore.Column
	// ProjectOptional allows a list without projectNumber (job only).
	ProjectOptional bool
	// GetByName enables identity.name on get.
	GetByName bool
	// NameParents are the parents that must be sent with a get by name.
	NameParents []ParentType
	// ListParents are the parents allowed on list.
	ListParents map[ParentType]ParentSpec
	// RequiredListParents must be present on every list.
	RequiredListParents []ParentType
	// NameFilter enables filters.name on list.
	NameFilter bool
	// StateColumn is the column filters.state compares with.
	StateColumn readstore.Column
	// States are the accepted filters.state values.
	States map[string]struct{}
	// TimeFields maps the accepted time filter fields to columns.
	TimeFields map[TimeField]readstore.Column
	// ExtraFilters maps the other accepted filter keys to columns.
	ExtraFilters map[string]readstore.Column
}

// GetRequest is a validated get. Exactly one of UUID or Name is set.
type GetRequest struct {
	UUID string
	Name string
	// Account is the resolved project, nil when projectNumber was not sent.
	Account *datamodel.Account
	// NameScope holds the project and parent predicates for a get by name.
	NameScope []readstore.Eq
}

// ListRequest is a validated list with a ready read-store query.
type ListRequest struct {
	Account *datamodel.Account
	Options readstore.ListOptions
}

// Resource is one mapped row.
type Resource struct {
	// ID is the internal row id, used only for the page token.
	ID int64
	// OwnerAccountID is the internal id of the owning account, nil when none.
	OwnerAccountID *int64
	// EnrichmentStatus is set by readers that call another system (job).
	EnrichmentStatus string
	Value            oasgenserver.OpsResourceV1
}

// Reader reads one resource type.
type Reader interface {
	Spec() Spec
	Get(ctx context.Context, req GetRequest) (Resource, error)
	List(ctx context.Context, req ListRequest) ([]Resource, error)
}
