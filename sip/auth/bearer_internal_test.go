package auth

import (
	"context"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func mustParseURI(t *testing.T, text string) uri.URI {
	t.Helper()

	u, err := uri.Parse(text)
	if err != nil {
		t.Fatalf("uri.Parse(%q) error = %v, want nil", text, err)
	}
	return u
}

func bearerTokenProvider(context.Context, *sip.Request, *header.BearerChallenge, bool) (string, error) {
	return "token", nil
}

type bearerTestAccount struct{ ID string }

func TestBearerConfiguration(t *testing.T) {
	t.Parallel()

	asURI := mustParseURI(t, "https://as.example/token")

	t.Run("client", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name string
			opts BearerClientOptions
		}{
			{"missing realm", BearerClientOptions{AuthzServer: asURI, Token: bearerTokenProvider}},
			{"missing authz server", BearerClientOptions{Realm: "example.com", Token: bearerTokenProvider}},
			{"missing token callback", BearerClientOptions{Realm: "example.com", AuthzServer: asURI}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if _, err := NewBearerClient(tt.opts); err == nil {
					t.Fatal("auth.NewBearerClient() error = nil, want non-nil")
				}
			})
		}

		for _, tt := range []struct {
			name string
			as   string
		}{
			{"http not https", "http://as.example/token"},
			{"hostless opaque", "https:opaque"},
			{"userinfo", "https://user@as.example/token"},
			{"fragment", "https://as.example/token#frag"},
		} {
			t.Run("untrusted as "+tt.name, func(t *testing.T) {
				t.Parallel()

				_, err := NewBearerClient(BearerClientOptions{
					Realm:       "example.com",
					AuthzServer: mustParseURI(t, tt.as),
					Token:       bearerTokenProvider,
				})
				if err == nil {
					t.Fatalf("auth.NewBearerClient() accepted %q, want error", tt.as)
				}
			})
		}

		t.Run("retains exact rendered spelling", func(t *testing.T) {
			t.Parallel()

			spelled := mustParseURI(t, "https://as.example/token?x=1&y=2")
			cln, err := NewBearerClient(BearerClientOptions{
				Realm:       "example.com",
				AuthzServer: spelled,
				Token:       bearerTokenProvider,
			})
			if err != nil {
				t.Fatalf("auth.NewBearerClient() error = %v, want nil", err)
			}
			if cln.authzSrv != "https://as.example/token?x=1&y=2" {
				t.Fatalf("trusted AS = %q, want exact rendered spelling", cln.authzSrv)
			}
		})

		t.Run("no aliasing of caller uri", func(t *testing.T) {
			t.Parallel()

			caller, _ := uri.Parse("https://as.example/token")
			cln, err := NewBearerClient(BearerClientOptions{
				Realm:       "example.com",
				AuthzServer: caller,
				Token:       bearerTokenProvider,
			})
			if err != nil {
				t.Fatalf("auth.NewBearerClient() error = %v, want nil", err)
			}
			caller.(*uri.Any).Host = "evil.example"
			if cln.authzSrv != "https://as.example/token" {
				t.Fatalf("trusted AS = %q after caller mutation, want original", cln.authzSrv)
			}
		})
	})

	t.Run("server", func(t *testing.T) {
		t.Parallel()

		validate := func(context.Context, *sip.Request, string) (bearerTestAccount, error) {
			return bearerTestAccount{ID: "a"}, nil
		}
		for _, tt := range []struct {
			name string
			opts BearerServerOptions[bearerTestAccount]
		}{
			{"missing realm", BearerServerOptions[bearerTestAccount]{AuthzServer: asURI, ValidateToken: validate}},
			{
				"missing authz server",
				BearerServerOptions[bearerTestAccount]{ServerOptions: testServerOpts(), ValidateToken: validate},
			},
			{
				"missing validator",
				BearerServerOptions[bearerTestAccount]{ServerOptions: testServerOpts(), AuthzServer: asURI},
			},
			{
				"untrusted as",
				BearerServerOptions[bearerTestAccount]{
					ServerOptions: testServerOpts(),
					AuthzServer:   mustParseURI(t, "http://as.example/token"),
					ValidateToken: validate,
				},
			},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if _, err := NewBearerServer(tt.opts); err == nil {
					t.Fatal("auth.NewBearerServer() error = nil, want non-nil")
				}
			})
		}

		t.Run("scope optional and copies retained state", func(t *testing.T) {
			t.Parallel()

			caller, _ := uri.Parse("https://as.example/token")
			srv, err := NewBearerServer(BearerServerOptions[bearerTestAccount]{
				ServerOptions: testServerOpts(),
				AuthzServer:   caller,
				Scope:         "write",
				ValidateToken: validate,
			})
			if err != nil {
				t.Fatalf("auth.NewBearerServer() error = %v, want nil", err)
			}
			caller.(*uri.Any).Host = "evil.example"
			if srv.asURI == nil || shared.Render(srv.asURI) != "https://as.example/token" {
				t.Fatalf("retained AS = %v after caller mutation, want original", srv.asURI)
			}
			if srv.scope != "write" {
				t.Fatalf("scope = %q, want write", srv.scope)
			}
		})
	})

	t.Run("constructors invoke no callbacks", func(t *testing.T) {
		t.Parallel()

		var calls int
		if _, err := NewBearerClient(BearerClientOptions{
			Realm: "example.com", AuthzServer: asURI,
			Token: func(context.Context, *sip.Request, *header.BearerChallenge, bool) (string, error) {
				calls++
				return "t", nil
			},
		}); err != nil {
			t.Fatalf("auth.NewBearerClient() error = %v, want nil", err)
		}
		if _, err := NewBearerServer(BearerServerOptions[bearerTestAccount]{
			ServerOptions: testServerOpts(), AuthzServer: asURI,
			ValidateToken: func(context.Context, *sip.Request, string) (bearerTestAccount, error) {
				calls++
				return bearerTestAccount{}, nil
			},
		}); err != nil {
			t.Fatalf("auth.NewBearerServer() error = %v, want nil", err)
		}
		if calls != 0 {
			t.Fatalf("constructors made %d callback calls, want 0", calls)
		}
	})
}

