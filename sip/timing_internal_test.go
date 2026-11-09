package sip

import (
	"math"
	"testing"
	"time"
)

func TestTimingConfig_BaseDefaults(t *testing.T) {
	t.Parallel()

	var (
		negativeCfg = TimingConfig{
			T1:      -time.Second,
			T2:      -time.Second,
			T4:      -time.Second,
			TimeD:   -time.Second,
			Time100: -time.Second,
		}
		positiveCfg = TimingConfig{
			T1:      2 * time.Second,
			T2:      2 * time.Second,
			T4:      2 * time.Second,
			TimeD:   2 * time.Second,
			Time100: 2 * time.Second,
		}
	)

	accessors := []struct {
		name string
		fn   func(TimingConfig) time.Duration
		def  time.Duration
	}{
		{"t1", TimingConfig.t1, T1},
		{"t2", TimingConfig.t2, T2},
		{"t4", TimingConfig.t4, T4},
		{"timeD", TimingConfig.timeD, TimeD},
		{"time100", TimingConfig.time100, Time100},
	}
	for _, acc := range accessors {
		t.Run(acc.name, func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name string
				cfg  TimingConfig
				want time.Duration
			}{
				{"zero fields", TimingConfig{}, acc.def},
				{"negative fields", negativeCfg, acc.def},
				{"positive fields", positiveCfg, 2 * time.Second},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					if got := acc.fn(tt.cfg); got != tt.want {
						t.Errorf("cfg.%s() = %v, want %v", acc.name, got, tt.want)
					}
				})
			}
		})
	}

	t.Run("derived methods use defaults for negative fields", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			fn   func(TimingConfig) time.Duration
			want time.Duration
		}{
			{"TimeA", TimingConfig.TimeA, T1},
			{"TimeB", TimingConfig.TimeB, 64 * T1},
			{"TimeC", TimingConfig.TimeC, 600 * T1},
			{"TimeE", TimingConfig.TimeE, T1},
			{"TimeF", TimingConfig.TimeF, 64 * T1},
			{"TimeG", TimingConfig.TimeG, T1},
			{"TimeH", TimingConfig.TimeH, 64 * T1},
			{"TimeI", TimingConfig.TimeI, T4},
			{"TimeJ", TimingConfig.TimeJ, 64 * T1},
			{"TimeK", TimingConfig.TimeK, T4},
			{"TimeL", TimingConfig.TimeL, 64 * T1},
			{"TimeM", TimingConfig.TimeM, 64 * T1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if got := tt.fn(negativeCfg); got != tt.want {
					t.Errorf("negativeCfg.%s() = %v, want %v", tt.name, got, tt.want)
				}
			})
		}
	})
}

func TestTimingConfig_NonInviteTryingDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     TimingConfig
		want    time.Duration
		wantErr bool
	}{
		// Effective defaults 0.5s and 4s accumulate 0.5 + 1 + 2 = 3.5s.
		{"defaults", TimingConfig{}, 3500 * time.Millisecond, false},
		{
			"non-positive fields use defaults",
			TimingConfig{T1: -time.Second, T2: -time.Second},
			3500 * time.Millisecond, false,
		},
		// 1s + 2s, then the interval reaches T2 = 4s.
		{"t1 1s t2 4s", TimingConfig{T1: time.Second, T2: 4 * time.Second}, 3 * time.Second, false},
		{"t1 reaching t2", TimingConfig{T1: 4 * time.Second, T2: 4 * time.Second}, 0, false},
		{"t1 exceeding t2", TimingConfig{T1: 5 * time.Second, T2: 4 * time.Second}, 0, false},
		// 0.5s fits once: the doubled interval clamps to T2 = 1s.
		{
			"doubling clamps at t2",
			TimingConfig{T1: 500 * time.Millisecond, T2: time.Second},
			500 * time.Millisecond, false,
		},
		// 3s fits once: doubling to 6s clamps to T2 = 4s.
		{"t1 3s t2 4s", TimingConfig{T1: 3 * time.Second, T2: 4 * time.Second}, 3 * time.Second, false},
		// The interval itself is valid; only its naive doubling would
		// overflow, so clamping to T2 keeps the mathematically valid delay.
		{
			"clamped doubling overflow",
			TimingConfig{T1: math.MaxInt64/2 + 1, T2: math.MaxInt64},
			math.MaxInt64/2 + 1, false,
		},
		// The accumulated sum genuinely overflows: 3ns + 6ns + ... exceeds
		// MaxInt64 before the interval reaches T2.
		{"accumulated sum overflow", TimingConfig{T1: 3, T2: math.MaxInt64}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			delay, err := tt.cfg.nonInviteTryingDelay()
			if err != nil {
				if !tt.wantErr {
					t.Fatalf("nonInviteTryingDelay: %v", err)
				}
				return
			}
			if tt.wantErr {
				t.Fatalf("nonInviteTryingDelay() error = nil, want error")
			}
			if got, want := delay, tt.want; got != want {
				t.Fatalf("delay=%s, want %s", got, want)
			}
		})
	}
}
