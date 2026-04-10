package server

import (
	"fmt"
	"math"
	"github.com/machbase/neo-pkg-bbox/internal/db"
	"github.com/machbase/neo-pkg-bbox/internal/logger"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// GetCameras handles GET /api/cameras.
// Returns list of cameras from config files in cameraDir.
func (h *Handler) GetCameras(c *gin.Context) {
	tick := time.Now()

	// Read camera config files from cameraDir
	entries, err := os.ReadDir(h.cameraDir)
	if err != nil {
		if os.IsNotExist(err) {
			successResponse(c, tick, GetCamerasResponse{
				Cameras: []Camera{},
			})
			return
		}
		logger.GetLogger().Errorf("GetCameras: failed to read camera directory %q: %v", h.cameraDir, err)
		errorResponse(c, tick, http.StatusInternalServerError, "Failed to read camera directory")
		return
	}

	ctx := c.Request.Context()

	// 존재하는 테이블 목록을 한 번만 조회 (N번 쿼리 대신 1번)
	existingTables, err := h.machbase.ListTables(ctx)
	tableSet := make(map[string]bool, len(existingTables))
	if err != nil {
		logger.GetLogger().Warnf("GetCameras: failed to list tables, skipping orphan check: %v", err)
	} else {
		for _, t := range existingTables {
			tableSet[strings.ToLower(t)] = true
		}
	}

	cameraList := make([]Camera, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		cameraName := strings.TrimSuffix(name, ".json")

		// 카메라 config에서 table 필드 읽기
		cfg := h.getCameraConfig(cameraName)
		if cfg == nil {
			continue
		}

		// 테이블 목록 조회에 성공했고, 테이블이 없으면 고아 설정파일 제거
		if err == nil && !tableSet[strings.ToLower(cfg.Table)] {
			logger.GetLogger().Warnf("GetCameras[%s]: table %q not found, removing orphaned config", cameraName, cfg.Table)
			cameraPath := filepath.Join(h.cameraDir, cameraName+".json")
			_ = os.Remove(cameraPath)
			h.removeMvsFiles(cameraName)
			h.removeCameraConfigCache(cameraName)
			continue
		}

		cameraList = append(cameraList, Camera{ID: cameraName, Label: cameraName})
	}

	sort.Slice(cameraList, func(i, j int) bool {
		return cameraList[i].ID < cameraList[j].ID
	})

	successResponse(c, tick, GetCamerasResponse{
		Cameras: cameraList,
	})
}

// GetTables handles GET /api/tables.
// Returns TAG table names from Machbase, excluding _event and _log tables.
func (h *Handler) GetTables(c *gin.Context) {
	tick := time.Now()

	tables, err := h.machbase.ListTables(c.Request.Context())
	if err != nil {
		logger.GetLogger().Errorf("GetTables: failed to list tables: %v", err)
		errorResponse(c, tick, http.StatusInternalServerError, fmt.Sprintf("failed to list tables: %v", err))
		return
	}
	if tables == nil {
		tables = []string{}
	}

	successResponse(c, tick, map[string]any{
		"tables": tables,
	})
}

// GetModels handles GET /api/models.
// 사용 가능한 AI 모델 목록을 반환 (하드코딩).
func (h *Handler) GetModels(c *gin.Context) {
	tick := time.Now()

	models := map[string]string{
		"0": "yolov8n.onnx",
		"1": "yolov8s.onnx",
		"2": "yolov8m.onnx",
		"3": "yolov8l.onnx",
		"4": "yolov8x.onnx",
	}

	successResponse(c, tick, map[string]any{
		"models": models,
	})
}

// GetDetectObjects handles GET /api/detect_objects.
// 감지 가능한 객체 목록을 반환 (object.txt에서 로드).
func (h *Handler) GetDetectObjects(c *gin.Context) {
	tick := time.Now()

	objects := h.getDetectObjects()

	successResponse(c, tick, map[string]any{
		"detect_objects": objects,
	})
}

// GetTimeRange handles GET /api/get_time_range.
func (h *Handler) GetTimeRange(c *gin.Context) {
	tick := time.Now()

	var req GetTimeRangeRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "Missing required parameter 'tagname'")
		return
	}

	cameraID, err := sanitizeTag(req.Tagname)
	if err != nil {
		errorResponse(c, tick, http.StatusBadRequest, err.Error())
		return
	}

	// Get camera config to retrieve table name
	config := h.getCameraConfig(cameraID)
	if config == nil {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("Camera '%s' not found", cameraID))
		return
	}

	ctx := c.Request.Context()
	var start, end *string

	// Use table name and camera ID from config for DB queries
	tableName := config.Table
	stats, err := h.machbase.BlackboxStatsByTag(ctx, tableName, cameraID)
	if err == nil && stats != nil {
		minStr := formatTime(stats.MinTime)
		maxStr := formatTime(stats.MaxTime)
		start = &minStr
		end = &maxStr
	}

	if start == nil || end == nil {
		bounds, err := h.machbase.BlackboxTimeBounds(ctx, tableName, cameraID)
		if err == nil && bounds != nil {
			if start == nil {
				minStr := formatTime(bounds.MinTime)
				start = &minStr
			}
			if end == nil {
				maxStr := formatTime(bounds.MaxTime)
				end = &maxStr
			}
		}
	}

	if start == nil || end == nil {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("No timeline entries for camera '%s'", cameraID))
		return
	}

	chunkDuration := 0.0
	interval, err := h.machbase.BlackboxChunkInterval(ctx, tableName, cameraID)
	if err == nil && interval > 0 {
		chunkDuration = interval
	}

	fps := h.getCameraFPS(c, cameraID)
	if chunkDuration == 0 && fps != nil && *fps > 0 {
		chunkDuration = 1.0 / float64(*fps)
	}

	if chunkDuration == 0 {
		chunkDuration = 5.0
	}

	successResponse(c, tick, GetTimeRangeResponse{
		Camera:               cameraID,
		Start:                *start,
		End:                  *end,
		ChunkDurationSeconds: chunkDuration,
		FPS:                  fps,
	})
}

