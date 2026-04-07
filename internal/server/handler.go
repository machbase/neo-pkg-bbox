package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/machbase/neo-pkg-bbox/internal/config"
	"github.com/machbase/neo-pkg-bbox/internal/db"
	"github.com/machbase/neo-pkg-bbox/internal/ffmpeg"
	"github.com/machbase/neo-pkg-bbox/internal/logger"
	"github.com/machbase/neo-pkg-bbox/internal/mediamtx"
	"github.com/machbase/neo-pkg-bbox/internal/watcher"

	"github.com/gin-gonic/gin"
)

const videoStreamIndex = 0

var defaultSensorNames = []string{
	"sensor-1", "sensor-2", "sensor-3", "sensor-4", "sensor-5",
	"sensor-6", "sensor-7", "sensor-8", "sensor-9", "sensor-10",
}

var defaultSensorLabels = map[string]string{
	"sensor-1":  "Sensor 1",
	"sensor-2":  "Sensor 2",
	"sensor-3":  "Sensor 3",
	"sensor-4":  "Sensor 4",
	"sensor-5":  "Sensor 5",
	"sensor-6":  "Sensor 6",
	"sensor-7":  "Sensor 7",
	"sensor-8":  "Sensor 8",
	"sensor-9":  "Sensor 9",
	"sensor-10": "Sensor 10",
}

var tagPattern = regexp.MustCompile(`^[\p{L}\p{N}_.:-]+$`)

// cameraProcess tracks a running ffmpeg process for a camera.
// cmd is protected by mu and may be nil during backoff between restarts.
type cameraProcess struct {
	cancel    context.CancelFunc // cancels the entire restart loop
	startedAt time.Time
	mu        sync.Mutex
	cmd       *exec.Cmd // current ffmpeg cmd; nil while in backoff
}

// Handler handles API requests.
type Handler struct {
	machbase             *db.Machbase
	watcher              Watcher // watcher interface for dynamic watch management
	configPath           string  // absolute path to config.yaml (for GET/POST /api/config)
	dataDir              string
	logDir               string // log directory (ffmpeg logs etc.)
	mvsDir               string
	cameraDir            string
	ffmpegBinary         string
	ffRunner             *ffmpeg.FFmpegRunner
	mediamtxClient         *mediamtx.Client // MediaMTX HTTP API 클라이언트
	mediamtxHost           string           // MediaMTX 내부 API 호스트 (heartbeat용)
	mediamtxWebRTCHost     string           // 프론트에 노출할 WebRTC URL 호스트 (실제 서버 IP)
	mediamtxPort           int              // MediaMTX HTTP API 포트 (heartbeat용)
	mediamtxWebRTCPort     int              // MediaMTX WebRTC 포트 (webrtc_url 생성용)
	mediamtxRtspServerPort int              // MediaMTX RTSP 서버 포트 (ffmpeg용, 기본: 8554)
	prefixCache          map[string]string
	fpsCache             map[string]*int
	cacheMu              sync.RWMutex
	processes            map[string]*cameraProcess
	processMu            sync.Mutex
	edgeState            map[string]bool // EDGE_ONLY 이전 상태: "camera_id.rule_id" → prev_result
	edgeMu               sync.Mutex
	cameraConfigs        map[string]*CameraCreateRequest // camera_id → full camera config 캐시
	configMu             sync.RWMutex
	detectObjects        []string // 감지 가능한 객체 목록 캐시
	detectObjectMu       sync.RWMutex
	lastEventQueryTime   int64 // 마지막 이벤트 조회 시간 (nanoseconds)
	lastEventQueryTimeMu sync.Mutex
	stateFilePath        string // {dataDir}/state.json 경로
}

// Watcher interface for adding/removing file system watches dynamically
type Watcher interface {
	AddWatch(ctx context.Context, rule watcher.WatcherRule) error
	RemoveWatch(ctx context.Context, cameraID string) error
}

