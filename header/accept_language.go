package header

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type AcceptLanguage []LanguageRange

func (AcceptLanguage) CanonicName() Name { return "Accept-Language" }

func (AcceptLanguage) CompactName() Name { return "Accept-Language" }

func (hdr AcceptLanguage) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr AcceptLanguage) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

func (hdr AcceptLanguage) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr AcceptLanguage) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

func (hdr AcceptLanguage) String() string { return hdr.RenderValue() }

func (hdr AcceptLanguage) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, hdr.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(hdr.String()))
		return
	default:
		type hideMethods AcceptLanguage
		type AcceptLanguage hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), AcceptLanguage(hdr))
		return
	}
}

func (hdr AcceptLanguage) Clone() Header { return cloneHdrEntries(hdr) }

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

func (hdr AcceptLanguage) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *AcceptLanguage) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(AcceptLanguage)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromAcceptLanguageNode(node *abnf.Node) AcceptLanguage {
	rngNodes := node.GetNodes("language")
	hdr := make(AcceptLanguage, len(rngNodes))
	for i, rngNode := range rngNodes {
		hdr[i] = LanguageRange{
			Lang:   Language(grammar.MustGetNode(rngNode, "language-range").String()),
			Params: buildFromHeaderParamNodes(rngNode.GetNodes("accept-param"), nil),
		}
	}
	return hdr
}

type LanguageRange struct {
	Lang   Language
	Params Values
}

func (rng LanguageRange) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	sb.WriteString(string(rng.Lang))
	renderHdrParams(sb, rng.Params, false) //nolint:errcheck
	return sb.String()
}

func (rng LanguageRange) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		fmt.Fprint(f, rng.String())
		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(rng.String()))
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			fmt.Fprint(f, rng.String())
			return
		}

		type hideMethods LanguageRange
		type LanguageRange hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), LanguageRange(rng))
		return
	}
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
	return rng.Lang.Equal(other.Lang) &&
		compareHdrParams(rng.Params, other.Params, map[string]bool{"q": true})
}

func (rng LanguageRange) IsValid() bool {
	return rng.Lang.IsValid() && validateHdrParams(rng.Params)
}

func (rng LanguageRange) IsZero() bool { return rng.Lang == "" && len(rng.Params) == 0 }

func (rng LanguageRange) Clone() LanguageRange {
	rng.Params = rng.Params.Clone()
	return rng
}

func (rng LanguageRange) MarshalText() ([]byte, error) {
	return []byte(rng.String()), nil
}

func (rng *LanguageRange) UnmarshalText(data []byte) error {
	node, err := grammar.ParseLanguage(data)
	if err != nil {
		*rng = LanguageRange{}
		if errors.Is(err, grammar.ErrEmptyInput) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	rng.Lang = Language(grammar.MustGetNode(node, "language-range").String())
	rng.Params = buildFromHeaderParamNodes(node.GetNodes("accept-param"), nil)
	return nil
}
