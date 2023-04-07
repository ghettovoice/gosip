package header_test

import (
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Warning", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Warning: ", &header.Any{"Warning", ""}, nil),
			Entry(nil, "Warning: qwerty", &header.Any{"Warning", "qwerty"}, nil),
			Entry(nil, "Warning: 307 isi.edu", &header.Any{"Warning", "307 isi.edu"}, nil),
			Entry(nil,
				"Warning: 307 isi.edu \"\"",
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "",
					},
				},
				nil,
			),
			Entry(nil,
				"Warning: 307 isi.edu \"Session parameter 'foo' not understood\",\r\n"+
					"\t301 isi.edu \"Incompatible network address type 'E.164'\"",
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				nil,
			),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.Warning(nil), ""),
			Entry(nil, header.Warning{}, "Warning: "),
			Entry(nil, header.Warning{{}}, "Warning: 0  \"\""),
			Entry(nil,
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				"Warning: 307 isi.edu \"Session parameter 'foo' not understood\", "+
					"301 isi.edu \"Incompatible network address type 'E.164'\"",
			),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.Warning(nil), nil, false),
			Entry(nil, header.Warning(nil), header.Warning(nil), true),
			Entry(nil, header.Warning{}, header.Warning(nil), true),
			Entry(nil, header.Warning{}, header.Warning{}, true),
			Entry(nil, header.Warning{{}}, header.Warning{}, false),
			Entry(nil,
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
				},
				false,
			),
			Entry(nil,
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				header.Warning{
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
				},
				false,
			),
			Entry(nil,
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				header.Warning{
					{
						Code:  307,
						Agent: "ISI.EDU",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "ISI.EDU",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				true,
			),
			Entry(nil,
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "SESSION PARAMETER 'FOO' NOT UNDERSTOOD",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.Warning(nil), false),
			Entry(nil, header.Warning{}, false),
			Entry(nil,
				header.Warning{
					{
						Code:  307,
						Agent: "isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				true,
			),
			Entry(nil,
				header.Warning{
					{
						Code:  307,
						Agent: " isi . edu ",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
				false,
			),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.Warning) {
				if len(hdr1) > 0 {
					Expect(reflect.ValueOf(hdr2).Pointer()).
						ToNot(Equal(reflect.ValueOf(hdr1).Pointer()))
				}
			},
			Entry(nil, header.Warning(nil)),
			Entry(nil, header.Warning{}),
			Entry(nil,
				header.Warning{
					{
						Code:  307,
						Agent: " isi.edu",
						Text:  "Session parameter 'foo' not understood",
					},
					{
						Code:  301,
						Agent: "isi.edu",
						Text:  "Incompatible network address type 'E.164'",
					},
				},
			),
			// endregion
		)
	})
})
