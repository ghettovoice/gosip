package proxy

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// newSelectCandidate returns a downstream response envelope for the final
// selection tables; extra headers are appended verbatim to the response.
func newSelectCandidate(
	tb testing.TB,
	req *sip.RequestEnvelope,
	sts sip.ResponseStatus,
	hdrs ...sip.Header,
) *sip.ResponseEnvelope {
	tb.Helper()

	res := newInternalInRes(tb, req, sts)
	if len(hdrs) > 0 {
		res.WithMessage(func(msg *sip.Response) {
			msg.Headers.Append(hdrs[0], hdrs[1:]...)
		})
	}
	return res
}

// resChallenges returns every WWW-Authenticate and Proxy-Authenticate header
// the response carries, in header-name order.
func resChallenges(res *sip.ResponseEnvelope) []sip.Header {
	var challenges []sip.Header
	res.WithMessage(func(msg *sip.Response) {
		challenges = append(challenges, msg.Headers.Get("WWW-Authenticate")...)
		challenges = append(challenges, msg.Headers.Get("Proxy-Authenticate")...)
	})
	return challenges
}

// resToTag returns the response's To header tag parameter.
func resToTag(res *sip.ResponseEnvelope) string {
	var tag string
	res.WithMessage(func(msg *sip.Response) {
		if to, ok := msg.Headers.To(); ok {
			tag, _ = to.Tag()
		}
	})
	return tag
}

