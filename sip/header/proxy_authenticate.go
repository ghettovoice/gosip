package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

type ProxyAuthenticate struct {
	AuthChallenge
}

func (*ProxyAuthenticate) CanonicName() Name { return "Proxy-Authenticate" }

func (*ProxyAuthenticate) CompactName() Name { return "Proxy-Authenticate" }

func (hdr *ProxyAuthenticate) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderValueTo(w, hdr.AuthChallenge, opts...))
	}))
}

func (hdr *ProxyAuthenticate) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

func (hdr *ProxyAuthenticate) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderValue(hdr.AuthChallenge)
}

func (hdr *ProxyAuthenticate) String() string { return hdr.RenderValue() }

func (hdr *ProxyAuthenticate) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods       ProxyAuthenticate
		ProxyAuthenticate hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*ProxyAuthenticate)(hdr))
}

func (hdr *ProxyAuthenticate) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	hdr2.AuthChallenge = types.Clone[AuthChallenge](hdr.AuthChallenge)
	return &hdr2
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

	if hdr == other {
		return true
	}
	if hdr == nil || other == nil {
		return false
	}
	return types.IsEqual(hdr.AuthChallenge, other.AuthChallenge)
}

func (hdr *ProxyAuthenticate) IsValid() bool {
	return hdr != nil && types.IsValid(hdr.AuthChallenge)
}

func (hdr *ProxyAuthenticate) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ProxyAuthenticate) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyHdrValue))
}

func buildFromProxyAuthenticateNode(node *abnf.Node) *ProxyAuthenticate {
	return &ProxyAuthenticate{AuthChallenge: buildAuthChallengeFromNode(node)}
}
