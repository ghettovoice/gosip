package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type AcceptLanguage []LanguageRange

func (AcceptLanguage) CanonicName() Name { return "Accept-Language" }

func (AcceptLanguage) CompactName() Name { return "Accept-Language" }

func (hdr AcceptLanguage) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr AcceptLanguage) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrValsTo(w, hdr))
}

func (hdr AcceptLanguage) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr AcceptLanguage) RenderValue() string {
	return renderHdrVal(hdr)
}

func (hdr AcceptLanguage) String() string {
	return hdr.RenderValue()
}

func (hdr AcceptLanguage) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods    AcceptLanguage
		AcceptLanguage hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), AcceptLanguage(hdr))
}

func (hdr AcceptLanguage) Clone() Header {
	return cloneHdrVals(hdr)
}

func (hdr AcceptLanguage) Equal(val any) bool {
	return util.EqualValue(val, func(other AcceptLanguage) bool {
		return slices.EqualFunc(hdr, other, func(rng1, rng2 LanguageRange) bool {
			return rng1.Equal(rng2)
		})
	})
}

func (hdr AcceptLanguage) IsValid() bool {
	return hdr != nil &&
		!slices.ContainsFunc(hdr, func(rng LanguageRange) bool {
			return !rng.IsValid()
		})
}

func (hdr AcceptLanguage) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *AcceptLanguage) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), nil))
}

func buildFromAcceptLanguageNode(node *abnf.Node) AcceptLanguage {
	rngNodes := node.GetNodes("language")
	hdr := make(AcceptLanguage, len(rngNodes))
	for i, rngNode := range rngNodes {
		hdr[i] = LanguageRange{
			Lang:   Language(grammar.MustGetNode(rngNode, "language-range").String()),
			Params: buildFromHdrParamNodes(rngNode.GetNodes("accept-param"), nil),
		}
	}
	return hdr
}

type LanguageRange struct {
	Lang   Language
	Params Values
}

func (rng LanguageRange) String() string {
	return renderVal(rng)
}

func (rng LanguageRange) renderTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.WriteString(string(rng.Lang))
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrParamsTo(w, rng.Params, false))
	})
	return errors.Wrap2(cw.Result())
}

func (rng LanguageRange) Format(f fmt.State, verb rune) {
	if formatVal(f, verb, rng) {
		return
	}
	type (
		hideMethods   LanguageRange
		LanguageRange hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), LanguageRange(rng))
}

func (rng LanguageRange) Equal(val any) bool {
	return util.EqualValue(val, func(other LanguageRange) bool {
		return rng.Lang.Equal(other.Lang) &&
			compareHdrParams(rng.Params, other.Params, map[string]bool{
				"q": true,
			})
	})
}

func (rng LanguageRange) IsValid() bool {
	return rng.Lang.IsValid() && validateHdrParams(rng.Params)
}

func (rng LanguageRange) IsZero() bool {
	return rng.Lang == "" && len(rng.Params) == 0
}

func (rng LanguageRange) Clone() LanguageRange {
	rng.Params = rng.Params.Clone()
	return rng
}

func (rng LanguageRange) MarshalText() ([]byte, error) {
	return []byte(rng.String()), nil
}

func (rng LanguageRange) AppendText(data []byte) ([]byte, error) {
	return append(data, rng.String()...), nil
}

func (rng *LanguageRange) UnmarshalText(data []byte) (finErr error) {
	if len(data) == 0 {
		*rng = LanguageRange{}
		return nil
	}

	defer func() {
		if rv := recover(); rv != nil {
			if e, ok := rv.(error); ok {
				finErr = errors.Wrap(e)
			} else {
				finErr = errors.ErrorfWrap("%v", rv)
			}
		}
	}()

	node, err := grammar.ParseLanguage(data)
	if err != nil {
		return errors.Wrap(err)
	}

	rng.Lang = Language(grammar.MustGetNode(node, "language-range").String())
	rng.Params = buildFromHdrParamNodes(node.GetNodes("accept-param"), nil)
	return nil
}
