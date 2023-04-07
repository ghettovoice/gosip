package utils

import cmp2 "github.com/google/go-cmp/cmp"

func IsValid(v any) bool {
	vv, ok := v.(interface{ IsValid() bool })
	return ok && vv.IsValid()
}

func IsEqual(v1, v2 any) bool {
	if v1, ok := v1.(interface{ Equal(v2 any) bool }); ok {
		return v1.Equal(v2)
	} else if v2, ok := v2.(interface{ Equal(v1 any) bool }); ok {
		return v2.Equal(v1)
	}
	return cmp2.Equal(v1, v2)
}

func Clone[T any](v any) T {
	if v1, ok := v.(interface{ Clone() T }); ok {
		return v1.Clone()
	}
	if v == nil {
		var zero T
		return zero
	}
	return v.(T)
}

func IsTemporaryErr(err error) bool {
	e, ok := err.(interface{ Temporary() bool })
	return ok && e.Temporary()
}

func IsTimeoutErr(err error) bool {
	e, ok := err.(interface{ Timeout() bool })
	return ok && e.Timeout()
}

func IsGrammarErr(err error) bool {
	e, ok := err.(interface{ Grammar() bool })
	return ok && e.Grammar()
}
