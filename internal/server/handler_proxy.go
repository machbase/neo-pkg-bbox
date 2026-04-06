package server

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ProxyMachbase godoc
// ANY /db/*path
// 프론트엔드 요청을 machbase-neo 로 중계한다.
// config.yaml 의 machbase.api_token 이 설정된 경우 Authorization: Bearer 헤더를 자동으로 추가한다.
func (h *Handler) ProxyMachbase(c *gin.Context) {
	// c.Param("path") 는 "/tql" 처럼 /db 가 빠진 부분만 반환하므로
	// 전체 경로 "/db/tql" 을 그대로 사용한다.
	path := c.Request.URL.Path

	resp, err := h.machbase.Forward(
		c.Request.Context(),
		c.Request.Method,
		path,
		c.Request.URL.RawQuery,
		c.Request.Body,
		c.GetHeader("Content-Type"),
	)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "reason": err.Error()})
		return
	}
	defer resp.Body.Close()

	// 응답 헤더 복사 (Content-Type 등)
	for key, values := range resp.Header {
		for _, v := range values {
			c.Header(key, v)
		}
	}

	c.Status(resp.StatusCode)
	io.Copy(c.Writer, resp.Body) //nolint:errcheck
}

// ProxyMachbaseWeb godoc
// ANY /web/*path
// /web/* 요청을 machbase-neo 로 중계한다.
// Content-Type, X-Chart-Type 헤더를 전달한다.
func (h *Handler) ProxyMachbaseWeb(c *gin.Context) {
	path := c.Request.URL.Path

	var extra []http.Header
	if auth := c.GetHeader("Authorization"); auth != "" {
		extra = append(extra, http.Header{"Authorization": {auth}})
	}

	resp, err := h.machbase.Forward(
		c.Request.Context(),
		c.Request.Method,
		path,
		c.Request.URL.RawQuery,
		c.Request.Body,
		c.GetHeader("Content-Type"),
		extra...,
	)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "reason": err.Error()})
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		for _, v := range values {
			c.Header(key, v)
		}
	}

	c.Status(resp.StatusCode)
	io.Copy(c.Writer, resp.Body) //nolint:errcheck
}
