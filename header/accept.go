package header

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type Accept []MIMERange

func (Accept) CanonicName() Name { return "Accept" }

func (Accept) CompactName() Name { return "Accept" }

func (hdr Accept) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr Accept) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

func (hdr Accept) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr Accept) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

func (hdr Accept) String() string { return hdr.RenderValue() }

func (hdr Accept) Format(f fmt.State, verb rune) {
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
		type hideMethods Accept
		type Accept hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), Accept(hdr))
		return
	}
}

func (hdr Accept) Clone() Header { return cloneHdrEntries(hdr) }

func (hdr Accept) Equal(val any) bool {
	var other Accept
	switch v := val.(type) {
	case Accept:
		other = v
	case *Accept:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(rng1, rng2 MIMERange) bool { return rng1.Equal(rng2) })
}

func (hdr Accept) IsValid() bool {
	return hdr != nil && !slices.ContainsFunc(hdr, func(rng MIMERange) bool { return !rng.IsValid() })
}

func (hdr Accept) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *Accept) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(Accept)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
	}

	*hdr = h
	return nil
}

func buildFromAcceptNode(node *abnf.Node) Accept {
	rngNodes := node.GetNodes("accept-range")
	hdr := make(Accept, 0, len(rngNodes))
	for _, rngNode := range rngNodes {
		hdr = append(hdr, buildFromAcceptRangeNode(rngNode))
	}
	return hdr
}

type MIMERange struct {
	MIMEType
	Params Values
}

func (rng MIMERange) String() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	sb.WriteString(rng.MIMEType.String())
	renderHdrParams(sb, rng.Params, len(rng.MIMEType.Params) > 0) //nolint:errcheck
	return sb.String()
}

func (rng MIMERange) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		fmt.Fprint(f, rng.String())
		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(rng.String()))
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			fmt.Fprint(f, rng.String())
			return
		}

		type hideMethods MIMERange
		type MIMERange hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), MIMERange(rng))
		return
	}
}

func (rng MIMERange) Equal(val any) bool {
	var other MIMERange
	switch v := val.(type) {
	case MIMERange:
		other = v
	case *MIMERange:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return rng.MIMEType.Equal(other.MIMEType) &&
		compareHdrParams(rng.Params, other.Params, map[string]bool{"q": true})
}

func (rng MIMERange) IsValid() bool {
	return rng.MIMEType.IsValid() &&
		validateHdrParams(rng.Params)
}

func (rng MIMERange) IsZero() bool {
	return rng.MIMEType.IsZero() && len(rng.Params) == 0
}

func (rng MIMERange) Clone() MIMERange {
	rng.MIMEType = rng.MIMEType.Clone()
	rng.Params = rng.Params.Clone()
	return rng
}

func (rng MIMERange) MarshalText() ([]byte, error) {
	return []byte(rng.String()), nil
}

func (rng *MIMERange) UnmarshalText(data []byte) error {
	node, err := grammar.ParseAcceptRange(data)
	if err != nil {
		*rng = MIMERange{}
		if errors.Is(err, grammar.ErrEmptyInput) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	*rng = buildFromAcceptRangeNode(node)
	return nil
}

func buildFromAcceptRangeNode(node *abnf.Node) MIMERange {
	mt, ps := buildFromMIMETypeNode(grammar.MustGetNode(node, "media-range"))
	rng := MIMERange{MIMEType: mt}

	if len(ps) > 0 {
		rng.Params = make(Values, len(ps))
		for _, kv := range ps {
			rng.Params.Append(kv[0], kv[1])
		}
	}

	rng.Params = buildFromHeaderParamNodes(node.GetNodes("accept-param"), rng.Params)
	return rng
}
