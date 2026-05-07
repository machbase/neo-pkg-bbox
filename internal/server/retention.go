package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/machbase/neo-pkg-bbox/internal/config"
	"github.com/machbase/neo-pkg-bbox/internal/db"
	"github.com/machbase/neo-pkg-bbox/internal/logger"
)

const retentionBatchSize = 1000

type deletedCameraState struct {
	CameraID   string `json:"camera_id"`
	Table      string `json:"table"`
	OutputDir  string `json:"output_dir"`
	ArchiveDir string `json:"archive_dir"`
	DeletedAt  string `json:"deleted_at"`
}

type RetentionRunResult struct {
	StartedAt       string                 `json:"started_at"`
	FinishedAt      string                 `json:"finished_at,omitempty"`
	DryRun          bool                   `json:"dry_run"`
	Cutoff          string                 `json:"cutoff"`
	CutoffNs        int64                  `json:"cutoff_ns"`
	Tables          []RetentionTableResult `json:"tables"`
	CandidateRows   int64                  `json:"candidate_rows"`
	DeletedFiles    int                    `json:"deleted_files"`
	MissingFiles    int                    `json:"missing_files"`
	SkippedFiles    int                    `json:"skipped_files"`
	DeletedMetadata int                    `json:"deleted_metadata"`
	Errors          []string               `json:"errors,omitempty"`
}

type RetentionTableResult struct {
	Table           string                  `json:"table"`
	Kind            string                  `json:"kind"`
	Cameras         []RetentionCameraResult `json:"cameras"`
	CandidateRows   int64                   `json:"candidate_rows"`
	DeletedFiles    int                     `json:"deleted_files"`
	MissingFiles    int                     `json:"missing_files"`
	SkippedFiles    int                     `json:"skipped_files"`
	DeletedMetadata int                     `json:"deleted_metadata"`
	Errors          []string                `json:"errors,omitempty"`
}

type RetentionCameraResult struct {
	CameraID        string   `json:"camera_id"`
	TagNames        []string `json:"tag_names,omitempty"`
	ArchiveDir      string   `json:"archive_dir,omitempty"`
	Active          bool     `json:"active"`
	CandidateRows   int64    `json:"candidate_rows"`
	DeletedFiles    int      `json:"deleted_files"`
	MissingFiles    int      `json:"missing_files"`
	SkippedFiles    int      `json:"skipped_files"`
	MetadataDeleted bool     `json:"metadata_deleted"`
	Errors          []string `json:"errors,omitempty"`
}

type retentionCameraInfo struct {
	ID         string
	Table      string
	OutputDir  string
	ArchiveDir string
	Active     bool
}

type retentionRunRequest struct {
	DryRun bool `json:"dry_run"`
}

func (h *Handler) GetRetentionStatus(c *gin.Context) {
	tick := time.Now()

	cfg, err := h.loadRetentionConfig()
	if err != nil {
		errorResponse(c, tick, http.StatusInternalServerError, err.Error())
		return
	}

	var nextRun string
	if cfg.Enabled {
		next, err := nextRetentionRunAt(cfg, time.Now().UTC())
		if err != nil {
			errorResponse(c, tick, http.StatusInternalServerError, err.Error())
			return
		}
		nextRun = next.Format(time.RFC3339)
	}

	h.retentionMu.Lock()
	running := h.retentionRunning
	last := h.lastRetentionResult
	h.retentionMu.Unlock()

	successResponse(c, tick, gin.H{
		"config":      cfg,
		"running":     running,
		"next_run_at": nextRun,
		"last_run":    last,
	})
}

func (h *Handler) PostRetentionRun(c *gin.Context) {
	tick := time.Now()

	var req retentionRunRequest
	if c.Request.Body != nil {
		_ = c.ShouldBindJSON(&req)
	}

	result, err := h.runRetentionWithLock(c.Request.Context(), req.DryRun)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errRetentionAlreadyRunning) {
			status = http.StatusConflict
		}
		errorResponse(c, tick, status, err.Error())
		return
	}

	successResponse(c, tick, result)
}

