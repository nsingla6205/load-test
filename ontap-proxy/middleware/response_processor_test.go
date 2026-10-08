package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/ruleengine/dsl"
)

type errCloser struct {
	io.Reader
}

func (errCloser) Close() error {
	return errors.New("close failed")
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (errReader) Close() error {
	return nil
}

func TestProcessResponseModification(t *testing.T) {
	t.Run("WhenResponseIsNil_ShouldReturnNil", func(t *testing.T) {
		err := ProcessResponseModification(nil)
		assert.NoError(t, err)
	})

	t.Run("WhenRequestIsNil_ShouldReturnNil", func(t *testing.T) {
		resp := &http.Response{}
		err := ProcessResponseModification(resp)
		assert.NoError(t, err)
	})

	t.Run("WhenNoRuleContextInRequest_ShouldReturnNil", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		resp := &http.Response{
			Request: req,
			Body:    io.NopCloser(bytes.NewBufferString(`{"name": "test"}`)),
		}

		err := ProcessResponseModification(resp)

		assert.NoError(t, err)
	})

	t.Run("WhenContextHasInvalidType_ShouldReturnNil", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(context.Background(), models.RuleContextKey, "invalid")
		req = req.WithContext(ctx)
		resp := &http.Response{
			Request: req,
			Body:    io.NopCloser(bytes.NewBufferString(`{"name": "test"}`)),
		}

		err := ProcessResponseModification(resp)

		assert.NoError(t, err)
	})

	t.Run("WhenContextHasValidAction_ShouldProcessResponse", func(t *testing.T) {
		action := dsl.Allow{
			Name: "Test Action",
			ModifyResponse: dsl.RemoveFields{
				Fields: []string{"$.sensitive"},
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(context.Background(), models.RuleContextKey, action)
		req = req.WithContext(ctx)

		resp := &http.Response{
			StatusCode: 200,
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(`{"name": "test", "sensitive": "secret"}`)),
			Header:     make(http.Header),
		}

		err := ProcessResponseModification(resp)

		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		assert.NotContains(t, string(body), "sensitive")
	})

	t.Run("WhenActionProcessResponseSucceeds_ShouldReturnNil", func(t *testing.T) {
		action := dsl.Allow{
			Name: "Test Action",
		}

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(context.Background(), models.RuleContextKey, action)
		req = req.WithContext(ctx)

		resp := &http.Response{
			StatusCode: 200,
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(`{"name": "test"}`)),
			Header:     make(http.Header),
		}

		err := ProcessResponseModification(resp)

		assert.NoError(t, err)
	})
}