// NewHandler creates a new Handler.
func NewHandler(machbase *db.Machbase, watcher Watcher, ffRunner *ffmpeg.FFmpegRunner, dataDir, logDir, mvsDir, cameraDir, ffmpegBinary, configPath string, mediamtxHost string, mediamtxWebRTCHost string, mediamtxPort int, mediamtxWebRTCPort int, mediamtxRtspServerPort int) *Handler {
	if dataDir == "" {
		dataDir = "/data"
	}
	if logDir == "" {
		logDir = "/var/log/blackbox"
	}
	mediamtxCfg := config.MediamtxConfig{Host: mediamtxHost, WebRTCHost: mediamtxWebRTCHost, Port: mediamtxPort, WebRTCPort: mediamtxWebRTCPort, RtspServerPort: mediamtxRtspServerPort}
	mediamtxCfg.ApplyDefaults()
	h := &Handler{
		machbase:               machbase,
		watcher:                watcher,
		ffRunner:               ffRunner,
		configPath:             configPath,
		dataDir:                dataDir,
		logDir:                 logDir,
		mvsDir:                 mvsDir,
		cameraDir:              cameraDir,
		ffmpegBinary:           ffmpegBinary,
		mediamtxClient:         mediamtx.NewClient(mediamtxCfg),
		mediamtxHost:           mediamtxCfg.Host,
		mediamtxWebRTCHost:     mediamtxCfg.WebRTCHost,
		mediamtxPort:           mediamtxCfg.Port,
		mediamtxWebRTCPort:     mediamtxCfg.WebRTCPort,
		mediamtxRtspServerPort: mediamtxCfg.RtspServerPort,
		prefixCache:    make(map[string]string),
		fpsCache:       make(map[string]*int),
		processes:      make(map[string]*cameraProcess),
		edgeState:      make(map[string]bool),
		cameraConfigs:  make(map[string]*CameraCreateRequest),
	}
	h.stateFilePath = filepath.Join(dataDir, "state.json")
	h.loadState()
	h.loadAllCameraConfigs()

	// COCO 80 classes (hardcoded)
	h.detectObjects = []string{
		"person", "bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck", "boat",
		"traffic light", "fire hydrant", "stop sign", "parking meter", "bench", "bird", "cat",
		"dog", "horse", "sheep", "cow", "elephant", "bear", "zebra", "giraffe", "backpack",
		"umbrella", "handbag", "tie", "suitcase", "frisbee", "skis", "snowboard", "sports ball",
		"kite", "baseball bat", "baseball glove", "skateboard", "surfboard", "tennis racket",
		"bottle", "wine glass", "cup", "fork", "knife", "spoon", "bowl", "banana", "apple",
		"sandwich", "orange", "broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair",
		"couch", "potted plant", "bed", "dining table", "toilet", "tv", "laptop", "mouse",
		"remote", "keyboard", "cell phone", "microwave", "oven", "toaster", "sink", "refrigerator",
		"book", "clock", "vase", "scissors", "teddy bear", "hair drier", "toothbrush",
	}

	return h
}

// serverState represents the persistent state saved to state.json.
type serverState struct {
	LastEventQueryTime int64 `json:"last_event_query_time"`
}

// loadState reads state.json and restores lastEventQueryTime.
func (h *Handler) loadState() {
	data, err := os.ReadFile(h.stateFilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.GetLogger().Warnf("loadState: failed to read %s: %v", h.stateFilePath, err)
		}
		return
	}
	var s serverState
	if err := json.Unmarshal(data, &s); err != nil {
		logger.GetLogger().Warnf("loadState: failed to parse %s: %v", h.stateFilePath, err)
		return
	}
	h.lastEventQueryTime = s.LastEventQueryTime
	logger.GetLogger().Infof("loadState: restored lastEventQueryTime=%d", s.LastEventQueryTime)
}

// saveState writes current lastEventQueryTime to state.json.
func (h *Handler) saveState() {
	s := serverState{LastEventQueryTime: h.lastEventQueryTime}
	data, err := json.Marshal(s)
	if err != nil {
		logger.GetLogger().Errorf("saveState: failed to marshal: %v", err)
		return
	}
	if err := os.WriteFile(h.stateFilePath, data, 0644); err != nil {
		logger.GetLogger().Errorf("saveState: failed to write %s: %v", h.stateFilePath, err)
	}
}

