package mediamtx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/machbase/neo-pkg-bbox/internal/logger"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config는 MediaMTX 서버 설정
type Config struct {
	Binary     string   // MediaMTX 실행 파일 경로
	ConfigFile string   // 설정 파일 경로 (비어있으면 바이너리와 같은 디렉토리에서 자동 탐색)
	Port       int      // MediaMTX HTTP API 포트 (기본: 9997)
	Args       []string // 추가 실행 인자
}

// Runner는 MediaMTX 미디어 서버 관리
type Runner struct {
	cfg    Config
	logDir string
	cmd    *exec.Cmd
	exited chan struct{} // closed when the current process exits
	mu     sync.Mutex
}

// ServerStatus는 서버 상태 정보
type ServerStatus struct {
	Running bool
	PID     int
}

// New는 새로운 MediaMTX Runner 생성
func New(cfg Config, logDir string) *Runner {
	return &Runner{
		cfg:    cfg,
		logDir: logDir,
	}
}

// Run은 MediaMTX를 시작하고 ctx가 취소될 때까지 대기한 후 종료한다.
// 프로세스가 예기치않게 종료되면 backoff 후 자동으로 재시작한다.
// binary가 설정되지 않으면 즉시 반환한다 (외부 서버 사용).
// main.go의 errgroup에서 g.Go(runner.Run) 형태로 사용한다.
func (r *Runner) Run(ctx context.Context) error {
	if r.cfg.Binary == "" {
		logger.GetLogger().Info("[mediamtx] binary not configured, using external server")
		return nil
	}

	const (
		initBackoff = 3 * time.Second
		maxBackoff  = 30 * time.Second
		stableTime  = 1 * time.Minute // 이 시간 이상 실행됐으면 backoff 리셋
	)
	backoff := initBackoff

	for {
		startTime := time.Now()

		if err := r.Start(); err != nil {
			logger.GetLogger().Warnf("[mediamtx] failed to start: %v, retrying in %v...", err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
				if backoff < maxBackoff {
					backoff *= 2
				}
			}
			continue
		}

		r.mu.Lock()
		exited := r.exited
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return r.Stop()
		case <-exited:
			if ctx.Err() != nil {
				// ctx 취소로 인한 종료 — 재시작 불필요
				return nil
			}
			uptime := time.Since(startTime)
			if uptime >= stableTime {
				backoff = initBackoff // 충분히 오래 실행됐으면 backoff 리셋
			}
			logger.GetLogger().Warnf("[mediamtx] exited unexpectedly (uptime: %v), restarting in %v...", uptime.Round(time.Second), backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
				if backoff < maxBackoff {
					backoff *= 2
				}
			}
		}
	}
}

// Start는 로컬 MediaMTX 서버를 시작한다.
func (r *Runner) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cmd != nil && r.cmd.Process != nil {
		return fmt.Errorf("server already running (PID: %d)", r.cmd.Process.Pid)
	}

	execArgs := r.buildExecArgs()
	logger.GetLogger().Infof("[mediamtx] starting: %s", prettyCommand(r.cfg.Binary, execArgs))

	cmd := exec.Command(r.cfg.Binary, execArgs...)

	// API 활성화 환경변수 주입 (mediamtx.yml에 api: yes 설정이 없어도 동작하도록 보험)
	apiPort := r.cfg.Port
	if apiPort == 0 {
		apiPort = 9997
	}
	cmd.Env = append(os.Environ(),
		"MTX_API=yes",
		fmt.Sprintf("MTX_APIADDRESS=:%d", apiPort),
	)

	// 로그 파일 설정
	if err := os.MkdirAll(r.logDir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logPath := filepath.Join(r.logDir, "mediamtx.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	setPdeathsig(cmd) // 부모 프로세스 종료 시 SIGTERM 전달 (Linux)

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start MediaMTX: %w", err)
	}

	r.cmd = cmd
	exited := make(chan struct{})
	r.exited = exited
	logger.GetLogger().Infof("[mediamtx] started (PID: %d, log: %s)", cmd.Process.Pid, logPath)

	// 백그라운드에서 프로세스 종료 대기
	go func() {
		defer logFile.Close()
		defer close(exited)
		err := cmd.Wait()
		r.mu.Lock()
		r.cmd = nil
		r.mu.Unlock()

		if err != nil {
			logger.GetLogger().Warnf("[mediamtx] exited: %v", err)
		} else {
			logger.GetLogger().Info("[mediamtx] stopped")
		}
	}()

	return nil
}

