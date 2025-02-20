package header

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
)

type Expires struct {
	time.Duration
}

func (*Expires) CanonicName() Name { return "Expires" }

func (hdr *Expires) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *Expires) renderValue(w io.Writer) error {
	_, err := fmt.Fprint(w, int(hdr.Seconds()))
	return err
}

func (hdr *Expires) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *Expires) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.renderValue(sb)
	return sb.String()
}

func (hdr *Expires) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	return &hdr2
}

func (hdr *Expires) Equal(val any) bool {
	var other *Expires
	switch v := val.(type) {
	case Expires:
		other = &v
	case *Expires:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return int(hdr.Seconds()) == int(other.Seconds())
}

func (hdr *Expires) IsValid() bool { return hdr != nil }

func buildFromExpiresNode(node *abnf.Node) *Expires {
	sec, _ := strconv.ParseUint(abnfutils.MustGetNode(node, "delta-seconds").String(), 10, 64)
	return &Expires{time.Duration(sec) * time.Second}
}
