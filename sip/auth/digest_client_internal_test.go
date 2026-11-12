package auth

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func stubDigestCreds(context.Context, *sip.Request, *header.DigestChallenge, bool) (username, password string, err error) {
	return "root", "secret", nil
}

func TestNewDigestClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts DigestClientOptions
		err  bool
	}{
		{"missing realm", DigestClientOptions{Credentials: stubDigestCreds}, true},
		{"missing provider", DigestClientOptions{Realm: "example.com"}, true},
		{
			"negative capacity",
			DigestClientOptions{Realm: "example.com", Credentials: stubDigestCreds, MaxNonces: -1},
			true,
		},
		{
			"explicit empty algorithms",
			DigestClientOptions{Realm: "example.com", Credentials: stubDigestCreds, Algorithms: []string{}},
			true,
		},
		{
			"empty algorithm name",
			DigestClientOptions{Realm: "example.com", Credentials: stubDigestCreds, Algorithms: []string{""}},
			true,
		},
		{
			"empty algorithm name among valid",
			DigestClientOptions{
				Realm:       "example.com",
				Credentials: stubDigestCreds,
				Algorithms:  []string{"SHA-256", ""},
			},
			true,
		},
		{
			"unsupported algorithm",
			DigestClientOptions{
				Realm:       "example.com",
				Credentials: stubDigestCreds,
				Algorithms:  []string{"MD5-sess"},
			},
			true,
		},
		{
			"duplicate algorithms",
			DigestClientOptions{
				Realm:       "example.com",
				Credentials: stubDigestCreds,
				Algorithms:  []string{"SHA-256", "sha-256"},
			},
			true,
		},
		{"defaults", DigestClientOptions{Realm: "example.com", Credentials: stubDigestCreds}, false},
		{
			"explicit md5",
			DigestClientOptions{
				Realm:       "example.com",
				Credentials: stubDigestCreds,
				Algorithms:  []string{"MD5", "SHA-256"},
				MaxNonces:   2,
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewDigestClient(tt.opts)
			if (err != nil) != tt.err {
				t.Fatalf("auth.NewDigestClient() error = %v, want err %v", err, tt.err)
			}
			if tt.err {
				return
			}
			if got == nil {
				t.Fatal("auth.NewDigestClient() = nil, want client")
			}
		})
	}

	t.Run("defaults and copied policy", func(t *testing.T) {
		t.Parallel()

		configured := []string{"MD5", "SHA-256"}
		client, err := NewDigestClient(DigestClientOptions{
			Realm:       "example.com",
			Credentials: stubDigestCreds,
			Algorithms:  configured,
			MaxNonces:   2,
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}
		configured[0] = "SHA-512-256"
		if client.algos[0] != "MD5" {
			t.Fatalf("client.algorithms = %v, want copied policy", client.algos)
		}
		if client.maxNonces != 2 {
			t.Fatalf("client.maxNonces = %d, want 2", client.maxNonces)
		}

		zero, err := NewDigestClient(DigestClientOptions{Realm: "example.com", Credentials: stubDigestCreds})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}
		if len(zero.algos) != 1 || zero.algos[0] != "SHA-256" {
			t.Fatalf("default algorithms = %v, want [SHA-256]", zero.algos)
		}
		if zero.maxNonces != 1024 {
			t.Fatalf("default maxNonces = %d, want 1024", zero.maxNonces)
		}
	})
}

