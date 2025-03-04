package util

import "iter"

func IterFirst[V any](seq iter.Seq[V]) (V, bool) {
	for v := range seq {
		return v, true
	}
	var v V
	return v, false
}

func IterFirst2[K, V any](seq iter.Seq2[K, V]) (K, V, bool) {
	for k, v := range seq {
		return k, v, true
	}
	var (
		k K
		v V
	)
	return k, v, false
}