func (h *Handler) startRetentionScheduler(ctx context.Context) {
	for {
		cfg, err := h.loadRetentionConfig()
		if err != nil {
			logger.GetLogger().Warnf("[retention] failed to load config: %v", err)
			if _, ok := h.waitRetentionOrReset(ctx, time.Minute); !ok {
				return
			}
			continue
		}

		if !cfg.Enabled {
			if _, ok := h.waitRetentionOrReset(ctx, time.Minute); !ok {
				return
			}
			continue
		}

		next, err := nextRetentionRunAt(cfg, time.Now().UTC())
		if err != nil {
			logger.GetLogger().Warnf("[retention] invalid schedule: %v", err)
			if _, ok := h.waitRetentionOrReset(ctx, time.Minute); !ok {
				return
			}
			continue
		}
		logger.GetLogger().Infof("[retention] scheduled next_run_at=%s keep_hours=%d start_at_utc=%s interval_hours=%d targets_database=%v targets_files=%v",
			next.Format(time.RFC3339), cfg.KeepHours, cfg.StartAtUTC, cfg.IntervalHours, cfg.Targets.Database, cfg.Targets.Files)

		timerFired, ok := h.waitRetentionOrReset(ctx, time.Until(next))
		if !ok {
			return
		}
		if !timerFired {
			logger.GetLogger().Info("[retention] schedule reset; reloading config")
			continue
		}

		latest, err := h.loadRetentionConfig()
		if err != nil {
			logger.GetLogger().Warnf("[retention] failed to reload config before run: %v", err)
			continue
		}
		if !latest.Enabled {
			logger.GetLogger().Info("[retention] scheduled run skipped because retention is disabled")
			continue
		}
		if retentionScheduleSignature(cfg) != retentionScheduleSignature(latest) {
			logger.GetLogger().Info("[retention] scheduled run skipped because schedule changed")
			continue
		}

		if _, err := h.runRetentionWithLock(ctx, false); err != nil {
			logger.GetLogger().Warnf("[retention] scheduled run failed: %v", err)
		}
	}
}

var errRetentionAlreadyRunning = errors.New("retention is already running")

func (h *Handler) runRetentionWithLock(ctx context.Context, dryRun bool) (*RetentionRunResult, error) {
	h.retentionMu.Lock()
	if h.retentionRunning {
		h.retentionMu.Unlock()
		return nil, errRetentionAlreadyRunning
	}
	h.retentionRunning = true
	h.retentionMu.Unlock()

	result, err := h.runRetention(ctx, dryRun)
	logRetentionRunResult(result, err)

	h.retentionMu.Lock()
	h.retentionRunning = false
	if result != nil {
		h.lastRetentionResult = result
	}
	h.retentionMu.Unlock()

	return result, err
}

func (h *Handler) runRetention(ctx context.Context, dryRun bool) (*RetentionRunResult, error) {
	cfg, err := h.loadRetentionConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Targets.Files && !cfg.Targets.Database {
		return nil, fmt.Errorf("retention files target requires database target")
	}

	keepDuration, err := cfg.KeepDuration()
	if err != nil {
		return nil, err
	}

	started := time.Now().UTC()
	cutoff := started.Add(-keepDuration)
	result := &RetentionRunResult{
		StartedAt: started.Format(time.RFC3339),
		DryRun:    dryRun,
		Cutoff:    cutoff.Format(time.RFC3339Nano),
		CutoffNs:  cutoff.UnixNano(),
	}
	logger.GetLogger().Infof("[retention] started dry_run=%v cutoff=%s keep_hours=%d start_at_utc=%s interval_hours=%d targets_database=%v targets_files=%v",
		dryRun, result.Cutoff, cfg.KeepHours, cfg.StartAtUTC, cfg.IntervalHours, cfg.Targets.Database, cfg.Targets.Files)
	defer func() {
		result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	}()

	active, err := h.loadRetentionCameraConfigs()
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	deleted := h.snapshotDeletedCameras()

	tables := retentionTableNames(active, deleted)

	processedArchives := make(map[string]bool)
	for _, table := range tables {
		if ctx.Err() != nil {
			result.Errors = append(result.Errors, ctx.Err().Error())
			return result, ctx.Err()
		}
		exists, err := h.machbase.TableExists(ctx, table)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s exists: %v", table, err))
			continue
		}
		if !exists {
			if cfg.Targets.Files {
				droppedResult := h.retentionDroppedTableFiles(table, dryRun, active, deleted, processedArchives)
				if len(droppedResult.Cameras) > 0 || len(droppedResult.Errors) > 0 {
					result.addTable(droppedResult)
				}
			}
			continue
		}
		tableResult := h.retentionMainTable(ctx, cfg, table, cutoff, dryRun, active, deleted, processedArchives)
		result.addTable(tableResult)

		for _, suffix := range []string{"_event", "_log"} {
			metaTable := table + suffix
			exists, err := h.machbase.TableExists(ctx, metaTable)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s exists: %v", metaTable, err))
				continue
			}
			if !exists {
				continue
			}
			metaResult := h.retentionMetadataTable(ctx, cfg, metaTable, strings.TrimPrefix(suffix, "_"), cutoff, dryRun, active)
			result.addTable(metaResult)
		}
	}

	if cfg.Targets.Files {
		orphanResult := h.retentionOrphanArchives(cutoff, dryRun, active, deleted, processedArchives)
		if len(orphanResult.Cameras) > 0 || len(orphanResult.Errors) > 0 {
			result.addTable(orphanResult)
		}
	}

	if cfg.ConsistencyCleanupEnabled() && cfg.Targets.Files && cfg.Targets.Database {
		for _, table := range tables {
			consistencyResult := h.retentionConsistencyTable(ctx, table, cutoff, dryRun, active, deleted)
			if len(consistencyResult.Cameras) > 0 || len(consistencyResult.Errors) > 0 {
				result.addTable(consistencyResult)
			}
		}
	}

	if !dryRun {
		h.pruneDeletedCameraStates(active, deleted)
	}

	return result, nil
}

