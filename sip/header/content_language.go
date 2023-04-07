package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type ContentLanguage []string

func (hdr ContentLanguage) HeaderName() string { return "Content-Language" }

func (hdr ContentLanguage) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr ContentLanguage) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr ContentLanguage) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr ContentLanguage) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	hdr.renderValue(sb)
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
	return slices.EqualFunc(hdr, other, func(lang1, lang2 string) bool { return utils.LCase(lang1) == utils.LCase(lang2) })
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
