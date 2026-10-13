package ontap_rest

import (
	"errors"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	san "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/s_a_n"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestIscsiServiceGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		response, err := client.IscsiServiceGet(&IscsiGetParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, response)
	})

	t.Run("WhenNoRecordsReturned_ThenReturnNotFoundError", func(tt *testing.T) {
		transport := &mockTransport{response: &san.IscsiServiceCollectionGetOK{
			Payload: &models.IscsiServiceResponse{
				IscsiServiceResponseInlineRecords: []*models.IscsiService{},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		response, err := client.IscsiServiceGet(&IscsiGetParams{})
		assert.EqualError(tt, err, "iscsi service not found")
		assert.Nil(tt, response)
	})

	t.Run("WhenMultipleRecordsReturned_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{response: &san.IscsiServiceCollectionGetOK{
			Payload: &models.IscsiServiceResponse{
				IscsiServiceResponseInlineRecords: []*models.IscsiService{
					{}, {},
				},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		response, err := client.IscsiServiceGet(&IscsiGetParams{})
		assert.EqualError(tt, err, "unexpected response when querying for iscsi service")
		assert.Nil(tt, response)
	})

	t.Run("WhenSingleRecordReturned_ThenReturnIscsiService", func(tt *testing.T) {
		iscsiService := &models.IscsiService{}
		transport := &mockTransport{response: &san.IscsiServiceCollectionGetOK{
			Payload: &models.IscsiServiceResponse{
				IscsiServiceResponseInlineRecords: []*models.IscsiService{iscsiService},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		response, err := client.IscsiServiceGet(&IscsiGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, response)
		assert.Equal(tt, iscsiService, &response.IscsiService)
	})
}

func TestIscsiServiceCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.IscsiServiceCreate(&IscsiCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		transport := &mockTransport{response: &san.IscsiServiceCreateCreated{}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.IscsiServiceCreate(&IscsiCreateParams{})
		assert.NoError(tt, err)
	})
}

func TestIGroupCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		_, err := client.IGroupCreate(&IgroupCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccessful_ThenReturnIgroupUUID", func(tt *testing.T) {
		igroupUUID := "9760acf5-4638-11e7-9bdb-020073ca0001"
		transport := &mockTransport{response: &san.IgroupCreateCreated{
			Payload: &models.IgroupResponse{
				IgroupResponseInlineRecords: []*models.Igroup{
					{UUID: &igroupUUID},
				},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		got, err := client.IGroupCreate(&IgroupCreateParams{})
		assert.NoError(tt, err)
		assert.Equal(tt, igroupUUID, got)
	})

	t.Run("WhenUUIDMissing_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{response: &san.IgroupCreateCreated{
			Payload: &models.IgroupResponse{
				IgroupResponseInlineRecords: []*models.Igroup{{}},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		got, err := client.IGroupCreate(&IgroupCreateParams{})
		assert.EqualError(tt, err, "ONTAP igroup create response is missing uuid")
		assert.Empty(tt, got)
	})
}

func TestIGroupDelete(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.IGroupDelete(&IgroupDeleteParams{UUID: "uuid"})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccessful_ThenReturnNoError", func(tt *testing.T) {
		transport := &mockTransport{response: &san.IgroupDeleteOK{}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.IGroupDelete(&IgroupDeleteParams{UUID: "uuid"})
		assert.NoError(tt, err)
	})
}

func TestLunCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		_, err := client.LunCreate(&LunCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenAcceptedResponse_ThenPollAndReturnLun", func(tt *testing.T) {
		poller := NewMockPoller(tt)
		poller.Mock.On("Poll", mock.Anything).Return(nil)
		lunName := "lun1"
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &san.LunCreateAccepted{
			Payload: &models.LunJobLinkResponse{
				Records: []*models.Lun{{Name: &lunName}},
				Job:     &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI, poller: poller}
		lun, err := client.LunCreate(&LunCreateParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, lun)
		assert.Equal(tt, lunName, *lun.Name)
	})

	t.Run("WhenCreatedResponse_ThenReturnLun", func(tt *testing.T) {
		lunName := "lun1"
		transport := &mockTransport{response: &san.LunCreateCreated{
			Payload: &models.LunResponse{
				LunResponseInlineRecords: []*models.Lun{{Name: &lunName}},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		lun, err := client.LunCreate(&LunCreateParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, lun)
		assert.Equal(tt, lunName, *lun.Name)
	})

	t.Run("WhenPollFails_ThenReturnError", func(tt *testing.T) {
		poller := NewMockPoller(tt)
		poller.Mock.On("Poll", mock.Anything).Return(errors.New("polling failed"))
		jobUUID := "job-uuid"
		transport := &mockTransport{response: &san.LunCreateAccepted{
			Payload: &models.LunJobLinkResponse{
				Records: []*models.Lun{{}},
				Job:     &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI, poller: poller}

		lun, err := client.LunCreate(&LunCreateParams{})
		assert.EqualError(tt, err, "polling failed")
		assert.Nil(tt, lun)
		poller.AssertCalled(tt, "Poll", jobUUID)
	})
}

func TestLunMapCreate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.LunMapCreate(&LunMapCreateParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenSuccessful_ThenNoError", func(tt *testing.T) {
		transport := &mockTransport{
			response: &san.LunMapCreateCreated{},
		}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.LunMapCreate(&LunMapCreateParams{})
		assert.NoError(tt, err)
	})
}

func TestIGroupGet(t *testing.T) {
	t.Run("WhenNameIsMissing_ThenReturnError", func(tt *testing.T) {
		sanAPI := san.New(&mockTransport{}, nil)
		client := &sanClient{api: sanAPI}
		_, err := client.IGroupGet(&IgroupGetParams{})
		assert.EqualError(tt, err, "missing required parameter 'name' or 'uuid' when getting a specific Igroup")
	})

	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		_, err := client.IGroupGet(&IgroupGetParams{Name: nillable.ToPointer("igroup1")})
		assert.EqualError(tt, err, transport.err.Error())
	})

	t.Run("WhenNoRecordsReturned_ThenReturnNotFoundError", func(tt *testing.T) {
		transport := &mockTransport{response: &san.IgroupCollectionGetOK{
			Payload: &models.IgroupResponse{
				IgroupResponseInlineRecords: []*models.Igroup{},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		_, err := client.IGroupGet(&IgroupGetParams{Name: nillable.ToPointer("igroup1")})
		assert.EqualError(tt, err, "igroup not found")
	})

	t.Run("WhenMultipleRecordsReturned_ThenReturnError", func(tt *testing.T) {
		igroupName := "igroup1"
		transport := &mockTransport{response: &san.IgroupCollectionGetOK{
			Payload: &models.IgroupResponse{
				IgroupResponseInlineRecords: []*models.Igroup{
					{Name: &igroupName},
					{Name: &igroupName},
				},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		_, err := client.IGroupGet(&IgroupGetParams{Name: nillable.ToPointer(igroupName)})
		assert.EqualError(tt, err, "unexpected response when querying igroup")
	})

	t.Run("WhenSingleRecordReturned_ThenReturnIgroup", func(tt *testing.T) {
		igroupName := "igroup1"
		transport := &mockTransport{response: &san.IgroupCollectionGetOK{
			Payload: &models.IgroupResponse{
				IgroupResponseInlineRecords: []*models.Igroup{
					{Name: &igroupName},
				},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		igroup, err := client.IGroupGet(&IgroupGetParams{Name: nillable.ToPointer(igroupName)})
		assert.NoError(tt, err)
		assert.NotNil(tt, igroup)
		assert.Equal(tt, igroupName, *igroup.Name)
	})

	t.Run("WhenUUIDProvided_ThenReturnIgroup", func(tt *testing.T) {
		igroupUUID := "9760acf5-4638-11e7-9bdb-020073ca0001"
		transport := &mockTransport{response: &san.IgroupCollectionGetOK{
			Payload: &models.IgroupResponse{
				IgroupResponseInlineRecords: []*models.Igroup{
					{UUID: &igroupUUID},
				},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		igroup, err := client.IGroupGet(&IgroupGetParams{UUID: &igroupUUID})
		assert.NoError(tt, err)
		assert.NotNil(tt, igroup)
		assert.Equal(tt, igroupUUID, *igroup.UUID)
	})
}

func TestLunGet(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		luns, err := client.LunGet(&LunGetParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.Nil(tt, luns)
	})

	t.Run("WhenNoRecordsReturned_ThenReturnNotFoundError", func(tt *testing.T) {
		transport := &mockTransport{response: &san.LunCollectionGetOK{
			Payload: &models.LunResponse{
				LunResponseInlineRecords: []*models.Lun{},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		luns, err := client.LunGet(&LunGetParams{})
		assert.EqualError(tt, err, "lun not found")
		assert.Nil(tt, luns)
	})

	t.Run("WhenMultipleRecordsReturned_ThenReturnMultipleLuns", func(tt *testing.T) {
		lunName1 := "lun1"
		lunName2 := "lun2"
		transport := &mockTransport{response: &san.LunCollectionGetOK{
			Payload: &models.LunResponse{
				LunResponseInlineRecords: []*models.Lun{
					{Name: &lunName1},
					{Name: &lunName2},
				},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		luns, err := client.LunGet(&LunGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, luns)
		assert.Len(tt, luns, 2)
		assert.Equal(tt, lunName1, *luns[0].Name)
		assert.Equal(tt, lunName2, *luns[1].Name)
	})

	t.Run("WhenSingleRecordReturned_ThenReturnLun", func(tt *testing.T) {
		lunName := "lun1"
		transport := &mockTransport{response: &san.LunCollectionGetOK{
			Payload: &models.LunResponse{
				LunResponseInlineRecords: []*models.Lun{
					{Name: &lunName},
				},
			},
		}}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		luns, err := client.LunGet(&LunGetParams{})
		assert.NoError(tt, err)
		assert.NotNil(tt, luns)
		assert.Len(tt, luns, 1)
		assert.Equal(tt, lunName, *luns[0].Name)
	})
}

func TestLunUpdate(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		ok, job, err := client.LunUpdate(&LunUpdateParams{})
		assert.EqualError(tt, err, transport.err.Error())
		assert.False(tt, ok)
		assert.Nil(tt, job)
	})

	t.Run("WhenOKResponse_ThenReturnTrue", func(tt *testing.T) {
		transport := &mockTransport{
			response: &san.LunModifyOK{},
		}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		ok, job, err := client.LunUpdate(&LunUpdateParams{})
		assert.NoError(tt, err)
		assert.True(tt, ok)
		assert.Nil(tt, job)
	})

	t.Run("WhenAcceptedResponse_ThenReturnJobAccepted", func(tt *testing.T) {
		jobUUID := "job-uuid"
		transport := &mockTransport{
			response: &san.LunModifyAccepted{
				Payload: &models.LunJobLinkResponse{
					Job: &models.JobLink{UUID: nillable.ToPointer(strfmt.UUID(jobUUID))},
				},
			},
		}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		ok, job, err := client.LunUpdate(&LunUpdateParams{})
		assert.NoError(tt, err)
		assert.False(tt, ok)
		assert.NotNil(tt, job)
		assert.Equal(tt, jobUUID, job.JobUUID)
	})
}

func TestIGroupAddInitiator(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.IGroupAddInitiator(&IgroupAddInitiatorParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})
	t.Run("WhenAcceptedResponse_ThenReturnJobAccepted", func(tt *testing.T) {
		transport := &mockTransport{
			response: &san.IgroupInitiatorCreateCreated{},
		}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.IGroupAddInitiator(&IgroupAddInitiatorParams{})
		assert.Nil(tt, err)
	})
}

func TestIGroupDeleteInitiator(t *testing.T) {
	t.Run("WhenRESTCallFails_ThenReturnError", func(tt *testing.T) {
		transport := &mockTransport{err: errors.New("something went wrong")}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.IGroupDeleteInitiator(&IgroupDeleteInitiatorParams{})
		assert.EqualError(tt, err, transport.err.Error())
	})
	t.Run("WhenAcceptedResponse_ThenReturnJobAccepted", func(tt *testing.T) {
		transport := &mockTransport{
			response: &san.IgroupInitiatorDeleteOK{},
		}
		sanAPI := san.New(transport, nil)
		client := &sanClient{api: sanAPI}
		err := client.IGroupDeleteInitiator(&IgroupDeleteInitiatorParams{})
		assert.Nil(tt, err)
	})
}
