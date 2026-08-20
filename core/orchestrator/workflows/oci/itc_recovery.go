package oci

import (
	"errors"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
)

var ociVlmITCRecoveryCapacityErrorMessage = env.GetString("OCI_VLM_ITC_RECOVERY_CAPACITY_ERROR_MESSAGE", "out of capacity")

// errOCIPartialVLMUpdateBatchFailure marks a VLM 200 response whose UpdateStatus
// reports sub-operation failures. ITC recovery re-runs attempted batches through
// the failing batch index (inclusive).
var errOCIPartialVLMUpdateBatchFailure = errors.New("oci partial vlm update batch failure")

type ociBatchLoopResult struct {
	Response       *vlm.UpdateVSAClusterDeploymentResponse
	FailedBatchNum int // 0-based batch index where the loop failed; -1 when all planned batches succeeded
}

type ociPoolUpdateVLMResult struct {
	Response             *vlm.UpdateVSAClusterDeploymentResponse
	ITCRecoverySucceeded bool
}

func vlmFailureIndicatesITCRecovery(err error) bool {
	if err == nil {
		return false
	}
	vlmErr, ok := extractVLMClientError(err)
	if !ok {
		return false
	}
	return vlmErrorMatchesITCRecoveryCause(vlmErr)
}

func batchFailureIndicatesITCRecovery(err error) bool {
	if vlmFailureIndicatesITCRecovery(err) {
		return true
	}
	return errors.Is(err, errOCIPartialVLMUpdateBatchFailure)
}

func vlmErrorMatchesITCRecoveryCause(vlmErr vlm.VLMClientError) bool {
	needle := strings.ToLower(ociVlmITCRecoveryCapacityErrorMessage)
	if needle == "" {
		return false
	}
	for _, cause := range vlmErr.Cause {
		if strings.Contains(strings.ToLower(cause), needle) {
			return true
		}
	}
	return false
}

func extractVLMClientError(err error) (vlm.VLMClientError, bool) {
	for err != nil {
		var appErr *temporal.ApplicationError
		if errors.As(err, &appErr) && appErr.Type() == vlm.ErrorTypeVLMClientError {
			var vlmClientErr vlm.VLMClientError
			if appErr.HasDetails() && appErr.Details(&vlmClientErr) == nil {
				return vlmClientErr, true
			}
		}
		err = errors.Unwrap(err)
	}
	return vlm.VLMClientError{}, false
}

func extractVLMRecoveryStatus(err error) string {
	if vlmErr, ok := extractVLMClientError(err); ok {
		if len(vlmErr.Cause) > 0 && vlmErr.Cause[0] != "" {
			return vlmErr.Cause[0]
		}
		if vlmErr.Code != "" {
			return vlmErr.Code
		}
		if vlmErr.Message != "" {
			return vlmErr.Message
		}
	}
	if err != nil {
		return err.Error()
	}
	return ""
}
