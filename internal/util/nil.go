package util

import "reflect"

// IsNil reports whether v is nil. It handles typed nil values stored in
// interfaces by checking reflect kinds that can be nil.
func IsNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return true
	}

	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func InitIfNil[T any](v *T) {
	if v == nil {
		return
	}

	rv := reflect.ValueOf(v).Elem()
	if rv.Kind() == reflect.Pointer && rv.IsNil() {
		rv.Set(reflect.New(rv.Type().Elem()))
	}
}
