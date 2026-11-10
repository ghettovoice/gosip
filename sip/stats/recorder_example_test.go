package stats_test

import (
	"context"
	"fmt"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/proxy"
	"github.com/ghettovoice/gosip/sip/stats"
)

// ExampleRecorder_proxy composes one [stats.Recorder] for both of
// its observer roles. Bound as element middleware it counts transport
// traffic and transaction lifecycles exactly as before; bound to the proxy
// it derives the cumulative [stats.Proxy] counters of
// [stats.Report.Proxy] from the committed [proxy.Event] stream. The
// two roles measure different layers — one request counts once in
// RequestsHandled while each forked branch settles as its own forward
// attempt — so a single recorder never counts one logical event twice.
//
// The proxy counters are subscription-lifetime observed totals: binding
// sees future events only, nothing is replayed, and they carry no
// per-request or per-target labels.
func ExampleRecorder_proxy() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	rcdr := &stats.Recorder{}

	// The middleware role installs the transport interceptors and the
	// transaction handler; the returned unbind detaches them all.
	unbindMw := elm.UseMiddleware(rcdr)
	defer unbindMw()

	prx, err := proxy.New([]proxy.Route{
		{
			Name: "static-location",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{
					{{
						ID: "alice-desk",
						URI: &sip.URI{
							User: sip.MakeUserInfo("alice"),
							Addr: sip.MakeHostPortAddr("192.0.2.10", 5060),
						},
					}},
				}}, nil
			},
		},
	}, proxy.Options{
		ServedDomains: []string{"example.com"},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// The proxy itself is explicit element middleware; the recorder's
	// middleware role above stays independent of this registration.
	// Closing the proxy retires its owned work before the middleware
	// unbind detaches the registration.
	unbindPrx := elm.UseMiddleware(prx)
	defer func() {
		_ = prx.Close()
		unbindPrx()
	}()

	// The observer role consumes each committed event synchronously; the
	// returned unbind stops future updates without touching the totals
	// recorded so far.
	unbindEv := prx.BindEventHandler(rcdr)
	defer unbindEv()

	report := rcdr.Report()
	fmt.Println("requests handled:", report.Proxy.RequestsHandled)

	// Output:
	// requests handled: 0
}
