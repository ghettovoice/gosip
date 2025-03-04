package header

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
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
func (hdr Server) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	return errtrace.Wrap2(fmt.Fprint(w, hdr.CanonicName(), ": ", hdr.RenderValue()))
}

// Render returns the string representation of the header.
func (hdr Server) Render(opts *RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr Server) RenderValue() string { return string(hdr) }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Server) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}
		fmt.Fprint(f, string(hdr))
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}
		fmt.Fprint(f, strconv.Quote(string(hdr)))
		return
	default:
		type hideMethods Server
		type Server hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), Server(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr Server) Clone() Header { return hdr }

// Equal compares this header with another for equality.
func (hdr Server) Equal(val any) bool {
	var other Server
	switch v := val.(type) {
	case Server:
		other = v
	case *Server:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return hdr == other
}

// IsValid checks whether the header is syntactically valid.
func (hdr Server) IsValid() bool { return hdr != "" }

func (hdr Server) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *Server) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = ""
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(Server)
	if !ok {
		*hdr = ""
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}
	*hdr = h
	return nil
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
