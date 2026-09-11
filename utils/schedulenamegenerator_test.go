package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertDaysOfWeekToIntArray(t *testing.T) {
	result, err := ConvertDaysOfWeekToIntArray("Monday,Tuesday,Wednesday")
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, result)

	_, err = ConvertDaysOfWeekToIntArray("Funday")
	require.Error(t, err)
}

func TestConvertDaysOfWeekToIntArray_EmptyDefaultsSunday(t *testing.T) {
	result, err := ConvertDaysOfWeekToIntArray("")
	require.NoError(t, err)
	assert.Equal(t, []int{int(time.Sunday)}, result)
}

func TestConvertDaysOfWeekToIntArray_RejectsDuplicateDays(t *testing.T) {
	_, err := ConvertDaysOfWeekToIntArray("Monday,Monday")
	require.Error(t, err)
}

func TestGenerateWeekdayPartOfScheduleName(t *testing.T) {
	assert.Equal(t, "sunday", GenerateWeekdayPartOfScheduleName([]int{0}))
	assert.Equal(t, "monday+tuesday", GenerateWeekdayPartOfScheduleName([]int{1, 2}))
	assert.Equal(t, "", GenerateWeekdayPartOfScheduleName([]int{}))
}

func TestGenerateHourPartOfScheduleName(t *testing.T) {
	assert.Equal(t, "12am", GenerateHourPartOfScheduleName([]int{}))
	assert.Equal(t, "12am", GenerateHourPartOfScheduleName([]int{0}))
	assert.Equal(t, "1am", GenerateHourPartOfScheduleName([]int{1}))
	assert.Equal(t, "12pm", GenerateHourPartOfScheduleName([]int{12}))
	assert.Equal(t, "1pm", GenerateHourPartOfScheduleName([]int{13}))
	assert.Equal(t, "11pm", GenerateHourPartOfScheduleName([]int{23}))
}

func TestGenerateMinutePartOfScheduleName(t *testing.T) {
	assert.Equal(t, "0-min-past", GenerateMinutePartOfScheduleName([]int{}))
	assert.Equal(t, "0-min-past", GenerateMinutePartOfScheduleName([]int{0}))
	assert.Equal(t, "15-min-past", GenerateMinutePartOfScheduleName([]int{15}))
	assert.Equal(t, "59-min-past", GenerateMinutePartOfScheduleName([]int{59}))
}
