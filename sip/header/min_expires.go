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

type MinExpires Expires

func (hdr *MinExpires) HeaderName() string { return "Min-Expires" }

func (hdr *MinExpires) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return (*Expires)(hdr).renderValue(w)
}

func (hdr *MinExpires) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *MinExpires) String() string {
	if hdr == nil {
		return "<nil>"
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	(*Expires)(hdr).renderValue(sb)
	return sb.String()
}

func (hdr *MinExpires) Clone() Header {
	if hdr == nil {
		return nil
	}
	return (*MinExpires)((*Expires)(hdr).Clone().(*Expires))
}

func (hdr *MinExpires) Equal(val any) bool {
	var other *MinExpires
	switch v := val.(type) {
	case MinExpires:
		other = &v
	case *MinExpires:
		other = v
	default:
		return false
	}
	return (*Expires)(hdr).Equal((*Expires)(other))
}

func (hdr *MinExpires) IsValid() bool { return (*Expires)(hdr).IsValid() }

func buildFromMinExpiresNode(node *abnf.Node) *MinExpires {
	sec, _ := strconv.ParseUint(utils.MustGetNode(node, "delta-seconds").String(), 10, 64)
	return &MinExpires{time.Duration(sec) * time.Second}
}
