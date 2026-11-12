package auth_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/auth"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
	"github.com/ghettovoice/gosip/uri"
)

// examplePrincipal is the application-owned identity the example credential
// lookups return. The authentication package treats it as opaque.
type examplePrincipal struct {
	ID      string
	Allowed bool
}

func exampleRequest() *sip.Request {
	return &sip.Request{
		Method: sip.RequestMethodInvite,
		URI: &uri.SIP{
			User: uri.MakeUserInfo("alice"),
			Addr: uri.MakeHostAddr("example.com"),
		},
		Headers: make(sip.Headers).
			Set(header.Via{header.ViaHop{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "UDP",
				Addr:      uri.MakeHostAddr("client.example.com"),
			}}).
			Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}),
	}
}

// ExampleDigestClient_AuthenticateRequest fills in credentials after the
// caller received a 401 challenge from its own SIP exchange.
func ExampleDigestClient_AuthenticateRequest() {
	var cln sip.ClientAuthenticator
	cln, err := auth.NewDigestClient(auth.DigestClientOptions{
		Realm: "example.com",
		Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
			// Dummy credentials; a real application consults its own store.
			return "alice", "example-password", nil
		},
	})
	if err != nil {
		panic(err)
	}

	req := sip.NewRequestEnvelope(exampleRequest())
	res := sip.NewResponseEnvelope(&sip.Response{
		Status: sip.ResponseStatusUnauthorized,
		Reason: sip.ResponseStatusUnauthorized.Reason(),
		Headers: make(sip.Headers).Set(&header.WWWAuthenticate{
			AuthChallenge: &header.DigestChallenge{
				Realm:     "example.com",
				Nonce:     "dcd98b7102dd2f0e8b11d0f600bfb0c093",
				Algorithm: "SHA-256",
				QOP:       []string{"auth"},
			},
		}),
	})

	// AuthenticateRequest only adds credentials. Sending the retried request
	// — CSeq, Via, transaction handling — stays with the application.
	if err := cln.AuthenticateRequest(context.Background(), req, res); err != nil {
		panic(err)
	}

	fmt.Println("credentials applied")

	// Output:
	// credentials applied
}

// ExampleDigestServer_AuthenticateRequest runs a full challenge round trip:
// the server issues a challenge, a client signs the request, and the server
// returns the application's principal.
func ExampleDigestServer_AuthenticateRequest() {
	const password = "example-password" // dummy secret

	var srv sip.ServerAuthenticator[examplePrincipal]
	srv, err := auth.NewDigestServer(auth.DigestServerOptions[examplePrincipal]{
		ServerOptions: auth.ServerOptions{Realm: "example.com"}, //nolint:modernize // embedded field must stay keyed alongside other fields
		LookupHA1: func(_ context.Context, username, realm, algorithm string) (string, examplePrincipal, error) {
			// The application owns the account store; only known accounts
			// resolve. DigestHA1 derives the stored HA1 representation
			// without exposing the password.
			if username != "alice" {
				return "", examplePrincipal{}, sip.ErrInvalidCredentials
			}
			ha1, err := auth.DigestHA1(username, realm, password, algorithm)
			if err != nil {
				return "", examplePrincipal{}, err
			}
			return ha1, examplePrincipal{ID: "alice", Allowed: true}, nil
		},
	})
	if err != nil {
		panic(err)
	}

	req := sip.NewRequestEnvelope(exampleRequest())

	// First pass: the server challenges the credential-less request. The
	// rejection carries the WWW-Authenticate headers but is not sent.
	var rejected *sip.RequestRejectedError
	if _, err := srv.AuthenticateRequest(context.Background(), req); !errors.As(err, &rejected) {
		panic("expected an authentication challenge")
	}

	// The application owns the response exchange; it copies the rejection's
	// status and headers into its own 401.
	res := sip.NewResponseEnvelope(&sip.Response{
		Status:  rejected.ResponseStatus(),
		Reason:  rejected.ResponseStatus().Reason(),
		Headers: rejected.RespondOptions().ResponseOptions.Headers,
	})

	var cln sip.ClientAuthenticator
	cln, err = auth.NewDigestClient(auth.DigestClientOptions{
		Realm: "example.com",
		Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
			return "alice", password, nil
		},
	})
	if err != nil {
		panic(err)
	}
	if err := cln.AuthenticateRequest(context.Background(), req, res); err != nil {
		panic(err)
	}

	// Second pass: the signed request authenticates and yields the principal.
	p, err := srv.AuthenticateRequest(context.Background(), req)
	if err != nil {
		panic(err)
	}
	if !p.Allowed {
		panic("expected an allowed principal")
	}

	fmt.Println("authenticated")

	// Output:
	// authenticated
}

