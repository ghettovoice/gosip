package header

import (
	"fmt"
	"io"
	"regexp"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

type MIMEVersion string

func (MIMEVersion) CanonicName() Name { return "MIME-Version" }

func (MIMEVersion) CompactName() Name { return "MIME-Version" }

func (hdr MIMEVersion) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr MIMEVersion) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(io.WriteString(w, string(hdr)))
}

func (hdr MIMEVersion) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

// RenderValue returns the header value without the name prefix.
func (hdr MIMEVersion) RenderValue() string {
	return string(hdr)
}

func (hdr MIMEVersion) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods MIMEVersion
		MIMEVersion hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), MIMEVersion(hdr))
}

func (hdr MIMEVersion) Clone() Header {
	return hdr
}

func (hdr MIMEVersion) Equal(val any) bool {
	return util.EqualValue(val, func(other MIMEVersion) bool {
		return util.EqualFold(hdr, other)
	})
}

var mimeVerRe = regexp.MustCompile(`^\d+\.\d+$`)

func (hdr MIMEVersion) IsValid() bool {
	return len(hdr) > 0 && mimeVerRe.MatchString(string(hdr))
}

func (hdr MIMEVersion) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *MIMEVersion) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromMIMEVersionNode(node *abnf.Node) MIMEVersion {
	var s []byte
	for _, n := range node.Children[2:] {
		if n.IsEmpty() {
			continue
		}
		s = append(s, n.Value...)
	}
	return MIMEVersion(s)
}
