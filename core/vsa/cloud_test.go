package vsa

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func strPtr(s string) *string {
	return &s
}

func TestCloudTargetCreateSucceeds(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockCloudClient := new(ontapRest.MockCloudClient)

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	ontapProvider := &OntapRestProvider{}
	expectedParams := &ontapRest.CloudTargetCreateParams{
		Name:      strPtr("targetName"),
		Container: strPtr("containerName"),
	}
	expectedJob := &ontapRest.JobAccepted{JobUUID: "jobUUID"}
	ct := models.CloudTarget{
		Name:      strPtr("targetName"),
		Container: strPtr("container"),
	}
	expectedCloudTarget := &ontapRest.CloudTarget{CloudTarget: ct}

	// Mocking the behavior of the Cloud and Poll methods
	mockClient.On("Cloud").Return(mockCloudClient)
	mockCloudClient.On("CloudTargetCreate", expectedParams).Return(nil, expectedJob, nil)
	mockCloudClient.On("CloudTargetGet", strPtr("targetName")).Return(expectedCloudTarget, nil)
	mockClient.On("Poll", "jobUUID").Return(nil) // Mocking Poll to return no error

	result, err := ontapProvider.CloudTargetCreate("targetName", "containerName", "", "", "")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "targetName", *result.Name)
	assert.Equal(t, "container", *result.Container)
}

func TestCloudTargetCreateSetsIpspaceWhenProvided(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockCloudClient := new(ontapRest.MockCloudClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	ontapProvider := &OntapRestProvider{}
	expectedParams := &ontapRest.CloudTargetCreateParams{
		Name:        strPtr("targetName"),
		Container:   strPtr("containerName"),
		IpspaceName: strPtr("cbs"),
	}
	expectedCloudTarget := &ontapRest.CloudTarget{CloudTarget: models.CloudTarget{
		Name:      strPtr("targetName"),
		Container: strPtr("container"),
	}}

	mockClient.On("Cloud").Return(mockCloudClient)
	mockCloudClient.On("CloudTargetCreate", expectedParams).Return(nil, nil, nil)
	mockCloudClient.On("CloudTargetGet", strPtr("targetName")).Return(expectedCloudTarget, nil)

	result, err := ontapProvider.CloudTargetCreate("targetName", "containerName", "", "", "cbs")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	mockClient.AssertExpectations(t)
	mockCloudClient.AssertExpectations(t)
}

func TestCloudTargetCreateFailsOnAPIError(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockCloudClient := new(ontapRest.MockCloudClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	ontapProvider := &OntapRestProvider{}

	expectedParams := &ontapRest.CloudTargetCreateParams{
		Name:      strPtr("targetName"),
		Container: strPtr("containerName"),
	}
	expectedError := fmt.Errorf("API error")

	mockClient.On("Cloud").Return(mockCloudClient)
	mockCloudClient.On("CloudTargetCreate", expectedParams).Return(nil, nil, expectedError)

	result, err := ontapProvider.CloudTargetCreate("targetName", "containerName", "", "", "")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, expectedError, err)
}

func TestCloudTargetCreateFails_getOntapClientFuncErr(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, errors.New("OntapClientFunc error")
	}
	ontapProvider := &OntapRestProvider{}
	result, err := ontapProvider.CloudTargetCreate("targetName", "containerName", "", "", "")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "OntapClientFunc error")
}

func TestCloudTargetGetReturnsValidTarget(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockCloudClient := new(ontapRest.MockCloudClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	ontapProvider := &OntapRestProvider{}

	expectedCloudTarget := &ontapRest.CloudTarget{
		CloudTarget: models.CloudTarget{
			Name:      strPtr("targetName"),
			Container: strPtr("container"),
		},
	}

	mockClient.On("Cloud").Return(mockCloudClient)
	mockCloudClient.On("CloudTargetGet", strPtr("targetName")).Return(expectedCloudTarget, nil)

	result, err := ontapProvider.CloudTargetGet(strPtr("targetName"))
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "targetName", *result.Name)
	assert.Equal(t, "container", *result.Container)
}

