package sip

import (
	"cmp"
	"encoding/json"
	"iter"
	"maps"
	"slices"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
)

// TransportProto is a transport protocol name.
type TransportProto = types.TransportProto

// TransportFlags is a bitmask that represents transport capabilities.
//
//nolint:recvcheck
type TransportFlags uint

const (
	// TransportFlagReliable indicates that the transport is reliable.
	TransportFlagReliable TransportFlags = 1 << iota
	// TransportFlagSecured indicates that the transport is secured.
	TransportFlagSecured
	// TransportFlagStreamed indicates that the transport is streamed.
	TransportFlagStreamed
)

func (f TransportFlags) Reliable() bool {
	return f&TransportFlagReliable != 0
}

func (f *TransportFlags) SetReliable(val bool) *TransportFlags {
	if val {
		*f |= TransportFlagReliable
	} else {
		*f &^= TransportFlagReliable
	}
	return f
}

func (f TransportFlags) Secured() bool {
	return f&TransportFlagSecured != 0
}

func (f *TransportFlags) SetSecured(val bool) *TransportFlags {
	if val {
		*f |= TransportFlagSecured
	} else {
		*f &^= TransportFlagSecured
	}
	return f
}

func (f TransportFlags) Streamed() bool {
	return f&TransportFlagStreamed != 0
}

func (f *TransportFlags) SetStreamed(val bool) *TransportFlags {
	if val {
		*f |= TransportFlagStreamed
	} else {
		*f &^= TransportFlagStreamed
	}
	return f
}

type transpFlagsData struct {
	Reliable bool `json:"reliable"`
	Secured  bool `json:"secured"`
	Streamed bool `json:"streamed"`
}

func (f TransportFlags) MarshalJSON() ([]byte, error) {
	return errors.Wrap2(json.Marshal(transpFlagsData{
		Reliable: f.Reliable(),
		Secured:  f.Secured(),
		Streamed: f.Streamed(),
	}))
}

func (f *TransportFlags) UnmarshalJSON(data []byte) error {
	var dto transpFlagsData
	if err := json.Unmarshal(data, &dto); err != nil {
		return errors.Wrap(err)
	}

	*f = 0
	f.SetReliable(dto.Reliable).
		SetSecured(dto.Secured).
		SetStreamed(dto.Streamed)
	return nil
}

// TransportMetadata represents transport metadata.
type TransportMetadata struct {
	// Proto is the transport protocol.
	Proto TransportProto `json:"proto"`
	// Network is the network type.
	Network string `json:"network"`
	// DefaultPort is the default port for the transport.
	DefaultPort uint16 `json:"default_port"`
	// Flags is a set of transport flags.
	Flags TransportFlags `json:"flags"`
	// NAPTRService is the NAPTR service string for the transport.
	NAPTRService string `json:"naptr_service"`
	// Priority defines the transport selection order.
	// Lower value means higher priority (e.g. UDP=0, TCP=10, TLS=20).
	Priority uint `json:"priority"`
	// MTU is the maximum Transport Unit.
	// It is used to limit the maximum size of the outbound message.
	// Value 0 means no limit.
	MTU uint
}

func (d TransportMetadata) IsValid() bool {
	return d.Proto.IsValid() && d.Network != "" && d.DefaultPort > 0
}

func (d TransportMetadata) Reliable() bool { return d.Flags.Reliable() }
func (d TransportMetadata) Secured() bool  { return d.Flags.Secured() }
func (d TransportMetadata) Streamed() bool { return d.Flags.Streamed() }

func (d TransportMetadata) Canonic() TransportMetadata {
	d.Proto = d.Proto.Canonic()
	d.Network = util.LCase(d.Network)
	d.NAPTRService = util.UCase(d.NAPTRService)
	return d
}

