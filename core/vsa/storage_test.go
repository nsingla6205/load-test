package vsa

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestLunResponseFromONTAP(t *testing.T) {
	assert.Nil(t, lunResponseFromONTAP(nil))

	response := lunResponseFromONTAP(&ontaprest.Lun{
		Lun: models.Lun{
			Name:            nillable.ToPointer("/vol/lun1"),
			UUID:            nillable.ToPointer("lun-uuid"),
			SerialNumberHex: nillable.ToPointer("serial"),
			OsType:          nillable.ToPointer("linux"),
			Space: &models.LunInlineSpace{
				Size: nillable.ToPointer(int64(1024)),
			},
			LunInlineLunMaps: []*models.LunInlineLunMapsInlineArrayItem{
				{Igroup: &models.LunInlineLunMapsInlineArrayItemInlineIgroup{Name: nillable.ToPointer("hg1")}},
			},
		},
	})
	require.NotNil(t, response)
	assert.Equal(t, "lun-uuid", response.ExternalUUID)
	assert.Equal(t, "serial", response.SerialNumber)
	assert.Equal(t, int64(1024), response.Size)
	assert.Equal(t, "linux", response.OSType)
	assert.Equal(t, []string{"hg1"}, response.IgroupNames)
}

func TestLunCreate_Success(t *testing.T) {
	mockSAN := new(ontaprest.MockSANClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("SAN").Return(mockSAN)

	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	lunName := "testLun"
	params := LunCreateParams{
		LunName:    lunName,
		SvmName:    "testSVM",
		OsType:     "linux",
		VolumeName: "testVolume",
		Size:       int64(1024),
	}

	mockLun := &ontaprest.Lun{
		Lun: models.Lun{
			Name:            nillable.ToPointer(lunName),
			UUID:            nillable.ToPointer("testUUID"),
			SerialNumberHex: nillable.ToPointer("6c5738423724595454686164"),
			OsType:          nillable.ToPointer("LINUX"),
			Space: &models.LunInlineSpace{
				Size: nillable.ToPointer(int64(1024)),
			},
		},
	}

	mockSAN.On("LunCreate", mock.Anything).Return(mockLun, nil)

	resp, err := rc.LunCreate(params)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, lunName, resp.Name)
	assert.Equal(t, "testUUID", resp.ExternalUUID)

	mockSAN.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestLunCreate_Error(t *testing.T) {
	mockSAN := new(ontaprest.MockSANClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("SAN").Return(mockSAN)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	lunName := "testLun"
	params := LunCreateParams{
		LunName:    lunName,
		SvmName:    "testSVM",
		OsType:     "linux",
		VolumeName: "testVolume",
		Size:       int64(1024),
	}

	mockSAN.On("LunCreate", mock.Anything).Return(nil, errors.New("creation error"))

	resp, err := rc.LunCreate(params)

	assert.Error(t, err)
	assert.Nil(t, resp)

	mockSAN.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestLunCreate_NilOptionalFields(t *testing.T) {
	mockSAN := new(ontaprest.MockSANClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("SAN").Return(mockSAN)

	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	lunName := "testLun"
	params := LunCreateParams{
		LunName:    lunName,
		SvmName:    "testSVM",
		OsType:     "linux",
		VolumeName: "testVolume",
		Size:       int64(1024),
	}

	mockLun := &ontaprest.Lun{
		Lun: models.Lun{
			Name:            nillable.ToPointer(lunName),
			UUID:            nillable.ToPointer("testUUID"),
			SerialNumberHex: nil,
			OsType:          nil,
			Space:           nil,
		},
	}

	mockSAN.On("LunCreate", mock.Anything).Return(mockLun, nil)

	resp, err := rc.LunCreate(params)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, lunName, resp.Name)
	assert.Equal(t, "testUUID", resp.ExternalUUID)
	assert.Equal(t, "", resp.SerialNumber)
	assert.Equal(t, int64(0), resp.Size)
	assert.Equal(t, "", resp.OSType)

	mockSAN.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestLunMapCreate_Success(t *testing.T) {
	mockSAN := new(ontaprest.MockSANClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("SAN").Return(mockSAN)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := LunMapCreateParams{
		LunName:    "testLun",
		SvmName:    "testSVM",
		IGroupName: []string{"iGroupName1", "iGroupName2"},
	}

	mockSAN.On("LunMapCreate", mock.Anything).Return(nil)

	err := rc.LunMapCreate(params)

	assert.NoError(t, err)

	mockSAN.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestLunMapCreate_Error(t *testing.T) {
	mockSAN := new(ontaprest.MockSANClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("SAN").Return(mockSAN)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := LunMapCreateParams{
		LunName:    "testLun",
		SvmName:    "testSVM",
		IGroupName: []string{"iGroupName1", "iGroupName2"},
	}

	mockSAN.On("LunMapCreate", mock.Anything).Return(errors.New("mapping error"))

	err := rc.LunMapCreate(params)

	assert.Error(t, err)

	mockSAN.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestLunMapCreate_ConflictError(t *testing.T) {
	mockSAN := new(ontaprest.MockSANClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("SAN").Return(mockSAN)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := LunMapCreateParams{
		LunName:    "testLun",
		SvmName:    "testSVM",
		IGroupName: []string{"iGroupName1", "iGroupName2"},
	}

	mockSAN.On("LunMapCreate", mock.Anything).Return(errors.New("LUN already mapped to initiator(s) in this group"))

	err := rc.LunMapCreate(params)

	assert.NoError(t, err)

	mockSAN.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestLunMapCreate_OntapClientFuncError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("OntapClientFunc error")
	}
	rc := &OntapRestProvider{}

	params := LunMapCreateParams{
		LunName:    "testLun",
		SvmName:    "testSVM",
		IGroupName: []string{"iGroupName1", "iGroupName2"},
	}

	err := rc.LunMapCreate(params)
	assert.Error(t, err)
	assert.Equal(t, "OntapClientFunc error", err.Error())
}

func TestIsAggregateOnline_Success(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	aggregateName := "testAggregate"
	mockAggregate := &ontaprest.Aggregate{
		Aggregate: models.Aggregate{
			Name:  &aggregateName,
			State: nillable.ToPointer("online"),
		},
	}

	mockStorage.On("AggregateFindByName", mock.Anything).Return(mockAggregate, nil)

	isOnline, err := rc.IsAggregateOnline(aggregateName)

	assert.NoError(t, err)
	assert.True(t, isOnline)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestIsAggregateOnline_NotFound(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	aggregateName := "testAggregate"

	mockStorage.On("AggregateFindByName", mock.Anything).Return(nil, nil)

	isOnline, err := rc.IsAggregateOnline(aggregateName)

	assert.NoError(t, err)
	assert.False(t, isOnline)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestIsAggregateOnline_Error(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	aggregateName := "testAggregate"

	mockStorage.On("AggregateFindByName", mock.Anything).Return(nil, errors.New("API error"))

	isOnline, err := rc.IsAggregateOnline(aggregateName)

	assert.Error(t, err)
	assert.False(t, isOnline)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestIsAggregateOnline_OntapClientFuncError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("OntapClientFunc error")
	}
	rc := &OntapRestProvider{}
	aggregateName := "testAggregate"
	aggregate, err := rc.IsAggregateOnline(aggregateName)

	assert.Error(t, err)
	assert.False(t, aggregate)
	assert.Equal(t, "OntapClientFunc error", err.Error())
}

// Helper function to create mock aggregates for testing
func createMockAggregates(count int, statePattern string) []*ontaprest.Aggregate {
	aggregates := make([]*ontaprest.Aggregate, count)
	for i := 0; i < count; i++ {
		state := "online"
		if statePattern == "mixed" && i%2 == 1 {
			state = "offline"
		} else if statePattern == "offline" {
			state = "offline"
		}

		aggregates[i] = &ontaprest.Aggregate{
			Aggregate: models.Aggregate{
				Name:        nillable.ToPointer(fmt.Sprintf("aggr%d", i+1)),
				State:       nillable.ToPointer(state),
				VolumeCount: nillable.ToPointer(int64(i * 5)),
			},
		}
	}
	return aggregates
}

// Helper function to create custom aggregates with specific states
func createCustomAggregates(configs []struct {
	name, state string
	volumeCount int64
}) []*ontaprest.Aggregate {
	aggregates := make([]*ontaprest.Aggregate, len(configs))
	for i, config := range configs {
		aggregates[i] = &ontaprest.Aggregate{
			Aggregate: models.Aggregate{
				Name:        nillable.ToPointer(config.name),
				State:       nillable.ToPointer(config.state),
				VolumeCount: nillable.ToPointer(config.volumeCount),
			},
		}
	}
	return aggregates
}

// Helper function to setup mock storage with aggregate callback
func setupMockStorageWithAggregates(aggregates []*ontaprest.Aggregate) (*ontaprest.MockStorageClient, *ontaprest.MockRESTClient) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)

	mockStorage.On("AggregateCollectionGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Aggregate])
		err := callback(aggregates)
		if err != nil {
			return
		}
	}).Return(nil)

	return mockStorage, mockClient
}

func TestGetAggregates_Success(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	rc := &OntapRestProvider{}

	// Use helper function to create mock aggregates
	mockAggregates := createMockAggregates(5, "online")
	mockStorage, mockClient := setupMockStorageWithAggregates(mockAggregates)

	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}

	result, err := rc.GetAggregates()

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 5)

	// Verify first aggregate
	assert.Equal(t, "aggr1", result[0].Name)
	assert.Equal(t, "online", result[0].State)
	assert.Equal(t, int64(0), result[0].VolumeCount)

	// Verify second aggregate
	assert.Equal(t, "aggr2", result[1].Name)
	assert.Equal(t, "online", result[1].State)
	assert.Equal(t, int64(5), result[1].VolumeCount)

	// Verify last aggregate
	assert.Equal(t, "aggr5", result[4].Name)
	assert.Equal(t, "online", result[4].State)
	assert.Equal(t, int64(20), result[4].VolumeCount)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetAggregates_EmptyResult(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	rc := &OntapRestProvider{}

	// Use helper to create empty aggregates list
	mockAggregates := createMockAggregates(0, "online")
	mockStorage, mockClient := setupMockStorageWithAggregates(mockAggregates)

	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}

	result, err := rc.GetAggregates()

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 0)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetAggregates_AggregateCollectionGetError(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	// Mock AggregateCollectionGet to return an error
	mockStorage.On("AggregateCollectionGet", mock.Anything, mock.Anything).Return(errors.New("API error"))

	result, err := rc.GetAggregates()

	assert.Error(t, err)
	assert.Nil(t, result)
	// Verify it's wrapped in CustomError
	assert.EqualError(t, err, "An internal error occurred.")
	var customErr *vsaerrors.CustomError
	if vsaerrors.As(err, &customErr) {
		assert.Equal(t, customErr.OriginalErr.Error(), "API error")
		assert.Equal(t, customErr.HttpCode, nillable.ToPointer(500))
		assert.Equal(t, customErr.TrackingID, 5006)
		assert.Equal(t, customErr.Message, "An internal error occurred.")
		assert.Equal(t, customErr.Retriable, false)
	} else {
		t.Fatalf("Expected a CustomError, got %T", err)
	}

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetAggregates_OntapClientFuncError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("OntapClientFunc error")
	}
	rc := &OntapRestProvider{}

	result, err := rc.GetAggregates()

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "OntapClientFunc error", err.Error())
}

func TestGetAggregates_CorrectFieldsRequested(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	// Mock the AggregateCollectionGet call and verify the fields parameter
	mockStorage.On("AggregateCollectionGet", mock.MatchedBy(func(params *ontaprest.AggregateCollectionGetParams) bool {
		// Verify that the correct fields are requested
		expectedFields := []string{"name", "uuid", "state", "volume-count", "space"}
		if len(params.Fields) != len(expectedFields) {
			return false
		}
		for i, field := range expectedFields {
			if params.Fields[i] != field {
				return false
			}
		}
		return true
	}), mock.Anything).Run(func(args mock.Arguments) {
		// Get the callback function with correct type and call it with empty data
		callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Aggregate])
		err := callback([]*ontaprest.Aggregate{})
		if err != nil {
			return
		}
	}).Return(nil)

	result, err := rc.GetAggregates()

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 0)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetAggregates_WithMixedStates(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	rc := &OntapRestProvider{}

	// Use helper to create aggregates with mixed states (online/offline alternating)
	mockAggregates := createMockAggregates(6, "mixed")
	mockStorage, mockClient := setupMockStorageWithAggregates(mockAggregates)

	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}

	result, err := rc.GetAggregates()

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 6)

	// Verify alternating states
	assert.Equal(t, "online", result[0].State)  // aggr1
	assert.Equal(t, "offline", result[1].State) // aggr2
	assert.Equal(t, "online", result[2].State)  // aggr3
	assert.Equal(t, "offline", result[3].State) // aggr4

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetAggregates_WithCustomConfiguration(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	rc := &OntapRestProvider{}

	// Use helper to create custom aggregates configuration
	customConfigs := []struct {
		name, state string
		volumeCount int64
	}{
		{"production-aggr", "online", 100},
		{"staging-aggr", "offline", 50},
		{"test-aggr", "online", 0},
	}
	mockAggregates := createCustomAggregates(customConfigs)
	mockStorage, mockClient := setupMockStorageWithAggregates(mockAggregates)

	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}

	result, err := rc.GetAggregates()

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 3)

	// Verify custom configurations
	assert.Equal(t, "production-aggr", result[0].Name)
	assert.Equal(t, "online", result[0].State)
	assert.Equal(t, int64(100), result[0].VolumeCount)

	assert.Equal(t, "staging-aggr", result[1].Name)
	assert.Equal(t, "offline", result[1].State)
	assert.Equal(t, int64(50), result[1].VolumeCount)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetAggregateByName_Success(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	aggregateName := "testAggregate"
	mockAggregate := &ontaprest.Aggregate{
		Aggregate: models.Aggregate{
			Name:  &aggregateName,
			State: nillable.ToPointer("online"),
			UUID:  nillable.ToPointer("uuid"),
		},
	}

	mockStorage.On("AggregateFindByName", mock.Anything).Return(mockAggregate, nil)

	aggregate, err := rc.GetAggregateByName(aggregateName)

	assert.NoError(t, err)
	assert.NotNil(t, aggregate)
	assert.Equal(t, aggregateName, aggregate.Name)
	assert.Equal(t, "online", aggregate.State)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetAggregateByName_NotFound(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	aggregateName := "testAggregate"

	mockStorage.On("AggregateFindByName", mock.Anything).Return(nil, nil)

	aggregate, err := rc.GetAggregateByName(aggregateName)

	assert.Error(t, err)
	assert.Nil(t, aggregate)
	assert.Equal(t, "aggregate not found", err.Error())

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetAggregateByName_Error(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	aggregateName := "testAggregate"

	mockStorage.On("AggregateFindByName", mock.Anything).Return(nil, errors.New("API error"))

	aggregate, err := rc.GetAggregateByName(aggregateName)

	assert.Error(t, err)
	assert.Nil(t, aggregate)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestGetAggregateByName_OntapClientFuncError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("OntapClientFunc error")
	}
	rc := &OntapRestProvider{}
	aggregateName := "testAggregate"
	aggregate, err := rc.GetAggregateByName(aggregateName)

	assert.Error(t, err)
	assert.Nil(t, aggregate)
	assert.Equal(t, "OntapClientFunc error", err.Error())
}

func TestIscsiServiceCreate(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	t.Run("WhenIscsiServiceIsCreatedSuccessfully_ThenReturnNil", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("IscsiServiceCreate", mock.Anything).Return(nil)

		err := rc.IscsiServiceCreate("testSvmUUID")

		assert.NoError(tt, err)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenIscsiServiceCreationFails_ThenReturnError", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)

		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("IscsiServiceCreate", mock.Anything).Return(errors.New("creation error"))

		err := rc.IscsiServiceCreate("testSvmUUID")

		assert.Error(tt, err)
		assert.Equal(tt, "creation error", err.Error())

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenOntapClientFunc_ThenReturnError", func(tt *testing.T) {
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("OntapClientFunc error")
		}
		rc := &OntapRestProvider{}

		err := rc.IscsiServiceCreate("testSvmUUID")

		assert.Error(tt, err)
		assert.Equal(tt, "OntapClientFunc error", err.Error())
	})
}

