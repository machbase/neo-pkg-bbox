package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetentionConfigKeepDuration(t *testing.T) {
	cfg := RetentionConfig{KeepHours: 6}

	duration, err := cfg.KeepDuration()

	require.NoError(t, err)
	assert.Equal(t, 6*time.Hour, duration)
}

func TestRetentionConfigKeepDuration_DefaultsToThirtyDaysInHours(t *testing.T) {
	cfg := RetentionConfig{}
	cfg.ApplyDefaults()

	duration, err := cfg.KeepDuration()

	require.NoError(t, err)
	assert.Equal(t, 30*24*time.Hour, duration)
}

func TestRetentionConfigIntervalDuration(t *testing.T) {
	cfg := RetentionConfig{IntervalHours: 2}

	duration, err := cfg.IntervalDuration()

	require.NoError(t, err)
	assert.Equal(t, 2*time.Hour, duration)
}

func TestRetentionConfigIntervalDuration_DefaultsToDaily(t *testing.T) {
	cfg := RetentionConfig{}

	duration, err := cfg.IntervalDuration()

	require.NoError(t, err)
	assert.Equal(t, 24*time.Hour, duration)
}

func TestRetentionConfigConsistencyCleanup_DefaultsToEnabled(t *testing.T) {
	cfg := RetentionConfig{}
	cfg.ApplyDefaults()

	assert.True(t, cfg.ConsistencyCleanupEnabled())
}

func TestRetentionConfigConsistencyCleanup_ExplicitFalse(t *testing.T) {
	disabled := false
	cfg := RetentionConfig{ConsistencyCleanup: &disabled}
	cfg.ApplyDefaults()

	assert.False(t, cfg.ConsistencyCleanupEnabled())
}
