package ontap_rest

import (
	"errors"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/svm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestSvmGet(t *testing.T) {
	t.Run("WhenParamsAreNil_ThenReturnError", func(tt *testing.T) {
		client := &svmClient{}
		response, err := client.SvmGet(nil)
		assert.EqualError(tt, err, "params for SvmGet cannot be nil")
		assert.Nil(tt, response)
	})

	t.Run("WhenSvmNameIsNil_ThenReturnError", func(tt *testing.T) {
		client := &svmClient{}
		response, err := client.SvmGet(&SvmGetParams{})
		assert.EqualError(tt, err, "params.SvmName cannot be empty")
		assert.Nil(tt, response)
	})

	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		response, err := client.SvmGet(&SvmGetParams{SvmName: "test"})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
	})

	t.Run("WhenResponseRecordIsEmpty_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{response: &svm.SvmCollectionGetOK{
			Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{},
			},
		}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		response, err := client.SvmGet(&SvmGetParams{SvmName: "test"})
		assert.EqualError(tt, err, "svm 'test' not found")
		assert.Nil(tt, response)
	})

	t.Run("WhenResponseRecordIsGreaterThan1_ThenReturnError", func(tt *testing.T) {
		name := "test-svm"
		transport := &mockTransport{response: &svm.SvmCollectionGetOK{
			Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: &name},
					{Name: &name},
				},
			},
		}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		response, err := client.SvmGet(&SvmGetParams{SvmName: "test"})
		assert.EqualError(tt, err, "unexpected number of svms returned")
		assert.Nil(tt, response)
	})

	t.Run("WhenSingleRecordReturned_ThenReturnSvm", func(tt *testing.T) {
		name := "test-svm"
		transport := &mockTransport{response: &svm.SvmCollectionGetOK{
			Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: &name},
				},
			},
		}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		response, err := client.SvmGet(&SvmGetParams{SvmName: "test"})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.Equal(tt, name, *response.Name)
	})
}

func TestSvmCollectionGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		svmName := "test-svm"
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		response, err := client.SvmCollectionGet(&SvmGetCollectionParams{SvmName: &svmName})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
	})
	t.Run("WhenSvmCollectionGetIsSuccessful_ThenReturnSvms", func(tt *testing.T) {
		name := "test-svm"
		numRecords := int64(1)
		transport := &mockTransport{response: &svm.SvmCollectionGetOK{
			Payload: &models.SvmResponse{
				SvmResponseInlineRecords: []*models.Svm{
					{Name: &name},
				},
				NumRecords: &numRecords,
			},
		}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		response, err := client.SvmCollectionGet(&SvmGetCollectionParams{SvmName: &name})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
	})
}

