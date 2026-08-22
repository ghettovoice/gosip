package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type ContentEncoding []Encoding

func (ContentEncoding) CanonicName() Name { return "Content-Encoding" }

func (ContentEncoding) CompactName() Name { return "e" }

func (hdr ContentEncoding) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.name(opts...), ": ")
	cw.Call(hdr.renderValueTo)
	return errors.Wrap2(cw.Result())
}

func (hdr ContentEncoding) name(opts ...RenderOptions) Name {
	if util.LastSliceElemOr(opts, RenderOptions{}).Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

func (hdr ContentEncoding) renderValueTo(w io.Writer) (num int, err error) {
	return errors.Wrap2(renderHdrEntries(w, hdr))
}

func (hdr ContentEncoding) Render(opts ...RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = hdr.RenderTo(sb, opts...)
	return sb.String()
}

func (hdr ContentEncoding) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = hdr.renderValueTo(sb)
	return sb.String()
}

func (hdr ContentEncoding) String() string { return hdr.RenderValue() }

func (hdr ContentEncoding) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		if f.Flag('+') {
			_, _ = hdr.RenderTo(f)
			return
		}
		fmt.Fprint(f, hdr.String())
		return
	case 'q':
		if f.Flag('+') {
			fmt.Fprint(f, strconv.Quote(hdr.Render()))
			return
		}
		fmt.Fprint(f, strconv.Quote(hdr.String()))
		return
	default:
		type (
			hideMethods     ContentEncoding
			ContentEncoding hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), ContentEncoding(hdr))
		return
	}
}

func (hdr ContentEncoding) Clone() Header { return slices.Clone(hdr) }

func (hdr ContentEncoding) Equal(val any) bool {
	var other ContentEncoding
	switch v := val.(type) {
	case ContentEncoding:
		other = v
	case *ContentEncoding:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return slices.EqualFunc(hdr, other, func(enc1, enc2 Encoding) bool { return enc1.Equal(enc2) })
}

func (hdr ContentEncoding) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(enc Encoding) bool { return !enc.IsValid() })
}

func (hdr ContentEncoding) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ContentEncoding) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		return errors.Wrap(err)
	}

	if gh == nil {
		*hdr = nil
		return nil
	}

	h, ok := gh.(ContentEncoding)
	if !ok {
		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && len(ah.Value) == 0 {
			return nil
		}
		return errors.Wrap(newUnexpectHdrTypeErr(gh))
	}

	*hdr = h
	return nil
}

func buildFromContentEncodingNode(node *abnf.Node) ContentEncoding {
	encNodes := node.GetNodes("token")
	h := make(ContentEncoding, len(encNodes))
	for i, encNode := range encNodes {
		h[i] = Encoding(encNode.String())
	}
	return h
}

type Encoding string

func (enc Encoding) IsValid() bool { return grammar.IsToken(enc) }

func (enc Encoding) Equal(val any) bool {
	var other Encoding
	switch v := val.(type) {
	case Encoding:
		other = v
	case *Encoding:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return util.EqFold(enc, other)
}
