package sip_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip"
)

type elemCapabilsMidware struct {
	methods []sip.RequestMethod
}

func (mw elemCapabilsMidware) Capabilities() sip.ElementCapabilities {
	return sip.ElementCapabilities{Methods: mw.methods}
}

func TestElement_UseMiddleware_PreservesRegistrationOrder(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	removeFirst := elm.UseMiddleware(elemCapabilsMidware{
		methods: []sip.RequestMethod{sip.RequestMethodInvite},
	})
	removeSecond := elm.UseMiddleware(elemCapabilsMidware{
		methods: []sip.RequestMethod{sip.RequestMethodBye},
	})
	removeThird := elm.UseMiddleware(elemCapabilsMidware{
		methods: []sip.RequestMethod{sip.RequestMethodCancel},
	})

	want := sip.ElementCapabilities{
		Methods: []sip.RequestMethod{
			sip.RequestMethodInvite,
			sip.RequestMethodBye,
			sip.RequestMethodCancel,
		},
	}
	if diff := cmp.Diff(want, elm.Capabilities()); diff != "" {
		t.Errorf("elm.Capabilities() mismatch (-want +got):\n%s", diff)
	}

	removeSecond()
	want.Methods = []sip.RequestMethod{
		sip.RequestMethodInvite,
		sip.RequestMethodCancel,
	}
	if diff := cmp.Diff(want, elm.Capabilities()); diff != "" {
		t.Errorf("elm.Capabilities() after middleware removal mismatch (-want +got):\n%s", diff)
	}

	removeFirst()
	removeThird()
}
