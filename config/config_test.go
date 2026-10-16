package config

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	mon "github.com/digineo/go-ping/monitor"
)

func TestParseMonitorKeyVersion(t *testing.T) {
	for _, tc := range []struct{ ip, version string }{{"192.0.2.1", "v4"}, {"2001:db8::1", "v6"}} {
		t.Run(tc.version, func(t *testing.T) {
			key := genPingMonitorKey("home.example", net.IPAddr{IP: net.ParseIP(tc.ip)})
			_, _, version := ParseMonitorKey(key)
			if version != tc.version {
				t.Fatalf("version = %s, want %s", version, tc.version)
			}
		})
	}
}

func TestVerify(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"valid", func(c *Config) {}, ""},
		{"interval", func(c *Config) { c.Ping.Interval = -time.Second }, "ping.interval"},
		{"timeout", func(c *Config) { c.Ping.Timeout = -time.Second }, "ping.timeout"},
		{"history", func(c *Config) { c.Ping.History = -1 }, "ping.history_size"},
		{"size", func(c *Config) { c.Ping.Size = 65501 }, "ping.payload_size"},
		{"no targets", func(c *Config) { c.Targets = nil }, "targets"},
		{"empty address", func(c *Config) { c.Targets[0].Addr = "" }, "targets[0].addr"},
		{"space in address", func(c *Config) { c.Targets[0].Addr = "bad host" }, "targets[0].addr"},
		{"duplicate address", func(c *Config) { c.Targets = append(c.Targets, c.Targets[0]) }, "duplicate"},
		{"global reserved", func(c *Config) { c.GlobalLabels["ip"] = "test" }, "global_labels.ip"},
		{"global invalid", func(c *Config) { c.GlobalLabels["bad-label"] = "test" }, "global_labels.bad-label"},
		{"target reserved", func(c *Config) { c.Targets[0].Labels = map[string]string{"addr": "test"} }, "targets[0].labels.addr"},
		{"target invalid", func(c *Config) { c.Targets[0].Labels = map[string]string{"__hidden": "test"} }, "targets[0].labels.__hidden"},
		{"label collision", func(c *Config) {
			c.GlobalLabels["host"] = "test"
			c.Targets[0].Labels = map[string]string{"host": "home"}
		}, "global_labels"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Targets: []TargetConfig{{Addr: "home.example"}}}
			cfg.Default()
			tc.change(cfg)
			err := cfg.Verify()
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want field %q", err, tc.want)
			}
		})
	}
}

func TestRefreshTargets(t *testing.T) {
	cfg := &Config{Targets: []TargetConfig{{Addr: "home.example"}}}
	// No ping is sent during this test; only target reconciliation is exercised.
	monitor := mon.New(nil, time.Hour, time.Second)
	current := make(map[string]map[string]net.IPAddr)
	t.Cleanup(func() {
		for _, targets := range current {
			for key := range targets {
				monitor.RemoveTarget(key)
			}
		}
	})
	oldIP := net.IPAddr{IP: net.ParseIP("192.0.2.1")}
	newIP := net.IPAddr{IP: net.ParseIP("192.0.2.2")}
	v6 := net.IPAddr{IP: net.ParseIP("2001:db8::1")}
	var ips []net.IPAddr
	var lookupErr error
	lookup := func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host != "home.example" {
			t.Fatalf("unexpected hostname: %s", host)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("DNS lookup has no deadline")
		}
		return ips, lookupErr
	}
	check := func(want ...net.IPAddr) {
		t.Helper()
		cfg.refreshTargets(monitor, lookup, current, nil)
		targets := current["home.example"]
		if len(targets) != len(want) {
			t.Fatalf("targets = %v, want %v", targets, want)
		}
		for _, ip := range want {
			if _, ok := targets[genPingMonitorKey("home.example", ip)]; !ok {
				t.Fatalf("target %s missing from %v", ip.String(), targets)
			}
		}
	}

	lookupErr = errors.New("temporary DNS failure")
	check()
	lookupErr = nil
	ips = []net.IPAddr{oldIP, v6}
	check(oldIP, v6)
	check(oldIP, v6)
	ips = []net.IPAddr{newIP, v6}
	check(newIP, v6)
	lookupErr = errors.New("temporary DNS failure")
	check(newIP, v6)
	lookupErr = nil
	ips = nil
	check(newIP, v6)
	ips = []net.IPAddr{oldIP}
	check(oldIP)
}

func TestSetupResolverDoesNotChangeConfig(t *testing.T) {
	cfg := &Config{}
	cfg.DNS.NameServer = "127.0.0.1:1053"
	cfg.SetupResolver()
	if cfg.DNS.NameServer != "127.0.0.1:1053" {
		t.Fatalf("DNS server changed to %q", cfg.DNS.NameServer)
	}
}
