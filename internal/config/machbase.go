package config

const DefaultMachbaseDatabase = "MACHBASEDB"

type MachbaseConfig struct {
	Disabled       bool   `yaml:"disabled" json:"disabled"`
	Scheme         string `yaml:"scheme" json:"scheme"`
	Host           string `yaml:"host" json:"host"`
	Port           int    `yaml:"port" json:"port"`
	Database       string `yaml:"database" json:"database"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"timeout_seconds"`
	APIToken       string `yaml:"api_token" json:"api_token"`
	User           string `yaml:"user" json:"user"`
	Password       string `yaml:"password" json:"password"`
}

func (m *MachbaseConfig) ApplyDefaults() {
	if m.Scheme == "" {
		m.Scheme = "http"
	}
	if m.Host == "" {
		m.Host = "127.0.0.1"
	}
	if m.Port == 0 {
		m.Port = 5654
	}
	if m.Database == "" {
		m.Database = DefaultMachbaseDatabase
	}
	if m.TimeoutSeconds == 0 {
		m.TimeoutSeconds = 10
	}
}
