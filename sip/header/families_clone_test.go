package header_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestHeaderFamilies_CloneTypesAndNilShapes(t *testing.T) {
	t.Parallel()

	sipURI := &uri.SIP{Addr: uri.MakeHostAddr("example.com")}
	hop := header.NameAddr{URI: sipURI}
	headers := []header.Header{
		header.Require{"timer"},
		header.ProxyRequire{"timer"},
		header.Supported{"timer"},
		header.Unsupported{"timer"},
		header.Route{hop},
		header.RecordRoute{hop},
		&header.Authorization{},
		&header.ProxyAuthorization{},
		&header.WWWAuthenticate{},
		&header.ProxyAuthenticate{},
		&header.From{URI: sipURI},
		&header.To{URI: sipURI},
		&header.ReplyTo{URI: sipURI},
		header.AlertInfo{{URI: sipURI}},
		header.CallInfo{{URI: sipURI}},
		header.ErrorInfo{{URI: sipURI}},
	}

	for _, hdr := range headers {
		t.Run(reflect.TypeOf(hdr).String(), func(t *testing.T) {
			t.Parallel()

			clone := hdr.Clone()

			if reflect.TypeOf(clone) != reflect.TypeOf(hdr) {
				t.Errorf("Clone() type = %T, want %T", clone, hdr)
			}
			if !hdr.Equal(clone) {
				t.Errorf("header.Equal(Clone()) = false for %T", hdr)
			}
		})
	}

	nilSlices := []header.Header{
		header.Require(nil),
		header.ProxyRequire(nil),
		header.Supported(nil),
		header.Unsupported(nil),
		header.Route(nil),
		header.RecordRoute(nil),
		header.AlertInfo(nil),
		header.CallInfo(nil),
		header.ErrorInfo(nil),
	}

	for _, hdr := range nilSlices {
		t.Run("nil_"+reflect.TypeOf(hdr).String(), func(t *testing.T) {
			t.Parallel()

			clone := hdr.Clone()

			if clone == nil {
				t.Fatalf("Clone() returned nil interface for %T", hdr)
			}
			if reflect.TypeOf(clone) != reflect.TypeOf(hdr) {
				t.Errorf("Clone() type = %T, want %T", clone, hdr)
			}
			if !reflect.ValueOf(clone).IsNil() {
				t.Errorf("Clone() value = %#v, want typed nil", clone)
			}
		})
	}

	nilPointers := []header.Header{
		(*header.Authorization)(nil),
		(*header.ProxyAuthorization)(nil),
		(*header.WWWAuthenticate)(nil),
		(*header.ProxyAuthenticate)(nil),
		(*header.From)(nil),
		(*header.To)(nil),
		(*header.ReplyTo)(nil),
	}

	for _, hdr := range nilPointers {
		t.Run("nil_"+reflect.TypeOf(hdr).String(), func(t *testing.T) {
			t.Parallel()

			if clone := hdr.Clone(); clone != nil {
				t.Errorf("Clone() = %#v, want nil interface", clone)
			}
		})
	}
}