// GetChunkInfo handles GET /api/get_chunk_info.
func (h *Handler) GetChunkInfo(c *gin.Context) {
	tick := time.Now()

	var req GetChunkInfoRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "Missing required parameter 'tagname' or 'time'")
		return
	}

	cameraID, err := sanitizeTag(req.Tagname)
	if err != nil {
		errorResponse(c, tick, http.StatusBadRequest, err.Error())
		return
	}

	// Get camera config to retrieve table name
	config := h.getCameraConfig(cameraID)
	if config == nil {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("Camera '%s' not found", cameraID))
		return
	}

	timestamp, err := parseTimeToken(req.Time)
	if err != nil {
		errorResponse(c, tick, http.StatusBadRequest, err.Error())
		return
	}

	ctx := c.Request.Context()
	tableName := config.Table
	record, err := h.machbase.ChunkRecordForTime(ctx, tableName, cameraID, timestamp)
	if err != nil {
		logger.GetLogger().Errorf("GetChunkInfo[%s]: failed to fetch chunk info at time %s: %v", cameraID, req.Time, err)
		errorResponse(c, tick, http.StatusInternalServerError, "Failed to fetch chunk info")
		return
	}

	if record == nil {
		successResponse(c, tick, GetChunkInfoResponse{})
		return
	}

	resp := GetChunkInfoResponse{
		Camera: cameraID,
		Time:   formatTime(record.EntryTime),
		Length: math.Ceil(record.Length*1000) / 1000, // 소수점 3자리 올림
	}

	successResponse(c, tick, resp)
}

