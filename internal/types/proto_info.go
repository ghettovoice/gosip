package types

import (
	"fmt"
	"strconv"

	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

type ProtoInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (p ProtoInfo) String() string { return p.Name + "/" + p.Version }

func (p ProtoInfo) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		fmt.Fprint(f, p.String())
		return
	case 'q':
		fmt.Fprint(f, strconv.Quote(p.String()))
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			fmt.Fprint(f, p.String())
			return
		}

		type hideMethods ProtoInfo
		type ProtoInfo hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), ProtoInfo(p))
		return
	}
}

func (p ProtoInfo) Equal(val any) bool {
	var other ProtoInfo
	switch v := val.(type) {
	case ProtoInfo:
		other = v
	case *ProtoInfo:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return util.EqFold(p.Name, other.Name) && util.EqFold(p.Version, other.Version)
}

func (p ProtoInfo) IsValid() bool { return grammar.IsToken(p.Name) && grammar.IsToken(p.Version) }

func (p ProtoInfo) IsZero() bool { return p.Name == "" && p.Version == "" }
