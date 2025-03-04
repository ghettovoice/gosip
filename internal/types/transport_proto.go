package types

import (
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

type TransportProto string

func (p TransportProto) ToUpper() TransportProto { return util.UCase(p) }

func (p TransportProto) ToLower() TransportProto { return util.LCase(p) }

func (p TransportProto) IsValid() bool { return grammar.IsToken(p) }

func (p TransportProto) Equal(val any) bool {
	var other TransportProto
	switch v := val.(type) {
	case TransportProto:
		other = v
	case *TransportProto:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}
	return util.EqFold(p, other)
}
