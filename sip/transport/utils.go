package transport

import (
	"iter"

	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// singleTranspMetaProvider is a [sip.TransportMetadataProvider] backed by a single TransportMetadata.
type singleTranspMetaProvider struct {
	meta sip.TransportMetadata
}

func (p *singleTranspMetaProvider) TransportMetadataByProto(proto sip.TransportProto) (sip.TransportMetadata, bool) {
	if !p.meta.Proto.Equal(proto) {
		return sip.TransportMetadata{}, false
	}
	return p.meta, true
}

func (p *singleTranspMetaProvider) TransportMetadataByNAPTRService(service string) (sip.TransportMetadata, bool) {
	if !util.EqFold(p.meta.NAPTRService, service) {
		return sip.TransportMetadata{}, false
	}
	return p.meta, true
}

func (p *singleTranspMetaProvider) AllTransportMetadata() iter.Seq[sip.TransportMetadata] {
	return func(yield func(sip.TransportMetadata) bool) { yield(p.meta) }
}
