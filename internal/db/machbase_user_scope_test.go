package db

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeRows(t *testing.T, w http.ResponseWriter, columns []string, rows any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"data": map[string]any{
			"columns": columns,
			"types":   make([]string, len(columns)),
			"rows":    rows,
		},
	}))
}

func assertCurrentUserScope(t *testing.T, sql string) {
	t.Helper()
	assert.Contains(t, sql, "USER_ID = CURRENT_USER_ID()")
	assert.NotContains(t, sql, "USER_ID = 1")
}

func TestListTagTablesScopesToCurrentUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertCurrentUserScope(t, r.URL.Query().Get("q"))
		writeRows(t, w, []string{"NAME"}, []map[string]any{{"NAME": "CAMERA_A"}})
	}))
	defer srv.Close()

	client, err := NewMachbase(testMachbaseConfig(t, srv.URL, "MACHBASEDB"))
	require.NoError(t, err)

	tables, err := client.ListTagTables(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"camera_a"}, tables)
}

func TestListTablesScopesToCurrentUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sql := r.URL.Query().Get("q")
		if strings.Contains(sql, "SELECT ID, NAME") {
			assertCurrentUserScope(t, sql)
			writeRows(t, w, []string{"ID", "NAME"}, []map[string]any{{"ID": 42, "NAME": "CAMERA_A"}})
			return
		}
		writeRows(t, w, []string{"NAME"}, []map[string]any{
			{"NAME": "CHUNK_PATH"},
			{"NAME": "NAME"},
			{"NAME": "TIME"},
			{"NAME": "VALUE"},
		})
	}))
	defer srv.Close()

	client, err := NewMachbase(testMachbaseConfig(t, srv.URL, "MACHBASEDB"))
	require.NoError(t, err)

	tables, err := client.ListTables(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"camera_a"}, tables)
}

func TestValidateCameraEventTableScopesToCurrentUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Query().Get("q"), "t.USER_ID = CURRENT_USER_ID()")
		assert.NotContains(t, r.URL.Query().Get("q"), "t.USER_ID = 1")
		writeRows(t, w, []string{"NAME", "TYPE"}, []map[string]any{
			{"NAME": "NAME", "TYPE": machbaseTypeVarchar},
			{"NAME": "TIME", "TYPE": machbaseTypeDatetime},
			{"NAME": "VALUE", "TYPE": machbaseTypeDouble},
			{"NAME": "EXPRESSION_TEXT", "TYPE": machbaseTypeVarchar},
			{"NAME": "USED_COUNTS_SNAPSHOT", "TYPE": machbaseTypeJSON},
			{"NAME": "CAMERA_ID", "TYPE": machbaseTypeVarchar},
			{"NAME": "RULE_ID", "TYPE": machbaseTypeVarchar},
			{"NAME": "RULE_NAME", "TYPE": machbaseTypeVarchar},
		})
	}))
	defer srv.Close()

	client, err := NewMachbase(testMachbaseConfig(t, srv.URL, "MACHBASEDB"))
	require.NoError(t, err)
	require.NoError(t, client.ValidateCameraEventTable(context.Background(), "camera_a"))
}
