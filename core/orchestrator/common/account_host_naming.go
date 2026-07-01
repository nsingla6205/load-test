package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

const (
	accountHostNameSuffixVar = "{name_suffix}"
	accountHostBaseHashLen   = 8
)

// DeriveAccountHostBaseHash returns the first 8 hex chars of sha256(accountHostUUID).
func DeriveAccountHostBaseHash(accountHostUUID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(accountHostUUID)))
	return hex.EncodeToString(sum[:])[:accountHostBaseHashLen]
}

// BuildAccountHostUsername renders the ONTAP service account name for an account-host row.
func BuildAccountHostUsername(baseHash string) string {
	return renderAccountHostName(env.AccountHostUserNameTemplate, baseHash)
}

// BuildAccountHostRoleName renders the ONTAP role name for an account-host row.
func BuildAccountHostRoleName(baseHash string) string {
	return renderAccountHostName(env.AccountHostRoleNameTemplate, baseHash)
}

func renderAccountHostName(template, baseHash string) string {
	return strings.ReplaceAll(
		template,
		accountHostNameSuffixVar,
		strings.ToLower(strings.TrimSpace(baseHash)),
	)
}

// BuildAccountHostSvmPattern returns the runtime ONTAP SVM wildcard for REST/RBAC scope.
// GCS RBAC CLI files use placeholder AccountHostRBACCLIQueryTemplate (gcnv-*-svm-*);
// ApplyAccountHostRBACSubstitutions replaces that token with this svm_{accountUUID}_* form.
// Hyphens are stripped from accountUUID so the pattern matches ONTAP SVM naming (e.g. svm_<32hex>_<8charSuffix>).
func BuildAccountHostSvmPattern(accountUUID string) string {
	normalizedUUID := strings.ReplaceAll(strings.TrimSpace(accountUUID), "-", "")
	return fmt.Sprintf("svm_%s_*", normalizedUUID)
}
