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
	"github.com/prometheus/common/model"
	"golang.org/x/net/dns/dnsmessage"
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
	for _, version := range []string{"all", "v4", "v6"} {
		t.Run(version, func(t *testing.T) { testDNSRefreshMetrics(t, version) })
	}
}

func testDNSRefreshMetrics(t *testing.T, version string) {
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tcp.Close() })
	dns, err := net.ListenPacket("udp4", tcp.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dns.Close() })
	var phase atomic.Int32
	var responses [4]atomic.Int64
	var tcpQueries atomic.Int32
	phase.Store(-1)
	respond := func(packet []byte, truncated bool) []byte {
		var request dnsmessage.Message
		if err := request.Unpack(packet); err != nil {
			t.Error(err)
			return nil
		}
		state := phase.Load()
		response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
		if state < 0 {
			response.RCode = dnsmessage.RCodeServerFailure
		} else if state > 0 {
			response.Truncated = truncated
			if !truncated {
				for _, question := range request.Questions {
					answer := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET}}
					switch question.Type {
					case dnsmessage.TypeA:
						answer.Body = &dnsmessage.AResource{A: [4]byte{127, 0, 0, byte(state)}}
					case dnsmessage.TypeAAAA:
						answer.Body = &dnsmessage.AAAAResource{AAAA: [16]byte{15: byte(state)}}
					default:
						continue
					}
					response.Answers = append(response.Answers, answer)
				}
			}
		}
		responses[state+1].Add(1)
		result, err := response.Pack()
		if err != nil {
			t.Error(err)
		}
		return result
	}
	go func() {
		packet := make([]byte, 4096)
		for {
			n, peer, err := dns.ReadFrom(packet)
			if err != nil {
				return
			}
			if _, err := dns.WriteTo(respond(packet[:n], true), peer); err != nil {
				return
			}
		}
	}()
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			var length [2]byte
			if _, err := io.ReadFull(conn, length[:]); err != nil {
				conn.Close()
				t.Error(err)
				return
			}
			packet := make([]byte, binary.BigEndian.Uint16(length[:]))
			if _, err := io.ReadFull(conn, packet); err != nil {
				conn.Close()
				t.Error(err)
				return
			}
			response := respond(packet, false)
			binary.BigEndian.PutUint16(length[:], uint16(len(response)))
			tcpQueries.Add(1)
			if _, err := conn.Write(append(length[:], response...)); err != nil {
				t.Error(err)
			}
			conn.Close()
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
ip_version = %q
[targets.labels]
host = "home"
[global_labels]
from_host = "fixture"
`, dns.LocalAddr().String(), address, version)
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
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		t.Log(logs.String())
		if t.Failed() {
			return
		}
		if bytes.Count(logs.Bytes(), []byte(`"message":"DNS addresses changed"`)) != 1 || !bytes.Contains(logs.Bytes(), []byte(`"previous_ips":`)) || !bytes.Contains(logs.Bytes(), []byte(`"current_ips":`)) {
			t.Error("expected exactly one address change log with previous and current IPs")
		}
		if bytes.Contains(logs.Bytes(), []byte("WARNING: DATA RACE")) {
			t.Error("race detected in exporter subprocess")
		}
		if tcpQueries.Load() == 0 {
			t.Error("exporter did not resolve truncated DNS responses over TCP")
		}
	})
	client := &http.Client{Timeout: time.Second}
	var body string
	get := func(name, ip, version string) (float64, bool) {
		parser := expfmt.NewTextParser(model.LegacyValidation)
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
				if counter := metric.GetCounter(); counter != nil {
					return counter.GetValue(), true
				}
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
	changes := "ping_exporter_dns_address_changes_total"
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
		count, _ := get(changes, "", "")
		return ok4 == (version != "v6") && ok6 == (version != "v4") && v4 == 0 && v6 == 0 && value == 1 && count == 0
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
			if version == "v6" {
				_, old = get(loss, "::1", "v6")
			}
			count, _ := get(changes, "", "")
			return value == 0 && old && count == 0 && responses[state+1].Load() >= previous+4
		})
		wait("failed DNS preserves last success timestamp", func() bool { now, ok := get(lastSuccess, "", ""); return ok && now == before })
	}
	phase.Store(2)
	wait("IP change replaces old metrics without restarting", func() bool {
		value, ok := get(loss, "127.0.0.2", "v4")
		_, old := get(loss, "127.0.0.1", "v4")
		_, new6 := get(loss, "::2", "v6")
		_, old6 := get(loss, "::1", "v6")
		dns, _ := get(success, "", "")
		last, _ := get(lastSuccess, "", "")
		count, _ := get(changes, "", "")
		return ok == (version != "v6") && value == 0 && !old && new6 == (version != "v4") && !old6 && dns == 1 && last > before && count == 1
	})
}
