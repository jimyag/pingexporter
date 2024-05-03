package config

type TargetConfig struct {
	Addr string `yaml:"addr"`
	// TODO support labels
	// Labels map[string]string `yaml:"labels"`
}
