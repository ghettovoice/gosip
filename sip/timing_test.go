package sip_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

func TestTimingConfig_MarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  sip.TimingConfig
		want string
	}{
		{
			"zero",
			sip.TimingConfig{},
			`{"t1":500000000,"t2":4000000000,"t4":5000000000,"time_d":32000000000,"time_100":200000000}`,
		},
		{
			"full",
			sip.NewTimings(700*time.Millisecond, 8*time.Second, 3*time.Second, 42*time.Second, 250*time.Millisecond),
			`{"t1":700000000,"t2":8000000000,"t4":3000000000,"time_d":42000000000,"time_100":250000000}`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(c.cfg)
			if err != nil {
				t.Fatalf("json.Marshal(cfg) error = %v, want nil", err)
			}

			if got := string(got); got != c.want {
				t.Fatalf("json.Marshal(cfg) = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTimingConfig_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		data    string
		want    sip.TimingConfig
		wantErr bool
	}{
		{"zero", `{}`, sip.TimingConfig{}, false},
		{
			"full",
			`{"t1":700000000,"t2":8000000000,"t4":3000000000,"time_d":42000000000,"time_100":250000000}`,
			sip.NewTimings(700*time.Millisecond, 8*time.Second, 3*time.Second, 42*time.Second, 250*time.Millisecond),
			false,
		},
		{"invalid json", `{"t1":`, sip.TimingConfig{}, true},
		{"invalid field type", `{"t1":"700ms"}`, sip.TimingConfig{}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var got sip.TimingConfig
			if err := json.Unmarshal([]byte(c.data), &got); err != nil {
				if !c.wantErr {
					t.Fatalf("json.Unmarshal(data, &got) error = %v, want nil", err)
				}
				return
			}

			if c.wantErr {
				t.Fatal("json.Unmarshal(data, &got) error = nil, want error")
			}

			assertTimingConfig(t, got, c.want)
		})
	}
}

func TestTimingConfig_UnmarshalJSON_NilReceiver(t *testing.T) {
	t.Parallel()

	var cfg *sip.TimingConfig
	if err := cfg.UnmarshalJSON([]byte(`{}`)); err == nil {
		t.Fatal("cfg.UnmarshalJSON(data) error = nil, want error")
	}
}

func assertTimingConfig(t *testing.T, got, want sip.TimingConfig) {
	t.Helper()

	if got.T1() != want.T1() {
		t.Fatalf("got.T1() = %v, want %v", got.T1(), want.T1())
	}

	if got.T2() != want.T2() {
		t.Fatalf("got.T2() = %v, want %v", got.T2(), want.T2())
	}

	if got.T4() != want.T4() {
		t.Fatalf("got.T4() = %v, want %v", got.T4(), want.T4())
	}

	if got.TimeD() != want.TimeD() {
		t.Fatalf("got.TimeD() = %v, want %v", got.TimeD(), want.TimeD())
	}

	if got.Time100() != want.Time100() {
		t.Fatalf("got.Time100() = %v, want %v", got.Time100(), want.Time100())
	}

	if got.IsZero() != want.IsZero() {
		t.Fatalf("got.IsZero() = %t, want %t", got.IsZero(), want.IsZero())
	}
}
