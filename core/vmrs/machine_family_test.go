package vmrs

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMachineFamilyType_IsNonLssd(t *testing.T) {
	cases := []struct {
		name   string
		family MachineFamilyType
		want   bool
	}{
		{"unset", MachineFamilyUnset, false},
		{"c3-lssd", MachineFamilyC3LSSD, false},
		{"c3-non-lssd", MachineFamilyC3NonLSSD, true},
		{"c4-lssd", MachineFamilyC4LSSD, false},
		{"c4-non-lssd", MachineFamilyC4NonLSSD, true},
		{"unknown", MachineFamilyType("unknown"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.family.IsNonLssd())
		})
	}
}

func TestMachineFamilyForVMType(t *testing.T) {
	cases := []struct {
		name   string
		vmType string
		want   MachineFamilyType
	}{
		{"empty is unset", "", MachineFamilyUnset},
		{"whitespace is unset", "  ", MachineFamilyUnset},
		{"c3 lssd row", "c3-standard-8-lssd", MachineFamilyC3LSSD},
		{"c3 bare row", "c3-standard-8", MachineFamilyC3NonLSSD},
		{"c4 lssd row", "c4-standard-4-lssd", MachineFamilyC4LSSD},
		{"c4 bare row", "c4-standard-4", MachineFamilyC4NonLSSD},
		{"trims whitespace", " c3-standard-8-lssd ", MachineFamilyC3LSSD},
		{"unrecognized prefix is unset", "n2-standard-8", MachineFamilyUnset},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, MachineFamilyForVMType(tc.vmType))
		})
	}
}

func TestMachineFamilyType_IsValid(t *testing.T) {
	cases := []struct {
		name   string
		family MachineFamilyType
		want   bool
	}{
		{"unset", MachineFamilyUnset, false},
		{"c3-lssd", MachineFamilyC3LSSD, true},
		{"c3-non-lssd", MachineFamilyC3NonLSSD, true},
		{"c4-lssd", MachineFamilyC4LSSD, true},
		{"c4-non-lssd", MachineFamilyC4NonLSSD, true},
		{"c5-lssd", MachineFamilyType("c5-lssd"), false},
		{"unknown", MachineFamilyType("unknown"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.family.IsValid())
		})
	}
}

func TestResolveMachineFamily(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want MachineFamilyType
	}{
		{"empty falls back to default", "", DefaultMachineFamily},
		{"whitespace falls back to default", "   ", DefaultMachineFamily},
		{"unrecognized falls back to default", "c9-lssd", DefaultMachineFamily},
		{"valid c3-lssd", "c3-lssd", MachineFamilyC3LSSD},
		{"valid c3-non-lssd", "c3-non-lssd", MachineFamilyC3NonLSSD},
		{"valid c4-lssd", "c4-lssd", MachineFamilyC4LSSD},
		{"valid c4-non-lssd", "c4-non-lssd", MachineFamilyC4NonLSSD},
		{"trims surrounding whitespace", " c4-lssd ", MachineFamilyC4LSSD},
		{"uppercase is normalized", "C4-LSSD", MachineFamilyC4LSSD},
		{"mixed case is normalized", "C4-Non-LSSD", MachineFamilyC4NonLSSD},
		{"uppercase with whitespace is normalized", "  C3-LSSD  ", MachineFamilyC3LSSD},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveMachineFamily(tc.raw)
			assert.Equal(t, tc.want, got)
			assert.True(t, got.IsValid(), "resolved family must always be valid/non-empty")
		})
	}
}

