package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/machbase/neo-pkg-bbox/internal/logger"
)

// CameraMetadataRow represents a camera metadata row.
type CameraMetadataRow struct {
	Name   string
	Prefix string
	FPS    *int
}

type metaRow struct {
	Name   string `json:"NAME"`
	Prefix string `json:"PREFIX"`
	FPS    *int   `json:"FPS"`
}

// Metadata fetches metadata for a given tag.
func (m *Machbase) Metadata(ctx context.Context, tag string) (*CameraMetadataRow, error) {
	safeTag := escapeSQLLiteral(tag)

	// Try first query
	sql := fmt.Sprintf("select name, prefix, fps from _blackbox3_meta where name = '%s'", safeTag)
	resp, err := m.Query(ctx, sql)
	if err == nil && resp.Data.Rows != nil {
		var rows []metaRow
		if json.Unmarshal(resp.Data.Rows, &rows) == nil && len(rows) > 0 {
			return &CameraMetadataRow{
				Name:   rows[0].Name,
				Prefix: rows[0].Prefix,
				FPS:    rows[0].FPS,
			}, nil
		}
	}

	// Try second query
	type metaRow2 struct {
		Prefix string `json:"PREFIX"`
		FPS    *int   `json:"FPS"`
	}
	sql = fmt.Sprintf("select prefix, fps from blackbox3 metadata where name = '%s'", safeTag)
	resp, err = m.Query(ctx, sql)
	if err == nil && resp.Data.Rows != nil {
		var rows []metaRow2
		if json.Unmarshal(resp.Data.Rows, &rows) == nil && len(rows) > 0 {
			return &CameraMetadataRow{
				Name:   tag,
				Prefix: rows[0].Prefix,
				FPS:    rows[0].FPS,
			}, nil
		}
	}

	return nil, nil
}

// CameraMetadata fetches all camera metadata.
func (m *Machbase) CameraMetadata(ctx context.Context) ([]CameraMetadataRow, error) {
	// Try first query
	sql := "select name, prefix, fps from _blackbox3_meta"
	resp, err := m.Query(ctx, sql)
	if err == nil && resp.Data.Rows != nil {
		var rows []metaRow
		if json.Unmarshal(resp.Data.Rows, &rows) == nil && len(rows) > 0 {
			results := make([]CameraMetadataRow, len(rows))
			for i, r := range rows {
				results[i] = CameraMetadataRow{
					Name:   r.Name,
					Prefix: r.Prefix,
					FPS:    r.FPS,
				}
			}
			return results, nil
		}
	}

	// Try second query
	sql = "select name, prefix, fps from blackbox3 metadata"
	resp, err = m.Query(ctx, sql)
	if err == nil && resp.Data.Rows != nil {
		var rows []metaRow
		if json.Unmarshal(resp.Data.Rows, &rows) == nil && len(rows) > 0 {
			results := make([]CameraMetadataRow, len(rows))
			for i, r := range rows {
				results[i] = CameraMetadataRow{
					Name:   r.Name,
					Prefix: r.Prefix,
					FPS:    r.FPS,
				}
			}
			return results, nil
		}
	}

	return nil, nil
}

// BlackboxStats represents blackbox statistics.
type BlackboxStats struct {
	Name    string
	MinTime time.Time
	MaxTime time.Time
}

