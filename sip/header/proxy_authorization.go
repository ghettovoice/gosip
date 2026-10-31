package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

type ProxyAuthorization struct {
	AuthCredentials
}

func (*ProxyAuthorization) CanonicName() Name { return "Proxy-Authorization" }

func (*ProxyAuthorization) CompactName() Name { return "Proxy-Authorization" }

func (hdr *ProxyAuthorization) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderValueTo(w, hdr.AuthCredentials, opts...))
	}))
}

func (hdr *ProxyAuthorization) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

func (hdr *ProxyAuthorization) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderValue(hdr.AuthCredentials)
}

func (hdr *ProxyAuthorization) String() string { return hdr.RenderValue() }

func (hdr *ProxyAuthorization) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods        ProxyAuthorization
		ProxyAuthorization hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*ProxyAuthorization)(hdr))
}

func (hdr *ProxyAuthorization) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	hdr2.AuthCredentials = types.Clone[AuthCredentials](hdr.AuthCredentials)
	return &hdr2
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

	if hdr == other {
		return true
	}
	if hdr == nil || other == nil {
		return false
	}
	return types.IsEqual(hdr.AuthCredentials, other.AuthCredentials)
}

func (hdr *ProxyAuthorization) IsValid() bool {
	return hdr != nil && types.IsValid(hdr.AuthCredentials)
}

func (hdr *ProxyAuthorization) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ProxyAuthorization) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyHdrValue))
}

func buildFromProxyAuthorizationNode(node *abnf.Node) *ProxyAuthorization {
	return &ProxyAuthorization{AuthCredentials: buildAuthCredentialsFromNode(node)}
}
