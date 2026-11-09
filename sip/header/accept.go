package header

import (
	"bytes"
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type Accept []MIMERange

func (Accept) CanonicName() Name { return "Accept" }

func (Accept) CompactName() Name { return "Accept" }

func (hdr Accept) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Accept) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrValsTo(w, hdr))
}

func (hdr Accept) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr Accept) RenderValue() string {
	return renderHdrVal(hdr)
}

func (hdr Accept) String() string {
	return hdr.RenderValue()
}

func (hdr Accept) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Accept
		Accept      hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Accept(hdr))
}

func (hdr Accept) Clone() Header {
	return cloneHdrVals(hdr)
}

func (hdr Accept) Equal(val any) bool {
	return util.EqualValue(val, func(other Accept) bool {
		return slices.EqualFunc(hdr, other, func(rng1, rng2 MIMERange) bool {
			return rng1.Equal(rng2)
		})
	})
}

func (hdr Accept) IsValid() bool {
	return hdr != nil &&
		!slices.ContainsFunc(hdr, func(rng MIMERange) bool {
			return !rng.IsValid()
		})
}

func (hdr Accept) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Accept) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), nil))
}

func buildFromAcceptNode(node *abnf.Node) Accept {
	rngNodes := node.GetNodes("accept-range")
	hdr := make(Accept, 0, len(rngNodes))
	for _, rngNode := range rngNodes {
		hdr = append(hdr, buildFromAcceptRangeNode(rngNode))
	}
	return hdr
}

type MIMERange struct {
	MIMEType
	Params Values
}

func (rng MIMERange) String() string {
	return renderVal(rng)
}

func (rng MIMERange) renderTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Call(rng.MIMEType.renderTo)
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrParamsTo(w, rng.Params, len(rng.MIMEType.Params) > 0))
	})
	return errors.Wrap2(cw.Result())
}

func (rng MIMERange) Format(f fmt.State, verb rune) {
	if formatVal(f, verb, rng) {
		return
	}
	type (
		hideMethods MIMERange
		MIMERange   hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), MIMERange(rng))
}

func (rng MIMERange) Equal(val any) bool {
	return util.EqualValue(val, func(other MIMERange) bool {
		return rng.MIMEType.Equal(other.MIMEType) &&
			compareHdrParams(rng.Params, other.Params, map[string]bool{
				"q": true,
			})
	})
}

func (rng MIMERange) IsValid() bool {
	return rng.MIMEType.IsValid() && validateHdrParams(rng.Params)
}

func (rng MIMERange) IsZero() bool {
	return rng.MIMEType.IsZero() && len(rng.Params) == 0
}

func (rng MIMERange) Clone() MIMERange {
	rng.MIMEType = rng.MIMEType.Clone()
	rng.Params = rng.Params.Clone()
	return rng
}

func (rng MIMERange) MarshalText() ([]byte, error) {
	return []byte(rng.String()), nil
}

func (rng MIMERange) AppendText(data []byte) ([]byte, error) {
	return append(data, rng.String()...), nil
}

func (rng *MIMERange) UnmarshalText(data []byte) (finErr error) {
	if len(data) == 0 || bytes.Equal(data, []byte("/")) {
		*rng = MIMERange{}
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

	node, err := grammar.ParseAcceptRange(data)
	if err != nil {
		return errors.Wrap(err)
	}

	*rng = buildFromAcceptRangeNode(node)
	return nil
}

func buildFromAcceptRangeNode(node *abnf.Node) MIMERange {
	mt, ps := buildFromMIMETypeNode(grammar.MustGetNode(node, "media-range"))
	rng := MIMERange{MIMEType: mt}
	if len(ps) > 0 {
		rng.Params = make(Values, len(ps))
		for _, kv := range ps {
			rng.Params.Append(kv[0], kv[1])
		}
	}
	rng.Params = buildFromHdrParamNodes(node.GetNodes("accept-param"), rng.Params)
	return rng
}
