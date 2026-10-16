package config

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestResolverFallsBackToTCP(t *testing.T) {
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tcp.Close() })
	udp, err := net.ListenPacket("udp4", tcp.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udp.Close() })
	var tcpQueries atomic.Int32
	respond := func(packet []byte, truncated bool) []byte {
		var request dnsmessage.Message
		if err := request.Unpack(packet); err != nil {
			t.Error(err)
			return nil
		}
		response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true, Truncated: truncated}, Questions: request.Questions}
		if !truncated {
			for _, question := range request.Questions {
				answer := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET}}
				switch question.Type {
				case dnsmessage.TypeA:
					answer.Body = &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}
				case dnsmessage.TypeAAAA:
					answer.Body = &dnsmessage.AAAAResource{AAAA: [16]byte{15: 1}}
				default:
					continue
				}
				response.Answers = append(response.Answers, answer)
			}
		}
		result, err := response.Pack()
		if err != nil {
			t.Error(err)
		}
		return result
	}
	go func() {
		packet := make([]byte, 4096)
		for {
			n, peer, err := udp.ReadFrom(packet)
			if err != nil {
				return
			}
			if _, err := udp.WriteTo(respond(packet[:n], true), peer); err != nil {
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
	cfg := &Config{}
	cfg.DNS.NameServer = tcp.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := cfg.SetupResolver().LookupIPAddr(ctx, "home.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 2 || tcpQueries.Load() != 2 {
		t.Fatalf("IPs = %v, TCP queries = %d", ips, tcpQueries.Load())
	}
}
