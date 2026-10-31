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

// Expires represents the Expires header field.
// The Expires header field gives the relative time after which the message (or content) expires.
type Expires struct {
	time.Duration
}

// CanonicName returns the canonical name of the header.
func (*Expires) CanonicName() Name { return "Expires" }

// CompactName returns the compact name of the header (Expires has no compact form).
func (*Expires) CompactName() Name { return "Expires" }

// RenderTo writes the header to the provided writer.
func (hdr *Expires) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderDeltaSecondsTo(w, hdr.Duration))
	}))
}

// Render returns the string representation of the header.
func (hdr *Expires) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr *Expires) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderDeltaSeconds(hdr.Duration)
}

func (hdr *Expires) String() string { return hdr.RenderValue() }

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr *Expires) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Expires
		Expires     hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*Expires)(hdr))
}

// Clone returns a copy of the header.
func (hdr *Expires) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := *hdr
	return &hdr2
}

// Equal compares this header with another for equality.
func (hdr *Expires) Equal(val any) bool {
	var other *Expires
	switch v := val.(type) {
	case Expires:
		other = &v
	case *Expires:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return hdr.Duration == other.Duration
}

// IsValid checks whether the header is syntactically valid.
func (hdr *Expires) IsValid() bool { return hdr != nil && validDeltaSeconds(hdr.Duration) }

func (hdr *Expires) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Expires) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyHdrValue))
}

func buildFromExpiresNode(node *abnf.Node) *Expires {
	duration, err := parseDeltaSeconds(grammar.MustGetNode(node, "delta-seconds").String())
	if err != nil {
		panic(errors.Wrap(err))
	}
	return &Expires{Duration: duration}
}
