package common

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixtures for the account-scoped LIF-name contract. The dash-stripped form
// is what GeneratePOICLifNames should embed in every generated name (matching
// the data-path naming convention: ipspace_/bdomain_/svm_ all use
// NormalizePOAccountUUID). The hyphen count / length invariants below rely
// on the specific 36-char UUID shape (8-4-4-4-12) collapsing to 32 chars
// once the four dashes are removed.
const (
	testICLifAccountUUID         = "abcdef12-3456-7890-abcd-ef1234567890"
	testICLifAccountUUIDNoDashes = "abcdef1234567890abcdef1234567890"
)

// icLifHexTailRE pins the 6-char lowercase hex suffix contract.
var icLifHexTailRE = regexp.MustCompile(`-[0-9a-f]{6}$`)

// TestGeneratePOICLifNames pins the batch generator contract for the
// account-scoped naming scheme (VSCP-7663 follow-up):
//
//   - returns exactly count names (including count=1 and count=N)
//   - every name has the exact shape
//     "ic-lif-<accountUUID-without-dashes>-<6hex>"
//   - the dash-stripped accountUUID segment is present verbatim, and the
//     original UUID hyphens are gone (matching the data-path convention)
//   - total length is 7 + 32 + 1 + 6 = 46 chars (well under ONTAP's 63)
//   - exactly three hyphens in each name: two inside "ic-lif" and one
//     between the account segment and the 6-hex tail
//   - the tail matches `-[0-9a-f]{6}$`
//   - every name is unique within a single call
func TestGeneratePOICLifNames(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{"single name", 1},
		{"two nodes", 2},
		{"N=8 nodes", 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			names, err := GeneratePOICLifNames(testICLifAccountUUID, tc.count)
			require.NoError(t, err)
			require.Len(t, names, tc.count, "must return exactly count names")

			seen := make(map[string]bool, tc.count)
			for i, n := range names {
				assert.True(t, strings.HasPrefix(n, "ic-lif-"),
					"name[%d]=%q must have ic-lif- prefix", i, n)
				assert.True(t, strings.Contains(n, testICLifAccountUUIDNoDashes),
					"name[%d]=%q must contain the dash-stripped account UUID %q",
					i, n, testICLifAccountUUIDNoDashes)
				assert.Equal(t, 7+32+1+6, len(n),
					"name[%d]=%q must be exactly 46 chars", i, n)
				// Exactly 3 hyphens: "ic-lif-" contributes two,
				// and one separates the account segment from the
				// hex tail. Any additional hyphen means the
				// account UUID's dashes leaked through.
				assert.Equal(t, 3, strings.Count(n, "-"),
					"name[%d]=%q must have exactly 3 hyphens (account UUID dashes must be removed)",
					i, n)
				assert.Regexp(t, icLifHexTailRE, n,
					"name[%d]=%q must end with a 6-char lowercase hex tail", i, n)
				assert.False(t, seen[n],
					"name[%d]=%q duplicated within the batch", i, n)
				seen[n] = true
			}
		})
	}
}

// TestGeneratePOICLifNames_ZeroCountRejected pins the input-validation
// contract: count<=0 is a caller bug and must surface as an error, not
// as a silent empty slice.
func TestGeneratePOICLifNames_ZeroCountRejected(t *testing.T) {
	for _, count := range []int{0, -1, -100} {
		_, err := GeneratePOICLifNames(testICLifAccountUUID, count)
		assert.Error(t, err, "count=%d must be rejected", count)
	}
}

// TestGeneratePOICLifNames_EmptyAccountUUID_Rejected pins the second
// input-validation contract: an empty accountUUID would produce
// "ic-lif--<hex>" (an empty account segment collapsing two hyphens into
// each other), which breaks the account-scoping guarantee callers rely
// on. It must fail loudly instead.
func TestGeneratePOICLifNames_EmptyAccountUUID_Rejected(t *testing.T) {
	_, err := GeneratePOICLifNames("", 1)
	assert.Error(t, err, "empty accountUUID must be rejected")
}

func TestDefaultPOICLifConfig_Defaults(t *testing.T) {
	t.Setenv(envEnableICLifsForPOPools(), "true")
	t.Setenv(envPOICLifSubnetMaskSize(), "28")

	cfg := DefaultPOICLifConfig()
	assert.True(t, cfg.Enabled)
	assert.Equal(t, 28, cfg.SubnetMaskSize)
	assert.NotEmpty(t, cfg.BasePort)
}

func TestDefaultPOICLifConfig_InvalidMaskFallsBack(t *testing.T) {
	t.Setenv(envPOICLifSubnetMaskSize(), "24")
	cfg := DefaultPOICLifConfig()
	assert.Equal(t, PODefaultICLifSubnetMaskSize, cfg.SubnetMaskSize)
}

func TestDefaultPOICLifConfig_Disabled(t *testing.T) {
	t.Setenv(envEnableICLifsForPOPools(), "false")
	cfg := DefaultPOICLifConfig()
	assert.False(t, cfg.Enabled)
}

// Helpers keep tests decoupled from the exact env constant names while still
// exercising DefaultPOICLifConfig's env reads.
func envEnableICLifsForPOPools() string { return "ENABLE_IC_LIFS_FOR_PO_POOLS" }
func envPOICLifSubnetMaskSize() string  { return "PO_IC_LIF_SUBNET_MASK_SIZE" }