func TestGetOntapClient(t *testing.T) {
	t.Run("WhenValidClientParamsProvided_ThenReturnOntapRestClient", func(tt *testing.T) {
		hostMap := map[string]string{}
		hostMap["192.168.1.1"] = "192.168.1.0"
		clientParams := ontaprest.RESTClientParams{
			Host:     "192.168.1.0",
			Hosts:    hostMap,
			Password: "test-password",
			Trace:    log.NewLogger().(*log.Slogger),
		}
		orginalTestConnection := ontaprest.TestConnection
		defer func() {
			ontaprest.TestConnection = orginalTestConnection // Reset to original after test
		}()

		ontaprest.TestConnection = func(params *ontaprest.OntapRestClient) error {
			return nil
		}

		client, err := getOntapClient(clientParams)

		assert.NoError(tt, err)
		assert.NotNil(tt, client)
		assert.Equal(tt, clientParams.Host, client.Host())
	})
}

func TestLunGet(t *testing.T) {
	t.Run("WhenLunIsFound_ThenReturnLunResponse", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockLun := &ontaprest.Lun{
			Lun: models.Lun{
				Name:            nillable.ToPointer("testLun"),
				UUID:            nillable.ToPointer("uuid-123"),
				SerialNumberHex: nillable.ToPointer("6c5738423724595454686164"),
				OsType:          nillable.ToPointer("LINUX"),
				Space: &models.LunInlineSpace{
					Size: nillable.ToPointer(int64(1024)),
				},
			},
		}

		param := &ontaprest.LunGetParams{
			BaseParams: ontaprest.BaseParams{
				Fields: []string{"status.*", "serial_number_hex", "class", "space.size", "location.*", "os_type", "lun_maps"},
			},
			SvmName:    nillable.GetStringPtr("testSVM"),
			VolumeName: nillable.GetStringPtr("testVol"),
			LunName:    nillable.GetStringPtr("testLun"),
		}

		mockSAN.On("LunGet", param).Return([]*ontaprest.Lun{mockLun}, nil)

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunGet(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, "testLun", resp.Name)
		assert.Equal(tt, "uuid-123", resp.ExternalUUID)
		assert.Equal(tt, "6c5738423724595454686164", resp.SerialNumber)
		assert.Equal(tt, "LINUX", resp.OSType)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenLunIsNotFound_ThenReturnError", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return(nil, nil)

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunGet(params)

		assert.Nil(tt, resp)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "An internal error occurred.")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Equal(tt, customErr.OriginalErr.Error(), "lun 'svm=testSVM, volume=testVol, lun=testLun' not found")
			assert.True(tt, errors.IsNotFoundErrForObjectTypeInChain(err, "lun"))
			assert.Equal(tt, customErr.HttpCode, nillable.ToPointer(500))
			assert.Equal(tt, customErr.TrackingID, 5006)
			assert.Equal(tt, customErr.Message, "An internal error occurred.")
			assert.Equal(tt, customErr.Retriable, false)
		} else {
			tt.Fatalf("Expected a CustomError, got %T", err)
		}
		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenLunGetReturnsError_ThenReturnError", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return(nil, errors.New("fetch error"))

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunGet(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.EqualError(tt, err, "An internal error occurred.")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Equal(tt, customErr.OriginalErr.Error(), "fetch error")
			assert.Equal(tt, customErr.HttpCode, nillable.ToPointer(500))
			assert.Equal(tt, customErr.TrackingID, 5006)
			assert.Equal(tt, customErr.Message, "An internal error occurred.")
			assert.Equal(tt, customErr.Retriable, false)
		} else {
			tt.Fatalf("Expected a CustomError, got %T", err)
		}
		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenLunGetReturnsError_OntapClientFuncError", func(tt *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("OntapClientFunc error")
		}
		rc := &OntapRestProvider{}

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunGet(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, "OntapClientFunc error", err.Error())
	})
}

