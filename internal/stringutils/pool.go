package stringutils

import (
	"strings"
	"sync"
)

var strBldrPool = &sync.Pool{
	New: func() any {
		sb := new(strings.Builder)
		sb.Grow(1024)
		return sb
	},
}

func NewStrBldr() *strings.Builder {
	return strBldrPool.Get().(*strings.Builder) //nolint:forcetypeassert
}

func FreeStrBldr(sb *strings.Builder) {
	sb.Reset()
	strBldrPool.Put(sb)
}
