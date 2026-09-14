package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAggregatedUsageRetentionDays(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want int
	}{
		{name: "default", want: defaultAggregatedUsageRetentionDays},
		{name: "configured", env: "14", want: 14},
		{name: "clamped", env: "0", want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AGGREGATED_USAGES_RETENTION_DAYS", test.env)
			assert.Equal(t, test.want, AggregatedUsageRetentionDays())
		})
	}
}

func TestParseAggregatedUsagePartitionDay(t *testing.T) {
	tests := []struct {
		name      string
		partition string
		want      time.Time
		ok        bool
	}{
		{
			name:      "valid partition",
			partition: "aggregated_usages_2026_08_30",
			want:      time.Date(2026, time.August, 30, 0, 0, 0, 0, time.UTC),
			ok:        true,
		},
		{name: "wrong prefix", partition: "aggregated_usage_2026_08_30"},
		{name: "invalid day", partition: "aggregated_usages_2026_02_30"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseAggregatedUsagePartitionDay(test.partition)
			assert.Equal(t, test.ok, ok)
			assert.Equal(t, test.want, got)
		})
	}
}
