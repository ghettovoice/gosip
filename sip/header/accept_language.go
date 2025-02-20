package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type AcceptLanguage []LanguageRange

func (AcceptLanguage) CanonicName() Name { return "Accept-Language" }

func (hdr AcceptLanguage) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr AcceptLanguage) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr AcceptLanguage) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr AcceptLanguage) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr AcceptLanguage) Clone() Header { return cloneHeaderEntries(hdr) }

func (hdr AcceptLanguage) Equal(val any) bool {
	var other AcceptLanguage
	switch v := val.(type) {
	case AcceptLanguage:
		other = v
	case *AcceptLanguage:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(rng1, rng2 LanguageRange) bool { return rng1.Equal(rng2) })
}

func (hdr AcceptLanguage) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(rng LanguageRange) bool { return !rng.IsValid() })
}

func buildFromAcceptLanguageNode(node *abnf.Node) AcceptLanguage {
	rngNodes := node.GetNodes("language")
	hdr := make(AcceptLanguage, len(rngNodes))
	for i, rngNode := range rngNodes {
		hdr[i] = LanguageRange{
			Lang:   abnfutils.MustGetNode(rngNode, "language-range").String(),
			Params: buildFromHeaderParamNodes(rngNode.GetNodes("accept-param"), nil),
		}
	}
	return hdr
}

type LanguageRange struct {
	Lang   string
	Params Values
}

func (rng LanguageRange) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteString(rng.Lang)
	_ = renderHeaderParams(sb, rng.Params, false)
	return sb.String()
}

func (rng LanguageRange) Equal(val any) bool {
	var other LanguageRange
	switch v := val.(type) {
	case LanguageRange:
		other = v
	case *LanguageRange:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return stringutils.LCase(rng.Lang) == stringutils.LCase(other.Lang) && compareHeaderParams(rng.Params, other.Params, map[string]bool{"q": true})
}

func (rng LanguageRange) IsValid() bool {
	return grammar.IsToken(rng.Lang) && validateHeaderParams(rng.Params)
}

func (rng LanguageRange) IsZero() bool { return rng.Lang == "" && len(rng.Params) == 0 }

func (rng LanguageRange) Clone() LanguageRange {
	rng.Params = rng.Params.Clone()
	return rng
}
