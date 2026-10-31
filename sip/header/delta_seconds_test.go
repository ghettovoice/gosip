package header_test

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

func TestDeltaSeconds_ParseBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		headerName string
		newHeader  func(time.Duration) header.Header
	}{
		{
			"Expires",
			"Expires",
			func(duration time.Duration) header.Header {
				return &header.Expires{Duration: duration}
			},
		},
		{
			"MinExpires",
			"Min-Expires",
			func(duration time.Duration) header.Header {
				return &header.MinExpires{Duration: duration}
			},
		},
	}
	maxDuration := time.Duration(math.MaxUint32) * time.Second
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, value := range []string{"0", "4294967295"} {
				parsed, err := header.Parse(tt.headerName + ": " + value)
				if err != nil {
					t.Fatalf("header.Parse(%q) error = %v", value, errors.Wrap(err))
				}

				wantDuration := time.Duration(0)
				if value == "4294967295" {
					wantDuration = maxDuration
				}
				if got := tt.newHeader(wantDuration).Equal(parsed); !got {
					t.Errorf("parsed %s value %q does not equal expected duration %v", tt.headerName, value, wantDuration)
				}

				if !parsed.IsValid() {
					t.Errorf("parsed %s value %q is invalid", tt.headerName, value)
				}

				if got := familyRender(t, parsed); got != tt.headerName+": "+value {
					t.Errorf("RenderTo() = %q, want %q", got, tt.headerName+": "+value)
				}
			}
		})
	}
}

func TestDeltaSeconds_RejectsValuesOutsideUint32(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Expires", "Min-Expires"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, value := range []string{
				"4294967296",
				"9223372037",
				"18446744073709551615",
				"18446744073709551616",
			} {
				if _, err := header.Parse(name + ": " + value); err == nil {
					t.Errorf("header.Parse(%q) unexpectedly succeeded", name+": "+value)
				}
			}
		})
	}
}

func TestDeltaSeconds_ProgrammaticValidityAndEquality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		headerName string
		newHeader  func(time.Duration) header.Header
	}{
		{
			"Expires",
			"Expires",
			func(duration time.Duration) header.Header {
				return &header.Expires{Duration: duration}
			},
		},
		{
			"MinExpires",
			"Min-Expires",
			func(duration time.Duration) header.Header {
				return &header.MinExpires{Duration: duration}
			},
		},
	}
	maxDuration := time.Duration(math.MaxUint32) * time.Second
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, test := range []struct {
				name     string
				duration time.Duration
				valid    bool
				rendered string
			}{
				{"negative nanosecond", -time.Nanosecond, false, ""},
				{"negative second", -time.Second, false, ""},
				{"maximum", maxDuration, true, "4294967295"},
				{"above maximum", maxDuration + time.Nanosecond, false, ""},
				{"fractional second", 1500 * time.Millisecond, true, "1"},
			} {
				if got := tt.newHeader(test.duration).IsValid(); got != test.valid {
					t.Errorf("%s validity = %v, want %v", test.name, got, test.valid)
				}

				if test.valid {
					want := tt.headerName + ": " + test.rendered
					if got := familyRender(t, tt.newHeader(test.duration)); got != want {
						t.Errorf("%s render = %q, want %q", test.name, got, want)
					}
				}
			}
		})
	}

	expires := header.Expires{Duration: time.Second}
	minExpires := header.MinExpires{Duration: time.Second}
	expiresValue := expires

	if !expires.Equal(expiresValue) || !expires.Equal(&expires) {
		t.Error("Expires should equal its own value and pointer")
	}
	minExpiresValue := minExpires

	if !minExpires.Equal(minExpiresValue) || !minExpires.Equal(&minExpires) {
		t.Error("MinExpires should equal its own value and pointer")
	}
	if expires.Equal(&minExpires) || minExpires.Equal(&expires) {
		t.Error("Expires and MinExpires should not compare equal across types")
	}
}

func TestDeltaSeconds_OversizedJSONPreservesTargets(t *testing.T) {
	t.Parallel()

	expires := header.Expires{Duration: 42 * time.Second}

	if err := json.Unmarshal([]byte(`{"name":"Expires","value":"4294967296"}`), &expires); err == nil {
		t.Fatal("oversized Expires JSON unexpectedly decoded")
	}

	if expires.Duration != 42*time.Second {
		t.Errorf("Expires after failed decode = %v, want %v", expires.Duration, 42*time.Second)
	}

	minExpires := header.MinExpires{Duration: 42 * time.Second}

	if err := json.Unmarshal([]byte(`{"name":"Min-Expires","value":"4294967296"}`), &minExpires); err == nil {
		t.Fatal("oversized Min-Expires JSON unexpectedly decoded")
	}

	if minExpires.Duration != 42*time.Second {
		t.Errorf("MinExpires after failed decode = %v, want %v", minExpires.Duration, 42*time.Second)
	}
}
