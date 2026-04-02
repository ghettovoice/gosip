package sip

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/util"
)

var flowTokenSecret [20]byte

func init() {
	if _, err := rand.Read(flowTokenSecret[:]); err != nil {
		panic(errors.ErrorfWrap("init flow token secret: %w", err))
	}
}

// FlowToken is a token that represents a flow between two endpoints (RFC 5626).
// It is used to identify the flow in the transport layer.
type FlowToken struct {
	Transport  TransportProto
	LocalAddr  netip.AddrPort
	RemoteAddr netip.AddrPort
}

func (t FlowToken) Canonic() FlowToken {
	t.Transport = t.Transport.Canonic()
	t.LocalAddr = netutil.UnmapAddrPort(t.LocalAddr)
	t.RemoteAddr = netutil.UnmapAddrPort(t.RemoteAddr)
	return t
}

func (t FlowToken) IsValid() bool {
	return t.Transport.IsValid() && t.LocalAddr.IsValid() && t.RemoteAddr.IsValid()
}

func (t FlowToken) IsZero() bool {
	return t.Transport == "" &&
		t.LocalAddr == netip.AddrPort{} &&
		t.RemoteAddr == netip.AddrPort{}
}

func (t FlowToken) Equal(val any) bool {
	var other FlowToken
	switch v := val.(type) {
	case FlowToken:
		other = v.Canonic()
	case *FlowToken:
		if v == nil {
			return false
		}

		other = v.Canonic()
	default:
		return false
	}

	t = t.Canonic() //nolint:revive

	return t.Transport.Equal(other.Transport) &&
		t.LocalAddr == other.LocalAddr &&
		t.RemoteAddr == other.RemoteAddr
}

// String returns the base64 encoded flow token.
// The token is calculated using example algorithm from RFC 5626 Section 5.2.
func (t FlowToken) String() string {
	if t.IsZero() {
		return ""
	}

	if !t.IsValid() {
		return "invalid flow token"
	}

	s := t.Canonic().payload()
	mac := hmac.New(sha256.New, flowTokenSecret[:])
	mac.Write(s)
	sum := mac.Sum(nil)

	b := make([]byte, 0, 10+len(s))
	b = append(b, sum[:10]...)
	b = append(b, s...)

	return base64.StdEncoding.EncodeToString(b)
}

func (t FlowToken) payload() []byte {
	lip := t.LocalAddr.Addr().AsSlice()
	rip := t.RemoteAddr.Addr().AsSlice()

	b := make([]byte, 0,
		util.SizePrefixedString(t.Transport)+
			util.SizePrefixedString(lip)+2+
			util.SizePrefixedString(rip)+2,
	)
	b = util.AppendPrefixedString(b, t.Transport)
	b = util.AppendPrefixedString(b, lip)
	b = binary.BigEndian.AppendUint16(b, t.LocalAddr.Port())
	b = util.AppendPrefixedString(b, rip)
	b = binary.BigEndian.AppendUint16(b, t.RemoteAddr.Port())

	return b
}

func (t FlowToken) Format(fs fmt.State, verb rune) {
	switch verb {
	case 's':
		fmt.Fprint(fs, t.String())
		return
	case 'q':
		fmt.Fprint(fs, strconv.Quote(t.String()))
		return
	default:
		if !fs.Flag('+') && !fs.Flag('#') {
			fmt.Fprint(fs, t.String())
			return
		}

		type (
			hideMethods FlowToken
			FlowToken   hideMethods
		)

		fmt.Fprintf(fs, fmt.FormatString(fs, verb), FlowToken(t))

		return
	}
}

func (t FlowToken) MarshalText() ([]byte, error) {
	return []byte(t.String()), nil
}

func (t FlowToken) AppendText(b []byte) ([]byte, error) {
	return append(b, t.String()...), nil
}

func (t *FlowToken) UnmarshalText(data []byte) error {
	if t == nil {
		return errors.NewInvalidArgumentErrorWrap("nil flow token")
	}

	if len(data) == 0 {
		*t = FlowToken{}
		return nil
	}

	raw := make([]byte, base64.StdEncoding.DecodedLen(len(data)))

	n, err := base64.StdEncoding.Decode(raw, data)
	if err != nil {
		*t = FlowToken{}
		return errors.NewInvalidArgumentErrorWrap("decode base64: %w", err)
	}

	raw = raw[:n]

	const hmacSize = 10

	if len(raw) <= hmacSize {
		*t = FlowToken{}
		return errors.NewInvalidArgumentErrorWrap("invalid token length %d", len(raw))
	}

	macPart := raw[:hmacSize]
	payload := raw[hmacSize:]

	mac := hmac.New(sha256.New, flowTokenSecret[:])
	mac.Write(payload)

	sum := mac.Sum(nil)
	if !hmac.Equal(macPart, sum[:hmacSize]) {
		*t = FlowToken{}
		return errors.NewInvalidArgumentErrorWrap("invalid token signature")
	}

	token, ok := parseFlowTokenPayload(payload)
	if !ok {
		*t = FlowToken{}
		return errors.NewInvalidArgumentErrorWrap("invalid token payload")
	}

	*t = token

	return nil
}

func parseFlowTokenPayload(payload []byte) (FlowToken, bool) {
	proto, rest, err := util.ConsumePrefixedString(payload)
	if err != nil {
		return FlowToken{}, false
	}

	localIPRaw, rest, err := util.ConsumePrefixedString(rest)
	if err != nil {
		return FlowToken{}, false
	}

	if len(rest) < 2 {
		return FlowToken{}, false
	}

	localPort := binary.BigEndian.Uint16(rest[:2])
	rest = rest[2:]

	remoteIPRaw, rest, err := util.ConsumePrefixedString(rest)
	if err != nil {
		return FlowToken{}, false
	}

	if len(rest) != 2 {
		return FlowToken{}, false
	}

	remotePort := binary.BigEndian.Uint16(rest)

	localIP, ok := netip.AddrFromSlice([]byte(localIPRaw))
	if !ok {
		return FlowToken{}, false
	}

	remoteIP, ok := netip.AddrFromSlice([]byte(remoteIPRaw))
	if !ok {
		return FlowToken{}, false
	}

	return FlowToken{
		Transport:  TransportProto(proto),
		LocalAddr:  netip.AddrPortFrom(localIP.Unmap(), localPort),
		RemoteAddr: netip.AddrPortFrom(remoteIP.Unmap(), remotePort),
	}, true
}

func (t FlowToken) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("transport", t.Transport),
		slog.Any("local_addr", t.LocalAddr),
		slog.Any("remote_addr", t.RemoteAddr),
	)
}
