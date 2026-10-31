package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// ProxyRequire represents the Proxy-Require header field.
// The Proxy-Require header field is used to indicate proxy-sensitive features that must be supported by the proxy.
type ProxyRequire []OptionTag

// CanonicName returns the canonical name of the header.
func (ProxyRequire) CanonicName() Name { return "Proxy-Require" }

// CompactName returns the compact name of the header (Proxy-Require has no compact form).
func (ProxyRequire) CompactName() Name { return "Proxy-Require" }

// RenderTo writes the header to the provided writer.
func (hdr ProxyRequire) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrEntries(w, hdr))
	}))
}

// Render returns the string representation of the header.
func (hdr ProxyRequire) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr ProxyRequire) RenderValue() string {
	return renderHdrEntriesString(hdr)
}

// String returns the string representation of the header value.
func (hdr ProxyRequire) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr ProxyRequire) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods  ProxyRequire
		ProxyRequire hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), ProxyRequire(hdr))
}

// Clone returns a copy of the header.
func (hdr ProxyRequire) Clone() Header {
	return slices.Clone(hdr)
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

	return slices.EqualFunc(hdr, other, func(tag1, tag2 OptionTag) bool { return tag1.Equal(tag2) })
}

// IsValid checks whether the header is syntactically valid.
func (hdr ProxyRequire) IsValid() bool { return len(hdr) > 0 && validOptionTags(hdr) }

func (hdr ProxyRequire) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ProxyRequire) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), nil))
}

func buildFromProxyRequireNode(node *abnf.Node) ProxyRequire {
	return ProxyRequire(buildOptionTagsFromNode(node))
}
