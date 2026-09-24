package utils

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

const (
	SnapshotScheduleLabelHourly  = "hourly"
	SnapshotScheduleLabelDaily   = "daily"
	SnapshotScheduleLabelWeekly  = "weekly"
	SnapshotScheduleLabelMonthly = "monthly"

	DaysOfWeekError = `day in weeklySchedule must include 1-7 (inclusive) unique weekdays, that are comma separated.`
)

// ConvertDaysOfWeekToIntArray parses weekly schedule day strings into weekday integers.
func ConvertDaysOfWeekToIntArray(days string) ([]int, error) {
	if days == "" {
		return []int{int(time.Sunday)}, nil
	}

	splitDays := strings.Split(days, ",")
	weekdays := []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}
	var result []int

	for _, day := range splitDays {
		day := strings.TrimSpace(day)
		hasFoundDay := false
		for _, weekday := range weekdays {
			if strings.HasPrefix(strings.ToLower(weekday.String()), strings.ToLower(day)) {
				weekDayValue := int(weekday)
				if slices.Contains(result, weekDayValue) {
					return nil, errors.NewUserInputValidationErr(DaysOfWeekError)
				}
				result = append(result, weekDayValue)
				hasFoundDay = true
				break
			}
		}
		if !hasFoundDay {
			return nil, errors.NewUserInputValidationErr(DaysOfWeekError)
		}
	}

	return result, nil
}

// ConvertDaysOfWeekFromIntArray formats weekday integers as comma-separated
// weekday names. Invalid values (outside 0-6) are skipped. Empty or all-invalid
// input defaults to Sunday.
func ConvertDaysOfWeekFromIntArray(days []int) string {
	resultDays := make([]string, 0, len(days))
	for _, day := range days {
		if day >= 0 && day <= 6 {
			resultDays = append(resultDays, time.Weekday(day).String())
		}
	}
	if len(resultDays) == 0 {
		resultDays = append(resultDays, time.Sunday.String())
	}
	return strings.Join(resultDays, ",")
}

func GenerateWeekdayPartOfScheduleName(daysOfWeek []int) (days string) {
	for i, day := range daysOfWeek {
		if i > 0 {
			days += "+"
		}
		days += strings.ToLower(time.Weekday(day).String())
	}
	return
}

func GenerateHourPartOfScheduleName(hours []int) string {
	hour := 12
	ampm := "am"
	if len(hours) > 0 {
		hour = hours[0]
		if hour > 11 {
			hour = hour - 12
			ampm = "pm"
		}
		if hour == 0 {
			hour = 12
		}
	}
	return fmt.Sprintf("%v%s", hour, ampm)
}

func GenerateMinutePartOfScheduleName(minutes []int) string {
	minute := 0
	if len(minutes) > 0 {
		minute = minutes[0]
	}
	return fmt.Sprintf("%v-min-past", minute)
}
