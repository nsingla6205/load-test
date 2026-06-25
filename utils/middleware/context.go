package middleware

type ContextKey int
type ContextString string

const (
	CorrelationContextKey      ContextKey    = iota
	CallerInfoContextKey       ContextKey    = iota
	CorrelationIDName          ContextString = "x-correlation-id"
	ContextSLoggerKey          ContextString = "ctxSLogger"
	HeaderContextKey           ContextString = "headerContextKey"
	TemporalSLoggerKey         ContextString = "fields"
	RequestCorrelationID       ContextString = "requestCorrelationID"
	RequestOpcWorkRequestID    ContextString = "requestOpcWorkRequestID"
	RequestID                  ContextString = "requestID"
	AuthorizationToken         ContextString = "Authorization"
	ContexMainThreadID         ContextString = "mainThread"
	OPCRequestIDHeaderName     ContextString = "opc-request-id"
	OPCWorkRequestIDHeaderName ContextString = "opc-work-request-id"
)
