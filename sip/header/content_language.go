package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

type ContentLanguage []Language

func (ContentLanguage) CanonicName() Name { return "Content-Language" }

func (ContentLanguage) CompactName() Name { return "Content-Language" }

func (hdr ContentLanguage) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr ContentLanguage) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTokensTo(w, hdr))
}

func (hdr ContentLanguage) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr ContentLanguage) String() string {
	return hdr.RenderValue()
}

func (hdr ContentLanguage) RenderValue() string {
	return renderHdrVal(hdr)
}

func (hdr ContentLanguage) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods     ContentLanguage
		ContentLanguage hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), ContentLanguage(hdr))
}

func (hdr ContentLanguage) Clone() Header {
	return slices.Clone(hdr)
}

func (hdr ContentLanguage) Equal(val any) bool {
	return util.EqualValue(val, func(other ContentLanguage) bool {
		return slices.EqualFunc(hdr, other, func(lang1, lang2 Language) bool {
			return lang1.Equal(lang2)
		})
	})
}

func (hdr ContentLanguage) IsValid() bool {
	return len(hdr) > 0 &&
		!slices.ContainsFunc(hdr, func(lang Language) bool {
			return !lang.IsValid()
		})
}

func (hdr ContentLanguage) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ContentLanguage) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromContentLanguageNode(node *abnf.Node) ContentLanguage {
	langNodes := node.GetNodes("language-tag")
	h := make(ContentLanguage, len(langNodes))
	for i, langNode := range langNodes {
		h[i] = Language(langNode.String())
	}
	return h
}

type Language string

func (lng Language) IsValid() bool {
	return grammar.IsToken(lng)
}

func (lng Language) Equal(val any) bool {
	return util.EqualValue(val, func(other Language) bool {
		return util.EqualFold(lng, other)
	})
}
