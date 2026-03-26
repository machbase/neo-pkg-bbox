package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/machbase/neo-pkg-blackbox/internal/db"
	"github.com/machbase/neo-pkg-blackbox/internal/ffmpeg"
	"github.com/machbase/neo-pkg-blackbox/internal/logger"
)

// WatcherRule represents a watcher rule configuration.
type WatcherRule struct {
	CameraID  string // 카메라 식별자 (Name)
	Table     string // DB 테이블명
	SourceDir string
	TargetDir string
	Ext       string
}

// ptsOffset은 카메라별 PTS→epoch 변환 오프셋을 관리한다.
// PTS가 상대시간(예: 22.3초)이든 epoch이든, offset을 더하면 절대시간이 된다.
type ptsOffset struct {
	value       float64 // 현재 오프셋 (초)
	initialized bool
}

// updateOffset은 새로운 오프셋 샘플로 기존 값을 갱신한다.
// - 첫 샘플이거나 큰 점프(>2초)가 발생하면 즉시 리셋 (스트림 재시작 대응)
// - 그 외엔 EMA(지수이동평균)로 평활화하여 이벤트 지터를 흡수
func (o *ptsOffset) updateOffset(newOffset float64) {
	const (
		resetThreshold = 2.0 // 초: 이보다 큰 차이면 리셋
		emaAlpha       = 0.1 // EMA 계수 (0.1 = 새 값 10% 반영)
	)

	if !o.initialized {
		o.value = newOffset
		o.initialized = true
		return
	}

	diff := math.Abs(newOffset - o.value)
	if diff > resetThreshold {
		// 스트림 재시작 또는 PTS 리셋 감지
		o.value = newOffset
		return
	}

	// EMA 평활화
	o.value = (1-emaAlpha)*o.value + emaAlpha*newOffset
}

type Watcher struct {
	neo     *db.Machbase
	ffRuner *ffmpeg.FFmpegRunner

	// 자주 사용하는 필드들
	RamDisk   string
	DataDir   string
	CameraDir string // 카메라 설정 파일 디렉토리

	// 동적 watch 관리 (thread-safe)
	mu       sync.Mutex
	fsw      *fsnotify.Watcher
	watchSet *WatchSet

	// 카메라별 PTS offset (mu로 보호)
	offsets map[string]*ptsOffset
}

func New(neo *db.Machbase, ffRunner *ffmpeg.FFmpegRunner, cameraDir string) *Watcher {
	return &Watcher{
		neo:       neo,
		ffRuner:   ffRunner,
		CameraDir: cameraDir,
		offsets:   make(map[string]*ptsOffset),
	}
}

type WatchSet struct {
	dirToRule   map[string]WatcherRule // source dir -> rule
	cameraToDir map[string]string      // cameraID -> source dir
}

func (ws *WatchSet) RemoveAll(fsw *fsnotify.Watcher) {
	for dir := range ws.dirToRule {
		_ = fsw.Remove(dir)
	}
}

func (ws *WatchSet) Add(fsw *fsnotify.Watcher, rule WatcherRule) error {
	// 이미 해당 카메라가 등록되어 있으면 먼저 제거
	if oldDir, exists := ws.cameraToDir[rule.CameraID]; exists {
		_ = fsw.Remove(oldDir)
		delete(ws.dirToRule, oldDir)
		delete(ws.cameraToDir, rule.CameraID)
	}

	if err := fsw.Add(rule.SourceDir); err != nil {
		return fmt.Errorf("failed to add watch(source_dir=%q): %v", rule.SourceDir, err)
	}

	ws.dirToRule[rule.SourceDir] = rule
	ws.cameraToDir[rule.CameraID] = rule.SourceDir
	return nil
}

