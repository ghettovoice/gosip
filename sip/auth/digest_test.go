package auth_test

import (
	"testing"

	"github.com/ghettovoice/gosip/sip/auth"
)

func TestDigestHA1(t *testing.T) {
	t.Parallel()

	// RFC 7616 section 3.9.1 literals.
	const (
		username = "Mufasa"
		realm    = "http-auth@example.org"
		password = "Circle of Life"
	)

	tests := []struct {
		name      string
		algorithm string
		want      string
	}{
		{"md5", "MD5", "3d78807defe7de2157e2b0b6573a855f"},
		{"sha-256", "SHA-256", "7987c64c30e25f1b74be53f966b49b90f2808aa92faf9a00262392d7b4794232"},
		{"omitted algorithm is md5", "", "3d78807defe7de2157e2b0b6573a855f"},
		{"algorithm name case-insensitive", "sha-256", "7987c64c30e25f1b74be53f966b49b90f2808aa92faf9a00262392d7b4794232"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := auth.DigestHA1(username, realm, password, tt.algorithm)
			if err != nil {
				t.Fatalf("auth.DigestHA1() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Fatalf("auth.DigestHA1() = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("empty password", func(t *testing.T) {
		t.Parallel()

		if _, err := auth.DigestHA1(username, realm, "", "SHA-256"); err != nil {
			t.Fatalf("auth.DigestHA1() error = %v, want nil", err)
		}
	})

	t.Run("unsupported algorithm", func(t *testing.T) {
		t.Parallel()

		for _, algorithm := range []string{"MD5-sess", "SHA-512-256", "CustomHash"} {
			if _, err := auth.DigestHA1(username, realm, password, algorithm); err == nil {
				t.Fatalf("auth.DigestHA1(algorithm=%q) error = nil, want non-nil", algorithm)
			}
		}
	})
}
