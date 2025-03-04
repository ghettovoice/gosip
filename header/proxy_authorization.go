package header

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type ProxyAuthorization Authorization

func (*ProxyAuthorization) CanonicName() Name { return "Proxy-Authorization" }

func (*ProxyAuthorization) CompactName() Name { return "Proxy-Authorization" }

func (hdr *ProxyAuthorization) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(func(w io.Writer) (int, error) {
		return errtrace.Wrap2((*Authorization)(hdr).renderValueTo(w, opts))
	})
	return errtrace.Wrap2(cw.Result())
}

func (hdr *ProxyAuthorization) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr *ProxyAuthorization) RenderValue() string {
	return (*Authorization)(hdr).RenderValue()
}

func (hdr *ProxyAuthorization) String() string { return hdr.RenderValue() }

func (hdr *ProxyAuthorization) Format(f fmt.State, verb rune) {
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
		type hideMethods ProxyAuthorization
		type ProxyAuthorization hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*ProxyAuthorization)(hdr))
		return
	}
}

func (hdr *ProxyAuthorization) Clone() Header {
	hdr2, ok := (*Authorization)(hdr).Clone().(*Authorization)
	if !ok {
		return nil
	}
	return (*ProxyAuthorization)(hdr2)
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

func (hdr *ProxyAuthorization) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

var zeroProxyAuthorization ProxyAuthorization

func (hdr *ProxyAuthorization) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = zeroProxyAuthorization
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(*ProxyAuthorization)
	if !ok {
		*hdr = zeroProxyAuthorization
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, hdr))
	}

	*hdr = *h
	return nil
}

func buildFromProxyAuthorizationNode(node *abnf.Node) *ProxyAuthorization {
	return (*ProxyAuthorization)(buildFromAuthorizationNode(node))
}