// GetChunk handles GET /api/v_get_chunk.
// Note: This returns binary data, not JSON Response format.
func (h *Handler) GetChunk(c *gin.Context) {
	tick := time.Now()

	var req GetChunkRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "Missing required parameter 'tagname'")
		return
	}

	cameraID, err := sanitizeTag(req.Tagname)
	if err != nil {
		errorResponse(c, tick, http.StatusBadRequest, err.Error())
		return
	}

	// Get camera config to retrieve table name
	config := h.getCameraConfig(cameraID)
	if config == nil {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("Camera '%s' not found", cameraID))
		return
	}

	timeToken := req.Time
	if timeToken == "" {
		timeToken = "0"
	}

	var chunkData []byte

	if timeToken == "0" || strings.ToLower(timeToken) == "init" {
		path := h.initPath(cameraID)
		chunkData, err = os.ReadFile(path)
		if err != nil {
			logger.GetLogger().Errorf("GetChunk[%s]: failed to read init file %q: %v", cameraID, path, err)
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("Segment not found for camera '%s'", cameraID))
			return
		}
	} else {
		timestamp, err := parseTimeToken(timeToken)
		if err != nil {
			errorResponse(c, tick, http.StatusBadRequest, err.Error())
			return
		}

		ctx := c.Request.Context()
		tableName := config.Table
		record, err := h.machbase.ChunkRecordForTime(ctx, tableName, cameraID, timestamp)
		if err != nil {
			logger.GetLogger().Errorf("GetChunk[%s]: failed to fetch chunk info at time %s: %v", cameraID, timeToken, err)
			errorResponse(c, tick, http.StatusInternalServerError, "Failed to fetch chunk info")
			return
		}

		if record == nil {
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("Chunk not found for camera '%s' at time '%s'", cameraID, timeToken))
			return
		}

		// 절대경로면 그대로 사용 (기존 데이터 호환), 상대경로면 archive_dir 붙여서 복원
		fullPath := record.ChunkPath
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(h.resolveArchiveDir(cameraID), fullPath)
		}
		chunkData, err = os.ReadFile(fullPath)
		if err != nil {
			logger.GetLogger().Errorf("GetChunk[%s]: failed to read chunk file %q: %v", cameraID, fullPath, err)
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("Segment not found for camera '%s' at path '%s'", cameraID, fullPath))
			return
		}
	}

	// Binary response - not JSON
	c.Data(http.StatusOK, "application/octet-stream", chunkData)
}

// GetCameraRollup handles GET /api/get_camera_rollup_info.
func (h *Handler) GetCameraRollup(c *gin.Context) {
	tick := time.Now()

	var req GetCameraRollupRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "Missing required parameters")
		return
	}

	cameraID, err := sanitizeTag(req.Tagname)
	if err != nil {
		errorResponse(c, tick, http.StatusBadRequest, err.Error())
		return
	}

	// Get camera config to retrieve table name
	config := h.getCameraConfig(cameraID)
	if config == nil {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("Camera '%s' not found", cameraID))
		return
	}

	minutes := req.Minutes
	if minutes <= 0 {
		minutes = 1
	}

	if req.StartTime < 0 || req.EndTime < 0 {
		errorResponse(c, tick, http.StatusBadRequest, "Start and end time must be non-negative")
		return
	}

	if req.StartTime >= req.EndTime {
		errorResponse(c, tick, http.StatusBadRequest, "Parameter 'start_time' must be earlier than 'end_time'")
		return
	}

	ctx := c.Request.Context()
	tableName := config.Table
	rows, err := h.machbase.CameraRollup(ctx, tableName, cameraID, minutes, req.StartTime, req.EndTime)
	if err != nil {
		logger.GetLogger().Errorf("GetCameraRollup[%s]: failed to fetch rollup data (start=%d, end=%d, minutes=%d): %v", cameraID, req.StartTime, req.EndTime, minutes, err)
		errorResponse(c, tick, http.StatusInternalServerError, "Failed to fetch rollup data")
		return
	}

	rollupRows := make([]RollupRow, len(rows))
	for i, row := range rows {
		rollupRows[i] = RollupRow{
			Time: formatTime(row.Time),
		}
		if row.SumLength != 0 {
			sum := row.SumLength
			rollupRows[i].SumLength = &sum
		}
	}

	startDt := utcNanosecondsToTime(req.StartTime)
	endDt := utcNanosecondsToTime(req.EndTime)

	successResponse(c, tick, GetCameraRollupResponse{
		Camera:      cameraID,
		Minutes:     minutes,
		StartTimeNs: req.StartTime,
		EndTimeNs:   req.EndTime,
		Start:       formatTime(startDt),
		End:         formatTime(endDt),
		Rows:        rollupRows,
	})
}

