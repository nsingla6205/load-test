package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSetupMetrics_ReturnsNilWhenProjectIDEmpty(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := setupMetrics(context.Background(), "", log)
	assert.Nil(t, m, "setupMetrics must return nil when gcpProjectID is empty")
}

func TestEmitAndFlush_NoopOnNilReceiver(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var m *reconcileMetrics
	assert.NotPanics(t, func() {
		m.emitAndFlush(context.Background(), outcomeSuccess, time.Second, log)
	})
}
