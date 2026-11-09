package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
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
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *ProxyAuthorization) renderValTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderTo(w, hdr.AuthCredentials, opts...))
}

func (hdr *ProxyAuthorization) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *ProxyAuthorization) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *ProxyAuthorization) String() string {
	return hdr.RenderValue()
}

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
	clone := *hdr
	clone.AuthCredentials = shared.Clone[AuthCredentials](hdr.AuthCredentials)
	return &clone
}

func (hdr *ProxyAuthorization) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *ProxyAuthorization) bool {
		return shared.IsEqual(hdr.AuthCredentials, other.AuthCredentials)
	})
}

func (hdr *ProxyAuthorization) IsValid() bool {
	return hdr != nil && shared.IsValid(hdr.AuthCredentials)
}

func (hdr *ProxyAuthorization) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ProxyAuthorization) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromProxyAuthorizationNode(node *abnf.Node) *ProxyAuthorization {
	return &ProxyAuthorization{AuthCredentials: buildAuthCredentialsFromNode(node)}
}
