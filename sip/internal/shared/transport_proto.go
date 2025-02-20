package shared

import (
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type TransportProto string

func (p TransportProto) ToUpper() TransportProto { return stringutils.UCase(p) }

func (p TransportProto) ToLower() TransportProto { return stringutils.LCase(p) }

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
	return stringutils.UCase(p) == stringutils.UCase(other)
}
