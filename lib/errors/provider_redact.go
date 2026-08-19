package errors

import "regexp"

// Patterns that commonly appear in GCP / cloud-provider error bodies and can
// identify tenant-owned resources. Matching substrings are replaced before a
// provider message is written to logs or other sinks.
var providerResourcePathPatterns = []*regexp.Regexp{
	// More specific path shapes first so shorter patterns do not leave fragments.
	regexp.MustCompile(`global/networks/[A-Za-z0-9_-]+`),
	regexp.MustCompile(`projects/[0-9]{6,12}`),
	regexp.MustCompile(`projects/[a-z][a-z0-9-]{4,61}[a-z0-9]`),
	regexp.MustCompile(`subnetworks/[A-Za-z0-9_-]+`),
	regexp.MustCompile(`networks/[A-Za-z0-9_-]+`),
}

const redactedProviderResource = "[REDACTED]"

// RedactProviderError strips known cloud-provider resource path patterns from
// a message so tenant identifiers are not written to logs or returned in
// intermediate error strings. Empty input is returned unchanged.
func RedactProviderError(msg string) string {
	if msg == "" {
		return msg
	}
	redacted := msg
	for _, pattern := range providerResourcePathPatterns {
		redacted = pattern.ReplaceAllString(redacted, redactedProviderResource)
	}
	return redacted
}