// BlackboxStatsByTag fetches blackbox statistics for a camera.
// tableName: the table name (e.g., "camera2")
// cameraID: the camera ID stored in the 'name' column (e.g., "camera1")
func (m *Machbase) BlackboxStatsByTag(ctx context.Context, tableName string, cameraID string) (*BlackboxStats, error) {
	safeTable := escapeSQLLiteral(tableName)
	safeCameraID := escapeSQLLiteral(cameraID)
	// 카메라별 STAT 테이블: V${TABLE_NAME}_STAT
	// Note: Machbase system views use uppercase
	statTable := fmt.Sprintf("V$%s_STAT", strings.ToUpper(safeTable))
	sql := fmt.Sprintf(
		"select name, min_time, max_time from %s where name = '%s'",
		statTable, safeCameraID,
	)

	resp, err := m.Query(ctx, sql, WithTimeformat("ns"))
	if err != nil {
		return nil, err
	}

	var rows []struct {
		Name    string `json:"NAME"`
		MinTime int64  `json:"MIN_TIME"`
		MaxTime int64  `json:"MAX_TIME"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	row := rows[0]
	return &BlackboxStats{
		Name:    row.Name,
		MinTime: time.Unix(0, row.MinTime),
		MaxTime: time.Unix(0, row.MaxTime),
	}, nil
}

// BlackboxTimeBounds fetches time bounds for a camera.
// tableName: the table name (e.g., "camera2")
// cameraID: the camera ID stored in the 'name' column (e.g., "camera1")
func (m *Machbase) BlackboxTimeBounds(ctx context.Context, tableName string, cameraID string) (*BlackboxStats, error) {
	safeTable := escapeSQLLiteral(tableName)
	safeCameraID := escapeSQLLiteral(cameraID)
	sql := fmt.Sprintf(
		"select min(time) as min_time, max(time) as max_time from %s where name = '%s'",
		safeTable, safeCameraID,
	)

	resp, err := m.Query(ctx, sql, WithTimeformat("ns"))
	if err != nil {
		return nil, err
	}

	var rows []struct {
		MinTime int64 `json:"MIN_TIME"`
		MaxTime int64 `json:"MAX_TIME"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	row := rows[0]
	if row.MinTime == 0 && row.MaxTime == 0 {
		return nil, nil
	}

	return &BlackboxStats{
		Name:    cameraID,
		MinTime: time.Unix(0, row.MinTime),
		MaxTime: time.Unix(0, row.MaxTime),
	}, nil
}

// BlackboxChunkInterval calculates the chunk interval for a camera.
// tableName: the table name (e.g., "camera2")
// cameraID: the camera ID stored in the 'name' column (e.g., "camera1")
func (m *Machbase) BlackboxChunkInterval(ctx context.Context, tableName string, cameraID string) (float64, error) {
	safeTable := escapeSQLLiteral(tableName)
	safeCameraID := escapeSQLLiteral(cameraID)
	sql := fmt.Sprintf(
		"select time from %s where name = '%s' order by time limit 2",
		safeTable, safeCameraID,
	)

	resp, err := m.Query(ctx, sql, WithTimeformat("ns"))
	if err != nil {
		return 0, err
	}

	var rows []struct {
		Time int64 `json:"TIME"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return 0, err
	}
	if len(rows) < 2 {
		return 0, nil
	}

	t1 := time.Unix(0, rows[0].Time)
	t2 := time.Unix(0, rows[1].Time)

	delta := t2.Sub(t1).Seconds()
	if delta <= 0 {
		return 0, nil
	}
	return delta, nil
}

// ListTags fetches all distinct tags.
// NOTE: Since each camera has its own table, this method needs to be implemented differently.
// Consider using GetCameras handler which reads from camera config files instead.
func (m *Machbase) ListTags(ctx context.Context) ([]string, error) {
	// TODO: Implement based on camera config files or system tables
	// For now, return empty list
	return []string{}, nil
}

// ListTagTables returns chunk-only TAG table names (TYPE=6, FLAG=0) in lowercase.
// _event, _log 파생 테이블을 제외하고 청크 전용 메인 테이블만 반환.
// 카메라 고아 설정파일 탐지용: 메인 테이블이 없을 때만 설정파일을 삭제하기 위함.
func (m *Machbase) ListTagTables(ctx context.Context) ([]string, error) {
	sql := "SELECT NAME FROM M$SYS_TABLES WHERE TYPE=6 AND FLAG=0 AND NAME NOT LIKE '%_EVENT' AND NAME NOT LIKE '%_LOG' ORDER BY NAME"
	resp, err := m.Query(ctx, sql)
	if err != nil {
		return nil, err
	}

	var rows []struct {
		Name string `json:"NAME"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, strings.ToLower(r.Name))
	}
	return names, nil
}