func TestLunsGet(t *testing.T) {
	t.Run("WhenMultipleLunsAreFound_ThenReturnLunResponseSlice", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockLun1 := &ontaprest.Lun{
			Lun: models.Lun{
				Name:            nillable.ToPointer("testLun1"),
				UUID:            nillable.ToPointer("uuid-123"),
				SerialNumberHex: nillable.ToPointer("6c5738423724595454686164"),
				OsType:          nillable.ToPointer("LINUX"),
				Space: &models.LunInlineSpace{
					Size: nillable.ToPointer(int64(1024)),
				},
			},
		}

		mockLun2 := &ontaprest.Lun{
			Lun: models.Lun{
				Name:            nillable.ToPointer("testLun2"),
				UUID:            nillable.ToPointer("uuid-456"),
				SerialNumberHex: nillable.ToPointer("7d6849534835606565797265"),
				OsType:          nillable.ToPointer("WINDOWS"),
				Space: &models.LunInlineSpace{
					Size: nillable.ToPointer(int64(2048)),
				},
			},
		}

		mockSAN.On("LunGet", mock.Anything).Return([]*ontaprest.Lun{mockLun1, mockLun2}, nil)

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "",
		}
		resp, err := rc.LunList(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Len(tt, resp, 2)
		assert.Equal(tt, "testLun1", resp[0].Name)
		assert.Equal(tt, "uuid-123", resp[0].ExternalUUID)
		assert.Equal(tt, "6c5738423724595454686164", resp[0].SerialNumber)
		assert.Equal(tt, int64(1024), resp[0].Size)
		assert.Equal(tt, "LINUX", resp[0].OSType)

		assert.Equal(tt, "testLun2", resp[1].Name)
		assert.Equal(tt, "uuid-456", resp[1].ExternalUUID)
		assert.Equal(tt, "7d6849534835606565797265", resp[1].SerialNumber)
		assert.Equal(tt, int64(2048), resp[1].Size)
		assert.Equal(tt, "WINDOWS", resp[1].OSType)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenSingleLunIsFound_ThenReturnLunResponseSlice", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockLun := &ontaprest.Lun{
			Lun: models.Lun{
				Name:            nillable.ToPointer("testLun"),
				UUID:            nillable.ToPointer("uuid-123"),
				SerialNumberHex: nillable.ToPointer("6c5738423724595454686164"),
				OsType:          nillable.ToPointer("LINUX"),
				Space: &models.LunInlineSpace{
					Size: nillable.ToPointer(int64(1024)),
				},
			},
		}

		mockSAN.On("LunGet", mock.Anything).Return([]*ontaprest.Lun{mockLun}, nil)

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunList(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Len(tt, resp, 1)
		assert.Equal(tt, "testLun", resp[0].Name)
		assert.Equal(tt, "uuid-123", resp[0].ExternalUUID)
		assert.Equal(tt, "6c5738423724595454686164", resp[0].SerialNumber)
		assert.Equal(tt, int64(1024), resp[0].Size)
		assert.Equal(tt, "LINUX", resp[0].OSType)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenLunHasNilOsType_ThenReturnEmptyString", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockLun := &ontaprest.Lun{
			Lun: models.Lun{
				Name:            nillable.ToPointer("testLun"),
				UUID:            nillable.ToPointer("uuid-123"),
				SerialNumberHex: nillable.ToPointer("6c5738423724595454686164"),
				OsType:          nil,
				Space: &models.LunInlineSpace{
					Size: nillable.ToPointer(int64(1024)),
				},
			},
		}

		mockSAN.On("LunGet", mock.Anything).Return([]*ontaprest.Lun{mockLun}, nil)

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunList(params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Len(tt, resp, 1)
		assert.Equal(tt, "testLun", resp[0].Name)
		assert.Equal(tt, "", resp[0].OSType)

		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenNoLunsAreFound_ThenReturnError", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return([]*ontaprest.Lun{}, nil)

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunList(params)

		assert.Nil(tt, resp)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "An internal error occurred.")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Equal(tt, customErr.OriginalErr.Error(), "lun 'svm=testSVM, volume=testVol, lun=testLun' not found")
			assert.True(tt, errors.IsNotFoundErrForObjectTypeInChain(err, "lun"))
			assert.Equal(tt, customErr.HttpCode, nillable.ToPointer(500))
			assert.Equal(tt, customErr.TrackingID, 5006)
			assert.Equal(tt, customErr.Message, "An internal error occurred.")
			assert.Equal(tt, customErr.Retriable, false)
		} else {
			tt.Fatalf("Expected a CustomError, got %T", err)
		}
		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenLunGetReturnsNil_ThenReturnError", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return(nil, nil)

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunList(params)

		assert.Nil(tt, resp)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "An internal error occurred.")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Equal(tt, customErr.OriginalErr.Error(), "lun 'svm=testSVM, volume=testVol, lun=testLun' not found")
			assert.True(tt, errors.IsNotFoundErrForObjectTypeInChain(err, "lun"))
			assert.Equal(tt, customErr.HttpCode, nillable.ToPointer(500))
			assert.Equal(tt, customErr.TrackingID, 5006)
			assert.Equal(tt, customErr.Message, "An internal error occurred.")
			assert.Equal(tt, customErr.Retriable, false)
		} else {
			tt.Fatalf("Expected a CustomError, got %T", err)
		}
		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenLunGetReturnsError_ThenReturnError", func(tt *testing.T) {
		mockSAN := new(ontaprest.MockSANClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("SAN").Return(mockSAN)
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
		rc := &OntapRestProvider{}

		mockSAN.On("LunGet", mock.Anything).Return(nil, errors.New("fetch error"))

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunList(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.EqualError(tt, err, "An internal error occurred.")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Equal(tt, customErr.OriginalErr.Error(), "fetch error")
			assert.Equal(tt, customErr.HttpCode, nillable.ToPointer(500))
			assert.Equal(tt, customErr.TrackingID, 5006)
			assert.Equal(tt, customErr.Message, "An internal error occurred.")
			assert.Equal(tt, customErr.Retriable, false)
		} else {
			tt.Fatalf("Expected a CustomError, got %T", err)
		}
		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenOntapClientFuncError_ThenReturnError", func(tt *testing.T) {
		originalgetOntapClientFunc := getOntapClientFunc
		defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("OntapClientFunc error")
		}
		rc := &OntapRestProvider{}

		params := LunGetParams{
			SvmName:    "testSVM",
			VolumeName: "testVol",
			LunName:    "testLun",
		}
		resp, err := rc.LunList(params)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.Equal(tt, "OntapClientFunc error", err.Error())
	})
}

