package db

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/machbase/neo-pkg-blackbox/internal/config"
	"github.com/machbase/neo-pkg-blackbox/internal/logger"
)

// Machbase is a client for Machbase HTTP API.
type Machbase struct {
	baseURL  *url.URL
	client   *http.Client
	apiToken string
}

// NewMachbase creates a new Machbase client.
func NewMachbase(cfg config.MachbaseConfig) (*Machbase, error) {
	cfg.ApplyDefaults()

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	u, err := url.Parse(fmt.Sprintf("%s://%s:%d", cfg.Scheme, cfg.Host, cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("invalid machbase config: %w", err)
	}

	return &Machbase{
		baseURL:  u,
		client:   &http.Client{Timeout: timeout},
		apiToken: cfg.APIToken,
	}, nil
}

// Start initializes the Machbase client.
func (m *Machbase) Start() {}

const maxResponseBytes int64 = 8 << 20 // 8 MiB

// QueryResponse is the response from Machbase query API.
type QueryResponse struct {
	Success bool   `json:"success"`
	Reason  string `json:"reason"`
	Data    struct {
		Columns []string        `json:"columns"`
		Types   []string        `json:"types"`
		Rows    json.RawMessage `json:"rows"`
	} `json:"data"`
}

// QueryOption configures query behavior.
type QueryOption func(*queryConfig)

type queryConfig struct {
	timeformat string
}

// WithTimeformat sets the timeformat for the query.
func WithTimeformat(tf string) QueryOption {
	return func(c *queryConfig) {
		c.timeformat = tf
	}
}

// Query executes a query and returns the response.
func (m *Machbase) Query(ctx context.Context, sql string, opts ...QueryOption) (*QueryResponse, error) {
	cfg := &queryConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	u := m.baseURL.JoinPath("db", "query")

	q := u.Query()
	q.Set("q", sql)
	q.Set("rowsArray", "true")
	if cfg.timeformat != "" {
		q.Set("timeformat", cfg.timeformat)
	}
	u.RawQuery = q.Encode()

	logger.GetLogger().Debugf("Machbase SQL: %s", sql)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if m.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiToken)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, body)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, fmt.Errorf("response too large: limit %d bytes", maxResponseBytes)
	}

	var out QueryResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if !out.Success {
		return nil, fmt.Errorf("query failed: %s", out.Reason)
	}

	return &out, nil
}

// writeRequest is the request for Machbase write API.
type writeRequest struct {
	Data struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
	} `json:"data"`
}

// writeResponse is the response from Machbase write API.
type writeResponse struct {
	Success bool   `json:"success"`
	Reason  string `json:"reason"`
}

// WriteOption configures write behavior.
type WriteOption func(*writeConfig)

type writeConfig struct {
	timeformat string
	tz         string
	method     string
}

// WriteRows writes rows to a table.
func (m *Machbase) WriteRows(ctx context.Context, table string, columns []string, rows [][]any, opts ...WriteOption) error {
	cfg := &writeConfig{
		timeformat: "ns",
		tz:         "UTC",
		method:     "insert",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if table == "" {
		return fmt.Errorf("table is empty")
	}
	if len(columns) == 0 {
		return fmt.Errorf("columns is empty")
	}
	if len(rows) == 0 {
		return fmt.Errorf("rows is empty")
	}

	u := m.baseURL.JoinPath("db", "write", table)

	q := u.Query()
	q.Set("timeformat", cfg.timeformat)
	q.Set("tz", cfg.tz)
	q.Set("method", cfg.method)
	u.RawQuery = q.Encode()

	var payload writeRequest
	payload.Data.Columns = columns
	payload.Data.Rows = rows

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if m.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiToken)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("http %d: %s", resp.StatusCode, respBody)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var out writeResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	if !out.Success {
		return fmt.Errorf("write failed: %s", out.Reason)
	}

	return nil
}

// Forward proxies an arbitrary request to the machbase server and returns the raw response.
// The caller is responsible for closing the response body.
// extraHeaders를 통해 전달된 헤더(Authorization 등)를 그대로 machbase 서버로 전달한다.
// apiToken이 설정된 경우 Authorization 헤더를 추가한다.
func (m *Machbase) Forward(ctx context.Context, method, path string, rawQuery string, body io.Reader, contentType string, extraHeaders ...http.Header) (*http.Response, error) {
	u := m.baseURL.JoinPath(path)
	u.RawQuery = rawQuery

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, h := range extraHeaders {
		for key, values := range h {
			for _, v := range values {
				req.Header.Set(key, v)
			}
		}
	}

	if m.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiToken)
	}

	return m.client.Do(req)
}

// BaseURL returns a copy of the base URL string (scheme://host:port).
func (m *Machbase) BaseURL() string {
	return m.baseURL.String()
}

// Helper functions

func escapeSQLLiteral(v string) string {
	return strings.ReplaceAll(v, "'", "''")
}
