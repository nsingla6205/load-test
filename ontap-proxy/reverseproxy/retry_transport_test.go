package reverseproxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedRoundTripper returns a scripted sequence of (response, error) pairs,
// one per attempt, and records the request bodies it observed.
type scriptedRoundTripper struct {
	responses    []*http.Response
	errs         []error
	calls        int
	observedBody []string
	idleClosed   int
}

func (s *scriptedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	idx := s.calls
	s.calls++

	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		s.observedBody = append(s.observedBody, string(b))
	} else {
		s.observedBody = append(s.observedBody, "")
	}

	var resp *http.Response
	if idx < len(s.responses) {
		resp = s.responses[idx]
	}
	var err error
	if idx < len(s.errs) {
		err = s.errs[idx]
	}
	return resp, err
}

func (s *scriptedRoundTripper) CloseIdleConnections() { s.idleClosed++ }

func newTestRetryTransport(base http.RoundTripper) *retryTransport {
	return &retryTransport{
		base: base,
		config: retryConfig{
			maxRetries:  2,
			baseBackoff: time.Millisecond,
			maxBackoff:  5 * time.Millisecond,
		},
	}
}

func statusResp(code int) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}
}

func connResetErr() error {
	return errors.New("read tcp 1.2.3.4:443: connection reset by peer")
}

func TestRetryTransport_RetriesTransientErrorThenSucceeds(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, nil, statusResp(200)},
		errs:      []error{connResetErr(), connResetErr(), nil},
	}
	rt := newTestRetryTransport(base)

	req, err := http.NewRequest(http.MethodGet, "https://ontap/api/cluster", nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 3, base.calls, "should retry twice then succeed")
}

func TestRetryTransport_ExhaustsRetriesReturnsLastError(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, nil, nil},
		errs:      []error{connResetErr(), connResetErr(), connResetErr()},
	}
	rt := newTestRetryTransport(base)

	req, _ := http.NewRequest(http.MethodGet, "https://ontap/api/cluster", nil)
	_, err := rt.RoundTrip(req)
	require.Error(t, err)
	assert.Equal(t, 3, base.calls, "1 initial + 2 retries")
}

func TestRetryTransport_DoesNotRetryOnAnyStatusCode(t *testing.T) {
	// Even a 503 (a response, not an error) is returned as-is; status is not inspected.
	base := &scriptedRoundTripper{
		responses: []*http.Response{statusResp(503), statusResp(200)},
	}
	rt := newTestRetryTransport(base)

	req, _ := http.NewRequest(http.MethodGet, "https://ontap/api/cluster", nil)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, 1, base.calls, "status codes are never retried")
}

func TestRetryTransport_DoesNotRetryNonTransientError(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{errors.New("some permanent error"), nil},
	}
	rt := newTestRetryTransport(base)

	req, _ := http.NewRequest(http.MethodGet, "https://ontap/api/cluster", nil)
	_, err := rt.RoundTrip(req)
	require.Error(t, err)
	assert.Equal(t, 1, base.calls, "unknown error message is not retryable")
}

func TestRetryTransport_DoesNotRetryNonIdempotentMethod(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{connResetErr(), nil},
	}
	rt := newTestRetryTransport(base)

	req, _ := http.NewRequest(http.MethodPost, "https://ontap/api/cluster", strings.NewReader("{}"))
	_, err := rt.RoundTrip(req)
	require.Error(t, err)
	assert.Equal(t, 1, base.calls, "POST must not be retried")
}

func TestRetryTransport_DoesNotRetryContextCanceled(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{context.Canceled, nil},
	}
	rt := newTestRetryTransport(base)

	req, _ := http.NewRequest(http.MethodGet, "https://ontap/api/cluster", nil)
	_, err := rt.RoundTrip(req)
	require.Error(t, err)
	assert.Equal(t, 1, base.calls, "context cancellation is not retryable")
}

func TestRetryTransport_ReplaysBodyForIdempotentMethod(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{connResetErr(), nil},
	}
	rt := newTestRetryTransport(base)

	// http.NewRequest with a *strings.Reader auto-populates GetBody. GET is used because
	// it is an idempotent (retriable) method; the point is to verify body replay on retry.
	req, err := http.NewRequest(http.MethodGet, "https://ontap/api/volume", strings.NewReader(`{"size":1}`))
	require.NoError(t, err)
	require.NotNil(t, req.GetBody)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 2, base.calls)
	// Each attempt must see the full body.
	assert.Equal(t, `{"size":1}`, base.observedBody[0])
	assert.Equal(t, `{"size":1}`, base.observedBody[1])
}

