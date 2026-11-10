package sip_test

import (
	"fmt"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

func ExampleMessageEnvelope_MarkForwarded() {
	env := sip.NewRequestEnvelope(&sip.Request{
		Method: sip.RequestMethodInvite,
		URI: &sip.URI{
			User: sip.MakeUserInfo("bob"),
			Addr: sip.MakeHostAddr("b.example.com"),
		},
		Proto: sip.ProtoVer20(),
	})

	fmt.Println(env.Forwarded())
	env.MarkForwarded()
	fmt.Println(env.Forwarded())

	// Output:
	// false
	// true
}

func ExampleRequestEnvelope_PrepareVia() {
	env := sip.NewRequestEnvelope(&sip.Request{
		Method: sip.RequestMethodInvite,
		URI: &sip.URI{
			User: sip.MakeUserInfo("bob"),
			Addr: sip.MakeHostAddr("b.example.com"),
		},
		Proto: sip.ProtoVer20(),
	})

	// A proxy forwards the request over its own top Via hop and records the
	// branch of that local hop as the designation.
	env.WithMessage(func(r *sip.Request) {
		r.Headers = make(sip.Headers)
		r.Headers.PrependVia(header.ViaHop{
			Proto:     sip.ProtoVer20(),
			Transport: "UDP",
			Addr:      sip.MakeHostPortAddr("proxy.example", 5060),
			Params:    sip.Values{"branch": {"z9hG4bK-local"}},
		})
	})
	env.MarkForwarded()
	env.SetForwardedViaBranch("z9hG4bK-local")

	// Transport finalization updates only the designated local top Via;
	// its branch is preserved.
	env.PrepareVia("TCP", sip.MakeHostPortAddr("203.0.113.10", 5060))

	env.WithMessage(func(r *sip.Request) {
		via, _ := r.Headers.FirstVia()
		branch, _ := via.Branch()
		fmt.Println(via.Transport)
		fmt.Println(via.Addr)
		fmt.Println(branch)
	})

	// Output:
	// TCP
	// 203.0.113.10:5060
	// z9hG4bK-local
}
