package config

import "net"

type TargetConfig struct {
	Addr      string            `toml:"addr"`
	IPVersion string            `toml:"ip_version"`
	Labels    map[string]string `toml:"labels"`
}

func (t TargetConfig) acceptsIP(ip net.IP) bool {
	return t.IPVersion == "" || t.IPVersion == "all" ||
		(t.IPVersion == "v4" && ip.To4() != nil) ||
		(t.IPVersion == "v6" && ip.To4() == nil)
}
