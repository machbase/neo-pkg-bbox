//go:build !linux

package watcher

import (
	"context"
	"fmt"
	"github.com/machbase/neo-pkg-bbox/internal/db"
	"github.com/machbase/neo-pkg-bbox/internal/ffmpeg"
	"github.com/machbase/neo-pkg-bbox/internal/logger"
)

// WatcherRule represents a watcher rule configuration.
type WatcherRule struct {
	CameraID  string // 카메라 식별자 (Name)
	Table     string // DB 테이블명
	SourceDir string
	TargetDir string
	Ext       string
}

type Watcher struct {
	neo       *db.Machbase
	ffRuner   *ffmpeg.FFmpegRunner
	CameraDir string

	RamDisk string
	DataDir string
}

func New(neo *db.Machbase, ffRunner *ffmpeg.FFmpegRunner, cameraDir string) *Watcher {
	return &Watcher{
		neo:       neo,
		ffRuner:   ffRunner,
		CameraDir: cameraDir,
	}
}

func (w *Watcher) Run(ctx context.Context) error {
	log := logger.GetLogger()
	log.Warn("Watcher is not supported on this platform (non-Linux)")
	log.Warn("Watcher will run in stub mode - file watching is disabled")

	// Just wait for context cancellation
	<-ctx.Done()
	log.Info("Stop Watcher (stub mode)")
	return nil
}

func (w *Watcher) AddWatch(ctx context.Context, rule WatcherRule) error {
	log := logger.GetLogger()
	log.Warnf("[watcher stub] AddWatch called for camera_id=%s (no-op on non-Linux)", rule.CameraID)
	return nil
}

func (w *Watcher) RemoveWatch(ctx context.Context, cameraID string) error {
	log := logger.GetLogger()
	log.Warnf("[watcher stub] RemoveWatch called for camera_id=%s (no-op on non-Linux)", cameraID)
	return nil
}

// Note: This is a stub implementation for non-Linux platforms (macOS, Windows, etc.)
// The actual file watching functionality using inotify is only available on Linux.
// For development on macOS, the watcher will be in stub mode and won't actually watch files.
func init() {
	fmt.Println("⚠️  WARNING: Running on non-Linux platform - file watcher is in stub mode")
	fmt.Println("   File watching will not work. Deploy to Linux for full functionality.")
}
