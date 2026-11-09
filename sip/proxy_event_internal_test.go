package sip

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// TestProxy_Observers covers the observation subscription contract of
// [Proxy.BindEventHandler] and the private [Proxy.emitEvent] dispatch seam.
func TestProxy_Observers(t *testing.T) {
	t.Parallel()

	t.Run("dispatch works with zero observers", func(t *testing.T) {
		t.Parallel()

		prx := &Proxy{}
		prx.emitEvt(t.Context(), ProxyEvent{
			Kind: ProxyEventRequestHandled,
			Time: time.Now(),
		})
	})

	t.Run("observers receive the same future event through isolated values", func(t *testing.T) {
		t.Parallel()

		prx := &Proxy{}
		evt1 := ProxyEvent{
			Kind:      ProxyEventRequestForwarded,
			Time:      time.Now(),
			RequestID: "rq-1",
			Sequence:  3,
			RouteName: "route-a",
			Mode:      ForwardModeStateful,
			Method:    RequestMethodInvite,
			TargetID:  "1.1",
			AttemptID: "att-1",
		}

		var (
			mu        sync.Mutex
			notified  []string
			notified2 []string
			captured  = make(map[string]ProxyEvent)
			sink      = &notified
		)
		record := func(name string, mutate bool) ProxyEventHandlerFunc {
			return func(_ context.Context, event ProxyEvent) {
				if mutate {
					// Mutating the delivered copy must not corrupt the
					// dispatch value observed by the other subscriber.
					event.RouteName = "mutated-by-" + name
					event.StatusCode = 599
				}

				mu.Lock()
				defer mu.Unlock()
				*sink = append(*sink, name)
				captured[name] = event
			}
		}

		prx.BindEventHandler(record("audit", true))
		unbindStats := prx.BindEventHandler(record("stats", false))

		prx.emitEvt(t.Context(), evt1)

		slices.Sort(notified)
		if diff := cmp.Diff([]string{"audit", "stats"}, notified); diff != "" {
			t.Fatalf("notified (-want +got): %s", diff)
		}

		wantMutated := evt1
		wantMutated.RouteName = "mutated-by-audit"
		wantMutated.StatusCode = 599
		if diff := cmp.Diff(wantMutated, captured["audit"]); diff != "" {
			t.Fatalf("audit event (-want +got): %s", diff)
		}
		if diff := cmp.Diff(evt1, captured["stats"]); diff != "" {
			t.Fatalf("stats event (-want +got): %s", diff)
		}

		unbindStats()
		unbindStats() // idempotent

		sink = &notified2
		evt2 := ProxyEvent{
			Kind:      ProxyEventRequestCompleted,
			Time:      time.Now(),
			RequestID: "rq-1",
			Sequence:  4,
			RouteName: "route-a",
			Mode:      ForwardModeStateful,
			Method:    RequestMethodInvite,
			Outcome:   ProxyOutcomeResponse,
		}
		// The previous synchronous dispatch already returned, so no
		// in-flight captured delivery can still reach the unbound observer.
		prx.emitEvt(t.Context(), evt2)
		if diff := cmp.Diff([]string{"audit"}, notified2); diff != "" {
			t.Fatalf("after unbind (-want +got): %s", diff)
		}
	})

	t.Run("no replay and mid-dispatch reentrancy", func(t *testing.T) {
		t.Parallel()

		prx := &Proxy{}
		evt1 := ProxyEvent{Kind: ProxyEventRequestHandled, Time: time.Now()}
		// Nothing is bound yet: this event is history and must never be
		// replayed to a later subscriber.
		prx.emitEvt(t.Context(), evt1)

		var (
			mu       sync.Mutex
			lateSeen []ProxyEventKind
		)
		var unbindSelf func()
		self := ProxyEventHandlerFunc(func(_ context.Context, _ ProxyEvent) {
			// Public state inspection inside dispatch must not deadlock:
			// emitEvent holds no coordinator or registry lock.
			if _, ok := prx.LookupRequest(ServerTransactionKey{}); ok {
				t.Error("unexpected request lookup hit")
			}

			// A handler bound mid-dispatch observes the next event, never
			// the in-flight one.
			prx.BindEventHandler(ProxyEventHandlerFunc(func(_ context.Context, event ProxyEvent) {
				mu.Lock()
				defer mu.Unlock()
				lateSeen = append(lateSeen, event.Kind)
			}))

			// Self-unbind mid-dispatch is idempotent and does not wait on
			// the in-flight dispatch.
			unbindSelf()
			unbindSelf()
		})
		unbindSelf = prx.BindEventHandler(self)

		prx.emitEvt(t.Context(), evt1)
		evt2 := ProxyEvent{Kind: ProxyEventResponseForwarded, Time: time.Now()}
		prx.emitEvt(t.Context(), evt2)

		mu.Lock()
		defer mu.Unlock()
		if diff := cmp.Diff([]ProxyEventKind{ProxyEventResponseForwarded}, lateSeen); diff != "" {
			t.Fatalf("late observer events (-want +got): %s", diff)
		}
	})

	t.Run("concurrent binds and unbinds are safe", func(t *testing.T) {
		t.Parallel()

		prx := &Proxy{}
		evt := ProxyEvent{Kind: ProxyEventForwardAttemptFinished, Time: time.Now()}
		h := ProxyEventHandlerFunc(func(context.Context, ProxyEvent) {})

		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 50 {
					unbind := prx.BindEventHandler(h)
					unbind()
				}
			})
		}
		for range 50 {
			prx.emitEvt(t.Context(), evt)
		}
		wg.Wait()
	})

	t.Run("event carries no mutable pipeline references", func(t *testing.T) {
		t.Parallel()

		// The detached value is pinned to its declared field types: every
		// correlation field is an immutable value, and no field may alias
		// an envelope, transaction, or coordinator pointer.
		want := map[string]reflect.Type{
			"Kind":       reflect.TypeFor[ProxyEventKind](),
			"Time":       reflect.TypeFor[time.Time](),
			"RequestID":  reflect.TypeFor[string](),
			"Sequence":   reflect.TypeFor[uint64](),
			"RouteName":  reflect.TypeFor[string](),
			"Mode":       reflect.TypeFor[ForwardMode](),
			"Method":     reflect.TypeFor[RequestMethod](),
			"TargetID":   reflect.TypeFor[string](),
			"AttemptID":  reflect.TypeFor[string](),
			"StatusCode": reflect.TypeFor[int](),
			"Outcome":    reflect.TypeFor[ProxyRequestOutcome](),
			"Error":      reflect.TypeFor[error](),
		}
		var fields []string
		for f := range reflect.TypeFor[ProxyEvent]().Fields() {
			fields = append(fields, f.Name)
			wt, ok := want[f.Name]
			if !ok {
				t.Errorf("unexpected ProxyEvent.%s field of type %s", f.Name, f.Type)
				continue
			}
			if f.Type != wt {
				t.Errorf("ProxyEvent.%s type = %s, want %s", f.Name, f.Type, wt)
			}
		}
		if len(fields) != len(want) {
			t.Errorf("ProxyEvent field count = %d, want %d", len(fields), len(want))
		}
	})

	t.Run("event kinds stringify", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			kind ProxyEventKind
			want string
		}{
			{ProxyEventRequestHandled, "request_handled"},
			{ProxyEventForwardAttemptFinished, "forward_attempt_finished"},
			{ProxyEventRequestForwarded, "request_forwarded"},
			{ProxyEventResponseForwarded, "response_forwarded"},
			{ProxyEventRequestCompleted, "request_completed"},
			{ProxyEventKind(99), "unknown(99)"},
		} {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("ProxyEventKind(%d).String() = %q, want %q", int(tt.kind), got, tt.want)
			}
		}
	})
}
