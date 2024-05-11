package config

type TargetConfig struct {
	Addr   string            `yaml:"addr"`
	Labels map[string]string `yaml:"labels"`
}