func TestSvmCreate(t *testing.T) {
	t.Run("WhenParamsAreNil_ThenReturnError", func(tt *testing.T) {
		client := &svmClient{}
		response, job, err := client.SvmCreate(nil)
		assert.EqualError(tt, err, "params for SvmCreate cannot be nil")
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		response, job, err := client.SvmCreate(&SvmCreateParams{Name: "test"})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
		assert.Nil(tt, job)
	})

	t.Run("WhenSvmCreateIsSuccessful_ThenReturnCreatedSvm", func(tt *testing.T) {
		name := "test-svm"
		transport := &mockTransport{response: &svm.SvmCreateCreated{
			Payload: &models.SvmJobLinkResponse{
				Records: []*models.Svm{{Name: &name}},
			},
		}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		response, job, err := client.SvmCreate(&SvmCreateParams{Name: "test"})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.Nil(tt, job)
		assert.Equal(tt, name, *response.Name)
	})

	t.Run("WhenSvmCreateIsInProgress_ThenReturnJob", func(tt *testing.T) {
		svmUUID := "svmUUID"
		jobUUID := "jobUUID"
		transport := &mockTransport{response: &svm.SvmCreateAccepted{
			Payload: &models.SvmJobLinkResponse{
				Records: []*models.Svm{{UUID: &svmUUID}},
				Job: &models.JobLink{
					UUID: nillable.ToPointer(strfmt.UUID(jobUUID)),
				},
			},
		}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		response, job, err := client.SvmCreate(&SvmCreateParams{Name: "test"})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.NotNil(tt, job)
		assert.Equal(tt, svmUUID, *response.UUID)
		assert.Equal(tt, svmUUID, job.ResourceUUID)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})
}

func TestSvmDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		done, job, err := client.SvmDelete("someUUID")
		assert.EqualError(tt, err, transport.err.Error())
		assert.False(tt, done)
		assert.Nil(tt, job)
	})

	t.Run("WhenSvmDeleteIsSuccessful_ThenReturnTrue", func(tt *testing.T) {
		transport := &mockTransport{response: &svm.SvmDeleteOK{}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		done, job, err := client.SvmDelete("someUUID")
		assert.True(tt, done)
		assert.NoError(tt, err)
		assert.Nil(tt, job)
	})

	t.Run("WhenSvmDeleteJobIsAccepted_ThenReturnTrue", func(tt *testing.T) {
		transport := &mockTransport{response: &svm.SvmDeleteAccepted{
			Payload: &models.SvmJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID("jobUUID"))},
			},
		}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		done, job, err := client.SvmDelete("someUUID")
		assert.NoError(tt, err)
		assert.False(tt, done)
		assert.NotNil(tt, job)
		assert.Equal(tt, "jobUUID", job.JobUUID)
	})
}

func TestSvmModify(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		done, job, err := client.SvmModify(&SvmModifyParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.False(tt, done)
		assert.Nil(tt, job)
	})

	t.Run("WhenSvmModifyJobIsAccepted_ThenReturnTrue", func(tt *testing.T) {
		transport := &mockTransport{response: &svm.SvmModifyAccepted{
			Payload: &models.SvmJobLinkResponse{
				Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID("jobUUID"))},
			},
		}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		done, job, err := client.SvmModify(&SvmModifyParams{})
		assert.NoError(tt, err)
		assert.False(tt, done)
		assert.NotNil(tt, job)
		assert.Equal(tt, "jobUUID", job.JobUUID)
	})

	t.Run("WhenSvmModifyIsSuccessful_ThenReturnTrue", func(tt *testing.T) {
		transport := &mockTransport{response: &svm.SvmModifyOK{}}
		svmAPI := svm.New(transport, nil)
		client := &svmClient{api: svmAPI}
		done, job, err := client.SvmModify(&SvmModifyParams{})
		assert.NoError(tt, err)
		assert.True(tt, done)
		assert.Nil(tt, job)
	})
}

func TestSvmPeerCollectionGet(t *testing.T) {
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		svm := svm.New(transport, nil)
		client := &svmClient{api: svm}
		response, err := client.SvmPeerCollectionGet(&SvmPeerGetCollectionParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		transport := &mockTransport{response: &svm.SvmPeerCollectionGetOK{
			Payload: &models.SvmPeerResponse{NumRecords: nillable.ToPointer(int64(1)), SvmPeerResponseInlineRecords: []*models.SvmPeer{{}}},
		}}
		svm := svm.New(transport, nil)
		client := &svmClient{api: svm}
		response, err := client.SvmPeerCollectionGet(&SvmPeerGetCollectionParams{})
		assert.NoError(tt, err)
		assert.NotEmpty(tt, response)
	})
}

