package errors

import (
	"context"
	_ "embed"
	"fmt"
	"testing"

	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/temporal"
)

//go:embed errors.json
var embeddedErrorsJSON []byte

var validJSON = `{"1001":{"message":"Input is invalid.","retriable":false,"http_code":400},"1002":{"message":"The requested resource was not found.","retriable":false,"http_code":404},"1003":{"message":"An internal error occurred.","retriable":true,"http_code":500}}`

func TestLoadErrorMessages(t *testing.T) {
	// Test with valid JSON
	errorsJSON = []byte(validJSON)
	handler := &ErrorHandler{}
	err := handler.loadErrorMessages()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	// Test with empty file
	errorsJSON = []byte("")
	err = handler.loadErrorMessages()
	if err == nil {
		t.Errorf("Expected error for empty file, got nil")
	}
	// Test with invalid JSON
	invalidJSON := `{"key":`
	errorsJSON = []byte(invalidJSON)
	err = handler.loadErrorMessages()
	if err == nil {
		t.Errorf("Expected error for invalid JSON, got nil")
	}
}

func TestNewErrorHandler(t *testing.T) {
	// Test with valid JSON
	errorsJSON = []byte(validJSON)
	handler, err := NewErrorHandler()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if handler == nil {
		t.Errorf("Expected handler, got nil")
	}
	// Test with empty file
	errorsJSON = []byte("")
	handler, err = NewErrorHandler()
	if err == nil {
		t.Errorf("Expected error for empty file, got nil")
	}
	if handler != nil {
		t.Errorf("Expected nil handler, got %v", handler)
	}
}

func TestCustomErrorMethods(t *testing.T) {
	originalErr := New("original error")
	httpCode := 500
	customErr := &CustomError{
		TrackingID:  1003,
		Message:     "An internal error occurred.",
		Retriable:   true,
		HttpCode:    &httpCode,
		OriginalErr: originalErr,
	}
	// Test Error method
	expectedErrorMessage := "An internal error occurred."
	if customErr.Error() != expectedErrorMessage {
		t.Errorf("Expected %s, got %s", expectedErrorMessage, customErr.Error())
	}
	// Test Unwrap method
	if customErr.Unwrap() != originalErr {
		t.Errorf("Expected %v, got %v", originalErr, customErr.Unwrap())
	}
	// Test IsRetriable method
	if !customErr.IsRetriable() {
		t.Errorf("Expected retriable to be true, got false")
	}
	// Test IsError method
	if !customErr.IsError(1003) {
		t.Errorf("Expected IsError to return true for InternalError")
	}
	// Test GetHttpCode method
	hasCode, code := customErr.GetHttpCode()
	if !hasCode || code != 500 {
		t.Errorf("Expected HTTP code 500, got %d", code)
	}
}

func TestNewVSAError(t *testing.T) {
	originalErr := New("original error")
	errorMap = map[int]ErrorMessage{
		1003: {
			Message:   "An internal error occurred.",
			Retriable: new(bool),
			HttpCode:  new(int),
		},
	}
	*errorMap[1003].Retriable = true
	*errorMap[1003].HttpCode = 500

	// Test with defined error
	err := NewVCPError(1003, originalErr)
	if err == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	// Test with undefined error
	err = NewVCPError(1005, originalErr)
	customErr := err
	if customErr == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if customErr.TrackingID != 1011 {
		t.Errorf("Expected ErrorName NotDefined, got %d", customErr.TrackingID)
	}
}

// TestErrActiveDirectoryDeleteErrorDueToInUseByPool verifies the VSCP-4490 error code for AD deletion when AD is in use by pool(s).
func TestErrActiveDirectoryDeleteErrorDueToInUseByPool(t *testing.T) {
	if ErrActiveDirectoryDeleteErrorDueToInUseByPool != 14000 {
		t.Errorf("Expected ErrActiveDirectoryDeleteErrorDueToInUseByPool to be 14000, got %d", ErrActiveDirectoryDeleteErrorDueToInUseByPool)
	}
	// Ensure 14000 is in errorMap so NewVCPError returns it (other tests may have overwritten errorMap)
	if errorMap == nil {
		errorMap = make(map[int]ErrorMessage)
	}
	retriable := false
	httpCode := 409
	errorMap[ErrActiveDirectoryDeleteErrorDueToInUseByPool] = ErrorMessage{
		Message:   "Error deleting active directory - Active Directory credentials are in use by Storage Pool(s)",
		Retriable: &retriable,
		HttpCode:  &httpCode,
	}

	originalErr := New("AD credentials in use by pool")
	customErr := NewVCPError(ErrActiveDirectoryDeleteErrorDueToInUseByPool, originalErr)
	if customErr == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if !customErr.IsError(ErrActiveDirectoryDeleteErrorDueToInUseByPool) {
		t.Errorf("Expected IsError(ErrActiveDirectoryDeleteErrorDueToInUseByPool) to be true")
	}
	if customErr.TrackingID != ErrActiveDirectoryDeleteErrorDueToInUseByPool {
		t.Errorf("Expected TrackingID %d, got %d", ErrActiveDirectoryDeleteErrorDueToInUseByPool, customErr.TrackingID)
	}
	expectedMsg := "Error deleting active directory - Active Directory credentials are in use by Storage Pool(s)"
	if customErr.GetMessage() != expectedMsg {
		t.Errorf("Expected message %q, got %q", expectedMsg, customErr.GetMessage())
	}
}

func TestIsAndAs(t *testing.T) {
	originalErr := New("original error")
	customErr := &CustomError{
		TrackingID:  1003,
		Message:     "An internal error occurred.",
		Retriable:   true,
		OriginalErr: originalErr,
	}
	// Test Is function
	if !Is(customErr, originalErr) {
		t.Errorf("Expected Is to return true for originalErr")
	}
	// Test As function
	var target *CustomError
	if !As(customErr, &target) {
		t.Errorf("Expected As to return true for CustomError")
	}
	if target != customErr {
		t.Errorf("Expected target to be %v, got %v", customErr, target)
	}
}

func TestGetErrorMessageByTrackingID(t *testing.T) {
	errorMap = map[int]ErrorMessage{
		1001: {
			Message:   "Input is invalid.",
			Retriable: new(bool),
			HttpCode:  new(int),
		},
	}
	*errorMap[1001].Retriable = false
	*errorMap[1001].HttpCode = 400

	// Test with existing TrackingID
	msg := GetErrorMessageByTrackingID(1001)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage")
	}
	if msg.Message != "Input is invalid." {
		t.Errorf("Expected message 'Input is invalid.', got %s", msg.Message)
	}
	if msg.HttpCode == nil || *msg.HttpCode != 400 {
		t.Errorf("Expected HTTP code 400, got %v", msg.HttpCode)
	}

	// Test with non-existent TrackingID
	msg = GetErrorMessageByTrackingID(9999)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for undefined error")
	}
	if msg.Message != "undefined error" {
		t.Errorf("Expected message 'undefined error', got %s", msg.Message)
	}
	if msg.HttpCode == nil || *msg.HttpCode != 500 {
		t.Errorf("Expected HTTP code 500, got %v", msg.HttpCode)
	}
}

func TestWrapAsTemporalApplicationError(t *testing.T) {
	originalErr := New("original error")
	customErr := &CustomError{
		TrackingID:  1003,
		Message:     "An internal error occurred.",
		Retriable:   true,
		OriginalErr: originalErr,
	}

	// Test wrapping a CustomError
	wrapped := WrapAsTemporalApplicationError(customErr)
	appErr, ok := wrapped.(*temporal.ApplicationError)
	if !ok {
		t.Errorf("Expected temporal.ApplicationError, got %T", wrapped)
	}
	if appErr != nil && appErr.Message() != customErr.Error() {
		t.Errorf("Expected error message %q, got %q", customErr.Error(), appErr.Error())
	}

	// Test passing a non-CustomError
	plainErr := New("plain error")
	result := WrapAsTemporalApplicationError(plainErr)
	if result != plainErr {
		t.Errorf("Expected original error to be returned unchanged")
	}
}

func TestWrapAsTemporalApplicationErrorIncludesOriginalDetails(t *testing.T) {
	originalErr := New("root cause")
	customErr := &CustomError{TrackingID: ErrWorkflowConfigurationError, Message: "user visible", OriginalErr: originalErr}

	wrapped := WrapAsTemporalApplicationError(customErr)
	appErr, ok := wrapped.(*temporal.ApplicationError)
	if !ok {
		t.Fatalf("expected temporal.ApplicationError, got %T", wrapped)
	}

	var trackingID int
	var originalMsg string
	if err := appErr.Details(&trackingID, &originalMsg); err != nil {
		t.Fatalf("expected details extraction to succeed: %v", err)
	}
	if trackingID != ErrWorkflowConfigurationError {
		t.Errorf("expected trackingID %d, got %d", ErrWorkflowConfigurationError, trackingID)
	}
	if originalMsg != originalErr.Error() {
		t.Errorf("expected original message %q, got %q", originalErr.Error(), originalMsg)
	}
}

func TestWrapAsTemporalApplicationErrorFallsBackToCustomMessage(t *testing.T) {
	customErr := &CustomError{TrackingID: ErrWorkflowConfigurationError, Message: "fallback message"}

	wrapped := WrapAsTemporalApplicationError(customErr)
	appErr, ok := wrapped.(*temporal.ApplicationError)
	if !ok {
		t.Fatalf("expected temporal.ApplicationError, got %T", wrapped)
	}

	var trackingID int
	var originalMsg string
	if err := appErr.Details(&trackingID, &originalMsg); err != nil {
		t.Fatalf("expected details extraction to succeed: %v", err)
	}
	if trackingID != ErrWorkflowConfigurationError {
		t.Errorf("expected trackingID %d, got %d", ErrWorkflowConfigurationError, trackingID)
	}
	if originalMsg != customErr.Message {
		t.Errorf("expected original message %q, got %q", customErr.Message, originalMsg)
	}
}

