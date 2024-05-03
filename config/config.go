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
)

// Config represents configuration for the exporter.
type Config struct {
	Targets []TargetConfig `toml:"targets"`
	Ping    struct {
		Interval time.Duration ` toml:"interval"`    // Interval for ICMP echo requests
		Timeout  time.Duration `toml:"timeout"`      // Timeout for ICMP echo request
		History  int           `toml:"history_size"` // Number of results to remember per target
		Size     uint16        `toml:"payload_size"` // Payload size for ICMP echo requests
	} `toml:"ping"`

	DNS struct {
		Refresh    time.Duration `toml:"refresh"`     // Interval for refreshing DNS records and updating targets accordingly (0 if disabled)
		NameServer string        `toml:"name_server"` // DNS server used to resolve hostname of targets
	} `yaml:"dns"`

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

func (c *Config) Verify() {
	if c.Ping.History < 1 {
		log.Panic().Msg("ping.history-size must be greater than 0")
	}
	if c.Ping.Size > 65500 {
		log.Panic().Msg("ping.size must be between 0 and 65500")
	}

	if len(c.Targets) == 0 {
		log.Panic().Msg("No targets specified")
	}
}

func (c *Config) SetupResolver() *net.Resolver {
	if c.DNS.NameServer == "" {
		return net.DefaultResolver
	}

	if !strings.HasSuffix(c.DNS.NameServer, ":53") {
		c.DNS.NameServer += ":53"
	}
	dialer := func(ctx context.Context, _, _ string) (net.Conn, error) {
		d := net.Dialer{}
		return d.DialContext(ctx, "udp", c.DNS.NameServer)
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

func (c *Config) GenMonitor() (*mon.Monitor, error) {
	var bind4, bind6 string
	if ln, err := net.Listen("tcp4", "127.0.0.1:0"); err == nil {
		// ipv4 enabled
		ln.Close()
		bind4 = "0.0.0.0"
	}
	if ln, err := net.Listen("tcp6", "[::1]:0"); err == nil {
		// ipv6 enabled
		ln.Close()
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
	for i, target := range c.Targets {
		addrs, err := resolver.LookupIPAddr(context.Background(), target.Addr)
		if err != nil {
			log.Error(err).Str("target", target.Addr).Msg("cannot resolve target address")
			continue
		}
		for j, addr := range addrs {
			key := genPingMonitorKey(target.Addr, addr)
			if err := monitor.AddTargetDelayed(key, addr,
				time.Duration(10*i+j)*time.Millisecond,
			); err != nil {
				log.Error(err).Str("target", target.Addr).
					Str("key", key).
					Msg("cannot add target")
			}
		}

	}
	return monitor, nil
}

// genPingMonitorKey returns a unique key for the monitor based on target and addr
// for example: "test.host.com 192.168.2.1 v4"
func genPingMonitorKey(target string, addr net.IPAddr) string {
	if addr.IP.To4() == nil {
		return fmt.Sprintf("%s %s v6", target, addr.String())
	}
	return fmt.Sprintf("%s %s v4", target, addr.String())
}

func ParseMonitorKey(key string) (string, net.IPAddr) {

	parts := strings.Split(key, " ")
	if len(parts) != 3 {
		log.Panic().Str("key", key).Msg("cannot parse monitor key")
	}
	host := parts[0]
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
	return host, net.IPAddr{IP: ip}
}
