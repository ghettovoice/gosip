package common

import (
	"github.com/ghettovoice/gosip/internal/utils"
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
	default:
		return false
	}
	return utils.LCase(p.Name) == utils.LCase(other.Name) && utils.LCase(p.Version) == utils.LCase(other.Version)
}

func (p ProtoInfo) IsValid() bool { return grammar.IsToken(p.Name) && grammar.IsToken(p.Version) }

func (p ProtoInfo) IsZero() bool { return p.Name == "" && p.Version == "" }