func logRetentionRunResult(result *RetentionRunResult, runErr error) {
	if result == nil {
		if runErr != nil {
			logger.GetLogger().Warnf("[retention] failed error=%v", runErr)
		}
		return
	}

	level := logger.GetLogger().Infof
	if runErr != nil || len(result.Errors) > 0 {
		level = logger.GetLogger().Warnf
	}
	level("[retention] finished dry_run=%v started_at=%s finished_at=%s cutoff=%s tables=%d candidate_rows=%d deleted_files=%d missing_files=%d skipped_files=%d deleted_metadata=%d errors=%d error=%v",
		result.DryRun, result.StartedAt, result.FinishedAt, result.Cutoff, len(result.Tables), result.CandidateRows, result.DeletedFiles, result.MissingFiles, result.SkippedFiles, result.DeletedMetadata, len(result.Errors), runErr)

	for _, table := range result.Tables {
		if table.CandidateRows == 0 && table.DeletedFiles == 0 && table.MissingFiles == 0 && table.SkippedFiles == 0 && table.DeletedMetadata == 0 && len(table.Errors) == 0 {
			continue
		}
		tableLevel := logger.GetLogger().Infof
		if len(table.Errors) > 0 {
			tableLevel = logger.GetLogger().Warnf
		}
		tableLevel("[retention] table table=%s kind=%s cameras=%d candidate_rows=%d deleted_files=%d missing_files=%d skipped_files=%d deleted_metadata=%d errors=%d",
			table.Table, table.Kind, len(table.Cameras), table.CandidateRows, table.DeletedFiles, table.MissingFiles, table.SkippedFiles, table.DeletedMetadata, len(table.Errors))
	}
}

func (r *RetentionRunResult) addTable(t RetentionTableResult) {
	r.Tables = append(r.Tables, t)
	r.CandidateRows += t.CandidateRows
	r.DeletedFiles += t.DeletedFiles
	r.MissingFiles += t.MissingFiles
	r.SkippedFiles += t.SkippedFiles
	r.DeletedMetadata += t.DeletedMetadata
	r.Errors = append(r.Errors, t.Errors...)
}

func (t *RetentionTableResult) addCamera(c RetentionCameraResult) {
	t.Cameras = append(t.Cameras, c)
	t.CandidateRows += c.CandidateRows
	t.DeletedFiles += c.DeletedFiles
	t.MissingFiles += c.MissingFiles
	t.SkippedFiles += c.SkippedFiles
	if c.MetadataDeleted {
		t.DeletedMetadata++
	}
	t.Errors = append(t.Errors, c.Errors...)
}

func (h *Handler) retentionMainTable(ctx context.Context, cfg config.RetentionConfig, table string, cutoff time.Time, dryRun bool, active map[string]retentionCameraInfo, deleted map[string]deletedCameraState, processedArchives map[string]bool) RetentionTableResult {
	tableResult := RetentionTableResult{Table: table, Kind: "main"}

	tags, err := h.machbase.ListTagNames(ctx, table)
	if err != nil {
		tableResult.Errors = append(tableResult.Errors, fmt.Sprintf("%s metadata: %v", table, err))
		return tableResult
	}

	for _, cameraID := range tags {
		info := h.retentionCameraInfo(cameraID, active, deleted)
		processedArchives[filepath.Clean(info.ArchiveDir)] = true
		cameraResult := h.retentionMainCamera(ctx, cfg, table, info, cutoff, dryRun)
		tableResult.addCamera(cameraResult)
	}

	return tableResult
}

