package config

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

const (
	defaultHost              = ""
	defaultPort              = "8092"
	defaultReadTimeout       = 30 * time.Second
	defaultWriteTimeout      = 60 * time.Second
	defaultIdleTimeout       = 120 * time.Second
	defaultReadHeaderTimeout = 2 * time.Second
)

// ServerConfig holds the environment-backed configuration needed to start VCM.
type ServerConfig struct {
	Host              string
	Port              string
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WorkflowUIEnabled bool
}

// LoadServerConfig loads only the configuration required by the VCM HTTP server.
func LoadServerConfig() *ServerConfig {
	return &ServerConfig{
		Host:              env.GetString("VCM_PROXY_HOST", defaultHost),
		Port:              env.GetString("VCM_PROXY_PORT", defaultPort),
		ReadTimeout:       env.GetDuration("READ_TIMEOUT", defaultReadTimeout),
		WriteTimeout:      env.GetDuration("WRITE_TIMEOUT", defaultWriteTimeout),
		IdleTimeout:       env.GetDuration("IDLE_TIMEOUT", defaultIdleTimeout),
		ReadHeaderTimeout: env.GetDuration("READ_HEADER_TIMEOUT", defaultReadHeaderTimeout),
		WorkflowUIEnabled: env.GetBool("VCM_WORKFLOW_UI_ENABLED", true),
	}
}
