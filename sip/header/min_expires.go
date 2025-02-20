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

type MinExpires Expires

func (*MinExpires) CanonicName() Name { return "Min-Expires" }

func (hdr *MinExpires) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return (*Expires)(hdr).renderValue(w)
}

func (hdr *MinExpires) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *MinExpires) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = (*Expires)(hdr).renderValue(sb)
	return sb.String()
}

func (hdr *MinExpires) Clone() Header {
	if hdr == nil {
		return nil
	}
	return (*MinExpires)((*Expires)(hdr).Clone().(*Expires)) //nolint:forcetypeassert
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
	sec, _ := strconv.ParseUint(abnfutils.MustGetNode(node, "delta-seconds").String(), 10, 64)
	return &MinExpires{time.Duration(sec) * time.Second}
}
