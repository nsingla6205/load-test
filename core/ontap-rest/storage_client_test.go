package ontap_rest

import (
	"context"
	"errors"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/storage"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	verrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestRootVolumeGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, err := client.RootVolumeGet(&VolumeGetParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
	})

	t.Run("WhenResponseIsEmpty_ThenReturnNotFoundError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeCollectionGetOK{
			Payload: &models.VolumeResponse{
				VolumeResponseInlineRecords: []*models.Volume{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, err := client.RootVolumeGet(&VolumeGetParams{})
		assert.EqualError(tt, err, "Root volume for SVM not found")
		assert.Nil(tt, response)
	})

	t.Run("WhenSuccessful_ThenReturnRootVolume", func(tt *testing.T) {
		volumeName := "root-volume"
		transport := &mockTransport{response: &storage.VolumeCollectionGetOK{
			Payload: &models.VolumeResponse{
				VolumeResponseInlineRecords: []*models.Volume{
					{Name: &volumeName},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, err := client.RootVolumeGet(&VolumeGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.Equal(tt, volumeName, *response.Name)
	})
}

func TestVolumeDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.VolumeDelete(&VolumeDeleteParams{UUID: "someUUID"})
		assert.Nil(tt, job)
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenUuidAndNameAreEmpty_ThenThrowError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeDeleteCollectionOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.VolumeDelete(&VolumeDeleteParams{})
		assert.Nil(tt, job)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "no name filter provided for VolumeDeleteCollection")
	})

	t.Run("WhenVolumeUUIDIsPassed_ThenSuccessfullyDeleteVolume_Synchronous", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeDeleteOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.VolumeDelete(&VolumeDeleteParams{UUID: "someUUID"})
		assert.Nil(tt, job) // Synchronous deletion, no job returned
		assert.NoError(tt, err)
	})

	t.Run("WhenVolumeUUIDIsPassed_ThenSuccessfullyDeleteVolume_Async", func(tt *testing.T) {
		jobUUID := "job-uuid-123"
		transport := &mockTransport{response: &storage.VolumeDeleteAccepted{
			Payload: &models.VolumeJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.VolumeDelete(&VolumeDeleteParams{UUID: "someUUID"})
		assert.NoError(tt, err)
		assert.NotNil(tt, job)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})

	t.Run("WhenVolumeNameIsPassed_ThenSuccessfullyDeleteVolume_Synchronous", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeDeleteCollectionOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.VolumeDelete(&VolumeDeleteParams{Name: "volumeName"})
		assert.Nil(tt, job) // Synchronous deletion, no job returned
		assert.NoError(tt, err)
	})

	t.Run("WhenVolumeNameIsPassed_ThenSuccessfullyDeleteVolume_Async", func(tt *testing.T) {
		jobUUID := "job-uuid-456"
		transport := &mockTransport{response: &storage.VolumeDeleteCollectionAccepted{
			Payload: &models.VolumeJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.VolumeDelete(&VolumeDeleteParams{Name: "volumeName"})
		assert.NoError(tt, err)
		assert.NotNil(tt, job)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})
}

func TestVolumeUnmount(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.VolumeUnmount(&VolumeUnmountParams{UUID: "someUUID"})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.VolumeModifyAccepted{
			Payload: &models.VolumeJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		accepted, err := client.VolumeUnmount(&VolumeUnmountParams{UUID: "someUUID"})

		assert.NoError(tt, err)
		assert.Equal(tt, accepted.JobUUID, jobUUID)
	})
}

func TestVolumeMount(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.VolumeMount(&VolumeMountParams{UUID: "someUUID", JunctionPath: "/test/path"})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.VolumeModifyAccepted{
			Payload: &models.VolumeJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		accepted, err := client.VolumeMount(&VolumeMountParams{UUID: "someUUID", JunctionPath: "/test/path"})

		assert.NoError(tt, err)
		assert.Equal(tt, accepted.JobUUID, jobUUID)
	})
}

func TestVolumeCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.VolumeCreate(&VolumeCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenResponseHasNoVolumeInfo_ThenReturnUnexpectedResponseError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeCreateCreated{
			Payload: &models.VolumeJobLinkResponse{
				Records: []*models.Volume{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.VolumeCreate(&VolumeCreateParams{})
		assert.EqualError(tt, err, "unexpected response from server while creating volume - received no volume info")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenResponseHasMultipleVolumes_ThenReturnUnexpectedResponseError", func(tt *testing.T) {
		volumeName1 := "volume1"
		volumeName2 := "volume2"
		transport := &mockTransport{response: &storage.VolumeCreateCreated{
			Payload: &models.VolumeJobLinkResponse{
				Records: []*models.Volume{
					{Name: &volumeName1},
					{Name: &volumeName2},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.VolumeCreate(&VolumeCreateParams{})
		assert.EqualError(tt, err, "unexpected response from server while creating volume - did not receive exactly one volume")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenSuccessfulWithCreatedResponse_ThenReturnVolume", func(tt *testing.T) {
		volumeName := "test-volume"
		transport := &mockTransport{response: &storage.VolumeCreateCreated{
			Payload: &models.VolumeJobLinkResponse{
				Records: []*models.Volume{{Name: &volumeName}},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.VolumeCreate(&VolumeCreateParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.Nil(tt, job)
		assert.Equal(tt, volumeName, *response.Name)
	})

	t.Run("WhenSuccessfulWithAcceptedResponse_ThenReturnVolumeAndJob", func(tt *testing.T) {
		volumeName := "test-volume"
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.VolumeCreateAccepted{
			Payload: &models.VolumeJobLinkResponse{
				Records: []*models.Volume{{Name: &volumeName}},
				Job:     &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.VolumeCreate(&VolumeCreateParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.NotNil(tt, job)
		assert.Equal(tt, volumeName, *response.Name)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})

	t.Run("WhenEmptyRecordsInResponse_ThenThrowError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeCreateAccepted{
			Payload: &models.VolumeJobLinkResponse{
				Records: []*models.Volume{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.VolumeCreate(&VolumeCreateParams{})
		assert.EqualError(tt, err, "unexpected response from server while creating volume - received no volume info")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenMoreThanOneRecordsInResponse_ThenThrowError", func(tt *testing.T) {
		volumeName := "test-volume"
		transport := &mockTransport{response: &storage.VolumeCreateAccepted{
			Payload: &models.VolumeJobLinkResponse{
				Records: []*models.Volume{{Name: &volumeName}, {Name: &volumeName}},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.VolumeCreate(&VolumeCreateParams{})
		assert.EqualError(tt, err, "unexpected response from server while creating volume - did not receive exactly one volume")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})
}

func TestAggregateFindByName(t *testing.T) {
	t.Run("WhenAggregateNameIsMissing_ThenReturnError", func(tt *testing.T) {
		storageAPI := storage.New(&mockTransport{}, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.AggregateFindByName(&AggregateCollectionGetParams{})
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Aggregate name missing")
	})

	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.AggregateFindByName(&AggregateCollectionGetParams{Name: nillable.ToPointer("aggregate1")})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenMultipleAggregatesReturned_ThenReturnError", func(tt *testing.T) {
		aggregateName := "aggregate1"
		transport := &mockTransport{response: &storage.AggregateCollectionGetOK{
			Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: &aggregateName},
					{Name: &aggregateName},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.AggregateFindByName(&AggregateCollectionGetParams{Name: nillable.ToPointer(aggregateName)})
		assert.EqualError(tt, err, "More than one Aggregates returned with the name")
	})

	t.Run("WhenNoAggregatesReturned_ThenReturnNotFoundError", func(tt *testing.T) {
		aggregateName := "aggregate1"
		transport := &mockTransport{response: &storage.AggregateCollectionGetOK{
			Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.AggregateFindByName(&AggregateCollectionGetParams{Name: nillable.ToPointer(aggregateName)})
		assert.EqualError(tt, err, "aggregate 'aggregate1' not found")
	})

	t.Run("WhenSingleAggregateReturned_ThenReturnAggregate", func(tt *testing.T) {
		aggregateName := "aggregate1"
		transport := &mockTransport{response: &storage.AggregateCollectionGetOK{
			Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: &aggregateName},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		aggregate, err := client.AggregateFindByName(&AggregateCollectionGetParams{Name: nillable.ToPointer(aggregateName)})
		assert.NoError(tt, err)
		assert.NotNil(tt, aggregate)
		assert.Equal(tt, aggregateName, *aggregate.Name)
	})
}

func TestAggregateCollectionGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.AggregateCollectionGet(&AggregateCollectionGetParams{}, func([]*Aggregate) error { return nil })
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenResponseIsEmpty_ThenReturnNoError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.AggregateCollectionGetOK{
			Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.AggregateCollectionGet(&AggregateCollectionGetParams{}, func([]*Aggregate) error { return nil })
		assert.NoError(tt, err)
	})

	t.Run("WhenResponseHasAggregates_ThenReturnAggregates", func(tt *testing.T) {
		aggregateName := "aggregate1"
		transport := &mockTransport{response: &storage.AggregateCollectionGetOK{
			Payload: &models.AggregateResponse{
				AggregateResponseInlineRecords: []*models.Aggregate{
					{Name: &aggregateName},
				},
				NumRecords: nillable.ToPointer(int64(1)),
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		var aggregates []*Aggregate
		err := client.AggregateCollectionGet(&AggregateCollectionGetParams{}, func(a []*Aggregate) error {
			aggregates = a
			return nil
		})
		assert.NoError(tt, err)
		assert.Len(tt, aggregates, 1)
		assert.Equal(tt, aggregateName, *aggregates[0].Name)
	})
}

func TestAggregateModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, _, err := client.AggregateModify(&AggregateModifyParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenAsyncResponseReturned_ThenReturnJob", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.AggregateModifyAccepted{
			Payload: &models.AggregateSimulate{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, job, err := client.AggregateModify(&AggregateModifyParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, job)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})

	t.Run("WhenSyncResponseReturned_ThenReturnAggregateSimulate", func(tt *testing.T) {
		aggregateName := "aggregate1"
		transport := &mockTransport{response: &storage.AggregateModifyOK{
			Payload: &models.AggregateSimulate{
				AggregateSimulateInlineRecords: []*models.Aggregate{
					{Name: &aggregateName},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		aggregateSimulate, job, err := client.AggregateModify(&AggregateModifyParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, aggregateSimulate)
		assert.Nil(tt, job)
		assert.Equal(tt, aggregateName, *aggregateSimulate.AggregateSimulateInlineRecords[0].Name)
	})
}

func TestSnapshotCollectionGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.SnapshotCollectionGet(&SnapshotCollectionGetParams{}, func([]*Snapshot) error { return nil })
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenResponseIsEmpty_ThenReturnNoError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.SnapshotCollectionGetOK{
			Payload: &models.SnapshotResponse{
				SnapshotResponseInlineRecords: []*models.Snapshot{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.SnapshotCollectionGet(&SnapshotCollectionGetParams{}, func([]*Snapshot) error { return nil })
		assert.NoError(tt, err)
	})

	t.Run("WhenResponseHasSnapshots_ThenReturnSnapshots", func(tt *testing.T) {
		snapshotName := "snapshot1"
		transport := &mockTransport{response: &storage.SnapshotCollectionGetOK{
			Payload: &models.SnapshotResponse{
				SnapshotResponseInlineRecords: []*models.Snapshot{
					{Name: &snapshotName},
				},
				NumRecords: nillable.ToPointer(int64(1)),
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		var snapshots []*Snapshot
		err := client.SnapshotCollectionGet(&SnapshotCollectionGetParams{}, func(s []*Snapshot) error {
			snapshots = s
			return nil
		})
		assert.NoError(tt, err)
		assert.Len(tt, snapshots, 1)
		assert.Equal(tt, snapshotName, *snapshots[0].Name)
	})
}

func TestSnapshotPolicyGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.SnapshotPolicyGet(&SnapshotPolicyGetParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenResponseIsSuccessful_ThenReturnSnapshotPolicy", func(tt *testing.T) {
		policyName := "policy1"
		transport := &mockTransport{response: &storage.SnapshotPolicyGetOK{
			Payload: &models.SnapshotPolicy{
				Name: &policyName,
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		policy, err := client.SnapshotPolicyGet(&SnapshotPolicyGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, policy)
		assert.Equal(tt, policyName, *policy.Name)
	})
}

func TestQosPolicyGroupCollectionGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.QosPolicyGroupCollectionGet(&QosPolicyGroupCollectionGetParams{}, func([]*QosPolicy) error { return nil })
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenResponseIsEmpty_ThenReturnNoError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.QosPolicyCollectionGetOK{
			Payload: &models.QosPolicyResponse{
				QosPolicyResponseInlineRecords: []*models.QosPolicy{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.QosPolicyGroupCollectionGet(&QosPolicyGroupCollectionGetParams{}, func([]*QosPolicy) error { return nil })
		assert.NoError(tt, err)
	})

	t.Run("WhenResponseHasQosPolicies_ThenReturnQosPolicies", func(tt *testing.T) {
		policyName := "policy1"
		transport := &mockTransport{response: &storage.QosPolicyCollectionGetOK{
			Payload: &models.QosPolicyResponse{
				QosPolicyResponseInlineRecords: []*models.QosPolicy{
					{Name: &policyName},
				},
				NumRecords: nillable.ToPointer(int64(1)),
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		var qosPolicies []*QosPolicy
		err := client.QosPolicyGroupCollectionGet(&QosPolicyGroupCollectionGetParams{}, func(q []*QosPolicy) error {
			qosPolicies = q
			return nil
		})
		assert.NoError(tt, err)
		assert.Len(tt, qosPolicies, 1)
		assert.Equal(tt, policyName, *qosPolicies[0].Name)
	})
}

func TestQosPolicyGroupCollectionModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, _, err := client.QosPolicyGroupCollectionModify([]*QosPolicyGroupModifyCollectionParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenAsyncResponseReturned_ThenReturnJob", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.QosPolicyModifyCollectionAccepted{
			Payload: &models.QosPolicyJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, job, err := client.QosPolicyGroupCollectionModify([]*QosPolicyGroupModifyCollectionParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, job)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})

	t.Run("WhenSyncResponseReturned_ThenReturnQosPolicyModifyCollection", func(tt *testing.T) {
		policyName := "policy1"
		transport := &mockTransport{response: &storage.QosPolicyModifyCollectionOK{
			Payload: &models.QosPolicyJobLinkResponse{
				Records: []*models.QosPolicy{
					{Name: &policyName},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		qosPolicyModifyCollection, job, err := client.QosPolicyGroupCollectionModify([]*QosPolicyGroupModifyCollectionParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, qosPolicyModifyCollection)
		assert.Nil(tt, job)
		assert.Len(tt, qosPolicyModifyCollection.Records, 1)
		assert.Equal(tt, policyName, *qosPolicyModifyCollection.Records[0].Name)
	})
}

func TestQoSPolicyGroupFind(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QoSPolicyGroupFind(&QoSPolicyGroupFindParams{Name: "sample-policy", SvmName: "test-svm"})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenNoNameProvided_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QoSPolicyGroupFind(&QoSPolicyGroupFindParams{Name: ""})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "either UUID or Name must be provided")
	})

	t.Run("WhenNotFound_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.QosPolicyCollectionGetOK{
			Payload: &models.QosPolicyResponse{
				QosPolicyResponseInlineRecords: []*models.QosPolicy{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QoSPolicyGroupFind(&QoSPolicyGroupFindParams{Name: "test-policy", SvmName: "test-svm"})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "not found")
	})

	t.Run("WhenMultipleFound_ThenReturnError", func(tt *testing.T) {
		policyName := "test-policy"
		transport := &mockTransport{response: &storage.QosPolicyCollectionGetOK{
			Payload: &models.QosPolicyResponse{
				QosPolicyResponseInlineRecords: []*models.QosPolicy{
					{Name: &policyName},
					{Name: &policyName},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QoSPolicyGroupFind(&QoSPolicyGroupFindParams{Name: "test-policy", SvmName: "test-svm"})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "multiple QoS policies found")
	})

	t.Run("WhenSuccessful_ThenReturnQoSPolicy", func(tt *testing.T) {
		policyName := "test-policy"
		policyUUID := "test-uuid"
		svmName := "test-svm"
		maxThroughput := int64(100)
		maxIOPS := int64(1000)
		transport := &mockTransport{response: &storage.QosPolicyCollectionGetOK{
			Payload: &models.QosPolicyResponse{
				QosPolicyResponseInlineRecords: []*models.QosPolicy{
					{
						Name: &policyName,
						UUID: &policyUUID,
						Svm: &models.QosPolicyInlineSvm{
							Name: &svmName,
						},
						Fixed: &models.QosPolicyInlineFixed{
							MaxThroughputMbps: &maxThroughput,
							MaxThroughputIops: &maxIOPS,
						},
					},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		result, err := client.QoSPolicyGroupFind(&QoSPolicyGroupFindParams{
			Name:    "test-policy",
			SvmName: "test-svm",
		})
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, policyName, *result.Name)
		assert.Equal(tt, policyUUID, *result.UUID)
		assert.Equal(tt, svmName, *result.Svm.Name)
		assert.Equal(tt, maxThroughput, *result.Fixed.MaxThroughputMbps)
		assert.Equal(tt, maxIOPS, *result.Fixed.MaxThroughputIops)
	})

	t.Run("WhenRESTCallFails_WithoutSvmName_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QoSPolicyGroupFind(&QoSPolicyGroupFindParams{Name: "sample-policy"})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenNotFound_WithoutSvmName_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.QosPolicyCollectionGetOK{
			Payload: &models.QosPolicyResponse{
				QosPolicyResponseInlineRecords: []*models.QosPolicy{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QoSPolicyGroupFind(&QoSPolicyGroupFindParams{Name: "test-policy"})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "not found")
	})

	t.Run("WhenSuccessful_WithName_WithoutSvmName_ThenReturnQoSPolicy", func(tt *testing.T) {
		policyName := "test-policy"
		policyUUID := "test-uuid"
		svmName := "test-svm"
		maxThroughput := int64(100)
		maxIOPS := int64(1000)
		transport := &mockTransport{response: &storage.QosPolicyCollectionGetOK{
			Payload: &models.QosPolicyResponse{
				QosPolicyResponseInlineRecords: []*models.QosPolicy{
					{
						Name: &policyName,
						UUID: &policyUUID,
						Svm: &models.QosPolicyInlineSvm{
							Name: &svmName,
						},
						Fixed: &models.QosPolicyInlineFixed{
							MaxThroughputMbps: &maxThroughput,
							MaxThroughputIops: &maxIOPS,
						},
					},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		result, err := client.QoSPolicyGroupFind(&QoSPolicyGroupFindParams{
			Name: "test-policy",
		})
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, policyName, *result.Name)
		assert.Equal(tt, policyUUID, *result.UUID)
	})
}

func TestQoSPolicyGroupUpdate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QoSPolicyGroupUpdate(&QoSPolicyGroupUpdateParams{UUID: "some-uuid"})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenNoUUIDProvided_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QoSPolicyGroupUpdate(&QoSPolicyGroupUpdateParams{UUID: ""})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "no UUID provided")
	})

	t.Run("WhenSuccessful_ThenReturnJob", func(tt *testing.T) {
		jobUUID := strfmt.UUID("123e4567-e89b-12d3-a456-426614174000")
		transport := &mockTransport{response: &storage.QosPolicyModifyCollectionAccepted{
			Payload: &models.QosPolicyJobLinkResponse{
				Job: &models.JobLink{
					UUID: &jobUUID,
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		result, err := client.QoSPolicyGroupUpdate(&QoSPolicyGroupUpdateParams{
			UUID:          "test-uuid",
			Name:          "test-policy",
			SvmName:       "test-svm",
			MaxThroughput: 200,
			MaxIOPS:       2000,
		})
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, jobUUID, strfmt.UUID(result.JobUUID))
	})
}

func TestCloudStoreCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.CloudStoreCreate(&storage.CloudStoreCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, job)
	})

	t.Run("WhenAsyncResponseReturned_ThenReturnJob", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.CloudStoreCreateAccepted{
			Payload: &models.CloudStoreJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.CloudStoreCreate(&storage.CloudStoreCreateParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, job)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})

	t.Run("WhenNoResponseReturned_ThenReturnNil", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.CloudStoreCreateAccepted{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.CloudStoreCreate(&storage.CloudStoreCreateParams{})
		assert.EqualError(tt, err, "unexpected response from CloudStoreCreate")
		assert.Nil(tt, job)
	})
}

func TestVolumeGet(t *testing.T) {
	t.Run("WhenUUIDIsProvided_ThenReturnVolume", func(tt *testing.T) {
		volumeName := "test-volume"
		transport := &mockTransport{response: &storage.VolumeGetOK{
			Payload: &models.Volume{Name: &volumeName},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		volume, err := client.VolumeGet(&VolumeGetParams{UUID: "someUUID"})
		assert.NoError(tt, err)
		assert.NotNil(tt, volume)
		assert.Equal(tt, volumeName, *volume.Name)
	})

	t.Run("WhenUUIDAndNameAreEmpty_ThenReturnError", func(tt *testing.T) {
		storageAPI := storage.New(&mockTransport{}, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.VolumeGet(&VolumeGetParams{})
		assert.EqualError(tt, err, "UUID and Name parameters cannot be empty when querying for a volume")
	})

	t.Run("WhenVolumeNotFound_ThenReturnNotFoundError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeCollectionGetOK{
			Payload: &models.VolumeResponse{
				VolumeResponseInlineRecords: []*models.Volume{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.VolumeGet(&VolumeGetParams{Name: "nonexistent-volume"})
		assert.EqualError(tt, err, "volume 'nonexistent-volume' not found")
	})

	t.Run("WhenVolumeFound_ThenReturnVolume", func(tt *testing.T) {
		volumeName := "test-volume"
		transport := &mockTransport{response: &storage.VolumeCollectionGetOK{
			Payload: &models.VolumeResponse{
				VolumeResponseInlineRecords: []*models.Volume{
					{Name: &volumeName},
				},
			},
		}}
		originalFetchVolumeDetails := FetchVolumeDetails
		defer func() { FetchVolumeDetails = originalFetchVolumeDetails }()
		// Mock implementation
		FetchVolumeDetails = func(sc *storageClient, volume *Volume) (*Volume, error) {
			return &Volume{Volume: models.Volume{Name: nillable.ToPointer("test-volume")}}, nil
		}

		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		volume, err := client.VolumeGet(&VolumeGetParams{Name: "test-volume"})
		assert.NoError(tt, err)
		assert.NotNil(tt, volume)
		assert.Equal(tt, volumeName, *volume.Name)
	})

	t.Run("WhenVolumeFound_ThenReturnVolume_GetVolume_Error", func(tt *testing.T) {
		volumeName := "test-volume"
		transport := &mockTransport{response: &storage.VolumeCollectionGetOK{
			Payload: &models.VolumeResponse{
				VolumeResponseInlineRecords: []*models.Volume{
					{Name: &volumeName},
				},
			},
		}}
		originalFetchVolumeDetails := FetchVolumeDetails
		defer func() { FetchVolumeDetails = originalFetchVolumeDetails }()
		// Mock implementation
		FetchVolumeDetails = func(sc *storageClient, volume *Volume) (*Volume, error) {
			return nil, errors.New("connection error")
		}

		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		volume, err := client.VolumeGet(&VolumeGetParams{Name: "test-volume"})
		assert.Error(tt, err)
		assert.Nil(tt, volume)
	})
}

func TestFetchVolumeDetails(t *testing.T) {
	t.Run("WhenVolumeDetailsFetchFails_ThenReturnError", func(tt *testing.T) {
		client := &storageClient{api: storage.New(&mockTransport{err: errors.New("fetch error")}, nil)}
		volume := &Volume{
			Volume: models.Volume{Name: nillable.ToPointer("test-volume"), UUID: nillable.GetStringPtr("test-uuid")},
		}
		_, err := FetchVolumeDetails(client, volume)
		assert.EqualError(tt, err, "fetch error")
	})

	t.Run("WhenVolumeDetailsFetchSucceeds_ThenReturnVolume", func(tt *testing.T) {
		client := &storageClient{api: storage.New(&mockTransport{
			response: &storage.VolumeGetOK{
				Payload: &models.Volume{Name: nillable.ToPointer("test-volume"), UUID: nillable.GetStringPtr("test-uuid")},
			},
		}, nil)}
		volume := &Volume{Volume: models.Volume{Name: nillable.ToPointer("test-volume"), UUID: nillable.GetStringPtr("test-uuid")}}
		result, err := FetchVolumeDetails(client, volume)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-volume", *result.Name)
	})

	t.Run("WhenVolumeDetailsFetchReturnsNil_ThenReturnError", func(tt *testing.T) {
		client := &storageClient{api: storage.New(&mockTransport{
			response: &storage.VolumeGetOK{
				Payload: nil, // Simulating no payload
			},
		}, nil)}
		volume := &Volume{Volume: models.Volume{Name: nillable.ToPointer("test-volume"), UUID: nillable.GetStringPtr("test-uuid")}}
		result, err := FetchVolumeDetails(client, volume)
		assert.Nil(tt, result)
		assert.EqualError(tt, err, "unexpected response from VolumeGet")
	})
}

func TestVolumeCollectionGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.VolumeCollectionGet(&VolumeCollectionGetParams{}, func([]*Volume) error { return nil })
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenResponseIsEmpty_ThenReturnNoError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeCollectionGetOK{
			Payload: &models.VolumeResponse{
				VolumeResponseInlineRecords: []*models.Volume{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.VolumeCollectionGet(&VolumeCollectionGetParams{}, func([]*Volume) error { return nil })
		assert.NoError(tt, err)
	})

	t.Run("WhenResponseHasVolumes_ThenReturnVolumes", func(tt *testing.T) {
		volumeName := "volume1"
		transport := &mockTransport{response: &storage.VolumeCollectionGetOK{
			Payload: &models.VolumeResponse{
				VolumeResponseInlineRecords: []*models.Volume{
					{Name: &volumeName},
				},
				NumRecords: nillable.ToPointer(int64(1)),
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		var volumes []*Volume
		err := client.VolumeCollectionGet(&VolumeCollectionGetParams{}, func(v []*Volume) error {
			volumes = v
			return nil
		})
		assert.NoError(tt, err)
		assert.Len(tt, volumes, 1)
		assert.Equal(tt, volumeName, *volumes[0].Name)
	})
}

func TestVolumeModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		success, job, err := client.VolumeModify(&VolumeModifyParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.False(tt, success)
		assert.Nil(tt, job)
	})

	t.Run("WhenSyncResponseReturned_ThenReturnSuccess", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeModifyOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		success, job, err := client.VolumeModify(&VolumeModifyParams{})
		assert.NoError(tt, err)
		assert.True(tt, success)
		assert.Nil(tt, job)
	})

	t.Run("WhenAsyncResponseReturned_ThenReturnJob", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.VolumeModifyAccepted{
			Payload: &models.VolumeJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		success, job, err := client.VolumeModify(&VolumeModifyParams{})
		assert.NoError(tt, err)
		assert.False(tt, success)
		assert.NotNil(tt, job)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})
}

func TestVolumeModifyCloudWriteMode(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		falseVal := false
		params := &VolumeModifyParams{
			UUID: "test-uuid",
			TieringPolicy: &TieringPolicy{
				CloudWriteModeEnabled: &falseVal,
			},
		}
		success, job, err := client.VolumeModifyCloudWriteMode(params)
		assert.EqualError(tt, err, transport.err.Error())
		assert.False(tt, success)
		assert.Nil(tt, job)
	})

	t.Run("WhenSyncResponseReturned_ThenReturnSuccess", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.VolumeModifyOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		falseVal := false
		params := &VolumeModifyParams{
			UUID: "test-uuid",
			TieringPolicy: &TieringPolicy{
				CloudWriteModeEnabled: &falseVal,
			},
		}
		success, job, err := client.VolumeModifyCloudWriteMode(params)
		assert.NoError(tt, err)
		assert.True(tt, success)
		assert.Nil(tt, job)
	})

	t.Run("WhenAsyncResponseReturned_ThenReturnJob", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.VolumeModifyAccepted{
			Payload: &models.VolumeJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		falseVal := false
		params := &VolumeModifyParams{
			UUID: "test-uuid",
			TieringPolicy: &TieringPolicy{
				CloudWriteModeEnabled: &falseVal,
			},
		}
		success, job, err := client.VolumeModifyCloudWriteMode(params)
		assert.NoError(tt, err)
		assert.True(tt, success)
		assert.NotNil(tt, job)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})
}

func TestSnapshotCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenResponseHasNoSnapshotInfo_ThenReturnUnexpectedResponseError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.SnapshotCreateCreated{
			Payload: &models.SnapshotJobLinkResponse{
				Records: []*models.Snapshot{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.EqualError(tt, err, "SnapshotCreate invalid created response from storage server - Expected a single record but got: '0'")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenResponseHasMultipleSnapshots_ThenReturnUnexpectedResponseError", func(tt *testing.T) {
		snapshotName1 := "snapshot1"
		snapshotName2 := "snapshot2"
		transport := &mockTransport{response: &storage.SnapshotCreateCreated{
			Payload: &models.SnapshotJobLinkResponse{
				Records: []*models.Snapshot{
					{Name: &snapshotName1},
					{Name: &snapshotName2},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.EqualError(tt, err, "SnapshotCreate invalid created response from storage server - Expected a single record but got: '2'")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenSuccessfulWithCreatedResponse_ThenReturnSnapshot", func(tt *testing.T) {
		snapshotName := "test-snapshot"
		transport := &mockTransport{response: &storage.SnapshotCreateCreated{
			Payload: &models.SnapshotJobLinkResponse{
				Records: []*models.Snapshot{{Name: &snapshotName}},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.Nil(tt, job)
		assert.Equal(tt, snapshotName, *response.Name)
	})

	t.Run("WhenSuccessfulWithAcceptedResponse_ThenReturnSnapshotAndJob", func(tt *testing.T) {
		snapshotName := "test-snapshot"
		UUID := "uuid"
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.SnapshotCreateAccepted{
			Payload: &models.SnapshotJobLinkResponse{
				Records: []*models.Snapshot{{Name: &snapshotName, UUID: &UUID}},
				Job:     &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.NotNil(tt, job)
		assert.Equal(tt, snapshotName, *response.Name)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})

	t.Run("WhenEmptyRecordsInResponse_ThenThrowError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.SnapshotCreateAccepted{
			Payload: &models.SnapshotJobLinkResponse{
				Records: []*models.Snapshot{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.ErrorContains(tt, err, "SnapshotCreate invalid accepted response from storage server - Expected a single record but got: '0'")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenMoreThanOneRecordsInResponse_ThenThrowError", func(tt *testing.T) {
		snapshotName := "test-snapshot"
		transport := &mockTransport{response: &storage.SnapshotCreateAccepted{
			Payload: &models.SnapshotJobLinkResponse{
				Records: []*models.Snapshot{{Name: &snapshotName}, {Name: &snapshotName}},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.EqualError(tt, err, "SnapshotCreate invalid accepted response from storage server - Expected a single record but got: '2'")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenConflictErrorReturned_ThenReturnConflictError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("snapshot with that name already exists")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.EqualError(tt, err, "snapshot with that name already exists")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenONTAPRWVolumeErrorReturned_ThenReturnBadRequestError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("Snapshots can only be created on read/write (RW) volumes")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.EqualError(tt, err, "snapshot creation operation not allowed for this volume")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenInsufficientSpaceErrorReturned_ThenReturnRawError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("Snapshot operation failed: No space left on device. Additional space required: 268KB.")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.Error(tt, err)
		assert.Nil(tt, response)
		assert.Nil(tt, job)
		// storage_client.go returns raw errors; wrapping happens in vsa/snapshot.go
		assert.Contains(tt, err.Error(), "No space left on device")
	})

	t.Run("WhenMaximumSnapshotLimitExceededErrorReturned_ThenReturnRawError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("Cannot exceed maximum number of snapshots.")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, job, err := client.SnapshotCreate(&SnapshotCreateParams{})
		assert.Error(tt, err)
		assert.Nil(tt, response)
		assert.Nil(tt, job)
		// storage_client.go returns raw errors; wrapping happens in vsa/snapshot.go
		assert.Contains(tt, err.Error(), "Cannot exceed maximum number of snapshots")
	})
}

func TestSnapshotGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		snapshot, err := client.SnapshotGet(&SnapshotGetParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, snapshot)
	})

	t.Run("WhenResponseIsSuccessful_ThenReturnSnapshot", func(tt *testing.T) {
		snapshotName := "snapshot1"
		transport := &mockTransport{response: &storage.SnapshotGetOK{
			Payload: &models.Snapshot{
				Name: &snapshotName,
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		snapshot, err := client.SnapshotGet(&SnapshotGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, snapshot)
		assert.Equal(tt, snapshotName, *snapshot.Name)
	})
}

func TestSnapshotGetParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsIsNil_ThenReturnNil", func(tt *testing.T) {
		result := snapshotGetParamsToONTAP(nil)
		assert.Nil(tt, result)
	})

	t.Run("WhenParamsIsNotNil_ThenFieldsAreMapped", func(tt *testing.T) {
		uuid := "snap-uuid"
		volumeUUID := "vol-uuid"
		params := &SnapshotGetParams{
			UUID:       uuid,
			VolumeUUID: volumeUUID,
		}
		result := snapshotGetParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.Equal(tt, uuid, result.UUID)
		assert.Equal(tt, volumeUUID, result.VolumeUUID)
	})
}

func TestSnapshotDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, jj, err := client.SnapshotDelete(&SnapshotDeleteParams{UUID: "someUUID"})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, jj)
	})

	t.Run("WhenUuidIsEmpty_ThenThrowError", func(tt *testing.T) {
		transport := &mockTransport{}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, jj, err := client.SnapshotDelete(&SnapshotDeleteParams{})
		assert.Nil(tt, jj)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "no UUID provided for SnapshotDelete")
	})

	t.Run("WhenSnapshotUUIDIsPassed_ThenSuccessfullyDeleteSnapshot", func(tt *testing.T) {
		snapshotName := "test-snapshot"
		UUID := "uuid"
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.SnapshotDeleteAccepted{
			Payload: &models.SnapshotJobLinkResponse{
				Records: []*models.Snapshot{{Name: &snapshotName, UUID: &UUID}},
				Job:     &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, job, err := client.SnapshotDelete(&SnapshotDeleteParams{UUID: "someUUID"})
		assert.NoError(tt, err)
		assert.NotNil(tt, job)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})
}

func TestDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsIsNotNil_ThenFieldsAreMapped", func(tt *testing.T) {
		uuid := "snap-uuid"
		volumeUUID := "vol-uuid"
		params := &SnapshotDeleteParams{
			UUID:       uuid,
			VolumeUUID: volumeUUID,
		}
		result := snapshotDeleteParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.Equal(tt, uuid, result.UUID)
		assert.Equal(tt, volumeUUID, result.VolumeUUID)
	})
}

func TestSnapshotPolicyCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		mockClient := &MockStorageClient{}
		mockClient.On("SnapshotPolicyCreate", mock.Anything).Return(errors.New("api error"))
		err := mockClient.SnapshotPolicyCreate(&SnapshotPolicyCreateParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenRESTCallSucceeds_ThenReturnNil", func(tt *testing.T) {
		mockClient := &MockStorageClient{}
		mockClient.On("SnapshotPolicyCreate", mock.Anything).Return(nil)
		err := mockClient.SnapshotPolicyCreate(&SnapshotPolicyCreateParams{})
		assert.NoError(tt, err)
	})
}

func TestSnapshotPolicyFind(t *testing.T) {
	t.Run("WhenNameIsEmpty_ThenReturnError", func(tt *testing.T) {
		client := &storageClient{api: storage.New(&mockTransport{}, nil)}
		_, err := client.SnapshotPolicyFind(&SnapshotPolicyFindParams{})
		assert.Error(tt, err)
		assert.EqualError(tt, err, "no name filter provided for SnapshotPolicyFind")
	})

	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.SnapshotPolicyFind(&SnapshotPolicyFindParams{Name: "policy1"})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenNoPoliciesReturned_ThenReturnNotFoundError", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.SnapshotPolicyCollectionGetOK{
			Payload: &models.SnapshotPolicyResponse{
				SnapshotPolicyResponseInlineRecords: []*models.SnapshotPolicy{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.SnapshotPolicyFind(&SnapshotPolicyFindParams{Name: "policy1"})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "not found")
	})

	t.Run("WhenSinglePolicyReturned_ThenReturnPolicy", func(tt *testing.T) {
		policyName := "policy1"
		transport := &mockTransport{response: &storage.SnapshotPolicyCollectionGetOK{
			Payload: &models.SnapshotPolicyResponse{
				SnapshotPolicyResponseInlineRecords: []*models.SnapshotPolicy{
					{Name: &policyName},
				},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		policy, err := client.SnapshotPolicyFind(&SnapshotPolicyFindParams{Name: policyName})
		assert.NoError(tt, err)
		assert.NotNil(tt, policy)
		assert.Equal(tt, policyName, *policy.Name)
	})
}

func TestSnapshotPolicyDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		mockClient := &MockStorageClient{}
		mockClient.On("SnapshotPolicyDelete", mock.Anything).Return(errors.New("api error"))
		err := mockClient.SnapshotPolicyDelete(&SnapshotPolicyDeleteParams{})
		assert.EqualError(tt, err, "api error")
	})

	t.Run("WhenRESTCallSucceeds_ThenReturnNil", func(tt *testing.T) {
		mockClient := &MockStorageClient{}
		mockClient.On("SnapshotPolicyDelete", mock.Anything).Return(nil)
		err := mockClient.SnapshotPolicyDelete(&SnapshotPolicyDeleteParams{})
		assert.NoError(tt, err)
	})
}

func TestSnapshotPolicyModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.SnapshotPolicyModify(&SnapshotPolicyModifyParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenRESTCallSucceeds_ThenReturnNil", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.SnapshotPolicyModifyOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.SnapshotPolicyModify(&SnapshotPolicyModifyParams{})
		assert.NoError(tt, err)
	})
}

func TestSnapshotPolicyDeleteParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsIsEmpty", func(tt *testing.T) {
		otParams := snapshotPolicyDeleteParamsToONTAPCollectionDelete(&SnapshotPolicyDeleteParams{})
		assert.NotNil(tt, otParams)
	})
	t.Run("WhenParamsIsSet", func(tt *testing.T) {
		params := &SnapshotPolicyDeleteParams{
			Name: "snap-policy-1",
		}
		otParams := snapshotPolicyDeleteParamsToONTAPCollectionDelete(params)
		assert.Equal(tt, params.Name, *otParams.Name)
	})
}

func TestSnapshotPolicyScheduleCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.SnapshotPolicyScheduleCreate(&SnapshotPolicyScheduleCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenRESTCallSucceeds_ThenReturnID", func(tt *testing.T) {
		location := "/api/storage/snapshot-policies/123/schedules/456"
		transport := &mockTransport{response: &storage.SnapshotPolicyScheduleCreateCreated{
			Location: location,
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		id, err := client.SnapshotPolicyScheduleCreate(&SnapshotPolicyScheduleCreateParams{})
		assert.NoError(tt, err)
		assert.Equal(tt, "456", id)
	})
}

func TestSnapshotPolicyScheduleModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.SnapshotPolicyScheduleModify(&SnapshotPolicyScheduleModifyParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenRESTCallSucceeds_ThenReturnNil", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.SnapshotPolicyScheduleModifyOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.SnapshotPolicyScheduleModify(&SnapshotPolicyScheduleModifyParams{})
		assert.NoError(tt, err)
	})
}

func TestSnapshotPolicyScheduleDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.SnapshotPolicyScheduleDelete(&SnapshotPolicyScheduleDeleteParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenRESTCallSucceeds_ThenReturnNil", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.SnapshotPolicyScheduleDeleteOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		err := client.SnapshotPolicyScheduleDelete(&SnapshotPolicyScheduleDeleteParams{})
		assert.NoError(tt, err)
	})
}

func TestSnapshotPolicyCreateParamsToONTAP(t *testing.T) {
	t.Run("NilParams_ReturnsDefaultParams", func(tt *testing.T) {
		result := snapshotPolicyCreateParamsToONTAP(nil)
		assert.NotNil(tt, result)
		assert.Nil(tt, result.Info)
	})

	t.Run("WithSchedules_MapsFieldsCorrectly", func(tt *testing.T) {
		name := "policy1"
		comment := "test policy"
		enabled := true
		scheduleName := "sched1"
		prefix := "prefix1"
		snapmirrorLabel := "label1"
		count := int64(5)
		params := &SnapshotPolicyCreateParams{
			Name:    &name,
			Comment: &comment,
			Enabled: &enabled,
			Schedules: []*SnapshotPolicySchedule{
				{
					Name:            scheduleName,
					Prefix:          prefix,
					SnapmirrorLabel: snapmirrorLabel,
					Count:           count,
				},
			},
		}
		result := snapshotPolicyCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Equal(tt, &name, result.Info.Name)
		assert.Equal(tt, &comment, result.Info.Comment)
		assert.Equal(tt, &enabled, result.Info.Enabled)
		assert.Len(tt, result.Info.SnapshotPolicyInlineCopies, 1)
		sched := result.Info.SnapshotPolicyInlineCopies[0]
		assert.Equal(tt, &count, sched.Count)
		assert.Equal(tt, &prefix, sched.Prefix)
		assert.Equal(tt, &snapmirrorLabel, sched.SnapmirrorLabel)
		assert.NotNil(tt, sched.Schedule)
		assert.Equal(tt, &scheduleName, sched.Schedule.Name)
	})

	t.Run("EmptySchedules_MapsToEmptyArray", func(tt *testing.T) {
		name := "policy2"
		params := &SnapshotPolicyCreateParams{
			Name:      &name,
			Schedules: []*SnapshotPolicySchedule{},
		}
		result := snapshotPolicyCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Equal(tt, &name, result.Info.Name)
		assert.NotNil(tt, result.Info.SnapshotPolicyInlineCopies)
		assert.Len(tt, result.Info.SnapshotPolicyInlineCopies, 0)
	})
}

func TestQoSPolicyGroupCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, _, err := client.QoSPolicyGroupCreate(&QoSPolicyGroupCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenRESTCallSucceeds_ThenReturnResponse", func(tt *testing.T) {
		// Mock response data
		qosPolicyName := "test-policy"
		transport := &mockTransport{response: &storage.QosPolicyCreateCreated{
			Location: "some-location",
			Payload: &models.QosPolicyJobLinkResponse{
				NumRecords: 1,
				Records: []*models.QosPolicy{
					{
						Name: &qosPolicyName,
					},
				},
			},
		}}

		// Create the storage API and client
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}

		// Call the method
		response, job, err := client.QoSPolicyGroupCreate(&QoSPolicyGroupCreateParams{})

		// Assertions
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.Nil(tt, job)
		assert.Equal(tt, qosPolicyName, *response.Name)
	})

	t.Run("WhenAcceptedResponseReturned_ThenReturnJob", func(tt *testing.T) {
		// Mock response data
		uuid := strfmt.UUID("123e4567-e89b-12d3-a456-426614174000")
		transport := &mockTransport{response: &storage.QosPolicyCreateAccepted{
			Payload: &models.QosPolicyJobLinkResponse{
				Job: &models.JobLink{
					UUID: &uuid,
				},
			},
		}}

		// Create the storage API and client
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}

		// Call the method
		response, job, err := client.QoSPolicyGroupCreate(&QoSPolicyGroupCreateParams{})

		// Assertions
		assert.NoError(tt, err)
		assert.Nil(tt, response)
		assert.NotNil(tt, job)
		assert.Equal(tt, uuid, strfmt.UUID(job.JobUUID))
	})
	t.Run("WhenAcceptedResponseReturned_WithError", func(tt *testing.T) {
		// Mock response data
		transport := &mockTransport{response: &storage.QosPolicyCreateAccepted{
			Payload: &models.QosPolicyJobLinkResponse{},
		}}

		// Create the storage API and client
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}

		// Call the method
		response, job, err := client.QoSPolicyGroupCreate(&QoSPolicyGroupCreateParams{})

		// Assertions
		assert.NotNil(tt, err)
		assert.EqualError(tt, err, "unexpected response from server while creating QoS policy - received no QoS info")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})
}

func TestQoSPolicyGroupCreateParamsToONTAP(t *testing.T) {
	t.Run("WhenParamsIsNil_ThenReturnDefaultParams", func(tt *testing.T) {
		result := qosPolicyGroupCreateParamsToONTAP(nil)
		assert.NotNil(tt, result)
		assert.Nil(tt, result.Info)
	})

	t.Run("WhenParamsIsSet_ThenFieldsAreMappedCorrectly", func(tt *testing.T) {
		name := "test-policy"
		svmName := "test-svm"
		maxThroughput := int64(1000)
		maxIOPS := int64(5000)
		params := &QoSPolicyGroupCreateParams{
			Name:          name,
			SvmName:       svmName,
			MaxThroughput: maxThroughput,
			MaxIOPS:       maxIOPS,
		}
		result := qosPolicyGroupCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Equal(tt, &name, result.Info.Name)
		assert.NotNil(tt, result.Info.Svm)
		assert.Equal(tt, &svmName, result.Info.Svm.Name)
		assert.NotNil(tt, result.Info.Fixed)
		assert.Equal(tt, &maxThroughput, result.Info.Fixed.MaxThroughputMbps)
		assert.Equal(tt, &maxIOPS, result.Info.Fixed.MaxThroughputIops)
		assert.Nil(tt, result.Info.Fixed.CapacityShared)
	})

	t.Run("WhenParamsHasZeroValues_ThenFieldsAreMappedCorrectly", func(tt *testing.T) {
		name := "zero-policy"
		svmName := "zero-svm"
		params := &QoSPolicyGroupCreateParams{
			Name:          name,
			SvmName:       svmName,
			MaxThroughput: 0,
			MaxIOPS:       0,
		}
		result := qosPolicyGroupCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Equal(tt, &name, result.Info.Name)
		assert.NotNil(tt, result.Info.Svm)
		assert.Equal(tt, &svmName, result.Info.Svm.Name)
		assert.NotNil(tt, result.Info.Fixed)
		assert.Equal(tt, nillable.ToPointer(int64(0)), result.Info.Fixed.MaxThroughputMbps)
		assert.Equal(tt, nillable.ToPointer(int64(0)), result.Info.Fixed.MaxThroughputIops)
		assert.Nil(tt, result.Info.Fixed.CapacityShared)
	})

	t.Run("WhenParamsHasNegativeValues_ThenFieldsAreMappedCorrectly", func(tt *testing.T) {
		name := "negative-policy"
		svmName := "negative-svm"
		maxThroughput := int64(-100)
		maxIOPS := int64(-500)
		params := &QoSPolicyGroupCreateParams{
			Name:          name,
			SvmName:       svmName,
			MaxThroughput: maxThroughput,
			MaxIOPS:       maxIOPS,
		}
		result := qosPolicyGroupCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Equal(tt, &name, result.Info.Name)
		assert.NotNil(tt, result.Info.Svm)
		assert.Equal(tt, &svmName, result.Info.Svm.Name)
		assert.NotNil(tt, result.Info.Fixed)
		assert.Equal(tt, &maxThroughput, result.Info.Fixed.MaxThroughputMbps)
		assert.Equal(tt, &maxIOPS, result.Info.Fixed.MaxThroughputIops)
		assert.Nil(tt, result.Info.Fixed.CapacityShared)
	})

	t.Run("WhenParamsHasLargeValues_ThenFieldsAreMappedCorrectly", func(tt *testing.T) {
		name := "large-policy"
		svmName := "large-svm"
		maxThroughput := int64(999999)
		maxIOPS := int64(999999)
		params := &QoSPolicyGroupCreateParams{
			Name:          name,
			SvmName:       svmName,
			MaxThroughput: maxThroughput,
			MaxIOPS:       maxIOPS,
		}
		result := qosPolicyGroupCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Equal(tt, &name, result.Info.Name)
		assert.NotNil(tt, result.Info.Svm)
		assert.Equal(tt, &svmName, result.Info.Svm.Name)
		assert.NotNil(tt, result.Info.Fixed)
		assert.Equal(tt, &maxThroughput, result.Info.Fixed.MaxThroughputMbps)
		assert.Equal(tt, &maxIOPS, result.Info.Fixed.MaxThroughputIops)
		assert.Nil(tt, result.Info.Fixed.CapacityShared)
	})

	t.Run("WhenParamsHasEmptyStrings_ThenFieldsAreMappedCorrectly", func(tt *testing.T) {
		name := ""
		svmName := ""
		maxThroughput := int64(100)
		maxIOPS := int64(500)
		params := &QoSPolicyGroupCreateParams{
			Name:          name,
			SvmName:       svmName,
			MaxThroughput: maxThroughput,
			MaxIOPS:       maxIOPS,
		}
		result := qosPolicyGroupCreateParamsToONTAP(params)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Info)
		assert.Equal(tt, &name, result.Info.Name)
		assert.NotNil(tt, result.Info.Svm)
		assert.Equal(tt, &svmName, result.Info.Svm.Name)
		assert.NotNil(tt, result.Info.Fixed)
		assert.Equal(tt, &maxThroughput, result.Info.Fixed.MaxThroughputMbps)
		assert.Equal(tt, &maxIOPS, result.Info.Fixed.MaxThroughputIops)
		assert.Nil(tt, result.Info.Fixed.CapacityShared)
	})
}

func TestQuotaRuleCollectionGet(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QuotaRuleCollectionGet(context.TODO(), storage.NewQuotaRuleCollectionGetParams())
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.QuotaRuleCollectionGetOK{
			Payload: &models.QuotaRuleResponse{
				QuotaRuleResponseInlineRecords: []*models.QuotaRule{},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		response, err := client.QuotaRuleCollectionGet(context.TODO(), storage.NewQuotaRuleCollectionGetParams())

		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.NotNil(tt, response.Payload)
	})
}

func TestQuotaRuleCreate(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QuotaRuleCreate(context.TODO(), storage.NewQuotaRuleCreateParams())
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.QuotaRuleCreateAccepted{
			Payload: &models.QuotaRuleJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		accepted, err := client.QuotaRuleCreate(context.TODO(), storage.NewQuotaRuleCreateParams())

		assert.NoError(tt, err)
		assert.NotNil(tt, accepted)
		assert.NotNil(tt, accepted.Payload)
		assert.NotNil(tt, accepted.Payload.Job)
		assert.Equal(tt, jobUUID, accepted.Payload.Job.UUID.String())
	})
}

func TestQuotaRuleModify(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QuotaRuleModify(context.TODO(), storage.NewQuotaRuleModifyParams())
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.QuotaRuleModifyAccepted{
			Payload: &models.QuotaRuleJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		accepted, err := client.QuotaRuleModify(context.TODO(), storage.NewQuotaRuleModifyParams())

		assert.NoError(tt, err)
		assert.NotNil(tt, accepted)
		assert.NotNil(tt, accepted.Payload)
		assert.NotNil(tt, accepted.Payload.Job)
		assert.Equal(tt, jobUUID, accepted.Payload.Job.UUID.String())
	})
}

func TestQuotaRuleDelete(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QuotaRuleDelete(context.TODO(), storage.NewQuotaRuleDeleteParams())
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &storage.QuotaRuleDeleteAccepted{
			Payload: &models.QuotaRuleJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		accepted, err := client.QuotaRuleDelete(context.TODO(), storage.NewQuotaRuleDeleteParams())

		assert.NoError(tt, err)
		assert.NotNil(tt, accepted)
		assert.NotNil(tt, accepted.Payload)
		assert.NotNil(tt, accepted.Payload.Job)
		assert.Equal(tt, jobUUID, accepted.Payload.Job.UUID.String())
	})
}

func TestQosPolicyDeleteCollection(t *testing.T) {
	t.Run("WhenBothUUIDAndNameAreEmpty_ThenReturnError", func(tt *testing.T) {
		storageAPI := storage.New(&mockTransport{}, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QosPolicyDeleteCollection(&QosPolicyDeleteCollectionParams{})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "either UUID or Name must be provided")
	})

	t.Run("WhenBothUUIDAndNameProvided_ThenReturnError", func(tt *testing.T) {
		storageAPI := storage.New(&mockTransport{}, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QosPolicyDeleteCollection(&QosPolicyDeleteCollectionParams{
			UUID: "uuid-123",
			Name: "test-policy",
		})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "UUID and Name cannot both be provided")
	})

	t.Run("WhenRESTCallFailsWithNotFound_ThenReturnNil", func(tt *testing.T) {
		notFoundErr := verrors.NewNotFoundErr("qosPolicy", nillable.GetStringPtr("uuid-not-found"))
		transport := &mockTransport{err: notFoundErr}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.QosPolicyDeleteCollection(&QosPolicyDeleteCollectionParams{
			UUID: "uuid-not-found",
		})
		assert.NoError(tt, err)
		assert.Nil(tt, job)
	})

	t.Run("WhenRESTCallFailsWithOtherError_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("generic API error")}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		_, err := client.QosPolicyDeleteCollection(&QosPolicyDeleteCollectionParams{
			UUID: "uuid-error",
		})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "generic API error")
	})

	t.Run("WhenDeleteSucceeds_WithUUID_ThenReturnNil", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.QosPolicyDeleteCollectionOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.QosPolicyDeleteCollection(&QosPolicyDeleteCollectionParams{
			UUID:    "uuid-success",
			SvmName: "svm1",
		})
		assert.NoError(tt, err)
		assert.Nil(tt, job)
	})

	t.Run("WhenDeleteSucceeds_WithName_ThenReturnNil", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.QosPolicyDeleteCollectionOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.QosPolicyDeleteCollection(&QosPolicyDeleteCollectionParams{
			Name:    "test-policy",
			SvmName: "svm1",
		})
		assert.NoError(tt, err)
		assert.Nil(tt, job)
	})

	t.Run("WhenDeleteSucceeds_WithUUID_WithoutSvmName_ThenReturnNil", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.QosPolicyDeleteCollectionOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.QosPolicyDeleteCollection(&QosPolicyDeleteCollectionParams{
			UUID: "uuid-success",
		})
		assert.NoError(tt, err)
		assert.Nil(tt, job)
	})

	t.Run("WhenDeleteSucceeds_WithName_WithoutSvmName_ThenReturnNil", func(tt *testing.T) {
		transport := &mockTransport{response: &storage.QosPolicyDeleteCollectionOK{}}
		storageAPI := storage.New(transport, nil)
		client := &storageClient{api: storageAPI}
		job, err := client.QosPolicyDeleteCollection(&QosPolicyDeleteCollectionParams{
			Name: "test-policy",
		})
		assert.NoError(tt, err)
		assert.Nil(tt, job)
	})
}
