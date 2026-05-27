package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	machbaseTypeDatetime = 6
	machbaseTypeVarchar  = 5
	machbaseTypeDouble   = 20
	machbaseTypeJSON     = 61
	machbaseTableTypeTag = 6
	sysUserID            = 1
	liveDatabaseID       = -1
)

// CreateTable creates a TAG table with the standard structure.
// If the table already exists, it will be reused.
func (m *Machbase) CreateTable(ctx context.Context, tableName string) error {
	sql := fmt.Sprintf(`CREATE TAG TABLE IF NOT EXISTS %s (
    name VARCHAR(128) PRIMARY KEY,
    time DATETIME BASETIME,
    value DOUBLE SUMMARIZED,
    chunk_path VARCHAR(128)
) WITH ROLLUP TAG_PARTITION_COUNT=1, TAG_DATA_PART_SIZE=4194304`, tableName)

	if _, err := m.Query(ctx, sql); err != nil {
		return fmt.Errorf("create table %s: %w", tableName, err)
	}

	return nil
}

// CreateCameraEventTable creates {table}_event table.
// If the table already exists, it will be reused.
func (m *Machbase) CreateCameraEventTable(ctx context.Context, tableName string) error {
	eventTable := tableName + "_event"
	sqlEvent := fmt.Sprintf(`CREATE TAG TABLE IF NOT EXISTS %s_event (
    name VARCHAR(128) PRIMARY KEY,
    time DATETIME BASETIME,
    value DOUBLE,
    expression_text VARCHAR(200),
    used_counts_snapshot JSON
) METADATA (
    camera_id VARCHAR(64),
    rule_id VARCHAR(64),
    rule_name VARCHAR(128)
) TAG_PARTITION_COUNT=1, TAG_DATA_PART_SIZE=4194304`, tableName)

	if _, err := m.Query(ctx, sqlEvent); err != nil {
		if validateErr := m.ValidateCameraEventTable(ctx, tableName); validateErr == nil {
			return nil
		}
		return fmt.Errorf("create table %s: %w", eventTable, err)
	}

	if err := m.ValidateCameraEventTable(ctx, tableName); err != nil {
		return fmt.Errorf("validate table %s: %w", eventTable, err)
	}

	return nil
}

// ValidateCameraEventTable verifies that {table}_event is the expected live SYS TAG table.
// Length is intentionally ignored for VARCHAR columns; user-created compatible
// tables can be reused when the required column names and data types match.
func (m *Machbase) ValidateCameraEventTable(ctx context.Context, tableName string) error {
	eventTable := strings.ToUpper(tableName + "_event")
	expected := map[string]int{
		"NAME":                 machbaseTypeVarchar,
		"TIME":                 machbaseTypeDatetime,
		"VALUE":                machbaseTypeDouble,
		"EXPRESSION_TEXT":      machbaseTypeVarchar,
		"USED_COUNTS_SNAPSHOT": machbaseTypeJSON,
		"CAMERA_ID":            machbaseTypeVarchar,
		"RULE_ID":              machbaseTypeVarchar,
		"RULE_NAME":            machbaseTypeVarchar,
	}

	sql := fmt.Sprintf(`
SELECT c.NAME, c.TYPE
FROM M$SYS_COLUMNS c, M$SYS_TABLES t
WHERE c.TABLE_ID = t.ID
  AND c.DATABASE_ID = t.DATABASE_ID
  AND t.NAME = '%s'
  AND t.TYPE = %d
  AND t.DATABASE_ID = %d
  AND t.USER_ID = %d
ORDER BY c.ID`,
		escapeSQLLiteral(eventTable),
		machbaseTableTypeTag,
		liveDatabaseID,
		sysUserID,
	)

	resp, err := m.Query(ctx, sql)
	if err != nil {
		return fmt.Errorf("read columns: %w", err)
	}

	var rows []struct {
		Name string `json:"NAME"`
		Type int    `json:"TYPE"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return fmt.Errorf("parse columns: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("%s does not exist as live SYS TAG table", eventTable)
	}

	actual := make(map[string]int, len(rows))
	for _, row := range rows {
		actual[strings.ToUpper(row.Name)] = row.Type
	}

	var missing []string
	var mismatched []string
	for name, expectedType := range expected {
		actualType, ok := actual[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if actualType != expectedType {
			mismatched = append(mismatched, fmt.Sprintf("%s expected TYPE=%d got TYPE=%d", name, expectedType, actualType))
		}
	}
	sort.Strings(missing)
	sort.Strings(mismatched)
	if len(missing) > 0 || len(mismatched) > 0 {
		parts := make([]string, 0, 2)
		if len(missing) > 0 {
			parts = append(parts, "missing columns: "+strings.Join(missing, ", "))
		}
		if len(mismatched) > 0 {
			parts = append(parts, "type mismatch: "+strings.Join(mismatched, "; "))
		}
		return fmt.Errorf("%s schema mismatch: %s", eventTable, strings.Join(parts, "; "))
	}
	return nil
}

// TableExists returns true if a TAG table with the given name exists.
func (m *Machbase) TableExists(ctx context.Context, tableName string) (bool, error) {
	sql := fmt.Sprintf("SELECT COUNT(*) as CNT FROM M$SYS_TABLES WHERE NAME = '%s' AND TYPE = 6",
		escapeSQLLiteral(strings.ToUpper(tableName)))
	resp, err := m.Query(ctx, sql)
	if err != nil {
		return false, err
	}
	var rows []struct {
		Count int64 `json:"CNT"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return false, err
	}
	return len(rows) > 0 && rows[0].Count > 0, nil
}

