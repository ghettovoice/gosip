package sip

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

// newSelectCandidate returns a downstream response envelope for the final
// selection tables; extra headers are appended verbatim to the response.
func newSelectCandidate(
	tb testing.TB,
	req *RequestEnvelope,
	sts ResponseStatus,
	hdrs ...Header,
) *ResponseEnvelope {
	tb.Helper()

	res := newInternalInRes(tb, req, sts)
	if len(hdrs) > 0 {
		res.WithMessage(func(msg *Response) {
			msg.Headers.Append(hdrs[0], hdrs[1:]...)
		})
	}
	return res
}

// resChallenges returns every WWW-Authenticate and Proxy-Authenticate header
// the response carries, in header-name order.
func resChallenges(res *ResponseEnvelope) []Header {
	var challenges []Header
	res.WithMessage(func(msg *Response) {
		challenges = append(challenges, msg.Headers.Get("WWW-Authenticate")...)
		challenges = append(challenges, msg.Headers.Get("Proxy-Authenticate")...)
	})
	return challenges
}

// resToTag returns the response's To header tag parameter.
func resToTag(res *ResponseEnvelope) string {
	var tag string
	res.WithMessage(func(msg *Response) {
		if to, ok := msg.Headers.To(); ok {
			tag, _ = to.Tag()
		}
	})
	return tag
}

