package util

import (
	"crypto/rand"

	"braces.dev/errtrace"
)

const charset = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func randStr(n int, cs string) string {
	buf := make([]byte, n)

	_, err := rand.Read(buf)
	if err != nil {
		panic(errtrace.Wrap(err))
	}

	for i, b := range buf {
		buf[i] = cs[b%byte(len(cs))]
	}

	return string(buf)
}

func RandString(n int) string {
	return randStr(n, charset)
}

func RandStringLC(n int) string {
	return randStr(n, charset[:36])
}
