package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type CallInfo []ResourceAddr

func (hdr CallInfo) HeaderName() string { return "Call-Info" }

func (hdr CallInfo) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr CallInfo) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr CallInfo) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr CallInfo) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr CallInfo) Clone() Header { return cloneHeaderEntries(hdr) }

func (hdr CallInfo) Equal(val any) bool {
	var other CallInfo
	switch v := val.(type) {
	case CallInfo:
		other = v
	case *CallInfo:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(addr1, addr2 ResourceAddr) bool { return addr1.Equal(addr2) })
}

func (hdr CallInfo) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(addr ResourceAddr) bool { return !addr.IsValid() })
}

func buildFromCallInfoNode(node *abnf.Node) CallInfo {
	entryNodes := node.GetNodes("info")
	h := make(CallInfo, len(entryNodes))
	for i, entryNode := range entryNodes {
		h[i] = buildFromInfoHeaderElemNode(entryNode)
	}
	return h
}
