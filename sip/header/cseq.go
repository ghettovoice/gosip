package header

import (
	"fmt"
	"io"
	"strconv"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/stringutils"
)

type CSeq struct {
	SeqNum uint
	Method RequestMethod
}

func (*CSeq) CanonicName() Name { return "CSeq" }

func (hdr *CSeq) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *CSeq) renderValue(w io.Writer) error {
	_, err := fmt.Fprint(w, hdr.SeqNum, " ", hdr.Method)
	return err
}

func (hdr *CSeq) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *CSeq) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.renderValue(sb)
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

	return hdr.SeqNum == other.SeqNum && hdr.Method.Equal(other.Method)
}

func (hdr *CSeq) IsValid() bool { return hdr != nil && hdr.SeqNum > 0 && hdr.Method.IsValid() }

func buildFromCSeqNode(node *abnf.Node) *CSeq {
	seq, _ := strconv.ParseUint(node.Children[2].String(), 10, 64)
	return &CSeq{
		SeqNum: uint(seq),
		Method: RequestMethod(abnfutils.MustGetNode(node, "Method").String()),
	}
}
