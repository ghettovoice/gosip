package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// RecordRoute represents the Record-Route header field.
// The Record-Route header field is inserted by proxies in a request to force future requests in the dialog to be routed through the proxy.
type RecordRoute []RouteHop

// CanonicName returns the canonical name of the header.
func (RecordRoute) CanonicName() Name { return "Record-Route" }

// CompactName returns the compact name of the header (Record-Route has no compact form).
func (RecordRoute) CompactName() Name { return "Record-Route" }

// RenderTo writes the header to the provided writer.
func (hdr RecordRoute) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrEntries(w, hdr))
	}))
}

// Render returns the string representation of the header.
func (hdr RecordRoute) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr RecordRoute) RenderValue() string {
	return renderHdrEntriesString(hdr)
}

// String returns the string representation of the header value.
func (hdr RecordRoute) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr RecordRoute) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods RecordRoute
		RecordRoute hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), RecordRoute(hdr))
}

// Clone returns a copy of the header.
func (hdr RecordRoute) Clone() Header {
	return cloneHdrEntries(hdr)
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

	return slices.EqualFunc(hdr, other, func(addr1, addr2 RouteHop) bool { return addr1.Equal(addr2) })
}

// IsValid checks whether the header is syntactically valid.
func (hdr RecordRoute) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(addr RouteHop) bool { return !addr.IsValid() })
}

func (hdr RecordRoute) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *RecordRoute) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyHdrValue))
}

func buildFromRecordRouteNode(node *abnf.Node) RecordRoute {
	addrNodes := node.GetNodes("rec-route")
	hdr := make(RecordRoute, 0, len(addrNodes))
	for i := range addrNodes {
		hdr = append(hdr, buildFromNameAddrNode(addrNodes[i], "generic-param"))
	}
	return hdr
}