func TestProxy_SelectFinalResponse(t *testing.T) {
	t.Parallel()

	req := newInternalInviteReq(sip.MagicCookie + ".select-final")
	cand := func(sts sip.ResponseStatus, hdrs ...sip.Header) *sip.ResponseEnvelope {
		return newSelectCandidate(t, req, sts, hdrs...)
	}

	for _, tt := range []struct {
		name  string
		cands []*sip.ResponseEnvelope
		want  sip.ResponseStatus
	}{
		{
			name:  "single final wins",
			cands: []*sip.ResponseEnvelope{cand(sip.ResponseStatusNotFound)},
			want:  sip.ResponseStatusNotFound,
		},
		{
			name: "provisional responses are not candidates",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusRinging),
				cand(sip.ResponseStatusNotFound),
			},
			want: sip.ResponseStatusNotFound,
		},
		{
			name: "6xx outranks every lower class",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusNotFound),
				cand(sip.ResponseStatusBusyHere),
				cand(sip.ResponseStatusBusyEverywhere),
			},
			want: sip.ResponseStatusBusyEverywhere,
		},
		{
			name: "earliest 6xx wins the arrival tie",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusDoesNotExistAnywhere),
				cand(sip.ResponseStatusBusyEverywhere),
			},
			want: sip.ResponseStatusDoesNotExistAnywhere,
		},
		{
			name: "success outranks an earlier 6xx",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusBusyEverywhere),
				cand(sip.ResponseStatusOK),
			},
			want: sip.ResponseStatusOK,
		},
		{
			name: "success outranks a later 6xx",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusOK),
				cand(sip.ResponseStatusBusyEverywhere),
			},
			want: sip.ResponseStatusOK,
		},
		{
			name: "lowest class wins without 6xx",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusServerInternalError),
				cand(sip.ResponseStatusNotFound),
				cand(sip.ResponseStatusMovedTemporarily),
			},
			want: sip.ResponseStatusMovedTemporarily,
		},
		{
			name: "success outranks redirection",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusMovedTemporarily),
				cand(sip.ResponseStatusOK),
			},
			want: sip.ResponseStatusOK,
		},
		{
			name: "401 is preferred inside 4xx",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusNotFound),
				cand(sip.ResponseStatusUnauthorized),
			},
			want: sip.ResponseStatusUnauthorized,
		},
		{
			name: "407 is preferred inside 4xx",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusNotFound),
				cand(sip.ResponseStatusProxyAuthenticationRequired),
			},
			want: sip.ResponseStatusProxyAuthenticationRequired,
		},
		{
			name: "415 is preferred inside 4xx",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusNotFound),
				cand(sip.ResponseStatusUnsupportedMediaType),
			},
			want: sip.ResponseStatusUnsupportedMediaType,
		},
		{
			name: "420 is preferred inside 4xx",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusNotFound),
				cand(sip.ResponseStatusBadExtension),
			},
			want: sip.ResponseStatusBadExtension,
		},
		{
			name: "484 is preferred inside 4xx",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusForbidden),
				cand(sip.ResponseStatusAddressIncomplete),
			},
			want: sip.ResponseStatusAddressIncomplete,
		},
		{
			name: "arrival order decides a preferred 4xx tie",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusAddressIncomplete),
				cand(sip.ResponseStatusUnauthorized),
			},
			want: sip.ResponseStatusAddressIncomplete,
		},
		{
			name: "arrival order decides a plain 4xx tie",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusBusyHere),
				cand(sip.ResponseStatusNotFound),
			},
			want: sip.ResponseStatusBusyHere,
		},
		{
			name: "4xx outranks 5xx",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusServiceUnavailable),
				cand(sip.ResponseStatusNotFound),
			},
			want: sip.ResponseStatusNotFound,
		},
		{
			name: "repeated 503 is not a lone 503",
			cands: []*sip.ResponseEnvelope{
				cand(sip.ResponseStatusServiceUnavailable),
				cand(sip.ResponseStatusServiceUnavailable),
			},
			want: sip.ResponseStatusServiceUnavailable,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			selected, err := selectFinalRes(tt.cands)
			if err != nil {
				t.Fatalf("selectFinalRes() error = %v, want nil", err)
			}

			var gotStatus sip.ResponseStatus
			selected.WithMessage(func(msg *sip.Response) { gotStatus = msg.Status })
			if gotStatus != tt.want {
				t.Fatalf("selected=%v, want %v", gotStatus, tt.want)
			}
		})
	}

	t.Run("empty response context is an error", func(t *testing.T) {
		t.Parallel()

		for _, cands := range [][]*sip.ResponseEnvelope{
			nil,
			{cand(sip.ResponseStatusRinging)},
			{nil, cand(sip.ResponseStatusSessionProgress)},
		} {
			if _, err := selectFinalRes(cands); !errors.Is(err, errNoFinalResCandidate) {
				t.Fatalf(
					"selectFinalRes(%d cands) error = %v, want %v",
					len(cands), err, errNoFinalResCandidate,
				)
			}
		}
	})

	t.Run("lone downstream 503 maps to a generated 500", func(t *testing.T) {
		t.Parallel()

		src := cand(sip.ResponseStatusServiceUnavailable)
		srcTag := resToTag(src)

		selected, err := selectFinalRes([]*sip.ResponseEnvelope{src})
		if err != nil {
			t.Fatalf("selectFinalRes() error = %v, want nil", err)
		}

		var (
			gotStatus sip.ResponseStatus
			gotReason sip.ResponseReason
		)
		selected.WithMessage(func(msg *sip.Response) {
			gotStatus = msg.Status
			gotReason = msg.Reason
		})
		if gotStatus != sip.ResponseStatusServerInternalError {
			t.Fatalf("selected=%v, want %v", gotStatus, sip.ResponseStatusServerInternalError)
		}
		if gotReason != sip.ResponseStatusServerInternalError.Reason() {
			t.Fatalf("selected reason=%q, want %q", gotReason, sip.ResponseStatusServerInternalError.Reason())
		}
		// The mapped response keeps the downstream dialog identity and the
		// stored candidate is never rewritten.
		if tag := resToTag(selected); tag != srcTag {
			t.Fatalf("selected To tag=%q, want %q", tag, srcTag)
		}
		if src.Status() != sip.ResponseStatusServiceUnavailable {
			t.Fatal("selectFinalRes rewrote the stored 503 candidate")
		}
	})

	t.Run("selected 401 merges every challenge verbatim", func(t *testing.T) {
		t.Parallel()

		wwwA := sip.Header(&header.WWWAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "a.example", Nonce: "n-a"},
		})
		wwwB := sip.Header(&header.WWWAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "b.example", Nonce: "n-b"},
		})
		prxAuth := sip.Header(&header.ProxyAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "p.example", Nonce: "n-p"},
		})
		c401 := cand(sip.ResponseStatusUnauthorized, wwwA)
		c407 := cand(sip.ResponseStatusProxyAuthenticationRequired, prxAuth)
		c401b := cand(sip.ResponseStatusUnauthorized, wwwB)
		srcTag := resToTag(c401)

		selected, err := selectFinalRes([]*sip.ResponseEnvelope{c401, c407, c401b})
		if err != nil {
			t.Fatalf("selectFinalRes() error = %v, want nil", err)
		}

		var gotStatus sip.ResponseStatus
		selected.WithMessage(func(msg *sip.Response) { gotStatus = msg.Status })
		if gotStatus != sip.ResponseStatusUnauthorized {
			t.Fatalf("selected=%v, want %v", gotStatus, sip.ResponseStatusUnauthorized)
		}
		// The earliest preferred 401 wins and collects the challenges of
		// every other response in the context: WWW-Authenticate headers in
		// candidate order first, then Proxy-Authenticate.
		wantChallenges := []sip.Header{wwwA, wwwB, prxAuth}
		if diff := cmp.Diff(wantChallenges, resChallenges(selected)); diff != "" {
			t.Fatalf("challenges (-want +got): %s", diff)
		}
		if tag := resToTag(selected); tag != srcTag {
			t.Fatalf("selected To tag=%q, want %q", tag, srcTag)
		}

		// The stored candidates are never rewritten by the aggregation.
		for res, want := range map[*sip.ResponseEnvelope][]sip.Header{
			c401:  {wwwA},
			c407:  {prxAuth},
			c401b: {wwwB},
		} {
			if diff := cmp.Diff(want, resChallenges(res)); diff != "" {
				t.Fatalf("stored candidate challenges (-want +got): %s", diff)
			}
		}
	})

	t.Run("selected 407 merges every challenge verbatim", func(t *testing.T) {
		t.Parallel()

		wwwA := sip.Header(&header.WWWAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "a.example", Nonce: "n-a"},
		})
		prxAuth := sip.Header(&header.ProxyAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "p.example", Nonce: "n-p"},
		})
		c486 := cand(sip.ResponseStatusBusyHere)
		c407 := cand(sip.ResponseStatusProxyAuthenticationRequired, prxAuth)
		c401 := cand(sip.ResponseStatusUnauthorized, wwwA)

		selected, err := selectFinalRes([]*sip.ResponseEnvelope{c486, c407, c401})
		if err != nil {
			t.Fatalf("selectFinalRes() error = %v, want nil", err)
		}

		var gotStatus sip.ResponseStatus
		selected.WithMessage(func(msg *sip.Response) { gotStatus = msg.Status })
		if gotStatus != sip.ResponseStatusProxyAuthenticationRequired {
			t.Fatalf("selected=%v, want %v", gotStatus, sip.ResponseStatusProxyAuthenticationRequired)
		}
		wantChallenges := []sip.Header{wwwA, prxAuth}
		if diff := cmp.Diff(wantChallenges, resChallenges(selected)); diff != "" {
			t.Fatalf("challenges (-want +got): %s", diff)
		}
	})

	t.Run("redirection preserves its contacts verbatim", func(t *testing.T) {
		t.Parallel()

		contact := sip.Header(header.Contact{{
			URI: &sip.URI{User: sip.MakeUserInfo("alice"), Addr: sip.MakeHostAddr("alt.example")},
		}})
		c302 := cand(sip.ResponseStatusMovedTemporarily, contact)
		c404 := cand(sip.ResponseStatusNotFound)

		selected, err := selectFinalRes([]*sip.ResponseEnvelope{c302, c404})
		if err != nil {
			t.Fatalf("selectFinalRes() error = %v, want nil", err)
		}

		var (
			gotStatus   sip.ResponseStatus
			gotContacts []sip.Header
		)
		selected.WithMessage(func(msg *sip.Response) {
			gotStatus = msg.Status
			gotContacts = append(gotContacts, msg.Headers.Get("Contact")...)
		})
		if gotStatus != sip.ResponseStatusMovedTemporarily {
			t.Fatalf("selected=%v, want %v", gotStatus, sip.ResponseStatusMovedTemporarily)
		}
		if diff := cmp.Diff([]sip.Header{contact}, gotContacts); diff != "" {
			t.Fatalf("contacts (-want +got): %s", diff)
		}
	})
}