func (ws *WatchSet) Remove(fsw *fsnotify.Watcher, cameraID string) error {
	dir, ok := ws.cameraToDir[cameraID]
	if !ok {
		return fmt.Errorf("camera %q not found in watch set", cameraID)
	}

	if err := fsw.Remove(dir); err != nil {
		return fmt.Errorf("failed to remove watch: %v", err)
	}

	delete(ws.dirToRule, dir)
	delete(ws.cameraToDir, cameraID)
	return nil
}

func (ws *WatchSet) GetRule(dir string) (WatcherRule, bool) {
	rule, ok := ws.dirToRule[dir]
	return rule, ok
}

func (w *Watcher) addWatches(fsw *fsnotify.Watcher, rules []WatcherRule) (*WatchSet, error) {
	ws := &WatchSet{
		dirToRule:   make(map[string]WatcherRule, len(rules)),
		cameraToDir: make(map[string]string, len(rules)),
	}

	for _, rule := range rules {
		if err := ws.Add(fsw, rule); err != nil {
			return nil, err
		}
	}

	return ws, nil
}

type RuleFailure struct {
	id   int
	Rule WatcherRule
	Err  error
}

func (w *Watcher) prepare() ([]WatcherRule, []RuleFailure) {
	// 카메라 설정 파일들을 읽어서 WatcherRule 생성
	type CameraConfig struct {
		Table      string `json:"table"`
		Name       string `json:"name"`
		OutputDir  string `json:"output_dir"`
		ArchiveDir string `json:"archive_dir"`
	}

	active := make([]WatcherRule, 0)
	failed := []RuleFailure{}

	// cameraDir의 모든 .json 파일 읽기
	entries, err := os.ReadDir(w.CameraDir)
	if err != nil {
		logger.GetLogger().Warnf("Failed to read camera directory %q: %v", w.CameraDir, err)
		return active, failed
	}

	for i, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		// JSON 파일 읽기
		filePath := filepath.Join(w.CameraDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			logger.GetLogger().Warnf("Failed to read camera config %q: %v", filePath, err)
			continue
		}

		var config CameraConfig
		if err := json.Unmarshal(data, &config); err != nil {
			logger.GetLogger().Warnf("Failed to parse camera config %q: %v", filePath, err)
			continue
		}

		// Validate required paths
		if config.OutputDir == "" {
			reason := fmt.Errorf("output_dir is empty")
			logger.GetLogger().Infof("watcher rule[%d] camera=%q: output_dir is required", i, config.Name)
			failed = append(failed, RuleFailure{id: i, Rule: WatcherRule{CameraID: config.Name}, Err: reason})
			continue
		}
		if config.ArchiveDir == "" {
			reason := fmt.Errorf("archive_dir is empty")
			logger.GetLogger().Infof("watcher rule[%d] camera=%q: archive_dir is required", i, config.Name)
			failed = append(failed, RuleFailure{id: i, Rule: WatcherRule{CameraID: config.Name}, Err: reason})
			continue
		}

		// Resolve paths:
		// - Absolute path: use as-is
		// - Relative path: join with DataDir
		sourceDir := config.OutputDir
		if !filepath.IsAbs(sourceDir) {
			sourceDir = filepath.Join(w.DataDir, sourceDir)
		}
		targetDir := config.ArchiveDir
		if !filepath.IsAbs(targetDir) {
			targetDir = filepath.Join(w.DataDir, targetDir)
		}

		rule := WatcherRule{
			CameraID:  config.Name,
			Table:     config.Table,
			SourceDir: sourceDir,
			TargetDir: targetDir,
			Ext:       ".mpd",
		}
		if err := os.MkdirAll(rule.SourceDir, 0o755); err != nil {
			reason := fmt.Errorf("failed to mkdir source_dir=%q: %v", rule.SourceDir, err)
			logger.GetLogger().Infof("watcher rule[%d] camera=%q: mkdir source_dir=%q: %v", i, config.Name, rule.SourceDir, err)
			failed = append(failed, RuleFailure{id: i, Rule: rule, Err: reason})
			continue
		}
		if err := os.MkdirAll(rule.TargetDir, 0o755); err != nil {
			reason := fmt.Errorf("failed to mkdir target_dir=%q: %v", rule.TargetDir, err)
			logger.GetLogger().Infof("watcher rule[%d] camera=%q: mkdir target_dir=%q: %v", i, config.Name, rule.TargetDir, err)
			failed = append(failed, RuleFailure{id: i, Rule: rule, Err: reason})
			continue
		}

		active = append(active, rule)
		logger.GetLogger().Debugf("Loaded watcher rule: camera=%q table=%q source=%q target=%q",
			rule.CameraID, rule.Table, rule.SourceDir, rule.TargetDir)
	}

	return active, failed
}

