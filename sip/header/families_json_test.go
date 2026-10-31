package header_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestHeaderJSON_EmptyValuesResetExistingTargets(t *testing.T) {
	t.Parallel()

	t.Run("From", func(t *testing.T) {
		t.Parallel()

		hdr := &header.From{
			DisplayName: "old",
			URI:         &uri.SIP{Addr: uri.MakeHostAddr("old.example")},
			Params:      make(header.Values).Set("tag", "old"),
		}

		familyUnmarshalHeaderJSON(t, "From", "", hdr)

		if hdr.DisplayName != "" || hdr.URI != nil || len(hdr.Params) != 0 {
			t.Errorf("From after empty JSON = %#v, want zero value", hdr)
		}
	})

	t.Run("To and ReplyTo", func(t *testing.T) {
		t.Parallel()

		to := &header.To{URI: &uri.SIP{Addr: uri.MakeHostAddr("old.example")}}

		familyUnmarshalHeaderJSON(t, "To", "<>", to)

		if to.URI != nil {
			t.Errorf("To URI after <> JSON = %v, want nil", to.URI)
		}

		replyTo := &header.ReplyTo{URI: &uri.SIP{Addr: uri.MakeHostAddr("old.example")}}

		familyUnmarshalHeaderJSON(t, "Reply-To", "<>", replyTo)

		if replyTo.URI != nil {
			t.Errorf("Reply-To URI after <> JSON = %v, want nil", replyTo.URI)
		}
	})

	t.Run("Server and ContentLength", func(t *testing.T) {
		t.Parallel()

		server := header.Server("old-server")

		familyUnmarshalHeaderJSON(t, "Server", "", &server)

		if server != "" {
			t.Errorf("Server after empty JSON = %q, want empty", server)
		}

		contentLength := header.ContentLength(42)

		familyUnmarshalHeaderJSON(t, "Content-Length", "", &contentLength)

		if contentLength != 0 {
			t.Errorf("ContentLength after empty JSON = %d, want 0", contentLength)
		}
	})

	t.Run("ContentType and CSeq", func(t *testing.T) {
		t.Parallel()

		contentType := header.ContentType{Type: "application", Subtype: "sdp"}

		familyUnmarshalHeaderJSON(t, "Content-Type", "", &contentType)

		if contentType.Type != "" || contentType.Subtype != "" {
			t.Errorf("ContentType after empty JSON = %#v, want zero value", contentType)
		}

		cseq := &header.CSeq{SeqNum: 42, Method: header.RequestMethod("INVITE")}

		familyUnmarshalHeaderJSON(t, "CSeq", "", cseq)

		if cseq.SeqNum != 0 || cseq.Method != "" {
			t.Errorf("CSeq after empty JSON = %#v, want zero value", cseq)
		}

		familyUnmarshalHeaderJSON(t, "CSeq", " 0 ", cseq)

		if cseq.SeqNum != 0 || cseq.Method != "" {
			t.Errorf("CSeq after whitespace-zero JSON = %#v, want zero value", cseq)
		}

		familyUnmarshalHeaderJSON(t, "Content-Type", "/", &contentType)

		if contentType.Type != "" || contentType.Subtype != "" {
			t.Errorf("ContentType after slash JSON = %#v, want zero value", contentType)
		}
	})

	t.Run("Route and RecordRoute", func(t *testing.T) {
		t.Parallel()

		route := header.Route{{URI: &uri.SIP{Addr: uri.MakeHostAddr("old.example")}}}

		familyUnmarshalHeaderJSON(t, "Route", "", &route)

		if route != nil {
			t.Errorf("Route after empty JSON = %#v, want nil slice", route)
		}

		recordRoute := header.RecordRoute{{URI: &uri.SIP{Addr: uri.MakeHostAddr("old.example")}}}

		familyUnmarshalHeaderJSON(t, "Record-Route", "", &recordRoute)

		if recordRoute != nil {
			t.Errorf("RecordRoute after empty JSON = %#v, want nil slice", recordRoute)
		}
	})

	t.Run("Contact", func(t *testing.T) {
		t.Parallel()

		contact := header.Contact{{URI: &uri.SIP{Addr: uri.MakeHostAddr("old.example")}}}

		familyUnmarshalHeaderJSON(t, "Contact", "", &contact)

		if contact != nil {
			t.Errorf("Contact after empty JSON = %#v, want nil slice", contact)
		}

		familyUnmarshalHeaderJSON(t, "Contact", "*", &contact)

		if contact == nil || len(contact) != 0 {
			t.Errorf("Contact after wildcard JSON = %#v, want non-nil empty slice", contact)
		}
	})

	t.Run("AuthHeaders", func(t *testing.T) {
		t.Parallel()

		authorization := &header.Authorization{AuthCredentials: &header.AnyCredentials{Scheme: "Token"}}

		familyUnmarshalHeaderJSON(t, "Authorization", "", authorization)

		if authorization.AuthCredentials != nil {
			t.Errorf("Authorization payload after empty JSON = %#v, want nil", authorization.AuthCredentials)
		}

		proxyAuthorization := &header.ProxyAuthorization{AuthCredentials: &header.AnyCredentials{Scheme: "Token"}}

		familyUnmarshalHeaderJSON(t, "Proxy-Authorization", "", proxyAuthorization)

		if proxyAuthorization.AuthCredentials != nil {
			t.Errorf("ProxyAuthorization payload after empty JSON = %#v, want nil", proxyAuthorization.AuthCredentials)
		}

		wwwAuthenticate := &header.WWWAuthenticate{AuthChallenge: &header.AnyChallenge{Scheme: "Token"}}

		familyUnmarshalHeaderJSON(t, "WWW-Authenticate", "", wwwAuthenticate)

		if wwwAuthenticate.AuthChallenge != nil {
			t.Errorf("WWWAuthenticate payload after empty JSON = %#v, want nil", wwwAuthenticate.AuthChallenge)
		}

		proxyAuthenticate := &header.ProxyAuthenticate{AuthChallenge: &header.AnyChallenge{Scheme: "Token"}}

		familyUnmarshalHeaderJSON(t, "Proxy-Authenticate", "", proxyAuthenticate)

		if proxyAuthenticate.AuthChallenge != nil {
			t.Errorf("ProxyAuthenticate payload after empty JSON = %#v, want nil", proxyAuthenticate.AuthChallenge)
		}
	})

	t.Run("ExpiryHeaders", func(t *testing.T) {
		t.Parallel()

		expires := &header.Expires{Duration: 42 * time.Second}

		familyUnmarshalHeaderJSON(t, "Expires", "", expires)

		if expires.Duration != 0 {
			t.Errorf("Expires after empty JSON = %v, want 0", expires.Duration)
		}

		minExpires := &header.MinExpires{Duration: 42 * time.Second}

		familyUnmarshalHeaderJSON(t, "Min-Expires", "", minExpires)

		if minExpires.Duration != 0 {
			t.Errorf("MinExpires after empty JSON = %v, want 0", minExpires.Duration)
		}
	})
}