// ExampleBearerServer_AuthenticateRequest wires a Bearer server through a
// proxy route authorization closure. Authentication success is distinct from
// the application's own authorization decision.
func ExampleBearerServer_AuthenticateRequest() {
	as, err := uri.Parse("https://as.example/token")
	if err != nil {
		panic(err)
	}

	var validations int
	var srv sip.ServerAuthenticator[examplePrincipal]
	srv, err = auth.NewBearerServer(auth.BearerServerOptions[examplePrincipal]{
		ServerOptions: auth.ServerOptions{Realm: "example.com", Proxy: true}, //nolint:modernize // embedded field must stay keyed alongside other fields
		AuthzServer:   as,
		ValidateToken: func(_ context.Context, _ *sip.Request, token string) (examplePrincipal, error) {
			validations++
			// Tokens stay opaque reference values; the application resolves
			// them against its own token store.
			switch token {
			case "allowed-token":
				return examplePrincipal{ID: "alice", Allowed: true}, nil
			case "denied-token":
				return examplePrincipal{ID: "bob", Allowed: false}, nil
			}
			return examplePrincipal{}, sip.ErrInvalidCredentials
		},
	})
	if err != nil {
		panic(err)
	}

	route := &proxy.Route{
		Name: "edge",
		AuthorizeRequest: func(ctx context.Context, req *sip.RequestEnvelope) error {
			// The mechanism authenticates only credential-bearing methods;
			// ACK and CANCEL follow the application's own method rules.
			if req.Method().Equal(sip.RequestMethodAck) || req.Method().Equal(sip.RequestMethodCancel) {
				return nil
			}
			p, err := srv.AuthenticateRequest(ctx, req)
			if err != nil {
				return err
			}
			// Authenticated is not the same as authorized: the application
			// applies its own policy and may reject with any status.
			if !p.Allowed {
				return sip.NewRequestRejectedError(
					errors.New("principal denied"),
					slog.LevelInfo,
					sip.ResponseStatusForbidden,
				)
			}
			return nil
		},
	}

	authenticate := func(token string) error {
		req := sip.NewRequestEnvelope(exampleRequest())
		req.WithMessage(func(msg *sip.Request) {
			msg.Headers.Append(&header.ProxyAuthorization{
				AuthCredentials: &header.BearerCredentials{Token: token},
			})
		})
		return route.AuthorizeRequest(context.Background(), req)
	}

	if err := authenticate("allowed-token"); err != nil {
		panic(err)
	}
	fmt.Println("allowed")

	err = authenticate("denied-token")
	if err == nil {
		panic("expected an authorization rejection")
	}
	var denied *sip.RequestRejectedError
	if !errors.As(err, &denied) || denied.ResponseStatus() != sip.ResponseStatusForbidden {
		panic("expected a 403 authorization rejection")
	}
	fmt.Println("denied")

	// ACK and CANCEL never reach the credential validator.
	for _, method := range []sip.RequestMethod{sip.RequestMethodAck, sip.RequestMethodCancel} {
		req := sip.NewRequestEnvelope(exampleRequest())
		req.WithMessage(func(msg *sip.Request) {
			msg.Method = method
			msg.Headers.Set(&header.CSeq{SeqNum: 1, Method: method})
		})
		if err := route.AuthorizeRequest(context.Background(), req); err != nil {
			panic(err)
		}
	}
	if validations != 2 {
		panic("validator ran for a method that is never authenticated")
	}

	// Output:
	// allowed
	// denied
}
