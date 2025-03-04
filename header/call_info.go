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

// CallInfo represents the Call-Info header field.
// The Call-Info header field provides additional information about the caller or callee.
type CallInfo []CallInfoAddr

// CanonicName returns the canonical name of the header.
func (CallInfo) CanonicName() Name { return "Call-Info" }

// CompactName returns the compact name of the header (Call-Info has no compact form).
func (CallInfo) CompactName() Name { return "Call-Info" }

// RenderTo writes the header to the provided writer.
func (hdr CallInfo) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr CallInfo) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

// Render returns the string representation of the header.
func (hdr CallInfo) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

// String returns the string representation of the header value.
func (hdr CallInfo) String() string { return hdr.RenderValue() }

// RenderValue returns the header value without the name prefix.
func (hdr CallInfo) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr CallInfo) Format(f fmt.State, verb rune) {
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
		type hideMethods CallInfo
		type CallInfo hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), CallInfo(hdr))
		return
	}
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
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *CallInfo) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}
	h, ok := gh.(CallInfo)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}
	*hdr = h
	return nil
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
