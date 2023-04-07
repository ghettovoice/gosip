package utils

import "iter"

func IterFirst[V any](seq iter.Seq[V]) V {
	var v V
	for v = range seq {
		break
	}
	return v
}

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
