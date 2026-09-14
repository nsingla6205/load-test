package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBackupChainHistoryRetentionDays(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want int
	}{
		{name: "default", want: defaultBackupChainHistoryRetentionDays},
		{name: "configured", env: "14", want: 14},
		{name: "clamped", env: "0", want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("BACKUP_CHAIN_HISTORY_RETENTION_DAYS", test.env)
			assert.Equal(t, test.want, BackupChainHistoryRetentionDays())
		})
	}
}

func TestParseBackupChainHistoryPartitionDay(t *testing.T) {
	tests := []struct {
		name      string
		partition string
		want      time.Time
		ok        bool
	}{
		{
			name:      "valid partition",
			partition: "backup_chain_histories_2026_08_30",
			want:      time.Date(2026, time.August, 30, 0, 0, 0, 0, time.UTC),
			ok:        true,
		},
		{name: "wrong prefix", partition: "backup_histories_2026_08_30"},
		{name: "invalid day", partition: "backup_chain_histories_2026_02_30"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseBackupChainHistoryPartitionDay(test.partition)
			assert.Equal(t, test.ok, ok)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestQuoteVCPPGIdent(t *testing.T) {
	assert.Equal(t, `"backup_chain_histories_2026_08_30"`, quoteVCPPGIdent("backup_chain_histories_2026_08_30"))
	assert.Equal(t, `"a""b"`, quoteVCPPGIdent(`a"b`))
}

func TestDayStartUTC(t *testing.T) {
	value := time.Date(2026, time.August, 30, 23, 45, 0, 0, time.FixedZone("UTC+5:30", 19800))
	assert.Equal(t, time.Date(2026, time.August, 30, 0, 0, 0, 0, time.UTC), dayStartUTC(value))
}