func (w *Watcher) Run(ctx context.Context) error {
	active, _ := w.prepare() // active, failed

	logger.GetLogger().Infof("Start Watcher (loaded %d rules from camera configs)", len(active))

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fsw.Close()

	watchSet, err := w.addWatches(fsw, active)
	if err != nil {
		return err
	}

	// Store in watcher for dynamic add/remove
	w.mu.Lock()
	w.fsw = fsw
	w.watchSet = watchSet
	w.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			watchSet.RemoveAll(fsw)
			logger.GetLogger().Info("Stop Watcher")
			return nil

		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			// Create 이벤트만 처리 (IN_MOVED_TO 대응)
			// fsnotify는 Linux에서 IN_CREATE|IN_MOVED_TO 모두 Create로 매핑
			if !event.Has(fsnotify.Create) {
				continue
			}
			dir := filepath.Dir(event.Name)
			name := filepath.Base(event.Name)

			w.mu.Lock()
			rule, ok := watchSet.GetRule(dir)
			w.mu.Unlock()

			if !ok {
				continue
			}
			if err := w.handleEvent(ctx, name, rule); err != nil {
				logger.GetLogger().Infof("handleEvent: %v", err)
			}

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			logger.GetLogger().Warnf("fsnotify error: %v", err)
		}
	}
}

// 특정 이름으로 처리하는 방식은 나중에 필요할때 추가
func (w *Watcher) handleEvent(ctx context.Context, name string, rule WatcherRule) error {
	if name == "" {
		return nil
	}
	if !strings.EqualFold(filepath.Ext(name), rule.Ext) {
		return nil
	}

	if err := w.syncInit(rule); err != nil {
		return fmt.Errorf("syncInit: %v", err)
	}

	base := filepath.Base(name)

	switch {
	case strings.HasPrefix(base, "init"):
		return nil

	case strings.HasPrefix(base, "chunk-stream"):
		return w.proecessChunk(ctx, rule, base)

	default:
		return nil
	}
}

func (w *Watcher) syncInit(rule WatcherRule) error {
	srcPattern := filepath.Join(rule.SourceDir, "init*"+rule.Ext)
	srcInits, err := filepath.Glob(srcPattern)
	if err != nil {
		return err
	}
	if len(srcInits) == 0 {
		return nil
	}

	destPattern := filepath.Join(rule.TargetDir, "init*"+rule.Ext)
	destInits, _ := filepath.Glob(destPattern)
	for _, p := range destInits {
		_ = os.Remove(p)
	}

	if err := os.MkdirAll(rule.TargetDir, 0o755); err != nil {
		return err
	}

	for _, src := range srcInits {
		dst := filepath.Join(rule.TargetDir, filepath.Base(src))
		if err := moveFile(src, dst); err != nil {
			return fmt.Errorf("move init %q -> %q: %v", src, dst, err)
		}
		logger.GetLogger().Infof("[INIT] moved: %s -> %s", src, dst)
	}
	return nil
}

