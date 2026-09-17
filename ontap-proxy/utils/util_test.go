package utils

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ontapserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/api/ontap-proxy-servergen"
)

func TestExtractOntapPath(t *testing.T) {
	tests := []struct {
		name     string
		fullPath string
		expected string
	}{
		{
			name:     "Valid ONTAP API path with full project path",
			fullPath: "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/qtrees",
			expected: "/api/storage/qtrees",
		},
		{
			name:     "ONTAP API path with query parameters",
			fullPath: "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes?fields=name,size",
			expected: "/api/storage/volumes?fields=name,size",
		},
		{
			name:     "Path without ontap segment",
			fullPath: "/v1beta/projects/1234/locations/us-central1/pools/my-pool/invalid-path",
			expected: "",
		},
		{
			name:     "Empty path",
			fullPath: "",
			expected: "",
		},
		{
			name:     "ONTAP API at root level",
			fullPath: "/ontap/api/storage/qtrees",
			expected: "/api/storage/qtrees",
		},
		{
			name:     "ONTAP API at end of path",
			fullPath: "/v1beta/projects/123/locations/us-central1/pools/pool1/ontap",
			expected: "/",
		},
		{
			name:     "Multiple ontap segments - should use first occurrence",
			fullPath: "/v1beta/projects/123/ontap/api1/ontap/api2",
			expected: "/api1/ontap/api2",
		},
		{
			name:     "Path starting with ontap (no leading slash)",
			fullPath: "ontap/api/storage/qtrees",
			expected: "/api/storage/qtrees",
		},
		{
			name:     "ONTAP API path with UUID returns normalized path",
			fullPath: "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000",
			expected: "/api/storage/volumes/{uuid}",
		},
		{
			name:     "ONTAP API path with nested paths",
			fullPath: "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes/12345/snapshots",
			expected: "/api/storage/volumes/12345/snapshots",
		},
		{
			name:     "Path with only ontap segment",
			fullPath: "/ontap",
			expected: "/",
		},
		{
			name:     "Path with ontap and trailing slash",
			fullPath: "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/",
			expected: "/",
		},
		{
			name:     "Path with private API",
			fullPath: "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/private/cli/snapmirror/break",
			expected: "/api/private/cli/snapmirror/break",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractOntapPath(tt.fullPath)
			assert.Equal(t, tt.expected, result, "ExtractOntapPath(%q) = %q, want %q", tt.fullPath, result, tt.expected)
		})
	}
}

