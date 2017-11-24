// Forked from github.com/StefanKopieczek/gossip by @StefanKopieczek
package util

import (
	"crypto/rand"
	"fmt"
)

// Check two string pointers for equality as follows:
// - If neither pointer is nil, check equality of the underlying strings.
// - If either pointer is nil, return true if and only if they both are.
func StrPtrEq(a *string, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// Check two uint16 pointers for equality as follows:
// - If neither pointer is nil, check equality of the underlying uint16s.
// - If either pointer is nil, return true if and only if they both are.
func Uint16PtrEq(a *uint16, b *uint16) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

func RandStr(length int, args ...interface{}) string {
	if length == 0 {
		length = 8
	}

	buf := make([]byte, length)
	rand.Read(buf)

	var prefix string
	if len(args) > 0 {
		prefix = fmt.Sprintf("%s", args[0])
	}

	return fmt.Sprintf("%s%x", prefix, buf)
}

func Coalesce(arg1 interface{}, arg2 interface{}, args ...interface{}) interface{} {
	all := append([]interface{}{arg1, arg2}, args...)
	for _, arg := range all {
		if arg != nil {
			return arg
		}
	}

	return nil
}