// loadAllCameraConfigs scans cameraDir and pre-loads all camera configs into cache.
func (h *Handler) loadAllCameraConfigs() {
	if h.cameraDir == "" {
		return
	}
	entries, err := os.ReadDir(h.cameraDir)
	if err != nil {
		logger.GetLogger().Warnf("[camera_configs] cameraDir not found, skip preload: %v", err)
		return
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		cameraID := strings.TrimSuffix(entry.Name(), ".json")
		config, err := h.loadCameraConfigFromFile(cameraID)
		if err != nil {
			logger.GetLogger().Errorf("[camera_configs] failed to load %s: %v", cameraID, err)
			continue
		}
		h.cameraConfigs[cameraID] = config
		if len(config.EventRule) > 0 {
			count += len(config.EventRule)
		}
	}
	logger.GetLogger().Infof("[camera_configs] preloaded %d rules from %d cameras", count, len(h.cameraConfigs))
}

// loadCameraConfigFromFile reads camera config from file. (caller holds lock or no lock needed)
func (h *Handler) loadCameraConfigFromFile(cameraID string) (*CameraCreateRequest, error) {
	path := filepath.Join(h.cameraDir, cameraID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config CameraCreateRequest
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// getCameraConfig returns cached camera config. Loads from file on cache miss.
func (h *Handler) getCameraConfig(cameraID string) *CameraCreateRequest {
	h.configMu.RLock()
	config, ok := h.cameraConfigs[cameraID]
	h.configMu.RUnlock()
	if ok {
		return config
	}

	// cache miss → load from file
	loaded, err := h.loadCameraConfigFromFile(cameraID)
	if err != nil {
		return nil
	}
	h.configMu.Lock()
	h.cameraConfigs[cameraID] = loaded
	h.configMu.Unlock()
	return loaded
}

// refreshCameraConfigCache reloads camera config for a specific camera from file.
func (h *Handler) refreshCameraConfigCache(cameraID string) {
	config, err := h.loadCameraConfigFromFile(cameraID)
	if err != nil {
		logger.GetLogger().Errorf("[camera_configs] failed to refresh %s: %v", cameraID, err)
		return
	}
	h.configMu.Lock()
	h.cameraConfigs[cameraID] = config
	h.configMu.Unlock()
}

// removeCameraConfigCache removes camera config cache for a camera.
func (h *Handler) removeCameraConfigCache(cameraID string) {
	h.configMu.Lock()
	delete(h.cameraConfigs, cameraID)
	h.configMu.Unlock()
}

// findMvsFiles returns all MVS file paths that belong to the given cameraID.
// MVS 파일명 형식: {cameraID}_{modelID}_{timestamp}.mvs
// 파일명에서 뒤쪽 두 _필드(modelID, timestamp)를 제거한 값이 cameraID와 정확히 일치하는 경우만 반환.
func (h *Handler) findMvsFiles(cameraID string) []string {
	entries, err := os.ReadDir(h.mvsDir)
	if err != nil {
		return nil
	}
	var result []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mvs") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".mvs")
		// {cameraID}_{modelID}_{timestamp} 에서 뒤 두 필드 제거
		if idx := strings.LastIndex(base, "_"); idx >= 0 {
			base = base[:idx]
		} else {
			continue
		}
		if idx := strings.LastIndex(base, "_"); idx >= 0 {
			base = base[:idx]
		} else {
			continue
		}
		if base == cameraID {
			result = append(result, filepath.Join(h.mvsDir, e.Name()))
		}
	}
	return result
}

// removeMvsFiles deletes all mvs files that belong to cameraID.
func (h *Handler) removeMvsFiles(cameraID string) {
	for _, path := range h.findMvsFiles(cameraID) {
		_ = os.Remove(path)
	}
}

// Shutdown stops all running ffmpeg processes.
func (h *Handler) Shutdown() {
	h.processMu.Lock()
	procs := make(map[string]*cameraProcess, len(h.processes))
	for k, v := range h.processes {
		procs[k] = v
	}
	h.processMu.Unlock()

	for id, proc := range procs {
		proc.mu.Lock()
		pid := 0
		if proc.cmd != nil && proc.cmd.Process != nil {
			pid = proc.cmd.Process.Pid
		}
		proc.mu.Unlock()
		logger.GetLogger().Infof("[camera:%s] shutting down ffmpeg (PID: %d)", id, pid)
		proc.cancel()
	}
}

// getDetectObjects returns the hardcoded COCO 80 class list.
func (h *Handler) getDetectObjects() []string {
	return h.detectObjects
}

// errorResponse sends a standardized error response.
func errorResponse(c *gin.Context, tick time.Time, status int, reason string) {
	c.JSON(status, Response{
		Success: false,
		Reason:  reason,
		Elapse:  time.Since(tick).String(),
		Data:    nil,
	})
}

// successResponse sends a standardized success response.
func successResponse(c *gin.Context, tick time.Time, data any) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Reason:  "success",
		Elapse:  time.Since(tick).String(),
		Data:    data,
	})
}

