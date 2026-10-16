package config

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/digineo/go-ping"
	mon "github.com/digineo/go-ping/monitor"
	"github.com/jimyag/log"
	"github.com/prometheus/common/model"
)

// Config represents configuration for the exporter.
type Config struct {
	Targets []TargetConfig `toml:"targets"`
	Ping    struct {
		Interval time.Duration `toml:"interval"`     // Interval for ICMP echo requests
		Timeout  time.Duration `toml:"timeout"`      // Timeout for ICMP echo request
		History  int           `toml:"history_size"` // Number of results to remember per target
		Size     uint16        `toml:"payload_size"` // Payload size for ICMP echo requests
	} `toml:"ping"`

	DNS struct {
		Refresh    time.Duration `toml:"refresh"`     // DNS refresh interval (0 defaults to one minute; negative disables refresh)
		NameServer string        `toml:"name_server"` // DNS server used to resolve hostname of targets
	} `toml:"dns"`

	Web struct {
		Address     string `toml:"address"`      // Address on which to expose metrics and web interface
		MetricsPath string `toml:"metrics_path"` // Path under which to expose metrics
	} `toml:"web"`
	GlobalLabels map[string]string `toml:"global_labels"`
}

func (c *Config) Default() {
	if c.Ping.Interval == 0 {
		c.Ping.Interval = 5 * time.Second
	}
	if c.Ping.Timeout == 0 {
		c.Ping.Timeout = 4 * time.Second
	}
	if c.Ping.History == 0 {
		c.Ping.History = 10
	}
	if c.Ping.Size == 0 {
		c.Ping.Size = 56
	}

	if c.DNS.Refresh == 0 {
		c.DNS.Refresh = 1 * time.Minute
	}

	if c.Web.Address == "" {
		c.Web.Address = ":9113"
	}
	if c.Web.MetricsPath == "" {
		c.Web.MetricsPath = "/metrics"
	}
	if c.GlobalLabels == nil {
		c.GlobalLabels = make(map[string]string)
	}
}

func (c *Config) Verify() error {
	if c.Ping.Interval <= 0 {
		return fmt.Errorf("ping.interval must be greater than zero")
	}
	if c.Ping.Timeout <= 0 {
		return fmt.Errorf("ping.timeout must be greater than zero")
	}
	if c.Ping.History < 1 {
		return fmt.Errorf("ping.history_size must be greater than zero")
	}
	if c.Ping.Size > 65500 {
		return fmt.Errorf("ping.payload_size must be at most 65500")
	}

	if len(c.Targets) == 0 {
		return fmt.Errorf("targets must not be empty")
	}
	validLabel := func(name string) bool {
		return model.LabelName(name).IsValid() && !strings.HasPrefix(name, "__") && name != "addr" && name != "ip" && name != "version"
	}
	for name := range c.GlobalLabels {
		if !validLabel(name) {
			return fmt.Errorf("global_labels.%s is invalid or reserved", name)
		}
	}
	seen := make(map[string]bool)
	for i, target := range c.Targets {
		if target.Addr == "" || strings.ContainsAny(target.Addr, " \t\r\n") {
			return fmt.Errorf("targets[%d].addr must be a nonempty address without whitespace", i)
		}
		if seen[target.Addr] {
			return fmt.Errorf("targets[%d].addr duplicates address %q", i, target.Addr)
		}
		seen[target.Addr] = true
		for name := range target.Labels {
			if !validLabel(name) {
				return fmt.Errorf("targets[%d].labels.%s is invalid or reserved", i, name)
			}
			if _, exists := c.GlobalLabels[name]; exists {
				return fmt.Errorf("targets[%d].labels.%s conflicts with global_labels.%s", i, name, name)
			}
		}
	}
	return nil
}

func (c *Config) SetupResolver() *net.Resolver {
	if c.DNS.NameServer == "" {
		return net.DefaultResolver
	}

	server := c.DNS.NameServer
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "53")
	}
	dialer := func(ctx context.Context, _, _ string) (net.Conn, error) {
		d := net.Dialer{}
		return d.DialContext(ctx, "udp", server)
	}

	return &net.Resolver{PreferGo: true, Dial: dialer}
}

func (cfg *Config) TargetConfigByAddr(addr string) TargetConfig {
	for _, t := range cfg.Targets {
		if t.Addr == addr {
			return t
		}
	}

	return TargetConfig{Addr: addr}
}

