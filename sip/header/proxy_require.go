package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type ProxyRequire Require

func (hdr ProxyRequire) HeaderName() string { return "Proxy-Require" }

func (hdr ProxyRequire) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return Require(hdr).renderValue(w)
}

func (hdr ProxyRequire) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr ProxyRequire) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	Require(hdr).renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr ProxyRequire) Clone() Header {
	return ProxyRequire(Require(hdr).Clone().(Require))
}

func (hdr ProxyRequire) Equal(val any) bool {
	var other ProxyRequire
	switch v := val.(type) {
	case ProxyRequire:
		other = v
	default:
		return false
	}
	return Require(hdr).Equal(Require(other))
}

func (hdr ProxyRequire) IsValid() bool { return Require(hdr).IsValid() }

func buildFromProxyRequireNode(node *abnf.Node) ProxyRequire {
	return ProxyRequire(buildFromRequireNode(node))
}
