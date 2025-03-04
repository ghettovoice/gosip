package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Via", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Via:", &header.Any{Name: "Via"}, nil),
			Entry(nil, "Via: ", &header.Any{Name: "Via"}, nil),
			Entry(nil, "Via: abc", &header.Any{Name: "Via", Value: "abc"}, nil),
			Entry(nil,
				"Via: SIP / 2.0 / UDP     erlang.bell-telephone.com:5060;received=192.0.2.207;branch=z9hG4bK87asdks7,\r\n"+
					"\tSIP/2.0/UDP first.example.com: 4000;ttl=16\r\n"+
					"\t;maddr=224.2.0.1 ;branch=z9hG4bKa7c6a8dlze.1",
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params: make(header.Values).
							Set("received", "192.0.2.207").
							Set("branch", "z9hG4bK87asdks7"),
					},
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("first.example.com", 4000),
						Params: make(header.Values).
							Set("ttl", "16").
							Set("maddr", "224.2.0.1").
							Set("branch", "z9hG4bKa7c6a8dlze.1"),
					},
				},
				nil,
			),
			Entry(nil,
				"Via: SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;rport",
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params: make(header.Values).
							Set("branch", "z9hG4bK87asdks7").
							Set("rport", ""),
					},
				},
				nil,
			),
			Entry(nil,
				"Via: SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;rport=123",
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params: make(header.Values).
							Set("branch", "z9hG4bK87asdks7").
							Set("rport", "123"),
					},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.Via(nil), ""),
			Entry(nil, header.Via{}, "Via: "),
			Entry(nil, header.Via{{}}, "Via: // "),
			Entry(nil,
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params: make(header.Values).
							Set("received", "192.0.2.207").
							Set("branch", "z9hG4bK87asdks7"),
					},
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "TCP",
						Addr:      header.HostPort("first.example.com", 4000),
						Params: make(header.Values).
							Set("ttl", "16").
							Set("maddr", "224.2.0.1").
							Set("branch", "z9hG4bKa7c6a8dlze.1"),
					},
				},
				"Via: SIP/2.0/UDP erlang.bell-telephone.com:5060;branch=z9hG4bK87asdks7;received=192.0.2.207, "+
					"SIP/2.0/TCP first.example.com:4000;branch=z9hG4bKa7c6a8dlze.1;maddr=224.2.0.1;ttl=16",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Via{}, nil, false),
			Entry(nil, header.Via(nil), header.Via(nil), true),
			Entry(nil, header.Via{}, header.Via(nil), true),
			Entry(nil, header.Via{}, header.Via{}, true),
			Entry(nil, header.Via{{}}, header.Via{}, false),
			Entry(nil,
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params: make(header.Values).
							Set("received", "192.0.2.207").
							Set("branch", "z9hG4bK87asdks7"),
					},
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "TCP",
						Addr:      header.HostPort("first.example.com", 4000),
						Params: make(header.Values).
							Set("ttl", "16").
							Set("maddr", "224.2.0.1").
							Set("branch", "z9hG4bKa7c6a8dlze.1"),
					},
				},
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "TCP",
						Addr:      header.HostPort("first.example.com", 4000),
						Params: make(header.Values).
							Set("ttl", "16").
							Set("maddr", "224.2.0.1").
							Set("branch", "z9hG4bKa7c6a8dlze.1"),
					},
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params: make(header.Values).
							Set("received", "192.0.2.207").
							Set("branch", "z9hG4bK87asdks7"),
					},
				},
				false,
			),
			Entry(nil,
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params: make(header.Values).
							Set("received", "192.0.2.207").
							Set("branch", "z9hG4bK87asdks7"),
					},
				},
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params: make(header.Values).
							Set("received", "192.0.2.207").
							Set("branch", "z9hG4bK87asdks7"),
					},
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "TCP",
						Addr:      header.HostPort("first.example.com", 4000),
						Params: make(header.Values).
							Set("ttl", "16").
							Set("maddr", "224.2.0.1").
							Set("branch", "z9hG4bKa7c6a8dlze.1"),
					},
				},
				false,
			),
			Entry(nil,
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "sip", Version: "2.0"},
						Transport: "udp",
						Addr:      header.HostPort("ERLANG.BELL-TELEPHONE.COM", 5060),
						Params: make(header.Values).
							Set("received", "192.0.2.207").
							Set("branch", "z9hG4bK87asdks7"),
					},
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "TCP",
						Addr:      header.HostPort("first.example.com", 4000),
						Params: make(header.Values).
							Set("ttl", "16").
							Set("maddr", "224.2.0.1").
							Set("branch", "z9hG4bKa7c6a8dlze.1").
							Set("custom_1", "qwerty").
							Set("custom_2", `"Aaa BBB"`),
					},
				},
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "UDP",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params: make(header.Values).
							Set("received", "192.0.2.207").
							Set("branch", "z9hG4bK87asdks7"),
					},
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "TCP",
						Addr:      header.HostPort("first.example.com", 4000),
						Params: make(header.Values).
							Set("ttl", "16").
							Set("maddr", "224.2.0.1").
							Set("branch", "z9hG4bKa7c6a8dlze.1").
							Set("custom_2", `"Aaa BBB"`),
					},
				},
				true,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.Via(nil), false),
			Entry(nil, header.Via{}, false),
			Entry(nil, header.Via{{}}, false),
			Entry(nil,
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "TLS",
						Addr:      header.HostPort("erlang.bell-telephone.com", 5060),
						Params:    make(header.Values).Set("branch", "z9hG4bK87asdks7"),
					},
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "TCP",
						Addr:      header.HostPort("first.example.com", 4000),
						Params: make(header.Values).
							Set("ttl", "16").
							Set("maddr", "224.2.0.1").
							Set("branch", "z9hG4bKa7c6a8dlze.1"),
					},
				},
				true,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Via) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).
						ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
					for i := range hdr1 {
						if hdr1[i].Params == nil {
							Expect(hdr2[i].Params).To(BeNil())
						} else {
							Expect(reflect.ValueOf(hdr2[i].Params).Pointer()).
								ToNot(Equal(reflect.ValueOf(hdr1[i].Params).Pointer()))
						}
					}
				}
			},
			Entry(nil, header.Via(nil)),
			Entry(nil, header.Via{}),
			Entry(nil,
				header.Via{
					{
						Proto:     header.ProtoInfo{Name: "sip", Version: "2.0"},
						Transport: "udp",
						Addr:      header.HostPort("ERLANG.BELL-TELEPHONE.COM", 5060),
						Params: make(header.Values).
							Set("received", "192.0.2.207").
							Set("branch", "z9hG4bK87asdks7"),
					},
					{
						Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
						Transport: "TCP",
						Addr:      header.HostPort("first.example.com", 4000),
						Params: make(header.Values).
							Set("ttl", "16").
							Set("maddr", "224.2.0.1").
							Set("branch", "z9hG4bKa7c6a8dlze.1").
							Set("custom_1", "qwerty").
							Set("custom_2", `"Aaa BBB"`),
					},
				},
			),
			// endregion
		)
	})
})