// sanitizeTag validates and sanitizes a tag value.
func sanitizeTag(value string) (string, error) {
	if value == "" {
		return "", ErrEmptyTag
	}
	if !tagPattern.MatchString(value) {
		return "", ErrIllegalTagCharacters
	}
	return value, nil
}

// parseTimeToken parses a time token string.
// Supports: Unix nanoseconds (integer), ISO date strings, or "now".
func parseTimeToken(raw string) (time.Time, error) {
	candidate := strings.TrimSpace(raw)
	if strings.ToLower(candidate) == "now" {
		return time.Now(), nil
	}

	// Try parsing as Unix nanoseconds (integer)
	if nsInt, err := strconv.ParseInt(candidate, 10, 64); err == nil {
		if nsInt > 0 {
			sec := nsInt / 1_000_000_000
			nsec := nsInt % 1_000_000_000
			return time.Unix(sec, nsec), nil
		}
	}

	// Fall back to ISO date string parsing
	normalized := strings.ReplaceAll(candidate, "T", " ")
	normalized = strings.TrimSuffix(normalized, "Z")

	layouts := []string{
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}

	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, normalized, time.Local); err == nil {
			return t, nil
		}
	}

	return time.Time{}, ErrInvalidTimeFormat
}

// formatTime formats a time value for response.
func formatTime(t time.Time) string {
	s := t.UTC().Format("2006-01-02T15:04:05.000000")
	if idx := strings.Index(s, "."); idx != -1 {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s + "Z"
}

// resolvePrefix resolves the chunk prefix for a camera.
func (h *Handler) resolvePrefix(c *gin.Context, camera string) string {
	h.cacheMu.RLock()
	if prefix, ok := h.prefixCache[camera]; ok {
		h.cacheMu.RUnlock()
		return prefix
	}
	h.cacheMu.RUnlock()

	// Get prefix from camera config file instead of DB metadata
	config := h.getCameraConfig(camera)
	if config == nil {
		h.cacheMu.Lock()
		h.prefixCache[camera] = "chunk-stream"
		h.fpsCache[camera] = nil
		h.cacheMu.Unlock()
		return "chunk-stream"
	}

	// Use default prefix for now
	// TODO: add prefix field to config if custom prefix is needed
	prefix := "chunk-stream"

	h.cacheMu.Lock()
	h.prefixCache[camera] = prefix
	h.fpsCache[camera] = nil // FPS not stored in config yet
	h.cacheMu.Unlock()

	return prefix
}

// getCameraFPS gets the FPS for a camera.
func (h *Handler) getCameraFPS(c *gin.Context, camera string) *int {
	h.cacheMu.RLock()
	if fps, ok := h.fpsCache[camera]; ok {
		h.cacheMu.RUnlock()
		return fps
	}
	h.cacheMu.RUnlock()

	h.resolvePrefix(c, camera)

	h.cacheMu.RLock()
	defer h.cacheMu.RUnlock()
	return h.fpsCache[camera]
}

// resolveArchiveDir returns the archive directory for a camera.
// Uses camera config's archive_dir if set (absolute path), otherwise defaults to {dataDir}/{camera}/out.
func (h *Handler) resolveArchiveDir(cameraID string) string {
	config := h.getCameraConfig(cameraID)
	if config != nil && config.ArchiveDir != "" && filepath.IsAbs(config.ArchiveDir) {
		return config.ArchiveDir
	}
	return filepath.Join(h.dataDir, cameraID, "out")
}

// initPath returns the path to the init segment.
func (h *Handler) initPath(camera string) string {
	return filepath.Join(h.resolveArchiveDir(camera), "init-stream0.m4s")
}

// chunkPath returns the path to a chunk segment.
func (h *Handler) chunkPath(c *gin.Context, camera string, chunkNumber int64) string {
	prefix := h.resolvePrefix(c, camera)
	t := time.UnixMilli(chunkNumber).UTC()
	dateDir := t.Format("20060102")
	filename := prefix + "0-" + time.UnixMilli(chunkNumber).UTC().Format("20060202150405") + ".m4s"
	return filepath.Join(h.dataDir, camera, "out", dateDir, filename)
}

// readChunkFile reads a chunk file.
func (h *Handler) readChunkFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// sensorSortKey returns a sort key for sensor IDs.
func sensorSortKey(sensorID string) (int, int, string) {
	re := regexp.MustCompile(`^sensor-(\d+)$`)
	if matches := re.FindStringSubmatch(sensorID); len(matches) == 2 {
		var num int
		_, _ = parseIntValue(matches[1], &num)
		return 0, num, ""
	}
	return 1, 0, sensorID
}

// sortSensorIDs sorts sensor IDs.
func sortSensorIDs(sensorIDs []string) {
	sort.Slice(sensorIDs, func(i, j int) bool {
		a1, a2, a3 := sensorSortKey(sensorIDs[i])
		b1, b2, b3 := sensorSortKey(sensorIDs[j])
		if a1 != b1 {
			return a1 < b1
		}
		if a2 != b2 {
			return a2 < b2
		}
		return a3 < b3
	})
}

func parseIntValue(s string, out *int) (bool, error) {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false, nil
		}
	}
	var val int
	for _, c := range s {
		val = val*10 + int(c-'0')
	}
	*out = val
	return true, nil
}