func extraChunkPrefix(filename, ext string) (string, error) {
	base := filepath.Base(filename)

	if ext == "" {
		ext = filepath.Ext(base)
	}
	if ext == "" {
		return "", fmt.Errorf("no extension in %q", base)
	}

	if !strings.EqualFold(filepath.Ext(base), ext) {
		return "", fmt.Errorf("invalid ext: name=%q ext=%q want=%q", base, filepath.Ext(base), ext)
	}

	stem := strings.TrimSuffix(base, filepath.Ext(base))

	i := strings.LastIndexByte(stem, '-')
	if i < 0 || i == len(stem)-1 {
		return "", fmt.Errorf("invalid chunk name (no numeric suffix): %q", base)
	}

	suffix := stem[i+1:]
	if !isAllDigits(suffix) {
		return "", fmt.Errorf("invalid chunk name (suffix not digits): %q", base)
	}

	prefix := stem[:i+1]
	if prefix == "" {
		return "", fmt.Errorf("invalid chunk name (empty prefix): %q", base)
	}

	return prefix, nil
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || '9' < c {
			return false
		}
	}
	return len(s) > 0
}

func (w *Watcher) proecessChunk(ctx context.Context, rule WatcherRule, name string) error {
	name = filepath.Base(name)

	observedEpochMs := time.Now().UnixMilli()

	// prefix 추출 + 새 이름 생성
	prefix, err := extraChunkPrefix(name, rule.Ext)
	if err != nil {
		return err
	}
	newName := prefix + strconv.FormatInt(observedEpochMs, 10) + rule.Ext

	// src -> dest 이동 (새 이름)
	srcPath := filepath.Join(rule.SourceDir, name)
	tmpDestPath := filepath.Join(rule.TargetDir, newName)

	if err := os.MkdirAll(rule.TargetDir, 0o755); err != nil {
		return err
	}
	if err := moveFile(srcPath, tmpDestPath); err != nil {
		return fmt.Errorf("move chunk %q -> %q: %v", srcPath, tmpDestPath, err)
	}

	// chunk 크기 검증
	if ok, err := checkFileMinSize(tmpDestPath, 1000); !ok || err != nil {
		return fmt.Errorf("chunk invalid: %v", err)
	}

	initPath := filepath.Join(rule.TargetDir, "init-stream0"+rule.Ext)
	if ok, err := checkFileMinSize(initPath, 100); !ok || err != nil {
		return fmt.Errorf("init invalid: %v", err)
	}

	timing, err := w.ffRuner.ProbeConcatPacketTiming(ctx, initPath, tmpDestPath)
	if err != nil {
		return fmt.Errorf("ProbeConcatPacketTiming: %v", err)
	}

	if timing.Length < 0 {
		return fmt.Errorf("negative length: start=%.6f last=%.6f dur=%.6f len=%.6f",
			timing.StartPTS, timing.LastPTS, timing.LastDur, timing.Length,
		)
	}
	if timing.Length < 0.1 || timing.Length > 30 {
		logger.GetLogger().Warnf("[CHUNK] abnormal length=%.6f for %s", timing.Length, name)
	}

	// PTS→절대시간 변환 (offset 매핑)
	// wallEnd = 이벤트 감지 시각 ≈ 세그먼트 끝 시점의 현실 시간
	// observedEpochMs는 ffprobe/파일이동 이전에 찍힌 값이므로 더 정확
	// offsetNew = wallEnd - endPts → PTS에 더하면 epoch가 됨
	wallEnd := float64(observedEpochMs) / 1e3
	offsetNew := wallEnd - timing.EndPTS

	w.mu.Lock()
	off, exists := w.offsets[rule.CameraID]
	if !exists {
		off = &ptsOffset{}
		w.offsets[rule.CameraID] = off
	}
	off.updateOffset(offsetNew)
	currentOffset := off.value
	w.mu.Unlock()

	absStart := timing.StartPTS + currentOffset
	utcTimeNs := int64(absStart * 1e9)

	logger.GetLogger().Debugf("[CHUNK] offset mapping: camera=%s pts=%.3f wallEnd=%.3f offsetNew=%.3f offset=%.3f absStart=%.3f",
		rule.CameraID, timing.StartPTS, wallEnd, offsetNew, currentOffset, absStart)

	// 날짜 디렉토리 생성 (영상 실제 시작 시각 기준)
	dateDir := time.Unix(0, utcTimeNs).UTC().Format("20060102")
	finalDir := filepath.Join(rule.TargetDir, dateDir)
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return fmt.Errorf("mkdir date dir %q: %v", finalDir, err)
	}

	// out 루트 -> 날짜 디렉토리로 이동
	finalPath := filepath.Join(finalDir, newName)
	if err := moveFile(tmpDestPath, finalPath); err != nil {
		return fmt.Errorf("move into date dir %q -> %q: %v", tmpDestPath, finalPath, err)
	}

	// DB에 청크 정보 저장: 카메라별 테이블명, 카메라ID, 실제 UTC 시간, 길이(초), 파일 경로
	// Table 필드가 없으면 CameraID를 대문자로 변환하여 사용 (하위 호환성)
	table := rule.Table
	if table == "" {
		table = strings.ToUpper(rule.CameraID)
	} else {
		table = strings.ToUpper(table)
	}
	// archive_dir(TargetDir) 기준 상대경로만 DB에 저장
	relPath, err := filepath.Rel(rule.TargetDir, finalPath)
	if err != nil {
		relPath = finalPath // fallback
	}
	if err := w.neo.InsertChunk(ctx, table, rule.CameraID, utcTimeNs, timing.Length, relPath); err != nil {
		return fmt.Errorf("InsertChunk: %v", err)
	}

	logger.GetLogger().Infof("[CHUNK] [%s] %s -> %s absStart=%.6f len=%.6f utcNs=%d relPath=%s", rule.CameraID, name, finalPath, absStart, timing.Length, utcTimeNs, relPath)

	return nil
}

