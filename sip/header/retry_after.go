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

func (hdr *RetryAfter) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *RetryAfter) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	cw.Fprint(int64(hdr.Delay.Seconds()))
	if hdr.Comment != "" {
		cw.Fprint(" (", hdr.Comment, ")")
	}
	cw.Call(func(w io.Writer) (int, error) {
		return errors.Wrap2(renderHdrParamsTo(w, hdr.Params, false))
	})
	return errors.Wrap2(cw.Result())
}

func (hdr *RetryAfter) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *RetryAfter) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *RetryAfter) String() string {
	return hdr.RenderValue()
}

func (hdr *RetryAfter) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods RetryAfter
		RetryAfter  hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*RetryAfter)(hdr))
}

func (hdr *RetryAfter) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := *hdr
	clone.Params = hdr.Params.Clone()
	return &clone
}

func (hdr *RetryAfter) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *RetryAfter) bool {
		return hdr.Delay == other.Delay &&
			hdr.Comment == other.Comment &&
			compareHdrParams(hdr.Params, other.Params, map[string]bool{
				"duration": true,
			})
	})
}

func (hdr *RetryAfter) IsValid() bool {
	return hdr != nil && validateHdrParams(hdr.Params)
}

func (hdr *RetryAfter) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *RetryAfter) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromRetryAfterNode(node *abnf.Node) *RetryAfter {
	sec, err := strconv.ParseUint(grammar.MustGetNode(node, "delta-seconds").String(), 10, 64)
	if err != nil {
		panic(errors.ErrorfWrap("invalid retry delay value: %w", err))
	}

	var comment string
	if n, _ := node.GetNode("comment"); n != nil {
		comment = strings.Trim(n.String(), "() \r\n\t")
	}
	return &RetryAfter{
		Delay:   time.Duration(sec) * time.Second,
		Comment: comment,
		Params:  buildFromHdrParamNodes(node.GetNodes("retry-param"), nil),
	}
}