// utcNanosecondsToTime converts UTC nanoseconds to time.Time.
// GetCameraEvents handles GET /api/camera_events.
// {table}_event 테이블에서 시간 범위로 이벤트 조회.
// camera_id가 없으면 전체 카메라의 이벤트를 조회.
func (h *Handler) GetCameraEvents(c *gin.Context) {
	tick := time.Now()

	cameraID := c.Query("camera_id")

	startTimeStr := c.Query("start_time")
	endTimeStr := c.Query("end_time")
	if startTimeStr == "" || endTimeStr == "" {
		errorResponse(c, tick, http.StatusBadRequest, "start_time and end_time are required (nanoseconds)")
		return
	}

	var startNs, endNs int64
	if _, err := fmt.Sscanf(startTimeStr, "%d", &startNs); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "invalid start_time")
		return
	}
	if _, err := fmt.Sscanf(endTimeStr, "%d", &endNs); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "invalid end_time")
		return
	}

	if startNs >= endNs {
		errorResponse(c, tick, http.StatusBadRequest, "start_time must be earlier than end_time")
		return
	}

	// Optional filters + pagination (default: size=100, page=1)
	eventName := c.Query("event_name")
	eventTypeStr := c.Query("event_type")
	sizeStr := c.Query("size")
	pageStr := c.Query("page")

	var size, page int
	if sizeStr != "" {
		if _, err := fmt.Sscanf(sizeStr, "%d", &size); err != nil || size < 0 {
			errorResponse(c, tick, http.StatusBadRequest, "invalid size")
			return
		}
	}
	if pageStr != "" {
		if _, err := fmt.Sscanf(pageStr, "%d", &page); err != nil || page < 0 {
			errorResponse(c, tick, http.StatusBadRequest, "invalid page")
			return
		}
	}

	limit, offset := paginationValid(size, page)
	filter := &db.CameraEventFilter{CameraID: cameraID, EventName: eventName, Limit: limit, Offset: offset}
	if eventTypeStr != "" {
		var eventType float64
		switch eventTypeStr {
		case "MATCH":
			eventType = 2
		case "TRIGGER":
			eventType = 1
		case "RESOLVE":
			eventType = 0
		case "ERROR":
			eventType = -1
		default:
			errorResponse(c, tick, http.StatusBadRequest, "invalid event_type: use MATCH, TRIGGER, RESOLVE, ERROR")
			return
		}
		filter.EventType = &eventType
	}

	// 조회할 테이블 목록 수집 (중복 제거)
	tables := make(map[string]bool)
	if cameraID != "" {
		config := h.getCameraConfig(cameraID)
		if config == nil {
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", cameraID))
			return
		}
		tables[config.Table] = true
	} else {
		h.configMu.RLock()
		for _, config := range h.cameraConfigs {
			tables[config.Table] = true
		}
		h.configMu.RUnlock()
	}

	ctx := c.Request.Context()
	var allRows []db.CameraEventQueryRow
	for table := range tables {
		rows, err := h.machbase.QueryCameraEvents(ctx, table, startNs, endNs, filter)
		if err != nil {
			logger.GetLogger().Errorf("GetCameraEvents: failed to query %s_event (start=%d, end=%d): %v", table, startNs, endNs, err)
			continue
		}
		allRows = append(allRows, rows...)
	}

	type eventRow struct {
		Name               string  `json:"name"`
		Time               string  `json:"time"`
		Value              float64 `json:"value"`
		ValueLabel         string  `json:"value_label"`
		ExpressionText     string  `json:"expression_text"`
		UsedCountsSnapshot string  `json:"used_counts_snapshot"`
		CameraID           string  `json:"camera_id"`
		RuleID             string  `json:"rule_id"`
		RuleName           string  `json:"rule_name"`
	}

	events := make([]eventRow, len(allRows))
	for i, r := range allRows {
		label := ""
		switch r.Value {
		case 2:
			label = "MATCH"
		case 1:
			label = "TRIGGER"
		case 0:
			label = "RESOLVE"
		case -1:
			label = "ERROR"
		}
		events[i] = eventRow{
			Name:               r.Name,
			Time:               formatTime(r.Time),
			Value:              r.Value,
			ValueLabel:         label,
			ExpressionText:     r.ExpressionText,
			UsedCountsSnapshot: r.UsedCountsSnapshot,
			CameraID:           r.CameraID,
			RuleID:             r.RuleID,
			RuleName:           r.RuleName,
		}
	}

	// 총 건수 조회 (페이지네이션 없이 같은 필터)
	var totalCount int64
	for table := range tables {
		cnt, err := h.machbase.CountCameraEvents(ctx, table, startNs, endNs, filter)
		if err != nil {
			logger.GetLogger().Errorf("GetCameraEvents: failed to count %s_event: %v", table, err)
			continue
		}
		totalCount += cnt
	}

	totalPages := int64(0)
	if limit > 0 {
		totalPages = (totalCount + int64(limit) - 1) / int64(limit)
	}

	// 마지막 이벤트 조회 시간 갱신: 조회된 데이터 중 가장 최신 시간 사용 (DESC 정렬이므로 첫 번째)
	if len(allRows) > 0 {
		latestNs := allRows[0].Time.UnixNano()
		h.lastEventQueryTimeMu.Lock()
		if latestNs > h.lastEventQueryTime {
			h.lastEventQueryTime = latestNs
			h.saveState()
		}
		h.lastEventQueryTimeMu.Unlock()
	}

	successResponse(c, tick, map[string]any{
		"events":      events,
		"total_count": totalCount,
		"total_pages": totalPages,
	})
}