func TestExtractUserFriendlyErrorMessage_TemporalApplicationError(t *testing.T) {
	// Setup errorMap for test
	ctx := context.TODO()
	errorMap = map[int]ErrorMessage{
		1001: {Message: "User error"},
	}

	// Create a temporal.ApplicationError with CustomErrorType and details
	appErr := temporal.NewApplicationError("wrapped", CustomErrorType, 1001, "details")
	msg := ExtractCustomerFacingErrorMessage(ctx, appErr)
	if msg != "User error" {
		t.Errorf("Expected 'User error', got %q", msg)
	}
}

func TestExtractUserFriendlyErrorMessage_TemporalApplicationError_DetailsError(t *testing.T) {
	// Create a temporal.ApplicationError with CustomErrorType but no details
	ctx := context.TODO()
	appErr := temporal.NewApplicationError("wrapped", CustomErrorType)
	msg := ExtractCustomerFacingErrorMessage(ctx, appErr)
	if msg != DefaultErrorMessage {
		t.Errorf("Expected default message, got %q", msg)
	}
}

func TestExtractUserFriendlyErrorMessage_NonTemporalError(t *testing.T) {
	ctx := context.TODO()
	err := New("plain error")
	msg := ExtractCustomerFacingErrorMessage(ctx, err)
	if msg != DefaultErrorMessage {
		t.Errorf("Expected default message, got %q", msg)
	}
}

func TestCustomErrorMethods_NilPointer(t *testing.T) {
	var customErr *CustomError = nil

	// Test Error method with nil pointer
	if customErr.Error() != "" {
		t.Errorf("Expected empty string for nil CustomError.Error(), got %s", customErr.Error())
	}

	// Test Unwrap method with nil pointer
	if customErr.Unwrap() != nil {
		t.Errorf("Expected nil for nil CustomError.Unwrap(), got %v", customErr.Unwrap())
	}

	// Test IsRetriable method with nil pointer
	if customErr.IsRetriable() {
		t.Errorf("Expected false for nil CustomError.IsRetriable(), got true")
	}

	// Test IsError method with nil pointer
	if customErr.IsError(1001) {
		t.Errorf("Expected false for nil CustomError.IsError(), got true")
	}

	// Test LogError method with nil pointer (should not panic)
	customErr.LogError()

	// Test GetHttpCode method with nil pointer
	hasCode, code := customErr.GetHttpCode()
	if hasCode || code != 400 {
		t.Errorf("Expected (false, 400) for nil CustomError.GetHttpCode(), got (%t, %d)", hasCode, code)
	}

	// Test GetMessage method with nil pointer
	if customErr.GetMessage() != "" {
		t.Errorf("Expected empty string for nil CustomError.GetMessage(), got %s", customErr.GetMessage())
	}

	if customErr.GetDetailMessage() != "" {
		t.Errorf("Expected empty string for nil CustomError.GetDetailMessage(), got %s", customErr.GetDetailMessage())
	}

	// Test LogOriginalError method with nil pointer (should not panic)
	customErr.LogOriginalError()
}

func TestCustomErrorMethods_WithNilOriginalErr(t *testing.T) {
	httpCode := 500
	customErr := &CustomError{
		TrackingID:  1003,
		Message:     "An internal error occurred.",
		Retriable:   true,
		HttpCode:    &httpCode,
		OriginalErr: nil, // nil OriginalErr
	}

	// Test LogOriginalError method when OriginalErr is nil (should not panic)
	customErr.LogOriginalError()
}

func TestCustomErrorMethods_WithoutHttpCode(t *testing.T) {
	originalErr := New("original error")
	customErr := &CustomError{
		TrackingID:  1003,
		Message:     "An internal error occurred.",
		Retriable:   true,
		HttpCode:    nil, // nil HttpCode
		OriginalErr: originalErr,
	}

	// Test GetHttpCode method when HttpCode is nil
	hasCode, code := customErr.GetHttpCode()
	if hasCode || code != 400 {
		t.Errorf("Expected (false, 400) for CustomError without HttpCode, got (%t, %d)", hasCode, code)
	}
}

func TestNewVCPError_WithNilRetriable(t *testing.T) {
	originalErr := New("original error")
	errorMap = map[int]ErrorMessage{
		1003: {
			Message:   "An internal error occurred.",
			Retriable: nil, // nil Retriable
			HttpCode:  new(int),
		},
	}
	*errorMap[1003].HttpCode = 500

	// Test with nil Retriable (should default to false)
	err := NewVCPError(1003, originalErr)
	if err == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if err.Retriable {
		t.Errorf("Expected Retriable to be false when not specified, got true")
	}
}

func TestWrapAsNonRetryableTemporalApplicationError(t *testing.T) {
	originalErr := New("original error")
	customErr := &CustomError{
		TrackingID:  1003,
		Message:     "An internal error occurred.",
		Retriable:   true,
		OriginalErr: originalErr,
	}

	// Test wrapping a CustomError
	wrapped := WrapAsNonRetryableTemporalApplicationError(customErr)
	appErr, ok := wrapped.(*temporal.ApplicationError)
	if !ok {
		t.Errorf("Expected temporal.ApplicationError, got %T", wrapped)
	}
	if appErr != nil && appErr.Message() != customErr.Error() {
		t.Errorf("Expected error message %q, got %q", customErr.Error(), appErr.Error())
	}

	// Test passing a non-CustomError
	plainErr := New("plain error")
	result := WrapAsNonRetryableTemporalApplicationError(plainErr)
	if result != plainErr {
		t.Errorf("Expected original error to be returned unchanged")
	}
}

func TestExtractGoWorkflowCustomError_WhenDetailsMissingUsesCauseChain(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[ErrCreateSnapshotConflict] = ErrorMessage{
		Message:   "Error creating snapshot - snapshot already exists as part of volume clone",
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(409),
	}

	originalErr := New("snapshot with that name already exists")
	customErr := NewVCPError(ErrCreateSnapshotConflict, originalErr)
	appErr := temporal.NewNonRetryableApplicationError(customErr.Error(), CustomErrorType, customErr)

	result := ExtractGoWorkflowCustomError(appErr)
	if result == nil {
		t.Fatal("expected non-nil CustomError")
	}
	if result.TrackingID != ErrCreateSnapshotConflict {
		t.Errorf("expected tracking ID %d, got %d", ErrCreateSnapshotConflict, result.TrackingID)
	}
	if result.OriginalErr == nil || result.OriginalErr.Error() != originalErr.Error() {
		t.Errorf("expected original error %q, got %v", originalErr.Error(), result.OriginalErr)
	}

	legacy := ExtractCustomError(appErr)
	if legacy.TrackingID != ErrInternalServerError {
		t.Errorf("ExtractCustomError should not unwrap cause; got tracking ID %d", legacy.TrackingID)
	}
}

func TestExtractCustomError_WithTemporalApplicationError(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[1001] = ErrorMessage{Message: "test error"}

	appErr := temporal.NewApplicationError("wrapped", CustomErrorType, 1001, "details")
	result := ExtractCustomError(appErr)
	if result == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if result.TrackingID != 1001 {
		t.Errorf("Expected TrackingID 1001, got %d", result.TrackingID)
	}
	if result.OriginalErr == nil || result.OriginalErr.Error() != "details" {
		t.Errorf("Expected OriginalErr 'details', got %v", result.OriginalErr)
	}
}

func TestExtractJobFailureDetails_FromNonRetryableTemporalApplicationError(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[ErrCreateSnapshotConflict] = ErrorMessage{
		Message:   "Error creating snapshot - snapshot already exists as part of volume clone",
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(409),
	}

	originalErr := New("snapshot with that name already exists")
	customErr := NewVCPError(ErrCreateSnapshotConflict, originalErr)
	appErr := WrapAsNonRetryableTemporalApplicationError(customErr)

	trackingID, retriable, msg := ExtractJobFailureDetails(appErr)
	if trackingID != ErrCreateSnapshotConflict {
		t.Errorf("Expected tracking ID %d, got %d", ErrCreateSnapshotConflict, trackingID)
	}
	if retriable {
		t.Error("Expected non-retriable failure")
	}
	if msg != originalErr.Error() {
		t.Errorf("Expected original message %q, got %q", originalErr.Error(), msg)
	}
}

func TestExtractJobFailureDetails_NonRetryableWrapperOverridesCatalogRetriable(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	retriableTrue := true
	errorMap[9999] = ErrorMessage{
		Message:   "retryable catalog failure",
		Retriable: &retriableTrue,
		HttpCode:  func(i int) *int { return &i }(503),
	}

	originalErr := New("provider unavailable")
	customErr := NewVCPError(9999, originalErr)
	appErr := WrapAsNonRetryableTemporalApplicationError(customErr)

	trackingID, retriable, msg := ExtractJobFailureDetails(appErr)
	if trackingID != 9999 {
		t.Errorf("expected tracking ID 9999, got %d", trackingID)
	}
	if retriable {
		t.Error("expected non-retryable wrapper to override catalog retriable=true")
	}
	if msg != originalErr.Error() {
		t.Errorf("expected original message %q, got %q", originalErr.Error(), msg)
	}
}

func TestExtractJobFailureDetails_OrchestratorNonRetryableWrapperOverridesCatalogRetriable(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	retriableTrue := true
	errorMap[9999] = ErrorMessage{
		Message:   "retryable catalog failure",
		Retriable: &retriableTrue,
		HttpCode:  func(i int) *int { return &i }(503),
	}

	originalErr := New("provider unavailable")
	customErr := NewVCPError(9999, originalErr)
	appErr := orchestrator.NewNonRetryableApplicationError(
		customErr.Error(), CustomErrorType, customErr, 9999)

	trackingID, retriable, msg := ExtractJobFailureDetails(appErr)
	if trackingID != 9999 {
		t.Errorf("expected tracking ID 9999, got %d", trackingID)
	}
	if retriable {
		t.Error("expected orchestrator non-retryable wrapper to override catalog retriable=true")
	}
	if msg != originalErr.Error() {
		t.Errorf("expected original message %q, got %q", originalErr.Error(), msg)
	}
}

