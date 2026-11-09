package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

type ContentType MIMEType

func (*ContentType) CanonicName() Name { return "Content-Type" }

func (*ContentType) CompactName() Name { return "c" }

func (hdr *ContentType) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *ContentType) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(MIMEType(*hdr).renderTo(w))
}

func (hdr *ContentType) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *ContentType) String() string {
	return hdr.RenderValue()
}

// RenderValue returns the header value without the name prefix.
func (hdr *ContentType) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *ContentType) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods ContentType
		ContentType hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*ContentType)(hdr))
}

func (hdr *ContentType) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := ContentType(MIMEType(*hdr).Clone())
	return &clone
}

func (hdr *ContentType) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *ContentType) bool {
		return MIMEType(*hdr).Equal(MIMEType(*other))
	})
}

func (hdr *ContentType) IsValid() bool {
	return hdr != nil && MIMEType(*hdr).IsValid()
}

func (hdr *ContentType) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ContentType) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), func(value string) bool {
		return value == "" || value == "/"
	}))
}

func buildFromContentTypeNode(node *abnf.Node) *ContentType {
	mt, ps := buildFromMIMETypeNode(grammar.MustGetNode(node, "media-type"))
	for i := range ps {
		mt.Params.Append(ps[i][0], ps[i][1])
	}
	hdr := ContentType(mt)
	return &hdr
}