// CreateCameraLogTable creates {table}_log table.
// If the table already exists, it will be reused.
func (m *Machbase) CreateCameraLogTable(ctx context.Context, tableName string) error {
	sqlLog := fmt.Sprintf(`CREATE TAG TABLE IF NOT EXISTS %s_log (
    name VARCHAR(128) PRIMARY KEY,
    time DATETIME BASETIME,
    value DOUBLE,
    model_id INTEGER
) METADATA (
    camera_id VARCHAR(64),
    ident VARCHAR(64)
) TAG_PARTITION_COUNT=1, TAG_DATA_PART_SIZE=4194304`, tableName)

	if _, err := m.Query(ctx, sqlLog); err != nil {
		return fmt.Errorf("create table %s_log: %w", tableName, err)
	}

	return nil
}

// CreateCameraTables creates 3 tables for a camera: {name}, {name}_event, {name}_log
// If tables already exist, they will be reused (multiple cameras can share the same table).
func (m *Machbase) CreateCameraTables(ctx context.Context, name string) error {
	// 1. Main camera table (video chunks)
	sqlMain := fmt.Sprintf(`CREATE TAG TABLE IF NOT EXISTS %s (
    name VARCHAR(128) PRIMARY KEY,
    time DATETIME BASETIME,
    value DOUBLE SUMMARIZED,
    chunk_path VARCHAR(128)
) WITH ROLLUP TAG_PARTITION_COUNT=1, TAG_DATA_PART_SIZE=4194304`, name)

	if _, err := m.Query(ctx, sqlMain); err != nil {
		return fmt.Errorf("create table %s: %w", name, err)
	}

	// 2. Event table (DSL evaluation results)
	sqlEvent := fmt.Sprintf(`CREATE TAG TABLE IF NOT EXISTS %s_event (
    name VARCHAR(128) PRIMARY KEY,
    time DATETIME BASETIME,
    value DOUBLE,
    expression_text VARCHAR(200),
    used_counts_snapshot JSON
) METADATA (
    camera_id VARCHAR(64),
    rule_id VARCHAR(64),
    rule_name VARCHAR(128)
) TAG_PARTITION_COUNT=1, TAG_DATA_PART_SIZE=4194304`, name)

	if _, err := m.Query(ctx, sqlEvent); err != nil {
		return fmt.Errorf("create table %s_event: %w", name, err)
	}

	// 3. Log table (detection counts per ident)
	sqlLog := fmt.Sprintf(`CREATE TAG TABLE IF NOT EXISTS %s_log (
    name VARCHAR(128) PRIMARY KEY,
    time DATETIME BASETIME,
    value DOUBLE,
    model_id INTEGER
) METADATA (
    camera_id VARCHAR(64),
    ident VARCHAR(64)
) TAG_PARTITION_COUNT=1, TAG_DATA_PART_SIZE=4194304`, name)
	// model_id VARCHAR(64)

	if _, err := m.Query(ctx, sqlLog); err != nil {
		return fmt.Errorf("create table %s_log: %w", name, err)
	}

	return nil
}

