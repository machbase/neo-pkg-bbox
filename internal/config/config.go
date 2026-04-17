package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	FFmpeg   FFmpegConfig   `yaml:"ffmpeg" json:"ffmpeg"`
	Server   ServerConfig   `yaml:"server" json:"server"`
	Machbase MachbaseConfig `yaml:"machbase" json:"machbase"`
	Mediamtx MediamtxConfig `yaml:"mediamtx" json:"mediamtx"`
	AI       AIConfig       `yaml:"ai" json:"ai"`
	Log      LogConfig      `yaml:"log" json:"log"`
}

type LogConfig struct {
	Dir    string        `yaml:"dir" json:"dir"`
	Level  string        `yaml:"level" json:"level"`
	Format string        `yaml:"format" json:"format"`
	Output string        `yaml:"output" json:"output"`
	File   LogFileConfig `yaml:"file" json:"file"`
}

type LogFileConfig struct {
	Filename   string `yaml:"filename" json:"filename"`
	MaxSize    int    `yaml:"max_size" json:"max_size"`
	MaxBackups int    `yaml:"max_backups" json:"max_backups"`
	MaxAge     int    `yaml:"max_age" json:"max_age"`
	Compress   bool   `yaml:"compress" json:"compress"`
}

func Load(path string) (*AppConfig, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	bdata, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	cfg := &AppConfig{}
	if err := unmarshalByExt(absPath, bdata, cfg); err != nil {
		return nil, err
	}

	applyDefaults(cfg)
	resolveRelativePaths(cfg, filepath.Dir(absPath))
	applyEnvOverrides(cfg)

	return cfg, nil
}

func applyEnvOverrides(cfg *AppConfig) {
	if v := os.Getenv("BB_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("BB_MACHBASE_HOST"); v != "" {
		cfg.Machbase.Host = v
	}
	if v := os.Getenv("BB_MACHBASE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.Machbase.Port = p
		}
	}
}

// resolveRelativePaths resolves relative path fields in the config
// relative to the directory of the config file itself.
// Absolute paths are left unchanged.
func resolveRelativePaths(cfg *AppConfig, base string) {
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}

	cfg.Server.CameraDir = resolve(cfg.Server.CameraDir)
	cfg.Server.MvsDir = resolve(cfg.Server.MvsDir)
	cfg.Server.DataDir = resolve(cfg.Server.DataDir)
	cfg.Server.BaseDir = resolve(cfg.Server.BaseDir)
	cfg.FFmpeg.Binary = resolve(cfg.FFmpeg.Binary)
	cfg.FFmpeg.Defaults.ProbeBinary = resolve(cfg.FFmpeg.Defaults.ProbeBinary)
	cfg.Mediamtx.Binary = resolve(cfg.Mediamtx.Binary)
	cfg.Mediamtx.ConfigFile = resolve(cfg.Mediamtx.ConfigFile)
	cfg.AI.Binary = resolve(cfg.AI.Binary)
	cfg.AI.ConfigFile = resolve(cfg.AI.ConfigFile)
	cfg.Log.Dir = resolve(cfg.Log.Dir)
}

func applyDefaults(cfg *AppConfig) {
	cfg.Mediamtx.ApplyDefaults()
}

func validate(cfg *AppConfig) error {
	return nil
}

// LoadRaw reads config file without applying defaults or resolving relative paths.
func LoadRaw(path string) (*AppConfig, error) {
	bdata, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &AppConfig{}
	if err := unmarshalByExt(path, bdata, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Save writes the config to the specified path.
// JSON or YAML is selected by file extension.
func Save(path string, cfg *AppConfig) error {
	var bdata []byte
	var err error
	if isJSON(path) {
		bdata, err = json.MarshalIndent(cfg, "", "  ")
	} else {
		bdata, err = yaml.Marshal(cfg)
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path, bdata, 0644)
}

func isJSON(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".json")
}

func unmarshalByExt(path string, data []byte, v any) error {
	if isJSON(path) {
		return json.Unmarshal(data, v)
	}
	return yaml.Unmarshal(data, v)
}
