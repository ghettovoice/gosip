package header

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type RetryAfter struct {
	Delay   time.Duration
	Comment string
	Params  Values
}

func (*RetryAfter) CanonicName() Name { return "Retry-After" }

func (*RetryAfter) CompactName() Name { return "Retry-After" }

func (hdr *RetryAfter) RenderTo(w io.Writer, _ *RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}

	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(hdr.CanonicName(), ": ")
	cw.Call(hdr.renderValueTo)

	return errors.Wrap2(cw.Result())
}

func (hdr *RetryAfter) renderValueTo(w io.Writer) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(int64(hdr.Delay.Seconds()))

	if hdr.Comment != "" {
		cw.Fprint(" (", hdr.Comment, ")")
	}

	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrParams(w, hdr.Params, false))
	})

	return errors.Wrap2(cw.Result())
}

func (hdr *RetryAfter) Render(opts *RenderOptions) string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.RenderTo(sb, opts) //nolint:errcheck

	return sb.String()
}

func (hdr *RetryAfter) RenderValue() string {
	if hdr == nil {
		return ""
	}

	sb := util.GetStringBuilder()
	defer util.FreeStringBuilder(sb)

	hdr.renderValueTo(sb) //nolint:errcheck

	return sb.String()
}

func (hdr *RetryAfter) String() string { return hdr.RenderValue() }

func (hdr *RetryAfter) Format(f fmt.State, verb rune) {
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
			hideMethods RetryAfter
			RetryAfter  hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), (*RetryAfter)(hdr))

		return
	}
}

func (hdr *RetryAfter) Clone() Header {
	if hdr == nil {
		return nil
	}

	hdr2 := *hdr
	hdr2.Params = hdr.Params.Clone()

	return &hdr2
}

func (hdr *RetryAfter) Equal(val any) bool {
	var other *RetryAfter
	switch v := val.(type) {
	case RetryAfter:
		other = &v
	case *RetryAfter:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return hdr.Delay == other.Delay &&
		hdr.Comment == other.Comment &&
		compareHdrParams(hdr.Params, other.Params, map[string]bool{"duration": true})
}

func (hdr *RetryAfter) IsValid() bool {
	return hdr != nil && validateHdrParams(hdr.Params)
}

func (hdr *RetryAfter) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *RetryAfter) UnmarshalJSON(data []byte) error {
	if hdr == nil {
		return errors.NewInvalidArgumentErrorWrap("nil header")
	}

	gh, err := FromJSON(data)
	if gh == nil {
		*hdr = RetryAfter{}
		return errors.Wrap(err)
	}

	h, ok := gh.(*RetryAfter)
	if !ok {
		*hdr = RetryAfter{}

		ah, ok := gh.(*Any)
		if ok && ah.CanonicName().Equal(hdr.CanonicName()) && len(ah.Value) == 0 {
			return nil
		}

		return newUnexpectHdrTypeErrWrap(gh)
	}

	*hdr = *h

	return nil
}

func buildFromRetryAfterNode(node *abnf.Node) *RetryAfter {
	sec, _ := strconv.ParseUint(grammar.MustGetNode(node, "delta-seconds").String(), 10, 64)

	var comment string
	if n, _ := node.GetNode("comment"); n != nil {
		comment = strings.Trim(n.String(), "() \r\n\t")
	}

	return &RetryAfter{
		Delay:   time.Duration(sec) * time.Second,
		Comment: comment,
		Params:  buildFromHeaderParamNodes(node.GetNodes("retry-param"), nil),
	}
}
