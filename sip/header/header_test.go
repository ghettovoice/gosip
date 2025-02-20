package header_test

import (
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

var _ = Describe("Header", Label("sip", "header"), func() {
	DescribeTable("CanonicName()",
		// region
		func(in string, expect header.Name) {
			Expect(header.CanonicName(in)).To(Equal(expect))
		},
		EntryDescription(`should convert "%s" to "%v"`),
		// region entries
		Entry(nil, "call-id", header.Name("Call-ID")),
		Entry(nil, "cALL-id", header.Name("Call-ID")),
		Entry(nil, "Call-Id", header.Name("Call-ID")),
		Entry(nil, "i", header.Name("Call-ID")),
		Entry(nil, "Call-ID", header.Name("Call-ID")),
		Entry(nil, "cseq", header.Name("CSeq")),
		Entry(nil, "Cseq", header.Name("CSeq")),
		Entry(nil, "x-custom-header", header.Name("X-Custom-Header")),
		Entry(nil, "l", header.Name("Content-Length")),
		Entry(nil, "mime-version", header.Name("MIME-Version")),
		// endregion
		// endregion
	)
})

func assertHeaderParsing(entries ...TableEntry) {
	DescribeTable("parsing", Label("parsing"),
		func(in string, expectHdr header.Header, expectErr any) {
			hdr, err := header.Parse(in, nil)
			if expectErr == nil {
				Expect(hdr).ToNot(BeNil(), "assert parsed header isn't nil")
				Expect(hdr).To(Equal(expectHdr), "assert parsed header is equal to the expected header")
				Expect(err).ToNot(HaveOccurred(), "assert parsed error is nil")
			} else {
				Expect(hdr).To(BeNil(), "assert parsed header is nil")
				Expect(err).To(MatchError(expectErr.(error)), "assert parse error matches the expected error") //nolint:forcetypeassert
			}
		},
		EntryDescription("%[1]q"),
		Entry(nil, "", nil, grammar.ErrEmptyInput),
		Entry(nil, "qwerty", nil, grammar.ErrMalformedInput),
		entries,
	)
}

func assertHeaderRendering(entries ...TableEntry) {
	DescribeTable("rendering", Label("rendering"),
		func(hdr header.Header, expect string) {
			Expect(hdr.Render()).To(Equal(expect))
		},
		EntryDescription("%#[1]v"),
		entries,
	)
}

func assertHeaderComparing(entries ...TableEntry) {
	DescribeTable("comparing", Label("comparing"),
		func(hdr header.Header, val any, expect bool) {
			Expect(utils.IsEqual(hdr, val)).To(Equal(expect))
		},
		EntryDescription("%#[1]v with value = %#[2]v"),
		entries,
	)
}

func assertHeaderValidating(entries ...TableEntry) {
	DescribeTable("validating", Label("validating"),
		func(hdr header.Header, expect bool) {
			Expect(utils.IsValid(hdr)).To(Equal(expect))
		},
		EntryDescription("%[1]q"),
		entries,
	)
}

func assertHeaderCloning[T header.Header](checkFn func(hdr1, hdr2 T), entries ...TableEntry) {
	DescribeTable("cloning", Label("cloning"),
		// region
		func(hdr1 T) {
			hdr2 := utils.Clone[header.Header](hdr1)
			rval := reflect.ValueOf(hdr1)
			if (rval.Kind() == reflect.Ptr ||
				rval.Kind() == reflect.Interface ||
				rval.Kind() == reflect.Slice) &&
				rval.IsNil() {
				Expect(hdr2).To(BeNil(), "assert cloned header is nil")
			} else {
				hdr2, ok := hdr2.(T)
				Expect(ok).To(BeTrue(), fmt.Sprintf("assert cloned header is %T", hdr1))
				Expect(hdr2).To(Equal(hdr1), "assert cloned header is equal to the original header")
				checkFn(hdr1, hdr2)
			}
		},
		EntryDescription("%#v"),
		entries,
		// endregion
	)
}
