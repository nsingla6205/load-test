package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

const testOperationRequestID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

const testOperationTimeout = "3600s"

func testOperationDeadline() time.Time {
	return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
}

func testOperationRequestParams() vcmserver.V1CreateVolumeParams {
	return vcmserver.V1CreateVolumeParams{
		RequestId: testOperationRequestID,
		Timeout:   testOperationTimeout,
	}
}

func testOperationUpdateParams() vcmserver.V1UpdateVolumeParams {
	return testOperationUpdateParamsWithMask("sizeInBytes")
}

func testOperationUpdateParamsWithMask(mask string) vcmserver.V1UpdateVolumeParams {
	return vcmserver.V1UpdateVolumeParams{
		UUID:       testVolumeUUID,
		RequestId:  testOperationRequestID,
		Timeout:    testOperationTimeout,
		UpdateMask: mask,
	}
}

func testOperationDeleteParams(uuid string) vcmserver.V1DeleteVolumeParams {
	return vcmserver.V1DeleteVolumeParams{
		UUID:      uuid,
		RequestId: testOperationRequestID,
		Timeout:   testOperationTimeout,
	}
}

func testOperationIgroupCreateParams() vcmserver.V1CreateIgroupParams {
	return vcmserver.V1CreateIgroupParams{
		RequestId: testOperationRequestID,
		Timeout:   testOperationTimeout,
	}
}

func testOperationIgroupUpdateParams(name string) vcmserver.V1UpdateIgroupParams {
	return vcmserver.V1UpdateIgroupParams{
		Name:      name,
		RequestId: testOperationRequestID,
		Timeout:   testOperationTimeout,
	}
}

func testOperationIgroupDeleteParams(name string) vcmserver.V1DeleteIgroupParams {
	return vcmserver.V1DeleteIgroupParams{
		Name:      name,
		RequestId: testOperationRequestID,
		Timeout:   testOperationTimeout,
	}
}

func testOperationClusterCreateParams(requestID string, timeout string) vcmserver.ClusterCreateV1Params {
	return vcmserver.ClusterCreateV1Params{
		RequestId: requestID,
		Timeout:   timeout,
	}
}

func testOperationClusterUpdateParams(
	requestID string,
	timeout string,
	updateMask string,
	changeType vcmserver.OptChangeTypeV1,
) vcmserver.ClusterUpdateV1Params {
	return vcmserver.ClusterUpdateV1Params{
		RequestId:  requestID,
		Timeout:    timeout,
		UpdateMask: updateMask,
		ChangeType: changeType,
	}
}

func TestParseProtobufDuration(t *testing.T) {
	duration, err := parseProtobufDuration("600s")
	require.NoError(t, err)
	assert.Equal(t, 600*time.Second, duration)

	duration, err = parseProtobufDuration("1.5s")
	require.NoError(t, err)
	assert.Equal(t, 1500*time.Millisecond, duration)

	_, err = parseProtobufDuration("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout is required")

	_, err = parseProtobufDuration("10m")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specified in seconds")

	_, err = parseProtobufDuration("600ms")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specified in seconds")

	_, err = parseProtobufDuration("0s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "greater than 0s")

	require.True(t, utilErrors.IsUserInputValidationErr(err))
}

func TestParseRequestIDAndTimeout(t *testing.T) {
	before := time.Now().UTC()
	requestID, deadline, err := parseRequestIDAndTimeout(testOperationRequestID, testOperationTimeout)
	require.NoError(t, err)
	assert.Equal(t, testOperationRequestID, requestID)
	assert.WithinDuration(t, before.Add(3600*time.Second), deadline, 2*time.Second)

	_, _, err = parseRequestIDAndTimeout("", testOperationTimeout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requestId is required")

	_, _, err = parseRequestIDAndTimeout("   ", testOperationTimeout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requestId is required")

	_, _, err = parseRequestIDAndTimeout(testOperationRequestID, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout is required")

	_, _, err = parseRequestIDAndTimeout(testOperationRequestID, "10m")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specified in seconds")

	require.True(t, utilErrors.IsUserInputValidationErr(err))
}

func TestParseRequestIDAndTimeout_AcceptsGeneratedUUID(t *testing.T) {
	requestID := uuid.NewString()
	parsedRequestID, _, err := parseRequestIDAndTimeout(requestID, testOperationTimeout)
	require.NoError(t, err)
	assert.Equal(t, requestID, parsedRequestID)
}
