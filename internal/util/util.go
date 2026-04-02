// Package util provides common utility functions.
package util

import "braces.dev/errtrace"

func Must(e error) {
	if e != nil {
		panic(errtrace.Wrap(e))
	}
}

func Must2[T any](v T, e error) T {
	if e != nil {
		panic(errtrace.Wrap(e))
	}
	return v
}

func Must3[T1, T2 any](v1 T1, v2 T2, e error) (T1, T2) {
	if e != nil {
		panic(errtrace.Wrap(e))
	}
	return v1, v2
}