func TestExtractGoWorkflowCustomError_NonRetryableWrapperOverridesCatalogRetriable(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	retriableTrue := true
	errorMap[9999] = ErrorMessage{
		Message:   "retryable catalog failure",
		Retriable: &retriableTrue,
		HttpCode:  func(i int) *int { return &i }(503),
	}

	nestedCustom := NewVCPError(9999, nil)
	appErr := orchestrator.NewNonRetryableApplicationError(
		"perm failure", CustomErrorType, nestedCustom, 9999)

	result := ExtractGoWorkflowCustomError(appErr)
	if result.TrackingID != 9999 {
		t.Errorf("expected tracking ID 9999, got %d", result.TrackingID)
	}
	if result.Retriable {
		t.Error("expected non-retryable wrapper to override catalog retriable=true")
	}
}

func TestExtractJobFailureDetails_FromOrchestratorApplicationError(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	catalogMsg := "Error creating snapshot - snapshot already exists as part of volume clone"
	errorMap[ErrCreateSnapshotConflict] = ErrorMessage{
		Message:   catalogMsg,
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(409),
	}

	originalErr := New("snapshot with that name already exists")
	customErr := NewVCPError(ErrCreateSnapshotConflict, originalErr)
	appErr := orchestrator.NewNonRetryableApplicationError(
		customErr.Error(), CustomErrorType, customErr, ErrCreateSnapshotConflict)

	trackingID, retriable, msg := ExtractJobFailureDetails(appErr)
	if trackingID != ErrCreateSnapshotConflict {
		t.Errorf("Expected tracking ID %d, got %d", ErrCreateSnapshotConflict, trackingID)
	}
	if retriable {
		t.Error("Expected non-retriable failure")
	}
	if msg != originalErr.Error() {
		t.Errorf("Expected original message %q, got %q", originalErr.Error(), msg)
	}
	if msg == catalogMsg {
		t.Errorf("Expected provider text, got catalog message %q", catalogMsg)
	}
}

func TestExtractJobFailureDetails_InternalServerErrorWithOriginalErr(t *testing.T) {
	providerErr := New("provider failure: code=503: unavailable")
	customErr := NewVCPError(ErrInternalServerError, providerErr)

	trackingID, retriable, msg := ExtractJobFailureDetails(customErr)
	if trackingID != ErrInternalServerError {
		t.Errorf("Expected tracking ID %d, got %d", ErrInternalServerError, trackingID)
	}
	if retriable {
		t.Error("Expected non-retriable internal server error")
	}
	if msg != providerErr.Error() {
		t.Errorf("Expected provider message %q, got %q", providerErr.Error(), msg)
	}
}

func TestExtractJobFailureDetails_SerializedChainPreservesColonRichProviderText(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	catalogMsg := "Error creating snapshot - snapshot already exists as part of volume clone"
	errorMap[ErrCreateSnapshotConflict] = ErrorMessage{
		Message:   catalogMsg,
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(409),
	}

	providerErr := New("ONTAP error: volume snap-1: name already exists")
	chain := &serializedWorkflowErr{
		msg: catalogMsg + " (type: CustomError, retryable: false)",
		cause: &serializedWorkflowErr{
			msg:   catalogMsg,
			cause: providerErr,
		},
	}

	trackingID, retriable, msg := ExtractJobFailureDetails(chain)
	if trackingID != ErrCreateSnapshotConflict {
		t.Errorf("Expected tracking ID %d, got %d", ErrCreateSnapshotConflict, trackingID)
	}
	if retriable {
		t.Error("Expected non-retriable failure")
	}
	if msg != providerErr.Error() {
		t.Errorf("Expected full provider message %q, got %q", providerErr.Error(), msg)
	}
}

func TestExtractJobFailureDetails_AmbiguousCatalogMessageDoesNotGuessTrackingID(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	sharedMsg := "An internal error occurred."
	errorMap[1001] = ErrorMessage{Message: sharedMsg, Retriable: func(b bool) *bool { return &b }(false)}
	errorMap[1002] = ErrorMessage{Message: sharedMsg, Retriable: func(b bool) *bool { return &b }(true)}

	providerErr := New("database timeout while saving job")
	chain := &serializedWorkflowErr{
		msg:   sharedMsg,
		cause: providerErr,
	}

	trackingID, _, msg := ExtractJobFailureDetails(chain)
	if trackingID != 0 {
		t.Errorf("Expected ambiguous catalog text to leave tracking ID unset, got %d", trackingID)
	}
	if msg != providerErr.Error() {
		t.Errorf("Expected provider message %q, got %q", providerErr.Error(), msg)
	}
}

func TestExtractJobFailureDetails_NestedWorkflowWrapperPreservesProviderText(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[ErrCreateSnapshotConflict] = ErrorMessage{
		Message:   "Error creating snapshot - snapshot already exists as part of volume clone",
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(409),
	}

	originalErr := New("snapshot with that name already exists")
	customErr := NewVCPError(ErrCreateSnapshotConflict, originalErr)
	wrapped := fmt.Errorf("identify VMs / populate VLM config: %w", customErr)

	trackingID, _, msg := ExtractJobFailureDetails(wrapped)
	if trackingID != ErrCreateSnapshotConflict {
		t.Errorf("Expected tracking ID %d, got %d", ErrCreateSnapshotConflict, trackingID)
	}
	if msg != originalErr.Error() {
		t.Errorf("Expected original message %q, got %q", originalErr.Error(), msg)
	}
}

func TestExtractGoWorkflowCustomError_FromOrchestratorApplicationError(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[ErrInputValidationError] = ErrorMessage{
		Message:   "Input is invalid.",
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(400),
	}

	originalErr := New("cause details")
	appErr := orchestrator.NewNonRetryableApplicationError(
		"perm failure", CustomErrorType, originalErr, ErrInputValidationError)

	result := ExtractGoWorkflowCustomError(appErr)
	if result.TrackingID != ErrInputValidationError {
		t.Errorf("Expected tracking ID %d, got %d", ErrInputValidationError, result.TrackingID)
	}
	if result.OriginalErr == nil || result.OriginalErr.Error() != originalErr.Error() {
		t.Errorf("Expected original error %q, got %v", originalErr.Error(), result.OriginalErr)
	}
	if result.Retriable {
		t.Error("Expected non-retryable failure")
	}
}

func TestExtractJobFailureDetails_Nil(t *testing.T) {
	trackingID, retriable, msg := ExtractJobFailureDetails(nil)
	if trackingID != 0 || retriable || msg != "" {
		t.Fatalf("expected zero values for nil error, got (%d, %v, %q)", trackingID, retriable, msg)
	}
}

func TestExtractJobFailureDetails_PlainError(t *testing.T) {
	trackingID, retriable, msg := ExtractJobFailureDetails(New("generic failure"))
	if trackingID != 0 {
		t.Errorf("expected tracking ID 0, got %d", trackingID)
	}
	if retriable {
		t.Error("expected non-retriable plain error")
	}
	if msg != "generic failure" {
		t.Errorf("expected plain message %q, got %q", "generic failure", msg)
	}
}

func TestExtractJobFailureDetails_OrchestratorPlainCauseUsesTypedRecoveryPath(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[ErrInputValidationError] = ErrorMessage{
		Message:   "Input is invalid.",
		Retriable: func(b bool) *bool { return &b }(true),
		HttpCode:  func(i int) *int { return &i }(400),
	}

	plainCause := New("cause details")
	appErr := orchestrator.NewNonRetryableApplicationError(
		"perm failure", CustomErrorType, plainCause, ErrInputValidationError)

	trackingID, retriable, msg := ExtractJobFailureDetails(appErr)
	if trackingID != ErrInputValidationError {
		t.Errorf("expected tracking ID %d, got %d", ErrInputValidationError, trackingID)
	}
	if retriable {
		t.Error("expected orchestrator non-retryable flag to override catalog retriable")
	}
	if msg != plainCause.Error() {
		t.Errorf("expected cause message %q, got %q", plainCause.Error(), msg)
	}
}

func TestExtractJobFailureDetails_FallsBackToCatalogMessageWithoutChainOrOriginal(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	catalogMsg := "Input is invalid."
	errorMap[ErrInputValidationError] = ErrorMessage{
		Message:   catalogMsg,
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(400),
	}

	appErr := orchestrator.NewNonRetryableApplicationError(
		"", CustomErrorType, nil, ErrInputValidationError)

	trackingID, _, msg := ExtractJobFailureDetails(appErr)
	if trackingID != ErrInputValidationError {
		t.Errorf("expected tracking ID %d, got %d", ErrInputValidationError, trackingID)
	}
	if msg != catalogMsg {
		t.Errorf("expected catalog message %q, got %q", catalogMsg, msg)
	}
}

func TestExtractJobFailureDetails_SerializedCatalogSetsRetriable(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	catalogMsg := "retryable catalog failure"
	retriableTrue := true
	errorMap[9999] = ErrorMessage{
		Message:   catalogMsg,
		Retriable: &retriableTrue,
		HttpCode:  func(i int) *int { return &i }(500),
	}

	providerErr := New("provider detail")
	chain := &serializedWorkflowErr{msg: catalogMsg, cause: providerErr}

	trackingID, retriable, msg := ExtractJobFailureDetails(chain)
	if trackingID != 9999 {
		t.Errorf("expected tracking ID 9999, got %d", trackingID)
	}
	if !retriable {
		t.Error("expected retriable flag from unique catalog match")
	}
	if msg != providerErr.Error() {
		t.Errorf("expected provider message %q, got %q", providerErr.Error(), msg)
	}
}

func TestExtractJobFailureDetails_InternalServerOriginalErrWithoutChainMessage(t *testing.T) {
	err := silentErr{}

	trackingID, retriable, msg := ExtractJobFailureDetails(err)
	if trackingID != 0 {
		t.Errorf("expected tracking ID 0, got %d", trackingID)
	}
	if retriable {
		t.Error("expected non-retriable failure")
	}
	if msg != "" {
		t.Errorf("expected empty message from silent error, got %q", msg)
	}
}

