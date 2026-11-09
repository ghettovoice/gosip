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

type AcceptEncoding []EncodingRange

func (AcceptEncoding) CanonicName() Name { return "Accept-Encoding" }

func (AcceptEncoding) CompactName() Name { return "Accept-Encoding" }

func (hdr AcceptEncoding) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr AcceptEncoding) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrValsTo(w, hdr))
}

func (hdr AcceptEncoding) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr AcceptEncoding) RenderValue() string {
	return renderHdrVal(hdr)
}

func (hdr AcceptEncoding) String() string {
	return hdr.RenderValue()
}

func (hdr AcceptEncoding) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods    AcceptEncoding
		AcceptEncoding hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), AcceptEncoding(hdr))
}

func (hdr AcceptEncoding) Clone() Header {
	return cloneHdrVals(hdr)
}

func (hdr AcceptEncoding) Equal(val any) bool {
	return util.EqualValue(val, func(other AcceptEncoding) bool {
		return slices.EqualFunc(hdr, other, func(rng1, rng2 EncodingRange) bool {
			return rng1.Equal(rng2)
		})
	})
}

func (hdr AcceptEncoding) IsValid() bool {
	return hdr != nil &&
		!slices.ContainsFunc(hdr, func(rng EncodingRange) bool {
			return !rng.IsValid()
		})
}

func (hdr AcceptEncoding) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *AcceptEncoding) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), nil))
}

func buildFromAcceptEncodingNode(node *abnf.Node) AcceptEncoding {
	rngNodes := node.GetNodes("encoding")
	hdr := make(AcceptEncoding, len(rngNodes))
	for i, rngNode := range rngNodes {
		hdr[i] = EncodingRange{
			Encoding: Encoding(grammar.MustGetNode(rngNode, "codings").String()),
			Params:   buildFromHdrParamNodes(rngNode.GetNodes("accept-param"), nil),
		}
	}
	return hdr
}

type EncodingRange struct {
	Encoding Encoding
	Params   Values
}

func (rng EncodingRange) String() string {
	return renderVal(rng)
}

func (rng EncodingRange) renderTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.WriteString(string(rng.Encoding))
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrParamsTo(w, rng.Params, false))
	})
	return errors.Wrap2(cw.Result())
}

func (rng EncodingRange) Format(f fmt.State, verb rune) {
	if formatVal(f, verb, rng) {
		return
	}
	type (
		hideMethods   EncodingRange
		EncodingRange hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), EncodingRange(rng))
}

func (rng EncodingRange) Equal(val any) bool {
	return util.EqualValue(val, func(other EncodingRange) bool {
		return rng.Encoding.Equal(other.Encoding) &&
			compareHdrParams(rng.Params, other.Params, map[string]bool{
				"q": true,
			})
	})
}

func (rng EncodingRange) IsValid() bool {
	return rng.Encoding.IsValid() && validateHdrParams(rng.Params)
}

func (rng EncodingRange) IsZero() bool {
	return rng.Encoding == "" && len(rng.Params) == 0
}

func (rng EncodingRange) Clone() EncodingRange {
	rng.Params = rng.Params.Clone()
	return rng
}

func (rng EncodingRange) MarshalText() ([]byte, error) {
	return []byte(rng.String()), nil
}

func (rng EncodingRange) AppendText(data []byte) ([]byte, error) {
	return append(data, rng.String()...), nil
}

func (rng *EncodingRange) UnmarshalText(data []byte) (finErr error) {
	if len(data) == 0 {
		*rng = EncodingRange{}
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

	node, err := grammar.ParseEncoding(data)
	if err != nil {
		return errors.Wrap(err)
	}

	rng.Encoding = Encoding(grammar.MustGetNode(node, "codings").String())
	rng.Params = buildFromHdrParamNodes(node.GetNodes("accept-param"), nil)
	return nil
}
