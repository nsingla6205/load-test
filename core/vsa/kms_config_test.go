package vsa

import (
	"errors"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestCreateKmsConfig(t *testing.T) {
	t.Run("CreateKmsConfigReturnsResponseOnSuccess", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		expectedUUID := "external-uuid"
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		response := []*ontaprest.GcpKms{{}}
		response[0].UUID = nillable.ToPointer(expectedUUID)
		defer func() { getOntapClientFunc = origGetClient }()
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsCreate", mock.Anything).Return(response, nil)
		provider := &OntapRestProvider{}
		params := CreateKmsConfigParams{
			KeyName:           "key",
			KeyRingLocation:   "us",
			KeyRingName:       "ring",
			ProjectID:         "project",
			Credentials:       nillable.ToPointer(strfmt.Password("credentials")),
			SvmName:           "svm",
			PrivilegedAccount: "svc@project.iam.gserviceaccount.com",
		}
		resp, err := provider.CreateKmsConfig(params)
		assert.NoError(t, err)
		assert.Equal(t, expectedUUID, resp.ExternalUUID)
	})
	t.Run("CreateKmsConfigReturnsResponseOnFailure", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsCreate", mock.Anything).Return(nil, errors.New("failed to create KMS config"))
		provider := &OntapRestProvider{}
		params := CreateKmsConfigParams{
			KeyName:           "key",
			KeyRingLocation:   "us",
			KeyRingName:       "ring",
			ProjectID:         "project",
			Credentials:       nillable.ToPointer(strfmt.Password("credentials")),
			SvmName:           "svm",
			PrivilegedAccount: "svc@project.iam.gserviceaccount.com",
		}
		resp, err := provider.CreateKmsConfig(params)
		assert.Error(t, err)
		assert.Nil(t, resp)
	})
}

func TestIsGcpKmsReachable(t *testing.T) {
	t.Run("IsGcpKmsReachableReturnsTrueWhenReachable", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()
		mockClient.On("Security").Return(mockSecurity)
		reachability := &models.GcpKmsInlineGoogleReachability{
			Reachable: nillable.ToPointer(true),
			Message:   nillable.ToPointer("ok"),
		}
		gcpKmsResponse := &ontaprest.GcpKms{}
		gcpKmsResponse.GoogleReachability = reachability
		mockSecurity.On("GcpKmsGet", mock.Anything).Return(gcpKmsResponse, nil)
		provider := &OntapRestProvider{}
		params := GetKmsConfigParams{ExternalKmsConfigID: "uuid"}
		result, err := provider.IsGcpKmsReachable(params)
		assert.NoError(t, err)
		assert.True(t, result)
	})
	t.Run("IsGcpKmsReachableReturnsFalseWhenNotReachable", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()
		mockClient.On("Security").Return(mockSecurity)
		reachability := &models.GcpKmsInlineGoogleReachability{
			Reachable: nillable.ToPointer(false),
			Message:   nillable.ToPointer("unreachable"),
		}
		gcpKmsResponse := &ontaprest.GcpKms{}
		gcpKmsResponse.GoogleReachability = reachability
		mockSecurity.On("GcpKmsGet", mock.Anything).Return(gcpKmsResponse, nil)
		provider := &OntapRestProvider{}
		params := GetKmsConfigParams{ExternalKmsConfigID: "uuid"}
		result, err := provider.IsGcpKmsReachable(params)
		assert.NoError(t, err)
		assert.False(t, result)
	})
	t.Run("IsGcpKmsReachableReturnsPermissionDeniedError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()
		mockClient.On("Security").Return(mockSecurity)
		reachability := &models.GcpKmsInlineGoogleReachability{
			Reachable: nillable.ToPointer(false),
			Message:   nillable.ToPointer("PERMISSION_DENIED: access denied"),
		}
		inlineEkmip := &models.GcpKmsInlineEkmipReachabilityInlineArrayItem{
			Reachable: nillable.ToPointer(false),
		}
		inline := []*models.GcpKmsInlineEkmipReachabilityInlineArrayItem{inlineEkmip}
		gcpKmsResponse := &ontaprest.GcpKms{}
		gcpKmsResponse.GoogleReachability = reachability
		gcpKmsResponse.GcpKmsInlineEkmipReachability = inline

		mockSecurity.On("GcpKmsGet", mock.Anything).Return(gcpKmsResponse, nil)
		provider := &OntapRestProvider{}
		params := GetKmsConfigParams{ExternalKmsConfigID: "uuid"}
		result, err := provider.IsGcpKmsReachable(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "permission_denied")
		assert.False(t, result)
	})
	t.Run("IsGcpKmsReachableReturnsFalseWhenInlineEkmipNotReachable", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()
		mockClient.On("Security").Return(mockSecurity)

		reachability := &models.GcpKmsInlineGoogleReachability{
			Reachable: nillable.ToPointer(true),
			Message:   nillable.ToPointer("ok"),
		}
		inlineEkmip := &models.GcpKmsInlineEkmipReachabilityInlineArrayItem{
			Reachable: nillable.ToPointer(false),
		}
		inline := []*models.GcpKmsInlineEkmipReachabilityInlineArrayItem{inlineEkmip}
		gcpKmsResponse := &ontaprest.GcpKms{}
		gcpKmsResponse.GoogleReachability = reachability
		gcpKmsResponse.GcpKmsInlineEkmipReachability = inline
		mockSecurity.On("GcpKmsGet", mock.Anything).Return(gcpKmsResponse, nil)
		provider := &OntapRestProvider{}
		params := GetKmsConfigParams{ExternalKmsConfigID: "uuid"}
		result, err := provider.IsGcpKmsReachable(params)
		assert.NoError(t, err)
		assert.False(t, result)
	})
	t.Run("IsGcpKmsReachableReturnsErrorOnClientFailure", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsGet", mock.Anything).Return(nil, errors.New("client failure"))
		provider := &OntapRestProvider{}
		params := GetKmsConfigParams{ExternalKmsConfigID: "uuid"}
		result, err := provider.IsGcpKmsReachable(params)
		assert.Error(t, err)
		assert.False(t, result)
	})
}

