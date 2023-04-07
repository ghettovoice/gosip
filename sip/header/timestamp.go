package header

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/pool"
)

type Timestamp struct {
	ReqTstamp, ResDelay time.Duration
}

func (hdr *Timestamp) HeaderName() string { return "Timestamp" }

func (hdr *Timestamp) RenderHeaderTo(w io.Writer) error {
	if hdr == nil {
		return nil
	}
	if _, err := fmt.Fprint(w, hdr.HeaderName(), ": "); err != nil {
		return err
	}
	return hdr.renderValue(w)
}

func (hdr *Timestamp) renderValue(w io.Writer) error {
	if _, err := fmt.Fprint(w, hdr.ReqTstamp.Seconds()); err != nil {
		return err
	}
	if hdr.ResDelay > 0 {
		if _, err := fmt.Fprint(w, " ", hdr.ResDelay.Seconds()); err != nil {
			return err
		}
	}
	return nil
}

func (hdr *Timestamp) RenderHeader() string {
	if hdr == nil {
		return ""
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.RenderHeaderTo(sb)
	return sb.String()
}

func (hdr *Timestamp) String() string {
	if hdr == nil {
		return "<nil>"
	}
	sb := pool.NewStrBldr()
	defer pool.FreeStrBldr(sb)
	hdr.renderValue(sb)
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

	return hdr.ReqTstamp == other.ReqTstamp && hdr.ResDelay == other.ResDelay
}

func (hdr *Timestamp) IsValid() bool {
	return hdr != nil && hdr.ReqTstamp >= 0 && hdr.ResDelay >= 0
}

func buildFromTimestampNode(node *abnf.Node) *Timestamp {
	var hdr Timestamp
	sec, _ := strconv.ParseFloat(string(append(node.Children[2].Value, node.Children[3].Value...)), 64)
	hdr.ReqTstamp = time.Duration(sec * float64(time.Second))
	if delNode := node.GetNode("delay"); !delNode.IsEmpty() {
		sec, _ = strconv.ParseFloat(string(append(delNode.Children[0].Value, delNode.Children[1].Value...)), 64)
		hdr.ResDelay = time.Duration(sec * float64(time.Second))
	}
	return &hdr
}