// CameraLogRow represents a detection log entry for {camera}_log table.
type CameraLogRow struct {
	Name     string  // cameraID.ident (ex: camera1.person)
	Time     int64   // nanoseconds
	Value    float64 // detection count
	ModelID  int64
	CameraID string // metadata
	Ident    string // metadata
}

func (m *Machbase) InsertCameraLogs(ctx context.Context, table string, logs []CameraLogRow) error {
	columns := []string{"name", "time", "value", "model_id", "camera_id", "ident"}
	rows := make([][]any, len(logs))
	for i, l := range logs {
		rows[i] = []any{l.Name, l.Time, l.Value, l.ModelID, l.CameraID, l.Ident}
	}
	return m.WriteRows(ctx, table, columns, rows)
}

// CameraEventRow represents a DSL evaluation result for {camera}_event table.
type CameraEventRow struct {
	Name               string  // camera_id.rule_id
	Time               int64   // nanoseconds
	Value              float64 // 2=MATCH, 1=TRIGGER, 0=RESOLVE, -1=ERROR
	ExpressionText     string
	UsedCountsSnapshot string // JSON string
	CameraID           string // metadata
	RuleID             string // metadata
	RuleName           string // metadata
}

func (m *Machbase) InsertCameraEvents(ctx context.Context, table string, events []CameraEventRow) error {
	columns := []string{"name", "time", "value", "expression_text", "used_counts_snapshot", "camera_id", "rule_id", "rule_name"}
	rows := make([][]any, len(events))
	for i, e := range events {
		rows[i] = []any{e.Name, e.Time, e.Value, e.ExpressionText, e.UsedCountsSnapshot, e.CameraID, e.RuleID, e.RuleName}
	}
	return m.WriteRows(ctx, table, columns, rows)
}

// CameraEventQueryRow represents a queried event row.
type CameraEventQueryRow struct {
	Name               string    `json:"name"`
	Time               time.Time `json:"time"`
	Value              float64   `json:"value"`
	ExpressionText     string    `json:"expression_text"`
	UsedCountsSnapshot string    `json:"used_counts_snapshot"`
	CameraID           string    `json:"camera_id"`
	RuleID             string    `json:"rule_id"`
	RuleName           string    `json:"rule_name"`
}

// CameraEventFilter holds optional filters for QueryCameraEvents.
type CameraEventFilter struct {
	CameraID  string   // filter by camera_id metadata column
	EventName string   // filter by name column (e.g. "camera_id.rule_id")
	EventType *float64 // filter by value column (2=MATCH, 1=TRIGGER, 0=RESOLVE, -1=ERROR)
	Limit     int      // max rows to return
	Offset    int      // rows to skip
}

