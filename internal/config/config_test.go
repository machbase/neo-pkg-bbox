package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfig는 테스트용 config.yaml을 임시 디렉토리에 생성합니다.
func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

// TestLoad_RelativePathsResolved: config.yaml 위치 기준으로 상대경로가 절대경로로 변환되는지 검증합니다.
func TestLoad_RelativePathsResolved(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
ffmpeg:
  binary: tools/ffmpeg
  defaults:
    probe_binary: tools/ffprobe
mediamtx:
  binary: tools/mediamtx
  config_file: tools/mediamtx.yml
ai:
  binary: bin/ai-manager
  config_file: bin/ai.json
server:
  camera_dir: data/cameras
  mvs_dir: data/mvs
  data_dir: data/segments
  base_dir: static
log:
  dir: logs
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, "tools/ffmpeg"), cfg.FFmpeg.Binary)
	assert.Equal(t, filepath.Join(dir, "tools/ffprobe"), cfg.FFmpeg.Defaults.ProbeBinary)
	assert.Equal(t, filepath.Join(dir, "tools/mediamtx"), cfg.Mediamtx.Binary)
	assert.Equal(t, filepath.Join(dir, "tools/mediamtx.yml"), cfg.Mediamtx.ConfigFile)
	assert.Equal(t, filepath.Join(dir, "bin/ai-manager"), cfg.AI.Binary)
	assert.Equal(t, filepath.Join(dir, "bin/ai.json"), cfg.AI.ConfigFile)
	assert.Equal(t, filepath.Join(dir, "data/cameras"), cfg.Server.CameraDir)
	assert.Equal(t, filepath.Join(dir, "data/mvs"), cfg.Server.MvsDir)
	assert.Equal(t, filepath.Join(dir, "data/segments"), cfg.Server.DataDir)
	assert.Equal(t, filepath.Join(dir, "static"), cfg.Server.BaseDir)
	assert.Equal(t, filepath.Join(dir, "logs"), cfg.Log.Dir)
}

// TestLoad_AbsolutePathsUnchanged: 절대경로는 그대로 유지되는지 검증합니다.
func TestLoad_AbsolutePathsUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
ffmpeg:
  binary: /usr/bin/ffmpeg
  defaults:
    probe_binary: /usr/bin/ffprobe
mediamtx:
  binary: /opt/mediamtx
  config_file: /opt/mediamtx.yml
log:
  dir: /var/log/neo-blackbox
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, "/usr/bin/ffmpeg", cfg.FFmpeg.Binary)
	assert.Equal(t, "/usr/bin/ffprobe", cfg.FFmpeg.Defaults.ProbeBinary)
	assert.Equal(t, "/opt/mediamtx", cfg.Mediamtx.Binary)
	assert.Equal(t, "/opt/mediamtx.yml", cfg.Mediamtx.ConfigFile)
	assert.Equal(t, "/var/log/neo-blackbox", cfg.Log.Dir)
}

// TestLoad_EmptyPathsUnchanged: 비어있는 경로 필드는 그대로 빈 문자열을 유지하는지 검증합니다.
func TestLoad_EmptyPathsUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
server:
  addr: "0.0.0.0:8000"
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Empty(t, cfg.FFmpeg.Binary)
	assert.Empty(t, cfg.Mediamtx.Binary)
	assert.Empty(t, cfg.AI.Binary)
	assert.Empty(t, cfg.Log.Dir)
}

// TestLoad_FileNotFound: 파일이 존재하지 않으면 에러를 반환하는지 검증합니다.
func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	require.Error(t, err)
}

// TestLoad_InvalidYAML: 잘못된 YAML이면 에러를 반환하는지 검증합니다.
func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("key: [unclosed"), 0644))

	_, err := Load(path)
	require.Error(t, err)
}

// TestLoad_MediamtxDefaultsApplied: mediamtx 섹션이 비어 있을 때 기본값이 적용되는지 검증합니다.
func TestLoad_MediamtxDefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `mediamtx: {}`)

	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.Mediamtx.Host)
	assert.Equal(t, 9997, cfg.Mediamtx.Port)
	assert.Equal(t, 8889, cfg.Mediamtx.WebRTCPort)
	assert.Equal(t, 8554, cfg.Mediamtx.RtspServerPort)
	assert.NotEmpty(t, cfg.Mediamtx.WebRTCHost) // detectOutboundIP 또는 Host 폴백
}

func TestLoad_MachbaseDatabaseDefaultsAndOverride(t *testing.T) {
	dir := t.TempDir()
	defaultPath := writeConfig(t, dir, `machbase: {}`)

	cfg, err := Load(defaultPath)
	require.NoError(t, err)
	assert.Equal(t, DefaultMachbaseDatabase, cfg.Machbase.Database)

	t.Setenv("BB_MACHBASE_DATABASE", "CODEX_V870_TEST")
	cfg, err = Load(defaultPath)
	require.NoError(t, err)
	assert.Equal(t, "CODEX_V870_TEST", cfg.Machbase.Database)
}

