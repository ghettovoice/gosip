package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
)

type ContentType MIMEType

func (hdr *ContentType) HeaderName() string { return "Content-Type" }

func (hdr *ContentType) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *ContentType) renderValue(w io.Writer) error {
	_, err := fmt.Fprint(w, MIMEType(*hdr))
	return err
}

func (hdr *ContentType) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *ContentType) String() string {
	if hdr == nil {
		return "<nil>"
	}
	return MIMEType(*hdr).String()
}

func (hdr *ContentType) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := ContentType(MIMEType(*hdr).Clone())
	return &hdr2
}

func (hdr *ContentType) Equal(val any) bool {
	var other *ContentType
	switch v := val.(type) {
	case ContentType:
		other = &v
	case *ContentType:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return MIMEType(*hdr).Equal(MIMEType(*other))
}

func (hdr *ContentType) IsValid() bool { return hdr != nil && MIMEType(*hdr).IsValid() }

func buildFromContentTypeNode(node *abnf.Node) *ContentType {
	mt, ps := buildFromMIMETypeNode(utils.MustGetNode(node, "media-type"))
	for i := range ps {
		mt.Params.Append(ps[i][0], ps[i][1])
	}
	hdr := ContentType(mt)
	return &hdr
}
