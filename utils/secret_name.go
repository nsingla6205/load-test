package utils

import "strings"

// BareSecretName reduces a Secret Manager resource name to the bare secret id
// that vsa.GetProviderByNode expects, leaving an already-bare name untouched:
//
//	projects/{project}/secrets/{secret}              -> {secret}
//	projects/{project}/secrets/{secret}/versions/{n} -> {secret}
//	{secret}                                         -> {secret}
//
// vsa.GetProviderByNode formats projects/{SECRET_MANAGER_PROJECT_ID}/secrets/{id}
// from the node's identifiers. VCM receives its credentials as fully qualified
// resource names, so handing one through unchanged yields
// projects/{p}/secrets/projects/{q}/secrets/... and a 404. Reducing to the bare
// name keeps that formatting valid.
func BareSecretName(name string) string {
	trimmed := strings.TrimSpace(name)
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	for i, part := range parts {
		if part == "secrets" && i+1 < len(parts) && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return trimmed
}
