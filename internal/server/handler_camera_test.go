package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/machbase/neo-pkg-bbox/internal/watcher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubWatcher struct {
	removeFn func(cameraID string) error
}

func (w *stubWatcher) AddWatch(ctx context.Context, rule watcher.WatcherRule) error {
	return nil
}

func (w *stubWatcher) RemoveWatch(ctx context.Context, cameraID string) error {
	if w.removeFn != nil {
		return w.removeFn(cameraID)
	}
	return nil
}

func TestDeleteCamera_PreservesDefaultCameraDataRootAndRecordsTombstone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rootDir := t.TempDir()
	dataDir := filepath.Join(rootDir, "data")
	cameraDir := filepath.Join(rootDir, "camera")
	mvsDir := filepath.Join(rootDir, "mvs")

	cameraID := "cam1"
	cameraDataDir := filepath.Join(dataDir, cameraID)
	outputDir := filepath.Join(cameraDataDir, "in")
	archiveDir := filepath.Join(cameraDataDir, "out")

	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	require.NoError(t, os.MkdirAll(archiveDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "chunk-stream0-1.m4s"), []byte("in"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "chunk-stream0-2.m4s"), []byte("out"), 0o644))

	writeTestCameraConfig(t, cameraDir, cameraID, CameraCreateRequest{
		Name:       cameraID,
		Table:      "cam1_table",
		OutputDir:  outputDir,
		ArchiveDir: archiveDir,
	})

	require.NoError(t, os.MkdirAll(mvsDir, 0o755))
	mvsPath := filepath.Join(mvsDir, cameraID+"_0_123.mvs")
	require.NoError(t, os.WriteFile(mvsPath, []byte("{}"), 0o644))

	var cancelled bool
	var watcherSawDataDir bool
	h := &Handler{
		watcher: &stubWatcher{removeFn: func(gotID string) error {
			require.Equal(t, cameraID, gotID)
			_, err := os.Stat(cameraDataDir)
			require.NoError(t, err)
			watcherSawDataDir = true
			return nil
		}},
		dataDir:   dataDir,
		cameraDir: cameraDir,
		mvsDir:    mvsDir,
		processes: map[string]*cameraProcess{
			cameraID: {
				cancel: func() {
					cancelled = true
				},
			},
		},
		deletedCameras: make(map[string]deletedCameraState),
	}
	h.stateFilePath = filepath.Join(dataDir, "state.json")

	rec := performDeleteCamera(t, h, cameraID)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, cancelled)
	assert.True(t, watcherSawDataDir)
	assert.DirExists(t, cameraDataDir)
	assert.FileExists(t, filepath.Join(outputDir, "chunk-stream0-1.m4s"))
	assert.FileExists(t, filepath.Join(archiveDir, "chunk-stream0-2.m4s"))
	assertPathNotExists(t, filepath.Join(cameraDir, cameraID+".json"))
	assertPathNotExists(t, mvsPath)
	require.Contains(t, h.deletedCameras, cameraID)
	assert.Equal(t, archiveDir, h.deletedCameras[cameraID].ArchiveDir)
	_, stillRunning := h.processes[cameraID]
	assert.False(t, stillRunning)
}

func TestDeleteCamera_PreservesCustomStorageDirs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rootDir := t.TempDir()
	dataDir := filepath.Join(rootDir, "data")
	cameraDir := filepath.Join(rootDir, "camera")

	cameraID := "cam2"
	sharedParent := filepath.Join(rootDir, "shared-parent")
	outputDir := filepath.Join(sharedParent, "custom-output")
	archiveDir := filepath.Join(sharedParent, "custom-archive")

	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	require.NoError(t, os.MkdirAll(archiveDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "chunk-stream0-1.m4s"), []byte("in"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "chunk-stream0-2.m4s"), []byte("out"), 0o644))

	writeTestCameraConfig(t, cameraDir, cameraID, CameraCreateRequest{
		Name:       cameraID,
		Table:      "cam2_table",
		OutputDir:  outputDir,
		ArchiveDir: archiveDir,
	})

	h := &Handler{
		watcher:        &stubWatcher{},
		dataDir:        dataDir,
		cameraDir:      cameraDir,
		processes:      map[string]*cameraProcess{},
		deletedCameras: make(map[string]deletedCameraState),
	}
	h.stateFilePath = filepath.Join(dataDir, "state.json")

	rec := performDeleteCamera(t, h, cameraID)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.DirExists(t, outputDir)
	assert.DirExists(t, archiveDir)
	assert.DirExists(t, sharedParent)
	assertPathNotExists(t, filepath.Join(cameraDir, cameraID+".json"))
	require.Contains(t, h.deletedCameras, cameraID)
	assert.Equal(t, archiveDir, h.deletedCameras[cameraID].ArchiveDir)
}

func writeTestCameraConfig(t *testing.T, cameraDir, cameraID string, cfg CameraCreateRequest) {
	t.Helper()

	require.NoError(t, os.MkdirAll(cameraDir, 0o755))
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cameraDir, cameraID+".json"), data, 0o644))
}

func performDeleteCamera(t *testing.T, h *Handler, cameraID string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/camera/"+cameraID, nil)
	c.Params = gin.Params{{Key: "id", Value: cameraID}}

	h.DeleteCamera(c)

	return rec
}

func assertPathNotExists(t *testing.T, path string) {
	t.Helper()

	_, err := os.Stat(path)
	require.Truef(t, os.IsNotExist(err), "expected %q to be removed, got err=%v", path, err)
}
