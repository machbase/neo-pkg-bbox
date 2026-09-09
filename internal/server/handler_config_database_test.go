package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/machbase/neo-pkg-bbox/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCfgToDTODefaultsDatabase(t *testing.T) {
	dto := cfgToDTO(&config.AppConfig{})
	assert.Equal(t, config.DefaultMachbaseDatabase, dto.Machbase.Database)
}

func TestCfgToDTOExposesCurrentAndLegacyTokenNames(t *testing.T) {
	dto := cfgToDTO(&config.AppConfig{Machbase: config.MachbaseConfig{APIToken: "test-token"}})
	assert.Equal(t, "test-token", dto.Machbase.APIToken)
	assert.Equal(t, "test-token", dto.Machbase.LegacyToken)
}

func databaseAPIConfig(t *testing.T, rawURL, accessDatabase string) MachbaseConfigAPI {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return MachbaseConfigAPI{
		Scheme: u.Scheme, Host: u.Hostname(), Port: port, Database: accessDatabase,
		TimeoutSeconds: 2,
	}
}

func databaseHTTPServer(t *testing.T, accessMode string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"columns": []string{"NAME", "KIND", "ACCESS_MODE", "CAN_USE", "STATE", "IS_DEFAULT"},
				"types":   []string{"string", "string", "string", "int32", "string", "int32"},
				"rows":    [][]any{{"MACHBASEDB", "ACTIVE", accessMode, 1, "NORMAL", 1}},
			},
		}))
	}))
}

func runConfigRequest(t *testing.T, handler func(*gin.Context), body any) (*httptest.ResponseRecorder, Response) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler(ctx)
	var response Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func TestPostDatabasesReturnsUsableDatabaseList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := databaseHTTPServer(t, "READ_WRITE")
	defer srv.Close()
	h := &Handler{}

	recorder, response := runConfigRequest(t, h.PostDatabases, databaseAPIConfig(t, srv.URL, "MACHBASEDB"))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, response.Success)
	data, ok := response.Data.(map[string]any)
	require.True(t, ok)
	assert.Len(t, data["databases"], 1)
}

func TestPostAppConfigRejectsReadOnlyWithoutChangingFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := databaseHTTPServer(t, "READ_ONLY")
	defer srv.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("original: true\n"), 0o644))
	h := &Handler{configPath: configPath}
	req := AppConfigDTO{Machbase: databaseAPIConfig(t, srv.URL, "MACHBASEDB")}

	recorder, response := runConfigRequest(t, h.PostAppConfig, req)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.False(t, response.Success)
	assert.Contains(t, response.Reason, "READ_WRITE")
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, "original: true\n", string(raw))
}

func TestDTOToCfgPreservesDatabase(t *testing.T) {
	dto := &AppConfigDTO{
		Machbase: MachbaseConfigAPI{Database: "CODEX_V870_TEST", APIToken: "test-token"},
	}
	cfg := dtoToCfg(dto)
	assert.Equal(t, "CODEX_V870_TEST", cfg.Machbase.Database)
	assert.Equal(t, "test-token", cfg.Machbase.APIToken)
}

func TestDTOToCfgAcceptsLegacyToken(t *testing.T) {
	dto := &AppConfigDTO{Machbase: MachbaseConfigAPI{LegacyToken: "legacy-token"}}
	cfg := dtoToCfg(dto)
	assert.Equal(t, "legacy-token", cfg.Machbase.APIToken)
}

func TestPostAppConfigSavesValidatedDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := databaseHTTPServer(t, "READ_WRITE")
	defer srv.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	h := &Handler{configPath: configPath}
	req := AppConfigDTO{Machbase: databaseAPIConfig(t, srv.URL, "machbasedb")}

	recorder, response := runConfigRequest(t, h.PostAppConfig, req)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, response.Success)
	data, ok := response.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, data["restart_required"])

	saved, err := config.LoadRaw(configPath)
	require.NoError(t, err)
	assert.Equal(t, "MACHBASEDB", saved.Machbase.Database)
}
