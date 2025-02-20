package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type ProxyAuthorization Authorization

func (*ProxyAuthorization) CanonicName() Name { return "Proxy-Authorization" }

func (hdr *ProxyAuthorization) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return (*Authorization)(hdr).renderValue(w)
}

func (hdr *ProxyAuthorization) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *ProxyAuthorization) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = (*Authorization)(hdr).renderValue(sb)
	return sb.String()
}

func (hdr *ProxyAuthorization) Clone() Header {
	if hdr == nil {
		return nil
	}
	return (*ProxyAuthorization)((*Authorization)(hdr).Clone().(*Authorization)) //nolint:forcetypeassert
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