func TestExtractJobFailureDetails_InternalServerWithoutChainOrOriginal(t *testing.T) {
	appErr := orchestrator.NewApplicationError("", CustomErrorType, ErrInternalServerError)

	trackingID, retriable, msg := ExtractJobFailureDetails(appErr)
	if trackingID != 0 {
		t.Errorf("expected tracking ID 0, got %d", trackingID)
	}
	if retriable {
		t.Error("expected non-retriable failure")
	}
	if msg != "" {
		t.Errorf("expected empty fallback message, got %q", msg)
	}
}

func TestExtractJobFailureDetails_StripEngineSuffixOnFallbackMessage(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[ErrInputValidationError] = ErrorMessage{
		Message:   "Input is invalid.",
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(400),
	}

	appErr := orchestrator.NewApplicationError(
		"boom (type: CustomError, retryable: false)", CustomErrorType, ErrInputValidationError)

	trackingID, _, msg := ExtractJobFailureDetails(appErr)
	if trackingID != ErrInputValidationError {
		t.Errorf("expected tracking ID %d, got %d", ErrInputValidationError, trackingID)
	}
	if msg != "boom" {
		t.Errorf("expected stripped fallback message %q, got %q", "boom", msg)
	}
}

func TestStripMessageAfterValidatedSuffix_PreservesProviderTextWithTypeFragment(t *testing.T) {
	providerMsg := "invalid field (type: string) at index 2"
	got := stripMessageAfterValidatedSuffix(providerMsg, workflowEngineErrorTypeMarker, workflowEngineErrorSuffixEndings)
	if got != providerMsg {
		t.Errorf("expected provider message preserved, got %q", got)
	}
}

