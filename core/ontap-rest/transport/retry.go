package transport

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/go-openapi/runtime"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

const (
	ontapTransportTimeoutDefault = 30
)

var (
	maxRetries      = env.GetIntNotNegative("ONTAP_REST_RETRIES", 5)
	maxOAuthRetries = env.GetIntNotNegative("ONTAP_REST_OAUTH_RETRIES", 5)

	// MD: meddling with this value is only useful when manipulating Ontap HTTP responses and is dangerous otherwise.
	// Therefore it will not be exported in the kubernetes deployments file and to be manually added to it when required in TESTING and NEVER in PROD.
	ontapTransportTimeout = time.Duration(env.GetIntNotNegative("ONTAP_REST_TRANSPORT_TIMEOUT_SECONDS", ontapTransportTimeoutDefault)) * time.Second
)

// RetryTransport is a wrapper for ClientTransport that performs a retry on transient errors
type RetryTransport struct {
	trace     log.Logger
	transport runtime.ClientTransport
	// usesOAuth bool
}

// NewRetryTransport returns a new instance of RetryTransport
func NewRetryTransport(trace log.Logger, transport runtime.ClientTransport) runtime.ClientTransport {
	return &RetryTransport{
		trace:     trace,
		transport: transport,
	}
}

type timeoutable interface {
	SetTimeout(timeout time.Duration)
}

// Submit submits the transport operation and retries if there is an error that is deemed retryable
func (t *RetryTransport) Submit(operation *runtime.ClientOperation) (result interface{}, err error) {
	if ontapTransportTimeout != ontapTransportTimeoutDefault {
		if tt, ok := operation.Params.(timeoutable); ok {
			tt.SetTimeout(ontapTransportTimeout)
		}
	}

	var authAttempts int
	for retries := 0; retries < maxRetries; {
		result, err = t.transport.Submit(operation)
		if err != nil {
			if rerr, ok := isRetriableError(err); ok {
				if rerr.isAuthError {
					authAttempts++
					if authAttempts >= maxOAuthRetries {
						t.trace.With(log.Fields{
							"operation":     operation.ID,
							"method":        operation.Method,
							"pathPattern":   operation.PathPattern,
							"waitDuration":  rerr.waitDuration,
							"auth attempts": authAttempts,
						}).Warn("ontap-rest auth failure and using oauth - maximum retries reached")
						return nil, errors.NewTimeoutErr("Internal server error")
					}

					t.trace.With(log.Fields{
						"operation":     operation.ID,
						"method":        operation.Method,
						"pathPattern":   operation.PathPattern,
						"waitDuration":  rerr.waitDuration,
						"auth attempts": authAttempts,
					}).Warn("ontap-rest auth failure and using oauth")

					time.Sleep(rerr.waitDuration)
					continue
				}

				retries++
				t.trace.With(log.Fields{
					"operation":    operation.ID,
					"method":       operation.Method,
					"pathPattern":  operation.PathPattern,
					"waitDuration": rerr.waitDuration,
					"attempts":     retries,
				}).Warn("ontap-rest request retry")

				time.Sleep(rerr.waitDuration)
				continue
			}

			return nil, ConvertFromRESTError(t.trace, err)
		}

		return result, nil
	}

	t.trace.With(log.Fields{
		"operation":   operation.ID,
		"method":      operation.Method,
		"pathPattern": operation.PathPattern,
		"err":         err.Error(),
	}).Warn("ontap-rest request retry exhausted")
	return nil, errors.NewTimeoutErr("Retries exhausted when attempting to reach the storage server")
}

type timeoutErr interface {
	Timeout() bool
	Error() string
}

type temporaryErr interface {
	Temporary() bool
	Error() string
}

var (
	defaultWait          = time.Duration(env.GetUint("ONTAP_REST_RETRY_WAIT_SECONDS", 3)) * time.Second
	defaultWaitLongRetry = time.Duration(env.GetUint("ONTAP_REST_RETRY_LONG_WAIT_MINUTES", 1)) * time.Minute
)

type retriableError struct {
	waitDuration time.Duration
	isAuthError  bool
}

// isRetriableError if err is a net.Error (transport layer error) or if the (http code/error code) from ONTAP is retryable then return true
func isRetriableError(err error) (retriableError, bool) {
	switch nerr := err.(type) {
	case *url.Error:
		// MD: we can never recover from this error by retrying
		if _, ok := nerr.Err.(*tls.CertificateVerificationError); ok {
			return retriableError{waitDuration: defaultWait}, false
		}

		// MD: this means that the remote OS acknowledged our request but no app is listening on the socket
		// This usually happens when the management LIF is migrating but it could also mean that
		// the proxy server in front of Ontap (if one exists) isn't ready to accept the connection
		if strings.Contains(nerr.Error(), "connection refused") {
			return retriableError{waitDuration: defaultWaitLongRetry}, true
		}

		return retriableError{waitDuration: defaultWait}, true
	case net.Error, timeoutErr, temporaryErr:
		return retriableError{waitDuration: defaultWait}, true
	case ontapRESTError:
		if payload := nerr.GetPayload(); payload != nil && payload.Error != nil && payload.Error.Code != nil {
			switch *payload.Error.Code {
			case "6691623":
				return retriableError{waitDuration: defaultWaitLongRetry, isAuthError: true}, ontapRestOAuthEnabled
			case "7", "8", "262160", "6619715", "1967171", "1967177", "13434889", "65536968", "65537564":
				return retriableError{waitDuration: defaultWait}, true
			case "13434894", "393271", "524424", "13", "2621556", "6620237":
				// MD: For these errors, add extra sleep time since they are almost always recoverable but need a bit more time before we can retry
				// MD: 13434894 Maximum SVM operations error
				// MD: The errors below mean that a node is in the process of crashing and storage failover is in process
				//  393271 - Node on ring "Management" is offline error
				//  524424 - vol offline: Error offlining volume. Error while deleting junction
				//  13 - unable to save data. Error while deleting cifs share
				//  2621556 - Volume Location Database is offline
				//  6620237 - SnapMirror relationship database write failed (VLDB node offline - transient)
				return retriableError{waitDuration: defaultWaitLongRetry}, true
			case "458753":
				// This code seems to have several different meanings.
				// Found on production as  "error":{"message":"[Job 900592] Job failed: \nA required service (secd) is not yet available; try again later\nWait a few minutes, and then try the Vserver delete operation again. If the error persists, contact technical support for assistance.","code":"458753"}
				// Found in documentation as  458753      | Destination and gateway must belong to the same address family.
				// So lets be sure we're only retrying on the error we expect.
				// The full error message suggests to wait a few minutes before retrying, so we use the long wait duration.
				if payload.Error.Message != nil && strings.Contains(*payload.Error.Message, "A required service (secd) is not yet available; try again later") {
					return retriableError{waitDuration: defaultWaitLongRetry}, true
				}
			}
		}
	}

	errValue := reflect.ValueOf(err)
	codeValue := errValue.Elem().FieldByName("_statusCode")
	if codeValue.IsValid() {
		code := codeValue.Int()
		switch code {
		case
			http.StatusRequestTimeout,
			http.StatusTooManyRequests,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return retriableError{waitDuration: defaultWait}, true
		}
	}

	return retriableError{}, false
}