func TestLunUpdate(t *testing.T) {
	mockSAN := new(ontaprest.MockSANClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("SAN").Return(mockSAN)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := LunUpdateParams{
		UUID:       "uuid-1",
		LunName:    "lun-1",
		SvmName:    "svm-1",
		VolumeName: "vol-1",
		Size:       int64(1024),
	}

	t.Run("WhenLunUpdateReturnsError", func(tt *testing.T) {
		mockSAN.On("LunUpdate", mock.Anything).Return(false, nil, errors.New("update error")).Once()
		err := rc.LunUpdate(params)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Error updating volume - Cannot update the volume with the specified size. Please increase the volume size.")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Equal(tt, customErr.OriginalErr.Error(), "update error")
			assert.Equal(tt, customErr.HttpCode, nillable.ToPointer(400))
			assert.Equal(tt, customErr.TrackingID, 7007)
			assert.Equal(tt, customErr.Message, "Error updating volume - Cannot update the volume with the specified size. Please increase the volume size.")
			assert.Equal(tt, customErr.Retriable, false)
		} else {
			tt.Fatalf("Expected a CustomError, got %T", err)
		}
		mockSAN.AssertExpectations(tt)
	})

	t.Run("WhenLunUpdateSuccessTrue", func(tt *testing.T) {
		mockSAN.On("LunUpdate", mock.Anything).Return(true, nil, nil).Once()
		err := rc.LunUpdate(params)
		assert.NoError(tt, err)
		mockSAN.AssertExpectations(tt)
	})

	t.Run("WhenLunUpdateSuccessFalseAndPollSucceeds", func(tt *testing.T) {
		mockSAN.On("LunUpdate", mock.Anything).Return(false, &ontaprest.JobAccepted{JobUUID: "job-1"}, nil).Once()
		mockClient.On("Poll", "job-1").Return(nil).Once()
		err := rc.LunUpdate(params)
		assert.NoError(tt, err)
		mockSAN.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenLunUpdateReturnsConflictError", func(tt *testing.T) {
		mockSAN.On("LunUpdate", mock.Anything).Return(false, nil, errors.New("New LUN size is the same as the old LUN size")).Once()
		err := rc.LunUpdate(params)
		assert.Error(tt, err)
		assert.True(tt, errors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "already has the specified size")
		mockSAN.AssertExpectations(tt)
	})

	t.Run("WhenLunUpdateReturnsOntapClientFuncError", func(tt *testing.T) {
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("OntapClientFunc error")
		}
		err := rc.LunUpdate(params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "OntapClientFunc error")
	})
}

