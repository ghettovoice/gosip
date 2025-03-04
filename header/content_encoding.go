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

type ContentEncoding []Encoding

func (ContentEncoding) CanonicName() Name { return "Content-Encoding" }

func (ContentEncoding) CompactName() Name { return "e" }

func (hdr ContentEncoding) RenderTo(w io.Writer, opts *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)
	cw.Fprint(hdr.name(opts), ": ")
	cw.Call(hdr.renderValueTo)
	return errtrace.Wrap2(cw.Result())
}

func (hdr ContentEncoding) name(opts *RenderOptions) Name {
	if opts != nil && opts.Compact {
		return hdr.CompactName()
	}
	return hdr.CanonicName()
}

func (hdr ContentEncoding) renderValueTo(w io.Writer) (num int, err error) {
	return errtrace.Wrap2(renderHdrEntries(w, hdr))
}

func (hdr ContentEncoding) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.RenderTo(sb, opts) //nolint:errcheck
	return sb.String()
}

func (hdr ContentEncoding) RenderValue() string {
	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)
	hdr.renderValueTo(sb) //nolint:errcheck
	return sb.String()
}

func (hdr ContentEncoding) String() string { return hdr.RenderValue() }

func (hdr ContentEncoding) Format(f fmt.State, verb rune) {
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
		type hideMethods ContentEncoding
		type ContentEncoding hideMethods
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
	return errtrace.Wrap2(ToJSON(hdr))
}

func (hdr *ContentEncoding) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		*hdr = nil
		if errors.Is(err, errNotHeaderJSON) {
			return nil
		}
		return errtrace.Wrap(err)
	}

	h, ok := gh.(ContentEncoding)
	if !ok {
		*hdr = nil
		return errtrace.Wrap(errorutil.Errorf("unexpected header: got %T, want %T", gh, *hdr))
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
