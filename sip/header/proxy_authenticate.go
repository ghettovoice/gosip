package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type ProxyAuthenticate WWWAuthenticate

func (*ProxyAuthenticate) CanonicName() Name { return "Proxy-Authenticate" }

func (*ProxyAuthenticate) CompactName() Name { return "Proxy-Authenticate" }

func (hdr *ProxyAuthenticate) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2((*WWWAuthenticate)(hdr).renderValueTo(w, opts))
	})

	return errors.Wrap2(cw.Result())
}

func (hdr *ProxyAuthenticate) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

func (hdr *ProxyAuthenticate) RenderValue() string {
	return (*WWWAuthenticate)(hdr).RenderValue()
}

func (hdr *ProxyAuthenticate) String() string { return hdr.RenderValue() }

func (hdr *ProxyAuthenticate) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}

		fmt.Fprint(f, hdr.String())

		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}

		fmt.Fprint(f, strconv.Quote(hdr.String()))

		return
	default:
		type (
			hideMethods       ProxyAuthenticate
			ProxyAuthenticate hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), (*ProxyAuthenticate)(hdr))

		return
	}
}

func (hdr *ProxyAuthenticate) Clone() Header {
	hdr2, ok := (*WWWAuthenticate)(hdr).Clone().(*WWWAuthenticate)
	if !ok {
		return nil
	}

	return (*ProxyAuthenticate)(hdr2)
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

func (hdr *ProxyAuthenticate) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ProxyAuthenticate) UnmarshalJSON(data []byte) error {
	if hdr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil header")
	}

	gh, err := FromJSON(data)
	if gh == nil {
		*hdr = ProxyAuthenticate{}
		return errors.Wrap(err)
	}

	h, ok := gh.(*ProxyAuthenticate)
	if !ok {
		*hdr = ProxyAuthenticate{}

		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && len(ah.Value) == 0 {
			return nil
		}

		return newUnexpectHdrTypeErrWrap(gh)
	}

	*hdr = *h

	return nil
}

func buildFromProxyAuthenticateNode(node *abnf.Node) *ProxyAuthenticate {
	return (*ProxyAuthenticate)(buildFromWWWAuthenticateNode(node))
}
