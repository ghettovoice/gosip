package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
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
func (hdr Require) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr Require) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTokensTo(w, hdr))
}

// Render returns the string representation of the header.
func (hdr Require) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr Require) RenderValue() string {
	return renderHdrVal(hdr)
}

// String returns the string representation of the header value.
func (hdr Require) String() string {
	return hdr.RenderValue()
}

// Format implements fmt.Formatter for custom formatting of the header.
func (hdr Require) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Require
		Require     hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), Require(hdr))
}

// Clone returns a copy of the header.
func (hdr Require) Clone() Header {
	return slices.Clone(hdr)
}

// Equal compares this header with another for equality.
func (hdr Require) Equal(val any) bool {
	return util.EqualValue(val, func(other Require) bool {
		return slices.EqualFunc(hdr, other, func(o1, o2 OptionTag) bool {
			return o1.Equal(o2)
		})
	})
}

// IsValid checks whether the header is syntactically valid.
func (hdr Require) IsValid() bool {
	return len(hdr) > 0 && validOptionTags(hdr)
}

func (hdr Require) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Require) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), nil))
}

func buildFromRequireNode(node *abnf.Node) Require {
	return Require(buildOptionTagsFromNode(node))
}

func buildOptionTagsFromNode(node *abnf.Node) []OptionTag {
	tagNodes := node.GetNodes("token")
	tags := make([]OptionTag, 0, len(tagNodes))
	for i := range tagNodes {
		if n, ok := tagNodes[i].GetNode("token"); ok {
			tags = append(tags, OptionTag(n.String()))
		}
	}
	return tags
}

type OptionTag string

func validOptionTags(tags []OptionTag) bool {
	for _, tag := range tags {
		if !tag.IsValid() {
			return false
		}
	}
	return true
}

func (o OptionTag) IsValid() bool {
	return grammar.IsToken(o)
}

func (o OptionTag) Equal(val any) bool {
	return util.EqualValue(val, func(other OptionTag) bool {
		return util.EqualFold(o, other)
	})
}
