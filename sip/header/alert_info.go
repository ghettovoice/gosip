package header

import (
	"fmt"
	"io"
	"slices"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
)

// AlertInfo implements the Alert-Info header.
type AlertInfo []AlertInfoAddr

func (AlertInfo) CanonicName() Name { return "Alert-Info" }

func (AlertInfo) CompactName() Name { return "Alert-Info" }

func (hdr AlertInfo) RenderTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr.CanonicName(), func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrEntries(w, hdr))
	}))
}

func (hdr AlertInfo) Render(opts ...RenderOptions) string {
	return types.Render(hdr, opts...)
}

func (hdr AlertInfo) RenderValue() string { return renderHdrEntriesString(hdr) }

func (hdr AlertInfo) String() string { return hdr.RenderValue() }

func (hdr AlertInfo) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods AlertInfo
		AlertInfo   hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), AlertInfo(hdr))
}

func (hdr AlertInfo) Clone() Header { return cloneHdrEntries(hdr) }

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

	return slices.EqualFunc(hdr, other, func(addr1, addr2 AlertInfoAddr) bool { return addr1.Equal(addr2) })
}

func (hdr AlertInfo) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(addr AlertInfoAddr) bool { return !addr.IsValid() })
}

func (hdr AlertInfo) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *AlertInfo) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), emptyHdrValue))
}

func buildFromAlertInfoNode(node *abnf.Node) AlertInfo {
	entryNodes := node.GetNodes("alert-param")
	hdr := make(AlertInfo, len(entryNodes))
	for i, entryNode := range entryNodes {
		hdr[i] = buildFromInfoAddrNode(entryNode)
	}
	return hdr
}

type AlertInfoAddr = InfoAddr
