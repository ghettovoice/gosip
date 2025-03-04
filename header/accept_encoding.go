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

type AcceptEncoding []EncodingRange

func (AcceptEncoding) CanonicName() Name { return "Accept-Encoding" }

func (AcceptEncoding) CompactName() Name { return "Accept-Encoding" }

func (hdr AcceptEncoding) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr AcceptEncoding) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

func (hdr AcceptEncoding) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr AcceptEncoding) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

func (hdr AcceptEncoding) String() string { return hdr.RenderValue() }

func (hdr AcceptEncoding) Format(f fmt.State, verb rune) {
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
		type hideMethods AcceptEncoding
		type AcceptEncoding hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), AcceptEncoding(hdr))
		return
	}
}

func (hdr AcceptEncoding) Clone() Header { return cloneHdrEntries(hdr) }

func (hdr AcceptEncoding) Equal(val any) bool {
	var other AcceptEncoding
	switch v := val.(type) {
	case AcceptEncoding:
		other = v
	case *AcceptEncoding:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(rng1, rng2 EncodingRange) bool { return rng1.Equal(rng2) })
}

func (hdr AcceptEncoding) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(rng EncodingRange) bool { return !rng.IsValid() })
}

func (hdr AcceptEncoding) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *AcceptEncoding) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(AcceptEncoding)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromAcceptEncodingNode(node *abnf.Node) AcceptEncoding {
	rngNodes := node.GetNodes("encoding")
	hdr := make(AcceptEncoding, len(rngNodes))
	for i, rngNode := range rngNodes {
		hdr[i] = EncodingRange{
			Encoding: Encoding(grammar.MustGetNode(rngNode, "codings").String()),
			Params:   buildFromHeaderParamNodes(rngNode.GetNodes("accept-param"), nil),
		}
	}
	return hdr
}

type EncodingRange struct {
	Encoding Encoding
	Params   Values
}

func (rng EncodingRange) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	sb.WriteString(string(rng.Encoding))
	renderHdrParams(sb, rng.Params, false) //nolint:errcheck
	return sb.String()
}

func (rng EncodingRange) Format(f fmt.State, verb rune) {
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

		type hideMethods EncodingRange
		type EncodingRange hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), EncodingRange(rng))
		return
	}
}

func (rng EncodingRange) Equal(val any) bool {
	var other EncodingRange
	switch v := val.(type) {
	case EncodingRange:
		other = v
	case *EncodingRange:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return rng.Encoding.Equal(other.Encoding) &&
		compareHdrParams(rng.Params, other.Params, map[string]bool{"q": true})
}

func (rng EncodingRange) IsValid() bool {
	return rng.Encoding.IsValid() &&
		validateHdrParams(rng.Params)
}

func (rng EncodingRange) IsZero() bool { return rng.Encoding == "" && len(rng.Params) == 0 }

func (rng EncodingRange) Clone() EncodingRange {
	rng.Params = rng.Params.Clone()
	return rng
}

func (rng EncodingRange) MarshalText() ([]byte, error) {
	return []byte(rng.String()), nil
}

func (rng *EncodingRange) UnmarshalText(data []byte) error {
	node, err := grammar.ParseEncoding(data)
	if err != nil {
		*rng = EncodingRange{}
		if errors.Is(err, grammar.ErrEmptyInput) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	rng.Encoding = Encoding(grammar.MustGetNode(node, "codings").String())
	rng.Params = buildFromHeaderParamNodes(node.GetNodes("accept-param"), nil)
	return nil
}
