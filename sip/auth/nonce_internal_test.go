package auth

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

func TestNonceStore(t *testing.T) {
	t.Parallel()

	const (
		realm = "example.com"
		user  = "alice"
	)

	newStore := func(t *testing.T, capacity int) *nonceStore {
		t.Helper()
		return newNonceStore(capacity)
	}

	t.Run("issue snapshot and commit progression", func(t *testing.T) {
		t.Parallel()

		store := newStore(t, 4)
		nonces, err := store.issue(t.Context(), realm, []string{"SHA-256"}, time.Minute)
		if err != nil {
			t.Fatalf("store.issue() error = %v, want nil", err)
		}
		if len(nonces) != 1 || len(nonces[0]) != 32 {
			t.Fatalf("nonces = %v, want one 32-hex value", nonces)
		}
		nonce := nonces[0]

		rec, ok := store.snapshot(nonce)
		if !ok {
			t.Fatal("store.snapshot() = false for issued nonce")
		}
		if rec.realm != realm || rec.algo != "SHA-256" || rec.user != "" || rec.count != 0 {
			t.Fatalf("snapshot = %+v, want unbound fresh record", rec)
		}
		rec.count = 99 // mutating the snapshot must not affect the store
		if again, _ := store.snapshot(nonce); again.count != 0 {
			t.Fatal("snapshot shares mutable state with the store")
		}

		outcome, err := store.commit(t.Context(), nonce, realm, "SHA-256", user, 1)
		if err != nil || outcome != nonceAccepted {
			t.Fatalf("store.commit() = %v, %v, want accepted", outcome, err)
		}
		for _, count := range []uint32{0, 1} {
			if outcome, _ := store.commit(t.Context(), nonce, realm, "SHA-256", user, count); outcome != nonceInvalid {
				t.Fatalf("store.commit(count=%d) = %v, want invalid", count, outcome)
			}
		}
		if outcome, _ := store.commit(t.Context(), nonce, realm, "SHA-256", user, 2); outcome != nonceAccepted {
			t.Fatalf("store.commit(count=2) = %v, want accepted", outcome)
		}
		if outcome, _ := store.commit(t.Context(), nonce, realm, "SHA-256", "mallory", 3); outcome != nonceInvalid {
			t.Fatalf("store.commit(foreign username) = %v, want invalid", outcome)
		}
		if outcome, _ := store.commit(t.Context(), nonce, "other.com", "SHA-256", user, 3); outcome != nonceInvalid {
			t.Fatalf("store.commit(foreign realm) = %v, want invalid", outcome)
		}
		if outcome, _ := store.commit(t.Context(), nonce, realm, "MD5", user, 3); outcome != nonceInvalid {
			t.Fatalf("store.commit(foreign algorithm) = %v, want invalid", outcome)
		}
	})

	t.Run("reading a nonce never consumes it", func(t *testing.T) {
		t.Parallel()

		store := newStore(t, 4)
		nonces, _ := store.issue(t.Context(), realm, []string{"SHA-256"}, time.Minute)
		for range 4 {
			if _, ok := store.snapshot(nonces[0]); !ok {
				t.Fatal("store.snapshot() = false for issued nonce")
			}
		}
		if outcome, _ := store.commit(t.Context(), nonces[0], realm, "SHA-256", user, 1); outcome != nonceAccepted {
			t.Fatalf("store.commit() after snapshots = %v, want accepted", outcome)
		}
	})

	t.Run("invented nonce is never installed", func(t *testing.T) {
		t.Parallel()

		store := newStore(t, 4)
		if _, ok := store.snapshot("invented"); ok {
			t.Fatal("store.snapshot() = true for unissued nonce")
		}
		if outcome, _ := store.commit(t.Context(), "invented", realm, "SHA-256", user, 1); outcome != nonceInvalid {
			t.Fatalf("store.commit(invented) = %v, want invalid", outcome)
		}
		if _, ok := store.snapshot("invented"); ok {
			t.Fatal("commit of invented nonce installed a record")
		}
	})

	t.Run("complete algorithm set is issued atomically", func(t *testing.T) {
		t.Parallel()

		store := newStore(t, 3)
		if _, err := store.issue(t.Context(), realm, []string{"SHA-256"}, time.Minute); err != nil {
			t.Fatalf("store.issue() error = %v, want nil", err)
		}
		nonces, err := store.issue(t.Context(), realm, []string{"SHA-256", "MD5"}, time.Minute)
		if err != nil {
			t.Fatalf("store.issue() error = %v, want nil", err)
		}
		if len(nonces) != 2 || nonces[0] == nonces[1] {
			t.Fatalf("nonces = %v, want two distinct values", nonces)
		}
		// The two-member set fits capacity 3 without evicting itself.
		for i, algo := range []string{"SHA-256", "MD5"} {
			rec, ok := store.snapshot(nonces[i])
			if !ok || rec.algo != algo {
				t.Fatalf("store.snapshot(%q) = %+v, %v, want %s record", nonces[i], rec, ok, algo)
			}
		}
	})

	t.Run("oldest issued is evicted first", func(t *testing.T) {
		t.Parallel()

		store := newStore(t, 2)
		first, _ := store.issue(t.Context(), realm, []string{"SHA-256"}, time.Minute)
		second, _ := store.issue(t.Context(), realm, []string{"SHA-256"}, time.Minute)
		// Reading the oldest must not keep it alive.
		_, _ = store.snapshot(first[0])
		third, _ := store.issue(t.Context(), realm, []string{"SHA-256"}, time.Minute)

		if _, ok := store.snapshot(first[0]); ok {
			t.Fatal("oldest issued record was not evicted")
		}
		for _, nonce := range []string{second[0], third[0]} {
			if _, ok := store.snapshot(nonce); !ok {
				t.Fatalf("store.snapshot(%q) = false, want retained", nonce)
			}
		}
		if len(store.recs) != 2 {
			t.Fatalf("records = %d, want capacity 2", len(store.recs))
		}
	})

	t.Run("expired record stays distinguishable until eviction", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			store := newStore(t, 4)
			nonces, err := store.issue(t.Context(), realm, []string{"SHA-256"}, time.Minute)
			if err != nil {
				t.Fatalf("store.issue() error = %v, want nil", err)
			}
			time.Sleep(2 * time.Minute)

			rec, ok := store.snapshot(nonces[0])
			if !ok {
				t.Fatal("store.snapshot() = false for expired but retained nonce")
			}
			if time.Now().Before(rec.expires) {
				t.Fatal("record still within ttl")
			}
			if outcome, _ := store.commit(t.Context(), nonces[0], realm, "SHA-256", user, 1); outcome != nonceExpired {
				t.Fatalf("store.commit(expired) = %v, want expired", outcome)
			}
		})
	})

	t.Run("canceled issue and commit fail cleanly", func(t *testing.T) {
		t.Parallel()

		store := newStore(t, 4)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := store.issue(ctx, realm, []string{"SHA-256"}, time.Minute); !errors.Is(err, context.Canceled) {
			t.Fatalf("store.issue() error = %v, want %v", err, context.Canceled)
		}
		nonces, _ := store.issue(t.Context(), realm, []string{"SHA-256"}, time.Minute)
		if outcome, err := store.commit(ctx, nonces[0], realm, "SHA-256", user, 1); !errors.Is(err, context.Canceled) || outcome == nonceAccepted {
			t.Fatalf("store.commit() = %v, %v, want canceled", outcome, err)
		}
	})

	t.Run("simultaneous commits admit exactly one", func(t *testing.T) {
		t.Parallel()

		store := newStore(t, 4)
		nonces, _ := store.issue(t.Context(), realm, []string{"SHA-256"}, time.Minute)

		const workers = 8
		var wg sync.WaitGroup
		outcomes := make(chan nonceOutcome, workers)
		for range workers {
			wg.Go(func() {
				outcome, err := store.commit(t.Context(), nonces[0], realm, "SHA-256", user, 1)
				if err != nil {
					t.Errorf("store.commit() error = %v, want nil", err)
					return
				}
				outcomes <- outcome
			})
		}
		wg.Wait()
		close(outcomes)

		accepted := 0
		for outcome := range outcomes {
			if outcome == nonceAccepted {
				accepted++
			}
		}
		if accepted != 1 {
			t.Fatalf("accepted commits = %d, want exactly 1", accepted)
		}
	})
}
