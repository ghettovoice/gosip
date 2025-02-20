package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type MIMEVersion string

func (MIMEVersion) CanonicName() Name { return "MIME-Version" }

func (hdr MIMEVersion) RenderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", string(hdr))
	return err
}

func (hdr MIMEVersion) Render() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr MIMEVersion) Clone() Header { return hdr }

func (hdr MIMEVersion) Equal(val any) bool {
	var other MIMEVersion
	switch v := val.(type) {
	case MIMEVersion:
		other = v
	case *MIMEVersion:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return stringutils.LCase(string(hdr)) == stringutils.LCase(string(other))
}

func (hdr MIMEVersion) IsValid() bool { return len(hdr) > 0 }

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
