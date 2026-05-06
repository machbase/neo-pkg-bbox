package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	FFmpeg    FFmpegConfig    `yaml:"ffmpeg" json:"ffmpeg"`
	Server    ServerConfig    `yaml:"server" json:"server"`
	Machbase  MachbaseConfig  `yaml:"machbase" json:"machbase"`
	Mediamtx  MediamtxConfig  `yaml:"mediamtx" json:"mediamtx"`
	AI        AIConfig        `yaml:"ai" json:"ai"`
	Log       LogConfig       `yaml:"log" json:"log"`
	Retention RetentionConfig `yaml:"retention" json:"retention"`
	Event     EventConfig     `yaml:"event" json:"event"`
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
	resolveExecutableExt(cfg)
	applyEnvOverrides(cfg)

	return cfg, nil
}

// resolveExecutableExt는 Windows 에서 실행 파일 경로에 .exe 가 없고,
// 해당 경로 자체(또는 .gz 압축본)가 존재하지 않으며 .exe 버전이 존재할 때
// 경로를 .exe 로 보정한다. Windows 릴리스 패키지는 tools/ffmpeg.exe 처럼
// 접미사가 포함된 이름으로 배포되지만 config.yaml 은 단일 경로를 사용하므로
// 로드 시점에 한 번 맞춰준다.
func resolveExecutableExt(cfg *AppConfig) {
	if runtime.GOOS != "windows" {
		return
	}
	cfg.FFmpeg.Binary = preferExe(cfg.FFmpeg.Binary)
	cfg.FFmpeg.Defaults.ProbeBinary = preferExe(cfg.FFmpeg.Defaults.ProbeBinary)
	cfg.Mediamtx.Binary = preferExe(cfg.Mediamtx.Binary)
	cfg.AI.Binary = preferExe(cfg.AI.Binary)
}

func preferExe(p string) string {
	if p == "" || strings.HasSuffix(strings.ToLower(p), ".exe") {
		return p
	}
	// path 또는 path.gz 가 이미 있으면 그대로 둔다 (EnsureUnpacked 대상).
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if _, err := os.Stat(p + ".gz"); err == nil {
		return p
	}
	exe := p + ".exe"
	if _, err := os.Stat(exe); err == nil {
		return exe
	}
	if _, err := os.Stat(exe + ".gz"); err == nil {
		return exe
	}
	return p
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
	cfg.Retention.ApplyDefaults()
	cfg.Event.ApplyDefaults()
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