func TestResolveMachineFamily_FallbackWarning(t *testing.T) {
	// Redirect the default slog logger to a buffer so we can assert on the
	// warning emitted when an unrecognized MACHINE_FAMILY_TYPE falls back.
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(orig) })

	t.Run("non-empty invalid value warns and falls back", func(t *testing.T) {
		buf.Reset()
		got := resolveMachineFamily("c4-lssdd")
		assert.Equal(t, DefaultMachineFamily, got)
		out := buf.String()
		assert.Contains(t, out, "unrecognized MACHINE_FAMILY_TYPE")
		assert.Contains(t, out, "c4-lssdd", "warning should surface the configured value")
		assert.Contains(t, out, string(DefaultMachineFamily), "warning should surface the fallback family")
	})

	t.Run("invalid casing is normalized and does not warn", func(t *testing.T) {
		buf.Reset()
		got := resolveMachineFamily("C4-LSSD")
		assert.Equal(t, MachineFamilyC4LSSD, got)
		assert.Empty(t, buf.String(), "a value that resolves after normalization must not warn")
	})

	t.Run("empty value falls back silently", func(t *testing.T) {
		buf.Reset()
		got := resolveMachineFamily("   ")
		assert.Equal(t, DefaultMachineFamily, got)
		assert.Empty(t, buf.String(), "unset/empty is the legitimate default path and must stay quiet")
	})

	t.Run("valid value does not warn", func(t *testing.T) {
		buf.Reset()
		got := resolveMachineFamily("c4-non-lssd")
		assert.Equal(t, MachineFamilyC4NonLSSD, got)
		assert.Empty(t, buf.String())
	})
}

func TestDefaultMachineFamilyIsC3LSSD(t *testing.T) {
	assert.Equal(t, MachineFamilyC3LSSD, DefaultMachineFamily)
}

func TestFilterVMsByMachineFamily(t *testing.T) {
	catalog := []VMPerfLimit{
		{VMType: "c3-standard-4-lssd"},
		{VMType: "c3-standard-8-lssd"},
		{VMType: "c3-standard-22-lssd"},
		{VMType: "c3-standard-4"},
		{VMType: "c3-standard-8"},
		{VMType: "c4-standard-4"},
		{VMType: "c4-standard-4-lssd"},
		{VMType: "c4-standard-8"},
		{VMType: "c4-standard-8-lssd"},
		{VMType: "c4-standard-22-lssd"},
	}

	cases := []struct {
		name   string
		family MachineFamilyType
		want   []string
	}{
		{
			name:   "unset returns all rows untouched",
			family: MachineFamilyUnset,
			want: []string{
				"c3-standard-4-lssd", "c3-standard-8-lssd", "c3-standard-22-lssd",
				"c3-standard-4", "c3-standard-8",
				"c4-standard-4", "c4-standard-4-lssd", "c4-standard-8",
				"c4-standard-8-lssd", "c4-standard-22-lssd",
			},
		},
		{
			name:   "c3-lssd keeps c3 lssd rows only",
			family: MachineFamilyC3LSSD,
			want:   []string{"c3-standard-4-lssd", "c3-standard-8-lssd", "c3-standard-22-lssd"},
		},
		{
			name:   "c3-non-lssd keeps c3 bare rows only",
			family: MachineFamilyC3NonLSSD,
			want:   []string{"c3-standard-4", "c3-standard-8"},
		},
		{
			name:   "c4-lssd keeps c4 lssd rows only",
			family: MachineFamilyC4LSSD,
			want:   []string{"c4-standard-4-lssd", "c4-standard-8-lssd", "c4-standard-22-lssd"},
		},
		{
			name:   "c4-non-lssd keeps c4 bare rows only",
			family: MachineFamilyC4NonLSSD,
			want:   []string{"c4-standard-4", "c4-standard-8"},
		},
		{
			name:   "unknown family value falls back to all rows",
			family: MachineFamilyType("totally-unknown"),
			want: []string{
				"c3-standard-4-lssd", "c3-standard-8-lssd", "c3-standard-22-lssd",
				"c3-standard-4", "c3-standard-8",
				"c4-standard-4", "c4-standard-4-lssd", "c4-standard-8",
				"c4-standard-8-lssd", "c4-standard-22-lssd",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterVMsByMachineFamily(catalog, tc.family)
			gotTypes := make([]string, 0, len(got))
			for _, vm := range got {
				gotTypes = append(gotTypes, vm.VMType)
			}
			assert.Equal(t, tc.want, gotTypes)
		})
	}
}

func TestFilterVMsByMachineFamily_DoesNotMutateInput(t *testing.T) {
	catalog := []VMPerfLimit{
		{VMType: "c3-standard-4-lssd"},
		{VMType: "c4-standard-4"},
	}
	original := append([]VMPerfLimit(nil), catalog...)
	_ = FilterVMsByMachineFamily(catalog, MachineFamilyC4NonLSSD)
	assert.Equal(t, original, catalog, "filter must not mutate the input slice")
}