func (c *Config) GenMonitor(onResolve func(string, bool)) (*mon.Monitor, error) {
	if err := c.Verify(); err != nil {
		return nil, err
	}
	var bind4, bind6 string
	if ln, err := net.Listen("tcp4", "127.0.0.1:0"); err == nil {
		_ = ln.Close()
		bind4 = "0.0.0.0"
	}
	if ln, err := net.Listen("tcp6", "[::1]:0"); err == nil {
		_ = ln.Close()
		bind6 = "::"
	}
	pinger, err := ping.New(bind4, bind6)
	if err != nil {
		return nil, fmt.Errorf("cannot start monitoring: %w", err)
	}

	if pinger.PayloadSize() != c.Ping.Size {
		pinger.SetPayloadSize(c.Ping.Size)
	}

	monitor := mon.New(pinger,
		c.Ping.Interval,
		c.Ping.Timeout)
	monitor.HistorySize = c.Ping.History
	resolver := c.SetupResolver()
	current := make(map[string]map[string]net.IPAddr)
	c.refreshTargets(monitor, resolver.LookupIPAddr, current, onResolve)
	if c.DNS.Refresh > 0 {
		go func() {
			ticker := time.NewTicker(c.DNS.Refresh)
			defer ticker.Stop()
			for range ticker.C {
				c.refreshTargets(monitor, resolver.LookupIPAddr, current, onResolve)
			}
		}()
	}
	return monitor, nil
}

func (c *Config) refreshTargets(monitor *mon.Monitor, lookup func(context.Context, string) ([]net.IPAddr, error), current map[string]map[string]net.IPAddr, onResolve func(string, bool)) {
	for i, target := range c.Targets {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ipAddrs, err := lookup(ctx, target.Addr)
		cancel()
		if onResolve != nil && net.ParseIP(target.Addr) == nil {
			onResolve(target.Addr, err == nil && len(ipAddrs) > 0)
		}
		if err != nil {
			log.Error(err).Str("target", target.Addr).Msg("cannot resolve target address")
			continue
		}
		if len(ipAddrs) == 0 {
			log.Error().Str("target", target.Addr).Msg("target address resolved to no IPs")
			continue
		}
		next := make(map[string]net.IPAddr, len(ipAddrs))
		for j, ipAddr := range ipAddrs {
			key := genPingMonitorKey(target.Addr, ipAddr)
			if _, exists := current[target.Addr][key]; exists {
				next[key] = ipAddr
				continue
			}
			if err := monitor.AddTargetDelayed(key, ipAddr,
				time.Duration(10*i+j)*time.Millisecond,
			); err != nil {
				log.Error(err).Str("addr", target.Addr).
					Str("key", key).
					Msg("cannot add target")
				continue
			}
			next[key] = ipAddr
		}
		// Keep existing probes if replacement targets could not be installed.
		if len(next) == 0 {
			continue
		}
		for key := range current[target.Addr] {
			if _, exists := next[key]; !exists {
				monitor.RemoveTarget(key)
			}
		}
		current[target.Addr] = next
	}
}

// genPingMonitorKey returns a unique key for the monitor based on addr and ipAddr
// for example: "test.host.com 192.168.2.1 v4"
func genPingMonitorKey(addr string, ipAddr net.IPAddr) string {
	if ipAddr.IP.To4() == nil {
		return fmt.Sprintf("%s %s v6", addr, ipAddr.String())
	}
	return fmt.Sprintf("%s %s v4", addr, ipAddr.String())
}

func ParseMonitorKey(key string) (addr string, ipAddr net.IPAddr, version string) {
	parts := strings.Split(key, " ")
	if len(parts) != 3 {
		log.Panic().Str("key", key).Msg("cannot parse monitor key")
	}
	addr = parts[0]
	ip := net.ParseIP(parts[1])
	if ip == nil {
		log.Panic().Str("key", key).Msg("unexpected ip in monitor key")
	}

	if parts[2] == "v4" && ip.To4() == nil {
		log.Panic().Str("key", key).Msg("unexpected ip version in monitor key")
	}
	if parts[2] == "v6" && ip.To4() != nil {
		log.Panic().Str("key", key).Msg("unexpected ip version in monitor key")
	}
	if ip.To4() == nil {
		version = "v6"
	} else {
		version = "v4"
	}
	return addr, net.IPAddr{IP: ip}, version
}
