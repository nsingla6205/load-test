package log

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
)

type Fields map[string]interface{}

type Logger interface {
	Errorf(format string, args ...any)
	Error(format string, args ...any)
	Warnf(format string, args ...any)
	Warn(format string, args ...any)
	Infof(format string, args ...any)
	Info(format string, args ...any)
	Debugf(format string, args ...any)
	Debug(format string, args ...any)

	// InfoContext logs the message with context for traceability.
	InfoContext(ctx context.Context, msg string, args ...any)
	WarnContext(ctx context.Context, msg string, args ...any)
	ErrorContext(ctx context.Context, msg string, args ...any)
	DebugContext(ctx context.Context, msg string, args ...any)

	// WithFields returns a new logger with the request fields grouped under a specific field name.
	WithFields(fieldName string, fields Fields) Logger
	// With returns a new logger with the request fields added directly as key-value pairs.
	With(fields Fields) Logger
}

var (
	defaultOutputStream = os.Stdout
	config              Config
	uuidNewString       = uuid.NewString
	globalLogger        Logger
)

type Config struct {
	LogLevel   string
	AddSource  bool
	LoggerType string
}

func init() {
	config = Config{
		AddSource:  env.AddSource,
		LogLevel:   env.LogLevel,
		LoggerType: env.LoggerType,
	}
	globalLogger = getLogger(config)
}

type LoggerType string

const (
	LoggerTypeSlog LoggerType = "slog"
)

// getLogger function with a switch case for logger types
func getLogger(config Config) Logger {
	var logger Logger
	var err error
	switch LoggerType(strings.ToLower(config.LoggerType)) {
	case LoggerTypeSlog:
		logger, err = getSlogger(config)
	// Add other logger types here if needed
	default:
		logger, err = getSlogger(config)
	}
	if err != nil {
		fallbackLogger, fallbackErr := getBasicLogger()
		if fallbackErr != nil {
			panic(fmt.Sprintf("failed to initialize logger: %v, fallback also failed: %v", err, fallbackErr))
		}
		return fallbackLogger
	}
	return logger
}

func getBasicLogger() (Logger, error) {
	// This prevents the application from crashing if logger setup fails
	basicConfig := Config{
		LogLevel:   "info", // Default to info level
		AddSource:  false,  // Disable source for fallback
		LoggerType: "slog", // Use slog as fallback
	}

	return getSlogger(basicConfig)
}

// LoggingMiddleware injects a logger into the request context
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Populating logger with fields like requestID, correlationID, traceURL, etc
		logger, logFields := NewRequestLogger(r)
		ctx := context.WithValue(r.Context(), middleware.ContextSLoggerKey, logger)
		// Populating the context with fields required for logger
		// creation. These values will be used in context propagation
		// and logger initialization later
		ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, logFields)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

// NewLogger returns the global logger
func NewLogger() Logger {
	return globalLogger
}

func NewRequestLogger(r *http.Request) (Logger, Fields) {
	return extractFieldsFromHttpRequest(r)
}

func extractFieldsFromHttpRequest(r *http.Request) (Logger, Fields) {
	correlationID := GetCorrelationID(r)
	requestID := GetRequestID(r)
	fields := Fields{
		"requestCorrelationID": correlationID,
		"requestID":            requestID,
		"traceMethod":          r.Method,
		"traceURL":             r.URL.String(),
	}
	if project := projectFromRequestPath(r.URL.Path); project != "" {
		fields["project"] = project
		fields["customerID"] = project
	}
	logger := globalLogger.WithFields("requestFields", fields)
	return logger, fields
}

func projectFromRequestPath(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+1 < len(segments); i++ {
		if segments[i] == "projects" && segments[i+1] != "" {
			return segments[i+1]
		}
	}
	return ""
}

// GetCorrelationID retrieves the correlation ID from the request header.
func GetCorrelationID(req *http.Request) string {
	return GetHeaderID(req, RequestCorrelationID)
}

// GetRequestID retrieves the request ID from the request header.
func GetRequestID(req *http.Request) string {
	return GetHeaderID(req, RequestID)
}

// GetHeaderID retrieves or generates a unique ID for the given header key.
func GetHeaderID(req *http.Request, headerKey string) string {
	id := req.Header.Get(headerKey)
	if id == "" {
		id = uuidNewString()
		req.Header.Set(headerKey, id)
	}
	return id
}