func TestDeleteEkmConfig(t *testing.T) {
	t.Run("WhenGetOntapClientFuncReturnsError", func(t *testing.T) {
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("get ontap client error")
		}
		defer func() { getOntapClientFunc = origGetClient }()
		provider := &OntapRestProvider{}
		params := DeleteKmsConfigParams{}
		err := provider.DeleteEkmConfig(params)
		assert.Error(t, err)
		assert.Errorf(t, err, "get ontap client error")
	})
	t.Run("WhenSecurityGcpKmsDeleteReturnsError", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("get ontap client error")
		}
		defer func() { getOntapClientFunc = origGetClient }()
		provider := &OntapRestProvider{}
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsDelete", mock.Anything).Return(errors.New("ekm delete failed"))
		params := DeleteKmsConfigParams{ExternalKmsConfigID: "uuid1"}

		err := provider.DeleteEkmConfig(params)
		assert.Error(t, err)
		assert.Errorf(t, err, "ekm delete failed")
	})
	t.Run("WhenDeleteEkmConfigIsSuccessful", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()
		provider := &OntapRestProvider{}
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsDelete", mock.Anything).Return(nil)
		params := DeleteKmsConfigParams{ExternalKmsConfigID: "uuid1"}

		err := provider.DeleteEkmConfig(params)
		assert.NoError(t, err)
		assert.Nil(t, err)
	})
}

