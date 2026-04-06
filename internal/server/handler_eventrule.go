package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/machbase/neo-pkg-bbox/internal/logger"

	"github.com/gin-gonic/gin"
)

// GetEventRules handles GET /api/event_rule/:camera_id.
// 특정 카메라의 모든 EventRule 배열을 조회.
func (h *Handler) GetEventRules(c *gin.Context) {
	tick := time.Now()

	cameraID := c.Param("camera_id")
	if cameraID == "" {
		errorResponse(c, tick, http.StatusBadRequest, "camera_id is required")
		return
	}

	// 카메라 설정 파일 읽기
	cameraPath := filepath.Join(h.cameraDir, cameraID+".json")
	data, err := os.ReadFile(cameraPath)
	if err != nil {
		if os.IsNotExist(err) {
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", cameraID))
			return
		}
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read camera config")
		return
	}

	var camera CameraCreateRequest
	if err := json.Unmarshal(data, &camera); err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to parse camera config")
		return
	}

	// nil slice를 빈 배열로 변환
	eventRules := camera.EventRule
	if eventRules == nil {
		eventRules = []EventRule{}
	}

	successResponse(c, tick, map[string]any{
		"camera_id":   cameraID,
		"event_rules": eventRules,
	})
}

// PostEventRulesRequest represents the request body for adding a new event rule.
type PostEventRulesRequest struct {
	CameraID string    `json:"camera_id" binding:"required"`
	Rule     EventRule `json:"rule" binding:"required"`
}

// PostEventRules handles POST /api/event_rule.
// 새로운 EventRule을 카메라 설정에 추가.
func (h *Handler) PostEventRules(c *gin.Context) {
	tick := time.Now()

	var req PostEventRulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "bad request parameter")
		return
	}

	// 카메라 설정 파일 읽기
	cameraPath := filepath.Join(h.cameraDir, req.CameraID+".json")
	data, err := os.ReadFile(cameraPath)
	if err != nil {
		if os.IsNotExist(err) {
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", req.CameraID))
			return
		}
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read camera config")
		return
	}

	var camera CameraCreateRequest
	if err := json.Unmarshal(data, &camera); err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to parse camera config")
		return
	}

	// rule_id 중복 체크
	for _, existing := range camera.EventRule {
		if existing.ID == req.Rule.ID {
			errorResponse(c, tick, http.StatusConflict, fmt.Sprintf("rule_id '%s' already exists", req.Rule.ID))
			return
		}
	}

	// expression_text를 소문자로 변환
	req.Rule.Expression = strings.ToLower(req.Rule.Expression)

	// record_mode 대문자 정규화 및 유효성 검사
	req.Rule.RecordMode = strings.ToUpper(req.Rule.RecordMode)
	if req.Rule.RecordMode != "ALL_MATCHES" && req.Rule.RecordMode != "EDGE_ONLY" {
		logger.GetLogger().Errorf("PostEventRules[%s]: invalid record_mode '%s'", req.CameraID, req.Rule.RecordMode)
		errorResponse(c, tick, http.StatusBadRequest, fmt.Sprintf("invalid record_mode '%s': must be 'ALL_MATCHES' or 'EDGE_ONLY'", req.Rule.RecordMode))
		return
	}

	// EventRule 추가
	camera.EventRule = append(camera.EventRule, req.Rule)

	// 이벤트룰 추가 시 event 테이블 생성
	if err := h.machbase.CreateCameraEventTable(c.Request.Context(), camera.Table); err != nil {
		logger.GetLogger().Warnf("PostEventRules[%s]: failed to create event table (may already exist): %v", req.CameraID, err)
		// 테이블 생성 실패해도 계속 진행 (이미 존재할 수 있음)
	}

	// 파일 저장
	cameraJSON, err := json.MarshalIndent(camera, "", "  ")
	if err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to marshal camera config")
		return
	}

	if err := os.WriteFile(cameraPath, cameraJSON, 0644); err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to write camera config file")
		return
	}

	// Event rules 캐시 갱신
	h.refreshCameraConfigCache(req.CameraID)

	successResponse(c, tick, map[string]any{
		"camera_id": req.CameraID,
		"rule":      req.Rule,
	})
}

// EventRuleUpdateRequest는 EventRule 수정 요청 구조체.
// rule_id는 URL path에서 받으므로 body에 포함하지 않음.
// Enabled는 false 값을 zero value와 구분하기 위해 *bool 사용.
type EventRuleUpdateRequest struct {
	Name       string `json:"name" binding:"required"`
	Expression string `json:"expression_text" binding:"required"`
	RecordMode string `json:"record_mode" binding:"required"`
	Enabled    *bool  `json:"enabled" binding:"required"`
}

