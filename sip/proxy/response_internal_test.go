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

func TestDefaultFinalResponseResolver(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		statuses []sip.ResponseStatus
		want     int
		wantErr  bool
	}{
		{
			name:     "nil and provisional entries preserve the original index",
			statuses: []sip.ResponseStatus{0, 180, 404},
			want:     2,
			wantErr:  false,
		},
		{
			name:     "success outranks an earlier 6xx",
			statuses: []sip.ResponseStatus{600, 200},
			want:     1,
			wantErr:  false,
		},
		{
			name:     "6xx outranks an earlier redirection",
			statuses: []sip.ResponseStatus{302, 600},
			want:     1,
			wantErr:  false,
		},
		{
			name:     "401 is preferred over 486",
			statuses: []sip.ResponseStatus{486, 401},
			want:     1,
			wantErr:  false,
		},
		{
			name:     "arrival order decides a plain 4xx tie",
			statuses: []sip.ResponseStatus{480, 486},
			want:     0,
			wantErr:  false,
		},
		{
			name:     "arrival order decides a 6xx tie",
			statuses: []sip.ResponseStatus{604, 600},
			want:     0,
			wantErr:  false,
		},
		{
			name:     "arrival order decides a preferred 4xx tie",
			statuses: []sip.ResponseStatus{484, 401},
			want:     0,
			wantErr:  false,
		},
		{
			name:     "lowest class wins without 6xx",
			statuses: []sip.ResponseStatus{500, 404, 302},
			want:     2,
			wantErr:  false,
		},
		{
			name:     "407 is preferred over 486",
			statuses: []sip.ResponseStatus{486, 407},
			want:     1,
			wantErr:  false,
		},
		{
			name:     "415 is preferred over 486",
			statuses: []sip.ResponseStatus{486, 415},
			want:     1,
			wantErr:  false,
		},
		{
			name:     "420 is preferred over 486",
			statuses: []sip.ResponseStatus{486, 420},
			want:     1,
			wantErr:  false,
		},
		{
			name:     "484 is preferred over 486",
			statuses: []sip.ResponseStatus{486, 484},
			want:     1,
			wantErr:  false,
		},
		{
			name:     "empty candidates",
			statuses: []sip.ResponseStatus{},
			want:     -1,
			wantErr:  true,
		},
		{
			name:     "all nil candidates",
			statuses: []sip.ResponseStatus{0, 0},
			want:     -1,
			wantErr:  true,
		},
		{
			name:     "nil and provisional only candidates",
			statuses: []sip.ResponseStatus{0, 180, 183},
			want:     -1,
			wantErr:  true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := newInternalInviteReq(sip.MagicCookie + ".default-final-resolver")
			candidates := make([]*sip.ResponseEnvelope, len(tt.statuses))
			for i, status := range tt.statuses {
				if status != 0 {
					candidates[i] = newSelectCandidate(t, req, status)
				}
			}

			type candidateState struct {
				Status  sip.ResponseStatus
				Headers []string
			}
			snapshot := func() []candidateState {
				states := make([]candidateState, len(candidates))
				for i, candidate := range candidates {
					if candidate == nil {
						continue
					}
					candidate.WithMessage(func(msg *sip.Response) {
						states[i].Status = msg.Status
						for _, hdr := range msg.Headers.All() {
							states[i].Headers = append(
								states[i].Headers, string(hdr.CanonicName())+": "+hdr.RenderValue(),
							)
						}
					})
				}
				return states
			}
			before := snapshot()
			want, wantErr := tt.want, tt.wantErr

			got, err := DefaultFinalResponseResolver(t.Context(), FinalResponseSelection{Candidates: candidates})
			if (err != nil) != wantErr || got != want {
				t.Fatalf("DefaultFinalResponseResolver() = (%d, %v), want (%d, error=%t)", got, err, want, wantErr)
			}
			if diff := cmp.Diff(before, snapshot()); diff != "" {
				t.Errorf("candidate mutation (-before +after):\n%s", diff)
			}
		})
	}
}

