package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/machbase/neo-pkg-bbox/internal/db"
	"github.com/machbase/neo-pkg-bbox/internal/dsl"
	"github.com/machbase/neo-pkg-bbox/internal/logger"
	"github.com/machbase/neo-pkg-bbox/internal/mediamtx"
	"github.com/machbase/neo-pkg-bbox/internal/watcher"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// validIdentifier matches SQL identifiers: starts with letter or underscore, followed by letters/digits/underscores.
var validIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type EventRule struct {
	ID         string `json:"rule_id" binding:"required"`
	Name       string `json:"name" binding:"required"`
	Expression string `json:"expression_text" binding:"required"`
	RecordMode string `json:"record_mode" binding:"required"`
	Enabled    bool   `json:"enabled"`
}

type MvsCameraCreateRequest struct {
	CameraID      string   `json:"camera_id"`                         // cam{id}_{model_id}_{time} (자동 생성 가능)
	CameraURL     string   `json:"camera_url" binding:"required"`     // rtsp URL
	ModelID       int      `json:"model_id"`                          // 기본 모델 0
	DetectObjects []string `json:"detect_objects" binding:"required"` // ex) ["person", "car", "truck", "bus"]
}

// 이 구조체는 테이블이 아닌 파일로 저장이됨, JSON형식
type CameraCreateRequest struct {
	Table string `json:"table" binding:"required"`
	Name  string `json:"name" binding:"required"`
	Desc  string `json:"desc"`

	Enabled         *bool  `json:"enabled,omitempty"` // nil = 구 설정파일 호환 (true로 간주), false = 비활성
	RtspURL         string `json:"rtsp_url"`          // RTSP 소스 URL (예: rtsp://user:pass@host/stream1)
	RtspPath        string `json:"rtsp_path"`         // MediaMTX path 이름 (빈 값이면 카메라 이름으로 자동 설정)
	WebRTCURL       string `json:"webrtc_url"`        // WebRTC URL (시스템 자동 생성, API 입력 무시)
	MediamtxRtspURL string `json:"mediamtx_rtsp_url"` // MediaMTX 프록시 RTSP URL (시스템 자동 생성, ffmpeg 입력용)
	MediaURL        string `json:"media_url"`         // 미디어 서버 URL

	ModelID       int      `json:"model_id"`
	DetectObjects []string `json:"detect_objects"` // ex) ["person", "car", "truck", "bus"]
	SaveObjects   bool     `json:"save_objects"`   // {camera}_log 테이블에 데이터 저장 여부

	FFmpegCommand string `json:"ffmpeg_command"` // ffmpeg 실행 경로
	OutputDir     string `json:"output_dir"`     // ffmpeg 청크 출력 디렉토리
	ArchiveDir    string `json:"archive_dir"`    // watcher가 파일을 이동시키는 디렉토리

	FFmpegOptions []ReqKV `json:"ffmpeg_options"` // 프론트에 전달 필요

	ServerURL string `json:"server_url,omitempty"` // WebRTC host 오버라이드 (WSL 등 환경에서 외부 접근 가능한 IP 지정)

	EventRule []EventRule `json:"event_rule"` // request에서는 안 받지만, 별도로 eventRule을 받는 API가 있고 CameraCreateRequest의 구조체는 파일에 json으로 저장됨
}

type CameraUpdateRequest struct {
	Desc string `json:"desc"`

	RtspURL  string `json:"rtsp_url"`
	RtspPath string `json:"rtsp_path"` // 빈 값이면 기존 rtsp_path 유지
	MediaURL string `json:"media_url"`

	ModelID       int      `json:"model_id"`
	DetectObjects []string `json:"detect_objects"`
	SaveObjects   bool     `json:"save_objects"`

	FFmpegCommand string `json:"ffmpeg_command"`
	OutputDir     string `json:"output_dir"`
	ArchiveDir    string `json:"archive_dir"`

	FFmpegOptions []ReqKV `json:"ffmpeg_options"`

	ServerURL string `json:"server_url,omitempty"` // WebRTC host 오버라이드

	// nil이면 기존 event_rule 유지, 빈 배열([])이면 모든 룰 삭제
	EventRule *[]EventRule `json:"event_rule"`
}

type ReqKV struct {
	K string  `json:"k" binding:"required"`
	V *string `json:"v"`
}

func boolPtr(b bool) *bool { return &b }

