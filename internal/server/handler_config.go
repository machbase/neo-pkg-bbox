package server

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/machbase/neo-pkg-blackbox/internal/config"

	"github.com/gin-gonic/gin"
)

// AppConfigDTO는 GET/POST /api/config 에서 사용하는 설정 구조체.
// server.addr 과 ai 는 읽기 전용 (POST 시 무시됨).
type AppConfigDTO struct {
	Server   ServerConfigAPI   `json:"server"`
	Machbase MachbaseConfigAPI `json:"machbase"`
	Ffmpeg   FfmpegConfigAPI   `json:"ffmpeg"`
	Mediamtx MediamtxConfigAPI `json:"mediamtx"`
	Log      LogConfigAPI      `json:"log"`
	AI       AIConfigAPI       `json:"ai"` // 읽기 전용 (POST 시 무시)
}

type ServerConfigAPI struct {
	Addr      string `json:"addr"` // 읽기 전용 (POST 시 무시)
	CameraDir string `json:"camera_dir"`
	MvsDir    string `json:"mvs_dir"`
	DataDir   string `json:"data_dir"`
}

type MachbaseConfigAPI struct {
	Scheme         string `json:"scheme"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Token          string `json:"token"`
	User           string `json:"user"`
	Password       string `json:"password"`
}

type FfmpegConfigAPI struct {
	Binary   string            `json:"binary"`
	Defaults FfmpegDefaultsAPI `json:"defaults"`
}

type FfmpegDefaultsAPI struct {
	ProbeBinary string         `json:"probe_binary"`
	ProbeArgs   []config.ArgKV `json:"probe_args"`
}

type MediamtxConfigAPI struct {
	Binary     string `json:"binary"`
	ConfigFile string `json:"config_file"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
}

type LogConfigAPI struct {
	Dir    string           `json:"dir"`
	Level  string           `json:"level"`
	Format string           `json:"format"`
	Output string           `json:"output"`
	File   LogFileConfigAPI `json:"file"`
}

type LogFileConfigAPI struct {
	Filename   string `json:"filename"`
	MaxSize    int    `json:"max_size"`
	MaxBackups int    `json:"max_backups"`
	MaxAge     int    `json:"max_age"`
	Compress   bool   `json:"compress"`
}

type AIConfigAPI struct {
	Binary     string `json:"binary"`
	ConfigFile string `json:"config_file"`
}

// GetAppConfig godoc
// GET /api/config
// config.yaml 의 모든 필드를 반환한다.
func (h *Handler) GetAppConfig(c *gin.Context) {
	tick := time.Now()

	cfg, err := config.LoadRaw(h.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			successResponse(c, tick, AppConfigDTO{})
			return
		}
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read config: "+err.Error())
		return
	}

	successResponse(c, tick, cfgToDTO(cfg))
}

// PostAppConfig godoc
// POST /api/config
// config.yaml 을 갱신한다. server.addr 과 ai 는 수정 불가 (기존 값 유지).
func (h *Handler) PostAppConfig(c *gin.Context) {
	tick := time.Now()

	var req AppConfigDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}

	// 기존 config 에서 읽기 전용 필드(server.addr, ai) 보존
	var preservedAddr string
	var preservedAI config.AIConfig

	existingCfg, err := config.LoadRaw(h.configPath)
	if err != nil && !os.IsNotExist(err) {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read config: "+err.Error())
		return
	}
	if existingCfg != nil {
		preservedAddr = existingCfg.Server.Addr
		preservedAI = existingCfg.AI
	}

	cfg := dtoToCfg(&req)
	cfg.Server.Addr = preservedAddr
	cfg.AI = preservedAI

	if err := os.MkdirAll(filepath.Dir(h.configPath), 0755); err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to create config dir: "+err.Error())
		return
	}

	if err := config.Save(h.configPath, &cfg); err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to save config: "+err.Error())
		return
	}

	successResponse(c, tick, nil)
}

