package opsinventory

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var poolStates = stateSet(lifecycleStates, resourceEventStates)

type poolReader struct {
	store readstore.ReadStore
}

// NewPoolReader returns the pool reader. It maps the persisted pools row, not
// PoolView: quota, volume count, throughput and IOPS aggregates are not read.
func NewPoolReader(store readstore.ReadStore) Reader {
	return &poolReader{store: store}
}

func (r *poolReader) Spec() Spec {
	return Spec{
		Type:          ResourceTypePool,
		AccountColumn: readstore.ColAccountID,
		GetByName:     true,
		ListParents: map[ParentType]ParentSpec{
			oasgenserver.OpsParentTypeV1Cmek:            {Column: readstore.ColKmsConfigID, Owner: readstore.OwnerKmsConfig, ByID: true, CheckOwner: true},
			oasgenserver.OpsParentTypeV1ActiveDirectory: {Column: readstore.ColActiveDirectoryID, Owner: readstore.OwnerActiveDirectory, ByID: true, CheckOwner: true},
			// Hosts are not project resources: only existence is checked.
			oasgenserver.OpsParentTypeV1Host: {Column: readstore.ColHostUUID, Owner: readstore.OwnerHost},
		},
		NameFilter:  true,
		StateColumn: readstore.ColState,
		States:      poolStates,
		TimeFields:  baseTimeFields,
	}
}

func (r *poolReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	var (
		p   *datamodel.Pool
		err error
	)
	if req.UUID != "" {
		p, err = r.store.GetPoolByUUID(ctx, req.UUID)
	} else {
		p, err = r.store.GetPoolByName(ctx, readstore.NameQuery{Name: req.Name, Eq: req.NameScope})
	}
	if err != nil {
		return Resource{}, err
	}
	return poolResource(p), nil
}

func (r *poolReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListPools(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, poolResource(&rows[i]))
	}
	return out, nil
}

// poolResource never maps PoolCredentials, ExpertModeCredentials,
// ExternalTempCredentials, VLMConfig, ClusterDetails or secret locators.
// Only the auth type and whether a credential exists are returned.
func poolResource(p *datamodel.Pool) Resource {
	details, withheld := stateDetails(p.StateDetails)
	dto := oasgenserver.OpsPoolV1{
		ResourceType:         string(ResourceTypePool),
		Source:               oasgenserver.OpsSourceV1VcpDb,
		UUID:                 p.UUID,
		Name:                 optStr(p.Name),
		Description:          optStr(p.Description),
		State:                optStr(p.State),
		StateDetails:         details,
		StateDetailsWithheld: withheld,
		ProjectNumber:        accountName(p.Account),
		ServiceLevel:         optStr(p.ServiceLevel),
		SizeInBytes:          oasgenserver.NewOptInt64(p.SizeInBytes),
		UsedBytes:            oasgenserver.NewOptInt64(p.UsedBytes),
		Network:              optStr(p.Network),
		AllowAutoTiering:     oasgenserver.NewOptBool(p.AllowAutoTiering),
		QosType:              optStr(p.QosType),
		DeploymentName:       optStr(p.DeploymentName),
		HostUuid:             optStr(p.HostUUID),
		KmsConfigUuid:        refUUID(p.KmsConfig, func(k *datamodel.KmsConfig) string { return k.UUID }),
		ActiveDirectoryUuid:  refUUID(p.ActiveDirectory, func(a *datamodel.ActiveDirectory) string { return a.UUID }),
		ApiAccessMode:        optStr(p.APIAccessMode),
		LargeCapacity:        oasgenserver.NewOptBool(p.LargeCapacity),
		IsPreview:            oasgenserver.NewOptBool(p.IsPreview),
		SatisfiesZi:          oasgenserver.NewOptBool(p.SatisfyZI),
		SatisfiesZs:          oasgenserver.NewOptBool(p.SatisfyZS),
		WorkflowId:           optStr(p.WorkflowID),
		CreatedAt:            p.CreatedAt.UTC(),
		UpdatedAt:            p.UpdatedAt.UTC(),
		DeletedAt:            deletedAt(p.DeletedAt),
	}
	if a := p.PoolAttributes; a != nil {
		dto.PrimaryZone = optStr(a.PrimaryZone)
		dto.SecondaryZone = optStr(a.SecondaryZone)
		dto.IsRegionalHa = oasgenserver.NewOptBool(a.IsRegionalHA)
		dto.IsTrueZonal = oasgenserver.NewOptBool(a.IsTrueZonal)
		dto.ThroughputMibps = oasgenserver.NewOptInt64(a.ThroughputMibps)
		dto.Iops = oasgenserver.NewOptInt64(a.Iops)
	}
	if c := p.PoolCredentials; c != nil {
		dto.AuthType = oasgenserver.NewOptInt32(int32(c.AuthType))
		dto.CredentialConfigured = oasgenserver.NewOptBool(c.SecretID != "" || c.CertificateID != "" || c.Password != "" ||
			c.ExternalSecret != nil || c.ExternalCertificate != nil)
		dto.CertificateRotation = oasgenserver.NewOptOpsPoolCertificateRotationV1(certificateRotation(c))
	} else {
		dto.CredentialConfigured = oasgenserver.NewOptBool(false)
	}
	return Resource{
		ID:             p.ID,
		OwnerAccountID: ownerID(p.AccountID),
		Value:          oasgenserver.NewOpsPoolV1OpsResourceV1(dto),
	}
}

// certificateRotation reports the rotation state of the pool credentials. The
// certificate and secret IDs are secret store locators and are never returned;
// only the time encoded in them is.
func certificateRotation(c *datamodel.PoolCredentials) oasgenserver.OpsPoolCertificateRotationV1 {
	out := oasgenserver.OpsPoolCertificateRotationV1{
		RotationStaged: c.CertificateIDNew != "" || c.SecretIDNew != "" || c.StagedCaURI != "",
		CaUri:          optStr(c.CaURI),
		StagedCaUri:    optStr(c.StagedCaURI),
	}
	if rotatedAt, ok := backgroundactivities.CertificateIDTimestamp(c.CertificateID); ok {
		out.ActiveCertificateRotatedAt = oasgenserver.NewOptDateTime(rotatedAt)
	}
	if createdAt, ok := backgroundactivities.CertificateIDTimestamp(c.CertificateIDNew); ok {
		out.StagedCertificateCreatedAt = oasgenserver.NewOptDateTime(createdAt)
	}
	return out
}
