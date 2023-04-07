package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
)

type MIMEVersion string

func (hdr MIMEVersion) HeaderName() string { return "MIME-Version" }

func (hdr MIMEVersion) RenderHeaderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", string(hdr))
	return err
}

func (hdr MIMEVersion) RenderHeader() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
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
	return utils.LCase(string(hdr)) == utils.LCase(string(other))
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