func TestHeaderJSON_NullResetsExistingTargets(t *testing.T) {
	t.Parallel()

	server := header.Server("old-server")

	if err := json.Unmarshal([]byte("null"), &server); err != nil {
		t.Fatalf("json.Unmarshal(Server null) error = %v", errors.Wrap(err))
	}

	if server != "" {
		t.Errorf("Server after null = %q, want empty", server)
	}

	route := header.Route{{URI: &uri.SIP{Addr: uri.MakeHostAddr("old.example")}}}

	if err := json.Unmarshal([]byte("null"), &route); err != nil {
		t.Fatalf("json.Unmarshal(Route null) error = %v", errors.Wrap(err))
	}

	if route != nil {
		t.Errorf("Route after null = %#v, want nil slice", route)
	}

	from := &header.From{URI: &uri.SIP{Addr: uri.MakeHostAddr("old.example")}}

	if err := json.Unmarshal([]byte("null"), from); err != nil {
		t.Fatalf("json.Unmarshal(From null) error = %v", errors.Wrap(err))
	}

	if from.URI != nil || from.DisplayName != "" || len(from.Params) != 0 {
		t.Errorf("From after null = %#v, want zero value", from)
	}
}

func TestHeaderJSON_EmptyTypedSlicesAndContactPolicies(t *testing.T) {
	t.Parallel()

	var supported header.Supported

	familyUnmarshalHeaderJSON(t, "Supported", "", &supported)

	if supported == nil || len(supported) != 0 {
		t.Errorf("Supported after typed empty JSON = %#v, want non-nil empty slice", supported)
	}

	var accept header.Accept

	familyUnmarshalHeaderJSON(t, "Accept", "", &accept)

	if accept == nil || len(accept) != 0 {
		t.Errorf("Accept after typed empty JSON = %#v, want non-nil empty slice", accept)
	}

	contact := header.Contact{{URI: &uri.SIP{Addr: uri.MakeHostAddr("old.example")}}}

	familyUnmarshalHeaderJSON(t, "Contact", "*", &contact)

	if contact == nil || len(contact) != 0 {
		t.Errorf("Contact wildcard = %#v, want non-nil empty slice", contact)
	}

	contact = header.Contact{{URI: &uri.SIP{Addr: uri.MakeHostAddr("old.example")}}}

	familyUnmarshalHeaderJSON(t, "Contact", "", &contact)

	if contact != nil {
		t.Errorf("Contact empty value = %#v, want nil slice", contact)
	}
}

func TestHeaderJSON_FailuresPreserveExistingTargets(t *testing.T) {
	t.Parallel()

	contentLength := header.ContentLength(42)

	if err := familyUnmarshalHeaderJSONValue(`{"name":"Wrong","value":""}`, &contentLength); err == nil {
		t.Fatal("wrong-name empty header unexpectedly decoded")
	}

	if contentLength != 42 {
		t.Errorf("ContentLength after wrong-name error = %d, want 42", contentLength)
	}

	if err := familyUnmarshalHeaderJSONValue(`{"name":`, &contentLength); err == nil {
		t.Fatal("malformed JSON unexpectedly decoded")
	}

	if contentLength != 42 {
		t.Errorf("ContentLength after malformed JSON = %d, want 42", contentLength)
	}

	if err := familyUnmarshalHeaderJSONValue(`{"name":"Content-Length","value":"invalid"}`, &contentLength); err == nil {
		t.Fatal("malformed matching header unexpectedly decoded")
	}

	if contentLength != 42 {
		t.Errorf("ContentLength after malformed header = %d, want 42", contentLength)
	}
}

func familyUnmarshalHeaderJSON(t *testing.T, name, value string, target any) {
	t.Helper()
	if err := familyUnmarshalHeaderJSONValue(`{"name":"`+name+`","value":"`+value+`"}`, target); err != nil {
		t.Fatalf("json.Unmarshal(%s, %T) error = %v", name, target, errors.Wrap(err))
	}
}

func familyUnmarshalHeaderJSONValue(data string, target any) error {
	return errors.Wrap(json.Unmarshal([]byte(data), target))
}