func TestRetryTransport_DoesNotRetryWhenBodyNotReplayable(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{connResetErr(), nil},
	}
	rt := newTestRetryTransport(base)

	// Idempotent method but a streaming body with no GetBody => not replayable.
	req, _ := http.NewRequest(http.MethodGet, "https://ontap/api/volume", nil)
	req.Body = io.NopCloser(bytes.NewReader([]byte("data")))
	req.GetBody = nil

	_, err := rt.RoundTrip(req)
	require.Error(t, err)
	assert.Equal(t, 1, base.calls, "cannot replay body => no retry")
}

func TestRetryTransport_RespectsContextDeadlineDuringBackoff(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{connResetErr(), nil},
	}
	rt := &retryTransport{
		base: base,
		config: retryConfig{
			maxRetries:  3,
			baseBackoff: 200 * time.Millisecond,
			maxBackoff:  time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://ontap/api/cluster", nil)

	_, err := rt.RoundTrip(req)
	// Backoff outlasts the context deadline, so the last error is returned without a 2nd call.
	require.Error(t, err)
	assert.Equal(t, 1, base.calls)
}

func TestRetryTransport_CloseIdleConnectionsDelegates(t *testing.T) {
	base := &scriptedRoundTripper{}
	rt := newTestRetryTransport(base)
	rt.CloseIdleConnections()
	assert.Equal(t, 1, base.idleClosed)
}

func TestRetryTransport_BackoffExponentialWithCap(t *testing.T) {
	rt := &retryTransport{config: retryConfig{maxRetries: 5, baseBackoff: 100 * time.Millisecond, maxBackoff: 300 * time.Millisecond}}
	assert.Equal(t, 100*time.Millisecond, rt.backoffFor(0))
	assert.Equal(t, 200*time.Millisecond, rt.backoffFor(1))
	assert.Equal(t, 300*time.Millisecond, rt.backoffFor(2)) // 400ms capped to 300ms
}

func TestIsRetryableError(t *testing.T) {
	assert.False(t, isRetryableError(nil))
	assert.False(t, isRetryableError(context.Canceled))
	assert.False(t, isRetryableError(context.DeadlineExceeded))
	assert.True(t, isRetryableError(io.EOF))
	assert.True(t, isRetryableError(io.ErrUnexpectedEOF))
	assert.True(t, isRetryableError(errors.New("write: broken pipe")))
	assert.True(t, isRetryableError(&net.DNSError{Err: "timeout", IsTimeout: true}))
	assert.True(t, isRetryableError(errors.New("net/http: timeout awaiting response headers")))
	// "connection refused" and bare "i/o timeout" are intentionally not treated as
	// transient transport signals, so they must not be retried by message match.
	assert.False(t, isRetryableError(errors.New("connection refused")))
	assert.False(t, isRetryableError(errors.New("read tcp 1.2.3.4:443: i/o timeout")))
	assert.False(t, isRetryableError(errors.New("some permanent error")))
}

func TestRetryTransport_RetriesTimeoutAwaitingResponseHeaders(t *testing.T) {
	// net/http surfaces ResponseHeaderTimeout as this transport error; it is transient
	// and should be retried for a retriable request, then succeed.
	headerTimeoutErr := errors.New("net/http: timeout awaiting response headers")
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{headerTimeoutErr, nil},
	}
	rt := newTestRetryTransport(base)

	req, err := http.NewRequest(http.MethodGet, "https://ontap/api/cluster", nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, base.calls, "header-timeout error should be retried then succeed")
}

func TestIsIdempotentMethod(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace} {
		assert.True(t, isIdempotentMethod(m), "%s should be idempotent", m)
	}
	// PUT and DELETE are intentionally excluded: a write whose response is lost could
	// be replayed and cause duplicate side effects, so they are not auto-retried.
	for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPost, http.MethodPatch} {
		assert.False(t, isIdempotentMethod(m), "%s should not be idempotent", m)
	}
}

func cliRequest(t *testing.T, input string) *http.Request {
	t.Helper()
	body := `{"input":` + strconv.Quote(input) + `,"privilege":"admin"}`
	req, err := http.NewRequest(http.MethodPost, "https://ontap"+cliEndpointPath, strings.NewReader(body))
	require.NoError(t, err)
	require.NotNil(t, req.GetBody)
	return req
}

