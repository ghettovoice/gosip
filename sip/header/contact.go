package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type Contact []EntityAddr

func (hdr Contact) HeaderName() string { return "Contact" }

func (hdr Contact) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr Contact) renderValue(w io.Writer) error {
	if len(hdr) == 0 {
		_, err := fmt.Fprint(w, "*")
		return err
	}
	return renderHeaderEntries(w, hdr)
}

func (hdr Contact) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr Contact) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr Contact) Clone() Header { return cloneHeaderEntries(hdr) }

func (hdr Contact) Equal(val any) bool {
	var other Contact
	switch v := val.(type) {
	case Contact:
		other = v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(addr1, addr2 EntityAddr) bool { return addr1.Equal(addr2) })
}

func (hdr Contact) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(addr EntityAddr) bool { return !addr.IsValid() })
}

func buildFromContactNode(node *abnf.Node) Contact {
	cntNodes := node.GetNodes("contact-param")
	h := make(Contact, len(cntNodes))
	for i, cntNode := range cntNodes {
		h[i] = buildFromHeaderAddrNode(cntNode, "contact-params")
	}
	return h
}
