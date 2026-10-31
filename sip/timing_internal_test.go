package sip

import (
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
