package sip

import (
	"time"
)

// Default values for SIP timers as described in RFC 3261.
const (
	// T1 is the message RTT estimate.
	T1 = 500 * time.Millisecond
	// T2 is the maximum retransmit interval for non-INVITE requests and INVITE responses.
	T2 = 4 * time.Second
	// T4 is the maximum duration a message will remain in the network.
	T4 = 5 * time.Second
	// TimeD is the wait duration for response retransmits via unreliable transport.
	TimeD = 32 * time.Second
	// Time100 is the timeout for automatic 100 Trying response on INVITE.
	Time100 = 200 * time.Millisecond
)

// TimingConfig represents SIP timing config.
// It is used to configure SIP timers as described in RFC 3261.
// Zero value uses default base values [T1], [T2], [T4], [TimeD], [Time100].
// All other timings are calculated based on these base values.
type TimingConfig struct {
	// T1 is the message RTT estimate.
	// It is equal to [T1] if not specified.
	T1 time.Duration `json:"t1,omitempty"`
	// T2 is the maximum retransmit interval for non-INVITE requests and INVITE responses.
	// It is equal to [T2] if not specified.
	T2 time.Duration `json:"t2,omitempty"`
	// T4 is the maximum duration a message will remain in the network.
	// It is equal to [T4] if not specified.
	T4 time.Duration `json:"t4,omitempty"`
	// TimeD is the wait duration for response retransmits via unreliable transport.
	// It is equal to [TimeD] if not specified.
	TimeD time.Duration `json:"time_d,omitempty"`
	// Time100 is the timeout for automatic 100 Trying response on INVITE.
	// It is equal to [Time100] if not specified.
	Time100 time.Duration `json:"time_100,omitempty"`
}

// NewTimings creates a new SIP timing config with specified base values.
// See [TimingConfig] for more details about how base timing values are used.
func NewTimings(t1, t2, t4, timeD, time100 time.Duration) TimingConfig {
	return TimingConfig{t1, t2, t4, timeD, time100}
}

func (c TimingConfig) t1() time.Duration {
	if c.T1 == 0 {
		return T1
	}
	return c.T1
}

func (c TimingConfig) t2() time.Duration {
	if c.T2 == 0 {
		return T2
	}
	return c.T2
}

func (c TimingConfig) t4() time.Duration {
	if c.T4 == 0 {
		return T4
	}
	return c.T4
}

func (c TimingConfig) timeD() time.Duration {
	if c.TimeD == 0 {
		return TimeD
	}
	return c.TimeD
}

func (c TimingConfig) time100() time.Duration {
	if c.Time100 == 0 {
		return Time100
	}
	return c.Time100
}

// TimeA returns initial INVITE request retransmit interval for unreliable transport.
// It is equal to [TimingConfig.T1].
func (c TimingConfig) TimeA() time.Duration { return c.t1() }

// TimeB returns INVITE client transaction timeout.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeB() time.Duration { return 64 * c.t1() }

// TimeC returns the INVITE transaction timeout on proxy.
// It is equal to 600*[TimingConfig.T1].
func (c TimingConfig) TimeC() time.Duration { return 600 * c.t1() }

// TimeE returns initial non-INVITE request retransmit interval for unreliable transport.
// It is equal to [TimingConfig.T1].
func (c TimingConfig) TimeE() time.Duration { return c.t1() }

// TimeF returns non-INVITE client transaction timeout.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeF() time.Duration { return 64 * c.t1() }

// TimeG returns initial INVITE response retransmit interval for any transport.
// It is equal to [TimingConfig.T1].
func (c TimingConfig) TimeG() time.Duration { return c.t1() }

// TimeH returns timeout for ACK request receipt.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeH() time.Duration { return 64 * c.t1() }

// TimeI returns wait duration for ACK request retransmits via unreliable transport.
// It is equal to [TimingConfig.T4].
func (c TimingConfig) TimeI() time.Duration { return c.t4() }

// TimeJ returns wait duration for non-INVITE request retransmits via unreliable transport.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeJ() time.Duration { return 64 * c.t1() }

// TimeK returns wait duration for response retransmits via unreliable transport.
// It is equal to [TimingConfig.T4].
func (c TimingConfig) TimeK() time.Duration { return c.t4() }

// TimeL returns the wait duration for accepted INVITE request retransmits.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeL() time.Duration { return 64 * c.t1() }

// TimeM returns the wait duration for retransmission of 2xx to INVITE or
// additional 2xx from other branches of a forked INVITE.
// It is equal to 64*[TimingConfig.T1].
func (c TimingConfig) TimeM() time.Duration { return 64 * c.t1() }
