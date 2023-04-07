package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type ErrorInfo []ResourceAddr

func (hdr ErrorInfo) HeaderName() string { return "Error-Info" }

func (hdr ErrorInfo) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr ErrorInfo) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr ErrorInfo) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr ErrorInfo) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr ErrorInfo) Clone() Header { return cloneHeaderEntries(hdr) }

func (hdr ErrorInfo) Equal(val any) bool {
	var other ErrorInfo
	switch v := val.(type) {
	case ErrorInfo:
		other = v
	case *ErrorInfo:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(addr1, addr2 ResourceAddr) bool { return addr1.Equal(addr2) })
}

func (hdr ErrorInfo) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(addr ResourceAddr) bool { return !addr.IsValid() })
}

func buildFromErrorInfoNode(node *abnf.Node) ErrorInfo {
	entryNodes := node.GetNodes("error-uri")
	h := make(ErrorInfo, len(entryNodes))
	for i, entryNode := range entryNodes {
		h[i] = buildFromInfoHeaderElemNode(entryNode)
	}
	return h
}
