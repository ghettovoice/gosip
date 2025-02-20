package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type ContentDisposition struct {
	Type   string
	Params Values
}

func (*ContentDisposition) CanonicName() Name { return "Content-Disposition" }

func (hdr *ContentDisposition) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *ContentDisposition) renderValue(w io.Writer) error {
	if _, err := fmt.Fprint(w, hdr.Type); err != nil {
		return err
	}
	return renderHeaderParams(w, hdr.Params, false)
}

func (hdr *ContentDisposition) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *ContentDisposition) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.renderValue(sb)
	return sb.String()
}

func (hdr *ContentDisposition) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	hdr2.Params = hdr.Params.Clone()
	return &hdr2
}

func (hdr *ContentDisposition) Equal(val any) bool {
	var other *ContentDisposition
	switch v := val.(type) {
	case *ContentDisposition:
		other = v
	case ContentDisposition:
		other = &v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return stringutils.LCase(hdr.Type) == stringutils.LCase(other.Type) &&
		compareHeaderParams(hdr.Params, other.Params, map[string]bool{"handling": true})
}

func (hdr *ContentDisposition) IsValid() bool {
	return hdr != nil && grammar.IsToken(hdr.Type) && validateHeaderParams(hdr.Params)
}

func buildFromContentDispositionNode(node *abnf.Node) *ContentDisposition {
	return &ContentDisposition{
		Type:   abnfutils.MustGetNode(node, "disp-type").String(),
		Params: buildFromHeaderParamNodes(node.GetNodes("disp-param"), nil),
	}
}
