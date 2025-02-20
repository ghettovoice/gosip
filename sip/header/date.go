package header

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
)

type Date struct {
	time.Time
}

func (*Date) CanonicName() Name { return "Date" }

func (hdr *Date) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *Date) renderValue(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.UTC().Format(http.TimeFormat))
	return err
}

func (hdr *Date) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *Date) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.renderValue(sb)
	return sb.String()
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

func buildFromDateNode(node *abnf.Node) *Date {
	t, _ := time.Parse(http.TimeFormat, abnfutils.MustGetNode(node, "rfc1123-date").String())
	return &Date{t}
}
