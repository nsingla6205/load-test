package reverseproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// idempotentMethods are HTTP methods that are safe to retry without risking
// duplicate side effects. POST and PATCH are deliberately excluded.
var idempotentMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodOptions: {},
	http.MethodTrace:   {},
}

const (
	// cliEndpointPath is the ONTAP private CLI endpoint. CLI commands are always sent
	// as POST, so they bypass the idempotent-method check; only read-only CLI commands
	// to this endpoint are treated as retriable on transient transport errors.
	cliEndpointPath = "/api/private/cli"

	// maxCLIBodyInspectBytes caps how much of the CLI request body is read when
	// deciding whether the command is read-only. CLI payloads are tiny.
	maxCLIBodyInspectBytes = 64 * 1024
)

// retryConfig holds the tunables for the chained retry transport.
type retryConfig struct {
	// maxRetries is the number of additional attempts after the first one.
	// maxRetries=2 means up to 3 total attempts.
	maxRetries  int
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

// retryTransport is an http.RoundTripper that wraps another RoundTripper and
// retries a small, curated set of transient transport errors (matched by error
// message / type) for retriable requests with a replayable body. Retriable means
// an idempotent HTTP method or a read-only ("show") ONTAP CLI command (CLI requests
// are always POST, so they are classified by the command rather than the method).
//
// Retries are driven purely by the transport error — HTTP status codes are not
// inspected. A response (any status code) is always returned to the caller as-is.
//
// It is chained in front of the pooled *http.Transport in createClient, so it
// transparently covers both the reverse-proxy path (PooledAuthTransport ->
// client.Do) and the RestOntapClient path (client.Do).
type retryTransport struct {
	base   http.RoundTripper
	config retryConfig
}

// newRetryTransport wraps base with retry behavior driven by env tunables.
func newRetryTransport(base http.RoundTripper) *retryTransport {
	return &retryTransport{
		base: base,
		config: retryConfig{
			maxRetries:  ontapRetryMaxRetries,
			baseBackoff: time.Duration(ontapRetryBaseBackoffMs) * time.Millisecond,
			maxBackoff:  time.Duration(ontapRetryMaxBackoffMs) * time.Millisecond,
		},
	}
}

// RoundTrip implements http.RoundTripper with bounded retries for transient
// transport errors. It never retries based on the HTTP status code.
func (rt *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	logger := util.GetLogger(req.Context())

	// A request can be retried only if its body can be replayed for each attempt.
	replayable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
	retriableMethod := isRetriableRequest(req)

	var resp *http.Response
	var err error

	for attempt := 0; ; attempt++ {
		// Reset the body for retries (attempt > 0). The base transport consumes
		// and closes req.Body, so it must be re-established from GetBody.
		if attempt > 0 && req.Body != nil && req.Body != http.NoBody {
			newBody, bodyErr := req.GetBody()
			if bodyErr != nil {
				return nil, bodyErr
			}
			req.Body = newBody
		}

		resp, err = rt.base.RoundTrip(req)

		// Success (a response was produced) is returned as-is regardless of status.
		if err == nil {
			return resp, nil
		}

		// Stop when attempts are exhausted, the request isn't safe/possible to
		// replay, or the error is not a known transient transport error.
		if attempt >= rt.config.maxRetries || !retriableMethod || !replayable || !isRetryableError(err) {
			return resp, err
		}

		backoff := rt.backoffFor(attempt)
		logger.WarnContext(req.Context(), "Retrying ONTAP backend request on transient error",
			"method", req.Method,
			"url", req.URL.Path,
			"attempt", attempt+1,
			"max_retries", rt.config.maxRetries,
			"backoff_ms", backoff.Milliseconds(),
			"error", err.Error(),
		)

		if waitErr := sleepWithContext(req.Context(), backoff); waitErr != nil {
			// Context cancelled/expired while waiting; return the last result.
			return resp, err
		}
	}
}

// CloseIdleConnections delegates to the wrapped transport when supported, so the
// connection pool's cleanup/Close logic keeps closing idle connections after the
// transport is chained.
func (rt *retryTransport) CloseIdleConnections() {
	type idleCloser interface{ CloseIdleConnections() }
	if c, ok := rt.base.(idleCloser); ok {
		c.CloseIdleConnections()
	}
}

// backoffFor returns the exponential delay before the next attempt, capped at maxBackoff.
func (rt *retryTransport) backoffFor(attempt int) time.Duration {
	backoff := rt.config.baseBackoff << attempt // base * 2^attempt
	if backoff <= 0 || backoff > rt.config.maxBackoff {
		return rt.config.maxBackoff
	}
	return backoff
}

func isIdempotentMethod(method string) bool {
	_, ok := idempotentMethods[strings.ToUpper(method)]
	return ok
}

// isRetriableRequest reports whether req may be retried on a transient transport
// error. Idempotent HTTP methods are always retriable. CLI commands are always sent
// as POST, so they are handled separately: only read-only ("show") CLI commands are
// retriable, since replaying a mutating CLI command could cause duplicate side effects.
func isRetriableRequest(req *http.Request) bool {
	if isIdempotentMethod(req.Method) {
		return true
	}
	return isReadOnlyCLIRequest(req)
}

// isReadOnlyCLIRequest reports whether req targets the ONTAP private CLI endpoint with
// a read-only command. The CLI command is read from a replayable copy of the request
// body (via GetBody), so the original req.Body is left untouched for the actual attempt.
func isReadOnlyCLIRequest(req *http.Request) bool {
	if req.Method != http.MethodPost || req.URL == nil || req.URL.Path != cliEndpointPath {
		return false
	}
	if req.GetBody == nil {
		return false
	}

	bodyReader, err := req.GetBody()
	if err != nil {
		return false
	}
	defer func() { _ = bodyReader.Close() }()

	data, err := io.ReadAll(io.LimitReader(bodyReader, maxCLIBodyInspectBytes))
	if err != nil {
		return false
	}

	var cliReq struct {
		Input string `json:"input"`
	}
	if err := json.Unmarshal(data, &cliReq); err != nil {
		return false
	}
	return isReadOnlyCLICommand(cliReq.Input)
}

// isReadOnlyCLICommand reports whether an ONTAP CLI command string is read-only and
// therefore safe to retry. A command is read-only when its primary action is "show"
// (e.g. "volume show" or "set diag; volume show"). A leading "set" prefix is ignored
// because it only configures output/privilege for the chained command.
//
// This uses a deliberately minimal parse (it does not depend on the CLI rule engine)
// and errs on the side of caution: anything it cannot confidently classify as a
// "show" command is treated as not retriable.
func isReadOnlyCLICommand(input string) bool {
	return strings.EqualFold(cliSubcommand(primaryCLICommand(input)), "show")
}

// primaryCLICommand returns the command whose action determines retriability. CLI
// input may chain at most two commands separated by ";", where the first must be a
// privilege "set" prefix; in that case the second (primary) command is returned. Any
// input outside that shape yields an empty string so the caller treats it as not
// retriable.
func primaryCLICommand(input string) string {
	parts := strings.Split(input, ";")
	switch len(parts) {
	case 1:
		return strings.TrimSpace(parts[0])
	case 2:
		if !strings.EqualFold(firstToken(parts[0]), "set") {
			return ""
		}
		return strings.TrimSpace(parts[1])
	default:
		return ""
	}
}

// cliSubcommand returns the action token of a CLI command: the last word before the
// first "-"-prefixed argument. For "storage aggregate show -fields x" it returns
// "show"; for "volume create -size 1g" it returns "create". A command with fewer than
// two leading words (e.g. "set" alone) has no action and returns an empty string.
func cliSubcommand(command string) string {
	var cmdTokens []string
	for _, token := range strings.Fields(command) {
		if strings.HasPrefix(token, "-") {
			break
		}
		cmdTokens = append(cmdTokens, token)
	}
	if len(cmdTokens) < 2 {
		return ""
	}
	return cmdTokens[len(cmdTokens)-1]
}

// firstToken returns the first whitespace-delimited token of s, or an empty string.
func firstToken(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// isRetryableError reports whether a transport error is transient and worth
// retrying, based purely on the error type/message. Context cancellation/expiry
// is never retried.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	msg := strings.ToLower(err.Error())
	transientSignals := []string{
		"connection reset by peer",
		"broken pipe",
		"server closed idle connection",
		"http2: server sent goaway",
		"timeout awaiting response headers",
	}
	for _, sig := range transientSignals {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}

// sleepWithContext waits for d or until the context is done, whichever first.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
