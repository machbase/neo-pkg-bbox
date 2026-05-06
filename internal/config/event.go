package config

import "time"

type EventConfig struct {
	RequireVideo             *bool `yaml:"require_video" json:"require_video"`
	VideoWaitSeconds         int   `yaml:"video_wait_seconds" json:"video_wait_seconds"`
	VideoRetryInitialSeconds int   `yaml:"video_retry_initial_seconds" json:"video_retry_initial_seconds"`
	VideoRetryMaxSeconds     int   `yaml:"video_retry_max_seconds" json:"video_retry_max_seconds"`
}

func (c *EventConfig) ApplyDefaults() {
	if c.RequireVideo == nil {
		enabled := true
		c.RequireVideo = &enabled
	}
	if c.VideoWaitSeconds <= 0 {
		c.VideoWaitSeconds = 120
	}
	if c.VideoRetryInitialSeconds <= 0 {
		c.VideoRetryInitialSeconds = 1
	}
	if c.VideoRetryMaxSeconds <= 0 {
		c.VideoRetryMaxSeconds = 5
	}
	if c.VideoRetryMaxSeconds < c.VideoRetryInitialSeconds {
		c.VideoRetryMaxSeconds = c.VideoRetryInitialSeconds
	}
}

func (c EventConfig) RequireVideoEnabled() bool {
	return c.RequireVideo == nil || *c.RequireVideo
}

func (c EventConfig) VideoWaitDuration() time.Duration {
	if c.VideoWaitSeconds <= 0 {
		return 120 * time.Second
	}
	return time.Duration(c.VideoWaitSeconds) * time.Second
}

func (c EventConfig) VideoRetryInitialDuration() time.Duration {
	if c.VideoRetryInitialSeconds <= 0 {
		return time.Second
	}
	return time.Duration(c.VideoRetryInitialSeconds) * time.Second
}

func (c EventConfig) VideoRetryMaxDuration() time.Duration {
	if c.VideoRetryMaxSeconds <= 0 {
		return 5 * time.Second
	}
	return time.Duration(c.VideoRetryMaxSeconds) * time.Second
}
