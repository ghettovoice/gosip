package header

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
)

type Expires struct {
	time.Duration
}

func (hdr *Expires) HeaderName() string { return "Expires" }

func (hdr *Expires) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *Expires) renderValue(w io.Writer) error {
	_, err := fmt.Fprint(w, int(hdr.Seconds()))
	return err
}

func (hdr *Expires) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *Expires) String() string {
	if hdr == nil {
		return "<nil>"
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.renderValue(sb)
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
	sec, _ := strconv.ParseUint(utils.MustGetNode(node, "delta-seconds").String(), 10, 64)
	return &Expires{time.Duration(sec) * time.Second}
}
