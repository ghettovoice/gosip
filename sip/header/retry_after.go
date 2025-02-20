package header

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
)

type RetryAfter struct {
	Delay   time.Duration
	Comment string
	Params  Values
}

func (*RetryAfter) CanonicName() Name { return "Retry-After" }

func (hdr *RetryAfter) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *RetryAfter) renderValue(w io.Writer) error {
	if _, err := fmt.Fprint(w, int(hdr.Delay.Seconds())); err != nil {
		return err
	}
	if hdr.Comment != "" {
		_, _ = fmt.Fprint(w, " (", hdr.Comment, ")")
	}
	return renderHeaderParams(w, hdr.Params, false)
}

func (hdr *RetryAfter) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *RetryAfter) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.renderValue(sb)
	return sb.String()
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

	return int(hdr.Delay.Seconds()) == int(other.Delay.Seconds()) && hdr.Comment == other.Comment &&
		compareHeaderParams(hdr.Params, other.Params, map[string]bool{"duration": true})
}

func (hdr *RetryAfter) IsValid() bool {
	return hdr != nil && hdr.Delay >= 0 && validateHeaderParams(hdr.Params)
}

func buildFromRetryAfterNode(node *abnf.Node) *RetryAfter {
	sec, _ := strconv.ParseUint(abnfutils.MustGetNode(node, "delta-seconds").String(), 10, 64)
	return &RetryAfter{
		Delay:   time.Duration(sec) * time.Second,
		Comment: strings.Trim(abnfutils.MustGetNode(node, "comment").String(), "()"),
		Params:  buildFromHeaderParamNodes(node.GetNodes("retry-param"), nil),
	}
}