func TestModifyGcpKms(t *testing.T) {
	t.Run("ModifyGcpKmsReturnsGcpKmsOnSuccess", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		expectedUUID := "modified-uuid"
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		gcpKmsResponse := &ontaprest.GcpKms{
			GcpKms: models.GcpKms{
				UUID: nillable.ToPointer(expectedUUID),
			},
		}
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsModify", mock.Anything).Return(gcpKmsResponse, nil, nil)

		provider := &OntapRestProvider{}
		credentials := (*log.Secret)(nillable.ToPointer(strfmt.Password("new-credentials")))
		result, jobUUID, err := provider.ModifyGcpKms("external-uuid", credentials)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, expectedUUID, *result.UUID)
		assert.Nil(t, jobUUID)
	})

	t.Run("ModifyGcpKmsReturnsJobUUIDWhenAsync", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		expectedJobUUID := "job-123"
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		job := &ontaprest.JobAccepted{
			JobUUID: expectedJobUUID,
		}
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsModify", mock.Anything).Return(nil, job, nil)

		provider := &OntapRestProvider{}
		credentials := (*log.Secret)(nillable.ToPointer(strfmt.Password("new-credentials")))
		result, jobUUID, err := provider.ModifyGcpKms("external-uuid", credentials)

		assert.NoError(t, err)
		assert.Nil(t, result)
		assert.NotNil(t, jobUUID)
		assert.Equal(t, expectedJobUUID, *jobUUID)
	})

	t.Run("ModifyGcpKmsReturnsErrorOnFailure", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		modifyErr := errors.New("modify failed")
		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsModify", mock.Anything).Return(nil, nil, modifyErr)

		provider := &OntapRestProvider{}
		credentials := (*log.Secret)(nillable.ToPointer(strfmt.Password("new-credentials")))
		result, jobUUID, err := provider.ModifyGcpKms("external-uuid", credentials)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Failed to establish connectivity with the cloud key management service while updating GCP KMS")
		assert.ErrorIs(t, err, modifyErr)
		assert.Contains(t, err.Error(), "modify failed")
		assert.Nil(t, result)
		assert.Nil(t, jobUUID)
	})

	t.Run("ModifyGcpKmsReturnsErrorOnClientCreationFailure", func(t *testing.T) {
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client creation failed")
		}
		defer func() { getOntapClientFunc = origGetClient }()

		provider := &OntapRestProvider{}
		credentials := (*log.Secret)(nillable.ToPointer(strfmt.Password("new-credentials")))
		result, jobUUID, err := provider.ModifyGcpKms("external-uuid", credentials)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client creation failed")
		assert.Nil(t, result)
		assert.Nil(t, jobUUID)
	})
}
func TestEnableAutoVolOfflineCronForGCPKMS(t *testing.T) {
	t.Run("EnableAutoVolOfflineCronForGCPKMSReturnsNilOnSuccess", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("EnableAutoVolOfflineCronForGCPKMS").Return(nil)

		provider := &OntapRestProvider{}
		err := provider.EnableAutoVolOfflineCronForGCPKMS()

		assert.NoError(t, err)
	})

	t.Run("EnableAutoVolOfflineCronForGCPKMSReturnsErrorOnClientFailure", func(t *testing.T) {
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client creation failed")
		}
		defer func() { getOntapClientFunc = origGetClient }()

		provider := &OntapRestProvider{}
		err := provider.EnableAutoVolOfflineCronForGCPKMS()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client creation failed")
	})

	t.Run("EnableAutoVolOfflineCronForGCPKMSReturnsErrorOnSecurityCallFailure", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("EnableAutoVolOfflineCronForGCPKMS").Return(errors.New("enable auto vol offline cron failed"))

		provider := &OntapRestProvider{}
		err := provider.EnableAutoVolOfflineCronForGCPKMS()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "enable auto vol offline cron failed")
	})
}

func TestRekeyGcpKms(t *testing.T) {
	params := RekeyGcpKmsParams{
		ExternalKmsConfigID: "external-kms-id",
		KeyName:             "key-name",
		KeyRingLocation:     "us-central1",
		KeyRingName:         "ring-name",
		ProjectID:           "project-id",
	}

	t.Run("RekeyGcpKmsReturnsNilWhenNoJobUUID", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsRekeyExternal", mock.Anything).Return(&ontaprest.JobAccepted{}, nil)

		provider := &OntapRestProvider{}
		err := provider.RekeyGcpKms(params)
		assert.NoError(t, err)
	})

	t.Run("RekeyGcpKmsReturnsErrorOnClientCreationFailure", func(t *testing.T) {
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client init failed")
		}
		defer func() { getOntapClientFunc = origGetClient }()

		provider := &OntapRestProvider{}
		err := provider.RekeyGcpKms(params)
		assert.EqualError(t, err, "client init failed")
	})

	t.Run("RekeyGcpKmsPollsAndReturnsNilOnSuccess", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsRekeyExternal", mock.Anything).Return(&ontaprest.JobAccepted{JobUUID: "job-uuid"}, nil)
		mockClient.On("Poll", "job-uuid").Return(nil)

		provider := &OntapRestProvider{}
		err := provider.RekeyGcpKms(params)
		assert.NoError(t, err)
	})

	t.Run("RekeyGcpKmsReturnsErrorOnPollFailure", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsRekeyExternal", mock.Anything).Return(&ontaprest.JobAccepted{JobUUID: "job-uuid"}, nil)
		mockClient.On("Poll", "job-uuid").Return(errors.New("poll failed"))

		provider := &OntapRestProvider{}
		err := provider.RekeyGcpKms(params)
		assert.EqualError(t, err, "poll failed")
	})

	t.Run("RekeyGcpKmsReturnsErrorOnRekeyCallFailure", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsRekeyExternal", mock.Anything).Return(nil, errors.New("rekey failed"))

		provider := &OntapRestProvider{}
		err := provider.RekeyGcpKms(params)
		assert.EqualError(t, err, "rekey failed")
	})
}

