package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type ProxyRequire Require

func (ProxyRequire) CanonicName() Name { return "Proxy-Require" }

func (hdr ProxyRequire) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return Require(hdr).renderValue(w)
}

func (hdr ProxyRequire) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr ProxyRequire) String() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	sb.WriteByte('[')
	_ = Require(hdr).renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr ProxyRequire) Clone() Header {
	return ProxyRequire(Require(hdr).Clone().(Require)) //nolint:forcetypeassert
}

func (hdr ProxyRequire) Equal(val any) bool {
	var other ProxyRequire
	switch v := val.(type) {
	case ProxyRequire:
		other = v
	case *ProxyRequire:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return Require(hdr).Equal(Require(other))
}

func (hdr ProxyRequire) IsValid() bool { return Require(hdr).IsValid() }

func buildFromProxyRequireNode(node *abnf.Node) ProxyRequire {
	return ProxyRequire(buildFromRequireNode(node))
}
