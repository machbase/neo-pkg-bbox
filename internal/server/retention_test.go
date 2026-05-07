package server

import (
	"context"
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

func TestRetentionConfigChanged_NormalizesDefaults(t *testing.T) {
	enabled := true
	oldCfg := config.RetentionConfig{}
	newCfg := config.RetentionConfig{
		KeepHours:          30 * 24,
		StartAtUTC:         "18:00",
		IntervalHours:      0,
		ConsistencyCleanup: &enabled,
		Targets: config.RetentionTargetsConfig{
			Database: true,
			Files:    true,
		},
	}

	assert.False(t, retentionConfigChanged(oldCfg, newCfg))
}

func TestRetentionConfigChanged_DetectsScheduleChange(t *testing.T) {
	oldCfg := config.RetentionConfig{StartAtUTC: "00:00"}
	newCfg := config.RetentionConfig{StartAtUTC: "01:00"}

	assert.True(t, retentionConfigChanged(oldCfg, newCfg))
}

func TestNotifyRetentionScheduleResetCoalesces(t *testing.T) {
	h := &Handler{retentionScheduleReset: make(chan struct{}, 1)}

	h.notifyRetentionScheduleReset()
	h.notifyRetentionScheduleReset()

	assert.Len(t, h.retentionScheduleReset, 1)
}

func TestWaitRetentionOrResetReturnsOnReset(t *testing.T) {
	h := &Handler{retentionScheduleReset: make(chan struct{}, 1)}
	h.notifyRetentionScheduleReset()

	timerFired, ok := h.waitRetentionOrReset(context.Background(), time.Hour)

	assert.True(t, ok)
	assert.False(t, timerFired)
}
