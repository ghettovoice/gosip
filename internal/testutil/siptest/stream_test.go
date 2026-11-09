package siptest_test

import (
	"fmt"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/sip"
)

func TestStreamReader_Read_FragmentedMessage(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		srv, cln := net.Pipe()
		t.Cleanup(func() {
			_ = srv.Close()
			_ = cln.Close()
		})
		rdr := siptest.NewStreamReader(t, srv)

		wire := streamTestReq("OPTIONS", nil)
		writeDone := make(chan error, 1)
		go func() {
			firstPart := len(wire) / 2
			if _, err := cln.Write([]byte(wire[:firstPart])); err != nil {
				writeDone <- errors.Wrap(err)
				return
			}

			time.Sleep(200 * time.Millisecond)
			_, err := cln.Write([]byte(wire[firstPart:]))
			writeDone <- errors.Wrap(err)
		}()

		msg := rdr.Read(t)
		req, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("rdr.Read() type = %T, want *sip.Request", msg)
		}
		if got, want := req.Method, sip.RequestMethodOptions; got != want {
			t.Errorf("rdr.Read() method = %q, want %q", got, want)
		}
		if err := testutil.WaitResult(t, writeDone, "cln.Write()"); err != nil {
			t.Fatalf("rdr.Write() error = %v, want nil", err)
		}
	})
}

func TestStreamReader_Read_CoalescedMessages(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		srv, cln := net.Pipe()
		t.Cleanup(func() {
			_ = srv.Close()
			_ = cln.Close()
		})
		rdr := siptest.NewStreamReader(t, srv)

		wire := streamTestReq("OPTIONS", nil) + streamTestRes()
		writeDone := make(chan error, 1)
		go func() {
			_, err := cln.Write([]byte(wire))
			writeDone <- errors.Wrap(err)
		}()

		firstMsg := rdr.Read(t)
		first, ok := firstMsg.(*sip.Request)
		if !ok {
			t.Fatalf("first rdr.Read() type = %T, want *sip.Request", firstMsg)
		}
		if got, want := first.Method, sip.RequestMethodOptions; got != want {
			t.Errorf("first rdr.Read() method = %q, want %q", got, want)
		}

		secondMsg := rdr.Read(t)
		second, ok := secondMsg.(*sip.Response)
		if !ok {
			t.Fatalf("second rdr.Read() type = %T, want *sip.Response", secondMsg)
		}
		if got, want := second.Status, sip.ResponseStatusOK; got != want {
			t.Errorf("second rdr.Read() status = %v, want %v", got, want)
		}
		if err := testutil.WaitResult(t, writeDone, "cln.Write()"); err != nil {
			t.Fatalf("cln.Write() error = %v, want nil", err)
		}
	})
}

func TestStreamReader_Read_ContentLengthBody(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		srv, cln := net.Pipe()
		t.Cleanup(func() {
			_ = srv.Close()
			_ = cln.Close()
		})
		rdr := siptest.NewStreamReader(t, srv)

		body := "SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"
		wire := streamTestReq("MESSAGE", []byte(body)) + streamTestReq("BYE", nil)
		writeDone := make(chan error, 1)
		go func() {
			_, err := cln.Write([]byte(wire))
			writeDone <- errors.Wrap(err)
		}()

		msg := rdr.Read(t)
		req, ok := msg.(*sip.Request)
		if !ok {
			t.Fatalf("rdr.Read() type = %T, want *sip.Request", msg)
		}
		if got := string(req.Body); got != body {
			t.Errorf("rdr.Read() body = %q, want %q", got, body)
		}

		msg = rdr.Read(t)
		req, ok = msg.(*sip.Request)
		if !ok {
			t.Fatalf("second rdr.Read() type = %T, want *sip.Request", msg)
		}
		if got, want := req.Method, sip.RequestMethodBye; got != want {
			t.Errorf("second rdr.Read() method = %q, want %q", got, want)
		}
		if err := testutil.WaitResult(t, writeDone, "rdr.Write()"); err != nil {
			t.Fatalf("rdr.Write() error = %v, want nil", err)
		}
	})
}

func streamTestReq(method string, body []byte) string {
	return fmt.Sprintf(
		"%s sip:alice@example.com SIP/2.0\r\n"+
			"Via: SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bK-test\r\n"+
			"From: <sip:bob@example.com>;tag=from\r\n"+
			"To: <sip:alice@example.com>\r\n"+
			"Call-ID: stream-test\r\n"+
			"CSeq: 1 %s\r\n"+
			"Content-Length: %d\r\n\r\n%s",
		method, method, len(body), body,
	)
}

func streamTestRes() string {
	return "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bK-test\r\n" +
		"From: <sip:bob@example.com>;tag=from\r\n" +
		"To: <sip:alice@example.com>;tag=to\r\n" +
		"Call-ID: stream-test\r\n" +
		"CSeq: 1 OPTIONS\r\n" +
		"Content-Length: 0\r\n\r\n"
}
