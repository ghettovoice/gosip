package shared

import (
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/util"
)

// TransportProto represents a transport protocol (e.g., TCP, UDP, TLS).
type TransportProto string

func (p TransportProto) Canonic() TransportProto { return util.ToUpper(p) }

func (p TransportProto) ToUpper() TransportProto { return util.ToUpper(p) }

func (p TransportProto) ToLower() TransportProto { return util.ToLower(p) }

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

	return util.EqualFold(p, other)
}