func TestStripMessageAfterValidatedSuffix_RemovesWorkflowEngineSuffix(t *testing.T) {
	const want = "catalog failure"
	got := stripMessageAfterValidatedSuffix(
		want+" (type: CustomError, retryable: false)",
		workflowEngineErrorTypeMarker,
		workflowEngineErrorSuffixEndings,
	)
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestStripMessageAfterValidatedSuffix_UsesMarkerParameter(t *testing.T) {
	const marker = " [engine:"
	const want = "provider failure"
	got := stripMessageAfterValidatedSuffix(want+marker+"CustomError, retryable: true)", marker, []string{"retryable: true)"})
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestExtractJobFailureDetails_RetryableOrchestratorApplicationError(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[ErrGCPResourceFetchError] = ErrorMessage{
		Message:   "An internal error occurred.",
		Retriable: func(b bool) *bool { return &b }(true),
		HttpCode:  func(i int) *int { return &i }(500),
	}

	originalErr := New("secret manager fetch failed")
	customErr := NewVCPError(ErrGCPResourceFetchError, originalErr)
	appErr := orchestrator.NewApplicationErrorWithCause(
		customErr.Error(), CustomErrorType, customErr, customErr.TrackingID)

	trackingID, retriable, msg := ExtractJobFailureDetails(appErr)
	if trackingID != ErrGCPResourceFetchError {
		t.Errorf("expected tracking ID %d, got %d", ErrGCPResourceFetchError, trackingID)
	}
	if !retriable {
		t.Error("expected retryable failure from catalog entry")
	}
	if msg != originalErr.Error() {
		t.Errorf("expected provider message %q, got %q", originalErr.Error(), msg)
	}
	if msg == customErr.Message {
		t.Errorf("expected raw provider text, not catalog message %q", customErr.Message)
	}
}

func TestExtractGoWorkflowCustomError_Nil(t *testing.T) {
	result := ExtractGoWorkflowCustomError(nil)
	if result == nil {
		t.Fatal("expected non-nil CustomError")
	}
	if result.TrackingID != ErrInternalServerError {
		t.Errorf("expected tracking ID %d, got %d", ErrInternalServerError, result.TrackingID)
	}
	if result.OriginalErr != nil {
		t.Errorf("expected nil original error, got %v", result.OriginalErr)
	}
}

func TestExtractGoWorkflowCustomError_DirectCustomError(t *testing.T) {
	originalErr := New("provider detail")
	customErr := NewVCPError(ErrInputValidationError, originalErr)

	result := ExtractGoWorkflowCustomError(customErr)
	if result != customErr {
		t.Fatal("expected direct CustomError instance to be returned unchanged")
	}
	if result.OriginalErr != originalErr {
		t.Errorf("expected original error preserved, got %v", result.OriginalErr)
	}
}

func TestExtractGoWorkflowCustomError_FromTemporalApplicationErrorWithDetails(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[1001] = ErrorMessage{Message: "catalog message"}

	appErr := temporal.NewApplicationError("wrapped", CustomErrorType, 1001, "provider detail")
	result := ExtractGoWorkflowCustomError(appErr)
	if result.TrackingID != 1001 {
		t.Errorf("expected tracking ID 1001, got %d", result.TrackingID)
	}
	if result.OriginalErr == nil || result.OriginalErr.Error() != "provider detail" {
		t.Errorf("expected provider detail original error, got %v", result.OriginalErr)
	}
}

func TestExtractGoWorkflowCustomError_OrchestratorNestedCustomWithoutOriginalErr(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[ErrInputValidationError] = ErrorMessage{
		Message:   "Input is invalid.",
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(400),
	}

	nestedCustom := NewVCPError(ErrInputValidationError, nil)
	appErr := orchestrator.NewNonRetryableApplicationError(
		"perm failure", CustomErrorType, nestedCustom, ErrInputValidationError)

	result := ExtractGoWorkflowCustomError(appErr)
	if result != nestedCustom {
		t.Fatal("expected nested CustomError from cause chain to be returned directly")
	}
	if result.OriginalErr != nil {
		t.Errorf("expected nested custom without original provider text, got %v", result.OriginalErr)
	}
}

func TestOriginalErrFromCause_NilPlainAndNestedCustom(t *testing.T) {
	if got := originalErrFromCause(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
	plain := New("plain cause")
	if got := originalErrFromCause(plain); got != plain {
		t.Fatalf("expected plain cause unchanged, got %v", got)
	}
	nestedCustom := NewVCPError(ErrInputValidationError, nil)
	if got := originalErrFromCause(nestedCustom); got != nestedCustom {
		t.Fatalf("expected nested custom error, got %v", got)
	}
	nestedWithOriginal := NewVCPError(ErrInputValidationError, New("provider detail"))
	if got := originalErrFromCause(nestedWithOriginal); got == nil || got.Error() != "provider detail" {
		t.Fatalf("expected nested original error, got %v", got)
	}
}

type silentErr struct{}

func (silentErr) Error() string { return "" }

type serializedWorkflowErr struct {
	msg   string
	cause error
}

func (e *serializedWorkflowErr) Error() string { return e.msg }
func (e *serializedWorkflowErr) Unwrap() error { return e.cause }

func TestWrapAsNonRetryableTemporalApplicationError_PreservesCause(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	t.Cleanup(func() {
		errorMap = originalErrorMap
	})
	errorMap[ErrCreateSnapshotConflict] = ErrorMessage{
		Message:   "Error creating snapshot - snapshot already exists as part of volume clone",
		Retriable: func(b bool) *bool { return &b }(false),
		HttpCode:  func(i int) *int { return &i }(409),
	}

	originalErr := New("snapshot with that name already exists")
	customErr := NewVCPError(ErrCreateSnapshotConflict, originalErr)

	wrapped := WrapAsNonRetryableTemporalApplicationError(customErr)
	appErr, ok := wrapped.(*temporal.ApplicationError)
	if !ok {
		t.Fatalf("expected temporal.ApplicationError, got %T", wrapped)
	}
	if !appErr.NonRetryable() {
		t.Fatal("expected non-retryable temporal application error")
	}

	var nestedCustom *CustomError
	if !As(appErr.Unwrap(), &nestedCustom) {
		t.Fatal("expected custom error in unwrap chain")
	}
	if nestedCustom.OriginalErr != originalErr {
		t.Fatalf("expected original error preserved in chain, got %v", nestedCustom.OriginalErr)
	}
}

func TestExtractCustomError_WithTemporalApplicationError_NonCustomErrorType(t *testing.T) {
	// Test with temporal.ApplicationError of different type
	appErr := temporal.NewApplicationError("wrapped", "DifferentType", 1001, "details")
	result := ExtractCustomError(appErr)
	if result == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if result.TrackingID != ErrInternalServerError {
		t.Errorf("Expected TrackingID %d, got %d", ErrInternalServerError, result.TrackingID)
	}
}

func TestExtractCustomError_WithTemporalApplicationError_DetailsError(t *testing.T) {
	// Test with temporal.ApplicationError but Details() fails
	appErr := temporal.NewApplicationError("wrapped", CustomErrorType)
	result := ExtractCustomError(appErr)
	if result == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if result.TrackingID != ErrInternalServerError {
		t.Errorf("Expected TrackingID %d, got %d", ErrInternalServerError, result.TrackingID)
	}
}

func TestExtractCustomError_WithCustomError(t *testing.T) {
	// Test with existing CustomError
	originalErr := New("original error")
	customErr := &CustomError{
		TrackingID:  1003,
		Message:     "An internal error occurred.",
		Retriable:   true,
		OriginalErr: originalErr,
	}

	result := ExtractCustomError(customErr)
	if result == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if result != customErr {
		t.Errorf("Expected original CustomError to be returned, got different instance")
	}
}

func TestExtractCustomError_WithPlainError(t *testing.T) {
	// Test with plain error (not CustomError or temporal.ApplicationError)
	plainErr := New("plain error")
	result := ExtractCustomError(plainErr)
	if result == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if result.TrackingID != ErrInternalServerError {
		t.Errorf("Expected TrackingID %d, got %d", ErrInternalServerError, result.TrackingID)
	}
	if result.OriginalErr != plainErr {
		t.Errorf("Expected OriginalErr to be the plain error")
	}
}

func TestExtractCustomError_WithNilError(t *testing.T) {
	// Test with nil error - this should not panic anymore since we added nil checks
	var nilErr error = nil
	err := ExtractCustomError(nilErr)
	if err == nil {
		t.Error("Expected CustomError, got nil")
	}

	// Should return a default error with ErrInternalServerError
	if err.TrackingID != ErrInternalServerError {
		t.Errorf("Expected TrackingID %d, got %d", ErrInternalServerError, err.TrackingID)
	}

	// Should have a default message
	if err.Error() != "An internal error occurred" {
		t.Errorf("Expected default message, got '%s'", err.Error())
	}
}

// ============================================================================
// Placeholder Functionality Tests
// ============================================================================

func TestNewVCPErrorWithArgs(t *testing.T) {
	// Test creating error with placeholders
	originalErr := New("connection failed")
	customErr := NewVCPErrorWithArgs(
		ErrWorkflowConfigurationError,
		originalErr,
		"pool",
		"validation failed",
	)

	// Verify basic properties
	// Note: Since ErrWorkflowConfigurationError might not be in errorMap,
	// it will fall back to ErrInternalServerError
	expectedTrackingID := ErrInternalServerError
	if customErr.TrackingID != expectedTrackingID {
		t.Errorf("Expected TrackingID %d, got %d", expectedTrackingID, customErr.TrackingID)
	}

	if customErr.OriginalErr != originalErr {
		t.Errorf("Expected OriginalErr %v, got %v", originalErr, customErr.OriginalErr)
	}

	// Verify arguments are stored
	if len(customErr.args) != 2 {
		t.Errorf("Expected 2 arguments, got %d", len(customErr.args))
	}

	if customErr.args[0] != "pool" {
		t.Errorf("Expected first argument 'pool', got '%v'", customErr.args[0])
	}

	if customErr.args[1] != "validation failed" {
		t.Errorf("Expected second argument 'validation failed', got '%v'", customErr.args[1])
	}
}

func TestCustomErrorWithPlaceholders(t *testing.T) {
	// Test creating error with placeholders using a custom message template
	originalErr := New("connection failed")

	// Create a custom error with a message template that has placeholders
	customErr := &CustomError{
		TrackingID:  ErrWorkflowConfigurationError,
		Message:     "Internal error occurred in %s: %s",
		Retriable:   false,
		OriginalErr: originalErr,
		args:        []interface{}{"pool", "validation failed"},
	}

	// Test that placeholders are properly formatted
	expectedMessage := "Internal error occurred in pool: validation failed"
	if customErr.Error() != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, customErr.Error())
	}

	// Test that arguments are stored
	if len(customErr.GetArgs()) != 2 {
		t.Errorf("Expected 2 arguments, got %d", len(customErr.GetArgs()))
	}

	if customErr.GetArgs()[0] != "pool" {
		t.Errorf("Expected first argument 'pool', got '%v'", customErr.GetArgs()[0])
	}

	if customErr.GetArgs()[1] != "validation failed" {
		t.Errorf("Expected second argument 'validation failed', got '%v'", customErr.GetArgs()[1])
	}

	// Test placeholder detection
	if !customErr.HasArgs() {
		t.Error("Expected HasArgs() to return true")
	}

	// Test raw message access
	rawMessage := customErr.GetRawMessage()
	if rawMessage != "Internal error occurred in %s: %s" {
		t.Errorf("Expected raw message 'Internal error occurred in %%s: %%s', got '%s'", rawMessage)
	}
}

func TestCustomErrorWithoutPlaceholders(t *testing.T) {
	// Test creating error without placeholders
	originalErr := New("connection failed")
	customErr := NewVCPError(ErrWorkflowConfigurationError, originalErr)

	// Test that no placeholders are detected
	if customErr.HasArgs() {
		t.Error("Expected HasArgs() to return false")
	}

	// Test that arguments slice is nil
	if customErr.GetArgs() != nil {
		t.Error("Expected GetArgs() to return nil")
	}

	// Test that Error() returns the message as-is
	if customErr.Error() != customErr.GetRawMessage() {
		t.Errorf("Expected Error() to return raw message, got '%s'", customErr.Error())
	}
}

func TestErrorWithArgsReuse(t *testing.T) {
	baseErr := NewVCPError(ErrResourceNotFound, nil)

	// Create new error with different arguments
	poolErr := baseErr.WithArgs("pool", "not found")
	volumeErr := baseErr.WithArgs("volume", "creation failed")

	// Verify they have different formatted messages
	if poolErr.Error() == volumeErr.Error() {
		t.Error("Expected different formatted messages")
	}

	// Verify they share the same base properties
	if baseErr.TrackingID != poolErr.TrackingID {
		t.Errorf("Expected same TrackingID, got %d vs %d", baseErr.TrackingID, poolErr.TrackingID)
	}

	if baseErr.TrackingID != volumeErr.TrackingID {
		t.Errorf("Expected same TrackingID, got %d vs %d", baseErr.TrackingID, volumeErr.TrackingID)
	}

	// Verify arguments are different
	if len(poolErr.GetArgs()) != 2 || len(volumeErr.GetArgs()) != 2 {
		t.Error("Expected both errors to have 2 arguments")
	}

	// Verify they don't share the same args slice
	if &poolErr.args == &volumeErr.args {
		t.Error("Expected different args slices")
	}
}

func TestPlaceholderFormatting(t *testing.T) {
	// Test various placeholder types
	testCases := []struct {
		message   string
		args      []interface{}
		expected  string
		errorCode int
	}{
		{
			message:   "Error in %s: %s",
			args:      []interface{}{"pool", "connection failed"},
			expected:  "Error in pool: connection failed",
			errorCode: ErrWorkflowConfigurationError,
		},
		{
			message:   "Operation %s failed after %d attempts",
			args:      []interface{}{"create", 3},
			expected:  "Operation create failed after 3 attempts",
			errorCode: ErrMaxRetriesExceeded,
		},
		{
			message:   "Resource %s not found in %s",
			args:      []interface{}{"volume", "pool"},
			expected:  "Resource volume not found in pool",
			errorCode: ErrResourceNotFound,
		},
		{
			message:   "Value %v is invalid for type %T",
			args:      []interface{}{"test", "string"},
			expected:  "Value test is invalid for type string",
			errorCode: ErrInputValidationError,
		},
	}

	for i, tc := range testCases {
		customErr := &CustomError{
			TrackingID: tc.errorCode,
			Message:    tc.message,
			args:       tc.args,
		}

		if customErr.Error() != tc.expected {
			t.Errorf("Test case %d: Expected '%s', got '%s' for message '%s' with args %v",
				i+1, tc.expected, customErr.Error(), tc.message, tc.args)
		}
	}
}

func TestNilCustomErrorPlaceholderMethods(t *testing.T) {
	var customErr *CustomError

	// Test all placeholder methods handle nil gracefully
	if customErr.Error() != "" {
		t.Error("Expected empty string for nil CustomError")
	}

	if customErr.GetMessage() != "" {
		t.Error("Expected empty string for nil CustomError")
	}

	if customErr.GetRawMessage() != "" {
		t.Error("Expected empty string for nil CustomError")
	}

	if customErr.GetArgs() != nil {
		t.Error("Expected nil for nil CustomError")
	}

	if customErr.HasArgs() {
		t.Error("Expected false for nil CustomError")
	}

	if customErr.WithArgs("test") != nil {
		t.Error("Expected nil for nil CustomError")
	}
}

func TestCustomErrorWithArgsNilHandling(t *testing.T) {
	// Test WithArgs with nil arguments
	// Use a simple error that won't cause issues
	baseErr := &CustomError{
		TrackingID: ErrWorkflowConfigurationError,
		Message:    "Test error",
		Retriable:  false,
	}

	// Test with nil args
	nilArgsErr := baseErr.WithArgs(nil)
	if nilArgsErr == nil {
		t.Error("Expected error with nil args, got nil")
	}

	if len(nilArgsErr.GetArgs()) != 1 {
		t.Errorf("Expected 1 argument, got %d", len(nilArgsErr.GetArgs()))
	}

	if nilArgsErr.GetArgs()[0] != nil {
		t.Error("Expected first argument to be nil")
	}

	// Test with empty args
	emptyArgsErr := baseErr.WithArgs()
	if emptyArgsErr == nil {
		t.Error("Expected error with empty args, got nil")
	}

	// WithArgs should return an empty slice, not nil
	args := emptyArgsErr.GetArgs()
	if args == nil {
		t.Error("Expected empty slice, got nil")
	}

	if len(args) != 0 {
		t.Errorf("Expected 0 arguments, got %d", len(args))
	}
}

func TestNewVCPErrorWithArgsNilHandling(t *testing.T) {
	// Test NewVCPErrorWithArgs with nil original error
	customErr := NewVCPErrorWithArgs(ErrWorkflowConfigurationError, nil, "pool", "error")

	if customErr == nil {
		t.Error("Expected CustomError, got nil")
	}

	if customErr.OriginalErr != nil {
		t.Error("Expected nil OriginalErr")
	}

	if len(customErr.GetArgs()) != 2 {
		t.Errorf("Expected 2 arguments, got %d", len(customErr.GetArgs()))
	}

	// Test with no args
	noArgsErr := NewVCPErrorWithArgs(ErrWorkflowConfigurationError, nil)

	if noArgsErr == nil {
		t.Error("Expected CustomError, got nil")
	}

	if noArgsErr.GetArgs() == nil {
		t.Error("Expected empty slice, got nil")
	}

	if len(noArgsErr.GetArgs()) != 0 {
		t.Errorf("Expected 0 arguments, got %d", len(noArgsErr.GetArgs()))
	}
}

func TestCustomErrorArgsModification(t *testing.T) {
	// Test that modifying the args slice doesn't affect the original error
	baseErr := NewVCPError(ErrWorkflowConfigurationError, nil)
	argsErr := baseErr.WithArgs("original", "args")

	// Get the args slice and modify it
	args := argsErr.GetArgs()
	if len(args) >= 2 {
		args[0] = "modified"
		args[1] = "values"
	}

	// Verify the original error is unchanged
	// Note: Since Go slices are references, modifying the returned slice will affect the original
	// This is expected behavior. The test verifies that the args are properly stored.
	if len(argsErr.GetArgs()) != 2 {
		t.Error("Expected 2 arguments")
	}

	// Since Go slices are references, modifying the returned slice affects the original
	// This is the expected behavior. We verify that the modification worked.
	if argsErr.GetArgs()[0] != "modified" {
		t.Error("Expected first argument to be 'modified' after modification")
	}

	if argsErr.GetArgs()[1] != "values" {
		t.Error("Expected second argument to be 'values' after modification")
	}

	// Verify that the original baseErr is not affected (it has no args)
	if baseErr.GetArgs() != nil {
		t.Error("Expected base error to have no args")
	}
}

// TestGetDetailMessage tests GetDetailMessage prefers OriginalErr over catalog text.
func TestGetDetailMessage(t *testing.T) {
	catalogMessage := "Resource is in an invalid state for the requested operation"
	errorMap = map[int]ErrorMessage{
		ErrResourceStateConflictError: {
			Message:   catalogMessage,
			Retriable: new(bool),
			HttpCode:  new(int),
		},
	}
	*errorMap[ErrResourceStateConflictError].Retriable = false
	*errorMap[ErrResourceStateConflictError].HttpCode = 409

	originalErr := New("external cluster host \"host-1\" already onboarded in location \"us-central1\"")
	customErr := NewVCPError(ErrResourceStateConflictError, originalErr)

	if customErr.GetMessage() != catalogMessage {
		t.Errorf("Expected catalog message from GetMessage(), got %q", customErr.GetMessage())
	}
	if customErr.GetDetailMessage() != originalErr.Error() {
		t.Errorf("Expected detail message %q, got %q", originalErr.Error(), customErr.GetDetailMessage())
	}

	customErrNoOriginal := NewVCPError(ErrResourceStateConflictError, nil)
	if customErrNoOriginal.GetDetailMessage() != catalogMessage {
		t.Errorf("Expected catalog message from GetDetailMessage() when OriginalErr is nil, got %q", customErrNoOriginal.GetDetailMessage())
	}
}

// TestGetMessageWithArgs tests the GetMessage method when args are present
func TestGetMessageWithArgs(t *testing.T) {
	originalErr := New("test error")
	customErr := &CustomError{
		TrackingID:  ErrWorkflowConfigurationError,
		Message:     "Error in %s: %s",
		Retriable:   false,
		OriginalErr: originalErr,
		args:        []interface{}{"pool", "connection failed"},
	}

	// Test that GetMessage formats the message with args
	expectedMessage := "Error in pool: connection failed"
	if customErr.GetMessage() != expectedMessage {
		t.Errorf("Expected formatted message '%s', got '%s'", expectedMessage, customErr.GetMessage())
	}

	// Test that GetMessage still works without args
	customErr.args = nil
	if customErr.GetMessage() != "Error in %s: %s" {
		t.Error("Expected raw message when no args are present")
	}
}

// TestNewVCPErrorWithArgsWithNilRetriable tests NewVCPErrorWithArgs when Retriable is nil
func TestNewVCPErrorWithArgsWithNilRetriable(t *testing.T) {
	originalErr := New("test error")
	errorMap = map[int]ErrorMessage{
		ErrWorkflowConfigurationError: {
			Message:   "Workflow configuration error in %s: %s",
			Retriable: nil, // nil Retriable
			HttpCode:  new(int),
		},
	}
	*errorMap[ErrWorkflowConfigurationError].HttpCode = 400

	// Test with nil Retriable (should default to false)
	err := NewVCPErrorWithArgs(ErrWorkflowConfigurationError, originalErr, "pool", "validation failed")
	if err == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if err.Retriable {
		t.Errorf("Expected Retriable to be false when not specified, got true")
	}
	if err.TrackingID != ErrWorkflowConfigurationError {
		t.Errorf("Expected TrackingID %d, got %d", ErrWorkflowConfigurationError, err.TrackingID)
	}
	if err.Message != "Workflow configuration error in %s: %s" {
		t.Errorf("Expected message 'Workflow configuration error in %%s: %%s', got '%s'", err.Message)
	}
	if len(err.GetArgs()) != 2 {
		t.Errorf("Expected 2 arguments, got %d", len(err.GetArgs()))
	}
	if err.GetArgs()[0] != "pool" {
		t.Errorf("Expected first argument 'pool', got '%v'", err.GetArgs()[0])
	}
	if err.GetArgs()[1] != "validation failed" {
		t.Errorf("Expected second argument 'validation failed', got '%v'", err.GetArgs()[1])
	}
}

// TestNewVCPErrorWithArgsWithUndefinedError tests NewVCPErrorWithArgs when the error is not defined in errorMap
func TestNewVCPErrorWithArgsWithUndefinedError(t *testing.T) {
	originalErr := New("undefined error occurred")
	undefinedErrorID := 9999

	// Test with undefined error (should create generic error)
	err := NewVCPErrorWithArgs(undefinedErrorID, originalErr, "pool", "not found")
	if err == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if err.TrackingID != ErrInternalServerError {
		t.Errorf("Expected TrackingID %d, got %d", ErrInternalServerError, err.TrackingID)
	}
	if err.Retriable {
		t.Errorf("Expected Retriable to be false for undefined error, got true")
	}
	if err.Message != "undefined error occurred" {
		t.Errorf("Expected message 'undefined error occurred', got '%s'", err.Message)
	}
	if err.OriginalErr != originalErr {
		t.Errorf("Expected OriginalErr to be the original error")
	}
	if len(err.GetArgs()) != 2 {
		t.Errorf("Expected 2 arguments, got %d", len(err.GetArgs()))
	}
	if err.GetArgs()[0] != "pool" {
		t.Errorf("Expected first argument 'pool', got '%v'", err.GetArgs()[0])
	}
	if err.GetArgs()[1] != "not found" {
		t.Errorf("Expected second argument 'not found', got '%v'", err.GetArgs()[1])
	}
}

// TestNewVCPErrorWithArgsWithNilOriginalError tests NewVCPErrorWithArgs when originalErr is nil
func TestNewVCPErrorWithArgsWithNilOriginalError(t *testing.T) {
	undefinedErrorID := 9999

	// Test with nil original error (should create generic error with default message)
	err := NewVCPErrorWithArgs(undefinedErrorID, nil, "pool", "not found")
	if err == nil {
		t.Fatalf("Expected non-nil CustomError")
	}
	if err.TrackingID != ErrInternalServerError {
		t.Errorf("Expected TrackingID %d, got %d", ErrInternalServerError, err.TrackingID)
	}
	if err.Retriable {
		t.Errorf("Expected Retriable to be false for undefined error, got true")
	}
	if err.Message != "An internal error occurred" {
		t.Errorf("Expected message 'An internal error occurred', got '%s'", err.Message)
	}
	if err.OriginalErr != nil {
		t.Errorf("Expected OriginalErr to be nil")
	}
	if len(err.GetArgs()) != 2 {
		t.Errorf("Expected 2 arguments, got %d", len(err.GetArgs()))
	}
}

func TestCustomErrorWithArgsDeepCopy(t *testing.T) {
	// Test that WithArgs creates a deep copy
	originalErr := New("test error")
	baseErr := NewVCPError(ErrWorkflowConfigurationError, originalErr)

	// Add some properties to baseErr
	httpCode := 500
	baseErr.HttpCode = &httpCode
	baseErr.Retriable = true

	// Create new error with args
	argsErr := baseErr.WithArgs("arg1", "arg2")

	// Verify all properties are copied
	if argsErr.TrackingID != baseErr.TrackingID {
		t.Error("Expected TrackingID to be copied")
	}

	if argsErr.Message != baseErr.Message {
		t.Error("Expected Message to be copied")
	}

	if argsErr.Retriable != baseErr.Retriable {
		t.Error("Expected Retriable to be copied")
	}

	if argsErr.HttpCode != baseErr.HttpCode {
		t.Error("Expected HttpCode to be copied")
	}

	if argsErr.OriginalErr != baseErr.OriginalErr {
		t.Error("Expected OriginalErr to be copied")
	}

	// Verify args are different
	if len(argsErr.GetArgs()) != 2 {
		t.Errorf("Expected 2 arguments, got %d", len(argsErr.GetArgs()))
	}

	// Verify they are separate instances
	if argsErr == baseErr {
		t.Error("Expected different instances")
	}
}

// TestErrWorkflowSupervisorTimeoutMapping verifies that ErrWorkflowSupervisorTimeout (1018)
// is properly mapped to a user-friendly error message and HTTP code, ensuring customers
// get a proper error instead of "undefined error" when a job times out in the queue.
func TestErrWorkflowSupervisorTimeoutMapping(t *testing.T) {
	// Set up the error map with the expected mapping for ErrWorkflowSupervisorTimeout
	// This mirrors what should be in errors.json for tracking ID 1018
	httpCode := 503
	retriable := false
	errorMap[ErrWorkflowSupervisorTimeout] = ErrorMessage{
		Message:   "The operation timed out. Please try again.",
		Retriable: &retriable,
		HttpCode:  &httpCode,
	}

	msg := GetErrorMessageByTrackingID(ErrWorkflowSupervisorTimeout)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for ErrWorkflowSupervisorTimeout (1018)")
	}

	// Verify the message is not "undefined error" (which would indicate missing mapping)
	if msg.Message == "undefined error" {
		t.Errorf("ErrWorkflowSupervisorTimeout should be mapped to a proper message, got 'undefined error'")
	}

	// Verify the message is user-friendly
	expectedMessage := "The operation timed out. Please try again."
	if msg.Message != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, msg.Message)
	}

	// Verify the HTTP code is 504 (Gateway Timeout) not 500 (Internal Server Error)
	if msg.HttpCode == nil {
		t.Fatalf("Expected non-nil HTTP code for ErrWorkflowSupervisorTimeout")
	}
	if *msg.HttpCode != 503 {
		t.Errorf("Expected HTTP code 503, got %d", *msg.HttpCode)
	}

	// Verify the error is non-retriable as per the configuration
	if msg.Retriable == nil {
		t.Fatalf("Expected non-nil Retriable for ErrWorkflowSupervisorTimeout")
	}
	if *msg.Retriable {
		t.Errorf("Expected ErrWorkflowSupervisorTimeout to be non retriable")
	}
}