func TestGetOntapGcpKmsConfig(t *testing.T) {
	t.Run("ReturnsHierarchyOnSuccess", func(t *testing.T) {
		mockClient := new(ontaprest.MockRESTClient)
		mockSecurity := new(ontaprest.MockSecurityClient)
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		defer func() { getOntapClientFunc = origGetClient }()

		mockClient.On("Security").Return(mockSecurity)
		mockSecurity.On("GcpKmsGet", mock.Anything).Return(&ontaprest.GcpKms{
			GcpKms: models.GcpKms{
				ProjectID:       nillable.ToPointer("g1p-astral-tst-host-01"),
				KeyRingName:     nillable.ToPointer("kr-east4"),
				KeyRingLocation: nillable.ToPointer("us-east4"),
				KeyName:         nillable.ToPointer("projects/g1p-astral-tst-host-01/locations/us-east4/keyRings/kr-east4/cryptoKeys/key-east4"),
			},
		}, nil)

		provider := &OntapRestProvider{}
		resp, err := provider.GetOntapGcpKmsConfig("ext-uuid")
		assert.NoError(t, err)
		assert.Equal(t, &GcpKmsHierarchy{
			ProjectID:       "g1p-astral-tst-host-01",
			KeyRingName:     "kr-east4",
			KeyRingLocation: "us-east4",
			KeyName:         "projects/g1p-astral-tst-host-01/locations/us-east4/keyRings/kr-east4/cryptoKeys/key-east4",
		}, resp)
	})

	t.Run("ReturnsErrorWhenClientInitFails", func(t *testing.T) {
		origGetClient := getOntapClientFunc
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client init failed")
		}
		defer func() { getOntapClientFunc = origGetClient }()

		provider := &OntapRestProvider{}
		resp, err := provider.GetOntapGcpKmsConfig("ext-uuid")
		assert.Nil(t, resp)
		assert.EqualError(t, err, "client init failed")
	})
}

func TestGcpKmsHierarchyMatchesTarget(t *testing.T) {
	t.Run("ReturnsTrueWhenHierarchyFullyMatchesIncludingFullPathKeyName", func(t *testing.T) {
		h := &GcpKmsHierarchy{
			ProjectID:       "g1p-astral-tst-host-01",
			KeyRingName:     "kr-east4",
			KeyRingLocation: "us-east4",
			KeyName:         "projects/g1p-astral-tst-host-01/locations/us-east4/keyRings/kr-east4/cryptoKeys/key-east4",
		}
		assert.True(t, h.MatchesTarget("g1p-astral-tst-host-01", "kr-east4", "us-east4", "key-east4"))
	})

	t.Run("ReturnsFalseWhenNonKeyFieldDiffers", func(t *testing.T) {
		h := &GcpKmsHierarchy{
			ProjectID:       "g1p-astral-tst-host-01",
			KeyRingName:     "kr-east4",
			KeyRingLocation: "us-east4",
			KeyName:         "key-east4",
		}
		assert.False(t, h.MatchesTarget("g1p-astral-tst-host-01", "different-ring", "us-east4", "key-east4"))
	})
}
