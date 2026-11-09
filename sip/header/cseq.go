package header

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
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
func (hdr *CSeq) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *CSeq) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(fmt.Fprint(w, hdr.SeqNum, " ", hdr.Method))
}

// Render returns the string representation of the header.
func (hdr *CSeq) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// String returns the string representation of the header value.
func (hdr *CSeq) String() string {
	return hdr.RenderValue()
}

// RenderValue returns the header value without the name prefix.
func (hdr *CSeq) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *CSeq) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods CSeq
		CSeq        hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*CSeq)(hdr))
}

// Clone returns a copy of the header.
func (hdr *CSeq) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := *hdr
	return &clone
}

// Equal compares this header with another for equality.
func (hdr *CSeq) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *CSeq) bool {
		return hdr.SeqNum == other.SeqNum && hdr.Method.Equal(other.Method)
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr *CSeq) IsValid() bool {
	return hdr != nil &&
		hdr.SeqNum > 0 &&
		hdr.Method.IsValid()
}

func (hdr *CSeq) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *CSeq) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), func(value string) bool {
		return value == "" || strings.TrimSpace(value) == "0"
	}))
}

func buildFromCSeqNode(node *abnf.Node) *CSeq {
	seq, err := strconv.ParseUint(node.Children[2].String(), 10, 64)
	if err != nil {
		panic(errors.ErrorfWrap("invalid sequence number: %w", err))
	}
	return &CSeq{
		SeqNum: uint(seq),
		Method: RequestMethod(grammar.MustGetNode(node, "Method").String()),
	}
}
