package header_test

import (
	"io"
	"strings"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

const (
	errFamilyWriter errors.Error = "writer failure"
	errFamilyURI    errors.Error = "nested URI failure"
)

func TestHeaderFamilies_RenderToPropagatesWriterErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hdr  header.Header
	}{
		{"list", header.Require{"timer"}},
		{"name address", &header.From{URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}}},
		{"info address", header.AlertInfo{{URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			writer := &familyLimitedWriter{remaining: 8, err: errFamilyWriter}
			num, err := tt.hdr.RenderTo(writer)

			if !errors.Is(err, errFamilyWriter) {
				t.Errorf("RenderTo() error = %v, want %v", err, errFamilyWriter)
			}
			if num != writer.written {
				t.Errorf("RenderTo() count = %d, accepted bytes = %d", num, writer.written)
			}
		})
	}
}

func TestHeaderFamilies_RenderToPropagatesValueWriterErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		hdr    header.Header
		prefix string
	}{
		{"list", header.Require{"timer"}, "Require: "},
		{"From", &header.From{URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}}, "From: "},
		{"Info", header.AlertInfo{{URI: &uri.SIP{Addr: uri.MakeHostAddr("example.com")}}}, "Alert-Info: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			capacity := len(tt.prefix) + 5
			writer := &familyLimitedWriter{remaining: capacity, err: errFamilyWriter}
			num, err := tt.hdr.RenderTo(writer)

			if !errors.Is(err, errFamilyWriter) {
				t.Errorf("RenderTo() error = %v, want %v", err, errFamilyWriter)
			}
			if num != capacity {
				t.Errorf("RenderTo() count = %d, want capacity %d", num, capacity)
			}
			if writer.written != capacity {
				t.Errorf("writer accepted %d bytes, want capacity %d", writer.written, capacity)
			}
		})
	}
}

func TestHeaderFamilies_RenderToPropagatesNestedURIErrors(t *testing.T) {
	t.Parallel()

	newURI := func() uri.URI {
		return &familyErrorURI{
			URI:       &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
			renderErr: errFamilyURI,
		}
	}
	tests := []struct {
		name string
		hdr  header.Header
	}{
		{"From", &header.From{URI: newURI()}},
		{"Route", header.Route{{URI: newURI()}}},
		{"RecordRoute", header.RecordRoute{{URI: newURI()}}},
		{"AlertInfo", header.AlertInfo{{URI: newURI()}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder
			if _, err := tt.hdr.RenderTo(&sb); !errors.Is(err, errFamilyURI) {
				t.Errorf("RenderTo() error = %v, want %v", err, errFamilyURI)
			}
		})
	}
}

type familyLimitedWriter struct {
	remaining int
	err       error
	written   int
}

func (w *familyLimitedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	num := min(len(p), w.remaining)
	w.remaining -= num
	w.written += num
	if num < len(p) || w.remaining == 0 {
		return num, errors.Wrap(w.err)
	}
	return num, nil
}

var _ io.Writer = (*familyLimitedWriter)(nil)

type familyErrorURI struct {
	uri.URI
	renderErr error
}

func (u *familyErrorURI) RenderTo(w io.Writer, opts ...uri.RenderOptions) (int, error) {
	num, err := u.URI.RenderTo(w, opts...)
	if err != nil {
		return num, errors.Wrap(err)
	}
	if u.renderErr != nil {
		return num, errors.Wrap(u.renderErr)
	}
	return num, nil
}
