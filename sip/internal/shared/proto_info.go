package shared

import (
	"github.com/ghettovoice/gosip/internal/stringutils"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
)

type ProtoInfo struct {
	Name, Version string
}

func (p ProtoInfo) String() string { return p.Name + "/" + p.Version }

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
	return stringutils.LCase(p.Name) == stringutils.LCase(other.Name) && stringutils.LCase(p.Version) == stringutils.LCase(other.Version)
}

func (p ProtoInfo) IsValid() bool { return grammar.IsToken(p.Name) && grammar.IsToken(p.Version) }

func (p ProtoInfo) IsZero() bool { return p.Name == "" && p.Version == "" }
