package datamodel

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseInternalTrialResourceName(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		project, location, err := ParseInternalTrialResourceName("projects/123/locations/us-central1/trial")
		require.NoError(t, err)
		assert.Equal(t, "123", project)
		assert.Equal(t, "us-central1", location)
	})

	t.Run("invalid format", func(t *testing.T) {
		_, _, err := ParseInternalTrialResourceName("invalid")
		assert.Error(t, err)
	})

	t.Run("wrong segment count", func(t *testing.T) {
		_, _, err := ParseInternalTrialResourceName("projects/123/locations/us-central1")
		assert.Error(t, err)
	})

	t.Run("wrong resource type", func(t *testing.T) {
		_, _, err := ParseInternalTrialResourceName("projects/123/locations/us-central1/pools/pool-1")
		assert.Error(t, err)
	})

	t.Run("empty project", func(t *testing.T) {
		_, _, err := ParseInternalTrialResourceName("projects//locations/us-central1/trial")
		assert.Error(t, err)
	})

	t.Run("empty location", func(t *testing.T) {
		_, _, err := ParseInternalTrialResourceName("projects/123/locations//trial")
		assert.Error(t, err)
	})
}

func TestFormatInternalTrialResourceName(t *testing.T) {
	assert.Equal(t, "projects/123/locations/us-central1/trial", FormatInternalTrialResourceName("123", "us-central1"))
}

func TestTrialResourceNameForAccount(t *testing.T) {
	assert.Equal(t, "projects/846223794136/locations/us-central1/trial",
		TrialResourceNameForAccount("846223794136", "us-central1"))
	assert.Equal(t, "", TrialResourceNameForAccount("", "us-central1"))
	assert.Equal(t, "", TrialResourceNameForAccount("846223794136", ""))
	assert.Equal(t, "projects/846223794136/locations/us-central1/trial",
		TrialResourceNameForAccount(" 846223794136 ", " us-central1 "))
}

func TestInternalTrial_UnmarshalJSON_GoogleSchema(t *testing.T) {
	start := time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("Google camelCase JSON", func(t *testing.T) {
		body := `{
			"name": "projects/1076051490066/locations/us-central1/trial",
			"startTime": "2025-05-01T12:00:00Z",
			"endTime": "2025-06-01T12:00:00Z",
			"exitReason": "CONVERTED"
		}`
		var trial InternalTrial
		require.NoError(t, json.Unmarshal([]byte(body), &trial))
		assert.Equal(t, "projects/1076051490066/locations/us-central1/trial", trial.Name)
		assert.True(t, start.Equal(trial.StartTime))
		assert.True(t, end.Equal(trial.EndTime))
		require.NotNil(t, trial.ExitReason)
		assert.Equal(t, TrialExitReason("CONVERTED"), *trial.ExitReason)
	})

	t.Run("numeric exitReason", func(t *testing.T) {
		body := `{"name":"projects/p/locations/us-central1/trial","startTime":"2025-05-01T12:00:00Z","endTime":"2025-06-01T12:00:00Z","exitReason":3}`
		var trial InternalTrial
		require.NoError(t, json.Unmarshal([]byte(body), &trial))
		require.NotNil(t, trial.ExitReason)
		assert.Equal(t, TrialExitReason("3"), *trial.ExitReason)
	})

	t.Run("ToAccountTrialMode uses camelCase for VCP storage", func(t *testing.T) {
		exit := TrialExitReason("CONVERTED")
		mode := (&InternalTrial{
			Name:       "projects/p/locations/us-central1/trial",
			StartTime:  start,
			EndTime:    end,
			ExitReason: &exit,
		}).ToAccountTrialMode()
		require.NotNil(t, mode)

		raw, err := json.Marshal(mode)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"startTime"`)
		assert.Contains(t, string(raw), `"endTime"`)
		assert.Contains(t, string(raw), `"exitReason"`)
		assert.NotContains(t, string(raw), `"start_time"`)
	})
}

func TestTrialExitReason_IsSet(t *testing.T) {
	assert.False(t, TrialExitReason("").IsSet())
	assert.False(t, TrialExitReason("EXIT_REASON_UNSPECIFIED").IsSet())
	assert.True(t, TrialExitReason("CONVERTED").IsSet())
}

func TestInternalTrial_ToAccountTrialMode(t *testing.T) {
	start := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	exit := TrialExitReason("TRIAL_ENDED")

	trial := (&InternalTrial{
		Name:       "projects/p/locations/us-central1/trial",
		StartTime:  start,
		EndTime:    end,
		ExitReason: &exit,
	}).ToAccountTrialMode()

	require.NotNil(t, trial)
	assert.True(t, start.Equal(*trial.StartTime))
	assert.True(t, end.Equal(*trial.EndTime))
	require.NotNil(t, trial.ExitReason)
	assert.Equal(t, string(exit), *trial.ExitReason)
}

func TestInternalTrial_ToAccountTrialMode_NilReceiver(t *testing.T) {
	var trial *InternalTrial
	assert.Nil(t, trial.ToAccountTrialMode())
}

func TestInternalTrial_ToAccountTrialMode_WithoutExitReason(t *testing.T) {
	start := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	mode := (&InternalTrial{
		Name:      "projects/p/locations/us-central1/trial",
		StartTime: start,
		EndTime:   end,
	}).ToAccountTrialMode()

	require.NotNil(t, mode)
	assert.Nil(t, mode.ExitReason)
	assert.True(t, start.Equal(*mode.StartTime))
	assert.True(t, end.Equal(*mode.EndTime))
}

func TestInternalTrial_ToAccountTrialMode_ZeroTimestampsLeftNil(t *testing.T) {
	// CCFE omits startTime/endTime when a trial has not started; the Go zero value
	// must not be persisted into account_metadata as "0001-01-01T00:00:00Z".
	mode := (&InternalTrial{
		Name: "projects/p/locations/us-central1/trial",
	}).ToAccountTrialMode()

	require.NotNil(t, mode)
	assert.Nil(t, mode.StartTime)
	assert.Nil(t, mode.EndTime)
}

func TestInternalTrial_ToAccountTrialMode_UnspecifiedExitReason(t *testing.T) {
	start := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	unspecified := TrialExitReason("EXIT_REASON_UNSPECIFIED")

	mode := (&InternalTrial{
		Name:       "projects/p/locations/us-central1/trial",
		StartTime:  start,
		EndTime:    end,
		ExitReason: &unspecified,
	}).ToAccountTrialMode()

	require.NotNil(t, mode)
	assert.Nil(t, mode.ExitReason)
}