func TestHeaderFamilies_CloneDeepCopiesPayloads(t *testing.T) {
	t.Parallel()

	optionHeaders := []struct {
		name string
		hdr  header.Header
		set  func(header.Header)
	}{
		{
			"Require",
			header.Require{"timer"},
			func(hdr header.Header) {
				hdr.(header.Require)[0] = "changed"
			},
		},
		{
			"ProxyRequire",
			header.ProxyRequire{"timer"},
			func(hdr header.Header) {
				hdr.(header.ProxyRequire)[0] = "changed"
			},
		},
		{
			"Supported",
			header.Supported{"timer"},
			func(hdr header.Header) {
				hdr.(header.Supported)[0] = "changed"
			},
		},
		{
			"Unsupported",
			header.Unsupported{"timer"},
			func(hdr header.Header) {
				hdr.(header.Unsupported)[0] = "changed"
			},
		},
	}

	for _, tt := range optionHeaders {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clone := tt.hdr.Clone()
			tt.set(clone)

			if tt.hdr.Equal(clone) {
				t.Errorf("mutating the cloned %s did not change equality", tt.name)
			}
			if got := tt.hdr.RenderValue(); got != "timer" {
				t.Errorf("original value = %q, want %q", got, "timer")
			}
		})
	}

	original := header.Route{{
		URI:    &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
		Params: make(header.Values).Set("x-test", "original"),
	}}

	clone := original.Clone().(header.Route)
	cloneURI, ok := clone[0].URI.(*uri.SIP)
	if !ok {
		t.Fatalf("cloned URI type = %T, want *uri.SIP", clone[0].URI)
	}
	if clone[0].URI == original[0].URI {
		t.Fatal("cloned route shares its URI")
	}
	cloneURI.Addr = uri.MakeHostAddr("changed.example")
	clone[0].Params.Set("x-test", "changed")

	var originalURI strings.Builder
	if _, err := original[0].URI.RenderTo(&originalURI); err != nil {
		t.Fatalf("original URI RenderTo() error = %v", err)
	}
	if got := originalURI.String(); got != "sip:example.com" {
		t.Errorf("original URI = %q, want %q", got, "sip:example.com")
	}
	if got, _ := original[0].Params.Last("x-test"); got != "original" {
		t.Errorf("original route param = %q, want %q", got, "original")
	}

	recordOriginal := header.RecordRoute{{
		URI:    &uri.SIP{Addr: uri.MakeHostAddr("record.example")},
		Params: make(header.Values).Set("x-test", "record-original"),
	}}

	recordClone := recordOriginal.Clone().(header.RecordRoute)
	recordURI, ok := recordClone[0].URI.(*uri.SIP)
	if !ok {
		t.Fatalf("cloned Record-Route URI type = %T, want *uri.SIP", recordClone[0].URI)
	}
	if recordClone[0].URI == recordOriginal[0].URI {
		t.Fatal("cloned Record-Route shares its URI")
	}
	recordURI.Addr = uri.MakeHostAddr("changed-record.example")
	recordClone[0].Params.Set("x-test", "changed")

	var originalRecordURI strings.Builder
	if _, err := recordOriginal[0].URI.RenderTo(&originalRecordURI); err != nil {
		t.Fatalf("original Record-Route URI RenderTo() error = %v", err)
	}
	if got := originalRecordURI.String(); got != "sip:record.example" {
		t.Errorf("original Record-Route URI = %q, want %q", got, "sip:record.example")
	}
	if got, _ := recordOriginal[0].Params.Last("x-test"); got != "record-original" {
		t.Errorf("original Record-Route param = %q, want %q", got, "record-original")
	}

	credentials := &header.DigestCredentials{
		URI:    &uri.SIP{Addr: uri.MakeHostAddr("auth.example")},
		Params: make(header.Values).Set("x-test", "auth-original"),
	}

	authorization := &header.ProxyAuthorization{AuthCredentials: credentials}
	authorizationClone := authorization.Clone().(*header.ProxyAuthorization)
	clonedCredentials, ok := authorizationClone.AuthCredentials.(*header.DigestCredentials)
	if !ok {
		t.Fatalf("cloned credentials type = %T, want *header.DigestCredentials", authorizationClone.AuthCredentials)
	}
	if clonedCredentials == credentials || clonedCredentials.URI == credentials.URI {
		t.Fatal("cloned credentials share payload state")
	}
	clonedCredentials.URI.(*uri.SIP).Addr = uri.MakeHostAddr("changed-auth.example")
	clonedCredentials.Params.Set("x-test", "changed")

	var originalAuthURI strings.Builder
	if _, err := credentials.URI.RenderTo(&originalAuthURI); err != nil {
		t.Fatalf("original credentials URI RenderTo() error = %v", err)
	}
	if got := originalAuthURI.String(); got != "sip:auth.example" {
		t.Errorf("original credentials URI = %q, want %q", got, "sip:auth.example")
	}
	if got, _ := credentials.Params.Last("x-test"); got != "auth-original" {
		t.Errorf("original credentials param = %q, want %q", got, "auth-original")
	}

	challenge := &header.DigestChallenge{
		QOP:    []string{"auth"},
		Params: make(header.Values).Set("x-test", "challenge-original"),
	}

	proxyChallenge := &header.ProxyAuthenticate{AuthChallenge: challenge}
	proxyChallengeClone := proxyChallenge.Clone().(*header.ProxyAuthenticate)
	clonedChallenge, ok := proxyChallengeClone.AuthChallenge.(*header.DigestChallenge)
	if !ok {
		t.Fatalf("cloned challenge type = %T, want *header.DigestChallenge", proxyChallengeClone.AuthChallenge)
	}
	if clonedChallenge == challenge {
		t.Fatal("cloned challenge shares its payload")
	}
	clonedChallenge.QOP[0] = "auth-int"
	clonedChallenge.Params.Set("x-test", "changed")

	if got := challenge.QOP[0]; got != "auth" {
		t.Errorf("original challenge QOP = %q, want %q", got, "auth")
	}
	if got, _ := challenge.Params.Last("x-test"); got != "challenge-original" {
		t.Errorf("original challenge param = %q, want %q", got, "challenge-original")
	}
}