// Stop은 실행 중인 MediaMTX 서버를 SIGTERM으로 종료한다.
// 5초 내에 종료되지 않으면 SIGKILL로 강제 종료한다.
func (r *Runner) Stop() error {
	r.mu.Lock()

	if r.cmd == nil || r.cmd.Process == nil {
		r.mu.Unlock()
		return nil
	}

	pid := r.cmd.Process.Pid
	proc := r.cmd.Process
	r.mu.Unlock()

	logger.GetLogger().Infof("[mediamtx] stopping (PID: %d)", pid)

	if err := proc.Signal(sigterm()); err != nil {
		return nil // 이미 종료된 경우 무시
	}

	// Start()의 goroutine이 cmd.Wait() 후 cmd=nil 로 설정할 때까지 폴링
	done := make(chan struct{})
	go func() {
		for {
			r.mu.Lock()
			stopped := (r.cmd == nil)
			r.mu.Unlock()
			if stopped {
				close(done)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	select {
	case <-time.After(5 * time.Second):
		logger.GetLogger().Warnf("[mediamtx] did not stop in 5s, killing (PID: %d)", pid)
		proc.Kill()
		<-done
	case <-done:
	}

	logger.GetLogger().Infof("[mediamtx] stopped (PID: %d)", pid)
	return nil
}

// Status는 현재 서버 상태를 반환한다.
func (r *Runner) Status() ServerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cmd != nil && r.cmd.Process != nil {
		return ServerStatus{Running: true, PID: r.cmd.Process.Pid}
	}
	return ServerStatus{}
}

// buildExecArgs는 실행 인자를 생성한다.
// ConfigFile이 비어있으면 바이너리와 같은 디렉토리에서 mediamtx.yml을 자동 탐색한다.
func (r *Runner) buildExecArgs() []string {
	var args []string

	configFile := r.cfg.ConfigFile
	if configFile != "" {
		// 명시적으로 지정된 경우 절대 경로로 변환
		if abs, err := filepath.Abs(configFile); err == nil {
			configFile = abs
		}
	} else {
		// 상대 경로를 절대 경로로 변환 후 config 파일 탐색
		binaryAbs, err := filepath.Abs(r.cfg.Binary)
		if err != nil {
			binaryAbs = r.cfg.Binary
		}
		candidate := filepath.Join(filepath.Dir(binaryAbs), "mediamtx.yml")
		if _, err := os.Stat(candidate); err == nil {
			configFile = candidate
			logger.GetLogger().Infof("[mediamtx] auto-detected config: %s", configFile)
		}
	}
	if configFile != "" {
		args = append(args, configFile)
	}

	args = append(args, r.cfg.Args...)
	return args
}

// prettyCommand는 명령어를 보기 좋게 포맷팅한다.
func prettyCommand(binary string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(binary))
	for _, arg := range args {
		parts = append(parts, " "+shellQuote(arg))
	}
	return strings.Join(parts, "")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	needQuote := false
	for _, c := range s {
		if c == ' ' || c == '\n' || c == '\t' || c == '\'' || c == '"' || c == '\\' {
			needQuote = true
			break
		}
	}
	if !needQuote {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// HeartbeatResponse represents MediaMTX API response
type HeartbeatResponse struct {
	Status  string                 `json:"status"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Message string                 `json:"message,omitempty"`
}

// Heartbeat checks if MediaMTX server is alive using HTTP API
func Heartbeat(ctx context.Context, mediaURL string, timeout time.Duration) (*HeartbeatResponse, error) {
	if mediaURL == "" {
		return nil, fmt.Errorf("media URL is empty")
	}

	parsedURL, err := url.Parse(mediaURL)
	if err != nil {
		return nil, fmt.Errorf("invalid media URL: %w", err)
	}

	apiURL := fmt.Sprintf("http://%s:9997/v3/config/global/get", parsedURL.Hostname())

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return &HeartbeatResponse{
			Status:  "error",
			Message: fmt.Sprintf("connection failed: %v", err),
		}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &HeartbeatResponse{
			Status:  "unhealthy",
			Message: fmt.Sprintf("HTTP %d", resp.StatusCode),
		}, fmt.Errorf("unhealthy: status code %d", resp.StatusCode)
	}

	var apiResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return &HeartbeatResponse{Status: "healthy", Message: "server responded"}, nil
	}

	return &HeartbeatResponse{Status: "healthy", Data: apiResp}, nil
}

// RunCommand는 일회성 명령 실행
func (r *Runner) RunCommand(ctx context.Context, args ...string) (string, error) {
	if r.cfg.Binary == "" {
		return "", fmt.Errorf("binary path not configured")
	}

	var outBuf, errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, r.cfg.Binary, args...)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("command failed: %w, stderr: %s", err, errBuf.String())
	}
	return outBuf.String(), nil
}
