package header_test

import (
	. "github.com/onsi/ginkgo/v2"

	"github.com/ghettovoice/gosip/sip/header"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	Describe("Content-Language", func() {
		assertHeaderParsing(
			// region
			Entry(nil, "Content-Language: ", &header.Any{Name: "Content-Language"}, nil),
			Entry(nil, "Content-Language: en, ru-RU", header.ContentLanguage{"en", "ru-RU"}, nil),
			// endregion
		)

		assertHeaderRendering(
			// region
			Entry(nil, header.ContentLanguage(nil), ""),
			Entry(nil, header.ContentLanguage{}, "Content-Language: "),
			Entry(nil, header.ContentLanguage{"en", "ru-RU"}, "Content-Language: en, ru-RU"),
			// endregion
		)

		assertHeaderComparing(
			// region
			Entry(nil, header.ContentLanguage(nil), nil, false),
			Entry(nil, header.ContentLanguage(nil), header.ContentLanguage(nil), true),
			Entry(nil, header.ContentLanguage{}, header.ContentLanguage(nil), true),
			Entry(nil, header.ContentLanguage{}, header.ContentLanguage{}, true),
			Entry(nil, header.ContentLanguage{}, header.ContentLanguage{"en"}, false),
			Entry(nil, header.ContentLanguage{"en", "ru-RU"}, header.ContentLanguage{"EN", "ru-RU"}, true),
			Entry(nil, header.ContentLanguage{"ru-RU", "en"}, header.ContentLanguage{"en", "ru-RU"}, false),
			Entry(nil, header.ContentLanguage{"ru-RU"}, header.ContentLanguage{"en", "ru-RU"}, false),
			// endregion
		)

		assertHeaderValidating(
			// region
			Entry(nil, header.ContentLanguage(nil), false),
			Entry(nil, header.ContentLanguage{}, false),
			Entry(nil, header.ContentLanguage{"en", "ru-RU"}, true),
			Entry(nil, header.ContentLanguage{"ru RU"}, false),
			// endregion
		)

		assertHeaderCloning(
			// region
			func(hdr1, hdr2 header.ContentLanguage) {},
			Entry(nil, header.ContentLanguage(nil)),
			Entry(nil, header.ContentLanguage{}),
			Entry(nil, header.ContentLanguage{"en", "ru-RU"}),
			// endregion
		)
	})
})
