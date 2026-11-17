package transaction

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/parser"
)

type registrationTransport struct {
	messages chan sip.Message
	send     func(sip.Message) error
}

func (tp *registrationTransport) Messages() <-chan sip.Message { return tp.messages }
func (tp *registrationTransport) Send(msg sip.Message) error   { return tp.send(msg) }
func (tp *registrationTransport) IsReliable(network string) bool {
	return network == "TCP"
}
func (tp *registrationTransport) IsStreamed(network string) bool {
	return tp.IsReliable(network)
}

func registrationRequest(t *testing.T, method sip.RequestMethod, network string) sip.Request {
	t.Helper()
	raw := strings.Join([]string{
		string(method) + " sip:device@127.0.0.1 SIP/2.0",
		"Via: SIP/2.0/" + network + " 127.0.0.1:5060;branch=z9hG4bK-registration",
		"From: <sip:platform@127.0.0.1>;tag=platform",
		"To: <sip:device@127.0.0.1>",
		"Call-ID: registration-order",
		"CSeq: 1 " + string(method),
		"Content-Length: 0", "", "",
	}, "\r\n")
	msg, err := parser.ParseMessage([]byte(raw), log.NewDefaultLogrusLogger())
	if err != nil {
		t.Fatal(err)
	}
	return msg.(sip.Request)
}

func TestClientTransactionMatchesResponseDuringInitialSend(t *testing.T) {
	for _, network := range []string{"UDP", "TCP"} {
		for _, method := range []sip.RequestMethod{sip.INVITE, sip.MESSAGE} {
			t.Run(network+"/"+string(method), func(t *testing.T) {
				tp := &registrationTransport{messages: make(chan sip.Message)}
				txl := NewLayer(tp, log.NewDefaultLogrusLogger()).(*layer)
				defer txl.Cancel()

				tp.send = func(msg sip.Message) error {
					res := sip.NewResponseFromRequest("", msg.(sip.Request), 200, "OK", "")
					// Check the transaction key before Send returns. This fails
					// deterministically even if the message loop has not run yet.
					if _, err := txl.getClientTx(res); err != nil {
						return fmt.Errorf("SIP response could not match during Send: %w", err)
					}
					select {
					case tp.messages <- res:
						return nil
					case <-time.After(time.Second):
						return errors.New("message loop did not accept the response")
					}
				}

				tx, err := txl.Request(registrationRequest(t, method, network))
				if err != nil {
					t.Fatal(err)
				}
				select {
				case res := <-tx.Responses():
					if res == nil || res.StatusCode() != 200 {
						t.Fatalf("expected 200 OK, got %v", res)
					}
				case res := <-txl.Responses():
					t.Fatalf("matched response reached the unmatched queue: %v", res)
				case <-time.After(time.Second):
					t.Fatal("transaction did not receive the immediate response")
				}
			})
		}
	}
}

func TestClientTransactionCanRetryKeyAfterSendFailure(t *testing.T) {
	tp := &registrationTransport{messages: make(chan sip.Message)}
	txl := NewLayer(tp, log.NewDefaultLogrusLogger()).(*layer)
	defer txl.Cancel()

	req := registrationRequest(t, sip.MESSAGE, "UDP")
	key, err := MakeClientTxKey(req)
	if err != nil {
		t.Fatal(err)
	}
	sendErr := errors.New("initial send failed")
	tp.send = func(sip.Message) error { return sendErr }
	if _, err := txl.Request(req); !errors.Is(err, sendErr) {
		t.Fatalf("expected send failure, got %v", err)
	}
	if _, exists := txl.transactions.get(key); exists {
		t.Fatal("failed request left its transaction registered")
	}

	tp.send = func(sip.Message) error { return nil }
	if _, err := txl.Request(registrationRequest(t, sip.MESSAGE, "UDP")); err != nil {
		t.Fatalf("retry with the same transaction key failed: %v", err)
	}
}

func TestClientTransactionDeregistersWhenLayerStopsDuringSend(t *testing.T) {
	tp := &registrationTransport{messages: make(chan sip.Message)}
	txl := NewLayer(tp, log.NewDefaultLogrusLogger()).(*layer)
	defer txl.Cancel()

	req := registrationRequest(t, sip.MESSAGE, "UDP")
	key, err := MakeClientTxKey(req)
	if err != nil {
		t.Fatal(err)
	}
	tp.send = func(sip.Message) error {
		txl.Cancel()
		<-txl.Done()
		return nil
	}
	if _, err := txl.Request(req); err == nil {
		t.Fatal("request succeeded after the transaction layer stopped")
	}
	if _, exists := txl.transactions.get(key); exists {
		t.Fatal("canceled request left its transaction registered")
	}
}

func TestDuplicateClientTransactionKeepsOriginal(t *testing.T) {
	tp := &registrationTransport{messages: make(chan sip.Message)}
	txl := NewLayer(tp, log.NewDefaultLogrusLogger()).(*layer)
	defer txl.Cancel()

	req := registrationRequest(t, sip.MESSAGE, "TCP")
	key, err := MakeClientTxKey(req)
	if err != nil {
		t.Fatal(err)
	}
	sends := 0
	tp.send = func(sip.Message) error {
		sends++
		return nil
	}
	if _, err := txl.Request(req); err != nil {
		t.Fatal(err)
	}
	original, _ := txl.transactions.get(key)
	if _, err := txl.Request(registrationRequest(t, sip.MESSAGE, "TCP")); err == nil {
		t.Fatal("duplicate transaction succeeded")
	}
	if stored, exists := txl.transactions.get(key); !exists || stored != original || sends != 1 {
		t.Fatalf("duplicate transaction disturbed the original: stored %v, sends %d", exists, sends)
	}
}
