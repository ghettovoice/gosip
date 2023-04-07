package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type CSeq struct {
	SeqNum uint
	Method string
}

func (hdr *CSeq) HeaderName() string { return "CSeq" }

func (hdr *CSeq) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *CSeq) renderValue(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.SeqNum, " ", hdr.Method)
	return err
}

func (hdr *CSeq) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *CSeq) String() string {
	if hdr == nil {
		return "<nil>"
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.renderValue(sb)
	return sb.String()
}

func (hdr *CSeq) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	return &hdr2
}

func (hdr *CSeq) Equal(val any) bool {
	var other *CSeq
	switch v := val.(type) {
	case CSeq:
		other = &v
	case *CSeq:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return hdr.SeqNum == other.SeqNum && utils.UCase(hdr.Method) == utils.UCase(other.Method)
}

func (hdr *CSeq) IsValid() bool { return hdr != nil && hdr.SeqNum > 0 && grammar.IsToken(hdr.Method) }

func buildFromCSeqNode(node *abnf.Node) *CSeq {
	seq, _ := strconv.ParseUint(node.Children[2].String(), 10, 64)
	return &CSeq{
		SeqNum: uint(seq),
		Method: utils.MustGetNode(node, "Method").String(),
	}
}
