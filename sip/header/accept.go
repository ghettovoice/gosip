package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
)

type Accept []MIMERange

func (Accept) CanonicName() Name { return "Accept" }

func (hdr Accept) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr Accept) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr Accept) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Accept) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr Accept) Clone() Header { return cloneHeaderEntries(hdr) }

func (hdr Accept) Equal(val any) bool {
	var other Accept
	switch v := val.(type) {
	case Accept:
		other = v
	case *Accept:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(rng1, rng2 MIMERange) bool { return rng1.Equal(rng2) })
}

func (hdr Accept) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(rng MIMERange) bool { return !rng.IsValid() })
}

func buildFromAcceptNode(node *abnf.Node) Accept {
	rngNodes := node.GetNodes("accept-range")
	hdr := make(Accept, 0, len(rngNodes))
	for _, rngNode := range rngNodes {
		mt, ps := buildFromMIMETypeNode(abnfutils.MustGetNode(rngNode, "media-range"))
		rng := MIMERange{MIMEType: mt}
		if len(ps) > 0 {
			rng.Params = make(Values, len(ps))
			for _, kv := range ps {
				rng.Params.Append(kv[0], kv[1])
			}
		}
		rng.Params = buildFromHeaderParamNodes(rngNode.GetNodes("accept-param"), rng.Params)
		hdr = append(hdr, rng)
	}
	return hdr
}

type MIMERange struct {
	MIMEType
	Params Values
}

func (rng MIMERange) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteString(rng.MIMEType.String())
	_ = renderHeaderParams(sb, rng.Params, len(rng.MIMEType.Params) > 0)
	return sb.String()
}

func (rng MIMERange) Equal(val any) bool {
	var other MIMERange
	switch v := val.(type) {
	case MIMERange:
		other = v
	case *MIMERange:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return rng.MIMEType.Equal(other.MIMEType) && compareHeaderParams(rng.Params, other.Params, map[string]bool{"q": true})
}

func (rng MIMERange) IsValid() bool {
	return rng.MIMEType.IsValid() && validateHeaderParams(rng.Params)
}

func (rng MIMERange) IsZero() bool {
	return rng.MIMEType.IsZero() && len(rng.Params) == 0
}

func (rng MIMERange) Clone() MIMERange {
	rng.MIMEType = rng.MIMEType.Clone()
	rng.Params = rng.Params.Clone()
	return rng
}
