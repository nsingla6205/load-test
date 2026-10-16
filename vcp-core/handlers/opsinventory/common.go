package opsinventory

import (
	"context"
	"regexp"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"gorm.io/gorm"
)

// Every model embeds BaseModel, so every type has both time columns.
var baseTimeFields = map[TimeField]readstore.Column{
	oasgenserver.OpsTimeFilterV1FieldCreatedAt: readstore.ColCreatedAt,
	oasgenserver.OpsTimeFilterV1FieldUpdatedAt: readstore.ColUpdatedAt,
}

// lifecycleStates are the shared storage lifecycle states (datamodel.LifeCycleState*).
var lifecycleStates = []string{
	datamodel.LifeCycleStateCreating, datamodel.LifeCycleStatePreparing, datamodel.LifeCycleStateOngoing,
	datamodel.LifeCycleStateReverting, datamodel.LifeCycleStateUndeleting, datamodel.LifeCycleStateCompleted,
	datamodel.LifeCycleStateRestoring, datamodel.LifeCycleStateSplitting, datamodel.LifeCycleStateAvailable,
	datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateDisabled,
	datamodel.LifeCycleStateDisabling, datamodel.LifeCycleStateEnabling, datamodel.LifeCycleStateUpdating,
	datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeleted, datamodel.LifeCycleStateError,
	datamodel.LifeCycleStateRetained, datamodel.LifeCycleStateCreated, datamodel.LifeCycleStateKeyCheckPending,
	datamodel.LifeCycleStateMigrating, datamodel.LifeCycleStateDegraded, datamodel.LifeCycleStateUnknown,
	datamodel.LifeCycleStateDiagnosing,
}

// resourceEventStates are written by project/resource events (datamodel.ResourceEventState*).
var resourceEventStates = []string{
	datamodel.ResourceEventStateOff, datamodel.ResourceEventStateOn, datamodel.ResourceEventStateDelete,
}

func stateSet(groups ...[]string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, group := range groups {
		for _, s := range group {
			set[s] = struct{}{}
		}
	}
	return set
}

// knownStateDetails are the fixed phrases the control plane writes to
// state_details. Any other value may be raw backend error text and is withheld.
var knownStateDetails = stateSet([]string{
	datamodel.LifeCycleStateCreatingDetails, datamodel.LifeCycleStateRevertingDetails,
	datamodel.LifeCycleStateUndeletingDetails, datamodel.LifeCycleStateRestoringDetails,
	datamodel.LifeCycleStateAvailableDetails, datamodel.LifeCycleStateDisabledDetails,
	datamodel.LifeCycleStateUpdatingDetails, datamodel.LifeCycleStateSyncDetails,
	datamodel.LifeCycleStateDeletingDetails, datamodel.LifeCycleStateSplittingDetails,
	datamodel.LifeCycleStateDeletedDetails, datamodel.LifeCycleStateCompletedDetails,
	datamodel.LifeCycleStateRetainedDetails, datamodel.LifeCycleStateOngoingDetails,
	datamodel.LifeCycleStateCreationErrorDetails, datamodel.LifeCycleStateUpdateErrorDetails,
	datamodel.LifeCycleStateDeletionErrorDetails, datamodel.LifeCycleStateReadyDetails,
	datamodel.LifeCycleStateCreatedDetails, datamodel.LifeCycleStateUnknownDetails,
	datamodel.LifeCycleStateInUseDetails, datamodel.LifeCycleStateMigratingDetails,
	datamodel.LifeCycleStateDegradedDetails, datamodel.LifeCycleStateVolMigratingDetails,
	datamodel.LifeCycleStateHyperscalerDisabledDetails, datamodel.LifeCycleStateDiagnosingDetails,
	datamodel.InitiatingClusterPeering, datamodel.InitiatingSVMPeering, datamodel.WaitingForClusterPeering,
	datamodel.ErrorDuringClusterPeer, datamodel.ClusterPeeringExpired, datamodel.WaitingForSVMPeering,
	datamodel.ErrorDuringSVMPeering, datamodel.SVMPeeringExpired, datamodel.ErrorUnencryptedVolume,
	datamodel.ErrorCreatingCacheVolume, datamodel.ClusterPeeringSourceUnreachable,
	datamodel.VolumeReplicationBreakRelationshipQuotaRuleFailure,
})

// stateDetails returns the stored details only when they are a known phrase.
// withheld is true when stored free text was dropped.
func stateDetails(details string) (value oasgenserver.OptString, withheld oasgenserver.OptBool) {
	if details == "" {
		return value, oasgenserver.NewOptBool(false)
	}
	if _, ok := knownStateDetails[details]; ok {
		return oasgenserver.NewOptString(details), oasgenserver.NewOptBool(false)
	}
	return value, oasgenserver.NewOptBool(true)
}

var formatVerb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)

// catalogMessage returns the error catalog message for a tracking id, with
// format verbs blanked. It never returns raw error text.
func catalogMessage(trackingID int) oasgenserver.OptString {
	if trackingID == 0 {
		return oasgenserver.OptString{}
	}
	msg := vsaerrors.GetErrorMessageByTrackingID(trackingID)
	if msg == nil || msg.Message == "" || msg.Message == "undefined error" {
		return oasgenserver.OptString{}
	}
	return oasgenserver.NewOptString(formatVerb.ReplaceAllString(msg.Message, "…"))
}

func optStr(s string) oasgenserver.OptString {
	if s == "" {
		return oasgenserver.OptString{}
	}
	return oasgenserver.NewOptString(s)
}

func optStrPtr(s *string) oasgenserver.OptString {
	if s == nil {
		return oasgenserver.OptString{}
	}
	return optStr(*s)
}

func optTime(t time.Time) oasgenserver.OptDateTime {
	if t.IsZero() {
		return oasgenserver.OptDateTime{}
	}
	return oasgenserver.NewOptDateTime(t.UTC())
}

func optTimePtr(t *time.Time) oasgenserver.OptDateTime {
	if t == nil {
		return oasgenserver.OptDateTime{}
	}
	return optTime(*t)
}

func deletedAt(d *gorm.DeletedAt) oasgenserver.NilDateTime {
	var out oasgenserver.NilDateTime
	if d == nil || !d.Valid {
		out.SetToNull()
		return out
	}
	out.SetTo(d.Time.UTC())
	return out
}

func accountName(a *datamodel.Account) oasgenserver.OptString {
	if a == nil {
		return oasgenserver.OptString{}
	}
	return optStr(a.Name)
}

func refUUID[T any](ref *T, uuid func(*T) string) oasgenserver.OptString {
	if ref == nil {
		return oasgenserver.OptString{}
	}
	return optStr(uuid(ref))
}

func ownerID(id int64) *int64 {
	return &id
}

// loadAccounts returns the projects of the given account ids with at most one
// query. known (the request project) is reused without a query.
func loadAccounts(ctx context.Context, store readstore.ReadStore, known *datamodel.Account, ids []int64) (map[int64]*datamodel.Account, error) {
	accounts := map[int64]*datamodel.Account{}
	if known != nil {
		accounts[known.ID] = known
	}
	var missing []int64
	for _, id := range ids {
		if _, ok := accounts[id]; ok {
			continue
		}
		accounts[id] = nil
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return accounts, nil
	}
	found, err := store.GetAccountsByIDs(ctx, missing)
	if err != nil {
		return nil, err
	}
	for id, account := range found {
		accounts[id] = account
	}
	return accounts, nil
}
