package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// Allow represents the Allow header field.
// The Allow header field lists the set of methods supported by the UA generating the message.
type Allow []RequestMethod

// CanonicName returns the canonical name of the header.
func (Allow) CanonicName() Name { return "Allow" }

// CompactName returns the compact name of the header (Allow has no compact form).
func (Allow) CompactName() Name { return "Allow" }

// RenderTo writes the header to the provided writer.
func (hdr Allow) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)

	return errors.Wrap2(cw.Result())
}

func (hdr Allow) renderValueTo(w io.Writer) (num int, err error) {
	return errors.Wrap2(renderHdrEntries(w, hdr))
}

// Render returns the string representation of the header.
func (hdr Allow) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

// RenderValue returns the string representation of the header value.
func (hdr Allow) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.renderValueTo(sb) //nolint:errcheck

	return sb.String()
}

// String returns the string representation of the header value.
func (hdr Allow) String() string {
	return hdr.RenderValue()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Allow) Format(f fmt.State, verb rune) {
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
		type (
			hideMethods Allow
			Allow       hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), Allow(hdr))

		return
	}
}

// Clone returns a copy of the header.
func (hdr Allow) Clone() Header { return slices.Clone(hdr) }

// Equal compares this header with another for equality.
func (hdr Allow) Equal(val any) bool {
	var other Allow
	switch v := val.(type) {
	case Allow:
		other = v
	case *Allow:
		if v == nil {
			return false
		}

		other = *v
	default:
		return false
	}

	return slices.EqualFunc(hdr, other, func(mtd1, mtd2 RequestMethod) bool { return mtd1.Equal(mtd2) })
}

// IsValid checks whether the header is syntactically valid.
func (hdr Allow) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(mtd RequestMethod) bool { return !mtd.IsValid() })
}

func (hdr Allow) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Allow) UnmarshalJSON(data []byte) error {
	if hdr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil header")
	}

	gh, err := FromJSON(data)
	if gh == nil {
		*hdr = nil
		return errors.Wrap(err)
	}

	h, ok := gh.(Allow)
	if !ok {
		*hdr = nil
		return newUnexpectHdrTypeErrWrap(gh)
	}

	*hdr = h

	return nil
}

func buildFromAllowNode(node *abnf.Node) Allow {
	mthNodes := node.GetNodes("Method")

	hdr := make(Allow, len(mthNodes))
	for i, mthNode := range mthNodes {
		hdr[i] = RequestMethod(mthNode.String())
	}

	return hdr
}
