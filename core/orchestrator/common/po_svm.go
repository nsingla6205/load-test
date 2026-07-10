package common

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// POSVMPostfixLen is the length (in hex characters) of the unique per-SVM
// suffix appended after the account prefix, matching SDE's 8-char postfix.
const POSVMPostfixLen = 8

// NormalizePOAccountUUID strips dashes from an account UUID for ONTAP resource
// naming (SVM prefix, ipspace, broadcast domain).
func NormalizePOAccountUUID(accountUUID string) string {
	return strings.ReplaceAll(accountUUID, "-", "")
}

// GetSVMPrefix returns the SVM name prefix for an account, mirroring SDE's
// storage.GetSVMPrefix: "svm_<accountUUID with dashes removed>_".
func GetSVMPrefix(accountUUID string) string {
	return fmt.Sprintf("svm_%s_", NormalizePOAccountUUID(accountUUID))
}

// DeterministicPOSVMPostfix returns a stable 8-char hex suffix derived from
// poolUUID. The same pool always gets the same postfix so Temporal activity
// retries do not allocate a new SVM name after partial provisioning.
func DeterministicPOSVMPostfix(poolUUID string) string {
	sum := sha256.Sum256([]byte(poolUUID))
	return hex.EncodeToString(sum[:4])[:POSVMPostfixLen]
}

// GenerateSVMPostfix returns a random lowercase hex string of length n, used as
// the unique per-SVM suffix (SDE uses 8 hex characters).
func GenerateSVMPostfix(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("postfix length must be positive")
	}
	buf := make([]byte, (n+1)/2)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf)[:n], nil
}

// GeneratePODataLifPostfixes returns count unique 8-char hex postfixes for PO
// data LIF names (<svmName>_<postfix>). Intended for use inside a Temporal
// workflow.SideEffect so randomness does not break replay determinism.
func GeneratePODataLifPostfixes(count int) ([]string, error) {
	if count <= 0 {
		return nil, fmt.Errorf("lif postfix count must be positive, got %d", count)
	}
	used := make(map[string]bool, count)
	result := make([]string, 0, count)
	for attempts := 0; len(result) < count && attempts < count*10; attempts++ {
		postfix, err := GenerateSVMPostfix(POSVMPostfixLen)
		if err != nil {
			return nil, err
		}
		if !used[postfix] {
			used[postfix] = true
			result = append(result, postfix)
		}
	}
	if len(result) < count {
		return nil, fmt.Errorf("unable to generate %d unique lif postfixes", count)
	}
	return result, nil
}