// fakeURI is a test double over arbitrary rendered text; renderErr makes
// RenderTo fail after writing whatever it emitted.
type fakeURI struct {
	text      string
	renderErr error
}

func (u *fakeURI) Scheme() string { return "https" }
func (u *fakeURI) RenderTo(w io.Writer, _ ...shared.RenderOptions) (int, error) {
	n, werr := io.WriteString(w, u.text)
	if u.renderErr != nil {
		return n, u.renderErr
	}
	return n, werr
}

func (u *fakeURI) Clone() uri.URI {
	if u == nil {
		return nil
	}
	c := *u
	return &c
}
func (u *fakeURI) IsValid() bool { return u != nil }
func (u *fakeURI) Equal(v any) bool {
	other, ok := v.(*fakeURI)
	return ok && u.text == other.text
}

const errTestSentinel errors.Error = "renderer exploded"

func TestBearerConfiguration_SecretsAndRendering(t *testing.T) {
	t.Parallel()

	validate := func(context.Context, *sip.Request, string) (bearerTestAccount, error) {
		return bearerTestAccount{}, sip.ErrInvalidCredentials
	}

	t.Run("rejection text carries no url contents", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name string
			as   uri.URI
		}{
			{"userinfo secret", mustParseURI(t, "https://alice:s3cret-passw0rd@as.example/token")},
			{"query token on rejected scheme", mustParseURI(t, "http://as.example/token?session=q1w2e3r4t5")},
			{"unparseable url", &fakeURI{text: "https://as.example/%zz/bad"}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				_, err := NewBearerClient(BearerClientOptions{
					Realm:       "example.com",
					AuthzServer: tt.as,
					Token:       bearerTokenProvider,
				})
				if err == nil {
					t.Fatal("auth.NewBearerClient() error = nil, want rejection")
				}
				for _, secret := range []string{"alice", "s3cret-passw0rd", "q1w2e3r4t5", "%zz"} {
					if strings.Contains(err.Error(), secret) {
						t.Fatalf("constructor error %q contains %q", err.Error(), secret)
					}
				}
				_, srvErr := NewBearerServer(BearerServerOptions[bearerTestAccount]{
					ServerOptions: testServerOpts(),
					AuthzServer:   tt.as,
					ValidateToken: validate,
				})
				if srvErr == nil {
					t.Fatal("auth.NewBearerServer() error = nil, want rejection")
				}
				for _, secret := range []string{"alice", "s3cret-passw0rd", "q1w2e3r4t5", "%zz"} {
					if strings.Contains(srvErr.Error(), secret) {
						t.Fatalf("constructor error %q contains %q", srvErr.Error(), secret)
					}
				}
			})
		}
	})

	t.Run("stricter host and fragment rules", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name string
			as   uri.URI
		}{
			{"empty hostname with port", &uri.Any{URL: url.URL{Scheme: "https", Host: ":443", Path: "/t"}}},
			{"bracketed empty host", &uri.Any{URL: url.URL{Scheme: "https", Host: "[]", Path: "/t"}}},
			{"literal empty fragment", &fakeURI{text: "https://as.example/token#"}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if _, err := NewBearerClient(BearerClientOptions{
					Realm:       "example.com",
					AuthzServer: tt.as,
					Token:       bearerTokenProvider,
				}); err == nil {
					t.Fatalf("auth.NewBearerClient() accepted %v, want error", shared.Render(tt.as))
				}
			})
		}
	})

	t.Run("renderer failure is inspectable and discards partial text", func(t *testing.T) {
		t.Parallel()

		as := &fakeURI{text: "https://as.example/token", renderErr: errTestSentinel}
		_, err := NewBearerClient(BearerClientOptions{
			Realm:       "example.com",
			AuthzServer: as,
			Token:       bearerTokenProvider,
		})
		if !errors.Is(err, errTestSentinel) {
			t.Fatalf("auth.NewBearerClient() error = %v, want it to identify %v", err, errTestSentinel)
		}
		_, err = NewBearerServer(BearerServerOptions[bearerTestAccount]{
			ServerOptions: testServerOpts(),
			AuthzServer:   as,
			ValidateToken: validate,
		})
		if !errors.Is(err, errTestSentinel) {
			t.Fatalf("auth.NewBearerServer() error = %v, want it to identify %v", err, errTestSentinel)
		}
	})
}

func TestBearerClient_SelectChallenge_RenderFailure(t *testing.T) {
	t.Parallel()

	cln, err := NewBearerClient(BearerClientOptions{
		Realm:       "example.com",
		AuthzServer: mustParseURI(t, "https://as.example/token"),
		Token:       bearerTokenProvider,
	})
	if err != nil {
		t.Fatalf("auth.NewBearerClient() error = %v, want nil", err)
	}
	hdr := &header.WWWAuthenticate{AuthChallenge: &header.BearerChallenge{
		Realm:       "example.com",
		AuthzServer: &fakeURI{text: "https://as.example/token", renderErr: errTestSentinel},
	}}
	if got := cln.selectChal(make(sip.Headers).Set(hdr), false); got != nil {
		t.Fatalf("cln.selectChallenge() = %+v, want nil for a failing renderer", got)
	}
}
