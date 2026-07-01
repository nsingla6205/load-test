package vsa

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestCreateSVM(t *testing.T) {
	params := CreateSvmParams{
		Name: "testSVM",
		Protocols: Protocols{
			EnableIscsi: true,
		},
	}

	t.Run("WhenSVMCreationSucceeds_ThenReturnSVMDetails", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSvm := &ontaprest.Svm{
			Svm: models.Svm{
				Name: nillable.ToPointer("testSVM"),
				UUID: nillable.ToPointer("testUUID"),
			},
		}

		mockSVM.On("SvmCreate", mock.Anything).Return(mockSvm, nil, nil)

		resp, err := rc.CreateSVM(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, "testSVM", resp.Name)
		assert.Equal(tt, "testUUID", resp.ExternalUUID)

		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenSVMCreationFails_ThenReturnError", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSVM.On("SvmCreate", mock.Anything).Return(nil, nil, errors.New("creation error"))

		resp, err := rc.CreateSVM(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)

		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenSVMResponseIsInvalid_ThenReturnError", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSVM.On("SvmCreate", mock.Anything).Return(nil, nil, nil)

		resp, err := rc.CreateSVM(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, "invalid SVM response from API", err.Error())

		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenSVMResponseIsInvalid_getOntapClientFuncError", func(tt *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("getOntapClientFunc error")
		}
		rc := &OntapRestProvider{}

		resp, err := rc.CreateSVM(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.EqualError(tt, err, "getOntapClientFunc error")
	})
}

func TestCreateSVM_PollJob(t *testing.T) {
	params := CreateSvmParams{
		Name: "testSVM",
		Protocols: Protocols{
			EnableIscsi: true,
		},
	}

	t.Run("WhenJobPollingSucceeds_ThenReturnSVMDetails", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockAcceptedJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		mockSvm := &ontaprest.Svm{
			Svm: models.Svm{
				Name: nillable.ToPointer("testSVM"),
				UUID: nillable.ToPointer("testUUID"),
			},
		}

		mockSVM.On("SvmCreate", mock.Anything).Return(mockSvm, mockAcceptedJob, nil)
		mockClient.On("Poll", "testJobUUID").Return(nil)

		resp, err := rc.CreateSVM(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, "testSVM", resp.Name)
		assert.Equal(tt, "testUUID", resp.ExternalUUID)

		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenJobPollingFails_ThenReturnError", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockAcceptedJob := &ontaprest.JobAccepted{
			JobUUID:      "testJobUUID",
			ResourceUUID: "testResourceUUID",
		}

		mockSVM.On("SvmCreate", mock.Anything).Return(nil, mockAcceptedJob, nil)
		mockClient.On("Poll", "testJobUUID").Return(fmt.Errorf("polling error"))

		resp, err := rc.CreateSVM(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, "polling error", err.Error())

		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenJobPollingFails_getOntapClientFuncError", func(tt *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("getOntapClientFunc error")
		}
		rc := &OntapRestProvider{}
		resp, err := rc.CreateSVM(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, "getOntapClientFunc error", err.Error())
	})
}

