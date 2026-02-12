package util

import "iter"

func SeqFirst[V any](seq iter.Seq[V]) (V, bool) {
	for v := range seq {
		return v, true
	}
	var v V
	return v, false
}

func SeqFirst2[K, V any](seq iter.Seq2[K, V]) (K, V, bool) {
	for k, v := range seq {
		return k, v, true
	}
	var (
		k K
		v V
	)
	return k, v, false
}

func SeqFilter[V any](seq iter.Seq[V], fn func(V) bool) iter.Seq[V] {
	return func(yield func(V) bool) {
		for v := range seq {
			if fn(v) {
				if !yield(v) {
					return
				}
			}
		}
	}
}

func SeqFilter2[K, V any](seq iter.Seq2[K, V], fn func(K, V) bool) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		for k, v := range seq {
			if fn(k, v) {
				if !yield(k, v) {
					return
				}
			}
		}
	}
}

func SeqValues[K, V any](seq iter.Seq2[K, V]) iter.Seq[V] {
	return func(yield func(V) bool) {
		for _, v := range seq {
			if !yield(v) {
				return
			}
		}
	}
}
