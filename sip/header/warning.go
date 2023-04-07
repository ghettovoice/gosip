package header

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type Warning []WarningItem

func (hdr Warning) HeaderName() string { return "Warning" }

func (hdr Warning) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr Warning) renderValue(w io.Writer) error { return renderHeaderEntries(w, hdr) }

func (hdr Warning) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr Warning) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	sb.WriteByte('[')
	hdr.renderValue(sb)
	sb.WriteByte(']')
	return sb.String()
}

func (hdr Warning) Clone() Header { return cloneHeaderEntries(hdr) }

func (hdr Warning) Equal(val any) bool {
	var other Warning
	switch v := val.(type) {
	case Warning:
		other = v
	case *Warning:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return slices.EqualFunc(hdr, other, func(wrn1, wrn2 WarningItem) bool { return wrn1.Equal(wrn2) })
}

func (hdr Warning) IsValid() bool {
	return len(hdr) > 0 && !slices.ContainsFunc(hdr, func(wrn WarningItem) bool { return !wrn.IsValid() })
}

type WarningItem struct {
	Code  uint
	Agent string
	Text  string
}

func (wrn WarningItem) String() string {
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	fmt.Fprintf(sb, "%d %s \"%s\"", wrn.Code, wrn.Agent, wrn.Text)
	return sb.String()
}

func (wrn WarningItem) Equal(val any) bool {
	var other WarningItem
	switch v := val.(type) {
	case WarningItem:
		other = v
	case *WarningItem:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return wrn.Code == other.Code && utils.LCase(wrn.Agent) == utils.LCase(other.Agent) && wrn.Text == other.Text
}

func (wrn WarningItem) IsValid() bool { return wrn.Code > 0 && grammar.IsToken(wrn.Agent) }

func (wrn WarningItem) IsZero() bool { return wrn.Code == 0 && wrn.Agent == "" && wrn.Text == "" }

func (wrn WarningItem) Clone() WarningItem { return wrn }

func buildFromWarningNode(node *abnf.Node) Warning {
	warnNodes := node.GetNodes("warning-value")
	h := make(Warning, len(warnNodes))
	for i, warnNode := range warnNodes {
		c, _ := strconv.ParseUint(utils.MustGetNode(warnNode, "warn-code").String(), 10, 64)
		h[i] = WarningItem{
			Code:  uint(c),
			Agent: utils.MustGetNode(warnNode, "warn-agent").String(),
			Text:  grammar.Unquote(warnNode.Children[4].String()),
		}
	}
	return h
}