func TestFinalizeRes(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		statuses []sip.ResponseStatus
		index    int
		want     sip.ResponseStatus
		rewrite  bool
		wantErr  bool
	}{
		{name: "empty candidates", index: 0, wantErr: true},
		{name: "negative index", statuses: []sip.ResponseStatus{486}, index: -1, wantErr: true},
		{name: "out of range", statuses: []sip.ResponseStatus{486}, index: 1, wantErr: true},
		{name: "selected nil", statuses: []sip.ResponseStatus{0, 486}, index: 0, wantErr: true},
		{name: "selected provisional", statuses: []sip.ResponseStatus{180, 486}, index: 0, wantErr: true},
		{name: "plain 486 keeps selected pointer", statuses: []sip.ResponseStatus{302, 486}, index: 1, want: 486},
		{name: "success keeps selected pointer", statuses: []sip.ResponseStatus{600, 200}, index: 1, want: 200},
		{name: "does not impose precedence", statuses: []sip.ResponseStatus{600, 200}, index: 0, want: 600},
		{name: "does not impose method rules", statuses: []sip.ResponseStatus{408}, index: 0, want: 408},
		{name: "lone 503 becomes owned 500", statuses: []sip.ResponseStatus{503}, index: 0, want: 500, rewrite: true},
		{
			name:     "lone final 503 ignores nil and provisional entries",
			statuses: []sip.ResponseStatus{0, 180, 503},
			index:    2,
			want:     500,
			rewrite:  true,
		},
		{name: "two 503s keep selected pointer", statuses: []sip.ResponseStatus{503, 503}, index: 1, want: 503},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := newInternalInviteReq(sip.MagicCookie + ".finalize")
			candidates := make([]*sip.ResponseEnvelope, len(tt.statuses))
			before := make([]string, len(candidates))
			for i, status := range tt.statuses {
				if status != 0 {
					candidates[i] = newSelectCandidate(t, req, status)
				}
				before[i] = candidates[i].String()
			}

			selected, err := finalizeRes(candidates, tt.index)
			if (err != nil) != tt.wantErr {
				t.Fatalf("finalizeRes() error = %v, want error=%t", err, tt.wantErr)
			}
			if tt.wantErr {
				if selected != nil || !errors.Is(err, ErrClass) {
					t.Fatalf("finalizeRes() = (%p, %v), want nil and proxy error", selected, err)
				}
				return
			}
			if selected == nil {
				t.Fatal("finalizeRes() = nil, want selected response")
			}
			if got := selected.Status(); got != tt.want {
				t.Fatalf("finalizeRes() status = %v, want %v", got, tt.want)
			}
			source := candidates[tt.index]
			wantReason := source.Reason()
			if tt.rewrite {
				wantReason = tt.want.Reason()
			}
			if got := selected.Reason(); got != wantReason {
				t.Errorf("finalizeRes() reason = %q, want %q", got, wantReason)
			}
			if tag := resToTag(selected); tag != resToTag(source) {
				t.Errorf("finalizeRes() To tag = %q, want %q", tag, resToTag(source))
			}
			if tt.rewrite {
				if selected == source {
					t.Fatal("finalizeRes() returned source pointer, want owned response")
				}
				selected.WithMessage(func(msg *sip.Response) {
					if to, ok := msg.Headers.To(); ok {
						to.Params.Set("tag", "rewritten-final-tag")
					}
				})
			} else if selected != source {
				t.Fatalf("finalizeRes() = (%p, %v), want original %p", selected, err, source)
			}
			for i, candidate := range candidates {
				if diff := cmp.Diff(before[i], candidate.String()); diff != "" {
					t.Errorf("candidate %d mutation (-before +after):\n%s", i, diff)
				}
			}
		})
	}

	for _, status := range []sip.ResponseStatus{401, 407} {
		t.Run(status.String()+" keeps selected tag and aggregates challenges", func(t *testing.T) {
			t.Parallel()

			req := newInternalInviteReq(sip.MagicCookie + ".finalize-auth")
			wwwA := sip.Header(&header.WWWAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "a.example", Nonce: "n-a"},
			})
			wwwA2 := sip.Header(&header.WWWAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "a2.example", Nonce: "n-a2"},
			})
			wwwB := sip.Header(&header.WWWAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "b.example", Nonce: "n-b"},
			})
			wwwC := sip.Header(&header.WWWAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "c.example", Nonce: "n-c"},
			})
			prxA := sip.Header(&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "pa.example", Nonce: "np-a"},
			})
			prxB := sip.Header(&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "pb.example", Nonce: "np-b"},
			})
			prxC := sip.Header(&header.ProxyAuthenticate{
				AuthChallenge: &header.DigestChallenge{Realm: "pc.example", Nonce: "np-c"},
			})
			candidates := []*sip.ResponseEnvelope{
				newSelectCandidate(t, req, 401, wwwA, wwwA2, prxA),
				newSelectCandidate(t, req, status, wwwB, prxB),
				newSelectCandidate(t, req, 407, wwwC, prxC),
				newSelectCandidate(t, req, 180, wwwA),
				nil,
			}
			before := make([]string, len(candidates))
			for i, candidate := range candidates {
				before[i] = candidate.String()
			}

			selected, err := finalizeRes(candidates, 1)
			if err != nil || selected == nil {
				t.Fatalf("finalizeRes() = (%p, %v), want response and nil error", selected, err)
			}
			if selected == candidates[1] {
				t.Fatal("finalizeRes() returned source pointer, want owned response")
			}
			if got := selected.Status(); got != status {
				t.Errorf("finalizeRes() status = %v, want %v", got, status)
			}
			if tag := resToTag(selected); tag != resToTag(candidates[1]) {
				t.Errorf("finalizeRes() To tag = %q, want %q", tag, resToTag(candidates[1]))
			}
			wantChallenges := []sip.Header{wwwB, wwwA, wwwA2, wwwC, prxB, prxA, prxC}
			if diff := cmp.Diff(wantChallenges, resChallenges(selected)); diff != "" {
				t.Errorf("challenges (-want +got):\n%s", diff)
			}
			selected.WithMessage(func(msg *sip.Response) {
				if to, ok := msg.Headers.To(); ok {
					to.Params.Set("tag", "aggregated-final-tag")
				}
			})
			for i, candidate := range candidates {
				if diff := cmp.Diff(before[i], candidate.String()); diff != "" {
					t.Errorf("candidate %d mutation (-before +after):\n%s", i, diff)
				}
			}
		})
	}
}

