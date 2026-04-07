package config

type MachbaseConfig struct {
	Disabled       bool   `yaml:"disabled"`
	Scheme         string `yaml:"scheme"`
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	APIToken       string `yaml:"api_token"`
	User           string `yaml:"user"`
	Password       string `yaml:"password"`
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
	if m.TimeoutSeconds == 0 {
		m.TimeoutSeconds = 10
	}
}
