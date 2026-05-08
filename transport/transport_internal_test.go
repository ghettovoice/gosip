package transport

import (
	"net"
	"sync"
	"testing"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
)

type mockProtocol struct{}

func (p *mockProtocol) Network() string {
	return "UDP"
}

func (p *mockProtocol) Reliable() bool {
	return false
}

func (p *mockProtocol) Streamed() bool {
	return false
}

func (p *mockProtocol) Listen(target *Target, options ...ListenOption) error {
	return nil
}

func (p *mockProtocol) Send(target *Target, msg sip.Message) error {
	return nil
}

func (p *mockProtocol) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (p *mockProtocol) String() string {
	return "mock"
}

func TestLayerSend_DataRace(t *testing.T) {
	store := newProtocolStore()
	store.put("UDP", &mockProtocol{})

	tpl := &layer{
		ip: net.ParseIP("127.0.0.1"),
		listenPorts: map[string][]sip.Port{
			"UDP": {5060},
		},
		protocols: store,
		log:       log.NewDefaultLogrusLogger(),
	}

	uri := &sip.SipUri{
		FUser: sip.String{Str: "test"},
		FHost: "example.com",
	}

	via := &sip.ViaHop{
		ProtocolName:    "SIP",
		ProtocolVersion: "2.0",
		Transport:       "UDP",
		Host:            "example.com",
	}

	req := sip.NewRequest(
		"",
		sip.INFO,
		uri,
		"SIP/2.0",
		[]sip.Header{
			sip.ViaHeader{via},
		},
		"",
		nil,
	)

	req.SetTransport("UDP")
	req.SetDestination("127.0.0.1:5060")

	var wg sync.WaitGroup

	// writers mutate ViaHop inside Send()
	for i := 0; i < 8; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < 100; j++ {
				_ = tpl.Send(req)
			}
		}()
	}

	// readers stringify the same request concurrently
	for i := 0; i < 8; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < 100; j++ {
				_ = req.String()
			}
		}()
	}

	wg.Wait()
}
