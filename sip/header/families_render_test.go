package header_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestHeaderFamilies_RenderingAndValidity(t *testing.T) {
	t.Parallel()

	sipURI := &uri.SIP{Addr: uri.MakeHostAddr("example.com")}
	hop := header.NameAddr{URI: sipURI}
	tests := []struct {
		name    string
		hdr     header.Header
		full    string
		compact string
	}{
		{
			"Require",
			header.Require{"timer"},
			"Require: timer",
			"Require: timer",
		},
		{
			"ProxyRequire",
			header.ProxyRequire{"timer"},
			"Proxy-Require: timer",
			"Proxy-Require: timer",
		},
		{
			"Supported",
			header.Supported{"timer"},
			"Supported: timer",
			"k: timer",
		},
		{
			"Unsupported",
			header.Unsupported{"timer"},
			"Unsupported: timer",
			"Unsupported: timer",
		},
		{
			"Route",
			header.Route{hop},
			"Route: <sip:example.com>",
			"Route: <sip:example.com>",
		},
		{
			"RecordRoute",
			header.RecordRoute{hop},
			"Record-Route: <sip:example.com>",
			"Record-Route: <sip:example.com>",
		},
		{
			"Authorization",
			&header.Authorization{},
			"Authorization: ",
			"Authorization: ",
		},
		{
			"ProxyAuthorization",
			&header.ProxyAuthorization{},
			"Proxy-Authorization: ",
			"Proxy-Authorization: ",
		},
		{
			"WWWAuthenticate",
			&header.WWWAuthenticate{},
			"WWW-Authenticate: ",
			"WWW-Authenticate: ",
		},
		{
			"ProxyAuthenticate",
			&header.ProxyAuthenticate{},
			"Proxy-Authenticate: ",
			"Proxy-Authenticate: ",
		},
		{
			"From",
			&header.From{URI: sipURI},
			"From: <sip:example.com>",
			"f: <sip:example.com>",
		},
		{
			"To",
			&header.To{URI: sipURI},
			"To: <sip:example.com>",
			"t: <sip:example.com>",
		},
		{
			"ReplyTo",
			&header.ReplyTo{URI: sipURI},
			"Reply-To: <sip:example.com>",
			"Reply-To: <sip:example.com>",
		},
		{
			"AlertInfo",
			header.AlertInfo{{URI: sipURI}},
			"Alert-Info: <sip:example.com>",
			"Alert-Info: <sip:example.com>",
		},
		{
			"CallInfo",
			header.CallInfo{{URI: sipURI}},
			"Call-Info: <sip:example.com>",
			"Call-Info: <sip:example.com>",
		},
		{
			"ErrorInfo",
			header.ErrorInfo{{URI: sipURI}},
			"Error-Info: <sip:example.com>",
			"Error-Info: <sip:example.com>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := familyRender(t, tt.hdr); got != tt.full {
				t.Errorf("header.RenderTo() = %q, want %q", got, tt.full)
			}

			if got := familyRender(t, tt.hdr, header.RenderOptions{Compact: true}); got != tt.compact {
				t.Errorf("compact header.RenderTo() = %q, want %q", got, tt.compact)
			}
		})
	}

	if got := familyRender(t, header.Supported{}); got != "Supported: " {
		t.Errorf("empty Supported.RenderTo() = %q, want %q", got, "Supported: ")
	}
	if !(header.Supported{}).IsValid() {
		t.Error("empty non-nil Supported should be valid")
	}
	if header.Supported(nil).IsValid() {
		t.Error("nil Supported should be invalid")
	}

	typedNil := []header.Header{
		header.Require(nil),
		header.ProxyRequire(nil),
		header.Supported(nil),
		header.Unsupported(nil),
		header.Route(nil),
		header.RecordRoute(nil),
		(*header.Authorization)(nil),
		(*header.ProxyAuthorization)(nil),
		(*header.WWWAuthenticate)(nil),
		(*header.ProxyAuthenticate)(nil),
		(*header.From)(nil),
		(*header.To)(nil),
		(*header.ReplyTo)(nil),
		header.AlertInfo(nil),
		header.CallInfo(nil),
		header.ErrorInfo(nil),
	}
	for _, hdr := range typedNil {
		t.Run("nil_render_"+fmt.Sprintf("%T", hdr), func(t *testing.T) {
			t.Parallel()

			if got := familyRender(t, hdr); got != "" {
				t.Errorf("RenderTo() = %q, want empty output", got)
			}
		})
	}

	invalid := []struct {
		name string
		hdr  header.Header
	}{
		{"Require", header.Require(nil)},
		{"ProxyRequire", header.ProxyRequire(nil)},
		{"Unsupported", header.Unsupported(nil)},
		{"Route", header.Route(nil)},
		{"RecordRoute", header.RecordRoute(nil)},
		{"empty Require", header.Require{}},
		{"empty ProxyRequire", header.ProxyRequire{}},
		{"empty Unsupported", header.Unsupported{}},
		{"empty Route", header.Route{}},
		{"empty RecordRoute", header.RecordRoute{}},
	}
	for _, tt := range invalid {
		t.Run("invalid_"+tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.hdr.IsValid() {
				t.Errorf("%T.IsValid() = true, want false", tt.hdr)
			}
		})
	}
}

