package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEventConfigApplyDefaults(t *testing.T) {
	cfg := EventConfig{}
	cfg.ApplyDefaults()

	assert.True(t, cfg.RequireVideoEnabled())
	assert.Equal(t, 120*time.Second, cfg.VideoWaitDuration())
	assert.Equal(t, time.Second, cfg.VideoRetryInitialDuration())
	assert.Equal(t, 5*time.Second, cfg.VideoRetryMaxDuration())
}

func TestEventConfigApplyDefaults_MaxRetryAtLeastInitial(t *testing.T) {
	cfg := EventConfig{
		VideoRetryInitialSeconds: 5,
		VideoRetryMaxSeconds:     1,
	}
	cfg.ApplyDefaults()

	assert.Equal(t, 5*time.Second, cfg.VideoRetryInitialDuration())
	assert.Equal(t, 5*time.Second, cfg.VideoRetryMaxDuration())
}
