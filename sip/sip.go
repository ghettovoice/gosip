// Package sip implements SIP protocol as described in RFC 3261.
package sip

import (
	"math"
	"time"

	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip/common"
)

const (
	maxMsgSize = math.MaxUint16 // max read buffer size, max size of the IP packet
)

var (
	Proto20 = Proto{Name: "SIP", Version: "2.0"}

	T1    = 500 * time.Millisecond
	TimeA = T1
	TimeB = 64 * T1
	TimeC = 600 * T1
)

type Proto = common.ProtoInfo

type Values = common.Values

type Addr = common.Addr

func Host(host string) Addr { return common.Host(host) }

func HostPort(host string, port uint16) Addr { return common.HostPort(host, port) }

type Metadata map[string]any

var (
	TransportField = "transport_proto"
	// RemoteAddrField is the field name of the message remote address.
	RemoteAddrField = "remote_addr"
	// LocalAddrField is the field name of the message local address.
	LocalAddrField = "local_addr"
	// RequestTstampField is the field name of the timestamp when the request was received or sent.
	RequestTstampField = "request_tstamp"
	// ResponseTstampField is the field name of the timestamp when the response was received or sent.
	ResponseTstampField = "response_tstamp"
)

var noopLogger = &log.NoopLogger{}