func TestSvmPeerCreate(t *testing.T) {
	expectedJobID := "1"
	params := &SvmPeerCreateParams{
		PeerClusterName: "src-cluster",
		PeerSVMName:     "src-svm",
		LocalSVMName:    "dest-svm",
	}

	ontapParams := svmPeerCreateParamsToONTAP(params)

	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		svm := svm.New(transport, nil)
		client := &svmClient{api: svm}
		err := client.SvmPeerCreate(params)
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenPollingFails", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		mp := NewMockPoller(tt)
		client := &svmClient{api: mcs, poller: mp}
		resp := &svm.SvmPeerCreateAccepted{Payload: &models.SvmPeerJobLinkResponse{Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(expectedJobID))}}}
		expectedError := errors.New("some error")

		mp.On("Poll", expectedJobID).Return(expectedError).Times(1)

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerCreate(params)
			assert.EqualError(tt, err, expectedError.Error())
		}()

		mcs.AssertSvmPeerCreate(svm.NewSvmPeerCreateParams().WithInfo(ontapParams.Info).WithReturnTimeout(&returnTimeout), nil, nil, nil, resp, nil)
		mcs.AssertMockClientServiceDone()
	})
	t.Run("WhenSuccessfulSync", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		client := &svmClient{api: mcs}
		resp := &svm.SvmPeerCreateCreated{Payload: &models.SvmPeerJobLinkResponse{Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(expectedJobID))}}}

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerCreate(params)
			assert.NoError(tt, err)
		}()

		mcs.AssertSvmPeerCreate(svm.NewSvmPeerCreateParams().WithInfo(ontapParams.Info).WithReturnTimeout(&returnTimeout), nil, nil, resp, nil, nil)
		mcs.AssertMockClientServiceDone()
	})
	t.Run("WhenSuccessfulAsync", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		mp := NewMockPoller(tt)
		client := &svmClient{api: mcs, poller: mp}
		resp := &svm.SvmPeerCreateAccepted{Payload: &models.SvmPeerJobLinkResponse{Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(expectedJobID))}}}

		mp.On("Poll", expectedJobID).Return(nil).Times(1)

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerCreate(params)
			assert.NoError(tt, err)
		}()
		mcs.AssertSvmPeerCreate(svm.NewSvmPeerCreateParams().WithInfo(ontapParams.Info).WithReturnTimeout(&returnTimeout), nil, nil, nil, resp, nil)
		mcs.AssertMockClientServiceDone()
	})
}

func TestSvmPeerModify(t *testing.T) {
	expectedJobID := "1"
	params := &SvmPeerModifyParams{
		UUID: "uuid",
		SvmPeer: models.SvmPeer{
			State: nillable.ToPointer(models.SvmPeerStatePeered),
		},
	}
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		client := &svmClient{api: mcs}
		expectedError := errors.New("some error")

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerModify(params)
			assert.EqualError(tt, err, expectedError.Error())
		}()

		mcs.AssertSvmPeerModify(svm.NewSvmPeerModifyParams().WithUUID(params.UUID).WithInfo(&params.SvmPeer).WithReturnTimeout(&returnTimeout), nil, nil, nil, nil, expectedError)
		mcs.AssertMockClientServiceDone()
	})
	t.Run("WhenPollingFails", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		mp := NewMockPoller(tt)
		client := &svmClient{api: mcs, poller: mp}
		resp := &svm.SvmPeerModifyAccepted{Payload: &models.SvmPeerJobLinkResponse{Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(expectedJobID))}}}
		expectedError := errors.New("some error")

		mp.On("Poll", expectedJobID).Return(expectedError).Times(1)

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerModify(params)
			assert.EqualError(tt, err, expectedError.Error())
		}()

		mcs.AssertSvmPeerModify(svm.NewSvmPeerModifyParams().WithUUID(params.UUID).WithInfo(&params.SvmPeer).WithReturnTimeout(&returnTimeout), nil, nil, nil, resp, nil)
		mcs.AssertMockClientServiceDone()
	})
	t.Run("WhenSuccessfulSync", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		client := &svmClient{api: mcs}
		resp := &svm.SvmPeerModifyOK{Payload: &models.SvmPeerJobLinkResponse{Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(expectedJobID))}}}

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerModify(params)
			assert.NoError(tt, err)
		}()

		mcs.AssertSvmPeerModify(svm.NewSvmPeerModifyParams().WithUUID(params.UUID).WithInfo(&params.SvmPeer).WithReturnTimeout(&returnTimeout), nil, nil, resp, nil, nil)
		mcs.AssertMockClientServiceDone()
	})
	t.Run("WhenSuccessfulAsync", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		mp := NewMockPoller(tt)
		client := &svmClient{api: mcs, poller: mp}
		resp := &svm.SvmPeerModifyAccepted{Payload: &models.SvmPeerJobLinkResponse{Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(expectedJobID))}}}

		mp.On("Poll", expectedJobID).Return(nil).Times(1)

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerModify(params)
			assert.NoError(tt, err)
		}()

		mcs.AssertSvmPeerModify(svm.NewSvmPeerModifyParams().WithUUID(params.UUID).WithInfo(&params.SvmPeer).WithReturnTimeout(&returnTimeout), nil, nil, nil, resp, nil)
		mcs.AssertMockClientServiceDone()
	})
}

