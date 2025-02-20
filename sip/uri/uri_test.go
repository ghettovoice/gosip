package uri_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

func assertURIParsing(entries ...TableEntry) {
	DescribeTable("parsing", Label("parsing"),
		func(in string, expectURI uri.URI, expectErr any) {
			u, err := uri.Parse(in)
			if expectErr == nil {
				Expect(u).ToNot(BeNil(), "assert parsed URI isn't nil")
				Expect(u).To(Equal(expectURI), "assert parsed %s URI is equal to the expected URI")
				// Expect(u.Equal(expectURI)).To(BeTrue(), "parsed URI == expected URI")
				Expect(err).ToNot(HaveOccurred(), "assert parse error is nil")
			} else {
				Expect(u).To(BeNil(), "assert parsed %s URI is nil")
				Expect(err).To(MatchError(expectErr.(error)), "assert parse error matches the expected error") //nolint:forcetypeassert
			}
		},
		EntryDescription("%[1]q"),
		Entry(nil, "", nil, grammar.ErrEmptyInput),
		entries,
	)
}

func assertURIRendering(entries ...TableEntry) {
	DescribeTable("rendering", Label("rendering"),
		func(u uri.URI, expect string) {
			Expect(u.Render()).To(Equal(expect))
		},
		EntryDescription("%#[1]v"),
		entries,
	)
}

func assertURIComparing(entries ...TableEntry) {
	DescribeTable("comparing", Label("comparing"),
		func(u uri.URI, v any, expect bool) {
			Expect(utils.IsEqual(u, v)).To(Equal(expect))
		},
		EntryDescription("%#[1]v with value = %#[1]v"),
		entries,
	)
}

func assertURIValidating(entries ...TableEntry) {
	DescribeTable("validating", Label("validating"),
		func(u uri.URI, expect bool) {
			Expect(utils.IsValid(u)).To(Equal(expect))
		},
		EntryDescription("%[1]q"),
		entries,
	)
}
