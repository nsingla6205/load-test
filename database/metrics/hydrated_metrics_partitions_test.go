package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHydratedMetricsPartitionDay(t *testing.T) {
	t.Run("valid day partition", func(t *testing.T) {
		day, ok := parseHydratedMetricsPartitionDay("hydrated_metrics_2026_08_11")
		require.True(t, ok)
		assert.Equal(t, time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC), day)
	})
	t.Run("rejects non-matching names", func(t *testing.T) {
		_, ok := parseHydratedMetricsPartitionDay("hydrated_metrics_legacy_pre_partition")
		assert.False(t, ok)
		_, ok = parseHydratedMetricsPartitionDay("other_table_2026_08_11")
		assert.False(t, ok)
	})
}

func TestQuotePGIdent(t *testing.T) {
	assert.Equal(t, `"hydrated_metrics_2026_08_11"`, quotePGIdent("hydrated_metrics_2026_08_11"))
	assert.Equal(t, `"weird""name"`, quotePGIdent(`weird"name`))
}
