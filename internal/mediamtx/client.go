package mediamtx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/machbase/neo-pkg-bbox/internal/config"
)

// Client는 MediaMTX HTTP API 클라이언트
type Client struct {
	baseURL *url.URL
	http    *http.Client
}

// NewClient는 MediamtxConfig로 Client를 생성한다.
func NewClient(cfg config.MediamtxConfig) *Client {
	cfg.ApplyDefaults()
	u := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
	}
	return &Client{
		baseURL: u,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// PathSource는 MediaMTX path의 source 프로토콜 상수
type PathSource string

const (
	PathSourceTCP       PathSource = "tcp"
	PathSourceUDP       PathSource = "udp"
	PathSourceMulticast PathSource = "multicast"
)

// PathConfig는 MediaMTX path 설정
// MediaMTX v3 API /v3/config/paths/add/{name} body
type PathConfig struct {
	// Source: 외부 스트림 URL (예: rtsp://192.168.0.87:8554/live)
	// 비어있으면 publisher가 직접 push하는 path로 동작
	Source string `json:"source,omitempty"`

	// SourceProtocol: RTSP transport 방식 (tcp, udp, multicast)
	SourceProtocol PathSource `json:"sourceProtocol,omitempty"`

	// SourceOnDemand: true이면 reader가 접속할 때만 source를 pull
	SourceOnDemand bool `json:"sourceOnDemand,omitempty"`

	// Record: 스트림 파일 녹화 여부
	Record bool `json:"record,omitempty"`
}

// AddPath는 MediaMTX에 path를 추가한다.
// 이미 존재하는 path면 에러를 반환한다.
func (c *Client) AddPath(ctx context.Context, name string, cfg PathConfig) error {
	return c.doPathRequest(ctx, http.MethodPost, "add", name, cfg)
}

// UpdatePath는 기존 path 설정을 덮어쓴다.
func (c *Client) UpdatePath(ctx context.Context, name string, cfg PathConfig) error {
	return c.doPathRequest(ctx, http.MethodPatch, "patch", name, cfg)
}

// RemovePath는 MediaMTX에서 path를 삭제한다.
func (c *Client) RemovePath(ctx context.Context, name string) error {
	u := c.baseURL.JoinPath("v3", "config", "paths", "delete", name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	return c.doRaw(req)
}

// GetPath는 path 설정을 조회한다.
func (c *Client) GetPath(ctx context.Context, name string) (*PathConfig, error) {
	u := c.baseURL.JoinPath("v3", "config", "paths", "get", name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, body)
	}

	var out PathConfig
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &out, nil
}

// AddOrUpdatePath는 path가 없으면 추가, 있으면 업데이트한다.
func (c *Client) AddOrUpdatePath(ctx context.Context, name string, cfg PathConfig) error {
	existing, err := c.GetPath(ctx, name)
	if err != nil {
		return fmt.Errorf("get path: %w", err)
	}
	if existing == nil {
		return c.AddPath(ctx, name, cfg)
	}
	return c.UpdatePath(ctx, name, cfg)
}

// PathStatus는 실행 중인 path의 실시간 상태
// GET /v3/paths/get/{name} 응답
type PathStatus struct {
	Name    string `json:"name"`
	// Ready: source가 연결되어 스트림이 흐르는 상태
	Ready   bool   `json:"ready"`
	// Tracks: 연결된 트랙 수 (video/audio)
	Tracks  []any  `json:"tracks"`
	// Readers: 현재 수신 중인 클라이언트 수
	Readers []any  `json:"readers"`
}

// GetPathStatus는 path의 실시간 상태를 조회한다.
// path가 등록되지 않았거나 source가 연결되지 않은 경우 Ready=false.
// path 자체가 존재하지 않으면 nil을 반환한다.
func (c *Client) GetPathStatus(ctx context.Context, name string) (*PathStatus, error) {
	u := c.baseURL.JoinPath("v3", "paths", "get", name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, body)
	}

	var out PathStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &out, nil
}

// WaitPathReady는 path가 Ready 상태가 될 때까지 폴링한다.
// ctx로 전체 타임아웃을 제어한다 (예: context.WithTimeout(ctx, 10*time.Second)).
// Ready가 되면 *PathStatus를 반환하고, 타임아웃 전에 Ready가 안 되면 에러를 반환한다.
func (c *Client) WaitPathReady(ctx context.Context, name string, interval time.Duration) (*PathStatus, error) {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for path %q to become ready: %w", name, ctx.Err())
		case <-ticker.C:
			status, err := c.GetPathStatus(ctx, name)
			if err != nil {
				return nil, err
			}
			if status != nil && status.Ready {
				return status, nil
			}
		}
	}
}

// doPathRequest는 add/patch 공통 요청 처리
func (c *Client) doPathRequest(ctx context.Context, method, action, name string, cfg PathConfig) error {
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	u := c.baseURL.JoinPath("v3", "config", "paths", action, name)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doRaw(req)
}

// doRaw는 응답 상태 코드만 확인한다.
func (c *Client) doRaw(req *http.Request) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("http %d: %s", resp.StatusCode, body)
	}
	return nil
}