func TestModifySVMWithQoSPolicy(t *testing.T) {
	params := ModifySVMWithQoSPolicyParams{
		SvmUUID:       "test-svm-uuid",
		QoSPolicyName: "test-qos-policy",
	}

	t.Run("WhenSVMModificationSucceeds_ThenReturnNil", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		// Mock successful synchronous modification
		mockSVM.On("SvmModify", mock.Anything).Return(true, nil, nil)

		err := rc.ModifySVMWithQoSPolicy(params)

		assert.NoError(tt, err)
		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenSVMModificationReturnsJob_ThenPollJobAndReturnNil", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		// Mock asynchronous modification with job
		job := &ontaprest.JobAccepted{JobUUID: "test-job-uuid"}
		mockSVM.On("SvmModify", mock.Anything).Return(false, job, nil)
		mockClient.On("Poll", "test-job-uuid").Return(nil)

		err := rc.ModifySVMWithQoSPolicy(params)

		assert.NoError(tt, err)
		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenGetOntapClientFails_ThenReturnError", func(tt *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client error")
		}
		rc := &OntapRestProvider{}

		err := rc.ModifySVMWithQoSPolicy(params)

		assert.Error(tt, err)
		assert.Equal(tt, "client error", err.Error())
	})

	t.Run("WhenSVMModifyFails_ThenReturnError", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		// Mock SVM modification failure
		mockSVM.On("SvmModify", mock.Anything).Return(false, nil, errors.New("svm modify error"))

		err := rc.ModifySVMWithQoSPolicy(params)

		assert.Error(tt, err)
		assert.Equal(tt, "svm modify error", err.Error())
		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenJobPollingFails_ThenReturnError", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		// Mock asynchronous modification with job
		job := &ontaprest.JobAccepted{JobUUID: "test-job-uuid"}
		mockSVM.On("SvmModify", mock.Anything).Return(false, job, nil)
		mockClient.On("Poll", "test-job-uuid").Return(errors.New("poll error"))

		err := rc.ModifySVMWithQoSPolicy(params)

		assert.Error(tt, err)
		assert.Equal(tt, "poll error", err.Error())
		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenParamsAreCorrectlyPassed_ThenCallSvmModifyWithCorrectParams", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		// Mock successful modification
		mockSVM.On("SvmModify", &ontaprest.SvmModifyParams{
			SvmUUID:       "test-svm-uuid",
			QoSPolicyName: nillable.ToPointer("test-qos-policy"),
		}).Return(true, nil, nil)

		err := rc.ModifySVMWithQoSPolicy(params)

		assert.NoError(tt, err)
		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenQoSPolicyNameEmpty_ThenCallSvmModifyWithNone", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() {
			getOntapClientFunc = originalgetOntapClientFunc
		}()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		// Empty QoSPolicyName clears policy; ONTAP expects "none"
		clearParams := ModifySVMWithQoSPolicyParams{
			SvmUUID:       "svm-uuid-clear",
			QoSPolicyName: "",
		}
		mockSVM.On("SvmModify", &ontaprest.SvmModifyParams{
			SvmUUID:       "svm-uuid-clear",
			QoSPolicyName: nillable.ToPointer("none"),
		}).Return(true, nil, nil)

		err := rc.ModifySVMWithQoSPolicy(clearParams)

		assert.NoError(tt, err)
		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

func TestGetSVM(t *testing.T) {
	params := GetSvmParams{Name: "testSVM"}

	t.Run("WhenSVMGetSucceeds_ThenReturnSVM", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		expectedSvm := &ontaprest.Svm{}
		mockSVM.On("SvmGet", &ontaprest.SvmGetParams{SvmName: "testSVM"}).Return(expectedSvm, nil)

		resp, err := rc.GetSVM(params)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedSvm, resp)
		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenGetOntapClientFails_ThenReturnError", func(tt *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client error")
		}
		rc := &OntapRestProvider{}
		resp, err := rc.GetSVM(params)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, "client error", err.Error())
	})

	t.Run("WhenSvmGetFails_ThenReturnError", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}
		mockSVM.On("SvmGet", &ontaprest.SvmGetParams{SvmName: "testSVM"}).Return(nil, errors.New("svm get error"))
		resp, err := rc.GetSVM(params)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, "svm get error", err.Error())
		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}

func TestListSVMs(t *testing.T) {
	namePattern := "gcnv-*"
	params := ListSvmsParams{
		NamePattern: &namePattern,
		Fields:      []string{"uuid", "name"},
	}

	t.Run("WhenListSucceeds_ThenReturnSVMs", func(tt *testing.T) {
		mockSVM := new(ontaprest.MockSVMClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SVM").Return(mockSVM)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		expected := []*ontaprest.Svm{{
			Svm: models.Svm{
				Name: nillable.ToPointer("gcnv-test-svm"),
				UUID: nillable.ToPointer("svm-uuid"),
			},
		}}
		mockSVM.On("SvmCollectionGet", &ontaprest.SvmGetCollectionParams{
			BaseParams: ontaprest.BaseParams{Fields: params.Fields},
			SvmName:    params.NamePattern,
		}).Return(expected, nil)

		got, err := rc.ListSVMs(params)
		assert.NoError(tt, err)
		assert.Equal(tt, expected, got)
		mockSVM.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenClientCreationFails_ThenReturnError", func(tt *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client error")
		}
		rc := &OntapRestProvider{}

		got, err := rc.ListSVMs(params)
		assert.Error(tt, err)
		assert.Nil(tt, got)
		assert.EqualError(tt, err, "client error")
	})
}
