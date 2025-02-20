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

type AcceptEncoding []EncodingRange

func (AcceptEncoding) CanonicName() Name { return "Accept-Encoding" }

func (hdr AcceptEncoding) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr AcceptEncoding) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr AcceptEncoding) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr AcceptEncoding) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr AcceptEncoding) Clone() Header { return cloneHeaderEntries(hdr) }

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

func buildFromAcceptEncodingNode(node *abnf.Node) AcceptEncoding {
	rngNodes := node.GetNodes("encoding")
	hdr := make(AcceptEncoding, len(rngNodes))
	for i, rngNode := range rngNodes {
		hdr[i] = EncodingRange{
			Encoding: abnfutils.MustGetNode(rngNode, "codings").String(),
			Params:   buildFromHeaderParamNodes(rngNode.GetNodes("accept-param"), nil),
		}
	}
	return hdr
}

type EncodingRange struct {
	Encoding string
	Params   Values
}

func (rng EncodingRange) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteString(rng.Encoding)
	_ = renderHeaderParams(sb, rng.Params, false)
	return sb.String()
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
	return stringutils.LCase(rng.Encoding) == stringutils.LCase(other.Encoding) && compareHeaderParams(rng.Params, other.Params, map[string]bool{"q": true})
}

func (rng EncodingRange) IsValid() bool {
	return grammar.IsToken(rng.Encoding) && validateHeaderParams(rng.Params)
}

func (rng EncodingRange) IsZero() bool { return rng.Encoding == "" && len(rng.Params) == 0 }

func (rng EncodingRange) Clone() EncodingRange {
	rng.Params = rng.Params.Clone()
	return rng
}