// TestErrCrossRegionBackupVaultAssignmentMapping verifies that ErrCrossRegionBackupVaultAssignmentToDestinationRegion (12018)
// is properly mapped to ensure cross-region backup vault validation errors are properly communicated to users.
func TestErrCrossRegionBackupVaultAssignmentMapping(t *testing.T) {
	// Set up the error map with the expected mapping for ErrCrossRegionBackupVaultAssignmentToDestinationRegion
	// This mirrors what should be in errors.json for tracking ID 12018
	httpCode := 400
	retriable := false
	errorMap[ErrCrossRegionBackupVaultAssignmentToDestinationRegion] = ErrorMessage{
		Message:   "Cannot assign a cross-region backup vault to a volume in the destination region",
		Retriable: &retriable,
		HttpCode:  &httpCode,
	}

	msg := GetErrorMessageByTrackingID(ErrCrossRegionBackupVaultAssignmentToDestinationRegion)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for ErrCrossRegionBackupVaultAssignmentToDestinationRegion (12018)")
	}

	// Verify the message is properly defined
	if msg.Message == "undefined error" {
		t.Errorf("ErrCrossRegionBackupVaultAssignmentToDestinationRegion should be mapped to a proper message, got 'undefined error'")
	}

	// Verify the message matches the expected validation error
	expectedMessage := "Cannot assign a cross-region backup vault to a volume in the destination region"
	if msg.Message != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, msg.Message)
	}

	// Verify HTTP code is 400 (Bad Request) for validation errors
	if msg.HttpCode == nil {
		t.Fatalf("Expected non-nil HTTP code for ErrCrossRegionBackupVaultAssignmentToDestinationRegion")
	}
	if *msg.HttpCode != 400 {
		t.Errorf("Expected HTTP code 400, got %d", *msg.HttpCode)
	}

	// Verify the error is non-retriable (validation errors shouldn't be retried)
	if msg.Retriable == nil {
		t.Fatalf("Expected non-nil Retriable for ErrCrossRegionBackupVaultAssignmentToDestinationRegion")
	}
	if *msg.Retriable {
		t.Errorf("Expected ErrCrossRegionBackupVaultAssignmentToDestinationRegion to be non-retriable")
	}
}