// TestProxyRequest_FinalOutcomeSelection pins the wiring between the settled
// attempt signals and selectFinalRes: response candidates carry the
// selection, declined branches contribute nothing, and the error
// classification only runs when no usable final response exists.
func TestProxyRequest_FinalOutcomeSelection(t *testing.T) {
	t.Parallel()

	route := Route{Name: "test", ForwardMode: ForwardModeStateful}
	req := newInternalInviteReq(sip.MagicCookie + ".choose-outcome")
	cand := func(sts sip.ResponseStatus) *sip.ResponseEnvelope {
		return newSelectCandidate(t, req, sts)
	}
	newRq := func(tb testing.TB) *Request {
		tb.Helper()
		elm, prx, _ := newInternalTestProxy(tb, route, Options{})
		return newReq(tb.Context(), elm, prx, &route, req, "choose-outcome")
	}
	timeoutErr := func() error { return errors.Wrap(sip.NewTransactionTimedOutError()) }
	sendErr := func() error { return errors.ErrorWrap("dial failed") }

	t.Run("lone 503 maps to 500 despite a sibling timeout outcome", func(t *testing.T) {
		t.Parallel()

		rq := newRq(t)
		rq.finals = []*sip.ResponseEnvelope{cand(sip.ResponseStatusServiceUnavailable)}
		rq.outcomes = []BranchOutcome{
			{TargetID: "B", Error: timeoutErr(), Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != OutcomeResponse {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, OutcomeResponse)
		}
		if got := rq.final.Status(); got != sip.ResponseStatusServerInternalError {
			t.Fatalf("committed final=%v, want %v", got, sip.ResponseStatusServerInternalError)
		}
	})

	t.Run("a racing success commits over an earlier 6xx", func(t *testing.T) {
		t.Parallel()

		rq := newRq(t)
		rq.finals = []*sip.ResponseEnvelope{
			cand(sip.ResponseStatusBusyEverywhere),
			cand(sip.ResponseStatusOK),
		}
		rq.outcomes = []BranchOutcome{
			{TargetID: "A", Response: rq.finals[0], Completed: true},
			{TargetID: "B", Response: rq.finals[1], Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != OutcomeResponse {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, OutcomeResponse)
		}
		// The committed final is the 2xx itself, by pointer identity: the
		// send dedup then makes the committed send a no-op for an
		// already-forwarded success.
		if rq.final != rq.finals[1] {
			t.Fatalf("committed final=%v, want the 200 candidate", rq.final)
		}
	})

	t.Run("selection ranks the accumulated finals", func(t *testing.T) {
		t.Parallel()

		rq := newRq(t)
		rq.finals = []*sip.ResponseEnvelope{
			cand(sip.ResponseStatusBusyHere),
			cand(sip.ResponseStatusUnauthorized),
		}
		rq.outcomes = []BranchOutcome{
			{TargetID: "A", Response: rq.finals[0], Completed: true},
			{TargetID: "B", Response: rq.finals[1], Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != OutcomeResponse {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, OutcomeResponse)
		}
		if got := rq.final.Status(); got != sip.ResponseStatusUnauthorized {
			t.Fatalf("committed final=%v, want %v", got, sip.ResponseStatusUnauthorized)
		}
	})

	t.Run("a timeout outcome classifies when no response arrived", func(t *testing.T) {
		t.Parallel()

		rq := newRq(t)
		rq.outcomes = []BranchOutcome{
			{TargetID: "A", Error: sendErr(), Completed: true},
			{TargetID: "B", Error: timeoutErr(), Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != OutcomeTimeout {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, OutcomeTimeout)
		}
		if rq.final != nil {
			t.Fatal("committed a final response for an all-error request")
		}
	})

	t.Run("non-timeout errors classify as send failure", func(t *testing.T) {
		t.Parallel()

		rq := newRq(t)
		rq.outcomes = []BranchOutcome{
			{TargetID: "A", Error: sendErr(), Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != OutcomeSendFailure {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, OutcomeSendFailure)
		}
	})

	t.Run("declined-only request classifies as timeout", func(t *testing.T) {
		t.Parallel()

		// Declined targets settle without outcomes: an all-declined request
		// leaves the upstream caller with silence, which is a timeout.
		rq := newRq(t)

		if got := rq.chooseOutcomeLocked(); got != OutcomeTimeout {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, OutcomeTimeout)
		}
	})
}