func cfgToDTO(cfg *config.AppConfig) AppConfigDTO {
	return AppConfigDTO{
		Server: ServerConfigAPI{
			Addr:      cfg.Server.Addr,
			CameraDir: cfg.Server.CameraDir,
			MvsDir:    cfg.Server.MvsDir,
			DataDir:   cfg.Server.DataDir,
		},
		Machbase: MachbaseConfigAPI{
			Scheme:         cfg.Machbase.Scheme,
			Host:           cfg.Machbase.Host,
			Port:           cfg.Machbase.Port,
			TimeoutSeconds: cfg.Machbase.TimeoutSeconds,
			Token:          cfg.Machbase.APIToken,
			User:           cfg.Machbase.User,
			Password:       cfg.Machbase.Password,
		},
		Ffmpeg: FfmpegConfigAPI{
			Binary: cfg.FFmpeg.Binary,
			Defaults: FfmpegDefaultsAPI{
				ProbeBinary: cfg.FFmpeg.Defaults.ProbeBinary,
				ProbeArgs:   cfg.FFmpeg.Defaults.ProbeArgs,
			},
		},
		Mediamtx: MediamtxConfigAPI{
			Binary:     cfg.Mediamtx.Binary,
			ConfigFile: cfg.Mediamtx.ConfigFile,
			Host:       cfg.Mediamtx.Host,
			Port:       cfg.Mediamtx.Port,
		},
		Log: LogConfigAPI{
			Dir:    cfg.Log.Dir,
			Level:  cfg.Log.Level,
			Format: cfg.Log.Format,
			Output: cfg.Log.Output,
			File: LogFileConfigAPI{
				Filename:   cfg.Log.File.Filename,
				MaxSize:    cfg.Log.File.MaxSize,
				MaxBackups: cfg.Log.File.MaxBackups,
				MaxAge:     cfg.Log.File.MaxAge,
				Compress:   cfg.Log.File.Compress,
			},
		},
		AI: AIConfigAPI{
			Binary:     cfg.AI.Binary,
			ConfigFile: cfg.AI.ConfigFile,
		},
	}
}

func dtoToCfg(req *AppConfigDTO) config.AppConfig {
	var probeArgs []config.ArgKV
	if len(req.Ffmpeg.Defaults.ProbeArgs) > 0 {
		probeArgs = req.Ffmpeg.Defaults.ProbeArgs
	}
	return config.AppConfig{
		Server: config.ServerConfig{
			// Addr 는 호출자에서 보존값으로 덮어씀
			CameraDir: req.Server.CameraDir,
			MvsDir:    req.Server.MvsDir,
			DataDir:   req.Server.DataDir,
		},
		Machbase: config.MachbaseConfig{
			Scheme:         req.Machbase.Scheme,
			Host:           req.Machbase.Host,
			Port:           req.Machbase.Port,
			TimeoutSeconds: req.Machbase.TimeoutSeconds,
			APIToken:       req.Machbase.Token,
			User:           req.Machbase.User,
			Password:       req.Machbase.Password,
		},
		FFmpeg: config.FFmpegConfig{
			Binary: req.Ffmpeg.Binary,
			Defaults: config.FFmpegDefaults{
				ProbeBinary: req.Ffmpeg.Defaults.ProbeBinary,
				ProbeArgs:   probeArgs,
			},
		},
		Mediamtx: config.MediamtxConfig{
			Binary:     req.Mediamtx.Binary,
			ConfigFile: req.Mediamtx.ConfigFile,
			Host:       req.Mediamtx.Host,
			Port:       req.Mediamtx.Port,
		},
		Log: config.LogConfig{
			Dir:    req.Log.Dir,
			Level:  req.Log.Level,
			Format: req.Log.Format,
			Output: req.Log.Output,
			File: config.LogFileConfig{
				Filename:   req.Log.File.Filename,
				MaxSize:    req.Log.File.MaxSize,
				MaxBackups: req.Log.File.MaxBackups,
				MaxAge:     req.Log.File.MaxAge,
				Compress:   req.Log.File.Compress,
			},
		},
		// AI 는 호출자에서 보존값으로 덮어씀
	}
}
