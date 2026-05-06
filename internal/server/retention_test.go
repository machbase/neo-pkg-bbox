package server

import (
	"testing"
	"time"

	"github.com/machbase/neo-pkg-bbox/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNextRetentionRunAt_IntervalSchedule(t *testing.T) {
	now := time.Date(2026, 4, 30, 5, 10, 0, 0, time.UTC)
	cfg := config.RetentionConfig{
		StartAtUTC:    "00:00",
		IntervalHours: 1,
	}

	next, err := nextRetentionRunAt(cfg, now)

	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 30, 6, 0, 0, 0, time.UTC), next)
}

func TestNextRetentionRunAt_StartAtPlusMultiDayInterval(t *testing.T) {
	now := time.Date(2026, 4, 30, 5, 10, 0, 0, time.UTC)
	cfg := config.RetentionConfig{
		StartAtUTC:    "00:00",
		IntervalHours: 48,
	}

	next, err := nextRetentionRunAt(cfg, now)

	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), next)
}

func TestNextRetentionRunAt_DailySchedule(t *testing.T) {
	now := time.Date(2026, 4, 30, 5, 10, 0, 0, time.UTC)
	cfg := config.RetentionConfig{StartAtUTC: "18:00"}

	next, err := nextRetentionRunAt(cfg, now)

	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 4, 30, 18, 0, 0, 0, time.UTC), next)
}
