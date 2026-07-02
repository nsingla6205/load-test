package utils

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// VLMWorkerFeatureFlagEnv is the environment variable that holds the VLM worker feature-flag map.
//
// It replaces account-allowlist based VLM worker selection with feature based selection. The value is
// a JSON object whose key is a feature name and whose value is a (JSON-encoded) VLMWorkerFeatureConfig,
// for example:
//
//	{"trueZonal":"{\"vlmWorkerVersion\":\"9.18.1\",\"VSA_IMAGE_NAME\":\"\",\"VSA_MEDIATOR_IMAGE_NAME\":\"\"}"}
//
// The per-feature value may also be supplied as a plain JSON object (not string-encoded); both forms
// are accepted. Feature names are supplied by the orchestrator; this package only resolves configured
// values for a given feature key.
const VLMWorkerFeatureFlagEnv = "VLM_WORKER_FEATURE_FLAG"

// VLMWorkerFeatureConfig describes which VLM worker (task queue version) and VSA images a feature uses.
type VLMWorkerFeatureConfig struct {
	// VlmWorkerVersion selects the VLM worker task queue suffix (vsa-lifecycle-manager-<vlmWorkerVersion>).
	VlmWorkerVersion string `json:"vlmWorkerVersion"`
	// VSAImageName overrides the default VSA build image when non-empty.
	VSAImageName string `json:"VSA_IMAGE_NAME"`
	// VSAMediatorImageName overrides the default mediator build image when non-empty.
	VSAMediatorImageName string `json:"VSA_MEDIATOR_IMAGE_NAME"`
}

// getVLMWorkerFeatureFlags returns the parsed VLM_WORKER_FEATURE_FLAG map, keyed by feature name.
// It reads from env at call time so local/dev env loading order does not freeze stale values.
func getVLMWorkerFeatureFlags() map[string]VLMWorkerFeatureConfig {
	return ParseVLMWorkerFeatureFlags(env.GetString(VLMWorkerFeatureFlagEnv, ""))
}

// ParseVLMWorkerFeatureFlags parses the raw VLM_WORKER_FEATURE_FLAG value into a feature->config map.
// Invalid entries (bad JSON or empty vlmWorkerVersion) are skipped so a single malformed feature does
// not break the whole map.
func ParseVLMWorkerFeatureFlags(raw string) map[string]VLMWorkerFeatureConfig {
	result := make(map[string]VLMWorkerFeatureConfig)
	if strings.TrimSpace(raw) == "" {
		return result
	}

	outer := make(map[string]json.RawMessage)
	if err := json.Unmarshal([]byte(raw), &outer); err != nil {
		return result
	}

	for feature, rawCfg := range outer {
		cfg, ok := parseVLMWorkerFeatureConfig(rawCfg)
		if !ok {
			continue
		}
		result[feature] = cfg
	}
	return result
}

// parseVLMWorkerFeatureConfig accepts either a JSON object or a JSON-encoded string and returns the
// decoded config. ok is false when the value cannot be decoded or has no vlmWorkerVersion.
func parseVLMWorkerFeatureConfig(raw json.RawMessage) (VLMWorkerFeatureConfig, bool) {
	var cfg VLMWorkerFeatureConfig

	// Form 1: per-feature value is a JSON object.
	if err := json.Unmarshal(raw, &cfg); err == nil {
		cfg.VlmWorkerVersion = strings.TrimSpace(cfg.VlmWorkerVersion)
		if cfg.VlmWorkerVersion != "" {
			return cfg, true
		}
	}

	// Form 2: per-feature value is a JSON-encoded string containing the object.
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil && strings.TrimSpace(encoded) != "" {
		cfg = VLMWorkerFeatureConfig{}
		if err := json.Unmarshal([]byte(encoded), &cfg); err == nil {
			cfg.VlmWorkerVersion = strings.TrimSpace(cfg.VlmWorkerVersion)
			if cfg.VlmWorkerVersion != "" {
				return cfg, true
			}
		}
	}

	return VLMWorkerFeatureConfig{}, false
}

// GetVLMWorkerFeatureConfig returns the configured VLM worker queue version and image overrides for
// the given feature name from VLM_WORKER_FEATURE_FLAG.
func GetVLMWorkerFeatureConfig(feature string) (VLMWorkerFeatureConfig, bool) {
	if strings.TrimSpace(feature) == "" {
		return VLMWorkerFeatureConfig{}, false
	}
	cfg, ok := getVLMWorkerFeatureFlags()[feature]
	return cfg, ok
}

// SetVLMWorkerFeatureFlagsForTesting sets the VLM_WORKER_FEATURE_FLAG env var.
// This should only be used in tests.
func SetVLMWorkerFeatureFlagsForTesting(raw string) {
	if err := os.Setenv(VLMWorkerFeatureFlagEnv, raw); err != nil {
		return
	}
}
