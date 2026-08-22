package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type ContentType MIMEType

func (*ContentType) CanonicName() Name { return "Content-Type" }

func (*ContentType) CompactName() Name { return "c" }

func (hdr *ContentType) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.name(opts...), ": ")
	cw.Fprint(hdr.RenderValue())
	return errors.Wrap2(cw.Result())
}

func (hdr *ContentType) name(opts ...RenderOptions) Name {
	if util.LastSliceElemOr(opts, RenderOptions{}).Compact {
		return hdr.CompactName()
	}

	return hdr.CanonicName()
}

func (hdr *ContentType) Render(opts ...RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	_, _ = hdr.RenderTo(sb, opts...)
	return sb.String()
}

func (hdr *ContentType) String() string { return hdr.RenderValue() }

// RenderValue returns the header value without the name prefix.
func (hdr *ContentType) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return MIMEType(*hdr).String()
}

func (hdr *ContentType) Format(f fmt.State, verb rune) {
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
			hideMethods ContentType
			ContentType hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), (*ContentType)(hdr))
		return
	}
}

func (hdr *ContentType) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := ContentType(MIMEType(*hdr).Clone())
	return &hdr2
}

func (hdr *ContentType) Equal(val any) bool {
	var other *ContentType
	switch v := val.(type) {
	case ContentType:
		other = &v
	case *ContentType:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return MIMEType(*hdr).Equal(MIMEType(*other))
}

func (hdr *ContentType) IsValid() bool { return hdr != nil && MIMEType(*hdr).IsValid() }

func (hdr *ContentType) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *ContentType) UnmarshalJSON(data []byte) error {
	gh, err := FromJSON(data)
	if err != nil {
		return errors.Wrap(err)
	}

	if gh == nil {
		*hdr = ContentType{}
		return nil
	}

	h, ok := gh.(*ContentType)
	if !ok {
		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && (len(ah.Value) == 0 || ah.Value == "/") {
			return nil
		}
		return errors.Wrap(newUnexpectHdrTypeErr(gh))
	}

	*hdr = *h
	return nil
}

func buildFromContentTypeNode(node *abnf.Node) *ContentType {
	mt, ps := buildFromMIMETypeNode(grammar.MustGetNode(node, "media-type"))
	for i := range ps {
		mt.Params.Append(ps[i][0], ps[i][1])
	}
	hdr := ContentType(mt)
	return &hdr
}
