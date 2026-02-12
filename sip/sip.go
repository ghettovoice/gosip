package sip

//go:generate errtrace -w .

import (
	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
)

// RenderOptions represents options for rendering SIP messages.
// See [types.RenderOptions].
type RenderOptions = types.RenderOptions

// ProtoInfo represents SIP protocol information.
// See [types.ProtoInfo].
type ProtoInfo = types.ProtoInfo

var protoVer20 = ProtoInfo{Name: "SIP", Version: "2.0"}

// ProtoVer20 returns the SIP 2.0 protocol information.
func ProtoVer20() ProtoInfo { return protoVer20 }

// Addr represents an address.
// See [types.Addr].
type Addr = types.Addr

// Host returns an [Addr] containing the provided host and no port.
func Host(host string) Addr { return types.Host(host) }

// HostPort returns an [Addr] containing the provided host and port.
func HostPort(host string, port uint16) Addr { return types.HostPort(host, port) }

// ParseAddr parses a "host[:port]" string into an [Addr].
func ParseAddr(s string) (Addr, error) {
	return errtrace.Wrap2(types.ParseAddr(s))
}

// Values represents a map of string keys to string values.
// See [types.Values].
type Values = types.Values

// GenerateTag generates a tag to be used in From/To headers.
// Tag is a random string of specified length.
// If length is not specified, it defaults to 8.
func GenerateTag(length uint) string {
	l := 8
	if length > 0 {
		l = int(length)
	}
	return util.RandStringLC(l)
}

// GenerateCallID generates a Call-ID.
// Call-ID is a random string of specified length or 16 if not specified.
// If host is provided, it is appended after "@".
func GenerateCallID(length uint, host string) string {
	l := 16
	if length > 0 {
		l = int(length)
	}
	if len(host) > 0 {
		return util.RandStringLC(l) + "@" + host
	}
	return util.RandStringLC(l)
}

// MagicCookie is a constant string defined in RFC 3261.
// It is used as a prefix for a branch in Via header.
const MagicCookie = "z9hG4bK"

// GenerateBranch generates a branch for a Via header.
// Branch is a random string of specified length or 16 if not specified.
func GenerateBranch(length uint) string {
	l := 16
	if length > 0 {
		l = int(length)
	}
	return MagicCookie + "." + util.RandStringLC(l)
}

// IsRFC3261Branch checks whether a branch is a valid RFC 3261 branch.
func IsRFC3261Branch(branch string) bool {
	return len(branch) > len(MagicCookie) && branch[:len(MagicCookie)] == MagicCookie
}
