package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// CallInfo represents the Call-Info header field.
// The Call-Info header field provides additional information about the caller or callee.
type CallInfo []CallInfoAddr

// CanonicName returns the canonical name of the header.
func (CallInfo) CanonicName() Name { return "Call-Info" }

// CompactName returns the compact name of the header (Call-Info has no compact form).
func (CallInfo) CompactName() Name { return "Call-Info" }

// RenderTo writes the header to the provided writer.
func (hdr CallInfo) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrEntries(w, hdr))
	}))
}

// Render returns the string representation of the header.
func (hdr CallInfo) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// String returns the string representation of the header value.
func (hdr CallInfo) String() string { return hdr.RenderValue() }

// RenderValue returns the header value without the name prefix.
func (hdr CallInfo) RenderValue() string {
	return renderHdrEntriesString(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr CallInfo) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods CallInfo
		CallInfo    hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), CallInfo(hdr))
}

// Clone returns a copy of the header.
func (hdr CallInfo) Clone() Header { return cloneHdrEntries(hdr) }

// Equal compares this header with another for equality.
func (hdr CallInfo) Equal(val any) bool {
	var other CallInfo
	switch v := val.(type) {
	case CallInfo:
		other = v
	case *CallInfo:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return slices.EqualFunc(hdr, other, func(addr1, addr2 CallInfoAddr) bool { return addr1.Equal(addr2) })
}

// IsValid checks whether the header is syntactically valid.
func (hdr CallInfo) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(addr CallInfoAddr) bool { return !addr.IsValid() })
}

func (hdr CallInfo) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *CallInfo) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyHdrValue))
}

func buildFromCallInfoNode(node *abnf.Node) CallInfo {
	entryNodes := node.GetNodes("info")
	h := make(CallInfo, len(entryNodes))
	for i, entryNode := range entryNodes {
		h[i] = buildFromInfoAddrNode(entryNode)
	}
	return h
}

type CallInfoAddr = InfoAddr
