package ontap_rest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/security"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// fakeClientResponse is a minimal runtime.ClientResponse backed by a fixed status
// code and JSON body, used to exercise the generated audit-messages response reader.
type fakeClientResponse struct {
	code int
	body string
}

func (f *fakeClientResponse) Code() int                    { return f.code }
func (f *fakeClientResponse) Message() string              { return "" }
func (f *fakeClientResponse) GetHeader(_ string) string    { return "" }
func (f *fakeClientResponse) GetHeaders(_ string) []string { return nil }
func (f *fakeClientResponse) Body() io.ReadCloser          { return io.NopCloser(strings.NewReader(f.body)) }

// fakeClientRequest is a minimal runtime.ClientRequest that records the query
// parameters written by a generated params WriteToRequest, so a test can assert
// the exact filters the client sends to ONTAP.
type fakeClientRequest struct {
	query url.Values
}

func newFakeClientRequest() *fakeClientRequest { return &fakeClientRequest{query: url.Values{}} }

func (r *fakeClientRequest) SetQueryParam(name string, values ...string) error {
	r.query[name] = values
	return nil
}
func (r *fakeClientRequest) GetQueryParams() url.Values                        { return r.query }
func (r *fakeClientRequest) SetHeaderParam(_ string, _ ...string) error        { return nil }
func (r *fakeClientRequest) GetHeaderParams() http.Header                      { return http.Header{} }
func (r *fakeClientRequest) SetFormParam(_ string, _ ...string) error          { return nil }
func (r *fakeClientRequest) SetPathParam(_ string, _ string) error             { return nil }
func (r *fakeClientRequest) SetFileParam(_ string, _ ...runtime.NamedReadCloser) error {
	return nil
}
func (r *fakeClientRequest) SetBodyParam(_ interface{}) error { return nil }
func (r *fakeClientRequest) SetTimeout(_ time.Duration) error { return nil }
func (r *fakeClientRequest) GetMethod() string                { return http.MethodGet }
func (r *fakeClientRequest) GetPath() string                  { return "/security/audit/messages" }
func (r *fakeClientRequest) GetBody() []byte                  { return nil }
func (r *fakeClientRequest) GetBodyParam() interface{}        { return nil }
func (r *fakeClientRequest) GetFileParam() map[string][]runtime.NamedReadCloser {
	return nil
}

// TestSecurityAuditLogCollectionGetParams_WriteToRequest verifies that the
// generated params serialize the exact audit-messages filters the temp-password
// verifier relies on (mirroring the operational curl).
func TestSecurityAuditLogCollectionGetParams_WriteToRequest(t *testing.T) {
	username := "ociadmin"
	input := "security login password -username ociadmin"
	state := "success"
	returnRecords := "true"
	since := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	timestamp := ">=" + since.Format(time.RFC3339)

	params := security.NewSecurityAuditLogCollectionGetParams()
	params.SetUser(&username)
	params.SetInput(&input)
	params.SetState(&state)
	params.SetTimestamp(&timestamp)
	params.SetReturnRecords(&returnRecords)
	params.SetFields(strings.Split(securityAuditMessagesFields, ","))

	req := newFakeClientRequest()
	require.NoError(t, params.WriteToRequest(req, strfmt.Default))

	q := req.GetQueryParams()
	assert.Equal(t, username, q.Get("user"))
	assert.Equal(t, input, q.Get("input"))
	assert.Equal(t, state, q.Get("state"))
	assert.Equal(t, timestamp, q.Get("timestamp"))
	assert.Equal(t, returnRecords, q.Get("return_records"))
	assert.Equal(t, securityAuditMessagesFields, q.Get("fields"))

	// ONTAP rejects a fractional-seconds timestamp (e.g. …59.402Z) for this
	// field, so guard against a strfmt.DateTime-style value creeping back in.
	assert.NotContains(t, q.Get("timestamp"), ".", "timestamp filter must not contain fractional seconds")
}

