package models

import (
	"time"
)

// IntervalType represents the type of scheduling interval
type IntervalType string

const (
	IntervalTypeHourly  IntervalType = "hourly"
	IntervalTypeDaily   IntervalType = "daily"
	IntervalTypeWeekly  IntervalType = "weekly"
	IntervalTypeMonthly IntervalType = "monthly"
)

// TaskType represents a predefined task that can be scheduled
type TaskType struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Interval    TaskInterval  `json:"interval"`
	Timeout     time.Duration `json:"timeout"`
	Retries     int           `json:"retries"`
	Config      TaskConfig    `json:"config"`
}

// TaskInterval defines when and how often a task should run
type TaskInterval struct {
	Type     IntervalType `json:"type"`     // "hourly", "daily", "weekly", "monthly"
	Value    int          `json:"value"`    // e.g., 2 for "every 2 hours"
	Timezone string       `json:"timezone"` // e.g., "UTC", "America/New_York"
	AtTime   *TimeOfDay   `json:"at_time"`  // For daily tasks, specific time
}

// TimeOfDay represents a specific time of day
type TimeOfDay struct {
	Hour   int `json:"hour"`   // 0-23
	Minute int `json:"minute"` // 0-59
}

// TaskConfig contains task-specific configuration
type TaskConfig struct {
	Endpoint    string            `json:"endpoint"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
	MaxDuration time.Duration     `json:"max_duration"`
}

// NextExecution calculates the next execution time based on the interval
func (ti TaskInterval) NextExecution(from time.Time) time.Time {
	loc, err := time.LoadLocation(ti.Timezone)
	if err != nil {
		loc = time.UTC
	}

	fromLocal := from.In(loc)

	switch ti.Type {
	case IntervalTypeHourly:
		// Round up to next hour, then add interval
		next := fromLocal.Truncate(time.Hour).Add(time.Hour)
		if ti.Value > 1 {
			next = next.Add(time.Duration(ti.Value-1) * time.Hour)
		}
		return next

	case IntervalTypeDaily:
		// Next day at specified time
		next := time.Date(
			fromLocal.Year(), fromLocal.Month(), fromLocal.Day(),
			0, 0, 0, 0, loc,
		).AddDate(0, 0, ti.Value)

		if ti.AtTime != nil {
			next = time.Date(
				next.Year(), next.Month(), next.Day(),
				ti.AtTime.Hour, ti.AtTime.Minute, 0, 0, loc,
			)
		}

		// If the time has already passed today, use today
		if ti.Value == 1 && ti.AtTime != nil {
			today := time.Date(
				fromLocal.Year(), fromLocal.Month(), fromLocal.Day(),
				ti.AtTime.Hour, ti.AtTime.Minute, 0, 0, loc,
			)
			if today.After(from) {
				return today
			}
		}

		return next

	case IntervalTypeWeekly:
		// Next week on the same day
		next := fromLocal.Truncate(24 * time.Hour).AddDate(0, 0, 7*ti.Value)
		if ti.AtTime != nil {
			next = time.Date(
				next.Year(), next.Month(), next.Day(),
				ti.AtTime.Hour, ti.AtTime.Minute, 0, 0, loc,
			)
		}
		return next

	case IntervalTypeMonthly:
		// Next month on the same day
		next := fromLocal.AddDate(0, ti.Value, 0)
		next = time.Date(
			next.Year(), next.Month(), fromLocal.Day(),
			0, 0, 0, 0, loc,
		)
		if ti.AtTime != nil {
			next = time.Date(
				next.Year(), next.Month(), next.Day(),
				ti.AtTime.Hour, ti.AtTime.Minute, 0, 0, loc,
			)
		}
		return next

	default:
		// Default to hourly
		return fromLocal.Add(time.Hour)
	}
}

// IsDue checks if the task is due for execution
func (ti TaskInterval) IsDue(lastExecution, now time.Time) bool {
	nextExecution := ti.NextExecution(lastExecution)
	return now.After(nextExecution) || now.Equal(nextExecution)
}