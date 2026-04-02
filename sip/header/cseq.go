package header

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// CSeq represents the CSeq header field.
// The CSeq header field serves as a way to identify and order transactions.
type CSeq struct {
	SeqNum uint
	Method RequestMethod
}

// CanonicName returns the canonical name of the header.
func (*CSeq) CanonicName() Name { return "CSeq" }

// CompactName returns the compact name of the header (CSeq has no compact form).
func (*CSeq) CompactName() Name { return "CSeq" }

// RenderTo writes the header to the provided writer.
func (hdr *CSeq) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)

	return errors.Wrap2(cw.Result())
}

func (hdr *CSeq) renderValueTo(w io.Writer) (num int, err error) {
	return errors.Wrap2(fmt.Fprint(w, hdr.SeqNum, " ", hdr.Method))
}

// Render returns the string representation of the header.
func (hdr *CSeq) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

// String returns the string representation of the header value.
func (hdr *CSeq) String() string { return hdr.RenderValue() }

// RenderValue returns the header value without the name prefix.
func (hdr *CSeq) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.renderValueTo(sb) //nolint:errcheck

	return sb.String()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *CSeq) Format(f fmt.State, verb rune) {
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
			hideMethods CSeq
			CSeq        hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), (*CSeq)(hdr))

		return
	}
}

// Clone returns a copy of the header.
func (hdr *CSeq) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := *hdr

	return &hdr2
}

// Equal compares this header with another for equality.
func (hdr *CSeq) Equal(val any) bool {
	var other *CSeq
	switch v := val.(type) {
	case CSeq:
		other = &v
	case *CSeq:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return hdr.SeqNum == other.SeqNum && hdr.Method.Equal(other.Method)
}

// IsValid checks whether the header is syntactically valid.
func (hdr *CSeq) IsValid() bool { return hdr != nil && hdr.SeqNum > 0 && hdr.Method.IsValid() }

func (hdr *CSeq) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *CSeq) UnmarshalJSON(data []byte) error {
	if hdr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil header")
	}

	gh, err := FromJSON(data)
	if gh == nil {
		*hdr = CSeq{}
		return errors.Wrap(err)
	}

	h, ok := gh.(*CSeq)
	if !ok {
		*hdr = CSeq{}

		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) &&
			(len(ah.Value) == 0 || strings.TrimSpace(ah.Value) == "0") {
			return nil
		}

		return newUnexpectHdrTypeErrWrap(gh)
	}

	*hdr = *h

	return nil
}

func buildFromCSeqNode(node *abnf.Node) *CSeq {
	seq, _ := strconv.ParseUint(node.Children[2].String(), 10, 64)

	return &CSeq{
		SeqNum: uint(seq),
		Method: RequestMethod(grammar.MustGetNode(node, "Method").String()),
	}
}
