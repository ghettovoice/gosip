package useragent_test

import (
	"context"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/useragent"
)

func TestErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sentinel error
		newErr   func() error
	}{
		{
			name:     "invalid config",
			sentinel: useragent.ErrInvalidConfig,
			newErr: func() error {
				return useragent.NewInvalidConfigError("missing field")
			},
		},
		{
			name:     "invalid request",
			sentinel: useragent.ErrInvalidRequest,
			newErr: func() error {
				return useragent.NewInvalidRequestError("missing request URI")
			},
		},
		{
			name:     "missing element",
			sentinel: useragent.ErrMissingElement,
			newErr:   useragent.NewMissingElementError,
		},
		{
			name:     "closed",
			sentinel: useragent.ErrClosed,
			newErr:   useragent.NewClosedError,
		},
		{
			name:     "canceled",
			sentinel: useragent.ErrCanceled,
			newErr: func() error {
				return useragent.NewCanceledError(context.Canceled)
			},
		},
		{
			name:     "authentication limited",
			sentinel: useragent.ErrAuthenticationLimited,
			newErr: func() error {
				return useragent.NewAuthenticationLimitedError("attempts %d", 3)
			},
		},
		{
			name:     "redirect loop",
			sentinel: useragent.ErrRedirectLoop,
			newErr: func() error {
				return useragent.NewRedirectLoopError("target %q", "sip:bob@example.com")
			},
		},
		{
			name:     "response final",
			sentinel: useragent.ErrResponseFinal,
			newErr:   useragent.NewResponseFinalError,
		},
		{
			name:     "cseq exhausted",
			sentinel: useragent.ErrCSeqExhausted,
			newErr:   useragent.NewCSeqExhaustedError,
		},
		{
			name:     "no final response",
			sentinel: useragent.ErrNoFinalResponse,
			newErr:   useragent.NewNoFinalResponseError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.newErr(); err == nil || !errors.Is(err, tt.sentinel) {
				t.Fatalf("constructor error = %v, want %v", err, tt.sentinel)
			}

			err := errors.Join(errors.Wrap(tt.newErr()), errors.New("unrelated"))
			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("joined error lost %v", tt.sentinel)
			}
			if !useragent.IsError(err) || !errors.Is(err, useragent.ErrClass) {
				t.Fatal("lost useragent domain class")
			}
		})
	}

	// An error carrying a foreign domain class is not a useragent error.
	foreign := errclass.Classify(errors.New("element error"), sip.ErrClassElement)
	if useragent.IsError(foreign) || errors.Is(foreign, useragent.ErrClass) {
		t.Fatal("element-domain error classified as a useragent error")
	}

	if !errclass.IsClosed(useragent.NewClosedError()) {
		t.Fatal("lost closed class")
	}
	if !errclass.IsCanceled(useragent.NewCanceledError(context.Canceled)) {
		t.Fatal("lost canceled class")
	}
	deadline := useragent.NewCanceledError(context.DeadlineExceeded)
	if !errclass.IsDeadline(deadline) || !errclass.IsTimeout(deadline) || errclass.IsCanceled(deadline) {
		t.Fatal("deadline cause collapsed into plain cancellation")
	}
	if !errors.Is(deadline, context.DeadlineExceeded) || !errors.Is(deadline, useragent.ErrCanceled) {
		t.Fatal("deadline error lost its cause or sentinel")
	}
	if !errclass.IsConflict(useragent.NewResponseFinalError()) {
		t.Fatal("lost conflict class")
	}
	if err := useragent.NewCanceledError(nil); err == nil || !errors.Is(err, useragent.ErrCanceled) {
		t.Fatalf("useragent.NewCanceledError(nil) = %v, want a live %v error", err, useragent.ErrCanceled)
	}
}