func TestSelectDigestChallenge(t *testing.T) {
	t.Parallel()

	digest := func(realm, nonce, algorithm string, qop ...string) *header.DigestChallenge {
		return &header.DigestChallenge{Realm: realm, Nonce: nonce, Algorithm: algorithm, QOP: qop}
	}
	algorithms := []string{"MD5", "SHA-256"}

	tests := []struct {
		name       string
		challenges []*header.DigestChallenge
		wantNonce  string
		wantAlgo   string
	}{
		{
			"stronger challenge follows legacy",
			[]*header.DigestChallenge{
				digest("example.com", "first-md5", "MD5"),
				digest("example.com", "second-sha", "SHA-256"),
			},
			"second-sha",
			"SHA-256",
		},
		{
			"sha-256 preferred regardless allowlist order",
			[]*header.DigestChallenge{
				digest("example.com", "first-sha", "SHA-256"),
				digest("example.com", "second-md5", "MD5"),
			},
			"first-sha",
			"SHA-256",
		},
		{
			"same algorithm wire tie",
			[]*header.DigestChallenge{
				digest("example.com", "first-sha", "SHA-256"),
				digest("example.com", "second-sha", "SHA-256"),
			},
			"first-sha",
			"SHA-256",
		},
		{
			"other realm skipped",
			[]*header.DigestChallenge{
				digest("other.com", "other-sha", "SHA-256"),
				digest("example.com", "ours-md5", "MD5"),
			},
			"ours-md5",
			"MD5",
		},
		{
			"case-sensitive realm",
			[]*header.DigestChallenge{
				digest("Example.com", "other-sha", "SHA-256"),
				digest("example.com", "ours-md5", "MD5"),
			},
			"ours-md5",
			"MD5",
		},
		{
			"missing nonce skipped",
			[]*header.DigestChallenge{
				digest("example.com", "", "SHA-256"),
				digest("example.com", "ours-md5", "MD5"),
			},
			"ours-md5",
			"MD5",
		},
		{
			"auth-int only skipped",
			[]*header.DigestChallenge{
				digest("example.com", "int-only", "SHA-256", "auth-int"),
				digest("example.com", "ours-md5", "MD5"),
			},
			"ours-md5",
			"MD5",
		},
		{
			"malformed qop token skipped",
			[]*header.DigestChallenge{
				{Realm: "example.com", Nonce: "bad-qop", Algorithm: "SHA-256", QOP: []string{"auth", "bad token"}},
				digest("example.com", "ours-md5", "MD5"),
			},
			"ours-md5",
			"MD5",
		},
		{
			"malformed domain skipped",
			[]*header.DigestChallenge{
				{Realm: "example.com", Nonce: "bad-domain", Algorithm: "SHA-256", Domain: []uri.URI{&uri.SIP{}}},
				digest("example.com", "ours-md5", "MD5"),
			},
			"ours-md5",
			"MD5",
		},
		{
			"malformed params skipped",
			[]*header.DigestChallenge{
				{Realm: "example.com", Nonce: "bad-params", Algorithm: "SHA-256", Params: header.Values{"not a token": {"v"}}},
				digest("example.com", "ours-md5", "MD5"),
			},
			"ours-md5",
			"MD5",
		},
		{
			"missing qop uses auth",
			[]*header.DigestChallenge{digest("example.com", "no-qop", "SHA-256")},
			"no-qop", "SHA-256",
		},
		{
			"qop list with auth",
			[]*header.DigestChallenge{digest("example.com", "auth-and-int", "SHA-256", "auth", "auth-int")},
			"auth-and-int",
			"SHA-256",
		},
		{
			"unsupported algorithm skipped",
			[]*header.DigestChallenge{
				digest("example.com", "sha512", "SHA-512-256"),
				digest("example.com", "ours-md5", "MD5"),
			},
			"ours-md5",
			"MD5",
		},
		{
			"disabled algorithm skipped",
			[]*header.DigestChallenge{
				digest("example.com", "sha", "SHA-256"),
				digest("example.com", "md5", "MD5"),
			},
			"md5",
			"MD5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			allowed := algorithms
			if tt.name == "disabled algorithm skipped" {
				allowed = []string{"MD5"}
			}
			got := selectDigestChal(tt.challenges, "example.com", allowed)
			if got == nil {
				t.Fatalf("auth.selectDigestChallenge() = nil, want nonce %q", tt.wantNonce)
			}
			if got.Nonce != tt.wantNonce {
				t.Fatalf("auth.selectDigestChallenge() nonce = %q, want %q", got.Nonce, tt.wantNonce)
			}
			if algo, _ := normalizeDigestAlgo(got.Algorithm); algo != tt.wantAlgo {
				t.Fatalf("auth.selectDigestChallenge() algorithm = %q, want %q", algo, tt.wantAlgo)
			}
		})
	}

	t.Run("nil challenge and no match", func(t *testing.T) {
		t.Parallel()

		if got := selectDigestChal(nil, "example.com", algorithms); got != nil {
			t.Fatalf("auth.selectDigestChallenge(nil) = %+v, want nil", got)
		}
		foreign := []*header.DigestChallenge{digest("other.com", "n", "SHA-256"), nil}
		if got := selectDigestChal(foreign, "example.com", algorithms); got != nil {
			t.Fatalf("auth.selectDigestChallenge() = %+v, want nil", got)
		}
	})

	t.Run("malformed only selects nothing", func(t *testing.T) {
		t.Parallel()

		challenges := []*header.DigestChallenge{
			{Realm: "example.com", Nonce: "bad-qop", Algorithm: "SHA-256", QOP: []string{"auth", "bad token"}},
			{Realm: "example.com", Nonce: "bad-domain", Algorithm: "SHA-256", Domain: []uri.URI{&uri.SIP{}}},
		}
		if got := selectDigestChal(challenges, "example.com", algorithms); got != nil {
			t.Fatalf("auth.selectDigestChallenge() = %+v, want nil for malformed challenges", got)
		}
	})
}

