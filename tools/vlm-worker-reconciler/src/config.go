package main

import (
	"os"
	"strconv"
)

type config struct {
	namespace              string
	dryRun                 bool
	dbHost                 string
	dbPort                 string
	dbName                 string
	dbUser                 string
	dbPassword             string
	dbSSLMode              string
	gcpProjectID           string
	cloudSqlIAMAuthEnabled bool
	// activeDeadlineSeconds mirrors the Kubernetes Job activeDeadlineSeconds so
	// the proxy-readiness retry budget can be derived at runtime rather than
	// hardcoded.
	activeDeadlineSeconds int
}

func configFromEnv() config {
	return config{
		namespace:              os.Getenv("NAMESPACE"),
		dryRun:                 os.Getenv("DRY_RUN") == "true",
		dbHost:                 os.Getenv("DB_HOST"),
		dbPort:                 envDefault("DB_PORT", "5432"),
		dbName:                 envDefault("DB_NAME", "vcp"),
		dbUser:                 os.Getenv("DB_USER"),
		dbPassword:             os.Getenv("DB_PASSWORD"),
		dbSSLMode:              envDefault("DB_SSL_MODE", "disable"),
		gcpProjectID:           os.Getenv("OTEL_GOOGLE_PROJECT_ID"),
		cloudSqlIAMAuthEnabled: os.Getenv("CLOUD_SQL_IAM_AUTH_ENABLED") == "true",
		activeDeadlineSeconds:  envDefaultInt("ACTIVE_DEADLINE_SECONDS", 300),
	}
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDefaultInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
