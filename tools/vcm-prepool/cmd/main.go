package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/prepoolinfra"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

var (
	vcmPrePoolPort = env.GetString("VCM_PREP_PORT", "9092")
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	slog.Info("VCM Pre-Pool: Start")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	addr := fmt.Sprintf(":%s", vcmPrePoolPort)
	server := &http.Server{
		Addr:              addr,
		Handler:           api.NewHandler(ctx, prepoolinfra.NewProvisioner),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	slog.Info("Server started", "addr", addr)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return
		}
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		slog.Info("Shutting down server")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server shutdown failed", "error", err)
	}
}
