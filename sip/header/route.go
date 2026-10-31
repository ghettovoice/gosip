package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// Route represents the Route header field.
// The Route header field is used to force routing for a request through the listed set of proxies.
type Route []RouteHop

// CanonicName returns the canonical name of the header.
func (Route) CanonicName() Name { return "Route" }

// CompactName returns the compact name of the header (Route has no compact form).
func (Route) CompactName() Name { return "Route" }

// RenderTo writes the header to the provided writer.
func (hdr Route) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrEntries(w, hdr))
	}))
}

// Render returns the string representation of the header.
func (hdr Route) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr Route) RenderValue() string {
	return renderHdrEntriesString(hdr)
}

// String returns the string representation of the header value.
func (hdr Route) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Route) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Route
		Route       hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Route(hdr))
}

// Clone returns a copy of the header.
func (hdr Route) Clone() Header { return cloneHdrEntries(hdr) }

// Equal compares this header with another for equality.
func (hdr Route) Equal(val any) bool {
	var other Route
	switch v := val.(type) {
	case Route:
		other = v
	case *Route:
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
func (hdr Route) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(addr NameAddr) bool { return !addr.IsValid() })
}

func (hdr Route) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Route) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyHdrValue))
}

func buildFromRouteNode(node *abnf.Node) Route {
	addrNodes := node.GetNodes("route-param")
	hdr := make(Route, 0, len(addrNodes))
	for i := range addrNodes {
		hdr = append(hdr, buildFromNameAddrNode(addrNodes[i], "generic-param"))
	}
	return hdr
}

type RouteHop = NameAddr
