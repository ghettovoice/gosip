package header_test

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

const (
	errFamilyNestedRenderer errors.Error = "nested renderer failure"
	errFamilyAuthWriter     errors.Error = "limited writer failure"
)

func TestAuthHeaderFamilies_ForwardRenderOptionsAndNames(t *testing.T) {
	t.Parallel()

	credentials := &familyCredentialsSpy{familyAuthSpy{value: "credentials"}}
	proxyCredentials := &familyCredentialsSpy{familyAuthSpy{value: "credentials"}}
	challenge := &familyChallengeSpy{familyAuthSpy{value: "challenge"}}
	proxyChallenge := &familyChallengeSpy{familyAuthSpy{value: "challenge"}}

	tests := []struct {
		name       string
		headerName string
		hdr        header.Header
		spy        *familyAuthSpy
		value      string
	}{
		{
			"Authorization",
			"Authorization",
			&header.Authorization{
				AuthCredentials: credentials,
			},
			&credentials.familyAuthSpy,
			"credentials",
		},
		{
			"ProxyAuthorization",
			"Proxy-Authorization",
			&header.ProxyAuthorization{
				AuthCredentials: proxyCredentials,
			},
			&proxyCredentials.familyAuthSpy,
			"credentials",
		},
		{
			"WWWAuthenticate",
			"WWW-Authenticate",
			&header.WWWAuthenticate{
				AuthChallenge: challenge,
			},
			&challenge.familyAuthSpy,
			"challenge",
		},
		{
			"ProxyAuthenticate",
			"Proxy-Authenticate",
			&header.ProxyAuthenticate{
				AuthChallenge: proxyChallenge,
			},
			&proxyChallenge.familyAuthSpy,
			"challenge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			num, err := tt.hdr.RenderTo(&sb, header.RenderOptions{Compact: false}, header.RenderOptions{Compact: true})
			if err != nil {
				t.Fatalf("RenderTo() error = %v", err)
			}
			want := tt.headerName + ": " + tt.value
			if sb.String() != want {
				t.Errorf("RenderTo() = %q, want %q", sb.String(), want)
			}
			if num != len(want) {
				t.Errorf("RenderTo() count = %d, want %d", num, len(want))
			}
			if len(tt.spy.options) != 2 || !tt.spy.options[1].Compact {
				t.Errorf("nested RenderTo options = %#v, want final Compact option", tt.spy.options)
			}
			if got := tt.hdr.RenderValue(); got != tt.value {
				t.Errorf("RenderValue() = %q, want %q", got, tt.value)
			}
			if got := fmt.Sprintf("%s", tt.hdr); got != tt.value {
				t.Errorf("%%s = %q, want %q", got, tt.value)
			}
			if got := fmt.Sprintf("%+s", tt.hdr); got != want {
				t.Errorf("%%+s = %q, want %q", got, want)
			}
			if got := fmt.Sprintf("%q", tt.hdr); got != fmt.Sprintf("%q", tt.value) {
				t.Errorf("%%q = %q, want %q", got, fmt.Sprintf("%q", tt.value))
			}
			if got := fmt.Sprintf("%+q", tt.hdr); got != fmt.Sprintf("%q", want) {
				t.Errorf("%%+q = %q, want %q", got, fmt.Sprintf("%q", want))
			}
		})
	}
}

func TestAuthHeaderFamilies_PropagateNestedErrors(t *testing.T) {
	t.Parallel()

	spy := &familyCredentialsSpy{familyAuthSpy{value: "credentials", renderErr: errFamilyNestedRenderer}}
	hdr := &header.Authorization{AuthCredentials: spy}
	var sb strings.Builder

	if _, err := hdr.RenderTo(&sb); !errors.Is(err, errFamilyNestedRenderer) {
		t.Errorf("RenderTo() error = %v, want %v", err, errFamilyNestedRenderer)
	}

	writer := &familyLimitedWriter{remaining: len("Authorization: ") + 3, err: errFamilyAuthWriter}
	hdr = &header.Authorization{AuthCredentials: &familyCredentialsSpy{familyAuthSpy{value: "credentials"}}}

	num, err := hdr.RenderTo(writer)

	if !errors.Is(err, errFamilyAuthWriter) {
		t.Errorf("RenderTo() error = %v, want %v", err, errFamilyAuthWriter)
	}
	if num != writer.written {
		t.Errorf("RenderTo() count = %d, accepted bytes = %d", num, writer.written)
	}
}

type familyAuthSpy struct {
	value     string
	options   []header.RenderOptions
	renderErr error
}

func (spy *familyAuthSpy) RenderTo(w io.Writer, opts ...header.RenderOptions) (int, error) {
	spy.options = append(spy.options, opts...)
	num, err := io.WriteString(w, spy.value)
	if err != nil {
		return num, errors.Wrap(err)
	}
	if spy.renderErr != nil {
		return num, errors.Wrap(spy.renderErr)
	}
	return num, nil
}

type familyCredentialsSpy struct {
	familyAuthSpy
}

func (spy *familyCredentialsSpy) Clone() header.AuthCredentials {
	if spy == nil {
		return nil
	}
	clone := *spy
	clone.options = append([]header.RenderOptions(nil), spy.options...)
	return &clone
}

func (spy *familyCredentialsSpy) Equal(val any) bool {
	other, ok := val.(*familyCredentialsSpy)
	return ok && spy == other
}

func (spy *familyCredentialsSpy) IsValid() bool { return spy != nil }

type familyChallengeSpy struct {
	familyAuthSpy
}

func (spy *familyChallengeSpy) Clone() header.AuthChallenge {
	if spy == nil {
		return nil
	}
	clone := *spy
	clone.options = append([]header.RenderOptions(nil), spy.options...)
	return &clone
}

func (spy *familyChallengeSpy) Equal(val any) bool {
	other, ok := val.(*familyChallengeSpy)
	return ok && spy == other
}

func (spy *familyChallengeSpy) IsValid() bool { return spy != nil }