func (h *Handler) retentionMainCamera(ctx context.Context, cfg config.RetentionConfig, table string, info retentionCameraInfo, cutoff time.Time, dryRun bool) RetentionCameraResult {
	cameraResult := RetentionCameraResult{
		CameraID:   info.ID,
		TagNames:   []string{info.ID},
		ArchiveDir: info.ArchiveDir,
		Active:     info.Active,
	}

	if dryRun {
		rows, err := h.machbase.CountTagRowsBefore(ctx, table, info.ID, cutoff.UnixNano())
		if err != nil {
			cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s count: %v", table, info.ID, err))
		}
		cameraResult.CandidateRows = rows
		return cameraResult
	}

	for {
		candidates, err := h.machbase.SelectChunkDeleteCandidatesBefore(ctx, table, info.ID, cutoff.UnixNano(), retentionBatchSize)
		if err != nil {
			cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s chunks: %v", table, info.ID, err))
			return cameraResult
		}
		if len(candidates) == 0 {
			break
		}

		maxTime := maxCandidateTime(candidates)
		if cfg.Targets.Database {
			if err := h.machbase.DeleteTagRowsThrough(ctx, table, info.ID, maxTime); err != nil {
				cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s delete rows: %v", table, info.ID, err))
				return cameraResult
			}
		}
		cameraResult.CandidateRows += int64(len(candidates))

		if cfg.Targets.Files {
			for _, candidate := range candidates {
				deleted, missing, skipped, err := h.deleteRetentionChunkFile(info.ArchiveDir, candidate.ChunkPath)
				if err != nil {
					cameraResult.Errors = append(cameraResult.Errors, err.Error())
					continue
				}
				if deleted {
					cameraResult.DeletedFiles++
				}
				if missing {
					cameraResult.MissingFiles++
				}
				if skipped {
					cameraResult.SkippedFiles++
				}
			}
		}
	}

	if !info.Active && cfg.Targets.Database {
		hasRows, err := h.machbase.TagHasRows(ctx, table, info.ID)
		if err != nil {
			cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s row check: %v", table, info.ID, err))
			return cameraResult
		}
		hasFiles := archiveHasMediaFiles(info.ArchiveDir)
		if !hasRows && !hasFiles {
			if err := h.machbase.DeleteMetadataByName(ctx, table, info.ID); err != nil {
				cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s metadata delete: %v", table, info.ID, err))
			} else {
				cameraResult.MetadataDeleted = true
			}
		}
	}

	return cameraResult
}

func (h *Handler) retentionDroppedTableFiles(table string, dryRun bool, active map[string]retentionCameraInfo, deleted map[string]deletedCameraState, processedArchives map[string]bool) RetentionTableResult {
	tableResult := RetentionTableResult{Table: table, Kind: "dropped_table_files"}

	cameraIDs := make([]string, 0, len(deleted))
	for cameraID, state := range deleted {
		if _, ok := active[cameraID]; ok {
			continue
		}
		if state.Table != table {
			continue
		}
		cameraIDs = append(cameraIDs, cameraID)
	}
	sort.Strings(cameraIDs)

	for _, cameraID := range cameraIDs {
		state := deleted[cameraID]
		cameraResult := RetentionCameraResult{
			CameraID:   cameraID,
			ArchiveDir: state.ArchiveDir,
			Active:     false,
		}

		cfg := CameraCreateRequest{
			Name:       cameraID,
			Table:      state.Table,
			OutputDir:  state.OutputDir,
			ArchiveDir: state.ArchiveDir,
		}
		files, err := h.countCameraStorageFiles(cameraID, &cfg)
		if err != nil {
			cameraResult.Errors = append(cameraResult.Errors, err.Error())
			tableResult.addCamera(cameraResult)
			continue
		}
		cameraResult.DeletedFiles = files
		if !dryRun {
			if err := h.removeCameraStorageDirs(cameraID, &cfg); err != nil {
				cameraResult.Errors = append(cameraResult.Errors, err.Error())
			}
		}

		_, archiveDir := h.resolveCameraStorageDirs(cameraID, &cfg)
		processedArchives[filepath.Clean(archiveDir)] = true
		tableResult.addCamera(cameraResult)
	}

	return tableResult
}

