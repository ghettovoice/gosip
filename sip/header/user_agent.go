package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// UserAgent represents the User-Agent header field.
// The User-Agent header field contains information about the UAC originating the request.
type UserAgent string

// CanonicName returns the canonical name of the header.
func (UserAgent) CanonicName() Name { return "User-Agent" }

// CompactName returns the compact name of the header (User-Agent has no compact form).
func (UserAgent) CompactName() Name { return "User-Agent" }

// RenderTo writes the header to the provided writer.
func (hdr UserAgent) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr UserAgent) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(io.WriteString(w, string(hdr)))
}

// Render returns the string representation of the header.
func (hdr UserAgent) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr UserAgent) RenderValue() string {
	return string(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr UserAgent) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods UserAgent
		UserAgent   hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), UserAgent(hdr))
}

// Clone returns a copy of the header.
func (hdr UserAgent) Clone() Header {
	return hdr
}

// Equal compares this header with another for equality.
func (hdr UserAgent) Equal(val any) bool {
	return util.EqualValue(val, func(other UserAgent) bool {
		return hdr == other
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr UserAgent) IsValid() bool {
	return hdr != ""
}

func (hdr UserAgent) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *UserAgent) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromUserAgentNode(node *abnf.Node) UserAgent {
	return UserAgent(buildFromServerNode(node))
}
