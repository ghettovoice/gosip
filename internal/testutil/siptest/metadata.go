// Package siptest provides reusable test helpers for SIP.
package siptest

import (
	"iter"

	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// MetadataProvider looks up transport metadata from the configured list.
type MetadataProvider struct {
	// Metas lists transport metadata in lookup and iteration order.
	Metas []sip.TransportMetadata
}

// TransportMetadataByProto returns the first metadata entry matching proto.
func (p MetadataProvider) TransportMetadataByProto(proto sip.TransportProto) (sip.TransportMetadata, bool) {
	for _, meta := range p.Metas {
		if meta.Proto.Equal(proto) {
			return meta, true
		}
	}
	return sip.TransportMetadata{}, false
}

// TransportMetadataByNAPTRService returns the first case-insensitive service match.
func (p MetadataProvider) TransportMetadataByNAPTRService(service string) (sip.TransportMetadata, bool) {
	for _, meta := range p.Metas {
		if util.EqFold(meta.NAPTRService, service) {
			return meta, true
		}
	}
	return sip.TransportMetadata{}, false
}

// AllTransportMetadata yields the configured entries in order.
func (p MetadataProvider) AllTransportMetadata() iter.Seq[sip.TransportMetadata] {
	return func(yield func(sip.TransportMetadata) bool) {
		for _, meta := range p.Metas {
			if !yield(meta) {
				return
			}
		}
	}
}
