package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type Supported Require

func (hdr Supported) HeaderName() string { return "Supported" }

func (hdr Supported) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return Require(hdr).renderValue(w)
}

func (hdr Supported) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr Supported) String() string { return Require(hdr).String() }

func (hdr Supported) Clone() Header {
	return Supported(Require(hdr).Clone().(Require))
}

func (hdr Supported) Equal(val any) bool {
	var other Supported
	switch v := val.(type) {
	case Supported:
		other = v
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