// ListTables fetches TAG table names from Machbase that match the video chunk table schema.
// Only returns tables with columns: name, time, value, chunk_path
// Excludes _event and _log suffixed tables.
func (m *Machbase) ListTables(ctx context.Context) ([]string, error) {
	// First, get all TAG tables (TYPE = 6) with their IDs
	sql := "SELECT ID, NAME FROM M$SYS_TABLES WHERE TYPE = 6 ORDER BY NAME"
	resp, err := m.Query(ctx, sql)
	if err != nil {
		return nil, err
	}

	var tableRows []struct {
		ID   int64  `json:"ID"`
		Name string `json:"NAME"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &tableRows); err != nil {
		return nil, err
	}

	var tables []string
	// Check each table's column structure
	for _, tbl := range tableRows {
		name := strings.ToLower(tbl.Name)

		// Skip _event and _log tables
		if strings.HasSuffix(name, "_event") || strings.HasSuffix(name, "_log") {
			continue
		}

		// Get columns for this table
		columnSQL := fmt.Sprintf("SELECT NAME FROM M$SYS_COLUMNS WHERE TABLE_ID = %d ORDER BY NAME", tbl.ID)
		columnResp, err := m.Query(ctx, columnSQL)
		if err != nil {
			logger.GetLogger().Warnf("ListTables: failed to query columns for table %s: %v", name, err)
			continue
		}

		var columnRows []struct {
			Name string `json:"NAME"`
		}
		if err := json.Unmarshal(columnResp.Data.Rows, &columnRows); err != nil {
			logger.GetLogger().Warnf("ListTables: failed to parse columns for table %s: %v", name, err)
			continue
		}

		// Check if table has the required columns: name, time, value, chunk_path
		requiredColumns := map[string]bool{
			"NAME":       false,
			"TIME":       false,
			"VALUE":      false,
			"CHUNK_PATH": false,
		}

		for _, col := range columnRows {
			upperName := strings.ToUpper(col.Name)
			if _, exists := requiredColumns[upperName]; exists {
				requiredColumns[upperName] = true
			}
		}

		// Only include table if all required columns are present
		hasAllColumns := true
		for _, found := range requiredColumns {
			if !found {
				hasAllColumns = false
				break
			}
		}

		if hasAllColumns {
			tables = append(tables, name)
		}
	}
	return tables, nil
}

// ChunkRecord represents a chunk record.
type ChunkRecord struct {
	ChunkPath string // 파일 경로
	EntryTime time.Time
	Length    float64 // 길이 (초)
}

// ChunkRecordForTime fetches chunk record for a specific time.
// tableName: the table name (e.g., "camera2")
// cameraID: the camera ID stored in the 'name' column (e.g., "camera1")
func (m *Machbase) ChunkRecordForTime(ctx context.Context, tableName string, cameraID string, ts time.Time) (*ChunkRecord, error) {
	safeTable := escapeSQLLiteral(tableName)
	safeCameraID := escapeSQLLiteral(cameraID)
	tsNs := ts.UnixNano()

	// Find chunk where: chunk_start <= ts+1ms <= chunk_end
	// +1ms buffer: 프론트(밀리초 정밀도)와 DB(나노초 정밀도) 차이를 보정
	// 같은 밀리초 내의 나노초 차이로 인한 404를 방지
	const msBuffer int64 = 1_000_000 // 1ms in nanoseconds
	// 비디오 청크 평균 길이가 5-6초, 안전 버퍼로 15초 사용
	// 하한선(time >= ts-15초)으로 스캔 범위를 제한하여 데이터가 많을 때 성능 개선
	const maxChunkDuration int64 = 15_000_000_000 // 15 seconds in nanoseconds
	sql := fmt.Sprintf(
		"select /*+ SCAN_FORWARD(%s) */ time, value, chunk_path from %s "+
			"where name = '%s' "+
			"and time <= %d "+ // 상한선 +1ms
			"and time >= %d "+ // 하한선 -15ms
			"and %d <= to_timestamp(time) + (value * 1000000000) "+
			"order by time desc limit 1", // desc 추가
		safeTable, safeTable, safeCameraID, tsNs+msBuffer, tsNs-maxChunkDuration, tsNs,
	)

	logger.GetLogger().Debugf("[CHUNK_QUERY] camera=%s, table=%s, ts_ns=%d", cameraID, safeTable, tsNs)

	resp, err := m.Query(ctx, sql, WithTimeformat("ns"))
	if err != nil {
		return nil, err
	}

	var rows []struct {
		Time      int64   `json:"TIME"`
		Value     float64 `json:"VALUE"`      // 길이 (초)
		ChunkPath string  `json:"CHUNK_PATH"` // 파일 경로
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	row := rows[0]
	return &ChunkRecord{
		ChunkPath: row.ChunkPath,
		EntryTime: time.Unix(0, row.Time),
		Length:    row.Value,
	}, nil
}

// RollupRow represents a rollup row.
type RollupRow struct {
	Time      time.Time
	SumLength float64
}

// CameraRollup fetches rollup data for a camera.
// tableName: the table name (e.g., "camera2")
// cameraID: the camera ID stored in the 'name' column (e.g., "camera1")
func (m *Machbase) CameraRollup(ctx context.Context, tableName string, cameraID string, minutes int, startNs, endNs int64) ([]RollupRow, error) {
	safeTable := escapeSQLLiteral(tableName)
	safeCameraID := escapeSQLLiteral(cameraID)
	sql := fmt.Sprintf(
		"select rollup('min', %d, time) as time, sum(value) as total_length "+
			"from %s where name = '%s' and time between %d and %d group by time order by time",
		minutes, safeTable, safeCameraID, startNs, endNs,
	)

	logger.GetLogger().Debugf("Machbase SQL (rollup): %s | minutes=%d | camera=%s | table=%s | start_ns=%d | end_ns=%d",
		sql, minutes, cameraID, safeTable, startNs, endNs)

	resp, err := m.Query(ctx, sql, WithTimeformat("ns"))
	if err != nil {
		return nil, err
	}

	var rows []struct {
		Time        int64   `json:"TIME"`
		TotalLength float64 `json:"TOTAL_LENGTH"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return nil, err
	}

	result := make([]RollupRow, len(rows))
	for i, r := range rows {
		result[i] = RollupRow{
			Time:      time.Unix(0, r.Time),
			SumLength: r.TotalLength,
		}
	}
	return result, nil
}

