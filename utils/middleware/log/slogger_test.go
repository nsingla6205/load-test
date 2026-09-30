package log

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestNewSlogHandler_HonorsLevel(t *testing.T) {
	handler := NewSlogHandler(slog.LevelInfo)
	require.NotNil(t, handler)
	assert.True(t, handler.Enabled(context.Background(), slog.LevelInfo))
	assert.False(t, handler.Enabled(context.Background(), slog.LevelDebug))
}

// TestGetSlogger tests the getSlogger function
func TestGetSlogger(t *testing.T) {
	t.Run("Initialize slogger with valid config", func(t *testing.T) {
		config := Config{
			LogLevel:  "info",
			AddSource: true,
		}
		logger, _ := getSlogger(config)
		assert.IsType(t, &Slogger{}, logger)
	})

	t.Run("Initialize slogger with invalid log level", func(t *testing.T) {
		config := Config{
			LogLevel:  "invalid",
			AddSource: true,
		}
		logger, _ := getSlogger(config)
		assert.IsType(t, &Slogger{}, logger)
	})

	t.Run("Initialize slogger with empty log level", func(t *testing.T) {
		config := Config{
			LogLevel:  "",
			AddSource: true,
		}
		logger, _ := getSlogger(config)
		assert.IsType(t, &Slogger{}, logger)
	})

	t.Run("Initialize slogger with AddSource as false", func(t *testing.T) {
		config := Config{
			LogLevel:  "info",
			AddSource: false,
		}
		logger, _ := getSlogger(config)
		assert.IsType(t, &Slogger{}, logger)
	})
}

// TestSloggerLogMethods tests the logging methods of Slogger
func TestSloggerLogMethods(t *testing.T) {
	logger := &Slogger{}
	t.Run("Error", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Error("error message")
		assert.Contains(t, buf.String(), "error message")
	})

	t.Run("Errorf", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Errorf("formatted %s", "errorf message")
		assert.Contains(t, buf.String(), "formatted errorf message")
	})

	t.Run("Warn", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Warn("warn message")
		assert.Contains(t, buf.String(), "warn message")
	})

	t.Run("Warnf", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Warnf("formatted %s", "warnf message")
		assert.Contains(t, buf.String(), "formatted warnf message")
	})

	t.Run("Info", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Info("info message")
		assert.Contains(t, buf.String(), "info message")
	})

	t.Run("Infof", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, nil))
		logger.Infof("formatted %s", "infof message")
		assert.Contains(t, buf.String(), "formatted infof message")
	})

	t.Run("Debug", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
		logger.Debug("debug message")
		assert.Contains(t, buf.String(), "debug message")
	})

	t.Run("Debugf", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
		logger.Debugf("formatted %s", "debug message")
		assert.Contains(t, buf.String(), "formatted debug message")
	})
}

// TestSloggerContextLogMethods tests the context logging methods of Slogger
func TestSloggerContextLogMethods(t *testing.T) {
	logger := &Slogger{}
	ctx := context.Background()

	t.Run("InfoContext", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, nil))
		logger.InfoContext(ctx, "info message")
		assert.Contains(t, buf.String(), "info message")
	})

	t.Run("WarnContext", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, nil))
		logger.WarnContext(ctx, "warn message")
		assert.Contains(t, buf.String(), "warn message")
	})

	t.Run("ErrorContext", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, nil))
		logger.ErrorContext(ctx, "error message")
		assert.Contains(t, buf.String(), "error message")
	})

	t.Run("DebugContext", func(t *testing.T) {
		var buf bytes.Buffer
		logger.slogger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
		logger.DebugContext(ctx, "debug message")
		assert.Contains(t, buf.String(), "debug message")
	})
}

