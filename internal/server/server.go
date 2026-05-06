package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/machbase/neo-pkg-bbox/internal/config"
	"github.com/machbase/neo-pkg-bbox/internal/db"
	"github.com/machbase/neo-pkg-bbox/internal/ffmpeg"
	"github.com/machbase/neo-pkg-bbox/internal/logger"

	"github.com/gin-gonic/gin"
)

// Server represents the HTTP server.
type Server struct {
	cfg     config.ServerConfig
	engine  *gin.Engine
	http    *http.Server
	handler *Handler
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	s.engine.ServeHTTP(w, req)
}

// New creates a new Server.
func New(cfg config.ServerConfig, mediamtxCfg config.MediamtxConfig, eventCfg config.EventConfig, logDir string, machbase *db.Machbase, watcher Watcher, ffRunner *ffmpeg.FFmpegRunner, ffmpegBinary string, configPath string, serveWeb bool) (*Server, error) {
	cfg.ApplyDefaults()
	eventCfg.ApplyDefaults()

	if cfg.BaseDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cfg.BaseDir = filepath.Dir(exe)
	}

	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(httpLogger())
	engine.Use(cors())

	s := &Server{
		cfg:     cfg,
		engine:  engine,
		handler: NewHandler(machbase, watcher, ffRunner, cfg.DataDir, logDir, cfg.MvsDir, cfg.CameraDir, ffmpegBinary, configPath, eventCfg, mediamtxCfg.Host, mediamtxCfg.WebRTCHost, mediamtxCfg.Port, mediamtxCfg.WebRTCPort, mediamtxCfg.RtspServerPort),
	}
	s.routes(serveWeb)

	s.http = &http.Server{
		Addr:         cfg.Addr,
		Handler:      engine,
		ReadTimeout:  cfg.ReadTimeout(),
		WriteTimeout: cfg.WriteTimeout(),
	}

	return s, nil
}

func (s *Server) routes(serveWeb bool) {
	api := s.engine.Group("/api")

	api.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	// ==================================================================
	// App Config (server, machbase, ffmpeg.binary)
	api.GET("/config", s.handler.GetAppConfig)
	api.POST("/config", s.handler.PostAppConfig)
	api.GET("/retention/status", s.handler.GetRetentionStatus)
	api.POST("/retention/run", s.handler.PostRetentionRun)

	// ==================================================================
	// 목록
	api.GET("/tables", s.handler.GetTables)
	api.POST("/table", s.handler.CreateTable) // Create TAG table
	api.GET("/models", s.handler.GetModels)
	api.GET("/detect_objects", s.handler.GetDetectObjects)
	api.GET("/cameras", s.handler.GetCameras)

	// ==================================================================
	// Blackbox
	api.GET("/get_time_range", s.handler.GetTimeRange)
	api.GET("/get_chunk_info", s.handler.GetChunkInfo)
	api.GET("/v_get_chunk", s.handler.GetChunk)
	api.GET("/get_camera_rollup_info", s.handler.GetCameraRollup)
	api.GET("/data_gaps", s.handler.GetDataGaps)

	// Sensor
	api.GET("/sensors", s.handler.GetSensors)
	api.GET("/sensor_data", s.handler.GetSensorData)
	// ==================================================================
	// Camera Management
	api.POST("/camera", s.handler.CreateCamera)       // O
	api.GET("/camera/:id", s.handler.GetCamera)       // O
	api.POST("/camera/:id", s.handler.UpdateCamera)   // O
	api.DELETE("/camera/:id", s.handler.DeleteCamera) // O
	api.POST("/camera/:id/test", s.handler.TestCameraConnection)

	// Camera Detect Objects
	api.GET("/camera/:id/detect_objects", s.handler.GetDetectObjectsByCamera)
	api.POST("/camera/:id/detect_objects", s.handler.UpdateDetectObjectsByCamera)

	// Camera Control
	api.POST("/camera/:id/enable", s.handler.EnableCamera)   // O
	api.POST("/camera/:id/disable", s.handler.DisableCamera) // O

	// Camera Status Monitoring
	api.GET("/camera/:id/status", s.handler.GetCameraStatus) // O
	api.GET("/cameras/health", s.handler.GetCamerasHealth)   // O

	// Media Server (MediaMTX)
	api.GET("/media/heartbeat", s.handler.HeartbeatMediaMTX) // MediaMTX heartbeat

	// Camera Events Query
	api.GET("/camera_events", s.handler.GetCameraEvents)
	api.GET("/camera_events/count", s.handler.GetCameraEventCount)

	// ==================================================================
	// Event Rule
	api.GET("/event_rule/:camera_id", s.handler.GetEventRules)
	api.POST("/event_rule", s.handler.PostEventRules)
	api.POST("/event_rule/:camera_id/:rule_id", s.handler.UpdateEventRules)
	api.DELETE("/event_rule/:camera_id/:rule_id", s.handler.DeleteEventRules)

	// ==================================================================
	// Network utilities
	api.POST("/cameras/ping", s.handler.PingIP)
	api.GET("/camera/ping", s.handler.PingIPQuery)

	// ==================================================================
	// AI
	api.POST("/ai/result", s.handler.UploadAIResult)

	// ==================================================================
	// MVS (Machine Vision System)
	api.POST("/mvs/camera", s.handler.CreateMvsCamera)

	// ==================================================================
	// Machbase proxy — machbase-neo 로 중계
	s.engine.POST("/db/tql", s.handler.ProxyMachbase)

	s.engine.GET("/web/*path", s.handler.ProxyMachbaseWeb)
	s.engine.POST("/web/*path", s.handler.ProxyMachbaseWeb)

	// Static frontend UI (-web 플래그를 줬을 때만 활성화)
	if serveWeb {
		webDir := filepath.Join(s.cfg.BaseDir, "web")
		s.engine.GET("/", func(c *gin.Context) {
			c.File(filepath.Join(webDir, "index.html"))
		})
		s.engine.StaticFS("/ui", http.Dir(webDir))
		logger.GetLogger().Infof("[server] web UI enabled (dir: %s)", webDir)
	}
}

// Run starts the server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}

	// 서버 시작 시 MediaMTX path 등록 후 카메라 프로세스 복원
	go s.handler.startupCamerasAsync(ctx)
	// 테이블 삭제 감지 → 고아 카메라 설정파일 자동 제거 (60초 주기)
	go s.handler.startOrphanWatcher(ctx)
	go s.handler.startRetentionScheduler(ctx)

	errCh := make(chan error, 1)
	go func() {
		logger.GetLogger().Infof("listening on http://%s", s.cfg.Addr)
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		s.handler.Shutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout())
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "Content-Type, X-Machbase-Api-Token, Authorization")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusOK)
			return
		}
		c.Next()
	}
}

// httpLogger returns a Gin middleware that logs HTTP requests to a separate file
func httpLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Start timer
		start := c.Value("start")
		if start == nil {
			c.Set("start", c.Request.Context().Value("start"))
		}

		// Process request
		c.Next()

		// Log request details
		httpLog := logger.GetHTTPLogger()
		httpLog.WithFields(map[string]interface{}{
			"method":     c.Request.Method,
			"path":       c.Request.URL.Path,
			"query":      c.Request.URL.RawQuery,
			"status":     c.Writer.Status(),
			"client_ip":  c.ClientIP(),
			"user_agent": c.Request.UserAgent(),
		}).Info("HTTP Request")
	}
}

type Response struct {
	Success bool   `json:"success"`
	Reason  string `json:"reason"`
	Elapse  string `json:"elapse"`
	Data    any    `json:"data"`
}