func TestProxy_SelectFinalResponse(t *testing.T) {
	t.Parallel()

	req := newInternalInviteReq(MagicCookie + ".select-final")
	cand := func(sts ResponseStatus, hdrs ...Header) *ResponseEnvelope {
		return newSelectCandidate(t, req, sts, hdrs...)
	}

	for _, tt := range []struct {
		name  string
		cands []*ResponseEnvelope
		want  ResponseStatus
	}{
		{
			name:  "single final wins",
			cands: []*ResponseEnvelope{cand(ResponseStatusNotFound)},
			want:  ResponseStatusNotFound,
		},
		{
			name: "provisional responses are not candidates",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusRinging),
				cand(ResponseStatusNotFound),
			},
			want: ResponseStatusNotFound,
		},
		{
			name: "6xx outranks every lower class",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusNotFound),
				cand(ResponseStatusBusyHere),
				cand(ResponseStatusBusyEverywhere),
			},
			want: ResponseStatusBusyEverywhere,
		},
		{
			name: "earliest 6xx wins the arrival tie",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusDoesNotExistAnywhere),
				cand(ResponseStatusBusyEverywhere),
			},
			want: ResponseStatusDoesNotExistAnywhere,
		},
		{
			name: "success outranks an earlier 6xx",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusBusyEverywhere),
				cand(ResponseStatusOK),
			},
			want: ResponseStatusOK,
		},
		{
			name: "success outranks a later 6xx",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusOK),
				cand(ResponseStatusBusyEverywhere),
			},
			want: ResponseStatusOK,
		},
		{
			name: "lowest class wins without 6xx",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusServerInternalError),
				cand(ResponseStatusNotFound),
				cand(ResponseStatusMovedTemporarily),
			},
			want: ResponseStatusMovedTemporarily,
		},
		{
			name: "success outranks redirection",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusMovedTemporarily),
				cand(ResponseStatusOK),
			},
			want: ResponseStatusOK,
		},
		{
			name: "401 is preferred inside 4xx",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusNotFound),
				cand(ResponseStatusUnauthorized),
			},
			want: ResponseStatusUnauthorized,
		},
		{
			name: "407 is preferred inside 4xx",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusNotFound),
				cand(ResponseStatusProxyAuthenticationRequired),
			},
			want: ResponseStatusProxyAuthenticationRequired,
		},
		{
			name: "415 is preferred inside 4xx",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusNotFound),
				cand(ResponseStatusUnsupportedMediaType),
			},
			want: ResponseStatusUnsupportedMediaType,
		},
		{
			name: "420 is preferred inside 4xx",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusNotFound),
				cand(ResponseStatusBadExtension),
			},
			want: ResponseStatusBadExtension,
		},
		{
			name: "484 is preferred inside 4xx",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusForbidden),
				cand(ResponseStatusAddressIncomplete),
			},
			want: ResponseStatusAddressIncomplete,
		},
		{
			name: "arrival order decides a preferred 4xx tie",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusAddressIncomplete),
				cand(ResponseStatusUnauthorized),
			},
			want: ResponseStatusAddressIncomplete,
		},
		{
			name: "arrival order decides a plain 4xx tie",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusBusyHere),
				cand(ResponseStatusNotFound),
			},
			want: ResponseStatusBusyHere,
		},
		{
			name: "4xx outranks 5xx",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusServiceUnavailable),
				cand(ResponseStatusNotFound),
			},
			want: ResponseStatusNotFound,
		},
		{
			name: "repeated 503 is not a lone 503",
			cands: []*ResponseEnvelope{
				cand(ResponseStatusServiceUnavailable),
				cand(ResponseStatusServiceUnavailable),
			},
			want: ResponseStatusServiceUnavailable,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			selected, err := selectFinalRes(tt.cands)
			if err != nil {
				t.Fatalf("selectFinalRes() error = %v, want nil", err)
			}

			var gotStatus ResponseStatus
			selected.WithMessage(func(msg *Response) { gotStatus = msg.Status })
			if gotStatus != tt.want {
				t.Fatalf("selected=%v, want %v", gotStatus, tt.want)
			}
		})
	}

	t.Run("empty response context is an error", func(t *testing.T) {
		t.Parallel()

		for _, cands := range [][]*ResponseEnvelope{
			nil,
			{cand(ResponseStatusRinging)},
			{nil, cand(ResponseStatusSessionProgress)},
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

		src := cand(ResponseStatusServiceUnavailable)
		srcTag := resToTag(src)

		selected, err := selectFinalRes([]*ResponseEnvelope{src})
		if err != nil {
			t.Fatalf("selectFinalRes() error = %v, want nil", err)
		}

		var (
			gotStatus ResponseStatus
			gotReason ResponseReason
		)
		selected.WithMessage(func(msg *Response) {
			gotStatus = msg.Status
			gotReason = msg.Reason
		})
		if gotStatus != ResponseStatusServerInternalError {
			t.Fatalf("selected=%v, want %v", gotStatus, ResponseStatusServerInternalError)
		}
		if gotReason != ResponseStatusServerInternalError.Reason() {
			t.Fatalf("selected reason=%q, want %q", gotReason, ResponseStatusServerInternalError.Reason())
		}
		// The mapped response keeps the downstream dialog identity and the
		// stored candidate is never rewritten.
		if tag := resToTag(selected); tag != srcTag {
			t.Fatalf("selected To tag=%q, want %q", tag, srcTag)
		}
		if src.Status() != ResponseStatusServiceUnavailable {
			t.Fatal("selectFinalRes rewrote the stored 503 candidate")
		}
	})

	t.Run("selected 401 merges every challenge verbatim", func(t *testing.T) {
		t.Parallel()

		wwwA := Header(&header.WWWAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "a.example", Nonce: "n-a"},
		})
		wwwB := Header(&header.WWWAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "b.example", Nonce: "n-b"},
		})
		prxAuth := Header(&header.ProxyAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "p.example", Nonce: "n-p"},
		})
		c401 := cand(ResponseStatusUnauthorized, wwwA)
		c407 := cand(ResponseStatusProxyAuthenticationRequired, prxAuth)
		c401b := cand(ResponseStatusUnauthorized, wwwB)
		srcTag := resToTag(c401)

		selected, err := selectFinalRes([]*ResponseEnvelope{c401, c407, c401b})
		if err != nil {
			t.Fatalf("selectFinalRes() error = %v, want nil", err)
		}

		var gotStatus ResponseStatus
		selected.WithMessage(func(msg *Response) { gotStatus = msg.Status })
		if gotStatus != ResponseStatusUnauthorized {
			t.Fatalf("selected=%v, want %v", gotStatus, ResponseStatusUnauthorized)
		}
		// The earliest preferred 401 wins and collects the challenges of
		// every other response in the context: WWW-Authenticate headers in
		// candidate order first, then Proxy-Authenticate.
		wantChallenges := []Header{wwwA, wwwB, prxAuth}
		if diff := cmp.Diff(wantChallenges, resChallenges(selected)); diff != "" {
			t.Fatalf("challenges (-want +got): %s", diff)
		}
		if tag := resToTag(selected); tag != srcTag {
			t.Fatalf("selected To tag=%q, want %q", tag, srcTag)
		}

		// The stored candidates are never rewritten by the aggregation.
		for res, want := range map[*ResponseEnvelope][]Header{
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

		wwwA := Header(&header.WWWAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "a.example", Nonce: "n-a"},
		})
		prxAuth := Header(&header.ProxyAuthenticate{
			AuthChallenge: &header.DigestChallenge{Realm: "p.example", Nonce: "n-p"},
		})
		c486 := cand(ResponseStatusBusyHere)
		c407 := cand(ResponseStatusProxyAuthenticationRequired, prxAuth)
		c401 := cand(ResponseStatusUnauthorized, wwwA)

		selected, err := selectFinalRes([]*ResponseEnvelope{c486, c407, c401})
		if err != nil {
			t.Fatalf("selectFinalRes() error = %v, want nil", err)
		}

		var gotStatus ResponseStatus
		selected.WithMessage(func(msg *Response) { gotStatus = msg.Status })
		if gotStatus != ResponseStatusProxyAuthenticationRequired {
			t.Fatalf("selected=%v, want %v", gotStatus, ResponseStatusProxyAuthenticationRequired)
		}
		wantChallenges := []Header{wwwA, prxAuth}
		if diff := cmp.Diff(wantChallenges, resChallenges(selected)); diff != "" {
			t.Fatalf("challenges (-want +got): %s", diff)
		}
	})

	t.Run("redirection preserves its contacts verbatim", func(t *testing.T) {
		t.Parallel()

		contact := Header(header.Contact{{
			URI: &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alt.example")},
		}})
		c302 := cand(ResponseStatusMovedTemporarily, contact)
		c404 := cand(ResponseStatusNotFound)

		selected, err := selectFinalRes([]*ResponseEnvelope{c302, c404})
		if err != nil {
			t.Fatalf("selectFinalRes() error = %v, want nil", err)
		}

		var (
			gotStatus   ResponseStatus
			gotContacts []Header
		)
		selected.WithMessage(func(msg *Response) {
			gotStatus = msg.Status
			gotContacts = append(gotContacts, msg.Headers.Get("Contact")...)
		})
		if gotStatus != ResponseStatusMovedTemporarily {
			t.Fatalf("selected=%v, want %v", gotStatus, ResponseStatusMovedTemporarily)
		}
		if diff := cmp.Diff([]Header{contact}, gotContacts); diff != "" {
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

	route := ProxyRoute{Name: "test", ForwardMode: ForwardModeStateful}
	req := newInternalInviteReq(MagicCookie + ".choose-outcome")
	cand := func(sts ResponseStatus) *ResponseEnvelope {
		return newSelectCandidate(t, req, sts)
	}
	newRq := func(tb testing.TB) *ProxyRequest {
		tb.Helper()
		_, prx, _ := newInternalTestProxy(tb, route, ProxyOptions{})
		return newPrxReq(tb.Context(), prx, &route, req)
	}
	timeoutErr := func() error { return errors.Wrap(NewTransactionTimedOutError()) }
	sendErr := func() error { return errors.ErrorWrap("dial failed") }

	t.Run("lone 503 maps to 500 despite a sibling timeout outcome", func(t *testing.T) {
		t.Parallel()

		rq := newRq(t)
		rq.finals = []*ResponseEnvelope{cand(ResponseStatusServiceUnavailable)}
		rq.outcomes = []ProxyBranchOutcome{
			{TargetID: "B", Error: timeoutErr(), Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != ProxyOutcomeResponse {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, ProxyOutcomeResponse)
		}
		if got := rq.final.Status(); got != ResponseStatusServerInternalError {
			t.Fatalf("committed final=%v, want %v", got, ResponseStatusServerInternalError)
		}
	})

	t.Run("a racing success commits over an earlier 6xx", func(t *testing.T) {
		t.Parallel()

		rq := newRq(t)
		rq.finals = []*ResponseEnvelope{
			cand(ResponseStatusBusyEverywhere),
			cand(ResponseStatusOK),
		}
		rq.outcomes = []ProxyBranchOutcome{
			{TargetID: "A", Response: rq.finals[0], Completed: true},
			{TargetID: "B", Response: rq.finals[1], Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != ProxyOutcomeResponse {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, ProxyOutcomeResponse)
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
		rq.finals = []*ResponseEnvelope{
			cand(ResponseStatusBusyHere),
			cand(ResponseStatusUnauthorized),
		}
		rq.outcomes = []ProxyBranchOutcome{
			{TargetID: "A", Response: rq.finals[0], Completed: true},
			{TargetID: "B", Response: rq.finals[1], Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != ProxyOutcomeResponse {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, ProxyOutcomeResponse)
		}
		if got := rq.final.Status(); got != ResponseStatusUnauthorized {
			t.Fatalf("committed final=%v, want %v", got, ResponseStatusUnauthorized)
		}
	})

	t.Run("a timeout outcome classifies when no response arrived", func(t *testing.T) {
		t.Parallel()

		rq := newRq(t)
		rq.outcomes = []ProxyBranchOutcome{
			{TargetID: "A", Error: sendErr(), Completed: true},
			{TargetID: "B", Error: timeoutErr(), Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != ProxyOutcomeTimeout {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, ProxyOutcomeTimeout)
		}
		if rq.final != nil {
			t.Fatal("committed a final response for an all-error request")
		}
	})

	t.Run("non-timeout errors classify as send failure", func(t *testing.T) {
		t.Parallel()

		rq := newRq(t)
		rq.outcomes = []ProxyBranchOutcome{
			{TargetID: "A", Error: sendErr(), Completed: true},
		}

		if got := rq.chooseOutcomeLocked(); got != ProxyOutcomeSendFailure {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, ProxyOutcomeSendFailure)
		}
	})

	t.Run("declined-only request classifies as timeout", func(t *testing.T) {
		t.Parallel()

		// Declined targets settle without outcomes: an all-declined request
		// leaves the upstream caller with silence, which is a timeout.
		rq := newRq(t)

		if got := rq.chooseOutcomeLocked(); got != ProxyOutcomeTimeout {
			t.Fatalf("chooseOutcomeLocked()=%v, want %v", got, ProxyOutcomeTimeout)
		}
	})
}