func (h *Handler) retentionMetadataTable(ctx context.Context, cfg config.RetentionConfig, table string, kind string, cutoff time.Time, dryRun bool, active map[string]retentionCameraInfo) RetentionTableResult {
	tableResult := RetentionTableResult{Table: table, Kind: kind}

	cameraIDs, err := h.machbase.ListMetadataCameraIDs(ctx, table)
	if err != nil {
		tableResult.Errors = append(tableResult.Errors, fmt.Sprintf("%s camera metadata: %v", table, err))
		return tableResult
	}

	for _, cameraID := range cameraIDs {
		_, isActive := active[cameraID]
		cameraResult := RetentionCameraResult{
			CameraID: cameraID,
			Active:   isActive,
		}

		tagNames, err := h.machbase.ListMetadataNamesByCamera(ctx, table, cameraID)
		if err != nil {
			cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s metadata names: %v", table, cameraID, err))
			tableResult.addCamera(cameraResult)
			continue
		}
		cameraResult.TagNames = tagNames

		for _, tagName := range tagNames {
			if dryRun {
				rows, err := h.machbase.CountTagRowsBefore(ctx, table, tagName, cutoff.UnixNano())
				if err != nil {
					cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s count: %v", table, tagName, err))
					continue
				}
				cameraResult.CandidateRows += rows
				continue
			}
			if cfg.Targets.Database {
				if err := h.machbase.DeleteTagRowsBefore(ctx, table, tagName, cutoff.UnixNano()); err != nil {
					cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s delete rows: %v", table, tagName, err))
					continue
				}
			}
		}

		if !dryRun && !isActive && cfg.Targets.Database {
			if h.metadataCameraHasRows(ctx, table, tagNames, &cameraResult) {
				tableResult.addCamera(cameraResult)
				continue
			}
			if err := h.machbase.DeleteMetadataByCamera(ctx, table, cameraID); err != nil {
				cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s metadata delete: %v", table, cameraID, err))
			} else {
				cameraResult.MetadataDeleted = true
			}
		}

		tableResult.addCamera(cameraResult)
	}

	return tableResult
}

func (h *Handler) metadataCameraHasRows(ctx context.Context, table string, tagNames []string, cameraResult *RetentionCameraResult) bool {
	for _, tagName := range tagNames {
		hasRows, err := h.machbase.TagHasRows(ctx, table, tagName)
		if err != nil {
			cameraResult.Errors = append(cameraResult.Errors, fmt.Sprintf("%s/%s row check: %v", table, tagName, err))
			return true
		}
		if hasRows {
			return true
		}
	}
	return false
}

func (h *Handler) retentionConsistencyTable(ctx context.Context, table string, cutoff time.Time, dryRun bool, active map[string]retentionCameraInfo, deleted map[string]deletedCameraState) RetentionTableResult {
	tableResult := RetentionTableResult{Table: table, Kind: "consistency_cleanup"}

	exists, err := h.machbase.TableExists(ctx, table)
	if err != nil {
		tableResult.Errors = append(tableResult.Errors, fmt.Sprintf("%s exists: %v", table, err))
		return tableResult
	}
	if !exists {
		return tableResult
	}

	cameraIDs, err := h.retentionConsistencyCameraIDs(ctx, table, active, deleted)
	if err != nil {
		tableResult.Errors = append(tableResult.Errors, err.Error())
		return tableResult
	}

	for _, cameraID := range cameraIDs {
		info := h.retentionCameraInfo(cameraID, active, deleted)
		cameraResult := RetentionCameraResult{
			CameraID:   cameraID,
			ArchiveDir: info.ArchiveDir,
			Active:     info.Active,
		}
		deletedFiles, err := h.deleteUnreferencedOldFilesInArchive(ctx, table, cameraID, info.ArchiveDir, cutoff, dryRun)
		if err != nil {
			cameraResult.Errors = append(cameraResult.Errors, err.Error())
		}
		cameraResult.DeletedFiles = deletedFiles
		if cameraResult.DeletedFiles > 0 || len(cameraResult.Errors) > 0 {
			tableResult.addCamera(cameraResult)
		}
	}

	return tableResult
}

func (h *Handler) retentionConsistencyCameraIDs(ctx context.Context, table string, active map[string]retentionCameraInfo, deleted map[string]deletedCameraState) ([]string, error) {
	seen := make(map[string]bool)
	for cameraID, info := range active {
		if info.Table == table {
			seen[cameraID] = true
		}
	}
	for cameraID, state := range deleted {
		if state.Table == table {
			seen[cameraID] = true
		}
	}

	tags, err := h.machbase.ListTagNames(ctx, table)
	if err != nil {
		return nil, fmt.Errorf("%s metadata: %v", table, err)
	}
	for _, tag := range tags {
		seen[tag] = true
	}

	cameraIDs := make([]string, 0, len(seen))
	for cameraID := range seen {
		cameraIDs = append(cameraIDs, cameraID)
	}
	sort.Strings(cameraIDs)
	return cameraIDs, nil
}

