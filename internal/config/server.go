package config

import (
	"strings"
	"time"
)

type ServerConfig struct {
	Addr                   string `yaml:"addr" json:"addr"` // e.g. "0.0.0.0:8000"
	MvsDir                 string `yaml:"mvs_dir" json:"mvs_dir"`
	CameraDir              string `yaml:"camera_dir" json:"camera_dir"`
	BaseDir                string `yaml:"base_dir" json:"base_dir"`                                 // static root
	DataDir                string `yaml:"data_dir" json:"data_dir"`                                 // segment root (default: /data)
	ReadTimeoutSeconds     int    `yaml:"read_timeout_seconds" json:"read_timeout_seconds"`         // optional
	WriteTimeoutSeconds    int    `yaml:"write_timeout_seconds" json:"write_timeout_seconds"`       // optional
	ShutdownTimeoutSeconds int    `yaml:"shutdown_timeout_seconds" json:"shutdown_timeout_seconds"` // graceful shutdown timeout
}

func (c *ServerConfig) ApplyDefaults() {
	if c.Addr == "" {
		c.Addr = "0.0.0.0:8000"
	}
	if c.DataDir == "" {
		c.DataDir = "/data"
	}
	if c.ShutdownTimeoutSeconds == 0 {
		c.ShutdownTimeoutSeconds = 10
	}
}

func (c ServerConfig) ShutdownTimeout() time.Duration {
	if c.ShutdownTimeoutSeconds <= 0 {
		return 10 * time.Second
	}
	return time.Duration(c.ShutdownTimeoutSeconds) * time.Second
}

func (c ServerConfig) ReadTimeout() time.Duration {
	if c.ReadTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(c.ReadTimeoutSeconds) * time.Second
}

func (c ServerConfig) WriteTimeout() time.Duration {
	if c.WriteTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(c.WriteTimeoutSeconds) * time.Second
}

func parseBoolLoose(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
