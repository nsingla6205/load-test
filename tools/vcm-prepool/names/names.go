package names

import (
	"crypto/sha256"
	"fmt"
	"regexp"

	"github.com/google/uuid"
)

const (
	VSADataSubnetPrefix = "vsa-"
	serviceAccountIDMax = 30
	// MaxDeploymentNameLength keeps derived GCS bucket names and GCP SA IDs within API limits
	// (bucket: name + "-vcm-at-bucket" ≤ 63; SA ID: "vcm-" + name ≤ 30).
	MaxDeploymentNameLength = 20
)

// deploymentNamePattern matches 1–20 lowercase letters, digits, or hyphens (no leading/trailing hyphen).
var deploymentNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,18}[a-z0-9])?$`)

// ValidateDeploymentName reports whether deploymentName is a valid 1–20 character identifier.
func ValidateDeploymentName(deploymentName string) error {
	if !deploymentNamePattern.MatchString(deploymentName) {
		return fmt.Errorf("deploymentId must contain 1-%d lowercase letters, digits, or hyphens", MaxDeploymentNameLength)
	}
	return nil
}

// GenerateDataSubnetPrefix returns the tenant-scoped name prefix, including the
// trailing hyphen so tenant "123" does not match "vsa-1234-...".
func GenerateDataSubnetPrefix(tenancyProject string) string {
	return fmt.Sprintf("%s%s-", VSADataSubnetPrefix, tenancyProject)
}

// GenerateDataSubnetName returns a collision-resistant subnet name.
func GenerateDataSubnetName(tenancyProject string) string {
	return fmt.Sprintf("%s%s", GenerateDataSubnetPrefix(tenancyProject), uuid.NewString()[:8])
}

// GenerateSVMName produces a deterministic SVM name (always -svm-01, single SVM per deployment).
func GenerateSVMName(deploymentName string) string {
	return fmt.Sprintf("%s-svm-%02d", deploymentName, 1)
}

// GenerateBucketName produces a stable bucket name from deployment name.
func GenerateBucketName(deploymentName string) string {
	return fmt.Sprintf("%s-vcm-at-bucket", deploymentName)
}

// GenerateServiceAccountInfo produces a service account info from deployment name and tenant project.
func GenerateServiceAccountInfo(deploymentName, tenantProject string) (string, string, error) {
	if err := ValidateDeploymentName(deploymentName); err != nil {
		return "", "", fmt.Errorf("invalid deployment name %q: %w", deploymentName, err)
	}

	saID := "vcm-" + deploymentName
	if len(saID) > serviceAccountIDMax {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(deploymentName)))[:8]
		prefixLength := serviceAccountIDMax - len(hash) - 1
		saID = fmt.Sprintf("%s-%s", saID[:prefixLength], hash)
	}
	saEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", saID, tenantProject)
	return saID, saEmail, nil
}
