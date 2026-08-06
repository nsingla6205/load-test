// Provider selection helpers owned by VCP. The VLM datamodel (config.go)
// resolves provider-specific wrapper types from the PROVIDER environment
// variable; these helpers are the VCP-side entrypoint for setting it.
package vlm

import "os"

// ProviderEnvKey is the environment variable the VLM datamodel reads to select
// the concrete provider types for its wrapper fields (provider_config,
// provider_disk_config, provider_network_config, provider_dev_flags).
const ProviderEnvKey = "PROVIDER"

// SetActiveProvider publishes the running cloud provider to the VLM datamodel.
// Call it once at process startup, before any VLM payload is encoded or
// decoded. An unsupported provider is not rejected here; wrapper decoding
// surfaces it as "provider %q is not supported".
func SetActiveProvider(provider string) {
	// Setenv only fails on an invalid key, which is a compile-time constant here.
	_ = os.Setenv(ProviderEnvKey, provider)
}

// ResetActiveProvider clears the active provider so a different one can be
// selected. Test-only; production code calls SetActiveProvider exactly once.
func ResetActiveProvider() {
	_ = os.Unsetenv(ProviderEnvKey)
}
