package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type Supported Require

func (Supported) CanonicName() Name { return "Supported" }

func (hdr Supported) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return Require(hdr).renderValue(w)
}

func (hdr Supported) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Supported) String() string { return Require(hdr).String() }

func (hdr Supported) Clone() Header {
	return Supported(Require(hdr).Clone().(Require)) //nolint:forcetypeassert
}

func (hdr Supported) Equal(val any) bool {
	var other Supported
	switch v := val.(type) {
	case Supported:
		other = v
	case *Supported:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return Require(hdr).Equal(Require(other))
}

func (hdr Supported) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(s string) bool { return !grammar.IsToken(s) })
}

func buildFromSupportedNode(node *abnf.Node) Supported {
	return Supported(buildFromRequireNode(node))
}
