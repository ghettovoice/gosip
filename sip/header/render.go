package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
)

func renderHdrName(
	hdr interface {
		CanonicName() Name
		CompactName() Name
	},
	opts ...RenderOptions,
) Name {
	if util.LastSliceElemOr(opts, RenderOptions{}).Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

func renderHdrTo(w io.Writer, name Name, value func(io.Writer) (int, error)) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(name, ": ")
	cw.Call(value)
	return errors.Wrap2(cw.Result())
}

func renderHdrEntriesString[H ~[]E, E any](hdr H) string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = renderHdrEntries(sb, hdr)
	return sb.String()
}

func formatHdr(f fmt.State, verb rune, hdr Header) bool {
	switch verb {
	case 's':
		if f.Flag('+') {
			_, _ = hdr.RenderTo(f)
		} else {
			fmt.Fprint(f, hdr.RenderValue())
		}
		return true
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(types.Render(hdr)))
		} else {
			fmt.Fprint(f, strconv.Quote(hdr.RenderValue()))
		}
		return true
	default:
		return false
	}
}

func renderValueTo(w io.Writer, value types.Renderer, opts ...RenderOptions) (int, error) {
	if value == nil {
		return 0, nil
	}
	return errors.Wrap2(value.RenderTo(w, opts...))
}

func renderValue(value types.Renderer) string {
	if value == nil {
		return ""
	}
	return types.Render(value)
}
