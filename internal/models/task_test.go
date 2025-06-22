package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTaskInterval_NextExecution(t *testing.T) {
	tests := []struct {
		name     string
		interval TaskInterval
		from     time.Time
		want     time.Time
	}{
		{
			name: "hourly - simple",
			interval: TaskInterval{
				Type:     IntervalTypeHourly,
				Value:    1,
				Timezone: "UTC",
			},
			from: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			want: time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC),
		},
		{
			name: "hourly - every 2 hours",
			interval: TaskInterval{
				Type:     IntervalTypeHourly,
				Value:    2,
				Timezone: "UTC",
			},
			from: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
			want: time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "daily - at specific time (future)",
			interval: TaskInterval{
				Type:     IntervalTypeDaily,
				Value:    1,
				Timezone: "UTC",
				AtTime:   &TimeOfDay{Hour: 14, Minute: 30},
			},
			from: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			want: time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC),
		},
		{
			name: "daily - at specific time (past)",
			interval: TaskInterval{
				Type:     IntervalTypeDaily,
				Value:    1,
				Timezone: "UTC",
				AtTime:   &TimeOfDay{Hour: 9, Minute: 0},
			},
			from: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			want: time.Date(2024, 1, 16, 9, 0, 0, 0, time.UTC),
		},
		{
			name: "weekly",
			interval: TaskInterval{
				Type:     IntervalTypeWeekly,
				Value:    1,
				Timezone: "UTC",
				AtTime:   &TimeOfDay{Hour: 12, Minute: 0},
			},
			from: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), // Monday
			want: time.Date(2024, 1, 22, 12, 0, 0, 0, time.UTC), // Next Monday
		},
		{
			name: "monthly",
			interval: TaskInterval{
				Type:     IntervalTypeMonthly,
				Value:    1,
				Timezone: "UTC",
				AtTime:   &TimeOfDay{Hour: 0, Minute: 0},
			},
			from: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			want: time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.interval.NextExecution(tt.from)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTaskInterval_IsDue(t *testing.T) {
	interval := TaskInterval{
		Type:     IntervalTypeDaily,
		Value:    1,
		Timezone: "UTC",
		AtTime:   &TimeOfDay{Hour: 10, Minute: 0},
	}

	tests := []struct {
		name          string
		lastExecution time.Time
		now           time.Time
		want          bool
	}{
		{
			name:          "not due yet",
			lastExecution: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			now:           time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC),
			want:          false,
		},
		{
			name:          "exactly due",
			lastExecution: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			now:           time.Date(2024, 1, 16, 10, 0, 0, 0, time.UTC),
			want:          true,
		},
		{
			name:          "overdue",
			lastExecution: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			now:           time.Date(2024, 1, 16, 11, 0, 0, 0, time.UTC),
			want:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interval.IsDue(tt.lastExecution, tt.now)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTimeZoneHandling(t *testing.T) {
	// Test with New York timezone
	interval := TaskInterval{
		Type:     IntervalTypeDaily,
		Value:    1,
		Timezone: "America/New_York",
		AtTime:   &TimeOfDay{Hour: 9, Minute: 0}, // 9 AM EST/EDT
	}

	// From UTC time
	from := time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC) // 10 AM EST
	next := interval.NextExecution(from)

	// Should be next day at 9 AM EST
	nyLoc, _ := time.LoadLocation("America/New_York")
	expected := time.Date(2024, 1, 16, 9, 0, 0, 0, nyLoc)
	
	assert.Equal(t, expected, next)
	assert.Equal(t, "America/New_York", next.Location().String())
}