//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
)

func TestExporterProcess(t *testing.T) {
	if cfg := os.Getenv("PINGEXPORTER_TEST_CONFIG"); cfg != "" {
		os.Args = []string{os.Args[0], cfg}
		main()
	}
}

func TestInvalidConfigProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.toml")
	if err := os.WriteFile(path, []byte("[ping]\ninterval = \"-1s\"\n[[targets]]\naddr = \"127.0.0.1\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestExporterProcess$")
	cmd.Env = append(os.Environ(), "PINGEXPORTER_TEST_CONFIG="+path)
	output, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !bytes.Contains(output, []byte("ping.interval")) || bytes.Contains(output, []byte("panic:")) {
		t.Fatalf("expected field error and exit 1, got %v:\n%s", err, output)
	}
}

// This test needs raw ICMP socket permissions. It runs the real server in a
// subprocess so its DNS refresher and ping workers end with the process.
func TestDNSRefreshMetrics(t *testing.T) {
	dns, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dns.Close() })
	var phase atomic.Int32
	var responses [4]atomic.Int64
	phase.Store(-1)
	go func() {
		packet := make([]byte, 4096)
		for {
			n, peer, err := dns.ReadFrom(packet)
			if err != nil {
				return
			}
			end := 12
			for end < n && packet[end] != 0 {
				end += int(packet[end]) + 1
			}
			end++
			if end+4 > n {
				continue
			}
			qtype := binary.BigEndian.Uint16(packet[end : end+2])
			response := append([]byte(nil), packet[:end+4]...)
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			clear(response[6:12])
			state := phase.Load()
			if state < 0 {
				binary.BigEndian.PutUint16(response[2:4], 0x8182)
			} else if state > 0 && (qtype == 1 || qtype == 28) {
				ip := net.ParseIP("::1").To16()
				if qtype == 1 {
					ip = net.IPv4(127, 0, 0, byte(state)).To4()
				}
				binary.BigEndian.PutUint16(response[6:8], 1)
				response = append(response, 0xc0, 0x0c, 0, byte(qtype), 0, 1, 0, 0, 0, 0, 0, byte(len(ip)))
				response = append(response, ip...)
			}
			if _, err := dns.WriteTo(response, peer); err != nil {
				return
			}
			responses[state+1].Add(1)
		}
	}()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := fmt.Sprintf(`[dns]
name_server = %q
refresh = "100ms"
[ping]
interval = "20ms"
timeout = "200ms"
history_size = 4
[web]
address = %q
[[targets]]
addr = "home.example"
[targets.labels]
host = "home"
[global_labels]
from_host = "fixture"
`, dns.LocalAddr().String(), address)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestExporterProcess$")
	cmd.Env = append(os.Environ(), "PINGEXPORTER_TEST_CONFIG="+path)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait(); t.Log(logs.String()) })
	client := &http.Client{Timeout: time.Second}
	var body string
	get := func(name, ip, version string) (float64, bool) {
		var parser expfmt.TextParser
		families, err := parser.TextToMetricFamilies(strings.NewReader(body))
		if err != nil {
			return 0, false
		}
		for _, metric := range families[name].GetMetric() {
			labels := make(map[string]string)
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["addr"] == "home.example" && labels["host"] == "home" && labels["from_host"] == "fixture" && labels["ip"] == ip && labels["version"] == version {
				return metric.GetGauge().GetValue(), true
			}
		}
		return 0, false
	}
	wait := func(description string, condition func() bool) {
		t.Helper()
		deadline := time.NewTimer(15 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			response, err := client.Get("http://" + address + "/metrics")
			if err == nil {
				data, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				if readErr == nil && response.StatusCode == http.StatusOK {
					body = string(data)
					if condition() {
						t.Log(description)
						return
					}
				}
			}
			select {
			case <-deadline.C:
				t.Fatalf("timed out: %s; last metrics:\n%s", description, body)
			case <-tick.C:
			}
		}
	}
	success := "ping_exporter_dns_lookup_success"
	lastSuccess := "ping_exporter_dns_last_success_timestamp_seconds"
	loss := "ping_exporter_loss_ratio"
	wait("startup DNS failure is observable", func() bool {
		value, ok := get(success, "", "")
		last, exists := get(lastSuccess, "", "")
		return ok && value == 0 && exists && last == 0
	})
	phase.Store(1)
	wait("startup recovers and exports correct IPv4/IPv6 labels", func() bool {
		v4, ok4 := get(loss, "127.0.0.1", "v4")
		v6, ok6 := get(loss, "::1", "v6")
		value, _ := get(success, "", "")
		return ok4 && ok6 && v4 == 0 && v6 == 0 && value == 1
	})
	before, _ := get(lastSuccess, "", "")
	wait("unchanged DNS targets continue refreshing", func() bool { now, _ := get(lastSuccess, "", ""); return now > before })
	before, _ = get(lastSuccess, "", "")
	for _, state := range []int32{-1, 0} {
		previous := responses[state+1].Load()
		phase.Store(state)
		wait("failed or empty DNS response retains probes", func() bool {
			value, _ := get(success, "", "")
			_, old := get(loss, "127.0.0.1", "v4")
			return value == 0 && old && responses[state+1].Load() >= previous+4
		})
		wait("failed DNS preserves last success timestamp", func() bool { now, ok := get(lastSuccess, "", ""); return ok && now == before })
	}
	phase.Store(2)
	wait("IP change replaces old metrics without restarting", func() bool {
		value, ok := get(loss, "127.0.0.2", "v4")
		_, old := get(loss, "127.0.0.1", "v4")
		dns, _ := get(success, "", "")
		last, _ := get(lastSuccess, "", "")
		return ok && value == 0 && !old && dns == 1 && last > before
	})
}
