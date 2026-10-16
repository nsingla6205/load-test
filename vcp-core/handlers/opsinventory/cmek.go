package opsinventory

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var cmekStates = stateSet(lifecycleStates, resourceEventStates)

type cmekReader struct {
	store readstore.ReadStore
}

// NewCmekReader returns the KMS config reader. GCP creation fills resource_id,
// not kms_configs.name, so get by name and the name filter are not supported.
func NewCmekReader(store readstore.ReadStore) Reader {
	return &cmekReader{store: store}
}

func (r *cmekReader) Spec() Spec {
	return Spec{
		Type:          ResourceTypeCmek,
		AccountColumn: readstore.ColAccountID,
		StateColumn:   readstore.ColState,
		States:        cmekStates,
		TimeFields:    baseTimeFields,
	}
}

func (r *cmekReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	k, err := r.store.GetKmsConfigByUUID(ctx, req.UUID)
	if err != nil {
		return Resource{}, err
	}
	return cmekResource(k), nil
}

func (r *cmekReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListKmsConfigs(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, cmekResource(&rows[i]))
	}
	return out, nil
}

// cmekResource never maps the KMS grant, SDE health errors, SDE operation URIs
// or any service account key material.
func cmekResource(k *datamodel.KmsConfig) Resource {
	details, withheld := stateDetails(k.StateDetails)
	dto := oasgenserver.OpsCmekV1{
		ResourceType:         string(ResourceTypeCmek),
		Source:               oasgenserver.OpsSourceV1VcpDb,
		UUID:                 k.UUID,
		ResourceId:           optStr(k.ResourceID),
		Description:          optStr(k.Description),
		State:                optStr(k.State),
		StateDetails:         details,
		StateDetailsWithheld: withheld,
		ProjectNumber:        accountName(k.Account),
		KeyRing:              optStr(k.KeyRing),
		KeyRingLocation:      optStr(k.KeyRingLocation),
		KeyName:              optStr(k.KeyName),
		KeyProjectId:         optStr(k.KeyProjectID),
		CustomerProjectId:    optStr(k.CustomerProjectID),
		CreatedAt:            k.CreatedAt.UTC(),
		UpdatedAt:            k.UpdatedAt.UTC(),
		DeletedAt:            deletedAt(k.DeletedAt),
	}
	if a := k.KmsAttributes; a != nil {
		dto.CreationMode = optStr(a.GetCreationMode())
		if a.IsVCPCreated() {
			dto.ServiceAccountEmail = optStr(a.VcpServiceAccountEmail)
		} else {
			dto.ServiceAccountEmail = optStr(a.SdeServiceAccountEmail)
			dto.SdeHealthy = oasgenserver.NewOptBool(a.SdeKmsConfigIsHealthy)
		}
	}
	if sa := k.ServiceAccount; sa != nil {
		dto.ServiceAccount = oasgenserver.NewOptOpsCmekServiceAccountV1(serviceAccountView(sa))
	}
	return Resource{
		ID:             k.ID,
		OwnerAccountID: ownerID(k.AccountID),
		Value:          oasgenserver.NewOpsCmekV1OpsResourceV1(dto),
	}
}

// serviceAccountView maps the linked service account. ServiceAccountPasswordLocation
// and ServiceAccountKey.KeyData hold encrypted key material and are never mapped.
func serviceAccountView(sa *datamodel.ServiceAccount) oasgenserver.OpsCmekServiceAccountV1 {
	details, withheld := stateDetails(sa.StateDetails)
	return oasgenserver.OpsCmekServiceAccountV1{
		UUID:                 sa.UUID,
		Name:                 optStr(sa.Name),
		Email:                optStr(sa.ServiceAccountEmail),
		ServiceName:          optStr(sa.ServiceName),
		State:                optStr(sa.State),
		StateDetails:         details,
		StateDetailsWithheld: withheld,
		KeyRotation:          oasgenserver.NewOptOpsServiceAccountKeyRotationV1(keyRotation(sa)),
		CreatedAt:            sa.CreatedAt.UTC(),
		UpdatedAt:            sa.UpdatedAt.UTC(),
		DeletedAt:            deletedAt(sa.DeletedAt),
	}
}

// keyRotation summarizes service_account_attributes.keys the same way the
// rotation workflow reads it: an active key that is not primary means a
// rotation is in progress; an inactive non-primary key waits for GCP deletion.
func keyRotation(sa *datamodel.ServiceAccount) oasgenserver.OpsServiceAccountKeyRotationV1 {
	out := oasgenserver.OpsServiceAccountKeyRotationV1{}
	if sa.ServiceAccountAttributes == nil {
		return out
	}
	for _, key := range sa.ServiceAccountAttributes.Keys {
		out.Keys = append(out.Keys, oasgenserver.OpsServiceAccountKeyV1{
			KeyId:     key.KeyID,
			IsPrimary: key.IsPrimary,
			IsActive:  key.IsActive,
			CreatedAt: optTime(key.CreatedAt),
		})
		switch {
		case key.IsPrimary:
			out.PrimaryKeyId = optStr(key.KeyID)
			out.PrimaryKeyCreatedAt = optTime(key.CreatedAt)
		case key.IsActive:
			out.RotationInProgress = true
		default:
			out.KeysPendingDeletion++
		}
	}
	return out
}
