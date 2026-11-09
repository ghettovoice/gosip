package header

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

type Date struct {
	time.Time
}

func (*Date) CanonicName() Name { return "Date" }

func (*Date) CompactName() Name { return "Date" }

func (hdr *Date) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *Date) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	return errors.Wrap2(fmt.Fprint(w, hdr.UTC().Format(http.TimeFormat)))
}

func (hdr *Date) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *Date) String() string {
	return hdr.RenderValue()
}

func (hdr *Date) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *Date) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Date
		Date        hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*Date)(hdr))
}

func (hdr *Date) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := *hdr
	return &clone
}

func (hdr *Date) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *Date) bool {
		return hdr.Time.Equal(other.Time)
	})
}

func (hdr *Date) IsValid() bool {
	return hdr != nil && !hdr.IsZero()
}

func (hdr *Date) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Date) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromDateNode(node *abnf.Node) *Date {
	t, err := time.Parse(http.TimeFormat, grammar.MustGetNode(node, "rfc1123-date").String())
	if err != nil {
		panic(errors.ErrorfWrap("invalid data value: %w", err))
	}
	return &Date{t}
}
