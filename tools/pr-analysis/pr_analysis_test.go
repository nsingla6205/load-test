package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestParseAnalysisResponse(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		status      string
		body        string
		wantContent string
		wantError   string
	}{
		{
			name:        "returns analysis content",
			statusCode:  http.StatusOK,
			status:      "200 OK",
			body:        `{"choices":[{"message":{"content":"analysis result"}}]}`,
			wantContent: "analysis result",
		},
		{
			name:       "returns proxy error response",
			statusCode: http.StatusRequestEntityTooLarge,
			status:     "413 Request Entity Too Large",
			body:       `{"error":{"message":"request too large"}}`,
			wantError:  "LLM proxy returned 413 Request Entity Too Large",
		},
		{
			name:       "rejects response without choices",
			statusCode: http.StatusOK,
			status:     "200 OK",
			body:       `{"error":{"message":"upstream failure"}}`,
			wantError:  "LLM proxy response contained no choices",
		},
		{
			name:       "rejects malformed response",
			statusCode: http.StatusOK,
			status:     "200 OK",
			body:       `{`,
			wantError:  "failed to unmarshal LLM proxy response",
		},
		{
			name:       "rejects empty content",
			statusCode: http.StatusOK,
			status:     "200 OK",
			body:       `{"choices":[{"message":{"content":"  "}}]}`,
			wantError:  "LLM proxy response contained empty analysis content",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content, err := parseAnalysisResponse(test.statusCode, test.status, []byte(test.body))

			if test.wantError != "" {
				if err == nil {
					t.Fatal("parseAnalysisResponse() error = nil, want an error")
				}
				if !strings.Contains(err.Error(), test.wantError) {
					t.Errorf("parseAnalysisResponse() error = %q, want error containing %q", err, test.wantError)
				}
				if content != "" {
					t.Errorf("parseAnalysisResponse() content = %q, want empty content", content)
				}
				return
			}

			if err != nil {
				t.Fatalf("parseAnalysisResponse() unexpected error: %v", err)
			}
			if content != test.wantContent {
				t.Errorf("parseAnalysisResponse() content = %q, want %q", content, test.wantContent)
			}
		})
	}
}

func TestResponseSnippetTruncatesLargeResponses(t *testing.T) {
	body := []byte(strings.Repeat("x", maxErrorResponseLength+1))

	snippet := responseSnippet(body)

	wantLength := maxErrorResponseLength + len("...[truncated]")
	if len(snippet) != wantLength {
		t.Errorf("responseSnippet() length = %d, want %d", len(snippet), wantLength)
	}
	if !strings.HasSuffix(snippet, "...[truncated]") {
		t.Errorf("responseSnippet() = %q, want truncated suffix", snippet)
	}
}
