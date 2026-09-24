package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input  string
		want   version
		wantOK bool
	}{
		// Base versions
		{"9.12.1", version{9, 12, 1, 0}, true},
		{"9.13.0", version{9, 13, 0, 0}, true},
		{"10.0.0", version{10, 0, 0, 0}, true},

		// Patch-level versions
		{"9.12.1P2", version{9, 12, 1, 2}, true},
		{"9.13.0P10", version{9, 13, 0, 10}, true},

		// Suffix stripping — non-P suffixes are discarded
		{"9.12.1RC1", version{9, 12, 1, 0}, true},
		{"9.12.1GA", version{9, 12, 1, 0}, true},
		{"9.12.1X50", version{9, 12, 1, 0}, true},
		// Patch level followed by suffix — keeps the Pn part
		{"9.12.1P2RC1", version{9, 12, 1, 2}, true},

		// Extra dot-segment treated as a non-P suffix and stripped to X.Y.Z
		{"9.12.1.2", version{9, 12, 1, 0}, true},

		// Invalid inputs
		{"", version{}, false},
		{"notaversion", version{}, false},
		{"9.12", version{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parseVersion(tt.input)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestVersionString(t *testing.T) {
	tests := []struct {
		v    version
		want string
	}{
		{version{9, 12, 1, 0}, "9.12.1"},
		{version{9, 12, 1, 2}, "9.12.1P2"},
		{version{9, 13, 0, 10}, "9.13.0P10"},
		{version{10, 0, 0, 0}, "10.0.0"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.v.String())
	}
}

func TestIsDrainRoleDeployment(t *testing.T) {
	assert.True(t, isDrainRoleDeployment(map[string]string{drainRoleLabel: "drain"}))
	assert.False(t, isDrainRoleDeployment(map[string]string{drainRoleLabel: "primary"}))
	assert.False(t, isDrainRoleDeployment(nil))
}

func TestLineLessThan(t *testing.T) {
	tests := []struct {
		a, b version
		want bool
	}{
		{version{9, 12, 1, 0}, version{9, 13, 0, 0}, true},  // lower minor
		{version{9, 13, 0, 0}, version{9, 12, 1, 0}, false}, // higher minor
		{version{9, 12, 0, 0}, version{9, 12, 1, 0}, true},  // lower patch
		{version{9, 12, 1, 0}, version{9, 12, 1, 0}, false}, // equal lines
		{version{9, 12, 1, 2}, version{9, 12, 1, 5}, false}, // same line, different level — lineLessThan ignores level
		{version{8, 12, 1, 0}, version{9, 12, 1, 0}, true},  // lower major
		{version{10, 0, 0, 0}, version{9, 12, 1, 0}, false}, // higher major
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, lineLessThan(tt.a, tt.b),
			"lineLessThan(%v, %v)", tt.a, tt.b)
	}
}

func TestDeployNameToVersion(t *testing.T) {
	tests := []struct {
		name   string
		want   version
		wantOK bool
	}{
		{"vlm-worker-9-12-1", version{9, 12, 1, 0}, true},
		{"vlm-worker-9-13-0", version{9, 13, 0, 0}, true},
		{"vlm-worker-9-12-1p2", version{9, 12, 1, 2}, true},
		{"vlm-worker-9-13-0p10", version{9, 13, 0, 10}, true},

		// Not a vlm-worker deployment
		{"other-deployment-9-12-1", version{}, false},
		// Missing version segments
		{"vlm-worker-9-12", version{}, false},
		// Empty
		{"", version{}, false},
		// vlm-worker prefix but wrong format
		{"vlm-worker-abc", version{}, false},
		// Drain Deployments are skipped by label before parse in reconcile.
		{"vlm-worker-9-18-1p3-drain-8083481", version{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := deployNameToVersion(tt.name)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestNormalizeVersions(t *testing.T) {
	t.Run("deduplicates identical versions", func(t *testing.T) {
		got := normalizeVersions([]string{"9.12.1", "9.12.1", "9.13.0"})
		assert.Equal(t, []version{{9, 12, 1, 0}, {9, 13, 0, 0}}, got)
	})

	t.Run("strips non-P suffixes before deduplication", func(t *testing.T) {
		// 9.12.1RC1 and 9.12.1GA both normalize to 9.12.1 — one entry
		got := normalizeVersions([]string{"9.12.1RC1", "9.12.1GA", "9.13.0"})
		assert.Len(t, got, 2)
		assert.Equal(t, version{9, 12, 1, 0}, got[0])
	})

	t.Run("sorts by major.minor.patch then level", func(t *testing.T) {
		raw := []string{"9.13.0", "9.12.1P2", "9.12.1", "8.0.0"}
		got := normalizeVersions(raw)
		assert.Equal(t, []version{
			{8, 0, 0, 0},
			{9, 12, 1, 0},
			{9, 12, 1, 2},
			{9, 13, 0, 0},
		}, got)
	})

	t.Run("skips unparseable entries", func(t *testing.T) {
		got := normalizeVersions([]string{"bad", "", "9.12.1"})
		assert.Equal(t, []version{{9, 12, 1, 0}}, got)
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		assert.Nil(t, normalizeVersions(nil))
	})
}

func TestShouldKeep(t *testing.T) {
	tests := []struct {
		name     string
		depVer   version
		active   []version
		wantKeep bool
	}{
		// Rule 1: direct match
		{
			name:     "rule1_direct_match_base",
			depVer:   version{9, 12, 1, 0},
			active:   []version{{9, 12, 1, 0}},
			wantKeep: true,
		},
		{
			name:     "rule1_direct_match_with_level",
			depVer:   version{9, 12, 1, 2},
			active:   []version{{9, 12, 1, 2}},
			wantKeep: true,
		},

		// Rule 2: lower-line active version means all higher-line workers stay up
		{
			name:     "rule2_higher_line_stays_for_migration",
			depVer:   version{9, 13, 0, 0},
			active:   []version{{9, 12, 1, 0}}, // 9.12 is active and lower-line
			wantKeep: true,
		},
		{
			name:     "rule2_much_higher_line_stays",
			depVer:   version{9, 14, 0, 0},
			active:   []version{{9, 12, 1, 0}, {9, 13, 0, 0}},
			wantKeep: true,
		},

		// Rule 3: patch ladder — same line, keep all levels >= min active level
		{
			name:     "rule3_equal_to_min_level",
			depVer:   version{9, 12, 1, 2},
			active:   []version{{9, 12, 1, 2}},
			wantKeep: true,
		},
		{
			name:     "rule3_above_min_level",
			depVer:   version{9, 12, 1, 3},
			active:   []version{{9, 12, 1, 2}},
			wantKeep: true,
		},
		{
			name:     "rule3_below_min_level_scales",
			depVer:   version{9, 12, 1, 1},
			active:   []version{{9, 12, 1, 2}}, // min level on this line is 2
			wantKeep: false,
		},

		// Scale-to-zero cases
		{
			name:     "scale_older_line_no_lower_active",
			depVer:   version{9, 11, 0, 0},
			active:   []version{{9, 12, 1, 0}, {9, 13, 0, 0}},
			wantKeep: false,
		},
		{
			name:     "scale_old_patch_below_ladder",
			depVer:   version{9, 12, 0, 0},
			active:   []version{{9, 12, 1, 0}}, // min level on 9.12.x is 0 but different patch
			wantKeep: false,                    // 9.12.0 vs 9.12.1 — different lineKey
		},
		{
			name:     "scale_no_match_no_lower_active_no_same_line",
			depVer:   version{9, 10, 0, 0},
			active:   []version{{9, 12, 1, 0}},
			wantKeep: false,
		},

		// Safe default: unparseable name handled upstream, but shouldKeep itself
		// only sees valid versions — confirming single-version active set
		{
			name:     "single_active_version_exact_match",
			depVer:   version{9, 12, 1, 0},
			active:   []version{{9, 12, 1, 0}},
			wantKeep: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keep, reason := shouldKeep(tt.depVer, tt.active)
			assert.Equal(t, tt.wantKeep, keep, "reason: %q", reason)
		})
	}
}