// GetCameraEventCount handles GET /api/camera_events/count.
// 마지막 이벤트 조회 시간부터 현재까지의 이벤트 개수를 반환.
// 마지막 조회 시간이 없는 경우(최초 실행 등) 전체 이벤트 개수를 반환.
func (h *Handler) GetCameraEventCount(c *gin.Context) {
	tick := time.Now()

	h.lastEventQueryTimeMu.Lock()
	startNs := h.lastEventQueryTime
	h.lastEventQueryTimeMu.Unlock()

	endNs := time.Now().UnixNano()

	// 조회할 테이블 목록 수집 (중복 제거)
	tables := make(map[string]bool)
	h.configMu.RLock()
	for _, config := range h.cameraConfigs {
		tables[config.Table] = true
	}
	h.configMu.RUnlock()

	ctx := c.Request.Context()
	var total int64
	for table := range tables {
		count, err := h.machbase.CountCameraEvents(ctx, table, startNs, endNs, nil)
		if err != nil {
			logger.GetLogger().Errorf("GetCameraEventCount: failed to count %s_event: %v", table, err)
			continue
		}
		total += count
	}

	// 조회 시간 갱신: 다음 count 호출 시 새 이벤트만 카운트
	h.lastEventQueryTimeMu.Lock()
	if endNs > h.lastEventQueryTime {
		h.lastEventQueryTime = endNs
		h.saveState()
	}
	h.lastEventQueryTimeMu.Unlock()

	successResponse(c, tick, map[string]any{
		"count": total,
	})
}

func paginationValid(size int, page int) (int, int) {
	if page > 0 {
		page = page - 1
	}
	if size == 0 {
		return 100, 100 * page
	}
	return size, size * page
}

func utcNanosecondsToTime(ns int64) time.Time {
	sec := ns / 1_000_000_000
	nsec := ns % 1_000_000_000
	return time.Unix(sec, nsec).Local()
}

