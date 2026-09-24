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

func TestConvertDaysOfWeekFromIntArray(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want string
	}{
		{name: "valid weekdays", in: []int{1, 2, 3}, want: "Monday,Tuesday,Wednesday"},
		{name: "empty defaults to Sunday", in: []int{}, want: "Sunday"},
		{name: "nil defaults to Sunday", in: nil, want: "Sunday"},
		{name: "ignores invalid ints", in: []int{-1, 0, 6, 7}, want: "Sunday,Saturday"},
		{name: "all weekdays", in: []int{0, 1, 2, 3, 4, 5, 6}, want: "Sunday,Monday,Tuesday,Wednesday,Thursday,Friday,Saturday"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ConvertDaysOfWeekFromIntArray(tt.in))
		})
	}
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
