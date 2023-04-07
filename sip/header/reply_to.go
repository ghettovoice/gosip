package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type ReplyTo EntityAddr

func (hdr *ReplyTo) HeaderName() string { return "Reply-To" }

func (hdr *ReplyTo) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	_, err := fmt.Fprint(w, hdr.HeaderName(), ": ", EntityAddr(*hdr))
	return err
}

func (hdr *ReplyTo) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *ReplyTo) String() string {
	if hdr == nil {
		return "<nil>"
	}
	return EntityAddr(*hdr).String()
}

func (hdr *ReplyTo) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := ReplyTo(EntityAddr(*hdr).Clone())
	return &hdr2
}

func (hdr *ReplyTo) Equal(val any) bool {
	var other *ReplyTo
	switch v := val.(type) {
	case ReplyTo:
		other = &v
	case *ReplyTo:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return EntityAddr(*hdr).Equal(EntityAddr(*other))
}

func (hdr *ReplyTo) IsValid() bool { return hdr != nil && EntityAddr(*hdr).IsValid() }

func buildFromReplyToNode(node *abnf.Node) *ReplyTo {
	hdr := ReplyTo(buildFromHeaderAddrNode(node, "generic-param"))
	return &hdr
}
