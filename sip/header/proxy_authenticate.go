package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type ProxyAuthenticate WWWAuthenticate

func (*ProxyAuthenticate) CanonicName() Name { return "Proxy-Authenticate" }

func (hdr *ProxyAuthenticate) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return (*WWWAuthenticate)(hdr).renderValue(w)
}

func (hdr *ProxyAuthenticate) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *ProxyAuthenticate) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = (*WWWAuthenticate)(hdr).renderValue(sb)
	return sb.String()
}

func (hdr *ProxyAuthenticate) Clone() Header {
	if hdr == nil {
		return nil
	}
	return (*ProxyAuthenticate)((*WWWAuthenticate)(hdr).Clone().(*WWWAuthenticate)) //nolint:forcetypeassert
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
