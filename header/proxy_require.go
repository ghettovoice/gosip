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

// ProxyRequire represents the Proxy-Require header field.
// The Proxy-Require header field is used to indicate proxy-sensitive features that must be supported by the proxy.
type ProxyRequire Require

// CanonicName returns the canonical name of the header.
func (ProxyRequire) CanonicName() Name { return "Proxy-Require" }

// CompactName returns the compact name of the header (Proxy-Require has no compact form).
func (ProxyRequire) CompactName() Name { return "Proxy-Require" }

// RenderTo writes the header to the provided writer.
func (hdr ProxyRequire) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(Require(hdr).renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

// RenderOptions returns the string representation of the header.
func (hdr ProxyRequire) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr ProxyRequire) RenderValue() string {
	return Require(hdr).RenderValue()
}

// String returns the string representation of the header value.
func (hdr ProxyRequire) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr ProxyRequire) Format(f fmt.State, verb rune) {
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
		type hideMethods ProxyRequire
		type ProxyRequire hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), ProxyRequire(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr ProxyRequire) Clone() Header {
	hdr2, ok := Require(hdr).Clone().(Require)
	if !ok {
		return nil
	}
	return ProxyRequire(hdr2)
}

// Equal compares this header with another for equality.
func (hdr ProxyRequire) Equal(val any) bool {
	var other ProxyRequire
	switch v := val.(type) {
	case ProxyRequire:
		other = v
	case *ProxyRequire:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return Require(hdr).Equal(Require(other))
}

// IsValid checks whether the header is syntactically valid.
func (hdr ProxyRequire) IsValid() bool { return Require(hdr).IsValid() }

func (hdr ProxyRequire) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *ProxyRequire) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(ProxyRequire)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromProxyRequireNode(node *abnf.Node) ProxyRequire {
	return ProxyRequire(buildFromRequireNode(node))
}
