package util

// LastSliceElem returns the last element of a slice, or def if the slice is empty.
func LastSliceElem[T any, S ~[]T](s S, def T) T {
	if len(s) == 0 {
		return def
	}
	return s[len(s)-1]
}

func CloneSliceFunc[S ~[]E, E any](s S, fn func(E) E) S {
	if s == nil {
		return nil
	}

	clone := make(S, len(s))
	for k, v := range s {
		clone[k] = fn(v)
	}
	return clone
}

func AppendSliceUniqFunc[S ~[]E, E any, K comparable](s1, s2 S, keyFunc func(E) K) S {
	exists := make(map[K]bool)
	for _, v := range s1 {
		exists[keyFunc(v)] = true
	}
	for _, v := range s2 {
		k := keyFunc(v)
		if !exists[k] {
			s1 = append(s1, v)
			exists[k] = true
		}
	}
	return s1
}
