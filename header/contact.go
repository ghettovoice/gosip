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

// Contact represents the Contact header field.
// The Contact header field provides a SIP or SIPS URI that can be used to contact that specific instance
// of the UA for subsequent requests.
type Contact []ContactAddr

// CanonicName returns the canonical name of the header.
func (Contact) CanonicName() Name { return "Contact" }

// CompactName returns the compact name of the header.
func (Contact) CompactName() Name { return "m" }

// RenderTo writes the header to the provided writer.
func (hdr Contact) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.name(opts), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr Contact) name(opts *RenderOptions) Name {
	if opts != nil && opts.Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

func (hdr Contact) renderValueTo(w io.Writer) (num int, err error) {
	if len(hdr) == 0 {
		return errtrace.Wrap2(fmt.Fprint(w, "*"))
	}
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

// Render returns the string representation of the header.
func (hdr Contact) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// String returns the string representation of the header value.
func (hdr Contact) String() string { return hdr.RenderValue() }

// RenderValue returns the header value without the name prefix.
func (hdr Contact) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Contact) Format(f fmt.State, verb rune) {
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
		type hideMethods Contact
		type Contact hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), Contact(hdr))
		return
	}
}

// Clone returns a copy of the header.
func (hdr Contact) Clone() Header { return cloneHdrEntries(hdr) }

// Equal compares this header with another for equality.
func (hdr Contact) Equal(val any) bool {
	var other Contact
	switch v := val.(type) {
	case Contact:
		other = v
	case *Contact:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(addr1, addr2 ContactAddr) bool { return addr1.Equal(addr2) })
}

// IsValid checks whether the header is syntactically valid.
func (hdr Contact) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(addr ContactAddr) bool { return !addr.IsValid() })
}

func (hdr Contact) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *Contact) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(Contact)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromContactNode(node *abnf.Node) Contact {
	cntNodes := node.GetNodes("contact-param")
	h := make(Contact, len(cntNodes))
	for i, cntNode := range cntNodes {
		h[i] = buildFromNameAddrNode(cntNode, "contact-params")
	}
	return h
}

type ContactAddr = NameAddr
