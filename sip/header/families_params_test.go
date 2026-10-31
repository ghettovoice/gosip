package header_test

import (
	"testing"

	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

func TestNameAddr_TagValueCaseSensitivity(t *testing.T) {
	t.Parallel()

	sipURI := &uri.SIP{Addr: uri.MakeHostAddr("example.com")}
	newNameAddr := func(params header.Values) header.NameAddr {
		return header.NameAddr{URI: sipURI, Params: params}
	}
	params := func(key, value string) header.Values {
		return make(header.Values).Set(key, value)
	}

	tests := []struct {
		name  string
		left  header.NameAddr
		right header.NameAddr
		want  bool
	}{
		{
			"tag differs by case",
			newNameAddr(params("tag", "AbC")),
			newNameAddr(params("tag", "abc")),
			false,
		},
		{
			"same tag",
			newNameAddr(params("tag", "AbC")),
			newNameAddr(params("tag", "AbC")),
			true,
		},
		{
			"tag name differs by case",
			newNameAddr(params("TAG", "AbC")),
			newNameAddr(params("tag", "AbC")),
			true,
		},
		{
			"tag missing on right",
			newNameAddr(params("tag", "AbC")),
			newNameAddr(nil),
			false,
		},
		{
			"generic unquoted value ignores case",
			newNameAddr(params("foo", "Bar")),
			newNameAddr(params("foo", "bar")),
			true,
		},
		{
			"generic quoted value preserves case",
			newNameAddr(params("foo", `"Bar"`)),
			newNameAddr(params("foo", `"bar"`)),
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.left.Equal(tt.right); got != tt.want {
				t.Errorf("NameAddr.Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFromTo_TagValueCaseSensitivity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		left  header.Header
		right header.Header
	}{
		{
			"From",
			&header.From{
				URI:    &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
				Params: make(header.Values).Set("tag", "AbC"),
			},
			&header.From{
				URI:    &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
				Params: make(header.Values).Set("tag", "abc"),
			},
		},
		{
			"To",
			&header.To{
				URI:    &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
				Params: make(header.Values).Set("tag", "AbC"),
			},
			&header.To{
				URI:    &uri.SIP{Addr: uri.MakeHostAddr("example.com")},
				Params: make(header.Values).Set("tag", "abc"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.left.Equal(tt.right) {
				t.Errorf("%T.Equal() = true for tag values differing only by case", tt.left)
			}

			if !tt.left.Equal(tt.left.Clone()) {
				t.Errorf("%T.Equal(Clone()) = false for matching tag values", tt.left)
			}
		})
	}
}
