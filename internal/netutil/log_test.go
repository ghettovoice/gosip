package netutil_test

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/netutil"
)

const logTestPayload = "ACK sip:example.test SIP/2.0\r\n" +
	"Authorization: Bearer fixture-endpoint-token\r\n" +
	"Proxy-Authorization: Bearer fixture-proxy-token\r\n\r\n"

func TestNewLogConn_MetadataOnly(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	for _, conn := range []net.Conn{a, b} {
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("conn.SetDeadline() error = %v, want nil", err)
		}
	}
	sender := netutil.NewLogConn(a, logger, slog.LevelDebug)
	receiver := netutil.NewLogConn(b, logger, slog.LevelDebug)

	wrote := make(chan error, 1)
	go func() {
		_, err := sender.Write([]byte(logTestPayload))
		wrote <- err
	}()
	buf := make([]byte, len(logTestPayload))
	if _, err := io.ReadFull(receiver, buf); err != nil {
		t.Fatalf("io.ReadFull() error = %v, want nil", err)
	}
	if err := <-wrote; err != nil {
		t.Fatalf("sender.Write() error = %v, want nil", err)
	}
	assertPayloadAndLogs(t, buf, logs.String())
}

func TestNewLogPacketConn_MetadataOnly(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	listen := func() net.PacketConn {
		t.Helper()
		conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.ListenPacket() error = %v, want nil", err)
		}
		t.Cleanup(func() { conn.Close() })
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("conn.SetDeadline() error = %v, want nil", err)
		}
		return netutil.NewLogPacketConn(conn, logger, slog.LevelDebug)
	}
	sender, receiver := listen(), listen()
	if _, err := sender.WriteTo([]byte(logTestPayload), receiver.LocalAddr()); err != nil {
		t.Fatalf("sender.WriteTo() error = %v, want nil", err)
	}
	buf := make([]byte, len(logTestPayload)+1)
	n, _, err := receiver.ReadFrom(buf)
	if err != nil {
		t.Fatalf("receiver.ReadFrom() error = %v, want nil", err)
	}
	assertPayloadAndLogs(t, buf[:n], logs.String())
}

func assertPayloadAndLogs(t *testing.T, payload []byte, logs string) {
	t.Helper()
	if string(payload) != logTestPayload {
		t.Error("received payload differs from the original")
	}
	if strings.Contains(logs, "fixture-endpoint-token") || strings.Contains(logs, "fixture-proxy-token") {
		t.Error("debug logs contain credential values")
	}
	if !strings.Contains(logs, "connection read buffer") || !strings.Contains(logs, "connection wrote buffer") ||
		strings.Count(logs, "buffer.size="+strconv.Itoa(len(logTestPayload))) != 2 {
		t.Error("debug logs do not record both I/O events with their sizes")
	}
}
