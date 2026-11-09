package header

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/ioutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type Timestamp struct {
	RequestTime   time.Time
	ResponseDelay time.Duration
}

func (*Timestamp) CanonicName() Name { return "Timestamp" }

func (*Timestamp) CompactName() Name { return "Timestamp" }

func (hdr *Timestamp) RenderTo(w io.Writer, opts ...RenderOptions) (num int, err error) {
	if hdr == nil {
		return 0, nil
	}
	return errors.Wrap2(renderHdrTo(w, hdr, opts...))
}

func (hdr *Timestamp) renderValTo(w io.Writer, _ ...RenderOptions) (num int, err error) {
	cw := ioutil.GetCountingWriter(w)
	defer ioutil.FreeCountingWriter(cw)

	if !hdr.RequestTime.IsZero() {
		cw.Fprint(formatTstampSecs(hdr.RequestTime.UnixNano()))
	} else {
		cw.Fprint("0")
	}

	if hdr.ResponseDelay > 0 {
		cw.Fprint(" ")
		cw.Fprint(formatTstampSecs(int64(hdr.ResponseDelay)))
	}
	return errors.Wrap2(cw.Result())
}

func (hdr *Timestamp) Render(opts ...RenderOptions) string {
	return render(hdr, opts...)
}

func (hdr *Timestamp) RenderValue() string {
	if hdr == nil {
		return ""
	}
	return renderHdrVal(hdr)
}

func (hdr *Timestamp) String() string {
	return hdr.RenderValue()
}

func (hdr *Timestamp) Format(f fmt.State, verb rune) {
	if formatHdr(f, verb, hdr) {
		return
	}
	type (
		hideMethods Timestamp
		Timestamp   hideMethods
	)
	fmt.Fprintf(f, fmt.FormatString(f, verb), (*Timestamp)(hdr))
}

func (hdr *Timestamp) Clone() Header {
	if hdr == nil {
		return nil
	}

	clone := *hdr
	return &clone
}

func (hdr *Timestamp) Equal(val any) bool {
	return util.EqualPointer(hdr, val, func(other *Timestamp) bool {
		return hdr.RequestTime.Equal(other.RequestTime) && hdr.ResponseDelay == other.ResponseDelay
	})
}

func (hdr *Timestamp) IsValid() bool {
	return hdr != nil &&
		!hdr.RequestTime.IsZero() &&
		hdr.ResponseDelay >= 0
}

func (hdr *Timestamp) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(ToJSON(hdr))
}

func (hdr *Timestamp) UnmarshalJSON(data []byte) error {
	return errors.Wrap(unmarshalHdrJSON(data, hdr, hdr.CanonicName(), isEmptyHdrVal))
}

func buildFromTimestampNode(node *abnf.Node) *Timestamp {
	var hdr Timestamp

	// node.Children: [0]="Timestamp" [1]=HCOLON [2]=1*(DIGIT) [3]=[ "." *(DIGIT) ] [4]=[ LWS delay ]
	intNs, fracNs, err := parseSecsNodes(node.Children[2].Value, node.Children[3].Value)
	if err != nil {
		panic(errors.ErrorfWrap("invalid request time: %w", err))
	}
	hdr.RequestTime = time.Unix(intNs, fracNs).UTC()

	if delNode, ok := node.GetNode("delay"); ok && !delNode.IsEmpty() {
		// delNode.Children: [0]=*(DIGIT) [1]=[ "." *(DIGIT) ]
		intNs, fracNs, err = parseSecsNodes(delNode.Children[0].Value, delNode.Children[1].Value)
		if err != nil {
			panic(errors.ErrorfWrap("invalid response delay: %w", err))
		}

		hdr.ResponseDelay = time.Duration(intNs*int64(time.Second) + fracNs)
	}
	return &hdr
}

// parseSecsNodes parses integer and optional fractional parts of a seconds value
// from raw ABNF node bytes, returning (wholeSecs, fracNanos, error).
// intBytes contains the digits of the integer part; fracBytes contains the optional
// "." *(DIGIT) node value (may be empty if the optional node was absent).
func parseSecsNodes(intBytes, fracBytes []byte) (wholeSecs, fracNanos int64, err error) {
	if len(intBytes) > 0 {
		wholeSecs, err = strconv.ParseInt(string(intBytes), 10, 64)
		if err != nil {
			return 0, 0, errors.Wrap(err)
		}
	}

	// fracBytes is the value of the Optional node "[ '.' *(DIGIT) ]".
	// It is either empty (optional absent) or starts with '.'.
	if len(fracBytes) <= 1 {
		return wholeSecs, 0, nil
	}

	// Drop the leading '.'.
	digits := fracBytes[1:]

	// Pad or truncate to exactly 9 digits for nanoseconds.
	const nsDigits = 9
	var buf [nsDigits]byte
	if len(digits) >= nsDigits {
		copy(buf[:], digits[:nsDigits])
	} else {
		copy(buf[:], digits)
		// Zero-pad on the right.
		for i := len(digits); i < nsDigits; i++ {
			buf[i] = '0'
		}
	}

	fracNanos, err = strconv.ParseInt(string(buf[:]), 10, 64)
	if err != nil {
		return 0, 0, errors.Wrap(err)
	}
	return wholeSecs, fracNanos, nil
}
