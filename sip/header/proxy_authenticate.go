package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type ProxyAuthenticate WWWAuthenticate

func (hdr *ProxyAuthenticate) HeaderName() string { return "Proxy-Authenticate" }

func (hdr *ProxyAuthenticate) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return (*WWWAuthenticate)(hdr).renderValue(w)
}

func (hdr *ProxyAuthenticate) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *ProxyAuthenticate) String() string {
	if hdr == nil {
		return "<nil>"
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	(*WWWAuthenticate)(hdr).renderValue(sb)
	return sb.String()
}

func (hdr *ProxyAuthenticate) Clone() Header {
	if hdr == nil {
		return nil
	}
	return (*ProxyAuthenticate)((*WWWAuthenticate)(hdr).Clone().(*WWWAuthenticate))
}

func (hdr *ProxyAuthenticate) Equal(val any) bool {
	var other *ProxyAuthenticate
	switch v := val.(type) {
	case ProxyAuthenticate:
		other = &v
	case *ProxyAuthenticate:
		other = v
	default:
		return false
	}
	return (*WWWAuthenticate)(hdr).Equal((*WWWAuthenticate)(other))
}

func (hdr *ProxyAuthenticate) IsValid() bool { return (*WWWAuthenticate)(hdr).IsValid() }

func buildFromProxyAuthenticateNode(node *abnf.Node) *ProxyAuthenticate {
	return (*ProxyAuthenticate)(buildFromWWWAuthenticateNode(node))
}