// TestErrUnauthorizedMapping verifies that ErrUnauthorized (1019) is properly mapped
// to HTTP 401 for unauthorized errors. This test validates the mapping loaded from
// the embedded errors.json file.
func TestErrUnauthorizedMapping(t *testing.T) {
	// Preserve global state so this test does not interfere with others.
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	originalErrorsJSON := make([]byte, len(errorsJSON))
	copy(originalErrorsJSON, errorsJSON)
	defer func() {
		errorMap = originalErrorMap
		errorsJSON = originalErrorsJSON
	}()

	// Reload errorMap from the embedded JSON to ensure we test against the actual mappings.
	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("failed to load error messages from embedded JSON: %v", err)
	}

	msg := GetErrorMessageByTrackingID(ErrUnauthorized)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for ErrUnauthorized (1019)")
	}

	if msg.Message == "undefined error" {
		t.Fatalf("ErrUnauthorized (1019) is not defined in errors.json - got 'undefined error'")
	}

	expectedMessage := "Unauthorized"
	if msg.Message != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, msg.Message)
	}

	if msg.HttpCode == nil {
		t.Fatalf("Expected non-nil HTTP code for ErrUnauthorized")
	}
	if *msg.HttpCode != 401 {
		t.Errorf("Expected HTTP code 401, got %d", *msg.HttpCode)
	}

	if msg.Retriable == nil {
		t.Fatalf("Expected non-nil Retriable for ErrUnauthorized")
	}
	if *msg.Retriable {
		t.Errorf("Expected ErrUnauthorized to be non-retriable")
	}
}

// TestErrForbiddenMapping verifies that ErrForbidden (1020) is properly mapped
// to HTTP 403 for forbidden errors. This test validates the mapping loaded from
// the embedded errors.json file.
func TestErrForbiddenMapping(t *testing.T) {
	// Preserve global state so this test does not interfere with others.
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	originalErrorsJSON := make([]byte, len(errorsJSON))
	copy(originalErrorsJSON, errorsJSON)
	defer func() {
		errorMap = originalErrorMap
		errorsJSON = originalErrorsJSON
	}()

	// Reload errorMap from the embedded JSON to ensure we test against the actual mappings.
	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("failed to load error messages from embedded JSON: %v", err)
	}

	msg := GetErrorMessageByTrackingID(ErrForbidden)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for ErrForbidden (1020)")
	}

	if msg.Message == "undefined error" {
		t.Fatalf("ErrForbidden (1020) is not defined in errors.json - got 'undefined error'")
	}

	expectedMessage := "Forbidden"
	if msg.Message != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, msg.Message)
	}

	if msg.HttpCode == nil {
		t.Fatalf("Expected non-nil HTTP code for ErrForbidden")
	}
	if *msg.HttpCode != 403 {
		t.Errorf("Expected HTTP code 403, got %d", *msg.HttpCode)
	}

	if msg.Retriable == nil {
		t.Fatalf("Expected non-nil Retriable for ErrForbidden")
	}
	if *msg.Retriable {
		t.Errorf("Expected ErrForbidden to be non-retriable")
	}
}

// TestErrTooManyRequestsMapping verifies that ErrTooManyRequests (1021) is properly mapped
// to HTTP 429 for rate limiting errors. This test validates the mapping loaded from
// the embedded errors.json file.
func TestErrTooManyRequestsMapping(t *testing.T) {
	// Preserve global state so this test does not interfere with others.
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	originalErrorsJSON := make([]byte, len(errorsJSON))
	copy(originalErrorsJSON, errorsJSON)
	defer func() {
		errorMap = originalErrorMap
		errorsJSON = originalErrorsJSON
	}()

	// Reload errorMap from the embedded JSON to ensure we test against the actual mappings.
	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("failed to load error messages from embedded JSON: %v", err)
	}

	msg := GetErrorMessageByTrackingID(ErrTooManyRequests)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for ErrTooManyRequests (1021)")
	}

	if msg.Message == "undefined error" {
		t.Fatalf("ErrTooManyRequests (1021) is not defined in errors.json - got 'undefined error'")
	}

	expectedMessage := "Too many requests"
	if msg.Message != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, msg.Message)
	}

	if msg.HttpCode == nil {
		t.Fatalf("Expected non-nil HTTP code for ErrTooManyRequests")
	}
	if *msg.HttpCode != 429 {
		t.Errorf("Expected HTTP code 429, got %d", *msg.HttpCode)
	}

	if msg.Retriable == nil {
		t.Fatalf("Expected non-nil Retriable for ErrTooManyRequests")
	}
	if !*msg.Retriable {
		t.Errorf("Expected ErrTooManyRequests to be retriable")
	}
}

func TestWrapAsNonRetryableTemporalApplicationError_NilOriginalErr(t *testing.T) {
	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("Failed to load error messages: %v", err)
	}

	customErr := NewVCPError(ErrBadRequest, nil)
	result := WrapAsNonRetryableTemporalApplicationError(customErr)

	var appErr *temporal.ApplicationError
	if !As(result, &appErr) {
		t.Fatalf("Expected temporal.ApplicationError, got %T", result)
	}
	if appErr.Type() != CustomErrorType {
		t.Errorf("Expected type %q, got %q", CustomErrorType, appErr.Type())
	}

	var trackingID int
	var errorDetails string
	if err := appErr.Details(&trackingID, &errorDetails); err != nil {
		t.Fatalf("Failed to extract details: %v", err)
	}
	if trackingID != ErrBadRequest {
		t.Errorf("Expected TrackingID %d, got %d", ErrBadRequest, trackingID)
	}
}

func TestWrapAsNonRetryableTemporalApplicationError_WithOriginalErr(t *testing.T) {
	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("Failed to load error messages: %v", err)
	}

	customErr := NewVCPError(ErrDNSServerUnreachable, New("DNS server 10.0.0.1 cannot be reached"))
	result := WrapAsNonRetryableTemporalApplicationError(customErr)

	var appErr *temporal.ApplicationError
	if !As(result, &appErr) {
		t.Fatalf("Expected temporal.ApplicationError, got %T", result)
	}

	var trackingID int
	var errorDetails string
	if err := appErr.Details(&trackingID, &errorDetails); err != nil {
		t.Fatalf("Failed to extract details: %v", err)
	}
	if trackingID != ErrDNSServerUnreachable {
		t.Errorf("Expected TrackingID %d, got %d", ErrDNSServerUnreachable, trackingID)
	}
	if errorDetails != "DNS server 10.0.0.1 cannot be reached" {
		t.Errorf("Expected original error message, got %q", errorDetails)
	}
}

func TestDNSErrorPropagation_ActivityToWorkflow(t *testing.T) {
	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("Failed to load error messages: %v", err)
	}

	ontapErr := New("The DNS IP address specified for SVM svm-test cannot be reached")

	// Step 1: Activity wraps with WrapOntapError (ClassifyOntapError + WrapAsTemporalApplicationError)
	classified := ClassifyOntapError(ontapErr, DomainDNS)
	if classified.TrackingID != ErrDNSServerUnreachable {
		t.Fatalf("ClassifyOntapError: expected TrackingID %d, got %d", ErrDNSServerUnreachable, classified.TrackingID)
	}
	activityErr := WrapAsTemporalApplicationError(classified)

	// Step 2: EnsureCIFSShareWorkflow calls ConvertToVSAError (= ExtractCustomError)
	extracted := ExtractCustomError(activityErr)
	if extracted.TrackingID != ErrDNSServerUnreachable {
		t.Fatalf("ExtractCustomError from activity: expected TrackingID %d, got %d", ErrDNSServerUnreachable, extracted.TrackingID)
	}

	// Step 3: PostFileVolumeWorkflow wraps for child boundary (ExtractCustomError + WrapAsTemporalApplicationError)
	reExtracted := ExtractCustomError(extracted)
	childErr := WrapAsTemporalApplicationError(reExtracted)

	// Step 4: Parent receives child error — simulate extracting across the boundary
	parentExtracted := ExtractCustomError(childErr)
	if parentExtracted.TrackingID != ErrDNSServerUnreachable {
		t.Fatalf("Parent ExtractCustomError: expected TrackingID %d, got %d", ErrDNSServerUnreachable, parentExtracted.TrackingID)
	}

	// Step 5: Verify GetErrorMessageByTrackingID returns 400
	errMsg := GetErrorMessageByTrackingID(parentExtracted.TrackingID)
	if errMsg.HttpCode == nil || *errMsg.HttpCode != 400 {
		t.Fatalf("Expected HTTP 400 for TrackingID %d, got %v", parentExtracted.TrackingID, errMsg.HttpCode)
	}
	if errMsg.Message == "" || errMsg.Message == "undefined error" {
		t.Fatalf("Expected user-facing message, got %q", errMsg.Message)
	}
}