func TestValidateFinalResIndex(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		statuses []sip.ResponseStatus
		index    int
		invite   bool
		wantErr  bool
	}{
		{name: "empty candidates", index: 0, wantErr: true},
		{name: "negative index", statuses: []sip.ResponseStatus{486}, index: -1, wantErr: true},
		{name: "out of range", statuses: []sip.ResponseStatus{486}, index: 1, wantErr: true},
		{name: "selected nil", statuses: []sip.ResponseStatus{0, 486}, index: 0, wantErr: true},
		{name: "selected provisional", statuses: []sip.ResponseStatus{180, 486}, index: 0, wantErr: true},
		{name: "non-INVITE 408", statuses: []sip.ResponseStatus{408}, index: 0, invite: false, wantErr: true},
		{name: "INVITE 408", statuses: []sip.ResponseStatus{408}, index: 0, invite: true, wantErr: false},
		{name: "486 cannot bypass 600", statuses: []sip.ResponseStatus{486, 600}, index: 0, wantErr: true},
		{name: "600 outranks 486", statuses: []sip.ResponseStatus{486, 600}, index: 1, wantErr: false},
		{name: "600 cannot bypass 200", statuses: []sip.ResponseStatus{600, 200}, index: 0, wantErr: true},
		{name: "200 outranks 600", statuses: []sip.ResponseStatus{600, 200}, index: 1, wantErr: false},
		{name: "486 may bypass 302", statuses: []sip.ResponseStatus{302, 486}, index: 1, wantErr: false},
		{name: "404 may bypass 401", statuses: []sip.ResponseStatus{401, 404}, index: 1, wantErr: false},
		{name: "later 6xx is selectable", statuses: []sip.ResponseStatus{604, 600}, index: 1, wantErr: false},
		{name: "later success is selectable", statuses: []sip.ResponseStatus{200, 202}, index: 1, wantErr: false},
		{
			name:     "non-INVITE 408 cannot bypass another final",
			statuses: []sip.ResponseStatus{408, 500},
			index:    0,
			invite:   false,
			wantErr:  true,
		},
		{
			name:     "nil and provisional entries do not block selected final",
			statuses: []sip.ResponseStatus{0, 180, 486},
			index:    2,
			wantErr:  false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := newInternalInviteReq(sip.MagicCookie + ".validate-final")
			candidates := make([]*sip.ResponseEnvelope, len(tt.statuses))
			before := make([]string, len(candidates))
			for i, status := range tt.statuses {
				if status != 0 {
					candidates[i] = newSelectCandidate(t, req, status)
				}
				before[i] = candidates[i].String()
			}

			err := validateFinalResIndex(candidates, tt.index, tt.invite)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateFinalResIndex() error = %v, want error=%t", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrClass) {
				t.Errorf("validateFinalResIndex() error = %v, want proxy error", err)
			}
			for i, candidate := range candidates {
				if diff := cmp.Diff(before[i], candidate.String()); diff != "" {
					t.Errorf("candidate %d mutation (-before +after):\n%s", i, diff)
				}
			}
		})
	}
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
