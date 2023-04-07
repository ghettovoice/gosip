package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type ProxyAuthorization Authorization

func (hdr *ProxyAuthorization) HeaderName() string { return "Proxy-Authorization" }

func (hdr *ProxyAuthorization) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return (*Authorization)(hdr).renderValue(w)
}

func (hdr *ProxyAuthorization) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *ProxyAuthorization) String() string {
	if hdr == nil {
		return "<nil>"
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	(*Authorization)(hdr).renderValue(sb)
	return sb.String()
}

func (hdr *ProxyAuthorization) Clone() Header {
	if hdr == nil {
		return nil
	}
	return (*ProxyAuthorization)((*Authorization)(hdr).Clone().(*Authorization))
}

func (hdr *ProxyAuthorization) Equal(val any) bool {
	var other *ProxyAuthorization
	switch v := val.(type) {
	case ProxyAuthorization:
		other = &v
	case *ProxyAuthorization:
		other = v
	default:
		return false
	}
	return (*Authorization)(hdr).Equal((*Authorization)(other))
}

func (hdr *ProxyAuthorization) IsValid() bool { return (*Authorization)(hdr).IsValid() }

func buildFromProxyAuthorizationNode(node *abnf.Node) *ProxyAuthorization {
	return (*ProxyAuthorization)(buildFromAuthorizationNode(node))
}
