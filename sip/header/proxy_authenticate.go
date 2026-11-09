package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
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
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *ProxyAuthenticate) renderValTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderTo(w, hdr.AuthChallenge, opts...))
}

func (hdr *ProxyAuthenticate) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *ProxyAuthenticate) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *ProxyAuthenticate) String() string {
	return hdr.RenderValue()
}

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
	clone := *hdr
	clone.AuthChallenge = shared.Clone[AuthChallenge](hdr.AuthChallenge)
	return &clone
}

func (hdr *ProxyAuthenticate) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *ProxyAuthenticate) bool {
		return shared.IsEqual(hdr.AuthChallenge, other.AuthChallenge)
	})
}

func (hdr *ProxyAuthenticate) IsValid() bool {
	return hdr != nil && shared.IsValid(hdr.AuthChallenge)
}

func (hdr *ProxyAuthenticate) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ProxyAuthenticate) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromProxyAuthenticateNode(node *abnf.Node) *ProxyAuthenticate {
	return &ProxyAuthenticate{AuthChallenge: buildAuthChallengeFromNode(node)}
}
