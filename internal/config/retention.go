package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RetentionConfig controls scheduled cleanup of old blackbox data.
// StartAtUTC is a UTC clock value in HH:mm or HH:mm:ss form.
type RetentionConfig struct {
	Enabled            bool                   `yaml:"enabled" json:"enabled"`
	KeepHours          int                    `yaml:"keep_hours" json:"keep_hours"`
	StartAtUTC         string                 `yaml:"start_at_utc" json:"start_at_utc"`
	IntervalHours      int                    `yaml:"interval_hours" json:"interval_hours"`
	ConsistencyCleanup *bool                  `yaml:"consistency_cleanup" json:"consistency_cleanup"`
	Targets            RetentionTargetsConfig `yaml:"targets" json:"targets"`
}

type RetentionTargetsConfig struct {
	Database bool `yaml:"database" json:"database"`
	Files    bool `yaml:"files" json:"files"`
}

func (c *RetentionConfig) ApplyDefaults() {
	if c.KeepHours <= 0 {
		c.KeepHours = 30 * 24
	}
	if strings.TrimSpace(c.StartAtUTC) == "" {
		c.StartAtUTC = "18:00"
	}
	if c.ConsistencyCleanup == nil {
		enabled := true
		c.ConsistencyCleanup = &enabled
	}
	if !c.Targets.Database && !c.Targets.Files {
		c.Targets.Database = true
		c.Targets.Files = true
	}
}

func (c RetentionConfig) KeepDuration() (time.Duration, error) {
	if c.KeepHours <= 0 {
		return 0, fmt.Errorf("keep_hours must be greater than zero")
	}
	return time.Duration(c.KeepHours) * time.Hour, nil
}

func (c RetentionConfig) IntervalDuration() (time.Duration, error) {
	if c.IntervalHours < 0 {
		return 0, fmt.Errorf("interval_hours must be greater than or equal to zero")
	}
	if c.IntervalHours == 0 {
		return 24 * time.Hour, nil
	}
	return time.Duration(c.IntervalHours) * time.Hour, nil
}

func (c RetentionConfig) ConsistencyCleanupEnabled() bool {
	return c.ConsistencyCleanup == nil || *c.ConsistencyCleanup
}

func (c RetentionConfig) StartAtDuration() (time.Duration, error) {
	parts := strings.Split(strings.TrimSpace(c.StartAtUTC), ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, fmt.Errorf("start_at_utc must be HH:mm or HH:mm:ss")
	}

	hour, err := parseClockPart(parts[0], "hour")
	if err != nil {
		return 0, err
	}
	minute, err := parseClockPart(parts[1], "minute")
	if err != nil {
		return 0, err
	}
	second := 0
	if len(parts) == 3 {
		second, err = parseClockPart(parts[2], "second")
		if err != nil {
			return 0, err
		}
	}

	if hour < 0 || hour > 23 {
		return 0, fmt.Errorf("start_at_utc hour out of range")
	}
	if minute < 0 || minute > 59 {
		return 0, fmt.Errorf("start_at_utc minute out of range")
	}
	if second < 0 || second > 59 {
		return 0, fmt.Errorf("start_at_utc second out of range")
	}
	return time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute + time.Duration(second)*time.Second, nil
}

func parseClockPart(raw string, label string) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid start_at_utc %s: %w", label, err)
	}
	return v, nil
}
