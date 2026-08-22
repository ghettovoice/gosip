package sip_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/sip"
)

type elemCapabilsMiddleware struct {
	methods []sip.RequestMethod
}

func (mw elemCapabilsMiddleware) Capabilities() sip.ElementCapabilities {
	return sip.ElementCapabilities{Methods: mw.methods}
}

func TestElement_UseMiddleware_PreservesRegistrationOrder(t *testing.T) {
	t.Parallel()

	elm, err := sip.NewElement()
	if err != nil {
		t.Fatalf("sip.NewElement() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = elm.Close(t.Context()) })

	removeFirst := elm.UseMiddleware(elemCapabilsMiddleware{
		methods: []sip.RequestMethod{sip.RequestMethodInvite},
	})
	removeSecond := elm.UseMiddleware(elemCapabilsMiddleware{
		methods: []sip.RequestMethod{sip.RequestMethodBye},
	})
	removeThird := elm.UseMiddleware(elemCapabilsMiddleware{
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
