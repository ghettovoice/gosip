package util

import (
	"cmp"
	"strings"
	"sync"
)

func ToUpper[T ~string](s T) T { return T(strings.ToUpper(string(s))) }

func ToLower[T ~string](s T) T { return T(strings.ToLower(string(s))) }

func TrimSpace[T ~string](s T) T { return T(strings.TrimSpace(string(s))) }

func CompareKeys[T ~string](kv1, kv2 []T) int { return cmp.Compare(kv1[0], kv2[0]) }

func EqualFold[T1, T2 ~string](s1 T1, s2 T2) bool {
	return strings.EqualFold(string(s1), string(s2))
}

func Ellipsis(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}

	return string(runes[0:maxLen]) + "..."
}

var strBldrPool = &sync.Pool{
	New: func() any {
		sb := new(strings.Builder)
		sb.Grow(1024)
		return sb
	},
}

func GetStringBuilder() *strings.Builder {
	return strBldrPool.Get().(*strings.Builder) //nolint:forcetypeassert // the pool only stores *strings.Builder
}

func FreeStringBuilder(sb *strings.Builder) {
	sb.Reset()
	strBldrPool.Put(sb)
}
