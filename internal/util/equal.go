package util

// EqualValue compares val as T or a non-nil *T using compare.
// It returns false without calling compare for other types or a nil *T.
func EqualValue[T any](val any, compare func(T) bool) bool {
	switch other := val.(type) {
	case *T:
		return other != nil && compare(*other)
	case T:
		return compare(other)
	default:
		return false
	}
}

// EqualPointer compares value with val, which must be T or *T.
// Identical pointers, including two nil pointers, are equal without calling compare.
// Otherwise, compare is called only for two non-nil pointers.
func EqualPointer[T any](value *T, val any, compare func(*T) bool) bool {
	var other *T
	switch v := val.(type) {
	case *T:
		other = v
	case T:
		other = &v
	default:
		return false
	}
	if value == other {
		return true
	}
	if value == nil || other == nil {
		return false
	}
	return compare(other)
}