// TestLoad_MediamtxCustomValuesPreserved: mediamtx에 명시적으로 설정한 값은 기본값으로 덮어쓰이지 않는지 검증합니다.
func TestLoad_MediamtxCustomValuesPreserved(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
mediamtx:
  host: "10.0.0.1"
  port: 19997
  webrtc_port: 18889
  rtsp_server_port: 18554
  webrtc_host: "192.168.1.100"
`)
	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, "10.0.0.1", cfg.Mediamtx.Host)
	assert.Equal(t, 19997, cfg.Mediamtx.Port)
	assert.Equal(t, 18889, cfg.Mediamtx.WebRTCPort)
	assert.Equal(t, 18554, cfg.Mediamtx.RtspServerPort)
	assert.Equal(t, "192.168.1.100", cfg.Mediamtx.WebRTCHost)
}

// TestSaveAndLoadRaw: Save로 저장한 config를 LoadRaw로 읽으면 동일한 값이 반환되는지 검증합니다.
// LoadRaw는 기본값 적용 및 경로 변환을 하지 않으므로 저장한 값 그대로 나와야 합니다.
func TestSaveAndLoadRaw(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	original := &AppConfig{}
	original.Server.Addr = "0.0.0.0:9000"
	original.FFmpeg.Binary = "/usr/bin/ffmpeg"
	original.Mediamtx.Port = 19997

	require.NoError(t, Save(path, original))

	loaded, err := LoadRaw(path)
	require.NoError(t, err)

	assert.Equal(t, original.Server.Addr, loaded.Server.Addr)
	assert.Equal(t, original.FFmpeg.Binary, loaded.FFmpeg.Binary)
	assert.Equal(t, original.Mediamtx.Port, loaded.Mediamtx.Port)
}

// TestResolveRelativePaths_AllFields: resolveRelativePaths가 모든 경로 필드를 base 기준으로 변환하는지 검증합니다.
func TestResolveRelativePaths_AllFields(t *testing.T) {
	base := "/base/dir"
	cfg := &AppConfig{}
	cfg.FFmpeg.Binary = "tools/ffmpeg"
	cfg.FFmpeg.Defaults.ProbeBinary = "tools/ffprobe"
	cfg.Mediamtx.Binary = "tools/mediamtx"
	cfg.Mediamtx.ConfigFile = "tools/mediamtx.yml"
	cfg.AI.Binary = "bin/ai"
	cfg.AI.ConfigFile = "bin/ai.json"
	cfg.Server.CameraDir = "data/cameras"
	cfg.Server.MvsDir = "data/mvs"
	cfg.Server.DataDir = "data/segments"
	cfg.Server.BaseDir = "static"
	cfg.Log.Dir = "logs"

	resolveRelativePaths(cfg, base)

	assert.Equal(t, "/base/dir/tools/ffmpeg", cfg.FFmpeg.Binary)
	assert.Equal(t, "/base/dir/tools/ffprobe", cfg.FFmpeg.Defaults.ProbeBinary)
	assert.Equal(t, "/base/dir/tools/mediamtx", cfg.Mediamtx.Binary)
	assert.Equal(t, "/base/dir/tools/mediamtx.yml", cfg.Mediamtx.ConfigFile)
	assert.Equal(t, "/base/dir/bin/ai", cfg.AI.Binary)
	assert.Equal(t, "/base/dir/bin/ai.json", cfg.AI.ConfigFile)
	assert.Equal(t, "/base/dir/data/cameras", cfg.Server.CameraDir)
	assert.Equal(t, "/base/dir/data/mvs", cfg.Server.MvsDir)
	assert.Equal(t, "/base/dir/data/segments", cfg.Server.DataDir)
	assert.Equal(t, "/base/dir/static", cfg.Server.BaseDir)
	assert.Equal(t, "/base/dir/logs", cfg.Log.Dir)
}

// TestResolveRelativePaths_AbsoluteUnchanged: 절대경로는 resolveRelativePaths에서 변경되지 않는지 검증합니다.
func TestResolveRelativePaths_AbsoluteUnchanged(t *testing.T) {
	base := "/some/base"
	cfg := &AppConfig{}
	cfg.FFmpeg.Binary = "/usr/bin/ffmpeg"
	cfg.Log.Dir = "/var/log"

	resolveRelativePaths(cfg, base)

	assert.Equal(t, "/usr/bin/ffmpeg", cfg.FFmpeg.Binary)
	assert.Equal(t, "/var/log", cfg.Log.Dir)
}

// TestResolveRelativePaths_EmptyUnchanged: 빈 경로는 resolveRelativePaths에서 변경되지 않는지 검증합니다.
func TestResolveRelativePaths_EmptyUnchanged(t *testing.T) {
	base := "/some/base"
	cfg := &AppConfig{}

	resolveRelativePaths(cfg, base)

	assert.Empty(t, cfg.FFmpeg.Binary)
	assert.Empty(t, cfg.Mediamtx.Binary)
	assert.Empty(t, cfg.Log.Dir)
}