// GetDataGaps handles GET /api/data_gaps.
// 5초 간격으로 데이터를 조회하여 빠진 시간대(gap)를 반환합니다.
func (h *Handler) GetDataGaps(c *gin.Context) {
	tick := time.Now()

	var req struct {
		CameraID  string `form:"camera_id" binding:"required"`
		StartTime string `form:"start_time" binding:"required"` // RFC3339 format
		EndTime   string `form:"end_time" binding:"required"`   // RFC3339 format
		Interval  int    `form:"interval"`                      // seconds (default: 5)
	}

	if err := c.ShouldBindQuery(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "Missing required parameters: camera_id, start_time, end_time")
		return
	}

	// Set default interval if not provided or invalid
	if req.Interval <= 0 {
		req.Interval = 5
	}

	// Get camera config to retrieve table name
	config := h.getCameraConfig(req.CameraID)
	if config == nil {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("Camera '%s' not found", req.CameraID))
		return
	}

	// Parse time strings
	startTime, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		errorResponse(c, tick, http.StatusBadRequest, fmt.Sprintf("Invalid start_time format (use RFC3339): %v", err))
		return
	}

	endTime, err := time.Parse(time.RFC3339, req.EndTime)
	if err != nil {
		errorResponse(c, tick, http.StatusBadRequest, fmt.Sprintf("Invalid end_time format (use RFC3339): %v", err))
		return
	}

	if endTime.Before(startTime) {
		errorResponse(c, tick, http.StatusBadRequest, "end_time must be after start_time")
		return
	}

	// Get rollup data from Machbase
	ctx := c.Request.Context()
	tableName := config.Table
	data, err := h.machbase.GetRollupData(ctx, tableName, req.CameraID, startTime, endTime, req.Interval)
	if err != nil {
		logger.GetLogger().Errorf("GetDataGaps: failed to query rollup data: %v", err)
		errorResponse(c, tick, http.StatusInternalServerError, fmt.Sprintf("Failed to query data: %v", err))
		return
	}

	// Create a map of existing timestamps
	existingTimes := make(map[int64]bool)
	for _, row := range data {
		existingTimes[row.Time.Unix()] = true
	}

	// Generate expected intervals
	var gaps []string
	intervalDuration := time.Duration(req.Interval) * time.Second
	startUnix := startTime.Unix()

	// Machbase rollup origin을 실제 데이터에서 추론
	var startAligned int64
	if len(data) > 0 {
		// 첫 번째 데이터를 기준으로 rollup origin offset 계산
		firstTime := data[0].Time.Unix()
		originOffset := firstTime % int64(req.Interval)

		// start_time을 rollup 경계로 정렬 (Machbase origin 고려)
		delta := startUnix - originOffset
		startAligned = (delta/int64(req.Interval))*int64(req.Interval) + originOffset
		if startAligned < startUnix {
			startAligned += int64(req.Interval)
		}
	} else {
		// 데이터가 없으면 Unix epoch 기준으로 계산
		startAligned = (startUnix / int64(req.Interval)) * int64(req.Interval)
	}

	currentTime := time.Unix(startAligned, 0)

	for currentTime.Before(endTime) || currentTime.Equal(endTime) {
		// start_time 이후의 시간만 gap에 추가 (초 단위 비교, 밀리초 무시)
		if !existingTimes[currentTime.Unix()] && currentTime.Unix() >= startUnix {
			gaps = append(gaps, formatTime(currentTime))
		}
		currentTime = currentTime.Add(intervalDuration)
	}

	// If no gaps, return empty array
	if gaps == nil {
		gaps = []string{}
	}

	successResponse(c, tick, map[string]any{
		"camera_id":     req.CameraID,
		"start_time":    req.StartTime,
		"end_time":      req.EndTime,
		"interval":      req.Interval,
		"total_gaps":    len(gaps),
		"missing_times": gaps,
	})
}
