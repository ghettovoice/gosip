package header

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// InReplyTo represents the In-Reply-To header field.
// The In-Reply-To header field enumerates the Call-IDs that this call references or returns.
type InReplyTo []CallID

// CanonicName returns the canonical name of the header.
func (InReplyTo) CanonicName() Name { return "In-Reply-To" }

// CompactName returns the compact name of the header (In-Reply-To has no compact form).
func (InReplyTo) CompactName() Name { return "In-Reply-To" }

// RenderTo writes the header to the provided writer.
func (hdr InReplyTo) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr InReplyTo) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

// Render returns the string representation of the header.
func (hdr InReplyTo) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// String returns the string representation of the header value.
func (hdr InReplyTo) String() string { return hdr.RenderValue() }

// RenderValue returns the header value without the name prefix.
func (hdr InReplyTo) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr InReplyTo) Format(f fmt.State, verb rune) {
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
		type hideMethods InReplyTo
		type InReplyTo hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), InReplyTo(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr InReplyTo) Clone() Header { return slices.Clone(hdr) }

// Equal compares this header with another for equality.
func (hdr InReplyTo) Equal(val any) bool {
	var other InReplyTo
	switch v := val.(type) {
	case InReplyTo:
		other = v
	case *InReplyTo:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(id1, id2 CallID) bool { return id1.Equal(id2) })
}

// IsValid checks whether the header is syntactically valid.
func (hdr InReplyTo) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(id CallID) bool { return !id.IsValid() })
}

func (hdr InReplyTo) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *InReplyTo) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(InReplyTo)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, hdr))
	}

	*hdr = h
	return nil
}

func buildFromInReplyToNode(node *abnf.Node) InReplyTo {
	idNodes := node.GetNodes("callid")
	h := make(InReplyTo, len(idNodes))
	for i, idNode := range idNodes {
		h[i] = CallID(idNode.Value)
	}
	return h
}