func (h *Handler) retentionOrphanArchives(cutoff time.Time, dryRun bool, active map[string]retentionCameraInfo, deleted map[string]deletedCameraState, processedArchives map[string]bool) RetentionTableResult {
	tableResult := RetentionTableResult{Table: "filesystem", Kind: "orphan_files"}
	archives := make(map[string]string)

	for cameraID, state := range deleted {
		if _, ok := active[cameraID]; ok {
			continue
		}
		if strings.TrimSpace(state.ArchiveDir) != "" {
			archives[cameraID] = state.ArchiveDir
		}
	}

	entries, err := os.ReadDir(h.dataDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			cameraID := entry.Name()
			if _, ok := active[cameraID]; ok {
				continue
			}
			if _, ok := archives[cameraID]; !ok {
				archives[cameraID] = filepath.Join(h.dataDir, cameraID, "out")
			}
		}
	} else if !os.IsNotExist(err) {
		tableResult.Errors = append(tableResult.Errors, fmt.Sprintf("read data_dir: %v", err))
	}

	cameraIDs := make([]string, 0, len(archives))
	for cameraID := range archives {
		cameraIDs = append(cameraIDs, cameraID)
	}
	sort.Strings(cameraIDs)

	for _, cameraID := range cameraIDs {
		archiveDir := filepath.Clean(archives[cameraID])
		if processedArchives[archiveDir] {
			continue
		}
		cameraResult := RetentionCameraResult{
			CameraID:   cameraID,
			ArchiveDir: archiveDir,
			Active:     false,
		}
		deletedFiles, err := deleteOldFilesInArchive(archiveDir, cutoff, dryRun)
		if err != nil {
			cameraResult.Errors = append(cameraResult.Errors, err.Error())
		}
		cameraResult.DeletedFiles = deletedFiles
		tableResult.addCamera(cameraResult)
	}

	return tableResult
}

func (h *Handler) countCameraStorageFiles(cameraID string, cfg *CameraCreateRequest) (int, error) {
	outputDir, archiveDir := h.resolveCameraStorageDirs(cameraID, cfg)

	targets := []string{outputDir}
	if archiveDir != outputDir {
		targets = append(targets, archiveDir)
	}
	sort.Slice(targets, func(i, j int) bool {
		return len(targets[i]) < len(targets[j])
	})

	seenParents := make([]string, 0, len(targets))
	total := 0
	for _, target := range targets {
		target = filepath.Clean(target)
		if !h.isSafeCameraStoragePath(target) {
			return 0, fmt.Errorf("refusing to count unsafe camera storage path %q", target)
		}
		skip := false
		for _, parent := range seenParents {
			if sameOrChildPath(target, parent) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		files, err := countFilesUnder(target)
		if err != nil {
			return 0, err
		}
		total += files
		seenParents = append(seenParents, target)
	}
	return total, nil
}

func countFilesUnder(root string) (int, error) {
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	count := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	})
	return count, err
}

func (h *Handler) deleteRetentionChunkFile(archiveDir string, chunkPath string) (deleted bool, missing bool, skipped bool, err error) {
	fullPath, ok := resolveRetentionChunkPath(archiveDir, chunkPath)
	if !ok {
		return false, false, true, nil
	}

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return false, true, false, nil
		}
		return false, false, false, fmt.Errorf("delete file %q: %w", fullPath, err)
	}

	removeEmptyParents(fullPath, filepath.Clean(archiveDir))
	return true, false, false, nil
}

func resolveRetentionChunkPath(archiveDir string, chunkPath string) (string, bool) {
	chunkPath = strings.TrimSpace(chunkPath)
	if chunkPath == "" {
		return "", false
	}
	base := filepath.Base(chunkPath)
	if base == "manifest.mpd" || strings.HasPrefix(base, "init-") || filepath.Ext(base) != ".m4s" {
		return "", false
	}

	cleanArchive := filepath.Clean(archiveDir)
	var fullPath string
	if filepath.IsAbs(chunkPath) {
		fullPath = filepath.Clean(chunkPath)
	} else {
		fullPath = filepath.Clean(filepath.Join(cleanArchive, chunkPath))
	}
	if !sameOrChildPath(fullPath, cleanArchive) {
		return "", false
	}
	return fullPath, true
}