func TestProcessResponseAndRecordBackendMetrics(t *testing.T) {
	t.Run("WhenResponseIsNil_ShouldReturnNil", func(t *testing.T) {
		err := ProcessResponseAndRecordBackendMetrics(nil)
		assert.NoError(t, err)
	})

	t.Run("WhenRequestIsNil_ShouldReturnNil", func(t *testing.T) {
		resp := &http.Response{
			StatusCode: 200,
			Request:    nil,
			Body:       io.NopCloser(bytes.NewBufferString("")),
		}
		err := ProcessResponseAndRecordBackendMetrics(resp)
		assert.NoError(t, err)
	})

	t.Run("WhenOntapReturns500WithClientErrorCode917621_ShouldRewriteTo400", func(t *testing.T) {
		orig := ontapClientErrorCodes
		ontapClientErrorCodes = parseOntapClientErrorCodes("917621")
		defer func() { ontapClientErrorCodes = orig }()

		body := `{"error":{"code":"917621","message":"Only volumes of type \"RW\" can be mounted during create."}}`
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}

		_ = ProcessResponseAndRecordBackendMetrics(resp)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		respBody, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(respBody), "917621")
	})

	t.Run("WhenOntapReturns500WithTransferInProgress13303812_ShouldRewriteTo400", func(t *testing.T) {
		orig := ontapClientErrorCodes
		ontapClientErrorCodes = parseOntapClientErrorCodes("13303812")
		defer func() { ontapClientErrorCodes = orig }()

		body := `{"error":{"code":"13303812","message":"Initialize operation failed. Another transfer is in progress."}}`
		req := httptest.NewRequest(http.MethodPost, "/api/snapmirror/relationships/transfers", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}

		_ = ProcessResponseAndRecordBackendMetrics(resp)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "400 Bad Request", resp.Status)

		respBody, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(respBody), "13303812")
		assert.Contains(t, string(respBody), "Another transfer is in progress.")
	})

	t.Run("WhenOntapReturns500WithUnknownErrorCode_ShouldKeep500", func(t *testing.T) {
		body := `{"error":{"code":"999999","message":"Some other ONTAP error."}}`
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}

		_ = ProcessResponseAndRecordBackendMetrics(resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("WhenOntapReturns500WithNoBody_ShouldKeep500", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       nil,
			Header:     make(http.Header),
		}

		_ = ProcessResponseAndRecordBackendMetrics(resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("WhenOntapReturns500WithInvalidJSON_ShouldKeep500", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString("not json")),
			Header:     make(http.Header),
		}

		_ = ProcessResponseAndRecordBackendMetrics(resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("WhenOntapReturns500WithCodeFromEnvConfig_ShouldRewriteTo400", func(t *testing.T) {
		orig := ontapClientErrorCodes
		ontapClientErrorCodes = parseOntapClientErrorCodes("917621,123456")
		defer func() { ontapClientErrorCodes = orig }()

		body := `{"error":{"code":"123456","message":"Some configured client error."}}`
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}

		_ = ProcessResponseAndRecordBackendMetrics(resp)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("WhenOntapReturns200_ShouldNotRewrite", func(t *testing.T) {
		body := `{"error":{"code":"917621","message":"Only volumes of type \"RW\" can be mounted during create."}}`
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 200,
			Status:     "200 OK",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}

		_ = ProcessResponseAndRecordBackendMetrics(resp)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestRewriteOntap500ToClientError(t *testing.T) {
	t.Run("WhenBodyCloseErrors_ShouldStillRewriteAndPreserveBody", func(t *testing.T) {
		orig := ontapClientErrorCodes
		ontapClientErrorCodes = parseOntapClientErrorCodes("917621")
		defer func() { ontapClientErrorCodes = orig }()

		body := `{"error":{"code":"917621","message":"test"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       errCloser{Reader: bytes.NewBufferString(body)},
			Header:     make(http.Header),
		}

		rewriteOntap500ToClientError(resp)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		respBody, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(respBody), "917621")
	})

	t.Run("WhenBodyReadErrors_ShouldKeep500AndSetEmptyBody", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       errReader{},
			Header:     make(http.Header),
		}

		rewriteOntap500ToClientError(resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		respBody, _ := io.ReadAll(resp.Body)
		assert.Empty(t, string(respBody))
	})

	t.Run("WhenNon500StatusCode_ShouldNotModify", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 403,
			Status:     "403 Forbidden",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"code":"917621"}}`)),
			Header:     make(http.Header),
		}

		rewriteOntap500ToClientError(resp)

		assert.Equal(t, 403, resp.StatusCode)
	})

	t.Run("WhenNilBody_ShouldKeep500", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       nil,
			Header:     make(http.Header),
		}

		rewriteOntap500ToClientError(resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("WhenInvalidJSON_ShouldKeep500AndPreserveBody", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString("not json")),
			Header:     make(http.Header),
		}

		rewriteOntap500ToClientError(resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		respBody, _ := io.ReadAll(resp.Body)
		assert.Equal(t, "not json", string(respBody))
	})

	t.Run("WhenMatchingCode_ShouldRewriteTo400AndPreserveBody", func(t *testing.T) {
		orig := ontapClientErrorCodes
		ontapClientErrorCodes = parseOntapClientErrorCodes("917621")
		defer func() { ontapClientErrorCodes = orig }()

		body := `{"error":{"code":"917621","message":"Only volumes of type \"RW\" can be mounted during create."}}`
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}

		rewriteOntap500ToClientError(resp)

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		respBody, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(respBody), "917621")
	})

	t.Run("WhenNonMatchingCode_ShouldKeep500AndPreserveBody", func(t *testing.T) {
		body := `{"error":{"code":"999999","message":"Unknown error."}}`
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", nil)
		resp := &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Request:    req,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}

		rewriteOntap500ToClientError(resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		respBody, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(respBody), "999999")
	})
}

func TestParseOntapClientErrorCodes(t *testing.T) {
	t.Run("SingleCode", func(t *testing.T) {
		codes := parseOntapClientErrorCodes("917621")
		assert.True(t, codes["917621"])
		assert.Len(t, codes, 1)
	})

	t.Run("MultipleCodes", func(t *testing.T) {
		codes := parseOntapClientErrorCodes("917621,123456,789012")
		assert.True(t, codes["917621"])
		assert.True(t, codes["123456"])
		assert.True(t, codes["789012"])
		assert.Len(t, codes, 3)
	})

	t.Run("WithWhitespace", func(t *testing.T) {
		codes := parseOntapClientErrorCodes(" 917621 , 123456 ")
		assert.True(t, codes["917621"])
		assert.True(t, codes["123456"])
		assert.Len(t, codes, 2)
	})

	t.Run("EmptyString", func(t *testing.T) {
		codes := parseOntapClientErrorCodes("")
		assert.Len(t, codes, 0)
	})

	t.Run("TrailingComma", func(t *testing.T) {
		codes := parseOntapClientErrorCodes("917621,")
		assert.True(t, codes["917621"])
		assert.Len(t, codes, 1)
	})
}
