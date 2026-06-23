package common

import (
	"strings"

	"github.com/google/uuid"
)

// GenerateSvmResourceName builds a unique SVM name by combining the given prefix
// with a hyphen-free UUID (32 hex chars). The caller is responsible for choosing
// a prefix that keeps the total length within the target system's limit.
//
// ONTAP SVM naming rules (https://docs.netapp.com/us-en/ontap-cli/vserver-create.html):
//   - Maximum 47 characters (41 for sync-source type)
//   - Must start with a letter (a-z, A-Z) or underscore (_)
//   - Allowed characters: letters, digits (0-9), hyphens (-), underscores (_), periods (.)
//   - Cannot use reserved names: "all", "local", "localhost"
//   - Must be unique within the cluster
//
// VLM enforces a stricter 40-character limit. With the "oci-svm-" prefix (8 chars)
// plus 32 hex chars the result is exactly 40 characters.
func GenerateSvmResourceName(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")
}
