// Package iterutils provides utilities for working with iter package.
package iterutils

import "iter"

// IterFirst returns the first value of the given sequence.
func IterFirst[V any](seq iter.Seq[V]) V {
	var v V
	for v = range seq {
		break
	}
	return v
}

// IterFirst2 returns the first key-value pair of the given sequence.
func IterFirst2[K, V any](seq iter.Seq2[K, V]) (K, V) {
	var (
		k K
		v V
	)
	for k, v = range seq {
		break
	}
	return k, v
}
