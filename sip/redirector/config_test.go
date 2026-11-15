package redirector_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/redirector"
)

func TestNew(t *testing.T) {
	t.Parallel()

	resolveContacts := func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
		return header.Contact{}, nil
	}

	tests := []struct {
		name    string
		routes  []redirector.Route
		opts    []redirector.Options
		wantErr string
	}{
		{
			name: "minimal route",
			routes: []redirector.Route{{
				Name:            "r",
				ResolveContacts: resolveContacts,
			}},
		},
		{
			name:    "nil resolver",
			routes:  []redirector.Route{{Name: "r"}},
			wantErr: "nil contact resolver",
		},
		{
			name: "constant status 400",
			routes: []redirector.Route{{
				Name:            "r",
				ResolveContacts: resolveContacts,
				Status:          sip.ResponseStatusBadRequest,
			}},
			wantErr: "unsupported status",
		},
		{
			name: "zero status accepted",
			routes: []redirector.Route{{
				Name:            "r",
				ResolveContacts: resolveContacts,
				Status:          0,
			}},
		},
		{
			name:   "empty routes",
			routes: []redirector.Route{},
		},
		{
			name:   "nil routes",
			routes: nil,
		},
		{
			name:   "explicit zero options",
			routes: []redirector.Route{{Name: "r", ResolveContacts: resolveContacts}},
			opts:   []redirector.Options{{}},
		},
		{
			name:   "empty options slice",
			routes: []redirector.Route{{Name: "r", ResolveContacts: resolveContacts}},
			opts:   []redirector.Options{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rdr, err := redirector.New(tt.routes, tt.opts...)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("redirector.New() error = nil, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("redirector.New() error = %q, want containing %q", err, tt.wantErr)
				}
				if !errors.Is(err, sip.ErrClassElement) {
					t.Fatalf("redirector.New() error = %v, want classified as %v", err, sip.ErrClassElement)
				}
				return
			}
			if err != nil {
				t.Fatalf("redirector.New() error = %v, want nil", err)
			}
			if rdr == nil {
				t.Fatal("redirector.New() returned nil redirector, want non-nil")
			}
		})
	}
}

// TestNew_Options verifies the constructor's options selection through the
// fallback logger: the resolved-route debug line goes to the logger of the
// selected [redirector.Options] value.
func TestNew_Options(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// makeOpts builds the constructor options from the subtest's fresh
		// loggers; wantLog is the index of the logger expected to record
		// the route-resolution debug line, or -1 for none.
		makeOpts func(logs []*slog.Logger) []redirector.Options
		numLogs  int
		wantLog  int
	}{
		{
			name: "single custom logger",
			makeOpts: func(logs []*slog.Logger) []redirector.Options {
				return []redirector.Options{{Logger: logs[0]}}
			},
			numLogs: 1,
			wantLog: 0,
		},
		{
			name: "last of two loggers wins",
			makeOpts: func(logs []*slog.Logger) []redirector.Options {
				return []redirector.Options{{Logger: logs[0]}, {Logger: logs[1]}}
			},
			numLogs: 2,
			wantLog: 1,
		},
		{
			name: "trailing zero options resets logger",
			makeOpts: func(logs []*slog.Logger) []redirector.Options {
				return []redirector.Options{{Logger: logs[0]}, {}}
			},
			numLogs: 1,
			wantLog: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bufs := make([]*bytes.Buffer, tt.numLogs)
			logs := make([]*slog.Logger, tt.numLogs)
			for i := range logs {
				bufs[i] = &bytes.Buffer{}
				logs[i] = slog.New(slog.NewTextHandler(bufs[i], &slog.HandlerOptions{
					Level: slog.LevelDebug,
				}))
			}

			rdr, err := redirector.New([]redirector.Route{{
				Name: "test",
				ResolveContacts: func(context.Context, *sip.RequestEnvelope) (header.Contact, error) {
					return testContacts, nil
				},
			}}, tt.makeOpts(logs)...)
			if err != nil {
				t.Fatalf("redirector.New() error = %v, want nil", err)
			}
			t.Cleanup(func() { rdr.Close() })

			// A claimed request resolved without a processing element logs
			// through the configured fallback logger before failing with
			// ErrMissingElement.
			next := sip.RequestReceiverFunc(func(context.Context, *sip.RequestEnvelope) error {
				return nil
			})
			err = rdr.InterceptInboundRequest(t.Context(), next, newTestReq(sip.RequestMethodMessage))
			if !errors.Is(err, redirector.ErrMissingElement) {
				t.Fatalf("rdr.InterceptInboundRequest() error = %v, want %v", err, redirector.ErrMissingElement)
			}

			for i, buf := range bufs {
				got := buf.String()
				if i == tt.wantLog {
					if !strings.Contains(got, "redirector route resolved") {
						t.Fatalf("logger %d output = %q, want containing %q", i, got, "redirector route resolved")
					}
				} else if got != "" {
					t.Fatalf("logger %d output = %q, want empty", i, got)
				}
			}
		})
	}
}
