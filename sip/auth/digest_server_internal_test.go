package auth

import (
	"context"
	"testing"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
)

type testPrincipal struct {
	ID string
}

func stubLookupHA1(context.Context, string, string, string) (string, testPrincipal, error) {
	return "", testPrincipal{}, nil
}

func testServerOpts() ServerOptions { return ServerOptions{Realm: "example.com"} }

func testPrxServerOpts() ServerOptions { return ServerOptions{Realm: "example.com", Proxy: true} }

func TestNewDigestServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts DigestServerOptions[testPrincipal]
		err  bool
	}{
		{"missing realm", DigestServerOptions[testPrincipal]{LookupHA1: stubLookupHA1}, true},
		{"missing lookup", DigestServerOptions[testPrincipal]{ServerOptions: testServerOpts()}, true},
		{
			"negative ttl",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testServerOpts(),
				LookupHA1:     stubLookupHA1,
				NonceTTL:      -1,
			},
			true,
		},
		{
			"negative capacity",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testServerOpts(),
				LookupHA1:     stubLookupHA1,
				MaxNonces:     -1,
			},
			true,
		},
		{
			"explicit empty algorithms",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testServerOpts(),
				LookupHA1:     stubLookupHA1,
				Algorithms:    []string{},
			},
			true,
		},
		{
			"empty algorithm name",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testServerOpts(),
				LookupHA1:     stubLookupHA1,
				Algorithms:    []string{""},
			},
			true,
		},
		{
			"duplicate algorithms",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testServerOpts(),
				LookupHA1:     stubLookupHA1,
				Algorithms:    []string{"SHA-256", "sha-256"},
			},
			true,
		},
		{
			"unsupported algorithm",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testServerOpts(),
				LookupHA1:     stubLookupHA1,
				Algorithms:    []string{"SHA-512-256"},
			},
			true,
		},
		{
			"capacity below algorithm set",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testServerOpts(),
				LookupHA1:     stubLookupHA1,
				Algorithms:    []string{"SHA-256", "MD5"},
				MaxNonces:     1,
			},
			true,
		},
		{
			"defaults",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testServerOpts(),
				LookupHA1:     stubLookupHA1,
			},
			false,
		},
		{
			"proxy realm",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testPrxServerOpts(),
				LookupHA1:     stubLookupHA1,
			},
			false,
		},
		{
			"explicit md5 preference order",
			DigestServerOptions[testPrincipal]{
				ServerOptions: testServerOpts(),
				LookupHA1:     stubLookupHA1,
				Algorithms:    []string{"MD5", "SHA-256"},
				MaxNonces:     2,
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewDigestServer(tt.opts)
			if (err != nil) != tt.err {
				t.Fatalf("auth.NewDigestServer() error = %v, want err %v", err, tt.err)
			}
			if tt.err {
				return
			}
			if got == nil {
				t.Fatal("auth.NewDigestServer() = nil, want server")
			}
		})
	}

	t.Run("defaults and copied policy", func(t *testing.T) {
		t.Parallel()

		configured := []string{"MD5", "SHA-256"}
		srv, err := NewDigestServer(DigestServerOptions[testPrincipal]{
			ServerOptions: testServerOpts(),
			LookupHA1:     stubLookupHA1,
			Algorithms:    configured,
			MaxNonces:     2,
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		configured[0] = "SHA-512-256"
		if srv.algos[0] != "MD5" || srv.algos[1] != "SHA-256" {
			t.Fatalf("srv.algorithms = %v, want configured order [MD5 SHA-256]", srv.algos)
		}
		if srv.maxNonces != 2 {
			t.Fatalf("srv.maxNonces = %d, want 2", srv.maxNonces)
		}

		zero, err := NewDigestServer(DigestServerOptions[testPrincipal]{
			ServerOptions: testServerOpts(),
			LookupHA1:     stubLookupHA1,
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		if len(zero.algos) != 1 || zero.algos[0] != "SHA-256" {
			t.Fatalf("default algorithms = %v, want [SHA-256]", zero.algos)
		}
		if zero.maxNonces != 4096 {
			t.Fatalf("default maxNonces = %d, want 4096", zero.maxNonces)
		}
		if zero.nonceTTL != defaultDigestNonceTTL {
			t.Fatalf("default nonceTTL = %v, want %v", zero.nonceTTL, defaultDigestNonceTTL)
		}
	})

	t.Run("struct and pointer principals", func(t *testing.T) {
		t.Parallel()

		lookupStruct := func(context.Context, string, string, string) (string, testPrincipal, error) {
			return "", testPrincipal{}, nil
		}
		if _, err := NewDigestServer(DigestServerOptions[testPrincipal]{
			ServerOptions: testServerOpts(),
			LookupHA1:     lookupStruct,
		}); err != nil {
			t.Fatalf("auth.NewDigestServer[testPrincipal]() error = %v, want nil", err)
		}

		lookupPtr := func(context.Context, string, string, string) (string, *testPrincipal, error) {
			return "", nil, nil
		}
		if _, err := NewDigestServer(DigestServerOptions[*testPrincipal]{
			ServerOptions: testServerOpts(),
			LookupHA1:     lookupPtr,
		}); err != nil {
			t.Fatalf("auth.NewDigestServer[*testPrincipal]() error = %v, want nil", err)
		}
	})

	t.Run("uri validator stored", func(t *testing.T) {
		t.Parallel()

		srv, err := NewDigestServer(DigestServerOptions[testPrincipal]{
			ServerOptions: testServerOpts(),
			LookupHA1:     stubLookupHA1,
			ValidateURI:   func(context.Context, *sip.Request, uri.URI) error { return nil },
		})
		if err != nil {
			t.Fatalf("auth.NewDigestServer() error = %v, want nil", err)
		}
		if srv.validateURI == nil {
			t.Fatal("ValidateURI not stored")
		}
	})
}
