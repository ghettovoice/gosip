package sip

import (
	"slices"

	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
)

type ElementMiddleware any

func (elm *Element) UseMiddleware(mw ElementMiddleware) (unbind func()) {
	remove := elm.mws.Add(mw)

	var unbinds []func()
	// bind to transport manager
	if v, ok := mw.(MessageInterceptor); ok {
		unbinds = append(unbinds, elm.tpm.UseMessageInterceptor(v))
	} else {
		if v, ok := mw.(InboundRequestInterceptor); ok {
			unbinds = append(unbinds, elm.tpm.UseInboundRequestInterceptor(v))
		}
		if v, ok := mw.(InboundResponseInterceptor); ok {
			unbinds = append(unbinds, elm.tpm.UseInboundResponseInterceptor(v))
		}
		if v, ok := mw.(OutboundRequestInterceptor); ok {
			unbinds = append(unbinds, elm.tpm.UseOutboundRequestInterceptor(v))
		}
		if v, ok := mw.(OutboundResponseInterceptor); ok {
			unbinds = append(unbinds, elm.tpm.UseOutboundResponseInterceptor(v))
		}
	}

	// bind to transaction manager
	if v, ok := mw.(TransactionHandler); ok {
		unbinds = append(unbinds, elm.txm.BindTransactionHandler(v))
	} else {
		if v, ok := mw.(ClientTransactionHandler); ok {
			unbinds = append(unbinds, elm.txm.BindClientTransactionHandler(v))
		}
		if v, ok := mw.(ServerTransactionHandler); ok {
			unbinds = append(unbinds, elm.txm.BindServerTransactionHandler(v))
		}
	}

	elm.updateCapabils()

	return func() {
		remove()

		for _, fn := range unbinds {
			fn()
		}

		elm.updateCapabils()
	}
}

type elemCapabilProvider interface {
	Capabilities() ElementCapabilities
}

func (elm *Element) updateCapabils() {
	cpb := ElementCapabilities{
		Methods:   slices.Clone(elm.baseCpb.Methods),
		Options:   slices.Clone(elm.baseCpb.Options),
		MIMETypes: slices.Clone(elm.baseCpb.MIMETypes),
	}

	for mw := range elm.mws.All() {
		if cpbPrvdr, ok := mw.(elemCapabilProvider); ok {
			mwCpb := cpbPrvdr.Capabilities()
			cpb.Methods = util.AppendSliceUniqFunc(cpb.Methods, mwCpb.Methods, func(e RequestMethod) string {
				return string(e)
			})
			cpb.Options = util.AppendSliceUniqFunc(cpb.Options, mwCpb.Options, func(e header.OptionTag) string {
				return string(e)
			})
			cpb.MIMETypes = util.AppendSliceUniqFunc(cpb.MIMETypes, mwCpb.MIMETypes, func(e header.MIMERange) string {
				return e.String()
			})
		}
	}

	elm.curCpb.Store(cpb)
}