var (
	udpMeta = TransportMetadata{
		Proto:        "UDP",
		Network:      "udp",
		DefaultPort:  5060,
		NAPTRService: "SIP+D2U",
		Priority:     0,
		MTU:          1500,
	}
	tcpMeta = TransportMetadata{
		Proto:        "TCP",
		Network:      "tcp",
		DefaultPort:  5060,
		Flags:        TransportFlagReliable | TransportFlagStreamed,
		NAPTRService: "SIP+D2T",
		Priority:     100,
	}
	tlsMeta = TransportMetadata{
		Proto:        "TLS",
		Network:      "tcp",
		DefaultPort:  5061,
		Flags:        TransportFlagReliable | TransportFlagStreamed | TransportFlagSecured,
		NAPTRService: "SIPS+D2T",
		Priority:     200,
	}
	sctpMeta = TransportMetadata{
		Proto:        "SCTP",
		Network:      "tcp",
		DefaultPort:  5060,
		Flags:        TransportFlagReliable,
		NAPTRService: "SIP+D2S",
		Priority:     300,
	}
	tlssctpMeta = TransportMetadata{
		Proto:        "TLS-SCTP",
		Network:      "tcp",
		DefaultPort:  5061,
		Flags:        TransportFlagReliable | TransportFlagSecured,
		NAPTRService: "SIPS+D2S",
		Priority:     400,
	}
	wsMeta = TransportMetadata{
		Proto:        "WS",
		Network:      "tcp",
		DefaultPort:  80,
		Flags:        TransportFlagReliable,
		NAPTRService: "SIP+D2W",
		Priority:     500,
	}
	wssMeta = TransportMetadata{
		Proto:        "WSS",
		Network:      "tcp",
		DefaultPort:  443,
		Flags:        TransportFlagReliable | TransportFlagSecured,
		NAPTRService: "SIPS+D2W",
		Priority:     600,
	}
)

// UDPMetadata returns the UDP transport metadata.
func UDPMetadata() TransportMetadata { return udpMeta }

// TCPMetadata returns the TCP transport metadata.
func TCPMetadata() TransportMetadata { return tcpMeta }

// TLSMetadata returns the TLS transport metadata.
func TLSMetadata() TransportMetadata { return tlsMeta }

// SCTPMetadata returns the SCTP transport metadata.
func SCTPMetadata() TransportMetadata { return sctpMeta }

// TLSSCTPMetadata returns the TLS-SCTP transport metadata.
func TLSSCTPMetadata() TransportMetadata { return tlssctpMeta }

// WSMetadata returns the WS transport metadata.
func WSMetadata() TransportMetadata { return wsMeta }

// WSSMetadata returns the WSS transport metadata.
func WSSMetadata() TransportMetadata { return wssMeta }

// TransportMetadataProvider provides transport metadata for different protocols and NAPTR services.
type TransportMetadataProvider interface {
	TransportMetadataByProto(proto TransportProto) (TransportMetadata, bool)
	TransportMetadataByNAPTRService(service string) (TransportMetadata, bool)
	// AllTransportMetadata returns a sequence of all registered transport metadata ordered by priority.
	AllTransportMetadata() iter.Seq[TransportMetadata]
}

type stdTranspMetaProvider struct {
	metas map[TransportProto]TransportMetadata
}

func (p *stdTranspMetaProvider) TransportMetadataByProto(proto TransportProto) (TransportMetadata, bool) {
	meta, ok := p.metas[proto.Canonic()]
	return meta, ok
}

func (p *stdTranspMetaProvider) TransportMetadataByNAPTRService(service string) (TransportMetadata, bool) {
	for _, meta := range p.metas {
		if util.EqFold(meta.NAPTRService, service) {
			return meta, true
		}
	}
	return TransportMetadata{}, false
}

func (p *stdTranspMetaProvider) AllTransportMetadata() iter.Seq[TransportMetadata] {
	return slices.Values(slices.SortedFunc(
		maps.Values(p.metas),
		func(a, b TransportMetadata) int {
			return cmp.Compare(a.Priority, b.Priority)
		},
	))
}

var defTranspMetaProvider = &stdTranspMetaProvider{
	metas: map[TransportProto]TransportMetadata{
		udpMeta.Proto:     udpMeta,
		tcpMeta.Proto:     tcpMeta,
		tlsMeta.Proto:     tlsMeta,
		sctpMeta.Proto:    sctpMeta,
		tlssctpMeta.Proto: tlssctpMeta,
		wsMeta.Proto:      wsMeta,
		wssMeta.Proto:     wssMeta,
	},
}
