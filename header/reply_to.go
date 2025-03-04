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

// ReplyTo represents the Reply-To header field.
// The Reply-To header field contains a logical return URI that may be different from the From header field.
type ReplyTo NameAddr

// CanonicName returns the canonical name of the header.
func (*ReplyTo) CanonicName() Name { return "Reply-To" }

// CompactName returns the compact name of the header (Reply-To has no compact form).
func (*ReplyTo) CompactName() Name { return "Reply-To" }

// RenderTo writes the header to the provided writer.
func (hdr *ReplyTo) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errtrace.Wrap2(fmt.Fprint(w, hdr.CanonicName(), ": ", hdr.RenderValue()))
}

// Render returns the string representation of the header.
func (hdr *ReplyTo) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr *ReplyTo) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return NameAddr(*hdr).String()
}

// String returns the string representation of the header value.
func (hdr *ReplyTo) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *ReplyTo) Format(f fmt.State, verb rune) {
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
		type hideMethods ReplyTo
		type ReplyTo hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*ReplyTo)(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr *ReplyTo) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := ReplyTo(NameAddr(*hdr).Clone())
	return &hdr2
}

// Equal compares this header with another for equality.
func (hdr *ReplyTo) Equal(val any) bool {
	var other *ReplyTo
	switch v := val.(type) {
	case ReplyTo:
		other = &v
	case *ReplyTo:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return NameAddr(*hdr).Equal(NameAddr(*other))
}

// IsValid checks whether the header is syntactically valid.
func (hdr *ReplyTo) IsValid() bool { return hdr != nil && NameAddr(*hdr).IsValid() }

func (hdr *ReplyTo) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

var zeroReplyTo ReplyTo

func (hdr *ReplyTo) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = zeroReplyTo
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(*ReplyTo)
	if !ok {
		*hdr = zeroReplyTo
		return errtrace.Wrap(errorutil.Errorf("decoded header %T, want %T", gh, hdr))
	}

	*hdr = *h
	return nil
}

func buildFromReplyToNode(node *abnf.Node) *ReplyTo {
	hdr := ReplyTo(buildFromNameAddrNode(node, "generic-param"))
	return &hdr
}