// QueryCameraEvents queries {table}_event with time range and optional filters.
func (m *Machbase) QueryCameraEvents(ctx context.Context, table string, startNs, endNs int64, filter *CameraEventFilter) ([]CameraEventQueryRow, error) {
	safeTable := escapeSQLLiteral(table)
	var where string
	if startNs <= 0 {
		where = fmt.Sprintf("time <= %d", endNs)
	} else {
		where = fmt.Sprintf("time BETWEEN %d AND %d", startNs, endNs)
	}

	if filter != nil {
		if filter.CameraID != "" {
			where += fmt.Sprintf(" AND camera_id = '%s'", escapeSQLLiteral(filter.CameraID))
		}
		if filter.EventName != "" {
			where += fmt.Sprintf(" AND name LIKE '%%%s%%'", escapeSQLLiteral(filter.EventName))
		}
		if filter.EventType != nil {
			where += fmt.Sprintf(" AND value = %g", *filter.EventType)
		}
	}

	pagination := ""
	if filter != nil {
		if filter.Limit > 0 {
			pagination += fmt.Sprintf(" LIMIT %d", filter.Limit)
		}
		if filter.Offset > 0 {
			pagination += fmt.Sprintf(" OFFSET %d", filter.Offset)
		}
	}

	sql := fmt.Sprintf(
		"SELECT name, time, value, expression_text, used_counts_snapshot, camera_id, rule_id, rule_name "+
			"FROM %s_event WHERE %s ORDER BY time DESC %s",
		safeTable, where, pagination,
	)

	resp, err := m.Query(ctx, sql, WithTimeformat("ns"))
	if err != nil {
		return nil, err
	}

	var rows []struct {
		Name               string  `json:"NAME"`
		Time               int64   `json:"TIME"`
		Value              float64 `json:"VALUE"`
		ExpressionText     string  `json:"EXPRESSION_TEXT"`
		UsedCountsSnapshot string  `json:"USED_COUNTS_SNAPSHOT"`
		CameraID           string  `json:"CAMERA_ID"`
		RuleID             string  `json:"RULE_ID"`
		RuleName           string  `json:"RULE_NAME"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return nil, err
	}

	result := make([]CameraEventQueryRow, len(rows))
	for i, r := range rows {
		result[i] = CameraEventQueryRow{
			Name:               r.Name,
			Time:               time.Unix(0, r.Time),
			Value:              r.Value,
			ExpressionText:     r.ExpressionText,
			UsedCountsSnapshot: r.UsedCountsSnapshot,
			CameraID:           r.CameraID,
			RuleID:             r.RuleID,
			RuleName:           r.RuleName,
		}
	}
	return result, nil
}

// UpdateEventRuleName updates the rule_name metadata column for all event rows
// whose tag name matches {cameraID}.{ruleID}.
// Machbase TAG 테이블은 동일한 name(태그)에 속한 모든 행이 같은 메타데이터를 공유하므로,
// rule name이 변경될 때 이 메서드로 Machbase 쪽 메타를 일치시킵니다.
func (m *Machbase) UpdateEventRuleName(ctx context.Context, table string, cameraID string, ruleID string, newRuleName string) error {
	tagName := cameraID + "." + ruleID
	sql := fmt.Sprintf(
		"UPDATE %s_event METADATA SET rule_name = '%s' WHERE name = '%s'",
		escapeSQLLiteral(table),
		escapeSQLLiteral(newRuleName),
		escapeSQLLiteral(tagName),
	)
	if _, err := m.Query(ctx, sql); err != nil {
		return fmt.Errorf("update rule_name for %s: %w", tagName, err)
	}
	return nil
}

// CountCameraEvents returns the count of events in {table}_event within a time range.
// Optional filter applies the same WHERE conditions as QueryCameraEvents (without LIMIT/OFFSET).
func (m *Machbase) CountCameraEvents(ctx context.Context, table string, startNs, endNs int64, filter *CameraEventFilter) (int64, error) {
	safeTable := escapeSQLLiteral(table)
	// startNs=0 이면 Machbase가 0을 INT32로 파싱해 DATETIME 비교 오류 발생.
	// startNs <= 0 인 경우 하한 없이 전체 기간 조회.
	var where string
	if startNs <= 0 {
		where = fmt.Sprintf("time <= %d", endNs)
	} else {
		where = fmt.Sprintf("time BETWEEN %d AND %d", startNs, endNs)
	}

	if filter != nil {
		if filter.CameraID != "" {
			where += fmt.Sprintf(" AND camera_id = '%s'", escapeSQLLiteral(filter.CameraID))
		}
		if filter.EventName != "" {
			where += fmt.Sprintf(" AND name LIKE '%%%s%%'", escapeSQLLiteral(filter.EventName))
		}
		if filter.EventType != nil {
			where += fmt.Sprintf(" AND value = %g", *filter.EventType)
		}
	}

	sql := fmt.Sprintf("SELECT count(*) FROM %s_event WHERE %s", safeTable, where)

	resp, err := m.Query(ctx, sql)
	if err != nil {
		return 0, err
	}

	var rows []struct {
		Count int64 `json:"COUNT(*)"`
	}
	if err := json.Unmarshal(resp.Data.Rows, &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Count, nil
}