func TestSvmPeerDelete(t *testing.T) {
	params := &SvmPeerDeleteParams{
		SvmPeerUUID: "uuid",
	}
	expectedJobID := "1"
	t.Run("WhenRESTCallFails", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		client := &svmClient{api: mcs}
		expectedError := errors.New("some error")

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerDelete(params)
			assert.EqualError(tt, err, expectedError.Error())
		}()

		mcs.AssertSvmPeerDelete(svm.NewSvmPeerDeleteParams().WithUUID(params.SvmPeerUUID).WithReturnTimeout(&returnTimeout), nil, nil, nil, nil, expectedError)
		mcs.AssertMockClientServiceDone()
	})
	t.Run("WhenPollingFailsAsync", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		mp := NewMockPoller(tt)
		client := &svmClient{api: mcs, poller: mp}
		resp := &svm.SvmPeerDeleteAccepted{Payload: &models.SvmPeerJobLinkResponse{Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(expectedJobID))}}}
		expectedError := errors.New("some error")

		mp.On("Poll", expectedJobID).Return(expectedError).Times(1)

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerDelete(params)
			assert.EqualError(tt, err, expectedError.Error())
		}()

		mcs.AssertSvmPeerDelete(svm.NewSvmPeerDeleteParams().WithUUID(params.SvmPeerUUID).WithReturnTimeout(&returnTimeout), nil, nil, nil, resp, nil)
		mcs.AssertMockClientServiceDone()
	})
	t.Run("WhenSuccessfulSync", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		client := &svmClient{api: mcs}
		resp := &svm.SvmPeerDeleteOK{Payload: &models.SvmPeerJobLinkResponse{Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(expectedJobID))}}}

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerDelete(params)
			assert.NoError(tt, err)
		}()

		mcs.AssertSvmPeerDelete(svm.NewSvmPeerDeleteParams().WithUUID(params.SvmPeerUUID).WithReturnTimeout(&returnTimeout), nil, nil, resp, nil, nil)
		mcs.AssertMockClientServiceDone()
	})
	t.Run("WhenSuccessfulAsync", func(tt *testing.T) {
		mcs := svm.NewMockClientService(tt)
		mp := NewMockPoller(tt)
		client := &svmClient{api: mcs, poller: mp}
		resp := &svm.SvmPeerDeleteAccepted{Payload: &models.SvmPeerJobLinkResponse{Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(expectedJobID))}}}

		mp.On("Poll", expectedJobID).Return(nil).Times(1)

		go func() {
			defer mcs.MockClientServiceDone()
			err := client.SvmPeerDelete(params)
			assert.NoError(tt, err)
		}()

		mcs.AssertSvmPeerDelete(svm.NewSvmPeerDeleteParams().WithUUID(params.SvmPeerUUID).WithReturnTimeout(&returnTimeout), nil, nil, nil, resp, nil)
		mcs.AssertMockClientServiceDone()
	})
}
