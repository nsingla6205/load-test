package opsinventory

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// Backup policies store their state in life_cycle_state.
var backupPolicyStates = stateSet(lifecycleStates, resourceEventStates)

type backupPolicyReader struct {
	store readstore.ReadStore
}

// NewBackupPolicyReader returns the backup policy reader.
func NewBackupPolicyReader(store readstore.ReadStore) Reader {
	return &backupPolicyReader{store: store}
}

func (r *backupPolicyReader) Spec() Spec {
	return Spec{
		Type:          ResourceTypeBackupPolicy,
		AccountColumn: readstore.ColAccountID,
		GetByName:     true,
		NameFilter:    true,
		StateColumn:   readstore.ColLifeCycleState,
		States:        backupPolicyStates,
		TimeFields:    baseTimeFields,
	}
}

func (r *backupPolicyReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	var (
		p   *datamodel.BackupPolicy
		err error
	)
	if req.UUID != "" {
		p, err = r.store.GetBackupPolicyByUUID(ctx, req.UUID)
	} else {
		p, err = r.store.GetBackupPolicyByName(ctx, readstore.NameQuery{Name: req.Name, Eq: req.NameScope})
	}
	if err != nil {
		return Resource{}, err
	}
	return backupPolicyResource(p), nil
}

func (r *backupPolicyReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListBackupPolicies(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, backupPolicyResource(&rows[i]))
	}
	return out, nil
}

func backupPolicyResource(p *datamodel.BackupPolicy) Resource {
	details, withheld := stateDetails(p.LifeCycleStateDetails)
	dto := oasgenserver.OpsBackupPolicyV1{
		ResourceType:         string(ResourceTypeBackupPolicy),
		Source:               oasgenserver.OpsSourceV1VcpDb,
		UUID:                 p.UUID,
		Name:                 optStr(p.Name),
		Description:          optStrPtr(p.Description),
		State:                optStr(p.LifeCycleState),
		StateDetails:         details,
		StateDetailsWithheld: withheld,
		ProjectNumber:        accountName(p.Account),
		DailyBackupsToKeep:   oasgenserver.NewOptInt64(p.DailyBackupsToKeep),
		WeeklyBackupsToKeep:  oasgenserver.NewOptInt64(p.WeeklyBackupsToKeep),
		MonthlyBackupsToKeep: oasgenserver.NewOptInt64(p.MonthlyBackupsToKeep),
		PolicyEnabled:        oasgenserver.NewOptBool(p.PolicyEnabled),
		CreatedAt:            p.CreatedAt.UTC(),
		UpdatedAt:            p.UpdatedAt.UTC(),
		DeletedAt:            deletedAt(p.DeletedAt),
	}
	return Resource{
		ID:             p.ID,
		OwnerAccountID: ownerID(p.AccountID),
		Value:          oasgenserver.NewOpsBackupPolicyV1OpsResourceV1(dto),
	}
}
