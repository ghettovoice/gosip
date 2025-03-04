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

// Subject represents the Subject header field.
// The Subject header field provides a summary or indicates the nature of the call.
type Subject string

// CanonicName returns the canonical name of the header.
func (Subject) CanonicName() Name { return "Subject" }

// CompactName returns the compact name of the header.
func (Subject) CompactName() Name { return "s" }

// RenderTo writes the header to the provided writer.
func (hdr Subject) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	return errtrace.Wrap2(fmt.Fprint(w, hdr.name(opts), ": ", hdr.RenderValue()))
}

func (hdr Subject) name(opts *RenderOptions) Name {
	if opts != nil && opts.Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

// Render returns the string representation of the header.
func (hdr Subject) Render(opts *RenderOptions) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr Subject) RenderValue() string { return string(hdr) }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Subject) Format(f fmt.State, verb rune) {
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
		type hideMethods Subject
		type Subject hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), Subject(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr Subject) Clone() Header { return hdr }

// Equal compares this header with another for equality.
func (hdr Subject) Equal(val any) bool {
	var other Subject
	switch v := val.(type) {
	case Subject:
		other = v
	case *Subject:
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
func (Subject) IsValid() bool { return true }

func (hdr Subject) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *Subject) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = ""
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(Subject)
	if !ok {
		*hdr = ""
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromSubjectNode(node *abnf.Node) Subject {
	return Subject(node.Children[2].Value)
}
