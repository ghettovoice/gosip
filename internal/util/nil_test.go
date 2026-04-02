package util_test

import (
	"testing"

	"github.com/ghettovoice/gosip/internal/util"
)

func TestIsNil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"nil interface", nil, true},
		{"non-nil value", 42, false},
		{"typed nil pointer", func() any {
			var p *int
			return p
		}(), true},
		{"non-nil pointer", new(int), false},
		{"typed nil slice", func() any {
			var s []int
			return s
		}(), true},
		{"non-nil slice", []int{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := util.IsNil(tt.val); got != tt.want {
				t.Fatalf("IsNil(%T) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}
