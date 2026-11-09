package util_test

import (
	"testing"

	"github.com/ghettovoice/gosip/internal/util"
)

func TestEqualValue_OperandContract(t *testing.T) {
	t.Parallel()

	type key string
	type otherKey string

	expected := key("expected")
	different := key("different")
	tests := []struct {
		name      string
		val       any
		want      bool
		wantCalls int
	}{
		{"value match", expected, true, 1},
		{"pointer match", &expected, true, 1},
		{"value mismatch", different, false, 1},
		{"pointer mismatch", &different, false, 1},
		{"typed nil pointer", (*key)(nil), false, 0},
		{"untyped nil", nil, false, 0},
		{"plain string", "expected", false, 0},
		{"other named string", otherKey("expected"), false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls int
			compare := func(other key) bool {
				calls++
				return other == "expected"
			}
			got := util.EqualValue(tt.val, compare)
			if got != tt.want {
				t.Errorf("util.EqualValue() = %v, want %v", got, tt.want)
			}
			if calls != tt.wantCalls {
				t.Errorf("util.EqualValue() comparison calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestEqualPointer_OperandContract(t *testing.T) {
	t.Parallel()

	type value struct {
		key string
	}
	type otherValue value

	same := &value{key: "same"}
	tests := []struct {
		name      string
		left      *value
		val       any
		want      bool
		wantCalls int
	}{
		{"same pointer", same, same, true, 0},
		{"value match", same, value{key: "same"}, true, 1},
		{"pointer match", same, &value{key: "same"}, true, 1},
		{"value mismatch", same, value{key: "different"}, false, 1},
		{"pointer mismatch", same, &value{key: "different"}, false, 1},
		{"both nil pointers", nil, (*value)(nil), true, 0},
		{"nil right pointer", same, (*value)(nil), false, 0},
		{"nil left with pointer", nil, same, false, 0},
		{"nil left with value", nil, value{key: "same"}, false, 0},
		{"other value type", same, otherValue{key: "same"}, false, 0},
		{"other typed nil pointer", nil, (*otherValue)(nil), false, 0},
		{"nil left with untyped nil", nil, nil, false, 0},
		{"untyped nil", same, nil, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls int
			compare := func(other *value) bool {
				calls++
				return other.key == tt.left.key
			}
			got := util.EqualPointer(tt.left, tt.val, compare)
			if got != tt.want {
				t.Errorf("util.EqualPointer() = %v, want %v", got, tt.want)
			}
			if calls != tt.wantCalls {
				t.Errorf("util.EqualPointer() comparison calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}
