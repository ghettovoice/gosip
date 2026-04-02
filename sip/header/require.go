package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// Require represents the Require header field.
// The Require header field is used by UACs to tell UASs about options that the UAC expects the UAS to support.
type Require []OptionTag

// CanonicName returns the canonical name of the header.
func (Require) CanonicName() Name { return "Require" }

// CompactName returns the compact name of the header (Require has no compact form).
func (Require) CompactName() Name { return "Require" }

// RenderTo writes the header to the provided writer.
func (hdr Require) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)

	return errors.Wrap2(cw.Result())
}

func (hdr Require) renderValueTo(w io.Writer) (num int, err error) {
	return errors.Wrap2(renderHdrEntries(w, hdr))
}

// Render returns the string representation of the header.
func (hdr Require) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

// RenderValue returns the header value without the name prefix.
func (hdr Require) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.renderValueTo(sb) //nolint:errcheck

	return sb.String()
}

// String returns the string representation of the header value.
func (hdr Require) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Require) Format(f fmt.State, verb rune) {
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
			hideMethods Require
			Require     hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), Require(hdr))

		return
	}
}

// Clone returns a copy of the header.
func (hdr Require) Clone() Header { return slices.Clone(hdr) }

// Equal compares this header with another for equality.
func (hdr Require) Equal(val any) bool {
	var other Require
	switch v := val.(type) {
	case Require:
		other = v
	case *Require:
		if v == nil {
			return false
		}

		other = *v
	default:
		return false
	}

	return slices.EqualFunc(hdr, other, util.EqFold)
}

// IsValid checks whether the header is syntactically valid.
func (hdr Require) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(s OptionTag) bool { return !grammar.IsToken(s) })
}

func (hdr Require) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Require) UnmarshalJSON(data []byte) error {
	if hdr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil header")
	}

	gh, err := FromJSON(data)
	if gh == nil {
		*hdr = nil
		return errors.Wrap(err)
	}

	h, ok := gh.(Require)
	if !ok {
		*hdr = nil
		return newUnexpectHdrTypeErrWrap(gh)
	}

	*hdr = h

	return nil
}

func buildFromRequireNode(node *abnf.Node) Require {
	tagNodes := node.GetNodes("token")

	h := make(Require, 0, len(tagNodes))
	for i := range tagNodes {
		if n, ok := tagNodes[i].GetNode("token"); ok {
			h = append(h, n.String())
		}
	}

	return h
}

type OptionTag = string
