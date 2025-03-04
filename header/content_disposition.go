package header

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
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

func (hdr *ContentDisposition) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr *ContentDisposition) renderValueTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.Type)
	cw.Call(func(w io.Writer) (int, error) { return errtrace.Wrap2(renderHdrParams(w, hdr.Params, false)) })
	return errtrace.Wrap2(cw.Result())
}

func (hdr *ContentDisposition) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr *ContentDisposition) String() string { return hdr.RenderValue() }

func (hdr *ContentDisposition) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

func (hdr *ContentDisposition) Format(f fmt.State, verb rune) {
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
		type hideMethods ContentDisposition
		type ContentDisposition hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*ContentDisposition)(hdr))
		return
	}
}

func (hdr *ContentDisposition) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := *hdr
	hdr2.Params = hdr.Params.Clone()
	return &hdr2
}

func (hdr *ContentDisposition) Equal(val any) bool {
	var other *ContentDisposition
	switch v := val.(type) {
	case *ContentDisposition:
		other = v
	case ContentDisposition:
		other = &v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return util.EqFold(hdr.Type, other.Type) &&
		compareHdrParams(hdr.Params, other.Params, map[string]bool{"handling": true})
}

func (hdr *ContentDisposition) IsValid() bool {
	return hdr != nil && grammar.IsToken(hdr.Type) && validateHdrParams(hdr.Params)
}

func (hdr *ContentDisposition) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

var zeroContentDisposition ContentDisposition

func (hdr *ContentDisposition) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = zeroContentDisposition
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(*ContentDisposition)
	if !ok {
		*hdr = zeroContentDisposition
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, hdr))
	}

	*hdr = *h
	return nil
}

func buildFromContentDispositionNode(node *abnf.Node) *ContentDisposition {
	return &ContentDisposition{
		Type:   grammar.MustGetNode(node, "disp-type").String(),
		Params: buildFromHeaderParamNodes(node.GetNodes("disp-param"), nil),
	}
}
