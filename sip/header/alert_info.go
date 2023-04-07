package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

// AlertInfo implements Alert-Info header.
type AlertInfo []ResourceAddr

func (hdr AlertInfo) HeaderName() string { return "Alert-Info" }

func (hdr AlertInfo) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr AlertInfo) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr AlertInfo) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr AlertInfo) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr AlertInfo) Clone() Header { return cloneHeaderEntries(hdr) }

func (hdr AlertInfo) Equal(val any) bool {
	var other AlertInfo
	switch v := val.(type) {
	case AlertInfo:
		other = v
	case *AlertInfo:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(addr1, addr2 ResourceAddr) bool { return addr1.Equal(addr2) })
}

func (hdr AlertInfo) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(addr ResourceAddr) bool { return !addr.IsValid() })
}

func buildFromAlertInfoNode(node *abnf.Node) AlertInfo {
	entryNodes := node.GetNodes("alert-param")
	hdr := make(AlertInfo, len(entryNodes))
	for i, entryNode := range entryNodes {
		hdr[i] = buildFromInfoHeaderElemNode(entryNode)
	}
	return hdr
}