// TestSpanContextLogHandler tests the spanContextLogHandler methods
func TestSpanContextLogHandler(t *testing.T) {
	t.Run("handle with valid context", func(t *testing.T) {
		ctx := context.Background()
		record := slog.Record{}
		handler := handlerWithSpanContext(slog.NewJSONHandler(defaultOutputStream, nil))

		err := handler.Handle(ctx, record)
		assert.NoError(t, err)
	})

	t.Run("handle with nil context", func(t *testing.T) {
		record := slog.Record{}
		handler := handlerWithSpanContext(slog.NewJSONHandler(defaultOutputStream, nil))

		err := handler.Handle(context.TODO(), record)
		assert.NoError(t, err)
	})

	t.Run("handle with valid context with TraceID and SpanID", func(t *testing.T) {
		ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1, 2, 3},
			SpanID:     trace.SpanID{4, 5, 6},
			TraceFlags: trace.FlagsSampled,
		}))
		record := slog.Record{}
		handler := handlerWithSpanContext(slog.NewJSONHandler(defaultOutputStream, nil))

		err := handler.Handle(ctx, record)
		assert.NoError(t, err)
	})
	t.Run("WithGroup creates a new handler with the specified group", func(t *testing.T) {
		handler := handlerWithSpanContext(slog.NewJSONHandler(defaultOutputStream, nil))
		groupedHandler := handler.WithGroup("testGroup")

		assert.NotNil(t, groupedHandler)
		assert.IsType(t, &spanContextLogHandler{}, groupedHandler)
	})

	t.Run("Enabled checks if the log level is enabled", func(t *testing.T) {
		handler := handlerWithSpanContext(slog.NewJSONHandler(defaultOutputStream, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
		ctx := context.Background()

		assert.True(t, handler.Enabled(ctx, slog.LevelDebug))
		assert.True(t, handler.Enabled(ctx, slog.LevelInfo))
		assert.True(t, handler.Enabled(ctx, slog.LevelWarn))
		assert.True(t, handler.Enabled(ctx, slog.LevelError))
	})
}

// TestConvertLogLevel tests the convertLogLevel function
func TestConvertLogLevel(t *testing.T) {
	t.Run("convert valid log level", func(t *testing.T) {
		assert.Equal(t, slog.LevelInfo, convertLogLevel("info"))
		assert.Equal(t, slog.LevelDebug, convertLogLevel("debug"))
		assert.Equal(t, slog.LevelWarn, convertLogLevel("warn"))
		assert.Equal(t, slog.LevelError, convertLogLevel("error"))
	})

	t.Run("convert invalid log level", func(t *testing.T) {
		assert.Equal(t, slog.LevelInfo, convertLogLevel("invalid"))
	})

	t.Run("Convert empty log level", func(t *testing.T) {
		assert.Equal(t, slog.LevelInfo, convertLogLevel(""))
	})

	t.Run("Convert uppercase log level", func(t *testing.T) {
		assert.Equal(t, slog.LevelDebug, convertLogLevel("DEBUG"))
		assert.Equal(t, slog.LevelError, convertLogLevel("ERROR"))
	})

	t.Run("Convert mixed-case log level", func(t *testing.T) {
		assert.Equal(t, slog.LevelWarn, convertLogLevel("WaRn"))
	})
}

// TestReplacer tests the replacer function
func TestReplacer(t *testing.T) {
	t.Run("Replace log level key and value", func(t *testing.T) {
		attr := slog.Attr{
			Key:   slog.LevelKey,
			Value: slog.AnyValue(slog.LevelWarn),
		}
		expected := slog.Attr{
			Key:   "severity",
			Value: slog.StringValue("WARNING"),
		}
		result := replacer(nil, attr)
		assert.Equal(t, expected, result)
	})

	t.Run("Replace time key", func(t *testing.T) {
		attr := slog.Attr{
			Key:   slog.TimeKey,
			Value: slog.StringValue("2023-01-01T00:00:00Z"),
		}
		expected := slog.Attr{
			Key:   "timestamp",
			Value: slog.StringValue("2023-01-01T00:00:00Z"),
		}
		result := replacer(nil, attr)
		assert.Equal(t, expected, result)
	})

	t.Run("Replace message key", func(t *testing.T) {
		attr := slog.Attr{
			Key:   slog.MessageKey,
			Value: slog.StringValue("test message"),
		}
		expected := slog.Attr{
			Key:   "message",
			Value: slog.StringValue("test message"),
		}
		result := replacer(nil, attr)
		assert.Equal(t, expected, result)
	})

	t.Run("Do not replace unrelated key", func(t *testing.T) {
		attr := slog.Attr{
			Key:   "unrelatedKey",
			Value: slog.StringValue("value"),
		}
		expected := slog.Attr{
			Key:   "unrelatedKey",
			Value: slog.StringValue("value"),
		}
		result := replacer(nil, attr)
		assert.Equal(t, expected, result)
	})
}

// TestSloggerWith tests the With method of Slogger
func TestSloggerWith(t *testing.T) {
	t.Run("Add fields to logger", func(t *testing.T) {
		var buf bytes.Buffer
		logger := &Slogger{
			slogger: slog.New(slog.NewJSONHandler(&buf, nil)),
		}

		fields := Fields{"key1": "value1", "key2": 123, "key3": true}
		newLogger := logger.With(fields)

		assert.IsType(t, &Slogger{}, newLogger)

		newLogger.(*Slogger).Info("test message")
		logOutput := buf.String()

		assert.Contains(t, logOutput, `"msg":"test message"`)
		assert.Contains(t, logOutput, `"key1":"value1"`)
		assert.Contains(t, logOutput, `"key2":123`)
		assert.Contains(t, logOutput, `"key3":true`)
	})

	t.Run("Add empty fields to logger", func(t *testing.T) {
		var buf bytes.Buffer
		logger := &Slogger{
			slogger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{})),
		}

		fields := Fields{}
		newLogger := logger.With(fields)

		assert.IsType(t, &Slogger{}, newLogger)

		newLogger.(*Slogger).Info("test message")
		logOutput := buf.String()

		assert.Contains(t, logOutput, `"msg":"test message"`)
		assert.NotContains(t, logOutput, `"key1"`)
	})

	t.Run("Add nil fields to logger", func(t *testing.T) {
		var buf bytes.Buffer
		logger := &Slogger{
			slogger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{})),
		}

		var nilFields Fields = nil
		newLogger := logger.With(nilFields)

		assert.IsType(t, &Slogger{}, newLogger)

		newLogger.(*Slogger).Info("test message")
		logOutput := buf.String()

		assert.Contains(t, logOutput, `"msg":"test message"`)
		assert.NotContains(t, logOutput, `"key1"`)
	})
}