func deleteOldFilesInArchive(archiveDir string, cutoff time.Time, dryRun bool) (int, error) {
	if strings.TrimSpace(archiveDir) == "" {
		return 0, nil
	}
	if _, err := os.Stat(archiveDir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	deleted := 0
	err := filepath.WalkDir(archiveDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		base := entry.Name()
		if base == "manifest.mpd" || strings.HasPrefix(base, "init-") || filepath.Ext(base) != ".m4s" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !archiveFileBeforeCutoff(path, info.ModTime(), cutoff) {
			return nil
		}
		deleted++
		if dryRun {
			return nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		removeEmptyParents(path, filepath.Clean(archiveDir))
		return nil
	})
	return deleted, err
}

func (h *Handler) deleteUnreferencedOldFilesInArchive(ctx context.Context, table string, cameraID string, archiveDir string, cutoff time.Time, dryRun bool) (int, error) {
	if strings.TrimSpace(archiveDir) == "" {
		return 0, nil
	}
	if _, err := os.Stat(archiveDir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cleanArchive := filepath.Clean(archiveDir)
	deleted := 0
	err := filepath.WalkDir(cleanArchive, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			return nil
		}
		base := entry.Name()
		if base == "manifest.mpd" || strings.HasPrefix(base, "init-") || filepath.Ext(base) != ".m4s" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !archiveFileBeforeCutoff(path, info.ModTime(), cutoff) {
			return nil
		}

		referenced, err := h.retentionChunkFileReferenced(ctx, table, cameraID, cleanArchive, path)
		if err != nil {
			return err
		}
		if referenced {
			return nil
		}

		deleted++
		if dryRun {
			return nil
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		removeEmptyParents(path, cleanArchive)
		return nil
	})
	return deleted, err
}

func (h *Handler) retentionChunkFileReferenced(ctx context.Context, table string, cameraID string, archiveDir string, fullPath string) (bool, error) {
	paths := []string{filepath.Clean(fullPath)}
	if rel, err := filepath.Rel(archiveDir, fullPath); err == nil && rel != "" && rel != "." {
		paths = append([]string{rel}, paths...)
	}

	seen := make(map[string]bool)
	for _, chunkPath := range paths {
		if seen[chunkPath] {
			continue
		}
		seen[chunkPath] = true
		exists, err := h.machbase.ChunkPathExists(ctx, table, cameraID, chunkPath)
		if err != nil {
			return false, fmt.Errorf("%s/%s chunk_path %q check: %w", table, cameraID, chunkPath, err)
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

func archiveHasMediaFiles(archiveDir string) bool {
	if strings.TrimSpace(archiveDir) == "" {
		return false
	}
	found := false
	_ = filepath.WalkDir(archiveDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || found || entry.IsDir() {
			return nil
		}
		base := entry.Name()
		if base != "manifest.mpd" && !strings.HasPrefix(base, "init-") && filepath.Ext(base) == ".m4s" {
			found = true
		}
		return nil
	})
	return found
}

func archiveFileBeforeCutoff(path string, modTime time.Time, cutoff time.Time) bool {
	if t, ok := chunkTimeFromFilename(filepath.Base(path)); ok {
		return t.Before(cutoff)
	}
	if t, ok := dateFromPath(path); ok {
		return t.Before(dayStartUTC(cutoff))
	}
	return modTime.Before(cutoff)
}

func chunkTimeFromFilename(name string) (time.Time, bool) {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	idx := strings.LastIndex(stem, "-")
	if idx < 0 || idx == len(stem)-1 {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(stem[idx+1:], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
}

func dateFromPath(path string) (time.Time, bool) {
	dir := filepath.Base(filepath.Dir(path))
	if len(dir) != 8 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("20060102", dir, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func dayStartUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func removeEmptyParents(path string, stopDir string) {
	dir := filepath.Dir(path)
	stopDir = filepath.Clean(stopDir)
	for sameOrChildPath(dir, stopDir) && dir != stopDir {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func maxCandidateTime(candidates []db.ChunkDeleteCandidate) int64 {
	var maxTime int64
	for _, candidate := range candidates {
		if candidate.Time > maxTime {
			maxTime = candidate.Time
		}
	}
	return maxTime
}

func (h *Handler) loadRetentionConfig() (config.RetentionConfig, error) {
	cfg, err := config.LoadRaw(h.configPath)
	if err != nil {
		return config.RetentionConfig{}, fmt.Errorf("load config: %w", err)
	}
	retention := cfg.Retention
	retention.ApplyDefaults()
	if _, err := retention.KeepDuration(); err != nil {
		return config.RetentionConfig{}, err
	}
	if _, err := retention.IntervalDuration(); err != nil {
		return config.RetentionConfig{}, err
	}
	if _, err := retention.StartAtDuration(); err != nil {
		return config.RetentionConfig{}, err
	}
	return retention, nil
}

func nextRetentionRunAt(cfg config.RetentionConfig, now time.Time) (time.Time, error) {
	startAt, err := cfg.StartAtDuration()
	if err != nil {
		return time.Time{}, err
	}
	interval, err := cfg.IntervalDuration()
	if err != nil {
		return time.Time{}, err
	}

	now = now.UTC()
	next := dayStartUTC(now).Add(startAt)
	if !next.After(now) {
		elapsed := now.Sub(next)
		steps := int64(elapsed/interval) + 1
		next = next.Add(time.Duration(steps) * interval)
	}
	return next, nil
}

type retentionScheduleKey struct {
	Enabled       bool
	StartAtUTC    string
	IntervalHours int
}

func retentionScheduleSignature(cfg config.RetentionConfig) retentionScheduleKey {
	cfg.ApplyDefaults()
	return retentionScheduleKey{
		Enabled:       cfg.Enabled,
		StartAtUTC:    cfg.StartAtUTC,
		IntervalHours: cfg.IntervalHours,
	}
}

func (h *Handler) notifyRetentionScheduleReset() {
	if h.retentionScheduleReset == nil {
		return
	}
	select {
	case h.retentionScheduleReset <- struct{}{}:
		logger.GetLogger().Info("[retention] schedule reset requested")
	default:
	}
}

func (h *Handler) waitRetentionOrReset(ctx context.Context, d time.Duration) (timerFired bool, ok bool) {
	if d <= 0 {
		d = time.Second
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, false
	case <-h.retentionScheduleReset:
		return false, true
	case <-timer.C:
		return true, true
	}
}

func (h *Handler) loadRetentionCameraConfigs() (map[string]retentionCameraInfo, error) {
	cameras := make(map[string]retentionCameraInfo)
	entries, err := os.ReadDir(h.cameraDir)
	if err != nil {
		if os.IsNotExist(err) {
			return cameras, nil
		}
		return cameras, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		cameraID := strings.TrimSuffix(entry.Name(), ".json")
		data, err := os.ReadFile(filepath.Join(h.cameraDir, entry.Name()))
		if err != nil {
			return cameras, err
		}
		var cfg CameraCreateRequest
		if err := json.Unmarshal(data, &cfg); err != nil {
			return cameras, err
		}
		outputDir, archiveDir := h.resolveCameraStorageDirs(cameraID, &cfg)
		cameras[cameraID] = retentionCameraInfo{
			ID:         cameraID,
			Table:      cfg.Table,
			OutputDir:  outputDir,
			ArchiveDir: archiveDir,
			Active:     true,
		}
	}
	return cameras, nil
}

func (h *Handler) retentionCameraInfo(cameraID string, active map[string]retentionCameraInfo, deleted map[string]deletedCameraState) retentionCameraInfo {
	if info, ok := active[cameraID]; ok {
		return info
	}
	if state, ok := deleted[cameraID]; ok {
		return retentionCameraInfo{
			ID:         cameraID,
			Table:      state.Table,
			OutputDir:  state.OutputDir,
			ArchiveDir: state.ArchiveDir,
			Active:     false,
		}
	}
	return retentionCameraInfo{
		ID:         cameraID,
		OutputDir:  filepath.Join(h.dataDir, cameraID, "in"),
		ArchiveDir: filepath.Join(h.dataDir, cameraID, "out"),
		Active:     false,
	}
}

func retentionTableNames(active map[string]retentionCameraInfo, deleted map[string]deletedCameraState) []string {
	seen := make(map[string]bool)
	for _, info := range active {
		if strings.TrimSpace(info.Table) != "" {
			seen[info.Table] = true
		}
	}
	for _, state := range deleted {
		if strings.TrimSpace(state.Table) != "" {
			seen[state.Table] = true
		}
	}

	tables := make([]string, 0, len(seen))
	for table := range seen {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	return tables
}

func (h *Handler) recordDeletedCameraState(cameraID string, cfg *CameraCreateRequest) {
	if cfg == nil {
		return
	}
	outputDir, archiveDir := h.resolveCameraStorageDirs(cameraID, cfg)
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if h.deletedCameras == nil {
		h.deletedCameras = make(map[string]deletedCameraState)
	}
	h.deletedCameras[cameraID] = deletedCameraState{
		CameraID:   cameraID,
		Table:      cfg.Table,
		OutputDir:  outputDir,
		ArchiveDir: archiveDir,
		DeletedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	h.saveStateLocked()
}

func (h *Handler) snapshotDeletedCameras() map[string]deletedCameraState {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	out := make(map[string]deletedCameraState, len(h.deletedCameras))
	for k, v := range h.deletedCameras {
		out[k] = v
	}
	return out
}

func (h *Handler) pruneDeletedCameraStates(active map[string]retentionCameraInfo, deleted map[string]deletedCameraState) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	changed := false
	for cameraID, state := range deleted {
		if _, ok := active[cameraID]; ok {
			delete(h.deletedCameras, cameraID)
			changed = true
			continue
		}
		if archiveHasMediaFiles(state.ArchiveDir) {
			continue
		}
		delete(h.deletedCameras, cameraID)
		changed = true
	}
	if changed {
		h.saveStateLocked()
	}
}
