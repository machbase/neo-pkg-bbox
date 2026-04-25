package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type PingRequest struct {
	IP      string `json:"ip" binding:"required"`
	Timeout int    `json:"timeout"` // seconds, max 5
}

type PingResult struct {
	IP      string  `json:"ip"`
	Alive   bool    `json:"alive"`
	Latency string  `json:"latency,omitempty"`
	Output  string  `json:"output,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// PingIP handles POST /api/cameras/ping
// Request: { "ip": "192.168.1.1", "timeout": 3 }
// timeout 기본값 5초, 최대 5초
func (h *Handler) PingIP(c *gin.Context) {
	tick := time.Now()

	var req PingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errorResponse(c, tick, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	h.doPing(c, tick, req.IP, req.Timeout)
}

// PingIPQuery handles GET /api/camera/ping?ip=192.168.1.1&timeout=3
// timeout 기본값 5초, 최대 5초
func (h *Handler) PingIPQuery(c *gin.Context) {
	tick := time.Now()

	ip := c.Query("ip")
	if ip == "" {
		errorResponse(c, tick, http.StatusBadRequest, "missing required query parameter: ip")
		return
	}

	timeout := 0
	if v := c.Query("timeout"); v != "" {
		t, err := strconv.Atoi(v)
		if err != nil {
			errorResponse(c, tick, http.StatusBadRequest, "invalid timeout: "+err.Error())
			return
		}
		timeout = t
	}

	h.doPing(c, tick, ip, timeout)
}

func (h *Handler) doPing(c *gin.Context, tick time.Time, ipRaw string, timeout int) {
	ip := strings.TrimSpace(ipRaw)

	// IP 유효성 검사 (command injection 방지)
	if net.ParseIP(ip) == nil {
		errorResponse(c, tick, http.StatusBadRequest, fmt.Sprintf("invalid ip address: %q", ip))
		return
	}

	if timeout <= 0 || timeout > 5 {
		timeout = 5
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	start := time.Now()
	cmd := buildPingCommand(ctx, ip, timeout)
	output, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	result := PingResult{
		IP:     ip,
		Output: strings.TrimSpace(string(output)),
	}

	if err != nil {
		result.Alive = false
		result.Error = err.Error()
	} else {
		result.Alive = true
		result.Latency = elapsed.String()
	}

	successResponse(c, tick, result)
}

// buildPingCommand: OS별 ping 옵션 차이를 흡수
//   - Windows : -n <count> -w <timeout(ms)>
//   - macOS   : -c <count> -W <timeout(ms)>
//   - Linux 등: -c <count> -W <timeout(s)>
func buildPingCommand(ctx context.Context, ip string, timeoutSec int) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		ms := strconv.Itoa(timeoutSec * 1000)
		return exec.CommandContext(ctx, "ping", "-n", "1", "-w", ms, ip)
	case "darwin":
		ms := strconv.Itoa(timeoutSec * 1000)
		return exec.CommandContext(ctx, "ping", "-c", "1", "-W", ms, ip)
	default:
		return exec.CommandContext(ctx, "ping", "-c", "1", "-W", strconv.Itoa(timeoutSec), ip)
	}
}
