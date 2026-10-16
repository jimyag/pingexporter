package config

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	mon "github.com/digineo/go-ping/monitor"
)

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
		cfg.refreshTargets(monitor, lookup, current)
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
