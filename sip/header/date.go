package header

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
)

type Date struct {
	time.Time
}

func (hdr *Date) HeaderName() string { return "Date" }

func (hdr *Date) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *Date) renderValue(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.UTC().Format(http.TimeFormat))
	return err
}

func (hdr *Date) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *Date) String() string {
	if hdr == nil {
		return "<nil>"
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.renderValue(sb)
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
	t, _ := time.Parse(http.TimeFormat, utils.MustGetNode(node, "rfc1123-date").String())
	return &Date{t}
}
