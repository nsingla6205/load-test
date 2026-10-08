package vcm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestCreateJobOrGet_CreatesNewJob(t *testing.T) {
	store := &jobTestStore{}
	job := &vcmdb.Job{
		ID:         "op-1",
		ResourceID: "hg-1",
		Type:       "CREATE_HOSTGROUP",
		State:      vcmdb.JobStateNew,
		DeadlineAt: time.Now().UTC().Add(time.Hour),
	}

	alreadyExists, err := createJobOrGet(context.Background(), store, job)

	require.NoError(t, err)
	assert.False(t, alreadyExists)
	require.NotNil(t, store.created)
	assert.Equal(t, "op-1", store.created.ID)
}

func TestCreateJobOrGet_ReturnsExistingJob(t *testing.T) {
	existing := &vcmdb.Job{
		ID:    "op-1",
		State: vcmdb.JobStateProcessing,
	}
	store := &jobTestStore{
		createErr:   fmt.Errorf("%w: op-1", vcmdb.ErrJobAlreadyExists),
		existingJob: existing,
	}
	job := &vcmdb.Job{ID: "op-1"}

	alreadyExists, err := createJobOrGet(context.Background(), store, job)

	require.NoError(t, err)
	assert.True(t, alreadyExists)
}

func TestCreateJobOrGet_CreateError(t *testing.T) {
	store := &jobTestStore{createErr: errors.New("database unavailable")}
	job := &vcmdb.Job{ID: "op-1"}

	_, err := createJobOrGet(context.Background(), store, job)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist job")
}

func TestCreateJobOrGet_GetJobError(t *testing.T) {
	store := &jobTestStore{
		createErr: fmt.Errorf("%w: op-1", vcmdb.ErrJobAlreadyExists),
		getJobErr: errors.New("database unavailable"),
	}
	job := &vcmdb.Job{ID: "op-1"}

	_, err := createJobOrGet(context.Background(), store, job)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get existing job")
}

func TestNodeAndSvmFromStore_ProvidedCertPreservesFullCASName(t *testing.T) {
	const casName = "projects/tenant-proj/locations/us-central1/caPools/vcm-pool/certificates/cert-1"
	store := &stubStore{input: vsa.NodeProviderInput{
		DeploymentName: "gcnv-test",
		ServerName:     "mgmt.gcnv-test.vsa.example.com",
		OntapCredentials: &datamodel.PoolCredentials{
			CertificateID:          casName,
			CertPrivateKeySecretID: "projects/tenant-proj/secrets/cert-key",
			SecretID:               "projects/tenant-proj/secrets/admin-pass",
			Username:               "vcm-admin",
			AuthType:               env.USER_CERTIFICATE,
		},
		Nodes: []*datamodel.Node{
			{Name: "vm1", EndpointAddress: "198.18.0.5", HostDNSName: "vm1.gcnv-test.internal"},
		},
	}}

	node, _, err := nodeAndSvmFromStore(store)

	require.NoError(t, err)
	// Full CAS resource name must be preserved so vsa.GetProvidedCertificate can
	// parse the 8-segment name; the admin password resource name is preserved so
	// the resource-name-aware lookup resolves it in the tenant project.
	assert.Equal(t, casName, node.CertificateID)
	assert.Equal(t, "projects/tenant-proj/secrets/cert-key", node.CertPrivateKeySecretID)
	assert.Equal(t, "projects/tenant-proj/secrets/admin-pass", node.SecretID)
	assert.Equal(t, "vcm-admin", node.Username)
	assert.Equal(t, "mgmt.gcnv-test.vsa.example.com", node.ServerName)
}

func TestNodeAndSvmFromStore_SelfManagedCertIsBareNamed(t *testing.T) {
	store := &stubStore{input: vsa.NodeProviderInput{
		DeploymentName: "gcnv-test",
		ServerName:     "mgmt.gcnv-test.vsa.example.com",
		OntapCredentials: &datamodel.PoolCredentials{
			CertificateID: "projects/p/secrets/cert-1",
			SecretID:      "projects/p/secrets/admin-pass",
			AuthType:      env.USER_CERTIFICATE,
		},
		Nodes: []*datamodel.Node{
			{Name: "vm1", EndpointAddress: "198.18.0.5", HostDNSName: "vm1.gcnv-test.internal"},
		},
	}}

	node, _, err := nodeAndSvmFromStore(store)

	require.NoError(t, err)
	// No provided private key -> legacy self-managed path -> bare-name the cert
	// id. The password resource name is preserved for the resource-name-aware
	// lookup.
	assert.Equal(t, "cert-1", node.CertificateID)
	assert.Equal(t, "projects/p/secrets/admin-pass", node.SecretID)
}

type jobTestStore struct {
	created     *vcmdb.Job
	createErr   error
	existingJob *vcmdb.Job
	getJobErr   error
}

func (s *jobTestStore) NodeProviderInput() (vsa.NodeProviderInput, error) {
	return vsa.NodeProviderInput{}, errors.New("not implemented")
}

func (s *jobTestStore) CreateJob(_ context.Context, job *vcmdb.Job) error {
	s.created = job
	return s.createErr
}

func (s *jobTestStore) GetOngoingJobByResource(context.Context, string, string) (*vcmdb.Job, error) {
	return nil, nil
}

func (s *jobTestStore) GetJob(_ context.Context, _ string) (*vcmdb.Job, error) {
	if s.getJobErr != nil {
		return nil, s.getJobErr
	}
	return s.existingJob, nil
}

func (s *jobTestStore) UpdateJobProgress(_ context.Context, _ vcmdb.UpdateJobProgressParams) error {
	return nil
}
