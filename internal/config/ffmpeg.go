package config

type ArgKV struct {
	Flag  string `yaml:"flag" json:"flag"`
	Value string `yaml:"value" json:"value"`
}

type FFmpegConfig struct {
	Binary   string         `yaml:"binary" json:"binary"`
	Defaults FFmpegDefaults `yaml:"defaults" json:"defaults"`
}

type FFmpegDefaults struct {
	ProbeBinary string  `yaml:"probe_binary" json:"probe_binary"`
	ProbeArgs   []ArgKV `yaml:"probe_args" json:"probe_args"`
}