// sensorKeyFromTag extracts sensor key from tag.
func sensorKeyFromTag(camera, tag string) string {
	if tag == "" {
		return ""
	}
	prefix1 := camera + ":"
	prefix2 := camera + "."
	if strings.HasPrefix(tag, prefix1) {
		return tag[len(prefix1):]
	}
	if strings.HasPrefix(tag, prefix2) {
		return tag[len(prefix2):]
	}
	return tag
}

// matchSensorID matches a tag name to a sensor ID.
func matchSensorID(tagName string, sensorIDs []string) string {
	for _, sensorID := range sensorIDs {
		if tagName == sensorID {
			return sensorID
		}
		if strings.HasSuffix(tagName, ":"+sensorID) || strings.HasSuffix(tagName, "."+sensorID) {
			return sensorID
		}
	}
	return ""
}

// uniqueStrings returns unique strings from a slice.
func uniqueStrings(ss []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// Error types
type ApiError struct {
	Status  int
	Message string
}

func (e ApiError) Error() string {
	return e.Message
}

func NewApiError(status int, message string) ApiError {
	return ApiError{Status: status, Message: message}
}

var (
	ErrEmptyTag             = NewApiError(http.StatusBadRequest, "Empty tag value")
	ErrIllegalTagCharacters = NewApiError(http.StatusBadRequest, "Illegal characters in tag")
	ErrInvalidTimeFormat    = NewApiError(http.StatusBadRequest, "Invalid time format")
)

// startOrphanWatcher는 60초 간격으로 Machbase 테이블 목록을 조회하여
// 대응 테이블이 삭제된 카메라 설정파일을 자동 제거하는 백그라운드 고루틴이다.
func (h *Handler) startOrphanWatcher(ctx context.Context) {
	const interval = 60 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.GetLogger().Infof("[orphan_watcher] started (interval=%v)", interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.checkAndRemoveOrphans(ctx)
		}
	}
}

