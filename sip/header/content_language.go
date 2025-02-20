package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type ContentLanguage []string

func (ContentLanguage) CanonicName() Name { return "Content-Language" }

func (hdr ContentLanguage) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr ContentLanguage) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr ContentLanguage) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr ContentLanguage) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr ContentLanguage) Clone() Header { return slices.Clone(hdr) }

func (hdr ContentLanguage) Equal(val any) bool {
	var other ContentLanguage
	switch v := val.(type) {
	case ContentLanguage:
		other = v
	case *ContentLanguage:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(lang1, lang2 string) bool { return stringutils.LCase(lang1) == stringutils.LCase(lang2) })
}

func (hdr ContentLanguage) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(lang string) bool { return !grammar.IsToken(lang) })
}

func buildFromContentLanguageNode(node *abnf.Node) ContentLanguage {
	langNodes := node.GetNodes("language-tag")
	h := make(ContentLanguage, len(langNodes))
	for i, langNode := range langNodes {
		h[i] = langNode.String()
	}
	return h
}