// DataGapRow represents a single time point from rollup query.
type DataGapRow struct {
	Time  time.Time `json:"time"`
	Count int       `json:"count"`
}

// GetRollupData fetches data with 5-second rollup interval.
// tableName: the table name (e.g., "camera1")
// cameraID: the camera ID stored in the 'name' column
// start, end: time range in nanoseconds
func (m *Machbase) GetRollupData(ctx context.Context, tableName string, cameraID string, start, end time.Time, interval int) ([]DataGapRow, error) {
	safeTable := escapeSQLLiteral(tableName)
	safeCameraID := escapeSQLLiteral(cameraID)
	startNs := start.UnixNano()
	endNs := end.UnixNano()

	// Origin 생략 (Machbase 기본값 사용, 핸들러에서 실제 데이터로 추론)
	sql := fmt.Sprintf(
		"SELECT rollup('sec', %d, time) as time, count(value) as value "+
			"FROM %s WHERE name = '%s' AND time BETWEEN %d AND %d "+
			"GROUP BY time ORDER BY time",
		interval, safeTable, safeCameraID, startNs, endNs,
	)

	logger.GetLogger().Debugf("GetRollupData SQL: %s", sql)

	resp, err := m.Query(ctx, sql, WithTimeformat("ns"))
	if err != nil {
		return nil, err
	}

	var rows []struct {
		Time  int64 `json:"TIME"`
		Value int   `json:"VALUE"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return nil, err
	}

	result := make([]DataGapRow, len(rows))
	for i, r := range rows {
		result[i] = DataGapRow{
			Time:  time.Unix(0, r.Time),
			Count: r.Value,
		}
	}
	return result, nil
}
