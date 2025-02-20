package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type Subject string

func (Subject) CanonicName() Name { return "Subject" }

func (hdr Subject) RenderTo(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.CanonicName(), ": ", string(hdr))
	return err
}

func (hdr Subject) Render() string {
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr Subject) Clone() Header { return hdr }

func (hdr Subject) Equal(val any) bool {
	var other Subject
	switch v := val.(type) {
	case Subject:
		other = v
	case *Subject:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return hdr == other
}

func (Subject) IsValid() bool { return true }

func buildFromSubjectNode(node *abnf.Node) Subject {
	return Subject(node.Children[2].Value)
}
