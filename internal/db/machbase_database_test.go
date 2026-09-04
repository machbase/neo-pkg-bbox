package db

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/machbase/neo-pkg-bbox/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testMachbaseConfig(t *testing.T, rawURL, database string) config.MachbaseConfig {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return config.MachbaseConfig{
		Scheme:   u.Scheme,
		Host:     u.Hostname(),
		Port:     port,
		Database: database,
	}
}

func writeQuerySuccess(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"data": map[string]any{
			"columns": []string{},
			"types":   []string{},
			"rows":    []any{},
		},
	}))
}

func TestMachbaseRequestsIncludeConfiguredDatabase(t *testing.T) {
	const database = "CODEX_V870_TEST"
	requests := make(chan *http.Request, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		if r.URL.Path == "/db/query" {
			writeQuerySuccess(t, w)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"success":true}`))
		require.NoError(t, err)
	}))
	defer srv.Close()

	client, err := NewMachbase(testMachbaseConfig(t, srv.URL, database))
	require.NoError(t, err)

	_, err = client.Query(context.Background(), "SELECT 1")
	require.NoError(t, err)
	err = client.WriteRows(context.Background(), "TAG", []string{"NAME"}, [][]any{{"tag-1"}})
	require.NoError(t, err)

	queryReq := <-requests
	writeReq := <-requests
	assert.Equal(t, database, queryReq.URL.Query().Get("db"))
	assert.Equal(t, database, writeReq.URL.Query().Get("db"))
}

func TestMachbaseDefaultsToMachbaseDB(t *testing.T) {
	var database string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		database = r.URL.Query().Get("db")
		writeQuerySuccess(t, w)
	}))
	defer srv.Close()

	client, err := NewMachbase(testMachbaseConfig(t, srv.URL, ""))
	require.NoError(t, err)
	_, err = client.Query(context.Background(), "SELECT CURRENT_DATABASE()")
	require.NoError(t, err)

	assert.Equal(t, config.DefaultMachbaseDatabase, database)
}

func TestMachbaseForwardAddsDatabaseUnlessCallerProvidesOne(t *testing.T) {
	const configured = "CODEX_V870_TEST"
	requests := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Query().Get("db")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := NewMachbase(testMachbaseConfig(t, srv.URL, configured))
	require.NoError(t, err)

	resp, err := client.ForwardWithoutAuth(context.Background(), http.MethodGet, "/db/query", "q=SELECT+1", nil, "")
	require.NoError(t, err)
	resp.Body.Close()
	resp, err = client.ForwardWithoutAuth(context.Background(), http.MethodGet, "/db/query", "q=SELECT+1&db=EXPLICIT_DB", nil, "")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, configured, <-requests)
	assert.Equal(t, "EXPLICIT_DB", <-requests)
}
