package header

import (
	"fmt"
	"io"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/types"
)

// MinExpires represents the Min-Expires header field.
// The Min-Expires header field conveys the minimum refresh interval supported for soft-state elements.
type MinExpires struct {
	time.Duration
}

// CanonicName returns the canonical name of the header.
func (*MinExpires) CanonicName() Name { return "Min-Expires" }

// CompactName returns the compact name of the header (Min-Expires has no compact form).
func (*MinExpires) CompactName() Name { return "Min-Expires" }

// RenderTo writes the header to the provided writer.
func (hdr *MinExpires) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, renderHdrName(hdr, opts...), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderDeltaSecondsTo(w, hdr.Duration))
	}))
}

// Render returns the string representation of the header.
func (hdr *MinExpires) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr *MinExpires) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderDeltaSeconds(hdr.Duration)
}

func (hdr *MinExpires) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *MinExpires) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods MinExpires
		MinExpires  hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*MinExpires)(hdr))
}

// Clone returns a copy of the header.
func (hdr *MinExpires) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	return &hdr2
}

// Equal compares this header with another for equality.
func (hdr *MinExpires) Equal(val any) bool {
	var other *MinExpires
	switch v := val.(type) {
	case MinExpires:
		other = &v
	case *MinExpires:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	}
	if hdr == nil || other == nil {
		return false
	}
	return hdr.Duration == other.Duration
}

// IsValid checks whether the header is syntactically valid.
func (hdr *MinExpires) IsValid() bool {
	return hdr != nil && validDeltaSeconds(hdr.Duration)
}

func (hdr *MinExpires) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *MinExpires) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyHdrValue))
}

func buildFromMinExpiresNode(node *abnf.Node) *MinExpires {
	duration, err := parseDeltaSeconds(grammar.MustGetNode(node, "delta-seconds").String())
	if err != nil {
		panic(errors.Wrap(err))
	}
	return &MinExpires{Duration: duration}
}
