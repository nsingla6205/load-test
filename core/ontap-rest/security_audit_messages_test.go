package ontap_rest

import (
	"context"
	"encoding/json"
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
func (r *fakeClientRequest) GetQueryParams() url.Values                 { return r.query }
func (r *fakeClientRequest) SetHeaderParam(_ string, _ ...string) error { return nil }
func (r *fakeClientRequest) GetHeaderParams() http.Header               { return http.Header{} }
func (r *fakeClientRequest) SetFormParam(_ string, _ ...string) error   { return nil }
func (r *fakeClientRequest) SetPathParam(_ string, _ string) error      { return nil }
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

// TestHasLoginPasswordResetSuccess_SendsExpectedFilters pins the audit-messages
// filters built by HasLoginPasswordResetSuccess itself (mirroring the operational
// curl), so a change to the production query cannot go unnoticed.
func TestHasLoginPasswordResetSuccess_SendsExpectedFilters(t *testing.T) {
	// A non-UTC zone with fractional seconds exercises both the UTC conversion and
	// the second-precision formatting of the timestamp filter.
	ist := time.FixedZone("IST", 5*60*60+30*60)
	since := time.Date(2026, time.July, 30, 15, 30, 0, 402_000_000, ist)

	tests := []struct {
		username  string
		wantInput string
	}{
		{
			username:  "ociadmin",
			wantInput: `security login password -username ociadmin|POST /api/security/authentication/password*"name":"ociadmin"*`,
		},
		{
			// The username must be substituted into both alternatives.
			username:  "opsadmin",
			wantInput: `security login password -username opsadmin|POST /api/security/authentication/password*"name":"opsadmin"*`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.username, func(t *testing.T) {
			transport := &capturingTransport{response: &security.SecurityAuditLogCollectionGetOK{
				Payload: &models.SecurityAuditLogResponse{NumRecords: nillable.ToPointer(int64(1))},
			}}
			api := security.New(transport, nil)
			client := &securityClient{api: &api}

			consumed, err := client.HasLoginPasswordResetSuccess(context.Background(), tc.username, since)
			require.NoError(t, err)
			assert.True(t, consumed)
			require.NotNil(t, transport.lastOp)

			req := newFakeClientRequest()
			require.NoError(t, transport.lastOp.Params.WriteToRequest(req, strfmt.Default))

			q := req.GetQueryParams()
			assert.Equal(t, tc.username, q.Get("user"))
			assert.Equal(t, tc.wantInput, q.Get("input"))
			assert.Equal(t, "success", q.Get("state"))
			assert.Equal(t, ">=2026-07-30T10:00:00Z", q.Get("timestamp"))
			assert.Equal(t, "false", q.Get("return_records"))
			assert.Equal(t, securityAuditMessagesFields, q.Get("fields"))

			// ONTAP rejects a fractional-seconds timestamp (e.g. …59.402Z) for this
			// field, so guard against a strfmt.DateTime-style value creeping back in.
			assert.NotContains(t, q.Get("timestamp"), ".", "timestamp filter must not contain fractional seconds")
		})
	}
}

// TestSecurityAuditLogCollectionGetReader_Success decodes a 200 response for each
// audit-record format a password change can produce (captured from a live VSA)
// and confirms num_records and the record input are surfaced on the typed payload.
func TestSecurityAuditLogCollectionGetReader_Success(t *testing.T) {
	tests := []struct {
		name        string
		application string
		input       string
	}{
		{
			name:        "cli record",
			application: "ssh",
			input:       `security login password -username ociadmin`,
		},
		{
			name:        "rest record from system manager",
			application: "http",
			input:       `POST /api/security/authentication/password : ["X-Dot-Client-App: SMv4"] {"name":"ociadmin","password":***}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := &security.SecurityAuditLogCollectionGetReader{}
			body, err := json.Marshal(map[string]interface{}{
				"num_records": 1,
				"records": []map[string]string{{
					"user":        "ociadmin",
					"application": tc.application,
					"input":       tc.input,
					"state":       "success",
				}},
			})
			require.NoError(t, err)

			result, err := reader.ReadResponse(&fakeClientResponse{code: 200, body: string(body)}, runtime.JSONConsumer())
			require.NoError(t, err)

			ok, isOK := result.(*security.SecurityAuditLogCollectionGetOK)
			require.True(t, isOK)
			require.NotNil(t, ok.Payload)
			require.NotNil(t, ok.Payload.NumRecords)
			assert.Equal(t, int64(1), *ok.Payload.NumRecords)
			require.Len(t, ok.Payload.SecurityAuditLogResponseInlineRecords, 1)
			record := ok.Payload.SecurityAuditLogResponseInlineRecords[0]
			require.NotNil(t, record.Input)
			assert.Equal(t, tc.input, *record.Input)
		})
	}
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