// checkAndRemoveOrphans는 Machbase에 존재하지 않는 테이블을 가진 카메라 설정파일을 제거한다.
func (h *Handler) checkAndRemoveOrphans(ctx context.Context) {
	tables, err := h.machbase.ListTagTables(ctx)
	if err != nil {
		logger.GetLogger().Warnf("[orphan_watcher] failed to list tables, skipping: %v", err)
		return
	}

	tableSet := make(map[string]bool, len(tables))
	for _, t := range tables {
		tableSet[t] = true
	}

	// 캐시에서 고아 카메라 목록 수집 (RLock → 복사 후 처리)
	type orphanEntry struct {
		cameraID string
		table    string
		rtspPath string
	}

	h.configMu.RLock()
	var orphans []orphanEntry
	for cameraID, cfg := range h.cameraConfigs {
		if cfg.Table == "" {
			continue
		}
		if !tableSet[strings.ToLower(cfg.Table)] {
			orphans = append(orphans, orphanEntry{
				cameraID: cameraID,
				table:    cfg.Table,
				rtspPath: cfg.RtspPath,
			})
		}
	}
	h.configMu.RUnlock()

	for _, o := range orphans {
		logger.GetLogger().Warnf("[orphan_watcher] table %q not found in Machbase, removing camera %q", o.table, o.cameraID)

		// ffmpeg 프로세스 종료
		h.processMu.Lock()
		proc, running := h.processes[o.cameraID]
		if running {
			delete(h.processes, o.cameraID)
		}
		h.processMu.Unlock()
		if running {
			proc.cancel()
			if err := h.watcher.RemoveWatch(ctx, o.cameraID); err != nil {
				logger.GetLogger().Warnf("[orphan_watcher] failed to remove watcher for %q: %v", o.cameraID, err)
			}
		}

		// MediaMTX path 삭제
		if o.rtspPath != "" {
			if err := h.mediamtxClient.RemovePath(ctx, o.rtspPath); err != nil {
				logger.GetLogger().Warnf("[orphan_watcher] failed to remove MediaMTX path %q: %v", o.rtspPath, err)
			}
		}

		// 설정파일 · MVS 파일 · 캐시 제거
		cameraPath := filepath.Join(h.cameraDir, o.cameraID+".json")
		if err := os.Remove(cameraPath); err != nil && !os.IsNotExist(err) {
			logger.GetLogger().Errorf("[orphan_watcher] failed to remove config file %q: %v", cameraPath, err)
		}
		h.removeMvsFiles(o.cameraID)
		h.removeCameraConfigCache(o.cameraID)

		logger.GetLogger().Infof("[orphan_watcher] removed orphaned camera %q (table=%q)", o.cameraID, o.table)
	}
}

// startupCamerasAsync는 서버 시작 시 모든 카메라를 복원한다.
// 순서: MediaMTX path 등록 완료 → 카메라 프로세스(ffmpeg) 시작
// 백그라운드 goroutine으로 호출한다.
func (h *Handler) startupCamerasAsync(ctx context.Context) {
	// Step 1: MediaMTX path 등록 (MediaMTX가 준비될 때까지 재시도)
	h.restoreMediaMTXPaths(ctx)
	if ctx.Err() != nil {
		return
	}

	// Step 2: 카메라 프로세스 시작
	h.configMu.RLock()
	toStart := make([]struct {
		id  string
		cam CameraCreateRequest
	}, 0, len(h.cameraConfigs))
	for id, cam := range h.cameraConfigs {
		if cam.RtspURL != "" && cam.isEnabled() {
			toStart = append(toStart, struct {
				id  string
				cam CameraCreateRequest
			}{id, *cam})
		}
	}
	h.configMu.RUnlock()

	if len(toStart) == 0 {
		return
	}

	started := 0
	for i := range toStart {
		e := &toStart[i]
		if err := h.enableCameraInternal(ctx, e.id, &e.cam, "Startup"); err != nil {
			if strings.Contains(err.Error(), "already running") {
				continue
			}
			logger.GetLogger().Warnf("[startup] failed to start camera %q: %v", e.id, err)
			continue
		}
		started++
	}
	logger.GetLogger().Infof("[startup] started %d/%d camera(s)", started, len(toStart))
}

