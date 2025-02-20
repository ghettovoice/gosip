package randutils

import "crypto/rand"

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func RandString(n int) string {
	buf := make([]byte, n)
	_, err := rand.Read(buf)
	if err != nil {
		panic(err)
	}
	for i, b := range buf {
		buf[i] = charset[b%byte(len(charset))]
	}
	return string(buf)
}
