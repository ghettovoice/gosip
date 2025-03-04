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

// UserAgent represents the User-Agent header field.
// The User-Agent header field contains information about the UAC originating the request.
type UserAgent string

// CanonicName returns the canonical name of the header.
func (UserAgent) CanonicName() Name { return "User-Agent" }

// CompactName returns the compact name of the header (User-Agent has no compact form).
func (UserAgent) CompactName() Name { return "User-Agent" }

// RenderTo writes the header to the provided writer.
func (hdr UserAgent) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	return errtrace.Wrap2(fmt.Fprint(w, hdr.CanonicName(), ": ", hdr.RenderValue()))
}

// RenderOptions returns the string representation of the header.
func (hdr UserAgent) Render(opts *RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr UserAgent) RenderValue() string { return string(hdr) }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr UserAgent) Format(f fmt.State, verb rune) {
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
		type hideMethods UserAgent
		type UserAgent hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), UserAgent(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr UserAgent) Clone() Header { return hdr }

// Equal compares this header with another for equality.
func (hdr UserAgent) Equal(val any) bool {
	var other UserAgent
	switch v := val.(type) {
	case UserAgent:
		other = v
	case *UserAgent:
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
func (hdr UserAgent) IsValid() bool { return hdr != "" }

func (hdr UserAgent) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *UserAgent) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = ""
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(UserAgent)
	if !ok {
		*hdr = ""
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromUserAgentNode(node *abnf.Node) UserAgent {
	return UserAgent(buildFromServerNode(node))
}
