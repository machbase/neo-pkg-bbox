package ffmpeg

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"github.com/machbase/neo-pkg-bbox/internal/config"
	"github.com/machbase/neo-pkg-bbox/internal/logger"
	"os/exec"
	"strconv"
	"strings"
)

type FFmpegRunner struct {
	cfg    config.FFmpegConfig
	logDir string
}

func New(cfg config.FFmpegConfig, logDir string) *FFmpegRunner {
	return &FFmpegRunner{
		cfg:    cfg,
		logDir: logDir,
	}
}

type SegmentTiming struct {
	StartPTS float64
	LastPTS  float64
	LastDur  float64
	EndPTS   float64 // LastPTS + LastDur
	Length   float64
}

func (r *FFmpegRunner) ProbeConcatPacketTiming(ctx context.Context, initFile string, chunkFile string) (SegmentTiming, error) {
	probeArgs := r.buildProbeArgs(initFile, chunkFile)

	// Resolve ffprobe binary: use config if set, otherwise system PATH
	ffprobeBin := "ffprobe"
	if r.cfg.Defaults.ProbeBinary != "" {
		ffprobeBin = r.cfg.Defaults.ProbeBinary
	}

	logger.GetLogger().Debugf("ffprobe command: %s\n", prettyCommand(ffprobeBin, probeArgs))

	var errBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, ffprobeBin, probeArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return SegmentTiming{}, err
	}
	cmd.Stderr = &errBuf

	if err := cmd.Start(); err != nil {
		return SegmentTiming{}, err
	}

	sc := bufio.NewScanner(stdout)
	var firstLine, lastLine, prevLine string
	for sc.Scan() {
		line := sc.Text()
		if firstLine == "" {
			firstLine = line
		}
		prevLine = lastLine
		lastLine = line
	}
	scErr := sc.Err()

	waitErr := cmd.Wait()
	if scErr != nil {
		return SegmentTiming{}, scErr
	}
	if waitErr != nil {
		return SegmentTiming{}, fmt.Errorf("ffprobe failed: %v, stderr=%s", waitErr, errBuf.String())
	}
	if firstLine == "" || lastLine == "" {
		return SegmentTiming{}, fmt.Errorf("ffprobe produced no packet lines; stderr=%s", errBuf.String())
	}

	start, err := parseCSVFloat(firstLine, 0)
	if err != nil {
		return SegmentTiming{}, fmt.Errorf("parse start pts: %w", err)
	}
	lastPts, err := parseCSVFloat(lastLine, 0)
	if err != nil {
		return SegmentTiming{}, fmt.Errorf("parse last pts: %w", err)
	}
	// 마지막 패킷의 duration이 N/A인 경우 직전 패킷의 duration으로 대체
	lastDur, err := parseCSVFloat(lastLine, 1)
	if err != nil && prevLine != "" {
		lastDur, err = parseCSVFloat(prevLine, 1)
	}
	if err != nil {
		return SegmentTiming{}, fmt.Errorf("parse last dur: %w", err)
	}
	endPts := lastPts + lastDur
	length := endPts - start

	return SegmentTiming{
		StartPTS: start,
		LastPTS:  lastPts,
		LastDur:  lastDur,
		EndPTS:   endPts,
		Length:   length,
	}, nil
}

func parseCSVFloat(line string, field int) (float64, error) {
	a, b, ok := strings.Cut(line, ",")
	if !ok {
		return 0, fmt.Errorf("bad csv line: %q", line)
	}

	var s string
	if field == 0 {
		s = a
	} else {
		s = b
	}

	if s == "N/A" || s == "" {
		return 0, fmt.Errorf("missing value in line: %q", line)
	}

	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parse float %q: %v", s, err)
	}

	return v, nil
}

// ProbeRTSP checks if an RTSP server is reachable via TCP dial.
// This avoids conflicting with existing consumers of the same RTSP stream.
func (r *FFmpegRunner) ProbeRTSP(ctx context.Context, rtspURL string) error {
	u, err := url.Parse(rtspURL)
	if err != nil {
		return fmt.Errorf("invalid RTSP URL: %w", err)
	}

	host := u.Host
	if host == "" {
		return fmt.Errorf("invalid RTSP URL: missing host")
	}
	// default RTSP port
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "554")
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", host)
	if err != nil {
		return fmt.Errorf("cannot reach RTSP server at %s: %w", host, err)
	}
	conn.Close()
	return nil
}

func (r *FFmpegRunner) buildProbeArgs(initFile string, chunkFile string) []string {
	var args []string
	if len(r.cfg.Defaults.ProbeArgs) > 0 {
		for _, kv := range r.cfg.Defaults.ProbeArgs {
			args = append(args, "-"+kv.Flag, kv.Value)
		}
	} else {
		// fallback defaults
		args = []string{
			"-v", "error",
			"-select_streams", "v:0",
			"-show_entries", "packet=pts_time,duration_time",
			"-of", "csv=p=0",
		}
	}
	args = append(args, fmt.Sprintf("concat:%s|%s", initFile, chunkFile))
	return args
}

func prettyCommand(b string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(b))

	for _, a := range args {
		qa := shellQuote(a)

		if strings.HasPrefix(a, "-") {
			parts = append(parts, " \\\n"+qa)
		} else {
			parts = append(parts, " "+qa)
		}
	}

	return strings.Join(parts, "")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}

	need := false
	for _, c := range s {
		if c == ' ' || c == '\n' || c == '\t' || c == '\'' || c == '"' || c == '\\' {
			need = true
			break
		}
	}

	if !need {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
