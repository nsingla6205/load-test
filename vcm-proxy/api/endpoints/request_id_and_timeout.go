package api

import (
	"strconv"
	"strings"
	"time"

	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// parseRequestIDAndTimeout validates requestId and timeout query parameters shared by VCM mutation APIs.
func parseRequestIDAndTimeout(requestID string, timeout string) (string, time.Time, error) {
	trimmedRequestID := strings.TrimSpace(requestID)
	if trimmedRequestID == "" {
		return "", time.Time{}, utilErrors.NewUserInputValidationErr("requestId is required")
	}

	duration, err := parseProtobufDuration(timeout)
	if err != nil {
		return "", time.Time{}, err
	}

	now := time.Now().UTC()
	return trimmedRequestID, now.Add(duration), nil
}

func parseProtobufDuration(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, utilErrors.NewUserInputValidationErr("timeout is required")
	}

	secondsStr := strings.TrimSuffix(trimmed, "s")
	seconds, err := strconv.ParseFloat(secondsStr, 64)
	if err != nil {
		return 0, utilErrors.NewUserInputValidationErr("timeout must be specified in seconds (for example 600s)")
	}

	duration := time.Duration(seconds * float64(time.Second))
	if duration <= 0 {
		return 0, utilErrors.NewUserInputValidationErr("timeout must be greater than 0s")
	}

	return duration, nil
}