func TestExtractOntapPathRaw(t *testing.T) {
	// Raw path keeps UUIDs as-is (used when forwarding to ONTAP)
	t.Run("path with UUID is not normalized", func(t *testing.T) {
		fullPath := "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000"
		result := ExtractOntapPathRaw(fullPath)
		assert.Equal(t, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", result)
	})
	t.Run("path without ontap returns empty", func(t *testing.T) {
		result := ExtractOntapPathRaw("/v1beta/projects/1234/pools/my-pool")
		assert.Equal(t, "", result)
	})
}

func TestNormalizeUUIDs(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"single UUID", "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", "/api/storage/volumes/{uuid}"},
		{"multiple UUIDs", "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000/snapshots/660e8400-e29b-41d4-a716-446655440001", "/api/storage/volumes/{uuid}/snapshots/{uuid}"},
		{"no UUID", "/api/storage/volumes", "/api/storage/volumes"},
		{"non-UUID segment unchanged", "/api/storage/volumes/12345/snapshots", "/api/storage/volumes/12345/snapshots"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeUUIDs(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWriteErrorResponse(t *testing.T) {
	tests := []struct {
		name           string
		code           int
		message        string
		expectedCode   int
		expectedBody   ontapserver.Error
		expectedHeader string
	}{
		{
			name:           "Bad Request (400)",
			code:           http.StatusBadRequest,
			message:        "Invalid request parameters",
			expectedCode:   http.StatusBadRequest,
			expectedBody:   ontapserver.Error{Code: http.StatusBadRequest, Message: "Invalid request parameters"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Unauthorized (401)",
			code:           http.StatusUnauthorized,
			message:        "Unauthorized access",
			expectedCode:   http.StatusUnauthorized,
			expectedBody:   ontapserver.Error{Code: http.StatusUnauthorized, Message: "Unauthorized access"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Forbidden (403)",
			code:           http.StatusForbidden,
			message:        "Forbidden access",
			expectedCode:   http.StatusForbidden,
			expectedBody:   ontapserver.Error{Code: http.StatusForbidden, Message: "Forbidden access"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Not Found (404)",
			code:           http.StatusNotFound,
			message:        "Pool not found",
			expectedCode:   http.StatusNotFound,
			expectedBody:   ontapserver.Error{Code: http.StatusNotFound, Message: "Pool not found"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Method Not Allowed (405)",
			code:           http.StatusMethodNotAllowed,
			message:        "Method not allowed",
			expectedCode:   http.StatusMethodNotAllowed,
			expectedBody:   ontapserver.Error{Code: http.StatusMethodNotAllowed, Message: "Method not allowed"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Internal Server Error (500)",
			code:           http.StatusInternalServerError,
			message:        "Internal server error",
			expectedCode:   http.StatusInternalServerError,
			expectedBody:   ontapserver.Error{Code: http.StatusInternalServerError, Message: "Internal server error"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Bad Gateway (502)",
			code:           http.StatusBadGateway,
			message:        "Cannot connect to ONTAP cluster",
			expectedCode:   http.StatusBadGateway,
			expectedBody:   ontapserver.Error{Code: http.StatusBadGateway, Message: "Cannot connect to ONTAP cluster"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Service Unavailable (503)",
			code:           http.StatusServiceUnavailable,
			message:        "Service unavailable",
			expectedCode:   http.StatusServiceUnavailable,
			expectedBody:   ontapserver.Error{Code: http.StatusServiceUnavailable, Message: "Service unavailable"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Gateway Timeout (504)",
			code:           http.StatusGatewayTimeout,
			message:        "Request timeout - ONTAP cluster not responding",
			expectedCode:   http.StatusGatewayTimeout,
			expectedBody:   ontapserver.Error{Code: http.StatusGatewayTimeout, Message: "Request timeout - ONTAP cluster not responding"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Empty message",
			code:           http.StatusBadRequest,
			message:        "",
			expectedCode:   http.StatusBadRequest,
			expectedBody:   ontapserver.Error{Code: http.StatusBadRequest, Message: ""},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Long error message",
			code:           http.StatusBadRequest,
			message:        "This is a very long error message that contains multiple words and should be properly encoded in the JSON response without any issues",
			expectedCode:   http.StatusBadRequest,
			expectedBody:   ontapserver.Error{Code: http.StatusBadRequest, Message: "This is a very long error message that contains multiple words and should be properly encoded in the JSON response without any issues"},
			expectedHeader: "application/json; charset=utf-8",
		},
		{
			name:           "Error message with special characters",
			code:           http.StatusBadRequest,
			message:        "Error: Invalid input \"test\" with 'quotes' and <tags>",
			expectedCode:   http.StatusBadRequest,
			expectedBody:   ontapserver.Error{Code: http.StatusBadRequest, Message: "Error: Invalid input \"test\" with 'quotes' and <tags>"},
			expectedHeader: "application/json; charset=utf-8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new recorder for each test
			w := httptest.NewRecorder()

			// Call WriteErrorResponse
			WriteErrorResponse(w, tt.code, tt.message)

			// Verify HTTP status code
			assert.Equal(t, tt.expectedCode, w.Code, "HTTP status code should match expected value")

			// Verify Content-Type header
			contentType := w.Header().Get("Content-Type")
			assert.Equal(t, tt.expectedHeader, contentType, "Content-Type header should be set to application/json")

			// Verify response body is valid JSON
			var errorResponse ontapserver.Error
			err := json.Unmarshal(w.Body.Bytes(), &errorResponse)
			require.NoError(t, err, "Response body should be valid JSON")

			// Verify error code in JSON body matches HTTP status code
			assert.Equal(t, tt.expectedBody.Code, errorResponse.Code, "Error code in JSON body should match HTTP status code")
			assert.Equal(t, tt.expectedBody.Message, errorResponse.Message, "Error message in JSON body should match expected message")

			// Verify that the code in the JSON body matches the HTTP status code
			assert.Equal(t, w.Code, errorResponse.Code, "JSON body code should match HTTP status code")
		})
	}
}

func TestWriteErrorResponse_CodeAndStatusAlignment(t *testing.T) {
	// This test specifically verifies that the code in the JSON body
	// always matches the HTTP status code, which is a key requirement
	t.Run("Code in JSON body matches HTTP status code", func(t *testing.T) {
		statusCodes := []int{
			http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusForbidden,
			http.StatusNotFound,
			http.StatusMethodNotAllowed,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout,
		}

		for _, statusCode := range statusCodes {
			t.Run(http.StatusText(statusCode), func(t *testing.T) {
				w := httptest.NewRecorder()
				message := "Test error message"

				WriteErrorResponse(w, statusCode, message)

				// Verify HTTP status code
				assert.Equal(t, statusCode, w.Code, "HTTP status code should match input")

				// Parse JSON response
				var errorResponse ontapserver.Error
				err := json.Unmarshal(w.Body.Bytes(), &errorResponse)
				require.NoError(t, err, "Response should be valid JSON")

				// Verify code in JSON body matches HTTP status code
				assert.Equal(t, statusCode, errorResponse.Code, "Code in JSON body should match HTTP status code")
				assert.Equal(t, message, errorResponse.Message, "Message should match input")
			})
		}
	})
}

func TestWriteErrorResponse_JSONFormat(t *testing.T) {
	t.Run("Response body is properly formatted JSON", func(t *testing.T) {
		w := httptest.NewRecorder()
		code := http.StatusBadRequest
		message := "Test error message"

		WriteErrorResponse(w, code, message)

		// Verify it's valid JSON
		var errorResponse ontapserver.Error
		err := json.Unmarshal(w.Body.Bytes(), &errorResponse)
		require.NoError(t, err, "Response should be valid JSON")

		// Verify JSON structure
		assert.Equal(t, code, errorResponse.Code)
		assert.Equal(t, message, errorResponse.Message)

		// Verify JSON can be re-marshaled (round-trip test)
		jsonBytes, err := json.Marshal(errorResponse)
		require.NoError(t, err, "Should be able to marshal back to JSON")

		var roundTripError ontapserver.Error
		err = json.Unmarshal(jsonBytes, &roundTripError)
		require.NoError(t, err, "Should be able to unmarshal round-trip JSON")
		assert.Equal(t, errorResponse, roundTripError, "Round-trip JSON should match original")
	})
}

func TestParseSizeString(t *testing.T) {
	tests := []struct {
		input  string
		expect float64
	}{
		{"1024", 1024},
		{"10g", 10 * 1024 * 1024 * 1024},
		{"10G", 10 * 1024 * 1024 * 1024},
		{"100m", 100 * 1024 * 1024},
		{"1t", 1 * 1024 * 1024 * 1024 * 1024},
		{"2k", 2 * 1024},
		{" 5gb ", 5 * 1024 * 1024 * 1024},
		{"invalid", 0},
		{"", 0},
		{"10x", 0},  // unknown unit
		{"+10g", 0}, // leading + not allowed
		{"-10g", 0}, // leading - not allowed
		{"-10", 0},  // negative size not allowed
		{"10.5g", 10.5 * 1024 * 1024 * 1024},
		{"1.5MB", 1.5 * 1024 * 1024},
		{"1p", 1 * 1024 * 1024 * 1024 * 1024 * 1024},
		{"2PB", 2 * 1024 * 1024 * 1024 * 1024 * 1024},
	}
	for _, tt := range tests {
		got := ParseSizeString(tt.input)
		if got != tt.expect {
			t.Errorf("ParseSizeString(%q) = %v, want %v", tt.input, got, tt.expect)
		}
	}
}

func TestParseOntapErrorBody(t *testing.T) {
	const fallback = "ONTAP returned an error"

	t.Run("valid error with code and message", func(t *testing.T) {
		body := []byte(`{"error":{"message":"Volume not found","code":"12345"}}`)
		code, message := ParseOntapErrorBody(body)
		assert.Equal(t, 12345, code)
		assert.Equal(t, "Volume not found", message)
	})

	t.Run("valid error with message only", func(t *testing.T) {
		body := []byte(`{"error":{"message":"Permission denied"}}`)
		code, message := ParseOntapErrorBody(body)
		assert.Equal(t, 0, code)
		assert.Equal(t, "Permission denied", message)
	})

	t.Run("invalid JSON returns generic message", func(t *testing.T) {
		body := []byte(`not json`)
		code, message := ParseOntapErrorBody(body)
		assert.Equal(t, 0, code)
		assert.Equal(t, fallback, message)
	})

	t.Run("empty error object returns generic message", func(t *testing.T) {
		body := []byte(`{"other":"data"}`)
		code, message := ParseOntapErrorBody(body)
		assert.Equal(t, 0, code)
		assert.Equal(t, fallback, message)
	})

	t.Run("empty body", func(t *testing.T) {
		body := []byte(``)
		code, message := ParseOntapErrorBody(body)
		assert.Equal(t, 0, code)
		assert.Equal(t, "", message)
	})

	t.Run("non-numeric code returns 0", func(t *testing.T) {
		body := []byte(`{"error":{"message":"Bad request","code":"ERR_INVALID"}}`)
		code, message := ParseOntapErrorBody(body)
		assert.Equal(t, 0, code)
		assert.Equal(t, "Bad request", message)
	})

	t.Run("error object with empty message returns generic message", func(t *testing.T) {
		body := []byte(`{"error":{"message":"","code":"500"}}`)
		code, message := ParseOntapErrorBody(body)
		assert.Equal(t, 500, code)
		assert.Equal(t, fallback, message)
	})
}

func TestStripOntapLoginBanner(t *testing.T) {
	t.Run("When_output_empty_returns_unchanged", func(t *testing.T) {
		got := StripOntapLoginBanner("")
		assert.Equal(t, "", got)
	})
	t.Run("When_banner_present_strips_banner", func(t *testing.T) {
		output := "\n\n" + OntapFirstLoginBanner + "\n\nVserver   Volume\n"
		got := StripOntapLoginBanner(output)
		assert.NotContains(t, got, OntapFirstLoginBanner)
		assert.Contains(t, got, "Vserver   Volume")
	})
	t.Run("When_no_banner_returns_unchanged", func(t *testing.T) {
		output := "Vserver   Volume\n--------- -----\n"
		got := StripOntapLoginBanner(output)
		assert.Equal(t, output, got)
	})
}

func TestParseSnaplockLegalHoldShowInstanceOutput(t *testing.T) {
	t.Run("When_single_block_returns_one_record", func(t *testing.T) {
		output := "Vserver: vs1\nLitigation Name: lit1\nPath: /dir1\n"
		records, err := ParseSnaplockLegalHoldShowInstanceOutput(output)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, "lit1", records[0].Name)
		assert.Equal(t, "/dir1", records[0].Path)
	})
	t.Run("When_name_missing_path_defaults_to_slash", func(t *testing.T) {
		output := "Vserver: vs1\nLitigation Name: mylit\n"
		records, err := ParseSnaplockLegalHoldShowInstanceOutput(output)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, "mylit", records[0].Name)
		assert.Equal(t, "/", records[0].Path)
	})
	t.Run("When_duplicate_name_dedupes", func(t *testing.T) {
		output := "Vserver: vs1\nLitigation Name: lit1\nPath: /p1\n\nVserver: vs1\nLitigation Name: lit1\nPath: /p2\n"
		records, err := ParseSnaplockLegalHoldShowInstanceOutput(output)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, "lit1", records[0].Name)
	})

	t.Run("When_no_vserver_line_treats_whole_output_as_one_block", func(t *testing.T) {
		output := "Litigation Name: standalone\nPath: /only\n"
		records, err := ParseSnaplockLegalHoldShowInstanceOutput(output)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, "standalone", records[0].Name)
		assert.Equal(t, "/only", records[0].Path)
	})

	t.Run("When_block_has_no_litigation_name_skips_block", func(t *testing.T) {
		output := "Vserver: vs1\nPath: /no-name\n\nVserver: vs1\nLitigation Name: lit1\nPath: /p1\n"
		records, err := ParseSnaplockLegalHoldShowInstanceOutput(output)
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, "lit1", records[0].Name)
		assert.Equal(t, "/p1", records[0].Path)
	})
}

func TestParseOperationIDFromBeginEndOutput(t *testing.T) {
	t.Run("When_operation_id_present_returns_id_and_true", func(t *testing.T) {
		output := "some text -operation-id 16908292 more text"
		id, ok := ParseOperationIDFromBeginEndOutput(output)
		require.True(t, ok)
		assert.Equal(t, 16908292, id)
	})
	t.Run("When_no_operation_id_returns_zero_and_false", func(t *testing.T) {
		id, ok := ParseOperationIDFromBeginEndOutput("no id here")
		assert.False(t, ok)
		assert.Equal(t, 0, id)
	})

	t.Run("When_operation_id_overflows_returns_zero_and_false", func(t *testing.T) {
		output := " -operation-id 99999999999999999999 "
		id, ok := ParseOperationIDFromBeginEndOutput(output)
		assert.False(t, ok)
		assert.Equal(t, 0, id)
	})
}

func TestParseSnaplockLegalHoldShowOperationOutput(t *testing.T) {
	t.Run("When_valid_block_returns_record", func(t *testing.T) {
		output := "Vserver: vs1\nOperation ID: 16908292\nLitigation Name: lit1\nPath: /dir1\nOperation Type: begin\nStatus: In-Progress\n"
		rec, err := ParseSnaplockLegalHoldShowOperationOutput(output)
		require.NoError(t, err)
		require.NotNil(t, rec)
		assert.Equal(t, 16908292, rec.OperationID)
		assert.Equal(t, "In-Progress", rec.Status)
		assert.Equal(t, "/dir1", rec.Path)
		assert.Equal(t, "begin", rec.OperationType)
	})
	t.Run("When_empty_output_returns_nil_nil", func(t *testing.T) {
		rec, err := ParseSnaplockLegalHoldShowOperationOutput("")
		require.NoError(t, err)
		assert.Nil(t, rec)
	})
	t.Run("When_no_operation_id_in_block_returns_nil_nil", func(t *testing.T) {
		output := "Vserver: vs1\nLitigation Name: lit1\n"
		rec, err := ParseSnaplockLegalHoldShowOperationOutput(output)
		require.NoError(t, err)
		assert.Nil(t, rec)
	})
	t.Run("When_whitespace_only_output_returns_nil_nil", func(t *testing.T) {
		rec, err := ParseSnaplockLegalHoldShowOperationOutput("   \n  ")
		require.NoError(t, err)
		assert.Nil(t, rec)
	})

	t.Run("When_vserver_only_then_whitespace_uses_output_as_single_block", func(t *testing.T) {
		output := "Vserver: \n\n\n"
		rec, err := ParseSnaplockLegalHoldShowOperationOutput(output)
		require.NoError(t, err)
		assert.Nil(t, rec) // no Operation ID in block
	})

	t.Run("When_operation_id_overflows_returns_nil_nil", func(t *testing.T) {
		output := "Vserver: vs1\nOperation ID: 99999999999999999999\nPath: /x\n"
		rec, err := ParseSnaplockLegalHoldShowOperationOutput(output)
		require.NoError(t, err)
		assert.Nil(t, rec)
	})
}

func TestParseSnaplockLegalHoldShowInstanceOutputToOperations(t *testing.T) {
	t.Run("When_multiple_blocks_returns_all_operations", func(t *testing.T) {
		output := "Vserver: vs1\nOperation ID: 1\nLitigation Name: lit1\nPath: /p1\nStatus: Completed\n\nVserver: vs1\nOperation ID: 2\nLitigation Name: lit1\nPath: /p2\nStatus: In-Progress\n"
		ops := ParseSnaplockLegalHoldShowInstanceOutputToOperations(output)
		require.Len(t, ops, 2)
		assert.Equal(t, 1, ops[0].OperationID)
		assert.Equal(t, "lit1", ops[0].LitigationName)
		assert.Equal(t, 2, ops[1].OperationID)
	})
	t.Run("When_empty_output_returns_empty_slice", func(t *testing.T) {
		ops := ParseSnaplockLegalHoldShowInstanceOutputToOperations("")
		assert.Empty(t, ops)
	})

	t.Run("When_vserver_only_then_whitespace_uses_output_as_block", func(t *testing.T) {
		ops := ParseSnaplockLegalHoldShowInstanceOutputToOperations("Vserver: \n\n\n")
		assert.Empty(t, ops) // no Operation ID in block
	})

	t.Run("When_block_has_overflow_operation_id_skips_block", func(t *testing.T) {
		output := "Vserver: vs1\nOperation ID: 99999999999999999999\nLitigation Name: lit1\n\nVserver: vs1\nOperation ID: 2\nLitigation Name: lit2\nPath: /p2\n"
		ops := ParseSnaplockLegalHoldShowInstanceOutputToOperations(output)
		require.Len(t, ops, 1)
		assert.Equal(t, 2, ops[0].OperationID)
		assert.Equal(t, "lit2", ops[0].LitigationName)
	})

	t.Run("When_block_has_litigation_name_and_operation_id_returns_record", func(t *testing.T) {
		output := "Vserver: vs1\nOperation ID: 42\nLitigation Name: mylit\nPath: /p\nStatus: Completed\nOperation Type: end\n"
		ops := ParseSnaplockLegalHoldShowInstanceOutputToOperations(output)
		require.Len(t, ops, 1)
		assert.Equal(t, 42, ops[0].OperationID)
		assert.Equal(t, "mylit", ops[0].LitigationName)
		assert.Equal(t, "/p", ops[0].Path)
		assert.Equal(t, "Completed", ops[0].Status)
		assert.Equal(t, "end", ops[0].OperationType)
	})
}

func TestMapOperationStatusToState(t *testing.T) {
	t.Run("When_status_completed_returns_completed", func(t *testing.T) {
		assert.Equal(t, "completed", MapOperationStatusToState("Completed"))
	})
	t.Run("When_status_in_progress_returns_in_progress", func(t *testing.T) {
		assert.Equal(t, "in_progress", MapOperationStatusToState("In-Progress"))
	})
	t.Run("When_status_failed_returns_failed", func(t *testing.T) {
		assert.Equal(t, "failed", MapOperationStatusToState("Failed"))
	})
	t.Run("When_status_aborting_returns_aborting", func(t *testing.T) {
		assert.Equal(t, "aborting", MapOperationStatusToState("Aborting"))
	})
	t.Run("When_status_unknown_returns_in_progress", func(t *testing.T) {
		assert.Equal(t, "in_progress", MapOperationStatusToState("unknown"))
	})
	t.Run("When_status_whitespace_returns_in_progress", func(t *testing.T) {
		assert.Equal(t, "in_progress", MapOperationStatusToState("  "))
	})
}

func TestEncodeOntapPathParam(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "snapshot relative path", input: ".snapshot/snap_e2e2", expected: "%2Esnapshot%2Fsnap_e2e2"},
		{name: "single dot segment", input: ".", expected: "%2E"},
		{name: "double dot segment", input: "..", expected: "%2E%2E"},
		{name: "nested directories", input: "d1/d2/d3", expected: "d1%2Fd2%2Fd3"},
		{name: "filename with extension dot is encoded", input: "file.txt", expected: "file%2Etxt"},
		{name: "leading slash", input: "/file2.txt", expected: "%2Ffile2%2Etxt"},
		{name: "plain segment unchanged", input: "data", expected: "data"},
		{name: "space is percent encoded", input: "my file", expected: "my%20file"},
		{name: "empty", input: "", expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, EncodeOntapPathParam(tt.input))
		})
	}
}

func TestReencodeOntapFilePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "files endpoint encodes only the path param",
			input:    "/api/storage/volumes/c208d402-47af-11f1-a635-912e2acd4456/files/.snapshot/snap_e2e2",
			expected: "/api/storage/volumes/c208d402-47af-11f1-a635-912e2acd4456/files/%2Esnapshot%2Fsnap_e2e2",
		},
		{
			name:     "files endpoint with nested path and trailing dot segment",
			input:    "/api/storage/volumes/c208d402-47af-11f1-a635-912e2acd4456/files/d1/d2/d3/.",
			expected: "/api/storage/volumes/c208d402-47af-11f1-a635-912e2acd4456/files/d1%2Fd2%2Fd3%2F%2E",
		},
		{
			name:     "files endpoint with plain path param returns empty",
			input:    "/api/storage/volumes/c208d402-47af-11f1-a635-912e2acd4456/files/data",
			expected: "",
		},
		{
			name:     "snaplock file endpoint is not re-encoded",
			input:    "/api/storage/snaplock/file/000dc5fd-4175-11e9-b937-0050568e3f82/dir/file2.txt",
			expected: "",
		},
		{
			name:     "non-file endpoint returns empty",
			input:    "/api/storage/volumes/c208d402-47af-11f1-a635-912e2acd4456",
			expected: "",
		},
		{
			name:     "volumes collection returns empty",
			input:    "/api/storage/volumes",
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ReencodeOntapFilePath(tt.input))
		})
	}
}
