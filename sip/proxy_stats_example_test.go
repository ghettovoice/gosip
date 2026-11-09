package sip_test

import (
	"context"
	"fmt"

	"github.com/ghettovoice/gosip/sip"
)

// ExampleStatsRecorder_proxy composes one [sip.StatsRecorder] for both of
// its observer roles. Bound as element middleware it counts transport
// traffic and transaction lifecycles exactly as before; bound to the proxy
// it derives the cumulative [sip.ProxyStats] counters of
// [sip.StatsReport.Proxy] from the committed [sip.ProxyEvent] stream. The
// two roles measure different layers — one request counts once in
// RequestsHandled while each forked branch settles as its own forward
// attempt — so a single recorder never counts one logical event twice.
//
// The proxy counters are subscription-lifetime observed totals: binding
// sees future events only, nothing is replayed, and they carry no
// per-request or per-target labels.
func ExampleStatsRecorder_proxy() {
	elm := &sip.Element{}
	defer func() { _ = elm.Close() }()

	stats := &sip.StatsRecorder{}

	// The middleware role installs the transport interceptors and the
	// transaction handler; the returned unbind detaches them all.
	unbindMw := elm.UseMiddleware(stats)
	defer unbindMw()

	prx, err := sip.NewProxy(elm, []sip.ProxyRoute{
		{
			Name: "static-location",
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (sip.ProxyTargetPlan, error) {
				return sip.ProxyTargetPlan{Groups: [][]sip.ProxyTarget{
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
	}, sip.ProxyOptions{
		ServedDomains: []string{"example.com"},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// The observer role consumes each committed event synchronously; the
	// returned unbind stops future updates without touching the totals
	// recorded so far.
	unbindEv := prx.BindEventHandler(stats)
	defer unbindEv()

	report := stats.Report()
	fmt.Println("requests handled:", report.Proxy.RequestsHandled)

	// Output:
	// requests handled: 0
}
