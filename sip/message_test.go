package sip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("SIP", Label("sip", "message"), func() {
	Describe("working with message headers", func() {
		var hdrs sip.Headers

		BeforeEach(func() {
			hdrs = make(sip.Headers).
				Append(header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: "UDP",
						Addr:      header.HostPort("127.0.0.1", 5060),
					},
					{
						Proto:     sip.ProtoVer20(),
						Transport: "TLS",
						Addr:      header.HostPort("127.0.0.2", 5061),
					},
				}).
				Append(header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: "TCP",
						Addr:      header.HostPort("127.0.0.3", 5062),
					},
				}).
				Append(header.Supported{"opt1"}).
				Append(header.Supported{"opt2", "opt3"})
		})

		It("should return all header elements", func() {
			Expect(sip.AllHeaderElems[header.Route](hdrs, "Route")).To(BeEmpty())
			Expect(sip.AllHeaderElems[header.Supported](hdrs, "Supported")).To(Equal([]string{"opt1", "opt2", "opt3"}))
			Expect(sip.AllHeaderElems[header.Via](hdrs, "Via")).To(Equal([]header.ViaHop{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      header.HostPort("127.0.0.1", 5060),
				},
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      header.HostPort("127.0.0.2", 5061),
				},
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TCP",
					Addr:      header.HostPort("127.0.0.3", 5062),
				},
			}))
		})

		It("should return first header", func() {
			Expect(sip.FirstHeader[*header.From](hdrs, "From")).To(BeNil())
			Expect(sip.FirstHeader[header.Via](hdrs, "Via")).To(Equal(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Addr:      header.HostPort("127.0.0.1", 5060),
				},
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TLS",
					Addr:      header.HostPort("127.0.0.2", 5061),
				},
			}))
			Expect(sip.FirstHeader[header.Supported](hdrs, "Supported")).To(Equal(header.Supported{"opt1"}))
		})

		It("should return last header", func() {
			Expect(sip.LastHeader[*header.From](hdrs, "From")).To(BeNil())
			Expect(sip.LastHeader[header.Via](hdrs, "Via")).To(Equal(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TCP",
					Addr:      header.HostPort("127.0.0.3", 5062),
				},
			}))
			Expect(sip.LastHeader[header.Supported](hdrs, "Supported")).To(Equal(header.Supported{"opt2", "opt3"}))
		})

		It("should return first element from the first header", func() {
			Expect(sip.FirstHeaderElem[header.Route](hdrs, "Route")).To(BeNil())
			Expect(sip.FirstHeaderElem[header.Via](hdrs, "Via")).To(Equal(&header.ViaHop{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      header.HostPort("127.0.0.1", 5060),
			}))
			Expect(sip.FirstHeaderElem[header.Supported](hdrs, "Supported")).To(HaveValue(Equal("opt1")))
		})

		It("should return last element from the last header", func() {
			Expect(sip.LastHeaderElem[header.Route](hdrs, "Route")).To(BeNil())
			Expect(sip.LastHeaderElem[header.Via](hdrs, "Via")).To(Equal(&header.ViaHop{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      header.HostPort("127.0.0.3", 5062),
			}))
			Expect(sip.LastHeaderElem[header.Supported](hdrs, "Supported")).To(HaveValue(Equal("opt3")))
		})

		It("should pop first element from the first header", func() {
			Expect(sip.PopFirstHeaderElem[header.Route](hdrs, "Route")).To(BeNil())

			Expect(sip.PopFirstHeaderElem[header.Via](hdrs, "Via")).To(Equal(&header.ViaHop{
				Proto:     sip.ProtoVer20(),
				Transport: "UDP",
				Addr:      header.HostPort("127.0.0.1", 5060),
			}))
			Expect(hdrs.Get("Via")).To(Equal([]header.Header{
				header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: "TLS",
						Addr:      header.HostPort("127.0.0.2", 5061),
					},
				},
				header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: "TCP",
						Addr:      header.HostPort("127.0.0.3", 5062),
					},
				},
			}))

			Expect(sip.PopFirstHeaderElem[header.Supported](hdrs, "Supported")).To(HaveValue(Equal("opt1")))
			Expect(hdrs.Get("Supported")).To(Equal([]header.Header{
				header.Supported{"opt2", "opt3"},
			}))
		})

		It("should pop last element from the last header", func() {
			Expect(sip.PopLastHeaderElem[header.Route](hdrs, "Route")).To(BeNil())

			Expect(sip.PopLastHeaderElem[header.Via](hdrs, "Via")).To(Equal(&header.ViaHop{
				Proto:     sip.ProtoVer20(),
				Transport: "TCP",
				Addr:      header.HostPort("127.0.0.3", 5062),
			}))
			Expect(hdrs.Get("Via")).To(Equal([]header.Header{
				header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: "UDP",
						Addr:      header.HostPort("127.0.0.1", 5060),
					},
					{
						Proto:     sip.ProtoVer20(),
						Transport: "TLS",
						Addr:      header.HostPort("127.0.0.2", 5061),
					},
				},
			}))

			Expect(sip.PopLastHeaderElem[header.Supported](hdrs, "Supported")).To(HaveValue(Equal("opt3")))
			Expect(hdrs.Get("Supported")).To(Equal([]header.Header{
				header.Supported{"opt1"},
				header.Supported{"opt2"},
			}))
		})
	})
})
