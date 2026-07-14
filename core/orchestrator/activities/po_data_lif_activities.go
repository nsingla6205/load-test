package activities

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/activity"
)

// PODataLifActivity manages PO data LIF IP reservations in the lifs table.
type PODataLifActivity struct {
	SE database.Storage
}

// ResolvePONodeIDsInput resolves ONTAP node names to nodes table IDs for a PO host.
type ResolvePONodeIDsInput struct {
	HostUUID  string
	NodeNames []string
}

// ResolvePONodeIDs maps each requested node name to its persisted nodes.id for the host.
func (a *PODataLifActivity) ResolvePONodeIDs(ctx context.Context, in ResolvePONodeIDsInput) (map[string]int64, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Resolving PO node IDs for host %s", in.HostUUID))
	if a.SE == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("PO data LIF activity storage is not configured")))
	}
	host, err := a.SE.GetHost(ctx, in.HostUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	nodeIDs, err := a.SE.GetNodesByHostIDAndNames(ctx, host.ID, in.NodeNames)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nodeIDs, nil
}

// PODataLifReservationInput describes one data LIF IP reservation row to create.
type PODataLifReservationInput struct {
	Name   string
	NodeID int64
}

// AllocatePODataLifIPsInput reserves data LIF IPs from an ip_range into the lifs table.
type AllocatePODataLifIPsInput struct {
	IPRangeUUID   string
	AccountID     int64
	SvmID         int64
	Reservations  []PODataLifReservationInput
}

// AllocatePODataLifIPs selects and persists data LIF IP reservations before ONTAP LIF creation.
func (a *PODataLifActivity) AllocatePODataLifIPs(ctx context.Context, in AllocatePODataLifIPsInput) ([]*datamodel.Lif, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Allocating %d PO data LIF IPs from range %s", len(in.Reservations), in.IPRangeUUID))
	if a.SE == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("PO data LIF activity storage is not configured")))
	}
	reservations := make([]*datamodel.Lif, 0, len(in.Reservations))
	for _, item := range in.Reservations {
		lif := &datamodel.Lif{
			Name:      item.Name,
			AccountID: in.AccountID,
			NodeID:    item.NodeID,
		}
		if in.SvmID > 0 {
			lif.SvmID = sql.NullInt64{Int64: in.SvmID, Valid: true}
		}
		reservations = append(reservations, lif)
	}
	created, err := a.SE.AllocateDataLifIPs(ctx, in.IPRangeUUID, reservations)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return created, nil
}

// ReleaseDataLifReservationsInput soft-deletes reserved data LIF rows for rollback after ONTAP failure.
type ReleaseDataLifReservationsInput struct {
	SvmID int64
}

// ReleaseDataLifReservations removes data LIF IP reservations for the given SVM.
func (a *PODataLifActivity) ReleaseDataLifReservations(ctx context.Context, in ReleaseDataLifReservationsInput) error {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Releasing PO data LIF reservations for svm %d", in.SvmID))
	if a.SE == nil {
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("PO data LIF activity storage is not configured")))
	}
	if err := a.SE.DeleteLifsBySvmID(ctx, in.SvmID); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}
