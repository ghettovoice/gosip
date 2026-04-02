package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

// AlertInfo implements the Alert-Info header.
type AlertInfo []AlertInfoAddr

func (AlertInfo) CanonicName() Name { return "Alert-Info" }

func (AlertInfo) CompactName() Name { return "Alert-Info" }

func (hdr AlertInfo) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)

	return errors.Wrap2(cw.Result())
}

func (hdr AlertInfo) renderValueTo(w io.Writer) (num int, err error) {
	return errors.Wrap2(renderHdrEntries(w, hdr))
}

func (hdr AlertInfo) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

func (hdr AlertInfo) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.renderValueTo(sb) //nolint:errcheck

	return sb.String()
}

func (hdr AlertInfo) String() string {
	return hdr.RenderValue()
}

func (hdr AlertInfo) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			hdr.RenderTo(f, nil) //nolint:errcheck
			return
		}

		fmt.Fprint(f, hdr.String())

		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render(nil)))
			return
		}

		fmt.Fprint(f, strconv.Quote(hdr.String()))

		return
	default:
		type (
			hideMethods AlertInfo
			AlertInfo   hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), AlertInfo(hdr))

		return
	}
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
	if hdr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil header")
	}

	gh, err := FromJSON(data)
	if gh == nil {
		*hdr = nil
		return errors.Wrap(err)
	}

	h, ok := gh.(AlertInfo)
	if !ok {
		*hdr = nil

		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && len(ah.Value) == 0 {
			return nil
		}

		return newUnexpectHdrTypeErrWrap(gh)
	}

	*hdr = h

	return nil
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
