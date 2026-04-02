package header

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type Date struct {
	time.Time
}

func (*Date) CanonicName() Name { return "Date" }

func (*Date) CompactName() Name { return "Date" }

func (hdr *Date) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)

	return errors.Wrap2(cw.Result())
}

func (hdr *Date) renderValueTo(w io.Writer) (num int, err error) {
	return errors.Wrap2(fmt.Fprint(w, hdr.UTC().Format(http.TimeFormat)))
}

func (hdr *Date) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

func (hdr *Date) String() string { return hdr.RenderValue() }

func (hdr *Date) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.renderValueTo(sb) //nolint:errcheck

	return sb.String()
}

func (hdr *Date) Format(f fmt.State, verb rune) {
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
			hideMethods Date
			Date        hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), (*Date)(hdr))

		return
	}
}

func (hdr *Date) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := *hdr

	return &hdr2
}

func (hdr *Date) Equal(val any) bool {
	var other *Date
	switch v := val.(type) {
	case Date:
		other = &v
	case *Date:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return hdr.Time.Equal(other.Time)
}

func (hdr *Date) IsValid() bool { return hdr != nil && !hdr.IsZero() }

func (hdr *Date) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Date) UnmarshalJSON(data []byte) error {
	if hdr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil header")
	}

	gh, err := FromJSON(data)
	if gh == nil {
		*hdr = Date{}
		return errors.Wrap(err)
	}

	h, ok := gh.(*Date)
	if !ok {
		*hdr = Date{}

		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && len(ah.Value) == 0 {
			return nil
		}

		return newUnexpectHdrTypeErrWrap(gh)
	}

	*hdr = *h

	return nil
}

func buildFromDateNode(node *abnf.Node) *Date {
	t, _ := time.Parse(http.TimeFormat, grammar.MustGetNode(node, "rfc1123-date").String())
	return &Date{t}
}