func TestIsReadOnlyCLICommand(t *testing.T) {
	readOnly := []string{
		"volume show",
		"volume show -vserver vs1 -volume vol1",
		"storage aggregate show",
		"set diag; volume show",
	}
	for _, in := range readOnly {
		assert.True(t, isReadOnlyCLICommand(in), "%q should be read-only", in)
	}

	mutating := []string{
		"volume create -vserver vs1 -volume vol1 -size 100g",
		"volume delete -vserver vs1 -volume vol1",
		"volume modify -vserver vs1 -volume vol1 -size 200g",
		"set diag; volume create -vserver vs1 -volume vol1 -size 100g",
		"set diag",
		"",
		// A non-"set" prefix is not a valid chain, so the chained "show" is ignored.
		"volume show; volume create -vserver vs1",
		// More than two chained commands are rejected outright.
		"set diag; volume show; volume create",
	}
	for _, in := range mutating {
		assert.False(t, isReadOnlyCLICommand(in), "%q should not be read-only", in)
	}
}

func TestPrimaryCLICommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"single command", "volume show", "volume show"},
		{"single command trims spaces", "  volume show  ", "volume show"},
		{"set prefix returns primary", "set diag; volume show", "volume show"},
		{"set prefix case insensitive", "SET diag; volume show", "volume show"},
		{"non-set prefix rejected", "volume show; volume create", ""},
		{"three commands rejected", "set diag; volume show; volume create", ""},
		{"empty input", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, primaryCLICommand(tt.input))
		})
	}
}

func TestCLISubcommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{"two-word command", "volume show", "show"},
		{"multi-word command", "storage aggregate show", "show"},
		{"stops at first flag", "volume create -size 1g", "create"},
		{"stops at first arg with value", "volume show -vserver vs1", "show"},
		{"single token has no action", "set", ""},
		{"empty command", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cliSubcommand(tt.command))
		})
	}
}

func TestIsReadOnlyCLIRequest(t *testing.T) {
	assert.True(t, isReadOnlyCLIRequest(cliRequest(t, "volume show")), "POST show command to CLI endpoint should be read-only")

	assert.False(t, isReadOnlyCLIRequest(cliRequest(t, "volume create -vserver vs1 -volume vol1 -size 100g")),
		"POST mutating command to CLI endpoint should not be read-only")

	getReq, err := http.NewRequest(http.MethodGet, "https://ontap"+cliEndpointPath, nil)
	require.NoError(t, err)
	assert.False(t, isReadOnlyCLIRequest(getReq), "non-POST to CLI endpoint should not be a read-only CLI request")

	otherReq, err := http.NewRequest(http.MethodPost, "https://ontap/api/storage/volumes", strings.NewReader(`{"input":"volume show"}`))
	require.NoError(t, err)
	assert.False(t, isReadOnlyCLIRequest(otherReq), "POST to a non-CLI endpoint should not be a read-only CLI request")
}

func TestRetryTransport_RetriesReadOnlyCLICommand(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{connResetErr(), nil},
	}
	rt := newTestRetryTransport(base)

	resp, err := rt.RoundTrip(cliRequest(t, "volume show -vserver vs1"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, base.calls, "read-only CLI command should be retried")
	// The body inspection must not consume the body sent to the base transport.
	assert.Equal(t, `{"input":"volume show -vserver vs1","privilege":"admin"}`, base.observedBody[0])
	assert.Equal(t, base.observedBody[0], base.observedBody[1], "body must be replayed intact on retry")
}

func TestRetryTransport_RetriesChainedReadOnlyCLICommand(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{connResetErr(), nil},
	}
	rt := newTestRetryTransport(base)

	resp, err := rt.RoundTrip(cliRequest(t, "set diag; volume show"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, base.calls, "chained read-only CLI command should be retried")
}

func TestRetryTransport_DoesNotRetryMutatingCLICommand(t *testing.T) {
	base := &scriptedRoundTripper{
		responses: []*http.Response{nil, statusResp(200)},
		errs:      []error{connResetErr(), nil},
	}
	rt := newTestRetryTransport(base)

	_, err := rt.RoundTrip(cliRequest(t, "volume create -vserver vs1 -volume vol1 -size 100g"))
	require.Error(t, err)
	assert.Equal(t, 1, base.calls, "mutating CLI command must not be retried")
}
