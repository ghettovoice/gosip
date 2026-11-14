package sip_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip"
)

type elemCapabilsComponent struct {
	methods []sip.RequestMethod
}

func (component elemCapabilsComponent) Capabilities() sip.ElementCapabilities {
	return sip.ElementCapabilities{Methods: component.methods}
}

func TestElement_UseComponent_PreservesRegistrationOrder(t *testing.T) {
	t.Parallel()

	elm := &sip.Element{}
	t.Cleanup(func() { _ = elm.Close() })

	removeFirst := elm.UseComponent(elemCapabilsComponent{
		methods: []sip.RequestMethod{sip.RequestMethodInvite},
	})
	removeSecond := elm.UseComponent(elemCapabilsComponent{
		methods: []sip.RequestMethod{sip.RequestMethodBye},
	})
	removeThird := elm.UseComponent(elemCapabilsComponent{
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
		t.Errorf("elm.Capabilities() after component removal mismatch (-want +got):\n%s", diff)
	}

	removeFirst()
	removeThird()
}
