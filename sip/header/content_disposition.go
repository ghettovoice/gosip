package header

import (
	"fmt"
	"io"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type ContentDisposition struct {
	Type   string
	Params Values
}

func (*ContentDisposition) CanonicName() Name { return "Content-Disposition" }

func (*ContentDisposition) CompactName() Name { return "Content-Disposition" }

func (hdr *ContentDisposition) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *ContentDisposition) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.Type)
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrParamsTo(w, hdr.Params, false))
	})
	return errors.Wrap2(cw.Result())
}

func (hdr *ContentDisposition) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *ContentDisposition) String() string {
	return hdr.RenderValue()
}

func (hdr *ContentDisposition) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *ContentDisposition) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods        ContentDisposition
		ContentDisposition hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*ContentDisposition)(hdr))
}

func (hdr *ContentDisposition) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := *hdr
	clone.Params = hdr.Params.Clone()
	return &clone
}

func (hdr *ContentDisposition) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *ContentDisposition) bool {
		return util.EqualFold(hdr.Type, other.Type) &&
			compareHdrParams(hdr.Params, other.Params, map[string]bool{
				"handling": true,
			})
	})
}

func (hdr *ContentDisposition) IsValid() bool {
	return hdr != nil &&
		grammar.IsToken(hdr.Type) &&
		validateHdrParams(hdr.Params)
}

func (hdr *ContentDisposition) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ContentDisposition) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromContentDispositionNode(node *abnf.Node) *ContentDisposition {
	return &ContentDisposition{
		Type:   grammar.MustGetNode(node, "disp-type").String(),
		Params: buildFromHdrParamNodes(node.GetNodes("disp-param"), nil),
	}
}
