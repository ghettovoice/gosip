package transport

import (
	"iter"

	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// singleTpMetaProvider is a [sip.TransportMetadataProvider] backed by a single TransportMetadata.
type singleTpMetaProvider struct {
	meta sip.TransportMetadata
}

func (p *singleTpMetaProvider) TransportMetadataByProto(proto sip.TransportProto) (sip.TransportMetadata, bool) {
	if !p.meta.Proto.Equal(proto) {
		return sip.TransportMetadata{}, false
	}
	return p.meta, true
}

func (p *singleTpMetaProvider) TransportMetadataByNAPTRService(service string) (sip.TransportMetadata, bool) {
	if !util.EqualFold(p.meta.NAPTRService, service) {
		return sip.TransportMetadata{}, false
	}
	return p.meta, true
}

func (p *singleTpMetaProvider) AllTransportMetadata() iter.Seq[sip.TransportMetadata] {
	return func(yield func(sip.TransportMetadata) bool) { yield(p.meta) }
}
