package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

// Server represents the Server header field.
// The Server header field contains information about the software used by the UAS to handle the request.
type Server string

// CanonicName returns the canonical name of the header.
func (Server) CanonicName() Name { return "Server" }

// CompactName returns the compact name of the header (Server has no compact form).
func (Server) CompactName() Name { return "Server" }

// RenderTo writes the header to the provided writer.
func (hdr Server) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Server) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(io.WriteString(w, string(hdr)))
}

// Render returns the string representation of the header.
func (hdr Server) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr Server) RenderValue() string {
	return string(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Server) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Server
		Server      hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Server(hdr))
}

// Clone returns a copy of the header.
func (hdr Server) Clone() Header {
	return hdr
}

// Equal compares this header with another for equality.
func (hdr Server) Equal(val any) bool {
	return util.EqualValue(val, func(other Server) bool {
		return hdr == other
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr Server) IsValid() bool {
	return hdr != ""
}

func (hdr Server) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Server) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromServerNode(node *abnf.Node) Server {
	var s []byte
	for _, n := range node.Children[2:] {
		if n.IsEmpty() {
			continue
		}
		s = append(s, n.Value...)
	}
	return Server(s)
}
