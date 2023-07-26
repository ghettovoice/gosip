package sip

import (
	"reflect"
	"testing"
)

func TestAuthFromValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  *Authorization
	}{
		{"quote", `username="1001",realm="sip.com"`,
			&Authorization{username: "1001", realm: "sip.com",
				algorithm: "MD5", other: map[string]string{}}},
		{"quote with blank", `username="1001",realm="sip.com",nonce="a b c"`,
			&Authorization{username: "1001", realm: "sip.com", nonce: "a b c",
				algorithm: "MD5", other: map[string]string{}}},
		{"no quote", `username=1001,realm=sip.com`,
			&Authorization{username: "1001", realm: "sip.com",
				algorithm: "MD5", other: map[string]string{}}},
		{"no quote with blank", `username=1001,realm=sip.com,nonce=a b c`,
			&Authorization{username: "1001", realm: "sip.com", nonce: "a",
				algorithm: "MD5", other: map[string]string{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AuthFromValue(tt.value); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AuthFromValue() = %v, want %v", got, tt.want)
			}
		})
	}
}