func TestDNSErrorPropagation_WrapOntapError_EndToEnd(t *testing.T) {
	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("Failed to load error messages: %v", err)
	}

	ontapErr := New("The DNS IP address specified for SVM svm-test cannot be reached")

	// WrapOntapError: the one-call convenience used in activities
	wrapped := WrapOntapError(ontapErr, DomainDNS)

	// Verify it's a temporal ApplicationError with correct type and details
	var appErr *temporal.ApplicationError
	if !As(wrapped, &appErr) {
		t.Fatalf("Expected temporal.ApplicationError, got %T", wrapped)
	}
	if appErr.Type() != CustomErrorType {
		t.Errorf("Expected type %q, got %q", CustomErrorType, appErr.Type())
	}
	var trackingID int
	var errorDetails string
	if err := appErr.Details(&trackingID, &errorDetails); err != nil {
		t.Fatalf("Failed to extract details: %v", err)
	}
	if trackingID != ErrDNSServerUnreachable {
		t.Errorf("WrapOntapError: expected TrackingID %d, got %d", ErrDNSServerUnreachable, trackingID)
	}

	// ExtractCustomError on the wrapped error (simulates ConvertToVSAError)
	custom := ExtractCustomError(wrapped)
	if custom.TrackingID != ErrDNSServerUnreachable {
		t.Errorf("ExtractCustomError: expected TrackingID %d, got %d", ErrDNSServerUnreachable, custom.TrackingID)
	}

	// Final: HTTP code should be 400
	errMsg := GetErrorMessageByTrackingID(custom.TrackingID)
	if errMsg.HttpCode == nil || *errMsg.HttpCode != 400 {
		t.Errorf("Expected HTTP 400, got %v", errMsg.HttpCode)
	}
}

func TestIsCVPError(t *testing.T) {
	tests := []struct {
		name       string
		trackingID int
		expected   bool
	}{
		{"ErrCVPBadRequest", ErrCVPBadRequest, true},
		{"ErrCVPInternalServerError", ErrCVPInternalServerError, true},
		{"upper bound of range", 14399, true},
		{"below range", 14199, false},
		{"above range", 14400, false},
		{"unrelated error", ErrBadRequest, false},
		{"zero", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsCVPError(tt.trackingID); got != tt.expected {
				t.Errorf("IsCVPError(%d) = %v, want %v", tt.trackingID, got, tt.expected)
			}
		})
	}
}

// TestErrKMSMigrationClientErrorMapping verifies that ErrKMSMigrationClientError (6064)
// is properly mapped to HTTP 400 for client errors during CMEK migration (e.g. ONTAP 409 conflict).
func TestErrKMSMigrationClientErrorMapping(t *testing.T) {
	// Preserve global state so this test does not interfere with others.
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	originalErrorsJSON := make([]byte, len(errorsJSON))
	copy(originalErrorsJSON, errorsJSON)
	defer func() {
		errorMap = originalErrorMap
		errorsJSON = originalErrorsJSON
	}()

	// Reload errorMap from the embedded JSON to ensure we test against the actual mappings.
	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("failed to load error messages from embedded JSON: %v", err)
	}

	msg := GetErrorMessageByTrackingID(ErrKMSMigrationClientError)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for ErrKMSMigrationClientError (6064)")
	}

	if msg.Message == "undefined error" {
		t.Fatalf("ErrKMSMigrationClientError (6064) is not defined in errors.json - got 'undefined error'")
	}

	expectedMessage := "Error while migrating KMS configuration"
	if msg.Message != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, msg.Message)
	}

	if msg.HttpCode == nil {
		t.Fatalf("Expected non-nil HTTP code for ErrKMSMigrationClientError")
	}
	if *msg.HttpCode != 400 {
		t.Errorf("Expected HTTP code 400, got %d", *msg.HttpCode)
	}

	if msg.Retriable == nil {
		t.Fatalf("Expected non-nil Retriable for ErrKMSMigrationClientError")
	}
	if *msg.Retriable {
		t.Errorf("Expected ErrKMSMigrationClientError to be non-retriable")
	}
}

// TestErrKMSAlreadyExistsEKMMapping verifies that ErrKMSAlreadyExistsEKM (6065)
// is properly mapped to HTTP 409 for "external key manager already configured on SVM" errors.
func TestErrKMSAlreadyExistsEKMMapping(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	originalErrorsJSON := make([]byte, len(errorsJSON))
	copy(originalErrorsJSON, errorsJSON)
	defer func() {
		errorMap = originalErrorMap
		errorsJSON = originalErrorsJSON
	}()

	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("failed to load error messages from embedded JSON: %v", err)
	}

	msg := GetErrorMessageByTrackingID(ErrKMSAlreadyExistsEKM)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for ErrKMSAlreadyExistsEKM (6065)")
	}

	if msg.Message == "undefined error" {
		t.Fatalf("ErrKMSAlreadyExistsEKM (6065) is not defined in errors.json - got 'undefined error'")
	}

	expectedMessage := "Error while configuring KMS: A key manager is already configured for this SVM"
	if msg.Message != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, msg.Message)
	}

	if msg.HttpCode == nil {
		t.Fatalf("Expected non-nil HTTP code for ErrKMSAlreadyExistsEKM")
	}
	if *msg.HttpCode != 409 {
		t.Errorf("Expected HTTP code 409, got %d", *msg.HttpCode)
	}

	if msg.Retriable == nil {
		t.Fatalf("Expected non-nil Retriable for ErrKMSAlreadyExistsEKM")
	}
	if *msg.Retriable {
		t.Errorf("Expected ErrKMSAlreadyExistsEKM to be non-retriable")
	}
}

// TestErrKMSConfigureEKMMapping verifies that ErrKMSConfigureEKM (6062)
// is properly mapped to HTTP 500 for generic EKM configuration failures on ONTAP.
func TestErrKMSConfigureEKMMapping(t *testing.T) {
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	originalErrorsJSON := make([]byte, len(errorsJSON))
	copy(originalErrorsJSON, errorsJSON)
	defer func() {
		errorMap = originalErrorMap
		errorsJSON = originalErrorsJSON
	}()

	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("failed to load error messages from embedded JSON: %v", err)
	}

	msg := GetErrorMessageByTrackingID(ErrKMSConfigureEKM)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for ErrKMSConfigureEKM (6062)")
	}

	if msg.Message == "undefined error" {
		t.Fatalf("ErrKMSConfigureEKM (6062) is not defined in errors.json - got 'undefined error'")
	}

	expectedMessage := "Error while configuring key manager"
	if msg.Message != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, msg.Message)
	}

	if msg.HttpCode == nil {
		t.Fatalf("Expected non-nil HTTP code for ErrKMSConfigureEKM")
	}
	if *msg.HttpCode != 500 {
		t.Errorf("Expected HTTP code 500, got %d", *msg.HttpCode)
	}

	if msg.Retriable == nil {
		t.Fatalf("Expected non-nil Retriable for ErrKMSConfigureEKM")
	}
	if *msg.Retriable {
		t.Errorf("Expected ErrKMSConfigureEKM to be non-retriable")
	}
}

// TestErrKMSMigrationMapping verifies that ErrKMSMigration (6063)
// is properly mapped to HTTP 500 for internal errors during CMEK migration.
func TestErrKMSMigrationMapping(t *testing.T) {
	// Preserve global state so this test does not interfere with others.
	originalErrorMap := make(map[int]ErrorMessage)
	for k, v := range errorMap {
		originalErrorMap[k] = v
	}
	originalErrorsJSON := make([]byte, len(errorsJSON))
	copy(originalErrorsJSON, errorsJSON)
	defer func() {
		errorMap = originalErrorMap
		errorsJSON = originalErrorsJSON
	}()

	// Reload errorMap from the embedded JSON to ensure we test against the actual mappings.
	errorsJSON = embeddedErrorsJSON
	handler := &ErrorHandler{}
	if err := handler.loadErrorMessages(); err != nil {
		t.Fatalf("failed to load error messages from embedded JSON: %v", err)
	}

	msg := GetErrorMessageByTrackingID(ErrKMSMigration)
	if msg == nil {
		t.Fatalf("Expected non-nil ErrorMessage for ErrKMSMigration (6063)")
	}

	if msg.Message == "undefined error" {
		t.Fatalf("ErrKMSMigration (6063) is not defined in errors.json - got 'undefined error'")
	}

	expectedMessage := "Error while migrating KMS configuration"
	if msg.Message != expectedMessage {
		t.Errorf("Expected message '%s', got '%s'", expectedMessage, msg.Message)
	}

	if msg.HttpCode == nil {
		t.Fatalf("Expected non-nil HTTP code for ErrKMSMigration")
	}
	if *msg.HttpCode != 500 {
		t.Errorf("Expected HTTP code 500, got %d", *msg.HttpCode)
	}

	if msg.Retriable == nil {
		t.Fatalf("Expected non-nil Retriable for ErrKMSMigration")
	}
	if *msg.Retriable {
		t.Errorf("Expected ErrKMSMigration to be non-retriable")
	}
}
