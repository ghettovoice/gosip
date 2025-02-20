package shared_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/internal/shared"
)

var _ = Describe("Shared", Label("sip", "shared"), func() {
	Describe("Addr", func() {
		DescribeTable("initializing",
			// region
			func(host string, port int, portSet bool) {
				var addr shared.Addr
				if portSet {
					addr = shared.HostPort(host, uint16(port))
				} else {
					addr = shared.Host(host)
				}
				Expect(addr.Host()).To(Equal(host), "assert host = %s", host)
				po, ok := addr.Port()
				Expect(po).To(BeEquivalentTo(port), "assert port = %d", port)
				Expect(ok).To(Equal(portSet), "assert port set = %v", portSet)
			},
			EntryDescription(`with host = %q, port = %v, port set = %v`),
			Entry(nil, "example.com", 0, false),
			Entry(nil, "example.com", 0, true),
			Entry(nil, "example.com", 5060, true),
			Entry(nil, "", 5060, true),
			// endregion
		)

		DescribeTable("rendering", Label("rendering"),
			// region
			func(addr shared.Addr, expect string) {
				Expect(addr.String()).To(Equal(expect))
			},
			EntryDescription("%#[1]v"),
			Entry(nil, shared.Addr{}, ""),
			Entry(nil, shared.Host(""), ""),
			Entry(nil, shared.HostPort("", 5060), ":5060"),
			Entry(nil, shared.HostPort(" ", 5060), " :5060"),
			Entry(nil, shared.Host("example.com"), "example.com"),
			Entry(nil, shared.Host("ExAmplE.COM"), "ExAmplE.COM"),
			Entry(nil, shared.Host("192.168.0.1"), "192.168.0.1"),
			Entry(nil, shared.Host("2001:db8::9:1"), "[2001:db8::9:1]"),
			Entry(nil, shared.HostPort("example.com", 0), "example.com:0"),
			Entry(nil, shared.HostPort("2001:db8::9:1", 5060), "[2001:db8::9:1]:5060"),
			// endregion
		)

		DescribeTable("comparing", Label("comparing"),
			// region
			func(addr shared.Addr, v any, expect bool) {
				Expect(addr.Equal(v)).To(Equal(expect))
			},
			EntryDescription("%#[1]v with value = %#[2]v"),
			Entry(nil, shared.Addr{}, nil, false),
			Entry(nil, shared.Addr{}, (*shared.Addr)(nil), false),
			Entry(nil, shared.Addr{}, shared.Addr{}, true),
			Entry(nil, shared.Host("example.com"), shared.Addr{}, false),
			Entry(nil, shared.HostPort("example.com", 0), shared.Host("example.com"), false),
			Entry(nil,
				shared.HostPort("example.com", 5060),
				shared.HostPort("example.com", 5060),
				true,
			),
			Entry(nil,
				shared.HostPort("example.com", 5060),
				shared.HostPort("EXAMPLE.COM", 5060),
				true,
			),
			Entry(nil,
				shared.HostPort("192.0.2.128", 5060),
				shared.HostPort("192.0.2.128", 5060),
				true,
			),
			Entry(nil,
				shared.HostPort("192.0.2.128", 5060),
				func() *shared.Addr {
					addr := shared.HostPort("192.0.2.128", 5060)
					return &addr
				}(),
				true,
			),
			Entry(nil,
				shared.HostPort("192.0.2.128", 5060),
				shared.HostPort("::ffff:192.0.2.128", 5060),
				true,
			),
			Entry(nil,
				shared.HostPort("2001:db8::9:1", 5060),
				shared.HostPort("2001:db8::9:01", 5060),
				true,
			),
			Entry(nil,
				shared.HostPort("localhost", 5060),
				shared.HostPort("127.0.0.1", 5060),
				false,
			),
			// endregion
		)

		DescribeTable("validating", Label("validating"),
			// region
			func(addr shared.Addr, expect bool) {
				Expect(addr.IsValid()).To(Equal(expect))
			},
			EntryDescription("%[1]q"),
			Entry(nil, shared.Host(""), false),
			Entry(nil, shared.HostPort("", 5060), false),
			Entry(nil, shared.Host("example.com"), true),
			Entry(nil, shared.HostPort("example.com", 0), true),
			Entry(nil, shared.HostPort("example.com", 999), true),
			// endregion
		)

		DescribeTable("cloning", Label("cloning"),
			// region
			func(addr1 shared.Addr) {
				addr2 := addr1.Clone()
				Expect(addr2).To(Equal(addr1), "assert cloned Addr is equal to the original")
			},
			EntryDescription("%#v"),
			Entry(nil, shared.Host("")),
			Entry(nil, shared.Host("example.com")),
			Entry(nil, shared.HostPort("example.com", 0)),
			Entry(nil, shared.HostPort("example.com", 5060)),
			// endregion
		)
	})
})