func TestDigestClient_NextNonceCount(t *testing.T) {
	t.Parallel()

	newClient := func(t *testing.T, maxNonces int) *DigestClient {
		t.Helper()

		client, err := NewDigestClient(DigestClientOptions{
			Realm:       "example.com",
			Credentials: stubDigestCreds,
			MaxNonces:   maxNonces,
		})
		if err != nil {
			t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
		}
		return client
	}

	t.Run("counts start at one and are scoped by leg and nonce", func(t *testing.T) {
		t.Parallel()

		client := newClient(t, 0)

		cnonce1, first, err := client.nextNonceCount(false, "nonce-1")
		if err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		_, second, err := client.nextNonceCount(false, "nonce-1")
		if err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		_, otherLeg, err := client.nextNonceCount(true, "nonce-1")
		if err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		_, otherNonce, err := client.nextNonceCount(false, "nonce-2")
		if err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}

		if first != 1 || second != 2 || otherLeg != 1 || otherNonce != 1 {
			t.Fatalf("counts = %d/%d/%d/%d, want 1/2/1/1: nonce counters not scoped by auth leg and nonce",
				first,
				second,
				otherLeg,
				otherNonce,
			)
		}
		if len(cnonce1) != 32 {
			t.Fatalf("cnonce = %q, want 32 lowercase hex digits", cnonce1)
		}
	})

	t.Run("session keeps its cnonce", func(t *testing.T) {
		t.Parallel()

		client := newClient(t, 0)
		cnonce1, _, err := client.nextNonceCount(false, "nonce-1")
		if err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		cnonce2, _, err := client.nextNonceCount(false, "nonce-1")
		if err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		if cnonce1 != cnonce2 {
			t.Fatalf("session cnonce changed: %q then %q", cnonce1, cnonce2)
		}
	})

	t.Run("capacity evicts the least recent session", func(t *testing.T) {
		t.Parallel()

		client := newClient(t, 2)

		oldCnonce, _, err := client.nextNonceCount(false, "nonce-1")
		if err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		oldCnonce2, _, err := client.nextNonceCount(false, "nonce-2")
		if err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		// Refresh nonce-1 so nonce-2 becomes the oldest.
		if _, _, err := client.nextNonceCount(false, "nonce-1"); err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		if _, _, err := client.nextNonceCount(false, "nonce-3"); err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		if len(client.sess) != 2 {
			t.Fatalf("sessions = %d, want capacity 2", len(client.sess))
		}
		if _, ok := client.sess[digestNonceSessKey{proxy: false, nonce: "nonce-2"}]; ok {
			t.Fatal("least recent session was not evicted")
		}

		// An evicted session restarts with a fresh cnonce at count 1.
		newCnonce, count, err := client.nextNonceCount(false, "nonce-2")
		if err != nil {
			t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
		}
		if count != 1 || newCnonce == oldCnonce2 || newCnonce == oldCnonce {
			t.Fatalf("evicted session restarted with count %d cnonce %q, want 1 and fresh", count, newCnonce)
		}
	})

	t.Run("default capacity stays bounded", func(t *testing.T) {
		t.Parallel()

		client := newClient(t, 0)
		for i := 0; i <= defDigestNonceCap; i++ {
			if _, _, err := client.nextNonceCount(false, fmt.Sprintf("nonce-%d", i)); err != nil {
				t.Fatalf("client.nextNonceCount() error = %v, want nil", err)
			}
		}
		if len(client.sess) != defDigestNonceCap {
			t.Fatalf("sessions = %d, want bounded %d", len(client.sess), defDigestNonceCap)
		}
	})

	t.Run("parallel reservations produce unique counts", func(t *testing.T) {
		t.Parallel()

		client := newClient(t, 0)
		const workers = 16
		var wg sync.WaitGroup
		counts := make(chan uint32, workers)
		for range workers {
			wg.Go(func() {
				_, count, err := client.nextNonceCount(false, "nonce-1")
				if err != nil {
					t.Errorf("client.nextNonceCount() error = %v, want nil", err)
					return
				}
				counts <- count
			})
		}
		wg.Wait()
		close(counts)

		seen := make(map[uint32]struct{}, workers)
		for count := range counts {
			if _, dup := seen[count]; dup {
				t.Fatalf("duplicate reserved count %d", count)
			}
			seen[count] = struct{}{}
		}
		if len(seen) != workers {
			t.Fatalf("reserved counts = %d, want %d", len(seen), workers)
		}
	})

	t.Run("exhaustion is an ordinary failure without wrap", func(t *testing.T) {
		t.Parallel()

		client := newClient(t, 0)
		key := digestNonceSessKey{proxy: false, nonce: "nonce-1"}
		sess := &digestNonceSess{cnonce: "seeded", elem: client.lru.PushFront(key)}
		sess.count.Store(math.MaxUint32)
		client.sess[key] = sess

		_, _, err := client.nextNonceCount(false, "nonce-1")
		if err == nil {
			t.Fatal("client.nextNonceCount() error = nil, want non-nil")
		}
		if errors.Is(err, sip.ErrUnsupportedChallenge) {
			t.Fatalf("exhaustion error = %v, want ordinary failure not unsupported challenge", err)
		}
		if sess.count.Load() != math.MaxUint32 {
			t.Fatalf("count = %d, want unwrapped max", sess.count.Load())
		}
	})
}

