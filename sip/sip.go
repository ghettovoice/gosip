// Package sip implements SIP protocol as described in RFC 3261.
package sip

import (
	"errors"
	"regexp"
	"time"

	"github.com/gabriel-vasile/mimetype"

	"github.com/ghettovoice/gosip/sip/internal/shared"
)

var (
	ErrInvalidMessage  = errors.New("invalid message")
	ErrMessageTooLarge = errors.New("message too large")
)

type ProtoInfo = shared.ProtoInfo

var protoVer20 = ProtoInfo{Name: "SIP", Version: "2.0"}

func ProtoVer20() ProtoInfo { return protoVer20 }

// SIP timers.
var (
	// T1 is the message RTT estimate.
	T1 = 500 * time.Millisecond
	// T2 is the maximum retransmit interval for non-INVITE requests and INVITE responses.
	T2 = 4 * time.Second
	// T4 is the maximum duration a message will remain in the network.
	T4 = 5 * time.Second

	// TimeD is the wait duration for response retransmits via unreliable transport.
	TimeD = 32 * time.Second
)

// Time100 is the timeout for automatic 100 Trying response on INVITE.
const Time100 = 200 * time.Millisecond

// TimeA returns initial INVITE request retransmit interval for unreliable transport.
// It is equal to [T1].
func TimeA() time.Duration { return T1 }

// TimeB returns INVITE transaction timeout.
// It is equal to 64*[T1].
func TimeB() time.Duration { return 64 * T1 }

// TimeC returns the INVITE transaction timeout on proxy.
// It is equal to 600*[T1].
func TimeC() time.Duration { return 600 * T1 }

// TimeE returns initial non-INVITE request retransmit interval for unreliable transport.
// It is equal to [T1].
func TimeE() time.Duration { return T1 }

// TimeF returns non-INVITE transaction timeout.
// It is equal to 64*[T1].
func TimeF() time.Duration { return 64 * T1 }

// TimeG returns initial INVITE response retransmit interval for any transport.
// It is equal to [T1].
func TimeG() time.Duration { return T1 }

// TimeH returns timeout for ACK request receipt.
// It is equal to 64*[T1].
func TimeH() time.Duration { return 64 * T1 }

// TimeI returns wait duration for ACK request retransmits via unreliable transport.
// It is equal to [T4].
func TimeI() time.Duration { return T4 }

// TimeJ returns wait duration for non-INVITE request retransmits via unreliable transport.
// It is equal to 64*[T4].
func TimeJ() time.Duration { return 64 * T4 }

// TimeK returns wait duration for response retransmits via unreliable transport.
// It is equal to [T4].
func TimeK() time.Duration { return T4 }

func TimeL() time.Duration { return 64 * T4 }

func TimeM() time.Duration { return 64 * T4 }

func init() {
	sdpRegex := regexp.MustCompile(`v=0\r?\no=.*\r?\ns=.*\r?\n`)
	mimetype.Extend(func(raw []byte, limit uint32) bool { return sdpRegex.Match(raw) }, "application/sdp", ".sdp")
	// TODO add other common mime-type detectors (DTMF, etc)
}
