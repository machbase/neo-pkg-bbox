package db

import (
	"context"
	"encoding/json"
	"fmt"
)

type ChunkDeleteCandidate struct {
	Time      int64
	ChunkPath string
}

type TagMetadataRow struct {
	Name     string
	CameraID string
}

func (m *Machbase) ListTagNames(ctx context.Context, table string) ([]string, error) {
	sql := fmt.Sprintf("SELECT name AS NAME FROM %s METADATA ORDER BY name", escapeSQLLiteral(table))
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

	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Name != "" {
			out = append(out, row.Name)
		}
	}
	return out, nil
}

func (m *Machbase) ListMetadataCameraIDs(ctx context.Context, table string) ([]string, error) {
	sql := fmt.Sprintf("SELECT camera_id AS CAMERA_ID FROM %s METADATA GROUP BY camera_id ORDER BY camera_id", escapeSQLLiteral(table))
	resp, err := m.Query(ctx, sql)
	if err != nil {
		return nil, err
	}

	var rows []struct {
		CameraID string `json:"CAMERA_ID"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.CameraID != "" {
			out = append(out, row.CameraID)
		}
	}
	return out, nil
}

func (m *Machbase) ListMetadataNamesByCamera(ctx context.Context, table string, cameraID string) ([]string, error) {
	sql := fmt.Sprintf(
		"SELECT name AS NAME FROM %s METADATA WHERE camera_id = '%s' ORDER BY name",
		escapeSQLLiteral(table),
		escapeSQLLiteral(cameraID),
	)
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

	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.Name != "" {
			out = append(out, row.Name)
		}
	}
	return out, nil
}

func (m *Machbase) SelectChunkDeleteCandidatesBefore(ctx context.Context, table string, tagName string, cutoffNs int64, limit int) ([]ChunkDeleteCandidate, error) {
	if limit <= 0 {
		limit = 1000
	}
	sql := fmt.Sprintf(
		"SELECT time, chunk_path FROM %s WHERE name = '%s' ORDER BY time LIMIT %d",
		escapeSQLLiteral(table),
		escapeSQLLiteral(tagName),
		limit,
	)
	resp, err := m.Query(ctx, sql, WithTimeformat("ns"))
	if err != nil {
		return nil, err
	}

	var rows []struct {
		Time      int64  `json:"time"`
		ChunkPath string `json:"chunk_path"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return nil, err
	}

	out := make([]ChunkDeleteCandidate, 0, len(rows))
	for _, row := range rows {
		if row.Time >= cutoffNs {
			break
		}
		out = append(out, ChunkDeleteCandidate{
			Time:      row.Time,
			ChunkPath: row.ChunkPath,
		})
	}
	return out, nil
}

func (m *Machbase) CountTagRowsBefore(ctx context.Context, table string, tagName string, cutoffNs int64) (int64, error) {
	const pageSize = 1000
	var total int64
	for offset := 0; ; offset += pageSize {
		sql := fmt.Sprintf(
			"SELECT time FROM %s WHERE name = '%s' ORDER BY time LIMIT %d OFFSET %d",
			escapeSQLLiteral(table),
			escapeSQLLiteral(tagName),
			pageSize,
			offset,
		)
		resp, err := m.Query(ctx, sql, WithTimeformat("ns"))
		if err != nil {
			return 0, err
		}

		var rows []struct {
			Time int64 `json:"time"`
		}
		if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			return total, nil
		}
		for _, row := range rows {
			if row.Time >= cutoffNs {
				return total, nil
			}
			total++
		}
		if len(rows) < pageSize {
			return total, nil
		}
	}
}

func (m *Machbase) DeleteTagRowsBefore(ctx context.Context, table string, tagName string, cutoffNs int64) error {
	sql := fmt.Sprintf(
		"DELETE FROM %s WHERE name = '%s' AND time < %d",
		escapeSQLLiteral(table),
		escapeSQLLiteral(tagName),
		cutoffNs,
	)
	_, err := m.Query(ctx, sql)
	return err
}

func (m *Machbase) DeleteTagRowsThrough(ctx context.Context, table string, tagName string, maxTimeNs int64) error {
	sql := fmt.Sprintf(
		"DELETE FROM %s WHERE name = '%s' AND time <= %d",
		escapeSQLLiteral(table),
		escapeSQLLiteral(tagName),
		maxTimeNs,
	)
	_, err := m.Query(ctx, sql)
	return err
}

func (m *Machbase) ChunkPathExists(ctx context.Context, table string, tagName string, chunkPath string) (bool, error) {
	sql := fmt.Sprintf(
		"SELECT 1 AS HAS_ROW FROM %s WHERE name = '%s' AND chunk_path = '%s' LIMIT 1",
		escapeSQLLiteral(table),
		escapeSQLLiteral(tagName),
		escapeSQLLiteral(chunkPath),
	)
	resp, err := m.Query(ctx, sql)
	if err != nil {
		return false, err
	}

	var rows []struct {
		HasRow int `json:"HAS_ROW"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func (m *Machbase) TagHasRows(ctx context.Context, table string, tagName string) (bool, error) {
	sql := fmt.Sprintf(
		"SELECT 1 AS HAS_ROW FROM %s WHERE name = '%s' LIMIT 1",
		escapeSQLLiteral(table),
		escapeSQLLiteral(tagName),
	)
	resp, err := m.Query(ctx, sql)
	if err != nil {
		return false, err
	}

	var rows []struct {
		HasRow int `json:"HAS_ROW"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func (m *Machbase) DeleteMetadataByName(ctx context.Context, table string, tagName string) error {
	sql := fmt.Sprintf(
		"DELETE FROM %s METADATA WHERE name = '%s'",
		escapeSQLLiteral(table),
		escapeSQLLiteral(tagName),
	)
	_, err := m.Query(ctx, sql)
	return err
}

func (m *Machbase) DeleteMetadataByCamera(ctx context.Context, table string, cameraID string) error {
	sql := fmt.Sprintf(
		"DELETE FROM %s METADATA WHERE camera_id = '%s'",
		escapeSQLLiteral(table),
		escapeSQLLiteral(cameraID),
	)
	_, err := m.Query(ctx, sql)
	return err
}