func TestCloudTargetGetFailsOnInvalidName(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockCloudClient := new(ontapRest.MockCloudClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	ontapProvider := &OntapRestProvider{}

	expectedError := fmt.Errorf("invalid CloudTarget response from API")

	mockClient.On("Cloud").Return(mockCloudClient)
	mockCloudClient.On("CloudTargetGet", strPtr("invalidName")).Return(nil, expectedError)

	result, err := ontapProvider.CloudTargetGet(strPtr("invalidName"))
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, expectedError, err)
}

func TestCloudTargetGetFails_getOntapClientFuncErr(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, errors.New("getOntapClientFunc error")
	}
	ontapProvider := &OntapRestProvider{}

	result, err := ontapProvider.CloudTargetGet(strPtr("invalidName"))
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "getOntapClientFunc error", err.Error())
}

func TestCloudTargetDeleteSucceedsWithJob(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockCloudClient := new(ontapRest.MockCloudClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	ontapProvider := &OntapRestProvider{}

	expectedParams := &ontapRest.CloudTargetDeleteParams{
		UUID: "test-uuid-123",
	}
	expectedJob := &ontapRest.JobAccepted{JobUUID: "job-uuid-456"}

	mockClient.On("Cloud").Return(mockCloudClient)
	mockCloudClient.On("CloudTargetDelete", expectedParams).Return(nil, expectedJob, nil)

	result, err := ontapProvider.CloudTargetDelete("test-uuid-123")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "job-uuid-456", result.JobUUID)
	mockClient.AssertExpectations(t)
	mockCloudClient.AssertExpectations(t)
}

func TestCloudTargetDeleteSucceedsWithoutJob(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockCloudClient := new(ontapRest.MockCloudClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	ontapProvider := &OntapRestProvider{}

	expectedParams := &ontapRest.CloudTargetDeleteParams{
		UUID: "test-uuid-123",
	}

	mockClient.On("Cloud").Return(mockCloudClient)
	mockCloudClient.On("CloudTargetDelete", expectedParams).Return(nil, nil, nil)

	result, err := ontapProvider.CloudTargetDelete("test-uuid-123")
	assert.NoError(t, err)
	assert.Nil(t, result)
	mockClient.AssertExpectations(t)
	mockCloudClient.AssertExpectations(t)
}

func TestCloudTargetDeleteFailsOnAPIError(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockCloudClient := new(ontapRest.MockCloudClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	ontapProvider := &OntapRestProvider{}

	expectedParams := &ontapRest.CloudTargetDeleteParams{
		UUID: "test-uuid-123",
	}
	expectedError := fmt.Errorf("API error")

	mockClient.On("Cloud").Return(mockCloudClient)
	mockCloudClient.On("CloudTargetDelete", expectedParams).Return(nil, nil, expectedError)

	result, err := ontapProvider.CloudTargetDelete("test-uuid-123")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, expectedError, err)
	mockClient.AssertExpectations(t)
	mockCloudClient.AssertExpectations(t)
}

func TestCloudTargetDeleteFailsOnGetOntapClientFuncError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, errors.New("getOntapClientFunc error")
	}
	ontapProvider := &OntapRestProvider{}

	result, err := ontapProvider.CloudTargetDelete("test-uuid-123")
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "getOntapClientFunc error")
}

func TestCloudTargetDeleteWithEmptyUUID(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockCloudClient := new(ontapRest.MockCloudClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	ontapProvider := &OntapRestProvider{}

	expectedParams := &ontapRest.CloudTargetDeleteParams{
		UUID: "",
	}
	expectedJob := &ontapRest.JobAccepted{JobUUID: "job-uuid-789"}

	mockClient.On("Cloud").Return(mockCloudClient)
	mockCloudClient.On("CloudTargetDelete", expectedParams).Return(nil, expectedJob, nil)

	result, err := ontapProvider.CloudTargetDelete("")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "job-uuid-789", result.JobUUID)
	mockClient.AssertExpectations(t)
	mockCloudClient.AssertExpectations(t)
}