// UpdateEventRules handles POST /api/event_rule/:camera_id/:rule_id.
// 기존 EventRule을 부분 수정 (전송된 필드만 업데이트).
func (h *Handler) UpdateEventRules(c *gin.Context) {
	tick := time.Now()

	// URL 파라미터에서 camera_id, rule_id 가져오기
	cameraID := c.Param("camera_id")
	ruleID := c.Param("rule_id")

	if cameraID == "" {
		errorResponse(c, tick, http.StatusBadRequest, "camera_id is required")
		return
	}
	if ruleID == "" {
		errorResponse(c, tick, http.StatusBadRequest, "rule_id is required")
		return
	}

	var req EventRuleUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "bad request parameter")
		return
	}

	// 카메라 설정 파일 읽기
	cameraPath := filepath.Join(h.cameraDir, cameraID+".json")
	data, err := os.ReadFile(cameraPath)
	if err != nil {
		if os.IsNotExist(err) {
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", cameraID))
			return
		}
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read camera config")
		return
	}

	var camera CameraCreateRequest
	if err := json.Unmarshal(data, &camera); err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to parse camera config")
		return
	}

	// rule_id 찾기
	ruleIdx := -1
	for i, existing := range camera.EventRule {
		if existing.ID == ruleID {
			ruleIdx = i
			break
		}
	}

	if ruleIdx == -1 {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("rule_id '%s' not found", ruleID))
		return
	}

	// expression_text를 소문자로 변환
	req.Expression = strings.ToLower(req.Expression)

	// record_mode 대문자 정규화 및 유효성 검사
	req.RecordMode = strings.ToUpper(req.RecordMode)
	if req.RecordMode != "ALL_MATCHES" && req.RecordMode != "EDGE_ONLY" {
		logger.GetLogger().Errorf("UpdateEventRules[%s/%s]: invalid record_mode '%s'", cameraID, ruleID, req.RecordMode)
		errorResponse(c, tick, http.StatusBadRequest, fmt.Sprintf("invalid record_mode '%s': must be 'ALL_MATCHES' or 'EDGE_ONLY'", req.RecordMode))
		return
	}

	// 기존 rule 업데이트
	rule := camera.EventRule[ruleIdx]
	rule.Name = req.Name
	rule.Expression = req.Expression
	rule.RecordMode = req.RecordMode
	rule.Enabled = *req.Enabled

	camera.EventRule[ruleIdx] = rule

	// 파일 저장
	cameraJSON, err := json.MarshalIndent(camera, "", "  ")
	if err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to marshal camera config")
		return
	}

	if err := os.WriteFile(cameraPath, cameraJSON, 0644); err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to write camera config file")
		return
	}

	// rule_name 메타데이터 업데이트: 파일 저장 후 Machbase 쪽 메타컬럼도 동기화
	// 실패해도 파일은 이미 저장됐으므로 경고만 남기고 계속 진행
	if camera.Table != "" {
		if err := h.machbase.UpdateEventRuleName(c.Request.Context(), camera.Table, cameraID, ruleID, rule.Name); err != nil {
			logger.GetLogger().Warnf("UpdateEventRules[%s/%s]: failed to update rule_name in Machbase: %v", cameraID, ruleID, err)
		}
	}

	// Event rules 캐시 갱신
	h.refreshCameraConfigCache(cameraID)

	successResponse(c, tick, map[string]any{
		"camera_id": cameraID,
		"rule":      rule,
	})
}

// DeleteEventRules handles DELETE /api/event_rule/:camera_id/:rule_id.
// 특정 EventRule을 삭제.
func (h *Handler) DeleteEventRules(c *gin.Context) {
	tick := time.Now()

	cameraID := c.Param("camera_id")
	ruleID := c.Param("rule_id")

	if cameraID == "" {
		errorResponse(c, tick, http.StatusBadRequest, "camera_id is required")
		return
	}
	if ruleID == "" {
		errorResponse(c, tick, http.StatusBadRequest, "rule_id is required")
		return
	}

	// 카메라 설정 파일 읽기
	cameraPath := filepath.Join(h.cameraDir, cameraID+".json")
	data, err := os.ReadFile(cameraPath)
	if err != nil {
		if os.IsNotExist(err) {
			errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("camera '%s' not found", cameraID))
			return
		}
		errorResponse(c, tick, http.StatusInternalServerError, "failed to read camera config")
		return
	}

	var camera CameraCreateRequest
	if err := json.Unmarshal(data, &camera); err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to parse camera config")
		return
	}

	// rule_id 찾아서 삭제
	found := false
	newRules := make([]EventRule, 0, len(camera.EventRule))
	for _, rule := range camera.EventRule {
		if rule.ID == ruleID {
			found = true
			continue // 이 rule은 스킵 (삭제)
		}
		newRules = append(newRules, rule)
	}

	if !found {
		errorResponse(c, tick, http.StatusNotFound, fmt.Sprintf("rule_id '%s' not found", ruleID))
		return
	}

	camera.EventRule = newRules

	// 파일 저장
	cameraJSON, err := json.MarshalIndent(camera, "", "  ")
	if err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to marshal camera config")
		return
	}

	if err := os.WriteFile(cameraPath, cameraJSON, 0644); err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, "failed to write camera config file")
		return
	}

	// EDGE_ONLY 상태 정리 (삭제된 rule의 이전 상태 제거)
	h.edgeMu.Lock()
	delete(h.edgeState, cameraID+"."+ruleID)
	h.edgeMu.Unlock()

	// Event rules 캐시 갱신
	h.refreshCameraConfigCache(cameraID)

	successResponse(c, tick, map[string]string{
		"camera_id": cameraID,
		"rule_id":   ruleID,
	})
}
