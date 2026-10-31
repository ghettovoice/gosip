package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// ErrorInfo represents the Error-Info header field.
// The Error-Info header field provides a pointer to additional information about the error status response.
type ErrorInfo []ErrorInfoAddr

// CanonicName returns the canonical name of the header.
func (ErrorInfo) CanonicName() Name { return "Error-Info" }

// CompactName returns the compact name of the header (Error-Info has no compact form).
func (ErrorInfo) CompactName() Name { return "Error-Info" }

// RenderTo writes the header to the provided writer.
func (hdr ErrorInfo) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrEntries(w, hdr))
	}))
}

// Render returns the string representation of the header.
func (hdr ErrorInfo) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// String returns the string representation of the header value.
func (hdr ErrorInfo) String() string { return hdr.RenderValue() }

// RenderValue returns the header value without the name prefix.
func (hdr ErrorInfo) RenderValue() string {
	return renderHdrEntriesString(hdr)
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr ErrorInfo) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods ErrorInfo
		ErrorInfo   hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), ErrorInfo(hdr))
}

// Clone returns a copy of the header.
func (hdr ErrorInfo) Clone() Header { return cloneHdrEntries(hdr) }

// Equal compares this header with another for equality.
func (hdr ErrorInfo) Equal(val any) bool {
	var other ErrorInfo
	switch v := val.(type) {
	case ErrorInfo:
		other = v
	case *ErrorInfo:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return slices.EqualFunc(hdr, other, func(addr1, addr2 ErrorInfoAddr) bool { return addr1.Equal(addr2) })
}

// IsValid checks whether the header is syntactically valid.
func (hdr ErrorInfo) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(addr ErrorInfoAddr) bool { return !addr.IsValid() })
}

func (hdr ErrorInfo) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ErrorInfo) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyHdrValue))
}

func buildFromErrorInfoNode(node *abnf.Node) ErrorInfo {
	entryNodes := node.GetNodes("error-uri")
	h := make(ErrorInfo, len(entryNodes))
	for i, entryNode := range entryNodes {
		h[i] = buildFromInfoAddrNode(entryNode)
	}
	return h
}

type ErrorInfoAddr = InfoAddr