// restoreMediaMTXPaths registers MediaMTX paths for all cameras that have rtsp_url and rtsp_path.
// MediaMTX가 준비될 때까지 재시도한다. 서버 시작 직후 백그라운드에서 호출한다.
func (h *Handler) restoreMediaMTXPaths(ctx context.Context) {
	h.configMu.RLock()
	type entry struct{ id, path, rtspURL string }
	toRegister := make([]entry, 0, len(h.cameraConfigs))
	for id, cam := range h.cameraConfigs {
		if cam.RtspURL != "" && cam.RtspPath != "" {
			toRegister = append(toRegister, entry{id, cam.RtspPath, cam.RtspURL})
		}
	}
	h.configMu.RUnlock()

	if len(toRegister) == 0 {
		return
	}

	const maxAttempts = 15
	const retryInterval = 2 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		failed := 0
		for _, e := range toRegister {
			if err := h.mediamtxClient.AddOrUpdatePath(ctx, e.path, mediamtx.PathConfig{
				Source:         e.rtspURL,
				SourceProtocol: mediamtx.PathSourceTCP,
			}); err != nil {
				failed++
			} else {
				logger.GetLogger().Infof("[startup] restored MediaMTX path %q -> %s (camera: %s)", e.path, e.rtspURL, e.id)
			}
		}

		if failed == 0 {
			logger.GetLogger().Infof("[startup] restored %d MediaMTX path(s)", len(toRegister))
			return
		}

		logger.GetLogger().Warnf("[startup] %d/%d MediaMTX path(s) failed (attempt %d/%d), retrying in %v...",
			failed, len(toRegister), attempt, maxAttempts, retryInterval)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryInterval):
		}
	}

	logger.GetLogger().Warnf("[startup] gave up restoring MediaMTX paths after %d attempts", maxAttempts)
}

// buildWebRTCURL generates the MediaMTX WebRTC URL for a given path name.
// serverURL이 있으면 해당 host를 사용, 없으면 config의 mediamtxWebRTCHost 사용.
// Format: http://{host}:{webrtcPort}/{pathName}/whep
func (h *Handler) buildWebRTCURL(pathName, serverURL string) string {
	if pathName == "" {
		return ""
	}
	host := h.mediamtxWebRTCHost
	if serverURL != "" {
		host = serverURL
	}
	return fmt.Sprintf("http://%s:%d/%s/whep", host, h.mediamtxWebRTCPort, pathName)
}

// buildMediamtxRtspURL generates the MediaMTX RTSP proxy URL for a given path name.
// ffmpeg는 원본 카메라 URL 대신 이 URL을 통해 MediaMTX에서 스트림을 가져온다.
// Format: rtsp://{host}:{rtspServerPort}/{pathName}
func (h *Handler) buildMediamtxRtspURL(pathName string) string {
	if pathName == "" {
		return ""
	}
	return fmt.Sprintf("rtsp://%s:%d/%s", h.mediamtxHost, h.mediamtxRtspServerPort, pathName)
}

// rtspPathInUse returns true if the given MediaMTX path name is already in use
// by another camera. excludeID is the camera ID to skip (use "" for create).
func (h *Handler) rtspPathInUse(excludeID, path string) bool {
	if path == "" {
		return false
	}
	h.configMu.RLock()
	defer h.configMu.RUnlock()
	for id, cfg := range h.cameraConfigs {
		if id == excludeID {
			continue
		}
		if cfg.RtspPath == path {
			return true
		}
	}
	return false
}