// TestDigestClient_AuthenticateRequest_RenderFailure covers a request URI whose renderer fails
// after emitting partial text: the render error must surface instead of
// signing the partial render, no provider runs, and the request stays
// untouched.
func TestDigestClient_AuthenticateRequest_RenderFailure(t *testing.T) {
	t.Parallel()

	var calls int
	cln, err := NewDigestClient(DigestClientOptions{
		Realm: "example.com",
		Credentials: func(context.Context, *sip.Request, *header.DigestChallenge, bool) (string, string, error) {
			calls++
			return "alice", "secret", nil
		},
	})
	if err != nil {
		t.Fatalf("auth.NewDigestClient() error = %v, want nil", err)
	}

	req := sip.NewRequestEnvelope(&sip.Request{
		Method: sip.RequestMethodInvite,
		URI:    &fakeURI{text: "sip:alice@example.com", renderErr: errTestSentinel},
		Headers: make(sip.Headers).
			Set(header.Via{{
				Proto:     header.ProtoInfo{Name: "SIP", Version: "2.0"},
				Transport: "UDP",
				Addr:      uri.MakeHostAddr("client.example.com"),
			}}).
			Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInvite}),
	})
	before, _ := req.Message().Clone().(*sip.Request)
	res := sip.NewResponseEnvelope(&sip.Response{
		Status: sip.ResponseStatusUnauthorized,
		Reason: sip.ResponseStatusUnauthorized.Reason(),
		Headers: make(sip.Headers).Set(&header.WWWAuthenticate{
			AuthChallenge: &header.DigestChallenge{
				Realm:     "example.com",
				Nonce:     "fixed-nonce",
				Algorithm: "SHA-256",
				QOP:       []string{"auth"},
			},
		}),
	})

	err = cln.AuthenticateRequest(t.Context(), req, res)
	if !errors.Is(err, errTestSentinel) {
		t.Fatalf("cln.AuthenticateRequest() error = %v, want %v", err, errTestSentinel)
	}
	if calls != 0 {
		t.Fatalf("provider calls = %d, want 0", calls)
	}
	if diff := cmp.Diff(before, req.Message()); diff != "" {
		t.Fatalf("request mutated after renderer failure (-want +got):\n%s", diff)
	}
}
