package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

// Any implements a generic header.
// It can be used to parse/append any headers that aren't natively supported by the lib.
type Any struct {
	Name, Value string
}

func (hdr *Any) HeaderName() string { return CanonicName(hdr.Name) }

func (hdr *Any) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", hdr.Value)
	return err
}

func (hdr *Any) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *Any) String() string {
	if hdr == nil {
		return "<nil>"
	}
	return hdr.Value
}

func (hdr *Any) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	return &hdr2
}

func (hdr *Any) Equal(val any) bool {
	var other *Any
	switch v := val.(type) {
	case Any:
		other = &v
	case *Any:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return CanonicName(hdr.Name) == CanonicName(other.Name) && hdr.Value == other.Value
}

func (hdr *Any) IsValid() bool { return hdr != nil && grammar.IsToken(hdr.Name) }

func buildFromExtensionHeaderNode(node *abnf.Node) *Any {
	return &Any{node.Children[0].String(), utils.MustGetNode(node, "header-value").String()}
}
