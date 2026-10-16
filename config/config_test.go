package config

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	mon "github.com/digineo/go-ping/monitor"
)

func TestTargetTOML(t *testing.T) {
	for _, labelKey := range []string{"labels", "Labels"} {
		var cfg Config
		if _, err := toml.Decode("[[targets]]\naddr = 'home.example'\nip_version = 'v6'\n[targets."+labelKey+"]\nhost = 'home'\n", &cfg); err != nil {
			t.Fatal(err)
		}
		cfg.Default()
		if err := cfg.Verify(); err != nil {
			t.Fatal(err)
		}
		if cfg.Targets[0].IPVersion != "v6" || cfg.Targets[0].Labels["host"] != "home" {
			t.Fatalf("incorrect target decoding: %+v", cfg.Targets)
		}
	}
}

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
		{"invalid IP version", func(c *Config) { c.Targets[0].IPVersion = "ipv4" }, "ip_version"},
		{"literal IP mismatch", func(c *Config) { c.Targets[0] = TargetConfig{Addr: "192.0.2.1", IPVersion: "v6"} }, "ip_version"},
		{"literal IPv6 mismatch", func(c *Config) { c.Targets[0] = TargetConfig{Addr: "2001:db8::1", IPVersion: "v4"} }, "ip_version"},
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
	changes, wantChanges := 0, 0
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
		cfg.refreshTargets(monitor, lookup, current, func(_ string, success, changed bool) {
			if success != (lookupErr == nil && len(ips) > 0) {
				t.Fatalf("unexpected lookup success: %v", success)
			}
			if changed {
				changes++
			}
		})
		if changes != wantChanges {
			t.Fatalf("changes = %d, want %d", changes, wantChanges)
		}
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
	ips = []net.IPAddr{v6, oldIP}
	check(oldIP, v6)
	ips = []net.IPAddr{newIP, v6}
	wantChanges = 1
	check(newIP, v6)
	lookupErr = errors.New("temporary DNS failure")
	check(newIP, v6)
	lookupErr = nil
	ips = nil
	check(newIP, v6)
	ips = []net.IPAddr{oldIP}
	wantChanges = 2
	check(oldIP)
}

func TestRefreshIPVersions(t *testing.T) {
	v4 := net.IPAddr{IP: net.ParseIP("192.0.2.1")}
	v6 := net.IPAddr{IP: net.ParseIP("2001:db8::1")}
	for _, version := range []string{"", "all", "v4", "v6"} {
		t.Run("version="+version, func(t *testing.T) {
			cfg := &Config{Targets: []TargetConfig{{Addr: "home.example", IPVersion: version}}}
			monitor := mon.New(nil, time.Hour, time.Second)
			current := make(map[string]map[string]net.IPAddr)
			t.Cleanup(func() {
				for key := range current["home.example"] {
					monitor.RemoveTarget(key)
				}
			})
			ips := []net.IPAddr{v4, v6}
			lookup := func(context.Context, string) ([]net.IPAddr, error) { return slices.Clone(ips), nil }
			success := false
			report := func(_ string, ok, changed bool) {
				success = ok
				if changed {
					t.Fatal("unexpected address change")
				}
			}
			cfg.refreshTargets(monitor, lookup, current, report)
			want := 2
			if version == "v4" || version == "v6" {
				want = 1
			}
			if !success || len(current["home.example"]) != want {
				t.Fatalf("success = %v, targets = %v", success, current)
			}
			for _, ip := range current["home.example"] {
				if !cfg.Targets[0].acceptsIP(ip.IP) {
					t.Fatalf("unexpected address: %s", ip.String())
				}
			}
			if version == "v4" || version == "v6" {
				ips = []net.IPAddr{v4, {IP: net.ParseIP("2001:db8::2")}}
				if version == "v6" {
					ips = []net.IPAddr{{IP: net.ParseIP("192.0.2.2")}, v6}
				}
				cfg.refreshTargets(monitor, lookup, current, report)
				if !success {
					t.Fatal("changing only the unselected family must not fail")
				}
				ips = []net.IPAddr{v6}
				if version == "v6" {
					ips = []net.IPAddr{v4}
				}
				cfg.refreshTargets(monitor, lookup, current, report)
				if success || len(current["home.example"]) != 1 {
					t.Fatalf("missing IP family must fail and retain existing target: %v", current)
				}
			}
		})
	}
}

func TestSetupResolverDoesNotChangeConfig(t *testing.T) {
	cfg := &Config{}
	cfg.DNS.NameServer = "127.0.0.1:1053"
	cfg.SetupResolver()
	if cfg.DNS.NameServer != "127.0.0.1:1053" {
		t.Fatalf("DNS server changed to %q", cfg.DNS.NameServer)
	}
}
