package server

import (
	"testing"

	"github.com/machbase/neo-pkg-bbox/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestCfgToDTODefaultsDatabase(t *testing.T) {
	dto := cfgToDTO(&config.AppConfig{})
	assert.Equal(t, config.DefaultMachbaseDatabase, dto.Machbase.Database)
}

func TestDTOToCfgPreservesDatabase(t *testing.T) {
	dto := &AppConfigDTO{
		Machbase: MachbaseConfigAPI{Database: "CODEX_V870_TEST"},
	}
	cfg := dtoToCfg(dto)
	assert.Equal(t, "CODEX_V870_TEST", cfg.Machbase.Database)
}
