package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("CSeq", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "CSeq: abc", &header.Any{Name: "CSeq", Value: "abc"}, nil),
			Entry(nil, "CSeq: 4711 INVITE", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, nil),
			Entry(nil, "Cseq: 4711 INVITE", &header.CSeq{SeqNum: 4711, Method: "INVITE"}, nil),
			Entry(nil, "CSeq: 33 CUSTOM", &header.CSeq{SeqNum: 33, Method: "CUSTOM"}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, (*header.CSeq)(nil), ""),
			Entry(nil, &header.CSeq{}, "CSeq: 0 "),
			Entry(nil, &header.CSeq{SeqNum: 4711, Method: "INVITE"}, "CSeq: 4711 INVITE"),
			Entry(nil, &header.CSeq{SeqNum: 4711, Method: "custom"}, "CSeq: 4711 custom"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, (*header.CSeq)(nil), nil, false),
			Entry(nil, (*header.CSeq)(nil), (*header.CSeq)(nil), true),
			Entry(nil, &header.CSeq{}, (*header.CSeq)(nil), false),
			Entry(nil,
				&header.CSeq{SeqNum: 4711, Method: "INVITE"},
				header.CSeq{SeqNum: 4711, Method: "invite"},
				true,
			),
			Entry(nil,
				&header.CSeq{SeqNum: 4711, Method: "INVITE"},
				&header.CSeq{SeqNum: 123, Method: "INVITE"},
				false,
			),
			Entry(nil,
				&header.CSeq{SeqNum: 4711, Method: "INVITE"},
				&header.CSeq{SeqNum: 4711, Method: "BYE"},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, (*header.CSeq)(nil), false),
			Entry(nil, &header.CSeq{}, false),
			Entry(nil, &header.CSeq{Method: "INVITE"}, false),
			Entry(nil, &header.CSeq{SeqNum: 4711, Method: "INVITE"}, true),
			Entry(nil, &header.CSeq{SeqNum: 4711, Method: "a c k"}, false),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 *header.CSeq) {},
			Entry(nil, (*header.CSeq)(nil)),
			Entry(nil, &header.CSeq{SeqNum: 472, Method: "INVITE"}),
			// endregion
		)
	})
})
