package common_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/common"
)

var _ = Describe("Common", func() {
	Describe("Addr", func() {
		DescribeTable("initializing",
			// region
			func(host string, port int, portSet bool) {
				var addr common.Addr
				if portSet {
					addr = common.HostPort(host, uint16(port))
				} else {
					addr = common.Host(host)
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
			func(addr common.Addr, expect string) {
				Expect(addr.String()).To(Equal(expect))
			},
			EntryDescription("%#[1]v"),
			Entry(nil, common.Addr{}, ""),
			Entry(nil, common.Host(""), ""),
			Entry(nil, common.HostPort("", 5060), ":5060"),
			Entry(nil, common.HostPort(" ", 5060), " :5060"),
			Entry(nil, common.Host("example.com"), "example.com"),
			Entry(nil, common.Host("ExAmplE.COM"), "ExAmplE.COM"),
			Entry(nil, common.Host("192.168.0.1"), "192.168.0.1"),
			Entry(nil, common.Host("2001:db8::9:1"), "[2001:db8::9:1]"),
			Entry(nil, common.HostPort("example.com", 0), "example.com:0"),
			Entry(nil, common.HostPort("2001:db8::9:1", 5060), "[2001:db8::9:1]:5060"),
			// endregion
		)

		DescribeTable("comparing", Label("comparing"),
			// region
			func(addr common.Addr, v any, expect bool) {
				Expect(addr.Equal(v)).To(Equal(expect))
			},
			EntryDescription("%#[1]v with value = %#[2]v"),
			Entry(nil, common.Addr{}, nil, false),
			Entry(nil, common.Addr{}, (*common.Addr)(nil), false),
			Entry(nil, common.Addr{}, common.Addr{}, true),
			Entry(nil, common.Host("example.com"), common.Addr{}, false),
			Entry(nil, common.HostPort("example.com", 0), common.Host("example.com"), false),
			Entry(nil,
				common.HostPort("example.com", 5060),
				common.HostPort("example.com", 5060),
				true,
			),
			Entry(nil,
				common.HostPort("example.com", 5060),
				common.HostPort("EXAMPLE.COM", 5060),
				true,
			),
			Entry(nil,
				common.HostPort("192.0.2.128", 5060),
				common.HostPort("192.0.2.128", 5060),
				true,
			),
			Entry(nil,
				common.HostPort("192.0.2.128", 5060),
				func() *common.Addr {
					addr := common.HostPort("192.0.2.128", 5060)
					return &addr
				}(),
				true,
			),
			Entry(nil,
				common.HostPort("192.0.2.128", 5060),
				common.HostPort("::ffff:192.0.2.128", 5060),
				true,
			),
			Entry(nil,
				common.HostPort("2001:db8::9:1", 5060),
				common.HostPort("2001:db8::9:01", 5060),
				true,
			),
			Entry(nil,
				common.HostPort("localhost", 5060),
				common.HostPort("127.0.0.1", 5060),
				false,
			),
			// endregion
		)

		DescribeTable("validating", Label("validating"),
			// region
			func(addr common.Addr, expect bool) {
				Expect(addr.IsValid()).To(Equal(expect))
			},
			EntryDescription("%[1]q"),
			Entry(nil, common.Host(""), false),
			Entry(nil, common.HostPort("", 5060), false),
			Entry(nil, common.Host("example.com"), true),
			Entry(nil, common.HostPort("example.com", 0), true),
			Entry(nil, common.HostPort("example.com", 999), true),
			// endregion
		)

		DescribeTable("cloning", Label("cloning"),
			// region
			func(addr1 common.Addr) {
				addr2 := addr1.Clone()
				Expect(addr2).To(Equal(addr1), "assert cloned Addr is equal to the original")
			},
			EntryDescription("%#v"),
			Entry(nil, common.Host("")),
			Entry(nil, common.Host("example.com")),
			Entry(nil, common.HostPort("example.com", 0)),
			Entry(nil, common.HostPort("example.com", 5060)),
			// endregion
		)
	})
})