// TestSloggerWithFields tests the WithFields method of Slogger
func TestSloggerWithFields(t *testing.T) {
	t.Run("Add fields to logger with field name", func(t *testing.T) {
		var buf bytes.Buffer
		logger := &Slogger{
			slogger: slog.New(slog.NewJSONHandler(&buf, nil)),
		}

		fields := Fields{"key1": "value1", "key2": 123, "key3": true}
		newLogger := logger.WithFields("FieldGroup", fields)

		assert.IsType(t, &Slogger{}, newLogger)

		newLogger.(*Slogger).Info("test message")
		logOutput := buf.String()

		assert.Contains(t, logOutput, `"msg":"test message"`)
		assert.Contains(t, logOutput, `"FieldGroup":{"key1":"value1","key2":123,"key3":true}`)
	})

	t.Run("Add empty fields to logger with field name", func(t *testing.T) {
		var buf bytes.Buffer
		logger := &Slogger{
			slogger: slog.New(slog.NewJSONHandler(&buf, nil)),
		}

		fields := Fields{}
		newLogger := logger.WithFields("FieldGroup", fields)

		assert.IsType(t, &Slogger{}, newLogger)

		newLogger.(*Slogger).Info("test message")
		logOutput := buf.String()

		assert.Contains(t, logOutput, `"msg":"test message"`)
		assert.Contains(t, logOutput, `"FieldGroup":{}`)
	})

	t.Run("Add nil fields to logger with field name", func(t *testing.T) {
		var buf bytes.Buffer
		logger := &Slogger{
			slogger: slog.New(slog.NewJSONHandler(&buf, nil)),
		}

		var nilFields Fields = nil
		newLogger := logger.WithFields("FieldGroup", nilFields)

		assert.IsType(t, &Slogger{}, newLogger)

		newLogger.(*Slogger).Info("test message")
		logOutput := buf.String()

		assert.Contains(t, logOutput, `"msg":"test message"`)
		assert.Contains(t, logOutput, `"FieldGroup":null`)
	})
}
