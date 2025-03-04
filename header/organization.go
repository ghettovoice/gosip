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

// Organization represents the Organization header field.
// The Organization header field conveys the name of the organization to which the SIP element issuing the request or response belongs.
type Organization string

// CanonicName returns the canonical name of the header.
func (Organization) CanonicName() Name { return "Organization" }

// CompactName returns the compact name of the header (Organization has no compact form).
func (Organization) CompactName() Name { return "Organization" }

// RenderTo writes the header to the provided writer.
func (hdr Organization) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	return errtrace.Wrap2(fmt.Fprint(w, hdr.CanonicName(), ": ", hdr.RenderValue()))
}

// Render returns the string representation of the header.
func (hdr Organization) Render(opts *RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr Organization) RenderValue() string { return string(hdr) }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Organization) Format(f fmt.State, verb rune) {
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
		type hideMethods Organization
		type Organization hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), Organization(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr Organization) Clone() Header { return hdr }

// Equal compares this header with another for equality.
func (hdr Organization) Equal(val any) bool {
	var other Organization
	switch v := val.(type) {
	case Organization:
		other = v
	case *Organization:
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
func (Organization) IsValid() bool { return true }

func (hdr Organization) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *Organization) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = ""
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(Organization)
	if !ok {
		*hdr = ""
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromOrganizationNode(node *abnf.Node) Organization {
	return Organization(node.Children[2].Value)
}