func moveFile(src, dst string) error {
	_ = os.Remove(dst)

	if err := os.Rename(src, dst); err == nil {
		return nil
	} else {
		// logger.GetLogger().Infof()
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}

	if err := os.Remove(src); err != nil {
		return err
	}

	return nil
}

func checkFileMinSize(path string, minSize int64) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("%q is not exist", path)
		}
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("%q is directory", path)
	}
	if info.Size() < minSize {
		return false, fmt.Errorf("%q size(%d) is too small (<%d)", path, info.Size(), minSize)
	}
	return true, nil
}

// AddWatch dynamically adds a new watch rule to the running watcher.
// This is called when a camera is enabled via API.
func (w *Watcher) AddWatch(ctx context.Context, rule WatcherRule) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.watchSet == nil {
		return fmt.Errorf("watcher not running")
	}

	if rule.TargetDir == "" {
		return fmt.Errorf("target_dir is empty")
	}
	if err := os.MkdirAll(rule.SourceDir, 0o755); err != nil {
		return fmt.Errorf("failed to mkdir source_dir=%q: %v", rule.SourceDir, err)
	}
	if err := os.MkdirAll(rule.TargetDir, 0o755); err != nil {
		return fmt.Errorf("failed to mkdir target_dir=%q: %v", rule.TargetDir, err)
	}

	if err := w.watchSet.Add(w.fsw, rule); err != nil {
		return err
	}

	logger.GetLogger().Infof("[watcher] added watch: camera_id=%s source_dir=%s", rule.CameraID, rule.SourceDir)
	return nil
}

// RemoveWatch dynamically removes a watch rule from the running watcher.
// This is called when a camera is disabled via API.
func (w *Watcher) RemoveWatch(ctx context.Context, cameraID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.watchSet == nil {
		return fmt.Errorf("watcher not running")
	}

	if err := w.watchSet.Remove(w.fsw, cameraID); err != nil {
		return err
	}

	logger.GetLogger().Infof("[watcher] removed watch: camera_id=%s", cameraID)
	return nil
}