func TestHeaderFamilies_EqualTypeGuards(t *testing.T) {
	t.Parallel()

	sipURI := &uri.SIP{Addr: uri.MakeHostAddr("example.com")}
	require := header.Require{"timer"}
	proxyRequire := header.ProxyRequire{"timer"}
	supported := header.Supported{"timer"}
	unsupported := header.Unsupported{"timer"}
	route := header.Route{{URI: sipURI}}
	recordRoute := header.RecordRoute{{URI: sipURI}}
	alertInfo := header.AlertInfo{{URI: sipURI}}
	callInfo := header.CallInfo{{URI: sipURI}}
	errorInfo := header.ErrorInfo{{URI: sipURI}}
	from := &header.From{URI: sipURI}
	to := &header.To{URI: sipURI}
	replyTo := &header.ReplyTo{URI: sipURI}
	auth := &header.Authorization{AuthCredentials: &header.AnyCredentials{Scheme: "Token"}}
	proxyAuth := &header.ProxyAuthorization{AuthCredentials: &header.AnyCredentials{Scheme: "Token"}}
	challenge := &header.WWWAuthenticate{AuthChallenge: &header.AnyChallenge{Scheme: "Token"}}
	proxyChallenge := &header.ProxyAuthenticate{AuthChallenge: &header.AnyChallenge{Scheme: "Token"}}

	tests := []struct {
		name        string
		hdr         header.Header
		sameValue   any
		samePointer any
		neighbor    any
	}{
		{"Require", require, header.Require{"timer"}, &require, proxyRequire},
		{"ProxyRequire", proxyRequire, header.ProxyRequire{"timer"}, &proxyRequire, require},
		{"Supported", supported, header.Supported{"timer"}, &supported, require},
		{"Unsupported", unsupported, header.Unsupported{"timer"}, &unsupported, require},
		{"Route", route, header.Route{{URI: sipURI}}, &route, recordRoute},
		{"RecordRoute", recordRoute, header.RecordRoute{{URI: sipURI}}, &recordRoute, route},
		{"AlertInfo", alertInfo, header.AlertInfo{{URI: sipURI}}, &alertInfo, callInfo},
		{"CallInfo", callInfo, header.CallInfo{{URI: sipURI}}, &callInfo, errorInfo},
		{"ErrorInfo", errorInfo, header.ErrorInfo{{URI: sipURI}}, &errorInfo, alertInfo},
		{"From", from, *from, from, to},
		{"To", to, *to, to, from},
		{"ReplyTo", replyTo, *replyTo, replyTo, from},
		{"Authorization", auth, *auth, auth, proxyAuth},
		{"ProxyAuthorization", proxyAuth, *proxyAuth, proxyAuth, auth},
		{"WWWAuthenticate", challenge, *challenge, challenge, proxyChallenge},
		{"ProxyAuthenticate", proxyChallenge, *proxyChallenge, proxyChallenge, challenge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !tt.hdr.Equal(tt.sameValue) {
				t.Errorf("%T.Equal(value) = false", tt.hdr)
			}
			if !tt.hdr.Equal(tt.samePointer) {
				t.Errorf("%T.Equal(pointer) = false", tt.hdr)
			}
			if tt.hdr.Equal(tt.neighbor) {
				t.Errorf("%T.Equal(%T) = true, want false", tt.hdr, tt.neighbor)
			}
		})
	}
}
