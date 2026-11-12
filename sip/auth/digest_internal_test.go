package auth

import (
	"strings"
	"testing"
)

func TestDigestRFC7616Vectors(t *testing.T) {
	t.Parallel()

	// RFC 7616 section 3.9.1 example exchange; the relative HTTP URI is a
	// pure calculation input, not a SIP fixture. Expected values are the
	// published literals, not implementation-derived.
	const (
		username = "Mufasa"
		realm    = "http-auth@example.org"
		password = "Circle of Life"
		method   = "GET"
		uriText  = "/dir/index.html"
		nonce    = "7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v"
		cnonce   = "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ"
	)

	tests := []struct {
		name     string
		algo     string
		wantHA1  string
		wantHA2  string
		wantResp string
	}{
		{
			"md5", "MD5",
			"3d78807defe7de2157e2b0b6573a855f",
			"39aff3a2bab6126f332b942af96d3366",
			"8ca523f5e9506fed4657c9700eebdbec",
		},
		{
			"sha-256", "SHA-256",
			"7987c64c30e25f1b74be53f966b49b90f2808aa92faf9a00262392d7b4794232",
			"9a3fdae9a622fe8de177c24fa9c070f2b181ec85e15dcbdc32e10c82ad450b04",
			"753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotHA1, err := DigestHA1(username, realm, password, tt.algo)
			if err != nil {
				t.Fatalf("auth.DigestHA1() error = %v, want nil", err)
			}
			if gotHA1 != tt.wantHA1 {
				t.Fatalf("auth.DigestHA1() = %q, want %q", gotHA1, tt.wantHA1)
			}

			gotHA2, err := digestHA2(method, uriText, tt.algo)
			if err != nil {
				t.Fatalf("auth.digestHA2() error = %v, want nil", err)
			}
			if gotHA2 != tt.wantHA2 {
				t.Fatalf("auth.digestHA2() = %q, want %q", gotHA2, tt.wantHA2)
			}

			gotResp, err := digestRes(gotHA1, method, uriText, nonce, cnonce, 1, tt.algo)
			if err != nil {
				t.Fatalf("auth.digestResponse() error = %v, want nil", err)
			}
			if gotResp != tt.wantResp {
				t.Fatalf("auth.digestResponse() = %q, want %q", gotResp, tt.wantResp)
			}

			if !matchDigestRes(tt.wantResp, gotResp, tt.algo) {
				t.Fatalf("auth.matchDigestResponse() = false, want true")
			}
		})
	}
}

func TestNormalizeDigestAlgorithm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
		err   bool
	}{
		{"empty means md5", "", "MD5", false},
		{"md5", "MD5", "MD5", false},
		{"md5 lowercase", "md5", "MD5", false},
		{"md5 mixed case", "Md5", "MD5", false},
		{"sha-256", "SHA-256", "SHA-256", false},
		{"sha-256 lowercase", "sha-256", "SHA-256", false},
		{"md5-sess rejected", "MD5-sess", "", true},
		{"sha-512-256 rejected", "SHA-512-256", "", true},
		{"extension rejected", "CustomHash", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeDigestAlgo(tt.input)
			if (err != nil) != tt.err {
				t.Fatalf("auth.normalizeDigestAlgorithm(%q) error = %v, want err %v", tt.input, err, tt.err)
			}
			if got != tt.want {
				t.Errorf("auth.normalizeDigestAlgorithm(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDigestResponse_InvalidInput(t *testing.T) {
	t.Parallel()

	const (
		ha1MD5  = "3d78807defe7de2157e2b0b6573a855f"
		ha1SHA  = "7987c64c30e25f1b74be53f966b49b90f2808aa92faf9a00262392d7b4794232"
		method  = "GET"
		uriText = "/dir/index.html"
		nonce   = "nonce-1"
		cnonce  = "cnonce-1"
	)

	tests := []struct {
		name      string
		ha1       string
		method    string
		uriText   string
		nonce     string
		cnonce    string
		count     uint32
		algorithm string
	}{
		{"count zero", ha1MD5, method, uriText, nonce, cnonce, 0, "MD5"},
		{"empty cnonce", ha1MD5, method, uriText, nonce, "", 1, "MD5"},
		{"empty ha1", "", method, uriText, nonce, cnonce, 1, "MD5"},
		{"uppercase ha1", strings.ToUpper(ha1MD5), method, uriText, nonce, cnonce, 1, "MD5"},
		{"non-hex ha1", "zzzz", method, uriText, nonce, cnonce, 1, "MD5"},
		{"short ha1", "0123456789abcdef", method, uriText, nonce, cnonce, 1, "MD5"},
		{"md5 ha1 for sha-256", ha1MD5, method, uriText, nonce, cnonce, 1, "SHA-256"},
		{"sha-256 ha1 for md5", ha1SHA, method, uriText, nonce, cnonce, 1, "MD5"},
		{"session algorithm", ha1MD5, method, uriText, nonce, cnonce, 1, "MD5-sess"},
		{"unsupported algorithm", ha1MD5, method, uriText, nonce, cnonce, 1, "CustomHash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := digestRes(
				tt.ha1, tt.method, tt.uriText, tt.nonce, tt.cnonce, tt.count, tt.algorithm,
			); err == nil {
				t.Fatal("auth.digestResponse() error = nil, want non-nil")
			}
		})
	}
}

func TestMatchDigestResponse(t *testing.T) {
	t.Parallel()

	const (
		want32 = "8ca523f5e9506fed4657c9700eebdbec"
		want64 = "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1"
	)

	tests := []struct {
		name      string
		got       string
		want      string
		algorithm string
		match     bool
	}{
		{"exact md5", want32, want32, "MD5", true},
		{"exact sha-256", want64, want64, "SHA-256", true},
		{"omitted algorithm is md5", want32, want32, "", true},
		{"one byte differs", "8ca523f5e9506fed4657c9700eebdbed", want32, "MD5", false},
		{"uppercase proof", strings.ToUpper(want32), want32, "MD5", false},
		{"non-hex proof", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", want32, "MD5", false},
		{"short proof", "8ca523f5", want32, "MD5", false},
		{"long proof", want32 + "00", want32, "MD5", false},
		{"empty proof", "", want32, "MD5", false},
		{"size mismatch for algorithm", want32, want32, "SHA-256", false},
		{"malformed want", want32, "zzzz", "MD5", false},
		{"unsupported algorithm", want32, want32, "MD5-sess", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := matchDigestRes(tt.got, tt.want, tt.algorithm); got != tt.match {
				t.Errorf("auth.matchDigestResponse(%q, %q, %q) = %v, want %v",
					tt.got,
					tt.want,
					tt.algorithm,
					got,
					tt.match,
				)
			}
		})
	}
}

func TestDigestHA2_ByteSensitiveInputs(t *testing.T) {
	t.Parallel()

	base, err := digestHA2("GET", "/dir/index.html", "MD5")
	if err != nil {
		t.Fatalf("auth.digestHA2() error = %v, want nil", err)
	}

	for _, tc := range []struct {
		method  string
		uriText string
	}{
		{"get", "/dir/index.html"},
		{"GET", "/dir/index.htmL"},
		{"GET", "/dir/index.htm"},
		{"PUT", "/dir/index.html"},
	} {
		got, err := digestHA2(tc.method, tc.uriText, "MD5")
		if err != nil {
			t.Fatalf("auth.digestHA2(%q, %q) error = %v, want nil", tc.method, tc.uriText, err)
		}
		if got == base {
			t.Errorf("auth.digestHA2(%q, %q) = %q, want it to differ", tc.method, tc.uriText, got)
		}
	}
}
