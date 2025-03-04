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

// RecordRoute represents the Record-Route header field.
// The Record-Route header field is inserted by proxies in a request to force future requests in the dialog to be routed through the proxy.
type RecordRoute Route

// CanonicName returns the canonical name of the header.
func (RecordRoute) CanonicName() Name { return "Record-Route" }

// CompactName returns the compact name of the header (Record-Route has no compact form).
func (RecordRoute) CompactName() Name { return "Record-Route" }

// RenderTo writes the header to the provided writer.
func (hdr RecordRoute) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(Route(hdr).renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

// Render returns the string representation of the header.
func (hdr RecordRoute) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr RecordRoute) RenderValue() string {
	return Route(hdr).RenderValue()
}

// String returns the string representation of the header value.
func (hdr RecordRoute) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr RecordRoute) Format(f fmt.State, verb rune) {
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
		type hideMethods RecordRoute
		type RecordRoute hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), RecordRoute(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr RecordRoute) Clone() Header {
	hdr2, ok := Route(hdr).Clone().(Route)
	if !ok {
		return nil
	}
	return RecordRoute(hdr2)
}

// Equal compares this header with another for equality.
func (hdr RecordRoute) Equal(val any) bool {
	var other RecordRoute
	switch v := val.(type) {
	case RecordRoute:
		other = v
	case *RecordRoute:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return Route(hdr).Equal(Route(other))
}

// IsValid checks whether the header is syntactically valid.
func (hdr RecordRoute) IsValid() bool { return Route(hdr).IsValid() }

func (hdr RecordRoute) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *RecordRoute) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(RecordRoute)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromRecordRouteNode(node *abnf.Node) RecordRoute {
	addrNodes := node.GetNodes("rec-route")
	hdr := make(RecordRoute, 0, len(addrNodes))
	for i := range addrNodes {
		hdr = append(hdr, buildFromNameAddrNode(addrNodes[i], "generic-param"))
	}
	return hdr
}