func TestHeaderFamilies_RenderOptionsUseLastValue(t *testing.T) {
	t.Parallel()

	sipURI := &uri.SIP{Addr: uri.MakeHostAddr("example.com")}
	tests := []struct {
		name      string
		hdr       header.Header
		canonical string
		compact   string
	}{
		{"Supported", header.Supported{"timer"}, "Supported: timer", "k: timer"},
		{"From", &header.From{URI: sipURI}, "From: <sip:example.com>", "f: <sip:example.com>"},
		{"To", &header.To{URI: sipURI}, "To: <sip:example.com>", "t: <sip:example.com>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := familyRender(t, tt.hdr, header.RenderOptions{Compact: true}, header.RenderOptions{Compact: false}); got != tt.canonical {
				t.Errorf("last canonical option: got %q, want %q", got, tt.canonical)
			}

			if got := familyRender(t, tt.hdr, header.RenderOptions{Compact: false}, header.RenderOptions{Compact: true}); got != tt.compact {
				t.Errorf("last compact option: got %q, want %q", got, tt.compact)
			}
		})
	}
}

func TestHeaderFamilies_Format(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Header
		full string
		val  string
	}{
		{
			"Require",
			header.Require{"timer"},
			"Require: timer",
			"timer",
		},
		{
			"Supported",
			header.Supported{"timer"},
			"Supported: timer",
			"timer",
		},
		{
			"From",
			&header.From{
				URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
			},
			"From: <sip:example.com>",
			"<sip:example.com>",
		},
		{
			"AlertInfo",
			header.AlertInfo{{
				URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
			}},
			"Alert-Info: <sip:example.com>",
			"<sip:example.com>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := fmt.Sprintf("%s", tt.hdr); got != tt.val {
				t.Errorf("%%s = %q, want %q", got, tt.val)
			}

			if got := fmt.Sprintf("%+s", tt.hdr); got != tt.full {
				t.Errorf("%%+s = %q, want %q", got, tt.full)
			}

			if got := fmt.Sprintf("%q", tt.hdr); got != fmt.Sprintf("%q", tt.val) {
				t.Errorf("%%q = %q, want %q", got, fmt.Sprintf("%q", tt.val))
			}

			if got := fmt.Sprintf("%+q", tt.hdr); got != fmt.Sprintf("%q", tt.full) {
				t.Errorf("%%+q = %q, want %q", got, fmt.Sprintf("%q", tt.full))
			}
		})
	}

	hdr := header.Require{"timer"}
	formats := []struct {
		format string
		want   string
	}{
		{"%v", "[timer]"},
		{"%+v", "[timer]"},
		{"%#v", `header.Require{"timer"}`},
	}
	for _, tt := range formats {
		t.Run(tt.format, func(t *testing.T) {
			t.Parallel()

			if got := fmt.Sprintf(tt.format, hdr); got != tt.want {
				t.Errorf("fmt.Sprintf(%q) = %q, want %q", tt.format, got, tt.want)
			}
		})
	}
}

func familyRender(t *testing.T, hdr header.Header, opts ...header.RenderOptions) string {
	t.Helper()
	var sb strings.Builder
	if _, err := hdr.RenderTo(&sb, opts...); err != nil {
		t.Fatalf("header.RenderTo() error = %v", err)
	}
	return sb.String()
}