// isEnabled는 카메라 활성 여부를 반환한다.
// Enabled가 nil인 경우(구 설정파일)는 true로 간주한다.
func (c *CameraCreateRequest) isEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// CreateCamera handles POST /api/camera.
// 1. Saves camera config as JSON file in cameraDir
// 2. Creates 3 tables: {name}, {name}_event, {name}_log
// 3. Saves MVS config file in mvsDir (for detection)
func (h *Handler) CreateCamera(c *gin.Context) {
	tick := time.Now()

	var req CameraCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.GetLogger().Errorf("CreateCamera: failed to bind JSON: %v", err)
		errorResponse(c, tick, http.StatusBadRequest, "bad request parameter")
		return
	}

	// Validate name (used as table name)
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		logger.GetLogger().Errorf("CreateCamera: camera name is required")
		errorResponse(c, tick, http.StatusBadRequest, "name is required")
		return
	}
	// 파일명에 문제가 되는 특수문자 검증
	if strings.ContainsAny(req.Name, " '\"`") {
		logger.GetLogger().Errorf("CreateCamera: invalid camera name %q (contains space or quotes)", req.Name)
		errorResponse(c, tick, http.StatusBadRequest, "camera name cannot contain spaces or quotes")
		return
	}

	// Validate table name: Machbase SQL identifiers allow only letters, digits, underscores
	req.Table = strings.TrimSpace(req.Table)
	if !validIdentifier.MatchString(req.Table) {
		logger.GetLogger().Errorf("CreateCamera: invalid table name %q", req.Table)
		errorResponse(c, tick, http.StatusBadRequest, "table must start with a letter or underscore and contain only letters, digits, or underscores")
		return
	}

	// 카메라 이름 중복 검사: 동일한 이름의 설정 파일이 이미 존재하면 거부
	existingPath := filepath.Join(h.cameraDir, req.Name+".json")
	if _, err := os.Stat(existingPath); err == nil {
		logger.GetLogger().Errorf("CreateCamera: camera name %q already exists", req.Name)
		errorResponse(c, tick, http.StatusConflict, fmt.Sprintf("camera name %q already exists", req.Name))
		return
	}

	// 1. Save camera config as JSON file
	if err := os.MkdirAll(h.cameraDir, 0755); err != nil {
		logger.GetLogger().Errorf("CreateCamera[%s]: failed to create camera directory: %v", req.Name, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to create camera directory")
		return
	}

	// Resolve paths: 빈 값이면 기본 경로 사용
	if req.OutputDir == "" {
		req.OutputDir = filepath.Join(h.dataDir, req.Name, "in")
	} else if !filepath.IsAbs(req.OutputDir) {
		req.OutputDir = filepath.Join(h.dataDir, req.OutputDir)
	}
	if req.ArchiveDir == "" {
		req.ArchiveDir = filepath.Join(h.dataDir, req.Name, "out")
	} else if !filepath.IsAbs(req.ArchiveDir) {
		req.ArchiveDir = filepath.Join(h.dataDir, req.ArchiveDir)
	}

	req.RtspURL = strings.TrimSpace(req.RtspURL)
	req.RtspPath = strings.TrimSpace(req.RtspPath)

	// rtsp_path 기본값: rtsp_url이 있고 rtsp_path가 비어있으면 랜덤 hex ID로 자동 설정
	if req.RtspURL != "" && req.RtspPath == "" {
		req.RtspPath = generateRtspPath()
	}
	// rtsp_path 중복 검사
	if req.RtspPath != "" && h.rtspPathInUse("", req.RtspPath) {
		logger.GetLogger().Errorf("CreateCamera[%s]: rtsp_path %q is already in use", req.Name, req.RtspPath)
		errorResponse(c, tick, http.StatusConflict, fmt.Sprintf("rtsp_path %q is already in use by another camera", req.RtspPath))
		return
	}
	// webrtc_url, mediamtx_rtsp_url 자동 생성 (API 입력값 무시, 시스템이 채움)
	req.WebRTCURL = h.buildWebRTCURL(req.RtspPath, req.ServerURL)
	req.MediamtxRtspURL = h.buildMediamtxRtspURL(req.RtspPath)
	// 신규 카메라는 항상 enabled = true
	req.Enabled = boolPtr(true)

	// Create camera data directories
	if err := os.MkdirAll(req.OutputDir, 0755); err != nil {
		logger.GetLogger().Errorf("CreateCamera[%s]: failed to create output directory %q: %v", req.Name, req.OutputDir, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to create output directory")
		return
	}
	if err := os.MkdirAll(req.ArchiveDir, 0755); err != nil {
		logger.GetLogger().Errorf("CreateCamera[%s]: failed to create archive directory %q: %v", req.Name, req.ArchiveDir, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to create archive directory")
		return
	}

	cameraJSON, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		logger.GetLogger().Errorf("CreateCamera[%s]: failed to marshal camera config: %v", req.Name, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to marshal camera config")
		return
	}

	cameraPath := filepath.Join(h.cameraDir, req.Name+".json")
	if err := os.WriteFile(cameraPath, cameraJSON, 0644); err != nil {
		logger.GetLogger().Errorf("CreateCamera[%s]: failed to write camera config file %q: %v", req.Name, cameraPath, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to write camera config file")
		return
	}

	// 2. Create tables conditionally
	// 2.1. Create {table}_log if save_objects is true
	if req.SaveObjects {
		if err := h.machbase.CreateCameraLogTable(c.Request.Context(), req.Table); err != nil {
			// Rollback: delete the config file
			_ = os.Remove(cameraPath)
			logger.GetLogger().Errorf("CreateCamera[%s]: failed to create log table for %q: %v", req.Name, req.Table, err)
			errorResponse(c, tick, http.StatusInternalServerError, fmt.Sprintf("failed to create log table: %v", err))
			return
		}
		logger.GetLogger().Infof("CreateCamera[%s]: created log table %s_log", req.Name, req.Table)
	}

	// 2.2. Create {table}_event if any event_rule is enabled
	hasEnabledRule := false
	for _, rule := range req.EventRule {
		if rule.Enabled {
			hasEnabledRule = true
			break
		}
	}
	if hasEnabledRule {
		if err := h.machbase.CreateCameraEventTable(c.Request.Context(), req.Table); err != nil {
			// Rollback: delete the config file
			_ = os.Remove(cameraPath)
			logger.GetLogger().Errorf("CreateCamera[%s]: failed to create event table for %q: %v", req.Name, req.Table, err)
			errorResponse(c, tick, http.StatusInternalServerError, fmt.Sprintf("failed to create event table: %v", err))
			return
		}
		logger.GetLogger().Infof("CreateCamera[%s]: created event table %s_event", req.Name, req.Table)
	}

	// 2.3. Create main {table} (always)
	if err := h.machbase.CreateTable(c.Request.Context(), req.Table); err != nil {
		// Rollback: delete the config file
		_ = os.Remove(cameraPath)
		logger.GetLogger().Errorf("CreateCamera[%s]: failed to create main table %q: %v", req.Name, req.Table, err)
		errorResponse(c, tick, http.StatusInternalServerError, fmt.Sprintf("failed to create main table: %v", err))
		return
	}
	logger.GetLogger().Infof("CreateCamera[%s]: created main table %s", req.Name, req.Table)

	// 3. Save MVS config file (for detection program)
	if err := os.MkdirAll(h.mvsDir, 0755); err != nil {
		logger.GetLogger().Errorf("CreateCamera[%s]: failed to create mvs directory %q: %v", req.Name, h.mvsDir, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to create mvs directory")
		return
	}

	// Build MVS config from request fields
	// AI 매니저는 MediaMTX 프록시 URL을 사용해야 카메라 동시접속 제한을 피할 수 있음
	mvsCameraURL := req.MediamtxRtspURL
	if mvsCameraURL == "" {
		mvsCameraURL = req.RtspURL
	}
	detectObjects := req.DetectObjects
	if detectObjects == nil {
		detectObjects = []string{}
	}
	mvs := MvsCameraCreateRequest{
		CameraID:      req.Name,
		CameraURL:     mvsCameraURL,
		ModelID:       req.ModelID,
		DetectObjects: detectObjects,
	}

	mvsJSON, err := json.MarshalIndent(mvs, "", "  ")
	if err != nil {
		logger.GetLogger().Errorf("CreateCamera[%s]: failed to marshal mvs data: %v", req.Name, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to marshal mvs data")
		return
	}

	mvsFileName := fmt.Sprintf("%s_%d_%d.mvs", mvs.CameraID, mvs.ModelID, time.Now().Unix())
	mvsPath := filepath.Join(h.mvsDir, mvsFileName)
	if err := os.WriteFile(mvsPath, mvsJSON, 0644); err != nil {
		logger.GetLogger().Errorf("CreateCamera[%s]: failed to write mvs file %q: %v", req.Name, mvsPath, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to write mvs file")
		return
	}

	// 5. Event rules 캐시 초기화
	h.refreshCameraConfigCache(req.Name)

	// 6. Automatically enable camera (start ffmpeg process)
	if err := h.enableCameraInternal(c.Request.Context(), req.Name, &req, "CreateCamera"); err != nil {
		logger.GetLogger().Warnf("CreateCamera[%s]: camera created but failed to start: %v", req.Name, err)
		// Don't fail the request - camera was created successfully
	}

	successResponse(c, tick, CreateCameraResponse{
		CameraID: req.Name,
	})
}

type CameraInfoResponse struct {
	Table string `json:"table"`
	Name  string `json:"name"`
	Desc  string `json:"desc"`

	RtspURL   string `json:"rtsp_url"`
	WebRTCUrl string `json:"webrtc_url"`
	MediaURL  string `json:"media_url"`

	EventRule     string `json:"event_rule"`
	DetectObjects string `json:"detect_objects"`
	FFmpegOption  string `json:"ffmpeg_option"`
}

// ============================================================
// MVS (Machine Vision System) Camera
// ============================================================

// type MvsCameraCreateRequest struct {
// 	CameraID      string   `json:"camera_id"`                         // cam{id}_{model_id}_{time} (자동 생성 가능)
// 	CameraURL     string   `json:"camera_url" binding:"required"`     // rtsp URL
// 	ModelID       int      `json:"model_id"`                          // 기본 모델 0
// 	DetectObjects []string `json:"detect_objects" binding:"required"` // ex) ["person", "car", "truck", "bus"]
// }

// CreateMvsCamera handles POST /api/mvs/camera.
// MVS 파일 저장용 - camera_id, camera_url, model_id, detect_objects만 저장.
func (h *Handler) CreateMvsCamera(c *gin.Context) {
	tick := time.Now()

	var req MvsCameraCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.GetLogger().Errorf("CreateMvsCamera: failed to bind JSON: %v", err)
		errorResponse(c, tick, http.StatusBadRequest, "bad request parameter")
		return
	}

	// camera_id 자동 생성: cam{id}_{model_id}_{unix_time}
	if req.CameraID == "" {
		req.CameraID = fmt.Sprintf("cam%d_%d_%d", time.Now().UnixNano()%100000, req.ModelID, time.Now().Unix())
	}

	// .mvs 파일로 저장
	mvsDetectObjects := req.DetectObjects
	if mvsDetectObjects == nil {
		mvsDetectObjects = []string{}
	}
	mvsData := map[string]any{
		"camera_id":      req.CameraID,
		"camera_url":     req.CameraURL,
		"model_id":       req.ModelID,
		"detect_objects": mvsDetectObjects,
	}

	mvsJSON, err := json.MarshalIndent(mvsData, "", "  ")
	if err != nil {
		logger.GetLogger().Errorf("CreateMvsCamera[%s]: failed to marshal mvs data: %v", req.CameraID, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to marshal mvs data")
		return
	}

	if err := os.MkdirAll(h.mvsDir, 0755); err != nil {
		logger.GetLogger().Errorf("CreateMvsCamera[%s]: failed to create mvs directory %q: %v", req.CameraID, h.mvsDir, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to create mvs directory")
		return
	}

	mvsFileName := fmt.Sprintf("%s_%d_%d.mvs", req.CameraID, req.ModelID, time.Now().Unix())
	mvsPath := filepath.Join(h.mvsDir, mvsFileName)
	if err := os.WriteFile(mvsPath, mvsJSON, 0644); err != nil {
		logger.GetLogger().Errorf("CreateMvsCamera[%s]: failed to write mvs file %q: %v", req.CameraID, mvsPath, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to write mvs file")
		return
	}

	successResponse(c, tick, CreateMvsCameraResponse{
		CameraID: req.CameraID,
		MvsPath:  mvsPath,
	})
}

// ============================================================
// MVS Event (외부 프로그램 → detection 결과 수신)
// ============================================================

type AIResultRequest struct {
	CameraID     string             `json:"camera_id" binding:"required"`
	ModelID      int64              `json:"model_id"`                      // 기본값 0
	Timestamp    int64              `json:"timestamp"`                     // Unix timestamp in milliseconds
	Detections   map[string]float64 `json:"detections" binding:"required"` // {"person": 3, "car": 5, ...}
	TotalObjects int                `json:"total_objects"`
}

// CreateMvsEvent handles POST /api/ai/results.
// 외부 AI 프로그램에서 detection 결과를 수신.
// 1) OR_LOG: {camera}_log 테이블에 ident별 count 저장
// 2) EventLog: 카메라 설정의 event_rule DSL 평가 → {camera}_event 테이블에 결과 저장
func (h *Handler) UploadAIResult(c *gin.Context) {
	tick := time.Now()

	var req AIResultRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.GetLogger().Errorf("upload AI result: %v", err)
		errorResponse(c, tick, http.StatusBadRequest, "bad request parameter")
		return
	}
	// timestamp: milliseconds → nanoseconds
	if req.Timestamp <= 0 {
		errorResponse(c, tick, http.StatusBadRequest, "invalid timestamp: must be positive milliseconds")
		return
	}
	tsNano := req.Timestamp * 1000000 // milliseconds to nanoseconds

	config := h.getCameraConfig(req.CameraID)
	if config == nil {
		logger.GetLogger().Errorf("UploadAIResult: camera config not found for camera_id=%q", req.CameraID)
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera config not found: %q", req.CameraID))
		return
	}

	// Detections map의 key를 소문자로 변환 (DSL과 일관성 유지)
	normalizedDetections := make(map[string]float64, len(req.Detections))
	for ident, value := range req.Detections {
		normalizedDetections[strings.ToLower(ident)] = value
	}

	// 1) OR_LOG: SaveObjects가 true일 때만 detections → {table}_log 테이블에 저장
	if config.SaveObjects {
		logs := make([]db.CameraLogRow, 0, len(normalizedDetections))
		for ident, value := range normalizedDetections {
			logs = append(logs, db.CameraLogRow{
				Name:     req.CameraID + "." + ident,
				Time:     tsNano,
				Value:    value,
				ModelID:  req.ModelID,
				CameraID: req.CameraID,
				Ident:    ident,
			})
		}

		if err := h.machbase.InsertCameraLogs(c.Request.Context(), config.Table+"_log", logs); err != nil {
			errorResponse(c, tick, http.StatusInternalServerError, "failed to insert camera logs")
			return
		}
	}

	// 2) EventLog: 캐시된 event rules로 DSL 평가 → {table}_event 저장
	_ = h.evaluateEventRules(c.Request.Context(), config.Table, config.Name, tsNano, normalizedDetections, config.EventRule)

	successResponse(c, tick, nil)
}

// CreateTable handles POST /api/table.
// Creates a TAG table with the standard structure.
func (h *Handler) CreateTable(c *gin.Context) {
	tick := time.Now()

	var req struct {
		TableName string `json:"table_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "table_name is required")
		return
	}

	if err := h.machbase.CreateTable(c.Request.Context(), req.TableName); err != nil {
		logger.GetLogger().Errorf("create table %s: %v", req.TableName, err)
		errorResponse(c, tick, http.StatusInternalServerError, fmt.Sprintf("failed to create table: %v", err))
		return
	}

	successResponse(c, tick, map[string]any{
		"table_name": req.TableName,
		"created":    true,
	})
}

// evaluateEventRules evaluates all enabled event rules against detection counts.
// Returns the number of event rows inserted.
func (h *Handler) evaluateEventRules(ctx context.Context, tableName string, cameraID string, tsNano int64, counts map[string]float64, rules []EventRule) int {
	if len(rules) == 0 {
		return 0
	}

	eventTable := tableName + "_event"
	var events []db.CameraEventRow

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		result, err := dsl.Evaluate(rule.Expression, counts)
		if err != nil {
			logger.GetLogger().Errorf("[table:%s][rule:%s] DSL parse error: %v", tableName, rule.ID, err)
			continue
		}

		// used_counts_snapshot 생성
		snapshot := make(map[string]any, len(counts)+1)
		for k, v := range counts {
			snapshot[k] = v
		}
		if result.Error != "" {
			snapshot["_error"] = result.Error
		}
		snapshotJSON, _ := json.Marshal(snapshot)

		stateKey := cameraID + "." + rule.ID
		var valueCode float64
		var shouldRecord bool

		if result.Error != "" {
			// ERROR(-1): 항상 기록, EDGE_ONLY 상태 변경 안 함
			valueCode = -1
			shouldRecord = true
		} else {
			switch rule.RecordMode {
			case "ALL_MATCHES":
				if result.Value {
					valueCode = 2 // MATCH
					shouldRecord = true
				}
			case "EDGE_ONLY":
				h.edgeMu.Lock()
				prev := h.edgeState[stateKey]
				if result.Value && !prev {
					valueCode = 1 // TRIGGER (false → true)
					shouldRecord = true
					h.edgeState[stateKey] = true
				} else if !result.Value && prev {
					valueCode = 0 // RESOLVE (true → false)
					shouldRecord = true
					h.edgeState[stateKey] = false
				}
				h.edgeMu.Unlock()
			}
		}

		if shouldRecord {
			events = append(events, db.CameraEventRow{
				Name:               stateKey,
				Time:               tsNano,
				Value:              valueCode,
				ExpressionText:     rule.Expression,
				UsedCountsSnapshot: string(snapshotJSON),
				CameraID:           cameraID,
				RuleID:             rule.ID,
				RuleName:           rule.Name,
			})
		}
	}

	if len(events) > 0 {
		if err := h.machbase.InsertCameraEvents(ctx, eventTable, events); err != nil {
			logger.GetLogger().Errorf("[camera:%s] failed to insert events: %v", cameraID, err)
			return 0
		}
	}

	return len(events)
}

// GetCamera handles GET /api/camera/:id.
// camera_dir 안의 {id}.json 파일을 읽어서 그대로 리턴.
func (h *Handler) GetCamera(c *gin.Context) {
	tick := time.Now()
	id := c.Param("id")

	cameraPath := filepath.Join(h.cameraDir, id+".json")
	data, err := os.ReadFile(cameraPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.GetLogger().Warnf("GetCamera[%s]: camera config file not found: %s", id, cameraPath)
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", id))
			return
		}
		logger.GetLogger().Errorf("GetCamera[%s]: failed to read camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read camera config")
		return
	}

	var camera CameraCreateRequest
	if err := json.Unmarshal(data, &camera); err != nil {
		logger.GetLogger().Errorf("GetCamera[%s]: failed to parse camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to parse camera config")
		return
	}

	// Convert camera to map and add camera_id
	var result map[string]any
	cameraData, _ := json.Marshal(camera)
	json.Unmarshal(cameraData, &result)
	result["camera_id"] = id
	delete(result, "server_url")

	successResponse(c, tick, result)
}

// UpdateCamera handles POST /api/camera/:id.
// camera_dir 안의 {id}.json 파일 내용을 수정하고 저장.
func (h *Handler) UpdateCamera(c *gin.Context) {
	tick := time.Now()
	id := c.Param("id")

	cameraPath := filepath.Join(h.cameraDir, id+".json")

	// 기존 카메라 설정 읽기
	data, err := os.ReadFile(cameraPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.GetLogger().Warnf("UpdateCamera[%s]: camera config file not found: %s", id, cameraPath)
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", id))
			return
		}
		logger.GetLogger().Errorf("UpdateCamera[%s]: failed to read camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read camera config")
		return
	}

	var existing CameraCreateRequest
	if err := json.Unmarshal(data, &existing); err != nil {
		logger.GetLogger().Errorf("UpdateCamera[%s]: failed to parse existing camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to parse camera config")
		return
	}

	var req CameraUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.GetLogger().Errorf("UpdateCamera[%s]: failed to bind JSON: %v", id, err)
		errorResponse(c, tick, http.StatusBadRequest, "bad request parameter")
		return
	}

	req.RtspURL = strings.TrimSpace(req.RtspURL)
	req.RtspPath = strings.TrimSpace(req.RtspPath)

	// rtsp_path 중복 검사: 변경되는 경우에만, 다른 카메라가 동일한 path를 사용 중이면 409
	if req.RtspPath != "" && req.RtspPath != existing.RtspPath && h.rtspPathInUse(id, req.RtspPath) {
		logger.GetLogger().Errorf("UpdateCamera[%s]: rtsp_path %q is already in use by another camera", id, req.RtspPath)
		errorResponse(c, tick, http.StatusConflict, fmt.Sprintf("rtsp_path %q is already in use by another camera", req.RtspPath))
		return
	}

	// 재시작 필요 여부 판단을 위해 ffmpeg 관련 old 값 미리 캡처
	oldRtspURL := existing.RtspURL
	oldRtspPath := existing.RtspPath
	oldFFmpegCommand := existing.FFmpegCommand
	oldOutputDir := existing.OutputDir
	oldArchiveDir := existing.ArchiveDir

	// 현재 실행 중인지 확인
	h.processMu.Lock()
	proc, wasRunning := h.processes[id]
	h.processMu.Unlock()

	// 기존 설정에 업데이트 필드 반영 (name, table은 유지)
	existing.Desc = req.Desc
	if req.RtspURL != "" {
		existing.RtspURL = req.RtspURL
	}
	if req.RtspPath != "" {
		existing.RtspPath = req.RtspPath
	}
	// server_url: 요청에 있으면 갱신, 없으면 기존값 유지
	if req.ServerURL != "" {
		existing.ServerURL = req.ServerURL
	}
	// webrtc_url, mediamtx_rtsp_url: rtsp_url이 없으면 클리어,
	// rtsp_path가 변경된 경우에만 재생성 (rtsp_url만 바뀐 경우 기존값 유지)
	if existing.RtspURL == "" {
		existing.WebRTCURL = ""
		existing.MediamtxRtspURL = ""
	} else if existing.RtspPath != oldRtspPath {
		existing.WebRTCURL = h.buildWebRTCURL(existing.RtspPath, existing.ServerURL)
		existing.MediamtxRtspURL = h.buildMediamtxRtspURL(existing.RtspPath)
	}
	existing.MediaURL = req.MediaURL
	existing.ModelID = req.ModelID
	existing.DetectObjects = req.DetectObjects
	existing.SaveObjects = req.SaveObjects
	existing.FFmpegCommand = req.FFmpegCommand
	// 빈 값이면 기존 유지 (인프라 필수 필드)
	if req.OutputDir != "" {
		existing.OutputDir = req.OutputDir
	}
	if req.ArchiveDir != "" {
		existing.ArchiveDir = req.ArchiveDir
	}
	existing.FFmpegOptions = req.FFmpegOptions

	// save_objects가 true이면 _log 테이블 생성 (IF NOT EXISTS라 중복 안전)
	if existing.SaveObjects {
		if err := h.machbase.CreateCameraLogTable(c.Request.Context(), existing.Table); err != nil {
			logger.GetLogger().Errorf("UpdateCamera[%s]: failed to create log table: %v", id, err)
			errorResponse(c, tick, http.StatusInternalServerError, fmt.Sprintf("failed to create log table: %v", err))
			return
		}
	}

	// event_rule: nil이면 기존 유지, 값이 있으면 덮어쓰기
	if req.EventRule != nil {
		existing.EventRule = *req.EventRule
		// enabled 룰이 하나라도 있으면 event 테이블 생성 (IF NOT EXISTS라 중복 안전)
		for _, rule := range existing.EventRule {
			if rule.Enabled {
				if err := h.machbase.CreateCameraEventTable(c.Request.Context(), existing.Table); err != nil {
					logger.GetLogger().Errorf("UpdateCamera[%s]: failed to create event table: %v", id, err)
					errorResponse(c, tick, http.StatusInternalServerError, fmt.Sprintf("failed to create event table: %v", err))
					return
				}
				break
			}
		}
	}

	cameraJSON, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		logger.GetLogger().Errorf("UpdateCamera[%s]: failed to marshal camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to marshal camera config")
		return
	}

	if err := os.WriteFile(cameraPath, cameraJSON, 0644); err != nil {
		logger.GetLogger().Errorf("UpdateCamera[%s]: failed to write camera config file %q: %v", id, cameraPath, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to write camera config file")
		return
	}

	// MVS 파일 갱신: 기존 파일 내용 수정 + 필요시 파일명 변경
	// 1. 기존 MVS 파일 찾기 (디렉토리 파싱으로 cameraID 정확 매칭)
	oldMvsFiles := h.findMvsFiles(id)

	// 2. 새 MVS 데이터 생성
	// AI 매니저는 MediaMTX 프록시 URL을 사용해야 카메라 동시접속 제한을 피할 수 있음
	newMvsCameraURL := existing.MediamtxRtspURL
	if newMvsCameraURL == "" {
		newMvsCameraURL = existing.RtspURL
	}
	newMvsDetectObjects := existing.DetectObjects
	if newMvsDetectObjects == nil {
		newMvsDetectObjects = []string{}
	}
	newMvs := MvsCameraCreateRequest{
		CameraID:      id,
		CameraURL:     newMvsCameraURL,
		ModelID:       existing.ModelID,
		DetectObjects: newMvsDetectObjects,
	}

	newMvsJSON, err := json.MarshalIndent(newMvs, "", "  ")
	if err != nil {
		logger.GetLogger().Errorf("UpdateCamera[%s]: failed to marshal mvs data: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to marshal mvs data")
		return
	}

	// 3. 기존 파일이 있으면 내용 비교 후 변경 시 파일명 갱신
	if len(oldMvsFiles) > 0 {
		oldMvsPath := oldMvsFiles[0] // 첫 번째 파일 사용

		// 기존 파일 읽어서 내용 비교
		oldData, err := os.ReadFile(oldMvsPath)
		var contentChanged bool = true // 기본값: 변경됨

		if err != nil {
			logger.GetLogger().Warnf("UpdateCamera[%s]: failed to read old mvs file %q: %v", id, oldMvsPath, err)
		} else {
			var oldMvs MvsCameraCreateRequest
			if err := json.Unmarshal(oldData, &oldMvs); err == nil {
				// 내용 비교: camera_url, model_id, detect_objects
				contentChanged = false

				// camera_url 비교
				if oldMvs.CameraURL != newMvs.CameraURL {
					contentChanged = true
				}

				// model_id 비교
				if oldMvs.ModelID != newMvs.ModelID {
					contentChanged = true
				}

				// detect_objects 배열 비교
				if len(oldMvs.DetectObjects) != len(newMvs.DetectObjects) {
					contentChanged = true
				} else {
					for i, obj := range oldMvs.DetectObjects {
						if obj != newMvs.DetectObjects[i] {
							contentChanged = true
							break
						}
					}
				}
			}
		}

		// 내용이 변경되었으면 파일 내용 업데이트 + 파일명 변경
		if contentChanged {
			// 1. 먼저 기존 파일의 내용을 업데이트
			if err := os.WriteFile(oldMvsPath, newMvsJSON, 0644); err != nil {
				logger.GetLogger().Errorf("UpdateCamera[%s]: failed to update mvs file %q: %v", id, oldMvsPath, err)
				errorResponse(c, tick, http.StatusInternalServerError, "failed to update mvs file")
				return
			}

			// 2. 새 파일명 생성 (timestamp 갱신)
			newMvsFileName := fmt.Sprintf("%s_%d_%d.mvs", id, newMvs.ModelID, time.Now().Unix())
			newMvsPath := filepath.Join(h.mvsDir, newMvsFileName)

			// 3. 파일명 변경 (내용은 이미 업데이트됨)
			if err := os.Rename(oldMvsPath, newMvsPath); err != nil {
				logger.GetLogger().Errorf("UpdateCamera[%s]: failed to rename mvs file from %q to %q: %v", id, oldMvsPath, newMvsPath, err)
				errorResponse(c, tick, http.StatusInternalServerError, "failed to rename mvs file")
				return
			}
		}
		// 내용이 같으면 파일 그대로 유지
	} else {
		// 기존 파일이 없으면 새로 생성
		newMvsFileName := fmt.Sprintf("%s_%d_%d.mvs", id, newMvs.ModelID, time.Now().Unix())
		newMvsPath := filepath.Join(h.mvsDir, newMvsFileName)
		if err := os.WriteFile(newMvsPath, newMvsJSON, 0644); err != nil {
			logger.GetLogger().Errorf("UpdateCamera[%s]: failed to write mvs file %q: %v", id, newMvsPath, err)
			errorResponse(c, tick, http.StatusInternalServerError, "failed to write mvs file")
			return
		}
	}

	// MediaMTX path 관리
	// 1. path 이름이 변경된 경우 기존 path 삭제
	if oldRtspPath != "" && oldRtspPath != existing.RtspPath {
		if err := h.mediamtxClient.RemovePath(c.Request.Context(), oldRtspPath); err != nil {
			logger.GetLogger().Warnf("UpdateCamera[%s]: failed to remove old MediaMTX path %q: %v", id, oldRtspPath, err)
		} else {
			logger.GetLogger().Infof("UpdateCamera[%s]: removed old MediaMTX path %q", id, oldRtspPath)
		}
	}
	// 2. rtsp_url과 rtsp_path가 모두 설정된 경우 등록/갱신
	if existing.RtspURL != "" && existing.RtspPath != "" {
		if err := h.mediamtxClient.AddOrUpdatePath(c.Request.Context(), existing.RtspPath, mediamtx.PathConfig{
			Source:         existing.RtspURL,
			SourceProtocol: mediamtx.PathSourceTCP,
		}); err != nil {
			logger.GetLogger().Warnf("UpdateCamera[%s]: failed to update MediaMTX path %q: %v", id, existing.RtspPath, err)
		} else {
			logger.GetLogger().Infof("UpdateCamera[%s]: updated MediaMTX path %q -> %s", id, existing.RtspPath, existing.RtspURL)
		}
	} else if existing.RtspURL == "" && existing.RtspPath != "" && oldRtspPath == existing.RtspPath {
		// rtsp_url이 지워졌고 path 이름은 그대로인 경우 → MediaMTX에서 path 제거
		if err := h.mediamtxClient.RemovePath(c.Request.Context(), existing.RtspPath); err != nil {
			logger.GetLogger().Warnf("UpdateCamera[%s]: failed to remove MediaMTX path %q after URL cleared: %v", id, existing.RtspPath, err)
		} else {
			logger.GetLogger().Infof("UpdateCamera[%s]: removed MediaMTX path %q (rtsp_url cleared)", id, existing.RtspPath)
		}
	}

	// Event rules 캐시 갱신
	h.refreshCameraConfigCache(id)

	// ffmpeg 관련 필드가 변경됐고 실행 중이면 재시작
	needsRestart := oldRtspURL != existing.RtspURL ||
		oldRtspPath != existing.RtspPath ||
		oldFFmpegCommand != existing.FFmpegCommand ||
		oldOutputDir != existing.OutputDir ||
		oldArchiveDir != existing.ArchiveDir

	if wasRunning && needsRestart {
		h.processMu.Lock()
		if h.processes[id] == proc {
			delete(h.processes, id)
		}
		h.processMu.Unlock()
		proc.cancel()
		if err := h.watcher.RemoveWatch(c.Request.Context(), id); err != nil {
			logger.GetLogger().Warnf("UpdateCamera[%s]: failed to remove watcher before restart: %v", id, err)
		}
		if existing.RtspURL != "" {
			if err := h.enableCameraInternal(c.Request.Context(), id, &existing, "UpdateCamera"); err != nil {
				logger.GetLogger().Warnf("UpdateCamera[%s]: failed to restart camera: %v", id, err)
			} else {
				logger.GetLogger().Infof("UpdateCamera[%s]: restarted with new config", id)
			}
		} else {
			logger.GetLogger().Infof("UpdateCamera[%s]: stopped camera (rtsp_url cleared)", id)
		}
	}

	successResponse(c, tick, CreateCameraResponse{
		CameraID: id,
	})
}

// DeleteCamera handles DELETE /api/camera/:id.
// camera_dir 안의 {id}.json 파일 삭제.
func (h *Handler) DeleteCamera(c *gin.Context) {
	tick := time.Now()
	id := c.Param("id")

	cameraPath := filepath.Join(h.cameraDir, id+".json")

	if _, err := os.Stat(cameraPath); os.IsNotExist(err) {
		logger.GetLogger().Warnf("DeleteCamera[%s]: camera config file not found: %s", id, cameraPath)
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", id))
		return
	}

	// 삭제 전에 rtsp_path를 미리 읽어둠
	var rtspPathToRemove string
	if cfg := h.getCameraConfig(id); cfg != nil {
		rtspPathToRemove = cfg.RtspPath
	}

	if err := os.Remove(cameraPath); err != nil {
		logger.GetLogger().Errorf("DeleteCamera[%s]: failed to delete camera config file %q: %v", id, cameraPath, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to delete camera config file")
		return
	}

	// MVS 파일도 삭제
	h.removeMvsFiles(id)

	// 실행 중인 ffmpeg 프로세스 종료
	h.processMu.Lock()
	proc, running := h.processes[id]
	if running {
		delete(h.processes, id)
	}
	h.processMu.Unlock()
	if running {
		proc.cancel()
		if err := h.watcher.RemoveWatch(c.Request.Context(), id); err != nil {
			logger.GetLogger().Warnf("DeleteCamera[%s]: failed to remove watcher: %v", id, err)
		}
		logger.GetLogger().Infof("DeleteCamera[%s]: stopped ffmpeg process", id)
	}

	// MediaMTX path 삭제
	if rtspPathToRemove != "" {
		if err := h.mediamtxClient.RemovePath(c.Request.Context(), rtspPathToRemove); err != nil {
			logger.GetLogger().Warnf("DeleteCamera[%s]: failed to remove MediaMTX path %q: %v", id, rtspPathToRemove, err)
		} else {
			logger.GetLogger().Infof("DeleteCamera[%s]: removed MediaMTX path %q", id, rtspPathToRemove)
		}
	}

	// Event rules 캐시 제거
	h.removeCameraConfigCache(id)

	successResponse(c, tick, map[string]string{
		"name": id,
	})
}

// TestCameraConnection handles POST /api/camera/:id/test.
// Tests RTSP URL connection using ffprobe.
func (h *Handler) TestCameraConnection(c *gin.Context) {
	tick := time.Now()

	id := c.Param("id")
	if id == "" {
		errorResponse(c, tick, http.StatusBadRequest, "camera id is required")
		return
	}

	if h.ffRunner == nil {
		errorResponse(c, tick, http.StatusInternalServerError, "ffprobe not available")
		return
	}

	config := h.getCameraConfig(id)
	if config == nil {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", id))
		return
	}
	if config.RtspURL == "" {
		errorResponse(c, tick, http.StatusBadRequest, "camera has no rtsp_url configured")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	if err := h.ffRunner.ProbeRTSP(ctx, config.RtspURL); err != nil {
		logger.GetLogger().Infof("TestCameraConnection[%s]: probe failed: %v", id, err)
		successResponse(c, tick, gin.H{"ok": false, "message": err.Error()})
		return
	}

	successResponse(c, tick, gin.H{"ok": true, "message": "connected"})
}

// enableCameraInternal starts the ffmpeg restart loop for a camera.
// If cam is nil, it will read the camera config from file.
// caller is used for log prefixes (e.g. "CreateCamera", "EnableCamera").
// The first ffmpeg start is performed synchronously; errors are returned to the caller.
// Subsequent restarts run in the background with exponential backoff.
func (h *Handler) enableCameraInternal(ctx context.Context, id string, cam *CameraCreateRequest, caller string) error {
	// Check if already running
	h.processMu.Lock()
	if proc, running := h.processes[id]; running {
		proc.mu.Lock()
		isActuallyRunning := proc.cmd != nil && proc.cmd.Process != nil
		proc.mu.Unlock()
		if isActuallyRunning {
			h.processMu.Unlock()
			return fmt.Errorf("camera '%s' is already running", id)
		}
		// backoff 중인 경우 (cmd == nil): 기존 loop 취소 후 재시작 허용
		delete(h.processes, id)
		h.processMu.Unlock()
		proc.cancel()
	} else {
		h.processMu.Unlock()
	}

	// Load camera config if not provided
	if cam == nil {
		cameraPath := filepath.Join(h.cameraDir, id+".json")
		data, err := os.ReadFile(cameraPath)
		if err != nil {
			return fmt.Errorf("failed to read camera config: %w", err)
		}

		var config CameraCreateRequest
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse camera config: %w", err)
		}
		cam = &config
	}

	if cam.RtspURL == "" {
		return fmt.Errorf("camera has no rtsp_url configured")
	}

	// Recreate MVS file if it was deleted by DisableCamera.
	mvsPattern := filepath.Join(h.mvsDir, fmt.Sprintf("%s_*.mvs", id))
	if existing, _ := filepath.Glob(mvsPattern); len(existing) == 0 {
		// AI 매니저는 MediaMTX 프록시 URL을 사용해야 카메라 동시접속 제한을 피할 수 있음
		mvsCamURL := cam.MediamtxRtspURL
		if mvsCamURL == "" {
			mvsCamURL = cam.RtspURL
		}
		startupDetectObjects := cam.DetectObjects
		if startupDetectObjects == nil {
			startupDetectObjects = []string{}
		}
		mvs := MvsCameraCreateRequest{
			CameraID:      id,
			CameraURL:     mvsCamURL,
			ModelID:       cam.ModelID,
			DetectObjects: startupDetectObjects,
		}
		if mvsJSON, err := json.MarshalIndent(mvs, "", "  "); err == nil {
			if err := os.MkdirAll(h.mvsDir, 0755); err == nil {
				mvsFileName := fmt.Sprintf("%s_%d_%d.mvs", id, mvs.ModelID, time.Now().Unix())
				if err := os.WriteFile(filepath.Join(h.mvsDir, mvsFileName), mvsJSON, 0644); err != nil {
					logger.GetLogger().Warnf("[camera:%s] failed to recreate mvs file: %v", id, err)
				}
			}
		}
	}

	// MediaMTX path 등록 (rtsp_url과 rtsp_path가 모두 설정된 경우)
	// 기존 path 삭제 후 재추가: source URL 변경 시 MediaMTX가 새 source로 즉시 재접속하도록 강제
	if cam.RtspURL != "" && cam.RtspPath != "" {
		pathCfg := mediamtx.PathConfig{
			Source:         cam.RtspURL,
			SourceProtocol: mediamtx.PathSourceTCP,
		}
		_ = h.mediamtxClient.RemovePath(ctx, cam.RtspPath) // 없으면 무시
		if err := h.mediamtxClient.AddPath(ctx, cam.RtspPath, pathCfg); err != nil {
			logger.GetLogger().Warnf("%s[%s]: failed to register MediaMTX path %q: %v", caller, id, cam.RtspPath, err)
		} else {
			logger.GetLogger().Infof("%s[%s]: registered MediaMTX path %q -> %s", caller, id, cam.RtspPath, cam.RtspURL)
		}
	}

	// Resolve ffmpeg binary
	// 1순위: config.yaml ffmpeg.binary, 2순위: 카메라별 ffmpeg_command, 3순위: 시스템 PATH
	ffmpegBin := "ffmpeg"
	if cam.FFmpegCommand != "" {
		ffmpegBin = cam.FFmpegCommand
	}
	if h.ffmpegBinary != "" {
		ffmpegBin = h.ffmpegBinary
	}

	// Validate paths
	if cam.OutputDir == "" {
		return fmt.Errorf("output_dir not configured")
	}
	if cam.ArchiveDir == "" {
		return fmt.Errorf("archive_dir not configured")
	}

	// Resolve paths
	outputDir := cam.OutputDir
	if !filepath.IsAbs(outputDir) {
		outputDir = filepath.Join(h.dataDir, outputDir)
	}

	archiveDir := cam.ArchiveDir
	if !filepath.IsAbs(archiveDir) {
		archiveDir = filepath.Join(h.dataDir, archiveDir)
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Backward compat: 기존 설정 파일에 mediamtx_rtsp_url이 없는 경우 rtsp_path로 자동 생성
	if cam.MediamtxRtspURL == "" && cam.RtspPath != "" {
		cam.MediamtxRtspURL = h.buildMediamtxRtspURL(cam.RtspPath)
	}

	// Build ffmpeg args
	args := buildFFmpegArgs(*cam)

	// Ensure log directory exists
	if err := os.MkdirAll(h.logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	logFilePath := filepath.Join(h.logDir, id+"_ffmpeg.log")

	loopCtx, loopCancel := context.WithCancel(context.Background())
	proc := &cameraProcess{
		cancel:    loopCancel,
		startedAt: time.Now(),
	}

	h.processMu.Lock()
	h.processes[id] = proc
	h.processMu.Unlock()

	// firstErr receives the result of the very first ffmpeg start.
	// The loop goroutine sends once; nil means started successfully.
	firstErr := make(chan error, 1)
	go h.runCameraLoop(loopCtx, id, cam, ffmpegBin, args, outputDir, archiveDir, logFilePath, proc, firstErr)

	if err := <-firstErr; err != nil {
		// First start failed – clean up and propagate the error.
		h.processMu.Lock()
		if h.processes[id] == proc {
			delete(h.processes, id)
		}
		h.processMu.Unlock()
		loopCancel()
		return err
	}

	return nil
}

// runCameraLoop runs ffmpeg in a restart loop until ctx is cancelled.
// On unexpected exit it waits (with exponential backoff) before restarting.
// On intentional stop (ctx cancelled) it waits for the current process to exit
// and removes the watcher, then removes itself from h.processes.
func (h *Handler) runCameraLoop(
	ctx context.Context,
	id string,
	cam *CameraCreateRequest,
	ffmpegBin string,
	args []string,
	outputDir, archiveDir, logFilePath string,
	proc *cameraProcess,
	firstErr chan<- error,
) {
	const (
		initBackoff = 3 * time.Second
		maxBackoff  = 10 * time.Second
		stableTime  = 1 * time.Minute
	)
	backoff := initBackoff
	isFirst := true

	defer func() {
		h.processMu.Lock()
		if h.processes[id] == proc {
			delete(h.processes, id)
		}
		h.processMu.Unlock()
		logger.GetLogger().Infof("[camera:%s] restart loop exited", id)
	}()

	for {
		if ctx.Err() != nil {
			if isFirst {
				firstErr <- ctx.Err()
			}
			return
		}

		startTime := time.Now()

		// Open (append) log file for this attempt.
		logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			if isFirst {
				firstErr <- fmt.Errorf("failed to open log file: %w", err)
				return
			}
			logger.GetLogger().Warnf("[camera:%s] failed to open log file: %v, retrying in %v...", id, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				if backoff < maxBackoff {
					backoff *= 2
				}
			}
			continue
		}

		// Start ffmpeg (no CommandContext – we manage lifecycle manually so
		// we can wait cleanly before returning on intentional stop).
		cmd := exec.Command(ffmpegBin, args...)
		cmd.Dir = outputDir
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		setPdeathsig(cmd)

		logger.GetLogger().Infof("[camera:%s] ffmpeg start: %s %s (log: %s)", id, ffmpegBin, strings.Join(args, " "), logFilePath)

		if err := cmd.Start(); err != nil {
			logFile.Close()
			if isFirst {
				firstErr <- fmt.Errorf("failed to start ffmpeg: %w", err)
				return
			}
			logger.GetLogger().Warnf("[camera:%s] ffmpeg failed to start: %v, retrying in %v...", id, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				if backoff < maxBackoff {
					backoff *= 2
				}
			}
			continue
		}

		proc.mu.Lock()
		proc.cmd = cmd
		proc.mu.Unlock()

		// Register watcher for this camera's output directory.
		rule := watcher.WatcherRule{
			CameraID:  id,
			Table:     cam.Table,
			SourceDir: outputDir,
			TargetDir: archiveDir,
			Ext:       ".m4s",
		}
		if err := h.watcher.AddWatch(context.Background(), rule); err != nil {
			logger.GetLogger().Errorf("[camera:%s] failed to add watcher: %v", id, err)
			// Continue even if watcher fails; ffmpeg is already running.
		}

		if isFirst {
			firstErr <- nil
			isFirst = false
		}

		// Wait for process exit in a goroutine so we can also select on ctx.
		exited := make(chan error, 1)
		go func() {
			exited <- cmd.Wait()
		}()

		var exitErr error
		select {
		case <-ctx.Done():
			// Intentional stop: kill ffmpeg and wait for it to exit.
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			exitErr = <-exited
		case exitErr = <-exited:
			// Process exited on its own; ctx may or may not be cancelled.
		}

		logFile.Close()

		proc.mu.Lock()
		proc.cmd = nil
		proc.mu.Unlock()

		if werr := h.watcher.RemoveWatch(context.Background(), id); werr != nil {
			logger.GetLogger().Warnf("[camera:%s] failed to remove watcher: %v", id, werr)
		}

		if ctx.Err() != nil {
			// Intentional stop – exit the loop.
			return
		}

		// Unexpected exit – schedule a restart.
		uptime := time.Since(startTime)
		if uptime >= stableTime {
			backoff = initBackoff
		}
		logger.GetLogger().Warnf("[camera:%s] ffmpeg exited unexpectedly (uptime: %v, err: %v), restarting in %v...",
			id, uptime.Truncate(time.Second), exitErr, backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			if backoff < maxBackoff {
				backoff *= 2
			}
		}
	}
}

// EnableCamera handles POST /api/camera/:id/enable.
// 카메라 설정파일을 읽어서 ffmpeg 프로세스를 시작.
func (h *Handler) EnableCamera(c *gin.Context) {
	tick := time.Now()
	id := c.Param("id")

	// config 파일에 enabled: true 저장
	if cfg := h.getCameraConfig(id); cfg != nil {
		cfg.Enabled = boolPtr(true)
		cameraPath := filepath.Join(h.cameraDir, id+".json")
		if data, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			if err := os.WriteFile(cameraPath, data, 0644); err != nil {
				logger.GetLogger().Warnf("EnableCamera[%s]: failed to save enabled state: %v", id, err)
			} else {
				h.refreshCameraConfigCache(id)
			}
		}
	}

	// Use internal function to enable camera
	if err := h.enableCameraInternal(c.Request.Context(), id, nil, "EnableCamera"); err != nil {
		if strings.Contains(err.Error(), "already running") {
			errorResponse(c, tick, http.StatusConflict, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "failed to read") {
			errorResponse(c, tick, http.StatusNotFound, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not configured") || strings.Contains(err.Error(), "no rtsp_url") {
			errorResponse(c, tick, http.StatusBadRequest, err.Error())
			return
		}
		logger.GetLogger().Errorf("EnableCamera[%s]: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, err.Error())
		return
	}

	// Get PID from process map
	h.processMu.Lock()
	proc, ok := h.processes[id]
	h.processMu.Unlock()
	var pid int
	if ok {
		proc.mu.Lock()
		if proc.cmd != nil && proc.cmd.Process != nil {
			pid = proc.cmd.Process.Pid
		}
		proc.mu.Unlock()
	}

	successResponse(c, tick, map[string]any{
		"name":   id,
		"pid":    pid,
		"status": "running",
	})
}

// DisableCamera handles POST /api/camera/:id/disable.
// 실행 중인 ffmpeg 프로세스를 중지.
func (h *Handler) DisableCamera(c *gin.Context) {
	tick := time.Now()
	id := c.Param("id")

	h.processMu.Lock()
	proc, running := h.processes[id]
	if !running {
		h.processMu.Unlock()
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' is not running", id))
		return
	}
	// Remove from the map immediately so subsequent status queries show "stopped".
	// The loop goroutine's deferred delete is a safe no-op when the entry is gone.
	delete(h.processes, id)
	h.processMu.Unlock()

	// Cancel the restart loop (kills ffmpeg and stops retrying).
	proc.cancel()

	// Remove watcher proactively; the loop goroutine also removes it, which is safe (idempotent).
	if err := h.watcher.RemoveWatch(c.Request.Context(), id); err != nil {
		logger.GetLogger().Warnf("[camera:%s] failed to remove watcher: %v", id, err)
	}

	// MVS 파일 삭제 (detection 프로그램이 비활성 카메라를 인식하지 않도록)
	h.removeMvsFiles(id)

	// MediaMTX path 삭제 + config 파일에 enabled: false 저장
	if cfg := h.getCameraConfig(id); cfg != nil {
		if cfg.RtspPath != "" {
			if err := h.mediamtxClient.RemovePath(c.Request.Context(), cfg.RtspPath); err != nil {
				logger.GetLogger().Warnf("DisableCamera[%s]: failed to remove MediaMTX path %q: %v", id, cfg.RtspPath, err)
			} else {
				logger.GetLogger().Infof("DisableCamera[%s]: removed MediaMTX path %q", id, cfg.RtspPath)
			}
		}
		cfg.Enabled = boolPtr(false)
		cameraPath := filepath.Join(h.cameraDir, id+".json")
		if data, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			if err := os.WriteFile(cameraPath, data, 0644); err != nil {
				logger.GetLogger().Warnf("DisableCamera[%s]: failed to save disabled state: %v", id, err)
			} else {
				h.refreshCameraConfigCache(id)
			}
		}
	}

	successResponse(c, tick, map[string]string{
		"name":   id,
		"status": "stopped",
	})
}

// buildFFmpegArgs builds ffmpeg command args from camera config.
// input options → -i rtsp_url → output options → output_name
func buildFFmpegArgs(cam CameraCreateRequest) []string {
	var inputArgs, outputArgs []string

	for _, opt := range cam.FFmpegOptions {
		key := opt.K
		flag := key
		if !strings.HasPrefix(flag, "-") {
			flag = "-" + flag
		}

		if isOutputOption(key) {
			outputArgs = append(outputArgs, flag)
			if opt.V != nil {
				outputArgs = append(outputArgs, *opt.V)
			}
		} else {
			inputArgs = append(inputArgs, flag)
			if opt.V != nil {
				inputArgs = append(inputArgs, *opt.V)
			}
		}
	}

	// ffmpeg 입력 URL: MediaMTX 프록시 URL이 있으면 사용, 없으면 원본 rtsp_url 직접 사용
	inputURL := cam.RtspURL
	if cam.MediamtxRtspURL != "" {
		inputURL = cam.MediamtxRtspURL
	}

	args := make([]string, 0, len(inputArgs)+len(outputArgs)+5)
	args = append(args, inputArgs...)
	args = append(args, "-i", inputURL)
	args = append(args, "-an") // 오디오 트랙 제외 (비디오 전용)
	args = append(args, outputArgs...)

	args = append(args, "manifest.mpd")

	return args
}

// isOutputOption returns true if the key is an ffmpeg output option (goes after -i).
func isOutputOption(key string) bool {
	// codec options
	if strings.HasPrefix(key, "c:") || strings.HasPrefix(key, "codec") ||
		key == "vcodec" || key == "acodec" {
		return true
	}
	// format & segment options
	switch key {
	case "f", "format",
		"seg_duration", "segment_time", "segment_format",
		"use_template", "use_timeline",
		"window_size", "extra_window_size", "min_seg_duration",
		"hls_time", "hls_list_size", "hls_segment_filename", "hls_flags",
		"movflags", "frag_type",
		"b", "b:v", "b:a", "preset", "crf",
		"r", "s", "an", "vn", "map",
		"copyts":
		return true
	}
	return false
}

// GetCameraStatus handles GET /api/camera/:id/status.
// 개별 카메라의 설정 존재 여부, ffmpeg 프로세스 실행 상태를 리턴.
func (h *Handler) GetCameraStatus(c *gin.Context) {
	tick := time.Now()
	id := c.Param("id")

	// 설정 파일 존재 확인
	cameraPath := filepath.Join(h.cameraDir, id+".json")
	_, err := os.Stat(cameraPath)
	if os.IsNotExist(err) {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", id))
		return
	}

	status := "stopped"
	var pid int
	var startedAt string
	var uptime string

	h.processMu.Lock()
	proc, running := h.processes[id]
	h.processMu.Unlock()
	if running {
		proc.mu.Lock()
		hasProcess := proc.cmd != nil && proc.cmd.Process != nil
		if hasProcess {
			pid = proc.cmd.Process.Pid
		}
		proc.mu.Unlock()

		if hasProcess {
			status = "running"
		} else {
			status = "restarting"
		}
		startedAt = proc.startedAt.Format(time.RFC3339)
		uptime = time.Since(proc.startedAt).Truncate(time.Second).String()
	}

	resp := map[string]any{
		"name":   id,
		"status": status,
	}
	if running {
		resp["pid"] = pid
		resp["started_at"] = startedAt
		resp["uptime"] = uptime
	}

	successResponse(c, tick, resp)
}

// GetCamerasHealth handles GET /api/cameras/health.
// 전체 카메라 헬스 요약: 총 카메라 수, 실행 중, 중지 상태.
func (h *Handler) GetCamerasHealth(c *gin.Context) {
	tick := time.Now()

	entries, err := os.ReadDir(h.cameraDir)
	if err != nil {
		if os.IsNotExist(err) {
			successResponse(c, tick, map[string]any{
				"total":   0,
				"running": 0,
				"stopped": 0,
				"cameras": []any{},
			})
			return
		}
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read camera directory")
		return
	}

	h.processMu.Lock()
	defer h.processMu.Unlock()

	var cameras []map[string]any
	runningCount := 0

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		cam := map[string]any{
			"name":   name,
			"status": "stopped",
		}

		if proc, ok := h.processes[name]; ok {
			proc.mu.Lock()
			hasProcess := proc.cmd != nil && proc.cmd.Process != nil
			if hasProcess {
				cam["pid"] = proc.cmd.Process.Pid
			}
			proc.mu.Unlock()

			if hasProcess {
				cam["status"] = "running"
			} else {
				cam["status"] = "restarting"
			}
			runningCount++
			cam["started_at"] = proc.startedAt.Format(time.RFC3339)
			cam["uptime"] = time.Since(proc.startedAt).Truncate(time.Second).String()
		}

		cameras = append(cameras, cam)
	}

	total := len(cameras)
	successResponse(c, tick, map[string]any{
		"total":   total,
		"running": runningCount,
		"stopped": total - runningCount,
		"cameras": cameras,
	})
}

// GetDetectObjectsByCamera handles GET /api/camera/:id/detect_objects.
// 특정 카메라의 detect_objects 조회.
func (h *Handler) GetDetectObjectsByCamera(c *gin.Context) {
	tick := time.Now()

	id := c.Param("id")
	if id == "" {
		errorResponse(c, tick, http.StatusBadRequest, "camera id is required")
		return
	}

	cameraPath := filepath.Join(h.cameraDir, id+".json")
	data, err := os.ReadFile(cameraPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.GetLogger().Warnf("GetDetectObjectsByCamera[%s]: camera config file not found: %s", id, cameraPath)
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", id))
			return
		}
		logger.GetLogger().Errorf("GetDetectObjectsByCamera[%s]: failed to read camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read camera config")
		return
	}

	var camera CameraCreateRequest
	if err := json.Unmarshal(data, &camera); err != nil {
		logger.GetLogger().Errorf("GetDetectObjectsByCamera[%s]: failed to parse camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to parse camera config")
		return
	}

	objects := camera.DetectObjects
	if objects == nil {
		objects = []string{}
	}

	successResponse(c, tick, map[string]any{
		"camera_id":      id,
		"detect_objects": objects,
	})
}

// UpdateDetectObjectsByCamera handles POST /api/camera/:id/detect_objects.
// 특정 카메라의 detect_objects 수정.
func (h *Handler) UpdateDetectObjectsByCamera(c *gin.Context) {
	tick := time.Now()

	id := c.Param("id")
	if id == "" {
		errorResponse(c, tick, http.StatusBadRequest, "camera id is required")
		return
	}

	var req struct {
		DetectObjects []string `json:"detect_objects" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.GetLogger().Errorf("UpdateDetectObjectsByCamera[%s]: failed to bind JSON: %v", id, err)
		errorResponse(c, tick, http.StatusBadRequest, "bad request parameter")
		return
	}

	cameraPath := filepath.Join(h.cameraDir, id+".json")
	data, err := os.ReadFile(cameraPath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.GetLogger().Warnf("UpdateDetectObjectsByCamera[%s]: camera config file not found: %s", id, cameraPath)
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", id))
			return
		}
		logger.GetLogger().Errorf("UpdateDetectObjectsByCamera[%s]: failed to read camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read camera config")
		return
	}

	var camera CameraCreateRequest
	if err := json.Unmarshal(data, &camera); err != nil {
		logger.GetLogger().Errorf("UpdateDetectObjectsByCamera[%s]: failed to parse camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to parse camera config")
		return
	}

	camera.DetectObjects = req.DetectObjects

	cameraJSON, err := json.MarshalIndent(camera, "", "  ")
	if err != nil {
		logger.GetLogger().Errorf("UpdateDetectObjectsByCamera[%s]: failed to marshal camera config: %v", id, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to marshal camera config")
		return
	}

	if err := os.WriteFile(cameraPath, cameraJSON, 0644); err != nil {
		logger.GetLogger().Errorf("UpdateDetectObjectsByCamera[%s]: failed to write camera config file %q: %v", id, cameraPath, err)
		errorResponse(c, tick, http.StatusInternalServerError, "failed to write camera config file")
		return
	}

	h.refreshCameraConfigCache(id)

	// MVS 파일도 갱신: AI 매니저가 detect_objects 변경을 감지할 수 있도록
	oldMvsFiles := h.findMvsFiles(id)
	if len(oldMvsFiles) > 0 {
		oldMvsData, err := os.ReadFile(oldMvsFiles[0])
		if err == nil {
			var oldMvs MvsCameraCreateRequest
			if err := json.Unmarshal(oldMvsData, &oldMvs); err == nil {
				oldMvs.DetectObjects = req.DetectObjects

				if newMvsJSON, err := json.MarshalIndent(oldMvs, "", "  "); err == nil {
					h.removeMvsFiles(id)
					newMvsFileName := fmt.Sprintf("%s_%d_%d.mvs", id, oldMvs.ModelID, time.Now().Unix())
					newMvsPath := filepath.Join(h.mvsDir, newMvsFileName)
					if err := os.WriteFile(newMvsPath, newMvsJSON, 0644); err != nil {
						logger.GetLogger().Warnf("UpdateDetectObjectsByCamera[%s]: failed to write mvs file %q: %v", id, newMvsPath, err)
					} else {
						logger.GetLogger().Infof("UpdateDetectObjectsByCamera[%s]: mvs file updated: %s", id, newMvsFileName)
					}
				}
			}
		}
	}

	successResponse(c, tick, map[string]any{
		"camera_id":      id,
		"detect_objects": req.DetectObjects,
	})
}

// HeartbeatMediaMTX handles GET /api/media/heartbeat.
// MediaMTX 서버의 상태를 확인 (config.yaml의 mediamtx 설정 사용).
func (h *Handler) HeartbeatMediaMTX(c *gin.Context) {
	tick := time.Now()

	// config의 mediamtx 설정으로 URL 생성
	mediaURL := fmt.Sprintf("http://%s:%d", h.mediamtxHost, h.mediamtxPort)

	// Heartbeat 호출 (5초 timeout)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	_, err := mediamtx.Heartbeat(ctx, mediaURL, 5*time.Second)
	if err != nil {
		logger.GetLogger().Warnf("HeartbeatMediaMTX: heartbeat failed for %s: %v", mediaURL, err)
		successResponse(c, tick, map[string]any{
			"healthy": false,
		})
		return
	}

	successResponse(c, tick, map[string]any{
		"healthy": true,
	})
}

// generateRtspPath는 MediaMTX path로 사용할 URL-safe 랜덤 ID를 생성한다.
// 카메라 이름에 특수문자가 포함될 수 있으므로 이름 대신 랜덤 ID를 사용한다.
func generateRtspPath() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// fallback: timestamp 기반
		return fmt.Sprintf("cam%d", time.Now().UnixNano())
	}
	return "cam-" + hex.EncodeToString(b)
}
