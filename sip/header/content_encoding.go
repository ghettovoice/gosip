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

type ContentEncoding []Encoding

func (ContentEncoding) CanonicName() Name { return "Content-Encoding" }

func (ContentEncoding) CompactName() Name { return "e" }

func (hdr ContentEncoding) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr ContentEncoding) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTokensTo(w, hdr))
}

func (hdr ContentEncoding) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr ContentEncoding) RenderValue() string {
	return renderHdrVal(hdr)
}

func (hdr ContentEncoding) String() string {
	return hdr.RenderValue()
}

func (hdr ContentEncoding) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods     ContentEncoding
		ContentEncoding hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), ContentEncoding(hdr))
}

func (hdr ContentEncoding) Clone() Header {
	return slices.Clone(hdr)
}

func (hdr ContentEncoding) Equal(val any) bool {
	return util.EqualValue(val, func(other ContentEncoding) bool {
		return slices.EqualFunc(hdr, other, func(enc1, enc2 Encoding) bool {
			return enc1.Equal(enc2)
		})
	})
}

func (hdr ContentEncoding) IsValid() bool {
	return len(hdr) > 0 &&
		!slices.ContainsFunc(hdr, func(enc Encoding) bool {
			return !enc.IsValid()
		})
}

func (hdr ContentEncoding) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ContentEncoding) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromContentEncodingNode(node *abnf.Node) ContentEncoding {
	encNodes := node.GetNodes("token")
	h := make(ContentEncoding, len(encNodes))
	for i, encNode := range encNodes {
		h[i] = Encoding(encNode.String())
	}
	return h
}

type Encoding string

func (enc Encoding) IsValid() bool {
	return grammar.IsToken(enc)
}

func (enc Encoding) Equal(val any) bool {
	return util.EqualValue(val, func(other Encoding) bool {
		return util.EqualFold(enc, other)
	})
}
