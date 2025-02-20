package header

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/stringutils"
)

type Timestamp struct {
	ReqTime  time.Time
	ResDelay time.Duration
}

func (*Timestamp) CanonicName() Name { return "Timestamp" }

func (hdr *Timestamp) RenderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.CanonicName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *Timestamp) renderValue(w io.Writer) error {
	if !hdr.ReqTime.IsZero() {
		if _, err := fmt.Fprintf(w, "%.3f", float64(hdr.ReqTime.UnixNano())/1e9); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprint(w, "0"); err != nil {
			return err
		}
	}
	if hdr.ResDelay > 0 {
		if _, err := fmt.Fprintf(w, " %.3f", hdr.ResDelay.Seconds()); err != nil {
			return err
		}
	}
	return nil
}

func (hdr *Timestamp) Render() string {
	if hdr == nil {
		return ""
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.RenderTo(sb)
	return sb.String()
}

func (hdr *Timestamp) String() string {
	if hdr == nil {
		return nilTag
	}
	sb := stringutils.NewStrBldr()
	defer stringutils.FreeStrBldr(sb)
	_ = hdr.renderValue(sb)
	return sb.String()
}

func (hdr *Timestamp) Clone() Header {
	if hdr == nil {
		return nil
	}
	hdr2 := *hdr
	return &hdr2
}

func (hdr *Timestamp) Equal(val any) bool {
	var other *Timestamp
	switch v := val.(type) {
	case Timestamp:
		other = &v
	case *Timestamp:
		other = v
	default:
		return false
	}

	if hdr == other {
		return true
	} else if hdr == nil || other == nil {
		return false
	}

	return hdr.ReqTime.Equal(other.ReqTime) && hdr.ResDelay == other.ResDelay
}

func (hdr *Timestamp) IsValid() bool {
	return hdr != nil && !hdr.ReqTime.IsZero() && hdr.ResDelay >= 0
}

func buildFromTimestampNode(node *abnf.Node) *Timestamp {
	var hdr Timestamp
	sec, _ := strconv.ParseFloat(string(append(node.Children[2].Value, node.Children[3].Value...)), 64)
	hdr.ReqTime = time.UnixMilli(int64(sec * 1e3)).UTC()
	if delNode := node.GetNode("delay"); !delNode.IsEmpty() {
		sec, _ = strconv.ParseFloat(string(append(delNode.Children[0].Value, delNode.Children[1].Value...)), 64)
		hdr.ResDelay = time.Duration(sec * float64(time.Second))
	}
	return &hdr
}