func TestSnapshotGet(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	snapshotUUID := "testSnapshotUUID"
	volumeUUID := "testVolumeUUID"
	snapshotName := "testSnapshot"
	mockSnapshot := &ontaprest.Snapshot{Snapshot: models.Snapshot{Name: &snapshotName, UUID: &snapshotUUID}}

	mockStorage.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)

	snapshot, err := rc.SnapshotGet(snapshotUUID, volumeUUID, snapshotName)

	assert.NoError(t, err)
	assert.NotNil(t, snapshot)
	assert.Equal(t, snapshotName, *snapshot.Name)
	assert.Equal(t, snapshotUUID, *snapshot.UUID)

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestSnapshotGet_OntapClientFuncErr(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("OntapClientFunc error")
	}
	rc := &OntapRestProvider{}
	snapshot, err := rc.SnapshotGet("testSnapshotUUID", "testVolumeUUID", "testSnapshot")

	assert.Error(t, err)
	assert.Nil(t, snapshot)
	assert.Contains(t, err.Error(), "OntapClientFunc error")
}

func TestUpdateAggregate_Success_SyncResponse(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateAggregateParams{
		UUID:                     "test-uuid",
		TieringFullnessThreshold: 75,
	}

	// Mock sync response with NumRecords > 0
	mockAggregateSimulate := &ontaprest.AggregateSimulate{}
	mockAggregateSimulate.NumRecords = nillable.ToPointer(int64(1))

	mockStorage.On("AggregateModify", mock.Anything).Return(mockAggregateSimulate, nil, nil)

	err := rc.UpdateAggregate(params)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateAggregate_Success_AsyncResponse(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateAggregateParams{
		UUID:                     "test-uuid",
		TieringFullnessThreshold: 50,
	}

	// Mock async response with job
	mockJob := &ontaprest.JobAccepted{JobUUID: "test-job-uuid"}
	mockStorage.On("AggregateModify", mock.Anything).Return(nil, mockJob, nil)
	mockClient.On("Poll", "test-job-uuid").Return(nil)

	err := rc.UpdateAggregate(params)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateAggregate_Success_SyncResponseWithZeroRecords(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateAggregateParams{
		UUID:                     "test-uuid",
		TieringFullnessThreshold: 80,
	}

	// Mock sync response with NumRecords = 0, should fall back to polling
	mockAggregateSimulate := &ontaprest.AggregateSimulate{}
	mockAggregateSimulate.NumRecords = nillable.ToPointer(int64(0))
	mockJob := &ontaprest.JobAccepted{JobUUID: "test-job-uuid-2"}

	mockStorage.On("AggregateModify", mock.Anything).Return(mockAggregateSimulate, mockJob, nil)
	mockClient.On("Poll", "test-job-uuid-2").Return(nil)

	err := rc.UpdateAggregate(params)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateAggregate_Success_SyncResponseWithNilNumRecords(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateAggregateParams{
		UUID:                     "test-uuid",
		TieringFullnessThreshold: 90,
	}

	// Mock sync response with nil NumRecords, should fall back to polling
	mockAggregateSimulate := &ontaprest.AggregateSimulate{}
	mockAggregateSimulate.NumRecords = nil // nil NumRecords
	mockJob := &ontaprest.JobAccepted{JobUUID: "test-job-uuid-3"}

	mockStorage.On("AggregateModify", mock.Anything).Return(mockAggregateSimulate, mockJob, nil)
	mockClient.On("Poll", "test-job-uuid-3").Return(nil)

	err := rc.UpdateAggregate(params)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateAggregate_Error_AggregateModifyFails(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateAggregateParams{
		UUID:                     "test-uuid",
		TieringFullnessThreshold: 75,
	}

	mockStorage.On("AggregateModify", mock.Anything).Return(nil, nil, errors.New("API error"))

	err := rc.UpdateAggregate(params)

	assert.Error(t, err)
	assert.Equal(t, "API error", err.Error())
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateAggregate_Error_PollingFails(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateAggregateParams{
		UUID:                     "test-uuid",
		TieringFullnessThreshold: 60,
	}

	// Mock async response with job that fails polling
	mockJob := &ontaprest.JobAccepted{JobUUID: "test-job-uuid-fail"}
	mockStorage.On("AggregateModify", mock.Anything).Return(nil, mockJob, nil)
	mockClient.On("Poll", "test-job-uuid-fail").Return(errors.New("polling failed"))

	err := rc.UpdateAggregate(params)

	assert.Error(t, err)
	assert.Equal(t, "polling failed", err.Error())
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateAggregate_Error_OntapClientFuncError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return nil, errors.New("OntapClientFunc error")
	}
	rc := &OntapRestProvider{}

	params := UpdateAggregateParams{
		UUID:                     "test-uuid",
		TieringFullnessThreshold: 75,
	}

	err := rc.UpdateAggregate(params)

	assert.Error(t, err)
	assert.Equal(t, "OntapClientFunc error", err.Error())
}

func TestUpdateAggregate_ParameterValidation(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	t.Run("ValidateCorrectParametersArePassed", func(tt *testing.T) {
		params := UpdateAggregateParams{
			UUID:                     "test-uuid-123",
			TieringFullnessThreshold: 85,
		}

		// Mock the AggregateModify call and verify the parameters
		mockAggregateSimulate := &ontaprest.AggregateSimulate{}
		mockAggregateSimulate.NumRecords = nillable.ToPointer(int64(1))

		mockStorage.On("AggregateModify", mock.MatchedBy(func(modifyParams *ontaprest.AggregateModifyParams) bool {
			return modifyParams.UUID == "test-uuid-123" &&
				modifyParams.TieringFullnessThreshold != nil &&
				*modifyParams.TieringFullnessThreshold == int64(85)
		})).Return(mockAggregateSimulate, nil, nil).Once()

		err := rc.UpdateAggregate(params)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestUpdateAggregate_BoundaryValues(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	testCases := []struct {
		name                     string
		tieringFullnessThreshold int64
	}{
		{"Zero threshold", 0},
		{"Minimum valid threshold", 1},
		{"Maximum valid threshold", 100},
		{"Large threshold value", 999},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(tt *testing.T) {
			params := UpdateAggregateParams{
				UUID:                     "test-uuid",
				TieringFullnessThreshold: tc.tieringFullnessThreshold,
			}

			mockAggregateSimulate := &ontaprest.AggregateSimulate{}
			mockAggregateSimulate.NumRecords = nillable.ToPointer(int64(1))

			mockStorage.On("AggregateModify", mock.Anything).Return(mockAggregateSimulate, nil, nil).Once()

			err := rc.UpdateAggregate(params)

			assert.NoError(tt, err)
		})
	}

	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestUpdateAggregate_EmptyUUID(t *testing.T) {
	mockStorage := new(ontaprest.MockStorageClient)
	mockClient := new(ontaprest.MockRESTClient)
	mockClient.On("Storage").Return(mockStorage)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()
	getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
		return mockClient, nil
	}
	rc := &OntapRestProvider{}

	params := UpdateAggregateParams{
		UUID:                     "", // Empty UUID
		TieringFullnessThreshold: 75,
	}

	// The function should still work with empty UUID (ONTAP might handle validation)
	mockAggregateSimulate := &ontaprest.AggregateSimulate{}
	mockAggregateSimulate.NumRecords = nillable.ToPointer(int64(1))

	mockStorage.On("AggregateModify", mock.Anything).Return(mockAggregateSimulate, nil, nil)

	err := rc.UpdateAggregate(params)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestNamespaceList(t *testing.T) {
	makeNVMeMocks := func() (*ontaprest.MockNVMeClient, *ontaprest.MockRESTClient) {
		mockNVMe := new(ontaprest.MockNVMeClient)
		mockClient := new(ontaprest.MockRESTClient)
		mockClient.On("NVMe").Return(mockNVMe)
		return mockNVMe, mockClient
	}

	setupClientFunc := func(tt *testing.T, mockClient *ontaprest.MockRESTClient) {
		original := getOntapClientFunc
		tt.Cleanup(func() { getOntapClientFunc = original })
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return mockClient, nil
		}
	}

	t.Run("WhenOntapClientFuncFails_ThenReturnError", func(tt *testing.T) {
		original := getOntapClientFunc
		tt.Cleanup(func() { getOntapClientFunc = original })
		getOntapClientFunc = func(params ontaprest.RESTClientParams) (ontaprest.RESTClient, error) {
			return nil, errors.New("client init error")
		}

		rc := &OntapRestProvider{}
		resp, err := rc.NamespaceList(NvmeNamespaceGetParams{SvmName: "svm1", VolumeName: "vol1"})

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.EqualError(tt, err, "client init error")
	})

	t.Run("WhenNamespaceGetReturnsError_ThenReturnVCPError", func(tt *testing.T) {
		mockNVMe, mockClient := makeNVMeMocks()
		setupClientFunc(tt, mockClient)
		rc := &OntapRestProvider{}

		mockNVMe.On("NamespaceGet", mock.Anything).Return(nil, errors.New("ontap api failure"))

		resp, err := rc.NamespaceList(NvmeNamespaceGetParams{SvmName: "svm1", VolumeName: "vol1"})

		assert.Error(tt, err)
		assert.Nil(tt, resp)
		assert.EqualError(tt, err, "An internal error occurred.")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Equal(tt, "ontap api failure", customErr.OriginalErr.Error())
			assert.Equal(tt, vsaerrors.ErrOntapRestAPIError, customErr.TrackingID)
		} else {
			tt.Fatalf("expected CustomError, got %T", err)
		}
		mockNVMe.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenOneNamespaceFound_ThenReturnMappedResponse", func(tt *testing.T) {
		mockNVMe, mockClient := makeNVMeMocks()
		setupClientFunc(tt, mockClient)
		rc := &OntapRestProvider{}

		ns := &ontaprest.NvmeNamespace{
			NvmeNamespace: models.NvmeNamespace{
				Name: nillable.ToPointer("/vol/vol1/ns1"),
				UUID: nillable.ToPointer("ns-uuid-1"),
			},
		}
		mockNVMe.On("NamespaceGet", mock.Anything).Return([]*ontaprest.NvmeNamespace{ns}, nil)

		resp, err := rc.NamespaceList(NvmeNamespaceGetParams{SvmName: "svm1", VolumeName: "vol1"})

		assert.NoError(tt, err)
		assert.Len(tt, resp, 1)
		assert.Equal(tt, "/vol/vol1/ns1", resp[0].Name)
		assert.Equal(tt, "ns-uuid-1", resp[0].ExternalUUID)
		mockNVMe.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenMultipleNamespacesFound_ThenReturnAllMapped", func(tt *testing.T) {
		mockNVMe, mockClient := makeNVMeMocks()
		setupClientFunc(tt, mockClient)
		rc := &OntapRestProvider{}

		namespaces := []*ontaprest.NvmeNamespace{
			{NvmeNamespace: models.NvmeNamespace{Name: nillable.ToPointer("/vol/vol1/ns1"), UUID: nillable.ToPointer("uuid-1")}},
			{NvmeNamespace: models.NvmeNamespace{Name: nillable.ToPointer("/vol/vol1/ns2"), UUID: nillable.ToPointer("uuid-2")}},
		}
		mockNVMe.On("NamespaceGet", mock.Anything).Return(namespaces, nil)

		resp, err := rc.NamespaceList(NvmeNamespaceGetParams{SvmName: "svm1", VolumeName: "vol1"})

		assert.NoError(tt, err)
		assert.Len(tt, resp, 2)
		assert.Equal(tt, "/vol/vol1/ns1", resp[0].Name)
		assert.Equal(tt, "uuid-1", resp[0].ExternalUUID)
		assert.Equal(tt, "/vol/vol1/ns2", resp[1].Name)
		assert.Equal(tt, "uuid-2", resp[1].ExternalUUID)
		mockNVMe.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenNamespaceHasNilFields_ThenReturnEmptyStrings", func(tt *testing.T) {
		mockNVMe, mockClient := makeNVMeMocks()
		setupClientFunc(tt, mockClient)
		rc := &OntapRestProvider{}

		ns := &ontaprest.NvmeNamespace{
			NvmeNamespace: models.NvmeNamespace{Name: nil, UUID: nil},
		}
		mockNVMe.On("NamespaceGet", mock.Anything).Return([]*ontaprest.NvmeNamespace{ns}, nil)

		resp, err := rc.NamespaceList(NvmeNamespaceGetParams{SvmName: "svm1", VolumeName: "vol1"})

		assert.NoError(tt, err)
		assert.Len(tt, resp, 1)
		assert.Equal(tt, "", resp[0].Name)
		assert.Equal(tt, "", resp[0].ExternalUUID)
		mockNVMe.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenNamespaceNameParamIsSet_ThenPassedToNamespaceGet", func(tt *testing.T) {
		mockNVMe, mockClient := makeNVMeMocks()
		setupClientFunc(tt, mockClient)
		rc := &OntapRestProvider{}

		ns := &ontaprest.NvmeNamespace{
			NvmeNamespace: models.NvmeNamespace{
				Name: nillable.ToPointer("/vol/vol1/ns1"),
				UUID: nillable.ToPointer("ns-uuid-1"),
			},
		}

		svmName := "svm1"
		volName := "vol1"
		nsName := "ns1"
		mockNVMe.On("NamespaceGet", &ontaprest.NvmeNamespaceGetParams{
			BaseParams:    ontaprest.BaseParams{Fields: []string{"name", "uuid", "svm", "location"}},
			SvmName:       &svmName,
			VolumeName:    &volName,
			NamespaceName: &nsName,
		}).Return([]*ontaprest.NvmeNamespace{ns}, nil)

		resp, err := rc.NamespaceList(NvmeNamespaceGetParams{SvmName: "svm1", VolumeName: "vol1", NamespaceName: "ns1"})

		assert.NoError(tt, err)
		assert.Len(tt, resp, 1)
		assert.Equal(tt, "/vol/vol1/ns1", resp[0].Name)
		mockNVMe.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})

	t.Run("WhenNamespaceNameParamIsEmpty_ThenNilPassedToNamespaceGet", func(tt *testing.T) {
		mockNVMe, mockClient := makeNVMeMocks()
		setupClientFunc(tt, mockClient)
		rc := &OntapRestProvider{}

		ns := &ontaprest.NvmeNamespace{
			NvmeNamespace: models.NvmeNamespace{
				Name: nillable.ToPointer("/vol/vol1/ns1"),
				UUID: nillable.ToPointer("ns-uuid-1"),
			},
		}

		svmName := "svm1"
		volName := "vol1"
		mockNVMe.On("NamespaceGet", &ontaprest.NvmeNamespaceGetParams{
			BaseParams:    ontaprest.BaseParams{Fields: []string{"name", "uuid", "svm", "location"}},
			SvmName:       &svmName,
			VolumeName:    &volName,
			NamespaceName: nil,
		}).Return([]*ontaprest.NvmeNamespace{ns}, nil)

		resp, err := rc.NamespaceList(NvmeNamespaceGetParams{SvmName: "svm1", VolumeName: "vol1", NamespaceName: ""})

		assert.NoError(tt, err)
		assert.Len(tt, resp, 1)
		mockNVMe.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
	})
}