// TestSecurityAuditLogCollectionGetReader_Success decodes a 200 response and
// confirms num_records is surfaced on the typed payload.
func TestSecurityAuditLogCollectionGetReader_Success(t *testing.T) {
	reader := &security.SecurityAuditLogCollectionGetReader{}
	body := `{"num_records":1,"records":[{"user":"ociadmin","input":"security login password -username ociadmin","state":"success"}]}`

	result, err := reader.ReadResponse(&fakeClientResponse{code: 200, body: body}, runtime.JSONConsumer())
	require.NoError(t, err)

	ok, isOK := result.(*security.SecurityAuditLogCollectionGetOK)
	require.True(t, isOK)
	require.NotNil(t, ok.Payload)
	require.NotNil(t, ok.Payload.NumRecords)
	assert.Equal(t, int64(1), *ok.Payload.NumRecords)
	assert.Len(t, ok.Payload.SecurityAuditLogResponseInlineRecords, 1)
}

// TestSecurityAuditLogCollectionGetReader_Error maps a non-2xx response to the
// typed default error.
func TestSecurityAuditLogCollectionGetReader_Error(t *testing.T) {
	reader := &security.SecurityAuditLogCollectionGetReader{}
	body := `{"error":{"code":"1234","message":"invalid query"}}`

	result, err := reader.ReadResponse(&fakeClientResponse{code: 400, body: body}, runtime.JSONConsumer())
	require.Error(t, err)
	assert.Nil(t, result)

	def, isDefault := err.(*security.SecurityAuditLogCollectionGetDefault)
	require.True(t, isDefault)
	assert.Equal(t, 400, def.Code())
}

func TestHasLoginPasswordResetSuccess(t *testing.T) {
	since := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)

	t.Run("empty username", func(t *testing.T) {
		client := &securityClient{}
		consumed, err := client.HasLoginPasswordResetSuccess(context.Background(), "", since)
		require.Error(t, err)
		assert.False(t, consumed)
		assert.Contains(t, err.Error(), "username is required")
	})

	t.Run("api error", func(t *testing.T) {
		transport := &mockTransport{err: errors.New("ontap down")}
		api := security.New(transport, nil)
		client := &securityClient{api: &api}

		consumed, err := client.HasLoginPasswordResetSuccess(context.Background(), "ociadmin", since)
		require.Error(t, err)
		assert.False(t, consumed)
		assert.Contains(t, err.Error(), "ontap down")
	})

	t.Run("nil response payload", func(t *testing.T) {
		transport := &mockTransport{response: &security.SecurityAuditLogCollectionGetOK{}}
		api := security.New(transport, nil)
		client := &securityClient{api: &api}

		consumed, err := client.HasLoginPasswordResetSuccess(context.Background(), "ociadmin", since)
		require.Error(t, err)
		assert.False(t, consumed)
		assert.Contains(t, err.Error(), "unexpected response")
	})

	t.Run("consumed when num_records positive", func(t *testing.T) {
		transport := &mockTransport{response: &security.SecurityAuditLogCollectionGetOK{
			Payload: &models.SecurityAuditLogResponse{
				NumRecords: nillable.ToPointer(int64(3)),
			},
		}}
		api := security.New(transport, nil)
		client := &securityClient{api: &api}

		consumed, err := client.HasLoginPasswordResetSuccess(context.Background(), "ociadmin", since)
		require.NoError(t, err)
		assert.True(t, consumed)
	})

	t.Run("not consumed when no records", func(t *testing.T) {
		transport := &mockTransport{response: &security.SecurityAuditLogCollectionGetOK{
			Payload: &models.SecurityAuditLogResponse{
				NumRecords: nillable.ToPointer(int64(0)),
			},
		}}
		api := security.New(transport, nil)
		client := &securityClient{api: &api}

		consumed, err := client.HasLoginPasswordResetSuccess(context.Background(), "ociadmin", since)
		require.NoError(t, err)
		assert.False(t, consumed)
	})
}
